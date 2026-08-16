// Runner is the scheduler loop (PLAN.md §13, §14.3, §15): it decides WHEN each
// feed's next generation fires, spreads same-schedule feeds across a jitter
// window, bounds how many generations run at once, and shuts down without
// throwing away a run that is already mid-flight.
//
// The interfaces below are declared here, not imported from internal/store or
// internal/generate, so this file has no dependency on the concurrently-built
// store side. Whatever implements Job/Executor/Gate elsewhere just has to
// satisfy these shapes.

package schedule

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/obs"
)

// Clock abstracts time so tests never sleep on the wall clock (PLAN.md §17).
type Clock interface {
	Now() time.Time
	After(time.Duration) <-chan time.Time
}

// Job is one schedulable feed. Next mirrors Schedule.Next's contract: given an
// instant, return the next firing strictly after it (undjittered — the Runner
// applies jitter on top, see dueTime).
type Job interface {
	FeedID() int64
	Slug() string
	Next(after time.Time) time.Time
}

// Executor actually runs a generation. trigger is always "cron" for runs the
// Runner dispatches — it exists so a shared Execute implementation can tell a
// scheduled run from a manual RunNow apart.
type Executor interface {
	Execute(ctx context.Context, feedID int64, trigger string) error
}

// Gate is the kill switch plus budget check (PLAN.md §13). It is consulted
// before a feed is dispatched, never after — a refusal must cost nothing.
type Gate interface {
	Allowed(feedID int64) (bool, string)
}

// Semaphore is a plain counting semaphore. The Runner owns one instance for
// provider calls and exposes it via ProviderSemaphore so that sampling (built
// elsewhere) can draw from the exact same pool — PLAN.md §13: "sampling draws
// from the same budget as scheduled generation, otherwise the safety net has
// a hole exactly where the interactive action is."
type Semaphore struct {
	ch chan struct{}
}

// NewSemaphore creates a semaphore that allows n concurrent holders.
func NewSemaphore(n int) *Semaphore {
	if n < 1 {
		n = 1
	}
	return &Semaphore{ch: make(chan struct{}, n)}
}

