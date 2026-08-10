// Run lifecycle persistence: the single-flight lock, heartbeat, and the
// atomic items+run commit that make a crash mid-generation legible instead of
// silently lossy (PLAN.md §9 step 7/8, §10, §13, §15).
//
// Everything here is a method on *Store, i.e. writer-only, matching items.go
// and store.go's split.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/novelty"
)

// ErrRunInProgress is returned by StartRun when a run for the same feed is
// already running and its heartbeat is still fresh (§13: "single-flight per
// feed via a DB run lock with heartbeat, so a slow run cannot stack").
var ErrRunInProgress = errors.New("store: run already in progress")

// runLockStaleAfter is how old a 'running' run's heartbeat may be before a
// new StartRun for the same feed is let through instead of being blocked.
// This mirrors the boot watchdog's staleness judgment (§15) so "in progress"
// means the same thing whether it's asked at request time or at boot.
const runLockStaleAfter = 3 * time.Minute

// RunSummary is the accounting a caller reports when closing a run, shared by
// CommitRun (success) and FailRun (failure) because §22 J5 requires tokens
// and cost to be recorded even when the run failed — a failure that spent
// money must still show the money.
type RunSummary struct {
	TokensIn      int
	TokensOut     int
	CostUSD       float64
	ItemsRejected int
	// RejectReasons maps a rejection reason (e.g. "link_mismatch",
	// "novelty_duplicate") to how many items were dropped for it. The sum
	// must equal ItemsRejected — see reasonsJSON.
	RejectReasons map[string]int
}

// reasonsJSON validates that the reject-reason counts reconcile with
// ItemsRejected (PLAN.md §9/§10: "items_added + items_rejected must
// reconcile with the recorded reject reasons") and serializes them as the
// JSON object reject_reasons_json stores.
func (rs RunSummary) reasonsJSON() (string, error) {
	total := 0
	for _, n := range rs.RejectReasons {
		total += n
	}
	if total != rs.ItemsRejected {
		return "", fmt.Errorf(
			"store: run summary reject reasons total %d does not reconcile with items_rejected %d",
			total, rs.ItemsRejected)
	}
	reasons := rs.RejectReasons
	if reasons == nil {
		reasons = map[string]int{}
	}
	b, err := json.Marshal(reasons)
	if err != nil {
		return "", fmt.Errorf("store: marshaling reject reasons: %w", err)
	}
	return string(b), nil
}

// Run is one row of the operational spine (§10): cost, drift, and "why is
// this feed stale" all resolve here.
type Run struct {
	ID     int64
	FeedID int64

	StartedAt  time.Time
	FinishedAt time.Time // zero while status = running

	Status    string // running | success | failed | skipped | interrupted | completed_unconfirmed
	ErrorKind string
	Error     string

	ItemsAdded    int
	ItemsRejected int
	RejectReasons map[string]int

	TokensIn   int
	TokensOut  int
	EstCostUSD float64

	Trigger     string // cron | manual
	LockHolder  string
	HeartbeatAt time.Time
}

const runColumns = `id, feed_id, started_at, finished_at, status, error_kind, error,
	items_added, items_rejected, reject_reasons_json, tokens_in, tokens_out, est_cost_usd,
	trigger, lock_holder, heartbeat_at`

