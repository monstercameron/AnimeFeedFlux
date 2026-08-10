package sectest

import (
	"fmt"
	"sort"
	"testing"
	"time"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/rpc"
)

// SEC-39: unknown-account vs wrong-password Login attempts must be
// statistically indistinguishable in wall-clock time, or an attacker can use
// a timing oracle to enumerate whether an admin account exists at all
// (PLAN.md §4: "always run the KDF ... so timing does not leak existence").
//
// This is a SMOKE TEST for a gross timing leak (e.g. a stray early return
// that skips the KDF entirely), not a rigorous side-channel analysis. Real
// timing side channels can be a few percent and require statistical tools
// this test does not have; the tolerance below is generous on purpose. A
// timing test that flakes in CI gets deleted, which defeats its own purpose
// more thoroughly than a loose tolerance does — the failure mode we actually
// care about (someone re-adds a "return early if no admin row" fast path) is
// an order-of-magnitude effect, not a few-percent one.
func TestTiming_UnknownAccountVsWrongPassword(t *testing.T) {
	const n = 200

	// "Wrong password" server: a real admin exists, every attempt supplies a
	// wrong password.
	wpSrv, _, _ := newTestServer(t)

	// "Unknown account" server: `aff admin init` has never run.
	uaSt := openTestStore(t)
	uaSrv, err := rpc.NewAuthServer(uaSt, testSecretKey)
	if err != nil {
		t.Fatalf("new auth server: %v", err)
	}

	measure := func(srv *rpc.AuthServer, ip string) time.Duration {
		ctx := withPeerIP(t.Context(), ip)
		start := time.Now()
		_, _ = srv.Login(ctx, &affv1.AuthServiceLoginRequest{
			Password: "this password is wrong but long enough",
			TotpCode: "000000",
		})
		return time.Since(start)
	}

	// Interleave the two populations and give each attempt a fresh IP, so
	// neither the OS scheduler nor the per-IP backoff tracker (which would
	// itself introduce a timing difference unrelated to the KDF) skews one
	// side more than the other.
	wpSamples := make([]time.Duration, 0, n)
	uaSamples := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		wpSamples = append(wpSamples, measure(wpSrv, ipFor("wp", i)))
		uaSamples = append(uaSamples, measure(uaSrv, ipFor("ua", i)))
	}

	wpMedian := median(wpSamples)
	uaMedian := median(uaSamples)

	ratio := float64(uaMedian) / float64(wpMedian)
	// A same-order-of-magnitude ratio (0.5x-2x) is the bar: a skipped-KDF
	// fast path is typically 100-1000x faster, not 1.5x.
	if ratio < 0.5 || ratio > 2.0 {
		t.Errorf("timing leak suspected: unknown-account median = %v, wrong-password median = %v (ratio %.3f, want roughly 1.0, tolerance [0.5,2.0])",
			uaMedian, wpMedian, ratio)
	} else {
		t.Logf("unknown-account median = %v, wrong-password median = %v (ratio %.3f)", uaMedian, wpMedian, ratio)
	}
}

func median(d []time.Duration) time.Duration {
	sorted := append([]time.Duration(nil), d...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[len(sorted)/2]
}

// ipFor manufactures a distinct address per (label, i) so each Login attempt
// gets its own backoff-tracker bucket rather than tripping each other's
// exponential backoff mid-measurement.
func ipFor(label string, i int) string {
	return fmt.Sprintf("10.%d.%d.%d", len(label), i/256, i%256)
}
