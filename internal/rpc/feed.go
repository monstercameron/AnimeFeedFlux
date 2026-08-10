// Package rpc holds the gRPC service implementations behind the admin UI and
// the CLI (PLAN.md §11) — the only way any of it writes to the database.
//
// This file implements FeedServiceServer: the editor pane's backend (§12.3).
// Two invariants apply to every write here that changes a feed's published
// representation — Update, SetEnabled, Delete, SetMembers, plus the create
// paths (§11, "Two invariants" paragraph):
//
//  1. Invalidate that feed's render cache (and any aggregate containing it)
//     AFTER the write commits, never from inside the transaction — a missed
//     invalidation self-heals on the next write or restart, but invalidating
//     a write that later rolls back would drop good cached content for a
//     change that never happened (internal/store/hooks.go's WriteHook doc
//     makes the identical argument for item writes).
//  2. Carry expected_version and refuse a mismatch with a status a caller can
//     distinguish from every other failure, so two browser tabs editing the
//     same feed cannot silently clobber one another.
//
// *store.Store's own feed methods (internal/store/items.go) cover only
// CreateFeed and GetFeedBySlug — Update/SetEnabled/SetMembers/List/Delete do
// not exist at the store layer yet (that file is off-limits to this change),
// so this file talks to the feeds and feed_members tables directly through
// Store.Writer(), rather than through internal/store/hooks.go's Hooked
// wrapper. Invalidation is therefore performed explicitly here, via an
// injected publish.Invalidator, instead of via Hooked's WriteHook.
package rpc

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/feedspec"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/publish"
	"github.com/monstercameron/AnimeFeedFlux/internal/schedule"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// feedJitterWindow is the default cron-jitter spread (§14.3: "a configurable
// window (default 10 minutes)"). Not yet exposed as a setting anywhere this
// package can reach, so the plan's stated default is used directly.
const feedJitterWindow = 10 * time.Minute

// FeedRunExecutor starts generation for a feed's already-created run.
//
// RunNow's contract (see FeedServiceRunNowResponse's doc in the generated
// code) is that run_id is "populated once the run is created (before it
// necessarily finishes)" — so ExecuteRun must not block the RPC caller; an
// implementation that wants to generate synchronously has to background it
// itself. Defined as an interface — rather than this package importing
// internal/scheduler directly — so package rpc has no dependency on the
// scheduler, per this change's constraints.
type FeedRunExecutor interface {
	ExecuteRun(feedID, runID int64)
}

// FeedServer implements affv1.FeedServiceServer.
type FeedServer struct {
	affv1.UnimplementedFeedServiceServer

	store *store.Store
	inv   publish.Invalidator
	exec  FeedRunExecutor
}

// NewFeedServer wires the FeedService RPC surface to its dependencies. exec
// may be nil in contexts that never call RunNow (e.g. some test setups);
// inv may be nil the same way, in which case cache invalidation is skipped
// rather than panicking — a cache is not a source of truth (§9), so a nil
// invalidator degrades to "never invalidate" rather than a crash.
func NewFeedServer(st *store.Store, inv publish.Invalidator, exec FeedRunExecutor) *FeedServer {
	return &FeedServer{store: st, inv: inv, exec: exec}
}

// --- feed row I/O -----------------------------------------------------

// feedRecord is the full feeds row, unlike internal/store/items.go's
// scanFeed (which backs a narrower model.Feed and omits spec_json,
// jitter_offset, consecutive_failures, deleted_at and created_at — all of
// which this service needs).
type feedRecord struct {
	ID                  int64
	Slug                string
	Title               string
	Description         string
	Language            string
	Kind                model.FeedKind
	SpecJSON            string
	Enabled             bool
	Timezone            string
	JitterOffset        int
	LastBuiltAt         time.Time
	ConsecutiveFailures int
	Author              string
	Copyright           string
	OGImage             string
	TTLMinutes          int
	Version             int64
	DeletedAt           time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

const feedFullColumns = `id, slug, title, description, language, kind, spec_json, enabled, timezone,
	jitter_offset, last_built_at, consecutive_failures, author, copyright, og_image, ttl_minutes,
	version, deleted_at, created_at, updated_at`

// feedRowScanner is satisfied by both *sql.Row and *sql.Rows.
type feedRowScanner interface {
	Scan(dest ...any) error
}

func feedScanRecord(sc feedRowScanner) (feedRecord, error) {
	var (
		rec                        feedRecord
		kind                       string
		enabled                    int64
		jitterOffset               int64
		lastBuiltAt                sql.NullString
		consecutiveFailures        int64
		author, copyright, ogImage sql.NullString
		ttlMinutes                 int64
		deletedAt                  sql.NullString
		createdAt, updatedAt       string
	)
	if err := sc.Scan(
		&rec.ID, &rec.Slug, &rec.Title, &rec.Description, &rec.Language, &kind, &rec.SpecJSON,
		&enabled, &rec.Timezone, &jitterOffset, &lastBuiltAt, &consecutiveFailures,
		&author, &copyright, &ogImage, &ttlMinutes, &rec.Version, &deletedAt, &createdAt, &updatedAt,
	); err != nil {
		return feedRecord{}, err
	}
	rec.Kind = model.FeedKind(kind)
	rec.Enabled = enabled != 0
	rec.JitterOffset = int(jitterOffset)
	rec.ConsecutiveFailures = int(consecutiveFailures)
	rec.Author = author.String
	rec.Copyright = copyright.String
	rec.OGImage = ogImage.String
	rec.TTLMinutes = int(ttlMinutes)

	var err error
	if rec.LastBuiltAt, err = feedParseNullTime(lastBuiltAt); err != nil {
		return feedRecord{}, fmt.Errorf("rpc: parsing feeds.last_built_at: %w", err)
	}
	if rec.DeletedAt, err = feedParseNullTime(deletedAt); err != nil {
		return feedRecord{}, fmt.Errorf("rpc: parsing feeds.deleted_at: %w", err)
	}
	if rec.CreatedAt, err = feedParseTime(createdAt); err != nil {
		return feedRecord{}, fmt.Errorf("rpc: parsing feeds.created_at: %w", err)
	}
	if rec.UpdatedAt, err = feedParseTime(updatedAt); err != nil {
		return feedRecord{}, fmt.Errorf("rpc: parsing feeds.updated_at: %w", err)
	}
	return rec, nil
}

// feedTimeLayout matches internal/store's own convention (RFC3339Nano, UTC)
// so timestamps written by this package and by internal/store round-trip
// identically.
const feedTimeLayout = time.RFC3339Nano

func feedFormatTime(t time.Time) string { return t.UTC().Format(feedTimeLayout) }

func feedParseTime(s string) (time.Time, error) { return time.Parse(feedTimeLayout, s) }

func feedParseNullTime(ns sql.NullString) (time.Time, error) {
	if !ns.Valid || ns.String == "" {
		return time.Time{}, nil
	}
	return feedParseTime(ns.String)
}

func feedBoolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// feedNullString maps "" to NULL, matching internal/store/items.go's
// nullString convention for optional text columns.
func feedNullString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func (s *FeedServer) feedRequireFeed(ctx context.Context, id int64) (feedRecord, error) {
	row := s.store.Writer().QueryRowContext(ctx,
		`SELECT `+feedFullColumns+` FROM feeds WHERE id = ? AND deleted_at IS NULL`, id)
	rec, err := feedScanRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return feedRecord{}, status.Errorf(codes.NotFound, "feed %d not found", id)
	}
	if err != nil {
		return feedRecord{}, status.Errorf(codes.Internal, "loading feed %d: %v", id, err)
	}
	return rec, nil
}

