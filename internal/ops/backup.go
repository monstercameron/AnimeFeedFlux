// Package ops is the operations layer (PLAN.md §15): nightly backup and
// restore, off-box encryption for shipped backups, feed staleness detection,
// and retention pruning. None of it runs on the request path — it exists so
// the one SQLite file that holds everything this system has ever generated
// can survive a bad day.
package ops

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver; matches internal/store's registration
)

// driverName is the name modernc.org/sqlite registers itself under.
const driverName = "sqlite"

// backupTimestamp turns "now" into a fixed-width, lexically-sortable suffix.
// Nanosecond resolution (rather than a formatted date-time) is deliberate: a
// nightly job that also runs under a fast test suite can produce two backups
// within the same wall-clock second, and a collision would silently
// overwrite the first one instead of erroring.
func backupTimestamp(t time.Time) string {
	return fmt.Sprintf("%019d", t.UTC().UnixNano())
}

// backupTimestampDigits is the width backupTimestamp always produces. Stated
// once, because retainBackups decides what to DELETE by this exact shape.
const backupTimestampDigits = 19

// isSnapshotName reports whether name is a snapshot this base owns:
// "<base>-<19 digits>.db", exactly. See retainBackups for why a prefix match
// is not good enough.
func isSnapshotName(name, base string) bool {
	prefix, suffix := base+"-", ".db"
	if len(name) != len(prefix)+backupTimestampDigits+len(suffix) {
		return false
	}
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return false
	}
	for _, r := range name[len(prefix) : len(name)-len(suffix)] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// backupBaseName derives the backup file's stem from the source database's
// filename, so multiple databases can share one outDir without colliding.
func backupBaseName(dbPath string) string {
	base := filepath.Base(dbPath)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// Backup takes a consistent, integrity-checked snapshot of the SQLite
// database at dbPath into outDir, then prunes outDir down to the keep most
// recent snapshots for this database.
//
// This is deliberately NOT a file copy. In WAL mode, committed data can live
// only in the "-wal" sidecar at any given instant, and copying the three
// files (db, -wal, -shm) copies them at three different moments — the result
// opens cleanly, passes a casual smoke test, and is missing a transaction.
// `VACUUM INTO` asks SQLite itself for one consistent point-in-time snapshot
// folded into a single ordinary file, which is what makes it correct against
// a database with an open writer. The copy is then reopened and
// PRAGMA-integrity-checked — a file that merely exists on disk is not
// verified; a file SQLite has read back and validated is.
func Backup(ctx context.Context, dbPath, outDir string, keep int) (path string, err error) {
	if dbPath == "" {
		return "", errors.New("ops: dbPath is required")
	}
	if outDir == "" {
		return "", errors.New("ops: outDir is required")
	}
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return "", fmt.Errorf("ops: creating backup dir %s: %w", outDir, err)
	}

	base := backupBaseName(dbPath)
	dest := filepath.Join(outDir, fmt.Sprintf("%s-%s.db", base, backupTimestamp(time.Now())))

	if err := vacuumInto(ctx, dbPath, dest); err != nil {
		return "", err
	}

	if err := Verify(ctx, dest); err != nil {
		_ = os.Remove(dest)
		return "", fmt.Errorf("ops: backup %s failed integrity check, removed: %w", dest, err)
	}

	if keep > 0 {
		if err := retainBackups(outDir, base, keep); err != nil {
			// The backup itself is good and verified; a pruning failure must
			// not be reported as "no backup was made".
			return dest, fmt.Errorf("ops: backup %s succeeded but pruning old backups failed: %w", dest, err)
		}
	}

	return dest, nil
}

// vacuumInto opens dbPath read-only and asks SQLite to write a single
// consistent snapshot to dest via VACUUM INTO. Read-only is safe (and
// preferred) here because VACUUM INTO only ever writes to the new file, never
// to the source — and it means this connection can never be the thing that
// blocks a concurrent writer on the live database.
func vacuumInto(ctx context.Context, dbPath, dest string) error {
	db, err := sql.Open(driverName, readOnlyDSN(dbPath))
	if err != nil {
		return fmt.Errorf("ops: opening %s for backup: %w", dbPath, err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ops: connecting to %s for backup: %w", dbPath, err)
	}

	// The destination path travels as a quoted SQL string literal, not a bind
	// parameter — VACUUM INTO's filename argument is not universally
	// supported as a placeholder across sqlite driver versions. dest is always
	// ours (backupTimestamp-derived), never attacker input, so escaping the
	// one character SQL string literals care about is sufficient.
	escaped := strings.ReplaceAll(filepath.ToSlash(dest), "'", "''")
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`VACUUM INTO '%s'`, escaped)); err != nil {
		return fmt.Errorf("ops: VACUUM INTO %s: %w", dest, err)
	}
	return nil
}

