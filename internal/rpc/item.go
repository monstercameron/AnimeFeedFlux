// ItemServer implements aff.v1.ItemService (PLAN.md §11, §12.4): full CRUD
// over published items, except there is deliberately no hard delete.
//
// PurgeDeleted was in an earlier draft and was cut (PLAN.md §12.4): it
// contradicted §7's "only runs rows and stale embeddings are ever pruned",
// §5.1's guid that is "never freed", and the permalink that 410s forever —
// purging leaves nothing to 410 on. affv1.ItemServiceServer (gen/aff/v1/
// item_grpc.pb.go) has no PurgeDeleted method, so there is nothing to
// implement even by omission; item_test.go asserts this by reflection so a
// future regeneration that reintroduces it fails loudly. Do not add one here.
//
// This file talks to SQLite directly through store.Store's exported
// Writer()/Reader() handles rather than adding methods to package store,
// because internal/store/items.go's SoftDeleteItem/RestoreItem take no
// expected_version, UpdateItem cannot be made to also write item_revisions
// atomically without editing that file, and there is no GetItem-by-id, no
// FTS5 search and no pagination there at all — and store.go/items.go are
// off-limits to edit for this change (PLAN.md §11, mirroring run.go's
// identical rationale for RunService.History). Where an existing *Store
// method already does the hard part correctly (InsertItem's nullable-column
// handling, PromoteSample's UNIQUE(feed_id, published_at) collision retry
// and item+sample atomicity), this file reuses it instead of duplicating it.
//
// Every mutation here calls the injected publish.Invalidator strictly AFTER
// its write commits (PLAN.md §11's closing paragraph: firing before commit
// would invalidate the cache for a write that then rolls back). Helper names
// are prefixed `item` per the sibling-file convention (run.go/auth.go/
// feed.go/sample.go use their own prefixes) so concurrent edits to this
// package do not collide on an identifier.
package rpc

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	sqlite "modernc.org/sqlite"
	sqlitelib "modernc.org/sqlite/lib"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/ids"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/publish"
	"github.com/monstercameron/AnimeFeedFlux/internal/sanitize"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// itemSanitizeHTML is the sanitize chokepoint for every markup field that
// enters through this service.
//
// internal/generate/contract.go sanitizes what the GENERATION pipeline
// produces, and for a long time that was described as though it covered
// everything — but this file is a second, entirely independent ingress:
// Create and Update take body_html/answer_html straight off the request, and
// PromoteSample takes them off a stored sample payload. Those values are
// written into a document COMPLETELY UNESCAPED downstream (render/
// permalink.go, render/rss.go's content:encoded), so "the generation pipeline
// sanitized it" is not a property this path can inherit — it never went
// through that pipeline.
//
// Applied unconditionally, including to values that ARE already sanitized
// (PromoteSample's candidates): sanitize.HTML is idempotent by contract (see
// its decodeEntities doc comment, which exists specifically so
// HTML(HTML(s)) == HTML(s)), so a second pass costs a string walk and buys a
// boundary that holds regardless of which path the value arrived on. A
// chokepoint that is only correct when every caller remembers it is not a
// chokepoint.
func itemSanitizeHTML(s string) string { return sanitize.HTML(s) }

// itemTimeLayout mirrors internal/store's unexported timeLayout (items.go:
// "the one format every timestamp column uses: RFC3339Nano, UTC"). It must
// match exactly — this file compares its own formatted bounds against
// store-written TEXT columns with plain SQL operators.
const itemTimeLayout = time.RFC3339Nano

func itemFormatTime(t time.Time) string { return t.UTC().Format(itemTimeLayout) }

func itemParseTime(s string) (time.Time, error) { return time.Parse(itemTimeLayout, s) }

func itemParseNullTime(ns sql.NullString) (time.Time, error) {
	if !ns.Valid || ns.String == "" {
		return time.Time{}, nil
	}
	return itemParseTime(ns.String)
}

// itemNullString maps "" to NULL, matching internal/store/items.go's
// nullString: answer_html/link/source_name have no default and are
// legitimately absent, so an empty Go string must round-trip as NULL rather
// than a stored "".
func itemNullString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// itemSelectColumns mirrors internal/store/items.go's unexported itemColumns
// exactly (same table, same order) — duplicated here because that constant
// is unexported and store/items.go is off-limits to edit.
const itemSelectColumns = `id, feed_id, item_key, content_hash, title, summary_text, body_html,
	answer_html, link, source_name, published_at, origin, version,
	created_at, updated_at, edited_at, deleted_at`

// itemScanner is satisfied by both *sql.Row and *sql.Rows.
type itemScanner interface {
	Scan(dest ...any) error
}

// itemQuerier is satisfied by *sql.DB and *sql.Tx, so read helpers below can
// run either against the store directly or inside a transaction this file
// opened.
type itemQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func itemScanRow(sc itemScanner) (model.Item, error) {
	var (
		it                                model.Item
		origin                            string
		answerHTML, link, sourceName      sql.NullString
		publishedAt, createdAt, updatedAt string
		editedAt, deletedAt               sql.NullString
	)
	if err := sc.Scan(
		&it.ID, &it.FeedID, &it.ItemKey, &it.ContentHash, &it.Title, &it.SummaryText, &it.BodyHTML,
		&answerHTML, &link, &sourceName, &publishedAt, &origin, &it.Version,
		&createdAt, &updatedAt, &editedAt, &deletedAt,
	); err != nil {
		return model.Item{}, err
	}
	it.AnswerHTML = answerHTML.String
	it.Link = link.String
	it.SourceName = sourceName.String
	it.Origin = model.Origin(origin)

	var err error
	if it.PublishedAt, err = itemParseTime(publishedAt); err != nil {
		return model.Item{}, fmt.Errorf("rpc: parsing item published_at: %w", err)
	}
	if it.CreatedAt, err = itemParseTime(createdAt); err != nil {
		return model.Item{}, fmt.Errorf("rpc: parsing item created_at: %w", err)
	}
	if it.UpdatedAt, err = itemParseTime(updatedAt); err != nil {
		return model.Item{}, fmt.Errorf("rpc: parsing item updated_at: %w", err)
	}
	if it.EditedAt, err = itemParseNullTime(editedAt); err != nil {
		return model.Item{}, fmt.Errorf("rpc: parsing item edited_at: %w", err)
	}
	if it.DeletedAt, err = itemParseNullTime(deletedAt); err != nil {
		return model.Item{}, fmt.Errorf("rpc: parsing item deleted_at: %w", err)
	}
	// Tags are not persisted: there is no tags column in the schema
	// (migrations/0002_feeds_items.sql), matching store/items.go's own
	// documented omission — not a bug here either.
	return it, nil
}

// itemLoadByID reads one item by its numeric id (the RPC surface's Id/ItemId,
// distinct from the opaque item_key). store.ErrNotFound is returned, wrapped,
// when no row matches, so callers can map it with errors.Is the same way the
// rest of this codebase does.
func itemLoadByID(ctx context.Context, db itemQuerier, id int64) (model.Item, error) {
	row := db.QueryRowContext(ctx, `SELECT `+itemSelectColumns+` FROM items WHERE id = ?`, id)
	it, err := itemScanRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Item{}, fmt.Errorf("rpc: item %d: %w", id, store.ErrNotFound)
	}
	if err != nil {
		return model.Item{}, fmt.Errorf("rpc: loading item %d: %w", id, err)
	}
	return it, nil
}

// itemFeedSlug resolves a feed id to its slug, for both the Invalidator call
// (keyed by slug, not id) and existence checks on Create.
func itemFeedSlug(ctx context.Context, db itemQuerier, feedID int64) (string, error) {
	var slug string
	err := db.QueryRowContext(ctx, `SELECT slug FROM feeds WHERE id = ?`, feedID).Scan(&slug)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("rpc: feed %d: %w", feedID, store.ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("rpc: resolving feed %d: %w", feedID, err)
	}
	return slug, nil
}

// itemNewestPublished returns the feed's current newest published_at, or the
// zero Time if the feed has no items yet. excludeID, if non-zero, omits that
// item's own row — an in-place edit that leaves published_at unchanged must
// not be compared against itself and rejected as "backdated" (RULE-7 below).
//
// No deleted_at filter, deliberately: this mirrors internal/store/samples.go
// PromoteSample's identical "MAX(published_at) FROM items WHERE feed_id = ?"
// read, kept consistent rather than introducing a second, disagreeing
// definition of "the feed's current newest" in the same codebase.
func itemNewestPublished(ctx context.Context, db itemQuerier, feedID, excludeID int64) (time.Time, error) {
	query := `SELECT MAX(published_at) FROM items WHERE feed_id = ?`
	args := []any{feedID}
	if excludeID != 0 {
		query += ` AND id != ?`
		args = append(args, excludeID)
	}
	var raw sql.NullString
	if err := db.QueryRowContext(ctx, query, args...).Scan(&raw); err != nil {
		return time.Time{}, fmt.Errorf("rpc: reading newest published_at for feed %d: %w", feedID, err)
	}
	if !raw.Valid || raw.String == "" {
		return time.Time{}, nil
	}
	t, err := itemParseTime(raw.String)
	if err != nil {
		return time.Time{}, fmt.Errorf("rpc: parsing newest published_at for feed %d: %w", feedID, err)
	}
	return t, nil
}