func (s *FeedServer) feedRequireFeedBySlug(ctx context.Context, slug string) (feedRecord, error) {
	row := s.store.Writer().QueryRowContext(ctx,
		`SELECT `+feedFullColumns+` FROM feeds WHERE slug = ? AND deleted_at IS NULL`, slug)
	rec, err := feedScanRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return feedRecord{}, status.Errorf(codes.NotFound, "feed %q not found", slug)
	}
	if err != nil {
		return feedRecord{}, status.Errorf(codes.Internal, "loading feed %q: %v", slug, err)
	}
	return rec, nil
}

func (s *FeedServer) feedListMembers(ctx context.Context, aggregateID int64) ([]string, error) {
	rows, err := s.store.Writer().QueryContext(ctx,
		`SELECT f2.slug FROM feed_members fm JOIN feeds f2 ON f2.id = fm.member_feed_id
		 WHERE fm.aggregate_feed_id = ? ORDER BY fm.position`, aggregateID)
	if err != nil {
		return nil, fmt.Errorf("rpc: listing members for feed %d: %w", aggregateID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, fmt.Errorf("rpc: scanning member slug for feed %d: %w", aggregateID, err)
		}
		out = append(out, slug)
	}
	return out, rows.Err()
}

func feedVersionConflict(id, expected, actual int64) error {
	return status.Errorf(codes.FailedPrecondition,
		"feed %d: version conflict (expected %d, have %d) — reload and retry", id, expected, actual)
}

// --- cache invalidation --------------------------------------------------

// feedInvalidate invalidates slug's cached representation, plus every
// aggregate that lists feedID as a member (§11: "Any aggregate containing the
// feed is invalidated too"). Called AFTER the write's transaction has
// committed — see the package doc comment for why order matters here.
func (s *FeedServer) feedInvalidate(ctx context.Context, feedID int64, slug string) {
	if s.inv == nil {
		return
	}
	s.inv.InvalidateFeed(slug)

	rows, err := s.store.Writer().QueryContext(ctx,
		`SELECT f.slug FROM feed_members fm JOIN feeds f ON f.id = fm.aggregate_feed_id
		 WHERE fm.member_feed_id = ?`, feedID)
	if err != nil {
		// Best-effort fan-out: a missed invalidation self-heals on the next
		// write or restart (internal/store/hooks.go's WriteHook doc makes the
		// identical argument), so this is not escalated to an RPC error.
		return
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var aggSlug string
		if rows.Scan(&aggSlug) == nil {
			s.inv.InvalidateFeed(aggSlug)
		}
	}
}

// --- proto <-> feedspec/model conversions ---------------------------------

func feedKindFromProto(k affv1.FeedKind) model.FeedKind {
	switch k {
	case affv1.FeedKind_FEED_KIND_GENERATIVE:
		return model.KindGenerative
	case affv1.FeedKind_FEED_KIND_GROUNDED:
		return model.KindGrounded
	case affv1.FeedKind_FEED_KIND_AGGREGATE:
		return model.KindAggregate
	default:
		return ""
	}
}

func feedKindToProto(k model.FeedKind) affv1.FeedKind {
	switch k {
	case model.KindGenerative:
		return affv1.FeedKind_FEED_KIND_GENERATIVE
	case model.KindGrounded:
		return affv1.FeedKind_FEED_KIND_GROUNDED
	case model.KindAggregate:
		return affv1.FeedKind_FEED_KIND_AGGREGATE
	default:
		return affv1.FeedKind_FEED_KIND_UNSPECIFIED
	}
}

func feedSourcesFromProto(in []*affv1.SourceSpec) []feedspec.Source {
	if len(in) == 0 {
		return nil
	}
	out := make([]feedspec.Source, len(in))
	for i, src := range in {
		out[i] = feedspec.Source{URL: src.GetUrl(), Kind: src.GetKind()}
	}
	return out
}

func feedSourcesToProto(in []feedspec.Source) []*affv1.SourceSpec {
	if len(in) == 0 {
		return nil
	}
	out := make([]*affv1.SourceSpec, len(in))
	for i, src := range in {
		out[i] = &affv1.SourceSpec{Url: src.URL, Kind: src.Kind}
	}
	return out
}

