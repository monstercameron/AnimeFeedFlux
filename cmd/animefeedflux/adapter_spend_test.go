package main

import (
	"testing"

	"github.com/monstercameron/AnimeFeedFlux/internal/generate"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
)

// genStoreAdapter.CommitRun is the only thing that turns a finished
// generate.RunRecord into a runs row, and that row is what SpendSince sums
// and every §13 ceiling is measured against. A skipped run is the case that
// gets this wrong quietly: the novelty-retry loop (§9 step 5) can burn tokens
// on attempt after attempt and still end in "skipped", so dropping the
// summary on that branch records real money as free and the ceiling never
// sees it.
//
// The store-level tests cover SkipRun's own handling, and internal/e2e covers
// the behaviour through a SEPARATE adapter it defines itself — so neither
// notices if this adapter stops passing the summary. This closes that.
func TestGenStoreAdapterRecordsSpendOnEveryTerminalOutcome(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status string
	}{
		{"skipped after spending on novelty retries", generate.StatusSkipped},
		{"failed after spending", generate.StatusFailed},
		{"completed", generate.StatusCompleted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t)
			feedID := seedFeed(t, st, "trivia-daily", true)
			ctx := t.Context()

			runID, err := st.StartRun(ctx, feedID, "cron", "worker-1")
			if err != nil {
				t.Fatalf("StartRun: %v", err)
			}

			adapter := genStoreAdapter{st: st, runID: runID}
			rec := generate.RunRecord{
				FeedID:     feedID,
				Status:     tc.status,
				ErrorKind:  "novelty_exhausted",
				Error:      "every candidate was a duplicate",
				TokensIn:   1200,
				TokensOut:  340,
				EstCostUSD: 0.0271,
			}
			if err := adapter.CommitRun(ctx, rec, []model.Item{}); err != nil {
				t.Fatalf("CommitRun(%s): %v", tc.status, err)
			}

			var tokensIn, tokensOut int
			var cost float64
			if err := st.Reader().QueryRowContext(ctx,
				`SELECT tokens_in, tokens_out, est_cost_usd FROM runs WHERE id = ?`, runID,
			).Scan(&tokensIn, &tokensOut, &cost); err != nil {
				t.Fatalf("reading run row: %v", err)
			}

			if tokensIn != 1200 || tokensOut != 340 {
				t.Errorf("a %s run recorded tokens %d/%d, want 1200/340 — spend the ceiling cannot see",
					tc.status, tokensIn, tokensOut)
			}
			if cost != 0.0271 {
				t.Errorf("a %s run recorded est_cost_usd %v, want 0.0271 — SpendSince sums this column, "+
					"so a zero here is money no §13 ceiling will ever count", tc.status, cost)
			}
		})
	}
}
