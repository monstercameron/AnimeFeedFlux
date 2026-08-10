package main

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

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