// feedSpecFromProto converts the recipe portion of a wire FeedSpec. Proto
// getters are nil-receiver-safe, so ps may be nil (aggregate feeds carry no
// spec, §14.2) without a guard here.
func feedSpecFromProto(ps *affv1.FeedSpec) feedspec.Spec {
	return feedspec.Spec{
		Cron:        ps.GetCron(),
		Timezone:    ps.GetTimezone(),
		ItemsPerRun: int(ps.GetItemsPerRun()),
		FeedWindow:  int(ps.GetFeedWindow()),
		Model: feedspec.ModelParams{
			Model:       ps.GetModel(),
			Temperature: ps.GetTemperature(),
			// SchemaFlux's actual per-call tuning knobs (Mode/Speed, §8.1)
			// have no wire representation on FeedSpec yet; "balanced" is
			// feedspec.Defaults()'s own choice, reused here rather than left
			// blank so an imported/created spec behaves the same as a
			// freshly-defaulted one.
			Mode: "balanced",
		},
		SystemPrompt: ps.GetSystemPromptTemplate(),
		UserPrompt:   ps.GetUserPromptTemplate(),
		Novelty: feedspec.Novelty{
			ExcludeLast: int(ps.GetNovelty().GetNoveltyWindowItems()),
			Threshold:   ps.GetNovelty().GetSimilarityThreshold(),
			// MaxRetries has no wire field either; fall back to the engineering
			// default rather than 0, which would disable the retry loop.
			MaxRetries: feedspec.Defaults().Novelty.MaxRetries,
		},
		Budgets: feedspec.Budgets{
			DailyTokens: int(ps.GetDailyTokenBudget()),
			DailyRuns:   int(ps.GetDailyRunBudget()),
		},
		Sources: feedSourcesFromProto(ps.GetSources()),
	}
}

func feedSpecToProto(spec feedspec.Spec) *affv1.FeedSpec {
	return &affv1.FeedSpec{
		Cron:                 spec.Cron,
		Timezone:             spec.Timezone,
		ItemsPerRun:          int32(spec.ItemsPerRun),
		FeedWindow:           int32(spec.FeedWindow),
		Model:                spec.Model.Model,
		Temperature:          spec.Model.Temperature,
		SystemPromptTemplate: spec.SystemPrompt,
		UserPromptTemplate:   spec.UserPrompt,
		Novelty: &affv1.NoveltySettings{
			NoveltyWindowItems:  int32(spec.Novelty.ExcludeLast),
			SimilarityThreshold: spec.Novelty.Threshold,
		},
		DailyTokenBudget: int64(spec.Budgets.DailyTokens),
		DailyRunBudget:   int32(spec.Budgets.DailyRuns),
		Sources:          feedSourcesToProto(spec.Sources),
	}
}

// feedFullSpecFromProto builds the complete feedspec.Spec a Create/Update
// needs to validate and persist: the recipe (spec) plus the identity fields
// (slug/title/description/language/kind) that live on Feed itself, not on
// FeedSpec, because they apply even to aggregates, which have no spec at all
// (gen/aff/v1/feed.pb.go's Feed doc comment).
func feedFullSpecFromProto(in *affv1.Feed, kind model.FeedKind) feedspec.Spec {
	spec := feedSpecFromProto(in.GetSpec())
	spec.Slug = in.GetSlug()
	spec.Title = in.GetTitle()
	spec.Description = in.GetDescription()
	spec.Language = in.GetLanguage()
	spec.Kind = kind
	if kind == model.KindAggregate {
		spec.Members = in.GetMemberSlugs()
	}
	return spec
}

func (s *FeedServer) feedToProto(ctx context.Context, rec feedRecord) (*affv1.Feed, error) {
	p := &affv1.Feed{
		Id:                  rec.ID,
		Slug:                rec.Slug,
		Kind:                feedKindToProto(rec.Kind),
		Title:               rec.Title,
		Description:         rec.Description,
		Language:            rec.Language,
		Author:              rec.Author,
		Copyright:           rec.Copyright,
		OgImage:             rec.OGImage,
		TtlMinutes:          int32(rec.TTLMinutes),
		Enabled:             rec.Enabled,
		CreatedAt:           timestamppb.New(rec.CreatedAt),
		JitterOffsetSeconds: int32(rec.JitterOffset),
		ConsecutiveFailures: int32(rec.ConsecutiveFailures),
		Version:             rec.Version,
	}
	if !rec.LastBuiltAt.IsZero() {
		p.LastBuiltAt = timestamppb.New(rec.LastBuiltAt)
	}
	if rec.Kind == model.KindAggregate {
		members, err := s.feedListMembers(ctx, rec.ID)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "loading members for feed %d: %v", rec.ID, err)
		}
		p.MemberSlugs = members
	} else {
		spec, err := feedspec.Import([]byte(rec.SpecJSON))
		if err != nil {
			return nil, status.Errorf(codes.Internal, "decoding spec for feed %d: %v", rec.ID, err)
		}
		p.Spec = feedSpecToProto(spec)
	}
	return p, nil
}

// --- validation ------------------------------------------------------------

// feedProblemMessage turns a feedspec.Problem's stable reason token into
// human text for the UI. The token itself stays the machine-readable part
// (already on FieldError.Field); this is display only.
func feedProblemMessage(reason string) string {
	switch reason {
	case feedspec.ReasonSlugShape:
		return "slug must be 3-48 lowercase letters, digits or hyphens"
	case feedspec.ReasonSlugReserved:
		return "slug is reserved and cannot be used"
	case feedspec.ReasonCronInvalid:
		return "cron expression is invalid"
	case feedspec.ReasonTimezoneInvalid:
		return "timezone is not a recognised IANA zone"
	case feedspec.ReasonTemplateInvalid:
		return "prompt template references an unknown variable or fails to execute"
	case feedspec.ReasonKindInvalid:
		return "kind must be generative, grounded or aggregate"
	case feedspec.ReasonGroundedRequiresSource:
		return "grounded feeds require at least one source"
	case feedspec.ReasonGenerativeForbidsSource:
		return "generative feeds must not declare sources"
	case feedspec.ReasonAggregateRequiresMember:
		return "aggregate feeds require at least one member"
	case feedspec.ReasonAggregateForbidsSource:
		return "aggregate feeds must not declare sources"
	case feedspec.ReasonAggregateForbidsPrompt:
		return "aggregate feeds must not declare prompts"
	case feedspec.ReasonAggregateSelfMember:
		return "an aggregate cannot list itself as a member"
	case feedspec.ReasonBudgetDailyTokensZero:
		return "daily token budget must be greater than zero"
	case feedspec.ReasonBudgetDailyRunsZero:
		return "daily run budget must be greater than zero"
	case feedspec.ReasonItemsPerRunRange:
		return "items per run is out of range"
	case feedspec.ReasonFeedWindowRange:
		return "feed window is out of range"
	default:
		return reason
	}
}

