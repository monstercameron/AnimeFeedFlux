// J10 — the subscriber lifecycle (PLAN.md §22 "J10 — Subscriber lifecycle
// (the one that matters)", TODOS.md BF-44..BF-51). Per §17.5, every claim in
// this file is checked against a REAL fetch over real HTTP (httptest's real
// TCP loopback, via World.FetchFeed / this file's own httptest servers) —
// never an in-process ResponseRecorder — because J10 is the one flow this
// whole test suite exists to prove for a consumer that is not the admin.
package flowtest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/feedvalidate"
	"github.com/monstercameron/AnimeFeedFlux/internal/generate"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/publish"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// rssDescriptionPattern extracts every <description>...</description> body
// (channel-level and item-level alike — either is fine to search, since
// neither should ever contain a trivia answer).
var rssDescriptionPattern = regexp.MustCompile(`(?s)<description>(.*?)</description>`)

// rssDescriptions concatenates every RSS <description> element's text, for
// TestJ10_TriviaAnswerNeverInDescriptionOrOGDescription's leak check.
func rssDescriptions(t *testing.T, doc []byte) string {
	t.Helper()
	var b strings.Builder
	for _, m := range rssDescriptionPattern.FindAllSubmatch(doc, -1) {
		b.Write(m[1])
		b.WriteByte('\n')
	}
	return b.String()
}

// atomSummaryPattern extracts every Atom <summary ...>...</summary> body.
var atomSummaryPattern = regexp.MustCompile(`(?s)<summary[^>]*>(.*?)</summary>`)

func atomSummaries(t *testing.T, doc []byte) string {
	t.Helper()
	var b strings.Builder
	for _, m := range atomSummaryPattern.FindAllSubmatch(doc, -1) {
		b.Write(m[1])
		b.WriteByte('\n')
	}
	return b.String()
}

// jsonFeedItemSummary is the one field this check cares about — jsonfeed.go
// documents "summary" as the preview-safe field, with the full body (and any
// answer) confined to content_html instead.
type jsonFeedItemSummary struct {
	Summary string `json:"summary"`
}

// jsonSummaries concatenates every item's "summary" field from a JSON Feed
// document.
func jsonSummaries(t *testing.T, doc []byte) string {
	t.Helper()
	var parsed struct {
		Items []jsonFeedItemSummary `json:"items"`
	}
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("parsing JSON Feed document: %v", err)
	}
	var b strings.Builder
	for _, it := range parsed.Items {
		b.WriteString(it.Summary)
		b.WriteByte('\n')
	}
	return b.String()
}

// publishableFeed is validGenerativeFeed (fixtures_test.go) plus a
// Description, which feedvalidate.RSS requires (§5.1 channel-required-
// elements) but the shared fixture leaves blank because most flow tests
// never render a full document. Every J10 test does, so every J10 test needs
// it — keeping it local here rather than editing fixtures_test.go.
func publishableFeed(slug string) model.Feed {
	f := validGenerativeFeed(slug)
	f.Description = "A flow test feed used to prove the subscriber-facing document is well formed."
	return f
}

// manualSpec is validSampleSpec (fixtures_test.go) with Trigger populated.
// Sample never touches runs.trigger, so the shared fixture leaves it blank;
// every J10 test below calls RunGeneration instead, which does write it, and
// runs.trigger has a CHECK(trigger IN ('cron','manual')) constraint that a
// blank string fails.
func manualSpec() generate.Spec {
	s := validSampleSpec()
	s.Trigger = "manual"
	return s
}

// findingsString joins feedvalidate.Finding values for a readable failure
// message — Finding has no Stringer of its own.
func findingsString(fs []feedvalidate.Finding) string {
	var b strings.Builder
	for _, f := range fs {
		b.WriteString(string(f.Level))
		b.WriteString(" ")
		b.WriteString(f.Rule)
		b.WriteString(": ")
		b.WriteString(f.Message)
		b.WriteString("\n")
	}
	return b.String()
}

