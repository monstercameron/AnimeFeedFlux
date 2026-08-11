package ops

import (
	"os"
	"path/filepath"
	"testing"
)

// Restore promises that "a crash mid-restore leaves the previous destPath
// untouched rather than a half-written database". The .db file was untouched;
// its WAL was not.
//
// The -wal/-shm removal ran BEFORE the rename, so a rename that failed left
// the previous database's main file in place with its write-ahead log deleted.
// In WAL mode a committed transaction can live only in that sidecar until it
// is checkpointed, so this is not housekeeping — it silently discards recent
// committed data from the database the operator was trying to protect, during
// the operation they reached for BECAUSE something was already wrong.
//
// The rename is forced to fail here by making destPath a directory, which is
// the cheapest deterministic stand-in for the real cases (a full disk, a
// permission error, a cross-device link).
func TestRestoreLeavesThePreviousWALAloneWhenItCannotFinish(t *testing.T) {
	s, livePath := openLiveStore(t)
	seedItems(t, s, 3)

	backupDir := t.TempDir()
	backupPath, err := Backup(t.Context(), livePath, backupDir, 5)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}

	// A destination the rename cannot overwrite, standing in for any failure
	// after the sidecars would have been removed.
	dir := t.TempDir()
	destPath := filepath.Join(dir, "restore-target.db")
	if err := os.Mkdir(destPath, 0o700); err != nil {
		t.Fatalf("creating blocking directory: %v", err)
	}

	walPath := destPath + "-wal"
	shmPath := destPath + "-shm"
	const walContents = "committed-but-not-yet-checkpointed"
	if err := os.WriteFile(walPath, []byte(walContents), 0o600); err != nil {
		t.Fatalf("writing wal: %v", err)
	}
	if err := os.WriteFile(shmPath, []byte("shm"), 0o600); err != nil {
		t.Fatalf("writing shm: %v", err)
	}

	if err := Restore(t.Context(), backupPath, destPath); err == nil {
		t.Fatal("Restore reported success despite being unable to finalize")
	}

	got, err := os.ReadFile(walPath)
	if err != nil {
		t.Fatalf("THE PREVIOUS DATABASE'S WAL WAS DELETED BY A RESTORE THAT FAILED: %v — "+
			"every transaction committed since the last checkpoint is gone", err)
	}
	if string(got) != walContents {
		t.Errorf("wal contents = %q, want %q", got, walContents)
	}
	if _, err := os.Stat(shmPath); err != nil {
		t.Errorf("the previous database's -shm was deleted by a failed restore: %v", err)
	}
}

// The sidecars must still be gone after a restore that SUCCEEDS: a -wal left
// beside the newly restored file belongs to the old database and would shadow
// the restored data on next open, which is the reason the removal exists.
func TestRestoreRemovesStaleSidecarsWhenItSucceeds(t *testing.T) {
	s, livePath := openLiveStore(t)
	seedItems(t, s, 2)

	backupPath, err := Backup(t.Context(), livePath, t.TempDir(), 5)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}

	destPath := filepath.Join(t.TempDir(), "restored.db")
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.WriteFile(destPath+suffix, []byte("stale"), 0o600); err != nil {
			t.Fatalf("writing %s: %v", suffix, err)
		}
	}

	if err := Restore(t.Context(), backupPath, destPath); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(destPath + suffix); !os.IsNotExist(err) {
			t.Errorf("stale %s survived a successful restore and will shadow the restored data", suffix)
		}
	}
}
