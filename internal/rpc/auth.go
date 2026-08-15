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
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
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
	"github.com/monstercameron/AnimeFeedFlux/internal/bridge"
	"github.com/monstercameron/AnimeFeedFlux/internal/config"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// SessionTokenHeader is the outgoing gRPC header AuthServer.Login and
// RecoverWithCode attach the raw session token to on success, via
// grpc.SetHeader. The raw token is deliberately NEVER placed in a response
// message body — PLAN.md §4 requires it exist only as the Set-Cookie value —
// so it travels out-of-band, and the bridge (transport layer) is the only
// thing that reads this header and turns it into a __Host- cookie.
const SessionTokenHeader = "x-aff-session-token"

// SessionTicketHeader is the outgoing gRPC header Login/RecoverWithCode
// attach a single-use login TICKET to — never the raw session token — for
// any caller that arrived over the bridge (a real browser/WASM connection).
// See internal/bridge/ticket.go's package doc comment for the full design:
// a ticket is safe to hand back over the same socket the request came in on
// (unlike SessionTokenHeader, whose value must never reach a browser) because
// it is single-use, expires in seconds, and on its own authenticates nothing
// — it only lets the NEXT WebSocket upgrade redeem the real session and
// receive the cookie that actually authenticates future calls.
const SessionTicketHeader = "x-aff-login-ticket"

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

	// pepperKey and pepperVersion are the optional second secret PLAN.md §4
	// describes. pepperKey is nil / pepperVersion is 0 for "no pepper
	// configured". Hashing goes through auth.HashPeppered and verification
	// through auth.VerifyPasswordPeppered — see hashPassword/verifyPassword
	// below — which apply the pepper to argon2id's OUTPUT, the order §4 and
	// internal/auth/pepper.go's doc comment specify.
	pepperKey     []byte
	pepperVersion int

	// tickets mints the single-use login tickets a bridge-transport caller
	// gets in place of the raw session token (see SessionTicketHeader and
	// internal/bridge/ticket.go). nil is a valid, if unusual, configuration
	// — see issueSessionCredential's doc comment for what it means when a
	// bridge caller shows up with no ticket store configured.
	tickets *bridge.TicketStore

	// devInsecureAuth relaxes two anti-abuse controls that exist to stop an
	// ATTACKER and that, on a local dev instance, only ever stop the
	// operator: TOTP replay rejection and login backoff. It changes nothing
	// about whether a credential must be CORRECT — the password is still
	// verified against argon2id and the code still has to be a valid TOTP
	// for the enrolled secret.
	//
	// It exists because the dev-build login prefill (web/pages/auth/
	// devfill_on.go) is meant to be a one-click sign-in and was not: a
	// second click inside the same 30-second step replays a consumed step
	// and is refused, and a few refused attempts then trip the backoff, so
	// the more the operator retried the more stuck they got — reported live,
	// repeatedly, on a dev box.
	//
	// Set ONLY from AFF_DEV_INSECURE_AUTH=1, which wire.go refuses to honour
	// unless the listener is loopback-only, and which logs a WARN on every
	// boot that enables it. See NewAuthServer's WithDevInsecureAuth.
	devInsecureAuth bool
}

// AuthServerOption configures optional AuthServer behavior. Added as a
// variadic parameter on NewAuthServer rather than a new required argument
// so every existing call site — including cmd/animefeedflux/wire.go, out of
// scope for this change — keeps compiling unmodified.
type AuthServerOption func(*AuthServer)

// WithPasswordPepper explicitly configures the optional pepper (PLAN.md §4).
// version must be >=1 — SEC-08's whole point is a pepper generation that can
// be recorded and later rotated, so a non-positive version paired with a
// non-empty key is treated as "not configured" (fails closed to "no
// pepper") rather than persisting a version nothing can rotate against.
// This is the path tests use; production (wire.go) instead falls back to
// reading AFF_PASSWORD_PEPPER / AFF_PASSWORD_PEPPER_VERSION directly — see
// NewAuthServer.
func WithPasswordPepper(key []byte, version int) AuthServerOption {
	return func(s *AuthServer) {
		if len(key) == 0 || version <= 0 {
			return
		}
		s.pepperKey = key
		s.pepperVersion = version
	}
}

