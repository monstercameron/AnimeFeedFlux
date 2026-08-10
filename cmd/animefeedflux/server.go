// This file wires the publish plane (internal/publish) to the real store
// (internal/store), so the binary serves actual feeds from SQLite instead of
// only /healthz. It is the first end-to-end integration in the project
// (PLAN.md §2, §6, §15).
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/config"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// staleRunThreshold mirrors store.go's internal single-flight staleness
// window (runLockStaleAfter, §13/§15): "stale" must mean the same thing at
// boot as it does when StartRun decides whether a running row still holds
// the lock. It cannot be imported (unexported), so the value is duplicated
// here deliberately rather than guessed.
const staleRunThreshold = 3 * time.Minute

// defaultFeedWindow is the feed window cap (§5.4: "Feed window capped
// (default 50 items / 512 KB hard ceiling)"). Recipes will eventually carry
// a per-feed window (§7), but that field does not exist in the schema yet,
// so every feed uses the spec's stated default until it does.
const defaultFeedWindow = 50

// docsURL is the RSS 2.0 specification URL emitted in <docs> (§5.1), the
// same value the renderer's own golden tests and fixtures use.
const docsURL = "https://www.rssboard.org/rss-specification"

// openStore opens the database, applies every pending migration, and runs
// the boot-time crash-recovery watchdog (§15): any run row still marked
// 'running' whose heartbeat is older than staleRunThreshold died with its
// process, mid-generation, without a clean shutdown. Reclaiming it here
// means a crash never wedges a feed until someone notices by hand.
func openStore(ctx context.Context, cfg *config.Config) (*store.Store, error) {
	st, err := store.Open(ctx, store.Options{Path: cfg.DBPath})
	if err != nil {
		return nil, fmt.Errorf("opening store: %w", err)
	}
	if _, err := st.Migrate(ctx); err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("migrating store: %w", err)
	}
	if _, _, err := st.ReclaimStaleRuns(ctx, staleRunThreshold); err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("reclaiming stale runs: %w", err)
	}
	return st, nil
}

// The publish handler is built by buildPublishHandlerWithInvalidator in
// wire.go, which is this function's former body plus the one thing it
// could not return: the publish.Invalidator bound to the very cache the
// handler serves from. Two builders existed briefly because wire.go was
// written while this file was off-limits, and a near-duplicate of a
// wiring function is exactly the kind of thing that drifts silently —
// the copy gets a fix the original does not, and which one production
// uses is decided by an import nobody re-reads. There is one now.
//
// The §2 property it exists to hold is unchanged and is the reason the
// function is worth reading: every publish.Deps closure is built over
// st.Reader(), never st.Writer(), so a bug in the public read path
// cannot corrupt the database because it structurally has no writer to
// reach — not because anyone was careful.

// bootTagYear resolves the fixed Tag URI epoch from the earliest feed
// creation timestamp on file. An empty store (first boot, before any feed
// exists) has nothing to derive a year from; it falls back to a hard-coded
// constant rather than time.Now().Year(), so the very first feed created
// still gets a guid epoch that was fixed at compile time, not at the moment
// it happened to be created.
func bootTagYear(ctx context.Context, reader store.Reader) (int, error) {
	// preFirstFeedTagYear is the project's own inception year. It is used
	// only until the first feed row exists, after which MIN(created_at)
	// below takes over and never changes again for the life of the
	// deployment (the whole point of "fixed").
	const preFirstFeedTagYear = 2026

	var earliest sql.NullString
	err := reader.QueryRowContext(ctx, `SELECT MIN(created_at) FROM feeds`).Scan(&earliest)
	if err != nil {
		return 0, fmt.Errorf("reading earliest feed creation time: %w", err)
	}
	if !earliest.Valid || earliest.String == "" {
		return preFirstFeedTagYear, nil
	}
	t, err := parseStoreTime(earliest.String)
	if err != nil {
		return 0, fmt.Errorf("parsing earliest feed creation time %q: %w", earliest.String, err)
	}
	return t.Year(), nil
}

// --- read-only queries ------------------------------------------------
//
// These mirror the column sets and scan logic of internal/store/items.go's
// scanFeed/scanItem exactly, because the schema (migrations/0002_feeds_
// items.sql) is the same one — but that file's helpers are all methods on
// *Store bound to the writer connection, which is precisely the handle this
// package must never touch. Duplicating the scan here is the price of
// keeping the read-only property real rather than nominal.

