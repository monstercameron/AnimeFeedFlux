// Command affseed is a LOCAL-ONLY, DEV-ONLY seeder. It exists solely to
// populate an otherwise-empty development database (PLAN.md's dev loop
// leaves `.devrun/aff.db` with an admin account and nothing else) so the
// admin UI and the published feeds have something real to look at.
//
// It is deliberately NOT wired into any production path: it is a new
// package, cmd/affseed, with its own main, and nothing in the rest of the
// repository imports it.
//
// Every write here goes through the same code the production admin UI and
// CLI use — internal/store's *Store methods for feeds, items and runs, and
// internal/rpc's ItemServer/FeedServer for the writes that carry extra
// invariants (item_revisions, corrections) that internal/store does not
// expose a method for. Nothing in this file hand-writes an INSERT against
// feeds/items/runs/item_revisions/corrections — those tables' invariants
// (guid derivation from an opaque ULID, the UNIQUE(feed_id, published_at)
// ordering rule, content hashing, the run+items atomic commit) live in
// store/rpc, not here, and re-deriving them by hand is exactly how a seeder
// produces a database that looks right and behaves wrong.
//
// Safety:
//   - Refuses to run against a database that already has feeds, unless
//     --force is given. Losing a working admin account or clobbering real
//     feed data to a seeder would be a self-inflicted lockout.
//   - Never touches the `admin` table, directly or indirectly: no function
//     in this file calls any admin-related *Store method.
//   - Every seeded string is plainly placeholder text ("(seed data)", a
//     fictional studio name that does not exist) so a seeded item can never
//     be mistaken for something actually published.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"time"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/ids"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/publish"
	"github.com/monstercameron/AnimeFeedFlux/internal/rpc"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"

	"google.golang.org/protobuf/types/known/timestamppb"
)

func main() {
	os.Exit(run())
}

func run() int {
	dbPath := flag.String("db", os.Getenv("AFF_DB_PATH"), "path to the SQLite database file (local-only, dev-only)")
	force := flag.Bool("force", false, "seed even if the database already has feeds")
	flag.Parse()

	if *dbPath == "" {
		fmt.Fprintln(os.Stderr, "affseed: --db or AFF_DB_PATH is required; this tool is local-only and needs "+
			"direct filesystem access to the SQLite file (see cmd/aff/admin_cmd.go's identical rule)")
		return 1
	}

	ctx := context.Background()
	st, err := store.Open(ctx, store.Options{Path: *dbPath, Log: slog.New(slog.DiscardHandler)})
	if err != nil {
		fmt.Fprintf(os.Stderr, "affseed: opening database at %q: %v\n", *dbPath, err)
		return 1
	}
	defer func() { _ = st.Close() }()

	if _, err := st.Migrate(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "affseed: migrating database: %v\n", err)
		return 1
	}

	var feedCount int
	if err := st.Writer().QueryRowContext(ctx, `SELECT COUNT(1) FROM feeds`).Scan(&feedCount); err != nil {
		fmt.Fprintf(os.Stderr, "affseed: checking for existing feeds: %v\n", err)
		return 1
	}
	if feedCount > 0 && !*force {
		fmt.Fprintf(os.Stderr, "affseed: database at %q already has %d feed(s); refusing to seed on top of "+
			"existing data without --force (this tool never touches the admin table, but it will happily add "+
			"more feeds/items/runs unless told to stop)\n", *dbPath, feedCount)
		return 1
	}

	inv := noopInvalidator{}
	idSrc := ids.NewSource()
	feedSrv := rpc.NewFeedServer(st, inv, nil)
	itemSrv := rpc.NewItemServer(st, inv, idSrc)

	seedNow := time.Now().UTC()

	fmt.Println("affseed: seeding feeds, items and runs (all content is placeholder/fictional)...")

	summary, err := seedAll(ctx, st, feedSrv, itemSrv, idSrc, seedNow)
	if err != nil {
		fmt.Fprintf(os.Stderr, "affseed: %v\n", err)
		return 1
	}

	summary.print()
	return 0
}

