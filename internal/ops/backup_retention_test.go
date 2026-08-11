package ops

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// retainBackups DELETES files, so what it considers "mine" has to be exact.
// backupBaseName's doc promises "multiple databases can share one outDir
// without colliding", and the retention glob is base+"-*.db" — which for base
// "aff" also matches every backup of a database called aff-test, because the
// wildcard happily swallows "test-<timestamp>".
//
// That is not a cosmetic miscount. Names sort reverse-lexically and 't' > '0',
// so the foreign aff-test files sort ahead of every genuine aff backup: with
// keep=2 the two survivors are both aff-test's, and all three real aff
// backups are deleted. The one routine whose job is preserving backups
// deletes exactly the ones that matter.
func TestRetainBackupsIgnoresAnotherDatabasesBackups(t *testing.T) {
	dir := t.TempDir()

	mine := make([]string, 0, 3)
	theirs := make([]string, 0, 3)
	for i := 1; i <= 3; i++ {
		m := filepath.Join(dir, fmt.Sprintf("aff-%019d.db", i))
		o := filepath.Join(dir, fmt.Sprintf("aff-test-%019d.db", i))
		writeStub(t, m)
		writeStub(t, o)
		mine = append(mine, m)
		theirs = append(theirs, o)
	}

	if err := retainBackups(dir, "aff", 2); err != nil {
		t.Fatalf("retainBackups: %v", err)
	}

	// Every aff-test file must still be there: this call was not about them.
	for _, p := range theirs {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("retention for base \"aff\" deleted another database's backup %s", filepath.Base(p))
		}
	}

	// And exactly the newest two of MY backups survive.
	survivors := existing(t, mine)
	sort.Strings(survivors)
	want := []string{filepath.Base(mine[1]), filepath.Base(mine[2])}
	if len(survivors) != 2 || survivors[0] != want[0] || survivors[1] != want[1] {
		t.Errorf("surviving aff backups = %v, want %v", survivors, want)
	}
}

// A file that merely lives in the backup directory is not a backup. Anything
// whose suffix is not exactly the fixed-width timestamp retainBackups relies
// on for ordering must be left alone — including a partially-written or
// hand-renamed file, which is precisely what someone would be trying to
// recover from.
func TestRetainBackupsLeavesNonSnapshotFilesAlone(t *testing.T) {
	dir := t.TempDir()

	keepers := []string{
		filepath.Join(dir, "aff-notatimestamp.db"),
		filepath.Join(dir, "aff-123.db"),                      // too short to be %019d
		filepath.Join(dir, "aff-00000000000000000001.db"),     // 20 digits, too long
		filepath.Join(dir, "aff-0000000000000000001-copy.db"), // hand-renamed
	}
	for _, p := range keepers {
		writeStub(t, p)
	}
	for i := 1; i <= 3; i++ {
		writeStub(t, filepath.Join(dir, fmt.Sprintf("aff-%019d.db", i)))
	}

	if err := retainBackups(dir, "aff", 1); err != nil {
		t.Fatalf("retainBackups: %v", err)
	}
	for _, p := range keepers {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("retention deleted %s, which is not a snapshot it owns", filepath.Base(p))
		}
	}
}

func writeStub(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("stub"), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func existing(t *testing.T, paths []string) []string {
	t.Helper()
	var out []string
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			out = append(out, filepath.Base(p))
		}
	}
	return out
}
