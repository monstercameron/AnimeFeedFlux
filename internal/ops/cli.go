// The `aff` operator-CLI layer behind PLAN.md §15 and deploy/RUNBOOK.md: the
// logic for `aff backup|restore|verify|encrypt|decrypt|prune|stale|doctor`.
// It lives here, not in cmd/aff, so it is testable without a process — every
// function below takes its dependencies as arguments (a *sql.DB, a clock, an
// io.Writer) rather than reaching for a global, and cmd/aff stays a thin
// flag-parsing shell that calls into it.
//
// A recurring theme across this file: every one of these commands may run
// against the SAME database file the live service is writing to (PLAN.md
// §15.4's containerized deploy has no maintenance window). Read paths use a
// read-only connection so they can never block or be blocked by the writer;
// the one write path (Prune) uses ordinary WAL semantics and busy_timeout,
// the same way the nightly in-process scheduler already does — racing the
// scheduler is an accepted, expected scenario, not a bug to design around.
package ops

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/feedspec"
	"github.com/monstercameron/AnimeFeedFlux/internal/schedule"
	"github.com/monstercameron/AnimeFeedFlux/migrations"
)

// ---------------------------------------------------------------------------
// Connections
// ---------------------------------------------------------------------------

// readWriteDSN mirrors internal/store's writerDSN (WAL, busy_timeout,
// foreign keys, NORMAL sync) rather than importing internal/store, so this
// package keeps its existing zero-dependency-on-store shape (backup.go
// already opens its own connections the same way). Duplicated deliberately:
// it is four lines, and importing internal/store here would pull the whole
// migration/reader/writer lifecycle into a package that must be usable
// against a database internal/store itself may simultaneously have open.
func readWriteDSN(path string) string {
	q := make([]string, 0, 4)
	q = append(q, "_pragma=journal_mode(WAL)")
	q = append(q, "_pragma=busy_timeout(5000)")
	q = append(q, "_pragma=foreign_keys(ON)")
	q = append(q, "_pragma=synchronous(NORMAL)")
	return "file:" + filepath.ToSlash(path) + "?" + strings.Join(q, "&")
}

// OpenReadOnly opens dbPath read-only, safe to hold alongside a live writer
// (PLAN.md §15.4). Used by every command here that only inspects the
// database: Verify, doctor's checks, and building the live feed-status list
// for Stale.
func OpenReadOnly(ctx context.Context, dbPath string) (*sql.DB, error) {
	db, err := sql.Open(driverName, readOnlyDSN(dbPath))
	if err != nil {
		return nil, fmt.Errorf("ops: opening %s read-only: %w", dbPath, err)
	}
	db.SetMaxOpenConns(4)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ops: connecting to %s read-only: %w", dbPath, err)
	}
	return db, nil
}

// OpenReadWrite opens dbPath as a second writer connection alongside
// whatever the live service already holds. Only Prune's real (non-dry-run)
// delete needs this — everything else here is read-only or operates on a
// standalone backup file, never the live path.
func OpenReadWrite(ctx context.Context, dbPath string) (*sql.DB, error) {
	db, err := sql.Open(driverName, readWriteDSN(dbPath))
	if err != nil {
		return nil, fmt.Errorf("ops: opening %s read-write: %w", dbPath, err)
	}
	// SQLite has one writer; the live service already serialises through its
	// own single connection, and this one must do the same rather than
	// opening a pool that fans out into more contention than busy_timeout was
	// sized for.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ops: connecting to %s read-write: %w", dbPath, err)
	}
	return db, nil
}

// ---------------------------------------------------------------------------
// Backup — CLI-facing wrapper (aff backup)
// ---------------------------------------------------------------------------

// DefaultBackupKeep matches the nightly scheduler's default (schedule.go:
// "Kept 14 days", PLAN.md §15) so an ad-hoc `aff backup` and the automatic
// nightly one prune to the same retention unless told otherwise.
const DefaultBackupKeep = 14

