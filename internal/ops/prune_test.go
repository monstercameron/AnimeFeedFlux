package ops

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

func seedFeedForPrune(t *testing.T, s *store.Store, slug string) int64 {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.Writer().ExecContext(t.Context(),
		`INSERT INTO feeds (slug, title, kind, created_at, updated_at) VALUES (?, ?, 'generative', ?, ?)`,
		slug, slug, now, now)
	if err != nil {
		t.Fatalf("seed feed: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("feed id: %v", err)
	}
	return id
}

func insertItemForPrune(t *testing.T, s *store.Store, feedID int64, key string, published time.Time) int64 {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.Writer().ExecContext(t.Context(), `
		INSERT INTO items (feed_id, item_key, content_hash, title, summary_text, body_html,
		                    published_at, origin, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'summary', '<p>body</p>', ?, 'generated', ?, ?)`,
		feedID, key, key+"-hash", "Title "+key, published.UTC().Format(time.RFC3339Nano), now, now)
	if err != nil {
		t.Fatalf("insert item %s: %v", key, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("item id: %v", err)
	}
	return id
}

func insertEmbedding(t *testing.T, s *store.Store, itemID int64) {
	t.Helper()
	_, err := s.Writer().ExecContext(t.Context(),
		`INSERT INTO item_embeddings (item_id, model, dim, vec) VALUES (?, 'test-model', 2, X'0000')`,
		itemID)
	if err != nil {
		t.Fatalf("insert embedding for item %d: %v", itemID, err)
	}
}

func insertSample(t *testing.T, s *store.Store, feedID int64, expiresAt time.Time) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.Writer().ExecContext(t.Context(), `
		INSERT INTO samples (feed_id, created_at, expires_at, payload_json)
		VALUES (?, ?, ?, '{}')`,
		feedID, now, expiresAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatalf("insert sample: %v", err)
	}
}

func insertRunForPrune(t *testing.T, s *store.Store, feedID int64, startedAt time.Time, status string) int64 {
	t.Helper()
	res, err := s.Writer().ExecContext(t.Context(), `
		INSERT INTO runs (feed_id, started_at, status, trigger) VALUES (?, ?, ?, 'cron')`,
		feedID, startedAt.UTC().Format(time.RFC3339Nano), status)
	if err != nil {
		t.Fatalf("insert run: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("run id: %v", err)
	}
	return id
}

func countRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("counting %s: %v", table, err)
	}
	return n
}

func TestPruneDeletesExpiredSamplesOnly(t *testing.T) {
	s, _ := openLiveStore(t)
	feedID := seedFeedForPrune(t, s, "sample-feed")

	now := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	insertSample(t, s, feedID, now.Add(-time.Hour)) // expired
	insertSample(t, s, feedID, now.Add(time.Hour))  // not expired

	result, err := Prune(t.Context(), s.Writer(), PruneOptions{Now: now})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if result.SamplesDeleted != 1 {
		t.Errorf("SamplesDeleted = %d, want 1", result.SamplesDeleted)
	}
	if got := countRows(t, s.Writer(), "samples"); got != 1 {
		t.Errorf("samples remaining = %d, want 1", got)
	}
}

func TestPruneEmbeddingsKeepsOnlyMostRecentWindowPerFeed(t *testing.T) {
	s, _ := openLiveStore(t)
	feedA := seedFeedForPrune(t, s, "feed-a")
	feedB := seedFeedForPrune(t, s, "feed-b")

	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	// Feed A: 5 items with embeddings, window = 3 -> 2 pruned.
	for i := 0; i < 5; i++ {
		id := insertItemForPrune(t, s, feedA, fmt.Sprintf("a-%d", i), base.Add(time.Duration(i)*time.Hour))
		insertEmbedding(t, s, id)
	}
	// Feed B: 2 items with embeddings, window = 3 -> none pruned.
	for i := 0; i < 2; i++ {
		id := insertItemForPrune(t, s, feedB, fmt.Sprintf("b-%d", i), base.Add(time.Duration(i)*time.Hour))
		insertEmbedding(t, s, id)
	}

	result, err := Prune(t.Context(), s.Writer(), PruneOptions{
		Now:             base.Add(100 * time.Hour),
		EmbeddingWindow: 3,
	})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if result.EmbeddingsDeleted != 2 {
		t.Fatalf("EmbeddingsDeleted = %d, want 2", result.EmbeddingsDeleted)
	}

	if got := countRows(t, s.Writer(), "item_embeddings"); got != 5 {
		t.Errorf("item_embeddings remaining = %d, want 5 (3 from feed-a + 2 from feed-b)", got)
	}

	// The two SURVIVING feed-a embeddings must be the two most recent (a-3, a-4).
	rows, err := s.Writer().QueryContext(t.Context(), `
		SELECT i.item_key FROM item_embeddings ie JOIN items i ON i.id = ie.item_id
		WHERE i.feed_id = ? ORDER BY i.published_at`, feedA)
	if err != nil {
		t.Fatalf("query surviving embeddings: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatalf("scan: %v", err)
		}
		keys = append(keys, k)
	}
	want := []string{"a-2", "a-3", "a-4"}
	if len(keys) != len(want) {
		t.Fatalf("surviving feed-a embeddings = %v, want %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Errorf("surviving feed-a embeddings = %v, want %v", keys, want)
			break
		}
	}
}

