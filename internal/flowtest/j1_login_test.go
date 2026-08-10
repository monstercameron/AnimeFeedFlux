// J1 — first login (PLAN.md §22 "J1 — First login", TODOS.md BF-01..05).
//
// AuthService (TODOS.md B1-02) and its interceptor (B1-08) do not exist yet,
// so there is no RPC to drive this flow through. What DOES exist, and is
// fully real, is everything AuthService will be built out of:
// internal/auth's password/TOTP/session primitives and internal/store's
// auth persistence (auth_events, sessions, totp_used, admin). This file
// wires those together into the same two-step "password, then TOTP" flow
// the real login handler will implement, so the sanity assertions run
// against a real store and real crypto rather than a mock — exactly what
// §17.5 asks for.
package flowtest

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/auth"
	"github.com/pquerna/otp/totp"
)

// j1Password is a long passphrase already proven to clear both the length
// floor and the breached-password blocklist (internal/auth/password_test.go
// uses the identical literal), so these tests are never accidentally
// exercising IsWeak's rejection path instead of the login flow.
const j1Password = "correct-horse-battery-staple-9!"

const (
	j1IPKnown       = "203.0.113.10" // the IP a legitimate login attempt comes from
	j1IPUnknownUser = "203.0.113.20" // a different IP, so RecentFailures never conflates the two scenarios
	j1TOTPIssuer    = "AnimeFeedFlux"
	j1TOTPAccount   = "admin"
)

// j1SecretKey stands in for the env-supplied AFF_SECRET_KEY (§4) that
// encrypts the TOTP secret at rest.
var j1SecretKey = []byte("j1-flowtest-totp-encryption-key")

// errJ1GenericLogin is the ONE message a wrong password and a login attempt
// against a not-yet-initialized admin must both produce (BF-04) — the
// generic failure message §4's login hardening requires, carrying no hint
// about which of the two actually happened.
var errJ1GenericLogin = errors.New("invalid credentials")

// j1DummyHash is a validly-encoded argon2id hash that does not correspond to
// any real password. A login attempt against a nonexistent admin still runs
// auth.Verify against THIS hash rather than short-circuiting before ever
// touching argon2id — that is what keeps "no admin exists" and "wrong
// password" costing the same CPU time (BF-04's timing half).
var j1DummyHash = func() string {
	h, err := auth.Hash("a dummy passphrase never used to log in, ever", auth.DefaultParams())
	if err != nil {
		panic(err)
	}
	return h
}()

// j1SetupAdmin performs the equivalent of `aff admin init` followed by TOTP
// enrollment: hash j1Password with the real argon2id parameters, persist the
// admin row, mint a TOTP secret, and store it encrypted exactly as §4
// requires (never the raw secret). Returns the raw secret so tests can mint
// valid codes with auth.ValidateCode.
func j1SetupAdmin(t *testing.T, w *World) (secret string) {
	t.Helper()
	ctx := t.Context()

	hash, err := auth.Hash(j1Password, auth.DefaultParams())
	if err != nil {
		t.Fatalf("hashing admin password: %v", err)
	}
	if err := w.Store.InitAdmin(ctx, hash, "argon2id-default"); err != nil {
		t.Fatalf("InitAdmin: %v", err)
	}

	secret, _, err = auth.Enroll(j1TOTPAccount, j1TOTPIssuer)
	if err != nil {
		t.Fatalf("auth.Enroll: %v", err)
	}
	enc, err := auth.EncryptSecret(secret, j1SecretKey)
	if err != nil {
		t.Fatalf("auth.EncryptSecret: %v", err)
	}
	if err := w.Store.SetTOTPSecret(ctx, enc); err != nil {
		t.Fatalf("SetTOTPSecret: %v", err)
	}
	return secret
}

