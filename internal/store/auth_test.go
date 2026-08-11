package store

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/auth"
)

// --- admin ------------------------------------------------------------

func TestInitAdminTwiceFails(t *testing.T) {
	s := openTemp(t)
	ctx := t.Context()

	if err := s.InitAdmin(ctx, "hash-1", "params-1"); err != nil {
		t.Fatalf("first InitAdmin: %v", err)
	}
	err := s.InitAdmin(ctx, "hash-2", "params-2")
	if !errors.Is(err, ErrAdminExists) {
		t.Fatalf("second InitAdmin error = %v, want ErrAdminExists", err)
	}

	// And the first admin's data must survive the refused second attempt —
	// a locked-out real admin is exactly what §4 refuses to risk.
	got, err := s.GetAdmin(ctx)
	if err != nil {
		t.Fatalf("GetAdmin: %v", err)
	}
	if got.PasswordHash != "hash-1" {
		t.Errorf("password hash = %q, want the original %q (overwritten by refused init)", got.PasswordHash, "hash-1")
	}
}

func TestGetAdminBeforeInitIsNotFound(t *testing.T) {
	s := openTemp(t)
	_, err := s.GetAdmin(t.Context())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetAdmin before init: err = %v, want ErrNotFound", err)
	}
}

func TestUpdatePasswordStampsChangeTime(t *testing.T) {
	s := openTemp(t)
	ctx := t.Context()

	if err := s.InitAdmin(ctx, "hash-1", "params-1"); err != nil {
		t.Fatalf("InitAdmin: %v", err)
	}
	before, err := s.GetAdmin(ctx)
	if err != nil {
		t.Fatalf("GetAdmin: %v", err)
	}
	if !before.PasswordChangedAt.IsZero() {
		t.Fatalf("password_changed_at set before any change: %v", before.PasswordChangedAt)
	}

	if err := s.UpdatePassword(ctx, "hash-2", "params-2"); err != nil {
		t.Fatalf("UpdatePassword: %v", err)
	}

	after, err := s.GetAdmin(ctx)
	if err != nil {
		t.Fatalf("GetAdmin after update: %v", err)
	}
	if after.PasswordHash != "hash-2" || after.KDFParams != "params-2" {
		t.Errorf("password/params did not update: %+v", after)
	}
	if after.PasswordChangedAt.IsZero() {
		t.Error("password_changed_at was not stamped")
	}
}

func TestUpdatePasswordWithoutAdminIsNotFound(t *testing.T) {
	s := openTemp(t)
	err := s.UpdatePassword(t.Context(), "hash", "params")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdatePassword with no admin row: err = %v, want ErrNotFound", err)
	}
}

func TestTOTPSecretRoundTrip(t *testing.T) {
	s := openTemp(t)
	ctx := t.Context()
	if err := s.InitAdmin(ctx, "hash", "params"); err != nil {
		t.Fatalf("InitAdmin: %v", err)
	}

	// Pre-enrollment: nil, no error — distinct from "no admin at all".
	enc, err := s.GetTOTPSecret(ctx)
	if err != nil {
		t.Fatalf("GetTOTPSecret before enrollment: %v", err)
	}
	if enc != nil {
		t.Errorf("secret before enrollment = %v, want nil", enc)
	}

	blob := []byte("ciphertext-not-a-real-secret")
	if err := s.SetTOTPSecret(ctx, blob); err != nil {
		t.Fatalf("SetTOTPSecret: %v", err)
	}

	got, err := s.GetTOTPSecret(ctx)
	if err != nil {
		t.Fatalf("GetTOTPSecret after enrollment: %v", err)
	}
	if string(got) != string(blob) {
		t.Errorf("secret = %q, want %q", got, blob)
	}
}

// --- TOTP replay --------------------------------------------------------

func TestMarkTOTPStepUsedRejectsReplay(t *testing.T) {
	s := openTemp(t)
	ctx := t.Context()

	if err := s.MarkTOTPStepUsed(ctx, 1000, "code-hash-a"); err != nil {
		t.Fatalf("first MarkTOTPStepUsed: %v", err)
	}
	err := s.MarkTOTPStepUsed(ctx, 1000, "code-hash-b")
	if !errors.Is(err, ErrTOTPReplay) {
		t.Fatalf("replayed step error = %v, want ErrTOTPReplay", err)
	}
}

