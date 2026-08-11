// SystemServer implements aff.v1.SystemService (PLAN.md §11, §12.5, §13):
// Stats, SetGenerationEnabled, GetSettings, UpdateSettings, Version, Backup,
// ListAuditEvents, Vacuum.
//
// `settings` (migrations/0002_feeds_items.sql) is a flat `key TEXT PRIMARY
// KEY, value TEXT` table with no schema of its own — this file owns the
// convention for it: one row per Settings section ("provider", "generation",
// "publishing"), each value the JSON encoding of the corresponding proto
// message. Settings reads/writes happen directly against store.Store's
// exported Writer()/Reader() handles, same as run.go, rather than through a
// store-layer method, because there is no natural store-side type for a
// section of a singleton key/value row. ListAuditEvents and Vacuum are
// different: they operate on real tables (`auth_events`, the database file
// itself) that already have — or now have — store-layer methods
// (internal/store/system.go), so this file calls into those rather than
// duplicating raw SQL. Helpers are prefixed `sys` per the sibling-file
// convention.
package rpc

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/llm"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// sysProviderAPIKeyEnv is the environment variable the provider key lives
// in. It must name the same variable internal/config.Load reads into
// Config.ProviderAPIKey ("SCHEMAFLUX_API_KEY") — GetSettings reports only
// presence, computed live from the environment, never a stored or cached
// copy of the key itself (PLAN.md §12.5, RULE-2: "never displayed, never
// sent to the client, never editable here — it lives in the environment").
const sysProviderAPIKeyEnv = "SCHEMAFLUX_API_KEY"

// sysPublicBaseURLEnv is the required boot-time public base URL (PLAN.md
// §16). It seeds settings.publishing.public_base_url when nothing has been
// saved — see sysLoadPublishing.
const sysPublicBaseURLEnv = "AFF_PUBLIC_BASE_URL"

// Settings-table keys. One row per Settings section (see package doc);
// sub-sectioning means UpdateSettings can replace one section without
// touching the others, matching the proto's per-section nesting
// (proto/aff/v1/system.proto: "Nested per-section so the editor can save one
// section without resending the others").
const (
	sysSettingsKeyProvider   = "provider"
	sysSettingsKeyGeneration = "generation"
	sysSettingsKeyPublishing = "publishing"
)

// SystemServer implements affv1.SystemServiceServer.
type SystemServer struct {
	affv1.UnimplementedSystemServiceServer

	st  *store.Store
	log *slog.Logger

	getenv func(string) string

	version   string
	build     string
	startedAt time.Time

	// defaultGenerationEnabled seeds settings.generation.enabled the first
	// time GetSettings/Stats is asked and no row exists yet, so a fresh
	// database's runtime kill switch starts in agreement with the cold-start
	// env var (PLAN.md §13: "SetGenerationEnabled, plus
	// AFF_GENERATION_ENABLED=0 for a cold start") rather than silently
	// defaulting to a different value than boot just used.
	defaultGenerationEnabled bool

	// applyPrices publishes a saved price table to whatever actually charges
	// for calls. Without it, /settings/provider's rates were written to the
	// settings row and read back by nothing: real per-run cost and §13's
	// budget ceilings are computed from internal/budget.Table, a separate
	// object this RPC never touched, so editing a rate changed a number on a
	// screen and nothing about money.
	//
	// nil is allowed and means "nobody is listening" — the tests, and any
	// deployment that has not wired a table.
	applyPrices func([]*affv1.PriceEntry)

	// applyPublishing publishes saved publishing settings to the publish
	// plane. Same reason as applyPrices: the public base URL was stored,
	// displayed, and read by nothing, while every generated feed URL came
	// from the env var the process booted with.
	applyPublishing func(*affv1.Settings_Publishing)

	// modelLister is the seam ListModels (models.go) resolves the provider
	// through. nil means "build a real OpenAI lister from the configured
	// key at call time", which is what production does; a test supplies a
	// stub so the default `go test ./...` never reaches a paid API
	// (TODOS.md RULE-1).
	modelLister llm.ModelLister

	// modelsCache holds the last successful provider model list. Guarded by
	// modelsMu because SystemServer is shared across concurrent RPCs.
	// See models.go's cachedModels/staleModels for the two ways it is read.
	modelsMu    sync.Mutex
	modelsCache *affv1.SystemServiceListModelsResponse
	modelsAt    time.Time
}

