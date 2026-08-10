// The in-process nightly scheduler for backup, prune, and the staleness
// watchdog (PLAN.md §15.4). This lives in-process rather than as a systemd
// timer because containerizing the app (§15.1-§15.3) drops the sibling
// timers that used to run these jobs on the droplet — compose has no
// scheduler of its own — and the app already runs a cron loop (§9) that
// understands WAL. Keeping backup/prune here keeps the nightly job in the
// one place that already understands the database it is operating on.
package ops

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Clock abstracts time so the nightly scheduler is testable without sleeping
// on the wall clock — the same shape internal/schedule uses, kept separate
// here so this package has no dependency on that one.
type Clock interface {
	Now() time.Time
	After(time.Duration) <-chan time.Time
}

// realClock is Clock backed by the actual wall clock, used outside tests.
type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// StaleFunc supplies the current feed statuses at notify time — a function
// rather than a static slice, since the scheduler runs once a day and needs
// a fresh read of "who last succeeded when" on every firing, not whatever
// was true at construction time.
type StaleFunc func(ctx context.Context, now time.Time) ([]FeedStatus, error)

// SchedulerConfig configures the nightly job. DBPath/BackupDir/Keep drive
// Backup; Writer/PruneOpts drive Prune; StaleFn/WebhookURL/Grace drive the
// watchdog. Writer, StaleFn, and WebhookURL are each independently optional:
// a zero SchedulerConfig still runs (and logs) a backup, because that is the
// one piece this file exists to guarantee happens even if nothing else is
// wired up yet.
type SchedulerConfig struct {
	Clock  Clock
	Logger *slog.Logger

	// RunAt is the time of day (UTC, offset from midnight) the nightly job
	// fires. Default 03:00 UTC — chosen only as "quiet hours", not load-bearing.
	RunAt time.Duration

	DBPath    string
	BackupDir string
	Keep      int

	// OffsiteDir, if set, is where the nightly job additionally writes an
	// AES-256-GCM encrypted copy of that night's freshly-verified backup
	// (Encrypt, offsite.go), keyed by EncryptionKeyEnv (cli.go). This wires
	// Encrypt against a real backup for the first time (TODOS.md C4-03:
	// "nothing calls it against a real backup"). It does NOT by itself
	// provide off-box TRANSPORT — writing to OffsiteDir only defends against
	// the volume-loss event PLAN.md §15 describes if that directory is
	// itself on separate infrastructure from BackupDir (a mounted network
	// share, a second volume, ...). Getting the encrypted bytes onto
	// genuinely separate infrastructure (upload, rsync, S3, ...) is
	// deployment configuration this package does not own; point OffsiteDir
	// at a mount that is actually off-box. Left empty, that gap is now
	// alerted on nightly rather than merely absent from the logs.
	OffsiteDir string

	Writer    *sql.DB
	PruneOpts PruneOptions

	StaleFn    StaleFunc
	WebhookURL string
	// Grace is shared between the staleness watchdog (Check) and the backup
	// absence check (runBackup) — both are "how many multiples of the
	// expected period before quiet becomes stale", and there is no reason
	// backups should tolerate a different multiple than feeds do.
	Grace float64
}

// DefaultRunAt is 03:00 UTC.
const DefaultRunAt = 3 * time.Hour

// Scheduler drives SchedulerConfig's jobs once every 24h.
type Scheduler struct {
	cfg SchedulerConfig
}