func TestMarkTOTPStepUsedConcurrentInsertsOnlyOneWins(t *testing.T) {
	// §4: two concurrent logins presenting the same code must lose the race
	// in the database, not in a check-then-insert. The step PRIMARY KEY is
	// what makes that true regardless of goroutine scheduling.
	s := openTemp(t)
	ctx := t.Context()

	const attempts = 8
	var wg sync.WaitGroup
	errs := make([]error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = s.MarkTOTPStepUsed(ctx, 42, "same-code-hash")
		}(i)
	}
	wg.Wait()

	successes, replays := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrTOTPReplay):
			replays++
		default:
			t.Fatalf("unexpected error from concurrent MarkTOTPStepUsed: %v", err)
		}
	}
	if successes != 1 {
		t.Errorf("successes = %d, want exactly 1", successes)
	}
	if replays != attempts-1 {
		t.Errorf("replays = %d, want %d", replays, attempts-1)
	}
}

// --- recovery codes -----------------------------------------------------

func TestUseRecoveryCodeCannotBeReused(t *testing.T) {
	s := openTemp(t)
	ctx := t.Context()

	if err := s.StoreRecoveryCodes(ctx, []string{"hash-a", "hash-b", "hash-c"}); err != nil {
		t.Fatalf("StoreRecoveryCodes: %v", err)
	}

	if n, err := s.CountUnusedRecoveryCodes(ctx); err != nil || n != 3 {
		t.Fatalf("CountUnusedRecoveryCodes = %d, %v, want 3, nil", n, err)
	}

	if err := s.UseRecoveryCode(ctx, 1); err != nil {
		t.Fatalf("UseRecoveryCode(1): %v", err)
	}

	if n, err := s.CountUnusedRecoveryCodes(ctx); err != nil || n != 2 {
		t.Fatalf("CountUnusedRecoveryCodes after use = %d, %v, want 2, nil", n, err)
	}

	err := s.UseRecoveryCode(ctx, 1)
	if !errors.Is(err, ErrRecoveryCodeUsed) {
		t.Fatalf("reusing a consumed code: err = %v, want ErrRecoveryCodeUsed", err)
	}

	// A different, still-unused code at a different index must still work.
	if err := s.UseRecoveryCode(ctx, 0); err != nil {
		t.Fatalf("UseRecoveryCode(0): %v", err)
	}
}

func TestUseRecoveryCodeOutOfRangeIsNotFound(t *testing.T) {
	s := openTemp(t)
	ctx := t.Context()
	if err := s.StoreRecoveryCodes(ctx, []string{"hash-a"}); err != nil {
		t.Fatalf("StoreRecoveryCodes: %v", err)
	}
	err := s.UseRecoveryCode(ctx, 5)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("out-of-range index: err = %v, want ErrNotFound", err)
	}
}

func TestStoreRecoveryCodesReplacesThePreviousSet(t *testing.T) {
	s := openTemp(t)
	ctx := t.Context()

	if err := s.StoreRecoveryCodes(ctx, []string{"old-a", "old-b"}); err != nil {
		t.Fatalf("first StoreRecoveryCodes: %v", err)
	}
	if err := s.StoreRecoveryCodes(ctx, []string{"new-a", "new-b", "new-c"}); err != nil {
		t.Fatalf("second StoreRecoveryCodes: %v", err)
	}
	n, err := s.CountUnusedRecoveryCodes(ctx)
	if err != nil {
		t.Fatalf("CountUnusedRecoveryCodes: %v", err)
	}
	if n != 3 {
		t.Errorf("unused count = %d, want 3 (re-enrollment must invalidate the old set)", n)
	}
}

// --- sessions -------------------------------------------------------------

func makeTestSession(now time.Time, tokenHash string) auth.Session {
	return auth.Session{
		TokenHash:  tokenHash,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(auth.SessionAbsoluteLifetime),
		IP:         "203.0.113.5",
		UserAgent:  "test-agent",
	}
}

