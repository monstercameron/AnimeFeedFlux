package store

import (
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
)

// Preview spend was recorded on the samples row and nowhere else, and every
// ceiling and every displayed figure reads runs. So previews cost real money
// and accumulated nothing: the daily total did not move, and CheckSample
// measured each new preview against run spend alone — preview #500 saw the
// same running total as preview #1.
//
// These tests pin the two halves of the fix: the spend is counted, and it
// survives the sample being deleted.

func sampleSpendFeed(t *testing.T, s *Store) int64 {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.Writer().ExecContext(t.Context(),
		`INSERT INTO feeds (slug, title, kind, created_at, updated_at) VALUES (?, ?, 'generative', ?, ?)`,
		"preview-feed", "Preview Feed", now, now)
	if err != nil {
		t.Fatalf("seed feed: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("feed id: %v", err)
	}
	return id
}

func TestTotalSpendSinceCountsPreviews(t *testing.T) {
	s := newTestStore(t)
	feedID := sampleSpendFeed(t, s)
	since := time.Now().Add(-time.Hour)

	if _, _, usd, err := s.TotalSpendSince(t.Context(), feedID, since); err != nil || usd != 0 {
		t.Fatalf("baseline total = %v (err %v), want 0", usd, err)
	}

	if _, err := s.PutSample(t.Context(), feedID, []byte(`{"items":[]}`), 1200, 340, 0.0271, time.Hour); err != nil {
		t.Fatalf("PutSample: %v", err)
	}

	in, out, usd, err := s.TotalSpendSince(t.Context(), feedID, since)
	if err != nil {
		t.Fatalf("TotalSpendSince: %v", err)
	}
	if usd != 0.0271 || in != 1200 || out != 340 {
		t.Errorf("total after one preview = %d/%d tokens, $%v; want 1200/340, $0.0271 — "+
			"a preview that billed but counted for nothing", in, out, usd)
	}

	// Runs-only must still mean runs-only: the split is what keeps a sample
	// from looking like a publish (§11/§22 J3).
	if _, _, runUSD, err := s.SpendSince(t.Context(), feedID, since); err != nil || runUSD != 0 {
		t.Errorf("SpendSince = $%v (err %v), want 0 — previews must not appear as runs", runUSD, err)
	}
}

// The reason the ledger exists. Discarding a preview is the ordinary thing to
// do with a bad one, and it deletes the samples row — so if the cost lived
// only there, anyone could spend without limit by discarding every result.
func TestPreviewSpendSurvivesDiscardAndExpiry(t *testing.T) {
	s := newTestStore(t)
	feedID := sampleSpendFeed(t, s)
	since := time.Now().Add(-time.Hour)

	id, err := s.PutSample(t.Context(), feedID, []byte(`{"items":[]}`), 100, 50, 0.005, time.Hour)
	if err != nil {
		t.Fatalf("PutSample: %v", err)
	}
	if err := s.DiscardSample(t.Context(), id); err != nil {
		t.Fatalf("DiscardSample: %v", err)
	}

	_, _, usd, err := s.TotalSpendSince(t.Context(), feedID, since)
	if err != nil {
		t.Fatalf("TotalSpendSince: %v", err)
	}
	if usd != 0.005 {
		t.Errorf("total after discarding the preview = $%v, want $0.005 — discarding a preview "+
			"must not erase what it cost, or the ceiling is bypassed by clicking Discard", usd)
	}

	// Expiry is the other deletion path, and it is automatic.
	if _, err := s.PruneExpiredSamples(t.Context(), time.Now().Add(2*time.Hour)); err != nil {
		t.Fatalf("PruneExpiredSamples: %v", err)
	}
	if _, _, usd, err := s.TotalSpendSince(t.Context(), feedID, since); err != nil || usd != 0.005 {
		t.Errorf("total after expiry = $%v (err %v), want $0.005 — a daily total that shrinks "+
			"as samples age is not a total", usd, err)
	}
}

// Promotion is the third deletion path: a good preview becomes a real item.
// Its cost must be counted exactly once and must not vanish with the sample.
func TestPreviewSpendSurvivesPromotion(t *testing.T) {
	s := newTestStore(t)
	feedID := sampleSpendFeed(t, s)
	since := time.Now().Add(-time.Hour)

	id, err := s.PutSample(t.Context(), feedID, []byte(`{"items":[]}`), 10, 5, 0.002, time.Hour)
	if err != nil {
		t.Fatalf("PutSample: %v", err)
	}
	if _, err := s.PromoteSample(t.Context(), id, model.Item{
		FeedID: feedID, ItemKey: "01JPREVIEW00000000000001",
		ContentHash: "preview-hash", Title: "Promoted", SummaryText: "s",
		BodyHTML: "<p>b</p>", PublishedAt: time.Now().UTC(), Origin: model.OriginSampled,
	}); err != nil {
		t.Fatalf("PromoteSample: %v", err)
	}

	if _, _, usd, err := s.TotalSpendSince(t.Context(), feedID, since); err != nil || usd != 0.002 {
		t.Errorf("total after promotion = $%v (err %v), want $0.002", usd, err)
	}
}
