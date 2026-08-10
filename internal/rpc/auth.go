// AuthServiceServer: the entire defense of a single-admin, no-authorization
// system (PLAN.md §4). Every rule enforced here exists to close a specific
// oracle or replay hole called out in the plan — see the comment on each
// method for which one.
package rpc

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/auth"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// SessionTokenHeader is the outgoing gRPC header AuthServer.Login and
// RecoverWithCode attach the raw session token to on success, via
// grpc.SetHeader. The raw token is deliberately NEVER placed in a response
// message body — PLAN.md §4 requires it exist only as the Set-Cookie value —
// so it travels out-of-band, and the bridge (transport layer) is the only
// thing that reads this header and turns it into a __Host- cookie.
const SessionTokenHeader = "x-aff-session-token"

// elevatedSessionTTL is the "short-lived" window PLAN.md §12.2 commits to for
// a recovery-code session: 10 minutes to change the password or re-enroll
// TOTP before it stops working entirely.
const elevatedSessionTTL = 10 * time.Minute

// recoveryCodeCount is how many single-use codes are (re)generated at once.
// PLAN.md §12.2 doesn't pin a number; 10 matches common TOTP-adjacent
// recovery-code schemes (enough to survive years of occasional use, few
// enough to fit on one printed sheet).
const recoveryCodeCount = 10

// dummyPasswordForTiming is hashed once at server construction and verified
// against on every login attempt against a nonexistent admin row. It is not
// a credential — nothing is ever compared to it meaningfully, it exists
// purely to make auth.Verify spend the same real argon2id wall-clock time
// whether or not `aff admin init` has ever run (PLAN.md §4: "always run the
// KDF ... so timing does not leak existence").
const dummyPasswordForTiming = "aff-timing-safe-dummy-passphrase-not-a-real-credential"

// errAuthFailed is the ONE error every credential-verification failure in
// this file returns — wrong password, wrong TOTP code, replayed TOTP step,
// backoff active, unknown/missing admin, bad recovery code, already-used
// recovery code. PLAN.md §4: "one generic failure message... a different
// message per cause is an enumeration oracle." Using a single sentinel
// (rather than independently written status.Error calls with the same
// string) makes that invariant something the compiler helps hold, not
// something every call site has to remember to copy correctly.
var errAuthFailed = status.Error(codes.Unauthenticated, "authentication failed")

// AuthServer implements affv1.AuthServiceServer and also owns the state the
// session interceptor (interceptor.go) needs — see that file's header for
// why the two live on one struct.
type AuthServer struct {
	affv1.UnimplementedAuthServiceServer

	store     *store.Store
	secretKey []byte // AFF_SECRET_KEY, for TOTP secret encryption (PLAN.md §4)
	now       func() time.Time

	backoff   *backoffTracker
	elevated  *elevatedTracker
	dummyHash string
}

// NewAuthServer wires an AuthServer against st, using secretKey to
// encrypt/decrypt the TOTP secret at rest (PLAN.md §4). secretKey is
// AFF_SECRET_KEY from the environment; callers must not derive it from
// anything stored in the database.
func NewAuthServer(st *store.Store, secretKey []byte) (*AuthServer, error) {
	dummyHash, err := auth.Hash(dummyPasswordForTiming, auth.DefaultParams())
	if err != nil {
		return nil, fmt.Errorf("rpc: preparing timing-safe dummy hash: %w", err)
	}
	return &AuthServer{
		store:     st,
		secretKey: secretKey,
		now:       time.Now,
		backoff:   newBackoffTracker(),
		elevated:  newElevatedTracker(),
		dummyHash: dummyHash,
	}, nil
}

// --- Login -------------------------------------------------------------

