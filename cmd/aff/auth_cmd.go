package main

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/rpc"
)

// cmdLogin implements `aff login`: prompt for password (no echo) then TOTP,
// call AuthService.Login, and persist the session it returns. This is the
// CLI's only entry point that authenticates — every other command reads the
// session saveLogin writes here (PLAN.md §11: the CLI is a client of the
// same AuthService the UI uses, not a bypass of it).
func (a *app) cmdLogin(args []string) int {
	fs := a.newFlagSet("aff login")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(a.Stderr, "aff login: takes no positional arguments")
		return exitUsage
	}

	password, err := a.readPassword("Password: ")
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff login: reading password: %v\n", err)
		return exitFail
	}
	code, err := a.readLine("TOTP code: ")
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff login: reading TOTP code: %v\n", err)
		return exitFail
	}

	ctx := context.Background()
	cb, err := a.client(ctx)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff login: %v\n", err)
		return exitFail
	}

	var header metadata.MD
	resp, err := cb.Auth.Login(ctx, &affv1.AuthServiceLoginRequest{
		Password: password,
		TotpCode: code,
	}, grpc.Header(&header))
	if err != nil {
		if isUnreachable(err) {
			// The RPC never reached AuthServer.Login at all, so
			// errAuthFailed's generic string would be actively misleading
			// here — it reads identically to a wrong password, and a dead
			// admin port produces exactly that message with auth_events
			// staying empty, which is what sends an operator hunting for a
			// credentials bug when the socket was never open. PLAN.md
			// §12.1's generic-failure rule protects against a server that
			// DID evaluate credentials; it says nothing about a connection
			// that never reached the server, so naming the transport
			// failure and the address dialled helps no attacker while
			// sparing the operator that detour.
			fmt.Fprintf(a.Stderr, "aff login: could not reach admin server at %s: %v\n", a.Server, err)
			return exitFail
		}
		// PLAN.md §12.1: one generic failure string for every credential
		// failure, wrong password or wrong code alike — the CLI must not
		// invent a more specific message from the gRPC status than the
		// server chose to send, or it reopens the account-existence oracle
		// §12.1 exists to close.
		fmt.Fprintln(a.Stderr, "aff login: authentication failed")
		return exitFail
	}

	token := firstMetadataValue(header, rpc.SessionTokenHeader)
	if token == "" {
		fmt.Fprintln(a.Stderr, "aff login: server accepted the login but returned no session token")
		return exitFail
	}

	sd := &sessionData{Server: a.Server, Token: token}
	if sess := resp.GetSession(); sess != nil {
		sd.SessionID = sess.GetId()
		if exp := sess.GetExpiresAt(); exp != nil {
			sd.ExpiresAt = exp.AsTime()
		}
	}

	if err := saveSession(a.SessionFile, sd); err != nil {
		fmt.Fprintf(a.Stderr, "aff login: %v\n", err)
		return exitFail
	}

	if a.JSON {
		_ = a.printJSON(map[string]any{
			"logged_in":  true,
			"session_id": sd.SessionID,
			"expires_at": sd.ExpiresAt,
		})
	} else {
		fmt.Fprintln(a.Stdout, "Logged in.")
	}
	return exitOK
}

func firstMetadataValue(md metadata.MD, key string) string {
	vals := md.Get(key)
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}

// cmdAuth dispatches `aff auth change-password|reenroll-totp` — the two
// ordinary, already-authenticated cases (PLAN.md §12.2's "same" flows
// login/settings use, just via the CLI). `aff recover` (recover_cmd.go) is
// the separate, unauthenticated-entry path for when the operator cannot log
// in at all; these two require an existing local session the way every other
// non-admin command does.
func (a *app) cmdAuth(args []string) int {
	if len(args) == 0 {
		return a.missingSubcommand("auth", "change-password", "reenroll-totp")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "change-password":
		return a.cmdAuthChangePassword(rest)
	case "reenroll-totp":
		return a.cmdAuthReenrollTOTP(rest)
	default:
		return a.unknownSubcommand("auth", sub, "change-password", "reenroll-totp")
	}
}

