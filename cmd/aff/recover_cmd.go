package main

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/rpc"
)

// cmdRecover implements `aff recover`: the unauthenticated-entry path back
// in when the operator cannot log in normally (PLAN.md §12.2's path 1 — a
// single-use recovery code, as opposed to the SSH break-glass paths in
// admin_cmd.go). It deliberately takes no --code flag: a recovery code is a
// bearer credential exactly like a password, and a flag value lands in `ps`
// output and the shell history file, so it is read from a prompt with echo
// suppressed like every other secret this CLI reads (readPassword).
//
// The whole flow — accept the code, then either change the password or
// re-enroll TOTP — runs inside this one invocation and never touches
// a.SessionFile. That is deliberate, not an oversight: RecoverWithCode opens
// a short-lived ELEVATED session scoped to exactly one follow-up call
// (internal/rpc/interceptor.go's elevatedAllowedMethods), and either
// follow-up call ends that session itself the instant it succeeds
// (PLAN.md §12.2: "forces a full re-login"). Persisting it to session.json
// between those two calls would create a session file that is either
// already dead (the common case, since one of the two calls below always
// runs immediately) or, if this process were interrupted between them,
// stranded and half-usable — reachable for nothing but expired. Not writing
// it at all removes that failure mode entirely: a failed or abandoned
// recovery leaves session.json exactly as it was found, and a
// process that dies mid-flow has consumed the recovery code (the store
// marks it used before the session is minted) but left nothing on disk to
// confuse the next command.
func (a *app) cmdRecover(args []string) int {
	fs := a.newFlagSet("aff recover")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(a.Stderr, "aff recover: takes no positional arguments")
		return exitUsage
	}

	// Stated plainly BEFORE the code is requested, per the task: discovering
	// the mutually-exclusive constraint only after the code is spent costs a
	// second code out of a finite set of ten.
	fmt.Fprintln(a.Stdout, "Account recovery consumes ONE single-use recovery code and opens a")
	fmt.Fprintln(a.Stdout, "short-lived elevated session that can do exactly ONE of the following —")
	fmt.Fprintln(a.Stdout, "not both, and not one after the other:")
	fmt.Fprintln(a.Stdout, "  1. Set a new password")
	fmt.Fprintln(a.Stdout, "  2. Re-enroll two-factor authentication (TOTP)")
	fmt.Fprintln(a.Stdout, "Choose now. If you need both, that costs two codes — decide which one this")
	fmt.Fprintln(a.Stdout, "code is for before you spend it.")

	choiceLine, err := a.readLine("Choose [1=password / 2=totp]: ")
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff recover: reading choice: %v\n", err)
		return exitFail
	}
	var wantsPassword bool
	switch strings.ToLower(strings.TrimSpace(choiceLine)) {
	case "1", "password", "change-password":
		wantsPassword = true
	case "2", "totp", "reenroll-totp":
		wantsPassword = false
	default:
		fmt.Fprintf(a.Stderr, "aff recover: %q is not a valid choice; want 1 (password) or 2 (totp)\n", choiceLine)
		return exitUsage
	}

	code, err := a.readPassword("Recovery code: ")
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff recover: reading recovery code: %v\n", err)
		return exitFail
	}

	ctx := context.Background()
	cb, err := a.client(ctx)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff recover: %v\n", err)
		return exitFail
	}

	var header metadata.MD
	resp, err := cb.Auth.RecoverWithCode(ctx, &affv1.AuthServiceRecoverWithCodeRequest{
		RecoveryCode: code,
	}, grpc.Header(&header))
	if err != nil {
		// Same generic-failure rule as `aff login` (PLAN.md §12.1/§12.2): one
		// string for every failure — unrecognized code, already-used code,
		// backoff active — so the response never becomes an oracle for
		// which of those is true.
		fmt.Fprintln(a.Stderr, "aff recover: recovery failed")
		return exitFail
	}
	elevatedToken := firstMetadataValue(header, rpc.SessionTokenHeader)
	if elevatedToken == "" {
		fmt.Fprintln(a.Stderr, "aff recover: server accepted the code but returned no elevated session token")
		return exitFail
	}

	remaining := resp.GetRemainingRecoveryCodes()
	fmt.Fprintf(a.Stdout, "\nRecovery code accepted. %d recovery code(s) remain.\n", remaining)
	if remaining <= 2 {
		fmt.Fprintln(a.Stdout, "WARNING: you are down to your last few recovery codes. Once they are gone,")
		fmt.Fprintln(a.Stdout, "the only way back in is SSH break-glass (`aff admin reset` on the box).")
	}

	// The elevated token this call minted is attached ONLY to the one
	// follow-up RPC below, as a per-call credential — never written to
	// a.SessionFile (see the doc comment above) and never mixed with
	// whatever a.client dialed with, which is whatever (if anything) the
	// operator's existing, ordinary session file held.
	elevatedCred := grpc.PerRPCCredentials(sessionCreds{token: elevatedToken})

	if wantsPassword {
		return a.recoverChangePassword(ctx, cb, elevatedCred)
	}
	return a.recoverReenrollTOTP(ctx, cb, elevatedCred)
}