// noopInvalidator satisfies publish.Invalidator without touching the live
// publish plane's in-memory cache: affseed runs as a separate process from
// the dev server, so there is no shared *Cache to invalidate here anyway —
// the running server's cache simply has no entry yet for a slug that did
// not exist before this ran, so the next request is a natural cache miss
// that reads the fresh rows straight from SQLite.
type noopInvalidator struct{}

func (noopInvalidator) InvalidateFeed(string) {}
func (noopInvalidator) InvalidateAll()        {}

var _ publish.Invalidator = noopInvalidator{}

// seedSummary is what gets printed at the end, so a run of affseed reports
// exactly what it did.
type seedSummary struct {
	feeds []feedSummary
}

type feedSummary struct {
	slug          string
	kind          string
	itemsAdded    int
	runsSucceeded int
	runsFailed    int
	runsSkipped   int
	deletedKey    string
	revisedKey    string
	correctionKey string
}

func (s seedSummary) print() {
	fmt.Println("\naffseed: done.")
	for _, f := range s.feeds {
		fmt.Printf("  feed %-28s kind=%-11s items=%-3d runs(success=%d,failed=%d,skipped=%d)\n",
			f.slug, f.kind, f.itemsAdded, f.runsSucceeded, f.runsFailed, f.runsSkipped)
		if f.deletedKey != "" {
			fmt.Printf("    soft-deleted item_key=%s -> GET /items/%s should 410\n", f.deletedKey, f.deletedKey)
		}
		if f.revisedKey != "" {
			fmt.Printf("    revised item_key=%s (has item_revisions history)\n", f.revisedKey)
		}
		if f.correctionKey != "" {
			fmt.Printf("    published correction item_key=%s\n", f.correctionKey)
		}
	}
	fmt.Println("\n  Feed documents: GET /feeds/<slug>.xml | .atom | .json on the publish plane.")
}

// itemPlan is one item's content before it is assigned a feed id, item_key
// or content_hash — those are only known once seedAll knows which feed and
// which run the item belongs to.
type itemPlan struct {
	title, summary, body, answer, link, source string
	publishedAt                                time.Time
	origin                                     model.Origin
}