// WithModelLister injects the provider model lister. Test-only in practice:
// production leaves it nil so ListModels builds one from the environment.
// WithPriceSink wires the saved price table through to the cost/budget
// engine. See SystemServer.applyPrices.
func WithPriceSink(apply func([]*affv1.PriceEntry)) SystemServerOption {
	return func(s *SystemServer) { s.applyPrices = apply }
}

// WithPublishingSink wires saved publishing settings through to the publish
// plane. See SystemServer.applyPublishing.
func WithPublishingSink(apply func(*affv1.Settings_Publishing)) SystemServerOption {
	return func(s *SystemServer) { s.applyPublishing = apply }
}

func WithModelLister(l llm.ModelLister) SystemServerOption {
	return func(s *SystemServer) { s.modelLister = l }
}

// SystemServerOption configures NewSystemServer.
type SystemServerOption func(*SystemServer)

// WithGetenv overrides how SystemServer reads the environment (tests only;
// production defaults to os.Getenv).
func WithGetenv(f func(string) string) SystemServerOption {
	return func(s *SystemServer) { s.getenv = f }
}

// WithVersionInfo sets the values Version() reports.
func WithVersionInfo(version, build string, startedAt time.Time) SystemServerOption {
	return func(s *SystemServer) {
		s.version = version
		s.build = build
		s.startedAt = startedAt
	}
}

// WithDefaultGenerationEnabled seeds the first-boot value of the kill switch
// (see defaultGenerationEnabled's doc); pass config.Config.GenerationEnabled.
func WithDefaultGenerationEnabled(enabled bool) SystemServerOption {
	return func(s *SystemServer) { s.defaultGenerationEnabled = enabled }
}

// NewSystemServer wires a SystemServer against an open store.
func NewSystemServer(st *store.Store, log *slog.Logger, opts ...SystemServerOption) *SystemServer {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	s := &SystemServer{
		st:                       st,
		log:                      log,
		getenv:                   os.Getenv,
		startedAt:                time.Now(),
		defaultGenerationEnabled: true,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *SystemServer) apiKeyPresent() bool {
	return s.getenv(sysProviderAPIKeyEnv) != ""
}

// sysExecer is satisfied by *sql.DB and *sql.Tx, letting sysUpsertSetting
// run inside or outside a transaction.
type sysExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func sysUpsertSetting(ctx context.Context, ex sysExecer, key, value string) error {
	_, err := ex.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value)
	if err != nil {
		return fmt.Errorf("rpc: writing setting %q: %w", key, err)
	}
	return nil
}

// sysReadSetting returns (value, true, nil) if key exists, ("", false, nil)
// if not, or a non-nil error on a genuine read failure.
func sysReadSetting(ctx context.Context, r store.Reader, key string) (string, bool, error) {
	var value string
	err := r.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	switch {
	case err == nil:
		return value, true, nil
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	default:
		return "", false, fmt.Errorf("rpc: reading setting %q: %w", key, err)
	}
}

// sysLoadProvider reads the Provider section, or a zero-value one if unset,
// and always overwrites ApiKeyPresent with the live environment check —
// never the persisted value, since key presence must never go stale
// relative to the actual environment.
func (s *SystemServer) sysLoadProvider(ctx context.Context, r store.Reader) (*affv1.Settings_Provider, error) {
	p := &affv1.Settings_Provider{}
	raw, ok, err := sysReadSetting(ctx, r, sysSettingsKeyProvider)
	if err != nil {
		return nil, err
	}
	if ok {
		if err := json.Unmarshal([]byte(raw), p); err != nil {
			return nil, fmt.Errorf("rpc: parsing stored provider settings: %w", err)
		}
	}
	p.ApiKeyPresent = s.apiKeyPresent()
	// Effort defaults to the tier internal/llm already used implicitly by
	// setting none at all, so an existing deployment's behaviour does not
	// change the moment this field exists.
	if p.GetEffort() == "" {
		p.Effort = defaultProviderEffort
	}
	// The two model fields default for the same reason Effort does, and they
	// did not, which is why /settings/provider showed "Choose a model…" under
	// both while Effort sat there correctly reading "Smart".
	//
	// Seeding on READ is safe for these two specifically: empty means "never
	// chosen", so there is no persisted value to overwrite. That is NOT true
	// in general — see sysLoadGeneration's warning about Enabled, where a
	// stored `false` serialises as an absent key and seeding a default would
	// silently re-enable a kill switch someone turned off.
	if p.GetDefaultModel() == "" {
		p.DefaultModel = DefaultProviderModel
	}
	if p.GetEmbeddingModel() == "" {
		p.EmbeddingModel = DefaultProviderEmbeddingModel
	}
	// KeyPresent is derived on every read and never stored: a profile row
	// records which ENVIRONMENT VARIABLE holds its key (PLAN.md §4 keeps key
	// material out of the database entirely), so whether that variable is
	// actually set is a fact about this process, not about the row. Storing
	// it would go stale the moment the env changed.
	for _, prof := range p.GetProfiles() {
		prof.KeyPresent = prof.GetApiKeyEnv() != "" && s.getenv(prof.GetApiKeyEnv()) != ""
	}
	return p, nil
}

