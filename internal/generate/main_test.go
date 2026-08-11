package generate

import (
	"os"
	"testing"

	"github.com/monstercameron/AnimeFeedFlux/internal/testutil"
)

// TestMain wires the RULE-1 network guard (A0-T07) into this package's test
// binary. Run (runner.go) drives generation entirely through the Provider
// interface, and every test in this package supplies a fake (see
// runner_test.go/soak_test.go) — but the guard is the actual enforcement
// point, not the convention of always remembering to pass one. It fails any
// non-loopback dial closed unless AFF_LIVE_LLM is set, matching
// internal/novelty, internal/sources, and internal/llm.
func TestMain(m *testing.M) {
	restore := testutil.InstallNetworkGuard()
	code := m.Run()
	restore()
	os.Exit(code)
}