// BackupInfo is what `aff backup` prints: the path it wrote and its size, so
// an operator under stress sees both facts on one screen without a second
// command.
type BackupInfo struct {
	Path  string
	Bytes int64
}

// CLIBackup runs Backup and stats the result, so the caller has the size to
// print without a second filesystem round trip of its own.
func CLIBackup(ctx context.Context, dbPath, outDir string, keep int) (BackupInfo, error) {
	path, err := Backup(ctx, dbPath, outDir, keep)
	if err != nil {
		return BackupInfo{}, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return BackupInfo{}, fmt.Errorf("ops: backup %s was written but could not be stat'd: %w", path, err)
	}
	return BackupInfo{Path: path, Bytes: fi.Size()}, nil
}

// ---------------------------------------------------------------------------
// Encryption key handling (aff encrypt / aff decrypt)
// ---------------------------------------------------------------------------

// EncryptionKeyEnv is the ONLY place the off-box backup key may come from.
// Deliberately not a flag: a flag value is visible for the life of the
// process in `ps`/task manager and lands in shell history the moment it is
// typed, which is the exact leak PLAN.md §15 describes ("...the key that
// decrypts them all lived on one DigitalOcean volume..." — a flag would add
// a SECOND leak path on top of that one, not fix it).
const EncryptionKeyEnv = "AFF_BACKUP_ENCRYPTION_KEY"

// LoadEncryptionKey reads and base64-decodes EncryptionKeyEnv, and validates
// its length is exactly KeySize (AES-256) before any caller gets to use it —
// a short or malformed key must fail here with a clear message, not three
// calls deep inside crypto/aes with an opaque one.
func LoadEncryptionKey() ([]byte, error) {
	raw := os.Getenv(EncryptionKeyEnv)
	if raw == "" {
		return nil, fmt.Errorf("ops: %s is not set; the backup encryption key is read ONLY from this "+
			"environment variable, never from a flag — a key on the command line lands in shell history "+
			"and in `ps`/task-manager output for as long as the process runs, which is how a backup key "+
			"leaks (generate one with: openssl rand -base64 32)", EncryptionKeyEnv)
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("ops: %s is not valid base64: %w", EncryptionKeyEnv, err)
	}
	if len(key) != KeySize {
		return nil, fmt.Errorf("ops: %s decodes to %d bytes, want %d (AES-256) — "+
			"generate one with: openssl rand -base64 32", EncryptionKeyEnv, len(key), KeySize)
	}
	return key, nil
}

// ---------------------------------------------------------------------------
// Prune — dry-run counting (aff prune)
// ---------------------------------------------------------------------------

// PruneDryRun reports what Prune, called with the same opts, would delete —
// same counts, same three independent queries — without deleting anything.
// It is a SELECT COUNT(*) mirror of prune.go's three DELETE statements
// rather than a call into Prune inside a rolled-back transaction, because
// the three real deletes are intentionally NOT one transaction (prune.go's
// doc comment: independent of each other) and a dry run must reflect that
// same independence rather than fabricating an atomicity the real run
// doesn't have.
//
// items is never queried here, on purpose, matching Prune: there is no dry
// run for something that is never deleted in the first place.
func PruneDryRun(ctx context.Context, db *sql.DB, opts PruneOptions) (PruneResult, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}

	var result PruneResult

	n, err := countExpiredSamples(ctx, db, now)
	if err != nil {
		return result, err
	}
	result.SamplesDeleted = n

	if opts.EmbeddingWindow > 0 {
		n, err := countEmbeddingsOverWindow(ctx, db, opts.EmbeddingWindow)
		if err != nil {
			return result, err
		}
		result.EmbeddingsDeleted = n
	}

	if opts.RunRetention > 0 {
		n, err := countOldRuns(ctx, db, now.Add(-opts.RunRetention))
		if err != nil {
			return result, err
		}
		result.RunsDeleted = n
	}

	return result, nil
}

