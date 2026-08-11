// wire.go is the composition root (PLAN.md §2, §13, §15): it is the ONLY
// place that constructs the six RPC services, the control-plane bridge, the
// scheduler, and the generation pipeline's adapters, and wires them all to
// the store and each other. Nothing here is business logic — every
// interesting decision (how a run commits, how novelty is checked, how the
// bridge validates a session) lives in the package that owns it; this file
// only supplies the concrete types those packages ask for as interfaces.
//
// Two invariants this file exists to hold, both load-bearing enough that
// tests assert them directly (wire_test.go):
//
//  1. The publish plane and the control plane are two listeners, never one
//     mux (§2) — the security argument is that the public surface cannot
//     reach the admin one, which is only true if they are structurally
//     separate net.Listeners.
//  2. The scheduler's dispatch loop and every interactive provider call
//     (Sample, RunNow) draw from the SAME *schedule.Semaphore instance
//     (§13) — a second semaphore sized identically would still let sampling
//     add concurrency the operator's AFF_PROVIDER_MAX_INFLIGHT never
//     intended.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"

	schemafluxotel "github.com/monstercameron/schemaflux/telemetry/otel"
	openai "github.com/sashabaranov/go-openai"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/auth"
	"github.com/monstercameron/AnimeFeedFlux/internal/bridge"
	"github.com/monstercameron/AnimeFeedFlux/internal/budget"
	"github.com/monstercameron/AnimeFeedFlux/internal/config"
	"github.com/monstercameron/AnimeFeedFlux/internal/feedspec"
	"github.com/monstercameron/AnimeFeedFlux/internal/generate"
	"github.com/monstercameron/AnimeFeedFlux/internal/ids"
	"github.com/monstercameron/AnimeFeedFlux/internal/llm"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/novelty"
	"github.com/monstercameron/AnimeFeedFlux/internal/obs"
	"github.com/monstercameron/AnimeFeedFlux/internal/ops"
	"github.com/monstercameron/AnimeFeedFlux/internal/publish"
	"github.com/monstercameron/AnimeFeedFlux/internal/rpc"
	"github.com/monstercameron/AnimeFeedFlux/internal/schedule"
	"github.com/monstercameron/AnimeFeedFlux/internal/sources"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// --- tunables not exposed as environment configuration --------------------
//
// PLAN.md §16 fixes the server-wide environment surface; none of these have
// an env var, so they live here as named constants rather than magic
// numbers scattered through the constructors below.
const (
	// defaultNoveltyThreshold/-Window mirror feedspec.Defaults' per-recipe
	// numbers (internal/feedspec/spec.go) — used only as the FALLBACK when a
	// feed's own spec_json omits a novelty section (a zero Threshold would
	// otherwise mark every candidate a duplicate, since 0 >= any cosine
	// similarity in [-1,1]... actually >= 0 is common, so zero is unsafe).
	defaultNoveltyThreshold = 0.92
	defaultNoveltyWindow    = 500

	// embeddingDim/-Model pin the OpenAI embedding model novelty checks use.
	// PLAN.md §8.1 is explicit these are direct OpenAI calls, not SchemaFlux.
	embeddingModel = openai.SmallEmbedding3
	embeddingDim   = 1536

	// manualRunTimeout bounds an interactive RunNow the same way
	// schedule.DefaultRunTimeout bounds a cron dispatch — a manual click
	// must not be allowed to hold the shared provider semaphore forever.
	manualRunTimeout = 5 * time.Minute

	// httpShutdownTimeout is how long the two HTTP servers get to drain
	// (PLAN.md §15). The scheduler has its own, longer budget
	// (schedule.DefaultShutdownTimeout) for in-flight generation, which is
	// the one a partially-charged LLM call actually needs. It also bounds
	// the telemetry flush (obs.Setup's shutdown func) — see runAll's
	// deferred call for why that reuse is deliberate.
	httpShutdownTimeout = 20 * time.Second

	// nightlyRunRetention is the ops scheduler's PruneOptions.RunRetention
	// (PLAN.md §15: "runs older than 180 days are pruned except failures").
	nightlyRunRetention = 180 * 24 * time.Hour
)

// =====================================================================
// Shared feed/spec loading — used by the scheduler, RunNow, and Sample
// =====================================================================

// feedRow is feedSelectColumns (server.go) plus spec_json, the one column
// the publish plane's read path never needs but every generation path does.
type feedRow struct {
	model.Feed
	SpecJSON string
}

// loadFeedRow resolves one feed, including its recipe, by id. Soft-deleted
// feeds are excluded — nothing should ever generate for or schedule a
// deleted feed, even if its row is still on disk pending GC.
func loadFeedRow(ctx context.Context, r store.Reader, feedID int64) (feedRow, error) {
	row := r.QueryRowContext(ctx,
		`SELECT `+feedSelectColumns+`, spec_json FROM feeds WHERE id = ? AND deleted_at IS NULL`, feedID)
	fr, err := scanFeedRowWithSpec(row)
	if errors.Is(err, sql.ErrNoRows) {
		return feedRow{}, fmt.Errorf("wire: feed %d: %w", feedID, store.ErrNotFound)
	}
	if err != nil {
		return feedRow{}, fmt.Errorf("wire: loading feed %d: %w", feedID, err)
	}
	return fr, nil
}

