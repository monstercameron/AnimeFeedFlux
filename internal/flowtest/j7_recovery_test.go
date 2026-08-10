// J7 — recover from lockout (PLAN.md §22 "J7 — Recover from lockout",
// TODOS.md BF-31..35).
//
// AuthService's recovery RPC (B1-02) and its elevated-session scope
// enforcement (B1-08's interceptor) do not exist yet. What's real and
// exercised here directly: internal/auth.VerifyCode/GenerateCodes and
// internal/store's recovery_codes, sessions, and auth_events persistence —
// exactly the primitives §12.2's recovery flow is built from.
package flowtest

import (
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/auth"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

const j7IP = "203.0.113.30"

// j7SetupRecoveryCodes enrolls n fresh recovery codes for w's admin the way
// TOTP enrollment does (§12.2: "generated once at enrollment, shown once,
// stored hashed") and returns the plaintext codes in the SAME order
// StoreRecoveryCodes wrote their hashes, since that insertion order is also
// the index auth.VerifyCode/store.UseRecoveryCode operate on.
func j7SetupRecoveryCodes(t *testing.T, w *World, n int) []string {
	t.Helper()
	plain, hashes, err := auth.GenerateCodes(n)
	if err != nil {
		t.Fatalf("auth.GenerateCodes: %v", err)
	}
	if err := w.Store.StoreRecoveryCodes(t.Context(), hashes); err != nil {
		t.Fatalf("StoreRecoveryCodes: %v", err)
	}
	return plain
}

// j7Recover drives one recovery attempt: verify the submitted code against
// the currently-stored hashes, mark the matching one used, log the attempt
// to auth_events either way, and — only on success — revoke every existing
// session (§12.2: "a reset that leaves old sessions alive has not actually
// locked anyone out").
func j7Recover(t *testing.T, w *World, code string) error {
	t.Helper()
	ctx := t.Context()

	hashes, err := j7CurrentHashes(t, w)
	if err != nil {
		return err
	}
	index, ok := auth.VerifyCode(code, hashes)
	if !ok {
		_ = w.Store.RecordAuthEvent(ctx, "recovery", j7IP, false, "code_not_recognized")
		return store.ErrNotFound
	}

	if err := w.Store.UseRecoveryCode(ctx, index); err != nil {
		_ = w.Store.RecordAuthEvent(ctx, "recovery", j7IP, false, "code_already_used")
		return err
	}

	if err := w.Store.RevokeAllSessions(ctx); err != nil {
		return err
	}
	_ = w.Store.RecordAuthEvent(ctx, "recovery", j7IP, true, "")
	return nil
}

// j7CurrentHashes reads back the hashes StoreRecoveryCodes wrote, in
// insertion order, so auth.VerifyCode's returned index lines up with
// store.UseRecoveryCode's index-by-offset contract (auth.go's doc comment on
// UseRecoveryCode).
func j7CurrentHashes(t *testing.T, w *World) ([]string, error) {
	t.Helper()
	rows, err := w.Store.Writer().QueryContext(t.Context(),
		`SELECT code_hash FROM recovery_codes ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// TestJ7_ConsumedCodeMarkedUsedAndRefusedOnReuse is BF-31.
func TestJ7_ConsumedCodeMarkedUsedAndRefusedOnReuse(t *testing.T) {
	w := New(t)
	j1SetupAdmin(t, w)
	codes := j7SetupRecoveryCodes(t, w, 5)

	if err := j7Recover(t, w, codes[0]); err != nil {
		t.Fatalf("first use of a fresh recovery code: %v", err)
	}

	// BF-31 (§22 J7): the consumed code is marked used and is refused on a
	// second attempt.
	if err := j7Recover(t, w, codes[0]); err == nil {
		t.Fatal("reusing an already-consumed recovery code succeeded, want it refused")
	}
}

// TestJ7_AllOtherSessionsRevoked is BF-33.
func TestJ7_AllOtherSessionsRevoked(t *testing.T) {
	w := New(t)
	ctx := t.Context()
	j1SetupAdmin(t, w)
	codes := j7SetupRecoveryCodes(t, w, 5)

	// Two sessions that existed before the lockout — the admin's laptop and
	// phone, say.
	id1, _, err := j1CreateSession(ctx, w, "203.0.113.40")
	if err != nil {
		t.Fatalf("creating pre-existing session 1: %v", err)
	}
	id2, _, err := j1CreateSession(ctx, w, "203.0.113.41")
	if err != nil {
		t.Fatalf("creating pre-existing session 2: %v", err)
	}

	if err := j7Recover(t, w, codes[0]); err != nil {
		t.Fatalf("j7Recover: %v", err)
	}

	// BF-33 (§22 J7): all other sessions were revoked.
	sessions, err := w.Store.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("ListSessions returned %d rows, want the original 2 (ids %d, %d)", len(sessions), id1, id2)
	}
	for _, s := range sessions {
		if s.RevokedAt.IsZero() {
			t.Fatalf("a session was never revoked: %+v", s)
		}
		// store.RevokeAllSessions stamps revoked_at with the real wall
		// clock (store/auth.go), not World's injected Clock, so Valid must
		// be checked against real time.Now() here — w.Clock.Now() is
		// pinned to the fixed test epoch and would make RevokedAt look
		// like it's in the future.
		if s.Valid(time.Now(), auth.SessionIdleTimeout) {
			t.Fatalf("a session is still valid after recovery revoked all sessions: %+v", s)
		}
	}
}

// TestJ7_RemainingCodeCountDecrementedByExactlyOne is BF-34.
func TestJ7_RemainingCodeCountDecrementedByExactlyOne(t *testing.T) {
	w := New(t)
	ctx := t.Context()
	j1SetupAdmin(t, w)
	codes := j7SetupRecoveryCodes(t, w, 5)

	before, err := w.Store.CountUnusedRecoveryCodes(ctx)
	if err != nil {
		t.Fatalf("CountUnusedRecoveryCodes (before): %v", err)
	}
	if before != 5 {
		t.Fatalf("unused recovery codes before recovery = %d, want 5", before)
	}

	if err := j7Recover(t, w, codes[2]); err != nil {
		t.Fatalf("j7Recover: %v", err)
	}

	// BF-34 (§22 J7): the remaining-code count decremented by exactly one.
	after, err := w.Store.CountUnusedRecoveryCodes(ctx)
	if err != nil {
		t.Fatalf("CountUnusedRecoveryCodes (after): %v", err)
	}
	if after != before-1 {
		t.Fatalf("unused recovery codes after recovery = %d, want %d (before - 1)", after, before-1)
	}
}

// TestJ7_RecoveryAttemptAppearsInAuthEvents is BF-35, covering both a
// successful and a failed attempt (§4's "every attempt" applies here too,
// not only to login).
func TestJ7_RecoveryAttemptAppearsInAuthEvents(t *testing.T) {
	w := New(t)
	ctx := t.Context()
	j1SetupAdmin(t, w)
	codes := j7SetupRecoveryCodes(t, w, 5)

	if err := j7Recover(t, w, "not-a-real-code-at-all"); err == nil {
		t.Fatal("a bogus recovery code unexpectedly succeeded")
	}
	if err := j7Recover(t, w, codes[1]); err != nil {
		t.Fatalf("j7Recover: %v", err)
	}

	// BF-35 (§22 J7): the recovery attempt is in auth_events — both the
	// failed and the successful one.
	events, err := w.Store.ListAuthEvents(ctx, 0)
	if err != nil {
		t.Fatalf("ListAuthEvents: %v", err)
	}
	var recoveryEvents []store.AuthEvent
	for _, e := range events {
		if e.Kind == "recovery" {
			recoveryEvents = append(recoveryEvents, e)
		}
	}
	if len(recoveryEvents) != 2 {
		t.Fatalf("auth_events has %d 'recovery' rows, want 2", len(recoveryEvents))
	}
	if recoveryEvents[0].OK == recoveryEvents[1].OK {
		t.Fatalf("expected one failed and one successful recovery event, got OK=%v and OK=%v", recoveryEvents[0].OK, recoveryEvents[1].OK)
	}
}

// TestJ7_ElevatedSessionScope_Skip documents BF-32, which cannot be tested
// yet: internal/auth.Session and the sessions table carry no scope/elevation
// concept at all (migrations/0001_auth.sql), so "reaches only password
// change and TOTP re-enrollment" has nothing to assert against until
// AuthService (B1-02) mints a scoped elevated session and its interceptor
// (B1-08) enforces that scope on every RPC. Faking a scope check here would
// only prove this test file agrees with itself, not that the real system
// enforces anything — exactly the "fabricated passing test" this package
// must not produce.
func TestJ7_ElevatedSessionScope_Skip(t *testing.T) {
	t.Skip("BF-32 needs AuthService's elevated-session scope (B1-02) enforced by the RPC interceptor (B1-08); neither exists yet, and sessions carry no scope field to assert against")
}