func countExpiredSamples(ctx context.Context, db *sql.DB, now time.Time) (int64, error) {
	var n int64
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM samples WHERE expires_at < ?`,
		now.UTC().Format(time.RFC3339Nano)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("ops: counting expired samples: %w", err)
	}
	return n, nil
}

func countEmbeddingsOverWindow(ctx context.Context, db *sql.DB, window int) (int64, error) {
	var n int64
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM item_embeddings
		WHERE item_id IN (
			SELECT item_id FROM (
				SELECT ie.item_id AS item_id,
				       ROW_NUMBER() OVER (
				           PARTITION BY i.feed_id
				           ORDER BY i.published_at DESC
				       ) AS rn
				FROM item_embeddings ie
				JOIN items i ON i.id = ie.item_id
			)
			WHERE rn > ?
		)`, window).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("ops: counting embeddings beyond window %d: %w", window, err)
	}
	return n, nil
}

func countOldRuns(ctx context.Context, db *sql.DB, cutoff time.Time) (int64, error) {
	var n int64
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE started_at < ? AND status != 'failed'`,
		cutoff.UTC().Format(time.RFC3339Nano)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("ops: counting old runs: %w", err)
	}
	return n, nil
}

// ---------------------------------------------------------------------------
// Stale — live feed statuses (aff stale, and doctor's "feeds running on
// schedule" check)
// ---------------------------------------------------------------------------

// LiveFeedStatuses reads every non-deleted feed from db and pairs it with its
// most recent successful (or completed_unconfirmed — it did produce items;
// see watchdog.go's FeedStatus doc comment) run, so the result can be handed
// straight to Check. A feed with an empty or unparseable cron expression
// (aggregates have none — §14.2, "aggregate never generates") gets
// Interval 0, which Check already treats as "nothing to compare against" and
// skips, rather than this function guessing at a default schedule for it.
func LiveFeedStatuses(ctx context.Context, db *sql.DB, now time.Time) ([]FeedStatus, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT f.id, f.slug, f.enabled, f.spec_json, f.timezone,
		       (SELECT MAX(r.started_at) FROM runs r
		        WHERE r.feed_id = f.id AND r.status IN ('success', 'completed_unconfirmed'))
		FROM feeds f
		WHERE f.deleted_at IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("ops: reading feeds: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []FeedStatus
	for rows.Next() {
		var (
			id             int64
			slug           string
			enabled        int
			specJSON       string
			timezone       string
			lastSuccessStr sql.NullString
		)
		if err := rows.Scan(&id, &slug, &enabled, &specJSON, &timezone, &lastSuccessStr); err != nil {
			return nil, fmt.Errorf("ops: scanning feed row: %w", err)
		}

		fs := FeedStatus{FeedSlug: slug, Enabled: enabled != 0}
		fs.Interval = feedInterval(specJSON, timezone, now)
		if lastSuccessStr.Valid {
			if t, err := time.Parse(time.RFC3339Nano, lastSuccessStr.String); err == nil {
				fs.LastSuccessAt = t
			}
		}
		out = append(out, fs)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ops: iterating feeds: %w", err)
	}
	return out, nil
}

// DefaultErrorCountLookback bounds how far back LiveFeedErrorCounts looks for
// a feed that has never succeeded — without a successful run to anchor on,
// "every failed run ever" would grow unbounded for a feed that has been
// failing for months, which is not a useful number on /healthz (it would
// just read as a large, ever-climbing count rather than "how bad is it right
// now"). A week matches the window most operators would actually look back
// over by hand.
const DefaultErrorCountLookback = 7 * 24 * time.Hour

// LiveFeedErrorCounts pairs each non-deleted feed's slug with the count of
// 'failed' runs recorded since its most recent success (or, for a feed that
// has never succeeded, since now-lookback) — the ErrorCount that
// internal/publish/health.go's FeedHealthInput carries for /healthz
// (TODOS.md C4-15). That package cannot compute this itself: the publish
// plane deliberately has no store dependency (§2), so the count has to be
// produced here, alongside LiveFeedStatuses, and handed to whatever builds
// FeedHealthInput.
//
// A feed absent from the returned map had zero matching failed runs — the
// map only ever contains feeds that were read from the feeds table, so
// "absent" cannot mean "unknown feed" the way it might for a smaller,
// caller-constructed set.
func LiveFeedErrorCounts(ctx context.Context, db *sql.DB, now time.Time, lookback time.Duration) (map[string]int, error) {
	if lookback <= 0 {
		lookback = DefaultErrorCountLookback
	}
	cutoff := now.Add(-lookback).UTC().Format(time.RFC3339Nano)

	rows, err := db.QueryContext(ctx, `
		SELECT f.slug, COUNT(r.id)
		FROM feeds f
		LEFT JOIN runs r
			ON r.feed_id = f.id
			AND r.status = 'failed'
			AND r.started_at > COALESCE(
				(SELECT MAX(r2.started_at) FROM runs r2
				 WHERE r2.feed_id = f.id AND r2.status IN ('success', 'completed_unconfirmed')),
				?)
		WHERE f.deleted_at IS NULL
		GROUP BY f.id, f.slug`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("ops: counting feed errors: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[string]int{}
	for rows.Next() {
		var slug string
		var n int
		if err := rows.Scan(&slug, &n); err != nil {
			return nil, fmt.Errorf("ops: scanning feed error count row: %w", err)
		}
		out[slug] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ops: iterating feed error counts: %w", err)
	}
	return out, nil
}

// feedInterval derives a schedule period from a feed's stored recipe: parse
// the cron expression against its timezone, then measure the gap between two
// consecutive future firings rather than trusting any single field to name
// "the interval" directly (a 5-field cron has no such field — "0 12 * * *"
// and "0 12 * * mon" are both valid and have very different periods). Any
// failure here (empty cron, unparseable cron, unknown timezone) yields 0,
// which callers treat as "nothing to compare against", not an error — a
// malformed recipe is Validate's problem (internal/feedspec), not this
// function's.
func feedInterval(specJSON, timezone string, now time.Time) time.Duration {
	spec, err := feedspec.Import([]byte(specJSON))
	if err != nil || spec.Cron == "" {
		return 0
	}
	// Ad-hoc feeds have no cadence to fall behind, and a watch feed's quiet
	// stretches — no successful publish for weeks — are the mode WORKING
	// (§7 revision 2026-08-15). Interval 0 is the established "nothing to
	// compare against" value Check already skips.
	if spec.IsAdhoc() || spec.IsWatch() {
		return 0
	}
	tz := spec.Timezone
	if tz == "" {
		tz = timezone
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	sched, err := schedule.Parse(spec.Cron, loc)
	if err != nil {
		return 0
	}
	first := sched.Next(now)
	second := sched.Next(first)
	interval := second.Sub(first)
	if interval <= 0 {
		return 0
	}
	return interval
}

// ---------------------------------------------------------------------------
// Doctor (aff doctor)
// ---------------------------------------------------------------------------

// DefaultStaleGrace matches the nightly scheduler's default grace factor
// (schedule.go), so `aff doctor`'s "feeds are current" check and the
// watchdog that actually pages someone agree on what "stale" means.
//
// 2.0 is a documented guess, not a measured value: TODOS.md C3-11 (recording
// Slack's actual observed poll interval, docs/slack-proof.md) is still
// unresolved, so there is no real number yet to feed the "max delay before a
// quiet feed looks stale" calculation C3-11 describes. The reasoning behind
// 2.0 specifically: a daily feed's grace window is then two full days, which
// absorbs exactly one missed-or-retried run (a transient provider outage, one
// skipped cron tick) without also absorbing a second consecutive miss — the
// point past which "quiet" stops looking like ordinary jitter and starts
// looking like the generator actually stopped. 1.0 would alert on routine
// single-run jitter (a fast way to get an alert muted); much above ~3.0 hides
// a genuinely-stuck daily feed for the better part of a week. Replace this
// default, not the mechanism, once C3-11 lands a measured number.
const DefaultStaleGrace = 2.0

// StaleGraceEnv is the environment variable that overrides DefaultStaleGrace
// when set to a valid, positive float — see ResolveStaleGrace. A single named
// knob (rather than a hardcoded constant per call site) exists because C3-11
// is unresolved: whoever eventually measures Slack's real poll interval
// should be able to correct the grace factor everywhere it is used — the
// nightly Slack webhook (schedule.go's SchedulerConfig.Grace,
// NewScheduler's zero-value default), `aff doctor` (NewDoctorConfig's
// StaleGrace default), and — once cmd/animefeedflux/wire.go passes this same
// resolved value into internal/publish's Deps.HealthGrace — /healthz too —
// without hunting down every default individually or letting them drift
// apart. See ResolveStaleGrace's doc comment for the current wiring status.
const StaleGraceEnv = "AFF_STALE_GRACE_FACTOR"

// ResolveStaleGrace resolves the effective staleness grace multiplier:
// StaleGraceEnv if set to a value strconv.ParseFloat accepts and that is
// > 0, otherwise DefaultStaleGrace. Both NewScheduler (schedule.go) and
// NewDoctorConfig (below) call this for their zero-value default, so setting
// StaleGraceEnv immediately changes both the nightly Slack alert and `aff
// doctor`'s "feeds running on schedule" check — both real, reachable call
// paths (cmd/animefeedflux/wire.go's runAll, cmd/aff/doctor_cmd.go) that
// neither sets Grace/StaleGrace explicitly today.
//
// /healthz (internal/publish/health.go's DefaultHealthGrace,
// server.go's Deps.HealthGrace) is NOT yet wired to this function — that
// package is intentionally untouched here (see this change's report for
// why) — so until cmd/animefeedflux/wire.go is changed to pass
// ops.ResolveStaleGrace() into Deps.HealthGrace, setting StaleGraceEnv
// changes what "stale" means for the webhook/doctor but leaves /healthz at
// the fixed DefaultHealthGrace (2.0, matching this package's own default at
// the moment, but no longer guaranteed to once the env var is set). Treat
// StaleGraceEnv as not fully supported until that one-line wire.go change
// lands.
func ResolveStaleGrace() float64 {
	if v := strings.TrimSpace(os.Getenv(StaleGraceEnv)); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			return f
		}
	}
	return DefaultStaleGrace
}