// defaultProviderEffort is the SchemaFlux Speed tier used when none is
// configured.
//
// "quick" — the operator asked for low effort (2026-08-11), and of the three
// tiers SchemaFlux exposes (smart/fast/quick, labelled "most thorough" /
// "balanced" / "cheapest") quick is the low one. There is no "low" tier to
// set literally; validProviderEfforts would reject it.
//
// This was "smart", chosen to match what internal/llm did before the setting
// existed, so the change is not cosmetic: any deployment that never picked an
// effort moves from the most thorough tier to the cheapest one on next boot.
// That is the point — cheaper and faster per run — but output quality is the
// thing being traded away, and a deployment happy with what it was getting
// should set the tier explicitly rather than rely on this.
const defaultProviderEffort = "quick"

// Cold-start model choices, shown on /settings/provider until an operator
// picks otherwise.
const (
	// DefaultProviderModel is the chat model a fresh install starts on, set
	// to the operator's stated choice (2026-08-11).
	//
	// The string must match the provider's model id EXACTLY. The settings
	// dropdown only renders options that SystemService.ListModels returned,
	// so an id the provider does not publish under this spelling shows as
	// "Choose a model…" again — the same empty box this constant exists to
	// fix, with a value behind it.
	//
	// Nothing generates from this value yet. Each feed carries its own model
	// in its recipe (feedspec.ModelParams.Model), and generateSpecFrom reads
	// THAT, so changing this does not change what runs today. It is the
	// value a new feed should inherit, and wiring that is a separate change.
	//
	// It also has no row in the price table, so a feed pointed at it prices
	// at zero and every USD ceiling goes inert for that feed — see
	// genGate.Allowed's warning in cmd/animefeedflux/wire.go. Entering a
	// price for whatever model you actually run is not optional if the
	// dollar ceiling is meant to hold.
	DefaultProviderModel = "gpt-55.6-luna"

	// DefaultProviderEmbeddingModel is not a preference — it is the model
	// the process genuinely uses. cmd/animefeedflux/wire.go pins
	// `embeddingModel = openai.SmallEmbedding3` and `embeddingDim = 1536` at
	// compile time and never reads this setting, so any other value shown
	// here would be a straightforward lie about what produced the vectors in
	// item_embeddings. Kept as the literal rather than importing the openai
	// package into this one for a single string; if wire.go's constant ever
	// changes, this must change with it.
	DefaultProviderEmbeddingModel = "text-embedding-3-small"
)

// validProviderEfforts are the only accepted values — they are SchemaFlux's
// Speed tiers verbatim (PLAN.md §8.1), not an invented scale this codebase
// would then have to translate at the call site.
var validProviderEfforts = map[string]bool{"smart": true, "fast": true, "quick": true}

// Cold-start ceilings for a fresh install. See sysLoadGeneration.
//
// Exported because cmd/animefeedflux has its OWN reader for the same row
// (loadGenerationSettings) with its own no-row fallback: two fallbacks for
// one setting is how the UI and the enforcement end up disagreeing about
// what the ceiling is. One definition, two readers.
const (
	DefaultGlobalDailyTokenCeiling    = 2_000_000
	DefaultGlobalDailySpendCeilingUSD = 5.0

	// Per-feed starting values. These are what a feed created from
	// /generate begins with, and they must be non-zero for a different
	// reason than the ceilings: internal/feedspec REJECTS a spec whose daily
	// token or run budget is zero, so a zero default made every new feed
	// unsaveable until the operator found two fields they had no reason to
	// think were required.
	DefaultFeedDailyTokenBudget = 100_000
	DefaultFeedDailyRunBudget   = 24
	DefaultFeedWindowItems      = 50
)

