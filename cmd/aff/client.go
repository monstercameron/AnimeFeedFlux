package main

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// realDial connects to a.Server and wires up the six generated clients. It
// is the only place a real network connection is opened; every command
// reaches it indirectly through a.client(), and tests never call it at all
// (they pre-populate app.clients with fakes — PLAN.md §17.1's "no network
// calls by design" applies to this CLI's test suite exactly as it does to
// the server's).
func (a *app) realDial(ctx context.Context) (*clientBundle, func() error, error) {
	if a.Server == "" {
		return nil, nil, errors.New("aff: no server configured (set --server or AFF_ADMIN_ADDR)")
	}

	// A missing session is fine here — `aff login` itself dials with no
	// token — but a corrupt session file is a real problem worth surfacing
	// rather than silently proceeding unauthenticated.
	var token string
	sess, err := loadSession(a.SessionFile)
	switch {
	case err == nil:
		token = sess.Token
	case errors.Is(err, errSessionNotFound):
		// unauthenticated dial, e.g. `aff login`
	default:
		return nil, nil, err
	}

	conn, err := grpc.NewClient(a.Server,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithPerRPCCredentials(sessionCreds{token: token}),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("aff: dialing %s: %w", a.Server, err)
	}

	cb := &clientBundle{
		Auth:   affv1.NewAuthServiceClient(conn),
		Feed:   affv1.NewFeedServiceClient(conn),
		Item:   affv1.NewItemServiceClient(conn),
		Run:    affv1.NewRunServiceClient(conn),
		Sample: affv1.NewSampleServiceClient(conn),
		System: affv1.NewSystemServiceClient(conn),
	}
	return cb, conn.Close, nil
}

// isUnreachable identifies the transport-level failures a Login RPC can
// surface when the call never reached AuthServer.Login at all: a refused or
// dropped connection, a DNS failure, a hung dial, or (were TLS ever
// configured on this client — it currently is not, see realDial's
// insecure.NewCredentials) a handshake failure. grpc.NewClient (realDial,
// above) is lazy — it does not dial until the first RPC — so these surface
// here, on Login's own error, rather than at client-construction time, and
// grpc-go reports all of them as codes.Unavailable (a refused/reset
// connection, a resolver failure) or codes.DeadlineExceeded (a dial that
// never completed), wrapping the underlying dial/transport error in the
// status message.
//
// Deliberately narrow and allow-list shaped, not the inverse
// (deny-list "anything that isn't Unauthenticated is unreachable"): the
// server's one genuine credential-rejection error, errAuthFailed
// (internal/rpc/auth.go), is codes.Unauthenticated, but callers of Login
// are not guaranteed to hand back a real gRPC status at all (see
// auth_cmd_test.go's fake, which returns a bare errors.New for "the server
// rejected this") — status.Code on a non-status error returns codes.Unknown,
// and treating Unknown as "unreachable" would print a misleading transport
// message for what both real production and this test suite mean as a
// credential rejection. Anything not positively identified as a transport
// failure is treated as a credential rejection, which is the safe default:
// PLAN.md §12.1's generic message is fine to over-apply, never to
// under-apply into an account-existence oracle.
func isUnreachable(err error) bool {
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded:
		return true
	default:
		return false
	}
}
