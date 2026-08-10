package bridge

import "time"

// Clock abstracts time so session expiry and the live-socket revalidation
// loop can be driven deterministically in tests instead of by sleeping
// through real wall-clock intervals.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
	// After returns a channel that receives the fire time once d has
	// elapsed. Mirrors time.After's shape so RealClock is a one-line
	// wrapper and fakes can hand back a channel they control by hand.
	After(d time.Duration) <-chan time.Time
}

// RealClock is the production Clock, backed by the time package.
type RealClock struct{}

// Now implements Clock.
func (RealClock) Now() time.Time { return time.Now() }

// After implements Clock.
func (RealClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
