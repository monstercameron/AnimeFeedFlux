package novelty

import (
	"os"
	"testing"

	"github.com/monstercameron/AnimeFeedFlux/internal/testutil"
)

// TestMain wires the RULE-1 network guard (A0-T07) into this package's test
// binary. novelty is the one deliberate exception documented in embed.go
// that calls the OpenAI embeddings endpoint directly rather than through
// SchemaFlux — which makes it the package where a test accidentally
// constructing a real OpenAIEmbedder (instead of FakeEmbedder) would be most
// likely to start billing a provider. The guard fails that dial closed
// unless AFF_LIVE_LLM is set.
func TestMain(m *testing.M) {
	restore := testutil.InstallNetworkGuard()
	code := m.Run()
	restore()
	os.Exit(code)
}
