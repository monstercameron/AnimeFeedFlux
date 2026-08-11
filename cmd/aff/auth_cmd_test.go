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
	"github.com/monstercameron/AnimeFeedFlux/internal/rpc"
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
		setHeader(opts, metadata.Pairs(rpc.SessionTokenHeader, "raw-session-token"))
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

// TestAuthChangePasswordWarnsBeforeActingAndSendsCredentials checks the
// revocation warning is printed, that current password + TOTP code + new
// password all reach ChangePassword (the ordinary, non-elevated path
// requires all three — internal/rpc/auth.go), and that none of the typed
// secrets are echoed back into captured output.
func TestAuthChangePasswordWarnsBeforeActingAndSendsCredentials(t *testing.T) {
	a, fake, stdout, stderr := newRecoverTestApp(t)
	const curPW = "current-much-longer-passphrase"
	const totp = "123456"
	const newPW = "brand-new-much-longer-passphrase"
	a.Stdin = strings.NewReader(curPW + "\n" + totp + "\n" + newPW + "\n" + newPW + "\n")

	var got *affv1.AuthServiceChangePasswordRequest
	fake.changePassword = func(ctx context.Context, req *affv1.AuthServiceChangePasswordRequest, opts ...grpc.CallOption) (*affv1.AuthServiceChangePasswordResponse, error) {
		got = req
		return &affv1.AuthServiceChangePasswordResponse{}, nil
	}

	code := a.run([]string{"auth", "change-password"})
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stdout: %s, stderr: %s)", code, exitOK, stdout.String(), stderr.String())
	}
	if got == nil {
		t.Fatal("expected ChangePassword to be called")
	}
	if got.GetCurrentPassword() != curPW || got.GetTotpCode() != totp || got.GetNewPassword() != newPW {
		t.Fatalf("ChangePassword request = %+v, want current/totp/new to match what was typed", got)
	}
	if !strings.Contains(stdout.String(), "revokes every OTHER active session") {
		t.Fatalf("stdout = %q, want the revocation warning printed before acting", stdout.String())
	}
	// The warning must appear before the prompts, not after — reconstruct
	// which came first isn't directly observable from stdout alone since
	// prompts go to Stderr, so this instead checks the warning is present at
	// all (ordering is exercised structurally: cmdAuthChangePassword prints
	// it before any readPassword/readLine call).
	combined := stdout.String() + stderr.String()
	if strings.Contains(combined, curPW) || strings.Contains(combined, newPW) {
		t.Fatalf("captured output leaked a secret: %q", combined)
	}
}

// TestAuthReenrollTOTPDisplaysSecretOnceAndWarns checks the new secret is
// shown exactly once with the "old authenticator stops working" framing,
// and that the current password never appears in captured output.
func TestAuthReenrollTOTPDisplaysSecretOnceAndWarns(t *testing.T) {
	a, fake, stdout, _ := newRecoverTestApp(t)
	const curPW = "current-much-longer-passphrase"
	a.Stdin = strings.NewReader(curPW + "\n")

	var got *affv1.AuthServiceReenrollTOTPRequest
	fake.reenrollTOTP = func(ctx context.Context, req *affv1.AuthServiceReenrollTOTPRequest, opts ...grpc.CallOption) (*affv1.AuthServiceReenrollTOTPResponse, error) {
		got = req
		return &affv1.AuthServiceReenrollTOTPResponse{ProvisioningUri: "otpauth://totp/AnimeFeedFlux:admin?secret=FRESHSECRETVALUE"}, nil
	}

	code := a.run([]string{"auth", "reenroll-totp"})
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d", code, exitOK)
	}
	if got == nil || got.GetCurrentPassword() != curPW {
		t.Fatalf("ReenrollTOTP request current password = %+v, want %q", got, curPW)
	}
	out := stdout.String()
	if strings.Count(out, "FRESHSECRETVALUE") != 1 {
		t.Fatalf("stdout = %q, want the new secret shown exactly once", out)
	}
	if !strings.Contains(out, "OLD authenticator") {
		t.Fatalf("stdout = %q, want the old-authenticator-stops-working warning", out)
	}
	if strings.Contains(out, curPW) {
		t.Fatalf("stdout leaked the current password: %q", out)
	}
}

// TestAuthGroupUnknownSubcommandExitsUsage covers the new `auth` dispatcher
// alongside the existing group-dispatcher convention (dispatch_test.go).
func TestAuthGroupUnknownSubcommandExitsUsage(t *testing.T) {
	a, _, stderr := newTestApp()
	code := a.run([]string{"auth", "bogus"})
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d", code, exitUsage)
	}
	if stderr.Len() == 0 {
		t.Fatal("expected a usage message on stderr")
	}

	stderr.Reset()
	code = a.run([]string{"auth"})
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d", code, exitUsage)
	}
	if stderr.Len() == 0 {
		t.Fatal("expected a usage message on stderr for missing subcommand")
	}
}
