package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/auth"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// admin_reset_test.go covers the two break-glass commands, which were the
// least-tested code in the CLI (`admin reset` at 21%, `admin reset-password`
// at 0%) and are also the two that can hand someone the account.
//
// The distinction between them is the thing most worth pinning, because it is
// a deliberate design decision recorded in cmdAdminResetPassword's own doc
// comment and it is invisible from the outside:
//
//   - `admin reset` is for "every factor is gone". It rotates the password,
//     re-enrolls TOTP, issues a fresh recovery-code set, and revokes every
//     session.
//   - `admin reset-password` is for "I forgot the password, my authenticator
//     still works". It must change ONLY the password — leaving TOTP enrollment
//     and the existing recovery codes alone, because forcing a re-enrollment
//     costs a QR scan on every device and burns a whole code set for nothing.
//
// A regression that made the narrow command behave like the wide one would
// pass every existing test, break nobody's build, and quietly cost the
// operator their authenticator setup the next time they forgot a password.

// seedAdmin runs `admin init` to create a real admin row, returning the
// database path. Going through the command rather than writing rows directly
// is the point: these tests are about what one command does to state another
// command created.
func seedAdmin(t *testing.T, password string) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "aff.db")
	a, _, stderr := newAdminTestApp(t, dbPath)
	a.Stdin = strings.NewReader(password + "\n")
	if code := a.run([]string{"admin", "init"}); code != exitOK {
		t.Fatalf("admin init failed (%d): %s", code, stderr.String())
	}
	return dbPath
}