func (s *SystemServer) sysLoadGeneration(ctx context.Context, r store.Reader) (*affv1.Settings_Generation, error) {
	raw, ok, err := sysReadSetting(ctx, r, sysSettingsKeyGeneration)
	if err != nil {
		return nil, err
	}
	if !ok {
		// No row yet: seed the cold-start defaults (§13). Once a row exists,
		// its persisted value is authoritative — see the warning below.
		//
		// The ceilings are seeded with REAL numbers, not left at zero.
		// internal/budget.CheckRequest gates every ceiling on `> 0`, so a
		// zero means "no limit" — and a fresh install therefore had §13's
		// global spend backstop switched off, which is the opposite of what
		// a backstop is for. The values below are deliberately generous
		// (this app's own seed recipes cost fractions of a cent per run):
		// they exist to stop a runaway loop, not to ration normal use, and
		// an operator who wants no ceiling can still set 0 explicitly.
		return &affv1.Settings_Generation{
			Enabled:                    s.defaultGenerationEnabled,
			GlobalDailyTokenCeiling:    DefaultGlobalDailyTokenCeiling,
			GlobalDailySpendCeilingUsd: DefaultGlobalDailySpendCeilingUSD,
			DefaultDailyTokenBudget:    DefaultFeedDailyTokenBudget,
			DefaultDailyRunBudget:      DefaultFeedDailyRunBudget,
			DefaultFeedWindow:          DefaultFeedWindowItems,
		}, nil
	}
	g := &affv1.Settings_Generation{}
	// Unmarshal into a ZERO-VALUE struct, never one pre-seeded with a
	// default. Settings_Generation.enabled has a `json:",omitempty"` tag
	// (proto3-generated), so a persisted `false` is written as an absent
	// key, not `"enabled":false` — unmarshaling that JSON onto a
	// pre-seeded {Enabled: true} would silently leave the default in
	// place and a disabled kill switch would read back as enabled.
	if err := json.Unmarshal([]byte(raw), g); err != nil {
		return nil, fmt.Errorf("rpc: parsing stored generation settings: %w", err)
	}
	return g, nil
}

func (s *SystemServer) sysLoadPublishing(ctx context.Context, r store.Reader) (*affv1.Settings_Publishing, error) {
	p := &affv1.Settings_Publishing{}
	raw, ok, err := sysReadSetting(ctx, r, sysSettingsKeyPublishing)
	if err != nil {
		return nil, err
	}
	if ok {
		if err := json.Unmarshal([]byte(raw), p); err != nil {
			return nil, fmt.Errorf("rpc: parsing stored publishing settings: %w", err)
		}
	}
	// Seed the base URL from the environment when the settings row has none.
	//
	// AFF_PUBLIC_BASE_URL is REQUIRED at boot (PLAN.md §16) and is what the
	// publish plane actually bakes into every guid and atom:link — so on a
	// fresh install the server knows its own public base URL while this
	// message reported an empty one, and anything reading it had to treat a
	// correctly-configured deployment as unconfigured. /generate's subscribe-
	// URL panel showed "set a public base URL" on exactly such a deployment.
	//
	// Same shape as defaultGenerationEnabled above: the environment is the
	// cold-start value, a saved setting overrides it, and nothing is written
	// back here — a read must not persist.
	if p.GetPublicBaseUrl() == "" {
		p.PublicBaseUrl = s.getenv(sysPublicBaseURLEnv)
	}
	return p, nil
}

// sysLoadSettings assembles the full Settings from all three sections.
func (s *SystemServer) sysLoadSettings(ctx context.Context, r store.Reader) (*affv1.Settings, error) {
	provider, err := s.sysLoadProvider(ctx, r)
	if err != nil {
		return nil, err
	}
	generation, err := s.sysLoadGeneration(ctx, r)
	if err != nil {
		return nil, err
	}
	publishing, err := s.sysLoadPublishing(ctx, r)
	if err != nil {
		return nil, err
	}
	return &affv1.Settings{Provider: provider, Generation: generation, Publishing: publishing}, nil
}