// j1AttemptPassword is login step 1 (§4, §22 J1). It looks up the admin,
// falls back to j1DummyHash when none exists so the KDF always runs, and
// logs the attempt to auth_events on both branches (BF-03) — never only on
// the interesting one.
func j1AttemptPassword(ctx context.Context, w *World, password, ip string) error {
	admin, gerr := w.Store.GetAdmin(ctx)
	hash := j1DummyHash
	if gerr == nil {
		hash = admin.PasswordHash
	}
	// auth.Verify always runs the real argon2id derivation regardless of
	// which hash it was handed — that uniform cost is what BF-04's timing
	// claim rests on.
	verified, _, verr := auth.Verify(password, hash)
	ok := gerr == nil && verr == nil && verified

	if err := w.Store.RecordAuthEvent(ctx, "login_password", ip, ok, ""); err != nil {
		return err
	}
	if !ok {
		return errJ1GenericLogin
	}
	return nil
}

// j1AttemptTOTP is login step 2. It decrypts the stored secret, validates
// the code (±1 step per §4), and marks the step used — MarkTOTPStepUsed's
// PRIMARY KEY is what turns a second presentation of the same code into
// ErrTOTPReplay (BF-05) rather than a second successful login.
func j1AttemptTOTP(ctx context.Context, w *World, code, ip string) error {
	encSecret, err := w.Store.GetTOTPSecret(ctx)
	if err != nil {
		_ = w.Store.RecordAuthEvent(ctx, "login_totp", ip, false, "no_totp_secret")
		return errJ1GenericLogin
	}
	secret, err := auth.DecryptSecret(encSecret, j1SecretKey)
	if err != nil {
		_ = w.Store.RecordAuthEvent(ctx, "login_totp", ip, false, "decrypt_failed")
		return errJ1GenericLogin
	}

	step, valid := auth.ValidateCode(secret, code, w.Clock.Now())
	if !valid {
		_ = w.Store.RecordAuthEvent(ctx, "login_totp", ip, false, "invalid_code")
		return errJ1GenericLogin
	}

	if err := w.Store.MarkTOTPStepUsed(ctx, step, auth.HashToken(code)); err != nil {
		// Covers both "genuinely wrong code" (never reaches here) and the
		// replay case: same step, already recorded.
		_ = w.Store.RecordAuthEvent(ctx, "login_totp", ip, false, "replayed_or_conflicting_step")
		return errJ1GenericLogin
	}

	_ = w.Store.RecordAuthEvent(ctx, "login_totp", ip, true, "")
	return nil
}

// j1CreateSession is the step after both factors clear: mint a session,
// store only its hash (§4 — the raw token never lives in the database), and
// build the Set-Cookie value the browser actually receives.
func j1CreateSession(ctx context.Context, w *World, ip string) (id int64, cookie *http.Cookie, err error) {
	raw, hash, err := auth.NewToken()
	if err != nil {
		return 0, nil, err
	}
	now := w.Clock.Now()
	sess := auth.Session{
		TokenHash:  hash,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(auth.SessionAbsoluteLifetime),
		IP:         ip,
	}
	id, err = w.Store.CreateSession(ctx, sess)
	if err != nil {
		return 0, nil, err
	}
	return id, auth.NewSessionCookie(raw, sess.ExpiresAt), nil
}

// j1Login drives the whole two-step flow end to end, exactly as the steps in
// §22 J1 describe: "submit password → submit TOTP → land on the default
// surface". It stops at the first failing step, matching how a real handler
// would refuse to even prompt for TOTP against a rejected password.
func j1Login(t *testing.T, w *World, password, code, ip string) (sessionID int64, cookie *http.Cookie, err error) {
	t.Helper()
	ctx := t.Context()
	if err := j1AttemptPassword(ctx, w, password, ip); err != nil {
		return 0, nil, err
	}
	if err := j1AttemptTOTP(ctx, w, code, ip); err != nil {
		return 0, nil, err
	}
	return j1CreateSession(ctx, w, ip)
}

// j1ValidCode mints a currently-valid TOTP code for secret. internal/auth
// exposes no "generate code" function — auth.ValidateCode only checks one —
// so this calls the same pquerna/otp/totp library internal/auth's own
// implementation is built on, directly, exactly the way a real authenticator
// app would compute the next code for the enrolled secret.
func j1ValidCode(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, at)
	if err != nil {
		t.Fatalf("generating a valid TOTP code: %v", err)
	}
	return code
}