// itemMaxPublishedAtYear is one past the highest year every renderer's
// four-digit-year date layout can actually round-trip. Fuzzing found that
// RFC822 and RFC3339 (internal/render/dates.go) each format a year >= 10000
// into a string their OWN reference layout ("2006") then rejects on Parse —
// time.Time.Format writes as many digits as the year needs, but Go's
// reference-layout Parse always consumes exactly four, so the two
// operations silently stop being inverses past year 9999. §5.1/§5.2/§5.3
// all promise a four-digit year; nothing between here and the renderer
// otherwise bounds published_at at all, so this is where that promise is
// actually enforced rather than merely documented.
const itemMaxPublishedAtYear = 9999

// itemValidatePublishedAt rejects a published_at that cannot survive a round
// trip through every feed format's date layout, and separately rejects the
// exact zero time.Time value. Two checks, not one:
//
//   - The zero value (0001-01-01T00:00:00Z) sits INSIDE the four-digit-year
//     range and parses just fine — it is wrong for a different reason. In
//     Go, `var t time.Time` — an uninitialized field, a struct literal that
//     forgot to set PublishedAt, a bug in some future caller — is far more
//     likely to reach this path than anyone ever intending to publish an
//     item dated the year 1, and unlike an out-of-range year it renders as
//     a plausible-looking "0001-01-01" pubDate instead of failing loudly
//     anywhere downstream. A renderer that quietly emits it produces a
//     document that disagrees with what the caller almost certainly meant,
//     which is worse than a rejection here.
//   - The four-digit-year bound is the fuzz-found gap itself: a year of
//     10000 or more (and, symmetrically, a year below 1 — Go's proleptic
//     calendar allows negative/zero years, RFC 822/3339 four-digit years do
//     not) formats into a string no reader can parse back.
//
// Distinguished from itemRejectBackdated's message on purpose: an operator
// who gets "date rejected" with no reason tends to retry the exact same
// request, and RULE-7 (relative to another item) and this range check
// (absolute, about the value itself) need different fixes.
func itemValidatePublishedAt(t time.Time) error {
	if t.IsZero() {
		return status.Error(codes.InvalidArgument,
			"rpc: published_at is the zero time.Time (0001-01-01T00:00:00Z) — this is almost always an unset/uninitialized value rather than an intended date; set an explicit published_at or omit the field to default to now")
	}
	if y := t.Year(); y < 1 || y > itemMaxPublishedAtYear {
		return status.Errorf(codes.InvalidArgument,
			"rpc: published_at year %d is outside the four-digit range [0001, %d] that every feed format's date layout requires (PLAN.md §5.1/§5.2/§5.3) — this is a format-range rejection, not the RULE-7 backdating rule",
			y, itemMaxPublishedAtYear)
	}
	return nil
}

// itemRejectBackdated enforces RULE-7 (PLAN.md §5.5): Slack's bookmark model
// fetches only items dated strictly later than the last one it retrieved, so
// a published_at at or before the feed's current newest item is invisible to
// it forever. Refusing with a message that says so beats silently accepting
// it and producing an item nobody's RSS reader will ever surface.
func itemRejectBackdated(published, newest time.Time, feedSlug string) error {
	if newest.IsZero() || published.After(newest) {
		return nil
	}
	return status.Errorf(codes.InvalidArgument,
		"rpc: published_at %s is at or before feed %q's current newest item (%s) — Slack's bookmark model would never surface it; choose a strictly later time",
		published.UTC().Format(time.RFC3339), feedSlug, newest.UTC().Format(time.RFC3339))
}

// itemContentHash gives a Create/PublishCorrection-authored item a value for
// items.content_hash, which is UNIQUE(feed_id, content_hash) (migrations/
// 0002_feeds_items.sql) and NOT NULL — unlike generation's dedup hash (§9),
// this one exists only to satisfy that constraint for hand-authored rows, so
// a plain digest of the fields that make the row unique is sufficient; nothing
// here claims it is a semantic novelty signal.
func itemContentHash(feedID int64, title, summary, body, link string) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00%s\x00%s\x00%s", feedID, title, summary, body, link)))
	return hex.EncodeToString(h[:])
}

// itemTimestampRetries caps the +1s bumps PublishCorrection will try before
// giving up on a UNIQUE(feed_id, published_at) collision, mirroring
// internal/store/samples.go's identical promoteTimestampRetries constant and
// its reasoning: a handful of attempts is the point past which colliding on
// every single try stops being a race and starts being a bug worth surfacing.
const itemTimestampRetries = 5

// itemCreateAfterReadNewestHook, when non-nil, runs right after Create has
// read the feed's current newest published_at and validated the new item's
// stamp against it, but before the insert attempt — mirroring
// internal/store/samples.go's promoteAfterReadNewestHook exactly, for the
// identical reason: Create's "read newest" and "insert" are two separate
// statements (not one transaction, same as PromoteSample), so two Create
// calls racing for the store's single writer connection (store.go:
// writer.SetMaxOpenConns(1)) can interleave between them. That window is
// otherwise unreachable in a single-process, timing-independent test; this
// hook lets item_test.go force it open deliberately, proving the
// collision-retry path (below) actually runs rather than merely compiling.
// Production leaves this nil.
var itemCreateAfterReadNewestHook func()

// itemIsUniquePublishedAtViolation mirrors internal/store/samples.go's
// unexported isUniquePublishedAtViolation (duplicated here for the same
// off-limits-to-edit reason as itemSelectColumns above).
func itemIsUniquePublishedAtViolation(err error) bool {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	return sqliteErr.Code() == sqlitelib.SQLITE_CONSTRAINT_UNIQUE
}

// --- proto <-> model conversions ----------------------------------------

var itemOriginToProtoMap = map[model.Origin]affv1.Origin{
	model.OriginGenerated:  affv1.Origin_ORIGIN_GENERATED,
	model.OriginSampled:    affv1.Origin_ORIGIN_SAMPLED,
	model.OriginManual:     affv1.Origin_ORIGIN_MANUAL,
	model.OriginCorrection: affv1.Origin_ORIGIN_CORRECTION,
}

func itemOriginToProto(o model.Origin) affv1.Origin {
	if p, ok := itemOriginToProtoMap[o]; ok {
		return p
	}
	return affv1.Origin_ORIGIN_UNSPECIFIED
}

var itemOriginToModelMap = map[affv1.Origin]model.Origin{
	affv1.Origin_ORIGIN_GENERATED:  model.OriginGenerated,
	affv1.Origin_ORIGIN_SAMPLED:    model.OriginSampled,
	affv1.Origin_ORIGIN_MANUAL:     model.OriginManual,
	affv1.Origin_ORIGIN_CORRECTION: model.OriginCorrection,
}

// itemOriginToModelString returns "" for ORIGIN_UNSPECIFIED (and any unknown
// value), which List treats as "no origin filter" rather than a literal
// (impossible) column match.
func itemOriginToModelString(o affv1.Origin) string {
	if m, ok := itemOriginToModelMap[o]; ok {
		return string(m)
	}
	return ""
}