// Login verifies password then TOTP, in that order, and mints a full
// session on success. See errAuthFailed for why every failure branch below
// returns the exact same error.
func (s *AuthServer) Login(ctx context.Context, req *affv1.AuthServiceLoginRequest) (*affv1.AuthServiceLoginResponse, error) {
	ip := clientIP(ctx)
	now := s.now()

	if s.backoff.blocked(ip, now) {
		_ = s.store.RecordAuthEvent(ctx, "login", ip, false, "backoff active")
		return nil, errAuthFailed
	}

	admin, adminErr := s.store.GetAdmin(ctx)

	// Always run the KDF, admin row or not (PLAN.md §4): verify against the
	// real hash when there is one, otherwise against dummyHash, so the two
	// cases cost the same wall-clock time and a timing side channel cannot
	// distinguish "no admin yet" from "wrong password".
	hash := s.dummyHash
	if adminErr == nil {
		hash = admin.PasswordHash
	}
	pwOK, needsRehash, verr := auth.Verify(req.Password, hash)

	if adminErr != nil || verr != nil || !pwOK {
		s.backoff.recordFailure(ip, now)
		_ = s.store.RecordAuthEvent(ctx, "login", ip, false, "credential check failed")
		return nil, errAuthFailed
	}

	// TOTP AFTER password, per §4.
	totpOK, err := s.verifyTOTPCode(ctx, req.TotpCode, now)
	if err != nil {
		return nil, status.Error(codes.Internal, "login failed")
	}
	if !totpOK {
		s.backoff.recordFailure(ip, now)
		_ = s.store.RecordAuthEvent(ctx, "login", ip, false, "totp check failed")
		return nil, errAuthFailed
	}

	if needsRehash {
		if err := s.rehashAdminPassword(ctx, req.Password); err != nil {
			// Not fatal to the login itself — the old hash still verified —
			// but worth surfacing rather than silently never migrating.
			_ = s.store.RecordAuthEvent(ctx, "login", ip, true, fmt.Sprintf("rehash failed: %v", err))
		}
	}

	rawToken, id, sess, err := s.mintSession(ctx, ip, userAgentFromContext(ctx), now, now.Add(auth.SessionAbsoluteLifetime))
	if err != nil {
		return nil, status.Error(codes.Internal, "login failed")
	}

	s.backoff.recordSuccess(ip)
	_ = s.store.RecordAuthEvent(ctx, "login", ip, true, "")
	setSessionTokenHeader(ctx, rawToken)

	return &affv1.AuthServiceLoginResponse{Session: toProtoSession(id, sess, true)}, nil
}

// --- RecoverWithCode -----------------------------------------------------

// RecoverWithCode consumes a single-use recovery code, revokes every
// existing session (PLAN.md §12.2: "forces a full re-login and revokes every
// other session" — there is no session to preserve here, since the caller
// isn't authenticated yet, so "every other" means "every"), and opens a new
// ELEVATED session scoped to ChangePassword/ReenrollTOTP by the interceptor.
func (s *AuthServer) RecoverWithCode(ctx context.Context, req *affv1.AuthServiceRecoverWithCodeRequest) (*affv1.AuthServiceRecoverWithCodeResponse, error) {
	ip := clientIP(ctx)
	now := s.now()

	if s.backoff.blocked(ip, now) {
		_ = s.store.RecordAuthEvent(ctx, "recover", ip, false, "backoff active")
		return nil, errAuthFailed
	}

	hashes, err := s.allRecoveryCodeHashes(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "recovery failed")
	}

	idx, ok := auth.VerifyCode(req.RecoveryCode, hashes)
	if !ok {
		s.backoff.recordFailure(ip, now)
		_ = s.store.RecordAuthEvent(ctx, "recover", ip, false, "code not recognized")
		return nil, errAuthFailed
	}

	if err := s.store.UseRecoveryCode(ctx, idx); err != nil {
		if errors.Is(err, store.ErrRecoveryCodeUsed) || errors.Is(err, store.ErrNotFound) {
			s.backoff.recordFailure(ip, now)
			_ = s.store.RecordAuthEvent(ctx, "recover", ip, false, "code already used")
			return nil, errAuthFailed
		}
		return nil, status.Error(codes.Internal, "recovery failed")
	}

	if err := s.store.RevokeAllSessions(ctx); err != nil {
		return nil, status.Error(codes.Internal, "recovery failed")
	}

	rawToken, id, sess, err := s.mintSession(ctx, ip, userAgentFromContext(ctx), now, now.Add(elevatedSessionTTL))
	if err != nil {
		return nil, status.Error(codes.Internal, "recovery failed")
	}
	s.elevated.mark(auth.HashToken(rawToken), now.Add(elevatedSessionTTL))

	remaining, err := s.store.CountUnusedRecoveryCodes(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "recovery failed")
	}

	s.backoff.recordSuccess(ip)
	_ = s.store.RecordAuthEvent(ctx, "recover", ip, true, "")
	setSessionTokenHeader(ctx, rawToken)

	return &affv1.AuthServiceRecoverWithCodeResponse{
		Session:                toProtoSession(id, sess, true),
		RemainingRecoveryCodes: int32(remaining),
	}, nil
}

