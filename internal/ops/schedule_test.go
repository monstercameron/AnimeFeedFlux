package ops

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/obs"
)

// fakeClock is a manually-advanced Clock so scheduler tests never sleep on
// the wall clock (PLAN.md §17: "Scheduler tests use an injected clock").
type fakeClock struct {
	mu  sync.Mutex
	now time.Time

	waiters []fakeWaiter
}

type fakeWaiter struct {
	at time.Time
	ch chan time.Time
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
	c.waiters = append(c.waiters, fakeWaiter{at: c.now.Add(d), ch: ch})
	return ch
}

// Advance moves the clock forward and fires any waiter whose deadline has
// now passed.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	var remaining []fakeWaiter
	for _, w := range c.waiters {
		if !w.at.After(now) {
			w.ch <- now
		} else {
			remaining = append(remaining, w)
		}
	}
	c.waiters = remaining
	c.mu.Unlock()
}

func TestNextRunComputesTonightOrTomorrow(t *testing.T) {
	runAt := 3 * time.Hour // 03:00 UTC

	// Before 03:00: next run is today at 03:00.
	now := time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC)
	got := nextRun(now, runAt)
	want := time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("nextRun(%v) = %v, want %v", now, got, want)
	}

	// After 03:00: next run is tomorrow at 03:00.
	now = time.Date(2026, 8, 10, 4, 0, 0, 0, time.UTC)
	got = nextRun(now, runAt)
	want = time.Date(2026, 8, 11, 3, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("nextRun(%v) = %v, want %v", now, got, want)
	}
}

func TestSchedulerFiresOncePerDayAndRunsBackup(t *testing.T) {
	s, dbPath := openLiveStore(t)
	seedItems(t, s, 2)

	outDir := t.TempDir()
	clock := newFakeClock(time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC))

	var backups atomic.Int32
	sched := NewScheduler(SchedulerConfig{
		Clock:     clock,
		Logger:    slog.New(slog.DiscardHandler),
		RunAt:     3 * time.Hour,
		DBPath:    dbPath,
		BackupDir: outDir,
		Keep:      14,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = sched.Run(ctx)
		close(done)
	}()

	// Cross two run-time boundaries in small steps, giving the scheduler
	// goroutine a moment after each step to actually perform the (real,
	// filesystem-touching) backup before advancing further.
	//
	// This real sleep is kept deliberately: the scheduler goroutine's
	// re-registration of its NEXT wait (nextRun's +24h) happens only after
	// runBackup's real VACUUM INTO/fsync completes, synchronously in that
	// same goroutine. A non-sleeping yield (runtime.Gosched() in a spin
	// loop) does not force that blocked disk I/O to progress — it was tried
	// here and, without a real pause, the test goroutine's Advance calls
	// race far ahead of the backup goroutine, so the eventual re-registered
	// wait is computed from an already-much-later "now" and its target
	// lands past this loop's advance budget. SchedulerConfig exposes no
	// "backup done" signal to wait on instead (unlike StaleFn below, which
	// is a test-supplied closure this file can hook), and adding one purely
	// to remove this sleep would be a production seam added as a side
	// effect, not a deliberate trade — so a short real sleep stays, bounded
	// to the minimum that has proven reliable.
	for i := 0; i < 50 && backups.Load() < 2; i++ {
		clock.Advance(time.Hour)
		time.Sleep(5 * time.Millisecond)
		matches, _ := filepath.Glob(filepath.Join(outDir, backupBaseName(dbPath)+"-*.db"))
		backups.Store(int32(len(matches)))
	}

	// Bounded poll for anything still in flight, rather than assuming the
	// last Advance's backup already landed by the time the loop above exited.
	// Same real-I/O reasoning as above applies to the pause between checks.
	// The bound is generous (well beyond a single VACUUM INTO/fsync's normal
	// cost) because this is real disk I/O sharing the machine with whatever
	// else is running, not a fixed compute cost this test controls.
	//
	// This loop ALSO advances the clock, and that is the fix for a real flake
	// (observed 2026-08-11: "backups created = 1, want at least 2", passing on
	// every rerun). The race is the one the comment above describes, taken one
	// step further: the scheduler re-registers its next wait only after
	// runBackup's real disk I/O finishes, and it computes that wait from the
	// clock as it stands AT THAT MOMENT. On a loaded machine the first backup
	// can complete when the fake clock has already been advanced most of the
	// way through the loop's budget, so the next target lands beyond it — and
	// a poll that only waits, without advancing, can never reach a target that
	// is in the fake future. Advancing here means however late the
	// re-registration happens, the clock keeps walking toward it.
	deadline := time.Now().Add(10 * time.Second)
	for backups.Load() < 2 && time.Now().Before(deadline) {
		clock.Advance(time.Hour)
		time.Sleep(10 * time.Millisecond)
		matches, _ := filepath.Glob(filepath.Join(outDir, backupBaseName(dbPath)+"-*.db"))
		backups.Store(int32(len(matches)))
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler did not stop after ctx cancellation")
	}

	if got := backups.Load(); got < 2 {
		t.Fatalf("backups created = %d, want at least 2 after crossing two run times", got)
	}
}

