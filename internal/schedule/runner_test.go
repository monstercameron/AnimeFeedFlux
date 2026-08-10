package schedule

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---- fake clock: deterministic, no real sleeping (PLAN.md §17) ----

type fakeWaiter struct {
	at time.Time
	ch chan time.Time
}

type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []fakeWaiter
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
	at := c.now.Add(d)
	if !at.After(c.now) {
		ch <- at
		return ch
	}
	c.waiters = append(c.waiters, fakeWaiter{at: at, ch: ch})
	return ch
}

// Advance moves virtual time forward by d and fires any waiter whose target
// has now been reached, in target order.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	var remaining, fire []fakeWaiter
	for _, w := range c.waiters {
		if !w.at.After(now) {
			fire = append(fire, w)
		} else {
			remaining = append(remaining, w)
		}
	}
	c.waiters = remaining
	c.mu.Unlock()
	for _, w := range fire {
		w.ch <- w.at
	}
}

// ---- test job / gate ----

// stubJob fires every interval starting from the instant it is first asked.
type stubJob struct {
	id       int64
	slug     string
	interval time.Duration
}

func (j stubJob) FeedID() int64                  { return j.id }
func (j stubJob) Slug() string                   { return j.slug }
func (j stubJob) Next(after time.Time) time.Time { return after.Add(j.interval) }

type allowAllGate struct{}

func (allowAllGate) Allowed(int64) (bool, string) { return true, "" }

type denyGate struct{ denied map[int64]bool }

func (g denyGate) Allowed(feedID int64) (bool, string) {
	if g.denied[feedID] {
		return false, "budget_exceeded"
	}
	return true, ""
}

// ---- test executor ----

type call struct {
	feedID int64
	at     time.Time
}

// recordingExecutor logs every call, optionally blocking or panicking on
// specific feed IDs under test control.
type recordingExecutor struct {
	clock Clock

	mu    sync.Mutex
	calls []call

	concurrent  int32
	maxObserved int32

	block   map[int64]chan struct{} // Execute waits on this channel if present
	panicOn map[int64]bool
	failOn  map[int64]bool
	delay   time.Duration // real (wall-clock) delay inside Execute, for concurrency tests
}

func newRecordingExecutor(clock Clock) *recordingExecutor {
	return &recordingExecutor{
		clock:   clock,
		block:   make(map[int64]chan struct{}),
		panicOn: make(map[int64]bool),
		failOn:  make(map[int64]bool),
	}
}

func (e *recordingExecutor) Execute(ctx context.Context, feedID int64, trigger string) error {
	n := atomic.AddInt32(&e.concurrent, 1)
	for {
		old := atomic.LoadInt32(&e.maxObserved)
		if n <= old || atomic.CompareAndSwapInt32(&e.maxObserved, old, n) {
			break
		}
	}
	defer atomic.AddInt32(&e.concurrent, -1)

	e.mu.Lock()
	e.calls = append(e.calls, call{feedID: feedID, at: e.clock.Now()})
	blockCh, blocking := e.block[feedID]
	shouldPanic := e.panicOn[feedID]
	shouldFail := e.failOn[feedID]
	e.mu.Unlock()

	if shouldPanic {
		panic("boom")
	}

	if blocking {
		select {
		case <-blockCh:
		case <-ctx.Done():
			return ctx.Err()
		}
	} else if e.delay > 0 {
		select {
		case <-time.After(e.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	if shouldFail {
		return errors.New("synthetic failure")
	}
	return nil
}

func (e *recordingExecutor) callCount(feedID int64) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, c := range e.calls {
		if c.feedID == feedID {
			n++
		}
	}
	return n
}

func (e *recordingExecutor) totalCalls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.calls)
}

func (e *recordingExecutor) callsFor(feedID int64) []call {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []call
	for _, c := range e.calls {
		if c.feedID == feedID {
			out = append(out, c)
		}
	}
	return out
}

// nudgeUntilCalls repeatedly advances the fake clock by jump and yields to
// the scheduler goroutine until it has recorded n calls. Re-advancing (rather
// than a single Advance) is what makes this immune to the goroutine-start
// race: Run()'s first clock.Now()/clock.After() may not have executed yet
// when the caller is ready to move time forward, so one Advance can land
// before any waiter is registered and do nothing. Advancing is monotonic and
// idempotent from the scheduler's point of view, so nudging again once it has
// started is always safe.
func nudgeUntilCalls(t *testing.T, clock *fakeClock, e *recordingExecutor, n int, jump, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if e.totalCalls() >= n {
			return
		}
		clock.Advance(jump)
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d calls, got %d", n, e.totalCalls())
}

// nudgeUntil is like nudgeUntilCalls but for an arbitrary condition.
func nudgeUntil(t *testing.T, clock *fakeClock, jump, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		clock.Advance(jump)
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for condition")
}

// ---- tests ----

