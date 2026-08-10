package store

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	ctx := t.Context()
	s, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "aff.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

// seedFeed inserts a feed and returns its id. Tests that care about items need
// one, and repeating the insert everywhere is how column changes become a
// twenty-file edit.
func seedFeed(t *testing.T, s *Store, slug string) int64 {
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

func insertItem(t *testing.T, s *Store, feedID int64, key, hash, title, published string) error {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.Writer().ExecContext(t.Context(),
		`INSERT INTO items (feed_id, item_key, content_hash, title, summary_text, body_html,
		                    published_at, origin, created_at, updated_at)
		 VALUES (?, ?, ?, ?, 'summary', '<p>body</p>', ?, 'generated', ?, ?)`,
		feedID, key, hash, title, published, now, now)
	return err
}

func TestOpenSetsPragmas(t *testing.T) {
	// A DSN typo silently leaves the default in place, and neither WAL nor
	// foreign keys being off is visible until much later.
	s := openTemp(t)

	var journal string
	if err := s.Writer().QueryRowContext(t.Context(), `PRAGMA journal_mode`).Scan(&journal); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if !strings.EqualFold(journal, "wal") {
		t.Errorf("journal_mode = %q, want wal", journal)
	}

	var fk int
	if err := s.Writer().QueryRowContext(t.Context(), `PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatalf("foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Error("foreign_keys is off")
	}
}

func TestColdBootCreatesWALSidecarsBeforeReaderAttaches(t *testing.T) {
	// The §2 boot-order claim. A read-only connection to a WAL database still
	// needs the -shm wal-index, so opening the reader first on a fresh file
	// fails with an error naming the wrong problem.
	dir := t.TempDir()
	path := filepath.Join(dir, "cold.db")

	s, err := Open(t.Context(), Options{Path: path})
	if err != nil {
		t.Fatalf("cold boot failed: %v", err)
	}
	defer func() { _ = s.Close() }()

	if _, err := os.Stat(path + "-wal"); err != nil {
		t.Errorf("-wal not created before reader attached: %v", err)
	}
	if _, err := os.Stat(path + "-shm"); err != nil {
		t.Errorf("-shm not created before reader attached: %v", err)
	}

	// And the reader genuinely works against it.
	if err := s.Reader().PingContext(t.Context()); err != nil {
		t.Errorf("reader ping: %v", err)
	}
}

func TestReaderRejectsWrites(t *testing.T) {
	// Proves the §2 claim rather than assuming it: the publish plane cannot
	// corrupt the database because its handle has no writer.
	s := openTemp(t)

	// The interface itself exposes no Exec. Reaching the underlying *sql.DB is
	// the only way to try, and even then the connection is mode=ro.
	db, ok := s.Reader().(*sql.DB)
	if !ok {
		t.Fatal("reader is not a *sql.DB; adjust this test, do not delete it")
	}

	_, err := db.ExecContext(t.Context(), `CREATE TABLE should_not_exist (id INTEGER)`)
	if err == nil {
		t.Fatal("read-only handle accepted a write")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "read") {
		t.Logf("write rejected with: %v", err) // message varies; rejection is what matters
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	ctx := t.Context()
	s, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "m.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()

	first, err := s.Migrate(ctx)
	if err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if first == 0 {
		t.Fatal("first migrate applied nothing")
	}

	second, err := s.Migrate(ctx)
	if err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if second != 0 {
		t.Errorf("second migrate applied %d migrations, want 0", second)
	}
}

func TestMigrateRecordsEveryVersion(t *testing.T) {
	s := openTemp(t)

	var count int
	if err := s.Writer().QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	all, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	if count != len(all) {
		t.Errorf("recorded %d migrations, want %d", count, len(all))
	}
}

func TestMigrationFilesAreWellFormed(t *testing.T) {
	all, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("no migrations embedded — the binary would ship without a schema")
	}
	for i, m := range all {
		if i > 0 && m.version <= all[i-1].version {
			t.Errorf("migrations out of order at %d: %d after %d", i, m.version, all[i-1].version)
		}
		if strings.TrimSpace(m.sql) == "" {
			t.Errorf("migration %d (%s) is empty", m.version, m.name)
		}
	}
}

func TestContentHashUniquenessGivesIdempotency(t *testing.T) {
	// §5.1: a repeated run must not add the same item twice. Identity is the
	// ULID; this constraint is only about not duplicating content.
	s := openTemp(t)
	feed := seedFeed(t, s, "trivia")

	if err := insertItem(t, s, feed, "key-1", "hash-a", "Question one", "2026-08-09T12:00:00Z"); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	err := insertItem(t, s, feed, "key-2", "hash-a", "Question one again", "2026-08-09T12:00:01Z")
	if err == nil {
		t.Fatal("the same content_hash was inserted twice into one feed")
	}
}

func TestPublishedAtUniquenessIsEnforced(t *testing.T) {
	// Slack drops items sharing a timestamp, silently (§5.5). This is the
	// constraint that makes that impossible rather than merely discouraged.
	s := openTemp(t)
	feed := seedFeed(t, s, "news")

	if err := insertItem(t, s, feed, "k1", "h1", "One", "2026-08-09T12:00:00Z"); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if err := insertItem(t, s, feed, "k2", "h2", "Two", "2026-08-09T12:00:00Z"); err == nil {
		t.Fatal("two items in one feed shared a published_at")
	}

	// A different feed at the same instant is fine — the constraint is per feed.
	other := seedFeed(t, s, "facts")
	if err := insertItem(t, s, other, "k3", "h3", "Three", "2026-08-09T12:00:00Z"); err != nil {
		t.Errorf("same timestamp in a different feed was rejected: %v", err)
	}
}

func TestGuidKeyIsStableAcrossATitleEdit(t *testing.T) {
	// The whole reason item_key is an opaque ULID rather than a content hash
	// (§5.1). If this ever fails, every edited item resurfaces as a duplicate
	// in every subscriber's inbox.
	s := openTemp(t)
	feed := seedFeed(t, s, "trivia")

	if err := insertItem(t, s, feed, "stable-key", "hash-1", "Original title", "2026-08-09T12:00:00Z"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if _, err := s.Writer().ExecContext(t.Context(),
		`UPDATE items SET title = ?, edited_at = ? WHERE item_key = ?`,
		"Corrected title", time.Now().UTC().Format(time.RFC3339Nano), "stable-key"); err != nil {
		t.Fatalf("update: %v", err)
	}

	var key, title string
	if err := s.Writer().QueryRowContext(t.Context(),
		`SELECT item_key, title FROM items WHERE id = (SELECT MAX(id) FROM items)`).Scan(&key, &title); err != nil {
		t.Fatalf("select: %v", err)
	}
	if key != "stable-key" {
		t.Errorf("item_key changed on edit: %q", key)
	}
	if title != "Corrected title" {
		t.Errorf("title did not change: %q", title)
	}
}

func TestFTS5IsAvailableAndStaysInSync(t *testing.T) {
	// A1-02: FTS5 must actually be compiled into the driver. If this fails, the
	// driver choice is wrong, not the query.
	s := openTemp(t)
	feed := seedFeed(t, s, "trivia")
	ctx := t.Context()

	if err := insertItem(t, s, feed, "k1", "h1", "Cowboy Bebop soundtrack", "2026-08-09T12:00:00Z"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	count := func(q string) int {
		t.Helper()
		var n int
		if err := s.Writer().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM items_fts WHERE items_fts MATCH ?`, q).Scan(&n); err != nil {
			t.Fatalf("fts query %q: %v", q, err)
		}
		return n
	}

	if got := count("Bebop"); got != 1 {
		t.Fatalf("insert trigger did not index the row: matches = %d", got)
	}

	if _, err := s.Writer().ExecContext(ctx,
		`UPDATE items SET title = 'Serial Experiments Lain' WHERE item_key = 'k1'`); err != nil {
		t.Fatalf("update: %v", err)
	}
	if got := count("Bebop"); got != 0 {
		t.Errorf("stale term still indexed after update: matches = %d", got)
	}
	if got := count("Lain"); got != 1 {
		t.Errorf("new term not indexed after update: matches = %d", got)
	}

	if _, err := s.Writer().ExecContext(ctx, `DELETE FROM items WHERE item_key = 'k1'`); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got := count("Lain"); got != 0 {
		t.Errorf("term still indexed after delete: matches = %d", got)
	}
}