// recoverChangePassword performs the "set new password" branch of `aff
// recover`, called only after RecoverWithCode has already succeeded and
// consumed the code. It requests no current password or TOTP code —
// internal/rpc/auth.go's ChangePassword skips that check entirely for an
// elevated caller, since the recovery code itself already re-proved
// identity.
func (a *app) recoverChangePassword(ctx context.Context, cb *clientBundle, elevatedCred grpc.CallOption) int {
	fmt.Fprintln(a.Stdout, "\nSetting a new password now ends this recovery session and revokes every")
	fmt.Fprintln(a.Stdout, "other active session. You will need to run `aff login` again afterward.")

	newPassword, err := a.readPassword("New password: ")
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff recover: reading new password: %v\n", err)
		return exitFail
	}
	confirmPassword, err := a.readPassword("Confirm new password: ")
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff recover: reading password confirmation: %v\n", err)
		return exitFail
	}
	if newPassword != confirmPassword {
		fmt.Fprintln(a.Stderr, "aff recover: new password and confirmation did not match")
		return exitFail
	}

	if _, err := cb.Auth.ChangePassword(ctx, &affv1.AuthServiceChangePasswordRequest{
		NewPassword: newPassword,
	}, elevatedCred); err != nil {
		fmt.Fprintf(a.Stderr, "aff recover: setting new password failed: %v\n", err)
		return exitFail
	}

	fmt.Fprintln(a.Stdout, "Password changed. Run `aff login` to authenticate again.")
	return exitOK
}

// recoverReenrollTOTP performs the "re-enroll TOTP" branch of `aff recover`,
// called only after RecoverWithCode has already succeeded and consumed the
// code. It sends no CurrentPassword — internal/rpc/auth.go's ReenrollTOTP
// skips that check entirely for an elevated caller, for the same reason as
// recoverChangePassword above.
func (a *app) recoverReenrollTOTP(ctx context.Context, cb *clientBundle, elevatedCred grpc.CallOption) int {
	fmt.Fprintln(a.Stdout, "\nRe-enrolling now ends this recovery session. Your OLD authenticator entry")
	fmt.Fprintln(a.Stdout, "stops working the instant this succeeds — have the new one ready to scan.")

	resp, err := cb.Auth.ReenrollTOTP(ctx, &affv1.AuthServiceReenrollTOTPRequest{}, elevatedCred)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff recover: re-enrolling TOTP failed: %v\n", err)
		return exitFail
	}

	a.printTOTPSecret(resp.GetProvisioningUri())
	fmt.Fprintln(a.Stdout, "Run `aff login` to authenticate again.")
	return exitOK
}