// --- Logout / Session ----------------------------------------------------

// Logout revokes only the calling session (PLAN.md §11).
func (s *AuthServer) Logout(ctx context.Context, _ *affv1.AuthServiceLogoutRequest) (*affv1.AuthServiceLogoutResponse, error) {
	cs, ok := callerSessionFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no session")
	}
	if err := s.store.RevokeSession(ctx, cs.ID); err != nil {
		return nil, status.Error(codes.Internal, "logout failed")
	}
	s.elevated.clear(cs.TokenHash)
	return &affv1.AuthServiceLogoutResponse{}, nil
}

// Session is the "whoami" call the shell uses on boot (PLAN.md §12).
func (s *AuthServer) Session(ctx context.Context, _ *affv1.AuthServiceSessionRequest) (*affv1.AuthServiceSessionResponse, error) {
	cs, ok := callerSessionFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no session")
	}
	sess, err := s.store.GetSessionByTokenHash(ctx, cs.TokenHash)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid session")
	}
	return &affv1.AuthServiceSessionResponse{Session: toProtoSession(cs.ID, sess, true)}, nil
}

// --- ChangePassword --------------------------------------------------------

// ChangePassword requires the current password and a fresh TOTP code before
// accepting a new one — UNLESS the caller holds an ELEVATED recovery session,
// where identity was already re-proven by the single-use recovery code
// itself (the same exception PLAN.md §12.2/the proto doc documents for
// ReenrollTOTPRequest.CurrentPassword; it applies here for the identical
// reason — a recovery flow that still demanded the forgotten password would
// not be a recovery flow). Either way it revokes every other session on
// success (PLAN.md hard rule), and if the caller was elevated it also ends
// that session, which is what forces the "full re-login" §12.2 describes.
func (s *AuthServer) ChangePassword(ctx context.Context, req *affv1.AuthServiceChangePasswordRequest) (*affv1.AuthServiceChangePasswordResponse, error) {
	cs, ok := callerSessionFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no session")
	}
	ip := clientIP(ctx)
	now := s.now()

	if !cs.Elevated {
		if err := s.verifyCurrentCredentials(ctx, req.CurrentPassword, req.TotpCode, now); err != nil {
			if errors.Is(err, errCredentialCheckFailed) {
				_ = s.store.RecordAuthEvent(ctx, "change_password", ip, false, "current credential check failed")
				return nil, errAuthFailed
			}
			return nil, status.Error(codes.Internal, "change password failed")
		}
	}

	if err := auth.IsWeak(req.NewPassword); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	newHash, err := auth.Hash(req.NewPassword, auth.DefaultParams())
	if err != nil {
		return nil, status.Error(codes.Internal, "change password failed")
	}
	if err := s.store.UpdatePassword(ctx, newHash, kdfParamsString(auth.DefaultParams())); err != nil {
		return nil, status.Error(codes.Internal, "change password failed")
	}

	if _, err := s.revokeOtherSessions(ctx, cs.TokenHash); err != nil {
		return nil, status.Error(codes.Internal, "change password failed")
	}
	if cs.Elevated {
		s.endElevatedSession(ctx, cs)
	}

	_ = s.store.RecordAuthEvent(ctx, "change_password", ip, true, "")
	return &affv1.AuthServiceChangePasswordResponse{}, nil
}

// --- ListSessions / RevokeSession / RevokeAllSessions ---------------------

