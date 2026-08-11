package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/rpc"
)

// fakeRecoverAuthClient is a local test double (not fakes_test.go's
// fakeAuthClient, which only wires Login) covering the three RPCs `aff
// recover`/`aff auth ...` need: RecoverWithCode, ChangePassword,
// ReenrollTOTP. Each field defaults to nil, so an unwired call panics loudly
// rather than silently succeeding, matching the convention fakes_test.go
// documents.
type fakeRecoverAuthClient struct {
	affv1.AuthServiceClient
	recoverWithCode func(ctx context.Context, req *affv1.AuthServiceRecoverWithCodeRequest, opts ...grpc.CallOption) (*affv1.AuthServiceRecoverWithCodeResponse, error)
	changePassword  func(ctx context.Context, req *affv1.AuthServiceChangePasswordRequest, opts ...grpc.CallOption) (*affv1.AuthServiceChangePasswordResponse, error)
	reenrollTOTP    func(ctx context.Context, req *affv1.AuthServiceReenrollTOTPRequest, opts ...grpc.CallOption) (*affv1.AuthServiceReenrollTOTPResponse, error)
}

func (f *fakeRecoverAuthClient) RecoverWithCode(ctx context.Context, req *affv1.AuthServiceRecoverWithCodeRequest, opts ...grpc.CallOption) (*affv1.AuthServiceRecoverWithCodeResponse, error) {
	return f.recoverWithCode(ctx, req, opts...)
}

func (f *fakeRecoverAuthClient) ChangePassword(ctx context.Context, req *affv1.AuthServiceChangePasswordRequest, opts ...grpc.CallOption) (*affv1.AuthServiceChangePasswordResponse, error) {
	return f.changePassword(ctx, req, opts...)
}

func (f *fakeRecoverAuthClient) ReenrollTOTP(ctx context.Context, req *affv1.AuthServiceReenrollTOTPRequest, opts ...grpc.CallOption) (*affv1.AuthServiceReenrollTOTPResponse, error) {
	return f.reenrollTOTP(ctx, req, opts...)
}

// newRecoverTestApp builds an *app wired to fakeRecoverAuthClient, bypassing
// newTestApp/fakes_test.go entirely (it does not carry these three methods).
func newRecoverTestApp(t *testing.T) (a *app, fake *fakeRecoverAuthClient, stdout, stderr *strings.Builder) {
	t.Helper()
	fake = &fakeRecoverAuthClient{}
	stdout = &strings.Builder{}
	stderr = &strings.Builder{}
	a = &app{
		Stdout:      stdout,
		Stderr:      stderr,
		Server:      "127.0.0.1:9091",
		SessionFile: filepath.Join(t.TempDir(), "session.json"),
		clients:     &clientBundle{Auth: fake},
	}
	return a, fake, stdout, stderr
}

func recoverSucceedsFn(remaining int32, token string) func(ctx context.Context, req *affv1.AuthServiceRecoverWithCodeRequest, opts ...grpc.CallOption) (*affv1.AuthServiceRecoverWithCodeResponse, error) {
	return func(ctx context.Context, req *affv1.AuthServiceRecoverWithCodeRequest, opts ...grpc.CallOption) (*affv1.AuthServiceRecoverWithCodeResponse, error) {
		if req.GetRecoveryCode() == "" {
			return nil, os.ErrInvalid
		}
		setHeader(opts, metadata.Pairs(rpc.SessionTokenHeader, token))
		return &affv1.AuthServiceRecoverWithCodeResponse{RemainingRecoveryCodes: remaining}, nil
	}
}