func TestSchedulerStopsOnContextCancellation(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC))
	sched := NewScheduler(SchedulerConfig{
		Clock:  clock,
		Logger: slog.New(slog.DiscardHandler),
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sched.Run(ctx) }()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned error on cancellation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return promptly after ctx cancellation")
	}
}

func TestSchedulerStalePathNeverPanicsOnNotifyFailure(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 8, 10, 2, 59, 0, 0, time.UTC))

	// staleCalled is a deterministic synchronization primitive: the test's
	// own staleFn closure signals it the instant the scheduler has actually
	// read stale statuses (i.e. processed the fired 03:00 tick), so the test
	// can wait for that exact event instead of sleeping and hoping the tick
	// was processed in time. Buffered so the (possibly repeated) call never
	// blocks the scheduler goroutine.
	staleCalled := make(chan struct{}, 1)
	staleFn := func(ctx context.Context, now time.Time) ([]FeedStatus, error) {
		select {
		case staleCalled <- struct{}{}:
		default:
		}
		return []FeedStatus{
			{FeedSlug: "quiet-feed", Enabled: true, Interval: time.Hour}, // never succeeded -> always stale
		}, nil
	}

	sched := NewScheduler(SchedulerConfig{
		Clock:      clock,
		Logger:     slog.New(slog.DiscardHandler),
		StaleFn:    staleFn,
		WebhookURL: "http://127.0.0.1:1/unreachable",
		Grace:      1.0,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("scheduler panicked: %v", r)
			}
			close(done)
		}()
		_ = sched.Run(ctx)
	}()

	// A single big Advance can race the scheduler goroutine's first
	// Clock.Now()/Clock.After() call: this fakeClock.After does not fire
	// immediately for an already-past target (unlike the schedule package's
	// fakeClock), it only fires on a later Advance — so if the goroutine
	// hasn't registered its wait for "today 03:00" yet when a single big
	// jump lands past it, the goroutine instead computes and registers a
	// wait for "tomorrow 03:00", and today's tick is lost entirely. Small
	// steps give the goroutine many chances to register while "today 03:00"
	// is still ahead of the clock; the short real wait between steps blocks
	// on staleCalled so it returns the instant the tick is actually
	// processed rather than after a fixed guessed delay.
	staleFired := false
	for i := 0; i < 40 && !staleFired; i++ {
		clock.Advance(3 * time.Second) // 40 * 3s = 120s, crosses the 2-minute gap to 03:00
		select {
		case <-staleCalled:
			staleFired = true
		case <-time.After(5 * time.Millisecond):
		}
	}
	if !staleFired {
		t.Fatal("staleFn was never invoked after crossing 03:00")
	}
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler did not stop")
	}
}