func itemTimestampOrNil(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

func itemToProto(it model.Item) *affv1.Item {
	return &affv1.Item{
		Id:          it.ID,
		FeedId:      it.FeedID,
		ItemKey:     it.ItemKey,
		Title:       it.Title,
		SummaryText: it.SummaryText,
		BodyHtml:    it.BodyHTML,
		AnswerHtml:  it.AnswerHTML,
		Link:        it.Link,
		SourceName:  it.SourceName,
		Tags:        it.Tags,
		Origin:      itemOriginToProto(it.Origin),
		PublishedAt: itemTimestampOrNil(it.PublishedAt),
		CreatedAt:   itemTimestampOrNil(it.CreatedAt),
		UpdatedAt:   itemTimestampOrNil(it.UpdatedAt),
		EditedAt:    itemTimestampOrNil(it.EditedAt),
		DeletedAt:   itemTimestampOrNil(it.DeletedAt),
		Version:     it.Version,
	}
}

// --- pagination -----------------------------------------------------------

const (
	itemDefaultPageSize = 50
	itemMaxPageSize     = 200
)

// itemEncodePageToken/itemDecodePageToken make List's cursor opaque to the
// client (PLAN.md §11) while internally being the (published_at, id) pair of
// the last row returned. A tuple, not just id, because List orders by
// published_at DESC (the same "newest first" order Slack's bookmark and the
// renderer use, PLAN.md §5.5) and id alone would not reproduce that order
// when two items share a published_at instant — which UNIQUE(feed_id,
// published_at) forbids within one feed but List spans every feed.
func itemEncodePageToken(publishedAt time.Time, id int64) string {
	raw := itemFormatTime(publishedAt) + "|" + strconv.FormatInt(id, 10)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func itemDecodePageToken(tok string) (time.Time, int64, error) {
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("rpc: decoding page_token: %w", err)
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, 0, fmt.Errorf("rpc: malformed page_token")
	}
	t, err := itemParseTime(parts[0])
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("rpc: page_token published_at: %w", err)
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("rpc: page_token id: %w", err)
	}
	return t, id, nil
}

// itemVersionConflictErr distinguishes "no such item" from "version
// mismatch" for a mutation whose UPDATE affected zero rows, by re-checking
// existence outside the hot path (mirrors internal/store/items.go's
// ErrVersionConflict semantics, but as a gRPC status since this package owns
// the RPC-facing error taxonomy that store.go does not).
func itemVersionConflictErr(ctx context.Context, db itemQuerier, id, expectedVersion int64) error {
	var exists int
	err := db.QueryRowContext(ctx, `SELECT 1 FROM items WHERE id = ?`, id).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return status.Errorf(codes.NotFound, "rpc: item %d not found", id)
	}
	if err != nil {
		return status.Errorf(codes.Internal, "rpc: checking existence of item %d: %v", id, err)
	}
	return status.Errorf(codes.FailedPrecondition,
		"rpc: item %d: expected_version %d does not match the current version", id, expectedVersion)
}

// --- ItemServer -------------------------------------------------------------

// ItemServer implements affv1.ItemServiceServer.
type ItemServer struct {
	affv1.UnimplementedItemServiceServer

	st  *store.Store
	inv publish.Invalidator
	ids ids.Source

	// now is the clock every mutation stamps with. A field, not a bare
	// time.Now call, so item_test.go can pin it — deterministic
	// "strictly later than the feed's newest item" assertions would
	// otherwise race the real wall clock on a slow CI runner.
	now func() time.Time

	// editMu/lastEditAt make the timestamp an EDIT is stamped with strictly
	// increasing. See editStamp.
	editMu     sync.Mutex
	lastEditAt time.Time
}

// editStamp is the timestamp an edit's item_revisions rows are written
// under, guaranteed to be strictly later than the previous edit's.
//
// The `at` column is the GROUP key: item_revisions holds one row per changed
// FIELD, and everything that reads history (ListRevisions, RevertRevision,
// the pagination cursor) treats rows sharing an `at` as one edit. So two
// edits landing on the same timestamp are not a cosmetic collision — they
// merge into a single history entry, and reverting one reverts both.
//
// time.Now() is not fine-grained enough to prevent that on its own. Windows'
// default timer resolution is ~15ms, so an edit and an immediate revert
// (exactly the sequence §12.4's "revert is an ordinary edit" produces) read
// the same wall clock and land in the same group. That is what made
// internal/e2e's TestItemRevisions and internal/rpc's own revert test flake:
// not a test-timing problem, a real one — an operator clicking revert right
// after an edit loses the audit trail of both.
//
// Nudging by a nanosecond is the smallest correct fix: it keeps the existing
// group-by-`at` model and every query built on it, and RFC3339Nano already
// stores that precision. The stamp stays within a nanosecond of the true
// wall clock even under a burst.
//
// # Why the last digit is forced non-zero
//
// These timestamps are stored as TEXT and ordered with SQL string operators,
// and time.RFC3339Nano REMOVES TRAILING ZEROS from the fraction. So a whole
// second formats as "…04:00:00Z" while one nanosecond later formats as
// "…04:00:00.000000001Z" — and '.' (0x2E) sorts before 'Z' (0x5A), which
// puts the LATER instant first. Ordering by such a column is only correct
// when every value has the same fractional width.
//
// Forcing the nanosecond component to a non-multiple of ten guarantees
// RFC3339Nano prints all nine digits, so every value this function produces
// is fixed-width and string order equals chronological order. It fixes
// ordering only for the values written HERE; the same trap exists anywhere
// else a store-written RFC3339Nano column is compared as text, which is
// worth knowing about but is not this function's to fix.
func (s *ItemServer) editStamp() time.Time {
	s.editMu.Lock()
	defer s.editMu.Unlock()
	at := s.now().UTC()
	if !at.After(s.lastEditAt) {
		at = s.lastEditAt.Add(time.Nanosecond)
	}
	if at.Nanosecond()%10 == 0 {
		at = at.Add(time.Nanosecond)
	}
	s.lastEditAt = at
	return at
}

var _ affv1.ItemServiceServer = (*ItemServer)(nil)

// NewItemServer wires an ItemServer against an open store, an Invalidator
// for the publish plane's render cache (PLAN.md §11's "every mutation ...
// invalidates that feed's render cache"), and an ids.Source for minting
// item_key values that must NEVER be derived from content (PLAN.md §5.1).
func NewItemServer(st *store.Store, inv publish.Invalidator, idSource ids.Source) *ItemServer {
	return &ItemServer{st: st, inv: inv, ids: idSource, now: time.Now}
}

// invalidate calls the injected Invalidator for slug. A nil Invalidator is
// tolerated (useful for tests exercising everything except cache behavior)
// rather than panicking a caller that has not wired one up yet.
func (s *ItemServer) invalidate(ctx context.Context, slug string) {
	if s.inv == nil || slug == "" {
		return
	}
	s.inv.InvalidateFeed(slug)

	// ...and every aggregate that lists this feed as a member.
	//
	// An aggregate's documents are cached under the AGGREGATE's slug, so the
	// call above reaches none of them — publish.Invalidator's own doc comment
	// makes the fan-out the caller's responsibility. FeedServer already did
	// this for recipe edits; item writes did not, so publishing, editing,
	// deleting or correcting an item left every aggregate containing the feed
	// serving whatever it had cached, indefinitely (there is no TTL on a
	// cache entry).
	//
	// Best-effort, and deliberately not escalated to an RPC error: the write
	// has already committed, and a missed invalidation self-heals on the next
	// write or restart — the identical argument feedInvalidate makes for its
	// own copy of this fan-out.
	slugs, err := s.st.AggregateSlugsFor(ctx, slug)
	if err != nil {
		return
	}
	for _, agg := range slugs {
		s.inv.InvalidateFeed(agg)
	}
}

