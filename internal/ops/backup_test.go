package ops

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// openLiveStore builds a real, migrated WAL database and returns both the
// *store.Store (whose writer connection stays open for the whole test, so
// Backup runs against a database with a live writer, matching the failure
// mode PLAN.md §15 warns about) and its path.
func openLiveStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "aff.db")
	s, err := store.Open(t.Context(), store.Options{Path: path})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s, path
}

func seedItems(t *testing.T, s *store.Store, n int) int64 {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.Writer().ExecContext(t.Context(),
		`INSERT INTO feeds (slug, title, kind, created_at, updated_at) VALUES (?, ?, 'generative', ?, ?)`,
		"live-feed", "Live Feed", now, now)
	if err != nil {
		t.Fatalf("seed feed: %v", err)
	}
	feedID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("feed id: %v", err)
	}
	base := time.Now().UTC()
	for i := 0; i < n; i++ {
		published := base.Add(time.Duration(i) * time.Second).Format(time.RFC3339Nano)
		key := fmt.Sprintf("item-%03d", i)
		if _, err := s.Writer().ExecContext(t.Context(), `
			INSERT INTO items (feed_id, item_key, content_hash, title, summary_text, body_html,
			                    published_at, origin, created_at, updated_at)
			VALUES (?, ?, ?, ?, 'summary', '<p>body</p>', ?, 'generated', ?, ?)`,
			feedID, key, key+"-hash", "Title "+key, published, now, now,
		); err != nil {
			t.Fatalf("insert item %d: %v", i, err)
		}
	}
	return feedID
}

// itemsChecksum hashes every row of the items table in a stable order, so
// two databases with the same logical content produce the same digest
// regardless of file layout.
func itemsChecksum(t *testing.T, ctx context.Context, db *sql.DB) (count int, checksum string) {
	t.Helper()
	rows, err := db.QueryContext(ctx, `
		SELECT item_key, content_hash, title, summary_text, published_at
		FROM items ORDER BY item_key`)
	if err != nil {
		t.Fatalf("query items: %v", err)
	}
	defer func() { _ = rows.Close() }()

	h := sha256.New()
	for rows.Next() {
		var key, hash, title, summary, published string
		if err := rows.Scan(&key, &hash, &title, &summary, &published); err != nil {
			t.Fatalf("scan item: %v", err)
		}
		fmt.Fprintf(h, "%s|%s|%s|%s|%s\n", key, hash, title, summary, published)
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate items: %v", err)
	}
	return count, hex.EncodeToString(h.Sum(nil))
}

func openRO(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open(driverName, readOnlyDSN(path))
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestBackupOfLiveWALDatabaseRestoresByteIdenticalLogicalState(t *testing.T) {
	s, dbPath := openLiveStore(t)
	seedItems(t, s, 25)
	// The writer stays open (s.Close is deferred to test cleanup) — Backup
	// must still produce a correct snapshot against a database with an open
	// writer and, in WAL mode, uncheckpointed data in -wal.

	wantCount, wantChecksum := itemsChecksum(t, t.Context(), s.Writer())

	outDir := t.TempDir()
	backupPath, err := Backup(t.Context(), dbPath, outDir, 14)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}

	destPath := filepath.Join(t.TempDir(), "restored.db")
	if err := Restore(t.Context(), backupPath, destPath); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	restored := openRO(t, destPath)
	gotCount, gotChecksum := itemsChecksum(t, t.Context(), restored)

	if gotCount != wantCount {
		t.Errorf("restored item count = %d, want %d", gotCount, wantCount)
	}
	if gotChecksum != wantChecksum {
		t.Errorf("restored items checksum = %s, want %s", gotChecksum, wantChecksum)
	}
}

func TestVerifyCatchesCorruptedCopy(t *testing.T) {
	s, dbPath := openLiveStore(t)
	seedItems(t, s, 3)

	outDir := t.TempDir()
	backupPath, err := Backup(t.Context(), dbPath, outDir, 14)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}

	// A good backup verifies clean.
	if err := Verify(t.Context(), backupPath); err != nil {
		t.Fatalf("Verify on a good backup: %v", err)
	}

	// Deliberately corrupt a copy: truncate it mid-file, which SQLite's
	// integrity check must catch even though the file still "opens".
	data, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	corruptPath := filepath.Join(outDir, "corrupt.db")
	if len(data) < 200 {
		t.Fatalf("backup file too small to meaningfully corrupt: %d bytes", len(data))
	}
	if err := os.WriteFile(corruptPath, data[:len(data)/2], 0o600); err != nil {
		t.Fatalf("write corrupt copy: %v", err)
	}

	if err := Verify(t.Context(), corruptPath); err == nil {
		t.Fatal("Verify on a truncated/corrupted copy: want error, got nil")
	}
}

func TestRestoreRefusesAnUnverifiedBackup(t *testing.T) {
	dir := t.TempDir()
	badBackup := filepath.Join(dir, "bad.db")
	if err := os.WriteFile(badBackup, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatalf("write bad backup: %v", err)
	}
	dest := filepath.Join(dir, "dest.db")
	if err := Restore(t.Context(), badBackup, dest); err == nil {
		t.Fatal("Restore of an invalid backup: want error, got nil")
	}
	if _, err := os.Stat(dest); err == nil {
		t.Fatal("Restore of an invalid backup must not create the destination file")
	}
}

func TestBackupRetentionPrunesOldestFirst(t *testing.T) {
	s, dbPath := openLiveStore(t)
	seedItems(t, s, 1)

	outDir := t.TempDir()
	const total = 5
	const keep = 3

	var paths []string
	for i := 0; i < total; i++ {
		p, err := Backup(t.Context(), dbPath, outDir, keep)
		if err != nil {
			t.Fatalf("Backup #%d: %v", i, err)
		}
		paths = append(paths, p)
	}

	matches, err := filepath.Glob(filepath.Join(outDir, backupBaseName(dbPath)+"-*.db"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) != keep {
		t.Fatalf("backups remaining = %d, want %d", len(matches), keep)
	}

	// The most recent `keep` backups (the last ones created) must be the
	// ones that survived; the earliest ones must be gone.
	remaining := map[string]bool{}
	for _, m := range matches {
		remaining[m] = true
	}
	for i, p := range paths {
		shouldSurvive := i >= total-keep
		if remaining[p] != shouldSurvive {
			t.Errorf("backup #%d (%s) survived=%v, want %v", i, p, remaining[p], shouldSurvive)
		}
	}
}
