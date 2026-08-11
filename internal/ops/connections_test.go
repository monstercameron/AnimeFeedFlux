package ops

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// The two connection openers and the doctor's failure branches had no
// coverage: everything that exercised doctor did so against a healthy
// database, which is the one state where its output does not matter.

func TestReadWriteDSNCarriesTheSamePragmasAsTheService(t *testing.T) {
	// This DSN is duplicated from internal/store on purpose (see its doc
	// comment). Duplication is only safe if it stays in step: WAL and a
	// busy_timeout are what let this second writer coexist with the live
	// service instead of returning SQLITE_BUSY the moment prune runs during
	// a generation.
	dsn := readWriteDSN(filepath.Join("some", "dir", "aff.db"))
	for _, want := range []string{
		"journal_mode(WAL)",
		"busy_timeout(5000)",
		"foreign_keys(ON)",
		"synchronous(NORMAL)",
	} {
		if !strings.Contains(dsn, want) {
			t.Errorf("DSN %q is missing %s", dsn, want)
		}
	}
	// Forward slashes even on Windows: SQLite reads the path out of a URI.
	if strings.Contains(dsn, `\`) {
		t.Errorf("DSN %q carries a backslash path separator", dsn)
	}
	if !strings.HasPrefix(dsn, "file:") {
		t.Errorf("DSN %q is not a file URI", dsn)
	}
}

func TestOpenReadWriteAndReadOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "aff.db")

	s, err := store.Open(t.Context(), store.Options{Path: path})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if _, err := s.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	t.Run("read-write can write alongside the live service", func(t *testing.T) {
		db, err := OpenReadWrite(t.Context(), path)
		if err != nil {
			t.Fatalf("OpenReadWrite: %v", err)
		}
		defer func() { _ = db.Close() }()

		if _, err := db.ExecContext(t.Context(),
			`INSERT INTO auth_events (at, kind, ip, ok, detail) VALUES (?, 'test', '', 1, '')`,
			time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("writing through the ops connection: %v", err)
		}
	})

	t.Run("read-only refuses to write", func(t *testing.T) {
		// The whole point of having two openers: everything except prune's
		// real delete must be unable to modify the live database, so a
		// diagnostic command can never be the thing that broke production.
		db, err := OpenReadOnly(t.Context(), path)
		if err != nil {
			t.Fatalf("OpenReadOnly: %v", err)
		}
		defer func() { _ = db.Close() }()

		_, err = db.ExecContext(t.Context(),
			`INSERT INTO auth_events (at, kind, ip, ok, detail) VALUES ('x', 'test', '', 1, '')`)
		if err == nil {
			t.Fatal("a read-only connection accepted a write")
		}
	})

	t.Run("a missing database is an error, not a new file", func(t *testing.T) {
		// SQLite happily creates an empty database for a path that does not
		// exist. For an ops command that means "your data is gone" would be
		// reported as "everything is fine, zero feeds".
		missing := filepath.Join(dir, "nope", "aff.db")
		if db, err := OpenReadOnly(t.Context(), missing); err == nil {
			_ = db.Close()
			t.Error("OpenReadOnly created or accepted a missing database")
		}
	})
}

func TestDoctorReportsAnUnopenableDatabaseOnceAndSkipsTheRest(t *testing.T) {
	// Repeating the same root cause under four check names buries it. The
	// contract is that the first line says what broke and the rest say they
	// were skipped because of it.
	report := Doctor(t.Context(), DoctorConfig{
		DBPath: filepath.Join(t.TempDir(), "does-not-exist", "aff.db"),
	})

	byName := map[string]DoctorCheck{}
	for _, c := range report.Checks {
		byName[c.Name] = c
	}
	open, ok := byName["database opens"]
	if !ok {
		t.Fatalf("no 'database opens' check in %+v", report.Checks)
	}
	if open.OK {
		t.Error("doctor reported a nonexistent database as opening fine")
	}
	for _, name := range []string{"integrity check", "migrations current", "feeds running on schedule"} {
		c, ok := byName[name]
		if !ok {
			t.Errorf("check %q is missing entirely — a silently absent check reads as a passing one", name)
			continue
		}
		if c.OK {
			t.Errorf("check %q passed against a database that never opened", name)
		}
		if !strings.Contains(c.Detail, "skipped") {
			t.Errorf("check %q says %q; it should say it was skipped", name, c.Detail)
		}
	}
	if report.Healthy() {
		t.Error("the report as a whole reports OK despite a failed check")
	}
}

func TestDoctorFlagsAStaleFeed(t *testing.T) {
	// The staleness check is the one that answers "is anything silently
	// broken", so a feed that has not run in days must show up as a failure
	// with the feed named — a bare "not ok" sends an operator reading logs.
	dir := t.TempDir()
	path := filepath.Join(dir, "aff.db")
	s, err := store.Open(t.Context(), store.Options{Path: path})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if _, err := s.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	feedID := seedFeedWithSchedule(t, s, "trivia-daily", "0 9 * * *", "UTC", true)

	// A run that succeeded a week ago, against a daily schedule.
	long := time.Now().UTC().Add(-7 * 24 * time.Hour).Format(time.RFC3339Nano)
	if _, err := s.Writer().ExecContext(t.Context(), `
		INSERT INTO runs (feed_id, trigger, status, started_at, finished_at, heartbeat_at, items_added)
		VALUES (?, 'cron', 'success', ?, ?, ?, 1)`, feedID, long, long, long); err != nil {
		t.Fatalf("seeding an old run: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("closing the store: %v", err)
	}

	report := Doctor(t.Context(), DoctorConfig{DBPath: path, Now: time.Now().UTC()})
	var check DoctorCheck
	for _, c := range report.Checks {
		if c.Name == "feeds running on schedule" {
			check = c
		}
	}
	if check.Name == "" {
		t.Fatalf("no staleness check in %+v", report.Checks)
	}
	if check.OK {
		t.Fatal("a feed that last ran a week ago on a daily schedule was reported healthy")
	}
	if !strings.Contains(check.Detail, "trivia-daily") {
		t.Errorf("the failure does not name the stale feed: %q", check.Detail)
	}
}

func TestRealClockIsTheWallClock(t *testing.T) {
	// realClock exists so the nightly scheduler can be tested without
	// sleeping; it is trivial, and it is also the only implementation that
	// runs in production.
	var c Clock = realClock{}
	before := time.Now()
	got := c.Now()
	if got.Before(before.Add(-time.Second)) || got.After(time.Now().Add(time.Second)) {
		t.Errorf("realClock.Now() = %s, which is not the wall clock", got)
	}
	select {
	case <-c.After(time.Millisecond):
	case <-time.After(2 * time.Second):
		t.Error("realClock.After never fired")
	}
}
