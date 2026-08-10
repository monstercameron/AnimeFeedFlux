// item_embeddings persistence: the write side of the §9 step 5 novelty gate.
//
// internal/novelty only ever READS vectors (via whatever loads its Gate's
// comparison corpus); nothing in this package wrote them until this file, so
// the gate always compared candidates against an empty corpus and always
// passed. See runs.go's CommitRun for how a row here is tied to the item
// commit that produced it.
package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/monstercameron/AnimeFeedFlux/internal/novelty"
)

// ItemEmbedding pairs an item — identified by its ItemKey, matching one of
// the model.Item values passed to CommitRun in the same call — with the
// novelty vector to persist for it. Keyed by ItemKey rather than a
// database id because the id does not exist yet when the caller builds this
// value: CommitRun assigns it during the insert.
type ItemEmbedding struct {
	ItemKey string
	Vector  novelty.Vector
}

// insertItemEmbedding writes one item_embeddings row for itemID inside tx.
//
// This is called ONLY from within CommitRun's transaction (never on its
// own writer handle) so that an embedding can never survive a rolled-back
// item insert. §9 is explicit that items, their embeddings, and the closed
// run row commit atomically together — an embedding that outlived its item
// would let a later novelty check compare a real candidate against a vector
// for an item that no longer exists, which is not "no signal", it is a
// wrong signal.
func insertItemEmbedding(ctx context.Context, tx *sql.Tx, itemID int64, v novelty.Vector) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO item_embeddings (item_id, model, dim, vec) VALUES (?, ?, ?, ?)`,
		itemID, v.Model, v.Dim, novelty.Encode(v.Vec),
	); err != nil {
		return fmt.Errorf("store: inserting embedding for item %d: %w", itemID, err)
	}
	return nil
}

// EmbeddingsForFeed loads feedID's comparison corpus for the novelty gate:
// the `limit` most recently published items' embeddings, newest first. This
// is the read side that makes CommitRun's writes actually reachable —
// without it, a write path with no matching read path is the same species
// of gap as no write path at all, just with the silence moved one hop over.
//
// limit <= 0 means "no cap", matching ListItems's and ListRuns's convention.
func (s *Store) EmbeddingsForFeed(ctx context.Context, feedID int64, limit int) ([]novelty.Vector, error) {
	if limit <= 0 {
		limit = -1
	}
	rows, err := s.writer.QueryContext(ctx, `
		SELECT items.item_key, item_embeddings.model, item_embeddings.dim, item_embeddings.vec
		FROM item_embeddings
		JOIN items ON items.id = item_embeddings.item_id
		WHERE items.feed_id = ?
		ORDER BY items.published_at DESC
		LIMIT ?`, feedID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: loading embeddings for feed %d: %w", feedID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []novelty.Vector
	for rows.Next() {
		var (
			v   novelty.Vector
			vec []byte
		)
		if err := rows.Scan(&v.ID, &v.Model, &v.Dim, &vec); err != nil {
			return nil, fmt.Errorf("store: scanning embedding for feed %d: %w", feedID, err)
		}
		v.Vec, err = novelty.Decode(vec)
		if err != nil {
			return nil, fmt.Errorf("store: decoding embedding for feed %d: %w", feedID, err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterating embeddings for feed %d: %w", feedID, err)
	}
	return out, nil
}
