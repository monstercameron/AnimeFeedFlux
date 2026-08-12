package ops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// doctor_checks_test.go covers the individual doctor checks at the level of
// their FAILURE branches. The existing tests in cli_test.go exercise Doctor
// end to end on a healthy database and on a wholly corrupt one; what was left
// uncovered is each check's specific "this particular thing is wrong" path.
//
// That matters more than the coverage number. `aff doctor` is what an
// operator runs at 2am when something is wrong, and every one of these
// branches exists to name a distinct problem. A check that silently reports
// OK when it should not is worse than no check at all: it is a positive
// assurance that the thing you are worried about is fine.

func TestDoctorWALSizeReportsAFreshlyCheckpointedDatabase(t *testing.T) {
	// No -wal file at all is the healthy steady state after a checkpoint, and
	// it must read as OK rather than as a missing-file error.
	dbPath := filepath.Join(t.TempDir(), "aff.db")
	if err := os.WriteFile(dbPath, []byte("db"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	var report DoctorReport
	doctorWAL(dbPath, DefaultWALMaxBytes, &report)

	if len(report.Checks) != 1 {
		t.Fatalf("expected one check, got %+v", report.Checks)
	}
	c := report.Checks[0]
	if !c.OK {
		t.Errorf("a missing -wal file reported unhealthy: %s", c.Detail)
	}
	if !strings.Contains(c.Detail, "checkpoint") {
		t.Errorf("detail = %q, want it to explain that no -wal means freshly checkpointed", c.Detail)
	}
}

// TestDoctorWALSizeFlagsAnOversizedWAL is the check's whole reason for
// existing: a WAL that keeps growing means checkpointing has stalled, and the
// symptom an operator sees first is the disk filling up, not SQLite
// complaining.
func TestDoctorWALSizeFlagsAnOversizedWAL(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "aff.db")
	if err := os.WriteFile(dbPath, []byte("db"), 0o600); err != nil {
		t.Fatalf("write db: %v", err)
	}
	if err := os.WriteFile(dbPath+"-wal", make([]byte, 4096), 0o600); err != nil {
		t.Fatalf("write wal: %v", err)
	}

	var report DoctorReport
	doctorWAL(dbPath, 1024, &report) // threshold below the file's size

	c := report.Checks[0]
	if c.OK {
		t.Fatal("a 4096-byte WAL against a 1024-byte threshold reported healthy")
	}
	for _, want := range []string{"4096", "1024"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail = %q, want it to state both the size and the threshold", c.Detail)
		}
	}
}

func TestDoctorWALSizeAcceptsAWALUnderTheThreshold(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "aff.db")
	if err := os.WriteFile(dbPath, []byte("db"), 0o600); err != nil {
		t.Fatalf("write db: %v", err)
	}
	if err := os.WriteFile(dbPath+"-wal", make([]byte, 512), 0o600); err != nil {
		t.Fatalf("write wal: %v", err)
	}

	var report DoctorReport
	doctorWAL(dbPath, 4096, &report)
	if !report.Checks[0].OK {
		t.Errorf("a 512-byte WAL under a 4096-byte threshold reported unhealthy: %s", report.Checks[0].Detail)
	}
}

// TestDoctorWALSizeFallsBackToTheDefaultThreshold pins the zero-value
// behaviour: a caller that does not configure a threshold must get the
// documented default rather than a threshold of zero, which would flag every
// database with any WAL at all.
func TestDoctorWALSizeFallsBackToTheDefaultThreshold(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "aff.db")
	if err := os.WriteFile(dbPath, []byte("db"), 0o600); err != nil {
		t.Fatalf("write db: %v", err)
	}
	if err := os.WriteFile(dbPath+"-wal", make([]byte, 8192), 0o600); err != nil {
		t.Fatalf("write wal: %v", err)
	}

	for _, threshold := range []int64{0, -1} {
		var report DoctorReport
		doctorWAL(dbPath, threshold, &report)
		if !report.Checks[0].OK {
			t.Errorf("threshold %d did not fall back to DefaultWALMaxBytes (%d): %s",
				threshold, int64(DefaultWALMaxBytes), report.Checks[0].Detail)
		}
	}
}

