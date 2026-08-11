package ops

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/feedspec"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// seedFeedWithSchedule inserts a feed carrying a real spec_json (cron +
// timezone), which seedFeedForPrune (prune_test.go) deliberately does not —
// LiveFeedStatuses/feedInterval need an actual recipe to parse.
func seedFeedWithSchedule(t *testing.T, s *store.Store, slug, cron, timezone string, enabled bool) int64 {
	t.Helper()
	spec := feedspec.Defaults()
	spec.Slug = slug
	spec.Title = slug
	spec.Kind = model.KindGenerative
	spec.Cron = cron
	spec.Timezone = timezone

	specJSON, err := feedspec.Export(spec)
	if err != nil {
		t.Fatalf("exporting spec: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	enabledInt := 0
	if enabled {
		enabledInt = 1
	}
	res, err := s.Writer().ExecContext(t.Context(), `
		INSERT INTO feeds (slug, title, kind, spec_json, enabled, timezone, created_at, updated_at)
		VALUES (?, ?, 'generative', ?, ?, ?, ?, ?)`,
		slug, slug, string(specJSON), enabledInt, timezone, now, now)
	if err != nil {
		t.Fatalf("seed feed with schedule: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("feed id: %v", err)
	}
	return id
}

// ---------------------------------------------------------------------------
// CLIBackup
// ---------------------------------------------------------------------------

func TestCLIBackupReportsPathAndAccurateSize(t *testing.T) {
	s, dbPath := openLiveStore(t)
	seedItems(t, s, 10)

	outDir := t.TempDir()
	info, err := CLIBackup(t.Context(), dbPath, outDir, DefaultBackupKeep)
	if err != nil {
		t.Fatalf("CLIBackup: %v", err)
	}
	if info.Path == "" {
		t.Fatal("CLIBackup returned an empty path")
	}
	fi, err := os.Stat(info.Path)
	if err != nil {
		t.Fatalf("stat %s: %v", info.Path, err)
	}
	if info.Bytes != fi.Size() {
		t.Errorf("CLIBackup reported %d bytes, actual file is %d bytes", info.Bytes, fi.Size())
	}
	if info.Bytes == 0 {
		t.Error("CLIBackup reported a zero-byte backup for a non-empty database")
	}
}

// ---------------------------------------------------------------------------
// Encryption key loading (aff encrypt / aff decrypt)
// ---------------------------------------------------------------------------

func TestLoadEncryptionKeyRoundTripsThroughEncryptDecrypt(t *testing.T) {
	key := randomKey(t)
	t.Setenv(EncryptionKeyEnv, encodeKeyForEnv(key))

	loaded, err := LoadEncryptionKey()
	if err != nil {
		t.Fatalf("LoadEncryptionKey: %v", err)
	}

	plaintext := []byte("this is a backup file, pretend it is bigger")
	var ciphertext, decrypted bytes.Buffer
	if err := Encrypt(bytes.NewReader(plaintext), &ciphertext, loaded); err != nil {
		t.Fatalf("Encrypt with loaded key: %v", err)
	}
	if err := Decrypt(&ciphertext, &decrypted, loaded); err != nil {
		t.Fatalf("Decrypt with loaded key: %v", err)
	}
	if decrypted.String() != string(plaintext) {
		t.Fatalf("round trip = %q, want %q", decrypted.String(), plaintext)
	}
}

func TestLoadEncryptionKeyMissingIsAnError(t *testing.T) {
	t.Setenv(EncryptionKeyEnv, "")
	if _, err := LoadEncryptionKey(); err == nil {
		t.Fatal("LoadEncryptionKey with the env var unset: want error, got nil")
	}
}

func TestLoadEncryptionKeyWrongLengthIsAnError(t *testing.T) {
	t.Setenv(EncryptionKeyEnv, encodeKeyForEnv([]byte("too-short")))
	if _, err := LoadEncryptionKey(); err == nil {
		t.Fatal("LoadEncryptionKey with a short key: want error, got nil")
	}
}

func TestLoadEncryptionKeyInvalidBase64IsAnError(t *testing.T) {
	t.Setenv(EncryptionKeyEnv, "not valid base64!!!")
	if _, err := LoadEncryptionKey(); err == nil {
		t.Fatal("LoadEncryptionKey with invalid base64: want error, got nil")
	}
}

func encodeKeyForEnv(key []byte) string {
	return base64.StdEncoding.EncodeToString(key)
}

// ---------------------------------------------------------------------------
// PruneDryRun
// ---------------------------------------------------------------------------

func TestPruneDryRunReportsAccurateCountsAndDeletesNothing(t *testing.T) {
	s, _ := openLiveStore(t)
	feedID := seedFeedForPrune(t, s, "dry-run-feed")

	now := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	insertSample(t, s, feedID, now.Add(-time.Hour)) // expired
	insertSample(t, s, feedID, now.Add(time.Hour))  // not expired

	for i := 0; i < 5; i++ {
		id := insertItemForPrune(t, s, feedID, sprintfKey("dry", i), now.Add(-time.Duration(i)*time.Hour))
		insertEmbedding(t, s, id)
	}

	old := now.Add(-200 * 24 * time.Hour)
	insertRunForPrune(t, s, feedID, old, "success")
	insertRunForPrune(t, s, feedID, old, "failed")

	opts := PruneOptions{Now: now, EmbeddingWindow: 3, RunRetention: 180 * 24 * time.Hour}

	before := snapshotCounts(t, s.Writer())

	result, err := PruneDryRun(t.Context(), s.Writer(), opts)
	if err != nil {
		t.Fatalf("PruneDryRun: %v", err)
	}
	if result.SamplesDeleted != 1 {
		t.Errorf("SamplesDeleted = %d, want 1", result.SamplesDeleted)
	}
	if result.EmbeddingsDeleted != 2 {
		t.Errorf("EmbeddingsDeleted = %d, want 2", result.EmbeddingsDeleted)
	}
	if result.RunsDeleted != 1 {
		t.Errorf("RunsDeleted = %d, want 1", result.RunsDeleted)
	}

	after := snapshotCounts(t, s.Writer())
	if after != before {
		t.Fatalf("PruneDryRun changed row counts: before=%+v after=%+v", before, after)
	}

	// The real Prune, given the exact same opts, must delete exactly what the
	// dry run reported — otherwise the dry run is a report about a different
	// operation than the one --dry-run=false would actually perform.
	real, err := Prune(t.Context(), s.Writer(), opts)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if real != result {
		t.Fatalf("real Prune result %+v != PruneDryRun report %+v", real, result)
	}
}

func TestPruneDryRunNeverReportsItemsForDeletion(t *testing.T) {
	s, _ := openLiveStore(t)
	feedID := seedFeedForPrune(t, s, "dry-run-items-feed")
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		id := insertItemForPrune(t, s, feedID, sprintfKey("item", i), base.Add(time.Duration(i)*24*time.Hour))
		insertEmbedding(t, s, id)
	}
	before := countRows(t, s.Writer(), "items")

	// Aggressive dry run: tiny window, short retention — if PruneDryRun ever
	// touched items, this is the configuration that would expose it.
	if _, err := PruneDryRun(t.Context(), s.Writer(), PruneOptions{
		Now:             base.Add(365 * 24 * time.Hour),
		EmbeddingWindow: 1,
		RunRetention:    time.Hour,
	}); err != nil {
		t.Fatalf("PruneDryRun: %v", err)
	}

	after := countRows(t, s.Writer(), "items")
	if after != before {
		t.Fatalf("items count changed from %d to %d — PruneDryRun must never touch items", before, after)
	}
}

type tableCounts struct {
	samples, embeddings, runs, items int
}

func snapshotCounts(t *testing.T, db *sql.DB) tableCounts {
	t.Helper()
	return tableCounts{
		samples:    countRows(t, db, "samples"),
		embeddings: countRows(t, db, "item_embeddings"),
		runs:       countRows(t, db, "runs"),
		items:      countRows(t, db, "items"),
	}
}

func sprintfKey(prefix string, i int) string {
	return fmt.Sprintf("%s-%03d", prefix, i)
}

// ---------------------------------------------------------------------------
// LiveFeedStatuses / Stale integration
// ---------------------------------------------------------------------------

func TestLiveFeedStatusesFlagsAStaleEnabledFeed(t *testing.T) {
	s, dbPath := openLiveStore(t)
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)

	freshID := seedFeedWithSchedule(t, s, "fresh-daily", "0 12 * * *", "UTC", true)
	insertRunForPrune(t, s, freshID, now.Add(-6*time.Hour), "success")

	staleID := seedFeedWithSchedule(t, s, "stale-daily", "0 12 * * *", "UTC", true)
	insertRunForPrune(t, s, staleID, now.Add(-72*time.Hour), "success")

	// Disabled and stale: must never be flagged, matching Check's contract.
	disabledID := seedFeedWithSchedule(t, s, "disabled-daily", "0 12 * * *", "UTC", false)
	insertRunForPrune(t, s, disabledID, now.Add(-720*time.Hour), "success")

	ro := openRO(t, dbPath)
	feeds, err := LiveFeedStatuses(t.Context(), ro, now)
	if err != nil {
		t.Fatalf("LiveFeedStatuses: %v", err)
	}

	byName := map[string]FeedStatus{}
	for _, f := range feeds {
		byName[f.FeedSlug] = f
	}
	if byName["fresh-daily"].Interval <= 0 {
		t.Fatalf("fresh-daily got Interval %v, want a positive daily interval", byName["fresh-daily"].Interval)
	}
	if byName["fresh-daily"].LastSuccessAt.IsZero() {
		t.Fatal("fresh-daily LastSuccessAt was not read back")
	}

	stale := Check(feeds, now, DefaultStaleGrace)
	slugs := map[string]bool{}
	for _, st := range stale {
		slugs[st.FeedSlug] = true
	}
	if !slugs["stale-daily"] {
		t.Error("stale-daily must be flagged stale")
	}
	if slugs["fresh-daily"] {
		t.Error("fresh-daily must not be flagged stale")
	}
	if slugs["disabled-daily"] {
		t.Error("disabled-daily must never be flagged stale, regardless of age")
	}
}

func TestLiveFeedStatusesSkipsFeedWithNoCron(t *testing.T) {
	s, dbPath := openLiveStore(t)
	// seedFeedForPrune (prune_test.go) leaves spec_json at its schema default
	// '{}' — no cron at all, the aggregate-feed shape (§14.2).
	seedFeedForPrune(t, s, "no-schedule-feed")

	ro := openRO(t, dbPath)
	feeds, err := LiveFeedStatuses(t.Context(), ro, time.Now())
	if err != nil {
		t.Fatalf("LiveFeedStatuses: %v", err)
	}
	if len(feeds) != 1 {
		t.Fatalf("got %d feeds, want 1", len(feeds))
	}
	if feeds[0].Interval != 0 {
		t.Errorf("feed with no cron got Interval %v, want 0", feeds[0].Interval)
	}
}

// ---------------------------------------------------------------------------
// ResolveStaleGrace (C4-08: configurable staleness grace factor)
// ---------------------------------------------------------------------------

func TestResolveStaleGraceDefaultsWhenEnvUnset(t *testing.T) {
	t.Setenv(StaleGraceEnv, "")
	if got := ResolveStaleGrace(); got != DefaultStaleGrace {
		t.Errorf("ResolveStaleGrace() = %v, want DefaultStaleGrace %v", got, DefaultStaleGrace)
	}
}

func TestResolveStaleGraceHonorsEnvOverride(t *testing.T) {
	t.Setenv(StaleGraceEnv, "3.5")
	if got := ResolveStaleGrace(); got != 3.5 {
		t.Errorf("ResolveStaleGrace() = %v, want 3.5", got)
	}
}

func TestResolveStaleGraceIgnoresInvalidOrNonPositiveValues(t *testing.T) {
	for _, bad := range []string{"not-a-number", "0", "-1", "  "} {
		t.Run(bad, func(t *testing.T) {
			t.Setenv(StaleGraceEnv, bad)
			if got := ResolveStaleGrace(); got != DefaultStaleGrace {
				t.Errorf("ResolveStaleGrace() with %q = %v, want DefaultStaleGrace %v", bad, got, DefaultStaleGrace)
			}
		})
	}
}

func TestNewSchedulerDefaultGraceRespectsEnvOverride(t *testing.T) {
	t.Setenv(StaleGraceEnv, "4")
	sched := NewScheduler(SchedulerConfig{})
	if sched.cfg.Grace != 4 {
		t.Errorf("NewScheduler default Grace = %v, want 4 (from %s)", sched.cfg.Grace, StaleGraceEnv)
	}
}

func TestNewSchedulerExplicitGraceIsNotOverriddenByEnv(t *testing.T) {
	t.Setenv(StaleGraceEnv, "4")
	sched := NewScheduler(SchedulerConfig{Grace: 1.5})
	if sched.cfg.Grace != 1.5 {
		t.Errorf("NewScheduler explicit Grace = %v, want 1.5 (env must not override an explicit value)", sched.cfg.Grace)
	}
}

func TestNewDoctorConfigDefaultStaleGraceRespectsEnvOverride(t *testing.T) {
	t.Setenv(StaleGraceEnv, "5")
	cfg := NewDoctorConfig("unused.db")
	if cfg.StaleGrace != 5 {
		t.Errorf("NewDoctorConfig().StaleGrace = %v, want 5 (from %s)", cfg.StaleGrace, StaleGraceEnv)
	}
}

// ---------------------------------------------------------------------------
// LiveFeedErrorCounts (C4-15: error counts on /healthz)
// ---------------------------------------------------------------------------

func TestLiveFeedErrorCountsCountsFailuresSinceLastSuccess(t *testing.T) {
	s, dbPath := openLiveStore(t)
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)

	feedID := seedFeedWithSchedule(t, s, "flaky-daily", "0 12 * * *", "UTC", true)
	insertRunForPrune(t, s, feedID, now.Add(-72*time.Hour), "failed")
	insertRunForPrune(t, s, feedID, now.Add(-48*time.Hour), "success") // the anchor
	insertRunForPrune(t, s, feedID, now.Add(-30*time.Hour), "failed")
	insertRunForPrune(t, s, feedID, now.Add(-10*time.Hour), "failed")

	ro := openRO(t, dbPath)
	counts, err := LiveFeedErrorCounts(t.Context(), ro, now, 0)
	if err != nil {
		t.Fatalf("LiveFeedErrorCounts: %v", err)
	}
	// Only the two failures AFTER the success anchor count; the one before it
	// belongs to a prior, already-resolved incident.
	if got := counts["flaky-daily"]; got != 2 {
		t.Errorf("flaky-daily error count = %d, want 2", got)
	}
}