func seedAll(ctx context.Context, st *store.Store, feedSrv *rpc.FeedServer, itemSrv *rpc.ItemServer,
	idSrc ids.Source, now time.Time) (seedSummary, error) {

	var out seedSummary

	triviaID, err := createFeed(ctx, feedSrv, feedSpecInput{
		slug:        "daily-anime-trivia",
		title:       "Daily Anime Trivia (seed data)",
		description: "A fictional daily trivia feed used only to exercise the dev environment. Every question is placeholder content (seed data).",
		kind:        affv1.FeedKind_FEED_KIND_GENERATIVE,
		cron:        "0 13 * * *",
		timezone:    "America/New_York",
		author:      "AnimeFeedFlux Seed Bot",
		copyright:   "(seed data, not a real publication)",
		ttlMinutes:  60,
		systemPrompt: "You write short anime trivia questions with a hidden answer. This is seed/placeholder " +
			"instruction text, never sent to a real model by this tool.",
		userPrompt: "Write one trivia question about a fictional anime series. (seed data)",
	})
	if err != nil {
		return out, fmt.Errorf("creating trivia feed: %w", err)
	}

	newsID, err := createFeed(ctx, feedSrv, feedSpecInput{
		slug:        "weekly-anime-news",
		title:       "Weekly Anime News Roundup (seed data)",
		description: "A fictional grounded news feed used only to exercise the dev environment. Every article summary is placeholder content (seed data), never a real studio's real statement.",
		kind:        affv1.FeedKind_FEED_KIND_GROUNDED,
		cron:        "0 9 * * 1",
		timezone:    "UTC",
		author:      "AnimeFeedFlux Seed Bot",
		copyright:   "(seed data, not a real publication)",
		ttlMinutes:  120,
		systemPrompt: "You edit anime news candidates down to a faithful summary, never inventing facts. This " +
			"is seed/placeholder instruction text, never sent to a real model by this tool.",
		userPrompt: "Summarize the most notable fictional-source candidate article. (seed data)",
		sources: []feedSourceInput{
			{url: "https://example-news.invalid/anime/feed.xml", kind: "rss"},
			{url: "https://example-wire.invalid/entertainment/anime.xml", kind: "rss"},
		},
	})
	if err != nil {
		return out, fmt.Errorf("creating news feed: %w", err)
	}

	spotlightID, err := createFeed(ctx, feedSrv, feedSpecInput{
		slug:        "character-spotlight-weekly",
		title:       "Character Spotlight Weekly (seed data)",
		description: "A fictional weekly character-spotlight feed used only to exercise the dev environment. Every spotlight is placeholder content about an invented character (seed data).",
		kind:        affv1.FeedKind_FEED_KIND_GENERATIVE,
		cron:        "0 10 * * 3",
		timezone:    "Asia/Tokyo",
		author:      "AnimeFeedFlux Seed Bot",
		copyright:   "(seed data, not a real publication)",
		ttlMinutes:  180,
		systemPrompt: "You write a short spotlight about one character from a fictional anime series. This is " +
			"seed/placeholder instruction text, never sent to a real model by this tool.",
		userPrompt: "Write one character spotlight. (seed data)",
	})
	if err != nil {
		return out, fmt.Errorf("creating spotlight feed: %w", err)
	}

	triviaSummary, err := seedTriviaFeed(ctx, st, idSrc, triviaID, "daily-anime-trivia", now)
	if err != nil {
		return out, fmt.Errorf("seeding trivia feed: %w", err)
	}
	newsSummary, err := seedNewsFeed(ctx, st, idSrc, newsID, "weekly-anime-news", now)
	if err != nil {
		return out, fmt.Errorf("seeding news feed: %w", err)
	}
	spotlightSummary, err := seedSpotlightFeed(ctx, st, idSrc, spotlightID, "character-spotlight-weekly", now)
	if err != nil {
		return out, fmt.Errorf("seeding spotlight feed: %w", err)
	}

	// One soft-deleted item, in the trivia feed: the oldest item, so the 410
	// permalink path (PLAN.md §12.4) has something real to show.
	if triviaSummary.oldestKey != "" {
		if err := st.SoftDeleteItem(ctx, triviaSummary.oldestKey); err != nil {
			return out, fmt.Errorf("soft-deleting trivia item %q: %w", triviaSummary.oldestKey, err)
		}
		triviaSummary.deletedKey = triviaSummary.oldestKey
	}

	// One item with a revision history, in the news feed: an ordinary edit
	// through ItemServer.Update, which writes item_revisions atomically with
	// the row change (internal/rpc/item.go's itemApplyEdit).
	if newsSummary.reviseKeyID != 0 {
		revised, err := reviseNewsItem(ctx, st, itemSrv, newsSummary.reviseKeyID)
		if err != nil {
			return out, fmt.Errorf("revising news item %d: %w", newsSummary.reviseKeyID, err)
		}
		newsSummary.revisedKey = revised
	}

	// One published correction, in the news feed: PublishCorrection creates
	// a brand-new item with origin=correction and a corrections row linking
	// it back to the original, without touching the original's guid or
	// published_at (PLAN.md §12.4).
	if newsSummary.correctKeyID != 0 {
		corrected, err := publishNewsCorrection(ctx, itemSrv, newsSummary.correctKeyID)
		if err != nil {
			return out, fmt.Errorf("publishing correction for news item %d: %w", newsSummary.correctKeyID, err)
		}
		newsSummary.correctionKey = corrected
	}

	out.feeds = []feedSummary{
		{
			slug: "daily-anime-trivia", kind: "generative",
			itemsAdded: triviaSummary.itemsAdded, runsSucceeded: triviaSummary.runsSucceeded,
			runsFailed: triviaSummary.runsFailed, runsSkipped: triviaSummary.runsSkipped,
			deletedKey: triviaSummary.deletedKey,
		},
		{
			slug: "weekly-anime-news", kind: "grounded",
			itemsAdded: newsSummary.itemsAdded, runsSucceeded: newsSummary.runsSucceeded,
			runsFailed: newsSummary.runsFailed, runsSkipped: newsSummary.runsSkipped,
			revisedKey: newsSummary.revisedKey, correctionKey: newsSummary.correctionKey,
		},
		{
			slug: "character-spotlight-weekly", kind: "generative",
			itemsAdded: spotlightSummary.itemsAdded, runsSucceeded: spotlightSummary.runsSucceeded,
			runsFailed: spotlightSummary.runsFailed, runsSkipped: spotlightSummary.runsSkipped,
		},
	}
	return out, nil
}