// WithTicketStore wires the login-ticket store a bridge-transport Login/
// RecoverWithCode mints from (see SessionTicketHeader). Without this option
// (or in a test that never sets it), a bridge-transport call that succeeds
// simply mints no ticket and sets no ticket header — see
// issueSessionCredential's doc comment for why that is a safe degraded
// state (the session itself is fine; only the browser has no way to redeem
// it without hand-supplying a cookie some other way) rather than an error,
// so every existing NewAuthServer(st, secretKey) call site written before
// this option existed keeps compiling and passing unmodified.
func WithTicketStore(tickets *bridge.TicketStore) AuthServerOption {
	return func(s *AuthServer) { s.tickets = tickets }
}

// WithDevInsecureAuth turns off TOTP replay rejection and login backoff.
//
// **This must never be enabled on anything reachable from a network.** Both
// controls it removes are real: replay rejection is what stops a code
// observed over someone's shoulder (or captured once) from being reused
// inside its 30-second window, and backoff is what makes online guessing
// expensive. PLAN.md §4 requires both.
//
// What it does NOT do is accept a wrong credential. The password still goes
// through argon2id and the code still has to validate against the enrolled
// secret; only the "you already used this step" and "you have tried too
// often" refusals are dropped. That keeps the blast radius to "a dev box is
// as strong as password + a currently-valid TOTP, with no rate limit"
// rather than "a dev box has no authentication".
//
// The gate lives at the wiring layer, not here: cmd/animefeedflux refuses to
// pass this unless the admin listener is bound to loopback, so a config file
// copied to a real host cannot quietly turn it on.
func WithDevInsecureAuth() AuthServerOption {
	return func(s *AuthServer) { s.devInsecureAuth = true }
}

// NewAuthServer wires an AuthServer against st, using secretKey to
// encrypt/decrypt the TOTP secret at rest (PLAN.md §4). secretKey is
// AFF_SECRET_KEY from the environment; callers must not derive it from
// anything stored in the database.
func NewAuthServer(st *store.Store, secretKey []byte, opts ...AuthServerOption) (*AuthServer, error) {
	dummyHash, err := auth.Hash(dummyPasswordForTiming, auth.DefaultParams())
	if err != nil {
		return nil, fmt.Errorf("rpc: preparing timing-safe dummy hash: %w", err)
	}
	s := &AuthServer{
		store:     st,
		secretKey: secretKey,
		now:       time.Now,
		backoff:   newBackoffTracker(),
		elevated:  newElevatedTracker(),
		dummyHash: dummyHash,
	}
	for _, opt := range opts {
		opt(s)
	}
	if len(s.pepperKey) == 0 {
		// No explicit WithPasswordPepper option was given. wire.go (out of
		// scope for this change) still calls NewAuthServer with just
		// (st, secretKey) — there is no plumbing yet from
		// config.Config.PasswordPepper through to here — so this falls back
		// to reading the same two environment variables
		// internal/config.Load already validates loudly at boot, directly,
		// the same "read AFF_* straight from the environment" pattern
		// cmd/aff/admin_cmd.go already uses for AFF_SECRET_KEY and for the
		// identical reason: the call site that would normally thread
		// validated config through is out of scope here.
		if key, version, ok := passwordPepperFromEnv(config.OSGetenv); ok {
			s.pepperKey = key
			s.pepperVersion = version
		}
	}
	return s, nil
}

// passwordPepperFromEnv reads AFF_PASSWORD_PEPPER / AFF_PASSWORD_PEPPER_VERSION
// directly and tolerantly: any problem (missing version, non-integer,
// non-positive) is treated as "no pepper configured" here rather than
// failing server construction, because internal/config.Load already
// validates these same two variables loudly at boot — see its
// PasswordPepper / PasswordPepperVersion fields and pepperVersion
// validator. This is only a fallback for the one call site
// (NewAuthServer, when no WithPasswordPepper option is given) that cannot
// be updated to thread that already-validated value through.
func passwordPepperFromEnv(getenv config.Getenv) (key []byte, version int, ok bool) {
	raw := strings.TrimSpace(getenv("AFF_PASSWORD_PEPPER"))
	if raw == "" {
		return nil, 0, false
	}
	v, err := strconv.Atoi(strings.TrimSpace(getenv("AFF_PASSWORD_PEPPER_VERSION")))
	if err != nil || v <= 0 {
		return nil, 0, false
	}
	return []byte(raw), v, true
}

