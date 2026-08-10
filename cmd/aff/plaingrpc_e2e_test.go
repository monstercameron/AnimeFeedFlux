package main

// End-to-end proof that `aff` actually authenticates over a real, plain
// (non-bridge) gRPC connection — the exact gap this change closes. Every
// other test in this package drives the CLI against fakeAuthClient, which
// proves the CLI's own logic but never proves the CLI and internal/rpc agree
// on anything on the wire. This file stands up a real internal/rpc.AuthServer
// behind a real net.Listener and drives it with the CLI's real dial path
// (a.realDial / sessionCreds), the same code `aff` runs in production.

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/auth"
	"github.com/monstercameron/AnimeFeedFlux/internal/rpc"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// e2ePassword clears internal/auth's 15-char NIST floor.
const e2ePassword = "correct horse battery staple long enough"

var e2eSecretKey = []byte("plaingrpc-e2e-secret-key-not-real")

// startPlainGRPCServer builds a real internal/rpc.AuthServer over a real
// store, wires its interceptors into a real grpc.Server on a real TCP
// listener, and returns the address plus the TOTP secret needed to compute
// login codes. This deliberately does NOT go through internal/bridge — the
// whole point is to reproduce cmd/aff's actual transport (a plain
// grpc.NewClient straight at AdminAddr), which is the leg that silently
// authorized as Unauthenticated before this change.
func startPlainGRPCServer(t *testing.T) (addr string, totpSecret string, st *store.Store) {
	t.Helper()

	st, err := store.Open(t.Context(), store.Options{Path: t.TempDir() + "/aff.db"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	hash, err := auth.Hash(e2ePassword, auth.DefaultParams())
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if err := st.InitAdmin(t.Context(), hash, "argon2id m=65536,t=3,p=1"); err != nil {
		t.Fatalf("init admin: %v", err)
	}

	secret, _, err := auth.Enroll("admin", "AnimeFeedFlux-e2e")
	if err != nil {
		t.Fatalf("enroll totp: %v", err)
	}
	enc, err := auth.EncryptSecret(secret, e2eSecretKey)
	if err != nil {
		t.Fatalf("encrypt totp secret: %v", err)
	}
	if err := st.SetTOTPSecret(t.Context(), enc); err != nil {
		t.Fatalf("set totp secret: %v", err)
	}

	srv, err := rpc.NewAuthServer(st, e2eSecretKey)
	if err != nil {
		t.Fatalf("new auth server: %v", err)
	}

	gs := grpc.NewServer(
		grpc.UnaryInterceptor(srv.UnaryInterceptor()),
		grpc.StreamInterceptor(srv.StreamInterceptor()),
	)
	affv1.RegisterAuthServiceServer(gs, srv)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	return lis.Addr().String(), secret, st
}

// newE2EApp builds a real (non-fake) *app pointed at addr, using the CLI's
// production dial path.
func newE2EApp(t *testing.T, addr, sessionFile string) *app {
	t.Helper()
	a := &app{
		Stdout:      new(strings.Builder),
		Stderr:      new(strings.Builder),
		Stdin:       strings.NewReader(""),
		Server:      addr,
		SessionFile: sessionFile,
	}
	a.dial = a.realDial
	return a
}

// TestPlainGRPCLoginThenAuthenticatedCallSucceeds is the headline proof: a
// real `aff login` against a real server, followed by a real authenticated
// RPC over the same plain-gRPC connection, using nothing but the CLI's own
// production code paths on both ends.
func TestPlainGRPCLoginThenAuthenticatedCallSucceeds(t *testing.T) {
	addr, secret, _ := startPlainGRPCServer(t)
	sessionFile := t.TempDir() + "/session.json"

	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate totp code: %v", err)
	}

	a := newE2EApp(t, addr, sessionFile)
	a.Stdin = strings.NewReader(e2ePassword + "\n" + code + "\n")
	if got := a.run([]string{"login"}); got != exitOK {
		t.Fatalf("aff login exit code = %d, want %d (stderr: %s)", got, exitOK, a.Stderr)
	}
	_ = a.Close()

	sd, err := loadSession(sessionFile)
	if err != nil {
		t.Fatalf("loadSession: %v", err)
	}
	if sd.Token == "" {
		t.Fatal("aff login saved an empty token")
	}

	// A second, freshly dialed app instance reads the saved session file and
	// must be able to make an authenticated call over the same plain-gRPC
	// transport — this is leg 2 (session.go's sessionCreds + interceptor.go's
	// metadata fallback) actually working end to end, not leg 1 (login) alone.
	b := newE2EApp(t, addr, sessionFile)
	cb, err := b.client(context.Background())
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	defer func() { _ = b.Close() }()

	resp, err := cb.Auth.Session(context.Background(), &affv1.AuthServiceSessionRequest{})
	if err != nil {
		t.Fatalf("authenticated Session call over plain gRPC failed: %v", err)
	}
	if resp.GetSession().GetId() != sd.SessionID {
		t.Fatalf("session id = %q, want %q", resp.GetSession().GetId(), sd.SessionID)
	}
}

// TestPlainGRPCRevokedSessionRefused proves SEC-41 holds on the metadata
// path too: a session revoked server-side after the CLI already stored a
// token for it must be refused on the very next call, not merely at the next
// login.
func TestPlainGRPCRevokedSessionRefused(t *testing.T) {
	addr, secret, st := startPlainGRPCServer(t)
	sessionFile := t.TempDir() + "/session.json"

	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate totp code: %v", err)
	}

	a := newE2EApp(t, addr, sessionFile)
	a.Stdin = strings.NewReader(e2ePassword + "\n" + code + "\n")
	if got := a.run([]string{"login"}); got != exitOK {
		t.Fatalf("aff login exit code = %d, want %d (stderr: %s)", got, exitOK, a.Stderr)
	}
	_ = a.Close()

	sd, err := loadSession(sessionFile)
	if err != nil {
		t.Fatalf("loadSession: %v", err)
	}

	sess, err := st.GetSessionByTokenHash(t.Context(), auth.HashToken(sd.Token))
	if err != nil {
		t.Fatalf("lookup session: %v", err)
	}
	var id int64
	if err := st.Writer().QueryRowContext(t.Context(),
		`SELECT id FROM sessions WHERE token_hash = ?`, sess.TokenHash).Scan(&id); err != nil {
		t.Fatalf("resolve session id: %v", err)
	}
	if err := st.RevokeSession(t.Context(), id); err != nil {
		t.Fatalf("revoke session: %v", err)
	}

	b := newE2EApp(t, addr, sessionFile)
	cb, err := b.client(context.Background())
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	defer func() { _ = b.Close() }()

	_, err = cb.Auth.Session(context.Background(), &affv1.AuthServiceSessionRequest{})
	if err == nil {
		t.Fatal("expected a revoked session to be refused over plain gRPC")
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", status.Code(err))
	}
}