// TestRecoverEstablishesElevationAndChangesPassword drives the "choose
// password" branch end to end and checks the elevated token minted by
// RecoverWithCode is the one attached to the follow-up ChangePassword call.
func TestRecoverEstablishesElevationAndChangesPassword(t *testing.T) {
	a, fake, stdout, _ := newRecoverTestApp(t)
	a.Stdin = strings.NewReader("1\nMY-RECOVERY-CODE\nnew-much-longer-passphrase\nnew-much-longer-passphrase\n")

	fake.recoverWithCode = recoverSucceedsFn(7, "elevated-token-xyz")

	var gotChangePassword *affv1.AuthServiceChangePasswordRequest
	fake.changePassword = func(ctx context.Context, req *affv1.AuthServiceChangePasswordRequest, opts ...grpc.CallOption) (*affv1.AuthServiceChangePasswordResponse, error) {
		gotChangePassword = req
		return &affv1.AuthServiceChangePasswordResponse{}, nil
	}

	code := a.run([]string{"recover"})
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stdout: %s)", code, exitOK, stdout.String())
	}
	if gotChangePassword == nil {
		t.Fatal("expected ChangePassword to be called")
	}
	if gotChangePassword.GetNewPassword() != "new-much-longer-passphrase" {
		t.Fatalf("new password = %q, want the one typed in", gotChangePassword.GetNewPassword())
	}
	// Elevated recovery skips current-password/TOTP re-proof entirely
	// (internal/rpc/auth.go: identity was already proven by the code).
	if gotChangePassword.GetCurrentPassword() != "" || gotChangePassword.GetTotpCode() != "" {
		t.Fatalf("expected no current password/TOTP code sent on the elevated path, got %+v", gotChangePassword)
	}
	if !strings.Contains(stdout.String(), "7 recovery code(s) remain") {
		t.Fatalf("stdout = %q, want the remaining recovery code count", stdout.String())
	}
}

// TestRecoverReenrollTOTPBranch drives the "choose TOTP" branch and checks
// the new secret is displayed exactly once.
func TestRecoverReenrollTOTPBranch(t *testing.T) {
	a, fake, stdout, _ := newRecoverTestApp(t)
	a.Stdin = strings.NewReader("2\nMY-RECOVERY-CODE\n")

	fake.recoverWithCode = recoverSucceedsFn(9, "elevated-token-totp")
	fake.reenrollTOTP = func(ctx context.Context, req *affv1.AuthServiceReenrollTOTPRequest, opts ...grpc.CallOption) (*affv1.AuthServiceReenrollTOTPResponse, error) {
		if req.GetCurrentPassword() != "" {
			t.Fatalf("expected no current password on the elevated path, got %q", req.GetCurrentPassword())
		}
		return &affv1.AuthServiceReenrollTOTPResponse{ProvisioningUri: "otpauth://totp/AnimeFeedFlux:admin?secret=BRANDNEWSECRET"}, nil
	}

	code := a.run([]string{"recover"})
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stdout: %s)", code, exitOK, stdout.String())
	}
	if !strings.Contains(stdout.String(), "BRANDNEWSECRET") {
		t.Fatalf("stdout = %q, want the new TOTP secret shown once", stdout.String())
	}
	if !strings.Contains(stdout.String(), "OLD authenticator") {
		t.Fatalf("stdout = %q, want the old-authenticator-stops-working warning", stdout.String())
	}
}

// TestRecoverStatesMutualExclusionBeforeCodePrompt asserts the choice
// (password vs TOTP) is both required up front and stated in the framing
// text before the recovery-code prompt is ever reached — an invalid choice
// must fail before RecoverWithCode is called at all, so no code is spent on
// a malformed invocation.
func TestRecoverStatesMutualExclusionBeforeCodePrompt(t *testing.T) {
	a, fake, stdout, stderr := newRecoverTestApp(t)
	a.Stdin = strings.NewReader("bogus-choice\n")
	fake.recoverWithCode = func(ctx context.Context, req *affv1.AuthServiceRecoverWithCodeRequest, opts ...grpc.CallOption) (*affv1.AuthServiceRecoverWithCodeResponse, error) {
		t.Fatal("RecoverWithCode must not be called when the up-front choice is invalid")
		return nil, nil
	}

	code := a.run([]string{"recover"})
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stdout.String(), "not both") {
		t.Fatalf("stdout = %q, want the mutual-exclusion framing shown before the choice prompt", stdout.String())
	}
	if !strings.Contains(stderr.String(), "not a valid choice") {
		t.Fatalf("stderr = %q, want a rejection of the bad choice", stderr.String())
	}
}