// --- feed creation ----------------------------------------------------

type feedSourceInput struct{ url, kind string }

type feedSpecInput struct {
	slug, title, description string
	kind                     affv1.FeedKind
	cron, timezone           string
	author, copyright        string
	ttlMinutes               int32
	systemPrompt, userPrompt string
	sources                  []feedSourceInput
}

func createFeed(ctx context.Context, feedSrv *rpc.FeedServer, in feedSpecInput) (int64, error) {
	spec := &affv1.FeedSpec{
		Cron:                 in.cron,
		Timezone:             in.timezone,
		ItemsPerRun:          2,
		FeedWindow:           50,
		Model:                "seed-model (not a real provider model id)",
		SystemPromptTemplate: in.systemPrompt,
		UserPromptTemplate:   in.userPrompt,
		Novelty: &affv1.NoveltySettings{
			NoveltyWindowItems:  50,
			SimilarityThreshold: 0.92,
		},
		DailyTokenBudget: 200_000,
		DailyRunBudget:   24,
	}
	for _, src := range in.sources {
		spec.Sources = append(spec.Sources, &affv1.SourceSpec{Url: src.url, Kind: src.kind})
	}

	resp, err := feedSrv.Create(ctx, &affv1.FeedServiceCreateRequest{
		Feed: &affv1.Feed{
			Slug:        in.slug,
			Kind:        in.kind,
			Title:       in.title,
			Description: in.description,
			Language:    "en",
			Author:      in.author,
			Copyright:   in.copyright,
			OgImage:     "",
			TtlMinutes:  in.ttlMinutes,
			Enabled:     true,
			Spec:        spec,
		},
	})
	if err != nil {
		return 0, err
	}
	return resp.GetFeed().GetId(), nil
}

// --- item/run seeding ---------------------------------------------------

// feedSeedResult is what each per-feed seeder reports back to seedAll.
type feedSeedResult struct {
	itemsAdded    int
	runsSucceeded int
	runsFailed    int
	runsSkipped   int

	oldestKey     string // trivia feed: candidate for soft-delete
	reviseKeyID   int64  // news feed: candidate item id for Update
	correctKeyID  int64  // news feed: candidate item id for PublishCorrection
	deletedKey    string
	revisedKey    string
	correctionKey string
}

// mintItem turns a plan into a fully-formed model.Item: a fresh opaque
// item_key from the real ids.Source (never derived from content, PLAN.md
// §5.1) and a content_hash unique enough to satisfy UNIQUE(feed_id,
// content_hash) without claiming to be a semantic novelty signal (mirrors
// internal/rpc/item.go's itemContentHash, which this file cannot import
// because it is unexported).
func mintItem(idSrc ids.Source, feedID int64, p itemPlan) model.Item {
	key := idSrc.NewItemKey(p.publishedAt)
	sum := sha256.Sum256([]byte(fmt.Sprintf("affseed\x00%d\x00%s\x00%s", feedID, key, p.publishedAt.Format(time.RFC3339Nano))))
	return model.Item{
		FeedID:      feedID,
		ItemKey:     key,
		ContentHash: hex.EncodeToString(sum[:]),
		Title:       p.title,
		SummaryText: p.summary,
		BodyHTML:    p.body,
		AnswerHTML:  p.answer,
		Link:        p.link,
		SourceName:  p.source,
		PublishedAt: p.publishedAt,
		Origin:      p.origin,
	}
}

