package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/config"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
	"io"
	"log/slog"
)

// openTestStore opens and migrates a fresh temp-file SQLite database, the
// same pattern internal/store's own tests use (store_test.go's openTemp).
func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := t.Context()
	st, err := store.Open(ctx, store.Options{Path: filepath.Join(t.TempDir(), "aff.db")})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return st
}

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load(func(k string) string {
		switch k {
		case "AFF_DB_PATH":
			return filepath.Join(t.TempDir(), "unused.db")
		case "AFF_PUBLISH_ADDR":
			return "127.0.0.1:0"
		case "AFF_ADMIN_ADDR":
			return "127.0.0.1:0"
		case "AFF_PUBLIC_BASE_URL":
			return "https://anime.example.com"
		case "AFF_ALLOWED_ORIGINS":
			return "https://admin.example.com"
		case "AFF_SECRET_KEY":
			return "test-secret-key"
		case "SCHEMAFLUX_API_KEY":
			return "test-provider-key"
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

// seedFeed inserts a feed row directly (there is no writer-side CreateFeed
// call in this package's scope) and returns its id.
func seedFeed(t *testing.T, st *store.Store, slug string, enabled bool) int64 {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	en := 0
	if enabled {
		en = 1
	}
	res, err := st.Writer().ExecContext(t.Context(), `
		INSERT INTO feeds (slug, title, description, language, kind, enabled, timezone, created_at, updated_at)
		VALUES (?, ?, '', 'en', 'generative', ?, 'UTC', ?, ?)`,
		slug, slug, en, now, now)
	if err != nil {
		t.Fatalf("seed feed %q: %v", slug, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("feed id: %v", err)
	}
	return id
}

// seedItem inserts an item row directly, at publishedAt, optionally
// soft-deleted.
func seedItem(t *testing.T, st *store.Store, feedID int64, key string, publishedAt time.Time, deleted bool) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var deletedAt any
	if deleted {
		deletedAt = now
	}
	_, err := st.Writer().ExecContext(t.Context(), `
		INSERT INTO items (feed_id, item_key, content_hash, title, summary_text, body_html,
		                    link, published_at, origin, created_at, updated_at, deleted_at)
		VALUES (?, ?, ?, ?, 'summary text', '<p>body</p>', 'https://source.example.com/a', ?, 'generated', ?, ?, ?)`,
		feedID, key, "hash-"+key, "Title for "+key,
		publishedAt.UTC().Format(time.RFC3339Nano), now, now, deletedAt)
	if err != nil {
		t.Fatalf("seed item %q: %v", key, err)
	}
}

func newTestHandler(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()
	st := openTestStore(t)
	cfg := testConfig(t)
	h, _, err := buildPublishHandlerWithInvalidator(st, cfg, "test", slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err != nil {
		t.Fatalf("buildPublishHandlerWithInvalidator: %v", err)
	}
	return h, st
}

func doReq(h http.Handler, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// --- serving real feeds from SQLite -------------------------------------

func TestServesRSSAtomJSONFromRealStore(t *testing.T) {
	h, st := newTestHandler(t)
	feedID := seedFeed(t, st, "trivia-daily", true)
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	seedItem(t, st, feedID, "01J000000000000000000AAA1", base, false)
	seedItem(t, st, feedID, "01J000000000000000000AAA2", base.Add(time.Second), false)
	seedItem(t, st, feedID, "01J000000000000000000AAA3", base.Add(2*time.Second), false)

	cases := []struct {
		path string
		want [3]string
	}{
		{"/feeds/trivia-daily.xml", [3]string{"AAA1", "AAA2", "AAA3"}},
		{"/feeds/trivia-daily.atom", [3]string{"AAA1", "AAA2", "AAA3"}},
		{"/feeds/trivia-daily.json", [3]string{"AAA1", "AAA2", "AAA3"}},
	}
	for _, c := range cases {
		rec := doReq(h, http.MethodGet, c.path, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200; body=%s", c.path, rec.Code, rec.Body.String())
		}
		for _, want := range c.want {
			if !bytes.Contains(rec.Body.Bytes(), []byte(want)) {
				t.Errorf("%s: body missing item key fragment %q\nbody=%s", c.path, want, rec.Body.String())
			}
		}
	}
}

func TestUnknownSlugIs404(t *testing.T) {
	h, _ := newTestHandler(t)
	rec := doReq(h, http.MethodGet, "/feeds/does-not-exist.xml", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestSoftDeletedItemPermalinkIs410NotFound(t *testing.T) {
	h, st := newTestHandler(t)
	feedID := seedFeed(t, st, "trivia-daily", true)
	seedItem(t, st, feedID, "01J000000000000000000DEAD", time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), true)

	rec := doReq(h, http.MethodGet, "/items/01J000000000000000000DEAD", nil)
	if rec.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410", rec.Code)
	}
	if rec.Code == http.StatusNotFound {
		t.Fatal("soft-deleted item permalink must be 410, not 404")
	}
}

func TestFeedIndexListsEnabledFeed(t *testing.T) {
	h, st := newTestHandler(t)
	seedFeed(t, st, "trivia-daily", true)

	rec := doReq(h, http.MethodGet, "/", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("trivia-daily")) {
		t.Errorf("index does not list the seeded feed:\n%s", rec.Body.String())
	}
}

// --- conditional GET ------------------------------------------------------

func TestConditionalGETReturns304OnMatchingETag(t *testing.T) {
	h, st := newTestHandler(t)
	feedID := seedFeed(t, st, "trivia-daily", true)
	seedItem(t, st, feedID, "01J000000000000000000BBB1", time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), false)

	first := doReq(h, http.MethodGet, "/feeds/trivia-daily.xml", nil)
	if first.Code != http.StatusOK {
		t.Fatalf("first request status = %d", first.Code)
	}
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("ETag missing on first response")
	}

	second := doReq(h, http.MethodGet, "/feeds/trivia-daily.xml", map[string]string{"If-None-Match": etag})
	if second.Code != http.StatusNotModified {
		t.Fatalf("second request status = %d, want 304", second.Code)
	}
}

// --- read-only handle: it must be structurally unable to write ------------

// TestPublishPlaneReaderCannotWrite proves the property PLAN.md §2 depends
// on, rather than trusting the comment above buildPublishHandler.
//
// Two things are checked, because either one alone would be trust-me rather
// than proof:
//
//  1. Compile-time: store.Reader (the type of the value buildPublishHandler
//     closes every Deps func over — see the reader variable there) declares
//     only QueryContext/QueryRowContext/PingContext. There is no Exec, no
//     Begin, on that interface — code written against it, including this
//     file's, has no write call available to make even if it wanted to.
//     That is already enforced by the compiler; it is not something a
//     runtime test can additionally demonstrate, only restate.
//  2. Runtime, and this is the part worth asserting: the connection behind
//     that interface was opened mode=ro (store.go's readerDSN), so a write
//     sent straight through it — bypassing the interface's method set
//     entirely, straight down QueryContext, which SQLite still parses as a
//     statement — is rejected by SQLite itself. This is the honest test:
//     it exercises the exact st.Reader() value buildPublishHandler wires
//     into Deps and confirms the database, not just the type checker,
//     refuses to be written through it.
func TestPublishPlaneReaderCannotWrite(t *testing.T) {
	st := openTestStore(t)
	reader := st.Reader()

	_, err := reader.QueryContext(t.Context(), `INSERT INTO feeds (slug, title, kind, created_at, updated_at)
		VALUES ('nope', 'nope', 'generative', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
	if err == nil {
		t.Fatal("a write sent through store.Reader's connection succeeded; the read-only handle no longer holds")
	}

	// Confirm nothing landed either, in case some driver quirk returned an
	// error while still applying the statement.
	var count int
	if err := reader.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM feeds WHERE slug = 'nope'`).Scan(&count); err != nil {
		t.Fatalf("checking for a leaked write: %v", err)
	}
	if count != 0 {
		t.Fatal("the rejected write landed anyway; store.Reader is not actually read-only")
	}
}

// --- openStore: migration + boot recovery ---------------------------------

func TestOpenStoreMigratesAndReclaimsStaleRuns(t *testing.T) {
	ctx := t.Context()
	cfg := testConfig(t)

	// Pre-create the DB file with a stale 'running' run, as if a previous
	// process crashed mid-generation, then reopen it through openStore and
	// confirm the boot watchdog reclaims it (§15).
	st, err := store.Open(ctx, store.Options{Path: cfg.DBPath})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	feedID := seedFeed(t, st, "trivia-daily", true)
	staleHeartbeat := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339Nano)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := st.Writer().ExecContext(ctx, `
		INSERT INTO runs (feed_id, started_at, status, trigger, lock_holder, heartbeat_at)
		VALUES (?, ?, 'running', 'cron', 'crashed-worker', ?)`,
		feedID, now, staleHeartbeat); err != nil {
		t.Fatalf("seed stale run: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("closing seed store: %v", err)
	}

	reopened, err := openStore(ctx, cfg)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer func() { _ = reopened.Close() }()

	var status string
	if err := reopened.Writer().QueryRowContext(ctx,
		`SELECT status FROM runs WHERE feed_id = ?`, feedID,
	).Scan(&status); err != nil {
		t.Fatalf("reading reclaimed run status: %v", err)
	}
	if status != "interrupted" {
		t.Errorf("stale run status = %q, want %q (zero items committed, per §15)", status, "interrupted")
	}
}