// captureWebhook returns a test server that appends every posted body to
// *posts, plus the server itself so callers can defer Close.
func captureWebhook(t *testing.T, posts *[]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*posts = append(*posts, string(b))
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRunBackupFailureAlerts(t *testing.T) {
	var posts []string
	srv := captureWebhook(t, &posts)

	clock := newFakeClock(time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC))
	sched := NewScheduler(SchedulerConfig{
		Clock:  clock,
		Logger: slog.New(slog.DiscardHandler),
		// A database file that does not exist: opened read-only, VACUUM INTO
		// cannot succeed against it, so Backup deterministically fails
		// without needing to sabotage a real store mid-run.
		DBPath:     filepath.Join(t.TempDir(), "missing.db"),
		BackupDir:  t.TempDir(),
		WebhookURL: srv.URL,
		Grace:      2.0,
	})

	sched.runBackup(context.Background())

	if len(posts) == 0 {
		t.Fatal("expected a backup-failure alert to be posted, got none")
	}
	found := false
	for _, p := range posts {
		if strings.Contains(p, "nightly backup run failed") {
			found = true
		}
	}
	if !found {
		t.Errorf("no post mentioned the backup run failure: %v", posts)
	}
}

func TestRunBackupSuccessDoesNotAlertOnFailureOrAbsence(t *testing.T) {
	var posts []string
	srv := captureWebhook(t, &posts)

	s, dbPath := openLiveStore(t)
	seedItems(t, s, 1)

	clock := newFakeClock(time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC))
	sched := NewScheduler(SchedulerConfig{
		Clock:      clock,
		Logger:     slog.New(slog.DiscardHandler),
		DBPath:     dbPath,
		BackupDir:  t.TempDir(),
		WebhookURL: srv.URL,
		Grace:      2.0,
		// OffsiteDir left unset deliberately: that path has its own alert,
		// tested separately, and would otherwise pollute this assertion.
	})

	sched.runBackup(context.Background())

	for _, p := range posts {
		if strings.Contains(p, "backup run failed") || strings.Contains(p, "no successful backup within") {
			t.Errorf("a succeeded backup must not alert on failure or absence, got: %s", p)
		}
	}
}

func TestRunBackupAbsenceAlertsWithLastGoodTimestamp(t *testing.T) {
	var posts []string
	srv := captureWebhook(t, &posts)

	outDir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "missing.db") // backup itself will also fail
	base := backupBaseName(dbPath)

	// A verified backup from nine days ago, well past a 2x-daily grace
	// window, so the absence check must fire independently of tonight's
	// (also failing) run.
	oldGood := time.Date(2026, 8, 1, 3, 0, 0, 0, time.UTC)
	oldPath := filepath.Join(outDir, base+"-"+backupTimestamp(oldGood)+".db")
	if err := os.WriteFile(oldPath, []byte("not a real db, only its name matters here"), 0o600); err != nil {
		t.Fatalf("seeding old backup file: %v", err)
	}

	clock := newFakeClock(time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC))
	sched := NewScheduler(SchedulerConfig{
		Clock:      clock,
		Logger:     slog.New(slog.DiscardHandler),
		DBPath:     dbPath,
		BackupDir:  outDir,
		WebhookURL: srv.URL,
		Grace:      2.0,
	})

	sched.runBackup(context.Background())

	joined := strings.Join(posts, "\n")
	if !strings.Contains(joined, "no successful backup within") {
		t.Fatalf("expected an absence alert, got posts: %v", posts)
	}
	if !strings.Contains(joined, oldGood.UTC().Format(time.RFC3339)) {
		t.Errorf("absence alert did not name the last good timestamp %s; got: %s",
			oldGood.UTC().Format(time.RFC3339), joined)
	}
}

