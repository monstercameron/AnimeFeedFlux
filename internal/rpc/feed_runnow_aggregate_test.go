package rpc

import (
	"testing"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// An aggregate feed has no generator: generate.Run refuses one outright
// (PLAN.md §14.2, "aggregate feeds have no generator to run"), and the
// scheduler never dispatches one because loadSchedulableFeedRows filters
// `kind != 'aggregate'`.
//
// RunNow had no such filter. It opened a run row, handed off to the executor,
// and generate.Run returned its refusal BEFORE the commit path — the one
// early return in a function whose doc says it "ALWAYS calls Store.CommitRun
// exactly once". The executor only logs that error, so the row was never
// closed: it stayed 'running' until a restart reclaimed it, the feed read as
// perpetually running, and RunNow returned a run id so the operator was told
// it had started.
//
// Each press leaked another one, too. The run lock goes stale after three
// minutes with nothing renewing it once the executor has returned, so the
// next press was not even refused as already-running.
func TestRunNowRefusesAnAggregateFeed(t *testing.T) {
	st := feedOpenTestStore(t)
	s := NewFeedServer(st, nil, nil)

	member := mustCreateFeed(t, s, feedTestFeed("member-feed"))
	agg := mustCreateFeed(t, s, feedTestAggregate("agg-feed", "Aggregate", []string{member.GetSlug()}))

	_, err := s.RunNow(t.Context(), &affv1.FeedServiceRunNowRequest{FeedId: agg.GetId()})
	if err == nil {
		t.Fatal("RunNow accepted an aggregate feed — it opens a run row nothing will ever close")
	}
	if got := status.Code(err); got != codes.FailedPrecondition {
		t.Errorf("code = %v, want FailedPrecondition", got)
	}

	// And nothing was opened: a refused request must not leave a row behind.
	var running int
	if err := st.Reader().QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM runs WHERE feed_id = ? AND status = 'running'`, agg.GetId(),
	).Scan(&running); err != nil {
		t.Fatalf("counting runs: %v", err)
	}
	if running != 0 {
		t.Errorf("%d run row(s) left open for a refused aggregate run", running)
	}

	// A generative feed is unaffected.
	if _, err := s.RunNow(t.Context(), &affv1.FeedServiceRunNowRequest{FeedId: member.GetId()}); err != nil {
		t.Errorf("RunNow on a generative feed was refused: %v", err)
	}
}