// Verify opens path read-only and runs SQLite's own integrity check against
// it. This is the check that catches a corrupted or partial file that would
// otherwise "restore, pass a smoke test, and be wrong" (PLAN.md §15).
func Verify(ctx context.Context, path string) error {
	db, err := sql.Open(driverName, readOnlyDSN(path))
	if err != nil {
		return fmt.Errorf("ops: opening %s for verification: %w", path, err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	var result string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&result); err != nil {
		return fmt.Errorf("ops: integrity_check on %s: %w", path, err)
	}
	if !strings.EqualFold(result, "ok") {
		return fmt.Errorf("ops: integrity_check on %s reported: %s", path, result)
	}
	return nil
}

// Restore copies a verified backup into place at destPath, atomically (write
// to a temp file, then rename) so a crash mid-restore leaves the previous
// destPath untouched rather than a half-written database. It refuses to
// restore a backup that fails Verify, and re-verifies the copy at destPath
// afterward — the destination is the thing that will actually be opened.
func Restore(ctx context.Context, backupPath, destPath string) error {
	if err := Verify(ctx, backupPath); err != nil {
		return fmt.Errorf("ops: refusing to restore unverified backup %s: %w", backupPath, err)
	}

	tmp := destPath + ".restoring"
	if err := copyFile(backupPath, tmp); err != nil {
		return fmt.Errorf("ops: copying %s to %s: %w", backupPath, tmp, err)
	}

	if err := os.Rename(tmp, destPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("ops: finalizing restore to %s: %w", destPath, err)
	}

	// A backup produced by VACUUM INTO is a single self-contained file with
	// no WAL of its own. Any -wal/-shm left beside a PREVIOUS database at
	// destPath must not be allowed to shadow the restored data on next open.
	//
	// AFTER the rename, never before. This ran first, which made the promise
	// in this function's doc comment false: a rename that failed left the
	// previous database's main file in place with its write-ahead log
	// deleted. In WAL mode a committed transaction can live only in that
	// sidecar until it is checkpointed, so removing it early does not tidy
	// anything — it discards recent committed data from the database the
	// operator was trying to protect, during the operation they reached for
	// because something had already gone wrong.
	//
	// Ordering it here narrows the exposure to the window between a
	// SUCCEEDED rename and these two removals, where the old sidecars sit
	// beside the new file. The Verify below opens destPath afterwards and is
	// what catches that state if the process dies inside it.
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(destPath + suffix)
	}

	if err := Verify(ctx, destPath); err != nil {
		return fmt.Errorf("ops: restored %s failed integrity check: %w", destPath, err)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// retainBackups keeps the `keep` most recent snapshots for base in outDir and
// removes the rest. Filenames sort lexically in creation order because
// backupTimestamp is fixed-width, so a plain reverse string sort is exact —
// no need to stat or parse anything.
//
// # What counts as "mine"
//
// The glob alone does not answer that, and getting it wrong here deletes
// backups. `base+"-*.db"` looks specific and is not: for base "aff" the
// wildcard also swallows "test-<timestamp>", so every backup of a database
// called aff-test matched too — and backupBaseName exists precisely so
// "multiple databases can share one outDir without colliding".
//
// The miscount was not the damage. Names sort reverse-lexically and 't' > '0',
// so the foreign aff-test files sorted ahead of every genuine aff backup:
// with keep=2, both survivors were aff-test's and ALL THREE real aff backups
// were deleted. The routine whose job is retaining backups removed exactly
// the ones that mattered, and reported success.
//
// So membership is checked rather than assumed: a snapshot is
// "<base>-<19 digits>.db" and nothing else. That also excludes a
// hand-renamed or half-written file someone left in the directory — likely
// the very thing they are mid-recovery on — and it restores the fixed-width
// premise the reverse sort above depends on, which only holds if every
// element really is a timestamp for this base.
func retainBackups(outDir, base string, keep int) error {
	globbed, err := filepath.Glob(filepath.Join(outDir, base+"-*.db"))
	if err != nil {
		return fmt.Errorf("ops: listing backups in %s: %w", outDir, err)
	}
	matches := make([]string, 0, len(globbed))
	for _, m := range globbed {
		if isSnapshotName(filepath.Base(m), base) {
			matches = append(matches, m)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(matches)))

	var errs []error
	for i, m := range matches {
		if i < keep {
			continue
		}
		if err := os.Remove(m); err != nil {
			errs = append(errs, fmt.Errorf("removing %s: %w", m, err))
		}
	}
	return errors.Join(errs...)
}

func readOnlyDSN(path string) string {
	q := url.Values{}
	q.Set("mode", "ro")
	q.Add("_pragma", "busy_timeout(5000)")
	return "file:" + filepath.ToSlash(path) + "?" + q.Encode()
}