func TestForeignKeysCascade(t *testing.T) {
	s := openTemp(t)
	feed := seedFeed(t, s, "trivia")
	if err := insertItem(t, s, feed, "k1", "h1", "One", "2026-08-09T12:00:00Z"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if _, err := s.Writer().ExecContext(t.Context(), `DELETE FROM feeds WHERE id = ?`, feed); err != nil {
		t.Fatalf("delete feed: %v", err)
	}

	var n int
	if err := s.Writer().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM items`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("%d orphaned items survived the feed deletion", n)
	}
}

func TestRunTriggerIsConstrained(t *testing.T) {
	// §10: only cron and manual produce runs. Sampling writes a samples row.
	s := openTemp(t)
	feed := seedFeed(t, s, "trivia")

	_, err := s.Writer().ExecContext(t.Context(),
		`INSERT INTO runs (feed_id, started_at, status, trigger) VALUES (?, ?, 'running', 'sample')`,
		feed, time.Now().UTC().Format(time.RFC3339Nano))
	if err == nil {
		t.Fatal("trigger 'sample' was accepted; only cron and manual exist")
	}
}

func TestOpenRequiresAPath(t *testing.T) {
	if _, err := Open(t.Context(), Options{}); err == nil {
		t.Fatal("Open succeeded with no path")
	}
}

func TestCloseIsSafeAndCheckpoints(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.db")
	s, err := Open(t.Context(), Options{Path: path})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := s.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// After a TRUNCATE checkpoint the WAL should be empty, so a copy of the .db
	// alone is not missing transactions.
	if fi, err := os.Stat(path + "-wal"); err == nil && fi.Size() > 0 {
		t.Errorf("WAL is %d bytes after close; checkpoint did not truncate", fi.Size())
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stat wal: %v", err)
	}
}
