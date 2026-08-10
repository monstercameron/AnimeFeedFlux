package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

func newAdminTestApp(t *testing.T, dbPath string) (a *app, stdout, stderr *bytes.Buffer) {
	t.Helper()
	stdout = &bytes.Buffer{}
	stderr = &bytes.Buffer{}
	a = &app{
		Stdout:      stdout,
		Stderr:      stderr,
		DBPath:      dbPath,
		SessionFile: filepath.Join(t.TempDir(), "session.json"),
	}
	return a, stdout, stderr
}

func TestAdminInitRefusesWeakPassword(t *testing.T) {
	t.Setenv("AFF_SECRET_KEY", "test-secret-key-value")
	dbPath := filepath.Join(t.TempDir(), "aff.db")
	a, _, stderr := newAdminTestApp(t, dbPath)
	a.Stdin = strings.NewReader("short\n")

	code := a.run([]string{"admin", "init"})
	if code != exitFail {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitFail, stderr.String())
	}
	if !strings.Contains(stderr.String(), "at least") {
		t.Fatalf("stderr = %q, want a length complaint", stderr.String())
	}

	// No admin row should have been written.
	st, err := store.Open(t.Context(), store.Options{Path: dbPath})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	if _, err := st.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := st.GetAdmin(t.Context()); err == nil {
		t.Fatal("expected no admin row after a refused weak-password init")
	}
}

func TestAdminInitRefusesToOverwriteExistingAdmin(t *testing.T) {
	t.Setenv("AFF_SECRET_KEY", "test-secret-key-value")
	dbPath := filepath.Join(t.TempDir(), "aff.db")

	// First init succeeds.
	a1, _, stderr1 := newAdminTestApp(t, dbPath)
	a1.Stdin = strings.NewReader("a-genuinely-long-passphrase-one\n")
	if code := a1.run([]string{"admin", "init"}); code != exitOK {
		t.Fatalf("first init: exit code = %d, want %d (stderr: %s)", code, exitOK, stderr1.String())
	}

	// Second init against the same DB must refuse, not overwrite.
	a2, _, stderr2 := newAdminTestApp(t, dbPath)
	a2.Stdin = strings.NewReader("a-different-long-passphrase-two\n")
	code := a2.run([]string{"admin", "init"})
	if code != exitFail {
		t.Fatalf("second init: exit code = %d, want %d (stderr: %s)", code, exitFail, stderr2.String())
	}
	if !strings.Contains(stderr2.String(), "already exists") {
		t.Fatalf("stderr = %q, want an already-exists refusal", stderr2.String())
	}

	// The original admin's hash must be untouched.
	st, err := store.Open(t.Context(), store.Options{Path: dbPath})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	admin, err := st.GetAdmin(t.Context())
	if err != nil {
		t.Fatalf("get admin: %v", err)
	}
	if admin.PasswordHash == "" {
		t.Fatal("expected a password hash to be set from the first init")
	}
}

func TestAdminInitSucceedsAndPrintsEnrollmentOnce(t *testing.T) {
	t.Setenv("AFF_SECRET_KEY", "test-secret-key-value")
	dbPath := filepath.Join(t.TempDir(), "aff.db")
	a, stdout, stderr := newAdminTestApp(t, dbPath)
	a.JSON = true
	a.Stdin = strings.NewReader("a-genuinely-long-passphrase-one\n")

	code := a.run([]string{"admin", "init"})
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitOK, stderr.String())
	}

	var got adminEnrollment
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("--json output does not parse: %v (stdout: %s)", err, stdout.String())
	}
	if got.ProvisioningURI == "" {
		t.Fatal("expected a non-empty provisioning URI")
	}
	if len(got.RecoveryCodes) != recoveryCodeCount {
		t.Fatalf("recovery codes = %d, want %d", len(got.RecoveryCodes), recoveryCodeCount)
	}

	st, err := store.Open(t.Context(), store.Options{Path: dbPath})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	if _, err := st.GetAdmin(t.Context()); err != nil {
		t.Fatalf("expected an admin row to exist: %v", err)
	}
}

func TestAdminInitRequiresDBPath(t *testing.T) {
	a, _, stderr := newAdminTestApp(t, "")
	a.Stdin = strings.NewReader("a-genuinely-long-passphrase-one\n")
	code := a.run([]string{"admin", "init"})
	if code != exitFail {
		t.Fatalf("exit code = %d, want %d", code, exitFail)
	}
	if !strings.Contains(stderr.String(), "LOCAL-ONLY") {
		t.Fatalf("stderr = %q, want it to say this command is local-only", stderr.String())
	}
}

func TestAdminResetRequiresExistingAdmin(t *testing.T) {
	t.Setenv("AFF_SECRET_KEY", "test-secret-key-value")
	dbPath := filepath.Join(t.TempDir(), "aff.db")
	a, _, stderr := newAdminTestApp(t, dbPath)
	a.Stdin = strings.NewReader("a-genuinely-long-passphrase-one\n")

	code := a.run([]string{"admin", "reset"})
	if code != exitFail {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitFail, stderr.String())
	}
	if !strings.Contains(stderr.String(), "admin init") {
		t.Fatalf("stderr = %q, want it to point at `admin init`", stderr.String())
	}
}
