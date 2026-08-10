// J8 — review and control spend (PLAN.md §22 "J8 — Review and control
// spend", TODOS.md BF-36..39).
//
// There is no spend-report RPC yet (that's SystemService/§12.5's job,
// B1-07, not built). What's real: internal/budget.Check/CheckRequest (the
// enforcement §13 describes), internal/store.SpendSince (the runs-table
// aggregation a report would read), and internal/store's samples table
// (which SpendSince deliberately does NOT include — it only sums `runs`,
// per runs.go's doc comment). So BF-39's "sampling spend appears in the same
// totals as scheduled spend" is driven here by building the combined total
// the way a real spend report will have to: runs' cost plus samples' cost,
// both read from the real store.
package flowtest

import (
	"context"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/budget"
	"github.com/monstercameron/AnimeFeedFlux/internal/generate"
)

// j8PriceModel is the model name every spec/table entry in this file prices
// under, so a *budget.Table lookup and a generate.Spec's price fields always
// agree on which row they're pricing against.
const j8PriceModel = "flowtest-model"

// j8SpecPricedFrom builds a valid generative-feed generate.Spec whose price
// fields are read from table at call time — exactly what a real run
// scheduler would do: price at the moment of the call, not at the moment the
// run row is later read back (that distinction is BF-37's whole point).
func j8SpecPricedFrom(table *budget.Table) generate.Spec {
	spec := validSampleSpec()
	spec.Model = j8PriceModel
	spec.Trigger = "manual"
	price, ok := table.Lookup(j8PriceModel)
	if !ok {
		panic("flowtest: j8 price table has no entry for " + j8PriceModel)
	}
	spec.PriceInputPerMToken = price.InputPerMTok
	spec.PriceOutputPerMToken = price.OutputPerMTok
	return spec
}