func scanRun(sc rowScanner) (Run, error) {
	var (
		r                                           Run
		startedAt                                   string
		finishedAt, errorKind, errText, heartbeatAt sql.NullString
		lockHolder                                  sql.NullString
		reasonsJSON                                 string
	)
	if err := sc.Scan(
		&r.ID, &r.FeedID, &startedAt, &finishedAt, &r.Status, &errorKind, &errText,
		&r.ItemsAdded, &r.ItemsRejected, &reasonsJSON, &r.TokensIn, &r.TokensOut, &r.EstCostUSD,
		&r.Trigger, &lockHolder, &heartbeatAt,
	); err != nil {
		return Run{}, err
	}

	var err error
	if r.StartedAt, err = parseTime(startedAt); err != nil {
		return Run{}, fmt.Errorf("store: parsing runs.started_at: %w", err)
	}
	if r.FinishedAt, err = parseNullTime(finishedAt); err != nil {
		return Run{}, fmt.Errorf("store: parsing runs.finished_at: %w", err)
	}
	if r.HeartbeatAt, err = parseNullTime(heartbeatAt); err != nil {
		return Run{}, fmt.Errorf("store: parsing runs.heartbeat_at: %w", err)
	}
	r.ErrorKind = errorKind.String
	r.Error = errText.String
	r.LockHolder = lockHolder.String

	if reasonsJSON == "" {
		reasonsJSON = "{}"
	}
	if err := json.Unmarshal([]byte(reasonsJSON), &r.RejectReasons); err != nil {
		return Run{}, fmt.Errorf("store: parsing runs.reject_reasons_json: %w", err)
	}
	return r, nil
}

// StartRun opens a new run row for feedID, unless one is already running
// with a fresh heartbeat — the single-flight-per-feed rule (§13) that stops
// a slow run from stacking. The lock check and the insert happen in one
// transaction so two concurrent StartRun calls for the same feed cannot both
// observe "nothing running" and both proceed.
//
// A running row whose heartbeat has gone stale does NOT block a new run: a
// crashed process left a lock nobody will ever renew, and refusing to start
// would wedge the feed until ReclaimStaleRuns next runs at boot (§15). That
// stale row is left as-is here; reclaiming it is ReclaimStaleRuns's job, not
// a side effect of starting an unrelated run.
func (s *Store) StartRun(ctx context.Context, feedID int64, trigger, holder string) (int64, error) {
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: begin start run for feed %d: %w", feedID, err)
	}
	defer func() { _ = tx.Rollback() }()

	var heartbeatAt sql.NullString
	err = tx.QueryRowContext(ctx,
		`SELECT heartbeat_at FROM runs WHERE feed_id = ? AND status = 'running'
		 ORDER BY started_at DESC LIMIT 1`, feedID,
	).Scan(&heartbeatAt)
	switch {
	case err == nil:
		hb, perr := parseNullTime(heartbeatAt)
		if perr != nil {
			return 0, fmt.Errorf("store: parsing existing run heartbeat_at for feed %d: %w", feedID, perr)
		}
		if !hb.IsZero() && time.Since(hb) < runLockStaleAfter {
			return 0, ErrRunInProgress
		}
	case errors.Is(err, sql.ErrNoRows):
		// no running run for this feed; fall through to insert
	default:
		return 0, fmt.Errorf("store: checking run lock for feed %d: %w", feedID, err)
	}

	now := formatTime(time.Now())
	res, err := tx.ExecContext(ctx, `
		INSERT INTO runs (feed_id, started_at, status, trigger, lock_holder, heartbeat_at)
		VALUES (?, ?, 'running', ?, ?, ?)`,
		feedID, now, trigger, holder, now)
	if err != nil {
		return 0, fmt.Errorf("store: starting run for feed %d: %w", feedID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: new run id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: committing start of run for feed %d: %w", feedID, err)
	}
	return id, nil
}

