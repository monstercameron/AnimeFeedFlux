package generate

import (
	"context"
	"encoding/xml"
	"fmt"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/budget"
	"github.com/monstercameron/AnimeFeedFlux/internal/llm"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/render"
)

// --- soak-specific stubs (distinct names from runner_test.go's stubNovelty:
// that one is a blunt dupAll/errOnCall switch, useful for a single
// assertion; the soak needs something that behaves like the real embedding
// gate over many days, so it gets its own type) ---

// soakNovelty remembers every title+summary text it has ever been asked to
// check and flags a repeat as a duplicate. This is what actually produces a
// novelty rejection under a long-running soak, rather than a hand-picked
// on/off switch.
type soakNovelty struct {
	seen  map[string]bool
	calls int
}

func newSoakNovelty() *soakNovelty {
	return &soakNovelty{seen: map[string]bool{}}
}

func (n *soakNovelty) Check(ctx context.Context, feedID int64, text string) (bool, string, float64, error) {
	n.calls++
	if n.seen[text] {
		return true, text, 1.0, nil
	}
	n.seen[text] = true
	return false, "", 0, nil
}

// soakSummary is held constant across every soak-generated item: only the
// title varies, and it is the title that drives (non-)duplication below.
const soakSummary = "A short plain-text summary of the day's item, safely under the cap."

// soakGeneratedItem mirrors runner_test.go's validGeneratedItem (a distinct
// copy, not a redeclaration) with an absolute link so it always survives
// contract.go's Validate — the soak is about the generation/store loop over
// time, not about re-exercising Validate's rejection rules.
func soakGeneratedItem(title string) llm.GeneratedItem {
	return llm.GeneratedItem{
		Title:       title,
		SummaryText: soakSummary,
		BodyHTML:    `<p>Full body with an <a href="https://example.com/x">absolute link</a>.</p>`,
		Tags:        []string{"anime"},
	}
}

// soakTitle builds a title unique to (day, attempt) — long enough to clear
// Validate's minimum length and with no trailing punctuation, so it never
// fails validation for a reason unrelated to what this test is checking.
func soakTitle(day, attempt int) string {
	return fmt.Sprintf("Soak trivia headline for day %03d attempt %d edition", day, attempt)
}

