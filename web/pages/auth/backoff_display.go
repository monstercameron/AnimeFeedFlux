package auth

import "time"

// backoffGrace/backoffCap/backoffFactorSteps mirror internal/rpc/auth.go's
// backoffDelay(failures) EXACTLY (same grace period, same doubling, same
// cap), so this package's client-side estimate tracks the server's real
// curve as closely as a client with no server signal can. See doc.go
// assumption #2 for why the server sends no backoff signal at all and
// this can only ever be an estimate keyed off a local failure count.
const (
	backoffGrace        = 2 // first two failures cost nothing
	backoffMaxDoublings = 6 // caps doubling at 2^6 = 64s before the clamp below
	backoffCap          = 60 * time.Second
)

// EstimateBackoff returns this client's estimate of how long to wait
// before the next submit is likely to succeed, given consecutiveFailures
// on THIS form since its last success. It is a pure re-implementation of
// internal/rpc/auth.go's backoffDelay, not a value read from any RPC
// response — the server never sends one (doc.go assumption #2).
func EstimateBackoff(consecutiveFailures int) time.Duration {
	if consecutiveFailures <= backoffGrace {
		return 0
	}
	n := consecutiveFailures - backoffGrace
	if n > backoffMaxDoublings {
		n = backoffMaxDoublings
	}
	d := time.Second * time.Duration(uint(1)<<uint(n))
	if d > backoffCap {
		d = backoffCap
	}
	return d
}

// RemainingBackoff returns how much of until is still in the future
// relative to now, floored at zero. It is the pure helper both
// LoginForm.Blocked/RemainingBackoff and RecoverForm's equivalents build
// on, factored out once since the two forms track the identical shape of
// state (a BackoffUntil timestamp) independently.
func RemainingBackoff(now, until time.Time) time.Duration {
	if until.IsZero() || !now.Before(until) {
		return 0
	}
	return until.Sub(now)
}

// BackoffSecondsCeil rounds d up to a whole number of seconds for display
// through the i18n plural key (common.backoffNotice's {count} argument
// wants a whole number — a fractional "1.3 seconds" reads worse than
// rounding up to a number that, if anything, slightly overestimates the
// wait rather than under-promising and forcing a second failed submit).
func BackoffSecondsCeil(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	secs := d / time.Second
	if d%time.Second != 0 {
		secs++
	}
	return int(secs)
}