// commitBatch runs one successful run for feedID, publishing plans[lo:hi] as
// its items, through StartRun+CommitRun so the run row and its items land
// atomically exactly the way the real generation pipeline commits them
// (internal/store/runs.go's CommitRun doc comment).
func commitBatch(ctx context.Context, st *store.Store, idSrc ids.Source, feedID int64,
	plans []itemPlan, lo, hi int, tokensIn, tokensOut int, costUSD float64) ([]string, error) {

	runID, err := st.StartRun(ctx, feedID, "cron", "affseed")
	if err != nil {
		return nil, fmt.Errorf("starting run: %w", err)
	}
	items := make([]model.Item, 0, hi-lo)
	for _, p := range plans[lo:hi] {
		items = append(items, mintItem(idSrc, feedID, p))
	}
	if err := st.CommitRun(ctx, runID, items, store.RunSummary{
		TokensIn:  tokensIn,
		TokensOut: tokensOut,
		CostUSD:   costUSD,
	}); err != nil {
		return nil, fmt.Errorf("committing run: %w", err)
	}
	keys := make([]string, len(items))
	for i, it := range items {
		keys[i] = it.ItemKey
	}
	return keys, nil
}

func failRun(ctx context.Context, st *store.Store, feedID int64, errorKind, message string,
	tokensIn, tokensOut int, costUSD float64, rejected int, reasons map[string]int) error {
	runID, err := st.StartRun(ctx, feedID, "cron", "affseed")
	if err != nil {
		return fmt.Errorf("starting run: %w", err)
	}
	return st.FailRun(ctx, runID, errorKind, message, store.RunSummary{
		TokensIn:      tokensIn,
		TokensOut:     tokensOut,
		CostUSD:       costUSD,
		ItemsRejected: rejected,
		RejectReasons: reasons,
	})
}

func skipRun(ctx context.Context, st *store.Store, feedID int64, reason string,
	tokensIn, tokensOut int, costUSD float64, rejected int, reasons map[string]int) error {
	runID, err := st.StartRun(ctx, feedID, "cron", "affseed")
	if err != nil {
		return fmt.Errorf("starting run: %w", err)
	}
	return st.SkipRun(ctx, runID, reason, store.RunSummary{
		TokensIn:      tokensIn,
		TokensOut:     tokensOut,
		CostUSD:       costUSD,
		ItemsRejected: rejected,
		RejectReasons: reasons,
	})
}