func TestRunBackupNeverAbortsOnUnreachableOrErroringWebhook(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("runBackup panicked on a broken webhook: %v", r)
		}
	}()

	s, dbPath := openLiveStore(t)
	seedItems(t, s, 1)

	clock := newFakeClock(time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC))
	sched := NewScheduler(SchedulerConfig{
		Clock:      clock,
		Logger:     slog.New(slog.DiscardHandler),
		DBPath:     dbPath,
		BackupDir:  t.TempDir(),
		WebhookURL: "http://127.0.0.1:1/unreachable",
		Grace:      2.0,
	})

	// Must return normally (no panic, and the local backup itself must still
	// have been taken) even though every alert attempt below will fail to
	// reach the webhook.
	sched.runBackup(context.Background())

	matches, _ := filepath.Glob(filepath.Join(sched.cfg.BackupDir, backupBaseName(dbPath)+"-*.db"))
	if len(matches) == 0 {
		t.Error("expected the local backup to have been written despite the webhook being unreachable")
	}
}

func TestOffsiteCopyWrittenWhenConfigured(t *testing.T) {
	key := make([]byte, KeySize)
	for i := range key {
		key[i] = byte(i)
	}
	t.Setenv(EncryptionKeyEnv, base64.StdEncoding.EncodeToString(key))

	var posts []string
	srv := captureWebhook(t, &posts)

	s, dbPath := openLiveStore(t)
	seedItems(t, s, 1)

	offsiteDir := t.TempDir()
	clock := newFakeClock(time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC))
	sched := NewScheduler(SchedulerConfig{
		Clock:      clock,
		Logger:     slog.New(slog.DiscardHandler),
		DBPath:     dbPath,
		BackupDir:  t.TempDir(),
		OffsiteDir: offsiteDir,
		WebhookURL: srv.URL,
		Grace:      2.0,
	})

	sched.runBackup(context.Background())

	matches, err := filepath.Glob(filepath.Join(offsiteDir, "*.enc"))
	if err != nil {
		t.Fatalf("glob offsite dir: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly one encrypted off-box copy, got %d: %v", len(matches), matches)
	}

	// The off-box copy must actually decrypt back to the local backup's
	// bytes with the same key — this is the wiring TODOS.md C4-03 asks for,
	// not just a file that happens to exist.
	localMatches, _ := filepath.Glob(filepath.Join(sched.cfg.BackupDir, backupBaseName(dbPath)+"-*.db"))
	if len(localMatches) != 1 {
		t.Fatalf("expected exactly one local backup, got %d", len(localMatches))
	}
	want, err := os.ReadFile(localMatches[0])
	if err != nil {
		t.Fatalf("reading local backup: %v", err)
	}

	enc, err := os.Open(matches[0])
	if err != nil {
		t.Fatalf("opening encrypted copy: %v", err)
	}
	defer func() { _ = enc.Close() }()
	var got strings.Builder
	if err := Decrypt(enc, writerFunc(func(p []byte) (int, error) {
		return got.Write(p)
	}), key); err != nil {
		t.Fatalf("decrypting off-box copy: %v", err)
	}
	if got.String() != string(want) {
		t.Error("decrypted off-box copy does not match the local backup's bytes")
	}

	for _, p := range posts {
		if strings.Contains(p, "off-box backup copy is not configured") {
			t.Errorf("off-box copy was configured and succeeded; must not also alert that it is unconfigured: %s", p)
		}
	}
}

// writerFunc adapts a func(p []byte) (int, error) to io.Writer, avoiding a
// dependency on any particular buffer type in the decrypt-and-compare test
// above.
type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// TestRunOncePruneFailureAlerts asserts the first of the two silent-failure
// paths this change closes: a failed nightly prune must reach the operator
// through the shared webhook, not just a log line, because unbounded growth
// on a 2 GB droplet is an ACT-on failure, not a read-about one.
func TestRunOncePruneFailureAlerts(t *testing.T) {
	var posts []string
	srv := captureWebhook(t, &posts)

	db, err := sql.Open(driverName, filepath.Join(t.TempDir(), "prune-test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db early: %v", err)
	}
	// A closed *sql.DB makes every query fail deterministically (database/sql
	// itself returns sql.ErrConnDone regardless of driver), without needing
	// to sabotage a real store mid-run — the same technique
	// TestRunBackupFailureAlerts uses for Backup.

	clock := newFakeClock(time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC))
	sched := NewScheduler(SchedulerConfig{
		Clock:      clock,
		Logger:     slog.New(slog.DiscardHandler),
		Writer:     db,
		PruneOpts:  PruneOptions{RunRetention: 24 * time.Hour, EmbeddingWindow: 500},
		WebhookURL: srv.URL,
	})

	sched.runOnce(context.Background())

	found := false
	for _, p := range posts {
		if strings.Contains(p, "nightly prune failed") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a prune-failure alert, got posts: %v", posts)
	}
}