// pepperKeyConfigured reports whether this server currently has a usable
// pepper. Used at HASH time (ChangePassword, rehashAdminPassword,
// CompletePasswordReset) to decide whether a freshly written credential
// gets peppered.
func (s *AuthServer) pepperKeyConfigured() bool {
	return len(s.pepperKey) > 0 && s.pepperVersion > 0
}

// hashPassword hashes password at DefaultParams(), applying the server's configured pepper (if
// any) via auth.HashPeppered — HMAC-SHA256 over the argon2id OUTPUT, per PLAN.md §4 and
// internal/auth/pepper.go's doc comment, not over the password beforehand. pepperVersion reports
// what to persist alongside hash: 0 (never peppered) when this server has no pepper configured, or
// s.pepperVersion when it does. Callers pass pepperVersion straight through to
// store.UpdatePasswordAndPepper / store.CompletePasswordReset so the row's recorded generation
// always agrees with what was actually mixed into the hash.
func (s *AuthServer) hashPassword(password string) (hash string, pepperVersion int, err error) {
	var key []byte
	if s.pepperKeyConfigured() {
		key = s.pepperKey
		pepperVersion = s.pepperVersion
	}
	hash, err = auth.HashPeppered(password, auth.DefaultParams(), key)
	return hash, pepperVersion, err
}

// verifyPassword checks password against hash, given the pepper generation (rowPepperVersion, from
// Admin.PepperVersion) that hash was written under. It always runs auth.VerifyPasswordPeppered —
// spending the real argon2id KDF cost — even when this server cannot reproduce rowPepperVersion, so
// a pepper-generation mismatch costs the same wall-clock time as a wrong password and is not a
// distinguishable timing side channel; ok is then forced false explicitly rather than relying on
// the mismatch alone, since "can't reproduce the pepper" and "wrong password" must both mean the
// same denial here (a row hashed under a pepper does not verify against any unpeppered candidate).
func (s *AuthServer) verifyPassword(password, hash string, rowPepperVersion int) (ok bool, needsRehash bool, err error) {
	var key []byte
	reproducible := true
	switch {
	case rowPepperVersion == 0:
		// never peppered — compare exactly as before pepper existed (PLAN.md §4)
	case len(s.pepperKey) == 0 || rowPepperVersion != s.pepperVersion:
		reproducible = false
	default:
		key = s.pepperKey
	}
	ok, needsRehash, err = auth.VerifyPasswordPeppered(password, hash, key)
	if !reproducible {
		ok = false
	}
	return ok, needsRehash, err
}

// --- Setup ---------------------------------------------------------------

// errSetupUnavailable is the ONE error Setup returns once an admin row
// exists, whatever the actual reason a given call was refused (row already
// there, lost the creation race, store error while checking). One string for
// every cause, same reasoning as errAuthFailed: the only fact Setup's
// availability may reveal is the one the open-first-come design already
// concedes — "no admin exists yet" — and nothing beyond it.
var errSetupUnavailable = status.Error(codes.FailedPrecondition, "setup unavailable")

