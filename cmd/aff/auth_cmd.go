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
		// PLAN.md §12.1: one generic failure string for every login failure,
		// wrong password or wrong code alike — the CLI must not invent a
		// more specific message from the gRPC status than the server chose
		// to send, or it reopens the oracle the server closed.
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
