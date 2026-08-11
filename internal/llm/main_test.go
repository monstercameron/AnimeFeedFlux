package llm

import (
	"os"
	"testing"

	"github.com/monstercameron/AnimeFeedFlux/internal/testutil"
)

// TestMain wires the RULE-1 network guard (A0-T07) into this package's test
// binary. internal/llm is where SchemaFluxProvider.Generate (llm.go) builds
// the real OpenAI-backed call — it is the single most likely place a test
// accidentally constructing a live client (instead of the Fake in fake.go)
// would start billing a provider. The guard fails that dial closed unless
// AFF_LIVE_LLM is set, matching internal/novelty and internal/sources.
func TestMain(m *testing.M) {
	restore := testutil.InstallNetworkGuard()
	code := m.Run()
	restore()
	os.Exit(code)
}
