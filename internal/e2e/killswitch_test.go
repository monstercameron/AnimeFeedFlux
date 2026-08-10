package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/generate"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/schedule"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// TestKillSwitch proves PLAN.md §13's kill switch stops spending without
// stopping publishing, flipped through the real SystemService RPC and
// observed against: (1) the publish plane, (2) SampleService's real
// pre-provider gate, and (3) a REAL internal/schedule.Runner whose Gate
// reads the exact settings row SystemService just wrote.
//
// internal/schedule.Runner is not wired into cmd/animefeedflux at all (see
// app.go's package doc — same unwired-control-plane finding, extended to
// the scheduler): nothing in production builds a schedule.Job/Executor pair
// for a real feed. This test therefore builds that wiring itself, the same
// way it builds the RPC/bridge wiring in app.go, rather than skip the
// assertion PLAN.md §17.5 asks for by name ("scheduled dispatch does not
// happen").
func TestKillSwitch(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e: full end-to-end suite skipped in -short mode")
	}

	ctx := context.Background()
	app := New(t)

	totpSecret, err := app.InitAdmin(ctx, adminPassword)
	if err != nil {
		t.Fatalf("InitAdmin: %v", err)
	}
	login, err := app.Login(ctx, adminPassword, totpSecret)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	defer login.Close()

	const slug = "e2e-killswitch-feed"
	createResp, err := login.Clients.Feed.Create(ctx, &affv1.FeedServiceCreateRequest{
		Feed: &affv1.Feed{
			Slug: slug, Kind: affv1.FeedKind_FEED_KIND_GENERATIVE,
			Title: "E2E Killswitch Fixture", Description: "d", Language: "en",
			Spec: &affv1.FeedSpec{
				Cron: "0 12 * * *", Timezone: "UTC", ItemsPerRun: 1, FeedWindow: 50,
				Model: "gpt-4o-mini", SystemPromptTemplate: "sys", UserPromptTemplate: "user",
				Novelty:          &affv1.NoveltySettings{NoveltyWindowItems: 50, SimilarityThreshold: 0.9},
				DailyTokenBudget: 1000000, DailyRunBudget: 50,
			},
		},
	})
	if err != nil {
		t.Fatalf("FeedService.Create: %v", err)
	}
	feedID := createResp.GetFeed().GetId()

	// --- flip the REAL kill switch, over the real bridge ---
	setResp, err := login.Clients.System.SetGenerationEnabled(ctx, &affv1.SystemServiceSetGenerationEnabledRequest{Enabled: false})
	if err != nil {
		t.Fatalf("SystemService.SetGenerationEnabled: %v", err)
	}
	if setResp.GetEnabled() {
		t.Fatal("SetGenerationEnabled(false) reported Enabled=true")
	}

	t.Run("feeds still serve", func(t *testing.T) {
		resp := app.FetchFeed(t, slug, "xml")
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("feed still served status = %d, want 200", resp.StatusCode)
		}
	})

	t.Run("sampling makes no provider call", func(t *testing.T) {
		before := app.Provider.GenerateCallCount()
		_, err := login.Clients.Sample.Sample(ctx, &affv1.SampleServiceSampleRequest{FeedId: feedID, SampleSize: 1})
		if err == nil {
			t.Fatal("Sample succeeded with the kill switch on")
		}
		if app.Provider.GenerateCallCount() != before {
			t.Fatalf("provider was called (%d -> %d) despite the kill switch",
				before, app.Provider.GenerateCallCount())
		}
	})

	t.Run("scheduled dispatch does not happen", func(t *testing.T) {
		before := app.Provider.GenerateCallCount()

		var runCountBefore int
		if err := app.Store.Writer().QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE feed_id = ?`, feedID).Scan(&runCountBefore); err != nil {
			t.Fatalf("counting runs before: %v", err)
		}

		job := alwaysDueJob{feedID: feedID, slug: slug, firstDue: app.Clock.Now()}
		exec := realExecutor{app: app}
		gate := settingsGate{app: app}

		runner := schedule.New(app.Clock, []schedule.Job{job}, exec, gate, schedule.WithJitterWindow(0))

		runCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			_ = runner.Run(runCtx)
			close(done)
		}()

		// The dispatch attempt happens synchronously inside Run's first loop
		// iteration (job is due at the clock's current instant, jitter
		// window zero) — poll briefly for the runner to have made and
		// abandoned that attempt, then stop it. No real sleep beyond a short
		// bound is needed for correctness, only to let the goroutine above
		// actually get scheduled.
		waitUntilOrFail(t, 2*time.Second, func() bool {
			return runner.QueueDepth() == 0 && runner.InFlight() == 0
		})
		cancel()
		<-done

		if app.Provider.GenerateCallCount() != before {
			t.Fatalf("scheduler reached the LLM provider (%d -> %d) with the kill switch on",
				before, app.Provider.GenerateCallCount())
		}
		var runCountAfter int
		if err := app.Store.Writer().QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE feed_id = ?`, feedID).Scan(&runCountAfter); err != nil {
			t.Fatalf("counting runs after: %v", err)
		}
		if runCountAfter != runCountBefore {
			t.Fatalf("scheduler created a run row (%d -> %d) with the kill switch on", runCountBefore, runCountAfter)
		}
	})
}

