// Authentication persistence: the writer-side half of PLAN.md §4. Every
// function here is a security boundary, not a convenience — see AGENTS.md's
// note that this package is where the replay/reuse races actually get
// resolved, not in application-level check-then-act code.
//
// Everything in this file is a method on *Store, i.e. writer-only, matching
// items.go and runs.go's split (store.go). The publish plane never touches
// any of this: it has no session, no admin credential, nothing to log in as.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/auth"
)

// ErrAdminExists is returned by InitAdmin when the singleton admin row
// already exists. There is exactly one admin (PLAN.md §4, §10: `admin.id`
// is CHECK(id = 1)), and silently overwriting it on a second `aff admin
// init` would be a way to lock the real admin out of their own account —
// so init refuses rather than clobbers.
var ErrAdminExists = errors.New("store: admin already exists")

// ErrTOTPReplay is returned by MarkTOTPStepUsed when the step has already
// been recorded. §4's replay prevention is enforced by the PRIMARY KEY on
// totp_used.step (migrations/0001_auth.sql), not by a check-then-insert in
// this function: two concurrent logins presenting the same code both reach
// this INSERT, and the database's uniqueness constraint decides which one
// wins the race — the loser gets this error, deterministically, no matter
// how the goroutines are scheduled.
var ErrTOTPReplay = errors.New("store: totp step already used")

// ErrRecoveryCodeUsed is returned by UseRecoveryCode when the code at the
// given index has already been consumed. PLAN.md §12.2 treats recovery
// codes as strictly single-use: a reusable one is a permanent backdoor, so
// this is an error rather than a silent no-op success.
var ErrRecoveryCodeUsed = errors.New("store: recovery code already used")

// ErrResetTokenInvalid is returned by CompletePasswordReset when tokenHash
// does not identify a row that is simultaneously unused and unexpired as of
// the given time. PLAN.md §4 requires a used token and an expired token to
// be refused with the SAME generic message an invalid token gets (SEC-34) —
// this one sentinel covers "never existed", "already used" and "expired"
// on purpose, so a caller cannot accidentally build three different
// user-facing messages out of three different errors.
var ErrResetTokenInvalid = errors.New("store: password reset token invalid, used, or expired")

// isConstraintErr reports whether err came from a SQLite constraint
// violation (UNIQUE, PRIMARY KEY, CHECK, ...). modernc.org/sqlite's error
// text names the failing constraint kind directly (e.g. "UNIQUE constraint
// failed: admin.id"), and the rest of this package already relies on that
// same substring convention (see store_test.go's read-only rejection
// check) rather than importing the driver's internal result-code
// machinery for a one-word classification.
func isConstraintErr(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "constraint")
}

// --- admin ----------------------------------------------------------------

// Admin is the singleton admin row (PLAN.md §10). TOTPSecretEnc is the
// AES-256-GCM ciphertext produced by auth.EncryptSecret — never the raw
// secret — and is nil until TOTP enrollment (SetTOTPSecret) runs.
type Admin struct {
	ID                int64
	PasswordHash      string
	KDFParams         string
	TOTPSecretEnc     []byte
	CreatedAt         time.Time
	PasswordChangedAt time.Time // zero value means "never changed since creation"
	// PepperVersion records which pepper generation (if any) PasswordHash was
	// derived under (migrations/0004_auth_hardening.sql). 0 means "no pepper
	// applied" — true for every row written before peppering existed, and the
	// only value that can be correct for those rows. A verifier MUST use this
	// to decide whether, and under which key, to pepper the login candidate —
	// never the server's current pepper configuration alone, or a row hashed
	// under an older (or no) pepper generation would never verify again.
	PepperVersion int
}

// InitAdmin creates the one and only admin row. It refuses if a row already
// exists (ErrAdminExists) rather than overwriting it — see ErrAdminExists.
func (s *Store) InitAdmin(ctx context.Context, passwordHash string, kdfParams string) error {
	now := formatTime(time.Now())
	_, err := s.writer.ExecContext(ctx,
		`INSERT INTO admin (id, password_hash, kdf_params, created_at) VALUES (1, ?, ?, ?)`,
		passwordHash, kdfParams, now)
	if isConstraintErr(err) {
		return ErrAdminExists
	}
	if err != nil {
		return fmt.Errorf("store: initializing admin: %w", err)
	}
	return nil
}

