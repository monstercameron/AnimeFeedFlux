package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

func newOpsTestApp(t *testing.T, dbPath string) (a *app, stdout, stderr *bytes.Buffer) {
	t.Helper()
	stdout = &bytes.Buffer{}
	stderr = &bytes.Buffer{}
	a = &app{
		Stdout:      stdout,
		Stderr:      stderr,
		Stdin:       strings.NewReader(""),
		DBPath:      dbPath,
		SessionFile: filepath.Join(t.TempDir(), "session.json"),
	}
	return a, stdout, stderr
}

func seedMigratedDB(t *testing.T, dbPath string) {
	t.Helper()
	st, err := store.Open(t.Context(), store.Options{Path: dbPath})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	if _, err := st.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
}

func TestBackupWritesAVerifiedSnapshotAndVerifyConfirmsIt(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "aff.db")
	seedMigratedDB(t, dbPath)
	outDir := t.TempDir()

	a, stdout, stderr := newOpsTestApp(t, dbPath)
	code := a.run([]string{"backup", "--out", outDir})
	if code != exitOK {
		t.Fatalf("aff backup: exit %d, want %d (stderr: %s)", code, exitOK, stderr.String())
	}
	if !strings.Contains(stdout.String(), "wrote:") {
		t.Fatalf("stdout = %q, want a 'wrote:' line", stdout.String())
	}

	matches, err := filepath.Glob(filepath.Join(outDir, "*.db"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("backup dir contents = %v (err %v), want exactly one .db file", matches, err)
	}

	a2, stdout2, stderr2 := newOpsTestApp(t, dbPath)
	code2 := a2.run([]string{"verify", matches[0]})
	if code2 != exitOK {
		t.Fatalf("aff verify: exit %d, want %d (stderr: %s)", code2, exitOK, stderr2.String())
	}
	if !strings.Contains(stdout2.String(), "ok") {
		t.Fatalf("aff verify stdout = %q, want it to report ok", stdout2.String())
	}
}

func TestVerifyReportsFailureOnACorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.db")
	if err := os.WriteFile(path, []byte("not a database"), 0o600); err != nil {
		t.Fatalf("write bad file: %v", err)
	}
	a, stdout, _ := newOpsTestApp(t, "")
	code := a.run([]string{"verify", path})
	if code != exitFail {
		t.Fatalf("aff verify on a corrupt file: exit %d, want %d", code, exitFail)
	}
	if !strings.Contains(stdout.String(), "FAILED") {
		t.Fatalf("stdout = %q, want a FAILED report", stdout.String())
	}
}

func TestRestoreRoundTripsAndVerifiesBothSides(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "aff.db")
	seedMigratedDB(t, dbPath)
	outDir := t.TempDir()

	a, _, stderr := newOpsTestApp(t, dbPath)
	if code := a.run([]string{"backup", "--out", outDir}); code != exitOK {
		t.Fatalf("aff backup: exit %d (stderr: %s)", code, stderr.String())
	}
	matches, _ := filepath.Glob(filepath.Join(outDir, "*.db"))
	if len(matches) != 1 {
		t.Fatalf("want exactly one backup, got %v", matches)
	}
	backupPath := matches[0]

	restoreDest := filepath.Join(t.TempDir(), "restored.db")
	a2, stdout2, stderr2 := newOpsTestApp(t, "")
	code := a2.run([]string{"restore", "--from", backupPath, "--to", restoreDest, "--yes"})
	if code != exitOK {
		t.Fatalf("aff restore: exit %d, want %d (stderr: %s)", code, exitOK, stderr2.String())
	}
	if !strings.Contains(stdout2.String(), "restored:") {
		t.Fatalf("stdout = %q, want a 'restored:' line", stdout2.String())
	}
	if _, err := os.Stat(restoreDest); err != nil {
		t.Fatalf("restored file does not exist: %v", err)
	}
}

// TestRestoreRefusesWithoutConfirmation is the assertion the task explicitly
// asks for: every destructive command refuses without confirmation. Stdin
// here supplies neither "y" nor "yes" (an empty line, from a closed pipe),
// and --yes is not passed.
func TestRestoreRefusesWithoutConfirmation(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "aff.db")
	seedMigratedDB(t, dbPath)
	outDir := t.TempDir()

	a, _, stderr := newOpsTestApp(t, dbPath)
	if code := a.run([]string{"backup", "--out", outDir}); code != exitOK {
		t.Fatalf("aff backup: exit %d (stderr: %s)", code, stderr.String())
	}
	matches, _ := filepath.Glob(filepath.Join(outDir, "*.db"))
	backupPath := matches[0]

	restoreDest := filepath.Join(t.TempDir(), "untouched.db")
	if err := os.WriteFile(restoreDest, []byte("original contents, must survive"), 0o600); err != nil {
		t.Fatalf("seed destination: %v", err)
	}

	a2, _, stderr2 := newOpsTestApp(t, "")
	a2.Stdin = strings.NewReader("n\n") // explicit refusal
	code := a2.run([]string{"restore", "--from", backupPath, "--to", restoreDest})
	if code != exitFail {
		t.Fatalf("aff restore without confirmation: exit %d, want %d (stderr: %s)", code, exitFail, stderr2.String())
	}
	if !strings.Contains(stderr2.String(), "aborted") {
		t.Fatalf("stderr = %q, want an aborted message", stderr2.String())
	}

	got, err := os.ReadFile(restoreDest)
	if err != nil {
		t.Fatalf("reading destination after refused restore: %v", err)
	}
	if string(got) != "original contents, must survive" {
		t.Fatalf("destination was modified despite a refused confirmation: %q", string(got))
	}
}

func TestRestoreRefusesAnUnverifiedBackupBeforeEverPrompting(t *testing.T) {
	badBackup := filepath.Join(t.TempDir(), "bad.db")
	if err := os.WriteFile(badBackup, []byte("garbage"), 0o600); err != nil {
		t.Fatalf("write bad backup: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "dest.db")

	a, _, stderr := newOpsTestApp(t, "")
	// No stdin available to answer a prompt — if this reached the confirm
	// step it would hang or fail on read, not just return exitFail cleanly.
	a.Stdin = strings.NewReader("")
	code := a.run([]string{"restore", "--from", badBackup, "--to", dest, "--yes"})
	if code != exitFail {
		t.Fatalf("aff restore of an unverified backup: exit %d, want %d (stderr: %s)", code, exitFail, stderr.String())
	}
	if _, err := os.Stat(dest); err == nil {
		t.Fatal("aff restore of an unverified backup must not create the destination")
	}
}

func TestBackupJSONOutputIsWellFormed(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "aff.db")
	seedMigratedDB(t, dbPath)
	outDir := t.TempDir()

	a, stdout, stderr := newOpsTestApp(t, dbPath)
	a.JSON = true
	if code := a.run([]string{"backup", "--out", outDir}); code != exitOK {
		t.Fatalf("aff backup --json: exit code, stderr: %s", stderr.String())
	}
	var decoded struct {
		Path  string `json:"path"`
		Bytes int64  `json:"bytes"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("decoding --json output: %v (stdout: %s)", err, stdout.String())
	}
	if decoded.Path == "" || decoded.Bytes == 0 {
		t.Fatalf("decoded JSON looks empty: %+v", decoded)
	}
}
