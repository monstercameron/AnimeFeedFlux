package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/feedspec"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

func seedStaleFixture(t *testing.T, dbPath string) {
	t.Helper()
	st, err := store.Open(t.Context(), store.Options{Path: dbPath})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	if _, err := st.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	spec := feedspec.Defaults()
	spec.Slug = "stale-feed"
	spec.Title = "Stale Feed"
	spec.Kind = model.KindGenerative
	spec.Cron = "0 12 * * *"
	spec.Timezone = "UTC"
	specJSON, err := feedspec.Export(spec)
	if err != nil {
		t.Fatalf("export spec: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := st.Writer().ExecContext(t.Context(), `
		INSERT INTO feeds (slug, title, kind, spec_json, enabled, timezone, created_at, updated_at)
		VALUES (?, ?, 'generative', ?, 1, 'UTC', ?, ?)`,
		spec.Slug, spec.Title, string(specJSON), now, now)
	if err != nil {
		t.Fatalf("seed feed: %v", err)
	}
	feedID, _ := res.LastInsertId()

	longAgo := time.Now().Add(-30 * 24 * time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := st.Writer().ExecContext(t.Context(),
		`INSERT INTO runs (feed_id, started_at, status, trigger) VALUES (?, ?, 'success', 'cron')`,
		feedID, longAgo); err != nil {
		t.Fatalf("seed run: %v", err)
	}
}

func TestStaleFlagsALongOverdueFeedAndExitsNonZero(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "aff.db")
	seedStaleFixture(t, dbPath)

	a, stdout, stderr := newOpsTestApp(t, dbPath)
	code := a.run([]string{"stale"})
	if code != exitFail {
		t.Fatalf("aff stale on an overdue feed: exit %d, want %d (stderr: %s)", code, exitFail, stderr.String())
	}
	if !strings.Contains(stdout.String(), "stale-feed") {
		t.Fatalf("stdout = %q, want it to name stale-feed", stdout.String())
	}
}

func TestStaleReportsCleanWhenNothingIsOverdue(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "aff.db")
	seedMigratedDB(t, dbPath) // no feeds at all

	a, stdout, stderr := newOpsTestApp(t, dbPath)
	code := a.run([]string{"stale"})
	if code != exitOK {
		t.Fatalf("aff stale with no feeds: exit %d, want %d (stderr: %s)", code, exitOK, stderr.String())
	}
	if !strings.Contains(stdout.String(), "no stale feeds") {
		t.Fatalf("stdout = %q, want a clean report", stdout.String())
	}
}