// seedTriviaFeed publishes ~20 generative trivia items across ~20 days,
// split into successful runs of 1-2 items each, plus one failed and one
// skipped run.
func seedTriviaFeed(ctx context.Context, st *store.Store, idSrc ids.Source, feedID int64, slug string, now time.Time) (feedSeedResult, error) {
	var res feedSeedResult

	const n = 20
	plans := make([]itemPlan, 0, n)
	studios := []string{"Studio Umbra Prism (fictional)", "Nonexistent Frame Works (fictional)", "Placeholder Anima Studio (fictional)"}
	for i := 0; i < n; i++ {
		day := now.AddDate(0, 0, -(n - i)).Truncate(24 * time.Hour).Add(13*time.Hour + time.Duration(i)*time.Minute)
		q, a := triviaQA(i)
		plans = append(plans, itemPlan{
			title:       fmt.Sprintf("Trivia #%02d (seed data): %s", i+1, q),
			summary:     fmt.Sprintf("A daily trivia question about a fictional anime series. (seed data, produced by affseed, not a real model call) Credited to %s.", studios[i%len(studios)]),
			body:        fmt.Sprintf("<p>%s</p><p><em>This item is placeholder seed data generated by cmd/affseed for local development. It does not describe any real anime, studio or statement.</em></p>", q),
			answer:      fmt.Sprintf("<p>%s</p>", a),
			origin:      model.OriginGenerated,
			publishedAt: day,
		})
	}
	// Successful runs: batches of 1-2 items.
	i := 0
	batchSizes := []int{2, 1, 2, 2, 1, 2, 2, 2, 2, 2, 2}
	var allKeys []string
	for _, size := range batchSizes {
		if i >= len(plans) {
			break
		}
		hi := i + size
		if hi > len(plans) {
			hi = len(plans)
		}
		keys, err := commitBatch(ctx, st, idSrc, feedID, plans, i, hi,
			randInt(350, 650), randInt(120, 280), randCost(0.0008, 0.004))
		if err != nil {
			return res, err
		}
		allKeys = append(allKeys, keys...)
		res.runsSucceeded++
		res.itemsAdded += hi - i
		i = hi
	}
	if len(allKeys) > 0 {
		res.oldestKey = allKeys[0]
	}

	// One failed run: the provider call itself errored out after spending
	// tokens on a candidate that was then rejected — the money is still
	// recorded even though nothing published (PLAN.md §22 J5).
	if err := failRun(ctx, st, feedID, "provider_error",
		"seed data: simulated SchemaFlux request failure (upstream timeout after 30s) — not a real incident",
		420, 0, 0.0011, 1, map[string]int{"novelty_duplicate": 1}); err != nil {
		return res, err
	}
	res.runsFailed++

	// One skipped run: every candidate came back a near-duplicate and the
	// novelty retry budget was exhausted before any item was committed.
	if err := skipRun(ctx, st, feedID,
		"seed data: novelty exhausted — 3/3 candidates were near-duplicates of recently published trivia",
		280, 0, 0.0006, 3, map[string]int{"novelty_duplicate": 3}); err != nil {
		return res, err
	}
	res.runsSkipped++

	return res, nil
}

// seedNewsFeed publishes ~18 grounded news items across ~24 days, with
// realistic-looking (but fictional) source names and links, plus one item
// earmarked for a later Update (revision history) and one for a later
// PublishCorrection.
func seedNewsFeed(ctx context.Context, st *store.Store, idSrc ids.Source, feedID int64, slug string, now time.Time) (feedSeedResult, error) {
	var res feedSeedResult

	const n = 18
	plans := make([]itemPlan, 0, n)
	sources := []string{"Placeholder Wire (fictional)", "Example Anime Ledger (fictional)", "Nonexistent Herald (fictional)"}
	for i := 0; i < n; i++ {
		day := now.AddDate(0, 0, -(24 - (i*24)/n)).Add(9*time.Hour + time.Duration(i)*3*time.Minute)
		headline, body := newsArticle(i)
		src := sources[i%len(sources)]
		plans = append(plans, itemPlan{
			title:       fmt.Sprintf("(seed data) %s", headline),
			summary:     fmt.Sprintf("%s (seed data, fictional summary produced by affseed, not a real news report)", headline),
			body:        body,
			link:        fmt.Sprintf("https://example-news.invalid/articles/seed-%03d", i+1),
			source:      src,
			origin:      model.OriginGenerated,
			publishedAt: day,
		})
	}

	i := 0
	batchSizes := []int{2, 3, 2, 3, 2, 3, 3}
	var mintedKeys []string
	for _, size := range batchSizes {
		if i >= len(plans) {
			break
		}
		hi := i + size
		if hi > len(plans) {
			hi = len(plans)
		}
		keys, err := commitBatch(ctx, st, idSrc, feedID, plans, i, hi,
			randInt(2400, 4200), randInt(400, 900), randCost(0.006, 0.018))
		if err != nil {
			return res, err
		}
		mintedKeys = append(mintedKeys, keys...)
		res.runsSucceeded++
		res.itemsAdded += hi - i
		i = hi
	}

	if err := failRun(ctx, st, feedID, "validation_failed",
		"seed data: simulated grounded-generation failure — 2 candidates rejected for citing a URL not present in the fetched source (link_mismatch), not a real incident",
		3100, 0, 0.0079, 2, map[string]int{"link_mismatch": 2}); err != nil {
		return res, err
	}
	res.runsFailed++

	if err := skipRun(ctx, st, feedID,
		"seed data: daily token budget exhausted before this feed's scheduled slot could run",
		0, 0, 0, 0, nil); err != nil {
		return res, err
	}
	res.runsSkipped++

	// Resolve item ids for the two items we will mutate afterward: the
	// second-oldest (revise) and the newest (correct), by item_key.
	if len(mintedKeys) >= 2 {
		reviseKey := mintedKeys[1]
		it, err := st.GetItem(ctx, reviseKey)
		if err != nil {
			return res, fmt.Errorf("loading item %q to revise: %w", reviseKey, err)
		}
		res.reviseKeyID = it.ID

		correctKey := mintedKeys[len(mintedKeys)-1]
		it2, err := st.GetItem(ctx, correctKey)
		if err != nil {
			return res, fmt.Errorf("loading item %q to correct: %w", correctKey, err)
		}
		res.correctKeyID = it2.ID
	}

	return res, nil
}

