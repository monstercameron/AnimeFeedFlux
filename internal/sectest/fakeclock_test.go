package sectest

import (
	"sync"
	"time"
)

// fakeClock implements bridge.Clock deterministically: Now() reads back
// whatever Advance moved it to, and After hands back a channel that only
// fires once a test calls Advance past its deadline — never on a real-time
// timer. This is what lets the "session invalidated on an already-open
// socket" tests (SEC-41/42/43) be deterministic instead of sleep-based flake
// bait.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []fakeWaiter
}

type fakeWaiter struct {
	fire time.Time
	ch   chan time.Time
}

func newFakeClock(start time.Time) *fakeClock {
	return &fakeClock{now: start}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	fire := c.now.Add(d)
	if !fire.After(c.now) {
		ch <- fire
		return ch
	}
	c.waiters = append(c.waiters, fakeWaiter{fire: fire, ch: ch})
	return ch
}

// Advance moves the clock forward and fires every waiter whose deadline is
// now in the past.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	remaining := c.waiters[:0]
	var fired []fakeWaiter
	for _, w := range c.waiters {
		if !w.fire.After(now) {
			fired = append(fired, w)
		} else {
			remaining = append(remaining, w)
		}
	}
	c.waiters = remaining
	c.mu.Unlock()
	for _, w := range fired {
		w.ch <- w.fire
	}
}

func (c *fakeClock) waiterCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.waiters)
}