func TestRunner_MaxConcurrentNeverExceeded(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0).UTC())
	var jobs []Job
	for i := int64(1); i <= 6; i++ {
		jobs = append(jobs, stubJob{id: i, slug: "feed", interval: time.Millisecond})
	}
	exec := newRecordingExecutor(clock)
	// A short real delay (rather than a channel that must be manually
	// released) is enough for two workers to overlap while the queued
	// remainder waits its turn, and lets all 6 complete on their own.
	exec.delay = 15 * time.Millisecond

	r := New(clock, jobs, exec, allowAllGate{}, WithMaxConcurrent(2), WithJitterWindow(time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()

	// All six jobs share the same nominal firing instant (now+1ms); jitter
	// spreads them across another 1ms.
	nudgeUntilCalls(t, clock, exec, 6, 5*time.Millisecond, 2*time.Second)

	if got := atomic.LoadInt32(&exec.maxObserved); got > 2 {
		t.Fatalf("observed %d concurrent executions, want <= 2", got)
	}
	if got := atomic.LoadInt32(&exec.maxObserved); got < 2 {
		t.Fatalf("never observed 2 concurrent executions, got max %d (worker pool not overlapping?)", got)
	}

	cancel()
	<-done
}

func TestRunner_JitterSpreadsIdenticalSchedule(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0).UTC())
	const n = 20
	window := 10 * time.Second
	// interval is deliberately >> window so no feed's SECOND occurrence can
	// fall inside the window we're measuring the first occurrence's spread
	// over — otherwise an early-jittered feed re-fires and inflates the
	// total call count before a late-jittered one has fired even once.
	interval := 2 * window
	var jobs []Job
	for i := int64(1); i <= n; i++ {
		jobs = append(jobs, stubJob{id: i, slug: "same-cron-" + string(rune('a'+i)), interval: interval})
	}
	exec := newRecordingExecutor(clock)
	r := New(clock, jobs, exec, allowAllGate{}, WithMaxConcurrent(n), WithJitterWindow(window))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()

	// All 20 jobs share nominal firing at now+interval; jitter spreads their
	// due times across [0,window). Advancing in small steps (rather than one
	// leap past the whole range) is essential here: a single huge Advance
	// would make every job "due" in the same wake, collapsing the very
	// spread this test exists to observe — real operation approaches each
	// due time gradually via clock.After(wait), and the test must too. The
	// condition is "every feed has fired at least once", not a raw call
	// count, precisely because interval > window rules out re-fires muddying
	// that count within this window.
	allFired := func() bool {
		for _, j := range jobs {
			if len(exec.callsFor(j.FeedID())) == 0 {
				return false
			}
		}
		return true
	}
	nudgeUntil(t, clock, window/50, 5*time.Second, allFired)

	var min, max time.Time
	for _, j := range jobs {
		calls := exec.callsFor(j.FeedID())
		if len(calls) == 0 {
			t.Fatalf("feed %d never executed", j.FeedID())
		}
		at := calls[0].at
		if min.IsZero() || at.Before(min) {
			min = at
		}
		if max.IsZero() || at.After(max) {
			max = at
		}
	}

	if !max.After(min) {
		t.Fatalf("expected executions spread across the jitter window, all fired at %v", min)
	}
	if spread := max.Sub(min); spread > window {
		t.Fatalf("spread %v exceeds jitter window %v", spread, window)
	}

	cancel()
	<-done
}

func TestRunner_SingleFlightPerFeed(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0).UTC())
	job := stubJob{id: 1, slug: "busy", interval: time.Millisecond}
	exec := newRecordingExecutor(clock)
	block := make(chan struct{})
	exec.block = map[int64]chan struct{}{1: block}

	r := New(clock, []Job{job}, exec, allowAllGate{}, WithMaxConcurrent(3), WithJitterWindow(time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()

	nudgeUntilCalls(t, clock, exec, 1, 3*time.Millisecond, time.Second)

	// Tick the schedule several more times while the single execution is
	// still blocked; single-flight must prevent a second dispatch.
	for i := 0; i < 5; i++ {
		clock.Advance(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond) // let the scheduler goroutine process ticks

	if got := exec.callCount(1); got != 1 {
		t.Fatalf("expected exactly 1 in-flight execution while busy, got %d", got)
	}

	close(block)
	cancel()
	<-done
}

func TestRunner_PanicRecoveredAndRecordedAsFailed(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0).UTC())
	const interval = time.Millisecond
	panicky := stubJob{id: 1, slug: "panicky", interval: interval}
	healthy := stubJob{id: 2, slug: "healthy", interval: interval}
	exec := newRecordingExecutor(clock)
	exec.panicOn = map[int64]bool{1: true}

	r := New(clock, []Job{panicky, healthy}, exec, allowAllGate{},
		WithMaxConcurrent(2), WithJitterWindow(time.Millisecond), WithMaxConsecutiveFailures(100))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()

	nudgeUntilCalls(t, clock, exec, 2, 3*time.Millisecond, time.Second)

	// The loop must still be alive: advance to the next nominal firing and
	// confirm both feeds run again.
	nudgeUntilCalls(t, clock, exec, 4, 3*time.Millisecond, time.Second)

	cancel()
	<-done

	if exec.callCount(1) < 2 || exec.callCount(2) < 2 {
		t.Fatalf("loop did not survive the panic: calls=%d/%d", exec.callCount(1), exec.callCount(2))
	}
}