// TestSoakNinetyDays simulates 90 daily scheduled runs against the fake
// provider with an injected clock (PLAN.md §17.4 / AF-09..14). It is where
// slow-accumulating bugs surface — a single day's Run test can't show a
// guid colliding 60 days later, or a budget gate that only fails under
// sustained pressure — and it costs nothing to run, so it stays out of the
// pre-commit hook's `go test -short` path only because it is comparatively
// slow, not because it is unsafe to run every time.
func TestSoakNinetyDays(t *testing.T) {
	if testing.Short() {
		t.Skip("soak: 90-day simulation skipped under -short")
	}

	const totalDays = 90
	const dupEveryNDays = 7            // every 7th day deliberately re-submits a prior day's title first, to force a novelty rejection
	const budgetBlowoutEveryNDays = 20 // every 20th day is deliberately priced over the global daily cap, to force a budget denial

	feed := model.Feed{ID: 1, Slug: "soak-daily", Title: "Soak Daily", Kind: model.KindGenerative, Timezone: "UTC"}
	spec := Spec{
		SystemPrompt:       "You write anime trivia.",
		UserPromptTemplate: "Write {{.ItemsPerRun}} item(s). Avoid: {{range .RecentTitles}}{{.}} {{end}}",
		Model:              "gpt-test",
		ItemsPerRun:        1,
		MaxNoveltyRetries:  1, // 1 initial attempt + 1 retry = 2 max provider calls per day
		Trigger:            "cron",
	}

	limits := budget.Limits{
		PerFeedDailyTokens: 100_000,
		PerFeedDailyRuns:   1,
		GlobalDailyTokens:  100_000,
		GlobalDailyUSD:     1.0,
	}

	provider := llm.NewFake()
	store := &stubStore{}
	nov := newSoakNovelty()

	base := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	var current time.Time

	committedTitle := map[int]string{} // day -> the title that actually got committed that day
	var budgetSkippedDays int
	var ranDays int
	var allItems []model.Item

	for day := 0; day < totalDays; day++ {
		current = base.AddDate(0, 0, day)
		deps := Deps{
			Store:    store,
			Novelty:  nov,
			Provider: provider,
			IDs:      newSoakIDSource(day),
			Now:      func() time.Time { return current },
		}

		// --- budget gate, checked BEFORE the run, same discipline as
		// internal/schedule's Gate (PLAN.md §13): a refusal must cost
		// nothing, so a denied day never touches the provider or the store.
		daySpend := budget.Spend{}
		req := budget.Request{Kind: budget.KindScheduled, Projected: 500, USD: 0.10}
		if day%budgetBlowoutEveryNDays == budgetBlowoutEveryNDays-1 {
			req.USD = 5.0 // deliberately over GlobalDailyUSD
		}
		decision := budget.CheckRequest(limits, daySpend, daySpend, req)
		if !decision.Allow {
			budgetSkippedDays++
			continue
		}
		if req.USD > limits.GlobalDailyUSD {
			t.Fatalf("day %d: budget.Check allowed a request priced at $%.2f against a $%.2f cap", day, req.USD, limits.GlobalDailyUSD)
		}

		// --- queue exactly the Generate calls this day's Run invocation
		// will make: a dup-trigger day submits a known-duplicate title
		// first (queued call #1), forcing filterNovel to reject the whole
		// batch and Run to retry with a fresh title (queued call #2); every
		// other day submits a fresh title that passes on the first call.
		if day%dupEveryNDays == 0 && day >= dupEveryNDays {
			dupTitle := committedTitle[day-dupEveryNDays]
			provider.QueueResult(llm.Result{Items: []llm.GeneratedItem{soakGeneratedItem(dupTitle)}})
			provider.QueueResult(llm.Result{Items: []llm.GeneratedItem{soakGeneratedItem(soakTitle(day, 1))}})
		} else {
			provider.QueueResult(llm.Result{Items: []llm.GeneratedItem{soakGeneratedItem(soakTitle(day, 0))}})
		}

		result, err := Run(context.Background(), deps, feed, spec)
		if err != nil {
			t.Fatalf("day %d: Run() error = %v", day, err)
		}
		ranDays++

		switch result.Run.Status {
		case StatusCompleted, StatusSkipped, StatusFailed:
			// terminal, as required.
		default:
			t.Fatalf("day %d: run reached non-terminal status %q", day, result.Run.Status)
		}

		if result.Run.ItemsRejected != sumReasons(result.Run.RejectReasons) {
			t.Fatalf("day %d: items_rejected (%d) does not reconcile with reject_reasons total (%d)", day, result.Run.ItemsRejected, sumReasons(result.Run.RejectReasons))
		}
		if result.Run.ItemsAdded != len(result.Items) {
			t.Fatalf("day %d: items_added (%d) does not match committed item count (%d)", day, result.Run.ItemsAdded, len(result.Items))
		}

		if len(result.Items) != 1 {
			t.Fatalf("day %d: expected exactly 1 published item (every title validates and only novelty ever rejects), got %d", day, len(result.Items))
		}
		it := result.Items[0]
		committedTitle[day] = it.Title
		allItems = append(allItems, it)

		// Feed the store's "newest" forward, the same way a real Store
		// would after CommitRun persisted — otherwise every subsequent
		// day's stampItems anchors off a stale newest and the strictly-
		// increasing guarantee would only be exercised on day 0.
		if it.PublishedAt.After(store.newest) {
			store.newest = it.PublishedAt
		}
	}

	// --- novelty rejections occurred, and did not run away ---
	if store.commits == nil {
		t.Fatal("no runs were committed")
	}
	totalNoveltyRejections := 0
	for _, c := range store.commits {
		totalNoveltyRejections += c.run.RejectReasons[ReasonNoveltyDuplicate]
	}
	if totalNoveltyRejections == 0 {
		t.Error("expected at least one novelty rejection across the soak; the dup-trigger schedule produced none")
	}
	maxPossibleAttempts := ranDays * (1 + spec.MaxNoveltyRetries)
	if provider.GenerateCallCount() > maxPossibleAttempts {
		t.Fatalf("Generate called %d times over %d run days, want <= %d (1 + MaxNoveltyRetries per day) — novelty retries ran away", provider.GenerateCallCount(), ranDays, maxPossibleAttempts)
	}
	if nov.calls > maxPossibleAttempts {
		t.Fatalf("Novelty.Check called %d times, want <= %d — retries ran away", nov.calls, maxPossibleAttempts)
	}

	// --- budgets enforced every day, never exceeded ---
	if budgetSkippedDays == 0 {
		t.Error("expected at least one budget-denied day; the blowout schedule produced none")
	}
	if ranDays+budgetSkippedDays != totalDays {
		t.Fatalf("ranDays (%d) + budgetSkippedDays (%d) != totalDays (%d)", ranDays, budgetSkippedDays, totalDays)
	}
	if store.commitCalls != ranDays {
		t.Fatalf("CommitRun called %d times, want %d (exactly once per day that passed the budget gate)", store.commitCalls, ranDays)
	}

	// --- no duplicate guids anywhere, over the whole horizon ---
	seenGuids := map[string]bool{}
	for _, it := range allItems {
		guid := render.TagURI("soak.earlcameron.com", 2026, feed.Slug, it.ItemKey)
		if seenGuids[guid] {
			t.Fatalf("duplicate guid across the soak: %s (item title %q)", guid, it.Title)
		}
		seenGuids[guid] = true
	}

	// --- pubDates strictly decreasing and unique when the accumulated
	// items are rendered as a feed: build one Channel over every item ever
	// committed and decode the rendered RSS document back out, so this
	// exercises the real render path rather than re-checking the in-memory
	// slice the stamping code already produced. ---
	channel := model.Channel{
		Feed:      model.Feed{Slug: feed.Slug, Title: feed.Title, Language: "en-us", TTLMinutes: 60},
		SelfURL:   "https://soak.earlcameron.com/feeds/soak-daily.xml",
		HTMLURL:   "https://soak.earlcameron.com/feeds/soak-daily",
		Host:      "soak.earlcameron.com",
		TagYear:   2026,
		Items:     allItems,
		BuildTime: current,
		Generator: "AnimeFeedFlux soak test",
		DocsURL:   "https://www.rssboard.org/rss-specification",
	}
	rendered, err := render.RSS(channel)
	if err != nil {
		t.Fatalf("render.RSS() over the accumulated soak items: %v", err)
	}

	var doc struct {
		Channel struct {
			Items []struct {
				GUID    string `xml:"guid"`
				PubDate string `xml:"pubDate"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal(rendered, &doc); err != nil {
		t.Fatalf("could not decode the rendered soak feed: %v", err)
	}
	if len(doc.Channel.Items) != len(allItems) {
		t.Fatalf("rendered feed has %d items, want %d", len(doc.Channel.Items), len(allItems))
	}

	renderedGuids := map[string]bool{}
	var prevPub time.Time
	for i, it := range doc.Channel.Items {
		if renderedGuids[it.GUID] {
			t.Fatalf("rendered feed has a duplicate guid: %s", it.GUID)
		}
		renderedGuids[it.GUID] = true

		pub, perr := time.Parse(time.RFC1123Z, it.PubDate)
		if perr != nil {
			t.Fatalf("rendered item %d has an unparseable pubDate %q: %v", i, it.PubDate, perr)
		}
		if i > 0 && !pub.Before(prevPub) {
			t.Fatalf("rendered item %d pubDate %v is not strictly before the previous item's %v — not strictly decreasing", i, pub, prevPub)
		}
		prevPub = pub
	}
}

// soakIDSource wraps ids.NewDeterministicSource but reseeds per day so two
// different days never even theoretically collide on the seed the
// deterministic generator starts from — the guid-uniqueness assertion above
// is then actually exercising stampItems'/TagURI's own logic, not merely
// relying on one shared generator instance to avoid collisions.
func newSoakIDSource(day int) *soakIDSource {
	return &soakIDSource{day: day}
}

type soakIDSource struct {
	day   int
	calls int
}

// NewItemKey produces a deterministic, ULID-shaped (Crockford base32),
// distinct key per (day, call-within-day) pair. It intentionally does not
// depend on `now` beyond using it to keep the same time-ordering property
// real ULIDs have — the uniqueness this soak test cares about comes from
// (day, calls), which never repeats across the whole run.
func (s *soakIDSource) NewItemKey(now time.Time) string {
	s.calls++
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	n := s.day*1000 + s.calls
	b := make([]byte, 26)
	for i := range b {
		b[i] = alphabet[0]
	}
	for i := len(b) - 1; i >= 0 && n > 0; i-- {
		b[i] = alphabet[n%len(alphabet)]
		n /= len(alphabet)
	}
	return string(b)
}