// storeTimeLayout matches store package's timeLayout (RFC3339Nano, UTC),
// which every timestamp column in this schema uses.
const storeTimeLayout = time.RFC3339Nano

func parseStoreTime(s string) (time.Time, error) {
	return time.Parse(storeTimeLayout, s)
}

func parseNullStoreTime(ns sql.NullString) (time.Time, error) {
	if !ns.Valid || ns.String == "" {
		return time.Time{}, nil
	}
	return parseStoreTime(ns.String)
}

const feedSelectColumns = `id, slug, title, description, language, kind, enabled, timezone,
	author, copyright, og_image, ttl_minutes, last_built_at, version`

type feedScanner interface {
	Scan(dest ...any) error
}

func scanFeedRow(sc feedScanner) (model.Feed, error) {
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
	lastBuilt, err := parseNullStoreTime(lastBuiltAt)
	if err != nil {
		return model.Feed{}, fmt.Errorf("parsing feeds.last_built_at: %w", err)
	}
	f.LastBuiltAt = lastBuilt
	return f, nil
}

func getFeedBySlug(ctx context.Context, reader store.Reader, slug string) (model.Feed, bool, error) {
	row := reader.QueryRowContext(ctx, `SELECT `+feedSelectColumns+` FROM feeds WHERE slug = ?`, slug)
	f, err := scanFeedRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Feed{}, false, nil
	}
	if err != nil {
		return model.Feed{}, false, fmt.Errorf("getting feed %q: %w", slug, err)
	}
	return f, true, nil
}

