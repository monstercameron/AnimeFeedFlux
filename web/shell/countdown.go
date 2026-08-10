package shell

import (
	"time"

	"github.com/monstercameron/AnimeFeedFlux/web/backoff"
)

// This file is deliberately NOT //go:build js — it holds the pure decision
// logic behind the DISCONNECTED banner's reconnect countdown (D0-09,
// PLAN.md §12.6) so it is host-testable (see countdown_test.go), matching
// this task's brief: "host-testable pure logic only (key resolution,
// plural selection, backoff sequence, state transitions)". The js-only
// banner.go (renderBanner) is the sole caller, wiring this to
// ui.UseState/ui.UseInterval and real wall-clock time — none of THAT
// wiring is testable outside a browser, but the arithmetic it drives is.

// nextReconnectPhase is the countdown's tick step: if deadline has already
// passed, it advances to the next backoff attempt and computes that
// attempt's new deadline from policy; otherwise it returns attempt/deadline
// unchanged. See banner.go's doc comment on renderBanner for why this is a
// locally simulated countdown (seeded from the same policy the real
// connection uses) rather than a read of wsconn's actual internal retry
// timer, which this package is not permitted to add.
func nextReconnectPhase(policy backoff.Policy, attempt int, deadline, now time.Time, rand01 func() float64) (int, time.Time) {
	if !now.Before(deadline) {
		attempt++
		deadline = now.Add(policy.Delay(attempt, rand01))
	}
	return attempt, deadline
}

// reconnectSecondsLeft returns the whole seconds remaining until deadline,
// rounded UP (a countdown that shows "1 second" for 0.2s remaining is more
// honest than one that shows "0 seconds" and then jumps back up on the next
// attempt), clamped to a minimum of 1. The clamp matters for D6-06: this
// feeds a plural {count} argument, and English's plural rules treat 0 as
// "other" (see GoWebComponents/v5/i18n's pluralCategoryForLocale), so an
// unclamped "reconnecting in 0 seconds" flash would be grammatically
// correct but read as a bug at the exact moment a new attempt starts.
func reconnectSecondsLeft(deadline, now time.Time) int {
	left := int(deadline.Sub(now).Seconds() + 0.999)
	if left < 1 {
		return 1
	}
	return left
}