func feedProblemsToFieldErrors(problems []feedspec.Problem) []*affv1.FieldError {
	if len(problems) == 0 {
		return nil
	}
	out := make([]*affv1.FieldError, len(problems))
	for i, p := range problems {
		out[i] = &affv1.FieldError{Field: p.Field, Message: feedProblemMessage(p.Reason)}
	}
	return out
}

// feedValidationError is used by the mutating RPCs (Create/Update/
// ImportTOML), none of which have a FieldError-carrying response type on the
// wire (only ValidateSpecResponse does) — so problems are folded into one
// InvalidArgument message instead. ValidateSpec itself returns the
// structured, per-field form.
func feedValidationError(problems []feedspec.Problem) error {
	parts := make([]string, 0, len(problems))
	for _, p := range problems {
		parts = append(parts, p.Field+": "+feedProblemMessage(p.Reason))
	}
	return status.Error(codes.InvalidArgument, "invalid feed spec: "+strings.Join(parts, "; "))
}

// feedCheckMembers is the RPC-layer half of "aggregates cannot nest" (§14.2):
// feedspec.Validate cannot enforce it because it has no visibility into other
// feeds' kinds, so this is where the invariant actually has an owner.
func (s *FeedServer) feedCheckMembers(ctx context.Context, aggregateSlug string, members []string) error {
	for _, slug := range members {
		if slug == aggregateSlug {
			return status.Errorf(codes.InvalidArgument, "aggregate %q cannot list itself as a member", aggregateSlug)
		}
		var kind string
		err := s.store.Writer().QueryRowContext(ctx,
			`SELECT kind FROM feeds WHERE slug = ? AND deleted_at IS NULL`, slug).Scan(&kind)
		if errors.Is(err, sql.ErrNoRows) {
			return status.Errorf(codes.InvalidArgument, "member feed %q not found", slug)
		}
		if err != nil {
			return status.Errorf(codes.Internal, "looking up member %q: %v", slug, err)
		}
		if model.FeedKind(kind) == model.KindAggregate {
			return status.Errorf(codes.InvalidArgument,
				"member feed %q is itself an aggregate; aggregates cannot nest (PLAN.md §14.2)", slug)
		}
	}
	return nil
}

// --- create / update primitives, shared by Create/Update/ImportTOML --------

// feedIdentityExtra is the Feed-level publishing identity (§14.1) that lives
// alongside, not inside, a recipe: it applies even to aggregates, which have
// no feedspec.Spec at all.
type feedIdentityExtra struct {
	Enabled    bool
	Author     string
	Copyright  string
	OGImage    string
	TTLMinutes int32
}

// feedReplaceMembersTx replaces aggregateID's membership inside tx. Every
// member is re-validated against the live table (not just against the
// pre-check the caller already ran) so a member deleted between the check
// and the write cannot slip through — the two are not the same transaction.
func feedReplaceMembersTx(ctx context.Context, tx *sql.Tx, aggregateID int64, members []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM feed_members WHERE aggregate_feed_id = ?`, aggregateID); err != nil {
		return status.Errorf(codes.Internal, "clearing members for feed %d: %v", aggregateID, err)
	}
	for i, slug := range members {
		var memberID int64
		err := tx.QueryRowContext(ctx,
			`SELECT id FROM feeds WHERE slug = ? AND deleted_at IS NULL`, slug).Scan(&memberID)
		if errors.Is(err, sql.ErrNoRows) {
			return status.Errorf(codes.InvalidArgument, "member feed %q not found", slug)
		}
		if err != nil {
			return status.Errorf(codes.Internal, "looking up member %q: %v", slug, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO feed_members (aggregate_feed_id, member_feed_id, position) VALUES (?, ?, ?)`,
			aggregateID, memberID, i); err != nil {
			return status.Errorf(codes.Internal, "adding member %q: %v", slug, err)
		}
	}
	return nil
}