// Acquire blocks until a slot is free or ctx is done.
func (s *Semaphore) Acquire(ctx context.Context) error {
	select {
	case s.ch <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Release frees a previously-acquired slot.
func (s *Semaphore) Release() {
	<-s.ch
}

// Defaults mirror PLAN.md §16's environment table so New's zero-value options
// behave the same as an unconfigured deployment.
const (
	DefaultMaxConcurrent          = 3
	DefaultProviderLimit          = 4
	DefaultJitterWindow           = 10 * time.Minute
	DefaultRunTimeout             = 5 * time.Minute
	DefaultMaxConsecutiveFailures = 5
	DefaultShutdownTimeout        = 30 * time.Second
)

// Option configures a Runner at construction.
type Option func(*Runner)

// WithMaxConcurrent caps how many generations run at once (AFF_MAX_CONCURRENT_RUNS).
func WithMaxConcurrent(n int) Option {
	return func(r *Runner) {
		if n > 0 {
			r.maxConcurrent = n
		}
	}
}

// WithProviderLimit caps concurrent provider calls, shared with sampling
// (AFF_PROVIDER_MAX_INFLIGHT).
func WithProviderLimit(n int) Option {
	return func(r *Runner) {
		if n > 0 {
			r.providerLimit = n
		}
	}
}

// WithJitterWindow sets the spread window for same-schedule feeds (AFF_SCHEDULE_JITTER).
func WithJitterWindow(d time.Duration) Option {
	return func(r *Runner) {
		if d > 0 {
			r.jitterWindow = d
		}
	}
}

// WithRunTimeout sets the hard wall-clock timeout per run.
func WithRunTimeout(d time.Duration) Option {
	return func(r *Runner) {
		if d > 0 {
			r.runTimeout = d
		}
	}
}

// FailureSink persists the failure state this Runner tracks in memory.
//
// It exists because that state was ONLY in memory. The consecutive-failure
// count and the auto-disable both lived in maps on the Runner, so:
//
//   - a restart wiped them, and a persistently broken feed retried forever in
//     bursts of maxConsecutiveFailures, each burst spending whatever it burns
//     before failing;
//   - feeds.consecutive_failures — a column that exists and is carried on the
//     Feed proto — was never written, so it read 0 no matter what, and an
//     operator asking "is this feed failing repeatedly?" was told no;
//   - the admin UI's Enabled toggle reads feeds.enabled, which auto-disable
//     never touched, so a dropped feed still displayed as Enabled while the
//     scheduler silently refused to dispatch it.
//
// Defined as an interface, and nil-tolerant, because this package is
// deliberately storage-agnostic (it knows Job, Gate and Executor, not SQL).
// The composition root supplies the implementation; tests that do not care
// about persistence leave it nil and get exactly the previous behaviour.
type FailureSink interface {
	// RecordFailure increments the stored consecutive-failure count.
	RecordFailure(feedID int64) error
	// ResetFailures clears it after a success.
	ResetFailures(feedID int64) error
	// DisableFeed persists the auto-disable so it survives a restart and so
	// the UI stops claiming the feed is enabled.
	DisableFeed(feedID int64) error
	// LoadFailures seeds the in-memory counts at startup, so a count that
	// reached four before a deploy does not restart at zero.
	LoadFailures() (map[int64]int, error)
}

// WithFailureSink persists consecutive-failure counts and auto-disables. See
// FailureSink for what was wrong without it.
func WithFailureSink(s FailureSink) Option {
	return func(r *Runner) {
		if s != nil {
			r.failureSink = s
		}
	}
}

// WithMaxConsecutiveFailures sets how many consecutive failures auto-disable
// a feed.
func WithMaxConsecutiveFailures(n int) Option {
	return func(r *Runner) {
		if n > 0 {
			r.maxConsecutiveFailures = n
		}
	}
}

// WithShutdownTimeout sets how long in-flight runs get to finish after ctx cancellation.
func WithShutdownTimeout(d time.Duration) Option {
	return func(r *Runner) {
		if d > 0 {
			r.shutdownTimeout = d
		}
	}
}

// WithLogger overrides the default logger.
func WithLogger(l *slog.Logger) Option {
	return func(r *Runner) {
		if l != nil {
			r.logger = l
		}
	}
}

// WithMetrics wires the §15.0a metric set into the Runner (TODOS A7-21). A
// nil m (the default) leaves metric recording a no-op, same as
// generate.Deps.Metrics — no MeterProvider configured is the common case
// (AFF_OTEL_ENABLED=0), not an error.
func WithMetrics(m *obs.Metrics) Option {
	return func(r *Runner) {
		r.metrics = m
	}
}

// Runner is the scheduler loop. Construct with New and start with Run.
type Runner struct {
	clock Clock
	exec  Executor
	gate  Gate

	jobs map[int64]Job

	maxConcurrent          int
	providerLimit          int
	jitterWindow           time.Duration
	runTimeout             time.Duration
	maxConsecutiveFailures int
	shutdownTimeout        time.Duration
	logger                 *slog.Logger
	metrics                *obs.Metrics

	// failureSink persists what failures/disabled hold in memory. nil means
	// "do not persist", which is what every test that predates it gets.
	failureSink FailureSink

	providerSem *Semaphore

	mu        sync.Mutex
	running   map[int64]bool
	disabled  map[int64]bool
	failures  map[int64]int
	runCancel map[int64]context.CancelFunc

	queueDepth int32
	inFlightN  int32

	dispatchCh chan int64
	inFlight   sync.WaitGroup
}

// New builds a Runner over the given jobs. jobs is the fixed feed set for
// this process's lifetime — feeds added or removed require a restart, which
// matches how the rest of the app boots (PLAN.md has no "hot reload the
// scheduler" requirement).
func New(clock Clock, jobs []Job, exec Executor, gate Gate, opts ...Option) *Runner {
	r := &Runner{
		clock:                  clock,
		exec:                   exec,
		gate:                   gate,
		jobs:                   make(map[int64]Job, len(jobs)),
		maxConcurrent:          DefaultMaxConcurrent,
		providerLimit:          DefaultProviderLimit,
		jitterWindow:           DefaultJitterWindow,
		runTimeout:             DefaultRunTimeout,
		maxConsecutiveFailures: DefaultMaxConsecutiveFailures,
		shutdownTimeout:        DefaultShutdownTimeout,
		logger:                 slog.Default(),
		running:                make(map[int64]bool),
		disabled:               make(map[int64]bool),
		failures:               make(map[int64]int),
		runCancel:              make(map[int64]context.CancelFunc),
	}
	for _, j := range jobs {
		r.jobs[j.FeedID()] = j
	}
	for _, opt := range opts {
		opt(r)
	}
	// Buffered to len(jobs): single-flight guarantees at most one queued
	// entry per feed at a time, so this size can never block a send — the
	// "queues rather than fails" requirement without an unbounded channel.
	bufSize := len(jobs)
	if bufSize == 0 {
		bufSize = 1
	}
	r.dispatchCh = make(chan int64, bufSize)
	r.providerSem = NewSemaphore(r.providerLimit)
	return r
}

// ProviderSemaphore returns the pool shared between scheduled runs and
// sampling (PLAN.md §13).
func (r *Runner) ProviderSemaphore() *Semaphore { return r.providerSem }

// QueueDepth reports how many dispatched jobs are waiting for a free worker —
// PLAN.md §14.3 wants this visible rather than a silent delay.
func (r *Runner) QueueDepth() int { return int(atomic.LoadInt32(&r.queueDepth)) }

// InFlight reports how many jobs are currently executing.
func (r *Runner) InFlight() int { return int(atomic.LoadInt32(&r.inFlightN)) }

// Disabled reports whether a feed has been auto-disabled.
func (r *Runner) Disabled(feedID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.disabled[feedID]
}

// Run drives the scheduler until ctx is cancelled. On cancellation it stops
// dispatching new work, waits up to shutdownTimeout for in-flight runs to
// finish on their own, and only then cancels anything still running — a
// partially-charged LLM call is given every chance to complete rather than
// being thrown away (PLAN.md §15).
func (r *Runner) Run(ctx context.Context) error {
	// Before any dispatch: a count that reached four before a restart must
	// not resume at zero, or a broken feed retries forever in bursts.
	r.seedFailures()

	var wg sync.WaitGroup
	for i := 0; i < r.maxConcurrent; i++ {
		wg.Add(1)
		go r.worker(&wg)
	}

	next := make(map[int64]time.Time, len(r.jobs))
	now := r.clock.Now()
	for id, j := range r.jobs {
		next[id] = j.Next(now)
	}

	for {
		_, at, found := earliest(next, r.jitterWindow, r.jobs)
		if !found {
			// No job has a next firing time, so there is nothing to wait for
			// except cancellation. A plain receive, not a one-case select.
			<-ctx.Done()
			close(r.dispatchCh)
			return r.shutdown(&wg)
		}

		now = r.clock.Now()
		wait := at.Sub(now)
		if wait < 0 {
			wait = 0
		}

		select {
		case <-ctx.Done():
			close(r.dispatchCh)
			return r.shutdown(&wg)
		case <-r.clock.After(wait):
			now = r.clock.Now()
			for id, nominal := range next {
				if nominal.IsZero() {
					continue
				}
				if dueTime(nominal, r.jobs[id].Slug(), r.jitterWindow).After(now) {
					continue
				}
				r.maybeDispatch(ctx, id)
				// Advance from the nominal (undjittered) time, not now, so
				// jitter never accumulates drift into the base schedule.
				next[id] = r.jobs[id].Next(nominal)
			}
		}
	}
}

// dueTime is a job's nominal next fire plus its deterministic per-slug jitter.
func dueTime(nominal time.Time, slug string, window time.Duration) time.Time {
	return nominal.Add(Offset(slug, window))
}

// earliest finds the soonest jittered fire time among jobs with a non-zero
// nominal next time.
func earliest(next map[int64]time.Time, window time.Duration, jobs map[int64]Job) (feedID int64, at time.Time, found bool) {
	for id, nominal := range next {
		if nominal.IsZero() {
			continue
		}
		candidate := dueTime(nominal, jobs[id].Slug(), window)
		if !found || candidate.Before(at) {
			feedID, at, found = id, candidate, true
		}
	}
	return
}

// maybeDispatch checks disabled-state, single-flight, and the Gate — in that
// order, all BEFORE anything is queued — then hands the feed to a worker.
func (r *Runner) maybeDispatch(ctx context.Context, feedID int64) {
	r.mu.Lock()
	if r.disabled[feedID] {
		r.mu.Unlock()
		r.logOutcome(feedID, "skipped", "disabled")
		return
	}
	if r.running[feedID] {
		r.mu.Unlock()
		return // single-flight: already queued or executing (PLAN.md §13)
	}
	r.mu.Unlock()

	// Gate is checked BEFORE dispatch — a refusal must cost nothing (PLAN.md
	// §13). A budget/kill-switch refusal is a normal, visible outcome, not a
	// fault (TODOS A7-21): it lands in aff_runs_total{outcome="skipped"},
	// never as an error, and generate.Run is never invoked for it — this is
	// the only site that can count it, since nothing downstream ever sees
	// this feed for this tick.
	if ok, reason := r.gate.Allowed(feedID); !ok {
		r.logOutcome(feedID, "skipped", reason)
		r.recordSkippedRun(feedID)
		return
	}

	r.mu.Lock()
	if r.running[feedID] { // re-check: Gate call above released the lock
		r.mu.Unlock()
		return
	}
	r.running[feedID] = true
	r.mu.Unlock()

	atomic.AddInt32(&r.queueDepth, 1)
	// inFlight.Add happens BEFORE the send, in the single-threaded scheduler
	// loop, for two reasons that pull in opposite directions and are both
	// satisfied only by this order:
	//
	//  1. vs shutdown's inFlight.Wait: every Add is issued by (and
	//     happens-before) the point where this same goroutine stops calling
	//     maybeDispatch and calls shutdown, so a fresh Add can never land
	//     after Wait observed zero. (Adding from the worker would break
	//     this — the reason the Add lives in this loop at all.)
	//  2. vs the worker's Done: the previous version Added AFTER the send
	//     succeeded, and a worker that received the id could run the whole
	//     job and hit runOne's deferred Done() before this goroutine
	//     executed Add(1) — "sync: negative WaitGroup counter", a panic the
	//     race detector's scheduling perturbation reproduced in CI
	//     (2026-08-15). Done must never be reachable before its Add.
	//
	// The ctx.Done arm undoes the Add it never handed to a worker.
	r.inFlight.Add(1)
	select {
	case r.dispatchCh <- feedID:
	case <-ctx.Done():
		r.inFlight.Done()
		atomic.AddInt32(&r.queueDepth, -1)
		r.mu.Lock()
		r.running[feedID] = false
		r.mu.Unlock()
	}
}

// worker pulls dispatched feed IDs and runs them, capping concurrency at
// len(workers) == maxConcurrent. Run() closes dispatchCh once it stops
// calling maybeDispatch (on ctx cancellation), so ranging over it both picks
// up anything already queued — those were already committed to inFlight's
// count before the channel closed and must be drained, not abandoned, or
// Wait() below hangs forever on a count that can never reach zero — and
// exits cleanly once drained, with no separate stop signal needed.
func (r *Runner) worker(wg *sync.WaitGroup) {
	defer wg.Done()
	for feedID := range r.dispatchCh {
		atomic.AddInt32(&r.queueDepth, -1)
		r.runOne(feedID)
	}
}

// runOne executes a single dispatched job: acquires the shared provider
// semaphore, applies the wall-clock run timeout, recovers panics at this
// boundary so one bad feed cannot take the loop down, and records the
// outcome for auto-disable accounting.
func (r *Runner) runOne(feedID int64) {
	atomic.AddInt32(&r.inFlightN, 1)
	defer func() {
		r.inFlight.Done()
		atomic.AddInt32(&r.inFlightN, -1)
		r.mu.Lock()
		r.running[feedID] = false
		r.mu.Unlock()
	}()

	if err := r.providerSem.Acquire(context.Background()); err != nil {
		return
	}
	defer r.providerSem.Release()

	runCtx, cancel := r.withRunTimeout()
	r.mu.Lock()
	r.runCancel[feedID] = cancel
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.runCancel, feedID)
		r.mu.Unlock()
		cancel()
	}()

	err := r.safeExecute(runCtx, feedID)
	r.recordOutcome(feedID, err)
}