// Setup is first-run account creation over the wire: the same sequence
// cmd/aff's cmdAdminInit runs locally (hash the password, create the
// singleton admin row, enroll TOTP, generate recovery codes), reachable
// without a session, working exactly once. Cam chose the open first-come
// variant deliberately (DEVLOG 2026-08-15): whoever reaches this first on a
// fresh or freshly-reset instance claims it, and the mitigation is
// operational (claim promptly), not mechanical.
//
// The password is hashed UNPEPPERED (pepper version 0), exactly as `aff
// admin init` writes it — store.InitAdmin records no pepper version — and
// Login's transparent re-pepper migration upgrades the row on the first
// successful sign-in if a pepper is configured.
//
// Ordering: everything fallible-but-pure (policy check, KDF, TOTP
// enrollment, secret encryption, code generation) happens BEFORE the first
// database write, so the only way to end up half-initialized is a database
// error between InitAdmin and the two writes after it. That window also
// exists in cmdAdminInit, and the remedy is the same: `aff admin reset`.
func (s *AuthServer) Setup(ctx context.Context, req *affv1.AuthServiceSetupRequest) (*affv1.AuthServiceSetupResponse, error) {
	ip := clientIP(ctx)

	if _, err := s.store.GetAdmin(ctx); err == nil {
		_ = s.store.RecordAuthEvent(ctx, "setup", ip, false, "admin already exists")
		return nil, errSetupUnavailable
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, errSetupUnavailable
	}

	// Real policy feedback, not a generic refusal: password policy is public
	// (PLAN.md §4 states it verbatim), and the only caller who can reach a
	// SUCCESSFUL Setup is the operator claiming the instance — there is no
	// enrolled credential yet for this message to leak anything about.
	if err := auth.IsWeak(req.Password); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	hash, err := auth.Hash(req.Password, auth.DefaultParams())
	if err != nil {
		return nil, status.Error(codes.Internal, "setup failed")
	}
	kdfParams, err := json.Marshal(auth.DefaultParams())
	if err != nil {
		return nil, status.Error(codes.Internal, "setup failed")
	}
	secret, provisioningURI, err := auth.Enroll("admin", "AnimeFeedFlux")
	if err != nil {
		return nil, status.Error(codes.Internal, "setup failed")
	}
	encSecret, err := auth.EncryptSecret(secret, s.secretKey)
	if err != nil {
		return nil, status.Error(codes.Internal, "setup failed")
	}
	plainCodes, hashedCodes, err := auth.GenerateCodes(recoveryCodeCount)
	if err != nil {
		return nil, status.Error(codes.Internal, "setup failed")
	}

	// InitAdmin is the atomic claim: two racing Setup calls both reach here,
	// exactly one insert succeeds (store.ErrAdminExists for the loser, same
	// refusal-not-overwrite contract cmdAdminInit relies on).
	if err := s.store.InitAdmin(ctx, hash, string(kdfParams)); err != nil {
		if errors.Is(err, store.ErrAdminExists) {
			_ = s.store.RecordAuthEvent(ctx, "setup", ip, false, "admin already exists")
			return nil, errSetupUnavailable
		}
		return nil, status.Error(codes.Internal, "setup failed")
	}
	if err := s.store.SetTOTPSecret(ctx, encSecret); err != nil {
		return nil, status.Error(codes.Internal, "setup failed")
	}
	if err := s.store.StoreRecoveryCodes(ctx, hashedCodes); err != nil {
		return nil, status.Error(codes.Internal, "setup failed")
	}

	_ = s.store.RecordAuthEvent(ctx, "setup", ip, true, "")
	return &affv1.AuthServiceSetupResponse{
		ProvisioningUri: provisioningURI,
		RecoveryCodes:   plainCodes,
	}, nil
}

// --- Login -------------------------------------------------------------