// List supports FTS5 search plus filters on feed, origin, published_at range
// and deleted state, newest-first, paginated with an opaque cursor
// (PLAN.md §12.4).
func (s *ItemServer) List(ctx context.Context, req *affv1.ItemServiceListRequest) (*affv1.ItemServiceListResponse, error) {
	pageSize := int(req.GetPageSize())
	if pageSize <= 0 {
		pageSize = itemDefaultPageSize
	}
	if pageSize > itemMaxPageSize {
		pageSize = itemMaxPageSize
	}

	var (
		where []string
		args  []any
	)
	if fid := req.GetFeedId(); fid != 0 {
		where = append(where, "feed_id = ?")
		args = append(args, fid)
	}
	if o := itemOriginToModelString(req.GetOrigin()); o != "" {
		where = append(where, "origin = ?")
		args = append(args, o)
	}
	if req.GetPublishedAfter() != nil {
		where = append(where, "published_at > ?")
		args = append(args, itemFormatTime(req.GetPublishedAfter().AsTime()))
	}
	if req.GetPublishedBefore() != nil {
		where = append(where, "published_at < ?")
		args = append(args, itemFormatTime(req.GetPublishedBefore().AsTime()))
	}
	switch req.GetDeletedFilter() {
	case affv1.DeletedFilter_DELETED_FILTER_ONLY_DELETED:
		where = append(where, "deleted_at IS NOT NULL")
	case affv1.DeletedFilter_DELETED_FILTER_INCLUDE_ALL:
		// No filter: every row, deleted or not.
	default: // UNSPECIFIED and EXCLUDE_DELETED both default to hiding tombstones.
		where = append(where, "deleted_at IS NULL")
	}
	if match := itemFTSQuery(req.GetQuery()); match != "" {
		// items_fts is external-content FTS5 keyed by rowid == items.id
		// (migrations/0003_fts.sql), kept in sync by triggers — a plain join
		// on rowid is enough, no denormalized copy to go stale.
		//
		// The query is BUILT from what was typed, not passed through: see
		// itemFTSQuery for why (prefix matching, and keeping FTS5's operator
		// syntax out of a plain text box). A search that reduces to nothing
		// searchable applies no filter at all.
		where = append(where, "id IN (SELECT rowid FROM items_fts WHERE items_fts MATCH ?)")
		args = append(args, match)
	}
	// Snapshotted BEFORE the cursor condition is added: total_count answers
	// "how many match these filters", and folding the cursor in would count
	// only what is left after the current page, so the total would shrink as
	// the operator pages forward and "page 3 of 9" would become "page 3 of 6".
	filterWhere := append([]string(nil), where...)
	filterArgs := append([]any(nil), args...)

	if tok := req.GetPageToken(); tok != "" {
		afterPub, afterID, err := itemDecodePageToken(tok)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "rpc: invalid page_token")
		}
		where = append(where, "(published_at < ? OR (published_at = ? AND id < ?))")
		args = append(args, itemFormatTime(afterPub), itemFormatTime(afterPub), afterID)
	}

	total, err := itemCountMatching(ctx, s.st.Reader(), filterWhere, filterArgs)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: counting items: %v", err)
	}

	query := `SELECT ` + itemSelectColumns + ` FROM items`
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, " AND ")
	}
	query += ` ORDER BY published_at DESC, id DESC LIMIT ?`
	args = append(args, pageSize+1) // +1 to detect a next page without a COUNT(*)

	rows, err := s.st.Reader().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: listing items: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var items []model.Item
	for rows.Next() {
		it, err := itemScanRow(rows)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "rpc: scanning item: %v", err)
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: iterating items: %v", err)
	}

	resp := &affv1.ItemServiceListResponse{TotalCount: total}
	if len(items) > pageSize {
		last := items[pageSize-1]
		resp.NextPageToken = itemEncodePageToken(last.PublishedAt, last.ID)
		items = items[:pageSize]
	}
	resp.Items = make([]*affv1.Item, len(items))
	for i, it := range items {
		resp.Items[i] = itemToProto(it)
	}
	return resp, nil
}

// Get returns one item by numeric id, deleted or not — this is the
// admin/back-office lookup, not the publish plane's route (which 410s a
// soft-deleted item instead of returning it, PLAN.md §12.4).
func (s *ItemServer) Get(ctx context.Context, req *affv1.ItemServiceGetRequest) (*affv1.ItemServiceGetResponse, error) {
	it, err := itemLoadByID(ctx, s.st.Reader(), req.GetId())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "rpc: item %d not found", req.GetId())
		}
		return nil, status.Errorf(codes.Internal, "rpc: getting item %d: %v", req.GetId(), err)
	}
	return &affv1.ItemServiceGetResponse{Item: itemToProto(it)}, nil
}

// Create hand-authors an item with origin forced to ORIGIN_MANUAL regardless
// of what the caller set (PLAN.md §12.4, and the proto doc on
// ItemServiceCreateRequest). id, item_key, version and timestamps are
// server-assigned. Tags are accepted but not persisted — there is no tags
// column yet (migrations/0002_feeds_items.sql), matching store/items.go's
// identical, already-documented gap.
func (s *ItemServer) Create(ctx context.Context, req *affv1.ItemServiceCreateRequest) (*affv1.ItemServiceCreateResponse, error) {
	in := req.GetItem()
	if in == nil {
		return nil, status.Error(codes.InvalidArgument, "rpc: item is required")
	}
	feedID := in.GetFeedId()
	if feedID == 0 {
		return nil, status.Error(codes.InvalidArgument, "rpc: item.feed_id is required")
	}
	title := strings.TrimSpace(in.GetTitle())
	if title == "" {
		return nil, status.Error(codes.InvalidArgument, "rpc: item.title is required")
	}

	db := s.st.Writer()
	slug, err := itemFeedSlug(ctx, db, feedID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "rpc: feed %d not found", feedID)
		}
		return nil, status.Errorf(codes.Internal, "rpc: resolving feed %d: %v", feedID, err)
	}

	now := s.now()
	published := now
	if ts := in.GetPublishedAt(); ts != nil {
		published = ts.AsTime()
	}
	// Truncated to whole seconds BEFORE any comparison: RFC 822 pubDate
	// (internal/render's RFC822, §5.1) has one-second resolution, and
	// items.published_at's UNIQUE(feed_id, published_at) constraint only
	// actually prevents two items sharing a rendered pubDate (§5.5 — the
	// invisible-forever failure Slack's bookmark model produces) if what
	// gets compared and stored is at that same granularity. Mirrors
	// PublishCorrection's identical truncation below and
	// store.truncatePublishedAt (internal/store/items.go), which now also
	// truncates on write as a second line of defense.
	published = published.Truncate(time.Second)
	if err := itemValidatePublishedAt(published); err != nil {
		return nil, err
	}

	newest, err := itemNewestPublished(ctx, db, feedID, 0)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: %v", err)
	}
	// Also truncated: a legacy row written before this fix existed (or a
	// row written directly by internal/store/runs.go's CommitRun, which
	// this package does not own) could still carry a sub-second
	// published_at. Comparing at second granularity on both sides keeps
	// "strictly after" meaning what it says once rendered, regardless of
	// what precision the newest existing row happens to be stored at.
	newest = newest.Truncate(time.Second)
	if err := itemRejectBackdated(published, newest, slug); err != nil {
		return nil, err
	}

	if itemCreateAfterReadNewestHook != nil {
		itemCreateAfterReadNewestHook()
	}

	// Sanitized before the content hash is computed, so the hash identifies
	// what is actually STORED and rendered rather than what was submitted —
	// otherwise two submissions differing only in markup this package strips
	// would hash differently while producing byte-identical published items.
	bodyHTML := itemSanitizeHTML(in.GetBodyHtml())
	answerHTML := itemSanitizeHTML(in.GetAnswerHtml())

	it := model.Item{
		FeedID:      feedID,
		ItemKey:     s.ids.NewItemKey(now),
		ContentHash: itemContentHash(feedID, title, in.GetSummaryText(), bodyHTML, in.GetLink()),
		Title:       title,
		SummaryText: in.GetSummaryText(),
		BodyHTML:    bodyHTML,
		AnswerHTML:  answerHTML,
		Link:        in.GetLink(),
		SourceName:  in.GetSourceName(),
		Origin:      model.OriginManual,
	}

	// Retried at +1 second on a UNIQUE(feed_id, published_at) collision,
	// exactly like PromoteSample (internal/store/samples.go) and
	// PublishCorrection below: the reads above happen outside a
	// transaction, so two Create calls that both read the same "newest"
	// before either has inserted can both compute the same truncated
	// stamp and both pass itemRejectBackdated — a real race, not a caller
	// mistake, and truncating to one-second buckets makes that race far
	// more likely to actually land in the same bucket than it was at
	// nanosecond precision. Goes through the SAME collision-retry shape
	// as those two call sites rather than a second mechanism beside it.
	var id int64
	for attempt := 0; ; attempt++ {
		it.PublishedAt = published
		id, err = s.st.InsertItem(ctx, it)
		if err == nil {
			break
		}
		if itemIsUniquePublishedAtViolation(err) && attempt < itemTimestampRetries {
			published = published.Add(time.Second)
			continue
		}
		return nil, status.Errorf(codes.Internal, "rpc: creating item: %v", err)
	}

	s.invalidate(ctx, slug)

	out, err := itemLoadByID(ctx, db, id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: reloading created item %d: %v", id, err)
	}
	return &affv1.ItemServiceCreateResponse{Item: itemToProto(out)}, nil
}

// itemRevisionEdit is one item_revisions row to write for an Update.
type itemRevisionEdit struct {
	field    string
	oldValue any
	newValue any
}

// itemDiff reports only the fields that actually changed between old and
// updated, so an Update that leaves a field untouched does not manufacture a
// no-op revision row (which would make the diff view in PLAN.md §12.4 lie
// about what an admin actually changed).
func itemDiff(old, updated model.Item) []itemRevisionEdit {
	var out []itemRevisionEdit
	addText := func(field, o, n string) {
		if o != n {
			out = append(out, itemRevisionEdit{field: field, oldValue: itemNullString(o), newValue: itemNullString(n)})
		}
	}
	addText("title", old.Title, updated.Title)
	addText("summary_text", old.SummaryText, updated.SummaryText)
	addText("body_html", old.BodyHTML, updated.BodyHTML)
	addText("answer_html", old.AnswerHTML, updated.AnswerHTML)
	addText("link", old.Link, updated.Link)
	addText("source_name", old.SourceName, updated.SourceName)
	if !old.PublishedAt.Equal(updated.PublishedAt) {
		out = append(out, itemRevisionEdit{
			field: "published_at", oldValue: itemFormatTime(old.PublishedAt), newValue: itemFormatTime(updated.PublishedAt),
		})
	}
	return out
}