func TestDoctorIntegrityPassesOnARealDatabase(t *testing.T) {
	s, _ := openLiveStore(t)

	var report DoctorReport
	doctorIntegrity(t.Context(), s.Writer(), &report)

	if len(report.Checks) != 1 || !report.Checks[0].OK {
		t.Errorf("integrity check failed on a freshly migrated database: %+v", report.Checks)
	}
}

func TestDoctorMigrationsReportsTheAppliedCount(t *testing.T) {
	s, _ := openLiveStore(t)

	var report DoctorReport
	doctorMigrations(t.Context(), s.Writer(), &report)

	c := report.Checks[0]
	if !c.OK {
		t.Fatalf("migrations check failed on a migrated database: %s", c.Detail)
	}
	if !strings.Contains(c.Detail, "applied") {
		t.Errorf("detail = %q, want it to report how many migrations are applied", c.Detail)
	}
}

// TestDoctorMigrationsFlagsAnUnmigratedDatabase covers the branch that names
// the actual problem. The wording is load-bearing: doctor deliberately never
// migrates a live database itself, so the message has to tell the operator
// what to run instead, or the check reports a problem with no way forward.
func TestDoctorMigrationsFlagsAnUnmigratedDatabase(t *testing.T) {
	s, _ := openLiveStore(t)

	// Drop the record of applied migrations without touching the schema:
	// this is what a database restored from a partial backup looks like.
	if _, err := s.Writer().ExecContext(t.Context(), `DELETE FROM schema_migrations`); err != nil {
		t.Fatalf("clearing schema_migrations: %v", err)
	}

	var report DoctorReport
	doctorMigrations(t.Context(), s.Writer(), &report)

	c := report.Checks[0]
	if c.OK {
		t.Fatal("a database with no applied migrations reported healthy")
	}
	if !strings.Contains(c.Detail, "not applied") {
		t.Errorf("detail = %q, want it to say migrations are not applied", c.Detail)
	}
	if !strings.Contains(c.Detail, "doctor never migrates") {
		t.Errorf("detail = %q, want it to state that doctor will not migrate for you", c.Detail)
	}
}

func TestDoctorReportHealthyIsFalseIfAnyCheckFails(t *testing.T) {
	var report DoctorReport
	report.add("first", true, "")
	if !report.Healthy() {
		t.Error("a report of one passing check is not Healthy()")
	}
	report.add("second", false, "something is wrong")
	if report.Healthy() {
		t.Error("a report containing a failed check reported Healthy()")
	}
	report.add("third", true, "")
	if report.Healthy() {
		t.Error("a later passing check masked an earlier failure")
	}
}

// TestDoctorRunsEveryCheckEvenAfterOneFails pins the ordering promise in
// Doctor's own doc comment: it "never stops early on a failure — a failed
// check must not hide the state of everything after it". At 2am, one problem
// is rarely the only problem.
func TestDoctorRunsEveryCheckEvenAfterOneFails(t *testing.T) {
	s, dbPath := openLiveStore(t)
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	feedID := seedFeedWithSchedule(t, s, "a-feed", "0 12 * * *", "UTC", true)
	insertRunForPrune(t, s, feedID, now.Add(-time.Hour), "success")

	// Provider key deliberately absent: that check must fail while every
	// other check still runs and still reports.
	t.Setenv("AFF_TEST_PROVIDER_KEY", "")

	cfg := NewDoctorConfig(dbPath)
	cfg.Now = now
	cfg.ProviderKeyEnv = "AFF_TEST_PROVIDER_KEY"
	cfg.DiskFreeBytes = 10 << 30
	cfg.DiskMinFreeBytes = 1 << 20

	report := Doctor(t.Context(), cfg)

	if report.Healthy() {
		t.Fatal("Doctor reported healthy with no provider key set")
	}

	names := make(map[string]bool, len(report.Checks))
	failed := 0
	for _, c := range report.Checks {
		names[c.Name] = true
		if !c.OK {
			failed++
		}
	}
	if failed == 0 {
		t.Fatal("no check failed, so this test proves nothing about continuing past a failure")
	}
	// The checks that come after the failing one must still be present.
	for _, want := range []string{"integrity check", "migrations current", "WAL size"} {
		if !names[want] {
			t.Errorf("check %q is missing from the report — Doctor stopped early after a failure", want)
		}
	}
}