// feedInsert creates a new feed row (and its feed_members rows, for an
// aggregate) in one transaction. Callers are responsible for having already
// run feedspec.Validate and feedCheckMembers — this function assumes spec is
// valid and only guards against a slug collision.
func (s *FeedServer) feedInsert(ctx context.Context, spec feedspec.Spec, extra feedIdentityExtra) (int64, error) {
	specJSON, err := feedspec.Export(spec)
	if err != nil {
		return 0, status.Errorf(codes.Internal, "encoding spec: %v", err)
	}

	tx, err := s.store.Writer().BeginTx(ctx, nil)
	if err != nil {
		return 0, status.Errorf(codes.Internal, "begin create feed: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM feeds WHERE slug = ?`, spec.Slug).Scan(&exists); err != nil {
		return 0, status.Errorf(codes.Internal, "checking slug %q: %v", spec.Slug, err)
	}
	if exists > 0 {
		return 0, status.Errorf(codes.AlreadyExists, "feed slug %q already exists", spec.Slug)
	}

	now := feedFormatTime(time.Now())
	// jitter_offset is derived from hash(slug) and stored so the UI's "next
	// three runs" readback matches what the scheduler will actually do
	// (§14.3) — computed once here, not recomputed at display time.
	jitter := int(schedule.Offset(spec.Slug, feedJitterWindow).Seconds())

	res, err := tx.ExecContext(ctx, `
		INSERT INTO feeds (slug, title, description, language, kind, spec_json, enabled, timezone,
		                    jitter_offset, author, copyright, og_image, ttl_minutes, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		spec.Slug, spec.Title, spec.Description, spec.Language, string(spec.Kind), string(specJSON),
		feedBoolToInt(extra.Enabled), spec.Timezone, jitter,
		feedNullString(extra.Author), feedNullString(extra.Copyright), feedNullString(extra.OGImage),
		extra.TTLMinutes, now, now)
	if err != nil {
		return 0, status.Errorf(codes.Internal, "creating feed %q: %v", spec.Slug, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, status.Errorf(codes.Internal, "feed id: %v", err)
	}

	if spec.Kind == model.KindAggregate {
		if err := feedReplaceMembersTx(ctx, tx, id, spec.Members); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, status.Errorf(codes.Internal, "committing feed %q: %v", spec.Slug, err)
	}
	return id, nil
}

// feedApplyUpdate updates rec's row under optimistic concurrency. Membership
// (feed_members) is deliberately untouched here — SetMembers is the only
// path that changes it (gen/aff/v1/feed_grpc.pb.go's doc comment on
// FeedServiceServer) — callers that DO need to change membership alongside
// an update (ImportTOML replacing an aggregate's recipe) do so separately,
// after this succeeds.
func (s *FeedServer) feedApplyUpdate(ctx context.Context, rec feedRecord, spec feedspec.Spec, extra feedIdentityExtra, expectedVersion int64) error {
	specJSON, err := feedspec.Export(spec)
	if err != nil {
		return status.Errorf(codes.Internal, "encoding spec: %v", err)
	}

	now := feedFormatTime(time.Now())
	res, err := s.store.Writer().ExecContext(ctx, `
		UPDATE feeds
		SET slug = ?, title = ?, description = ?, language = ?, spec_json = ?,
		    enabled = ?, timezone = ?, author = ?, copyright = ?, og_image = ?,
		    ttl_minutes = ?, updated_at = ?, version = version + 1
		WHERE id = ? AND version = ?`,
		spec.Slug, spec.Title, spec.Description, spec.Language, string(specJSON),
		feedBoolToInt(extra.Enabled), spec.Timezone,
		feedNullString(extra.Author), feedNullString(extra.Copyright), feedNullString(extra.OGImage),
		extra.TTLMinutes, now, rec.ID, expectedVersion)
	if err != nil {
		return status.Errorf(codes.Internal, "updating feed %d: %v", rec.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return status.Errorf(codes.Internal, "rows affected updating feed %d: %v", rec.ID, err)
	}
	if n == 0 {
		return feedVersionConflict(rec.ID, expectedVersion, rec.Version)
	}
	return nil
}

// feedCheckSlugImmutable refuses a slug change once the feed has published at
// least one item (§14.1: the slug is embedded in every item's Tag URI guid,
// so changing it orphans every item ever published). Items are counted
// regardless of soft-delete state — a deleted item's guid must still never
// be reused (§12.4), so the orphan risk does not go away just because the
// item is hidden from the feed window.
func (s *FeedServer) feedCheckSlugImmutable(ctx context.Context, rec feedRecord, newSlug string) error {
	if newSlug == rec.Slug {
		return nil
	}
	var itemCount int
	if err := s.store.Writer().QueryRowContext(ctx,
		`SELECT COUNT(1) FROM items WHERE feed_id = ?`, rec.ID).Scan(&itemCount); err != nil {
		return status.Errorf(codes.Internal, "checking published items for feed %d: %v", rec.ID, err)
	}
	if itemCount > 0 {
		return status.Errorf(codes.FailedPrecondition,
			"feed %q: slug is immutable after first publish — it is embedded in every item's Tag URI guid (PLAN.md §14.1)",
			rec.Slug)
	}
	return nil
}

// --- FeedServiceServer -----------------------------------------------------

func feedEncodeCursor(id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(id, 10)))
}

func feedDecodeCursor(token string) (int64, error) {
	if token == "" {
		return 0, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return 0, fmt.Errorf("invalid page token")
	}
	id, err := strconv.ParseInt(string(b), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid page token")
	}
	return id, nil
}

func feedEscapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// List paginates with an opaque cursor (§11): a base64 encoding of the last
// row's id, so a caller can never construct or rely on its internal shape.
func (s *FeedServer) List(ctx context.Context, req *affv1.FeedServiceListRequest) (*affv1.FeedServiceListResponse, error) {
	pageSize := int(req.GetPageSize())
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 50
	}
	afterID, err := feedDecodeCursor(req.GetPageToken())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	query := `SELECT ` + feedFullColumns + ` FROM feeds WHERE deleted_at IS NULL AND id > ?`
	args := []any{afterID}
	if req.GetEnabledOnly() {
		query += ` AND enabled = 1`
	}
	if search := strings.TrimSpace(req.GetSearch()); search != "" {
		query += ` AND (slug LIKE ? ESCAPE '\' OR title LIKE ? ESCAPE '\')`
		like := "%" + feedEscapeLike(search) + "%"
		args = append(args, like, like)
	}
	query += ` ORDER BY id ASC LIMIT ?`
	// Fetch one extra row so a next page can be detected without a second
	// COUNT query.
	args = append(args, pageSize+1)

	rows, err := s.store.Writer().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "listing feeds: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var recs []feedRecord
	for rows.Next() {
		rec, err := feedScanRecord(rows)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "scanning feed: %v", err)
		}
		recs = append(recs, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, status.Errorf(codes.Internal, "iterating feeds: %v", err)
	}

	var nextToken string
	if len(recs) > pageSize {
		nextToken = feedEncodeCursor(recs[pageSize-1].ID)
		recs = recs[:pageSize]
	}

	out := make([]*affv1.Feed, 0, len(recs))
	for _, rec := range recs {
		p, err := s.feedToProto(ctx, rec)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return &affv1.FeedServiceListResponse{Feeds: out, NextPageToken: nextToken}, nil
}

// Get looks a feed up by id or slug (§12.3: "the editor deep-link by URL
// without a prior List round trip").
func (s *FeedServer) Get(ctx context.Context, req *affv1.FeedServiceGetRequest) (*affv1.FeedServiceGetResponse, error) {
	var (
		rec feedRecord
		err error
	)
	switch {
	case req.GetId() != 0:
		rec, err = s.feedRequireFeed(ctx, req.GetId())
	case req.GetSlug() != "":
		rec, err = s.feedRequireFeedBySlug(ctx, req.GetSlug())
	default:
		return nil, status.Error(codes.InvalidArgument, "id or slug is required")
	}
	if err != nil {
		return nil, err
	}
	p, err := s.feedToProto(ctx, rec)
	if err != nil {
		return nil, err
	}
	return &affv1.FeedServiceGetResponse{Feed: p}, nil
}

// Create validates and persists a new feed. id, version,
// jitter_offset_seconds, consecutive_failures and last_built_at on the
// request are server-assigned and ignored, per the request message's doc
// comment.
func (s *FeedServer) Create(ctx context.Context, req *affv1.FeedServiceCreateRequest) (*affv1.FeedServiceCreateResponse, error) {
	in := req.GetFeed()
	if in == nil {
		return nil, status.Error(codes.InvalidArgument, "feed is required")
	}
	kind := feedKindFromProto(in.GetKind())
	spec := feedFullSpecFromProto(in, kind)
	if problems := feedspec.Validate(spec); len(problems) > 0 {
		return nil, feedValidationError(problems)
	}
	if kind == model.KindAggregate {
		if err := s.feedCheckMembers(ctx, spec.Slug, spec.Members); err != nil {
			return nil, err
		}
	}

	extra := feedIdentityExtra{
		Enabled:    in.GetEnabled(),
		Author:     in.GetAuthor(),
		Copyright:  in.GetCopyright(),
		OGImage:    in.GetOgImage(),
		TTLMinutes: in.GetTtlMinutes(),
	}
	id, err := s.feedInsert(ctx, spec, extra)
	if err != nil {
		return nil, err
	}

	rec, err := s.feedRequireFeed(ctx, id)
	if err != nil {
		return nil, err
	}
	p, err := s.feedToProto(ctx, rec)
	if err != nil {
		return nil, err
	}

	s.feedInvalidate(ctx, id, spec.Slug)
	return &affv1.FeedServiceCreateResponse{Feed: p}, nil
}

// Update edits an existing feed's identity and recipe. Membership
// (feed_members) is untouched — see feedApplyUpdate's doc comment.
func (s *FeedServer) Update(ctx context.Context, req *affv1.FeedServiceUpdateRequest) (*affv1.FeedServiceUpdateResponse, error) {
	in := req.GetFeed()
	if in == nil {
		return nil, status.Error(codes.InvalidArgument, "feed is required")
	}
	rec, err := s.feedRequireFeed(ctx, in.GetId())
	if err != nil {
		return nil, err
	}
	if rec.Version != req.GetExpectedVersion() {
		return nil, feedVersionConflict(rec.ID, req.GetExpectedVersion(), rec.Version)
	}
	if feedKindFromProto(in.GetKind()) != rec.Kind {
		return nil, status.Error(codes.InvalidArgument, "kind cannot change after creation")
	}
	if err := s.feedCheckSlugImmutable(ctx, rec, in.GetSlug()); err != nil {
		return nil, err
	}

	spec := feedFullSpecFromProto(in, rec.Kind)
	if rec.Kind == model.KindAggregate {
		spec.Members, err = s.feedListMembers(ctx, rec.ID)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "loading members for feed %d: %v", rec.ID, err)
		}
	}
	if problems := feedspec.Validate(spec); len(problems) > 0 {
		return nil, feedValidationError(problems)
	}

	extra := feedIdentityExtra{
		Enabled:    in.GetEnabled(),
		Author:     in.GetAuthor(),
		Copyright:  in.GetCopyright(),
		OGImage:    in.GetOgImage(),
		TTLMinutes: in.GetTtlMinutes(),
	}
	if err := s.feedApplyUpdate(ctx, rec, spec, extra, req.GetExpectedVersion()); err != nil {
		return nil, err
	}

	updated, err := s.feedRequireFeed(ctx, rec.ID)
	if err != nil {
		return nil, err
	}
	p, err := s.feedToProto(ctx, updated)
	if err != nil {
		return nil, err
	}

	// A feed's title, og:image, TTL etc. is baked into every channel element
	// (§11), so ANY successful Update invalidates — not just ones that
	// changed the slug.
	s.feedInvalidate(ctx, rec.ID, rec.Slug)
	if in.GetSlug() != rec.Slug {
		s.feedInvalidate(ctx, rec.ID, in.GetSlug())
	}
	return &affv1.FeedServiceUpdateResponse{Feed: p}, nil
}

