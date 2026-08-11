package rpc

import (
	"context"
	"testing"

	"google.golang.org/grpc/metadata"

	"github.com/monstercameron/AnimeFeedFlux/internal/bridge"
)

// An ANONYMOUS bridge upgrade carries a Session with an empty Token. The
// metadata fallback was gated on `ok && sess.Token != ""`, so an anonymous
// connection fell through to gRPC metadata — a value the browser's own WASM
// supplies — and §4 states "the token never touches JavaScript or WASM" as an
// invariant (A8-37).
func TestAnonymousBridgeSessionDoesNotFallThroughToMetadata(t *testing.T) {
	ctx := bridge.WithSession(context.Background(), bridge.Session{}) // anonymous: no token
	ctx = metadata.NewIncomingContext(ctx, metadata.Pairs(SessionTokenHeader, "attacker-supplied"))

	if tok, ok := sessionTokenFromContext(ctx); ok {
		t.Errorf("an anonymous bridge connection authenticated from metadata (%q)", tok)
	}
}

// An authenticated bridge session still wins, and metadata riding along on
// the same connection cannot override it.
func TestBridgeSessionBeatsMetadata(t *testing.T) {
	ctx := bridge.WithSession(context.Background(), bridge.Session{Token: "real-bridge-token"})
	ctx = metadata.NewIncomingContext(ctx, metadata.Pairs(SessionTokenHeader, "attacker-supplied"))

	tok, ok := sessionTokenFromContext(ctx)
	if !ok || tok != "real-bridge-token" {
		t.Errorf("token = %q ok=%v, want the bridge's own token", tok, ok)
	}
}

// cmd/aff dials the admin port directly and has no bridge session at all —
// metadata is the only source that authenticates it, and must keep working.
func TestMetadataStillAuthenticatesWithoutABridgeSession(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs(SessionTokenHeader, "cli-token"))

	tok, ok := sessionTokenFromContext(ctx)
	if !ok || tok != "cli-token" {
		t.Errorf("token = %q ok=%v, want the CLI's metadata token", tok, ok)
	}
}

// The explicit context key still outranks everything.
func TestExplicitContextTokenWins(t *testing.T) {
	ctx := ContextWithSessionToken(context.Background(), "explicit")
	ctx = bridge.WithSession(ctx, bridge.Session{Token: "bridge"})
	if tok, _ := sessionTokenFromContext(ctx); tok != "explicit" {
		t.Errorf("token = %q, want the explicit context value", tok)
	}
}