// ListSessions powers §12.5's active-sessions table.
func (s *AuthServer) ListSessions(ctx context.Context, _ *affv1.AuthServiceListSessionsRequest) (*affv1.AuthServiceListSessionsResponse, error) {
	cs, ok := callerSessionFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no session")
	}

	rows, err := s.store.Writer().QueryContext(ctx,
		`SELECT id, token_hash, created_at, last_seen_at, expires_at, ip, user_agent, revoked_at
		 FROM sessions ORDER BY created_at DESC`)
	if err != nil {
		return nil, status.Error(codes.Internal, "listing sessions failed")
	}
	defer func() { _ = rows.Close() }()

	var out []*affv1.Session
	for rows.Next() {
		var (
			id                               int64
			tokenHash                        string
			sess                             auth.Session
			createdAt, lastSeenAt, expiresAt string
			ip, userAgent, revokedAt         sql.NullString
		)
		if err := rows.Scan(&id, &tokenHash, &createdAt, &lastSeenAt, &expiresAt, &ip, &userAgent, &revokedAt); err != nil {
			return nil, status.Error(codes.Internal, "listing sessions failed")
		}
		sess.IP, sess.UserAgent = ip.String, userAgent.String
		if sess.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
			return nil, status.Error(codes.Internal, "listing sessions failed")
		}
		if sess.LastSeenAt, err = time.Parse(time.RFC3339Nano, lastSeenAt); err != nil {
			return nil, status.Error(codes.Internal, "listing sessions failed")
		}
		if sess.ExpiresAt, err = time.Parse(time.RFC3339Nano, expiresAt); err != nil {
			return nil, status.Error(codes.Internal, "listing sessions failed")
		}
		if revokedAt.Valid {
			if sess.RevokedAt, err = time.Parse(time.RFC3339Nano, revokedAt.String); err != nil {
				return nil, status.Error(codes.Internal, "listing sessions failed")
			}
		}
		out = append(out, toProtoSession(id, sess, tokenHash == cs.TokenHash))
	}
	if err := rows.Err(); err != nil {
		return nil, status.Error(codes.Internal, "listing sessions failed")
	}

	// Pagination is not yet load-bearing for a single-admin session list that
	// realistically holds a handful of rows; every row is returned on one
	// page and next_page_token stays empty. Revisit if that stops being true.
	return &affv1.AuthServiceListSessionsResponse{Sessions: out}, nil
}

// RevokeSession kills one session by the id ListSessions handed out.
func (s *AuthServer) RevokeSession(ctx context.Context, req *affv1.AuthServiceRevokeSessionRequest) (*affv1.AuthServiceRevokeSessionResponse, error) {
	if _, ok := callerSessionFromContext(ctx); !ok {
		return nil, status.Error(codes.Unauthenticated, "no session")
	}
	id, err := strconv.ParseInt(req.SessionId, 10, 64)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid session_id")
	}
	if err := s.store.RevokeSession(ctx, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "session not found")
		}
		return nil, status.Error(codes.Internal, "revoking session failed")
	}
	return &affv1.AuthServiceRevokeSessionResponse{}, nil
}

