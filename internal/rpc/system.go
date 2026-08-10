// SystemServer implements aff.v1.SystemService (PLAN.md §11, §12.5, §13):
// Stats, SetGenerationEnabled, GetSettings, UpdateSettings, Version, Backup.
//
// `settings` (migrations/0002_feeds_items.sql) is a flat `key TEXT PRIMARY
// KEY, value TEXT` table with no schema of its own — this file owns the
// convention for it: one row per Settings section ("provider", "generation",
// "publishing"), each value the JSON encoding of the corresponding proto
// message. internal/store has no settings.go (only auth/hooks/items/runs/
// samples), and this package may not add one, so reads and writes happen
// directly against store.Store's exported Writer()/Reader() handles, same as
// run.go. Helpers are prefixed `sys` per the sibling-file convention.
package rpc

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// sysProviderAPIKeyEnv is the environment variable the provider key lives
// in. It must name the same variable internal/config.Load reads into
// Config.ProviderAPIKey ("SCHEMAFLUX_API_KEY") — GetSettings reports only
// presence, computed live from the environment, never a stored or cached
// copy of the key itself (PLAN.md §12.5, RULE-2: "never displayed, never
// sent to the client, never editable here — it lives in the environment").
const sysProviderAPIKeyEnv = "SCHEMAFLUX_API_KEY"

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
	return p, nil
}

func (s *SystemServer) sysLoadGeneration(ctx context.Context, r store.Reader) (*affv1.Settings_Generation, error) {
	raw, ok, err := sysReadSetting(ctx, r, sysSettingsKeyGeneration)
	if err != nil {
		return nil, err
	}
	if !ok {
		// No row yet: seed the cold-start default (§13). Once a row exists,
		// its persisted value is authoritative — see the warning below.
		return &affv1.Settings_Generation{Enabled: s.defaultGenerationEnabled}, nil
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

func sysLoadPublishing(ctx context.Context, r store.Reader) (*affv1.Settings_Publishing, error) {
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
	publishing, err := sysLoadPublishing(ctx, r)
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
		toStore := &affv1.Settings_Provider{
			ActiveProvider: p.GetActiveProvider(),
			DefaultModel:   p.GetDefaultModel(),
			EmbeddingModel: p.GetEmbeddingModel(),
			PriceTable:     p.GetPriceTable(),
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