// TestJ10_FeedValidatesCleanInAllThreeFormats is BF-44. It deliberately uses
// internal/feedvalidate — an independent re-implementation of the RSS/Atom/
// JSON Feed rules this project depends on (see that package's doc comment) —
// rather than internal/render's own golden tests, so a bug shared between
// the renderer and its own test fixtures cannot hide from this check.
func TestJ10_FeedValidatesCleanInAllThreeFormats(t *testing.T) {
	w := New(t)
	ctx := t.Context()

	feed, err := w.CreateFeed(ctx, publishableFeed("j10-validate"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}

	spec := manualSpec()
	spec.ItemsPerRun = 3
	w.Provider.QueueResult(validGenerateResult(
		"First validated trivia question about a classic anime",
		"Second validated trivia question about a different anime",
		"Third validated trivia question rounding out the batch",
	))
	if _, err := w.RunGeneration(ctx, feed, spec); err != nil {
		t.Fatalf("RunGeneration: %v", err)
	}

	for _, format := range []string{"xml", "atom", "json"} {
		resp := w.FetchFeed(t, feed.Slug, format)
		defer resp.Body.Close()
		body := readAll(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: GET status = %d, want 200", format, resp.StatusCode)
		}

		var findings []feedvalidate.Finding
		switch format {
		case "xml":
			findings = feedvalidate.RSS(body)
		case "atom":
			findings = feedvalidate.Atom(body)
		case "json":
			findings = feedvalidate.JSONFeed(body)
		}
		// BF-44 (§22 J10): the feed validates with zero findings — errors
		// AND warnings — in all three formats.
		if len(findings) != 0 {
			t.Fatalf("%s: %d validator findings, want 0:\n%s", format, len(findings), findingsString(findings))
		}
	}
}

// readAll reads and closes resp.Body's remaining bytes — resp.Body itself
// stays valid to defer-Close after this returns, matching http.Response's
// contract (a second Close on an already-drained body is a no-op).
func readAll(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return buf
}

// TestJ10_PubDatesUniqueAndStrictlyDecreasing is BF-45, asserted directly
// against the store's own ordering (ListItems's contract, items.go) rather
// than re-deriving it from parsed XML — feedvalidate.RSS already checks the
// RENDERED §5.5 ordering rule as part of BF-44 above; this test independently
// confirms the underlying data the renderer works from actually has that
// property, so the two checks cannot both be fooled by the same rendering
// bug.
func TestJ10_PubDatesUniqueAndStrictlyDecreasing(t *testing.T) {
	w := New(t)
	ctx := t.Context()

	feed, err := w.CreateFeed(ctx, publishableFeed("j10-pubdates"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}

	spec := manualSpec()
	spec.ItemsPerRun = 4
	w.Provider.QueueResult(validGenerateResult(
		"Pubdate ordering item number one today",
		"Pubdate ordering item number two today",
		"Pubdate ordering item number three today",
		"Pubdate ordering item number four today",
	))
	if _, err := w.RunGeneration(ctx, feed, spec); err != nil {
		t.Fatalf("RunGeneration: %v", err)
	}

	items, err := w.Store.ListItems(ctx, feed.ID, 0, false)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) < 2 {
		t.Fatalf("only %d items, need at least 2 to prove strict ordering", len(items))
	}

	// BF-45 (§22 J10): every item has a unique, strictly decreasing
	// PublishedAt (ListItems is documented newest-first, items.go).
	seen := map[time.Time]bool{}
	for i, it := range items {
		if seen[it.PublishedAt] {
			t.Fatalf("duplicate published_at %v", it.PublishedAt)
		}
		seen[it.PublishedAt] = true
		if i > 0 && !items[i-1].PublishedAt.After(it.PublishedAt) {
			t.Fatalf("items[%d].PublishedAt %v is not strictly after items[%d].PublishedAt %v",
				i-1, items[i-1].PublishedAt, i, it.PublishedAt)
		}
	}
}

// TestJ10_ItemDeliveredExactlyOnceAcrossTwoPollingCycles is BF-46 — per the
// task brief, the single most load-bearing test in this file.
//
// Why two Worlds sharing one on-disk store rather than two polls against one
// running World: this package's publish.Cache (internal/publish/cache.go)
// caches a rendered feed document FOREVER once populated — nothing in
// World's construction (harness.go) or anywhere reachable from flowtest ever
// calls Cache.Invalidate, because that wiring belongs to the not-yet-built
// control plane (TODOS.md B1). TestJ4_RenderCacheInvalidationIsUnwired
// (j4_promote_test.go) already names this exact gap with a t.Skip. Polling
// the SAME running server twice would therefore prove nothing about
// delivery — the second response would be byte-identical to the first
// regardless of what got generated in between, which is a cache bug, not a
// pass. Two Worlds opened against the SAME sqlite file (via NewWithDir, a
// harness.go entry point built for exactly this — reusing a caller-owned
// directory) is a fresh cache each poll while the underlying feed state
// persists across both, which is what a subscriber's client actually
// observes across two real polls of a long-lived service: whatever changed
// on the server between requests, not whatever one particular in-memory
// cache instance happened to still be holding.
func TestJ10_ItemDeliveredExactlyOnceAcrossTwoPollingCycles(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	worldA, err := NewWithDir(t, dir)
	if err != nil {
		t.Fatalf("NewWithDir (cycle 1): %v", err)
	}
	// defer, not just the explicit Close below: t.Fatalf runs deferred calls
	// (via runtime.Goexit) before unwinding, so this still releases the
	// store's file handles if an assertion below fails mid-test — otherwise
	// t.TempDir's own cleanup fails to remove a file a crashed-out test left
	// locked (exactly what an earlier draft of this test hit). World.Close is
	// safe to call twice.
	defer worldA.Close()

	feed, err := worldA.CreateFeed(ctx, publishableFeed("j10-two-cycles"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}

	spec := manualSpec()
	spec.ItemsPerRun = 2
	worldA.Provider.QueueResult(validGenerateResult(
		"Cycle one item alpha delivered on the first poll",
		"Cycle one item beta delivered on the first poll",
	))
	round1, err := worldA.RunGeneration(ctx, feed, spec)
	if err != nil {
		t.Fatalf("RunGeneration (round 1): %v", err)
	}
	if len(round1.Items) != 2 {
		t.Fatalf("round 1 produced %d items, want 2", len(round1.Items))
	}

	poll1 := readAll(t, worldA.FetchFeed(t, feed.Slug, "xml"))
	worldA.Close()

	worldB, err := NewWithDir(t, dir)
	if err != nil {
		t.Fatalf("NewWithDir (cycle 2): %v", err)
	}
	defer worldB.Close()

	feed2, err := worldB.Store.GetFeedBySlug(ctx, feed.Slug)
	if err != nil {
		t.Fatalf("re-resolving feed in cycle 2: %v", err)
	}

	spec2 := manualSpec()
	spec2.ItemsPerRun = 2
	worldB.Provider.QueueResult(validGenerateResult(
		"Cycle two item gamma delivered only on the second poll",
		"Cycle two item delta delivered only on the second poll",
	))
	round2, err := worldB.RunGeneration(ctx, feed2, spec2)
	if err != nil {
		t.Fatalf("RunGeneration (round 2): %v", err)
	}
	if len(round2.Items) != 2 {
		t.Fatalf("round 2 produced %d items, want 2", len(round2.Items))
	}

	poll2 := readAll(t, worldB.FetchFeed(t, feed2.Slug, "xml"))

	// BF-46 (§22 J10, §17.5): every round-1 item is present in BOTH polls
	// (delivered, and not lost across the second cycle)...
	for _, it := range round1.Items {
		if !strings.Contains(string(poll1), it.ItemKey) {
			t.Fatalf("round 1 item %q missing from poll 1", it.ItemKey)
		}
		if !strings.Contains(string(poll2), it.ItemKey) {
			t.Fatalf("round 1 item %q missing from poll 2 — an item must never disappear across polls", it.ItemKey)
		}
	}
	// ...every round-2 item is absent from poll 1 (it did not exist yet) and
	// present in poll 2 (newly delivered)...
	for _, it := range round2.Items {
		if strings.Contains(string(poll1), it.ItemKey) {
			t.Fatalf("round 2 item %q already present in poll 1, before it was generated", it.ItemKey)
		}
		if !strings.Contains(string(poll2), it.ItemKey) {
			t.Fatalf("round 2 item %q missing from poll 2 — a new item must be delivered", it.ItemKey)
		}
	}
	// ...and every item — from either round — appears EXACTLY ONCE within
	// poll 2's single document, which is the "exactly once" half of BF-46
	// that a pure subset check above cannot catch (a renderer bug could
	// duplicate an entry and every containment check above would still
	// pass).
	for _, it := range append(append([]model.Item{}, round1.Items...), round2.Items...) {
		if n := strings.Count(string(poll2), it.ItemKey); n != 1 {
			t.Fatalf("item %q appears %d times in poll 2, want exactly 1", it.ItemKey, n)
		}
	}
}

// countingDeps wraps a *World's real store reads in publish.Deps that also
// count backend calls, independent of World.publishDeps (harness.go) which
// exposes no counters. Used only by TestJ10_UnchangedFeedReturns304 below.
type countingDeps struct {
	w              *World
	getFeedCalls   atomic.Int64
	listItemsCalls atomic.Int64
}

func (c *countingDeps) deps() publish.Deps {
	return publish.Deps{
		GetFeed: func(ctx context.Context, slug string) (model.Feed, bool, error) {
			c.getFeedCalls.Add(1)
			f, err := c.w.Store.GetFeedBySlug(ctx, slug)
			if errors.Is(err, store.ErrNotFound) {
				return model.Feed{}, false, nil
			}
			if err != nil {
				return model.Feed{}, false, err
			}
			return f, true, nil
		},
		ListItems: func(ctx context.Context, feedID int64) ([]model.Item, error) {
			c.listItemsCalls.Add(1)
			return c.w.Store.ListItems(ctx, feedID, 0, false)
		},
		ListFeeds: func(ctx context.Context) ([]model.Feed, error) { return nil, nil },
		GetItem: func(ctx context.Context, itemKey string) (model.Feed, model.Item, bool, error) {
			return model.Feed{}, model.Item{}, false, nil
		},
		BaseURL:   c.w.BaseURL,
		Generator: "AnimeFeedFlux flowtest (counting)",
		DocsURL:   "https://www.rssboard.org/rss-specification",
		TagYear:   2026,
		Now:       c.w.Clock.Now,
	}
}

// TestJ10_UnchangedFeedReturns304AndDoesNotReRender is BF-47. It stands up
// its own httptest server over publish.NewServer (the real, unmodified
// handler World itself uses) wired to counting Deps, so "does not touch
// SQLite or the LLM" is checked as a literal call count rather than inferred
// from response timing.
func TestJ10_UnchangedFeedReturns304AndDoesNotReRender(t *testing.T) {
	w := New(t)
	ctx := t.Context()

	feed, err := w.CreateFeed(ctx, publishableFeed("j10-304"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}
	w.Provider.QueueResult(validGenerateResult("The only item behind the 304 check"))
	if _, err := w.RunGeneration(ctx, feed, manualSpec()); err != nil {
		t.Fatalf("RunGeneration: %v", err)
	}

	cd := &countingDeps{w: w}
	srv := httptest.NewServer(publish.NewServer(cd.deps()))
	defer srv.Close()

	url := srv.URL + "/feeds/" + feed.Slug + ".xml"
	resp1, err := srv.Client().Get(url)
	if err != nil {
		t.Fatalf("GET (populate cache): %v", err)
	}
	etag := resp1.Header.Get("ETag")
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("first GET status = %d, want 200", resp1.StatusCode)
	}
	if etag == "" {
		t.Fatal("first response carried no ETag")
	}

	afterFirst := cd.getFeedCalls.Load()
	afterFirstItems := cd.listItemsCalls.Load()

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("building conditional request: %v", err)
	}
	req.Header.Set("If-None-Match", etag)
	resp2, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("conditional GET: %v", err)
	}
	defer resp2.Body.Close()

	// BF-47 (§22 J10): an unchanged feed answers 304...
	if resp2.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional GET status = %d, want 304", resp2.StatusCode)
	}
	// ...and does not re-render — the cache HIT path (server.go's writeEntry)
	// returns before ever calling Deps.GetFeed/Deps.ListItems again, so
	// neither counter should have moved past the first request.
	if got := cd.getFeedCalls.Load(); got != afterFirst {
		t.Fatalf("GetFeed was called %d more time(s) on the 304 path, want 0", got-afterFirst)
	}
	if got := cd.listItemsCalls.Load(); got != afterFirstItems {
		t.Fatalf("ListItems was called %d more time(s) on the 304 path, want 0", got-afterFirstItems)
	}
}