// RevokeAllSessions is the global "sign out everywhere" (PLAN.md §12.5),
// including the caller's own session.
func (s *AuthServer) RevokeAllSessions(ctx context.Context, _ *affv1.AuthServiceRevokeAllSessionsRequest) (*affv1.AuthServiceRevokeAllSessionsResponse, error) {
	if _, ok := callerSessionFromContext(ctx); !ok {
		return nil, status.Error(codes.Unauthenticated, "no session")
	}
	var n int
	// Counted before revoking (rather than making store.RevokeAllSessions
	// return a count) to avoid changing internal/store, which is out of
	// scope for this change; a race against a concurrent login on a
	// single-admin system is an acceptable imprecision in a UI counter.
	if err := s.store.Writer().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sessions WHERE revoked_at IS NULL`).Scan(&n); err != nil {
		return nil, status.Error(codes.Internal, "revoking sessions failed")
	}
	if err := s.store.RevokeAllSessions(ctx); err != nil {
		return nil, status.Error(codes.Internal, "revoking sessions failed")
	}
	return &affv1.AuthServiceRevokeAllSessionsResponse{RevokedCount: int32(n)}, nil
}

// --- ReenrollTOTP / RegenerateRecoveryCodes --------------------------------

// ReenrollTOTP replaces the TOTP secret. Reachable with a live session (after
// proving CurrentPassword) or from an ELEVATED recovery session (no password
// needed — see the proto doc on ReenrollTOTPRequest.CurrentPassword).
func (s *AuthServer) ReenrollTOTP(ctx context.Context, req *affv1.AuthServiceReenrollTOTPRequest) (*affv1.AuthServiceReenrollTOTPResponse, error) {
	cs, ok := callerSessionFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no session")
	}
	ip := clientIP(ctx)

	if !cs.Elevated {
		admin, err := s.store.GetAdmin(ctx)
		if err != nil {
			return nil, status.Error(codes.Internal, "reenroll failed")
		}
		pwOK, _, verr := auth.Verify(req.CurrentPassword, admin.PasswordHash)
		if verr != nil || !pwOK {
			_ = s.store.RecordAuthEvent(ctx, "reenroll_totp", ip, false, "current password check failed")
			return nil, errAuthFailed
		}
	}

	secret, uri, err := auth.Enroll("admin", "AnimeFeedFlux")
	if err != nil {
		return nil, status.Error(codes.Internal, "reenroll failed")
	}
	enc, err := auth.EncryptSecret(secret, s.secretKey)
	if err != nil {
		return nil, status.Error(codes.Internal, "reenroll failed")
	}
	if err := s.store.SetTOTPSecret(ctx, enc); err != nil {
		return nil, status.Error(codes.Internal, "reenroll failed")
	}

	if cs.Elevated {
		if _, err := s.revokeOtherSessions(ctx, cs.TokenHash); err != nil {
			return nil, status.Error(codes.Internal, "reenroll failed")
		}
		s.endElevatedSession(ctx, cs)
	}

	_ = s.store.RecordAuthEvent(ctx, "reenroll_totp", ip, true, "")
	return &affv1.AuthServiceReenrollTOTPResponse{ProvisioningUri: uri}, nil
}

// RegenerateRecoveryCodes invalidates the old code set and returns a new one.
// Never reachable from an elevated session — it isn't in elevatedAllowedMethods,
// so the interceptor refuses it before this method body ever runs — so no
// elevated branch is needed here, unlike ChangePassword/ReenrollTOTP.
func (s *AuthServer) RegenerateRecoveryCodes(ctx context.Context, req *affv1.AuthServiceRegenerateRecoveryCodesRequest) (*affv1.AuthServiceRegenerateRecoveryCodesResponse, error) {
	if _, ok := callerSessionFromContext(ctx); !ok {
		return nil, status.Error(codes.Unauthenticated, "no session")
	}
	ip := clientIP(ctx)
	now := s.now()

	if err := s.verifyCurrentCredentials(ctx, req.CurrentPassword, req.TotpCode, now); err != nil {
		if errors.Is(err, errCredentialCheckFailed) {
			_ = s.store.RecordAuthEvent(ctx, "regenerate_recovery_codes", ip, false, "current credential check failed")
			return nil, errAuthFailed
		}
		return nil, status.Error(codes.Internal, "regenerating recovery codes failed")
	}

	plain, hashes, err := auth.GenerateCodes(recoveryCodeCount)
	if err != nil {
		return nil, status.Error(codes.Internal, "regenerating recovery codes failed")
	}
	if err := s.store.StoreRecoveryCodes(ctx, hashes); err != nil {
		return nil, status.Error(codes.Internal, "regenerating recovery codes failed")
	}

	_ = s.store.RecordAuthEvent(ctx, "regenerate_recovery_codes", ip, true, "")
	return &affv1.AuthServiceRegenerateRecoveryCodesResponse{RecoveryCodes: plain}, nil
}

// --- shared helpers ---------------------------------------------------------

// errCredentialCheckFailed distinguishes "the password/TOTP was wrong" (the
// caller should get errAuthFailed) from "something broke while checking"
// (the caller should get codes.Internal) inside verifyCurrentCredentials,
// without verifyCurrentCredentials itself having to know which gRPC status
// its caller wants — each RPC method above owns that translation.
var errCredentialCheckFailed = errors.New("rpc: credential check failed")

// verifyCurrentCredentials re-proves password + TOTP for an already-live
// session (ChangePassword's non-elevated path, RegenerateRecoveryCodes) —
// PLAN.md §4: a stolen, still-live session token must not be enough on its
// own for either of those.
func (s *AuthServer) verifyCurrentCredentials(ctx context.Context, password, totpCode string, now time.Time) error {
	admin, err := s.store.GetAdmin(ctx)
	if err != nil {
		return fmt.Errorf("rpc: loading admin: %w", err)
	}
	pwOK, _, verr := auth.Verify(password, admin.PasswordHash)
	if verr != nil {
		return fmt.Errorf("rpc: verifying password: %w", verr)
	}
	if !pwOK {
		return errCredentialCheckFailed
	}
	totpOK, err := s.verifyTOTPCode(ctx, totpCode, now)
	if err != nil {
		return err
	}
	if !totpOK {
		return errCredentialCheckFailed
	}
	return nil
}

// verifyTOTPCode decrypts the stored secret, validates code against it, and
// records the step used — a returned store.ErrTOTPReplay is folded into
// (false, nil): PLAN.md §4 treats a replayed step as a failed login, not a
// server error, so callers must not distinguish it in what they return.
func (s *AuthServer) verifyTOTPCode(ctx context.Context, code string, now time.Time) (bool, error) {
	secretEnc, err := s.store.GetTOTPSecret(ctx)
	if err != nil || len(secretEnc) == 0 {
		return false, nil
	}
	secret, err := auth.DecryptSecret(secretEnc, s.secretKey)
	if err != nil {
		return false, nil
	}
	step, ok := auth.ValidateCode(secret, code, now)
	if !ok {
		return false, nil
	}
	if err := s.store.MarkTOTPStepUsed(ctx, step, sha256Hex(code)); err != nil {
		if errors.Is(err, store.ErrTOTPReplay) {
			return false, nil
		}
		return false, fmt.Errorf("rpc: marking totp step used: %w", err)
	}
	return true, nil
}

// rehashAdminPassword re-derives and persists the password hash at current
// DefaultParams() cost after a successful login whose stored hash was
// weaker (auth.Verify's needsRehash) — the transparent migration path §4
// requires so a cost increase doesn't need a maintenance job or a forced
// reset.
func (s *AuthServer) rehashAdminPassword(ctx context.Context, password string) error {
	newHash, err := auth.Hash(password, auth.DefaultParams())
	if err != nil {
		return err
	}
	return s.store.UpdatePassword(ctx, newHash, kdfParamsString(auth.DefaultParams()))
}

// mintSession creates a session row and returns the raw token (never stored,
// only ever returned to the caller — see SessionTokenHeader), its row id,
// and the auth.Session that was persisted.
func (s *AuthServer) mintSession(ctx context.Context, ip, userAgent string, now, expiresAt time.Time) (rawToken string, id int64, sess auth.Session, err error) {
	rawToken, hash, err := auth.NewToken()
	if err != nil {
		return "", 0, auth.Session{}, fmt.Errorf("rpc: generating session token: %w", err)
	}
	sess = auth.Session{
		TokenHash:  hash,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  expiresAt,
		IP:         ip,
		UserAgent:  userAgent,
	}
	id, err = s.store.CreateSession(ctx, sess)
	if err != nil {
		return "", 0, auth.Session{}, fmt.Errorf("rpc: creating session: %w", err)
	}
	return rawToken, id, sess, nil
}

// sessionIDByHash resolves a session's database row id from its token hash.
// internal/store exposes sessions only by hash (GetSessionByTokenHash,
// ListSessions) or by id (TouchSession, RevokeSession) but never a direct
// hash->id lookup, so this reaches through the exported Store.Writer()
// handle rather than requiring a change to internal/store, which is out of
// scope here.
func (s *AuthServer) sessionIDByHash(ctx context.Context, hash string) (int64, error) {
	var id int64
	err := s.store.Writer().QueryRowContext(ctx,
		`SELECT id FROM sessions WHERE token_hash = ?`, hash).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, store.ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("rpc: looking up session id: %w", err)
	}
	return id, nil
}

// revokeOtherSessions revokes every still-active session except the one
// whose hash is keep — the "revoke every other session" half of
// ChangePassword's hard rule and ReenrollTOTP's elevated path. Implemented
// by listing ids and calling store.RevokeSession per row (rather than a
// bespoke bulk UPDATE against Store.Writer()) so it goes through the same
// idempotent, ErrNotFound-safe path store.RevokeSession already provides.
func (s *AuthServer) revokeOtherSessions(ctx context.Context, keep string) (int, error) {
	rows, err := s.store.Writer().QueryContext(ctx,
		`SELECT id FROM sessions WHERE token_hash != ? AND revoked_at IS NULL`, keep)
	if err != nil {
		return 0, fmt.Errorf("rpc: listing other sessions: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("rpc: scanning other session id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, fmt.Errorf("rpc: iterating other sessions: %w", err)
	}
	_ = rows.Close()

	for _, id := range ids {
		if err := s.store.RevokeSession(ctx, id); err != nil && !errors.Is(err, store.ErrNotFound) {
			return 0, fmt.Errorf("rpc: revoking session %d: %w", id, err)
		}
	}
	return len(ids), nil
}

// endElevatedSession revokes the elevated session itself and drops it from
// the tracker, once the one privileged action it exists for has been taken.
// Combined with revokeOtherSessions this is what makes PLAN.md §12.2's
// "forces a full re-login" true: after this call, nothing the caller
// currently holds authenticates anything.
func (s *AuthServer) endElevatedSession(ctx context.Context, cs callerSession) {
	s.elevated.clear(cs.TokenHash)
	_ = s.store.RevokeSession(ctx, cs.ID)
}

// allRecoveryCodeHashes returns every stored code_hash — used and unused —
// in the same id-ascending order StoreRecoveryCodes wrote them in. This must
// include already-used codes: auth.VerifyCode's returned index is a
// position within this exact ordering, and store.UseRecoveryCode's index
// parameter is an OFFSET over ALL rows (see its doc comment), not just
// unused ones. Filtering to unused-only here would desync the two and let a
// used code's hash silently match a different, wrong row.
func (s *AuthServer) allRecoveryCodeHashes(ctx context.Context) ([]string, error) {
	rows, err := s.store.Writer().QueryContext(ctx,
		`SELECT code_hash FROM recovery_codes ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("rpc: listing recovery codes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, fmt.Errorf("rpc: scanning recovery code: %w", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rpc: iterating recovery codes: %w", err)
	}
	return out, nil
}