// DefaultWALMaxBytes is the threshold above which doctor flags the WAL
// sidecar as unexpectedly large. Not a PLAN.md-pinned number — chosen as
// "clearly bigger than this small-feeds database should ever accumulate
// between checkpoints" so it catches a checkpoint that has stopped running
// (e.g. a wedged writer) without false-alarming on ordinary WAL growth
// between the default 24h prune/backup cycle.
const DefaultWALMaxBytes = 256 << 20 // 256 MiB

// DefaultDiskMinFreeBytes is doctor's disk-space floor: below this, a
// nightly backup or an ordinary write is at real risk of failing outright
// rather than just running low.
const DefaultDiskMinFreeBytes = 512 << 20 // 512 MiB

// DoctorConfig configures one Doctor pass. Now/ProviderKeyEnv/StaleGrace/
// WALMaxBytes/DiskMinFreeBytes all default sensibly via NewDoctorConfig; the
// disk figures are supplied by the caller rather than probed here because
// free-space queries are platform-specific syscalls (statfs vs
// GetDiskFreeSpaceEx) that belong in cmd/aff's per-OS files, not in this
// portable package.
type DoctorConfig struct {
	DBPath string
	Now    time.Time

	ProviderKeyEnv string
	StaleGrace     float64
	WALMaxBytes    int64

	DiskFreeBytes    uint64
	DiskMinFreeBytes uint64
}