// Heartbeat renews a running run's lock. It is a no-op error (ErrNotFound)
// if the run does not exist or is no longer 'running' — a heartbeat arriving
// after the run already finished (or was reclaimed) has nothing left to
// renew.
func (s *Store) Heartbeat(ctx context.Context, runID int64) error {
	res, err := s.writer.ExecContext(ctx,
		`UPDATE runs SET heartbeat_at = ? WHERE id = ? AND status = 'running'`,
		formatTime(time.Now()), runID)
	if err != nil {
		return fmt.Errorf("store: heartbeat for run %d: %w", runID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: rows affected heartbeat for run %d: %w", runID, err)
	}
	if n == 0 {
		return fmt.Errorf("store: heartbeat for run %d: %w", runID, ErrNotFound)
	}
	return nil
}

// insertRunItemSQL mirrors items.go's InsertItem, plus run_id, so a
// commit can attribute every item it inserts back to the run that produced
// it — ReclaimStaleRuns depends on that link to tell "crashed with nothing
// done" apart from "crashed after committing" (§15).
const insertRunItemSQL = `
	INSERT INTO items (feed_id, item_key, content_hash, title, summary_text, body_html,
	                    answer_html, link, source_name, published_at, origin, run_id,
	                    created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

// CommitRun inserts items and closes the run row in ONE transaction. This is
// the §9 rule and the reason this function takes both together: a crash
// between "items committed" and "run marked finished" would leave live items
// beside a run the watchdog marks interrupted, and the history would then
// lie about what happened. If any item fails to insert (e.g. a
// UNIQUE(feed_id, published_at) collision, §5.5), the whole transaction rolls
// back — the run stays 'running' and no items land, never a partial commit.
//
// embeddings is optional and variadic ONLY so every existing 4-argument call
// site (the composition root, hooks.go's wrapper, and every test outside
// this package) keeps compiling unchanged; logically it is a single
// []ItemEmbedding. Any entry whose ItemKey does not match one of items is a
// caller bug and fails the whole commit — silently dropping it would look
// exactly like the bug this exists to close: an embedding nobody notices is
// missing. §9 requires items, their embeddings, and the closed run row to
// commit atomically together, so each embedding is inserted in THIS
// transaction, keyed off the item id CommitRun itself just minted — not
// looked up afterward, and never on a separate connection.
func (s *Store) CommitRun(ctx context.Context, runID int64, items []model.Item, summary RunSummary, embeddings ...ItemEmbedding) error {
	reasonsJSON, err := summary.reasonsJSON()
	if err != nil {
		return err
	}

	byKey := make(map[string]novelty.Vector, len(embeddings))
	for _, e := range embeddings {
		byKey[e.ItemKey] = e.Vector
	}

	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin commit run %d: %w", runID, err)
	}
	defer func() { _ = tx.Rollback() }()

	now := formatTime(time.Now())
	for _, it := range items {
		res, err := tx.ExecContext(ctx, insertRunItemSQL,
			it.FeedID, it.ItemKey, it.ContentHash, it.Title, it.SummaryText, it.BodyHTML,
			nullString(it.AnswerHTML), nullString(it.Link), nullString(it.SourceName),
			formatTime(it.PublishedAt), string(it.Origin), runID,
			now, now,
		)
		if err != nil {
			return fmt.Errorf("store: inserting item %q for run %d: %w", it.ItemKey, runID, err)
		}
		if v, ok := byKey[it.ItemKey]; ok {
			itemID, err := res.LastInsertId()
			if err != nil {
				return fmt.Errorf("store: new item id for %q in run %d: %w", it.ItemKey, runID, err)
			}
			if err := insertItemEmbedding(ctx, tx, itemID, v); err != nil {
				return fmt.Errorf("store: run %d: %w", runID, err)
			}
			delete(byKey, it.ItemKey)
		}
	}
	if len(byKey) > 0 {
		leftover := make([]string, 0, len(byKey))
		for k := range byKey {
			leftover = append(leftover, k)
		}
		return fmt.Errorf("store: commit run %d: embeddings given for item key(s) %v not present in items", runID, leftover)
	}

	res, err := tx.ExecContext(ctx, `
		UPDATE runs
		SET status = 'success', finished_at = ?, items_added = ?, items_rejected = ?,
		    reject_reasons_json = ?, tokens_in = ?, tokens_out = ?, est_cost_usd = ?, heartbeat_at = ?
		WHERE id = ? AND status = 'running'`,
		now, len(items), summary.ItemsRejected, reasonsJSON,
		summary.TokensIn, summary.TokensOut, summary.CostUSD, now,
		runID)
	if err != nil {
		return fmt.Errorf("store: closing run %d: %w", runID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: rows affected closing run %d: %w", runID, err)
	}
	if n == 0 {
		return fmt.Errorf("store: committing run %d: %w", runID, ErrNotFound)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: committing run %d: %w", runID, err)
	}
	return nil
}

// FailRun closes a run as 'failed'. Tokens and cost are still recorded here
// (PLAN.md §22 J5): a call that spent money and then hit a validation or
// provider error must not show as free just because nothing published.
func (s *Store) FailRun(ctx context.Context, runID int64, errorKind, message string, summary RunSummary) error {
	reasonsJSON, err := summary.reasonsJSON()
	if err != nil {
		return err
	}

	now := formatTime(time.Now())
	res, err := s.writer.ExecContext(ctx, `
		UPDATE runs
		SET status = 'failed', finished_at = ?, error_kind = ?, error = ?,
		    items_added = 0, items_rejected = ?, reject_reasons_json = ?,
		    tokens_in = ?, tokens_out = ?, est_cost_usd = ?, heartbeat_at = ?
		WHERE id = ? AND status = 'running'`,
		now, nullString(errorKind), nullString(message),
		summary.ItemsRejected, reasonsJSON,
		summary.TokensIn, summary.TokensOut, summary.CostUSD, now,
		runID)
	if err != nil {
		return fmt.Errorf("store: failing run %d: %w", runID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: rows affected failing run %d: %w", runID, err)
	}
	if n == 0 {
		return fmt.Errorf("store: failing run %d: %w", runID, ErrNotFound)
	}
	return nil
}

// SkipRun closes a run as 'skipped' — a distinct status from 'failed'
// (§10/§13): budget exhaustion or novelty exhaustion (§9 step 5) is an
// intentional no-op, not an error, and the dashboard should be able to tell
// the two apart at a glance.
//
// summary is optional and variadic ONLY so every existing 3-argument call
// site keeps compiling unchanged; logically it is a single RunSummary,
// exactly like FailRun's parameter, and passing more than one is a caller
// bug. The caller contract: a run skipped BEFORE any spend — a per-feed
// budget or run cap enforced ahead of the call (§13) — should omit summary
// or pass a zero RunSummary, and this records tokens_in/tokens_out/
// est_cost_usd as zero, honestly. A run skipped AFTER spend — the novelty
// gate exhausting its retries once every candidate came back a duplicate
// (§9 step 5), where the model has already been called one or more times —
// MUST pass the RunSummary recording that spend, or the run's cost silently
// disappears from SpendSince and the cost history (§13: "every run ...
// stores its own est_cost_usd at the price in force, so history stays
// meaningful after a price change" — a skipped run is no exception).
func (s *Store) SkipRun(ctx context.Context, runID int64, reason string, summary ...RunSummary) error {
	var rs RunSummary
	switch len(summary) {
	case 0:
	case 1:
		rs = summary[0]
	default:
		return fmt.Errorf("store: SkipRun: at most one RunSummary may be passed, got %d", len(summary))
	}
	reasonsJSON, err := rs.reasonsJSON()
	if err != nil {
		return err
	}

	now := formatTime(time.Now())
	res, err := s.writer.ExecContext(ctx, `
		UPDATE runs
		SET status = 'skipped', finished_at = ?, error = ?, heartbeat_at = ?,
		    tokens_in = ?, tokens_out = ?, est_cost_usd = ?,
		    items_rejected = ?, reject_reasons_json = ?
		WHERE id = ? AND status = 'running'`,
		now, nullString(reason), now,
		rs.TokensIn, rs.TokensOut, rs.CostUSD,
		rs.ItemsRejected, reasonsJSON,
		runID)
	if err != nil {
		return fmt.Errorf("store: skipping run %d: %w", runID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: rows affected skipping run %d: %w", runID, err)
	}
	if n == 0 {
		return fmt.Errorf("store: skipping run %d: %w", runID, ErrNotFound)
	}
	return nil
}

// ReclaimStaleRuns is the boot watchdog (§15): any 'running' row whose
// heartbeat is older than olderThan had its process die without a clean
// shutdown. Because items and the closed run row commit atomically (§9), a
// stale run with zero committed items is, by construction, one that never
// reached CommitRun — it is marked 'interrupted' truthfully. The watchdog
// still ASSERTS that rather than assuming it: if a stale run is somehow
// found WITH committed items (run_id references), it is marked
// 'completed_unconfirmed' and flagged for review instead of being recorded
// as a failure that demonstrably did work.
func (s *Store) ReclaimStaleRuns(ctx context.Context, olderThan time.Duration) (reclaimed int, unconfirmed int, err error) {
	cutoff := formatTime(time.Now().Add(-olderThan))

	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("store: begin reclaim stale runs: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx,
		`SELECT id FROM runs WHERE status = 'running' AND heartbeat_at < ?`, cutoff)
	if err != nil {
		return 0, 0, fmt.Errorf("store: finding stale runs: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return 0, 0, fmt.Errorf("store: scanning stale run id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, 0, fmt.Errorf("store: iterating stale runs: %w", err)
	}
	_ = rows.Close()

	now := formatTime(time.Now())
	for _, id := range ids {
		var itemCount int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM items WHERE run_id = ?`, id,
		).Scan(&itemCount); err != nil {
			return 0, 0, fmt.Errorf("store: counting items for stale run %d: %w", id, err)
		}

		status := "interrupted"
		if itemCount > 0 {
			status = "completed_unconfirmed"
		}

		if _, err := tx.ExecContext(ctx,
			`UPDATE runs SET status = ?, finished_at = ?, heartbeat_at = ?
			 WHERE id = ? AND status = 'running'`,
			status, now, now, id,
		); err != nil {
			return 0, 0, fmt.Errorf("store: reclaiming stale run %d: %w", id, err)
		}

		if itemCount > 0 {
			unconfirmed++
		} else {
			reclaimed++
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("store: committing reclaim of stale runs: %w", err)
	}
	return reclaimed, unconfirmed, nil
}