// itemApplyEdit is the shared commit path for every write that changes an
// item's title/summary/body/answer/link/source/published_at under optimistic
// concurrency: it UPDATEs the row (item_key and feed_id excluded from the SET
// list by construction, not by a runtime check — PLAN.md §5.1) and records
// item_revisions rows for whatever itemDiff(old, updated) finds changed, all
// in one transaction, so a revision row can never exist for an edit that did
// not actually commit or vice versa.
//
// Update and RevertRevision both call this. That sharing is deliberate and
// load-bearing: RevertRevision (PLAN.md §12.4) is defined as "an ordinary
// edit, not a rewind," and the only way to make that true rather than
// merely asserted is for it to run through literally the same commit path
// as a hand-typed edit — same version check, same guid-preserving column
// list, same revision bookkeeping.
func (s *ItemServer) itemApplyEdit(ctx context.Context, db *sql.DB, old, updated model.Item, expectedVersion int64, now time.Time) (model.Item, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return model.Item{}, status.Errorf(codes.Internal, "rpc: begin edit item %d: %v", old.ID, err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
		UPDATE items
		SET title = ?, summary_text = ?, body_html = ?, answer_html = ?, link = ?, source_name = ?,
		    published_at = ?, edited_at = ?, updated_at = ?, version = version + 1
		WHERE id = ? AND version = ?`,
		updated.Title, updated.SummaryText, updated.BodyHTML, itemNullString(updated.AnswerHTML),
		itemNullString(updated.Link), itemNullString(updated.SourceName),
		itemFormatTime(updated.PublishedAt), itemFormatTime(now), itemFormatTime(now),
		old.ID, expectedVersion)
	if err != nil {
		return model.Item{}, status.Errorf(codes.Internal, "rpc: updating item %d: %v", old.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return model.Item{}, status.Errorf(codes.Internal, "rpc: rows affected updating item %d: %v", old.ID, err)
	}
	if n == 0 {
		// Deliberately queries through tx, not db: db is the underlying
		// *sql.DB, which SQLite's config (store.go: SetMaxOpenConns(1)) limits
		// to a single connection — the one this still-open transaction is
		// holding. Querying db here would block forever waiting for a
		// connection tx will not release until this function returns and its
		// deferred Rollback runs, which can never happen while this call is
		// itself blocked: a self-deadlock. tx satisfies itemQuerier and reuses
		// the same connection instead.
		return model.Item{}, itemVersionConflictErr(ctx, tx, old.ID, expectedVersion)
	}

	for _, rev := range itemDiff(old, updated) {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO item_revisions (item_id, at, field, old_value, new_value) VALUES (?, ?, ?, ?, ?)`,
			old.ID, itemFormatTime(now), rev.field, rev.oldValue, rev.newValue,
		); err != nil {
			return model.Item{}, status.Errorf(codes.Internal, "rpc: recording revision for item %d: %v", old.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return model.Item{}, status.Errorf(codes.Internal, "rpc: commit edit %d: %v", old.ID, err)
	}

	out, err := itemLoadByID(ctx, db, old.ID)
	if err != nil {
		return model.Item{}, status.Errorf(codes.Internal, "rpc: reloading item %d: %v", old.ID, err)
	}
	return out, nil
}

// Update edits title/summary/body/answer/link/source/published_at under
// optimistic concurrency, records the change in item_revisions, and bumps
// updated_at, via itemApplyEdit.
//
// item.ItemKey and item.FeedId on the request are READ BUT NEVER WRITTEN:
// item_key is immutable by construction (PLAN.md §5.1 — the guid derives
// from it and must outlive an edit or the item resurfaces as a duplicate in
// every subscriber's inbox), and there is no reparenting an item to a
// different feed via Update. A test proves the guid is byte-identical
// before and after.
func (s *ItemServer) Update(ctx context.Context, req *affv1.ItemServiceUpdateRequest) (*affv1.ItemServiceUpdateResponse, error) {
	in := req.GetItem()
	if in == nil {
		return nil, status.Error(codes.InvalidArgument, "rpc: item is required")
	}
	id := in.GetId()
	if id == 0 {
		return nil, status.Error(codes.InvalidArgument, "rpc: item.id is required")
	}
	title := strings.TrimSpace(in.GetTitle())
	if title == "" {
		return nil, status.Error(codes.InvalidArgument, "rpc: item.title is required")
	}

	db := s.st.Writer()
	old, err := itemLoadByID(ctx, db, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "rpc: item %d not found", id)
		}
		return nil, status.Errorf(codes.Internal, "rpc: loading item %d: %v", id, err)
	}

	newPublished := old.PublishedAt
	if ts := in.GetPublishedAt(); ts != nil {
		// Truncated to whole seconds for the same reason Create truncates
		// (RFC 822 pubDate has one-second resolution, §5.1/§5.5) — see the
		// comment there.
		candidate := ts.AsTime().Truncate(time.Second)
		// Validated only when the caller actively supplies a value, not
		// unconditionally on newPublished: an Update that leaves
		// published_at untouched must still be able to fix an unrelated
		// field (title, body) on a row whose published_at predates this
		// check, rather than getting permanently stuck because of a value
		// the caller never asked to change.
		if err := itemValidatePublishedAt(candidate); err != nil {
			return nil, err
		}
		// Compared against old.PublishedAt TRUNCATED, not old.PublishedAt
		// itself: a row written before this fix existed (or by
		// internal/store/runs.go's CommitRun, which this package does not
		// own and which does not truncate) can still carry a sub-second
		// published_at. A caller that round-trips that exact value back in
		// — e.g. an edit that only touches title/body, PLAN.md §12.4's
		// "an edit is not a rewind" pattern cmd/affseed/main.go's
		// reviseNewsItem relies on — must see that as the no-op it is, not
		// as "moved earlier" once truncation changes what the two sides
		// compare equal to. Only actually adopt the truncated candidate
		// (and require it to be strictly after the feed's newest) when the
		// caller asked for a genuinely different second; a legacy
		// sub-second row that nothing ever asks to change is left exactly
		// as it was written, not silently rewritten out from under an
		// unrelated edit.
		if !candidate.Equal(old.PublishedAt.Truncate(time.Second)) {
			// Excludes this item's own row from "the feed's current
			// newest" — otherwise a no-op re-save of the newest item in a
			// feed would be compared against itself and always rejected
			// (RULE-7 is about backdating relative to OTHER items, not a
			// stable timestamp).
			newest, err := itemNewestPublished(ctx, db, old.FeedID, old.ID)
			if err != nil {
				return nil, status.Errorf(codes.Internal, "rpc: %v", err)
			}
			// Truncated for the same legacy-row reason above.
			newest = newest.Truncate(time.Second)
			if err := itemRejectBackdated(candidate, newest, strconv.FormatInt(old.FeedID, 10)); err != nil {
				return nil, err
			}
			newPublished = candidate
		}
	}

	updated := old
	updated.Title = title
	updated.SummaryText = in.GetSummaryText()
	updated.BodyHTML = itemSanitizeHTML(in.GetBodyHtml())
	updated.AnswerHTML = itemSanitizeHTML(in.GetAnswerHtml())
	updated.Link = in.GetLink()
	updated.SourceName = in.GetSourceName()
	updated.PublishedAt = newPublished
	// updated.ItemKey and updated.FeedID stay old's — see doc comment above.

	out, err := s.itemApplyEdit(ctx, db, old, updated, req.GetExpectedVersion(), s.editStamp())
	if err != nil {
		return nil, err
	}

	if slug, serr := itemFeedSlug(ctx, db, old.FeedID); serr == nil {
		s.invalidate(ctx, slug)
	}

	return &affv1.ItemServiceUpdateResponse{Item: itemToProto(out)}, nil
}

// Delete is SOFT only (PLAN.md §12.4): the item leaves the feed window, its
// permalink returns 410 Gone, and the guid is never reused. There is no hard
// delete method on this server — see the file header.
func (s *ItemServer) Delete(ctx context.Context, req *affv1.ItemServiceDeleteRequest) (*affv1.ItemServiceDeleteResponse, error) {
	id := req.GetItemId()
	db := s.st.Writer()
	old, err := itemLoadByID(ctx, db, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "rpc: item %d not found", id)
		}
		return nil, status.Errorf(codes.Internal, "rpc: loading item %d: %v", id, err)
	}

	now := itemFormatTime(s.now())
	res, err := db.ExecContext(ctx,
		`UPDATE items SET deleted_at = ?, updated_at = ?, version = version + 1 WHERE id = ? AND version = ?`,
		now, now, id, req.GetExpectedVersion())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: soft-deleting item %d: %v", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: rows affected soft-deleting item %d: %v", id, err)
	}
	if n == 0 {
		return nil, itemVersionConflictErr(ctx, db, id, req.GetExpectedVersion())
	}

	if slug, serr := itemFeedSlug(ctx, db, old.FeedID); serr == nil {
		s.invalidate(ctx, slug)
	}
	return &affv1.ItemServiceDeleteResponse{}, nil
}