// Login verifies password then TOTP, in that order, and mints a full
// session on success. See errAuthFailed for why every failure branch below
// returns the exact same error.
func (s *AuthServer) Login(ctx context.Context, req *affv1.AuthServiceLoginRequest) (*affv1.AuthServiceLoginResponse, error) {
	ip := clientIP(ctx)
	now := s.now()

	if !s.devInsecureAuth && s.backoff.blocked(ip, now) {
		_ = s.store.RecordAuthEvent(ctx, "login", ip, false, "backoff active")
		return nil, errAuthFailed
	}

	admin, adminErr := s.store.GetAdmin(ctx)

	// Always run the KDF, admin row or not (PLAN.md §4): verify against the
	// real hash when there is one, otherwise against dummyHash, so the two
	// cases cost the same wall-clock time and a timing side channel cannot
	// distinguish "no admin yet" from "wrong password".
	hash := s.dummyHash
	rowPepperVersion := 0
	if adminErr == nil {
		hash = admin.PasswordHash
		rowPepperVersion = admin.PepperVersion
	}

	// verifyPassword applies the pepper matching rowPepperVersion (never the
	// server's current pepper alone) — a row hashed under no pepper, or an
	// older/different one, must not be compared against a peppered candidate.
	// It still spends the real KDF cost even when that pepper can't be
	// reproduced, so a pepper-generation mismatch is not a timing oracle.
	pwOK, needsRehash, verr := s.verifyPassword(req.Password, hash, rowPepperVersion)

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

	// needsRepepper mirrors needsRehash's transparent-migration path (§4):
	// this row was hashed with no pepper (or a stale generation) but the
	// server now has a current one configured, so upgrade it on this
	// successful login rather than requiring a forced password change.
	// pepperOK is already true here (an unreachable pepper generation would
	// have failed pwOK above), so the only way rowPepperVersion can differ
	// from s.pepperVersion at this point is rowPepperVersion == 0.
	needsRepepper := s.pepperKeyConfigured() && rowPepperVersion != s.pepperVersion
	if needsRehash || needsRepepper {
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
	s.issueSessionCredential(ctx, now, rawToken)

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

	if !s.devInsecureAuth && s.backoff.blocked(ip, now) {
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
	// Persisted at creation, not just tracked in memory.
	//
	// The in-memory mark above is lost on a restart, and until this write
	// existed the row carried its creation default of `full` — so a recovery
	// session that had not yet made a call came back after a restart with the
	// scope of an ordinary login, reachable across every RPC in the system
	// (A8-31). The row is what interceptor.go's authorize now treats as
	// authoritative, so it has to say `elevated` from the moment the session
	// exists rather than from its first authorized call.
	if err := s.store.SetSessionScope(ctx, id, store.SessionScopeElevated); err != nil {
		return nil, status.Error(codes.Internal, "recovery failed")
	}

	remaining, err := s.store.CountUnusedRecoveryCodes(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "recovery failed")
	}

	s.backoff.recordSuccess(ip)
	_ = s.store.RecordAuthEvent(ctx, "recover", ip, true, "")
	s.issueSessionCredential(ctx, now, rawToken)

	return &affv1.AuthServiceRecoverWithCodeResponse{
		Session:                toProtoSession(id, sess, true),
		RemainingRecoveryCodes: int32(remaining),
	}, nil
}

// --- Password reset tokens -------------------------------------------------
//
// internal/auth/reset.go's NewResetToken/VerifyResetToken are complete and
// tested (SEC-31). IssuePasswordResetToken and CompletePasswordReset below
// are the orchestration and single-use enforcement (SEC-33/34) on top of
// them — store.CompletePasswordReset does the actual atomic
// mark-used+rewrite-password+revoke-all-sessions in one transaction.
//
// Reachability decision (closing SEC-31/33's "zero callers" gap): NEITHER
// method is a proto RPC, and none should be added. AuthService's whole
// design (see the service doc comment above) is "every RPC is reached only
// through a session minted by Login/RecoverWithCode, checked by an
// interceptor on every call" — but a caller invoking IssuePasswordResetToken
// is, by definition, one who cannot get a session (that is the entire point
// of a password reset). There is no way to require authentication on that
// call without defeating its purpose, and with a single admin account and no
// email infrastructure (PLAN.md §12.2), an unauthenticated network RPC that
// mints a reset token would just be an unauthenticated "take over the only
// account" RPC. So issuing cannot be exposed over the bridge at all.
// Completing inherits the same answer rather than a weaker one: this system
// has no channel to deliver a freshly minted raw token to a browser that
// doesn't already pass through the same machine issuing it, so splitting
// "issue" (local) from "complete" (networked) would add a whole gRPC
// service method for zero actual capability while growing the unauthenticated
// attack surface for no reason.
//
// The only caller is `aff admin reset-password` (cmd/aff/admin_cmd.go),
// which — like `aff admin init`/`aff admin reset` — requires direct
// filesystem access to the SQLite file as its authorization; it calls
// IssuePasswordResetToken and CompletePasswordReset as ordinary Go methods
// in the same process, never over a network, and mints+consumes the token in
// one invocation so the raw value never needs to be displayed, stored, or
// carried anywhere. Unlike `aff admin reset`, it leaves TOTP enrollment and
// recovery codes untouched — the scenario it exists for is "forgot the
// password but the other factors are still fine", where blowing away a
// working TOTP enrollment and burning a full set of recovery codes (which
// `aff admin reset` does, by design) is real, avoidable cost. Sessions are
// still fully revoked (SEC-33), because that property is unconditional.
// TestPasswordResetNotOnGRPCSurface (auth_test.go) makes this a checked
// invariant, not just a comment: it fails if a future proto change ever adds
// either method to AuthService.
//
// PLAN.md §11's "aff admin init and aff admin reset are the only local-only
// commands" and §12.2's silence on a third recovery path are now stale
// against this and should be updated to mention `aff admin reset-password`
// alongside them — noted here rather than edited in PLAN.md itself, which is
// out of scope for this change.

// IssuePasswordResetToken mints a fresh single-use reset token and persists
// only its SHA-256 hash (internal/auth/reset.go: "raw is returned to the
// caller exactly once"). The raw value is returned here and nowhere else —
// it is never logged, never placed in the auth_events detail column, and
// callers must not do so either (SEC-50-style requirement, same as a
// session token).
func (s *AuthServer) IssuePasswordResetToken(ctx context.Context) (rawToken string, err error) {
	raw, hash, expiresAt, err := auth.NewResetToken()
	if err != nil {
		return "", fmt.Errorf("rpc: issuing password reset token: %w", err)
	}
	if err := s.store.CreatePasswordResetToken(ctx, hash, expiresAt); err != nil {
		return "", fmt.Errorf("rpc: persisting password reset token: %w", err)
	}
	_ = s.store.RecordAuthEvent(ctx, "password_reset_issued", "", true, "")
	return raw, nil
}

// CompletePasswordReset consumes rawToken: found, unused and unexpired, or
// this fails with errAuthFailed — the SAME generic error whether the token
// never existed, was already used, or has expired (PLAN.md §4/§12.1: "an
// expired token is refused with the same generic message as an invalid
// one... distinguishing them tells an attacker a token existed").
//
// A live token is looked up by scanning every still-active candidate and
// running auth.VerifyResetToken(rawToken, candidate) against each — there is
// no exported way to hash a raw token from outside internal/auth, by design
// (see reset.go), so this mirrors how RecoverWithCode above resolves an
// incoming recovery code against allRecoveryCodeHashes. The actual single-
// use decision is made by store.CompletePasswordReset's atomic UPDATE, not
// by this scan: two callers racing the same token both find the same match
// here, but only one of the resulting store.CompletePasswordReset calls
// succeeds (see that function's doc comment), and the loser gets
// store.ErrResetTokenInvalid — mapped to the identical errAuthFailed.
//
// On success every existing session is revoked, including ones opened
// before the reset (PLAN.md §4: "a reset that leaves old sessions alive has
// not actually locked anyone out") — store.CompletePasswordReset does this
// in the same transaction as the password write, so a crash between the two
// cannot leave sessions alive under a new password subscribers don't know
// yet, or vice versa.
func (s *AuthServer) CompletePasswordReset(ctx context.Context, rawToken, newPassword string) error {
	if rawToken == "" {
		return errAuthFailed
	}
	now := s.now()

	hashes, err := s.store.ActiveResetTokenHashes(ctx, now)
	if err != nil {
		return status.Error(codes.Internal, "password reset failed")
	}
	matched := ""
	for _, h := range hashes {
		if auth.VerifyResetToken(rawToken, h) {
			matched = h
			break
		}
	}
	if matched == "" {
		_ = s.store.RecordAuthEvent(ctx, "password_reset", "", false, "token not recognized")
		return errAuthFailed
	}

	if err := auth.IsWeak(newPassword); err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}

	newHash, pepperVersion, err := s.hashPassword(newPassword)
	if err != nil {
		return status.Error(codes.Internal, "password reset failed")
	}

	if err := s.store.CompletePasswordReset(ctx, matched, now, newHash, kdfParamsString(auth.DefaultParams()), pepperVersion); err != nil {
		if errors.Is(err, store.ErrResetTokenInvalid) {
			_ = s.store.RecordAuthEvent(ctx, "password_reset", "", false, "token already used or expired")
			return errAuthFailed
		}
		return status.Error(codes.Internal, "password reset failed")
	}

	_ = s.store.RecordAuthEvent(ctx, "password_reset", "", true, "")
	return nil
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
	// The recovery-code count rides along on the session lookup rather than
	// getting its own RPC: it is account state the admin app wants at the
	// same moments it wants "am I signed in", and a second round trip for
	// one integer would be a worse trade. A failure to count is NOT a
	// failure to report the session — being unable to say how many codes
	// remain must not log anybody out — so the error is logged and the
	// count reported as zero-unknown, with the session still returned.
	remaining, err := s.store.CountUnusedRecoveryCodes(ctx)
	if err != nil {
		remaining = 0
	}
	return &affv1.AuthServiceSessionResponse{
		Session:                toProtoSession(cs.ID, sess, true),
		RemainingRecoveryCodes: int32(remaining),
	}, nil
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

	newHash, newPepperVersion, err := s.hashPassword(req.NewPassword)
	if err != nil {
		return nil, status.Error(codes.Internal, "change password failed")
	}
	if err := s.store.UpdatePasswordAndPepper(ctx, newHash, kdfParamsString(auth.DefaultParams()), newPepperVersion); err != nil {
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
		pwOK, _, verr := s.verifyPassword(req.CurrentPassword, admin.PasswordHash, admin.PepperVersion)
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
// VerifyCurrentCredentials is verifyCurrentCredentials, exported so
// SystemServer can put Backup behind the same gate as ChangePassword and
// RegenerateRecoveryCodes (A8-40).
//
// An interface satisfied by this method — rather than SystemServer importing
// AuthServer — is what keeps the two services from depending on each other;
// the composition root supplies it.
func (s *AuthServer) VerifyCurrentCredentials(ctx context.Context, password, totpCode string, now time.Time) error {
	return s.verifyCurrentCredentials(ctx, password, totpCode, now)
}

func (s *AuthServer) verifyCurrentCredentials(ctx context.Context, password, totpCode string, now time.Time) error {
	admin, err := s.store.GetAdmin(ctx)
	if err != nil {
		return fmt.Errorf("rpc: loading admin: %w", err)
	}
	pwOK, _, verr := s.verifyPassword(password, admin.PasswordHash, admin.PepperVersion)
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
			// The code was VALID; it had simply been used already. On a dev
			// instance that is the operator clicking the prefilled one-click
			// sign-in twice, not an attacker replaying a captured code.
			if s.devInsecureAuth {
				return true, nil
			}
			return false, nil
		}
		return false, fmt.Errorf("rpc: marking totp step used: %w", err)
	}
	return true, nil
}