// TestJ8_SumOfRunCostsEqualsReportedTotal is BF-36.
func TestJ8_SumOfRunCostsEqualsReportedTotal(t *testing.T) {
	w := New(t)
	ctx := t.Context()

	table := budget.NewTable()
	table.Set(budget.Price{Model: j8PriceModel, InputPerMTok: 1.50, OutputPerMTok: 3.00})

	feed, err := w.CreateFeed(ctx, validGenerativeFeed("j8-sum-total"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}

	since := w.Clock.Now()
	var wantTotal float64
	for i := 0; i < 3; i++ {
		// Distinct titles: contentHash (runner.go) is sha256(slug|title|day),
		// and all 3 runs land on the same calendar day here, so a repeated
		// title would collide on items' UNIQUE(feed_id, content_hash) — a
		// self-inflicted collision unrelated to what this test checks.
		w.Provider.QueueResult(validGenerateResult(spendItemTitle(i)))
		res, err := w.RunGeneration(ctx, feed, j8SpecPricedFrom(table))
		if err != nil {
			t.Fatalf("run %d: RunGeneration: %v", i, err)
		}
		if res.Run.Status != generate.StatusCompleted {
			t.Fatalf("run %d: status = %q, want %q", i, res.Run.Status, generate.StatusCompleted)
		}
		if res.Run.EstCostUSD <= 0 {
			t.Fatalf("run %d: EstCostUSD = %v, want > 0", i, res.Run.EstCostUSD)
		}
		wantTotal += res.Run.EstCostUSD
		w.Clock.Advance(time.Second) // keep runs.started_at strictly ordered for SpendSince's >= filter
	}

	// BF-36 (§22 J8): the sum of per-run est_cost_usd equals the reported
	// total — here, store.SpendSince, the aggregation a spend report reads.
	_, _, total, err := w.Store.SpendSince(ctx, feed.ID, since)
	if err != nil {
		t.Fatalf("SpendSince: %v", err)
	}
	if total != wantTotal {
		t.Fatalf("SpendSince total = %v, want %v (sum of the 3 runs' EstCostUSD)", total, wantTotal)
	}
}

// TestJ8_EditingPriceTableDoesNotRewriteHistory is BF-37.
func TestJ8_EditingPriceTableDoesNotRewriteHistory(t *testing.T) {
	w := New(t)
	ctx := t.Context()

	table := budget.NewTable()
	table.Set(budget.Price{Model: j8PriceModel, InputPerMTok: 1.00, OutputPerMTok: 2.00})

	feed, err := w.CreateFeed(ctx, validGenerativeFeed("j8-price-history"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}

	w.Provider.QueueResult(validGenerateResult("Priced under the original rate"))
	res, err := w.RunGeneration(ctx, feed, j8SpecPricedFrom(table))
	if err != nil {
		t.Fatalf("RunGeneration: %v", err)
	}
	originalCost := res.Run.EstCostUSD
	if originalCost <= 0 {
		t.Fatalf("EstCostUSD = %v, want > 0", originalCost)
	}

	// The price table is edited AFTER the run closed — e.g. the provider
	// raised its rates and the admin updates §12.5's editor.
	table.Set(budget.Price{Model: j8PriceModel, InputPerMTok: 100.00, OutputPerMTok: 200.00})

	// BF-37 (§22 J8): editing the price table does NOT rewrite historical
	// run costs. runner.go's estimateCostUSD priced this run against the
	// Spec's price fields AT CALL TIME (captured from the table before the
	// edit above) and store.CommitRun persisted that number — nothing
	// re-derives it from the table on read.
	run := runByID(t, w, feed.ID, findRunID(t, mustListRuns(t, w, feed.ID), "success"))
	if run.EstCostUSD != originalCost {
		t.Fatalf("persisted run cost = %v after editing the price table, want the unchanged original %v", run.EstCostUSD, originalCost)
	}
}

// TestJ8_FeedAtCapLogsSkippedRunWithDistinctStatus is BF-38. The scheduler
// that would actually call budget.Check before every run (§13) is B1-06/A7
// territory and does not exist yet, so this drives the two pieces that DO
// exist and are real — budget.Check's enforcement decision, and
// store.SkipRun's distinct terminal status — the same sequence that
// scheduler will run.
func TestJ8_FeedAtCapLogsSkippedRunWithDistinctStatus(t *testing.T) {
	w := New(t)
	ctx := t.Context()

	feed, err := w.CreateFeed(ctx, validGenerativeFeed("j8-at-cap"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}

	limits := budget.Limits{PerFeedDailyRuns: 1}
	feedSpend := budget.Spend{Runs: 1} // already at the daily cap
	decision := budget.Check(limits, feedSpend, budget.Spend{}, 0)
	if decision.Allow {
		t.Fatal("budget.Check allowed a run for a feed already at its daily run cap")
	}
	if decision.Reason != budget.ReasonFeedRunCap {
		t.Fatalf("budget.Check reason = %q, want %q", decision.Reason, budget.ReasonFeedRunCap)
	}

	runID, err := w.Store.StartRun(ctx, feed.ID, "cron", "flowtest-scheduler")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if err := w.Store.SkipRun(ctx, runID, string(decision.Reason)); err != nil {
		t.Fatalf("SkipRun: %v", err)
	}

	// BF-38 (§22 J8): a feed at its cap logs a SKIPPED run with a distinct
	// status rather than failing.
	run := runByID(t, w, feed.ID, runID)
	if run.Status != "skipped" {
		t.Fatalf("run status = %q, want %q", run.Status, "skipped")
	}
	if run.Status == "failed" {
		t.Fatal("a budget-capped run must never read as 'failed'")
	}
	if w.Provider.GenerateCallCount() != 0 {
		t.Fatalf("provider was called %d times for a run refused before it started, want 0", w.Provider.GenerateCallCount())
	}
}

// TestJ8_SamplingSpendInSameTotals is BF-39. store.SpendSince only sums
// `runs` (runs.go's doc comment: "the sum includes failed runs... [but is]
// only... runs WHERE started_at >= ?" — samples never write a runs row, by
// design, since a sample must never look like a publish, §11/§22 J3). A real
// spend report therefore has to combine BOTH sources; this test builds that
// combination directly against the store and checks it reconciles, proving
// sampling spend is available to be counted in the same total scheduled
// spend already is, not silently invisible to it.
func TestJ8_SamplingSpendInSameTotals(t *testing.T) {
	w := New(t)
	ctx := t.Context()

	table := budget.NewTable()
	table.Set(budget.Price{Model: j8PriceModel, InputPerMTok: 1.00, OutputPerMTok: 2.00})

	feed, err := w.CreateFeed(ctx, validGenerativeFeed("j8-sample-and-scheduled"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}

	since := w.Clock.Now()

	// Scheduled spend: one real run.
	w.Provider.QueueResult(validGenerateResult("A scheduled, published item"))
	runRes, err := w.RunGeneration(ctx, feed, j8SpecPricedFrom(table))
	if err != nil {
		t.Fatalf("RunGeneration: %v", err)
	}
	if runRes.Run.EstCostUSD <= 0 {
		t.Fatalf("scheduled run EstCostUSD = %v, want > 0", runRes.Run.EstCostUSD)
	}

	// Sampling spend: one dry run that publishes nothing — but still costs
	// money and draws from the SAME budget scheduled runs use (BF-13,
	// already covered by J3; this test only cares that its cost is
	// discoverable in a combined total).
	w.Provider.QueueResult(validGenerateResult("A sampled, never-published item"))
	sampleOutcome, err := w.Sample(ctx, feed, j8SpecPricedFrom(table))
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	if sampleOutcome.Result.EstCostUSD <= 0 {
		t.Fatalf("sample EstCostUSD = %v, want > 0", sampleOutcome.Result.EstCostUSD)
	}

	_, _, runsTotal, err := w.Store.SpendSince(ctx, feed.ID, since)
	if err != nil {
		t.Fatalf("SpendSince: %v", err)
	}
	samplesTotal, err := j8SamplesCostSince(ctx, w, feed.ID, since)
	if err != nil {
		t.Fatalf("summing sample cost: %v", err)
	}

	// BF-39 (§22 J8): sampling spend appears in the same totals as scheduled
	// spend — asserted by combining both sources the way a real report must,
	// and checking it reconciles to exactly the two costs above (no
	// double-counting, nothing dropped).
	combined := runsTotal + samplesTotal
	want := runRes.Run.EstCostUSD + sampleOutcome.Result.EstCostUSD
	if combined != want {
		t.Fatalf("combined runs+samples total = %v, want %v (scheduled %v + sample %v)", combined, want, runRes.Run.EstCostUSD, sampleOutcome.Result.EstCostUSD)
	}
	if samplesTotal <= 0 {
		t.Fatal("sample spend contributed nothing to the combined total — it would be invisible to a spend report")
	}
}

// spendItemTitle gives each run in TestJ8_SumOfRunCostsEqualsReportedTotal a
// distinct, contract.go-valid title (10-200 runes, no trailing punctuation).
func spendItemTitle(i int) string {
	return [...]string{
		"A spend-tracked trivia item number one",
		"A spend-tracked trivia item number two",
		"A spend-tracked trivia item number three",
	}[i]
}

// j8SamplesCostSince sums cost_usd across feedID's unexpired samples created
// at or after since — the samples-side half of the combined total a real
// spend report will need, mirroring SpendSince's own >= since convention
// (runs.go).
func j8SamplesCostSince(ctx context.Context, w *World, feedID int64, since time.Time) (float64, error) {
	var total float64
	err := w.Store.Writer().QueryRowContext(ctx,
		`SELECT COALESCE(SUM(cost_usd), 0) FROM samples WHERE feed_id = ? AND created_at >= ?`,
		feedID, since.UTC().Format(time.RFC3339Nano),
	).Scan(&total)
	return total, err
}