// Restore reverses a soft delete. The item_key and guid never moved — this
// only clears deleted_at, mirroring internal/store/items.go's RestoreItem,
// plus the expected_version check that function does not have.
func (s *ItemServer) Restore(ctx context.Context, req *affv1.ItemServiceRestoreRequest) (*affv1.ItemServiceRestoreResponse, error) {
	id := req.GetItemId()
	db := s.st.Writer()
	old, err := itemLoadByID(ctx, db, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "rpc: item %d not found", id)
		}
		return nil, status.Errorf(codes.Internal, "rpc: loading item %d: %v", id, err)
	}

	now := itemFormatTime(s.now())
	res, err := db.ExecContext(ctx,
		`UPDATE items SET deleted_at = NULL, updated_at = ?, version = version + 1 WHERE id = ? AND version = ?`,
		now, id, req.GetExpectedVersion())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: restoring item %d: %v", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: rows affected restoring item %d: %v", id, err)
	}
	if n == 0 {
		return nil, itemVersionConflictErr(ctx, db, id, req.GetExpectedVersion())
	}

	if slug, serr := itemFeedSlug(ctx, db, old.FeedID); serr == nil {
		s.invalidate(ctx, slug)
	}

	out, err := itemLoadByID(ctx, db, id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: reloading item %d: %v", id, err)
	}
	return &affv1.ItemServiceRestoreResponse{Item: itemToProto(out)}, nil
}

// itemFindCandidate resolves one candidate out of a samples row's
// payload_json.
//
// It decodes into the GENERATED type, deliberately. This file previously
// carried a hand-written mirror of the candidate struct, on the assumption
// that the payload used protojson field names (`candidateId`). It does not:
// sample.go writes the payload with encoding/json over
// []*affv1.SampleCandidate, and encoding/json honours the generated
// `json:"candidate_id,omitempty"` tag rather than the protobuf `json=`
// alias. Every field matched by coincidence except the one that mattered, so
// the lookup always fell through and PromoteSample could not resolve any
// candidate a real Sample call had produced — the whole promote journey (§22
// J4) was dead, and it failed with "not present in sample payload", which
// reads like missing data rather than a decode mismatch.
//
// Sharing the generated type is what actually fixes it. Two hand-maintained
// definitions of one wire format will drift again; there is now one.
func itemFindCandidate(payload []byte, candidateID string) (*affv1.SampleCandidate, error) {
	var list []*affv1.SampleCandidate
	if err := json.Unmarshal(payload, &list); err != nil {
		var wrapper struct {
			Candidates []*affv1.SampleCandidate `json:"candidates"`
		}
		if werr := json.Unmarshal(payload, &wrapper); werr != nil {
			return nil, fmt.Errorf("parsing sample payload: %w", err)
		}
		list = wrapper.Candidates
	}
	for _, c := range list {
		if c.GetCandidateId() == candidateID {
			return c, nil
		}
	}
	return nil, fmt.Errorf("candidate %q not present in sample payload", candidateID)
}

// PromoteSample persists a chosen sample candidate as a real item, origin =
// ORIGIN_SAMPLED, stamped now (PLAN.md §12.3). No expected_version: this
// creates a new item rather than mutating an existing one.
//
// The heavy lifting — UNIQUE(feed_id, published_at) collision retry, and
// inserting the item plus discarding the sample atomically — is
// store.Store.PromoteSample (internal/store/samples.go); this method only
// resolves the candidate and the feed's slug. The slug MUST be resolved
// BEFORE calling store.PromoteSample: that call deletes the samples row in
// the same transaction that inserts the item, so by the time it returns
// successfully the sample's feed_id is no longer reachable through it
// (mirrors internal/store/hooks.go's Hooked.PromoteSample wrapper, which
// faces the identical ordering constraint).
func (s *ItemServer) PromoteSample(ctx context.Context, req *affv1.ItemServicePromoteSampleRequest) (*affv1.ItemServicePromoteSampleResponse, error) {
	sampleID, err := strconv.ParseInt(req.GetSampleId(), 10, 64)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "rpc: sample_id %q is not a valid id", req.GetSampleId())
	}

	db := s.st.Writer()
	var feedID int64
	if err := db.QueryRowContext(ctx, `SELECT feed_id FROM samples WHERE id = ?`, sampleID).Scan(&feedID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, status.Errorf(codes.NotFound, "rpc: sample %d not found", sampleID)
		}
		return nil, status.Errorf(codes.Internal, "rpc: reading sample %d: %v", sampleID, err)
	}
	slug, err := itemFeedSlug(ctx, db, feedID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: resolving feed %d for sample %d: %v", feedID, sampleID, err)
	}

	sm, err := s.st.GetSample(ctx, sampleID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "rpc: sample %d not found or expired", sampleID)
		}
		return nil, status.Errorf(codes.Internal, "rpc: getting sample %d: %v", sampleID, err)
	}
	cand, err := itemFindCandidate(sm.Payload, req.GetCandidateId())
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "rpc: %v", err)
	}

	// Re-sanitized even though a candidate reached the sample payload through
	// internal/generate's own sanitize pass: see itemSanitizeHTML for why this
	// path does not get to inherit that, and why a second (idempotent) pass is
	// the price of a boundary that holds no matter how the value arrived.
	candBodyHTML := itemSanitizeHTML(cand.GetBodyHtml())
	candAnswerHTML := itemSanitizeHTML(cand.GetAnswerHtml())

	now := s.now()
	it := model.Item{
		ItemKey:     s.ids.NewItemKey(now),
		ContentHash: itemContentHash(feedID, cand.GetTitle(), cand.GetSummaryText(), candBodyHTML, cand.GetLink()),
		Title:       cand.GetTitle(),
		SummaryText: cand.GetSummaryText(),
		BodyHTML:    candBodyHTML,
		AnswerHTML:  candAnswerHTML,
		Link:        cand.GetLink(),
		SourceName:  cand.GetSourceName(),
		Origin:      model.OriginSampled,
	}

	itemID, err := s.st.PromoteSample(ctx, sampleID, it)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "rpc: sample %d not found, expired, or already promoted/discarded", sampleID)
		}
		return nil, status.Errorf(codes.Internal, "rpc: promoting sample %d: %v", sampleID, err)
	}

	s.invalidate(ctx, slug)

	out, err := itemLoadByID(ctx, db, itemID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: reloading promoted item %d: %v", itemID, err)
	}
	// PromoteSampleRequest has no published_at field (proto/aff/v1/
	// item.proto): the stamp is entirely internal to store.Store.PromoteSample
	// (internal/store/samples.go's promoteClock + the feed's stored newest),
	// which is off-limits to edit from this package and already committed by
	// the time we can see it. There is nothing here to reject BEFORE the
	// write, so this checks the committed result instead: if it is somehow
	// out of range (a corrupted "newest" upstream of the stamp, a future
	// store regression), returning Internal rather than silently reporting
	// success at least surfaces the inconsistency to the caller instead of
	// producing an item that later fails to render/parse with no warning
	// anyone saw at write time.
	if verr := itemValidatePublishedAt(out.PublishedAt); verr != nil {
		return nil, status.Errorf(codes.Internal,
			"rpc: promoted item %d committed with published_at %s outside the range every feed format can render — this indicates upstream data corruption (e.g. the feed's stored newest published_at), not a caller mistake: %v",
			itemID, out.PublishedAt.UTC().Format(time.RFC3339Nano), verr)
	}
	return &affv1.ItemServicePromoteSampleResponse{Item: itemToProto(out)}, nil
}

