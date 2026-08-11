package e2e

import (
	"context"
	"io"
	"strings"
	"testing"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/llm"
)

// TestJ6PublishCorrection drives PLAN.md §22 J6 end to end: find a published
// item, publish a correction, and verify the correction reaches
// subscribers while the original is provably untouched — same guid, same
// published_at, same content — rather than merely "still exists". RSS has
// no retraction (item.proto's PublishCorrection doc), so a correction that
// silently mutated the original instead of creating a new item would never
// redeliver to a subscriber who already saw the old version; this is the
// bug this test exists to catch, checked against the real published bytes,
// not the database row.
func TestJ6PublishCorrection(t *testing.T) {
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

	const slug = "e2e-j6-correction-feed"
	createResp, err := login.Clients.Feed.Create(ctx, &affv1.FeedServiceCreateRequest{
		Feed: &affv1.Feed{
			Slug: slug, Kind: affv1.FeedKind_FEED_KIND_GENERATIVE,
			Title: "E2E J6 Fixture", Description: "d", Language: "en",
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

	app.Provider.QueueResult(llm.Result{Items: []llm.GeneratedItem{{
		Title:       "A factually wrong trivia question",
		SummaryText: "This summary contains a mistake that needs correcting.",
		BodyHTML:    `<p>Body with an <a href="https://example.com/a">absolute link</a></p>`,
		AnswerHTML:  "<p>Wrong answer.</p>",
	}}})
	sampleResp, err := login.Clients.Sample.Sample(ctx, &affv1.SampleServiceSampleRequest{FeedId: feedID, SampleSize: 1})
	if err != nil {
		t.Fatalf("SampleService.Sample: %v", err)
	}
	candidate := sampleResp.GetCandidates()[0]
	promoteResp, err := login.Clients.Item.PromoteSample(ctx, &affv1.ItemServicePromoteSampleRequest{
		SampleId: sampleResp.GetSampleId(), CandidateId: candidate.GetCandidateId(),
	})
	if err != nil {
		t.Fatalf("ItemService.PromoteSample: %v", err)
	}
	original := promoteResp.GetItem()
	originalGuid := original.GetItemKey()
	originalPublishedAt := original.GetPublishedAt()

	// --- publish a correction: a new item, real over-the-bridge RPC ---
	const correctionTitle = "Correction: the trivia question above was wrong"
	corrResp, err := login.Clients.Item.PublishCorrection(ctx, &affv1.ItemServicePublishCorrectionRequest{
		CorrectsItemId: original.GetId(),
		Title:          correctionTitle,
		SummaryText:    "The corrected, accurate summary text.",
		BodyHtml:       "<p>The corrected body.</p>",
	})
	if err != nil {
		t.Fatalf("ItemService.PublishCorrection: %v", err)
	}
	correction := corrResp.GetItem()

	// --- the correction is its OWN item: a fresh ULID guid, strictly later
	// published_at ---
	if correction.GetItemKey() == originalGuid {
		t.Fatal("correction reused the original item's guid")
	}
	if correction.GetItemKey() == "" {
		t.Fatal("correction has an empty item_key")
	}
	if !correction.GetPublishedAt().AsTime().After(originalPublishedAt.AsTime()) {
		t.Fatalf("correction published_at (%v) is not strictly after the original's (%v)",
			correction.GetPublishedAt().AsTime(), originalPublishedAt.AsTime())
	}

	// --- the ORIGINAL is provably untouched: re-fetch it over the real
	// ItemService and diff every field a correction must never alter ---
	getOriginal, err := login.Clients.Item.Get(ctx, &affv1.ItemServiceGetRequest{Id: original.GetId()})
	if err != nil {
		t.Fatalf("ItemService.Get (original after correction): %v", err)
	}
	reread := getOriginal.GetItem()
	if reread.GetItemKey() != originalGuid {
		t.Fatalf("original guid changed: got %q, want %q", reread.GetItemKey(), originalGuid)
	}
	if !reread.GetPublishedAt().AsTime().Equal(originalPublishedAt.AsTime()) {
		t.Fatalf("original published_at changed: got %v, want %v", reread.GetPublishedAt().AsTime(), originalPublishedAt.AsTime())
	}
	if reread.GetTitle() != original.GetTitle() {
		t.Fatalf("original title changed: got %q, want %q", reread.GetTitle(), original.GetTitle())
	}
	if reread.GetSummaryText() != original.GetSummaryText() {
		t.Fatalf("original summary_text changed: got %q, want %q", reread.GetSummaryText(), original.GetSummaryText())
	}
	if reread.GetVersion() != original.GetVersion() {
		t.Fatalf("original version changed from %d to %d — PublishCorrection must not touch the original's row at all",
			original.GetVersion(), reread.GetVersion())
	}

	// --- the correction reaches subscribers: fetch the REAL published
	// bytes and confirm BOTH the correction's title is present and the
	// original's is still present, unmodified, in the same feed ---
	resp := app.FetchFeed(t, slug, "xml")
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("reading feed body: %v", err)
	}
	if !strings.Contains(string(body), correctionTitle) {
		t.Fatalf("published feed does not contain the correction's title:\n%s", body)
	}
	if !strings.Contains(string(body), original.GetTitle()) {
		t.Fatalf("published feed no longer contains the original's (unmodified) title:\n%s", body)
	}
	if !strings.Contains(string(body), originalGuid) {
		t.Fatalf("published feed does not contain the original item's guid %q:\n%s", originalGuid, body)
	}
	if !strings.Contains(string(body), correction.GetItemKey()) {
		t.Fatalf("published feed does not contain the correction's guid %q:\n%s", correction.GetItemKey(), body)
	}

	// --- the original's permalink is still resolvable (not 410, not 404) ---
	permalinkResp, err := app.publishSrv.Client().Get(app.PublishURL + "/items/" + originalGuid)
	if err != nil {
		t.Fatalf("fetching original item's permalink: %v", err)
	}
	_ = permalinkResp.Body.Close()
	if permalinkResp.StatusCode != 200 {
		t.Fatalf("original item's permalink status = %d, want 200 (still resolvable, not deleted)", permalinkResp.StatusCode)
	}
}