func TestSessionCreateLookupTouchRevoke(t *testing.T) {
	s := openTemp(t)
	ctx := t.Context()
	now := time.Now().UTC()

	id, err := s.CreateSession(ctx, makeTestSession(now, "token-hash-1"))
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if id == 0 {
		t.Fatal("CreateSession returned id 0")
	}

	got, err := s.GetSessionByTokenHash(ctx, "token-hash-1")
	if err != nil {
		t.Fatalf("GetSessionByTokenHash: %v", err)
	}
	if got.TokenHash != "token-hash-1" || got.IP != "203.0.113.5" || got.UserAgent != "test-agent" {
		t.Errorf("round trip mismatch: %+v", got)
	}
	if !got.RevokedAt.IsZero() {
		t.Errorf("new session already revoked: %v", got.RevokedAt)
	}

	later := now.Add(5 * time.Minute)
	if err := s.TouchSession(ctx, id, later); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}
	touched, err := s.GetSessionByTokenHash(ctx, "token-hash-1")
	if err != nil {
		t.Fatalf("GetSessionByTokenHash after touch: %v", err)
	}
	if !touched.LastSeenAt.Equal(later) {
		t.Errorf("last_seen_at = %v, want %v", touched.LastSeenAt, later)
	}

	if err := s.RevokeSession(ctx, id); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	revoked, err := s.GetSessionByTokenHash(ctx, "token-hash-1")
	if err != nil {
		t.Fatalf("GetSessionByTokenHash after revoke: %v", err)
	}
	if revoked.RevokedAt.IsZero() {
		t.Error("session not revoked")
	}
}

// TestNewSessionDefaultsToFullScope proves migrations/0005_session_scope.sql's
// DEFAULT 'full' applies to every session CreateSession inserts, not just
// rows that existed before the column was added — CreateSession's INSERT
// never mentions the scope column, so this is entirely the schema default
// doing the work.
func TestNewSessionDefaultsToFullScope(t *testing.T) {
	s := openTemp(t)
	ctx := t.Context()
	now := time.Now().UTC()

	id, err := s.CreateSession(ctx, makeTestSession(now, "scope-default-token"))
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	scope, err := s.SessionScope(ctx, id)
	if err != nil {
		t.Fatalf("SessionScope: %v", err)
	}
	if scope != SessionScopeFull {
		t.Errorf("new session scope = %q, want %q", scope, SessionScopeFull)
	}
}

// TestSessionScopeRoundTrip: setting elevated and reading it back returns
// exactly what was set, and it is distinguishable from the full default.
func TestSessionScopeRoundTrip(t *testing.T) {
	s := openTemp(t)
	ctx := t.Context()
	now := time.Now().UTC()

	id, err := s.CreateSession(ctx, makeTestSession(now, "scope-roundtrip-token"))
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if err := s.SetSessionScope(ctx, id, SessionScopeElevated); err != nil {
		t.Fatalf("SetSessionScope(elevated): %v", err)
	}
	got, err := s.SessionScope(ctx, id)
	if err != nil {
		t.Fatalf("SessionScope: %v", err)
	}
	if got != SessionScopeElevated {
		t.Errorf("scope after setting elevated = %q, want %q", got, SessionScopeElevated)
	}

	if err := s.SetSessionScope(ctx, id, SessionScopeFull); err != nil {
		t.Fatalf("SetSessionScope(full): %v", err)
	}
	got, err = s.SessionScope(ctx, id)
	if err != nil {
		t.Fatalf("SessionScope: %v", err)
	}
	if got != SessionScopeFull {
		t.Errorf("scope after setting full = %q, want %q", got, SessionScopeFull)
	}
}

// TestSetSessionScopeUnknownIDIsNotFound covers the RowsAffected == 0 path.
func TestSetSessionScopeUnknownIDIsNotFound(t *testing.T) {
	s := openTemp(t)
	err := s.SetSessionScope(t.Context(), 999999, SessionScopeElevated)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetSessionScope on unknown id: err = %v, want ErrNotFound", err)
	}
}

// TestSessionScopeColumnRejectsUnknownValue pins the CHECK constraint
// (migrations/0005_session_scope.sql): scope is a closed set, and a typo'd
// value must fail loudly at write time rather than silently landing in a
// column an enforcement check does not recognize as either full or elevated.
func TestSessionScopeColumnRejectsUnknownValue(t *testing.T) {
	s := openTemp(t)
	ctx := t.Context()
	now := time.Now().UTC()

	id, err := s.CreateSession(ctx, makeTestSession(now, "scope-check-token"))
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	err = s.SetSessionScope(ctx, id, "root")
	if !isConstraintErr(err) {
		t.Fatalf("SetSessionScope(\"root\") error = %v, want a CHECK constraint violation", err)
	}
}