// TestRunOnceStaleReadFailureAlerts asserts the second, sharper silent-
// failure path: when the watchdog's own read of feed statuses fails, it must
// alert rather than merely log — a log-only failure here means the watchdog
// silently stops watching every feed at once, and nothing else will ever
// report that (reporting it IS this component's job).
func TestRunOnceStaleReadFailureAlerts(t *testing.T) {
	var posts []string
	srv := captureWebhook(t, &posts)

	staleFn := func(ctx context.Context, now time.Time) ([]FeedStatus, error) {
		return nil, errors.New("feeds table unreadable")
	}

	clock := newFakeClock(time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC))
	sched := NewScheduler(SchedulerConfig{
		Clock:      clock,
		Logger:     slog.New(slog.DiscardHandler),
		StaleFn:    staleFn,
		WebhookURL: srv.URL,
		Grace:      2.0,
	})

	sched.runOnce(context.Background())

	found := false
	for _, p := range posts {
		if strings.Contains(p, "could not read feed statuses") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a staleness-read-failure alert, got posts: %v", posts)
	}
}

// TestRunOnceSuccessPathsDoNotAlert is the mirror of the two tests above: a
// clean prune and a clean stale read (with nothing actually stale) must stay
// quiet, so the new alerting is precise rather than firing on every run.
func TestRunOnceSuccessPathsDoNotAlert(t *testing.T) {
	var posts []string
	srv := captureWebhook(t, &posts)

	s, dbPath := openLiveStore(t)
	_ = dbPath

	staleFn := func(ctx context.Context, now time.Time) ([]FeedStatus, error) {
		return nil, nil // no feeds at all: nothing to flag stale
	}

	clock := newFakeClock(time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC))
	sched := NewScheduler(SchedulerConfig{
		Clock:      clock,
		Logger:     slog.New(slog.DiscardHandler),
		Writer:     s.Writer(),
		PruneOpts:  PruneOptions{RunRetention: 24 * time.Hour, EmbeddingWindow: 500},
		StaleFn:    staleFn,
		WebhookURL: srv.URL,
		Grace:      2.0,
	})

	sched.runOnce(context.Background())

	if len(posts) != 0 {
		t.Errorf("a clean prune and stale-read with nothing stale must not alert, got: %v", posts)
	}
}

// TestRunOnceStaleNotifyFailureStaysLogOnly encodes the rule this change
// deliberately does NOT extend: once feed statuses are read successfully and
// Check has run, a failure to POST the resulting staleness report (a broken
// webhook) stays a log line, not a second alert. The watchdog itself worked —
// only tonight's notification failed — and escalating "the alert failed to
// send" through the same possibly-broken webhook would be redundant at best
// and silently drop the escalation too at worst. This test would fail (by
// requiring a post that will never arrive) if that boundary were ever
// accidentally widened.
func TestRunOnceStaleNotifyFailureStaysLogOnly(t *testing.T) {
	var posts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		posts = append(posts, string(b))
		w.WriteHeader(http.StatusInternalServerError) // every post to it fails
	}))
	t.Cleanup(srv.Close)

	staleFn := func(ctx context.Context, now time.Time) ([]FeedStatus, error) {
		return []FeedStatus{
			{FeedSlug: "quiet-feed", Enabled: true, Interval: time.Hour}, // never succeeded -> always stale
		}, nil
	}

	clock := newFakeClock(time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC))
	sched := NewScheduler(SchedulerConfig{
		Clock:      clock,
		Logger:     slog.New(slog.DiscardHandler),
		StaleFn:    staleFn,
		WebhookURL: srv.URL,
		Grace:      1.0,
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		sched.runOnce(context.Background())
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runOnce did not return promptly against a failing webhook")
	}

	// Exactly one post attempt (Notify's own, which failed): a failed
	// notification must not trigger a second, escalated alert attempt
	// through alertOps/NotifyOpsAlert — that is the rule this test encodes,
	// per this file's comment above.
	if len(posts) != 1 {
		t.Errorf("expected exactly one alert attempt (the failed stale report itself), got %d: %v", len(posts), posts)
	}
	if !strings.Contains(posts[0], "quiet-feed") {
		t.Errorf("the one post attempted should be the stale report, got: %s", posts[0])
	}
}