// NewDoctorConfig fills in every default a caller left zero, mirroring
// NewScheduler's pattern in schedule.go.
func NewDoctorConfig(dbPath string) DoctorConfig {
	return DoctorConfig{
		DBPath:           dbPath,
		Now:              time.Now(),
		ProviderKeyEnv:   "SCHEMAFLUX_API_KEY", // PLAN.md §16 — never OPENAI_API_KEY
		StaleGrace:       ResolveStaleGrace(),
		WALMaxBytes:      DefaultWALMaxBytes,
		DiskMinFreeBytes: DefaultDiskMinFreeBytes,
	}
}

// DoctorCheck is one named pass/fail line — "one line per fact" (the CLI's
// own requirement), not a paragraph.
type DoctorCheck struct {
	Name   string
	OK     bool
	Detail string
}

// DoctorReport is every check Doctor ran, in the order a 2am operator should
// read them: can the database even be reached, is it structurally sound, is
// its schema current, is it growing normally, is content actually being
// produced, can the provider be called, is there room left on disk.
type DoctorReport struct {
	Checks []DoctorCheck
}

// Healthy reports whether every check passed — the one bit `aff doctor`
// needs for its exit code.
func (r DoctorReport) Healthy() bool {
	for _, c := range r.Checks {
		if !c.OK {
			return false
		}
	}
	return true
}

