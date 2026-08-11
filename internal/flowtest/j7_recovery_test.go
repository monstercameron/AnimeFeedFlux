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
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/auth"
	"github.com/monstercameron/AnimeFeedFlux/internal/rpc"
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

// j7BuildAuthRPCServer stands up a real, minimal *grpc.Server exposing ONLY
// AuthService, with authSrv's own session interceptor (internal/rpc/
// interceptor.go's UnaryInterceptor) chained in front of every call — the
// same shape internal/e2e/app.go's plain, pre-bridge listener uses for
// Login, reproduced here because this package may not import internal/e2e
// (a `_test`-suffixed sibling suite, not a library) and may not edit
// internal/rpc or internal/bridge to expose a shared test helper. This is
// what turns BF-32 from "does the interceptor's logic look right in
// isolation" into "does a real RPC call over a real network connection get
// refused or allowed exactly as PLAN.md §12.2 requires" (§17.5).
func j7BuildAuthRPCServer(t *testing.T, w *World) (authSrv *rpc.AuthServer, addr string) {
	t.Helper()
	authSrv, err := rpc.NewAuthServer(w.Store, j1SecretKey)
	if err != nil {
		t.Fatalf("rpc.NewAuthServer: %v", err)
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening for j7's plain grpc server: %v", err)
	}
	gs := grpc.NewServer(grpc.ChainUnaryInterceptor(authSrv.UnaryInterceptor()))
	affv1.RegisterAuthServiceServer(gs, authSrv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	return authSrv, lis.Addr().String()
}

// j7DialAuthClient dials addr with no transport security (matching the
// loopback, no-TLS shape every other plain-grpc test fixture in this repo
// uses, e.g. internal/e2e/app.go's LoginWithCode).
func j7DialAuthClient(t *testing.T, addr string) affv1.AuthServiceClient {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dialing j7's plain grpc server: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return affv1.NewAuthServiceClient(conn)
}

// j7CtxWithToken attaches the raw session token to ctx as outgoing gRPC
// metadata under rpc.SessionTokenHeader — interceptor.go's
// sessionTokenFromContext documents this as the source used by "any future
// transport that isn't internal/bridge (e.g. cmd/aff's plain-gRPC
// session-metadata path)", which is exactly the shape of the plain
// connection j7BuildAuthRPCServer/j7DialAuthClient set up: there is no
// bridge.Session on this connection to carry the token instead.
func j7CtxWithToken(ctx context.Context, token string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, rpc.SessionTokenHeader, token)
}