// GetAdmin returns the singleton admin row, or ErrNotFound if `aff admin
// init` has never run.
func (s *Store) GetAdmin(ctx context.Context) (Admin, error) {
	var (
		a                 Admin
		totpSecretEnc     []byte
		createdAt         string
		passwordChangedAt sql.NullString
	)
	err := s.writer.QueryRowContext(ctx,
		`SELECT id, password_hash, kdf_params, totp_secret_enc, created_at, password_changed_at, pepper_version
		 FROM admin WHERE id = 1`,
	).Scan(&a.ID, &a.PasswordHash, &a.KDFParams, &totpSecretEnc, &createdAt, &passwordChangedAt, &a.PepperVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return Admin{}, fmt.Errorf("store: admin: %w", ErrNotFound)
	}
	if err != nil {
		return Admin{}, fmt.Errorf("store: getting admin: %w", err)
	}
	a.TOTPSecretEnc = totpSecretEnc
	if a.CreatedAt, err = parseTime(createdAt); err != nil {
		return Admin{}, fmt.Errorf("store: parsing admin.created_at: %w", err)
	}
	if a.PasswordChangedAt, err = parseNullTime(passwordChangedAt); err != nil {
		return Admin{}, fmt.Errorf("store: parsing admin.password_changed_at: %w", err)
	}
	return a, nil
}

