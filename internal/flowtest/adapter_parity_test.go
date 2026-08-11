package flowtest

import (
	"testing"

	"github.com/monstercameron/AnimeFeedFlux/internal/generate"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
)

// This harness exists to exercise production behaviour, so its storeAdapter
// has to close a run the way cmd/animefeedflux's genStoreAdapter does. It did
// not: the skipped branch dropped the RunSummary and passed the free-form
// run.Error where the runs table wants a sanitized reason token. Production
// had already fixed that branch; this copy stayed behind, so every journey
// that skipped a run was validating spend accounting production no longer
// does — the worst kind of green test, one certifying a system that is not
// the one shipping.
//
// Three copies of this adapter exist (here, cmd/animefeedflux, internal/e2e)
// with nothing structurally forcing agreement. This pins the property that
// matters on all three: a run that spent tokens records them whatever its
// terminal status.
func TestStoreAdapterRecordsSpendOnEveryTerminalOutcome(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status string
	}{
		{"skipped", generate.StatusSkipped},
		{"failed", generate.StatusFailed},
		{"completed", generate.StatusCompleted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := New(t)
			ctx := t.Context()

			feed, err := w.CreateFeed(ctx, validGenerativeFeed("adapter-parity-"+tc.name))
			if err != nil {
				t.Fatalf("CreateFeed: %v", err)
			}

			adapter := storeAdapter{s: w.Store}
			err = adapter.CommitRun(ctx, generate.RunRecord{
				FeedID:     feed.ID,
				Trigger:    "cron",
				Status:     tc.status,
				ErrorKind:  "novelty_exhausted",
				Error:      "every candidate was a duplicate",
				TokensIn:   1200,
				TokensOut:  340,
				EstCostUSD: 0.0271,
			}, []model.Item{})
			if err != nil {
				t.Fatalf("CommitRun(%s): %v", tc.status, err)
			}

			var tokensIn, tokensOut int
			var cost float64
			if err := w.Store.Reader().QueryRowContext(ctx,
				`SELECT tokens_in, tokens_out, est_cost_usd FROM runs WHERE feed_id = ?`, feed.ID,
			).Scan(&tokensIn, &tokensOut, &cost); err != nil {
				t.Fatalf("reading run row: %v", err)
			}
			if tokensIn != 1200 || tokensOut != 340 {
				t.Errorf("a %s run recorded tokens %d/%d, want 1200/340", tc.status, tokensIn, tokensOut)
			}
			if cost != 0.0271 {
				t.Errorf("a %s run recorded est_cost_usd %v, want 0.0271 — SpendSince sums this column",
					tc.status, cost)
			}
		})
	}
}