func TestOffsiteCopyMissingAlertsAbsenceVisibly(t *testing.T) {
	var posts []string
	srv := captureWebhook(t, &posts)

	s, dbPath := openLiveStore(t)
	seedItems(t, s, 1)

	clock := newFakeClock(time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC))
	sched := NewScheduler(SchedulerConfig{
		Clock:     clock,
		Logger:    slog.New(slog.DiscardHandler),
		DBPath:    dbPath,
		BackupDir: t.TempDir(),
		// OffsiteDir intentionally left unset.
		WebhookURL: srv.URL,
		Grace:      2.0,
	})

	sched.runBackup(context.Background())

	joined := strings.Join(posts, "\n")
	if !strings.Contains(joined, "off-box backup copy is not configured") {
		t.Fatalf("expected an alert making the missing off-box copy visible, got: %v", posts)
	}
}

// fakeStalenessRecorder implements FeedStalenessRecorder without a
// MeterProvider at all, mirroring fakeClock's role for time — it lets
// runOnce's metrics wiring be tested as plain recorded calls.
type fakeStalenessRecorder struct {
	mu   sync.Mutex
	seen map[string]float64
}

func (f *fakeStalenessRecorder) RecordFeedStaleness(_ context.Context, feedSlug string, ageSeconds float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.seen == nil {
		f.seen = make(map[string]float64)
	}
	f.seen[feedSlug] = ageSeconds
	return nil
}

// TestRunOnceRecordsFeedStalenessMetricForEveryEligibleFeed is TODOS.md
// C4-16: the watchdog's number must be graphable, which means recording
// aff_feed_staleness_seconds for every enabled/interval-bearing feed on each
// nightly firing — not only the ones Check flags as stale. It also checks
// the two documented exclusions: a disabled feed and a feed that has never
// succeeded (no defined age) are both skipped.
func TestRunOnceRecordsFeedStalenessMetricForEveryEligibleFeed(t *testing.T) {
	s, dbPath := openLiveStore(t)
	_ = dbPath

	now := time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC)
	fresh := now.Add(-2 * time.Hour)    // well within threshold, not stale
	overdue := now.Add(-72 * time.Hour) // daily feed, grace 2.0 -> stale

	staleFn := func(ctx context.Context, callNow time.Time) ([]FeedStatus, error) {
		return []FeedStatus{
			{FeedSlug: "fresh-feed", Enabled: true, Interval: 24 * time.Hour, LastSuccessAt: fresh},
			{FeedSlug: "overdue-feed", Enabled: true, Interval: 24 * time.Hour, LastSuccessAt: overdue},
			{FeedSlug: "disabled-feed", Enabled: false, Interval: 24 * time.Hour, LastSuccessAt: overdue},
			{FeedSlug: "never-succeeded-feed", Enabled: true, Interval: 24 * time.Hour},
		}, nil
	}

	rec := &fakeStalenessRecorder{}
	clock := newFakeClock(now)
	sched := NewScheduler(SchedulerConfig{
		Clock:      clock,
		Logger:     slog.New(slog.DiscardHandler),
		Writer:     s.Writer(),
		PruneOpts:  PruneOptions{RunRetention: 24 * time.Hour, EmbeddingWindow: 500},
		StaleFn:    staleFn,
		WebhookURL: "",
		Grace:      2.0,
		Metrics:    rec,
	})

	sched.runOnce(context.Background())

	if len(rec.seen) != 2 {
		t.Fatalf("recorded staleness for %d feeds, want 2 (fresh-feed, overdue-feed): %v", len(rec.seen), rec.seen)
	}
	if got, want := rec.seen["fresh-feed"], (2 * time.Hour).Seconds(); got != want {
		t.Errorf("fresh-feed age = %v, want %v", got, want)
	}
	if got, want := rec.seen["overdue-feed"], (72 * time.Hour).Seconds(); got != want {
		t.Errorf("overdue-feed age = %v, want %v", got, want)
	}
	if _, ok := rec.seen["disabled-feed"]; ok {
		t.Errorf("disabled-feed must not be recorded")
	}
	if _, ok := rec.seen["never-succeeded-feed"]; ok {
		t.Errorf("never-succeeded-feed must not be recorded (no defined age)")
	}
}