// seedSpotlightFeed publishes ~15 generative character-spotlight items
// across ~15 days, plus one failed and one skipped run.
func seedSpotlightFeed(ctx context.Context, st *store.Store, idSrc ids.Source, feedID int64, slug string, now time.Time) (feedSeedResult, error) {
	var res feedSeedResult

	const n = 15
	plans := make([]itemPlan, 0, n)
	for i := 0; i < n; i++ {
		day := now.AddDate(0, 0, -(n - i)).Truncate(24 * time.Hour).Add(10*time.Hour + time.Duration(i)*2*time.Minute)
		name, blurb := spotlight(i)
		plans = append(plans, itemPlan{
			title:       fmt.Sprintf("(seed data) Character Spotlight: %s", name),
			summary:     fmt.Sprintf("A short profile of the fictional character %s. (seed data, not a real character)", name),
			body:        fmt.Sprintf("<p>%s</p><p><em>This item is placeholder seed data generated by cmd/affseed. %s does not exist in any real anime.</em></p>", blurb, name),
			origin:      model.OriginGenerated,
			publishedAt: day,
		})
	}

	i := 0
	batchSizes := []int{2, 2, 1, 2, 2, 2, 2, 2}
	for _, size := range batchSizes {
		if i >= len(plans) {
			break
		}
		hi := i + size
		if hi > len(plans) {
			hi = len(plans)
		}
		if _, err := commitBatch(ctx, st, idSrc, feedID, plans, i, hi,
			randInt(300, 550), randInt(150, 320), randCost(0.0009, 0.0035)); err != nil {
			return res, err
		}
		res.runsSucceeded++
		res.itemsAdded += hi - i
		i = hi
	}

	if err := failRun(ctx, st, feedID, "provider_error",
		"seed data: simulated SchemaFlux request failure (malformed response) — not a real incident",
		200, 0, 0.0004, 0, nil); err != nil {
		return res, err
	}
	res.runsFailed++

	if err := skipRun(ctx, st, feedID,
		"seed data: novelty exhausted — every candidate character had already been spotlighted recently",
		190, 0, 0.0004, 2, map[string]int{"novelty_duplicate": 2}); err != nil {
		return res, err
	}
	res.runsSkipped++

	return res, nil
}

// reviseNewsItem performs one ordinary edit through ItemServer.Update, which
// writes item_revisions atomically with the row change.
func reviseNewsItem(ctx context.Context, st *store.Store, itemSrv *rpc.ItemServer, itemID int64) (string, error) {
	old, err := st.GetItem(ctx, itemKeyByID(ctx, st, itemID))
	if err != nil {
		return "", err
	}
	resp, err := itemSrv.Update(ctx, &affv1.ItemServiceUpdateRequest{
		Item: &affv1.Item{
			Id:          itemID,
			Title:       old.Title + " [corrected typo]",
			SummaryText: old.SummaryText + " (edited: fixed a wording error — seed data)",
			BodyHtml:    old.BodyHTML,
			AnswerHtml:  old.AnswerHTML,
			Link:        old.Link,
			SourceName:  old.SourceName,
			PublishedAt: timestamppb.New(old.PublishedAt),
		},
		ExpectedVersion: old.Version,
	})
	if err != nil {
		return "", err
	}
	return resp.GetItem().GetItemKey(), nil
}