// sysValidateBaseURL enforces §12.5's "validated (absolute URL, correct
// scheme) on save" for the publishing base URL, because it is baked into
// every guid (§12.4/§5.1) — an invalid value here is wrong in every channel
// element at once, so it must be caught before it is ever stored.
func sysValidateBaseURL(raw string) error {
	if raw == "" {
		return errors.New("public_base_url is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("public_base_url: %w", err)
	}
	if !u.IsAbs() || u.Host == "" {
		return errors.New("public_base_url must be an absolute URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("public_base_url: scheme %q must be http or https", u.Scheme)
	}
	return nil
}

// GetSettings returns the persisted settings, with the provider API key's
// PRESENCE only — never the key (§12.5, RULE-2).
func (s *SystemServer) GetSettings(ctx context.Context, _ *affv1.SystemServiceGetSettingsRequest) (*affv1.SystemServiceGetSettingsResponse, error) {
	settings, err := s.sysLoadSettings(ctx, s.st.Reader())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: loading settings: %v", err)
	}
	return &affv1.SystemServiceGetSettingsResponse{Settings: settings}, nil
}

// UpdateSettings replaces only the sections present in the request (each of
// Provider/Generation/Publishing is a proto3 message field, so nil is
// distinguishable from "present but zero" — a client sends only the
// section(s) it changed). No expected_version: `settings` is a singleton
// key/value table with no version column, and §11's optimistic-concurrency
// convention exists to stop two tabs clobbering a *recipe*, which this is
// not (PLAN.md §11, proto doc on SystemService).
//
// The provider API key is never settable here (§12.5, RULE-2): whatever the
// client sends for api_key_present is discarded before storage and always
// recomputed live on the next read.
func (s *SystemServer) UpdateSettings(ctx context.Context, req *affv1.SystemServiceUpdateSettingsRequest) (*affv1.SystemServiceUpdateSettingsResponse, error) {
	in := req.GetSettings()
	if in == nil {
		return nil, status.Error(codes.InvalidArgument, "rpc: settings is required")
	}

	// Validate BEFORE writing anything (§12.5): a rejected base URL must
	// leave every other section, including ones in this same request,
	// untouched rather than half-applied.
	if in.GetPublishing() != nil {
		if err := sysValidateBaseURL(in.GetPublishing().GetPublicBaseUrl()); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "rpc: %v", err)
		}
	}

	tx, err := s.st.Writer().BeginTx(ctx, nil)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: begin update settings: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	if p := in.GetProvider(); p != nil {
		// ApiKeyPresent is never persisted: it is always derived live from
		// the environment (sysLoadProvider), so whatever the client sent is
		// discarded here rather than round-tripped into storage.
		effort := p.GetEffort()
		if effort == "" {
			effort = defaultProviderEffort
		}
		if !validProviderEfforts[effort] {
			// Rejected rather than silently corrected: an unknown tier means
			// the caller and this server disagree about what the field is,
			// and quietly substituting a default would hide that until
			// someone wondered why generation was not behaving as configured.
			return nil, status.Errorf(codes.InvalidArgument,
				"provider.effort %q is not one of smart, fast, quick", p.GetEffort())
		}
		profiles, perr := sysValidateProfiles(p.GetProfiles(), p.GetActiveProfile())
		if perr != nil {
			return nil, status.Error(codes.InvalidArgument, perr.Error())
		}
		toStore := &affv1.Settings_Provider{
			ActiveProvider: p.GetActiveProvider(),
			DefaultModel:   p.GetDefaultModel(),
			EmbeddingModel: p.GetEmbeddingModel(),
			PriceTable:     p.GetPriceTable(),
			Effort:         effort,
			ActiveProfile:  p.GetActiveProfile(),
			Profiles:       profiles,
		}
		raw, err := json.Marshal(toStore)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "rpc: encoding provider settings: %v", err)
		}
		if err := sysUpsertSetting(ctx, tx, sysSettingsKeyProvider, string(raw)); err != nil {
			return nil, status.Errorf(codes.Internal, "rpc: %v", err)
		}
	}
	if g := in.GetGeneration(); g != nil {
		raw, err := json.Marshal(g)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "rpc: encoding generation settings: %v", err)
		}
		if err := sysUpsertSetting(ctx, tx, sysSettingsKeyGeneration, string(raw)); err != nil {
			return nil, status.Errorf(codes.Internal, "rpc: %v", err)
		}
	}
	if p := in.GetPublishing(); p != nil {
		raw, err := json.Marshal(p)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "rpc: encoding publishing settings: %v", err)
		}
		if err := sysUpsertSetting(ctx, tx, sysSettingsKeyPublishing, string(raw)); err != nil {
			return nil, status.Errorf(codes.Internal, "rpc: %v", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: committing settings update: %v", err)
	}

	settings, err := s.sysLoadSettings(ctx, s.st.Reader())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: reloading settings: %v", err)
	}
	// Publish the rates AFTER the commit, and from the reloaded settings
	// rather than from the request, so what the engine charges with is
	// exactly what was stored — never a value a failed transaction rolled
	// back.
	if s.applyPrices != nil {
		s.applyPrices(settings.GetProvider().GetPriceTable())
	}
	if s.applyPublishing != nil {
		s.applyPublishing(settings.GetPublishing())
	}
	return &affv1.SystemServiceUpdateSettingsResponse{Settings: settings}, nil
}

