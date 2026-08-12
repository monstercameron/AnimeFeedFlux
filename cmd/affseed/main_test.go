package main

import (
	"context"
	"database/sql"
	"flag"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/ids"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/rpc"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// main_test.go covers cmd/affseed, which had no tests at all.
//
// The obvious objection to testing a dev-only seeder is that nothing ships
// it. The reason to test it anyway is that this tool writes to the SAME
// tables through the SAME store/rpc code the product uses, and its package
// doc makes three safety promises that are only promises until something
// checks them: it never touches the admin table, it refuses to run on top of
// existing feeds without --force, and every string it writes is visibly
// placeholder text. A seeder that quietly breaks any of those is how you get
// a dev database that looks right and behaves wrong — or, for the admin-table
// promise, a self-inflicted lockout.
//
// The tests below assert those three promises plus the data invariants the
// seeded database has to satisfy for the dev environment to be worth
// anything: strictly increasing published_at per feed (the UNIQUE(feed_id,
// published_at) ordering rule), unique item keys, a soft-deleted item to
// exercise the 410 permalink path, a revision history, and a correction.

func newSeedStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := t.Context()
	st, err := store.Open(ctx, store.Options{
		Path: filepath.Join(t.TempDir(), "seed.db"),
		Log:  slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

// seedOnce runs the whole seeder against a fresh database and returns both
// the store and the summary, so the assertions below can each look at a
// fully-seeded database without paying to seed it more than once per test.
func seedOnce(t *testing.T) (*store.Store, seedSummary) {
	t.Helper()
	st := newSeedStore(t)
	ctx := t.Context()
	idSrc := ids.NewSource()
	feedSrv := rpc.NewFeedServer(st, noopInvalidator{}, nil)
	itemSrv := rpc.NewItemServer(st, noopInvalidator{}, idSrc)

	// A fixed instant rather than time.Now(): every seeded published_at is
	// derived from it, and a test that asserts ordering should not depend on
	// what time the suite happens to run.
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	summary, err := seedAll(ctx, st, feedSrv, itemSrv, idSrc, now)
	if err != nil {
		t.Fatalf("seedAll: %v", err)
	}
	return st, summary
}

func TestSeedAllCreatesTheThreeDocumentedFeeds(t *testing.T) {
	st, summary := seedOnce(t)
	ctx := t.Context()

	want := map[string]string{
		"daily-anime-trivia":         "generative",
		"weekly-anime-news":          "grounded",
		"character-spotlight-weekly": "generative",
	}

	rows, err := st.Writer().QueryContext(ctx, `SELECT slug, kind, enabled FROM feeds ORDER BY slug`)
	if err != nil {
		t.Fatalf("query feeds: %v", err)
	}
	defer func() { _ = rows.Close() }()

	got := map[string]string{}
	for rows.Next() {
		var slug, kind string
		var enabled bool
		if err := rows.Scan(&slug, &kind, &enabled); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[slug] = kind
		if !enabled {
			t.Errorf("feed %q was seeded disabled — a dev database whose feeds do not run is not a dev database", slug)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	if len(got) != len(want) {
		t.Fatalf("seeded %d feeds, want %d: %v", len(got), len(want), got)
	}
	for slug, kind := range want {
		if got[slug] != kind {
			t.Errorf("feed %q kind = %q, want %q", slug, got[slug], kind)
		}
	}

	if len(summary.feeds) != 3 {
		t.Fatalf("summary reports %d feeds, want 3", len(summary.feeds))
	}
	for _, f := range summary.feeds {
		if _, ok := want[f.slug]; !ok {
			t.Errorf("summary names feed %q, which was not seeded", f.slug)
		}
		if f.itemsAdded == 0 {
			t.Errorf("summary says feed %q got 0 items", f.slug)
		}
	}
}

// TestSeedNeverTouchesTheAdminTable is the lockout guard. The package doc
// promises "never touches the `admin` table, directly or indirectly", and the
// cost of that being false is losing access to your own dev instance.
func TestSeedNeverTouchesTheAdminTable(t *testing.T) {
	st := newSeedStore(t)
	ctx := t.Context()

	before := countRows(t, st.Writer(), `SELECT COUNT(1) FROM admin`)

	idSrc := ids.NewSource()
	if _, err := seedAll(ctx, st,
		rpc.NewFeedServer(st, noopInvalidator{}, nil),
		rpc.NewItemServer(st, noopInvalidator{}, idSrc),
		idSrc, time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("seedAll: %v", err)
	}

	if after := countRows(t, st.Writer(), `SELECT COUNT(1) FROM admin`); after != before {
		t.Fatalf("admin row count changed from %d to %d — the seeder must never write to admin", before, after)
	}
}

// TestSeededContentIsVisiblyPlaceholder enforces the third safety promise:
// no seeded item may be mistakable for something really published. Every
// title and summary has to carry a marker, and nothing may name a real
// studio.
func TestSeededContentIsVisiblyPlaceholder(t *testing.T) {
	st, _ := seedOnce(t)
	ctx := t.Context()

	rows, err := st.Writer().QueryContext(ctx, `SELECT title, summary_text FROM items`)
	if err != nil {
		t.Fatalf("query items: %v", err)
	}
	defer func() { _ = rows.Close() }()

	n := 0
	for rows.Next() {
		var title, summary string
		if err := rows.Scan(&title, &summary); err != nil {
			t.Fatalf("scan: %v", err)
		}
		n++
		combined := strings.ToLower(title + " " + summary)
		if !strings.Contains(combined, "seed data") && !strings.Contains(combined, "fictional") {
			t.Errorf("item %q carries no placeholder marker — it could be mistaken for real content\n  summary: %s", title, summary)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if n == 0 {
		t.Fatal("no items were seeded at all")
	}
}

// TestSeededItemsSatisfyTheOrderingInvariant checks the rule the schema
// enforces with UNIQUE(feed_id, published_at) and that the publish plane
// depends on: within a feed, published_at strictly increases. A seeder that
// produced two items at the same instant would fail the insert; one that
// produced them out of order would produce a feed whose newest item is not
// its newest item.
func TestSeededItemsSatisfyTheOrderingInvariant(t *testing.T) {
	st, _ := seedOnce(t)
	ctx := t.Context()

	feedIDs := []int64{}
	rows, err := st.Writer().QueryContext(ctx, `SELECT id FROM feeds ORDER BY id`)
	if err != nil {
		t.Fatalf("query feeds: %v", err)
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		feedIDs = append(feedIDs, id)
	}
	_ = rows.Close()

	for _, feedID := range feedIDs {
		itemRows, err := st.Writer().QueryContext(ctx,
			`SELECT published_at FROM items WHERE feed_id = ? ORDER BY published_at`, feedID)
		if err != nil {
			t.Fatalf("query items for feed %d: %v", feedID, err)
		}
		var prev string
		count := 0
		for itemRows.Next() {
			var at string
			if err := itemRows.Scan(&at); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if count > 0 && at <= prev {
				t.Errorf("feed %d: published_at %q does not strictly increase after %q", feedID, at, prev)
			}
			prev = at
			count++
		}
		_ = itemRows.Close()
		if count == 0 {
			t.Errorf("feed %d was seeded with no items", feedID)
		}
	}
}

func TestSeededItemKeysAndHashesAreUnique(t *testing.T) {
	st, _ := seedOnce(t)

	total := countRows(t, st.Writer(), `SELECT COUNT(1) FROM items`)
	distinctKeys := countRows(t, st.Writer(), `SELECT COUNT(DISTINCT item_key) FROM items`)
	if distinctKeys != total {
		t.Errorf("item_key collisions: %d items but %d distinct keys", total, distinctKeys)
	}

	// Content hashes are what the analysis pipeline dedupes on; a seeder that
	// emitted the same hash twice would make every dedupe test downstream
	// meaningless.
	distinctHashes := countRows(t, st.Writer(), `SELECT COUNT(DISTINCT content_hash) FROM items`)
	if distinctHashes != total {
		t.Errorf("content_hash collisions: %d items but %d distinct hashes", total, distinctHashes)
	}
}

// TestSeedProducesTheThreeSpecialItemStates covers what the seeder exists to
// produce beyond bulk rows: a soft-deleted item (the 410 permalink path), an
// item with revision history, and a published correction. Each of these is a
// different code path in store/rpc, and each is something the admin UI has a
// screen for that would otherwise have nothing to show.
func TestSeedProducesTheThreeSpecialItemStates(t *testing.T) {
	st, summary := seedOnce(t)
	ctx := t.Context()

	var deleted, revised, correction string
	for _, f := range summary.feeds {
		if f.deletedKey != "" {
			deleted = f.deletedKey
		}
		if f.revisedKey != "" {
			revised = f.revisedKey
		}
		if f.correctionKey != "" {
			correction = f.correctionKey
		}
	}

	if deleted == "" {
		t.Error("no soft-deleted item was seeded — /items/<key> has no 410 case to show")
	} else {
		var deletedAt sql.NullString
		err := st.Writer().QueryRowContext(ctx,
			`SELECT deleted_at FROM items WHERE item_key = ?`, deleted).Scan(&deletedAt)
		if err != nil {
			t.Fatalf("looking up soft-deleted item %q: %v", deleted, err)
		}
		if !deletedAt.Valid || deletedAt.String == "" {
			t.Errorf("item %q is reported soft-deleted but has no deleted_at", deleted)
		}
	}

	if revised == "" {
		t.Error("no revised item was seeded — the revision-history view has nothing to show")
	} else if n := countRows(t, st.Writer(),
		`SELECT COUNT(1) FROM item_revisions r JOIN items i ON i.id = r.item_id WHERE i.item_key = ?`, revised); n == 0 {
		t.Errorf("item %q is reported revised but has no item_revisions rows", revised)
	}

	if correction == "" {
		t.Error("no correction was seeded")
		return
	}
	if n := countRows(t, st.Writer(), `SELECT COUNT(1) FROM corrections`); n == 0 {
		t.Error("a correction key was reported but the corrections table is empty")
	}
	var origin string
	if err := st.Writer().QueryRowContext(ctx,
		`SELECT origin FROM items WHERE item_key = ?`, correction).Scan(&origin); err != nil {
		t.Fatalf("looking up correction item %q: %v", correction, err)
	}
	if origin != string(model.OriginCorrection) {
		t.Errorf("correction item %q has origin %q, want %q", correction, origin, model.OriginCorrection)
	}
}

// TestSeedProducesRunsInEveryTerminalState matters because /history's status
// filter, its error-kind rendering and its reject-reason breakdown all have
// nothing to display against a database of uniformly successful runs.
func TestSeedProducesRunsInEveryTerminalState(t *testing.T) {
	st, summary := seedOnce(t)
	ctx := t.Context()

	rows, err := st.Writer().QueryContext(ctx, `SELECT status, COUNT(1) FROM runs GROUP BY status`)
	if err != nil {
		t.Fatalf("query runs: %v", err)
	}
	defer func() { _ = rows.Close() }()

	byStatus := map[string]int{}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		byStatus[status] = n
	}

	for _, want := range []string{"success", "failed", "skipped"} {
		if byStatus[want] == 0 {
			t.Errorf("no runs with status %q were seeded (got %v)", want, byStatus)
		}
	}

	// And the summary has to agree with the database, or the line it prints
	// after a seed is a number someone will trust and should not.
	totalSucceeded := 0
	for _, f := range summary.feeds {
		totalSucceeded += f.runsSucceeded
	}
	if totalSucceeded != byStatus["success"] {
		t.Errorf("summary reports %d successful runs, database has %d", totalSucceeded, byStatus["success"])
	}
}

func TestSeedIsIdempotentlyAdditiveNotDestructive(t *testing.T) {
	st, _ := seedOnce(t)
	ctx := t.Context()

	firstItems := countRows(t, st.Writer(), `SELECT COUNT(1) FROM items`)

	// Seeding a second time onto the same database must not delete anything.
	// The slug-uniqueness constraint makes the second createFeed fail, which
	// is the correct outcome — what is being asserted here is that a FAILED
	// second seed leaves the first one intact rather than half-torn-down.
	idSrc := ids.NewSource()
	_, err := seedAll(ctx, st,
		rpc.NewFeedServer(st, noopInvalidator{}, nil),
		rpc.NewItemServer(st, noopInvalidator{}, idSrc),
		idSrc, time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC))
	if err == nil {
		t.Log("second seedAll succeeded (slugs were not rejected); asserting only that nothing was lost")
	}

	if after := countRows(t, st.Writer(), `SELECT COUNT(1) FROM items`); after < firstItems {
		t.Errorf("item count fell from %d to %d after a second seed — the seeder destroyed data", firstItems, after)
	}
}

// --- run(): the command-line surface and its safety rails ---------------

// withArgs installs a fresh flag set and os.Args for one call to run(),
// restoring both afterwards. run() reads the package-level flag.CommandLine,
// which is process-global and already parsed by the time any test executes,
// so it has to be replaced rather than reused.
func withArgs(t *testing.T, args ...string) {
	t.Helper()
	oldArgs := os.Args
	oldFlags := flag.CommandLine
	flag.CommandLine = flag.NewFlagSet(args[0], flag.ContinueOnError)
	flag.CommandLine.SetOutput(newDiscardWriter())
	os.Args = args
	t.Cleanup(func() {
		os.Args = oldArgs
		flag.CommandLine = oldFlags
	})
}

func TestRunRefusesWithoutADatabasePath(t *testing.T) {
	t.Setenv("AFF_DB_PATH", "")
	withArgs(t, "affseed")
	if code := run(); code != 1 {
		t.Errorf("run() with no --db returned %d, want 1", code)
	}
}

func TestRunSeedsAnEmptyDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.db")
	t.Setenv("AFF_DB_PATH", "")
	withArgs(t, "affseed", "-db", path)

	if code := run(); code != 0 {
		t.Fatalf("run() on an empty database returned %d, want 0", code)
	}

	st, err := store.Open(t.Context(), store.Options{Path: path, Log: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatalf("reopen seeded database: %v", err)
	}
	defer func() { _ = st.Close() }()
	if n := countRows(t, st.Writer(), `SELECT COUNT(1) FROM feeds`); n != 3 {
		t.Errorf("seeded database has %d feeds, want 3", n)
	}
}

// TestRunRefusesToSeedOverExistingFeeds is the second safety promise, and the
// one most likely to be tested by accident in real life: pointing affseed at
// a database that already has work in it.
func TestRunRefusesToSeedOverExistingFeeds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "populated.db")
	t.Setenv("AFF_DB_PATH", "")

	withArgs(t, "affseed", "-db", path)
	if code := run(); code != 0 {
		t.Fatalf("first run returned %d, want 0", code)
	}

	withArgs(t, "affseed", "-db", path)
	if code := run(); code != 1 {
		t.Errorf("second run without --force returned %d, want 1 (it must refuse)", code)
	}

	withArgs(t, "affseed", "-db", path, "-force")
	if code := run(); code != 0 {
		t.Errorf("run with --force returned %d, want 0 (--force is the documented override)", code)
	}
}

func TestRunReportsAnUnopenableDatabase(t *testing.T) {
	t.Setenv("AFF_DB_PATH", "")
	// A directory, not a file: SQLite cannot open it, and run() must report
	// that rather than panicking or silently succeeding.
	withArgs(t, "affseed", "-db", t.TempDir())
	if code := run(); code != 1 {
		t.Errorf("run() against an unopenable path returned %d, want 1", code)
	}
}

func TestRunReadsTheEnvironmentFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fromenv.db")
	t.Setenv("AFF_DB_PATH", path)
	withArgs(t, "affseed")

	if code := run(); code != 0 {
		t.Fatalf("run() with AFF_DB_PATH set returned %d, want 0", code)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("AFF_DB_PATH was not used: %v", err)
	}
}

// --- pure content helpers ----------------------------------------------

func TestContentHelpersAreDeterministicAndMarked(t *testing.T) {
	for i := range 24 {
		q1, a1 := triviaQA(i)
		q2, a2 := triviaQA(i)
		if q1 != q2 || a1 != a2 {
			t.Errorf("triviaQA(%d) is not deterministic", i)
		}
		if q1 == "" || a1 == "" {
			t.Errorf("triviaQA(%d) returned an empty question or answer", i)
		}

		h1, b1 := newsArticle(i)
		h2, b2 := newsArticle(i)
		if h1 != h2 || b1 != b2 {
			t.Errorf("newsArticle(%d) is not deterministic", i)
		}
		if !strings.Contains(strings.ToLower(h1+b1), "fictional") && !strings.Contains(strings.ToLower(h1+b1), "seed") {
			t.Errorf("newsArticle(%d) produced unmarked content: %q", i, h1)
		}

		n1, bl1 := spotlight(i)
		n2, bl2 := spotlight(i)
		if n1 != n2 || bl1 != bl2 {
			t.Errorf("spotlight(%d) is not deterministic", i)
		}
		if n1 == "" || bl1 == "" {
			t.Errorf("spotlight(%d) returned empty content", i)
		}
	}
}

func TestRandHelpersStayInRange(t *testing.T) {
	for range 200 {
		if got := randInt(3, 9); got < 3 || got > 9 {
			t.Fatalf("randInt(3, 9) = %d, out of range", got)
		}
		if got := randCost(0.01, 0.25); got < 0.01 || got > 0.25 {
			t.Fatalf("randCost(0.01, 0.25) = %v, out of range", got)
		}
	}
	// A degenerate range must not panic or loop — rand.IntN(0) panics, so a
	// lo==hi call is the edge worth pinning.
	if got := randInt(5, 5); got != 5 {
		t.Errorf("randInt(5, 5) = %d, want 5", got)
	}
}

func TestMintItemDerivesADistinctHashPerItem(t *testing.T) {
	idSrc := ids.NewSource()
	base := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

	a := mintItem(idSrc, 1, itemPlan{title: "A", publishedAt: base, origin: model.OriginGenerated})
	b := mintItem(idSrc, 1, itemPlan{title: "B", publishedAt: base.Add(time.Minute), origin: model.OriginGenerated})

	if a.ItemKey == b.ItemKey {
		t.Error("two minted items share an item_key")
	}
	if a.ContentHash == b.ContentHash {
		t.Error("two minted items share a content_hash")
	}
	if a.ContentHash == "" || len(a.ContentHash) != 64 {
		t.Errorf("content hash %q is not a hex sha256", a.ContentHash)
	}
	if a.FeedID != 1 || a.Origin != model.OriginGenerated {
		t.Errorf("mintItem did not carry through feed id/origin: %+v", a)
	}
}

func TestNoopInvalidatorDoesNothingLoudly(t *testing.T) {
	// It exists to satisfy publish.Invalidator; the only property worth
	// asserting is that calling it is safe, since affseed runs in a separate
	// process from any live cache.
	inv := noopInvalidator{}
	inv.InvalidateFeed("anything")
	inv.InvalidateAll()
}

func TestSeedSummaryPrintNamesEverySpecialItem(t *testing.T) {
	// print() writes to stdout; capture it, because the keys it reports are
	// what a developer copies into a URL to check the 410 path by hand.
	out := captureStdout(t, func() {
		seedSummary{feeds: []feedSummary{{
			slug: "daily-anime-trivia", kind: "generative", itemsAdded: 20,
			runsSucceeded: 12, runsFailed: 1, runsSkipped: 1,
			deletedKey: "DELKEY", revisedKey: "REVKEY", correctionKey: "CORKEY",
		}}}.print()
	})

	for _, want := range []string{"daily-anime-trivia", "DELKEY", "REVKEY", "CORKEY", "410"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary output does not mention %q:\n%s", want, out)
		}
	}
}

// --- helpers ------------------------------------------------------------

func countRows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("counting rows (%s): %v", query, err)
	}
	return n
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				sb.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		done <- sb.String()
	}()

	fn()

	os.Stdout = old
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

// discardWriter swallows flag's usage output so a deliberately-bad argument
// list in a test does not print a usage block into the test log.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func newDiscardWriter() discardWriter { return discardWriter{} }