func (r *DoctorReport) add(name string, ok bool, detail string) {
	r.Checks = append(r.Checks, DoctorCheck{Name: name, OK: ok, Detail: detail})
}

// Doctor runs every check a 2am operator would otherwise run by hand, in
// order, and never stops early on a failure — a failed check must not hide
// the checks after it, since the whole point is one screen with every fact
// on it at once.
func Doctor(ctx context.Context, cfg DoctorConfig) DoctorReport {
	var report DoctorReport
	now := cfg.Now
	if now.IsZero() {
		now = time.Now()
	}

	db, err := OpenReadOnly(ctx, cfg.DBPath)
	if err != nil {
		report.add("database opens", false, err.Error())
		// Every remaining DB-dependent check would just repeat this same
		// failure with less clarity, so they are reported as failed-because-
		// unreachable rather than attempted.
		report.add("integrity check", false, "skipped: database did not open")
		report.add("migrations current", false, "skipped: database did not open")
		report.add("feeds running on schedule", false, "skipped: database did not open")
	} else {
		defer func() { _ = db.Close() }()
		report.add("database opens", true, "")
		doctorIntegrity(ctx, db, &report)
		doctorMigrations(ctx, db, &report)
		doctorStaleness(ctx, db, now, cfg.StaleGrace, &report)
	}

	doctorWAL(cfg.DBPath, cfg.WALMaxBytes, &report)
	doctorProviderKey(cfg.ProviderKeyEnv, &report)
	doctorDiskSpace(cfg.DiskFreeBytes, cfg.DiskMinFreeBytes, &report)

	return report
}

func doctorIntegrity(ctx context.Context, db *sql.DB, report *DoctorReport) {
	var result string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&result); err != nil {
		report.add("integrity check", false, err.Error())
		return
	}
	if !strings.EqualFold(result, "ok") {
		report.add("integrity check", false, result)
		return
	}
	report.add("integrity check", true, "")
}

func doctorMigrations(ctx context.Context, db *sql.DB, report *DoctorReport) {
	applied, err := appliedMigrationVersions(ctx, db)
	if err != nil {
		report.add("migrations current", false, err.Error())
		return
	}
	embedded, err := embeddedMigrationVersions()
	if err != nil {
		report.add("migrations current", false, err.Error())
		return
	}

	var missing []int
	for _, v := range embedded {
		if !applied[v] {
			missing = append(missing, v)
		}
	}
	if len(missing) > 0 {
		report.add("migrations current", false,
			fmt.Sprintf("%d migration(s) not applied: %v (run the server once, or `aff admin init`'s "+
				"open path, to apply them — doctor never migrates a live database itself)", len(missing), missing))
		return
	}
	report.add("migrations current", true, fmt.Sprintf("%d applied", len(applied)))
}

func appliedMigrationVersions(ctx context.Context, db *sql.DB) (map[int]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("reading schema_migrations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	have := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("scanning schema_migrations: %w", err)
		}
		have[v] = true
	}
	return have, rows.Err()
}