// ListRuns returns a feed's runs newest-first, the same ordering the history
// page and "why is this feed stale" both want (§10).
//
// limit <= 0 means "no cap", matching ListItems's convention in items.go.
func (s *Store) ListRuns(ctx context.Context, feedID int64, limit int) ([]Run, error) {
	if limit <= 0 {
		limit = -1
	}
	rows, err := s.writer.QueryContext(ctx,
		`SELECT `+runColumns+` FROM runs WHERE feed_id = ? ORDER BY started_at DESC LIMIT ?`,
		feedID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: listing runs for feed %d: %w", feedID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scanning run: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterating runs for feed %d: %w", feedID, err)
	}
	return out, nil
}

// SpendSince sums tokens and estimated cost across runs started at or after
// since (§13's budget enforcement reads this). feedID = 0 is the global
// variant — every feed's spend — because the failure mode §13 calls out is
// "N feeds each individually within budget" but collectively not, which a
// per-feed-only query could never see.
//
// The sum includes failed runs deliberately: money already spent shows up
// here even when the run did not succeed (§22 J5).
func (s *Store) SpendSince(ctx context.Context, feedID int64, since time.Time) (tokensIn, tokensOut int, costUSD float64, err error) {
	query := `SELECT COALESCE(SUM(tokens_in), 0), COALESCE(SUM(tokens_out), 0), COALESCE(SUM(est_cost_usd), 0)
	          FROM runs WHERE started_at >= ?`
	args := []any{formatTime(since)}
	if feedID != 0 {
		query += ` AND feed_id = ?`
		args = append(args, feedID)
	}

	row := s.writer.QueryRowContext(ctx, query, args...)
	if err := row.Scan(&tokensIn, &tokensOut, &costUSD); err != nil {
		return 0, 0, 0, fmt.Errorf("store: summing spend since %s: %w", formatTime(since), err)
	}
	return tokensIn, tokensOut, costUSD, nil
}
