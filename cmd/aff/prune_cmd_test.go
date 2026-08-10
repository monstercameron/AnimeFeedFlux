package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// seedPruneFixture builds a feed with one expired sample, one old
// non-failure run, and some items+embeddings — enough for every prune
// stage to have something to report.
func seedPruneFixture(t *testing.T, dbPath string) {
	t.Helper()
	st, err := store.Open(t.Context(), store.Options{Path: dbPath})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	if _, err := st.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := st.Writer().ExecContext(t.Context(),
		`INSERT INTO feeds (slug, title, kind, created_at, updated_at) VALUES ('f', 'F', 'generative', ?, ?)`,
		now, now)
	if err != nil {
		t.Fatalf("seed feed: %v", err)
	}
	feedID, _ := res.LastInsertId()

	if _, err := st.Writer().ExecContext(t.Context(),
		`INSERT INTO samples (feed_id, created_at, expires_at, payload_json) VALUES (?, ?, ?, '{}')`,
		feedID, now, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("seed sample: %v", err)
	}

	old := time.Now().Add(-200 * 24 * time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := st.Writer().ExecContext(t.Context(),
		`INSERT INTO runs (feed_id, started_at, status, trigger) VALUES (?, ?, 'success', 'cron')`,
		feedID, old); err != nil {
		t.Fatalf("seed run: %v", err)
	}

	if _, err := st.Writer().ExecContext(t.Context(), `
		INSERT INTO items (feed_id, item_key, content_hash, title, summary_text, body_html,
		                    published_at, origin, created_at, updated_at)
		VALUES (?, 'item-1', 'hash-1', 'Title', 'summary', '<p>body</p>', ?, 'generated', ?, ?)`,
		feedID, now, now, now); err != nil {
		t.Fatalf("seed item: %v", err)
	}
}

func countTable(t *testing.T, dbPath, table string) int {
	t.Helper()
	st, err := store.Open(t.Context(), store.Options{Path: dbPath})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	var n int
	if err := st.Writer().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("counting %s: %v", table, err)
	}
	return n
}

func TestPruneDefaultIsDryRunAndDeletesNothing(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "aff.db")
	seedPruneFixture(t, dbPath)

	before := map[string]int{
		"samples": countTable(t, dbPath, "samples"),
		"runs":    countTable(t, dbPath, "runs"),
		"items":   countTable(t, dbPath, "items"),
	}

	a, stdout, stderr := newOpsTestApp(t, dbPath)
	code := a.run([]string{"prune"})
	if code != exitOK {
		t.Fatalf("aff prune (default dry-run): exit %d, want %d (stderr: %s)", code, exitOK, stderr.String())
	}
	if !strings.Contains(stdout.String(), "DRY RUN") {
		t.Fatalf("stdout = %q, want a DRY RUN heading by default", stdout.String())
	}
	if !strings.Contains(stdout.String(), "samples:     1") {
		t.Fatalf("stdout = %q, want an accurate samples count of 1", stdout.String())
	}
	if !strings.Contains(stdout.String(), "runs:        1") {
		t.Fatalf("stdout = %q, want an accurate runs count of 1", stdout.String())
	}

	for table, want := range before {
		if got := countTable(t, dbPath, table); got != want {
			t.Errorf("%s count changed from %d to %d — default `aff prune` must never delete", table, want, got)
		}
	}
}

// TestPruneRefusesToDeleteWithoutConfirmation is the explicit assertion the
// task asks for on the destructive path.
func TestPruneRefusesToDeleteWithoutConfirmation(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "aff.db")
	seedPruneFixture(t, dbPath)
	before := countTable(t, dbPath, "samples")

	a, _, stderr := newOpsTestApp(t, dbPath)
	a.Stdin = strings.NewReader("n\n")
	code := a.run([]string{"prune", "--dry-run=false"})
	if code != exitFail {
		t.Fatalf("aff prune --dry-run=false without confirmation: exit %d, want %d (stderr: %s)",
			code, exitFail, stderr.String())
	}
	if !strings.Contains(stderr.String(), "aborted") {
		t.Fatalf("stderr = %q, want an aborted message", stderr.String())
	}
	if got := countTable(t, dbPath, "samples"); got != before {
		t.Fatalf("samples count changed from %d to %d despite a refused confirmation", before, got)
	}
}

func TestPruneDeletesWithYesAndNeverTouchesItems(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "aff.db")
	seedPruneFixture(t, dbPath)
	itemsBefore := countTable(t, dbPath, "items")

	a, stdout, stderr := newOpsTestApp(t, dbPath)
	code := a.run([]string{"prune", "--dry-run=false", "--yes"})
	if code != exitOK {
		t.Fatalf("aff prune --dry-run=false --yes: exit %d, want %d (stderr: %s)", code, exitOK, stderr.String())
	}
	if !strings.Contains(stdout.String(), "samples:     1") {
		t.Fatalf("stdout = %q, want the deleted sample count reported", stdout.String())
	}
	if got := countTable(t, dbPath, "samples"); got != 0 {
		t.Fatalf("samples remaining = %d, want 0 after confirmed prune", got)
	}
	if got := countTable(t, dbPath, "items"); got != itemsBefore {
		t.Fatalf("items count changed from %d to %d — aff prune must never delete items", itemsBefore, got)
	}
}
