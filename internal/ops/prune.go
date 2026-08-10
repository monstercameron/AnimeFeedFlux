// The nightly retention job (PLAN.md §15, §7). Three things are pruned and
// nothing else: expired samples, embeddings past the novelty comparison
// window, and old non-failure runs. Items are never in this list — they are
// the archive, permalinks must resolve forever (§5.1, §12.4), and a
// PurgeDeleted-shaped function was deliberately cut from an earlier draft for
// exactly that reason (PLAN.md line ~941). Do not add item deletion here.
package ops

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Defaults mirror the numbers named elsewhere in PLAN.md: 180 days for run
// retention (§15's "Data growth"), and 500 items for the novelty comparison
// window (§8's "novelty window of 500 items").
const (
	DefaultRunRetention    = 180 * 24 * time.Hour
	DefaultEmbeddingWindow = 500
)

// PruneOptions configures one Prune pass. A zero value for RunRetention or
// EmbeddingWindow disables that stage rather than pruning everything or
// nothing by surprise — an explicit opt-in, not a trap for an unset field.
type PruneOptions struct {
	Now time.Time

	// RunRetention: runs.started_at older than this are deleted, EXCEPT
	// status = 'failed'. Zero disables run pruning.
	RunRetention time.Duration

	// EmbeddingWindow: per feed, only the EmbeddingWindow most recent items'
	// embeddings are kept (ordered by published_at DESC); the rest are
	// deleted. Zero or negative disables embedding pruning.
	EmbeddingWindow int
}

// PruneResult reports what one pass actually removed, for the nightly job's
// canonical log line (PLAN.md §15.0: one wide event, not a running
// commentary).
type PruneResult struct {
	SamplesDeleted    int64
	EmbeddingsDeleted int64
	RunsDeleted       int64
}

// Prune deletes expired samples, over-window embeddings, and old
// non-failure runs from db in three separate statements — not one
// transaction, deliberately: these three deletions are independent of each
// other, and a corruption or lock contention on one must not block the
// other two from doing their (unrelated) job.
func Prune(ctx context.Context, db *sql.DB, opts PruneOptions) (PruneResult, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}

	var result PruneResult

	samplesDeleted, err := pruneExpiredSamples(ctx, db, now)
	if err != nil {
		return result, err
	}
	result.SamplesDeleted = samplesDeleted

	if opts.EmbeddingWindow > 0 {
		embeddingsDeleted, err := pruneEmbeddings(ctx, db, opts.EmbeddingWindow)
		if err != nil {
			return result, err
		}
		result.EmbeddingsDeleted = embeddingsDeleted
	}

	if opts.RunRetention > 0 {
		runsDeleted, err := pruneOldRuns(ctx, db, now.Add(-opts.RunRetention))
		if err != nil {
			return result, err
		}
		result.RunsDeleted = runsDeleted
	}

	return result, nil
}

// pruneExpiredSamples deletes samples rows past their expires_at. Sampling
// is meant to be a cheap loop run often (§14), and expires_at filtering
// alone at read time would let the table grow forever without this.
func pruneExpiredSamples(ctx context.Context, db *sql.DB, now time.Time) (int64, error) {
	res, err := db.ExecContext(ctx,
		`DELETE FROM samples WHERE expires_at < ?`,
		now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("ops: pruning expired samples: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("ops: rows affected pruning samples: %w", err)
	}
	return n, nil
}

// pruneEmbeddings keeps only the `window` most recent (by the owning item's
// published_at) embeddings per feed and deletes the rest. It never touches
// `items` — only item_embeddings — so a pruned embedding leaves its item
// fully intact; the item just stops being compared against for novelty.
func pruneEmbeddings(ctx context.Context, db *sql.DB, window int) (int64, error) {
	res, err := db.ExecContext(ctx, `
		DELETE FROM item_embeddings
		WHERE item_id IN (
			SELECT item_id FROM (
				SELECT ie.item_id AS item_id,
				       ROW_NUMBER() OVER (
				           PARTITION BY i.feed_id
				           ORDER BY i.published_at DESC
				       ) AS rn
				FROM item_embeddings ie
				JOIN items i ON i.id = ie.item_id
			)
			WHERE rn > ?
		)`, window)
	if err != nil {
		return 0, fmt.Errorf("ops: pruning embeddings beyond window %d: %w", window, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("ops: rows affected pruning embeddings: %w", err)
	}
	return n, nil
}

// pruneOldRuns deletes runs started before cutoff, except failures — a
// failure is diagnostic history (PLAN.md §22 J5) and is kept regardless of
// age; only successful/skipped/interrupted noise ages out.
func pruneOldRuns(ctx context.Context, db *sql.DB, cutoff time.Time) (int64, error) {
	res, err := db.ExecContext(ctx,
		`DELETE FROM runs WHERE started_at < ? AND status != 'failed'`,
		cutoff.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("ops: pruning old runs: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("ops: rows affected pruning runs: %w", err)
	}
	return n, nil
}