// toProtoSession converts a stored session into the wire type. RevokedAt is
// left unset (proto zero value) while live, matching Session's own "unset
// while live" contract.
func toProtoSession(id int64, sess auth.Session, isCurrent bool) *affv1.Session {
	out := &affv1.Session{
		Id:         strconv.FormatInt(id, 10),
		CreatedAt:  timestamppb.New(sess.CreatedAt),
		LastSeenAt: timestamppb.New(sess.LastSeenAt),
		ExpiresAt:  timestamppb.New(sess.ExpiresAt),
		Ip:         sess.IP,
		UserAgent:  sess.UserAgent,
		IsCurrent:  isCurrent,
	}
	if !sess.RevokedAt.IsZero() {
		out.RevokedAt = timestamppb.New(sess.RevokedAt)
	}
	return out
}

// kdfParamsString is the human-readable record stored beside the hash in
// admin.kdf_params (PLAN.md §4: params travel with the hash so cost can be
// raised later and compared against). auth.Verify/Hash actually re-derive
// params from the PHC-encoded hash string itself, so this column is
// record-keeping for operators and future migration tooling, not something
// this package reads back.
func kdfParamsString(p auth.Params) string {
	return fmt.Sprintf("argon2id m=%d,t=%d,p=%d", p.Memory, p.Time, p.Threads)
}