// loadSchedulableFeedRows is the scheduler's fixed job set at boot
// (schedule.New's contract: feeds added or removed require a restart).
// Aggregate feeds are excluded — they have no generator to run (§14.2).
func loadSchedulableFeedRows(ctx context.Context, r store.Reader) ([]feedRow, error) {
	rows, err := r.QueryContext(ctx,
		`SELECT `+feedSelectColumns+`, spec_json FROM feeds
		 WHERE enabled = 1 AND deleted_at IS NULL AND kind != 'aggregate'
		 ORDER BY slug`)
	if err != nil {
		return nil, fmt.Errorf("wire: listing schedulable feeds: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []feedRow
	for rows.Next() {
		fr, err := scanFeedRowWithSpec(rows)
		if err != nil {
			return nil, fmt.Errorf("wire: scanning feed: %w", err)
		}
		out = append(out, fr)
	}
	return out, rows.Err()
}

// scanFeedRowWithSpec mirrors server.go's scanFeedRow but for the wider
// column set above (feedSelectColumns + spec_json). Duplicated rather than
// composed with scanFeedRow because that function owns its Scan call
// internally with a fixed argument list — there is no seam to append one
// more column through it without editing server.go, which is off-limits.
func scanFeedRowWithSpec(sc feedScanner) (feedRow, error) {
	var (
		fr                                      feedRow
		enabled                                 int64
		kind                                    string
		author, copyright, ogImage, lastBuiltAt sql.NullString
		specJSON                                string
	)
	if err := sc.Scan(
		&fr.ID, &fr.Slug, &fr.Title, &fr.Description, &fr.Language, &kind, &enabled, &fr.Timezone,
		&author, &copyright, &ogImage, &fr.TTLMinutes, &lastBuiltAt, &fr.Version, &specJSON,
	); err != nil {
		return feedRow{}, err
	}
	fr.Kind = model.FeedKind(kind)
	fr.Enabled = enabled != 0
	fr.Author = author.String
	fr.Copyright = copyright.String
	fr.OGImage = ogImage.String
	lastBuilt, err := parseNullStoreTime(lastBuiltAt)
	if err != nil {
		return feedRow{}, fmt.Errorf("parsing feeds.last_built_at: %w", err)
	}
	fr.LastBuiltAt = lastBuilt
	fr.SpecJSON = specJSON
	return fr, nil
}

// specFromRow parses a feed's stored recipe. Validate is deliberately not
// called here (feedspec.Import's own contract) — an already-saved recipe was
// validated at save time; re-validating on every run would let a later
// tightening of validation rules retroactively break a feed that has been
// generating successfully for months.
func specFromRow(row feedRow) (feedspec.Spec, error) {
	fs, err := feedspec.Import([]byte(row.SpecJSON))
	if err != nil {
		return feedspec.Spec{}, fmt.Errorf("wire: parsing spec for feed %q: %w", row.Slug, err)
	}
	return fs, nil
}

// loadPriceTableAtBoot applies the stored rates before the first scheduled
// run, so a restart does not silently revert to an empty table (and with an
// empty table every cost reads $0.0000 and no ceiling can ever trip).
func loadPriceTableAtBoot(ctx context.Context, r store.Reader, t *budget.Table) error {
	var raw string
	err := r.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'provider'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("wire: reading provider settings: %w", err)
	}
	p := &affv1.Settings_Provider{}
	if err := json.Unmarshal([]byte(raw), p); err != nil {
		return fmt.Errorf("wire: parsing provider settings: %w", err)
	}
	applyPriceTable(t, p.GetPriceTable())
	return nil
}

// applyPriceTable copies the operator-configured rates into the live
// budget.Table every cost figure and every §13 ceiling is computed from.
//
// **Unit conversion, and it matters.** The wire field is
// usd_per_1k_tokens_in/out; budget.Price is per MILLION tokens. Passing one
// straight into the other is a 1000x error in either direction — in the
// dangerous direction it makes every run look 1000x cheaper than it is and
// no ceiling ever trips. The multiplication below is the only place the two
// units meet.
func applyPriceTable(t *budget.Table, entries []*affv1.PriceEntry) {
	if t == nil {
		return
	}
	for _, e := range entries {
		model := strings.TrimSpace(e.GetModel())
		if model == "" {
			continue
		}
		t.Set(budget.Price{
			Model:         model,
			InputPerMTok:  e.GetUsdPer_1KTokensIn() * 1000,
			OutputPerMTok: e.GetUsdPer_1KTokensOut() * 1000,
		})
	}
}

// generateSpecFrom maps the recipe layer (internal/feedspec) onto the
// pipeline layer (internal/generate) — the two packages deliberately don't
// know about each other (generate/runner.go's header), so this file is the
// only place the mapping happens.
func generateSpecFrom(fs feedspec.Spec, trigger string, prices *budget.Table) generate.Spec {
	var priceIn, priceOut float64
	if p, ok := prices.Lookup(fs.Model.Model); ok {
		priceIn, priceOut = p.InputPerMTok, p.OutputPerMTok
	}
	return generate.Spec{
		SystemPrompt:         fs.SystemPrompt,
		UserPromptTemplate:   fs.UserPrompt,
		Model:                fs.Model.Model,
		Temperature:          fs.Model.Temperature,
		ItemsPerRun:          fs.ItemsPerRun,
		RecentTitlesN:        fs.Novelty.ExcludeLast,
		MaxNoveltyRetries:    fs.Novelty.MaxRetries,
		PriceInputPerMToken:  priceIn,
		PriceOutputPerMToken: priceOut,
		Trigger:              trigger,
	}
}

func formatStoreTime(t time.Time) string { return t.UTC().Format(storeTimeLayout) }

func midnightUTC(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func countRunsSince(ctx context.Context, r store.Reader, feedID int64, since time.Time) (int, error) {
	var n int
	err := r.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM runs WHERE feed_id = ? AND started_at >= ?`,
		feedID, formatStoreTime(since)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("wire: counting runs since %s for feed %d: %w", formatStoreTime(since), feedID, err)
	}
	return n, nil
}

// loadGenerationSettings reads the SAME settings.generation row
// internal/rpc's SystemServer reads and writes (system.go's sysLoadGeneration
// / SetGenerationEnabled) — the live, DB-backed kill switch an admin can flip
// at runtime, layered on top of AFF_GENERATION_ENABLED's cold-start default.
// Reading it directly (rather than through SystemServer, which has no
// exported accessor for this) keeps the scheduler and the budget gate in
// agreement with whatever the admin UI shows, not a stale value cached from
// boot.
func loadGenerationSettings(ctx context.Context, r store.Reader, defaultEnabled bool) (*affv1.Settings_Generation, error) {
	var raw string
	err := r.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'generation'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		// Same cold-start values the RPC seeds, from the same constants — a
		// zero ceiling means "no limit" to internal/budget, so a fallback
		// that quietly returned zeros would disable §13's backstop for
		// exactly the deployment that never opened the settings screen.
		return &affv1.Settings_Generation{
			Enabled:                    defaultEnabled,
			GlobalDailyTokenCeiling:    rpc.DefaultGlobalDailyTokenCeiling,
			GlobalDailySpendCeilingUsd: rpc.DefaultGlobalDailySpendCeilingUSD,
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("wire: reading generation settings: %w", err)
	}
	g := &affv1.Settings_Generation{}
	if err := json.Unmarshal([]byte(raw), g); err != nil {
		return nil, fmt.Errorf("wire: parsing generation settings: %w", err)
	}
	return g, nil
}

// =====================================================================
// generate.Store adapter — binds one already-open run row to the pipeline
// =====================================================================

// genStoreAdapter adapts *store.Store's run lifecycle (StartRun/CommitRun/
// FailRun/SkipRun, plus the read helpers RecentTitles/NewestPublished that
// internal/store does not itself expose) to generate.Store. runID is bound
// per invocation because generate.Run's contract is "close the run StartRun
// already opened", never "choose or create one" — Run itself never sees an
// id.
type genStoreAdapter struct {
	st    *store.Store
	runID int64
}

func (a genStoreAdapter) RecentTitles(ctx context.Context, feedID int64, n int) ([]string, error) {
	rows, err := a.st.Reader().QueryContext(ctx,
		`SELECT title FROM items WHERE feed_id = ? AND deleted_at IS NULL
		 ORDER BY published_at DESC LIMIT ?`, feedID, n)
	if err != nil {
		return nil, fmt.Errorf("wire: recent titles for feed %d: %w", feedID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			return nil, fmt.Errorf("wire: scanning recent title: %w", err)
		}
		out = append(out, title)
	}
	return out, rows.Err()
}

func (a genStoreAdapter) NewestPublished(ctx context.Context, feedID int64) (time.Time, error) {
	var s sql.NullString
	err := a.st.Reader().QueryRowContext(ctx,
		`SELECT MAX(published_at) FROM items WHERE feed_id = ?`, feedID).Scan(&s)
	if err != nil {
		return time.Time{}, fmt.Errorf("wire: newest published for feed %d: %w", feedID, err)
	}
	if !s.Valid || s.String == "" {
		return time.Time{}, nil
	}
	return parseStoreTime(s.String)
}

// CommitRun closes the SAME run row StartRun opened, choosing which store
// method to call from run.Status (generate.Run's three terminal outcomes).
//
// Known gap, forced by store.go/runs.go being off-limits to this change:
// SkipRun (internal/store/runs.go) has no tokens/cost parameters, so a run
// skipped after spending tokens on failed novelty-retry attempts (§9 step 5)
// records that spend as zero here. The estimate is still visible on the
// attempt's own log line; only the runs-table rollup loses it.
func (a genStoreAdapter) CommitRun(ctx context.Context, run generate.RunRecord, items []model.Item) error {
	summary := store.RunSummary{
		TokensIn:      run.TokensIn,
		TokensOut:     run.TokensOut,
		CostUSD:       run.EstCostUSD,
		ItemsRejected: run.ItemsRejected,
		RejectReasons: run.RejectReasons,
	}
	switch run.Status {
	case generate.StatusCompleted:
		// run.Embeddings was captured during the SAME embed call the novelty
		// gate already made per candidate (generate/runner.go's filterNovel,
		// via VectorNovelty) — converted here rather than passed straight
		// through because generate.ItemEmbedding is this package's own type
		// (internal/generate must not import internal/store, per its file
		// header), even though the shape is identical.
		embeddings := make([]store.ItemEmbedding, len(run.Embeddings))
		for i, e := range run.Embeddings {
			embeddings[i] = store.ItemEmbedding{ItemKey: e.ItemKey, Vector: e.Vector}
		}
		return a.st.CommitRun(ctx, a.runID, items, summary, embeddings...)
	case generate.StatusSkipped:
		// summary carries whatever tokens/cost the novelty-retry loop already
		// spent before every attempt came back a duplicate (§9 step 5) — a
		// run skipped BEFORE any spend (a budget/run-cap refusal ahead of
		// generate.Run even being called) still reports zero honestly,
		// because run.TokensIn/TokensOut/EstCostUSD are genuinely zero in
		// that case. Passing a bare run.ErrorKind here (the pre-existing
		// call) silently recorded every skip as free, including the
		// after-spend case SkipRun's own doc comment calls out by name.
		return a.st.SkipRun(ctx, a.runID, run.ErrorKind, summary)
	default:
		return a.st.FailRun(ctx, a.runID, run.ErrorKind, run.Error, summary)
	}
}

// =====================================================================
// Novelty adapter — loads the comparison corpus, delegates to novelty.Gate
// =====================================================================

// noveltyAdapter implements generate.Novelty (Check) and generate.VectorNovelty
// (CheckVector) over novelty.Gate. It loads the comparison corpus from
// item_embeddings for the given feed via *store.Store.EmbeddingsForFeed —
// the store package's own reader (embeddings.go), not a second copy of that
// query kept here. Two copies of the same SQL is exactly the shape of bug
// this file used to have: this adapter's corpus query and CommitRun's
// insert used to live in two different packages with nothing forcing them to
// agree, and the read side happened to be built (§9's gate) while the write
// side was not — see embeddings.go's own header for how that gap closed.
// Keeping a single, store-owned implementation is what stops that pair from
// drifting apart again.
//
// The gate is no longer a dead branch: internal/generate's runner.go now
// writes a row per published item (via CommitRun's embeddings argument,
// captured through CheckVector below), so a feed that has generated before
// has a real corpus to compare the next candidate against.
type noveltyAdapter struct {
	st        *store.Store
	embedder  novelty.Embedder
	threshold float64
	window    int
}

func (n noveltyAdapter) Check(ctx context.Context, feedID int64, text string) (dup bool, nearest string, score float64, err error) {
	dup, nearest, score, _, err = n.check(ctx, feedID, text)
	return dup, nearest, score, err
}

// CheckVector implements generate.VectorNovelty: identical to Check, but
// also returns the embedding novelty.Gate computed for the candidate, so
// generate.Run can hand it straight to CommitRun instead of calling the
// embedder a second time for the same text — see runner.go's VectorNovelty
// doc comment for why a second call would be wrong (cost AND a chance for
// the two vectors to disagree), not merely wasteful.
func (n noveltyAdapter) CheckVector(ctx context.Context, feedID int64, text string) (dup bool, nearest string, score float64, vec novelty.Vector, err error) {
	return n.check(ctx, feedID, text)
}

// check is the shared implementation. It wraps the configured embedder in a
// capturingEmbedder scoped to THIS call (a fresh value, never shared across
// calls) so a concurrent Check/CheckVector for a different feed can never
// see or overwrite another call's captured vector — novelty.Gate.Check calls
// Embed exactly once, with exactly the one candidate string, so there is
// exactly one vector to capture per call.
func (n noveltyAdapter) check(ctx context.Context, feedID int64, text string) (dup bool, nearest string, score float64, vec novelty.Vector, err error) {
	corpus, err := n.st.EmbeddingsForFeed(ctx, feedID, n.window)
	if err != nil {
		return false, "", 0, novelty.Vector{}, err
	}
	capture := &capturingEmbedder{Embedder: n.embedder}
	gate := novelty.Gate{Embedder: capture, Threshold: n.threshold, Window: n.window}
	dup, nearest, score, err = gate.Check(ctx, text, corpus)
	if err != nil {
		return false, "", 0, novelty.Vector{}, err
	}
	return dup, nearest, score, capture.vec, nil
}

// capturingEmbedder wraps a novelty.Embedder and records the single vector
// its one Embed call produced. It exists only so noveltyAdapter.check can
// recover the vector novelty.Gate.Check computed internally and would
// otherwise discard — Gate's own Check signature (internal/novelty, off
// limits to edit for this change) has no way to return it.
type capturingEmbedder struct {
	novelty.Embedder
	vec novelty.Vector
}

func (c *capturingEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	vecs, err := c.Embedder.Embed(ctx, texts)
	if err != nil {
		return nil, err
	}
	if len(vecs) > 0 {
		c.vec = novelty.Vector{Model: c.Embedder.Model(), Dim: c.Embedder.Dim(), Vec: vecs[0]}
	}
	return vecs, nil
}

// =====================================================================
// Budget gate — kill switch + per-feed/global caps (PLAN.md §13)
// =====================================================================

// genGate implements schedule.Gate: the kill switch first (a refusal here
// must cost nothing — checked before any DB spend lookup even runs), then
// the per-feed and global caps from budget.Check.
type genGate struct {
	st     *store.Store
	cfg    *config.Config
	prices *budget.Table
	log    *slog.Logger

	// monthlyCeilingUSD is cfg.MonthlySpendCeilingUSD, hoisted so Allowed
	// can skip the month-to-date query entirely when it is zero.
	monthlyCeilingUSD float64
}

func (g *genGate) Allowed(feedID int64) (bool, string) {
	ctx := context.Background()
	now := time.Now()

	settings, err := loadGenerationSettings(ctx, g.st.Reader(), g.cfg.GenerationEnabled)
	if err != nil {
		g.log.Warn("gate: reading generation settings, failing closed", "error", err)
		return false, "settings_lookup_failed"
	}
	if !settings.GetEnabled() {
		return false, "generation_disabled"
	}

	row, err := loadFeedRow(ctx, g.st.Reader(), feedID)
	if err != nil {
		return false, "feed_lookup_failed"
	}
	fs, err := specFromRow(row)
	if err != nil {
		return false, "spec_parse_failed"
	}

	since := midnightUTC(time.Now())
	feedIn, feedOut, feedUSD, err := g.st.SpendSince(ctx, feedID, since)
	if err != nil {
		return false, "budget_lookup_failed"
	}
	runs, err := countRunsSince(ctx, g.st.Reader(), feedID, since)
	if err != nil {
		return false, "budget_lookup_failed"
	}
	globalIn, globalOut, globalUSD, err := g.st.SpendSince(ctx, 0, since)
	if err != nil {
		return false, "budget_lookup_failed"
	}

	limits := budget.Limits{
		PerFeedDailyTokens: fs.Budgets.DailyTokens,
		PerFeedDailyRuns:   fs.Budgets.DailyRuns,
		GlobalDailyTokens:  int(settings.GetGlobalDailyTokenCeiling()),
		GlobalDailyUSD:     settings.GetGlobalDailySpendCeilingUsd(),
		// The monthly ceiling had no caller at all, so the mechanism existed
		// and bounded nothing. A daily cap does not bound a month, and §19's
		// done-criterion is stated per BILLING MONTH — unmeasurable while
		// nothing supplied this.
		MonthlyUSDCeiling: g.monthlyCeilingUSD,
	}
	feedSpend := budget.Spend{TokensIn: feedIn, TokensOut: feedOut, Runs: runs, USD: feedUSD}
	globalSpend := budget.Spend{TokensIn: globalIn, TokensOut: globalOut, USD: globalUSD}

	// Month-to-date across every feed. Only queried when a ceiling is set:
	// an unlimited month should not pay for a wider scan on every run.
	var monthSpend budget.Spend
	if g.monthlyCeilingUSD > 0 {
		mIn, mOut, mUSD, err := g.st.SpendSince(ctx, 0, budget.MonthStart(now))
		if err != nil {
			return false, "budget_lookup_failed"
		}
		monthSpend = budget.Spend{TokensIn: mIn, TokensOut: mOut, USD: mUSD}
	}

	decision := budget.CheckRequest(limits, feedSpend, globalSpend, budget.Request{MonthlySpend: monthSpend})
	if !decision.Allow {
		return false, string(decision.Reason)
	}
	return true, ""
}

// =====================================================================
// generate.Fetcher stub — grounded-feed source fetching is not wired
// =====================================================================

// noFetcher implements generate.Fetcher with a clear, immediate error rather
// than a nil-interface panic. Wiring the real internal/sources pipeline
// (per-feed `sources` rows, conditional GET, parsing, candidate ranking)
// is a substantial feature in its own right and out of scope for this
// change; a grounded feed configured in this build fails loudly and
// legibly at generation time instead of silently at candidate-fetch time.
type noFetcher struct{}

func (noFetcher) Candidates(ctx context.Context, feedID int64) ([]sources.Candidate, error) {
	return nil, fmt.Errorf("wire: grounded-feed source fetching is not wired in this build (feed %d)", feedID)
}

// =====================================================================
// Cron scheduling glue
// =====================================================================

// realClock is schedule.Clock over the wall clock.
type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// cronJob implements schedule.Job over a parsed feedspec cron expression.
type cronJob struct {
	feedID   int64
	slug     string
	schedule schedule.Schedule
}

func (j cronJob) FeedID() int64                  { return j.feedID }
func (j cronJob) Slug() string                   { return j.slug }
func (j cronJob) Next(after time.Time) time.Time { return j.schedule.Next(after) }

// =====================================================================
// Provider semaphore sharing (PLAN.md §13)
// =====================================================================

// semaphoreProvider wraps llm.Provider so an interactive caller (Sample)
// acquires the SAME semaphore the scheduler's Runner already gates cron
// dispatch through (schedule.Runner.ProviderSemaphore) — the whole point of
// §13's "sampling draws from the same budget as scheduled generation":
// without this wrapper, sampling would have no cap of its own and could run
// unbounded concurrency alongside a full scheduler queue.
type semaphoreProvider struct {
	llm.Provider
	sem *schedule.Semaphore
}

func (p semaphoreProvider) Generate(ctx context.Context, req llm.Request) (llm.Result, error) {
	if err := p.sem.Acquire(ctx); err != nil {
		return llm.Result{}, err
	}
	defer p.sem.Release()
	return p.Provider.Generate(ctx, req)
}

func (p semaphoreProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if err := p.sem.Acquire(ctx); err != nil {
		return nil, err
	}
	defer p.sem.Release()
	return p.Provider.Embed(ctx, texts)
}

// =====================================================================
// generate.Executor — the scheduler's per-feed dispatch target
// =====================================================================

// genExecutor implements schedule.Executor. It does NOT acquire the
// provider semaphore itself: schedule.Runner.runOne already holds it for
// the whole call (runner.go), and acquiring a second time here would
// double-count against the same cap (and self-deadlock at
// AFF_PROVIDER_MAX_INFLIGHT=1). provider is therefore the RAW, unwrapped
// provider — see wireRunExecutor and semaphoreProvider for the paths that
// DO need to acquire it themselves because they run outside the Runner.
type genExecutor struct {
	st       *store.Store
	provider llm.Provider
	novelty  generate.Novelty
	fetcher  generate.Fetcher
	ids      ids.Source
	prices   *budget.Table
	metrics  *obs.Metrics
	inv      publish.Invalidator
	log      *slog.Logger
	holder   string
}

func (e *genExecutor) Execute(ctx context.Context, feedID int64, trigger string) error {
	row, err := loadFeedRow(ctx, e.st.Reader(), feedID)
	if err != nil {
		return err
	}
	fs, err := specFromRow(row)
	if err != nil {
		return err
	}

	runID, err := e.st.StartRun(ctx, feedID, trigger, e.holder)
	if errors.Is(err, store.ErrRunInProgress) {
		// Not a failure: the Runner's own single-flight check should have
		// prevented this, but the DB lock is the authoritative check (§13),
		// so a race here is expected-and-harmless, not something that
		// should count against the feed's consecutive-failure total.
		return nil
	}
	if err != nil {
		return fmt.Errorf("wire: starting run for feed %d: %w", feedID, err)
	}

	deps := generate.Deps{
		Store:    genStoreAdapter{st: e.st, runID: runID},
		Novelty:  e.novelty,
		Fetcher:  e.fetcher,
		Provider: e.provider,
		IDs:      e.ids,
		Metrics:  e.metrics,
	}
	result, err := generate.Run(ctx, deps, row.Feed, generateSpecFrom(fs, trigger, e.prices))
	if err == nil && result.Run.Status == generate.StatusCompleted && e.inv != nil {
		// RULE-6: every write that changes a feed's published shape
		// invalidates its render cache, AFTER commit, never before (a
		// canceled/rolled-back run must not drop good cached content for a
		// change that never happened).
		e.inv.InvalidateFeed(row.Slug)
	}
	return err
}

// =====================================================================
// FeedRunExecutor — RunNow's manual dispatch (rpc.FeedRunExecutor)
// =====================================================================

// wireRunExecutor implements rpc.FeedRunExecutor. FeedServer.RunNow
// (internal/rpc/feed.go) already calls store.StartRun itself before handing
// off here, so ExecuteRun's only job is to run the SAME pipeline the
// scheduler uses against that already-open run, in the background (RunNow's
// contract: "must not block the RPC caller"), gated by the SAME provider
// semaphore the scheduler uses — a human mashing "Run now" repeatedly must
// not be able to exceed AFF_PROVIDER_MAX_INFLIGHT any more than the
// scheduler itself can.
type wireRunExecutor struct {
	st       *store.Store
	provider llm.Provider
	novelty  generate.Novelty
	fetcher  generate.Fetcher
	ids      ids.Source
	prices   *budget.Table
	metrics  *obs.Metrics
	inv      publish.Invalidator
	sem      *schedule.Semaphore
	log      *slog.Logger
}

func (e *wireRunExecutor) ExecuteRun(feedID, runID int64) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), manualRunTimeout)
		defer cancel()

		if err := e.sem.Acquire(ctx); err != nil {
			e.log.Warn("manual run: provider capacity not available before timeout",
				"feed_id", feedID, "run_id", runID, "error", err)
			_ = e.st.FailRun(ctx, runID, "provider_semaphore_timeout", err.Error(), store.RunSummary{})
			return
		}
		defer e.sem.Release()

		row, err := loadFeedRow(ctx, e.st.Reader(), feedID)
		if err != nil {
			_ = e.st.FailRun(ctx, runID, "feed_lookup_failed", err.Error(), store.RunSummary{})
			return
		}
		fs, err := specFromRow(row)
		if err != nil {
			_ = e.st.FailRun(ctx, runID, "spec_parse_failed", err.Error(), store.RunSummary{})
			return
		}

		deps := generate.Deps{
			Store:    genStoreAdapter{st: e.st, runID: runID},
			Novelty:  e.novelty,
			Fetcher:  e.fetcher,
			Provider: e.provider,
			IDs:      e.ids,
			Metrics:  e.metrics,
		}
		result, err := generate.Run(ctx, deps, row.Feed, generateSpecFrom(fs, "manual", e.prices))
		if err == nil && result.Run.Status == generate.StatusCompleted && e.inv != nil {
			e.inv.InvalidateFeed(row.Slug)
		}
		if err != nil {
			e.log.Warn("run.finished", "feed_slug", row.Slug, "run_id", runID,
				"outcome", "failed", "reason", err.Error())
		}
	}()
}

// =====================================================================
// Sample's dependencies (rpc.SampleServerConfig)
// =====================================================================

// feedLookup implements rpc's smpFeedLookup.
type feedLookup struct {
	st     *store.Store
	prices *budget.Table
}

func (l feedLookup) GetFeedForSample(ctx context.Context, feedID int64) (model.Feed, generate.Spec, error) {
	row, err := loadFeedRow(ctx, l.st.Reader(), feedID)
	if err != nil {
		return model.Feed{}, generate.Spec{}, err
	}
	fs, err := specFromRow(row)
	if err != nil {
		return model.Feed{}, generate.Spec{}, err
	}
	return row.Feed, generateSpecFrom(fs, "", l.prices), nil
}

// sampleBudget implements rpc's smpBudgetChecker over the identical
// budget.Check machinery genGate uses for scheduled runs — PLAN.md §13's
// "sampling draws from the same budget" requirement, satisfied by literally
// sharing the arithmetic, not just the intent.
type sampleBudget struct {
	st *store.Store
}

func (b sampleBudget) CheckSample(ctx context.Context, feedID int64, projected int) budget.Decision {
	settings, err := loadGenerationSettings(ctx, b.st.Reader(), true)
	if err != nil {
		return budget.Decision{Allow: false, Reason: "settings_lookup_failed"}
	}
	if !settings.GetEnabled() {
		return budget.Decision{Allow: false, Reason: "generation_disabled"}
	}

	row, err := loadFeedRow(ctx, b.st.Reader(), feedID)
	if err != nil {
		return budget.Decision{Allow: false, Reason: "feed_lookup_failed"}
	}
	fs, err := specFromRow(row)
	if err != nil {
		return budget.Decision{Allow: false, Reason: "spec_parse_failed"}
	}

	since := midnightUTC(time.Now())
	feedIn, feedOut, feedUSD, err := b.st.SpendSince(ctx, feedID, since)
	if err != nil {
		return budget.Decision{Allow: false, Reason: "budget_lookup_failed"}
	}
	runs, err := countRunsSince(ctx, b.st.Reader(), feedID, since)
	if err != nil {
		return budget.Decision{Allow: false, Reason: "budget_lookup_failed"}
	}
	globalIn, globalOut, globalUSD, err := b.st.SpendSince(ctx, 0, since)
	if err != nil {
		return budget.Decision{Allow: false, Reason: "budget_lookup_failed"}
	}

	limits := budget.Limits{
		PerFeedDailyTokens: fs.Budgets.DailyTokens,
		PerFeedDailyRuns:   fs.Budgets.DailyRuns,
		GlobalDailyTokens:  int(settings.GetGlobalDailyTokenCeiling()),
		GlobalDailyUSD:     settings.GetGlobalDailySpendCeilingUsd(),
	}
	feedSpend := budget.Spend{TokensIn: feedIn, TokensOut: feedOut, Runs: runs, USD: feedUSD}
	globalSpend := budget.Spend{TokensIn: globalIn, TokensOut: globalOut, USD: globalUSD}

	return budget.CheckRequest(limits, feedSpend, globalSpend, budget.Request{Kind: budget.KindSample, Projected: projected})
}

func (b sampleBudget) RemainingDailyUSD(ctx context.Context, feedID int64) (float64, error) {
	settings, err := loadGenerationSettings(ctx, b.st.Reader(), true)
	if err != nil {
		return 0, err
	}
	ceiling := settings.GetGlobalDailySpendCeilingUsd()
	if ceiling <= 0 {
		return 0, nil // no global ceiling configured: nothing meaningful to report
	}
	_, _, usd, err := b.st.SpendSince(ctx, 0, midnightUTC(time.Now()))
	if err != nil {
		return 0, err
	}
	remaining := ceiling - usd
	if remaining < 0 {
		remaining = 0
	}
	return remaining, nil
}

// =====================================================================
// buildScheduler
// =====================================================================

// buildScheduler assembles the cron loop (PLAN.md §9, §13, §14.3): one Job
// per enabled, non-aggregate feed, an Executor that runs generate.Run, and a
// Gate combining the live kill switch with the §13 budget check. provider,
// nov, prices and metrics are constructed once by the caller (runAll) and
// passed in rather than rebuilt here, so the scheduler and the control
// plane's RunNow/Sample paths share identical novelty/pricing/metrics
// configuration — only the provider SEMAPHORE (not the provider itself)
// needs to be the literal same instance (§13); sharing the rest avoids two
// independently-editable copies of the same config drifting apart. metrics
// feeds both schedule.WithMetrics (the Runner's own aff_runs_total
// recording for gate-blocked dispatch) and generate.Deps.Metrics (via
// genExecutor, for every run that actually executes).
func buildScheduler(
	ctx context.Context,
	st *store.Store,
	cfg *config.Config,
	provider llm.Provider,
	nov generate.Novelty,
	prices *budget.Table,
	metrics *obs.Metrics,
	inv publish.Invalidator,
	log *slog.Logger,
) (*schedule.Runner, error) {
	rows, err := loadSchedulableFeedRows(ctx, st.Reader())
	if err != nil {
		return nil, fmt.Errorf("wire: building scheduler: %w", err)
	}

	jobs := make([]schedule.Job, 0, len(rows))
	for _, row := range rows {
		fs, err := specFromRow(row)
		if err != nil {
			log.Warn("scheduler: skipping feed with unparsable recipe", "feed_slug", row.Slug, "error", err)
			continue
		}
		loc, err := time.LoadLocation(fs.Timezone)
		if err != nil {
			loc = time.UTC
		}
		sched, err := schedule.Parse(fs.Cron, loc)
		if err != nil {
			log.Warn("scheduler: skipping feed with unparsable cron", "feed_slug", row.Slug, "error", err)
			continue
		}
		jobs = append(jobs, cronJob{feedID: row.ID, slug: row.Slug, schedule: sched})
	}

	exec := &genExecutor{
		st:       st,
		provider: provider,
		novelty:  nov,
		fetcher:  noFetcher{},
		ids:      ids.NewSource(),
		prices:   prices,
		metrics:  metrics,
		inv:      inv,
		log:      log,
		holder:   "scheduler",
	}
	gate := &genGate{st: st, cfg: cfg, prices: prices, log: log, monthlyCeilingUSD: cfg.MonthlySpendCeilingUSD}

	r := schedule.New(realClock{}, jobs, exec, gate,
		schedule.WithMaxConcurrent(cfg.MaxConcurrentRuns),
		schedule.WithProviderLimit(cfg.ProviderMaxInflight),
		schedule.WithJitterWindow(cfg.ScheduleJitter),
		schedule.WithLogger(log),
		schedule.WithMetrics(metrics),
	)
	return r, nil
}

// =====================================================================
// Admin listener mux — bridge (control plane) + static (admin UI bundle)
// =====================================================================

// adminMux is the ADMIN listener's top-level handler (PLAN.md §2, §16).
//
// An earlier version gave the bridge "/" and static everything else, because
// wsconn.DefaultEndpoint was "/" and web/wsconn was out of that change's
// scope. Running the server exposed what that costs, and it is not subtle:
// the SPA declares <base href="/"> and so must LOAD at "/", which returned
// the bridge's 401; and every client-side route (/login, /generate) returned
// 404 because no asset lives at those paths. The admin UI was unreachable at
// its own base URL and every deep link and page refresh was broken — none of
// it visible to any test, because no test loads the app over HTTP.
//
// So the bridge now owns one explicit path, bridgeEndpoint, and the UI owns
// the rest. Two rules:
//
//   - A path that names a real asset is served as that asset.
//   - Anything else is served index.html, so the WASM router can resolve it.
//     This is the server half of <base href>; without it the client router
//     never gets the chance to run, because the document never loads.
//
// wsconn.DefaultEndpoint must equal bridgeEndpoint. A mismatch here fails as
// "the UI cannot connect" with no useful error anywhere, discoverable only in
// a browser — so it is asserted in wire_test.go rather than left to comments.
//
// static is nil when the bundle directory was absent at boot (the normal
// state before `make web` has run). Then every path falls through to the
// bridge, which is the pre-static behaviour: unauthenticated requests get
// 401, nothing panics.
type adminMux struct {
	bridge http.Handler
	static *publish.StaticHandler
}

// bridgeEndpoint is the control-plane WebSocket path. It must stay in sync
// with web/wsconn.DefaultEndpoint.
//
// There is deliberately no HTTP `/auth/login` (or `/auth/logout`) route
// alongside it anymore. One used to live here, in internal/bridge/httpauth.go,
// because the bridge's own ServeHTTP required a valid session cookie before
// it would even attempt the WebSocket upgrade — a browser with no cookie
// yet could not open the control-plane socket at all, and AuthService.Login
// (the only thing that mints one) lived nowhere else, so there was no way
// to ever get from "no session" to "session." That was worked around with
// a plain HTTP side door, which itself violated PLAN.md §3's "gRPC, no
// HTTP" rule for the control plane. The actual fix is internal/bridge/
// server.go now allowing an ANONYMOUS WebSocket upgrade (internal/rpc's
// interceptor confines it to Login/RecoverWithCode) and Login/RecoverWithCode
// handing back a single-use login TICKET (internal/rpc/auth.go's
// SessionTicketHeader) a reconnect can present to receive the real cookie —
// see internal/bridge/ticket.go and server.go's NewServer doc comment for
// the full mechanism. That closes the chicken-and-egg gap without a second
// login mechanism, which is the property httpauth.go's removal is for:
// there is now exactly one way to authenticate a browser, the bridge.
const bridgeEndpoint = "/grpc"

func (m *adminMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == bridgeEndpoint || m.static == nil {
		m.bridge.ServeHTTP(w, r)
		return
	}
	if !m.static.Has(r.URL.Path) {
		// A client-side route, not a missing asset. Serve the shell and let
		// the router decide — including for an unknown route, which the
		// client renders as its own not-found rather than as a bare HTTP 404
		// with no way back into the app.
		r = r.Clone(r.Context())
		r.URL.Path = "/"
	}
	m.static.ServeHTTP(w, r)
}

// =====================================================================
// Admin listener router — plain gRPC (cmd/aff) vs. everything else
// =====================================================================

// adminRouter is the ADMIN listener's actual top-level handler: it sits in
// front of adminMux and gives cmd/aff a real transport to dial.
//
// Before this type existed, the admin listener was ONLY adminMux — a plain
// HTTP mux serving the bridge's WebSocket tunnel and the admin UI static
// bundle. cmd/aff's client.go dials with a bare grpc.NewClient(a.Server,
// grpc.WithTransportCredentials(insecure.NewCredentials())), i.e. a real
// gRPC/HTTP2 client — but no *grpc.Server was ever listening anywhere on
// that address, so every CLI command failed at the transport, and
// specifically `aff login` reported the exact same "authentication failed"
// a wrong password produces (auth_cmd.go's errAuthFailed-shaped message),
// because the RPC never reached the server at all. This is what closes that
// gap: *grpc.Server implements http.Handler (grpc-go's own doc comment on
// Server.ServeHTTP), so the SAME admin port can serve gRPC directly by
// routing on the request shape, with no second listener and no change to
// the §2 two-listener boundary (this is still the ADMIN listener only).
//
// Routing rule: an HTTP/2 request whose Content-Type is (or begins)
// "application/grpc" is gRPC — grpc-go's own client always sets exactly
// this, and it is the same signature (*grpc.Server).ServeHTTP checks
// internally, so this router agrees with the server it hands requests to
// about what counts as a gRPC request. Everything else — the bridge's
// WebSocket upgrade (HTTP/1.1) and the admin UI's ordinary asset/page
// fetches — falls through to mux unchanged.
type adminRouter struct {
	grpcServer *grpc.Server
	mux        http.Handler
}

func (r *adminRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.ProtoMajor == 2 && strings.HasPrefix(req.Header.Get("Content-Type"), "application/grpc") {
		r.grpcServer.ServeHTTP(w, req)
		return
	}
	r.mux.ServeHTTP(w, req)
}

// =====================================================================
// buildControlPlane
// =====================================================================

// buildControlPlane constructs all six RPC services, registers them on a
// grpc.Server behind the session interceptor (internal/rpc/interceptor.go),
// and wraps that in the bridge (WebSocket) handler with the Origin allowlist
// from cfg.AllowedOrigins.
//
// Deviates from a bare (st, cfg, inv, log) parameter list by three
// arguments — provider, nov, prices, sem — because SampleServer and
// FeedServer's RunNow executor both make real provider calls, and §13
// requires those calls to share the scheduler's exact semaphore instance
// (sem) and, for consistency, the same novelty/pricing configuration
// (provider, nov, prices) rather than a second, independently-constructed
// copy of each. Building the four independently inside this function was
// tried first and rejected for exactly that "same instance" requirement:
// there is no way to obtain the scheduler's semaphore from within this
// function's body without either constructing the scheduler here too
// (out of scope for this function's stated purpose) or accepting it as a
// parameter.
func buildControlPlane(
	st *store.Store,
	cfg *config.Config,
	inv publish.Invalidator,
	log *slog.Logger,
	provider llm.Provider,
	nov generate.Novelty,
	prices *budget.Table,
	sem *schedule.Semaphore,
	metrics *obs.Metrics,
) (http.Handler, error) {
	// tickets is the single-use login-ticket store Login/RecoverWithCode mint
	// from for a bridge-transport caller (internal/rpc/auth.go's
	// SessionTicketHeader) and internal/bridge/server.go's ServeHTTP redeems
	// on the reconnect that follows — see ticket.go's package doc comment.
	// ONE instance, shared between the two, is what makes "the ticket Login
	// minted is the ticket the upgrade redeems" true; two independently
	// constructed stores would never agree on what tickets exist.
	tickets := bridge.NewTicketStore()

	authOpts := []rpc.AuthServerOption{rpc.WithTicketStore(tickets)}
	// AFF_DEV_INSECURE_AUTH drops TOTP replay rejection and login backoff so
	// the dev build's one-click prefilled sign-in actually works on the
	// second click. Read with os.Getenv rather than through internal/config
	// deliberately: this must never become a documented, first-class setting
	// that looks deployable, and it must never appear in deploy/env
	// templates.
	//
	// The loopback check is the real guard. A bound address of 127.0.0.1 or
	// ::1 means nothing off this machine can reach the admin plane at all,
	// so the controls being dropped are ones that could only ever have been
	// protecting against the operator themselves. Anything else — including
	// the 0.0.0.0 an env file copied to a real host would carry — refuses
	// and says so, rather than honouring the variable quietly.
	if os.Getenv("AFF_DEV_INSECURE_AUTH") == "1" {
		if isLoopbackAddr(cfg.AdminAddr) {
			authOpts = append(authOpts, rpc.WithDevInsecureAuth())
			slog.Warn("AFF_DEV_INSECURE_AUTH=1: TOTP replay rejection and login backoff are DISABLED. " +
				"Never run this on anything reachable from a network.")
		} else {
			slog.Error("AFF_DEV_INSECURE_AUTH=1 ignored: the admin listener is not loopback-only",
				"admin_addr", cfg.AdminAddr)
		}
	}
	authSrv, err := rpc.NewAuthServer(st, []byte(cfg.SecretKey.Reveal()), authOpts...)
	if err != nil {
		return nil, fmt.Errorf("wire: building auth server: %w", err)
	}

	sampledProvider := semaphoreProvider{Provider: provider, sem: sem}

	feedSrv := rpc.NewFeedServer(st, inv, &wireRunExecutor{
		st: st, provider: provider, novelty: nov, fetcher: noFetcher{},
		ids: ids.NewSource(), prices: prices, metrics: metrics, inv: inv, sem: sem, log: log,
	})
	itemSrv := rpc.NewItemServer(st, inv, ids.NewSource())
	runSrv := rpc.NewRunServer(st, log)
	sysSrv := rpc.NewSystemServer(st, log,
		rpc.WithVersionInfo(version, commit, time.Now()),
		rpc.WithPriceSink(func(entries []*affv1.PriceEntry) { applyPriceTable(prices, entries) }),
		rpc.WithDefaultGenerationEnabled(cfg.GenerationEnabled))
	sampleSrv := rpc.NewSampleServer(rpc.SampleServerConfig{
		Feeds:      feedLookup{st: st, prices: prices},
		GenStore:   genStoreAdapter{st: st},
		Novelty:    nov,
		Fetcher:    noFetcher{},
		Provider:   sampledProvider,
		IDs:        ids.NewSource(),
		Budget:     sampleBudget{st: st},
		Samples:    st,
		PublicHost: cfg.PublicBaseURL.Host,
		Generator:  "AnimeFeedFlux " + version,
		Enabled: func(ctx context.Context) (bool, string, error) {
			settings, err := loadGenerationSettings(ctx, st.Reader(), cfg.GenerationEnabled)
			if err != nil {
				return false, "", err
			}
			if !settings.GetEnabled() {
				return false, "generation_disabled", nil
			}
			return true, "", nil
		},
	})

	register := func(gs *grpc.Server) {
		affv1.RegisterAuthServiceServer(gs, authSrv)
		affv1.RegisterFeedServiceServer(gs, feedSrv)
		affv1.RegisterItemServiceServer(gs, itemSrv)
		affv1.RegisterRunServiceServer(gs, runSrv)
		affv1.RegisterSampleServiceServer(gs, sampleSrv)
		affv1.RegisterSystemServiceServer(gs, sysSrv)
	}

	// plainGRPCServer is a SECOND *grpc.Server, distinct from the one
	// bridge.NewServer builds internally for its WebSocket tunnel (that one
	// is never exposed outside internal/bridge — there is no seam to reuse
	// it here). Both multiplexers register the SAME service objects
	// (authSrv, feedSrv, ...) constructed once above, so a request answered
	// through this direct path and one answered through the bridge tunnel
	// run identical business logic; only the framing differs. The
	// interceptor chain is copied from bridge.Config.GRPCOptions below
	// (authSrv.UnaryInterceptor/StreamInterceptor) so a direct gRPC caller
	// gets the exact same per-RPC session re-check the bridge path gets —
	// PLAN.md §11's "the CLI is a client of the same AuthService the UI
	// uses, not a bypass of it" applies to authorization, not just routing.
	plainGRPCServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(authSrv.UnaryInterceptor()),
		grpc.ChainStreamInterceptor(authSrv.StreamInterceptor()),
	)
	register(plainGRPCServer)

	// The bridge's own SessionValidator (below) checks the cookie once at
	// WebSocket upgrade and periodically thereafter (internal/bridge's own
	// concern, session identity only). internal/rpc's interceptor
	// (authSrv.UnaryInterceptor/StreamInterceptor) re-checks on every single
	// RPC (PLAN.md §4: "authenticated at upgrade, trusted forever" is
	// exactly what that guards against), using the raw cookie value.
	//
	// Getting that raw value to the interceptor used to be this file's
	// problem, solved here with an HTTP middleware. It is now the bridge's:
	// ServeHTTP sets Session.Token from the cookie it already validated,
	// unconditionally, so no composition root can wire the bridge up and
	// silently get no token. That mattered because the omission failed as
	// "every RPC is unauthenticated" with nothing pointing at the cause —
	// two callers hit the same seam and improvised two different
	// workarounds before it was closed properly.
	// closes that gap; see its comment.
	validator := bridge.SessionValidatorFunc(func(ctx context.Context, token string, now time.Time) (bridge.Session, error) {
		sess, err := st.GetSessionByTokenHash(ctx, auth.HashToken(token))
		if err != nil {
			return bridge.Session{}, err
		}
		if !sess.Valid(now, auth.SessionIdleTimeout) {
			return bridge.Session{}, fmt.Errorf("wire: session no longer valid")
		}
		return bridge.Session{UserID: sess.TokenHash, ExpiresAt: sess.ExpiresAt}, nil
	})

	bridgeHandler, err := bridge.NewServer(bridge.Config{
		SessionCookieName: auth.CookieName(),
		AllowedOrigins:    cfg.AllowedOrigins,
		Validator:         validator,
		// Tickets is the SAME store authSrv (rpc.WithTicketStore, above)
		// mints into — see that construction's comment for why sharing one
		// instance is load-bearing. This is what lets an anonymous upgrade's
		// Login/RecoverWithCode call turn a following reconnect into an
		// authenticated one (server.go's ServeHTTP), which is the mechanism
		// that replaces the removed HTTP `/auth/login` side door.
		Tickets: tickets,
		GRPCOptions: []grpc.ServerOption{
			grpc.ChainUnaryInterceptor(authSrv.UnaryInterceptor()),
			grpc.ChainStreamInterceptor(authSrv.StreamInterceptor()),
		},
	}, register)
	if err != nil {
		return nil, fmt.Errorf("wire: building bridge server: %w", err)
	}

	// The admin UI bundle (internal/publish/static.go's own doc comment is
	// explicit that this is where it must be mounted: the ADMIN listener,
	// never the public publish plane). A missing directory is the normal
	// state before web/build.sh / `make web` has run — NOT a fatal boot
	// error here: the control plane (this bridge, and the CLI at
	// cmd/aff, which is a gRPC client against the same services per
	// PLAN.md §11 — "no privileged back door") is fully usable without it,
	// and wire_test.go's own tests boot runAll against exactly this
	// unbuilt state. Treating it as fatal would mean a fresh clone cannot
	// even start the server to run `go test`. It is still a clear,
	// loud STARTUP log — never a per-request panic, and never silent.
	// Concrete type, not http.Handler: the mux needs Has() to tell a real
	// asset from a client-side route, and a nil *StaticHandler compares
	// correctly against nil where a nil-valued interface would not.
	var static *publish.StaticHandler
	if sh, err := publish.NewStaticHandler(cfg.AdminStaticDir); err != nil {
		log.Error("admin UI bundle not available; admin listener will serve the control-plane API only, no static UI",
			slog.String("dir", cfg.AdminStaticDir), slog.Any("error", err))
	} else {
		static = sh
	}

	return &adminRouter{
		grpcServer: plainGRPCServer,
		mux:        &adminMux{bridge: bridgeHandler, static: static},
	}, nil
}

// =====================================================================
// buildPublishHandlerWithInvalidator
// =====================================================================

// buildPublishHandlerWithInvalidator is buildPublishHandler (server.go)
// with one difference: it calls publish.NewServerAndInvalidator instead of
// publish.NewServer, so this file can hand the SAME *publish.Cache instance
// the publish listener answers requests from to every mutating RPC service
// (RULE-6). server.go cannot be edited to return the invalidator itself
// (out of scope for this change), and publish.NewServer's return type (bare
// http.Handler) has no hook back to the Cache it built — see
// internal/publish/invalidate.go's doc comment for why that gap exists and
// why duplicating this ~15-line constructor is the documented way around
// it. Every line below except the last mirrors buildPublishHandler exactly.
func buildPublishHandlerWithInvalidator(st *store.Store, cfg *config.Config, version string, log *slog.Logger, metrics *obs.Metrics) (http.Handler, publish.Invalidator, error) {
	reader := st.Reader()

	tagYear, err := bootTagYear(context.Background(), reader)
	if err != nil {
		return nil, nil, fmt.Errorf("resolving tag year: %w", err)
	}

	deps := publish.Deps{
		// Logger and Metrics were absent here, and their absence was silent
		// in the worst way: internal/publish's handlers DO call
		// obs.HTTPRequest and Metrics.RecordHTTPRequest/RecordCacheResult on
		// every request, so the call sites looked wired and the tasks looked
		// done. With these two fields unset the metrics incremented nothing
		// and the canonical wide event fell through to an unconfigured
		// slog.Default(), leaving the entire public surface — the only part
		// of this product a subscriber ever touches — unobservable.
		Logger:  log,
		Metrics: metrics,

		// HealthFeeds is what makes /healthz report staleness rather than
		// mere liveness (§15). BuildHealthReport existed and was tested;
		// nothing supplied it, so a silently-stopped generator stayed
		// invisible on the one endpoint an uptime check watches — which is
		// precisely the absence-of-work failure §15 is written against.
		HealthFeeds: func(ctx context.Context) ([]publish.FeedHealthInput, error) {
			now := time.Now()
			statuses, err := ops.LiveFeedStatuses(ctx, st.Writer(), now)
			if err != nil {
				return nil, err
			}
			// error_count was in the /healthz JSON from the start and was
			// hardcoded to 0 for every feed, because nothing ever queried it.
			// A field that always reads zero is worse than an absent one: it
			// answers the question "is this feed erroring?" with a confident
			// no.
			errCounts, err := ops.LiveFeedErrorCounts(ctx, st.Writer(), now, 0)
			if err != nil {
				return nil, err
			}
			out := make([]publish.FeedHealthInput, 0, len(statuses))
			for _, fs := range statuses {
				out = append(out, publish.FeedHealthInput{
					FeedStatus: fs,
					ErrorCount: errCounts[fs.FeedSlug],
				})
			}
			return out, nil
		},

		// Without this, /healthz computed staleness against
		// publish.DefaultHealthGrace while the nightly webhook and `aff doctor`
		// used ops.ResolveStaleGrace() — so setting AFF_STALE_GRACE_FACTOR
		// moved two of the three surfaces and left the endpoint an uptime
		// check actually watches on the old threshold. health.go's own doc
		// comment warns against exactly this drift.
		HealthGrace: ops.ResolveStaleGrace(),
		GetFeed: func(ctx context.Context, slug string) (model.Feed, bool, error) {
			return getFeedBySlug(ctx, reader, slug)
		},
		ListItems: func(ctx context.Context, feedID int64) ([]model.Item, error) { return listItems(ctx, reader, feedID) },
		ListFeeds: func(ctx context.Context) ([]model.Feed, error) { return listFeeds(ctx, reader) },
		GetItem: func(ctx context.Context, itemKey string) (model.Feed, model.Item, bool, error) {
			return getItem(ctx, reader, itemKey)
		},
		BaseURL:   cfg.PublicBaseURL.String(),
		Generator: "AnimeFeedFlux " + version,
		DocsURL:   docsURL,
		TagYear:   tagYear,
	}

	handler, inv := publish.NewServerAndInvalidator(deps)
	return handler, inv, nil
}

// =====================================================================
// runAll — the composition root
// =====================================================================

// runAll opens the store, reclaims stale runs (openStore, server.go), builds
// the publish plane, the scheduler, and the control plane, starts all three,
// and shuts them down cleanly on ctx cancellation (PLAN.md §15).
//
// Two listeners, never one mux (§2): publishSrv and adminSrv are separate
// *http.Server values bound to cfg.PublishAddr and cfg.AdminAddr
// respectively, so the public surface has no code path to the admin one.
func runAll(ctx context.Context, cfg *config.Config, log *slog.Logger) int {
	// obs.Setup installs the real TracerProvider/MeterProvider EARLY, before
	// anything below can emit a span or a metric — every call site in this
	// process (including the store open just below) already calls
	// obs.Start/the Metrics recorders unconditionally; before this call they
	// ran against the real no-op OTel providers (obs/otel.go's package-level
	// defaults), which is cheap but means nothing was ever exported. cfg's
	// OTelEnabled/OTelExporter/TraceSampleRatio/OTelServiceName default to
	// "off" (config.go), so a deployment that never touches those variables
	// gets identical behavior to before this change.
	obsShutdown, err := obs.Setup(ctx, obs.Config{
		Enabled:     cfg.OTelEnabled,
		Exporter:    cfg.OTelExporter,
		ServiceName: cfg.OTelServiceName,
		Version:     version,
		Commit:      commit,
		SampleRatio: cfg.TraceSampleRatio,
	})
	if err != nil {
		log.Error("setting up telemetry", slog.Any("error", err))
		return exitRuntimeFail
	}

	// SchemaFlux emits its own spans (schemaflux.<operation>, one per LLM
	// call — PLAN.md §15.0a's diagram names this "llm.generate", but the
	// library itself names it "schemaflux."+operation; that is a plan
	// documentation discrepancy, not something to paper over here by
	// renaming anything) but only attaches them to OUR trace once the host
	// calls schemaflux/telemetry/otel.Install with OUR TracerProvider — the
	// library deliberately never calls otel.SetTracerProvider itself (that
	// package's own doc comment: "a library that sets it cannot be embedded
	// twice"). This MUST run after obs.Setup above, using
	// obs.GetTracerProvider() (the provider Setup just installed): calling
	// it any earlier would capture the package-level no-op default and
	// silently produce the exact gap it exists to close, just harder to
	// notice because the call site is present. Without this, the slowest
	// and most expensive span in every run (the LLM call) never appears as
	// a child of generation.run — the run just looks like unexplained
	// latency.
	schemafluxRestore, err := schemafluxotel.Install(obs.GetTracerProvider())
	if err != nil {
		log.Error("installing schemaflux telemetry", slog.Any("error", err))
		return exitRuntimeFail
	}

	// metrics is the §15.0a instrument set, built from the SAME
	// MeterProvider obs.Setup installed above — passed to both the
	// generation scheduler (schedule.WithMetrics, buildScheduler) and every
	// generate.Run call (generate.Deps.Metrics, genExecutor/
	// wireRunExecutor below) so aff_runs_total/aff_run_duration_seconds/
	// aff_tokens_total/aff_cost_usd_total are recorded from a real run,
	// not merely registered and never fed. A nil MeterProvider (OTelEnabled
	// = false) still returns a working *Metrics here — NewMetrics registers
	// against otelmetric/noop's real no-op MeterProvider in that case, which
	// records into nothing rather than needing a nil check at every call
	// site (the same "genuine no-op, not a nil check" contract obs/otel.go
	// documents for the providers themselves).
	metrics, err := obs.NewMetrics(obs.GetMeterProvider())
	if err != nil {
		log.Error("building metrics", slog.Any("error", err))
		return exitRuntimeFail
	}

	st, err := openStore(ctx, cfg)
	if err != nil {
		log.Error("opening store", slog.Any("error", err))
		return exitRuntimeFail
	}
	defer func() {
		if err := st.Close(); err != nil {
			log.Error("closing store", slog.Any("error", err))
		}
	}()

	// Registered AFTER st's own defer above, so defer's LIFO order runs
	// these two FIRST at return — flushing telemetry (and detaching
	// SchemaFlux's observer) before the store closes — which is the
	// ordering runAll's shutdown sequence needs (dispatch stopped,
	// in-flight work drained, THEN flush, THEN close the store): every
	// explicit statement below (including both schedulers' drain waits)
	// runs and completes before any defer fires, so by the time these run,
	// dispatch is already stopped and drained regardless of which return
	// path got here.
	defer schemafluxRestore()
	// obs.Setup's shutdown func self-bounds to 5s if the context it's given
	// has no deadline of its own; httpShutdownTimeout gives it an explicit
	// one here instead of relying on that fallback.
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), httpShutdownTimeout)
		defer cancel()
		if err := obsShutdown(shutdownCtx); err != nil {
			log.Error("shutting down telemetry", slog.Any("error", err))
		}
	}()

	if cfg.SlackWebhookURL == "" {
		// Visible, not silent: a disabled webhook is a valid, safe
		// configuration (ops.Notify/NotifyBackupAlert/NotifyOpsAlert all
		// no-op cleanly against an empty URL), but the whole point of the
		// nightly alerting subsystem is that its absence must never be
		// discoverable only by noticing, weeks later, that nothing ever
		// alerted.
		log.Warn("AFF_SLACK_WEBHOOK_URL not set; nightly backup/prune/staleness alerting is disabled")
	}

	publishHandler, inv, err := buildPublishHandlerWithInvalidator(st, cfg, version, log, metrics)
	if err != nil {
		log.Error("building publish handler", slog.Any("error", err))
		return exitRuntimeFail
	}

	provider, err := llm.NewSchemaFluxProvider(llm.Config{APIKey: cfg.ProviderAPIKey.Reveal()})
	if err != nil {
		log.Error("building llm provider", slog.Any("error", err))
		return exitRuntimeFail
	}
	embedder := novelty.NewOpenAIEmbedder(cfg.ProviderAPIKey.Reveal(), embeddingModel, embeddingDim)
	nov := noveltyAdapter{st: st, embedder: embedder, threshold: defaultNoveltyThreshold, window: defaultNoveltyWindow}
	prices := budget.NewTable()
	if err := loadPriceTableAtBoot(ctx, st.Reader(), prices); err != nil {
		// Not fatal: an unreadable price row degrades to "cost unknown",
		// which internal/budget already treats as a reason to refuse rather
		// than as free. Logged loudly because that refusal will otherwise
		// look inexplicable.
		log.Error("loading price table", slog.Any("error", err))
	}

	// runCtx is derived from ctx (not ctx itself) so the scheduler can be
	// stopped unconditionally during shutdown even when shutdown was
	// triggered by a listener failure rather than by ctx's own
	// cancellation (SIGTERM) — see the select below.
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	scheduler, err := buildScheduler(runCtx, st, cfg, provider, nov, prices, metrics, inv, log)
	if err != nil {
		log.Error("building scheduler", slog.Any("error", err))
		return exitRuntimeFail
	}
	// The kill switch (AFF_GENERATION_ENABLED / SetGenerationEnabled) is
	// enforced inside genGate.Allowed, consulted before every dispatch — the
	// scheduler loop itself always starts and always keeps the feed serving
	// via the publish plane regardless of its value (PLAN.md §13).
	sem := scheduler.ProviderSemaphore()

	// The ops scheduler (internal/ops.Scheduler, PLAN.md §15.4) is a SEPARATE
	// scheduler from the generation cron dispatcher above: it runs once
	// nightly (backup, prune, the staleness watchdog), not per-feed cron
	// jobs, and it must not be merged into the generation scheduler's loop
	// — see schedule.go's own package doc for why the nightly maintenance
	// job stays in-process here rather than becoming a second cron entry.
	// It shares the SAME metrics instance built above, so its
	// aff_feed_staleness_seconds observations land on the one real
	// pipeline obsShutdown flushes at shutdown.
	opsScheduler := ops.NewScheduler(ops.SchedulerConfig{
		Logger:     log,
		DBPath:     cfg.DBPath,
		BackupDir:  cfg.BackupDir,
		OffsiteDir: cfg.OffsiteDir,
		Writer:     st.Writer(),
		PruneOpts: ops.PruneOptions{
			RunRetention:    nightlyRunRetention,
			EmbeddingWindow: defaultNoveltyWindow,
		},
		StaleFn: func(ctx context.Context, now time.Time) ([]ops.FeedStatus, error) {
			return ops.LiveFeedStatuses(ctx, st.Writer(), now)
		},
		WebhookURL: cfg.SlackWebhookURL,
		Metrics:    metrics,
	})

	controlHandler, err := buildControlPlane(st, cfg, inv, log, provider, nov, prices, sem, metrics)
	if err != nil {
		log.Error("building control plane", slog.Any("error", err))
		return exitRuntimeFail
	}

	publishSrv := &http.Server{
		Addr:              cfg.PublishAddr,
		Handler:           publishHandler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 16,
	}
	adminSrv := &http.Server{
		Addr:    cfg.AdminAddr,
		Handler: controlHandler,
		// UnencryptedHTTP2 makes the admin listener understand cleartext
		// HTTP/2 — grpc-go's client (cmd/aff, dialing with
		// insecure.NewCredentials()) speaks HTTP/2 with prior knowledge,
		// sending the h2c client preface directly over the plain TCP
		// connection rather than negotiating it via TLS ALPN or an
		// HTTP/1.1 Upgrade. Without it, net/http's default protocol set is
		// HTTP/1.1 plus TLS-only h2: the connection is never promoted,
		// every gRPC frame is misread as malformed HTTP/1.1, and
		// adminRouter's req.ProtoMajor == 2 check never sees a 2 to route
		// on. HTTP1 stays in the set because plain HTTP/1.1 traffic (the
		// bridge's WebSocket upgrade, the admin UI's asset/page fetches)
		// shares this listener — see adminRouter's own comment for why
		// this stays admin-only and never touches publishSrv.
		//
		// This replaced golang.org/x/net/http2/h2c, which the same lines
		// used until x/net deprecated the package in favour of this field.
		Protocols: adminProtocols(),

		ReadHeaderTimeout: 5 * time.Second,
		// No WriteTimeout/IdleTimeout on the admin listener: it carries a
		// long-lived WebSocket (GoGRPCBridge) that a fixed write deadline
		// would sever mid-session (PLAN.md §15.4's nginx-timeout note makes
		// the identical point one layer up the stack).
		MaxHeaderBytes: 1 << 16,
	}

	errCh := make(chan error, 3)
	go func() {
		log.Info("publish listener up", slog.String("addr", publishSrv.Addr))
		if err := publishSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("publish listener: %w", err)
		}
	}()
	go func() {
		log.Info("admin listener up", slog.String("addr", adminSrv.Addr))
		if err := adminSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("admin listener: %w", err)
		}
	}()

	schedDone := make(chan error, 1)
	go func() { schedDone <- scheduler.Run(runCtx) }()

	// opsScheduler shares runCtx with the generation scheduler: the same
	// cancelRun() below stops both dispatch loops together, and both are
	// drained (below) before the deferred telemetry flush runs.
	opsDone := make(chan error, 1)
	go func() { opsDone <- opsScheduler.Run(runCtx) }()

	select {
	case err := <-errCh:
		log.Error("listener failed, shutting down", slog.Any("error", err))
	case <-ctx.Done():
		log.Info("shutdown signal received, draining")
	}

	// Stop the scheduler dispatching new work immediately; Run's own
	// shutdown() (schedule/runner.go) then gives in-flight generation up to
	// schedule.DefaultShutdownTimeout to finish on its own before cancelling
	// it — "a partially-charged LLM call is given every chance to complete"
	// (PLAN.md §15).
	cancelRun()

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), httpShutdownTimeout)
	defer cancelShutdown()

	var mu sync.Mutex
	var shutdownErr error
	record := func(err error) {
		if err == nil {
			return
		}
		mu.Lock()
		shutdownErr = errors.Join(shutdownErr, err)
		mu.Unlock()
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); record(publishSrv.Shutdown(shutdownCtx)) }()
	go func() { defer wg.Done(); record(adminSrv.Shutdown(shutdownCtx)) }()
	wg.Wait()

	// Block until the scheduler has actually finished draining (or been
	// cancelled past its own timeout) — not merely until cancelRun was
	// called — so runAll never returns, and the deferred st.Close()
	// (which checkpoints the WAL) never runs, while a run is still writing.
	if err := <-schedDone; err != nil {
		record(fmt.Errorf("scheduler shutdown: %w", err))
	}
	// Same drain discipline for the ops scheduler: Scheduler.Run (unlike the
	// generation scheduler) has no in-flight-timeout of its own to wait
	// out — it fires at most once a day and simply returns nil once runCtx
	// is Done — but it must still be waited on rather than assumed finished
	// the instant cancelRun() returned, for the identical reason: a nightly
	// job that is mid-VACUUM-INTO when the deferred telemetry flush (and
	// then st.Close()) runs would be racing the very store it is backing up.
	if err := <-opsDone; err != nil {
		record(fmt.Errorf("ops scheduler shutdown: %w", err))
	}

	if shutdownErr != nil {
		log.Error("shutdown did not complete cleanly", slog.Any("error", shutdownErr))
		return exitRuntimeFail
	}
	log.Info("stopped cleanly")
	return exitOK
}

// isLoopbackAddr reports whether addr ("host:port") binds only to this
// machine. Used as the gate on AFF_DEV_INSECURE_AUTH: a loopback-only admin
// listener cannot be reached from anywhere else, which is the whole basis
// for relaxing controls that exist to stop a remote attacker.
//
// A missing or unparseable host fails CLOSED (not loopback), as does the
// empty host in ":8082" — that form binds every interface, which is exactly
// the case this must refuse.
func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// adminProtocols is the protocol set for the admin listener: HTTP/1.1 for
// the browser and the WebSocket upgrade, cleartext HTTP/2 for grpc-go's
// prior-knowledge client. Written as a helper so the set is stated once and
// so the &http.Server literal below stays readable.
func adminProtocols() *http.Protocols {
	var p http.Protocols
	p.SetHTTP1(true)
	p.SetUnencryptedHTTP2(true)
	return &p
}