// TestJ1_ExactlyOneUnexpiredSessionAfterLogin is BF-01.
func TestJ1_ExactlyOneUnexpiredSessionAfterLogin(t *testing.T) {
	w := New(t)
	ctx := t.Context()
	secret := j1SetupAdmin(t, w)

	code := j1ValidCode(t, secret, w.Clock.Now())
	sessionID, cookie, err := j1Login(t, w, j1Password, code, j1IPKnown)
	if err != nil {
		t.Fatalf("j1Login: %v", err)
	}
	if sessionID == 0 || cookie == nil {
		t.Fatal("j1Login returned a zero session id or nil cookie on success")
	}

	// BF-01 (§22 J1): exactly one unexpired session row exists.
	sessions, err := w.Store.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("ListSessions returned %d rows, want exactly 1", len(sessions))
	}
	if !sessions[0].Valid(w.Clock.Now(), auth.SessionIdleTimeout) {
		t.Fatal("the one session row is not valid (expired, idle-timed-out, or revoked) immediately after login")
	}
}

// TestJ1_SessionCookieFlags is BF-02.
func TestJ1_SessionCookieFlags(t *testing.T) {
	w := New(t)
	secret := j1SetupAdmin(t, w)
	code := j1ValidCode(t, secret, w.Clock.Now())

	_, cookie, err := j1Login(t, w, j1Password, code, j1IPKnown)
	if err != nil {
		t.Fatalf("j1Login: %v", err)
	}

	// BF-02 (§22 J1): HttpOnly, Secure, SameSite=Strict, the __Host- prefix,
	// and — per §4 — NO Domain attribute, ever (the __Host- prefix is only
	// honored by browsers when Domain is entirely absent).
	if !cookie.HttpOnly {
		t.Error("cookie is missing HttpOnly")
	}
	if !cookie.Secure {
		t.Error("cookie is missing Secure")
	}
	if cookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("cookie SameSite = %v, want SameSiteStrictMode", cookie.SameSite)
	}
	if cookie.Domain != "" {
		t.Errorf("cookie Domain = %q, want empty (the __Host- prefix requires no Domain attribute)", cookie.Domain)
	}
	if cookie.Path != "/" {
		t.Errorf("cookie Path = %q, want \"/\"", cookie.Path)
	}
	const wantName = "__Host-aff_session"
	if cookie.Name != wantName {
		t.Errorf("cookie Name = %q, want %q", cookie.Name, wantName)
	}
}

// TestJ1_EveryAttemptLandsInAuthEvents is BF-03.
func TestJ1_EveryAttemptLandsInAuthEvents(t *testing.T) {
	w := New(t)
	ctx := t.Context()
	secret := j1SetupAdmin(t, w)

	// One successful full login.
	code := j1ValidCode(t, secret, w.Clock.Now())
	if _, _, err := j1Login(t, w, j1Password, code, j1IPKnown); err != nil {
		t.Fatalf("successful j1Login: %v", err)
	}

	// One wrong-password attempt (fails at step 1, never reaches TOTP).
	if err := j1AttemptPassword(ctx, w, "not the right passphrase at all!!", j1IPKnown); err == nil {
		t.Fatal("wrong password unexpectedly succeeded")
	}

	// One wrong-TOTP attempt against a correct password.
	if err := j1AttemptPassword(ctx, w, j1Password, j1IPKnown); err != nil {
		t.Fatalf("password step of the wrong-TOTP attempt: %v", err)
	}
	if err := j1AttemptTOTP(ctx, w, "000000", j1IPKnown); err == nil {
		t.Fatal("wrong TOTP code unexpectedly succeeded")
	}

	// BF-03 (§22 J1): every attempt, success or failure, appears in
	// auth_events. That's 2 password events (1 ok, 1 not) + 3 totp events (2
	// ok from the two password-step successes above, 1 not from the bad
	// code) = 5.
	events, err := w.Store.ListAuthEvents(ctx, 0)
	if err != nil {
		t.Fatalf("ListAuthEvents: %v", err)
	}
	if len(events) != 5 {
		t.Fatalf("ListAuthEvents returned %d rows, want 5 (one per attempted step above)", len(events))
	}
	var oks, fails int
	for _, e := range events {
		if e.OK {
			oks++
		} else {
			fails++
		}
	}
	if oks != 3 || fails != 2 {
		t.Fatalf("auth_events has %d ok / %d failed, want 3 ok / 2 failed", oks, fails)
	}
}

