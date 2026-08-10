package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

func TestLoginSavesSessionFromResponseHeader(t *testing.T) {
	a, stdout, stderr := newTestApp()
	a.Stdin = strings.NewReader("hunter2-a-much-longer-passphrase\ntotp-code-here\n")
	a.Server = "127.0.0.1:9091"
	a.SessionFile = filepath.Join(t.TempDir(), "session.json")

	auth := a.clients.Auth.(*fakeAuthClient)
	auth.login = func(ctx context.Context, req *affv1.AuthServiceLoginRequest, opts ...grpc.CallOption) (*affv1.AuthServiceLoginResponse, error) {
		if req.GetPassword() == "" || req.GetTotpCode() == "" {
			t.Fatalf("expected non-empty password and totp code, got %q / %q", req.GetPassword(), req.GetTotpCode())
		}
		setHeader(opts, metadata.Pairs(sessionMetadataKey, "raw-session-token"))
		return &affv1.AuthServiceLoginResponse{
			Session: &affv1.Session{
				Id:        "sess-42",
				ExpiresAt: timestamppb.Now(),
			},
		}, nil
	}

	code := a.run([]string{"login"})
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stdout: %s, stderr: %s)", code, exitOK, stdout.String(), stderr.String())
	}

	sd, err := loadSession(a.SessionFile)
	if err != nil {
		t.Fatalf("loadSession: %v", err)
	}
	if sd.Token != "raw-session-token" {
		t.Fatalf("session token = %q, want %q", sd.Token, "raw-session-token")
	}
	if sd.SessionID != "sess-42" {
		t.Fatalf("session id = %q, want %q", sd.SessionID, "sess-42")
	}
}

func TestLoginFailsWithoutSessionToken(t *testing.T) {
	a, _, stderr := newTestApp()
	a.Stdin = strings.NewReader("hunter2-a-much-longer-passphrase\ntotp-code-here\n")
	a.Server = "127.0.0.1:9091"
	a.SessionFile = filepath.Join(t.TempDir(), "session.json")

	auth := a.clients.Auth.(*fakeAuthClient)
	auth.login = func(ctx context.Context, req *affv1.AuthServiceLoginRequest, opts ...grpc.CallOption) (*affv1.AuthServiceLoginResponse, error) {
		// No header set: the server "accepted" the login but the CLI has no
		// token to persist, which must fail rather than write a useless
		// session file.
		return &affv1.AuthServiceLoginResponse{Session: &affv1.Session{Id: "sess-1"}}, nil
	}

	code := a.run([]string{"login"})
	if code != exitFail {
		t.Fatalf("exit code = %d, want %d", code, exitFail)
	}
	if !strings.Contains(stderr.String(), "no session token") {
		t.Fatalf("stderr = %q, want it to mention the missing token", stderr.String())
	}
	if _, err := loadSession(a.SessionFile); err == nil {
		t.Fatal("expected no session file to be written")
	}
}

func TestLoginGenericErrorOnAuthFailure(t *testing.T) {
	a, _, stderr := newTestApp()
	a.Stdin = strings.NewReader("hunter2-a-much-longer-passphrase\nwrong-code\n")
	a.Server = "127.0.0.1:9091"
	a.SessionFile = filepath.Join(t.TempDir(), "session.json")

	auth := a.clients.Auth.(*fakeAuthClient)
	auth.login = func(ctx context.Context, req *affv1.AuthServiceLoginRequest, opts ...grpc.CallOption) (*affv1.AuthServiceLoginResponse, error) {
		return nil, errors.New("rpc error: code = Unauthenticated desc = invalid credentials")
	}

	code := a.run([]string{"login"})
	if code != exitFail {
		t.Fatalf("exit code = %d, want %d", code, exitFail)
	}
	// PLAN.md §12.1: one generic failure string, never a more specific one
	// leaked from the gRPC status.
	if !strings.Contains(stderr.String(), "authentication failed") {
		t.Fatalf("stderr = %q, want the generic authentication-failed message", stderr.String())
	}
}