// TestRunOnceNilMetricsIsSafe confirms the zero-value SchedulerConfig
// (Metrics left nil, as every deployment without a wired MeterProvider has
// it today — see obs.Setup's production call sites) does not panic or alter
// runOnce's other behavior.
func TestRunOnceNilMetricsIsSafe(t *testing.T) {
	s, dbPath := openLiveStore(t)
	_ = dbPath

	staleFn := func(ctx context.Context, now time.Time) ([]FeedStatus, error) {
		return []FeedStatus{
			{FeedSlug: "some-feed", Enabled: true, Interval: time.Hour, LastSuccessAt: now.Add(-30 * time.Minute)},
		}, nil
	}

	clock := newFakeClock(time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC))
	sched := NewScheduler(SchedulerConfig{
		Clock:     clock,
		Logger:    slog.New(slog.DiscardHandler),
		Writer:    s.Writer(),
		PruneOpts: PruneOptions{RunRetention: 24 * time.Hour, EmbeddingWindow: 500},
		StaleFn:   staleFn,
		Grace:     2.0,
		// Metrics intentionally left nil.
	})

	sched.runOnce(context.Background()) // must not panic
}

// TestAlertBackupSanitizesReasonInLogFallback covers the migration onto
// PLAN.md §15.0's canonical vocabulary: BackupAlert.Reason (alert.go) is
// human-facing Slack prose by design and must stay prose there, but when the
// alert itself fails to send and alertBackup falls back to a log line, that
// same prose becomes a LOG FIELD — which obs.FieldReason constrains to a
// short stable token (A0-L05). A prose reason must come through
// obs.SanitizeReason's fallback token, never verbatim, or every distinct
// sentence becomes its own ungroupable bucket.
func TestAlertBackupSanitizesReasonInLogFallback(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	sched := NewScheduler(SchedulerConfig{
		Clock:  newFakeClock(time.Now()),
		Logger: logger,
		// An invalid URL makes postToSlack fail synchronously with no
		// network call, so the log-fallback branch is exercised deterministically.
		WebhookURL: "://not-a-valid-url",
	})

	proseReason := "no successful backup within the schedule's grace window"
	sched.alertBackup(context.Background(), BackupAlert{Reason: proseReason})

	dec := json.NewDecoder(bytes.NewReader(buf.Bytes()))
	var rec map[string]any
	if !dec.More() {
		t.Fatal("expected a log record from the alert-send failure, got none")
	}
	if err := dec.Decode(&rec); err != nil {
		t.Fatalf("log line is not JSON: %v\n%s", err, buf.String())
	}

	got, _ := rec[obs.FieldReason].(string)
	if got == proseReason {
		t.Fatalf("prose reason %q reached the log field verbatim", proseReason)
	}
	if got != obs.SanitizeReason(proseReason) {
		t.Errorf("reason = %q, want SanitizeReason(%q) = %q", got, proseReason, obs.SanitizeReason(proseReason))
	}
	if !obs.IsCanonicalField(obs.FieldReason) {
		t.Fatal("obs.FieldReason is not itself canonical, which would make this whole test meaningless")
	}
}