// sha256Hex is used only to give MarkTOTPStepUsed's code_hash column
// something stable to store; unlike auth.HashToken (base64, session-token
// specific) this has no other consumer, so it stays local rather than
// reusing that helper for an unrelated value.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// clientIP pulls the peer address gRPC attaches to ctx, stripping the port —
// auth_events.ip and the backoff tracker key on the address alone.
func clientIP(ctx context.Context) string {
	p, ok := peer.FromContext(ctx)
	if !ok || p.Addr == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(p.Addr.String())
	if err != nil {
		return p.Addr.String()
	}
	return host
}

// userAgentFromContext reads the standard grpc "user-agent" metadata the
// client library sets automatically, for sessions.user_agent.
func userAgentFromContext(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	if v := md.Get("user-agent"); len(v) > 0 {
		return v[0]
	}
	return ""
}

// setSessionTokenHeader hands the raw token to the transport layer via a
// gRPC response header rather than the response body (see SessionTokenHeader).
// SetHeader only fails if called after the first message is sent or the
// server doesn't support headers, neither of which applies to a synchronous
// unary handler returning its first and only header set — an error here
// would mean something is deeply wrong with the transport, not something a
// caller can act on, so it is intentionally not propagated as an RPC
// failure after the security-critical work above already succeeded.
func setSessionTokenHeader(ctx context.Context, rawToken string) {
	_ = grpc.SetHeader(ctx, metadata.Pairs(SessionTokenHeader, rawToken))
}

