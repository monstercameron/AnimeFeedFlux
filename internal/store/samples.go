// Sample persistence: the "sample this recipe" dry-run loop (PLAN.md §11,
// §12.3) and its one promotion path into the live feed (§9 step 7, §5.5,
// §11's collision-retry rule).
//
// A sample is deliberately NOT an item: Sample writes nothing to items or
// runs, costs money but publishes nothing, and is the thing an admin iterates
// on before committing to a prompt. Everything here is a method on *Store,
// i.e. writer-only, matching items.go and runs.go's split.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"

	sqlite "modernc.org/sqlite"
	sqlitelib "modernc.org/sqlite/lib"
)

// Sample is one row of the sampling loop's scratch table (§10). It never
// becomes an item on its own — PromoteSample is the only path from here into
// items, and it is a distinct write, not a status flip on this row.
type Sample struct {
	ID      int64
	FeedID  int64
	Payload []byte // the candidate item(s) and rendered fragment, as JSON (§11 Sample RPC)

	TokensIn  int
	TokensOut int
	CostUSD   float64

	CreatedAt time.Time
	ExpiresAt time.Time
}

const sampleColumns = `id, feed_id, created_at, expires_at, payload_json, tokens_in, tokens_out, cost_usd`

func scanSample(sc rowScanner) (Sample, error) {
	var (
		sm                   Sample
		createdAt, expiresAt string
		payload              string
	)
	if err := sc.Scan(
		&sm.ID, &sm.FeedID, &createdAt, &expiresAt, &payload,
		&sm.TokensIn, &sm.TokensOut, &sm.CostUSD,
	); err != nil {
		return Sample{}, err
	}
	sm.Payload = []byte(payload)

	var err error
	if sm.CreatedAt, err = parseTime(createdAt); err != nil {
		return Sample{}, fmt.Errorf("store: parsing samples.created_at: %w", err)
	}
	if sm.ExpiresAt, err = parseTime(expiresAt); err != nil {
		return Sample{}, fmt.Errorf("store: parsing samples.expires_at: %w", err)
	}
	return sm, nil
}