// SetEnabled toggles a feed on or off. A disabled feed keeps serving its last
// built content (§6) — this RPC only flips the flag and stops future
// generation; it does not touch items.
func (s *FeedServer) SetEnabled(ctx context.Context, req *affv1.FeedServiceSetEnabledRequest) (*affv1.FeedServiceSetEnabledResponse, error) {
	rec, err := s.feedRequireFeed(ctx, req.GetFeedId())
	if err != nil {
		return nil, err
	}
	if rec.Version != req.GetExpectedVersion() {
		return nil, feedVersionConflict(rec.ID, req.GetExpectedVersion(), rec.Version)
	}

	now := feedFormatTime(time.Now())
	res, err := s.store.Writer().ExecContext(ctx,
		`UPDATE feeds SET enabled = ?, updated_at = ?, version = version + 1 WHERE id = ? AND version = ?`,
		feedBoolToInt(req.GetEnabled()), now, rec.ID, req.GetExpectedVersion())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "setting enabled for feed %d: %v", rec.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rows affected for feed %d: %v", rec.ID, err)
	}
	if n == 0 {
		return nil, feedVersionConflict(rec.ID, req.GetExpectedVersion(), rec.Version)
	}

	updated, err := s.feedRequireFeed(ctx, rec.ID)
	if err != nil {
		return nil, err
	}
	p, err := s.feedToProto(ctx, updated)
	if err != nil {
		return nil, err
	}

	// SetEnabled writes no item at all, yet changes rendered output (a
	// disabled feed keeps its last content but a re-enable resumes it, and
	// the admin rail's status must not disagree with the published state) —
	// exactly the case §11 calls out by name.
	s.feedInvalidate(ctx, rec.ID, rec.Slug)
	return &affv1.FeedServiceSetEnabledResponse{Feed: p}, nil
}