// TestJ7_ElevatedSessionReachesOnlyPasswordAndTOTP is BF-32. The blocker the
// original skip named — "sessions carry no scope field, and neither
// AuthService nor its interceptor exist" — is gone: migrations/
// 0005_session_scope.sql added sessions.scope, AuthService is real
// (internal/rpc/auth.go), and its UnaryInterceptor (internal/rpc/
// interceptor.go) enforces elevatedAllowedMethods as a default-deny
// allowlist on every single RPC, not just the two it happens to permit.
//
// This drives two REAL RecoverWithCode calls over a real (plain,
// pre-bridge) grpc.Server with authSrv's own interceptor chained in front,
// then proves: the resulting session's own row is marked elevated (real
// state, not the in-memory tracker's opinion of itself); an ordinary
// AuthService method with nothing bespoke about it (ListSessions — chosen
// specifically because it is unremarkable surface, not something
// special-cased to fail) is refused with PermissionDenied; and each of the
// two methods §12.2 actually grants — ChangePassword and ReenrollTOTP — is
// reachable. Two separate recovery flows are needed because a successful
// ChangePassword or ReenrollTOTP from an elevated session ends that session
// (auth.go's endElevatedSession), so testing both allowed methods against
// the same still-elevated session is not possible without exercising a
// state the real system never lets exist.
func TestJ7_ElevatedSessionReachesOnlyPasswordAndTOTP(t *testing.T) {
	w := New(t)
	ctx := t.Context()
	j1SetupAdmin(t, w)
	recoveryCodes := j7SetupRecoveryCodes(t, w, 5)

	_, addr := j7BuildAuthRPCServer(t, w)
	client := j7DialAuthClient(t, addr)

	// --- Flow 1: elevated session's scope, and the default-deny refusal ---
	var header1 metadata.MD
	rec1, err := client.RecoverWithCode(ctx, &affv1.AuthServiceRecoverWithCodeRequest{
		RecoveryCode: recoveryCodes[0],
	}, grpc.Header(&header1))
	if err != nil {
		t.Fatalf("RecoverWithCode (flow 1): %v", err)
	}
	token1 := header1.Get(rpc.SessionTokenHeader)
	if len(token1) == 0 || token1[0] == "" {
		t.Fatal("RecoverWithCode response carried no session token header")
	}
	elevatedCtx1 := j7CtxWithToken(ctx, token1[0])
	sessID1, err := strconv.ParseInt(rec1.GetSession().GetId(), 10, 64)
	if err != nil {
		t.Fatalf("parsing session id %q: %v", rec1.GetSession().GetId(), err)
	}

	// BF-32 (§22 J7): an elevated session cannot reach anything but password
	// reset and TOTP re-enrollment. ListSessions is ordinary AuthService
	// surface with no elevated-specific branch of its own — exactly the kind
	// of method a hand-maintained per-handler check would be most likely to
	// forget to gate, which is why interceptor.go's default-deny allowlist
	// matters more here than on a method that already special-cases itself.
	if _, err := client.ListSessions(elevatedCtx1, &affv1.AuthServiceListSessionsRequest{}); err == nil {
		t.Fatal("ListSessions unexpectedly succeeded from an elevated session")
	} else if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("ListSessions from an elevated session: code = %v, want PermissionDenied", status.Code(err))
	}

	// The session row itself is scoped elevated — real state persisted by
	// migration 0005, not merely the elevatedTracker's private bookkeeping.
	// This is checked AFTER the ListSessions call above rather than right
	// after RecoverWithCode, because interceptor.go's authorize() is what
	// writes the scope column (RecoverWithCode itself is in
	// noSessionMethods and short-circuits authorize entirely, per
	// interceptor.go's own comment) — the column reflects "the scope of the
	// last authorized call", not "the scope this session was minted with",
	// until at least one authenticated call has gone through the
	// interceptor. ListSessions above is exactly such a call, even though
	// it was refused.
	scope, err := w.Store.SessionScope(ctx, sessID1)
	if err != nil {
		t.Fatalf("SessionScope: %v", err)
	}
	if scope != store.SessionScopeElevated {
		t.Fatalf("recovery session scope = %q, want %q", scope, store.SessionScopeElevated)
	}
	// RegenerateRecoveryCodes is likewise outside the allowlist (auth.go's
	// own comment on it: "never reachable from an elevated session").
	if _, err := client.RegenerateRecoveryCodes(elevatedCtx1, &affv1.AuthServiceRegenerateRecoveryCodesRequest{}); err == nil {
		t.Fatal("RegenerateRecoveryCodes unexpectedly succeeded from an elevated session")
	} else if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("RegenerateRecoveryCodes from an elevated session: code = %v, want PermissionDenied", status.Code(err))
	}

	// Allowed: ChangePassword. An elevated session skips the current-
	// password/TOTP re-proof (ChangePassword's own doc comment) — identity
	// was already re-proven by the single-use recovery code that minted this
	// session.
	if _, err := client.ChangePassword(elevatedCtx1, &affv1.AuthServiceChangePasswordRequest{
		NewPassword: "a brand new elevated-session passphrase!!",
	}); err != nil {
		t.Fatalf("ChangePassword from an elevated session should be reachable, got: %v", err)
	}

	// --- Flow 2: the OTHER allowed method, ReenrollTOTP ---
	// A fresh recovery is needed: flow 1's ChangePassword ended its own
	// session (auth.go's endElevatedSession) and RecoverWithCode revokes
	// every existing session on success regardless, so this is a clean new
	// elevated session, not a reuse of flow 1's.
	var header2 metadata.MD
	if _, err := client.RecoverWithCode(ctx, &affv1.AuthServiceRecoverWithCodeRequest{
		RecoveryCode: recoveryCodes[1],
	}, grpc.Header(&header2)); err != nil {
		t.Fatalf("RecoverWithCode (flow 2): %v", err)
	}
	token2 := header2.Get(rpc.SessionTokenHeader)
	if len(token2) == 0 || token2[0] == "" {
		t.Fatal("RecoverWithCode (flow 2) response carried no session token header")
	}
	elevatedCtx2 := j7CtxWithToken(ctx, token2[0])

	if _, err := client.ReenrollTOTP(elevatedCtx2, &affv1.AuthServiceReenrollTOTPRequest{}); err != nil {
		t.Fatalf("ReenrollTOTP from an elevated session should be reachable, got: %v", err)
	}
}