// rehashAdminPassword re-derives and persists the password hash at current
// DefaultParams() cost after a successful login whose stored hash was
// weaker (auth.Verify's needsRehash), OR whose pepper generation is behind
// the server's current one (needsRepepper in Login) — both are the same
// kind of transparent migration §4 requires so a cost increase, or newly
// configuring a pepper, doesn't need a maintenance job or a forced reset.
// It always writes the row's pepper_version consistent with what it just
// wrote into password_hash (0 if this server currently has no pepper
// configured), never leaving the two disagreeing.
func (s *AuthServer) rehashAdminPassword(ctx context.Context, password string) error {
	newHash, pepperVersion, err := s.hashPassword(password)
	if err != nil {
		return err
	}
	return s.store.UpdatePasswordAndPepper(ctx, newHash, kdfParamsString(auth.DefaultParams()), pepperVersion)
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

// setLoginTicketHeader hands a single-use login ticket to the transport
// layer via a gRPC response header, the ticket analog of
// setSessionTokenHeader — see SessionTicketHeader's doc comment for why a
// TICKET, unlike the raw session token, is safe to send this way even to a
// caller whose transport is the bridge.
func setLoginTicketHeader(ctx context.Context, ticket string) {
	_ = grpc.SetHeader(ctx, metadata.Pairs(SessionTicketHeader, ticket))
}

// issueSessionCredential is Login/RecoverWithCode's shared last step after a
// session has been minted: decide whether the CALLER (not the session) is a
// browser on the other end of the bridge, and hand back the credential that
// caller can safely receive.
//
//   - bridge.SessionFromContext(ctx) succeeding is what identifies a
//     bridge-transport call — internal/bridge's ServeHTTP (server.go)
//     attaches a Session to EVERY request context it hands to the WebSocket
//     handler, authenticated or anonymous alike (see that file's own doc
//     comment), so its mere presence — not its Token field, which is empty
//     for the anonymous case Login/RecoverWithCode are reached through —
//     is what this checks. A caller on the plain grpc.Server at AdminAddr
//     (cmd/aff, this project's own e2e suite) never has one attached at
//     all, which is exactly the "not a browser" signal this needs: that
//     transport is a trusted local process reading its own response, not
//     JavaScript/WASM in a browser, so PLAN.md §4's "the token never
//     touches JavaScript or WASM" simply does not apply to it, and it keeps
//     getting the raw token exactly as it always has (SessionTokenHeader) —
//     changing that would break cmd/aff's `aff login` and this project's
//     entire e2e suite, which both depend on reading it back directly.
//   - A bridge-transport call instead gets ONLY a ticket
//     (SessionTicketHeader) — see that constant's doc comment for why a
//     ticket, unlike the raw token, is safe to place in gRPC response
//     metadata a WASM client will read.
//   - s.tickets == nil (WithTicketStore never configured — every test
//     written before this option existed) degrades to "mint nothing for a
//     bridge caller": the session itself is still fully valid and usable by
//     any client that already has its raw token some other way, it is only
//     unreachable via ticket-driven reconnect. This mirrors how a missing
//     Config.Tickets on the bridge side simply ignores an incoming ticket
//     query parameter (server.go) rather than erroring — "ticket
//     infrastructure not wired" degrades to "anonymous stays anonymous /
//     bridge caller gets no ticket," never to "leak the raw token instead."
func (s *AuthServer) issueSessionCredential(ctx context.Context, now time.Time, rawToken string) {
	if _, onBridge := bridge.SessionFromContext(ctx); !onBridge {
		setSessionTokenHeader(ctx, rawToken)
		return
	}
	if s.tickets == nil {
		return
	}
	ticket, _, err := s.tickets.Issue(now, rawToken)
	if err != nil {
		// Ticket minting is not security-critical to fail loudly on here —
		// the session itself is already committed and valid; a caller that
		// gets no ticket simply cannot reconnect the anonymous socket into
		// an authenticated one and has to retry Login. Recorded nowhere
		// beyond this comment because auth_events already has a true "login
		// succeeded" row for this attempt (RecordAuthEvent, just above every
		// call site) and a second, ticket-specific failure event would be
		// more oracle than signal on a single-admin system.
		return
	}
	setLoginTicketHeader(ctx, ticket)
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
	t.sweepLocked(now)
	st, ok := t.m[ip]
	if !ok {
		st = &backoffState{}
		t.m[ip] = st
	}
	st.failures++
	st.until = now.Add(backoffDelay(st.failures))
}

// backoffRetention is how long an expired entry is kept after its window
// closes. Comfortably past the 60s cap, so a genuine attacker pausing between
// attempts does not get a free reset by waiting — the entry, and therefore
// the failure count that makes the next delay longer, is still there.
const backoffRetention = 15 * time.Minute

// sweepLocked drops entries whose window closed longer ago than
// backoffRetention. The caller holds t.mu.
//
// The map was never evicted: one entry per distinct peer address, retained
// for the life of the process. Inert behind a proxy, where every request
// arrives from one address, and unbounded on a directly-reachable listener —
// a slow scan from many sources grows it without limit (A8-38).
//
// Swept on failure rather than on a timer: entries are only ever created
// here, so this is the one path that can grow the map, and it costs a walk
// of a map that is small precisely because this runs. No goroutine to own,
// start or stop.
func (t *backoffTracker) sweepLocked(now time.Time) {
	cutoff := now.Add(-backoffRetention)
	for ip, st := range t.m {
		if st.until.Before(cutoff) {
			delete(t.m, ip)
		}
	}
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