func openAdminStore(t *testing.T, dbPath string) *store.Store {
	t.Helper()
	st, err := store.Open(t.Context(), store.Options{Path: dbPath})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

func adminSnapshot(t *testing.T, dbPath string) (hash string, totp []byte, codes int) {
	t.Helper()
	st := openAdminStore(t, dbPath)
	admin, err := st.GetAdmin(t.Context())
	if err != nil {
		t.Fatalf("get admin: %v", err)
	}
	n, err := st.CountUnusedRecoveryCodes(t.Context())
	if err != nil {
		t.Fatalf("count recovery codes: %v", err)
	}
	return admin.PasswordHash, append([]byte(nil), admin.TOTPSecretEnc...), n
}

func TestAdminResetRotatesEveryFactor(t *testing.T) {
	t.Setenv("AFF_SECRET_KEY", "test-secret-key-value")
	const oldPassword = "a-genuinely-long-passphrase-one"
	const newPassword = "a-completely-different-long-passphrase"

	dbPath := seedAdmin(t, oldPassword)
	beforeHash, beforeTOTP, beforeCodes := adminSnapshot(t, dbPath)
	if beforeCodes == 0 {
		t.Fatal("init produced no recovery codes; the rest of this test would prove nothing")
	}

	a, stdout, stderr := newAdminTestApp(t, dbPath)
	a.Stdin = strings.NewReader(newPassword + "\n")
	if code := a.run([]string{"admin", "reset"}); code != exitOK {
		t.Fatalf("admin reset exit = %d, want %d (stderr: %s)", code, exitOK, stderr.String())
	}

	afterHash, afterTOTP, afterCodes := adminSnapshot(t, dbPath)

	if afterHash == beforeHash {
		t.Error("password hash unchanged after `admin reset`")
	}
	if bytes.Equal(afterTOTP, beforeTOTP) {
		t.Error("TOTP secret unchanged after `admin reset` — the wide reset must re-enroll")
	}
	if afterCodes == 0 {
		t.Error("no recovery codes exist after `admin reset`")
	}

	// The new password must actually verify, and the old one must not. A
	// reset that writes an unusable hash is the worst possible outcome of a
	// command run by someone already locked out.
	st := openAdminStore(t, dbPath)
	admin, err := st.GetAdmin(t.Context())
	if err != nil {
		t.Fatalf("get admin: %v", err)
	}
	ok, _, err := auth.Verify(newPassword, admin.PasswordHash)
	if err != nil || !ok {
		t.Errorf("the new password does not verify after reset (ok=%v err=%v)", ok, err)
	}
	if ok, _, _ := auth.Verify(oldPassword, admin.PasswordHash); ok {
		t.Error("the OLD password still verifies after reset")
	}

	// The enrollment material is printed exactly once, and it has to be
	// printed — an operator who cannot see the new QR/codes is still locked
	// out after a successful reset.
	out := stdout.String()
	if !strings.Contains(out, "otpauth://") {
		t.Errorf("reset printed no provisioning URI:\n%s", out)
	}
	if !strings.Contains(strings.ToLower(out), "recovery") {
		t.Errorf("reset printed no recovery codes:\n%s", out)
	}
}

// TestAdminResetRevokesEverySession is the half of `admin reset` that exists
// for the compromise case: if the reason you are resetting is that someone
// else has your credentials, a reset that leaves their session alive has
// accomplished nothing.
func TestAdminResetRevokesEverySession(t *testing.T) {
	t.Setenv("AFF_SECRET_KEY", "test-secret-key-value")
	dbPath := seedAdmin(t, "a-genuinely-long-passphrase-one")

	st := openAdminStore(t, dbPath)
	if _, err := st.CreateSession(t.Context(), auth.Session{
		TokenHash:  "hash-of-a-live-session",
		CreatedAt:  time.Now().UTC(),
		ExpiresAt:  time.Now().UTC().Add(24 * time.Hour),
		LastSeenAt: time.Now().UTC(),
		UserAgent:  "test",
		IP:         "127.0.0.1",
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	live := func() int {
		t.Helper()
		sessions, err := st.ListSessions(t.Context())
		if err != nil {
			t.Fatalf("list sessions: %v", err)
		}
		n := 0
		for _, s := range sessions {
			if s.RevokedAt.IsZero() {
				n++
			}
		}
		return n
	}
	if live() == 0 {
		t.Fatal("no live session was created; the assertion below would be vacuous")
	}

	a, _, stderr := newAdminTestApp(t, dbPath)
	a.Stdin = strings.NewReader("a-completely-different-long-passphrase\n")
	if code := a.run([]string{"admin", "reset"}); code != exitOK {
		t.Fatalf("admin reset exit = %d: %s", code, stderr.String())
	}

	if n := live(); n != 0 {
		t.Errorf("%d session(s) still live after `admin reset` — a reset must end every session", n)
	}
}

// TestAdminResetPasswordLeavesTOTPAndRecoveryCodesAlone is the invariant that
// separates the two break-glass commands. See this file's header.
func TestAdminResetPasswordLeavesTOTPAndRecoveryCodesAlone(t *testing.T) {
	t.Setenv("AFF_SECRET_KEY", "test-secret-key-value")
	const oldPassword = "a-genuinely-long-passphrase-one"
	const newPassword = "a-completely-different-long-passphrase"

	dbPath := seedAdmin(t, oldPassword)
	beforeHash, beforeTOTP, beforeCodes := adminSnapshot(t, dbPath)

	a, _, stderr := newAdminTestApp(t, dbPath)
	a.Stdin = strings.NewReader(newPassword + "\n")
	if code := a.run([]string{"admin", "reset-password"}); code != exitOK {
		t.Fatalf("admin reset-password exit = %d, want %d (stderr: %s)", code, exitOK, stderr.String())
	}

	afterHash, afterTOTP, afterCodes := adminSnapshot(t, dbPath)

	if afterHash == beforeHash {
		t.Error("password hash unchanged — reset-password did not reset the password")
	}
	if !bytes.Equal(afterTOTP, beforeTOTP) {
		t.Error("reset-password re-enrolled TOTP; it must not — that is what `admin reset` is for, " +
			"and a needless re-enrollment costs a QR scan on every device")
	}
	if afterCodes != beforeCodes {
		t.Errorf("recovery-code count changed from %d to %d; reset-password must not burn the code set",
			beforeCodes, afterCodes)
	}

	st := openAdminStore(t, dbPath)
	admin, err := st.GetAdmin(t.Context())
	if err != nil {
		t.Fatalf("get admin: %v", err)
	}
	if ok, _, err := auth.Verify(newPassword, admin.PasswordHash); err != nil || !ok {
		t.Errorf("the new password does not verify after reset-password (ok=%v err=%v)", ok, err)
	}
}

func TestAdminResetPasswordRequiresAnExistingAdmin(t *testing.T) {
	t.Setenv("AFF_SECRET_KEY", "test-secret-key-value")
	dbPath := filepath.Join(t.TempDir(), "empty.db")

	a, _, stderr := newAdminTestApp(t, dbPath)
	a.Stdin = strings.NewReader("a-genuinely-long-passphrase-one\n")

	if code := a.run([]string{"admin", "reset-password"}); code != exitFail {
		t.Errorf("exit = %d, want %d on a database with no admin", code, exitFail)
	}
	if !strings.Contains(stderr.String(), "admin init") {
		t.Errorf("stderr should point at `admin init`, got %q", stderr.String())
	}
}

// TestBreakGlassCommandsRequireTheSecretKey covers the shared secretKey()
// gate. Without AFF_SECRET_KEY there is no way to encrypt a TOTP secret at
// rest, so both commands must refuse rather than write something unreadable.
func TestBreakGlassCommandsRequireTheSecretKey(t *testing.T) {
	for _, sub := range []string{"reset", "reset-password"} {
		t.Run(sub, func(t *testing.T) {
			t.Setenv("AFF_SECRET_KEY", "test-secret-key-value")
			dbPath := seedAdmin(t, "a-genuinely-long-passphrase-one")

			t.Setenv("AFF_SECRET_KEY", "")
			a, _, stderr := newAdminTestApp(t, dbPath)
			a.Stdin = strings.NewReader("a-completely-different-long-passphrase\n")

			if code := a.run([]string{"admin", sub}); code != exitFail {
				t.Errorf("exit = %d, want %d with no AFF_SECRET_KEY (stderr: %s)", code, exitFail, stderr.String())
			}
		})
	}
}

func TestBreakGlassCommandsRejectPositionalArguments(t *testing.T) {
	for _, sub := range []string{"reset", "reset-password"} {
		t.Run(sub, func(t *testing.T) {
			t.Setenv("AFF_SECRET_KEY", "test-secret-key-value")
			a, _, stderr := newAdminTestApp(t, filepath.Join(t.TempDir(), "aff.db"))
			if code := a.run([]string{"admin", sub, "unexpected"}); code != exitUsage {
				t.Errorf("exit = %d, want %d for a stray positional argument (stderr: %s)",
					code, exitUsage, stderr.String())
			}
		})
	}
}

func TestBreakGlassCommandsRequireADatabasePath(t *testing.T) {
	for _, sub := range []string{"reset", "reset-password"} {
		t.Run(sub, func(t *testing.T) {
			t.Setenv("AFF_SECRET_KEY", "test-secret-key-value")
			a, _, stderr := newAdminTestApp(t, "")
			if code := a.run([]string{"admin", sub}); code != exitFail {
				t.Errorf("exit = %d, want %d with no --db (stderr: %s)", code, exitFail, stderr.String())
			}
		})
	}
}

func TestAdminResetRefusesAWeakPassword(t *testing.T) {
	t.Setenv("AFF_SECRET_KEY", "test-secret-key-value")
	dbPath := seedAdmin(t, "a-genuinely-long-passphrase-one")
	beforeHash, _, _ := adminSnapshot(t, dbPath)

	a, _, stderr := newAdminTestApp(t, dbPath)
	a.Stdin = strings.NewReader("short\n")

	if code := a.run([]string{"admin", "reset"}); code != exitFail {
		t.Fatalf("exit = %d, want %d for a weak password", code, exitFail)
	}
	if !strings.Contains(stderr.String(), "at least") {
		t.Errorf("stderr = %q, want a length complaint", stderr.String())
	}

	// And nothing may have been rotated on the way to that refusal: a
	// half-applied reset would leave the operator worse off than before they
	// ran it.
	if afterHash, _, _ := adminSnapshot(t, dbPath); afterHash != beforeHash {
		t.Error("the password was changed despite the reset being refused")
	}
}

func TestAdminRejectsAnUnknownSubcommand(t *testing.T) {
	a, _, stderr := newAdminTestApp(t, filepath.Join(t.TempDir(), "aff.db"))
	if code := a.run([]string{"admin", "definitely-not-a-subcommand"}); code != exitUsage {
		t.Errorf("exit = %d, want %d (stderr: %s)", code, exitUsage, stderr.String())
	}
}

func TestAdminWithNoSubcommandIsAUsageError(t *testing.T) {
	a, _, _ := newAdminTestApp(t, filepath.Join(t.TempDir(), "aff.db"))
	if code := a.run([]string{"admin"}); code != exitUsage {
		t.Errorf("exit = %d, want %d", code, exitUsage)
	}
}