// PutSample writes a fresh sample and returns its id. It is the only write
// the Sample RPC makes (§11): the whole point of sampling is that it costs
// money but publishes nothing, and this row is the receipt.
//
// It writes the spend to sample_spend as well, in the SAME transaction, so
// the two cannot disagree about whether a provider call happened. The sample
// row is the artifact and is deleted on expiry, discard or promotion; the
// sample_spend row is the money and outlives all three. Keeping the cost only
// on the artifact made the daily total shrink through the day and let anyone
// erase a preview's cost by discarding it — see migrations/0006.
func (s *Store) PutSample(ctx context.Context, feedID int64, payload []byte, tokensIn, tokensOut int, costUSD float64, ttl time.Duration) (int64, error) {
	now := time.Now()

	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: begin putting sample for feed %d: %w", feedID, err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO samples (feed_id, created_at, expires_at, payload_json, tokens_in, tokens_out, cost_usd)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		feedID, formatTime(now), formatTime(now.Add(ttl)), string(payload), tokensIn, tokensOut, costUSD)
	if err != nil {
		return 0, fmt.Errorf("store: putting sample for feed %d: %w", feedID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: sample id: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sample_spend (feed_id, at, tokens_in, tokens_out, cost_usd)
		VALUES (?, ?, ?, ?, ?)`,
		feedID, formatTime(now), tokensIn, tokensOut, costUSD); err != nil {
		return 0, fmt.Errorf("store: recording sample spend for feed %d: %w", feedID, err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: commit putting sample for feed %d: %w", feedID, err)
	}
	return id, nil
}

// SampleSpendSince sums preview spend from sample_spend, the same shape as
// SpendSince does for runs. feedID 0 means every feed.
//
// Read from the ledger, never from `samples`: that table's rows are deleted on
// expiry, discard and promotion, so summing it would report a total that falls
// through the day and would let a preview's cost be erased by discarding it.
func (s *Store) SampleSpendSince(ctx context.Context, feedID int64, since time.Time) (tokensIn, tokensOut int, costUSD float64, err error) {
	query := `SELECT COALESCE(SUM(tokens_in), 0), COALESCE(SUM(tokens_out), 0), COALESCE(SUM(cost_usd), 0)
	          FROM sample_spend WHERE at >= ?`
	args := []any{formatTime(since)}
	if feedID != 0 {
		query += ` AND feed_id = ?`
		args = append(args, feedID)
	}
	row := s.writer.QueryRowContext(ctx, query, args...)
	if err := row.Scan(&tokensIn, &tokensOut, &costUSD); err != nil {
		return 0, 0, 0, fmt.Errorf("store: summing sample spend since %s: %w", formatTime(since), err)
	}
	return tokensIn, tokensOut, costUSD, nil
}

// TotalSpendSince is what every budget ceiling and every spend figure should
// use: scheduled runs PLUS previews. They are stored apart because a sample
// must never look like a publish (§11/§22 J3), not because a preview's dollars
// are a different kind of dollar.
//
// # One place still disagrees
//
// The generate page's rail shows a 7-day spend PER FEED, and it does not come
// from here. That page has no RPC carrying a cost figure — Feed and
// SystemService both lack one — so it aggregates Run.EstCostUsd client-side
// from RunService.History (web/pages/generate/render.go). That is runs only,
// so a feed whose spend went on previews reads lower in the rail than it does
// in any total, and the two will not reconcile.
//
// It cannot be closed from the client. Summing SampleService.ListSamples
// would undercount for exactly the reason this ledger exists: sample rows are
// deleted on expiry, discard and promotion, so the live list is not the
// history. Closing it needs a per-feed spend figure on an RPC — a field on
// Feed, or a SystemService method — reading TotalSpendSince server-side.
func (s *Store) TotalSpendSince(ctx context.Context, feedID int64, since time.Time) (tokensIn, tokensOut int, costUSD float64, err error) {
	runIn, runOut, runUSD, err := s.SpendSince(ctx, feedID, since)
	if err != nil {
		return 0, 0, 0, err
	}
	smpIn, smpOut, smpUSD, err := s.SampleSpendSince(ctx, feedID, since)
	if err != nil {
		return 0, 0, 0, err
	}
	return runIn + smpIn, runOut + smpOut, runUSD + smpUSD, nil
}

// GetSample looks a sample up by id. An EXPIRED sample returns ErrNotFound,
// the same as a missing one (§12.3): samples persist 24h so a good one
// survives a page refresh, but serving one past that window would let an
// admin promote an item generated against a prompt that has since changed.
// "Expired" is therefore absence, not a status the caller has to remember to
// check separately.
func (s *Store) GetSample(ctx context.Context, id int64) (Sample, error) {
	row := s.writer.QueryRowContext(ctx, `SELECT `+sampleColumns+` FROM samples WHERE id = ?`, id)
	sm, err := scanSample(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Sample{}, fmt.Errorf("store: sample %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return Sample{}, fmt.Errorf("store: getting sample %d: %w", id, err)
	}
	if !time.Now().Before(sm.ExpiresAt) {
		return Sample{}, fmt.Errorf("store: sample %d: %w", id, ErrNotFound)
	}
	return sm, nil
}

// ListSamples returns a feed's UNEXPIRED samples, newest first — the same
// "survives a refresh" contract as GetSample (§12.3), applied to the list the
// sampler pane shows.
func (s *Store) ListSamples(ctx context.Context, feedID int64) ([]Sample, error) {
	rows, err := s.writer.QueryContext(ctx,
		`SELECT `+sampleColumns+` FROM samples WHERE feed_id = ? AND expires_at > ? ORDER BY created_at DESC`,
		feedID, formatTime(time.Now()))
	if err != nil {
		return nil, fmt.Errorf("store: listing samples for feed %d: %w", feedID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []Sample
	for rows.Next() {
		sm, err := scanSample(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scanning sample: %w", err)
		}
		out = append(out, sm)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterating samples for feed %d: %w", feedID, err)
	}
	return out, nil
}

// DiscardSample drops a sample outright (the Sampler pane's "Discard"
// button, §12.3). Unlike items, a sample was never published, so there is no
// permalink to keep 410ing and no guid to keep from being reused — dropping
// the row is simply correct here, not a documented exception.
func (s *Store) DiscardSample(ctx context.Context, id int64) error {
	res, err := s.writer.ExecContext(ctx, `DELETE FROM samples WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: discarding sample %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: rows affected discarding sample %d: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("store: discarding sample %d: %w", id, ErrNotFound)
	}
	return nil
}

// PruneExpiredSamples hard-deletes every sample whose expires_at is at or
// before now, and reports how many it removed.
//
// This is the one place in this package a hard delete is correct, and it is
// worth being explicit about why it does not contradict §12.4's "no hard
// delete" rule for items: that rule protects PUBLISHED content — permalinks
// that must keep 410ing and guids that must never be reused. A sample was
// never published; it has no permalink and no guid, so there is nothing a
// hard delete here could break. §15 is explicit that the nightly job DELETES
// expired samples rather than merely filtering them out, because sampling is
// meant to be a cheap loop run often — filtering alone (as GetSample and
// ListSamples already do, for the 24h window itself) would let the table
// grow forever with rows nothing will ever read again.
// Superseded, and not the one that runs. The nightly job prunes expired
// samples with its own statement (internal/ops/prune.go's
// pruneExpiredSamples, over *sql.DB), and this method has no caller outside
// tests. Two implementations of one retention policy, and they do not even
// agree on the boundary: ops uses `expires_at < now`, this uses `<=`. That
// difference is worth exactly nothing today and is precisely the kind of
// drift that matters the day someone changes one of them.
//
// Kept because it is the store-layer expression of the rule and its tests
// document the "hard delete is correct here" reasoning above; wire callers to
// internal/ops.Prune, not to this.
func (s *Store) PruneExpiredSamples(ctx context.Context, now time.Time) (int, error) {
	res, err := s.writer.ExecContext(ctx, `DELETE FROM samples WHERE expires_at <= ?`, formatTime(now))
	if err != nil {
		return 0, fmt.Errorf("store: pruning expired samples: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: rows affected pruning expired samples: %w", err)
	}
	return int(n), nil
}

// promoteTimestampRetries caps how many +1s bumps PromoteSample will try
// before giving up on a UNIQUE(feed_id, published_at) collision. A promote
// races, at most, the scheduler and other manual promotes for the same feed
// (§11) — a handful of attempts is the point past which a collision on every
// single try stops being a race and starts being a bug worth surfacing.
const promoteTimestampRetries = 5

// promoteClock is the source of "now" for PromoteSample, overridable so
// samples_test.go can make the chosen stamp deterministic instead of racing
// the real wall clock. Production always uses time.Now.
var promoteClock = time.Now

// promoteAfterReadNewestHook, when non-nil, runs right after PromoteSample has
// read the feed's current newest published_at and before it attempts the
// insert. Production leaves this nil. samples_test.go uses it to open a
// deliberate race window — inserting a colliding row through a second
// connection while this transaction's own insert has not yet run — which is
// otherwise unreachable in a single-process, single-writer test in any
// timing-independent way, to prove the UNIQUE-collision retry path actually
// runs rather than merely compiling.
var promoteAfterReadNewestHook func()

// PromoteSample persists a sample's chosen item into the live feed and
// discards the sample, in ONE transaction (§9 step 7, §11): a promote that
// wrote the item but left the sample behind (or vice versa) is indistinguishable
// from a bug, exactly the failure §9's run-commit atomicity already refuses
// to allow for generation.
//
// item.FeedID is ignored in favor of the sample's own feed_id — the sample
// row is the source of truth for which feed this was ever sampled against,
// the same reasoning UpdateItem uses to ignore a caller-supplied item_key.
//
// published_at is always stamped strictly after the feed's current newest
// item (§5.5's no-backdating rule), even if the caller's clock is somehow
// behind that item's timestamp — never simply "now" taken on faith. A
// promote can race a scheduled run inserting at the same instant (§11), so a
// UNIQUE(feed_id, published_at) collision is retried at +1 second rather than
// surfacing as a raw constraint error to the admin.
func (s *Store) PromoteSample(ctx context.Context, id int64, item model.Item) (int64, error) {
	// The "what's the newest item" read is deliberately OUTSIDE the write
	// transaction below, and done as plain (non-transactional) reads rather
	// than folded into it. In WAL mode a transaction that starts with a read
	// takes a snapshot; if it later tries to write after a concurrent writer
	// has committed, SQLite returns SQLITE_BUSY (a stale-snapshot conflict),
	// not the UNIQUE-constraint error this function is built to catch and
	// retry past. Reading first, then opening a fresh transaction whose FIRST
	// statement is the write, avoids that: the write transaction takes its
	// snapshot at the write itself, so a genuine collision surfaces as the
	// UNIQUE violation §11 describes, not an unrelated busy error.
	var feedID int64
	if err := s.writer.QueryRowContext(ctx,
		`SELECT feed_id FROM samples WHERE id = ?`, id,
	).Scan(&feedID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("store: promoting sample %d: %w", id, ErrNotFound)
		}
		return 0, fmt.Errorf("store: reading sample %d: %w", id, err)
	}
	item.FeedID = feedID

	var newestRaw sql.NullString
	if err := s.writer.QueryRowContext(ctx,
		`SELECT MAX(published_at) FROM items WHERE feed_id = ?`, feedID,
	).Scan(&newestRaw); err != nil {
		return 0, fmt.Errorf("store: reading newest published_at for feed %d: %w", feedID, err)
	}
	newest := time.Time{}
	if newestRaw.Valid && newestRaw.String != "" {
		var perr error
		if newest, perr = parseTime(newestRaw.String); perr != nil {
			return 0, fmt.Errorf("store: parsing newest published_at for feed %d: %w", feedID, perr)
		}
	}

	if promoteAfterReadNewestHook != nil {
		promoteAfterReadNewestHook()
	}

	// Truncated to whole seconds: §5.5's "distinct, strictly increasing
	// pubDate (base + N seconds)" scheme is second-granularity throughout the
	// rest of the store (CommitRun's items, RFC 822 rendering has no
	// fractional seconds), and the "+1 second" retry only makes sense against
	// stamps that share that granularity.
	stamp := promoteClock().Truncate(time.Second)
	if !stamp.After(newest) {
		stamp = newest.Add(time.Second)
	}

	for attempt := 0; ; attempt++ {
		itemID, err := s.promoteOnce(ctx, id, item, stamp)
		if err == nil {
			return itemID, nil
		}
		if isUniquePublishedAtViolation(err) && attempt < promoteTimestampRetries {
			stamp = stamp.Add(time.Second)
			continue
		}
		return 0, fmt.Errorf("store: promoting sample %d: %w", id, err)
	}
}