// SetGenerationEnabled is the global kill switch (§13): existing feeds keep
// serving, nothing generates. It flips only settings.generation.enabled,
// read-modify-write inside one transaction so a concurrent UpdateSettings
// touching the same section cannot interleave and drop this flip.
func (s *SystemServer) SetGenerationEnabled(ctx context.Context, req *affv1.SystemServiceSetGenerationEnabledRequest) (*affv1.SystemServiceSetGenerationEnabledResponse, error) {
	tx, err := s.st.Writer().BeginTx(ctx, nil)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: begin set generation enabled: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	g, err := s.sysLoadGenerationTx(ctx, tx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: %v", err)
	}
	g.Enabled = req.GetEnabled()

	raw, err := json.Marshal(g)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: encoding generation settings: %v", err)
	}
	if err := sysUpsertSetting(ctx, tx, sysSettingsKeyGeneration, string(raw)); err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: %v", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: committing generation enabled: %v", err)
	}
	return &affv1.SystemServiceSetGenerationEnabledResponse{Enabled: g.Enabled}, nil
}

// sysLoadGenerationTx mirrors sysLoadGeneration but reads through a *sql.Tx
// (store.Reader's interface is satisfied by *sql.DB/*sql.Tx alike via
// QueryRowContext, so this is the same query, just against the writer's
// in-flight transaction to make the read-modify-write atomic).
func (s *SystemServer) sysLoadGenerationTx(ctx context.Context, tx *sql.Tx) (*affv1.Settings_Generation, error) {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, sysSettingsKeyGeneration).Scan(&raw)
	switch {
	case err == nil:
		// See sysLoadGeneration's comment: unmarshal onto a zero-value
		// struct, never one pre-seeded with the default, because `enabled`
		// carries `json:",omitempty"` and a persisted `false` would
		// otherwise silently read back as the seeded `true`.
		g := &affv1.Settings_Generation{}
		if err := json.Unmarshal([]byte(raw), g); err != nil {
			return nil, fmt.Errorf("parsing stored generation settings: %w", err)
		}
		return g, nil
	case errors.Is(err, sql.ErrNoRows):
		return &affv1.Settings_Generation{Enabled: s.defaultGenerationEnabled}, nil
	default:
		return nil, fmt.Errorf("reading generation settings: %w", err)
	}
}

// Version reports the running build and process uptime origin.
func (s *SystemServer) Version(context.Context, *affv1.SystemServiceVersionRequest) (*affv1.SystemServiceVersionResponse, error) {
	return &affv1.SystemServiceVersionResponse{
		Version:   s.version,
		Build:     s.build,
		StartedAt: timestamppb.New(s.startedAt),
	}, nil
}

// sysDBSizeBytes sums the main database file with its WAL and shared-memory
// sidecars (§3: journal_mode=WAL), because under WAL the main file alone
// understates on-disk size — recent writes live in `-wal` until the next
// checkpoint.
func sysDBSizeBytes(path string) (int64, error) {
	var total int64
	for _, suffix := range []string{"", "-wal", "-shm"} {
		fi, err := os.Stat(path + suffix)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return 0, err
		}
		total += fi.Size()
	}
	return total, nil
}

