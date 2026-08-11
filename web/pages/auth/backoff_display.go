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

// keyConnectionUnreachable is a common.* i18n key this package references
// and web/i18n declares (KeyCommonConnectionUnreachable). It resolves to
// real text in every locale; the D6-07 "a missing key renders the key
// itself" fallback this comment once relied on is no longer load-bearing
// here (A5-42).
//
// It exists to fix a real gap: §12.1's "one generic error string" rule
// (D1-02) exists to hide CREDENTIAL causes — wrong password vs wrong TOTP
// vs unknown account — from becoming an oracle. It was never about hiding
// CONNECTIVITY. Telling the admin "the server is unreachable, nothing was
// sent" leaks nothing an attacker could use (it says nothing about any
// account), and a login button that silently does nothing because the
// socket never came up is the worst possible first impression on the page
// most likely to be hit right after a deploy. See login.go/recover.go for
// where AuthErrorKey below is called with shell.IsDisconnected(err).
const keyConnectionUnreachable = "connectionUnreachable"

// AuthErrorKey picks which common.* key a failed auth RPC should render.
// disconnected is shell.IsDisconnected(err) evaluated by the caller — this
// function takes a bool rather than an error so it is host-testable
// without importing web/shell (js-only per its own //go:build tag).
// genericKey is the caller's existing D1-02 generic-failure key
// (afi18n.KeyCommonGenericAuthError), passed in rather than imported here
// so this pure file needn't depend on web/i18n's key constants at all.
func AuthErrorKey(disconnected bool, genericKey string) string {
	if disconnected {
		return keyConnectionUnreachable
	}
	return genericKey
}

// ShouldAnnounceBackoffStarted reports whether a freshly computed backoff
// window is worth an assertive/polite announcement at all. d<=0 means the
// grace period (backoffGrace failures) has not yet been exceeded, so there
// is no countdown to announce.
func ShouldAnnounceBackoffStarted(d time.Duration) bool {
	return d > 0
}

// ShouldAnnounceBackoffCleared reports whether the backoff countdown
// reaching zero is worth announcing. Called from an effect keyed on the
// form's Blocked(now) boolean (ui.UseEffectOf only re-runs the effect when
// that value actually changes, never once per per-second tick — see
// login.go/recover.go's clock re-render), so this only needs to rule out
// announcing "cleared" for a backoff that was never announced as started
// in the first place: wasActive is false on initial mount (blocked starts
// false, nothing ever counted down) and after a cleared backoff has
// already been announced once.
func ShouldAnnounceBackoffCleared(blockedNow, wasActive bool) bool {
	return !blockedNow && wasActive
}