// itemInsertCorrectionOnce is one attempt at PublishCorrection's atomic pair:
// insert the correction item, then link it via `corrections`, both in ONE
// transaction — mirrors internal/store/samples.go's promoteOnce, including
// opening a fresh transaction per call so a UNIQUE(feed_id, published_at)
// collision aborts cleanly via the deferred Rollback and the caller can retry
// at stamp+1s with a clean slate.
func itemInsertCorrectionOnce(
	ctx context.Context, db *sql.DB, feedID int64, key, hash, title, summary, body, link, source string,
	stamp, now time.Time, correctsID int64,
) (int64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO items (feed_id, item_key, content_hash, title, summary_text, body_html,
		                    link, source_name, published_at, origin, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		feedID, key, hash, title, summary, body,
		itemNullString(link), itemNullString(source), itemFormatTime(stamp), string(model.OriginCorrection),
		itemFormatTime(now), itemFormatTime(now))
	if err != nil {
		return 0, fmt.Errorf("inserting correction item: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("correction item id: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO corrections (item_id, corrects_item_id) VALUES (?, ?)`, id, correctsID,
	); err != nil {
		return 0, fmt.Errorf("linking correction: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return id, nil
}

// PublishCorrection creates a NEW item — a fresh ULID, a strictly later
// published_at than the feed's current newest, origin = ORIGIN_CORRECTION —
// and a corrections row linking it to the original. It does NOT modify the
// original item's row: its guid and published_at are untouched, which is the
// entire point (PLAN.md §12.4: "RSS has no retraction... this is the only
// mechanism that actually reaches subscribers"). No expected_version on the
// original, because nothing about the original's row changes.
func (s *ItemServer) PublishCorrection(ctx context.Context, req *affv1.ItemServicePublishCorrectionRequest) (*affv1.ItemServicePublishCorrectionResponse, error) {
	correctsID := req.GetCorrectsItemId()
	if correctsID == 0 {
		return nil, status.Error(codes.InvalidArgument, "rpc: corrects_item_id is required")
	}
	title := strings.TrimSpace(req.GetTitle())
	if title == "" {
		return nil, status.Error(codes.InvalidArgument, "rpc: title is required")
	}

	db := s.st.Writer()
	original, err := itemLoadByID(ctx, db, correctsID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "rpc: item %d not found", correctsID)
		}
		return nil, status.Errorf(codes.Internal, "rpc: loading item %d: %v", correctsID, err)
	}

	slug, err := itemFeedSlug(ctx, db, original.FeedID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: resolving feed %d: %v", original.FeedID, err)
	}

	correctionTitle := title
	if !strings.HasPrefix(correctionTitle, "Correction:") {
		correctionTitle = "Correction: " + correctionTitle
	}

	now := s.now()
	newest, err := itemNewestPublished(ctx, db, original.FeedID, 0)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: %v", err)
	}
	// Always stamped strictly after the feed's newest item — a correction
	// with a backdated published_at would be exactly the invisible-forever
	// mistake RULE-7 exists to prevent, and defeat the one mechanism PLAN.md
	// §12.4 says actually reaches subscribers.
	stamp := now.Truncate(time.Second)
	if !stamp.After(newest) {
		stamp = newest.Add(time.Second)
	}
	// PublishCorrection's request has no published_at field (proto/aff/v1/
	// item.proto) — stamp is derived entirely from s.now() and the feed's
	// stored newest, never from caller input. Validating it here still
	// matters: a corrupted "newest" already sitting in the database (e.g.
	// legacy data written before this check existed) would otherwise
	// silently propagate into this brand-new item via newest.Add(1s).
	if err := itemValidatePublishedAt(stamp); err != nil {
		return nil, err
	}

	key := s.ids.NewItemKey(now)
	// A correction is a brand-new published item built from caller-supplied
	// markup, so it goes through the same gate Create and Update do — it
	// reaches content:encoded and the permalink by exactly the same route.
	correctionBodyHTML := itemSanitizeHTML(req.GetBodyHtml())
	hash := itemContentHash(original.FeedID, correctionTitle, req.GetSummaryText(), correctionBodyHTML, original.Link)

	var itemID int64
	for attempt := 0; ; attempt++ {
		itemID, err = itemInsertCorrectionOnce(ctx, db, original.FeedID, key, hash, correctionTitle,
			req.GetSummaryText(), correctionBodyHTML, original.Link, original.SourceName, stamp, now, correctsID)
		if err == nil {
			break
		}
		if itemIsUniquePublishedAtViolation(err) && attempt < itemTimestampRetries {
			stamp = stamp.Add(time.Second)
			continue
		}
		return nil, status.Errorf(codes.Internal, "rpc: publishing correction for item %d: %v", correctsID, err)
	}

	s.invalidate(ctx, slug)

	out, err := itemLoadByID(ctx, db, itemID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: reloading correction item %d: %v", itemID, err)
	}
	return &affv1.ItemServicePublishCorrectionResponse{Item: itemToProto(out)}, nil
}

// --- revision history and revert -------------------------------------------
//
// item_revisions (migrations/0002_feeds_items.sql) is one row PER CHANGED
// FIELD, not one row per edit: itemDiff/itemApplyEdit above insert several
// rows sharing a single `at` for a single Update or RevertRevision call. The
// history UI wants one entry per edit with its fields bundled (PLAN.md
// §12.4: "enough per row to render a diff without a second call"), so
// everything below groups by `at` rather than exposing the raw rows.
//
// An ItemRevision's id is the smallest item_revisions.id in its group. That
// is stable forever because rows are only ever inserted, never updated or
// deleted (the whole "history that can be erased by the thing it audits is
// not history" property), which is what makes it safe to hand back out of
// ListRevisions and accept back into RevertRevision as an opaque identifier.

const (
	itemRevisionDefaultPageSize = 20
	itemRevisionMaxPageSize     = 100
)

// itemRevisionEncodePageToken/itemRevisionDecodePageToken make ListRevisions'
// cursor opaque (PLAN.md §11), mirroring itemEncodePageToken/
// itemDecodePageToken above but keyed on `at` alone: a revision GROUP is the
// pagination unit here, not a row, and every row in a group shares one `at`.
func itemRevisionEncodePageToken(at time.Time) string {
	return base64.RawURLEncoding.EncodeToString([]byte(itemFormatTime(at)))
}

func itemRevisionDecodePageToken(tok string) (time.Time, error) {
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		return time.Time{}, fmt.Errorf("rpc: decoding page_token: %w", err)
	}
	t, err := itemParseTime(string(raw))
	if err != nil {
		return time.Time{}, fmt.Errorf("rpc: page_token at: %w", err)
	}
	return t, nil
}

// itemLoadRevisionFields fetches every item_revisions row sharing `at` for
// item_id, ordered by id (i.e. the order itemDiff originally wrote them in).
func itemLoadRevisionFields(ctx context.Context, db itemQuerier, itemID int64, at string) (*affv1.ItemRevision, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, field, old_value, new_value FROM item_revisions WHERE item_id = ? AND at = ? ORDER BY id ASC`,
		itemID, at)
	if err != nil {
		return nil, fmt.Errorf("reading revision fields: %w", err)
	}
	defer func() { _ = rows.Close() }()

	rev := &affv1.ItemRevision{ItemId: itemID}
	first := true
	for rows.Next() {
		var (
			id                 int64
			field              string
			oldValue, newValue sql.NullString
		)
		if err := rows.Scan(&id, &field, &oldValue, &newValue); err != nil {
			return nil, fmt.Errorf("scanning revision field: %w", err)
		}
		if first {
			rev.Id = id // the group's identifier — see file-section doc comment above.
			first = false
		}
		rev.Changes = append(rev.Changes, &affv1.ItemRevisionChange{
			Field: field, OldValue: oldValue.String, NewValue: newValue.String,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating revision fields: %w", err)
	}
	if first {
		return nil, store.ErrNotFound // `at` matched no rows at all
	}
	atTime, err := itemParseTime(at)
	if err != nil {
		return nil, fmt.Errorf("parsing revision at %q: %w", at, err)
	}
	rev.At = timestamppb.New(atTime)
	return rev, nil
}

// ListRevisions returns item_id's edit history newest-first, one entry per
// edit, paginated by `at` so a page boundary can never split one edit's
// field changes across two pages (PLAN.md §12.4). An item with no recorded
// edits returns an empty list, not an error.
func (s *ItemServer) ListRevisions(ctx context.Context, req *affv1.ItemServiceListRevisionsRequest) (*affv1.ItemServiceListRevisionsResponse, error) {
	itemID := req.GetItemId()
	if itemID == 0 {
		return nil, status.Error(codes.InvalidArgument, "rpc: item_id is required")
	}
	if _, err := itemLoadByID(ctx, s.st.Reader(), itemID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "rpc: item %d not found", itemID)
		}
		return nil, status.Errorf(codes.Internal, "rpc: loading item %d: %v", itemID, err)
	}

	pageSize := int(req.GetPageSize())
	if pageSize <= 0 {
		pageSize = itemRevisionDefaultPageSize
	}
	if pageSize > itemRevisionMaxPageSize {
		pageSize = itemRevisionMaxPageSize
	}

	query := `SELECT DISTINCT at FROM item_revisions WHERE item_id = ?`
	args := []any{itemID}
	if tok := req.GetPageToken(); tok != "" {
		after, err := itemRevisionDecodePageToken(tok)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "rpc: invalid page_token")
		}
		query += ` AND at < ?`
		args = append(args, itemFormatTime(after))
	}
	query += ` ORDER BY at DESC LIMIT ?`
	args = append(args, pageSize+1) // +1 to detect a next page without a COUNT(*)

	rows, err := s.st.Reader().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: listing revisions for item %d: %v", itemID, err)
	}
	var ats []string
	for rows.Next() {
		var at string
		if err := rows.Scan(&at); err != nil {
			_ = rows.Close()
			return nil, status.Errorf(codes.Internal, "rpc: scanning revision timestamp for item %d: %v", itemID, err)
		}
		ats = append(ats, at)
	}
	rowsErr := rows.Err()
	_ = rows.Close()
	if rowsErr != nil {
		return nil, status.Errorf(codes.Internal, "rpc: iterating revisions for item %d: %v", itemID, rowsErr)
	}

	resp := &affv1.ItemServiceListRevisionsResponse{}
	if len(ats) > pageSize {
		last, err := itemParseTime(ats[pageSize-1])
		if err != nil {
			return nil, status.Errorf(codes.Internal, "rpc: parsing revision timestamp for item %d: %v", itemID, err)
		}
		resp.NextPageToken = itemRevisionEncodePageToken(last)
		ats = ats[:pageSize]
	}

	for _, at := range ats {
		rev, err := itemLoadRevisionFields(ctx, s.st.Reader(), itemID, at)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "rpc: loading revision fields for item %d: %v", itemID, err)
		}
		resp.Revisions = append(resp.Revisions, rev)
	}
	return resp, nil
}