func TestLiveFeedErrorCountsNeverSucceededUsesLookbackWindow(t *testing.T) {
	s, dbPath := openLiveStore(t)
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)

	feedID := seedFeedWithSchedule(t, s, "always-failing", "0 12 * * *", "UTC", true)
	insertRunForPrune(t, s, feedID, now.Add(-20*time.Hour), "failed")
	// Older than the 24h lookback used below — must not be counted.
	insertRunForPrune(t, s, feedID, now.Add(-10*24*time.Hour), "failed")

	ro := openRO(t, dbPath)
	counts, err := LiveFeedErrorCounts(t.Context(), ro, now, 24*time.Hour)
	if err != nil {
		t.Fatalf("LiveFeedErrorCounts: %v", err)
	}
	if got := counts["always-failing"]; got != 1 {
		t.Errorf("always-failing error count = %d, want 1 (only the run within the 24h lookback)", got)
	}
}

func TestLiveFeedErrorCountsFeedWithNoFailuresIsZero(t *testing.T) {
	s, dbPath := openLiveStore(t)
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)

	feedID := seedFeedWithSchedule(t, s, "clean-daily", "0 12 * * *", "UTC", true)
	insertRunForPrune(t, s, feedID, now.Add(-6*time.Hour), "success")

	ro := openRO(t, dbPath)
	counts, err := LiveFeedErrorCounts(t.Context(), ro, now, 0)
	if err != nil {
		t.Fatalf("LiveFeedErrorCounts: %v", err)
	}
	if got := counts["clean-daily"]; got != 0 {
		t.Errorf("clean-daily error count = %d, want 0", got)
	}
}