// promoteOnce is one attempt at PromoteSample's atomic pair: insert the item
// at stamp, then discard the sample, both in ONE transaction (§9 step 7,
// §11) — a promote that wrote the item but left the sample behind, or vice
// versa, is indistinguishable from a bug. It opens a fresh transaction per
// call rather than being handed a shared one, so a UNIQUE(feed_id,
// published_at) collision on the insert aborts cleanly (via the deferred
// Rollback) and PromoteSample's caller can retry with a clean slate at
// stamp+1s instead of reusing a transaction already poisoned by the failed
// statement.
func (s *Store) promoteOnce(ctx context.Context, sampleID int64, item model.Item, stamp time.Time) (int64, error) {
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	item.PublishedAt = stamp
	now := formatTime(time.Now())
	formatsJSON, ferr := encodeFormats(item.Formats)
	if ferr != nil {
		return 0, ferr
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO items (feed_id, item_key, content_hash, title, summary_text, body_html,
		                    answer_html, link, source_name, formats_json, published_at, origin,
		                    created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.FeedID, item.ItemKey, item.ContentHash, item.Title, item.SummaryText, item.BodyHTML,
		nullString(item.AnswerHTML), nullString(item.Link), nullString(item.SourceName),
		formatsJSON, formatTime(stamp), string(item.Origin), now, now)
	if err != nil {
		return 0, fmt.Errorf("inserting item: %w", err)
	}
	itemID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("promoted item id: %w", err)
	}

	delRes, err := tx.ExecContext(ctx, `DELETE FROM samples WHERE id = ?`, sampleID)
	if err != nil {
		return 0, fmt.Errorf("discarding sample: %w", err)
	}
	n, err := delRes.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected discarding sample: %w", err)
	}
	if n == 0 {
		// The sample vanished (discarded, or pruned as expired) between
		// PromoteSample's initial read and this transaction. Neither the
		// item nor the discard commits — the deferred Rollback undoes the
		// insert above.
		return 0, fmt.Errorf("sample no longer exists: %w", ErrNotFound)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return itemID, nil
}

// isUniquePublishedAtViolation reports whether err is a SQLite UNIQUE
// constraint failure, so PromoteSample can tell "the timestamp collided,
// retry" apart from every other reason an insert might fail (a bad content
// hash, a missing feed, a driver error) — those must surface, not be
// silently retried into a wrong result.
func isUniquePublishedAtViolation(err error) bool {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	return sqliteErr.Code() == sqlitelib.SQLITE_CONSTRAINT_UNIQUE
}