// TestJ10_DeletedItemPermalinkReturns410NeverNotFound is BF-48.
func TestJ10_DeletedItemPermalinkReturns410NeverNotFound(t *testing.T) {
	w := New(t)
	ctx := t.Context()

	feed, err := w.CreateFeed(ctx, publishableFeed("j10-410"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}
	item := insertItem(t, w, feed, "An item that will be deleted", w.Clock.Now(), model.OriginGenerated)

	if err := w.Store.SoftDeleteItem(ctx, item.ItemKey); err != nil {
		t.Fatalf("SoftDeleteItem: %v", err)
	}

	resp, err := w.httpServer.Client().Get(w.BaseURL + "/items/" + item.ItemKey)
	if err != nil {
		t.Fatalf("GET deleted item's permalink: %v", err)
	}
	defer resp.Body.Close()

	// BF-48 (§22 J10): a deleted item's permalink returns 410, never 404.
	if resp.StatusCode != http.StatusGone {
		t.Fatalf("GET deleted permalink = %d, want 410", resp.StatusCode)
	}
}

// TestJ10_TriviaAnswerNeverInDescriptionOrOGDescription is BF-49.
func TestJ10_TriviaAnswerNeverInDescriptionOrOGDescription(t *testing.T) {
	w := New(t)
	ctx := t.Context()

	feed, err := w.CreateFeed(ctx, publishableFeed("j10-no-spoiler"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}

	const answerText = "Cowboy Bebop"
	now := w.Clock.Now()
	item := model.Item{
		FeedID:      feed.ID,
		ItemKey:     w.IDs.NewItemKey(now),
		ContentHash: "j10-trivia-answer",
		Title:       "Which anime popularized jazz-inspired soundtracks",
		SummaryText: "A short plain-text summary that never names the answer.",
		BodyHTML:    "<p>Question body.</p>",
		AnswerHTML:  "<p>" + answerText + "</p>",
		PublishedAt: now,
		Origin:      model.OriginGenerated,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if _, err := w.Store.InsertItem(ctx, item); err != nil {
		t.Fatalf("InsertItem: %v", err)
	}

	xmlBody := readAll(t, w.FetchFeed(t, feed.Slug, "xml"))
	atomBody := readAll(t, w.FetchFeed(t, feed.Slug, "atom"))
	jsonBody := readAll(t, w.FetchFeed(t, feed.Slug, "json"))

	// BF-49 (§22 J10): no trivia answer appears in description or
	// og:description. This deliberately checks only the preview-safe fields
	// (RSS <description>, Atom <summary>, JSON Feed "summary") and NOT the
	// whole document: content:encoded/content/content_html legitimately
	// carry AnswerHTML by design (rss.go's contentEncoded, "the only place
	// AnswerHTML is written"), so asserting the answer is absent from the
	// entire body would fail on correct output, not catch a real leak.
	if got := rssDescriptions(t, xmlBody); strings.Contains(got, answerText) {
		t.Fatalf("RSS <description> leaks the trivia answer %q: %s", answerText, got)
	}
	if got := atomSummaries(t, atomBody); strings.Contains(got, answerText) {
		t.Fatalf("Atom <summary> leaks the trivia answer %q: %s", answerText, got)
	}
	if got := jsonSummaries(t, jsonBody); strings.Contains(got, answerText) {
		t.Fatalf("JSON Feed \"summary\" leaks the trivia answer %q: %s", answerText, got)
	}

	resp, err := w.httpServer.Client().Get(w.BaseURL + "/items/" + item.ItemKey)
	if err != nil {
		t.Fatalf("GET permalink: %v", err)
	}
	defer resp.Body.Close()
	permalinkBody := readAll(t, resp)
	// The permalink page DOES legitimately show the answer in the article
	// body (that's the point of visiting it) — the leak this guards against
	// is specifically the <meta name="description">/og:description preview
	// tags, so this checks those tags' content, not the whole page.
	for _, meta := range []string{`name="description" content="`, `property="og:description" content="`} {
		idx := strings.Index(string(permalinkBody), meta)
		if idx == -1 {
			t.Fatalf("permalink page is missing the %q meta tag", meta)
		}
		rest := string(permalinkBody)[idx+len(meta):]
		end := strings.Index(rest, `"`)
		if end == -1 {
			t.Fatalf("could not find the closing quote for %q", meta)
		}
		if strings.Contains(rest[:end], answerText) {
			t.Fatalf("%q meta tag leaks the trivia answer %q", meta, answerText)
		}
	}
}

// TestJ10_EditedItemNotRedeliveredButCorrectionIs is BF-50 — the subscriber-
// facing half of the same claim TestJ6_PlainEditProducesNoNewGuidAndNoRedelivery
// (j6_correction_test.go) proves at the store level; this test checks it the
// way §17.5 requires J10 to be checked: over real HTTP, before and after
// each change.
//
// Like TestJ10_ItemDeliveredExactlyOnceAcrossTwoPollingCycles above, each
// poll here uses its own World (fresh cache) against one shared on-disk
// store, for the same reason: World's publish.Cache is never invalidated by
// a write anywhere reachable from this package (TestJ4_RenderCacheInvalidationIsUnwired,
// j4_promote_test.go, names the exact gap), so polling one running World
// three times in a row would just keep re-serving poll 1's cached bytes and
// prove nothing about the edit or the correction.
func TestJ10_EditedItemNotRedeliveredButCorrectionIs(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	w1, err := NewWithDir(t, dir)
	if err != nil {
		t.Fatalf("NewWithDir (poll 1): %v", err)
	}
	defer w1.Close() // see the matching comment in the two-cycles test above

	feed, err := w1.CreateFeed(ctx, publishableFeed("j10-edit-vs-correction"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}
	original := insertItem(t, w1, feed, "An item that will be edited, not corrected", w1.Clock.Now(), model.OriginGenerated)

	before := readAll(t, w1.FetchFeed(t, feed.Slug, "xml"))
	if n := strings.Count(string(before), original.ItemKey); n != 1 {
		t.Fatalf("original item appears %d times before any change, want 1", n)
	}
	w1.Close()

	w2, err := NewWithDir(t, dir)
	if err != nil {
		t.Fatalf("NewWithDir (poll 2, after edit): %v", err)
	}
	defer w2.Close()

	edited := original
	edited.Title = "The same item, title fixed by a plain edit"
	if err := w2.Store.UpdateItem(ctx, edited, original.Version); err != nil {
		t.Fatalf("UpdateItem: %v", err)
	}

	afterEdit := readAll(t, w2.FetchFeed(t, feed.Slug, "xml"))
	// BF-50, edit half: an edited item is not redelivered — still exactly
	// one entry under the SAME guid, no second entry appeared.
	if n := strings.Count(string(afterEdit), original.ItemKey); n != 1 {
		t.Fatalf("edited item appears %d times after the edit, want 1 (still)", n)
	}
	if !strings.Contains(string(afterEdit), "title fixed by a plain edit") {
		t.Fatal("the edit's new title never made it into the served document")
	}
	w2.Close()

	w3, err := NewWithDir(t, dir)
	if err != nil {
		t.Fatalf("NewWithDir (poll 3, after correction): %v", err)
	}
	defer w3.Close()
	w3.Clock.Advance(time.Hour)
	correction := insertItem(t, w3, feed, "A correction that DOES get delivered", w3.Clock.Now(), model.OriginCorrection)
	linkCorrection(t, w3, correction.ID, original.ID)

	afterCorrection := readAll(t, w3.FetchFeed(t, feed.Slug, "xml"))
	// BF-50, correction half: a correction IS delivered — a new entry, under
	// its own new guid, alongside the still-present original.
	if n := strings.Count(string(afterCorrection), correction.ItemKey); n != 1 {
		t.Fatalf("correction appears %d times after being published, want exactly 1", n)
	}
	if n := strings.Count(string(afterCorrection), original.ItemKey); n != 1 {
		t.Fatalf("original still appears %d times after a correction was published, want 1", n)
	}
}

// TestJ10_BackdatedItemRefused is BF-51. There is no dedicated "reject a
// backdated write" error path anywhere in this codebase to assert against
// (see store.PromoteSample's doc comment, samples.go) — instead, PLAN.md
// §5.5's no-backdating rule is enforced by construction: the promote/publish
// path never trusts a caller-supplied published_at, it always clamps to
// strictly after the feed's current newest item. This test drives that path
// with a deliberately backdated candidate and asserts the STORED result
// was NOT backdated — the promotion silently overriding an attempt to
// backdate an item IS the refusal §22 J10 describes ("delivered to nobody,
// which is why creating one is blocked").
func TestJ10_BackdatedItemRefused(t *testing.T) {
	w := New(t)
	ctx := t.Context()

	feed, err := w.CreateFeed(ctx, publishableFeed("j10-backdated"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}

	newest := insertItem(t, w, feed, "The feed's current newest item", w.Clock.Now(), model.OriginGenerated)

	w.Provider.QueueResult(validGenerateResult("A candidate item stamped with a backdated timestamp"))
	outcome, err := w.Sample(ctx, feed, validSampleSpec())
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	candidate := w.StampForPromotion(outcome.Result.Items[0], feed)
	// Deliberately backdated: a full year before the feed's current newest
	// item. StampForPromotion (harness.go) never sets PublishedAt itself —
	// this simulates a caller (or a compromised admin session) trying to
	// force one anyway.
	candidate.PublishedAt = newest.PublishedAt.Add(-365 * 24 * time.Hour)

	if _, err := w.PromoteSample(ctx, outcome.SampleID, candidate); err != nil {
		t.Fatalf("PromoteSample: %v", err)
	}

	stored, err := w.Store.GetItem(ctx, candidate.ItemKey)
	if err != nil {
		t.Fatalf("reading back the promoted item: %v", err)
	}

	// BF-51 (§22 J10): a backdated item is refused — the promote path
	// overrode the requested backdated stamp rather than honoring it.
	if !stored.PublishedAt.After(newest.PublishedAt) {
		t.Fatalf("promoted published_at %v is not after the feed's newest %v — the backdate attempt was not refused",
			stored.PublishedAt, newest.PublishedAt)
	}
	if stored.PublishedAt.Equal(candidate.PublishedAt) {
		t.Fatalf("promoted published_at %v equals the requested backdated stamp — it was honored instead of refused", stored.PublishedAt)
	}

	// And the practical consequence the sanity clause names: the backdated
	// item, being newer-stamped than the existing item, still sorts
	// correctly and is delivered like any other item — it is the BACKDATE
	// that was refused, not the item itself.
	body := readAll(t, w.FetchFeed(t, feed.Slug, "xml"))
	if !strings.Contains(string(body), stored.ItemKey) {
		t.Fatal("the (correctly re-stamped) item is missing from the served feed")
	}
}