// cmdAuthChangePassword implements `aff auth change-password`: the ordinary
// (non-elevated) path through AuthServer.ChangePassword, which requires the
// current password AND a fresh TOTP code (internal/rpc/auth.go) before
// accepting a new one. Every secret is read from a prompt, never a flag —
// a flag value lands in `ps` output and the shell history file.
func (a *app) cmdAuthChangePassword(args []string) int {
	fs := a.newFlagSet("aff auth change-password")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(a.Stderr, "aff auth change-password: takes no positional arguments")
		return exitUsage
	}

	// Stated before anything is prompted, not discovered afterward
	// (PLAN.md hard rule: "changing the password revokes every other
	// session"), so the operator can back out before typing a current
	// password into the terminal for nothing.
	fmt.Fprintln(a.Stdout, "Changing your password revokes every OTHER active session (this one stays")
	fmt.Fprintln(a.Stdout, "signed in). Any other device or CLI session will need to log in again.")

	curPassword, err := a.readPassword("Current password: ")
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff auth change-password: reading current password: %v\n", err)
		return exitFail
	}
	totpCode, err := a.readLine("TOTP code: ")
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff auth change-password: reading TOTP code: %v\n", err)
		return exitFail
	}
	newPassword, err := a.readPassword("New password: ")
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff auth change-password: reading new password: %v\n", err)
		return exitFail
	}
	confirmPassword, err := a.readPassword("Confirm new password: ")
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff auth change-password: reading password confirmation: %v\n", err)
		return exitFail
	}
	if newPassword != confirmPassword {
		fmt.Fprintln(a.Stderr, "aff auth change-password: new password and confirmation did not match")
		return exitFail
	}

	ctx := context.Background()
	cb, err := a.client(ctx)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff auth change-password: %v\n", err)
		return exitFail
	}

	if _, err := cb.Auth.ChangePassword(ctx, &affv1.AuthServiceChangePasswordRequest{
		CurrentPassword: curPassword,
		TotpCode:        totpCode,
		NewPassword:     newPassword,
	}); err != nil {
		fmt.Fprintf(a.Stderr, "aff auth change-password: %v\n", err)
		return exitFail
	}

	if a.JSON {
		_ = a.printJSON(map[string]any{"password_changed": true})
	} else {
		fmt.Fprintln(a.Stdout, "Password changed. Every other session was revoked.")
	}
	return exitOK
}

// cmdAuthReenrollTOTP implements `aff auth reenroll-totp`: the ordinary
// (non-elevated) path through AuthServer.ReenrollTOTP, which requires the
// current password (internal/rpc/auth.go's ReenrollTOTPRequest.CurrentPassword
// doc). It replaces the TOTP secret immediately on success — the old
// authenticator entry stops working the instant this call succeeds, so the
// warning is printed before the current password is even requested.
func (a *app) cmdAuthReenrollTOTP(args []string) int {
	fs := a.newFlagSet("aff auth reenroll-totp")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(a.Stderr, "aff auth reenroll-totp: takes no positional arguments")
		return exitUsage
	}

	fmt.Fprintln(a.Stdout, "Re-enrolling replaces your TOTP secret immediately. Your OLD authenticator")
	fmt.Fprintln(a.Stdout, "entry stops working the instant this succeeds — have the new one ready to")
	fmt.Fprintln(a.Stdout, "scan before you continue.")

	curPassword, err := a.readPassword("Current password: ")
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff auth reenroll-totp: reading current password: %v\n", err)
		return exitFail
	}

	ctx := context.Background()
	cb, err := a.client(ctx)
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff auth reenroll-totp: %v\n", err)
		return exitFail
	}

	resp, err := cb.Auth.ReenrollTOTP(ctx, &affv1.AuthServiceReenrollTOTPRequest{
		CurrentPassword: curPassword,
	})
	if err != nil {
		fmt.Fprintf(a.Stderr, "aff auth reenroll-totp: %v\n", err)
		return exitFail
	}

	a.printTOTPSecret(resp.GetProvisioningUri())
	return exitOK
}

// printTOTPSecret is the "shown once, write this down now" framing shared by
// `aff auth reenroll-totp` and `aff recover`'s TOTP branch — the same
// treatment `aff admin init`/`aff admin reset` give a freshly enrolled
// secret (admin_cmd.go's printEnrollment). The provisioning URI is the
// secret's only representation this CLI ever holds; it is never logged and
// never written anywhere but here, exactly once.
func (a *app) printTOTPSecret(provisioningURI string) {
	if a.JSON {
		_ = a.printJSON(map[string]any{"provisioning_uri": provisioningURI})
		return
	}
	fmt.Fprintln(a.Stdout, "\nNew TOTP secret (shown once — scan this into your authenticator app now):")
	fmt.Fprintln(a.Stdout, "  "+provisioningURI)
}
