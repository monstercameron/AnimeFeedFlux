// Feed and item persistence: the writer-side CRUD that generation, sampling
// and the admin UI's item history all sit on top of (PLAN.md §10, §11, §12.4).
//
// Everything here is a method on *Store, i.e. writer-only. The publish plane
// never imports this file's callers — it reads through Reader (store.go).
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
)

// ErrNotFound is returned, wrapped or bare, when a lookup by key finds no row.
var ErrNotFound = errors.New("store: not found")

// ErrVersionConflict is returned when an UpdateItem's expected_version does
// not match the stored row — either it was already changed by another writer
// (the two-tabs case, §11) or the id does not exist. Optimistic concurrency
// exists so two admin sessions editing the same item cannot silently clobber
// one another.
var ErrVersionConflict = errors.New("store: version conflict")

// timeLayout is the one format every timestamp column uses: RFC3339Nano, UTC.
// store_test.go already writes timestamps this way for the tables this file
// touches, so this is not a new convention, just the one already in force.
const timeLayout = time.RFC3339Nano

func formatTime(t time.Time) string {
	return t.UTC().Format(timeLayout)
}

func parseTime(s string) (time.Time, error) {
	return time.Parse(timeLayout, s)
}

// parseNullTime turns a nullable TEXT column into a zero time.Time when the
// column is NULL or empty, matching model.Item's convention (IsDeleted checks
// DeletedAt.IsZero, not a separate bool).
func parseNullTime(ns sql.NullString) (time.Time, error) {
	if !ns.Valid || ns.String == "" {
		return time.Time{}, nil
	}
	return parseTime(ns.String)
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows, so QueryRow and
// Query call sites can share one scan function.
type rowScanner interface {
	Scan(dest ...any) error
}

// --- feeds ------------------------------------------------------------

// CreateFeed inserts a new feed and returns its id.
//
// Only the columns model.Feed carries are set explicitly; spec_json,
// jitter_offset, last_fired_slot, consecutive_failures and deleted_at are
// left to their schema defaults (or NULL) because nothing in this package
// owns recipe authoring or scheduling — that is FeedService's job (§11).
func (s *Store) CreateFeed(ctx context.Context, f model.Feed) (int64, error) {
	now := formatTime(time.Now())
	res, err := s.writer.ExecContext(ctx, `
		INSERT INTO feeds (slug, title, description, language, kind, enabled, timezone,
		                    author, copyright, og_image, ttl_minutes, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.Slug, f.Title, f.Description, f.Language, string(f.Kind),
		boolToInt(f.Enabled), f.Timezone,
		f.Author, f.Copyright, f.OGImage, f.TTLMinutes,
		now, now)
	if err != nil {
		return 0, fmt.Errorf("store: creating feed %q: %w", f.Slug, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: feed id: %w", err)
	}
	return id, nil
}

const feedColumns = `id, slug, title, description, language, kind, enabled, timezone,
	author, copyright, og_image, ttl_minutes, last_built_at, version`

func scanFeed(sc rowScanner) (model.Feed, error) {
	var (
		f                                       model.Feed
		enabled                                 int64
		kind                                    string
		author, copyright, ogImage, lastBuiltAt sql.NullString
	)
	if err := sc.Scan(
		&f.ID, &f.Slug, &f.Title, &f.Description, &f.Language, &kind, &enabled, &f.Timezone,
		&author, &copyright, &ogImage, &f.TTLMinutes, &lastBuiltAt, &f.Version,
	); err != nil {
		return model.Feed{}, err
	}
	f.Kind = model.FeedKind(kind)
	f.Enabled = enabled != 0
	f.Author = author.String
	f.Copyright = copyright.String
	f.OGImage = ogImage.String
	lastBuilt, err := parseNullTime(lastBuiltAt)
	if err != nil {
		return model.Feed{}, fmt.Errorf("store: parsing feeds.last_built_at: %w", err)
	}
	f.LastBuiltAt = lastBuilt
	return f, nil
}

// GetFeedBySlug looks up a feed by its (unique, immutable §14.1) slug.
func (s *Store) GetFeedBySlug(ctx context.Context, slug string) (model.Feed, error) {
	row := s.writer.QueryRowContext(ctx,
		`SELECT `+feedColumns+` FROM feeds WHERE slug = ?`, slug)
	f, err := scanFeed(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Feed{}, fmt.Errorf("store: feed %q: %w", slug, ErrNotFound)
	}
	if err != nil {
		return model.Feed{}, fmt.Errorf("store: getting feed %q: %w", slug, err)
	}
	return f, nil
}

// --- items --------------------------------------------------------------

const itemColumns = `id, feed_id, item_key, content_hash, title, summary_text, body_html,
	answer_html, link, source_name, published_at, origin, version,
	created_at, updated_at, edited_at, deleted_at`

func scanItem(sc rowScanner) (model.Item, error) {
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
	if it.PublishedAt, err = parseTime(publishedAt); err != nil {
		return model.Item{}, fmt.Errorf("store: parsing items.published_at: %w", err)
	}
	if it.CreatedAt, err = parseTime(createdAt); err != nil {
		return model.Item{}, fmt.Errorf("store: parsing items.created_at: %w", err)
	}
	if it.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return model.Item{}, fmt.Errorf("store: parsing items.updated_at: %w", err)
	}
	if it.EditedAt, err = parseNullTime(editedAt); err != nil {
		return model.Item{}, fmt.Errorf("store: parsing items.edited_at: %w", err)
	}
	if it.DeletedAt, err = parseNullTime(deletedAt); err != nil {
		return model.Item{}, fmt.Errorf("store: parsing items.deleted_at: %w", err)
	}
	// Tags are not persisted yet: there is no tags column in the schema
	// (0002_feeds_items.sql). They land with the migration that adds one;
	// until then this is a deliberate omission, not a bug.
	return it, nil
}

// truncatePublishedAt normalizes a published_at to whole-second precision
// before it is ever compared or stored by this package.
//
// RSS's RFC 822 pubDate (internal/render's RFC822, §5.1) has one-second
// resolution — there is no fractional-second slot in "Mon, 02 Jan 2006
// 15:04:05 -0700". items.published_at is UNIQUE(feed_id, published_at)
// (migrations/0002_feeds_items.sql), but that constraint only actually
// prevents "two items share a rendered pubDate" (the failure PLAN.md §5.5
// warns about — Slack's bookmark advances past the newer of the two and the
// other is invisible forever, silently) if the column being compared is at
// the SAME granularity the wire format renders. Storing at sub-second
// precision let two items land less than a second apart, pass the UNIQUE
// check as distinct rows, and still collide byte-for-byte once rendered.
// Truncating here — the single place every write in this package funnels
// through — makes the existing index do the right thing by construction,
// the same choice PromoteSample (internal/store/samples.go) and
// PublishCorrection (internal/rpc/item.go) already made for their own
// stamps.
func truncatePublishedAt(t time.Time) time.Time {
	return t.Truncate(time.Second)
}

// InsertItem creates a new item and returns its id.
//
// tokens_in/out, model, prompt_hash and run_id belong to the generation
// pipeline (§9), not to this package's contract, and are left at their
// schema defaults (0 / NULL) here. Tags are deliberately not persisted —
// see scanItem.
func (s *Store) InsertItem(ctx context.Context, it model.Item) (int64, error) {
	now := formatTime(time.Now())
	published := truncatePublishedAt(it.PublishedAt)
	res, err := s.writer.ExecContext(ctx, `
		INSERT INTO items (feed_id, item_key, content_hash, title, summary_text, body_html,
		                    answer_html, link, source_name, published_at, origin,
		                    created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		it.FeedID, it.ItemKey, it.ContentHash, it.Title, it.SummaryText, it.BodyHTML,
		nullString(it.AnswerHTML), nullString(it.Link), nullString(it.SourceName),
		formatTime(published), string(it.Origin),
		now, now)
	if err != nil {
		return 0, fmt.Errorf("store: inserting item %q: %w", it.ItemKey, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: item id: %w", err)
	}
	return id, nil
}

// nullString maps "" to NULL. answer_html/link/source_name have no default
// and are legitimately absent for e.g. a generative item with no answer, so
// an empty Go string round-trips as NULL rather than as a stored "".
func nullString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// GetItem looks up an item by its opaque, never-changing item_key (§5.1).
// A soft-deleted item is still found here — GetItem is not the publish
// plane's route lookup, it is the admin/back-office path, and callers that
// need to check deletion use IsDeleted().
func (s *Store) GetItem(ctx context.Context, itemKey string) (model.Item, error) {
	row := s.writer.QueryRowContext(ctx,
		`SELECT `+itemColumns+` FROM items WHERE item_key = ?`, itemKey)
	it, err := scanItem(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Item{}, fmt.Errorf("store: item %q: %w", itemKey, ErrNotFound)
	}
	if err != nil {
		return model.Item{}, fmt.Errorf("store: getting item %q: %w", itemKey, err)
	}
	return it, nil
}

// ListItems returns a feed's items newest-first by published_at — the same
// ordering the renderer and Slack's bookmark model require (§5.5).
//
// limit <= 0 means "no cap": SQLite treats a negative LIMIT as unlimited,
// which lets callers pass 0 as "give me everything" without a branch here.
func (s *Store) ListItems(ctx context.Context, feedID int64, limit int, includeDeleted bool) ([]model.Item, error) {
	if limit <= 0 {
		limit = -1
	}
	query := `SELECT ` + itemColumns + ` FROM items WHERE feed_id = ?`
	if !includeDeleted {
		query += ` AND deleted_at IS NULL`
	}
	query += ` ORDER BY published_at DESC LIMIT ?`

	rows, err := s.writer.QueryContext(ctx, query, feedID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: listing items for feed %d: %w", feedID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []model.Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scanning item: %w", err)
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterating items for feed %d: %w", feedID, err)
	}
	return out, nil
}

// UpdateItem edits title/summary/body/answer/link/source/published_at under
// optimistic concurrency (§11): the WHERE clause pins both id and the
// caller's expected version, and a zero-row update means someone else moved
// it first.
//
// item_key is never written here — it.ItemKey is ignored even if the caller
// supplied a different value, by construction (it is absent from the SET
// list), rather than by a runtime check. The guid is derived from it and
// must outlive an edit (§5.1, §12.4) or an edited item resurfaces as a
// duplicate in every subscriber's inbox.
func (s *Store) UpdateItem(ctx context.Context, it model.Item, expectedVersion int64) error {
	now := time.Now()
	published := truncatePublishedAt(it.PublishedAt)
	res, err := s.writer.ExecContext(ctx, `
		UPDATE items
		SET title = ?, summary_text = ?, body_html = ?, answer_html = ?, link = ?, source_name = ?,
		    published_at = ?, origin = ?, edited_at = ?, updated_at = ?, version = version + 1
		WHERE id = ? AND version = ?`,
		it.Title, it.SummaryText, it.BodyHTML, nullString(it.AnswerHTML), nullString(it.Link), nullString(it.SourceName),
		formatTime(published), string(it.Origin), formatTime(now), formatTime(now),
		it.ID, expectedVersion)
	if err != nil {
		return fmt.Errorf("store: updating item %d: %w", it.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: rows affected updating item %d: %w", it.ID, err)
	}
	if n == 0 {
		return fmt.Errorf("store: updating item %d at version %d: %w", it.ID, expectedVersion, ErrVersionConflict)
	}
	return nil
}

// SoftDeleteItem is the ONLY delete path in this package (§12.4). There is
// no Purge/hard-delete function, deliberately: the permalink must return 410
// forever and the guid is never freed, so a row that stops existing would
// break both promises. A `PurgeDeleted` RPC was cut from the design for
// exactly this reason — do not reintroduce it here.
func (s *Store) SoftDeleteItem(ctx context.Context, itemKey string) error {
	now := formatTime(time.Now())
	res, err := s.writer.ExecContext(ctx,
		`UPDATE items SET deleted_at = ?, updated_at = ? WHERE item_key = ?`,
		now, now, itemKey)
	if err != nil {
		return fmt.Errorf("store: soft-deleting item %q: %w", itemKey, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: rows affected soft-deleting item %q: %w", itemKey, err)
	}
	if n == 0 {
		return fmt.Errorf("store: soft-deleting item %q: %w", itemKey, ErrNotFound)
	}
	return nil
}

// RestoreItem clears a soft delete. The item_key and guid never moved, so
// restoring is just clearing deleted_at — nothing about identity changes.
func (s *Store) RestoreItem(ctx context.Context, itemKey string) error {
	now := formatTime(time.Now())
	res, err := s.writer.ExecContext(ctx,
		`UPDATE items SET deleted_at = NULL, updated_at = ? WHERE item_key = ?`,
		now, itemKey)
	if err != nil {
		return fmt.Errorf("store: restoring item %q: %w", itemKey, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: rows affected restoring item %q: %w", itemKey, err)
	}
	if n == 0 {
		return fmt.Errorf("store: restoring item %q: %w", itemKey, ErrNotFound)
	}
	return nil
}

// AggregateSlugsFor returns the slug of every aggregate feed that lists the
// feed named by memberSlug as a member (PLAN.md §14.2).
//
// It exists because an aggregate's rendered documents are cached under the
// AGGREGATE's slug, so invalidating a member reaches none of them: every
// caller that changes a member's published shape has to invalidate the
// aggregates too, and publish.Invalidator's doc comment says so explicitly —
// "any aggregate containing this feed must also be invalidated; that is the
// caller's responsibility". Before this helper existed only one of the three
// such callers (internal/rpc's FeedServer, on recipe edits) actually did it,
// with its own copy of this query keyed by feed id; item writes and completed
// generation runs — the frequent cases, and the ones that add items — did
// not. An aggregate that had been fetched once therefore served that snapshot
// until a recipe edit or a restart.
//
// Keyed by slug rather than id because that is what the cache and the
// Invalidator interface speak, and what every one of those call sites already
// has in hand at the moment it needs to fan out.
//
// A feed with no aggregates returns nil, not an error: that is the common
// case, not a failure.
func (s *Store) AggregateSlugsFor(ctx context.Context, memberSlug string) ([]string, error) {
	rows, err := s.reader.QueryContext(ctx, `
		SELECT a.slug
		  FROM feed_members fm
		  JOIN feeds a ON a.id = fm.aggregate_feed_id
		  JOIN feeds m ON m.id = fm.member_feed_id
		 WHERE m.slug = ? AND a.deleted_at IS NULL`, memberSlug)
	if err != nil {
		return nil, fmt.Errorf("store: listing aggregates for %q: %w", memberSlug, err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, fmt.Errorf("store: scanning aggregate slug: %w", err)
		}
		out = append(out, slug)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterating aggregates for %q: %w", memberSlug, err)
	}
	return out, nil
}