// ---------------------------------------------------------------------------
// Doctor
// ---------------------------------------------------------------------------

func TestDoctorReportsAHealthyDatabaseAsHealthy(t *testing.T) {
	s, dbPath := openLiveStore(t)
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	feedID := seedFeedWithSchedule(t, s, "healthy-feed", "0 12 * * *", "UTC", true)
	insertRunForPrune(t, s, feedID, now.Add(-time.Hour), "success")

	t.Setenv("AFF_TEST_PROVIDER_KEY", "present")

	cfg := NewDoctorConfig(dbPath)
	cfg.Now = now
	cfg.ProviderKeyEnv = "AFF_TEST_PROVIDER_KEY"
	cfg.DiskFreeBytes = 10 << 30
	cfg.DiskMinFreeBytes = 1 << 20

	report := Doctor(t.Context(), cfg)
	if !report.Healthy() {
		t.Fatalf("Doctor on a healthy database reported unhealthy: %+v", report.Checks)
	}
	for _, c := range report.Checks {
		if !c.OK {
			t.Errorf("check %q failed: %s", c.Name, c.Detail)
		}
	}
}

func TestDoctorReportsACorruptedDatabaseAsUnhealthy(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "corrupt.db")
	if err := os.WriteFile(dbPath, []byte("not a sqlite database at all"), 0o600); err != nil {
		t.Fatalf("write corrupt db: %v", err)
	}

	cfg := NewDoctorConfig(dbPath)
	report := Doctor(t.Context(), cfg)
	if report.Healthy() {
		t.Fatal("Doctor on a corrupted/non-database file reported healthy")
	}
}