// Stats reports the system-wide dashboard numbers (§15): feed/item counts,
// DB size, today's spend, remaining budget and the kill switch state.
//
// TodaySpendUsd (and therefore TodayRemainingBudgetUsd, derived from it) is
// an ESTIMATE, not a measured figure — SchemaFlux reports no usage or cost
// (§8.1), so every run's est_cost_usd this sums is itself an estimate, and
// that label lives on the Run.est_cost_usd field this is aggregated from
// (run.go's runToProto doc); nothing here may present the total as measured.
func (s *SystemServer) Stats(ctx context.Context, _ *affv1.SystemServiceStatsRequest) (*affv1.SystemServiceStatsResponse, error) {
	r := s.st.Reader()

	var feedCount int32
	var enabledFeedCount sql.NullInt64
	if err := r.QueryRowContext(ctx,
		`SELECT COUNT(*), SUM(CASE WHEN enabled = 1 THEN 1 ELSE 0 END)
		 FROM feeds WHERE deleted_at IS NULL`,
	).Scan(&feedCount, &enabledFeedCount); err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: counting feeds: %v", err)
	}

	var itemCount int64
	if err := r.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM items WHERE deleted_at IS NULL`,
	).Scan(&itemCount); err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: counting items: %v", err)
	}

	dbSize, err := sysDBSizeBytes(s.st.Path())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: statting database: %v", err)
	}

	todayStart := time.Now().UTC().Truncate(24 * time.Hour)
	_, _, todaySpend, err := s.st.SpendSince(ctx, 0, todayStart)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: summing today's spend: %v", err)
	}

	generation, err := s.sysLoadGeneration(ctx, r)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: loading generation settings: %v", err)
	}

	var remaining float64
	if generation.GetGlobalDailySpendCeilingUsd() > 0 {
		remaining = generation.GetGlobalDailySpendCeilingUsd() - todaySpend
		if remaining < 0 {
			remaining = 0
		}
	}

	return &affv1.SystemServiceStatsResponse{
		FeedCount:               feedCount,
		EnabledFeedCount:        int32(enabledFeedCount.Int64),
		ItemCount:               itemCount,
		DbSizeBytes:             dbSize,
		TodaySpendUsd:           todaySpend,
		TodayRemainingBudgetUsd: remaining,
		GenerationEnabled:       generation.GetEnabled(),
	}, nil
}

// Backup produces a consistent on-demand snapshot via SQLite's `VACUUM
// INTO`, which — unlike copying the .db file directly — is safe against a
// concurrent WAL writer: it folds the WAL into a single self-contained file
// rather than risking a copy that misses recent commits still sitting in
// `-wal` (§3, §12.5 Data section: "on-demand backup download").
func (s *SystemServer) Backup(ctx context.Context, _ *affv1.SystemServiceBackupRequest) (*affv1.SystemServiceBackupResponse, error) {
	filename := fmt.Sprintf("animefeedflux-%s.db", time.Now().UTC().Format("20060102-150405"))

	dir, err := os.MkdirTemp("", "aff-backup-")
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: preparing backup: %v", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	dest := dir + string(os.PathSeparator) + filename
	if _, err := s.st.Writer().ExecContext(ctx, `VACUUM INTO ?`, dest); err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: vacuuming backup: %v", err)
	}

	data, err := os.ReadFile(dest)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: reading backup file: %v", err)
	}

	return &affv1.SystemServiceBackupResponse{DbFile: data, Filename: filename}, nil
}

// --- ListAuditEvents ------------------------------------------------------

// sysAuditDefaultPageSize and sysAuditMaxPageSize bound ListAuditEvents's
// page_size (§11), mirroring run.go's runDefaultPageSize/runMaxPageSize.
const (
	sysAuditDefaultPageSize = 50
	sysAuditMaxPageSize     = 200
)

// sysAuditEncodeCursor/sysAuditDecodeCursor make ListAuditEvents' page token
// opaque to the client (§11) while staying a plain auth_events.id
// internally — see store.ListAuditEventsPage's doc comment for why id is a
// tie-free "older than" boundary. Mirrors run.go's runEncodeCursor/
// runDecodeCursor exactly; not shared because that pair is unexported in
// the same package under a `run`-specific name, and duplicating four lines
// here keeps this file's cursor concept legible without a cross-file jump.
func sysAuditEncodeCursor(id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(id, 10)))
}

func sysAuditDecodeCursor(token string) (int64, error) {
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(string(b), 10, 64)
}

// sysAuditEventToProto converts a store.AuthEvent to the wire AuditEvent.
// Deliberately narrow: it copies id/at/kind/ip/ok and drops Detail, per
// AuditEvent's doc comment in proto/aff/v1/system.proto (this RPC answers
// "who logged in, from where, when, did it succeed" — nothing more).
func sysAuditEventToProto(e store.AuthEvent) *affv1.AuditEvent {
	return &affv1.AuditEvent{
		Id:   e.ID,
		At:   timestamppb.New(e.At),
		Kind: e.Kind,
		Ip:   e.IP,
		Ok:   e.OK,
	}
}

// ListAuditEvents reads the `auth_events` audit trail newest-first (§10),
// the read side of the ~19 write sites RecordAuthEvent has and — until this
// RPC existed — nothing ever read. See AuditEvent's proto doc comment for
// exactly what is and is not exposed and why.
//
// An empty log (fresh database, nobody has ever logged in through it)
// returns an empty `events` slice and no next_page_token — a normal,
// successful response, never an error; a caller checking "was there a
// login I did not make" must not have to distinguish "nothing happened yet"
// from "the query failed".
func (s *SystemServer) ListAuditEvents(ctx context.Context, req *affv1.SystemServiceListAuditEventsRequest) (*affv1.SystemServiceListAuditEventsResponse, error) {
	pageSize := int(req.GetPageSize())
	if pageSize <= 0 {
		pageSize = sysAuditDefaultPageSize
	}
	if pageSize > sysAuditMaxPageSize {
		pageSize = sysAuditMaxPageSize
	}

	var beforeID int64
	if tok := req.GetPageToken(); tok != "" {
		cursor, err := sysAuditDecodeCursor(tok)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "rpc: invalid page_token")
		}
		beforeID = cursor
	}

	rows, err := s.st.ListAuditEventsPage(ctx, beforeID, pageSize+1) // +1: next-page lookahead
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: listing audit events: %v", err)
	}

	resp := &affv1.SystemServiceListAuditEventsResponse{}
	for i, e := range rows {
		if i == pageSize {
			// Lookahead row (see ListAuditEventsPage's doc comment on the
			// +1): proves a next page exists but is not itself returned.
			// The cursor is the id of the LAST ROW ACTUALLY RETURNED —
			// using this lookahead row's id would make "id < cursor" skip
			// it again on the next page, silently dropping one event per
			// page boundary (same trap run.go's History avoids).
			resp.NextPageToken = sysAuditEncodeCursor(rows[i-1].ID)
			break
		}
		resp.Events = append(resp.Events, sysAuditEventToProto(e))
	}
	return resp, nil
}