func listFeeds(ctx context.Context, reader store.Reader) ([]model.Feed, error) {
	rows, err := reader.QueryContext(ctx, `SELECT `+feedSelectColumns+` FROM feeds ORDER BY slug`)
	if err != nil {
		return nil, fmt.Errorf("listing feeds: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []model.Feed
	for rows.Next() {
		f, err := scanFeedRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning feed: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating feeds: %w", err)
	}
	// Deterministic order is asserted at the SQL layer above (ORDER BY slug);
	// render.Index re-sorts by title anyway, so this is just a stable,
	// caller-independent contract for anything else that consumes ListFeeds.
	sort.SliceStable(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

const itemSelectColumns = `id, feed_id, item_key, content_hash, title, summary_text, body_html,
	answer_html, link, source_name, published_at, origin, version,
	created_at, updated_at, edited_at, deleted_at`

func scanItemRow(sc feedScanner) (model.Item, error) {
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
	if it.PublishedAt, err = parseStoreTime(publishedAt); err != nil {
		return model.Item{}, fmt.Errorf("parsing items.published_at: %w", err)
	}
	if it.CreatedAt, err = parseStoreTime(createdAt); err != nil {
		return model.Item{}, fmt.Errorf("parsing items.created_at: %w", err)
	}
	if it.UpdatedAt, err = parseStoreTime(updatedAt); err != nil {
		return model.Item{}, fmt.Errorf("parsing items.updated_at: %w", err)
	}
	if it.EditedAt, err = parseNullStoreTime(editedAt); err != nil {
		return model.Item{}, fmt.Errorf("parsing items.edited_at: %w", err)
	}
	if it.DeletedAt, err = parseNullStoreTime(deletedAt); err != nil {
		return model.Item{}, fmt.Errorf("parsing items.deleted_at: %w", err)
	}
	return it, nil
}

// listItems returns feedID's rendered window: newest-first, capped at
// defaultFeedWindow, excluding soft-deleted items — a deleted item is only
// ever reachable at its own permalink (410), never inside a feed document
// (§6, §12.4).
func listItems(ctx context.Context, reader store.Reader, feedID int64) ([]model.Item, error) {
	rows, err := reader.QueryContext(ctx,
		`SELECT `+itemSelectColumns+` FROM items
		 WHERE feed_id = ? AND deleted_at IS NULL
		 ORDER BY published_at DESC LIMIT ?`,
		feedID, defaultFeedWindow)
	if err != nil {
		return nil, fmt.Errorf("listing items for feed %d: %w", feedID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []model.Item
	for rows.Next() {
		it, err := scanItemRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning item: %w", err)
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating items for feed %d: %w", feedID, err)
	}
	return out, nil
}

// getItem resolves one item by its opaque item_key together with the feed
// that owns it, in a single joined query — the permalink route needs both
// (og:image and Channel context come from the feed) and a soft-deleted item
// must still be found here (its caller maps that to 410, not 404).
func getItem(ctx context.Context, reader store.Reader, itemKey string) (model.Feed, model.Item, bool, error) {
	row := reader.QueryRowContext(ctx, `
		SELECT items.id, items.feed_id, items.item_key, items.content_hash, items.title,
		       items.summary_text, items.body_html, items.answer_html, items.link, items.source_name,
		       items.published_at, items.origin, items.version,
		       items.created_at, items.updated_at, items.edited_at, items.deleted_at,
		       `+feedTableColumns()+`
		FROM items JOIN feeds ON feeds.id = items.feed_id
		WHERE items.item_key = ?`, itemKey)

	var (
		it                                model.Item
		origin                            string
		answerHTML, link, sourceName      sql.NullString
		publishedAt, createdAt, updatedAt string
		editedAt, deletedAt               sql.NullString

		f                                       model.Feed
		enabled                                 int64
		kind                                    string
		author, copyright, ogImage, lastBuiltAt sql.NullString
	)
	err := row.Scan(
		&it.ID, &it.FeedID, &it.ItemKey, &it.ContentHash, &it.Title, &it.SummaryText, &it.BodyHTML,
		&answerHTML, &link, &sourceName, &publishedAt, &origin, &it.Version,
		&createdAt, &updatedAt, &editedAt, &deletedAt,
		&f.ID, &f.Slug, &f.Title, &f.Description, &f.Language, &kind, &enabled, &f.Timezone,
		&author, &copyright, &ogImage, &f.TTLMinutes, &lastBuiltAt, &f.Version,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Feed{}, model.Item{}, false, nil
	}
	if err != nil {
		return model.Feed{}, model.Item{}, false, fmt.Errorf("getting item %q: %w", itemKey, err)
	}

	it.AnswerHTML = answerHTML.String
	it.Link = link.String
	it.SourceName = sourceName.String
	it.Origin = model.Origin(origin)
	if it.PublishedAt, err = parseStoreTime(publishedAt); err != nil {
		return model.Feed{}, model.Item{}, false, fmt.Errorf("parsing items.published_at: %w", err)
	}
	if it.CreatedAt, err = parseStoreTime(createdAt); err != nil {
		return model.Feed{}, model.Item{}, false, fmt.Errorf("parsing items.created_at: %w", err)
	}
	if it.UpdatedAt, err = parseStoreTime(updatedAt); err != nil {
		return model.Feed{}, model.Item{}, false, fmt.Errorf("parsing items.updated_at: %w", err)
	}
	if it.EditedAt, err = parseNullStoreTime(editedAt); err != nil {
		return model.Feed{}, model.Item{}, false, fmt.Errorf("parsing items.edited_at: %w", err)
	}
	if it.DeletedAt, err = parseNullStoreTime(deletedAt); err != nil {
		return model.Feed{}, model.Item{}, false, fmt.Errorf("parsing items.deleted_at: %w", err)
	}

	f.Kind = model.FeedKind(kind)
	f.Enabled = enabled != 0
	f.Author = author.String
	f.Copyright = copyright.String
	f.OGImage = ogImage.String
	lastBuilt, err := parseNullStoreTime(lastBuiltAt)
	if err != nil {
		return model.Feed{}, model.Item{}, false, fmt.Errorf("parsing feeds.last_built_at: %w", err)
	}
	f.LastBuiltAt = lastBuilt

	return f, it, true, nil
}

// feedTableColumns is feedSelectColumns qualified with the "feeds." prefix,
// for the joined query in getItem where an unqualified "id" would be
// ambiguous between items.id and feeds.id.
func feedTableColumns() string {
	return `feeds.id, feeds.slug, feeds.title, feeds.description, feeds.language, feeds.kind, feeds.enabled,
	feeds.timezone, feeds.author, feeds.copyright, feeds.og_image, feeds.ttl_minutes, feeds.last_built_at, feeds.version`
}