func TestPruneNeverDeletesItems(t *testing.T) {
	s, _ := openLiveStore(t)
	feedID := seedFeedForPrune(t, s, "item-feed")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		id := insertItemForPrune(t, s, feedID, fmt.Sprintf("item-%d", i), base.Add(time.Duration(i)*24*time.Hour))
		insertEmbedding(t, s, id)
	}

	before := countRows(t, s.Writer(), "items")

	// Aggressive prune: tiny embedding window, very short run retention.
	_, err := Prune(t.Context(), s.Writer(), PruneOptions{
		Now:             base.Add(365 * 24 * time.Hour),
		EmbeddingWindow: 1,
		RunRetention:    time.Hour,
	})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}

	after := countRows(t, s.Writer(), "items")
	if after != before {
		t.Fatalf("items count changed from %d to %d — Prune must never delete items", before, after)
	}
}

func TestPruneRunsDeletesOldNonFailuresKeepsFailures(t *testing.T) {
	s, _ := openLiveStore(t)
	feedID := seedFeedForPrune(t, s, "run-feed")
	now := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	old := now.Add(-200 * 24 * time.Hour)
	recent := now.Add(-10 * 24 * time.Hour)

	insertRunForPrune(t, s, feedID, old, "success")     // pruned: old, not a failure
	insertRunForPrune(t, s, feedID, old, "failed")      // kept: failure, regardless of age
	insertRunForPrune(t, s, feedID, recent, "success")  // kept: recent
	insertRunForPrune(t, s, feedID, old, "interrupted") // pruned: old, not a failure

	result, err := Prune(t.Context(), s.Writer(), PruneOptions{
		Now:          now,
		RunRetention: 180 * 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if result.RunsDeleted != 2 {
		t.Fatalf("RunsDeleted = %d, want 2", result.RunsDeleted)
	}

	rows, err := s.Writer().QueryContext(t.Context(), `SELECT status FROM runs ORDER BY status`)
	if err != nil {
		t.Fatalf("query remaining runs: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var statuses []string
	for rows.Next() {
		var st string
		if err := rows.Scan(&st); err != nil {
			t.Fatalf("scan: %v", err)
		}
		statuses = append(statuses, st)
	}
	want := []string{"failed", "success"}
	if len(statuses) != len(want) {
		t.Fatalf("remaining run statuses = %v, want %v", statuses, want)
	}
	for i := range want {
		if statuses[i] != want[i] {
			t.Errorf("remaining run statuses = %v, want %v", statuses, want)
			break
		}
	}
}

func TestPruneDisabledStagesWhenOptionsAreZero(t *testing.T) {
	s, _ := openLiveStore(t)
	feedID := seedFeedForPrune(t, s, "noop-feed")
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	id := insertItemForPrune(t, s, feedID, "old-item", base)
	insertEmbedding(t, s, id)
	insertRunForPrune(t, s, feedID, base, "success")

	// RunRetention and EmbeddingWindow both zero: neither stage should touch
	// anything, even though every row here is ancient.
	result, err := Prune(t.Context(), s.Writer(), PruneOptions{Now: time.Now()})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if result.EmbeddingsDeleted != 0 || result.RunsDeleted != 0 {
		t.Fatalf("Prune with zero-valued options touched embeddings/runs: %+v", result)
	}
	if got := countRows(t, s.Writer(), "item_embeddings"); got != 1 {
		t.Errorf("item_embeddings = %d, want 1 (untouched)", got)
	}
	if got := countRows(t, s.Writer(), "runs"); got != 1 {
		t.Errorf("runs = %d, want 1 (untouched)", got)
	}
}