// Delete soft-deletes a feed: it stops publishing (the route 410s, §6), the
// slug is never reused as live, but the row and its items remain — there is
// no hard delete anywhere in this design (§12.4).
func (s *FeedServer) Delete(ctx context.Context, req *affv1.FeedServiceDeleteRequest) (*affv1.FeedServiceDeleteResponse, error) {
	rec, err := s.feedRequireFeed(ctx, req.GetFeedId())
	if err != nil {
		return nil, err
	}
	if rec.Version != req.GetExpectedVersion() {
		return nil, feedVersionConflict(rec.ID, req.GetExpectedVersion(), rec.Version)
	}

	now := feedFormatTime(time.Now())
	res, err := s.store.Writer().ExecContext(ctx,
		`UPDATE feeds SET deleted_at = ?, updated_at = ?, version = version + 1
		 WHERE id = ? AND version = ? AND deleted_at IS NULL`,
		now, now, rec.ID, req.GetExpectedVersion())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "deleting feed %d: %v", rec.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rows affected deleting feed %d: %v", rec.ID, err)
	}
	if n == 0 {
		return nil, feedVersionConflict(rec.ID, req.GetExpectedVersion(), rec.Version)
	}

	s.feedInvalidate(ctx, rec.ID, rec.Slug)
	return &affv1.FeedServiceDeleteResponse{}, nil
}

// RunNow triggers an out-of-cycle manual run. Single-flight is enforced by
// internal/store's existing run lock (StartRun/ErrRunInProgress, §13) rather
// than reimplemented here — a second call while one is already running is
// refused with a status the caller can distinguish from every other failure.
func (s *FeedServer) RunNow(ctx context.Context, req *affv1.FeedServiceRunNowRequest) (*affv1.FeedServiceRunNowResponse, error) {
	rec, err := s.feedRequireFeed(ctx, req.GetFeedId())
	if err != nil {
		return nil, err
	}

	runID, err := s.store.StartRun(ctx, rec.ID, "manual", "rpc:feed.RunNow")
	if errors.Is(err, store.ErrRunInProgress) {
		return nil, status.Errorf(codes.FailedPrecondition, "feed %q already has a run in progress", rec.Slug)
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "starting run for feed %d: %v", rec.ID, err)
	}

	if s.exec != nil {
		s.exec.ExecuteRun(rec.ID, runID)
	}
	return &affv1.FeedServiceRunNowResponse{RunId: runID}, nil
}

// ValidateSpec is the server-side gate behind the editor's live validation
// (§7: "the UI's copy is a convenience, never the gate"). feedspec.Validate
// executes the prompt templates rather than merely parsing them, per §7's
// own reasoning: Parse succeeding does not catch an unknown {{.Variable}}.
func (s *FeedServer) ValidateSpec(ctx context.Context, req *affv1.FeedServiceValidateSpecRequest) (*affv1.FeedServiceValidateSpecResponse, error) {
	spec := feedSpecFromProto(req.GetSpec())
	spec.Slug = req.GetSlug()
	spec.Kind = feedKindFromProto(req.GetKind())

	problems := feedspec.Validate(spec)
	return &affv1.FeedServiceValidateSpecResponse{
		Valid:  len(problems) == 0,
		Errors: feedProblemsToFieldErrors(problems),
	}, nil
}