// NewScheduler builds a Scheduler, filling in defaults for anything the
// caller left zero.
func NewScheduler(cfg SchedulerConfig) *Scheduler {
	if cfg.Clock == nil {
		cfg.Clock = realClock{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.RunAt <= 0 {
		cfg.RunAt = DefaultRunAt
	}
	if cfg.Keep <= 0 {
		cfg.Keep = 14 // PLAN.md §15: "Kept 14 days"
	}
	if cfg.Grace <= 0 {
		cfg.Grace = 2.0
	}
	return &Scheduler{cfg: cfg}
}

// Run blocks, firing the nightly job once every 24h until ctx is cancelled.
// Each firing is independent of wall-clock drift: the next target is always
// computed from "now", not by accumulating 24h offsets, so a slow or
// suspended process cannot cause firings to bunch up on resume.
func (s *Scheduler) Run(ctx context.Context) error {
	for {
		now := s.cfg.Clock.Now()
		wait := nextRun(now, s.cfg.RunAt).Sub(now)
		if wait < 0 {
			wait = 0
		}

		select {
		case <-ctx.Done():
			return nil
		case <-s.cfg.Clock.After(wait):
			s.runOnce(ctx)
		}
	}
}

// nextRun returns the next UTC instant, strictly after now, at runAt past
// midnight.
func nextRun(now time.Time, runAt time.Duration) time.Time {
	nowUTC := now.UTC()
	midnight := time.Date(nowUTC.Year(), nowUTC.Month(), nowUTC.Day(), 0, 0, 0, 0, time.UTC)
	candidate := midnight.Add(runAt)
	if !candidate.After(nowUTC) {
		candidate = candidate.AddDate(0, 0, 1)
	}
	return candidate
}

// runOnce runs backup, prune, and the staleness notify in sequence. Each
// stage's failure is logged and does not prevent the next stage from
// running — a failed backup must not also silently skip pruning, and vice
// versa, or one bad night quietly compounds into two problems.
func (s *Scheduler) runOnce(ctx context.Context) {
	c := s.cfg

	if c.DBPath != "" && c.BackupDir != "" {
		s.runBackup(ctx)
	}

	if c.Writer != nil {
		opts := c.PruneOpts
		opts.Now = c.Clock.Now()
		result, err := Prune(ctx, c.Writer, opts)
		if err != nil {
			c.Logger.Error("nightly prune failed", "error", err)
			// ACT, not just read: a failed prune is unbounded disk growth,
			// not a one-off, and on a 2 GB droplet running four services it
			// stays invisible until something else (a write, a checkpoint, an
			// unrelated service) fails for a reason that looks unrelated —
			// exactly the failure mode §15 alerts on for backups, applied
			// here to the other nightly job that protects the disk.
			s.alertOps(ctx, "nightly prune failed", err)
		} else {
			c.Logger.Info("nightly prune complete",
				"samples_deleted", result.SamplesDeleted,
				"embeddings_deleted", result.EmbeddingsDeleted,
				"runs_deleted", result.RunsDeleted)
		}
	}

	if c.StaleFn != nil {
		now := c.Clock.Now()
		feeds, err := c.StaleFn(ctx, now)
		if err != nil {
			c.Logger.Error("nightly staleness check failed to read feed statuses", "error", err)
			// ACT: this is the watchdog's own read failing, not a feed it
			// watches. A log line here is invisible in exactly the way §15.0a
			// argues absence-of-work alerting exists to catch — the watchdog
			// has stopped watching, and nothing else will ever report that,
			// because reporting it IS this component's job. Without this
			// alert a broken StaleFn (bad query, dropped connection, ...)
			// silently disables every feed's staleness protection at once.
			s.alertOps(ctx, "staleness watchdog could not read feed statuses; it is not currently watching any feed", err)
			return
		}
		stale := Check(feeds, now, c.Grace)
		if err := Notify(ctx, c.WebhookURL, stale); err != nil {
			// A failed Slack post must not crash the scheduler loop — see
			// Notify's doc comment. This one stays log-only rather than
			// escalating to a second alert: the watchdog itself is fine (feeds
			// WERE read and checked), only tonight's notification failed, and
			// alerting on "the alert failed to send" through the same
			// possibly-still-broken webhook would at best be redundant and at
			// worst never arrive either. runBackup's alertBackup applies the
			// identical rule for the same reason.
			c.Logger.Warn("nightly staleness notify failed", "error", err, "stale_count", len(stale))
		}
	}
}

// alertOps posts a generic (non-backup) nightly-job failure via the shared
// webhook and logs if the alert itself fails to send — mirroring
// alertBackup's contract exactly: every call site here is already deep
// inside "something already went wrong tonight", so a second failure (the
// alert) must degrade to a log line, never compound into a crash of the
// nightly job (the constraint carried from the backup-alert work).
func (s *Scheduler) alertOps(ctx context.Context, subject string, err error) {
	c := s.cfg
	if aerr := NotifyOpsAlert(ctx, c.WebhookURL, OpsAlert{Subject: subject, Err: err}); aerr != nil {
		c.Logger.Warn("ops alert failed to send", "error", aerr, "subject", subject)
	}
}

// nominalBackupInterval is the period the backup absence check compares
// against: backups are meant to run nightly regardless of what RunAt (a
// time-of-day offset, not a period) happens to be configured to.
const nominalBackupInterval = 24 * time.Hour

// runBackup runs the nightly VACUUM INTO snapshot, ships an encrypted
// off-box copy if configured, and alerts on either a failed run or a run
// whose last SUCCESS has drifted further behind schedule than the grace
// window allows (TODOS.md C4-05) — the same absence-of-work check the
// watchdog (Check) already applies to feeds, applied here to the thing that
// protects the data. A backup that silently stopped succeeding is discovered
// at the exact moment it's needed, which is the one moment it cannot be
// fixed — that is the whole argument for alerting on absence, not just on
// error (PLAN.md §15).
func (s *Scheduler) runBackup(ctx context.Context) {
	c := s.cfg
	now := c.Clock.Now()
	base := backupBaseName(c.DBPath)

	path, backupErr := Backup(ctx, c.DBPath, c.BackupDir, c.Keep)
	if backupErr != nil {
		c.Logger.Error("nightly backup failed", "error", backupErr)
	} else {
		c.Logger.Info("nightly backup complete", "path", path)
		s.shipOffsite(ctx, path)
	}

	lastGood, err := latestBackupTime(c.BackupDir, base)
	if err != nil {
		// Not fatal — it only means the alerts below fall back to "never",
		// which is a safe (if slightly alarming) default, not a crash.
		c.Logger.Warn("could not determine last successful backup time", "error", err)
	}
	age := backupAge(now, lastGood)

	if backupErr != nil {
		s.alertBackup(ctx, BackupAlert{
			Reason:        "nightly backup run failed",
			Err:           backupErr,
			LastSuccessAt: lastGood,
			Age:           age,
			Dir:           c.BackupDir,
		})
	}

	// Absence check, independent of tonight's own outcome: a run that
	// "succeeded" is trivially fresh (it just wrote lastGood), so this only
	// ever fires on its own after a string of prior failures, or when backup
	// was misconfigured out from under a previously-working deployment —
	// exactly the case a bare log line would hide.
	grace := c.Grace
	if grace <= 0 {
		grace = 1
	}
	stale := Check([]FeedStatus{{
		FeedSlug:      "sqlite backup",
		Enabled:       true,
		Interval:      nominalBackupInterval,
		LastSuccessAt: lastGood,
	}}, now, grace)
	if len(stale) > 0 {
		s.alertBackup(ctx, BackupAlert{
			Reason:        "no successful backup within the schedule's grace window",
			LastSuccessAt: lastGood,
			Age:           age,
			Dir:           c.BackupDir,
		})
	}
}

// backupAge is time elapsed since lastGood as measured by now (the
// scheduler's own, possibly-injected, clock) rather than time.Since, which
// would read the real wall clock and make this untestable without sleeping.
// Zero when lastGood is zero — there is no meaningful age for "never".
func backupAge(now, lastGood time.Time) time.Duration {
	if lastGood.IsZero() {
		return 0
	}
	return now.Sub(lastGood)
}

// latestBackupTime finds the newest verified local backup for base in
// outDir and returns the time it was taken, decoded from the filename's own
// timestamp suffix (backupTimestamp, backup.go) rather than the file's
// mtime — mtime can be altered by a copy or a restore, the encoded name
// can't. A zero result with a nil error means "no backup has ever
// succeeded"; Check already treats a zero LastSuccessAt as maximally stale,
// which is the correct read here too.
func latestBackupTime(outDir, base string) (time.Time, error) {
	if outDir == "" || base == "" {
		return time.Time{}, nil
	}
	matches, err := filepath.Glob(filepath.Join(outDir, base+"-*.db"))
	if err != nil {
		return time.Time{}, fmt.Errorf("ops: listing backups in %s: %w", outDir, err)
	}
	if len(matches) == 0 {
		return time.Time{}, nil
	}
	// backupTimestamp is fixed-width, so the newest filename also sorts last
	// lexically — no need to stat or parse every candidate.
	sort.Strings(matches)
	name := filepath.Base(matches[len(matches)-1])
	ts := strings.TrimSuffix(strings.TrimPrefix(name, base+"-"), ".db")
	nanos, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("ops: parsing backup timestamp from %s: %w", name, err)
	}
	return time.Unix(0, nanos).UTC(), nil
}

// alertBackup posts to the shared webhook (alert.go) and logs, rather than
// returning an error to the caller — every call site here is already deep
// inside "something already went wrong with tonight's backup", and a second
// failure (the alert itself) must degrade to a log line, not compound into a
// crash of the nightly job.
func (s *Scheduler) alertBackup(ctx context.Context, a BackupAlert) {
	c := s.cfg
	if err := NotifyBackupAlert(ctx, c.WebhookURL, a); err != nil {
		c.Logger.Warn("backup alert failed to send", "error", err, "reason", a.Reason)
	}
}

// shipOffsite encrypts backupPath (that night's freshly-verified local
// backup) and writes it to OffsiteDir — see SchedulerConfig.OffsiteDir's doc
// comment for exactly what this does and does not provide. Every branch that
// skips the copy alerts rather than only logging, per TODOS.md C4-03: the
// absence of an off-box copy must be visible, not merely inferable from
// "nothing called Encrypt".
func (s *Scheduler) shipOffsite(ctx context.Context, backupPath string) {
	c := s.cfg

	if c.OffsiteDir == "" {
		c.Logger.Warn("off-box backup copy not configured; the only copy of this backup stays on this volume")
		s.alertBackup(ctx, BackupAlert{
			Reason: "off-box backup copy is not configured (PLAN.md §15, TODOS.md C4-03)",
			Dir:    c.BackupDir,
		})
		return
	}

	key, err := LoadEncryptionKey()
	if err != nil {
		c.Logger.Error("off-box backup copy skipped: no usable encryption key", "error", err)
		s.alertBackup(ctx, BackupAlert{
			Reason: "off-box backup copy skipped: encryption key unavailable",
			Err:    err,
			Dir:    c.OffsiteDir,
		})
		return
	}

	if err := os.MkdirAll(c.OffsiteDir, 0o700); err != nil {
		c.Logger.Error("off-box backup copy skipped: could not create OffsiteDir", "error", err)
		s.alertBackup(ctx, BackupAlert{
			Reason: "off-box backup copy skipped: destination unavailable",
			Err:    err,
			Dir:    c.OffsiteDir,
		})
		return
	}

	dest := filepath.Join(c.OffsiteDir, filepath.Base(backupPath)+".enc")
	if err := encryptBackupFile(backupPath, dest, key); err != nil {
		c.Logger.Error("off-box backup copy failed", "error", err)
		s.alertBackup(ctx, BackupAlert{
			Reason: "off-box backup copy failed",
			Err:    err,
			Dir:    c.OffsiteDir,
		})
		return
	}
	c.Logger.Info("off-box backup copy complete", "path", dest)
}

// encryptBackupFile streams src through Encrypt (offsite.go) into dest. A
// partial encrypted file left behind on a failed Encrypt is worse than none
// — it looks like a restorable off-box copy and isn't — so a failure removes
// dest before returning.
func encryptBackupFile(src, dest string, key []byte) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("ops: opening backup for off-box encryption: %w", err)
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("ops: creating off-box copy %s: %w", dest, err)
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
	}()

	if err = Encrypt(in, out, key); err != nil {
		_ = os.Remove(dest)
		return fmt.Errorf("ops: encrypting backup for off-box copy: %w", err)
	}
	return nil
}