// TestMigration0005AppliesOnto0004AndLeavesExistingSessionsFull is the
// deploy-safety property migrations/0005_session_scope.sql's own comment
// commits to: applying it onto a database that already has live sessions
// (i.e. one migrated only through 0004) must not narrow any of them — a
// migration that invalidated or restricted live sessions would lock the
// admin out at deploy time.
func TestMigration0005AppliesOnto0004AndLeavesExistingSessionsFull(t *testing.T) {
	ctx := t.Context()
	s, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "pre-0005.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if _, err := s.writer.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			name       TEXT NOT NULL,
			applied_at TEXT NOT NULL
		) STRICT`); err != nil {
		t.Fatalf("creating schema_migrations: %v", err)
	}

	all, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	for _, m := range all {
		if m.version > 4 {
			continue
		}
		if err := s.applyOne(ctx, m); err != nil {
			t.Fatalf("applying migration %d: %v", m.version, err)
		}
	}

	// A live session, as it would exist in production the instant before
	// 0005 lands — inserted with raw SQL because sessions.scope does not
	// exist yet at this point in the test.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.writer.ExecContext(ctx, `
		INSERT INTO sessions (token_hash, created_at, last_seen_at, expires_at)
		VALUES (?, ?, ?, ?)`,
		"pre-existing-session-token-hash", now, now, now)
	if err != nil {
		t.Fatalf("seeding a pre-0005 session: %v", err)
	}
	preexistingID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("pre-existing session id: %v", err)
	}

	applied, err := s.Migrate(ctx)
	if err != nil {
		t.Fatalf("migrating onto 0004: %v", err)
	}
	if applied == 0 {
		t.Fatal("Migrate applied nothing — 0005 (and anything after it) did not run")
	}

	scope, err := s.SessionScope(ctx, preexistingID)
	if err != nil {
		t.Fatalf("SessionScope for pre-existing session: %v", err)
	}
	if scope != SessionScopeFull {
		t.Errorf("pre-existing session scope after migrating onto 0005 = %q, want %q (full access preserved)", scope, SessionScopeFull)
	}

	// And the session is still otherwise usable — GetSessionByTokenHash
	// (the path the interceptor actually uses) still resolves it.
	if _, err := s.GetSessionByTokenHash(ctx, "pre-existing-session-token-hash"); err != nil {
		t.Fatalf("pre-existing session unusable after migrating onto 0005: %v", err)
	}
}

func TestGetSessionByTokenHashNotFound(t *testing.T) {
	s := openTemp(t)
	_, err := s.GetSessionByTokenHash(t.Context(), "no-such-hash")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestRevokeAllSessions(t *testing.T) {
	s := openTemp(t)
	ctx := t.Context()
	now := time.Now().UTC()

	if _, err := s.CreateSession(ctx, makeTestSession(now, "hash-1")); err != nil {
		t.Fatalf("create session 1: %v", err)
	}
	if _, err := s.CreateSession(ctx, makeTestSession(now, "hash-2")); err != nil {
		t.Fatalf("create session 2: %v", err)
	}

	if err := s.RevokeAllSessions(ctx); err != nil {
		t.Fatalf("RevokeAllSessions: %v", err)
	}

	list, err := s.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("len(list) = %d, want 2", len(list))
	}
	for _, sess := range list {
		if sess.RevokedAt.IsZero() {
			t.Errorf("session %q not revoked by RevokeAllSessions", sess.TokenHash)
		}
	}
}

func TestListSessionsNewestFirst(t *testing.T) {
	s := openTemp(t)
	ctx := t.Context()
	base := time.Now().UTC().Add(-time.Hour)

	if _, err := s.CreateSession(ctx, makeTestSession(base, "first")); err != nil {
		t.Fatalf("create first: %v", err)
	}
	if _, err := s.CreateSession(ctx, makeTestSession(base.Add(time.Minute), "second")); err != nil {
		t.Fatalf("create second: %v", err)
	}

	list, err := s.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("len(list) = %d, want 2", len(list))
	}
	if list[0].TokenHash != "second" || list[1].TokenHash != "first" {
		t.Errorf("order = [%q, %q], want [second, first] (newest first)", list[0].TokenHash, list[1].TokenHash)
	}
}

func TestPurgeExpiredSessions(t *testing.T) {
	s := openTemp(t)
	ctx := t.Context()
	now := time.Now().UTC()

	expired := makeTestSession(now.Add(-2*time.Hour), "expired")
	expired.ExpiresAt = now.Add(-time.Hour)
	if _, err := s.CreateSession(ctx, expired); err != nil {
		t.Fatalf("create expired: %v", err)
	}

	live := makeTestSession(now, "live")
	if _, err := s.CreateSession(ctx, live); err != nil {
		t.Fatalf("create live: %v", err)
	}

	n, err := s.PurgeExpiredSessions(ctx, now)
	if err != nil {
		t.Fatalf("PurgeExpiredSessions: %v", err)
	}
	if n != 1 {
		t.Fatalf("purged = %d, want 1", n)
	}

	list, err := s.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(list) != 1 || list[0].TokenHash != "live" {
		t.Errorf("survivors = %+v, want only 'live'", list)
	}
}

// --- auth events ----------------------------------------------------------

func TestAuthEventsRecordedForSuccessAndFailure(t *testing.T) {
	s := openTemp(t)
	ctx := t.Context()

	if err := s.RecordAuthEvent(ctx, "login", "203.0.113.9", true, "ok"); err != nil {
		t.Fatalf("recording success: %v", err)
	}
	if err := s.RecordAuthEvent(ctx, "login", "203.0.113.9", false, "bad password"); err != nil {
		t.Fatalf("recording failure: %v", err)
	}

	events, err := s.ListAuthEvents(ctx, 0)
	if err != nil {
		t.Fatalf("ListAuthEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(events))
	}
	// Newest first.
	if events[0].OK {
		t.Error("events[0] (most recent) should be the failure")
	}
	if events[0].Detail != "bad password" {
		t.Errorf("events[0].Detail = %q, want %q", events[0].Detail, "bad password")
	}
	if !events[1].OK || events[1].Detail != "ok" {
		t.Errorf("events[1] = %+v, want the success event", events[1])
	}
}

func TestListAuthEventsRespectsLimit(t *testing.T) {
	s := openTemp(t)
	ctx := t.Context()
	for i := 0; i < 5; i++ {
		if err := s.RecordAuthEvent(ctx, "login", "1.2.3.4", true, "ok"); err != nil {
			t.Fatalf("recording event %d: %v", i, err)
		}
	}
	events, err := s.ListAuthEvents(ctx, 2)
	if err != nil {
		t.Fatalf("ListAuthEvents: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("len(events) = %d, want 2", len(events))
	}
}

func TestRecentFailuresRespectsWindowAndIP(t *testing.T) {
	s := openTemp(t)
	ctx := t.Context()
	now := time.Now().UTC()

	// Two old failures from the target IP, outside the window.
	oldTime := now.Add(-2 * time.Hour)
	recordAuthEventAt(t, s, oldTime, "203.0.113.1", false)
	recordAuthEventAt(t, s, oldTime, "203.0.113.1", false)

	// One recent failure from the target IP, inside the window.
	recentTime := now.Add(-1 * time.Minute)
	recordAuthEventAt(t, s, recentTime, "203.0.113.1", false)

	// One recent failure from a different IP — must not count toward the
	// first IP's backoff.
	recordAuthEventAt(t, s, recentTime, "203.0.113.2", false)

	// One recent SUCCESS from the target IP — must not count as a failure.
	recordAuthEventAt(t, s, recentTime, "203.0.113.1", true)

	since := now.Add(-10 * time.Minute)
	n, err := s.RecentFailures(ctx, "203.0.113.1", since)
	if err != nil {
		t.Fatalf("RecentFailures: %v", err)
	}
	if n != 1 {
		t.Errorf("RecentFailures = %d, want 1 (window + IP scoping)", n)
	}
}

// recordAuthEventAt inserts an auth_events row with an explicit timestamp,
// bypassing RecordAuthEvent's now() stamping so RecentFailures's window
// logic can be tested without sleeping in real time.
func recordAuthEventAt(t *testing.T, s *Store, at time.Time, ip string, ok bool) {
	t.Helper()
	if _, err := s.writer.ExecContext(t.Context(),
		`INSERT INTO auth_events (at, kind, ip, ok, detail) VALUES (?, 'login', ?, ?, 'seed')`,
		formatTime(at), ip, boolToInt(ok),
	); err != nil {
		t.Fatalf("seeding auth event: %v", err)
	}
}