func TestDoctorFlagsMissingProviderKey(t *testing.T) {
	s, dbPath := openLiveStore(t)
	_ = seedFeedWithSchedule(t, s, "no-key-feed", "0 12 * * *", "UTC", false)

	t.Setenv("AFF_TEST_PROVIDER_KEY_UNSET", "")

	cfg := NewDoctorConfig(dbPath)
	cfg.ProviderKeyEnv = "AFF_TEST_PROVIDER_KEY_UNSET"
	cfg.DiskFreeBytes = 10 << 30
	cfg.DiskMinFreeBytes = 1 << 20

	report := Doctor(t.Context(), cfg)
	var found bool
	for _, c := range report.Checks {
		if c.Name == "provider key present" {
			found = true
			if c.OK {
				t.Error("provider key present check passed with the env var unset")
			}
			if c.Detail == "" || containsSecretLikeValue(c.Detail) {
				t.Errorf("provider key check detail looks wrong or leaks a value: %q", c.Detail)
			}
		}
	}
	if !found {
		t.Fatal("Doctor report has no 'provider key present' check")
	}
}

func TestDoctorFlagsLowDiskSpace(t *testing.T) {
	_, dbPath := openLiveStore(t)
	t.Setenv("AFF_TEST_PROVIDER_KEY", "present")

	cfg := NewDoctorConfig(dbPath)
	cfg.ProviderKeyEnv = "AFF_TEST_PROVIDER_KEY"
	cfg.DiskFreeBytes = 100
	cfg.DiskMinFreeBytes = 1 << 30

	report := Doctor(t.Context(), cfg)
	if report.Healthy() {
		t.Fatal("Doctor with disk free far below the floor reported healthy")
	}
}

func containsSecretLikeValue(s string) bool {
	return s == "present" // the literal value used as the fake key in these tests
}