// embeddedMigrationVersions parses version numbers out of the embedded
// NNNN_name.sql filenames (migrations.FS), the same NNNN_ prefix convention
// internal/store's Migrate uses, without importing internal/store — this
// package must stay usable independently of it (see readWriteDSN's comment).
func embeddedMigrationVersions() ([]int, error) {
	entries, err := migrations.FS.ReadDir(".")
	if err != nil {
		return nil, fmt.Errorf("reading embedded migrations: %w", err)
	}
	var out []int
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		num, _, found := strings.Cut(strings.TrimSuffix(e.Name(), ".sql"), "_")
		if !found {
			continue
		}
		v, err := strconv.Atoi(num)
		if err != nil {
			continue
		}
		out = append(out, v)
	}
	return out, nil
}

func doctorStaleness(ctx context.Context, db *sql.DB, now time.Time, grace float64, report *DoctorReport) {
	if grace <= 0 {
		grace = DefaultStaleGrace
	}
	feeds, err := LiveFeedStatuses(ctx, db, now)
	if err != nil {
		report.add("feeds running on schedule", false, err.Error())
		return
	}
	stale := Check(feeds, now, grace)
	if len(stale) > 0 {
		slugs := make([]string, len(stale))
		for i, s := range stale {
			slugs[i] = s.FeedSlug
		}
		report.add("feeds running on schedule", false,
			fmt.Sprintf("%d stale: %s", len(stale), strings.Join(slugs, ", ")))
		return
	}
	report.add("feeds running on schedule", true, fmt.Sprintf("%d feed(s) checked", len(feeds)))
}

// doctorWAL stats the -wal sidecar directly (portable, no syscall needed): a
// missing file is reported as healthy (0 bytes) rather than an error, since
// "no WAL yet" is the normal state immediately after a checkpoint.
func doctorWAL(dbPath string, maxBytes int64, report *DoctorReport) {
	if maxBytes <= 0 {
		maxBytes = DefaultWALMaxBytes
	}
	fi, err := os.Stat(dbPath + "-wal")
	if errors.Is(err, os.ErrNotExist) {
		report.add("WAL size", true, "no -wal file (freshly checkpointed)")
		return
	}
	if err != nil {
		report.add("WAL size", false, err.Error())
		return
	}
	if fi.Size() > maxBytes {
		report.add("WAL size", false,
			fmt.Sprintf("%d bytes exceeds %d byte threshold — checkpointing may have stalled", fi.Size(), maxBytes))
		return
	}
	report.add("WAL size", true, fmt.Sprintf("%d bytes", fi.Size()))
}

// doctorProviderKey checks PRESENCE ONLY — the value itself is never read
// into the report, never logged, never printed. A key that is merely set may
// still be wrong or revoked; this check catches only the "we forgot to set
// it at all" failure mode, which is the one doctor can check without making
// a paid provider call (PLAN.md §17's "no network calls by design" applies
// to this CLI's tests, and by extension doctor should not need one either to
// report a useful signal).
func doctorProviderKey(envVar string, report *DoctorReport) {
	if envVar == "" {
		envVar = "SCHEMAFLUX_API_KEY"
	}
	if os.Getenv(envVar) == "" {
		report.add("provider key present", false, envVar+" is not set")
		return
	}
	report.add("provider key present", true, envVar+" is set")
}

// doctorDiskSpace compares caller-supplied free space (probed in cmd/aff via
// a per-OS syscall — see this file's DoctorConfig doc comment) against the
// floor. minFreeBytes of 0 disables the check rather than failing everything
// on a caller that could not probe disk space at all — an unknown quantity
// is not the same claim as "there is definitely not enough".
func doctorDiskSpace(freeBytes, minFreeBytes uint64, report *DoctorReport) {
	if minFreeBytes == 0 {
		report.add("disk space", true, "check skipped (no threshold configured)")
		return
	}
	if freeBytes < minFreeBytes {
		report.add("disk space", false,
			fmt.Sprintf("%d bytes free, want at least %d", freeBytes, minFreeBytes))
		return
	}
	report.add("disk space", true, fmt.Sprintf("%d bytes free", freeBytes))
}
