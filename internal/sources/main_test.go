package sources

import (
	"os"
	"testing"

	"github.com/monstercameron/AnimeFeedFlux/internal/testutil"
)

// TestMain wires the RULE-1 network guard (A0-T07) into this package's test
// binary. This package is the one that actually performs HTTP fetches in
// production (Fetcher), so it is where an accidentally-live test — someone
// swapping a testutil.StaticClient for http.DefaultClient "just to try it" —
// would first start dialing out. httptest.Server-backed tests, if any are
// added later, are unaffected: they bind loopback, which the guard allows.
func TestMain(m *testing.M) {
	restore := testutil.InstallNetworkGuard()
	code := m.Run()
	restore()
	os.Exit(code)
}