func TestRunner_AutoDisableAfterConsecutiveFailures(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0).UTC())
	const interval = time.Millisecond
	job := stubJob{id: 1, slug: "flaky", interval: interval}
	exec := newRecordingExecutor(clock)
	exec.failOn = map[int64]bool{1: true}

	const maxFailures = 3
	r := New(clock, []Job{job}, exec, allowAllGate{},
		WithMaxConcurrent(1), WithJitterWindow(time.Millisecond), WithMaxConsecutiveFailures(maxFailures))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()

	for i := 0; i < maxFailures; i++ {
		nudgeUntilCalls(t, clock, exec, i+1, 2*time.Millisecond, time.Second)
	}

	deadline := time.Now().Add(200 * time.Millisecond)
	for !r.Disabled(1) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !r.Disabled(1) {
		t.Fatalf("expected feed to be auto-disabled after %d consecutive failures", maxFailures)
	}

	callsBefore := exec.callCount(1)
	// Further ticks must not dispatch a disabled feed.
	for i := 0; i < 3; i++ {
		clock.Advance(2 * time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	if got := exec.callCount(1); got != callsBefore {
		t.Fatalf("disabled feed dispatched again: before=%d after=%d", callsBefore, got)
	}

	cancel()
	<-done
}

func TestRunner_GateDenialSkipsWithoutExecute(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0).UTC())
	job := stubJob{id: 1, slug: "denied", interval: time.Millisecond}
	exec := newRecordingExecutor(clock)
	gate := denyGate{denied: map[int64]bool{1: true}}

	r := New(clock, []Job{job}, exec, gate, WithMaxConcurrent(1), WithJitterWindow(time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()

	clock.Advance(3 * time.Millisecond)
	time.Sleep(50 * time.Millisecond)

	if got := exec.totalCalls(); got != 0 {
		t.Fatalf("gate-denied feed must not call Execute, got %d calls", got)
	}

	cancel()
	<-done
}

func TestRunner_ShutdownStopsDispatchAndWaitsForInFlight(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0).UTC())
	running := stubJob{id: 1, slug: "running", interval: time.Millisecond}
	idle := stubJob{id: 2, slug: "idle", interval: time.Millisecond}
	exec := newRecordingExecutor(clock)
	block := make(chan struct{})
	exec.block = map[int64]chan struct{}{1: block}

	r := New(clock, []Job{running, idle}, exec, allowAllGate{},
		WithMaxConcurrent(2), WithJitterWindow(time.Millisecond), WithShutdownTimeout(time.Hour))

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() { r.Run(ctx); close(runDone) }()

	nudgeUntilCalls(t, clock, exec, 2, 3*time.Millisecond, time.Second)

	callsAtCancel := exec.totalCalls()
	cancel()

	// Run() must not return while feed 1's Execute is still blocked.
	select {
	case <-runDone:
		t.Fatalf("Run returned before in-flight work finished")
	case <-time.After(50 * time.Millisecond):
	}

	// No further dispatch should occur after cancellation even though the
	// idle feed's schedule keeps coming due.
	for i := 0; i < 5; i++ {
		clock.Advance(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	if got := exec.totalCalls(); got != callsAtCancel {
		t.Fatalf("dispatch continued after cancellation: before=%d after=%d", callsAtCancel, got)
	}

	close(block)
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatalf("Run did not return after in-flight work completed")
	}
}

func TestRunner_RunExceedingTimeoutIsCancelled(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0).UTC())
	// interval is deliberately >> runTimeout: after the timeout cancels the
	// one run under test, the job must not immediately come due again — a
	// short interval turns the run into a busy loop of re-fire/timeout/fail
	// cycles, which makes "InFlight dropped to 0" a racy signal (it can
	// catch the brief gap between one cancelled run ending and the next
	// starting) rather than the single, stable transition this test wants.
	job := stubJob{id: 1, slug: "slow", interval: time.Hour}
	exec := newRecordingExecutor(clock)
	block := make(chan struct{}) // never closed: only ctx cancellation ends Execute
	exec.block = map[int64]chan struct{}{1: block}

	r := New(clock, []Job{job}, exec, allowAllGate{},
		WithMaxConcurrent(1), WithJitterWindow(time.Millisecond), WithRunTimeout(time.Minute))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()

	nudgeUntilCalls(t, clock, exec, 1, 2*time.Minute, 2*time.Second)

	// The run is now blocked inside Execute. Advancing the clock past the
	// run timeout must cancel its context and let Execute return. The
	// timeout's own timer (registered inside runOne, via r.clock.After) is
	// subject to the same start race as the initial dispatch, so nudge here
	// too rather than a single Advance.
	nudgeUntil(t, clock, 10*time.Second, time.Second, func() bool { return r.InFlight() == 0 })

	cancel()
	<-done
}
