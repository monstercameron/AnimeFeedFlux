package e2e

import (
	"os"
	"testing"

	"github.com/monstercameron/AnimeFeedFlux/internal/testutil"
)

// TestMain wires the RULE-1 network guard (A0-T07) into this package's test
// binary, matching internal/generate, internal/llm, internal/novelty, and
// internal/sources. Without it, "the default `go test ./...` needs no
// network and no API key" was true of those four packages only by
// convention here, not by anything that would actually fail if it stopped
// being true: this package's own harness (app.go) makes a real http.Get in
// DialBridgeUnauthenticated, and every fixture it targets is loopback
// (httptest servers this suite itself starts), so the guard costs nothing —
// it blocks exactly the non-loopback dial a future regression would add.
func TestMain(m *testing.M) {
	restore := testutil.InstallNetworkGuard()
	code := m.Run()
	restore()
	os.Exit(code)
}