// withRunTimeout builds a context cancelled either by explicit Cancel or by
// the Runner's own Clock reaching runTimeout — using the injected clock
// rather than context.WithTimeout (real wall time) so the timeout is
// deterministically testable (PLAN.md §17).
func (r *Runner) withRunTimeout() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	stop := make(chan struct{})
	var once sync.Once
	timer := r.clock.After(r.runTimeout)
	go func() {
		select {
		case <-timer:
			cancel()
		case <-stop:
		}
	}()
	return ctx, func() {
		once.Do(func() { close(stop) })
		cancel()
	}
}

// safeExecute recovers a panicking Executor and turns it into a failed run
// (PLAN.md §14.3: "one misbehaving feed must not take the loop down").
func (r *Runner) safeExecute(ctx context.Context, feedID int64) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("schedule: job for feed %d panicked: %v", feedID, p)
		}
	}()
	return r.exec.Execute(ctx, feedID, "cron")
}

// recordOutcome updates consecutive-failure accounting and auto-disables a
// feed with a loud reason once the threshold is hit, rather than burning
// budget on it every hour forever (PLAN.md §14.3).
func (r *Runner) recordOutcome(feedID int64, err error) {
	slug := ""
	if j, ok := r.jobs[feedID]; ok {
		slug = j.Slug()
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if err == nil {
		r.failures[feedID] = 0
		r.persistLocked(feedID, "reset", func(s FailureSink) error { return s.ResetFailures(feedID) })
		r.logOutcomeLocked(feedID, slug, "success", "")
		return
	}

	r.failures[feedID]++
	r.persistLocked(feedID, "record", func(s FailureSink) error { return s.RecordFailure(feedID) })
	r.logOutcomeLocked(feedID, slug, "failed", err.Error())

	if r.failures[feedID] >= r.maxConsecutiveFailures && !r.disabled[feedID] {
		r.disabled[feedID] = true
		// Persisted, so the feed stays stopped across a restart and the UI's
		// Enabled toggle stops disagreeing with the scheduler about whether
		// this feed runs.
		r.persistLocked(feedID, "disable", func(s FailureSink) error { return s.DisableFeed(feedID) })
		// NOT a run.finished event (this is a feed-state change, not a run
		// outcome — "disabled" is not one of obs.Outcome's four values), so
		// it does not go through obs.RunFinished. FieldFeedSlug is reused
		// since it IS canonical; "consecutive_failures" is NOT in
		// obs.canonicalFields today and A0-L12 (internal/obs/fields_test.go)
		// would flag it if that test's coverage reached this call site —
		// it's a gap for whoever owns internal/obs to resolve (add the
		// field or drop it here), not something this package can fix by
		// inventing a new canonical constant.
		r.logger.Error("feed auto-disabled after consecutive failures",
			obs.FieldFeedSlug, slug,
			"consecutive_failures", r.failures[feedID],
		)
	}
}

// persistLocked runs one FailureSink write, logging a failure rather than
// propagating it. The caller holds r.mu.
//
// A persistence error must not change what the scheduler does: the in-memory
// state is already updated and is what governs dispatch, so failing to write
// it down degrades to the previous behaviour (correct in this process, lost
// on restart) instead of turning a database hiccup into a missed run or a
// feed that never gets disabled.
func (r *Runner) persistLocked(feedID int64, op string, write func(FailureSink) error) {
	if r.failureSink == nil {
		return
	}
	if err := write(r.failureSink); err != nil {
		r.logger.Warn("persisting feed failure state failed; it is correct in memory but will not survive a restart",
			"feed_id", feedID, "op", op, "error", err)
	}
}

// seedFailures loads persisted consecutive-failure counts at startup so a
// count that reached four before a deploy does not resume at zero. Feeds
// already auto-disabled are not re-seeded into r.disabled: the sink persists
// that as feeds.enabled = 0, and loadSchedulableFeedRows filters on it, so a
// disabled feed is simply not in the job set to begin with.
func (r *Runner) seedFailures() {
	if r.failureSink == nil {
		return
	}
	counts, err := r.failureSink.LoadFailures()
	if err != nil {
		r.logger.Warn("loading persisted feed failure counts failed; starting from zero",
			"error", err)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for feedID, n := range counts {
		if _, scheduled := r.jobs[feedID]; scheduled {
			r.failures[feedID] = n
		}
	}
}

// recordSkippedRun emits aff_runs_total{outcome="skipped"} for a feed the
// Gate refused before dispatch (TODOS A7-21). Trigger is always "cron" here
// — the Runner only ever dispatches cron-driven work (Executor's doc
// comment) — and duration is passed as -1 because no run actually started:
// RecordRun only observes aff_run_duration_seconds when duration >= 0
// (obs/metrics.go), so this correctly counts the skip without inventing a
// wall-clock time for work that never ran.
func (r *Runner) recordSkippedRun(feedID int64) {
	if r.metrics == nil {
		return
	}
	slug := ""
	if j, ok := r.jobs[feedID]; ok {
		slug = j.Slug()
	}
	if err := r.metrics.RecordRun(context.Background(), slug, "cron", "skipped", -1); err != nil {
		r.logger.Warn("obs: recording skipped-run metric rejected", "error", err, "feed_slug", slug)
	}
}

func (r *Runner) logOutcome(feedID int64, outcome, reason string) {
	slug := ""
	if j, ok := r.jobs[feedID]; ok {
		slug = j.Slug()
	}
	r.logOutcomeLocked(feedID, slug, outcome, reason)
}

// logOutcomeLocked emits the scheduler-level outcome through obs.RunFinished
// (§15.0) rather than hand-rolling a second "run.finished" line: a
// hand-rolled emitter is exactly how two events drift apart, and this one
// previously logged bare "feed_slug"/"outcome"/"reason" string keys instead
// of the canonical obs.Field* constants, with a FAILED outcome's reason set
// to err.Error() directly — an unbounded, unsanitized string (could carry a
// path, an address, or model-derived text) landing in the one field §15.0
// requires to be a short stable token because it gets grouped on. Routing
// through obs.RunFinished gets the canonical fields AND obs.SanitizeReason's
// enforcement for free, matching internal/generate/runner.go's own call.
//
// outcome here is one of "success" or "failed" (recordOutcome, after
// Execute returns) or "skipped" (logOutcome, a pre-dispatch refusal —
// disabled or gate-denied — for which generate.Run is never invoked, so
// this IS the only run.finished-shaped event for it, not a duplicate of
// one).
func (r *Runner) logOutcomeLocked(feedID int64, slug, outcome, reason string) {
	o := obs.Outcome(outcome)
	if !obs.ValidOutcome(o) {
		o = obs.OutcomeFailed
	}
	obs.RunFinished(context.Background(), r.logger, obs.RunFinishedFields{
		FeedSlug: slug,
		Trigger:  "cron",
		Outcome:  o,
		Reason:   reason,
	})
}

// shutdown waits up to shutdownTimeout for in-flight work to finish on its
// own; only past that deadline does it cancel whatever is still running
// (marking it interrupted rather than pretending it never happened). It
// blocks until every worker has actually returned, so Run never returns
// while a goroutine is still touching shared state.
func (r *Runner) shutdown(wg *sync.WaitGroup) error {
	done := make(chan struct{})
	go func() {
		r.inFlight.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-r.clock.After(r.shutdownTimeout):
		r.mu.Lock()
		cancels := make([]context.CancelFunc, 0, len(r.runCancel))
		for _, c := range r.runCancel {
			cancels = append(cancels, c)
		}
		r.mu.Unlock()
		for _, c := range cancels {
			c()
		}
		<-done
	}

	wg.Wait()
	return nil
}