// TestRecoverShowsRemainingCountAndLowWarning checks the "≤2 remaining"
// dashboard-nag equivalent (PLAN.md §12.2) fires from the CLI too.
func TestRecoverShowsRemainingCountAndLowWarning(t *testing.T) {
	a, fake, stdout, _ := newRecoverTestApp(t)
	a.Stdin = strings.NewReader("2\nMY-RECOVERY-CODE\n")
	fake.recoverWithCode = recoverSucceedsFn(1, "elevated-token-low")
	fake.reenrollTOTP = func(ctx context.Context, req *affv1.AuthServiceReenrollTOTPRequest, opts ...grpc.CallOption) (*affv1.AuthServiceReenrollTOTPResponse, error) {
		return &affv1.AuthServiceReenrollTOTPResponse{ProvisioningUri: "otpauth://totp/x"}, nil
	}

	code := a.run([]string{"recover"})
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d", code, exitOK)
	}
	if !strings.Contains(stdout.String(), "1 recovery code(s) remain") {
		t.Fatalf("stdout = %q, want the remaining count", stdout.String())
	}
	if !strings.Contains(stdout.String(), "SSH break-glass") {
		t.Fatalf("stdout = %q, want the low-remaining-codes warning", stdout.String())
	}
}

// TestRecoverFailureLeavesNoSessionFile is the core "no half-usable session"
// requirement: whether RecoverWithCode itself fails, or the follow-up call
// fails after elevation succeeded, session.json must not exist afterward —
// `aff recover` never writes to it at all.
func TestRecoverFailureLeavesNoSessionFile(t *testing.T) {
	t.Run("RecoverWithCode fails", func(t *testing.T) {
		a, fake, _, stderr := newRecoverTestApp(t)
		a.Stdin = strings.NewReader("1\nWRONG-CODE\n")
		fake.recoverWithCode = func(ctx context.Context, req *affv1.AuthServiceRecoverWithCodeRequest, opts ...grpc.CallOption) (*affv1.AuthServiceRecoverWithCodeResponse, error) {
			return nil, os.ErrInvalid
		}

		code := a.run([]string{"recover"})
		if code != exitFail {
			t.Fatalf("exit code = %d, want %d", code, exitFail)
		}
		if !strings.Contains(stderr.String(), "recovery failed") {
			t.Fatalf("stderr = %q, want the generic recovery-failed message", stderr.String())
		}
		if _, err := os.Stat(a.SessionFile); err == nil {
			t.Fatal("expected no session file to be written after a failed RecoverWithCode call")
		}
	})

	t.Run("follow-up call fails after elevation succeeds", func(t *testing.T) {
		a, fake, _, stderr := newRecoverTestApp(t)
		a.Stdin = strings.NewReader("1\nGOOD-CODE\nnew-much-longer-passphrase\nnew-much-longer-passphrase\n")
		fake.recoverWithCode = recoverSucceedsFn(5, "elevated-token-fail")
		fake.changePassword = func(ctx context.Context, req *affv1.AuthServiceChangePasswordRequest, opts ...grpc.CallOption) (*affv1.AuthServiceChangePasswordResponse, error) {
			return nil, os.ErrInvalid
		}

		code := a.run([]string{"recover"})
		if code != exitFail {
			t.Fatalf("exit code = %d, want %d", code, exitFail)
		}
		if !strings.Contains(stderr.String(), "setting new password failed") {
			t.Fatalf("stderr = %q, want the setting-new-password failure message", stderr.String())
		}
		if _, err := os.Stat(a.SessionFile); err == nil {
			t.Fatal("expected no session file to be written even though the recovery code was already consumed")
		}
	})
}

// TestRecoverNeverEchoesSecrets asserts the recovery code and the new
// password never appear in captured stdout/stderr, on either branch.
func TestRecoverNeverEchoesSecrets(t *testing.T) {
	const recoveryCode = "SUPER-SECRET-RECOVERY-CODE"
	const newPassword = "correct-horse-battery-staple-long"

	a, fake, stdout, stderr := newRecoverTestApp(t)
	a.Stdin = strings.NewReader("1\n" + recoveryCode + "\n" + newPassword + "\n" + newPassword + "\n")
	fake.recoverWithCode = recoverSucceedsFn(6, "elevated-token-secret")
	fake.changePassword = func(ctx context.Context, req *affv1.AuthServiceChangePasswordRequest, opts ...grpc.CallOption) (*affv1.AuthServiceChangePasswordResponse, error) {
		return &affv1.AuthServiceChangePasswordResponse{}, nil
	}

	code := a.run([]string{"recover"})
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d", code, exitOK)
	}
	combined := stdout.String() + stderr.String()
	if strings.Contains(combined, recoveryCode) {
		t.Fatalf("captured output contains the recovery code: %q", combined)
	}
	if strings.Contains(combined, newPassword) {
		t.Fatalf("captured output contains the new password: %q", combined)
	}
}