// TestJ1_WrongPasswordAndUnknownAccountIndistinguishable is BF-04, covering
// both halves the sanity assertion names: the message and the timing.
func TestJ1_WrongPasswordAndUnknownAccountIndistinguishable(t *testing.T) {
	// Message: a fresh World with no admin ever initialized ("unknown
	// account", the closest this single-admin system has to that concept —
	// §4 has no username, so "no admin row exists yet" is the analogous
	// state) versus an initialized admin given the wrong password.
	wUnknown := New(t)
	errUnknown := j1AttemptPassword(t.Context(), wUnknown, j1Password, j1IPUnknownUser)

	wWrongPW := New(t)
	j1SetupAdmin(t, wWrongPW)
	errWrongPW := j1AttemptPassword(t.Context(), wWrongPW, "definitely the wrong passphrase!!", j1IPKnown)

	if errUnknown == nil || errWrongPW == nil {
		t.Fatalf("expected both attempts to fail; got unknown=%v wrongPW=%v", errUnknown, errWrongPW)
	}
	if !errors.Is(errUnknown, errJ1GenericLogin) || !errors.Is(errWrongPW, errJ1GenericLogin) {
		t.Fatalf("messages differ: unknown=%q wrongPW=%q, want the same generic message for both", errUnknown, errWrongPW)
	}

	// Timing: both branches must run the real argon2id derivation, so the
	// two should cost about the same. Measured across several iterations
	// (against separate, otherwise-idle Worlds so RecentFailures backoff
	// never enters into it) and compared with a generous tolerance —
	// tight enough to catch a short-circuit that skips the KDF outright
	// (which would make one branch orders of magnitude faster), loose
	// enough not to flake on shared CI hardware.
	const iterations = 5
	unknownDur := j1MeasureAttempts(t, iterations, func() {
		w := New(t)
		_ = j1AttemptPassword(t.Context(), w, j1Password, j1IPUnknownUser)
	})
	wrongPWDur := j1MeasureAttempts(t, iterations, func() {
		w := New(t)
		j1SetupAdmin(t, w)
		_ = j1AttemptPassword(t.Context(), w, "definitely the wrong passphrase!!", j1IPKnown)
	})

	ratio := float64(unknownDur) / float64(wrongPWDur)
	if ratio < 0.3 || ratio > 3.0 {
		t.Fatalf("unknown-account attempts took %v, wrong-password attempts took %v (ratio %.2f) — too far apart for uniform-timing (BF-04)",
			unknownDur, wrongPWDur, ratio)
	}
}

func j1MeasureAttempts(t *testing.T, n int, attempt func()) time.Duration {
	t.Helper()
	start := time.Now()
	for i := 0; i < n; i++ {
		attempt()
	}
	return time.Since(start)
}

// TestJ1_TOTPStepCannotBeReplayed is BF-05.
func TestJ1_TOTPStepCannotBeReplayed(t *testing.T) {
	w := New(t)
	ctx := t.Context()
	secret := j1SetupAdmin(t, w)
	if err := j1AttemptPassword(ctx, w, j1Password, j1IPKnown); err != nil {
		t.Fatalf("password step: %v", err)
	}

	code := j1ValidCode(t, secret, w.Clock.Now())
	if err := j1AttemptTOTP(ctx, w, code, j1IPKnown); err != nil {
		t.Fatalf("first use of a fresh TOTP code should succeed: %v", err)
	}

	// BF-05 (§22 J1): the TOTP step just used cannot be replayed. Presenting
	// the exact same code again (still within its validity window) must be
	// refused, even though auth.ValidateCode alone would happily accept it
	// again — MarkTOTPStepUsed's PRIMARY KEY is what turns that into a
	// refusal.
	if err := j1AttemptPassword(ctx, w, j1Password, j1IPKnown); err != nil {
		t.Fatalf("password step of the replay attempt: %v", err)
	}
	if err := j1AttemptTOTP(ctx, w, code, j1IPKnown); err == nil {
		t.Fatal("replaying the same TOTP code succeeded, want it refused")
	}
}