// SetMembers is the only place aggregate membership changes, and the only
// place "aggregates cannot nest" is enforced (§14.2) — feedCheckMembers is
// where that owner lives.
func (s *FeedServer) SetMembers(ctx context.Context, req *affv1.FeedServiceSetMembersRequest) (*affv1.FeedServiceSetMembersResponse, error) {
	rec, err := s.feedRequireFeed(ctx, req.GetAggregateFeedId())
	if err != nil {
		return nil, err
	}
	if rec.Kind != model.KindAggregate {
		return nil, status.Errorf(codes.InvalidArgument, "feed %q is not an aggregate", rec.Slug)
	}
	if rec.Version != req.GetExpectedVersion() {
		return nil, feedVersionConflict(rec.ID, req.GetExpectedVersion(), rec.Version)
	}

	members := req.GetMemberSlugs()
	if err := s.feedCheckMembers(ctx, rec.Slug, members); err != nil {
		return nil, err
	}

	tx, err := s.store.Writer().BeginTx(ctx, nil)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "begin set members for feed %d: %v", rec.ID, err)
	}
	defer func() { _ = tx.Rollback() }()

	now := feedFormatTime(time.Now())
	res, err := tx.ExecContext(ctx,
		`UPDATE feeds SET version = version + 1, updated_at = ? WHERE id = ? AND version = ?`,
		now, rec.ID, req.GetExpectedVersion())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "bumping version for feed %d: %v", rec.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rows affected for feed %d: %v", rec.ID, err)
	}
	if n == 0 {
		return nil, feedVersionConflict(rec.ID, req.GetExpectedVersion(), rec.Version)
	}

	if err := feedReplaceMembersTx(ctx, tx, rec.ID, members); err != nil {
		return nil, err
	}

	// Mirror the new membership into spec_json too, so ExportTOML and a
	// subsequent Get both see the same list SetMembers just wrote, without a
	// second round trip through feed_members.
	spec, err := feedspec.Import([]byte(rec.SpecJSON))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "decoding spec for feed %d: %v", rec.ID, err)
	}
	spec.Members = members
	specJSON, err := feedspec.Export(spec)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "encoding spec for feed %d: %v", rec.ID, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE feeds SET spec_json = ? WHERE id = ?`, string(specJSON), rec.ID); err != nil {
		return nil, status.Errorf(codes.Internal, "storing member list for feed %d: %v", rec.ID, err)
	}

	if err := tx.Commit(); err != nil {
		return nil, status.Errorf(codes.Internal, "committing set members for feed %d: %v", rec.ID, err)
	}

	updated, err := s.feedRequireFeed(ctx, rec.ID)
	if err != nil {
		return nil, err
	}
	p, err := s.feedToProto(ctx, updated)
	if err != nil {
		return nil, err
	}

	// The aggregate's own rendered output changed (its member list determines
	// what it renders, §14.2) even though not one item row was touched.
	s.feedInvalidate(ctx, rec.ID, rec.Slug)
	return &affv1.FeedServiceSetMembersResponse{Feed: p}, nil
}

// ExportTOML serializes a feed's recipe for the "aff recipe export|import"
// versioning and disaster-recovery path (§7). Despite the RPC/field name
// (matching PLAN.md §7's wording), the payload is JSON — see
// internal/feedspec/codec.go's Export/Import doc comment for why: no TOML
// dependency exists in go.mod and this change may not add one.
func (s *FeedServer) ExportTOML(ctx context.Context, req *affv1.FeedServiceExportTOMLRequest) (*affv1.FeedServiceExportTOMLResponse, error) {
	rec, err := s.feedRequireFeed(ctx, req.GetFeedId())
	if err != nil {
		return nil, err
	}
	spec, err := feedspec.Import([]byte(rec.SpecJSON))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "decoding spec for feed %d: %v", rec.ID, err)
	}
	spec.Slug, spec.Title, spec.Description, spec.Language, spec.Kind =
		rec.Slug, rec.Title, rec.Description, rec.Language, rec.Kind
	if rec.Kind == model.KindAggregate {
		spec.Members, err = s.feedListMembers(ctx, rec.ID)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "loading members for feed %d: %v", rec.ID, err)
		}
	}

	out, err := feedspec.Export(spec)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "encoding recipe for feed %d: %v", rec.ID, err)
	}
	return &affv1.FeedServiceExportTOMLResponse{Toml: string(out)}, nil
}

// ImportTOML round-trips ExportTOML's payload: replaces an existing feed's
// recipe (feed_id set, subject to expected_version) or creates a new feed
// (feed_id unset).
func (s *FeedServer) ImportTOML(ctx context.Context, req *affv1.FeedServiceImportTOMLRequest) (*affv1.FeedServiceImportTOMLResponse, error) {
	spec, err := feedspec.Import([]byte(req.GetToml()))
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "parsing recipe: %v", err)
	}

	if req.GetFeedId() == 0 {
		if problems := feedspec.Validate(spec); len(problems) > 0 {
			return nil, feedValidationError(problems)
		}
		if spec.Kind == model.KindAggregate {
			if err := s.feedCheckMembers(ctx, spec.Slug, spec.Members); err != nil {
				return nil, err
			}
		}
		id, err := s.feedInsert(ctx, spec, feedIdentityExtra{})
		if err != nil {
			return nil, err
		}
		rec, err := s.feedRequireFeed(ctx, id)
		if err != nil {
			return nil, err
		}
		p, err := s.feedToProto(ctx, rec)
		if err != nil {
			return nil, err
		}
		s.feedInvalidate(ctx, id, spec.Slug)
		return &affv1.FeedServiceImportTOMLResponse{Feed: p}, nil
	}

	rec, err := s.feedRequireFeed(ctx, req.GetFeedId())
	if err != nil {
		return nil, err
	}
	if rec.Version != req.GetExpectedVersion() {
		return nil, feedVersionConflict(rec.ID, req.GetExpectedVersion(), rec.Version)
	}
	if spec.Kind != rec.Kind {
		return nil, status.Error(codes.InvalidArgument, "kind cannot change after creation")
	}
	if err := s.feedCheckSlugImmutable(ctx, rec, spec.Slug); err != nil {
		return nil, err
	}
	if spec.Kind == model.KindAggregate {
		if err := s.feedCheckMembers(ctx, spec.Slug, spec.Members); err != nil {
			return nil, err
		}
	}
	if problems := feedspec.Validate(spec); len(problems) > 0 {
		return nil, feedValidationError(problems)
	}

	extra := feedIdentityExtra{
		Enabled:    rec.Enabled,
		Author:     rec.Author,
		Copyright:  rec.Copyright,
		OGImage:    rec.OGImage,
		TTLMinutes: int32(rec.TTLMinutes),
	}
	if err := s.feedApplyUpdate(ctx, rec, spec, extra, req.GetExpectedVersion()); err != nil {
		return nil, err
	}

	if spec.Kind == model.KindAggregate {
		tx, err := s.store.Writer().BeginTx(ctx, nil)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "begin member update for feed %d: %v", rec.ID, err)
		}
		if err := feedReplaceMembersTx(ctx, tx, rec.ID, spec.Members); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, status.Errorf(codes.Internal, "committing member update for feed %d: %v", rec.ID, err)
		}
	}

	updated, err := s.feedRequireFeed(ctx, rec.ID)
	if err != nil {
		return nil, err
	}
	p, err := s.feedToProto(ctx, updated)
	if err != nil {
		return nil, err
	}

	s.feedInvalidate(ctx, rec.ID, rec.Slug)
	if spec.Slug != rec.Slug {
		s.feedInvalidate(ctx, rec.ID, spec.Slug)
	}
	return &affv1.FeedServiceImportTOMLResponse{Feed: p}, nil
}