// --- Vacuum -----------------------------------------------------------

// Vacuum runs SQLite's VACUUM against the live database (PLAN.md §12.5 Data
// section, TODOS.md D4-10). This BLOCKS the caller for the duration — see
// store.Vacuum's doc comment for the rough size-to-duration budget — and
// takes SQLite's exclusive lock for that whole window, so it refuses
// outright while a generation run is in flight (store.RunInFlight) rather
// than contending with it for the single writer connection
// (store.Store.writer has MaxOpenConns(1)): a run whose heartbeat renewal
// or item commit stalls behind an in-progress VACUUM can look crashed to
// ReclaimStaleRuns even though nothing actually failed. The response
// reports size before and after, because "did that accomplish anything" is
// the only reason to ever call this (§12.5).
func (s *SystemServer) Vacuum(ctx context.Context, _ *affv1.SystemServiceVacuumRequest) (*affv1.SystemServiceVacuumResponse, error) {
	inFlight, err := s.st.RunInFlight(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: checking for an in-flight run: %v", err)
	}
	if inFlight {
		return nil, status.Error(codes.FailedPrecondition, "rpc: a generation run is in progress; vacuum refuses to contend for the write lock, try again once it finishes")
	}

	stats, err := s.st.Vacuum(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: vacuuming database: %v", err)
	}

	return &affv1.SystemServiceVacuumResponse{
		SizeBeforeBytes: stats.SizeBeforeBytes,
		SizeAfterBytes:  stats.SizeAfterBytes,
		DurationMs:      stats.Duration.Milliseconds(),
	}, nil
}

// sysValidateProfiles checks operator-configured provider profiles and
// returns the sanitised list to persist.
//
// It strips KeyPresent on the way in for the same reason ApiKeyPresent is
// stripped: it is derived from this process's environment on every read
// (sysLoadProvider), so a stored copy is a value that goes stale silently.
//
// The base URL is validated the same way §12.5 already requires of the
// publishing base URL — absolute, http or https — because a malformed one
// here does not fail loudly at save time, it fails at 4am on the next
// scheduled run, as a provider error nobody can explain.
func sysValidateProfiles(in []*affv1.ProviderProfile, active string) ([]*affv1.ProviderProfile, error) {
	out := make([]*affv1.ProviderProfile, 0, len(in))
	seen := map[string]bool{}
	for _, p := range in {
		name := strings.TrimSpace(p.GetName())
		if name == "" {
			return nil, errors.New("every provider profile needs a name")
		}
		if seen[name] {
			return nil, fmt.Errorf("two provider profiles are both named %q", name)
		}
		seen[name] = true

		base := strings.TrimSpace(p.GetBaseUrl())
		if base != "" {
			u, err := url.Parse(base)
			if err != nil {
				return nil, fmt.Errorf("provider profile %q: base URL: %w", name, err)
			}
			if !u.IsAbs() || u.Host == "" {
				return nil, fmt.Errorf("provider profile %q: base URL must be absolute", name)
			}
			if u.Scheme != "http" && u.Scheme != "https" {
				return nil, fmt.Errorf("provider profile %q: base URL scheme %q must be http or https", name, u.Scheme)
			}
		}

		out = append(out, &affv1.ProviderProfile{
			Name:      name,
			BaseUrl:   base,
			ApiKeyEnv: strings.TrimSpace(p.GetApiKeyEnv()),
			// KeyPresent deliberately not carried through — derived on read.
		})
	}
	if active != "" && !seen[active] {
		return nil, fmt.Errorf("active provider profile %q is not in the list", active)
	}
	return out, nil
}