// UpdatePassword replaces the stored hash and KDF params, and stamps
// password_changed_at — the readback §12.5's Security pane shows and the
// signal a "rehash on next login because params got stronger" migration
// path (§4) depends on to know it actually happened.
//
// It also resets pepper_version to 0. That is deliberate, not incidental:
// every remaining caller of this exact function (cmd/aff's `admin init` /
// `admin reset` and internal/e2e's InitAdmin, both out of scope for this
// change) writes a plain, unpeppered hash — internal/rpc/auth.go's
// pepper-aware call sites use UpdatePasswordAndPepper below instead. Leaving
// pepper_version untouched here would let a break-glass CLI reset silently
// desync it from what was actually stored (still claiming "peppered" for a
// hash that is not), which is worse than resetting it: a verifier trusts
// this column to decide whether, and how, to pepper the login candidate.
func (s *Store) UpdatePassword(ctx context.Context, hash, kdfParams string) error {
	now := formatTime(time.Now())
	res, err := s.writer.ExecContext(ctx,
		`UPDATE admin SET password_hash = ?, kdf_params = ?, password_changed_at = ?, pepper_version = 0 WHERE id = 1`,
		hash, kdfParams, now)
	if err != nil {
		return fmt.Errorf("store: updating password: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: rows affected updating password: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("store: updating password: %w", ErrNotFound)
	}
	return nil
}

// UpdatePasswordAndPepper is UpdatePassword's pepper-aware sibling: it does
// the same write plus records which pepper generation (if any) hash was
// derived under, so a later verifier (GetAdmin + Admin.PepperVersion) knows
// whether — and under which key — to pepper the login candidate before
// comparing (PLAN.md §4: "a pepper_version column from day one so rotation
// is possible at all"). pepperVersion 0 means "hashed without a pepper",
// matching UpdatePassword's own behavior exactly when the caller has no
// pepper configured.
func (s *Store) UpdatePasswordAndPepper(ctx context.Context, hash, kdfParams string, pepperVersion int) error {
	now := formatTime(time.Now())
	res, err := s.writer.ExecContext(ctx,
		`UPDATE admin SET password_hash = ?, kdf_params = ?, password_changed_at = ?, pepper_version = ? WHERE id = 1`,
		hash, kdfParams, now, pepperVersion)
	if err != nil {
		return fmt.Errorf("store: updating password and pepper version: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: rows affected updating password and pepper version: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("store: updating password and pepper version: %w", ErrNotFound)
	}
	return nil
}

// SetTOTPSecret persists an already-encrypted TOTP secret. The parameter is
// named `encrypted`, not `secret`, so a caller who reaches for this
// function sees at the call site that it must have already run the value
// through auth.EncryptSecret — PLAN.md §4 requires the secret encrypted at
// rest specifically so a stolen DB file alone is not a second factor.
func (s *Store) SetTOTPSecret(ctx context.Context, encrypted []byte) error {
	res, err := s.writer.ExecContext(ctx,
		`UPDATE admin SET totp_secret_enc = ? WHERE id = 1`, encrypted)
	if err != nil {
		return fmt.Errorf("store: setting totp secret: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: rows affected setting totp secret: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("store: setting totp secret: %w", ErrNotFound)
	}
	return nil
}

// GetTOTPSecret returns the encrypted TOTP secret blob (still ciphertext —
// callers run it through auth.DecryptSecret). It returns (nil, nil), not an
// error, when the admin exists but has never enrolled TOTP: that is a valid
// state (pre-enrollment), distinct from ErrNotFound, which means the admin
// row itself does not exist.
func (s *Store) GetTOTPSecret(ctx context.Context) ([]byte, error) {
	var enc []byte
	err := s.writer.QueryRowContext(ctx,
		`SELECT totp_secret_enc FROM admin WHERE id = 1`,
	).Scan(&enc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: getting totp secret: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: getting totp secret: %w", err)
	}
	return enc, nil
}

// MarkTOTPStepUsed records that a TOTP step's code has been consumed. It
// returns ErrTOTPReplay if the step is already recorded — see ErrTOTPReplay
// for why that check is the PRIMARY KEY on totp_used.step doing the work,
// not a SELECT-then-INSERT here.
func (s *Store) MarkTOTPStepUsed(ctx context.Context, step int64, codeHash string) error {
	now := formatTime(time.Now())
	_, err := s.writer.ExecContext(ctx,
		`INSERT INTO totp_used (step, code_hash, at) VALUES (?, ?, ?)`,
		step, codeHash, now)
	if isConstraintErr(err) {
		return ErrTOTPReplay
	}
	if err != nil {
		return fmt.Errorf("store: marking totp step %d used: %w", step, err)
	}
	return nil
}

// --- recovery codes ---------------------------------------------------------

// StoreRecoveryCodes replaces the entire set of recovery codes with hashes —
// only hashes, never the raw codes (PLAN.md §12.2: "shown once, stored
// hashed"). It is a full replace, not an append, because re-enrollment
// (SystemService.RegenerateRecoveryCodes) must invalidate every old code,
// not accumulate them forever. The delete and inserts run in one
// transaction so a caller never observes a half-replaced set (e.g. zero
// codes) if the process dies partway through.
func (s *Store) StoreRecoveryCodes(ctx context.Context, hashes []string) error {
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin storing recovery codes: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM recovery_codes`); err != nil {
		return fmt.Errorf("store: clearing recovery codes: %w", err)
	}

	for _, h := range hashes {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO recovery_codes (code_hash) VALUES (?)`, h,
		); err != nil {
			return fmt.Errorf("store: storing recovery code: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: committing recovery codes: %w", err)
	}
	return nil
}

// UseRecoveryCode marks the code at the given index (its position, 0-based,
// among the currently-stored set ordered by insertion — the same order
// StoreRecoveryCodes wrote them in and the same order auth.VerifyCode
// resolves a match to) as used. It refuses if that code was already used:
// PLAN.md §12.2 is explicit that a recovery code is single-use, and a code
// that can be replayed is a standing backdoor into the one account this
// system has.
//
// The lookup-then-update is race-safe against two concurrent uses of the
// same code: the UPDATE's `WHERE used_at IS NULL` means only one concurrent
// caller can flip a given row from unused to used, so the loser sees
// RowsAffected = 0 and gets ErrRecoveryCodeUsed even though its SELECT ran
// before the winner's UPDATE committed.
func (s *Store) UseRecoveryCode(ctx context.Context, index int) error {
	if index < 0 {
		return fmt.Errorf("store: recovery code index %d is negative", index)
	}

	var id int64
	err := s.writer.QueryRowContext(ctx,
		`SELECT id FROM recovery_codes ORDER BY id ASC LIMIT 1 OFFSET ?`, index,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("store: recovery code at index %d: %w", index, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("store: finding recovery code at index %d: %w", index, err)
	}

	now := formatTime(time.Now())
	res, err := s.writer.ExecContext(ctx,
		`UPDATE recovery_codes SET used_at = ? WHERE id = ? AND used_at IS NULL`,
		now, id)
	if err != nil {
		return fmt.Errorf("store: marking recovery code used: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: rows affected marking recovery code used: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("store: recovery code at index %d: %w", index, ErrRecoveryCodeUsed)
	}
	return nil
}

// CountUnusedRecoveryCodes feeds §12.2's "at <=2 remaining, the dashboard
// nags" behavior.
func (s *Store) CountUnusedRecoveryCodes(ctx context.Context) (int, error) {
	var n int
	err := s.writer.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM recovery_codes WHERE used_at IS NULL`,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: counting unused recovery codes: %w", err)
	}
	return n, nil
}

// --- sessions ---------------------------------------------------------------

const sessionColumns = `id, token_hash, created_at, last_seen_at, expires_at, ip, user_agent, revoked_at`

func scanSession(sc rowScanner) (int64, auth.Session, error) {
	var (
		id                               int64
		sess                             auth.Session
		createdAt, lastSeenAt, expiresAt string
		ip, userAgent, revokedAt         sql.NullString
	)
	if err := sc.Scan(&id, &sess.TokenHash, &createdAt, &lastSeenAt, &expiresAt, &ip, &userAgent, &revokedAt); err != nil {
		return 0, auth.Session{}, err
	}
	sess.IP = ip.String
	sess.UserAgent = userAgent.String

	var err error
	if sess.CreatedAt, err = parseTime(createdAt); err != nil {
		return 0, auth.Session{}, fmt.Errorf("store: parsing sessions.created_at: %w", err)
	}
	if sess.LastSeenAt, err = parseTime(lastSeenAt); err != nil {
		return 0, auth.Session{}, fmt.Errorf("store: parsing sessions.last_seen_at: %w", err)
	}
	if sess.ExpiresAt, err = parseTime(expiresAt); err != nil {
		return 0, auth.Session{}, fmt.Errorf("store: parsing sessions.expires_at: %w", err)
	}
	if sess.RevokedAt, err = parseNullTime(revokedAt); err != nil {
		return 0, auth.Session{}, fmt.Errorf("store: parsing sessions.revoked_at: %w", err)
	}
	return id, sess, nil
}

// CreateSession persists a new session row and returns its id. Only
// s.TokenHash is ever written — CreateSession has no way to be handed a raw
// token, by construction of auth.Session's field name, which is the point:
// PLAN.md §4 requires the raw token exist only in the Set-Cookie response
// and nowhere in storage.
func (s *Store) CreateSession(ctx context.Context, sess auth.Session) (int64, error) {
	var revokedAt any
	if !sess.RevokedAt.IsZero() {
		revokedAt = formatTime(sess.RevokedAt)
	}
	res, err := s.writer.ExecContext(ctx, `
		INSERT INTO sessions (token_hash, created_at, last_seen_at, expires_at, ip, user_agent, revoked_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sess.TokenHash, formatTime(sess.CreatedAt), formatTime(sess.LastSeenAt), formatTime(sess.ExpiresAt),
		nullString(sess.IP), nullString(sess.UserAgent), revokedAt)
	if err != nil {
		return 0, fmt.Errorf("store: creating session: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: session id: %w", err)
	}
	return id, nil
}

// GetSessionByTokenHash looks up a session the way every authenticated
// request does: by the SHA-256 hash of the cookie's token (auth.HashToken),
// never by the raw value — the raw token never reaches this package.
func (s *Store) GetSessionByTokenHash(ctx context.Context, hash string) (auth.Session, error) {
	row := s.writer.QueryRowContext(ctx,
		`SELECT `+sessionColumns+` FROM sessions WHERE token_hash = ?`, hash)
	_, sess, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return auth.Session{}, fmt.Errorf("store: session: %w", ErrNotFound)
	}
	if err != nil {
		return auth.Session{}, fmt.Errorf("store: getting session: %w", err)
	}
	return sess, nil
}

// TouchSession renews last_seen_at, the clock the idle timeout (§4, 60m)
// measures against. Every authenticated RPC calls this — "authenticated at
// upgrade, trusted forever" is exactly what §4 refuses to allow.
func (s *Store) TouchSession(ctx context.Context, id int64, at time.Time) error {
	res, err := s.writer.ExecContext(ctx,
		`UPDATE sessions SET last_seen_at = ? WHERE id = ?`, formatTime(at), id)
	if err != nil {
		return fmt.Errorf("store: touching session %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: rows affected touching session %d: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("store: touching session %d: %w", id, ErrNotFound)
	}
	return nil
}

// RevokeSession kills one session (logout, or the admin revoking a device
// from §12.5). It is idempotent: revoking an already-revoked session keeps
// the original revocation time (COALESCE) rather than erroring or sliding
// the timestamp forward, so calling it twice — e.g. a retried RPC — is
// harmless. Revoking a session that does not exist at all is still an
// error, since that likely means the caller has the wrong id.
func (s *Store) RevokeSession(ctx context.Context, id int64) error {
	now := formatTime(time.Now())
	res, err := s.writer.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = COALESCE(revoked_at, ?) WHERE id = ?`, now, id)
	if err != nil {
		return fmt.Errorf("store: revoking session %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: rows affected revoking session %d: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("store: revoking session %d: %w", id, ErrNotFound)
	}
	return nil
}

// RevokeAllSessions kills every still-active session in one statement — the
// "revoke all" kebab action (§12.5) and the forced-full-relogin step of the
// recovery-code path (§12.2). Zero affected rows (nothing was active) is
// not an error; there is nothing wrong with revoking an empty set.
func (s *Store) RevokeAllSessions(ctx context.Context) error {
	now := formatTime(time.Now())
	if _, err := s.writer.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = COALESCE(revoked_at, ?) WHERE revoked_at IS NULL`, now,
	); err != nil {
		return fmt.Errorf("store: revoking all sessions: %w", err)
	}
	return nil
}

// ListSessions returns every session, newest-first, for the §12.5 "active
// sessions with device/IP/last-seen" table. It intentionally does not
// filter out expired or revoked rows — the admin UI is allowed to show a
// dead session (greyed out, say); filtering that is a rendering decision,
// not a storage one.
func (s *Store) ListSessions(ctx context.Context) ([]auth.Session, error) {
	rows, err := s.writer.QueryContext(ctx,
		`SELECT `+sessionColumns+` FROM sessions ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("store: listing sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []auth.Session
	for rows.Next() {
		_, sess, err := scanSession(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scanning session: %w", err)
		}
		out = append(out, sess)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterating sessions: %w", err)
	}
	return out, nil
}

// --- session scope -----------------------------------------------------

// SessionScopeFull and SessionScopeElevated are the two values
// sessions.scope can hold (migrations/0005_session_scope.sql, whose CHECK
// constraint is the authority — these constants exist so nothing in Go
// spells either value as a bare string literal that could drift from it).
//
// Full is what every session Login mints gets, and what every session that
// existed before this column did — an ordinary session must keep reaching
// everything it always could. Elevated is the narrow scope a
// RecoverWithCode session is moved into; PLAN.md §12.2 requires it reach
// only password change and TOTP re-enrollment, enforced centrally by
// internal/rpc/interceptor.go's authorize, not by individual handlers.
const (
	SessionScopeFull     = "full"
	SessionScopeElevated = "elevated"
)

// SetSessionScope sets the scope column on session id. The interceptor
// calls this on every authorized request so a session's row always
// reflects what the session currently is — full or elevated — rather than
// that fact living only in the server's process memory, which would not
// survive being read back by anything else and would not be "the session
// itself" carrying its own scope.
func (s *Store) SetSessionScope(ctx context.Context, id int64, scope string) error {
	res, err := s.writer.ExecContext(ctx,
		`UPDATE sessions SET scope = ? WHERE id = ?`, scope, id)
	if err != nil {
		return fmt.Errorf("store: setting session %d scope: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: rows affected setting session %d scope: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("store: setting session %d scope: %w", id, ErrNotFound)
	}
	return nil
}

// SessionScope returns the current scope column for session id — the value
// authorize's default-deny check enforces against.
func (s *Store) SessionScope(ctx context.Context, id int64) (string, error) {
	var scope string
	err := s.writer.QueryRowContext(ctx,
		`SELECT scope FROM sessions WHERE id = ?`, id).Scan(&scope)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("store: session %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("store: getting session %d scope: %w", id, err)
	}
	return scope, nil
}

// --- password reset tokens --------------------------------------------------

// CreatePasswordResetToken persists a newly issued reset token by its hash
// and expiry only — the raw token itself never reaches this package (see
// internal/auth/reset.go's NewResetToken: raw goes to the caller exactly
// once, hash is what gets stored), mirroring sessions' "only the hash is a
// row" construction.
func (s *Store) CreatePasswordResetToken(ctx context.Context, tokenHash string, expiresAt time.Time) error {
	if _, err := s.writer.ExecContext(ctx,
		`INSERT INTO password_reset_tokens (token_hash, expires_at) VALUES (?, ?)`,
		tokenHash, formatTime(expiresAt),
	); err != nil {
		return fmt.Errorf("store: creating password reset token: %w", err)
	}
	return nil
}

// ActiveResetTokenHashes returns the token_hash of every reset token that is
// still unused and unexpired as of now. There is no exported way to hash a
// raw token from outside internal/auth (hashResetToken is unexported by
// design — see reset.go), so a caller holding only an incoming raw token
// looks it up the same way internal/rpc/auth.go already resolves a recovery
// code: fetch the small set of live candidates and run
// auth.VerifyResetToken(raw, candidate) against each, in constant time per
// candidate, rather than reversing a hash. On a single-admin system this set
// is realistically zero or one rows.
func (s *Store) ActiveResetTokenHashes(ctx context.Context, now time.Time) ([]string, error) {
	rows, err := s.writer.QueryContext(ctx,
		`SELECT token_hash FROM password_reset_tokens WHERE used_at IS NULL AND expires_at > ? ORDER BY id ASC`,
		formatTime(now))
	if err != nil {
		return nil, fmt.Errorf("store: listing active password reset tokens: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, fmt.Errorf("store: scanning password reset token: %w", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterating password reset tokens: %w", err)
	}
	return out, nil
}

// CompletePasswordReset is the entire "using a reset token" operation
// (PLAN.md §4, §12.2; internal/auth/reset.go's doc comment on VerifyResetToken,
// which documents this exact obligation for "the caller completing a
// reset"), done as ONE transaction: mark tokenHash used, write the new
// password hash (and its pepper generation), and revoke every existing
// session. Skipping any one of the three leaves a real gap — see reset.go's
// comment for which gap each omission opens.
//
// Single-use under concurrency is enforced by the UPDATE's
// `WHERE used_at IS NULL AND expires_at > ?` clause, not by a SELECT this
// function runs first: SQLite serializes writers, so of two callers racing
// the same tokenHash, exactly one UPDATE affects a row. The loser's
// RowsAffected is 0, this function returns ErrResetTokenInvalid, and the
// whole transaction (including the password write and session revocation
// that would otherwise have followed) rolls back — a losing racer never
// partially applies a reset. That is the database deciding the race, not a
// check-then-act sequence in this Go code.
func (s *Store) CompletePasswordReset(ctx context.Context, tokenHash string, now time.Time, newHash, kdfParams string, pepperVersion int) error {
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin completing password reset: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	nowStr := formatTime(now)
	res, err := tx.ExecContext(ctx,
		`UPDATE password_reset_tokens SET used_at = ? WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`,
		nowStr, tokenHash, nowStr)
	if err != nil {
		return fmt.Errorf("store: consuming password reset token: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: rows affected consuming password reset token: %w", err)
	}
	if n == 0 {
		return ErrResetTokenInvalid
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE admin SET password_hash = ?, kdf_params = ?, password_changed_at = ?, pepper_version = ? WHERE id = 1`,
		newHash, kdfParams, nowStr, pepperVersion,
	); err != nil {
		return fmt.Errorf("store: writing new password during reset: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = COALESCE(revoked_at, ?) WHERE revoked_at IS NULL`, nowStr,
	); err != nil {
		return fmt.Errorf("store: revoking sessions during reset: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: committing password reset: %w", err)
	}
	return nil
}

// PurgeExpiredSessions deletes sessions whose absolute lifetime has passed
// as of now, and reports how many were removed. This is housekeeping, not a
// security control by itself — Session.Valid already refuses an expired
// session on every use — but an unbounded sessions table is still a table
// that should not grow forever on a single-admin system that logs in daily.
//
// Nothing calls it. The nightly job (internal/ops.Prune) prunes exactly three
// things — expired samples, over-window embeddings, old non-failure runs —
// and its package doc states that boundary deliberately, so sessions are
// simply not in it. Left unwired rather than added there: at a 12h absolute
// lifetime and one admin logging in daily, this is on the order of a few
// hundred rows a year, which is not worth widening a stated scope for. If the
// deployment shape ever changes — more operators, shorter sessions, a
// programmatic login — this is the function to wire, and it wants a
// PruneResult field beside the other three rather than a call of its own.
func (s *Store) PurgeExpiredSessions(ctx context.Context, now time.Time) (int, error) {
	res, err := s.writer.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at < ?`, formatTime(now))
	if err != nil {
		return 0, fmt.Errorf("store: purging expired sessions: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: rows affected purging expired sessions: %w", err)
	}
	return int(n), nil
}

// --- auth events --------------------------------------------------------

// AuthEvent is one row of the audit trail §4 requires for every login
// attempt, success or failure, and §12.2 calls out as "the one event worth
// alerting on" for recovery attempts.
type AuthEvent struct {
	ID     int64
	At     time.Time
	Kind   string
	IP     string
	OK     bool
	Detail string
}

// RecordAuthEvent logs one authentication attempt. PLAN.md §4 requires
// EVERY attempt logged, success or failure — this function takes no path
// that silently drops a failure, and callers must call it on both branches
// of a login rather than only the ones that look interesting at the time.
func (s *Store) RecordAuthEvent(ctx context.Context, kind, ip string, ok bool, detail string) error {
	_, err := s.writer.ExecContext(ctx,
		`INSERT INTO auth_events (at, kind, ip, ok, detail) VALUES (?, ?, ?, ?, ?)`,
		formatTime(time.Now()), kind, nullString(ip), boolToInt(ok), nullString(detail))
	if err != nil {
		return fmt.Errorf("store: recording auth event: %w", err)
	}
	return nil
}

// ListAuthEvents returns the audit trail newest-first, for the security
// pane / login-hardening review. limit <= 0 means "no cap", matching
// ListItems/ListRuns's convention (items.go, runs.go).
func (s *Store) ListAuthEvents(ctx context.Context, limit int) ([]AuthEvent, error) {
	if limit <= 0 {
		limit = -1
	}
	rows, err := s.writer.QueryContext(ctx,
		`SELECT id, at, kind, ip, ok, detail FROM auth_events ORDER BY at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: listing auth events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []AuthEvent
	for rows.Next() {
		var (
			e       AuthEvent
			at      string
			ip, det sql.NullString
			ok      int64
		)
		if err := rows.Scan(&e.ID, &at, &e.Kind, &ip, &ok, &det); err != nil {
			return nil, fmt.Errorf("store: scanning auth event: %w", err)
		}
		if e.At, err = parseTime(at); err != nil {
			return nil, fmt.Errorf("store: parsing auth_events.at: %w", err)
		}
		e.IP = ip.String
		e.Detail = det.String
		e.OK = ok != 0
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterating auth events: %w", err)
	}
	return out, nil
}

// RecentFailures counts failed attempts from ip since the given time.
//
// NOT the query the live backoff reads, despite what this comment used to
// say. §4's per-IP exponential backoff is implemented in internal/rpc/auth.go
// as an in-process backoffTracker (blocked/recordFailure/recordSuccess), and
// this method has no caller outside tests. Anyone auditing the throttle by
// following this comment would have concluded the database was its source of
// truth and gone looking for a bug in the wrong layer.
//
// The difference is not academic, and it is the reason this is left in place
// rather than deleted: an in-process tracker starts empty on every restart
// and is not shared between processes, so backoff state does not survive a
// deploy. That is acceptable for a single-admin, single-process deployment —
// restarts are operator-driven, not attacker-driven — and it is what this
// method would fix if the trade ever stops being acceptable.
func (s *Store) RecentFailures(ctx context.Context, ip string, since time.Time) (int, error) {
	var n int
	err := s.writer.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM auth_events WHERE ip = ? AND ok = 0 AND at >= ?`,
		ip, formatTime(since)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: counting recent failures for %q: %w", ip, err)
	}
	return n, nil
}
