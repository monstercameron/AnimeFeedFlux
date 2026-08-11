package publish

import (
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/ops"
)

func TestBuildHealthReportAgeAndErrorCounts(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	lastSuccess := now.Add(-90 * time.Minute)

	report := BuildHealthReport([]FeedHealthInput{
		{
			FeedStatus: ops.FeedStatus{
				FeedSlug:      "anime-trivia-daily",
				Enabled:       true,
				Interval:      24 * time.Hour,
				LastSuccessAt: lastSuccess,
			},
			ErrorCount: 3,
		},
	}, now, 0)

	if len(report.Feeds) != 1 {
		t.Fatalf("got %d feeds, want 1", len(report.Feeds))
	}
	fh := report.Feeds[0]
	if fh.ErrorCount != 3 {
		t.Errorf("ErrorCount = %d, want 3", fh.ErrorCount)
	}
	if fh.AgeSeconds == nil {
		t.Fatalf("AgeSeconds is nil, want ~5400")
	}
	if got, want := *fh.AgeSeconds, (90 * time.Minute).Seconds(); got != want {
		t.Errorf("AgeSeconds = %v, want %v", got, want)
	}
	if fh.LastSuccessAt == nil || *fh.LastSuccessAt != lastSuccess.Format(time.RFC3339) {
		t.Errorf("LastSuccessAt = %v, want %s", fh.LastSuccessAt, lastSuccess.Format(time.RFC3339))
	}
	if fh.NeverSucceeded {
		t.Errorf("NeverSucceeded = true, want false")
	}
	if fh.Stale {
		t.Errorf("Stale = true, want false (90m << 24h*2 threshold)")
	}
	if report.Status != "ok" {
		t.Errorf("Status = %q, want ok", report.Status)
	}
	if report.StaleFeedCount != 0 {
		t.Errorf("StaleFeedCount = %d, want 0", report.StaleFeedCount)
	}
}

func TestBuildHealthReportStaleFeedIsVisiblyStale(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	lastSuccess := now.Add(-72 * time.Hour) // daily feed, default grace 2.0 -> threshold 48h

	report := BuildHealthReport([]FeedHealthInput{
		{FeedStatus: ops.FeedStatus{
			FeedSlug:      "anime-news-daily",
			Enabled:       true,
			Interval:      24 * time.Hour,
			LastSuccessAt: lastSuccess,
		}},
		{FeedStatus: ops.FeedStatus{
			FeedSlug: "anime-fact-daily",
			Enabled:  true,
			Interval: 24 * time.Hour,
			// never succeeded
		}},
	}, now, 0)

	if report.StaleFeedCount != 2 {
		t.Fatalf("StaleFeedCount = %d, want 2", report.StaleFeedCount)
	}
	if report.Status != "degraded" {
		t.Errorf("Status = %q, want degraded", report.Status)
	}

	byslug := map[string]FeedHealth{}
	for _, f := range report.Feeds {
		byslug[f.FeedSlug] = f
	}

	stale := byslug["anime-news-daily"]
	if !stale.Stale {
		t.Errorf("anime-news-daily: Stale = false, want true")
	}
	if stale.ThresholdSeconds == nil || *stale.ThresholdSeconds != (48*time.Hour).Seconds() {
		t.Errorf("ThresholdSeconds = %v, want %v", stale.ThresholdSeconds, (48 * time.Hour).Seconds())
	}

	never := byslug["anime-fact-daily"]
	if !never.Stale {
		t.Errorf("anime-fact-daily: Stale = false, want true (never succeeded)")
	}
	if !never.NeverSucceeded {
		t.Errorf("anime-fact-daily: NeverSucceeded = false, want true")
	}
	if never.AgeSeconds != nil {
		t.Errorf("anime-fact-daily: AgeSeconds = %v, want nil", never.AgeSeconds)
	}
	if never.LastSuccessAt != nil {
		t.Errorf("anime-fact-daily: LastSuccessAt = %v, want nil", never.LastSuccessAt)
	}
}

func TestBuildHealthReportDisabledAndIntervalessFeedsNeverStale(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	longAgo := now.Add(-365 * 24 * time.Hour)

	report := BuildHealthReport([]FeedHealthInput{
		{FeedStatus: ops.FeedStatus{
			FeedSlug:      "disabled-feed",
			Enabled:       false,
			Interval:      24 * time.Hour,
			LastSuccessAt: longAgo,
		}},
		{FeedStatus: ops.FeedStatus{
			FeedSlug:      "no-interval-feed",
			Enabled:       true,
			Interval:      0,
			LastSuccessAt: longAgo,
		}},
	}, now, 0)

	if report.StaleFeedCount != 0 {
		t.Fatalf("StaleFeedCount = %d, want 0", report.StaleFeedCount)
	}
	for _, f := range report.Feeds {
		if f.Stale {
			t.Errorf("%s: Stale = true, want false", f.FeedSlug)
		}
		if f.FeedSlug == "no-interval-feed" && f.ThresholdSeconds != nil {
			t.Errorf("no-interval-feed: ThresholdSeconds = %v, want nil", f.ThresholdSeconds)
		}
	}
	if report.Status != "ok" {
		t.Errorf("Status = %q, want ok", report.Status)
	}
}

func TestHealthReportHTTPStatusAlwaysOK(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	report := BuildHealthReport([]FeedHealthInput{
		{FeedStatus: ops.FeedStatus{
			FeedSlug: "never-ran",
			Enabled:  true,
			Interval: time.Hour,
		}},
	}, now, 0)
	if report.StaleFeedCount == 0 {
		t.Fatalf("test setup: expected a stale feed")
	}
	if got := report.HTTPStatus(); got != 200 {
		t.Errorf("HTTPStatus() = %d, want 200 even when feeds are stale (liveness, not readiness — see package doc comment)", got)
	}
}

func TestBuildHealthReportDefaultGraceMatchesOpsScheduleDefault(t *testing.T) {
	if DefaultHealthGrace != 2.0 {
		t.Fatalf("DefaultHealthGrace = %v, want 2.0 to match ops schedule.go's SchedulerConfig default", DefaultHealthGrace)
	}
}