// itemRevisionGroupAt resolves revisionID to the `at` value its group shares,
// scoped to itemID so a revision id from a DIFFERENT item can never be
// applied here (the request only carries the numeric ids, not a foreign-key
// guarantee).
func itemRevisionGroupAt(ctx context.Context, db itemQuerier, itemID, revisionID int64) (string, error) {
	var at string
	err := db.QueryRowContext(ctx,
		`SELECT at FROM item_revisions WHERE id = ? AND item_id = ?`, revisionID, itemID,
	).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return "", store.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("reading revision %d: %w", revisionID, err)
	}
	return at, nil
}

// itemApplyRevisionField sets one field on target to a revision change's
// recorded old_value, reconstructing target's state immediately BEFORE that
// revision's edit was applied — which is the definition of "revert" (PLAN.md
// §12.4). published_at is stored and read back unconditionally (itemDiff
// never itemNullString's it); every other field follows items.go's
// NULL-means-"" convention, matching how itemDiff wrote it in the first
// place. item_key/feed_id are deliberately absent from this switch: no
// revision row is ever written for them (itemDiff never diffs either), so
// there is no field value here that could resurrect an old guid (PLAN.md
// §5.1) even by an unrecognized-field bug — an unknown field name errors
// instead of silently doing nothing.
func itemApplyRevisionField(target *model.Item, field, oldValue string) error {
	switch field {
	case "title":
		target.Title = oldValue
	case "summary_text":
		target.SummaryText = oldValue
	case "body_html":
		target.BodyHTML = oldValue
	case "answer_html":
		target.AnswerHTML = oldValue
	case "link":
		target.Link = oldValue
	case "source_name":
		target.SourceName = oldValue
	case "published_at":
		t, err := itemParseTime(oldValue)
		if err != nil {
			return fmt.Errorf("parsing published_at revision value %q: %w", oldValue, err)
		}
		target.PublishedAt = t
	default:
		return fmt.Errorf("unrecognized revision field %q", field)
	}
	return nil
}

// RevertRevision is an ORDINARY EDIT, not a rewind (PLAN.md §12.4): it
// reconstructs the item's state immediately before the chosen revision's
// edit (by applying each of that revision's recorded old_values) and commits
// that through itemApplyEdit — the exact same path Update uses. That commit
// writes a BRAND-NEW set of item_revisions rows recording the revert as its
// own edit; nothing here deletes or rewrites the reverted-past revision or
// any row between it and now. History that could be erased by the thing it
// audits would not be history.
//
// item_key is never touched: updated starts as a copy of old, and
// itemApplyRevisionField's field switch has no case that writes it (see its
// doc comment) — "revert restores the old state" is exactly the reasoning
// that would wrongly restore an old guid too, so there is deliberately no
// code path here that could.
func (s *ItemServer) RevertRevision(ctx context.Context, req *affv1.ItemServiceRevertRevisionRequest) (*affv1.ItemServiceRevertRevisionResponse, error) {
	itemID := req.GetItemId()
	if itemID == 0 {
		return nil, status.Error(codes.InvalidArgument, "rpc: item_id is required")
	}
	revisionID := req.GetRevisionId()
	if revisionID == 0 {
		return nil, status.Error(codes.InvalidArgument, "rpc: revision_id is required")
	}

	db := s.st.Writer()
	old, err := itemLoadByID(ctx, db, itemID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "rpc: item %d not found", itemID)
		}
		return nil, status.Errorf(codes.Internal, "rpc: loading item %d: %v", itemID, err)
	}

	at, err := itemRevisionGroupAt(ctx, db, itemID, revisionID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "rpc: revision %d for item %d not found", revisionID, itemID)
		}
		return nil, status.Errorf(codes.Internal, "rpc: resolving revision %d: %v", revisionID, err)
	}
	rev, err := itemLoadRevisionFields(ctx, db, itemID, at)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: loading revision %d fields: %v", revisionID, err)
	}

	updated := old
	for _, ch := range rev.GetChanges() {
		if err := itemApplyRevisionField(&updated, ch.GetField(), ch.GetOldValue()); err != nil {
			return nil, status.Errorf(codes.Internal, "rpc: reverting item %d to revision %d: %v", itemID, revisionID, err)
		}
	}
	if strings.TrimSpace(updated.Title) == "" {
		// Cannot actually happen — Update/Create both reject an empty title
		// before it is ever written to item_revisions — but failing loudly
		// beats silently persisting an invalid item if that invariant is
		// ever violated elsewhere.
		return nil, status.Error(codes.FailedPrecondition, "rpc: reverting would leave item.title empty")
	}

	if !updated.PublishedAt.Equal(old.PublishedAt) {
		// Same range guard Update applies when it actually changes
		// published_at, for the same reason: a revert can restore a
		// published_at written before this check existed (item_revisions
		// rows predate the fix and are never rewritten), and reverting TO an
		// out-of-range value would reintroduce exactly the unrenderable
		// document this check exists to prevent.
		if err := itemValidatePublishedAt(updated.PublishedAt); err != nil {
			return nil, err
		}
		// Same RULE-7 guard Update applies, and for the same reason: a revert
		// is an ordinary edit, and an ordinary edit that backdates a feed's
		// newest item is invisible to Slack forever regardless of how the
		// new timestamp was chosen.
		newest, err := itemNewestPublished(ctx, db, old.FeedID, old.ID)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "rpc: %v", err)
		}
		if err := itemRejectBackdated(updated.PublishedAt, newest, strconv.FormatInt(old.FeedID, 10)); err != nil {
			return nil, err
		}
	}

	out, err := s.itemApplyEdit(ctx, db, old, updated, req.GetExpectedVersion(), s.editStamp())
	if err != nil {
		return nil, err
	}

	// RULE-6: a revert changes published output (it can touch every field
	// content:encoded/description/link/pubDate are built from) while reading,
	// at the RPC surface, like an internal bookkeeping operation over
	// item_revisions — precisely the kind of write that is easy to leave
	// uninvalidated. Fire strictly after itemApplyEdit's commit, matching
	// every other mutation in this file, so a rolled-back revert never
	// invalidates a cache for a write that did not happen.
	if slug, serr := itemFeedSlug(ctx, db, old.FeedID); serr == nil {
		s.invalidate(ctx, slug)
	}

	return &affv1.ItemServiceRevertRevisionResponse{Item: itemToProto(out)}, nil
}

// itemCountMatching counts every item matching the FILTERS, ignoring paging.
//
// Cursor paging cannot say how many pages exist — next_page_token reports
// only whether one more does — so without this the pager can offer Next and
// never "page 3 of 9". The cursor condition is deliberately excluded: counting
// with it folded in would return how many rows remain after the current page,
// so the total would shrink as the operator paged forward.
func itemCountMatching(ctx context.Context, r store.Reader, where []string, args []any) (int64, error) {
	query := `SELECT COUNT(*) FROM items`
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, " AND ")
	}
	var n int64
	if err := r.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}