// waitUntilOrFail polls cond every 5ms up to timeout — used only to
// synchronize on the scheduler goroutine reaching its parked state, never to
// wait out a real generation (which never happens in this test, by
// assertion).
func waitUntilOrFail(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("condition not met within %s", timeout)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// alwaysDueJob implements schedule.Job for a single feed that is due
// immediately at firstDue and, if it were ever allowed to fire, would not
// fire again for an hour — that "again" branch is never exercised here
// because the Gate always refuses (kill switch on), but a Job whose Next
// never advances would busy-loop the scheduler's dispatch/refuse cycle, so
// it still needs a real forward-progressing schedule.
type alwaysDueJob struct {
	feedID   int64
	slug     string
	firstDue time.Time
}

func (j alwaysDueJob) FeedID() int64 { return j.feedID }
func (j alwaysDueJob) Slug() string  { return j.slug }
func (j alwaysDueJob) Next(after time.Time) time.Time {
	if after.Before(j.firstDue) || after.Equal(j.firstDue) {
		return j.firstDue
	}
	return after.Add(time.Hour)
}

// settingsGate implements schedule.Gate by reading the exact settings row
// SystemService.SetGenerationEnabled writes — the same helper app.go's
// generationEnabled uses for SampleServer, reused here so both the sample
// path and the scheduler path are provably reading the identical kill
// switch state rather than two independently-faked ones.
type settingsGate struct{ app *App }

func (g settingsGate) Allowed(feedID int64) (bool, string) {
	enabled, reason, err := g.app.generationEnabled(context.Background())
	if err != nil {
		return false, fmt.Sprintf("reading kill switch: %v", err)
	}
	return enabled, reason
}

// realExecutor implements schedule.Executor over the real generate.Run
// pipeline, via the same read/write store adapter shape internal/flowtest/
// harness.go's storeAdapter uses (StartRun, then CommitRun/FailRun/SkipRun
// composed into generate.Store's single CommitRun call) — reproduced here
// because that adapter is unexported in internal/flowtest and this
// package's own scheduler wiring is itself a from-scratch gap (see the file
// doc comment), not something flowtest already solved for the scheduler
// case.
type realExecutor struct{ app *App }

func (e realExecutor) Execute(ctx context.Context, feedID int64, trigger string) error {
	feed, spec, err := feedLookup{e.app.Store}.GetFeedForSample(ctx, feedID)
	if err != nil {
		return err
	}
	spec.Trigger = trigger
	deps := generate.Deps{
		Store:    runStoreAdapter{st: e.app.Store},
		Provider: e.app.Provider,
		IDs:      e.app.idSource,
		Now:      e.app.Clock.Now,
	}
	_, err = generate.Run(ctx, deps, feed, spec)
	return err
}

type runStoreAdapter struct{ st *store.Store }

func (a runStoreAdapter) RecentTitles(ctx context.Context, feedID int64, n int) ([]string, error) {
	return genStoreAdapter(a).RecentTitles(ctx, feedID, n)
}

func (a runStoreAdapter) NewestPublished(ctx context.Context, feedID int64) (time.Time, error) {
	return genStoreAdapter(a).NewestPublished(ctx, feedID)
}

func (a runStoreAdapter) CommitRun(ctx context.Context, run generate.RunRecord, items []model.Item) error {
	runID, err := a.st.StartRun(ctx, run.FeedID, run.Trigger, "e2e-scheduler")
	if err != nil {
		return err
	}
	summary := store.RunSummary{
		TokensIn: run.TokensIn, TokensOut: run.TokensOut, CostUSD: run.EstCostUSD,
		ItemsRejected: run.ItemsRejected, RejectReasons: run.RejectReasons,
	}
	switch run.Status {
	case generate.StatusCompleted:
		return a.st.CommitRun(ctx, runID, items, summary)
	case generate.StatusSkipped:
		return a.st.SkipRun(ctx, runID, run.Error)
	default:
		return a.st.FailRun(ctx, runID, run.ErrorKind, run.Error, summary)
	}
}