// --- backoff tracker ---------------------------------------------------

// backoffTracker implements the per-IP exponential backoff PLAN.md §4
// requires. It collapses "per-IP" and "per-account" into one counter because
// there is exactly one account in this whole system — a second axis would
// only ever agree with the first. State is process-local and in-memory
// (not persisted) deliberately: a restart resetting backoff is the safe
// direction to fail in, unlike a restart that forgot a lockout would be.
type backoffTracker struct {
	mu sync.Mutex
	m  map[string]*backoffState
}

type backoffState struct {
	failures int
	until    time.Time
}

func newBackoffTracker() *backoffTracker {
	return &backoffTracker{m: make(map[string]*backoffState)}
}

// blocked reports whether ip is currently inside its backoff window.
func (t *backoffTracker) blocked(ip string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	st, ok := t.m[ip]
	if !ok {
		return false
	}
	return now.Before(st.until)
}

// recordFailure grows the delay before ip may try again. The first two
// failures cost nothing (a real admin mistyping a password twice should not
// be rate-limited), then the delay doubles each additional failure up to a
// 60s cap.
func (t *backoffTracker) recordFailure(ip string, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	st, ok := t.m[ip]
	if !ok {
		st = &backoffState{}
		t.m[ip] = st
	}
	st.failures++
	st.until = now.Add(backoffDelay(st.failures))
}

// recordSuccess clears ip's failure count entirely.
func (t *backoffTracker) recordSuccess(ip string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.m, ip)
}

func backoffDelay(failures int) time.Duration {
	const grace = 2
	if failures <= grace {
		return 0
	}
	n := failures - grace
	if n > 6 {
		n = 6 // caps the doubling at 64s before the explicit 60s clamp below
	}
	d := time.Second * time.Duration(1<<uint(n))
	if d > 60*time.Second {
		d = 60 * time.Second
	}
	return d
}

// --- elevated-session tracker -------------------------------------------

// elevatedTracker records which live sessions (by token hash) are the
// scope-limited kind RecoverWithCode opens, and until when. There is no
// schema column for this — sessions has no "kind" — and elevated sessions
// are short-lived (10 min) by construction, so process-local state that
// defaults to "not elevated" (i.e. "not trusted with anything extra") on
// restart is the safe failure mode: the caller just runs RecoverWithCode
// again rather than a restart accidentally granting standing elevated
// access it shouldn't have.
type elevatedTracker struct {
	mu sync.Mutex
	m  map[string]time.Time // token hash -> elevated-until
}

func newElevatedTracker() *elevatedTracker {
	return &elevatedTracker{m: make(map[string]time.Time)}
}

func (t *elevatedTracker) mark(tokenHash string, until time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.m[tokenHash] = until
}

func (t *elevatedTracker) isElevated(tokenHash string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	until, ok := t.m[tokenHash]
	if !ok {
		return false
	}
	if !now.Before(until) {
		delete(t.m, tokenHash)
		return false
	}
	return true
}

func (t *elevatedTracker) clear(tokenHash string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.m, tokenHash)
}
