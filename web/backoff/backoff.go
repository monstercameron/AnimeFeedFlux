// Package backoff computes the reconnect delay sequence for D0-09's
// "DISCONNECTED banner with automatic reconnect and backoff." The
// sequence itself is pure and deterministic given a jitter source, so it
// is host-testable; the js-only web/wsconn package is what actually
// starts timers and redials.
//
// PLAN.md does not specify concrete reconnect numbers for the admin WASM
// client (it does specify a 60s session-revalidation interval and a
// 30s/20s keepalive ping/timeout pair in internal/bridge/server.go, which
// this package's defaults are chosen to stay well under, so a reconnect
// attempt is never mistaken for a second, independent liveness signal).
// DefaultPolicy's numbers are this task's own documented choice, not a
// value taken from the plan.
package backoff

import "time"

// Policy is a full-jitter exponential backoff: each attempt's delay is a
// uniformly random duration between 0 and min(Max, Base*Factor^attempt).
// "Full jitter" (as opposed to additive/equal jitter) is chosen because
// this client has exactly one peer (its own control-plane socket) and no
// thundering-herd concern to balance against faster average reconnects.
type Policy struct {
	// Base is the first attempt's cap (attempt 0).
	Base time.Duration
	// Max caps every attempt's delay, however large attempt grows.
	Max time.Duration
	// Factor is the exponential growth rate. Must be > 1 to actually grow.
	Factor float64
}

// DefaultPolicy reconnects quickly at first (typical of a laptop waking
// or a deploy's brief blip) and settles at a steady 30s ceiling so a
// truly down backend is not hammered.
var DefaultPolicy = Policy{
	Base:   500 * time.Millisecond,
	Max:    30 * time.Second,
	Factor: 2.0,
}

// capForAttempt returns min(Max, Base*Factor^attempt) as a duration, with
// attempt clamped to 0 and overflow guarded by capping the exponent
// growth once it would already exceed Max.
func (p Policy) capForAttempt(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	ceiling := float64(p.Base)
	for i := 0; i < attempt; i++ {
		ceiling *= p.Factor
		if ceiling >= float64(p.Max) {
			return p.Max
		}
	}
	if ceiling > float64(p.Max) {
		return p.Max
	}
	return time.Duration(ceiling)
}

// Delay returns the delay to wait before reconnect attempt number
// attempt (0-indexed: attempt 0 is the delay before the *first* retry,
// after the initial connection attempt already failed once). rand01
// must return a value in [0, 1); inject a fixed function in tests for a
// deterministic result, and math/rand's Float64 in production (that
// wiring lives in web/wsconn, not here, since this package must stay
// import-free of anything the host can't also build).
func (p Policy) Delay(attempt int, rand01 func() float64) time.Duration {
	ceiling := p.capForAttempt(attempt)
	if ceiling <= 0 {
		return 0
	}
	r := rand01()
	if r < 0 {
		r = 0
	}
	if r >= 1 {
		r = 1 - 1e-9
	}
	return time.Duration(r * float64(ceiling))
}

// Sequence computes n delays for attempts 0..n-1 in order, useful for
// tests and for logging/displaying a preview of the backoff curve.
func (p Policy) Sequence(n int, rand01 func() float64) []time.Duration {
	out := make([]time.Duration, n)
	for i := 0; i < n; i++ {
		out[i] = p.Delay(i, rand01)
	}
	return out
}