func publishNewsCorrection(ctx context.Context, itemSrv *rpc.ItemServer, itemID int64) (string, error) {
	resp, err := itemSrv.PublishCorrection(ctx, &affv1.ItemServicePublishCorrectionRequest{
		CorrectsItemId: itemID,
		Title:          "an earlier figure in this story was wrong (seed data)",
		SummaryText:    "This corrects a detail in the earlier item. (seed data, fictional correction, not a real retraction)",
		BodyHtml:       "<p>An earlier item in this feed stated an incorrect detail. This item corrects it. Both this correction and the original item are placeholder seed data.</p>",
	})
	if err != nil {
		return "", err
	}
	return resp.GetItem().GetItemKey(), nil
}

// itemKeyByID is a small helper: the store package has GetItem-by-key but
// not GetItem-by-id, and reviseNewsItem is called with an id (from
// st.GetItem's own ID field, captured earlier in seedNewsFeed). Rather than
// hand-writing a SELECT here, this looks the row up through the writer
// handle's ordinary SQL read — the same kind read every RPC handler in this
// codebase already does for id-keyed lookups (e.g. internal/rpc/item.go's
// itemLoadByID) — to recover the item_key, then hands off to the real
// st.GetItem for everything else.
func itemKeyByID(ctx context.Context, st *store.Store, id int64) string {
	var key string
	_ = st.Writer().QueryRowContext(ctx, `SELECT item_key FROM items WHERE id = ?`, id).Scan(&key)
	return key
}

// --- fake content generators --------------------------------------------
//
// Every string below is plainly placeholder text: invented character and
// studio names that do not correspond to any real anime, and explicit
// "(seed data)" / "fictional" markers throughout, per the requirement that
// seeded content can never be mistaken for something actually published.

func triviaQA(i int) (q, a string) {
	characters := []string{"Kaji Nowhere", "Rin Placeholder", "Suzu Nonexistent", "Tomo Blankslate", "Yui Fictional"}
	c := characters[i%len(characters)]
	return fmt.Sprintf("In the fictional series used for this seed item, what color is %s's signature scarf?", c),
		"Violet. (This answer is invented for seed data and has no real-world source.)"
}

func newsArticle(i int) (headline, bodyHTML string) {
	titles := []string{
		"invented studio announces a fictional new season",
		"placeholder convention panel teases an imaginary spin-off",
		"nonexistent voice cast reveal for a fictional film",
		"imaginary streaming platform picks up a fictional series",
		"fictional director discusses an invented production delay",
		"placeholder merchandise line announced for a fictional property",
	}
	t := titles[i%len(titles)]
	return t, fmt.Sprintf(
		"<p>%s</p><p><em>This article is placeholder seed data generated by cmd/affseed for local development. "+
			"No real studio, publisher or person is quoted or described here.</em></p>", t)
}

func spotlight(i int) (name, blurb string) {
	names := []string{"Aki Emptyframe", "Ren Unwritten", "Nao Backstory-Pending", "Sora Undrawn", "Haru Notreal"}
	n := names[i%len(names)]
	return n, fmt.Sprintf("%s is a fictional character invented only for this seed item, with no canon, studio or series attached.", n)
}

// --- small deterministic-ish randomness helpers --------------------------
//
// A seeder's exact token/cost figures do not need to be reproducible, only
// plausible, so this uses math/rand/v2's unseeded top-level functions
// (safe, non-cryptographic) rather than pulling in a Source.

func randInt(lo, hi int) int {
	if hi <= lo {
		return lo
	}
	return lo + rand.IntN(hi-lo)
}

func randCost(lo, hi float64) float64 {
	return lo + rand.Float64()*(hi-lo)
}
