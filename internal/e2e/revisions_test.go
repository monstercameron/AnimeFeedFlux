package e2e

import (
	"context"
	"testing"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/llm"
)

// TestItemRevisions drives PLAN.md §22's "edit an item, list its revisions,
// revert" flow (§12.4) end to end over the real bridge: ItemService.Update,
// ListRevisions, RevertRevision, and Get, against a real item promoted from
// a real sample. It proves the two things §12.4 promises and that a naive
// implementation could get wrong in opposite directions: RevertRevision is
// an ORDINARY EDIT (it must add a NEW item_revisions entry, not delete or
// rewind the one it reverts), and it is not a guid rewind (item_key must be
// byte-identical across the whole edit -> revert sequence).
func TestItemRevisions(t *testing.T) {
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

	const slug = "e2e-revisions-feed"
	createResp, err := login.Clients.Feed.Create(ctx, &affv1.FeedServiceCreateRequest{
		Feed: &affv1.Feed{
			Slug: slug, Kind: affv1.FeedKind_FEED_KIND_GENERATIVE,
			Title: "E2E Revisions Fixture", Description: "d", Language: "en",
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
		Title:       "The original trivia question title",
		SummaryText: "The original summary text for this trivia item.",
		BodyHTML:    `<p>Original body with an <a href="https://example.com/a">absolute link</a></p>`,
		AnswerHTML:  "<p>Original answer.</p>",
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
	if originalGuid == "" {
		t.Fatal("promoted item has an empty item_key")
	}

	// --- edit it: a real Update over the bridge, carrying every editable
	// field forward except title (Update overwrites the full field set from
	// the request, so an omitted field would be wiped, not merged) ---
	const editedTitle = "The EDITED trivia question title"
	updateResp, err := login.Clients.Item.Update(ctx, &affv1.ItemServiceUpdateRequest{
		Item: &affv1.Item{
			Id:          original.GetId(),
			Title:       editedTitle,
			SummaryText: original.GetSummaryText(),
			BodyHtml:    original.GetBodyHtml(),
			AnswerHtml:  original.GetAnswerHtml(),
			Link:        original.GetLink(),
			SourceName:  original.GetSourceName(),
			PublishedAt: original.GetPublishedAt(),
		},
		ExpectedVersion: original.GetVersion(),
	})
	if err != nil {
		t.Fatalf("ItemService.Update: %v", err)
	}
	edited := updateResp.GetItem()
	if edited.GetItemKey() != originalGuid {
		t.Fatalf("guid changed on Update: got %q, want %q", edited.GetItemKey(), originalGuid)
	}
	if edited.GetTitle() != editedTitle {
		t.Fatalf("Update did not apply: title = %q, want %q", edited.GetTitle(), editedTitle)
	}
	if edited.GetVersion() != original.GetVersion()+1 {
		t.Fatalf("version after Update = %d, want %d", edited.GetVersion(), original.GetVersion()+1)
	}

	// --- list revisions: exactly one entry, recording the title change ---
	listResp, err := login.Clients.Item.ListRevisions(ctx, &affv1.ItemServiceListRevisionsRequest{ItemId: original.GetId()})
	if err != nil {
		t.Fatalf("ItemService.ListRevisions (after edit): %v", err)
	}
	if len(listResp.GetRevisions()) != 1 {
		t.Fatalf("revisions after one edit = %d, want 1", len(listResp.GetRevisions()))
	}
	editRevision := listResp.GetRevisions()[0]
	var foundTitleChange bool
	for _, ch := range editRevision.GetChanges() {
		if ch.GetField() == "title" {
			foundTitleChange = true
			if ch.GetOldValue() != original.GetTitle() {
				t.Fatalf("revision title old_value = %q, want %q", ch.GetOldValue(), original.GetTitle())
			}
			if ch.GetNewValue() != editedTitle {
				t.Fatalf("revision title new_value = %q, want %q", ch.GetNewValue(), editedTitle)
			}
		}
	}
	if !foundTitleChange {
		t.Fatalf("revision %+v recorded no title change", editRevision)
	}

	// --- revert it: an ORDINARY EDIT through the same RevertRevision RPC ---
	revertResp, err := login.Clients.Item.RevertRevision(ctx, &affv1.ItemServiceRevertRevisionRequest{
		ItemId:          original.GetId(),
		RevisionId:      editRevision.GetId(),
		ExpectedVersion: edited.GetVersion(),
	})
	if err != nil {
		t.Fatalf("ItemService.RevertRevision: %v", err)
	}
	reverted := revertResp.GetItem()

	// The guid is BYTE-IDENTICAL across the whole edit -> revert sequence —
	// PLAN.md §5.1's guid is never allowed to move, and a revert that
	// wrongly restored an old item_key alongside the old title would pass a
	// looser "title matches" check while still breaking every subscriber.
	if reverted.GetItemKey() != originalGuid {
		t.Fatalf("guid changed on revert: got %q, want %q (byte-identical)", reverted.GetItemKey(), originalGuid)
	}
	if reverted.GetTitle() != original.GetTitle() {
		t.Fatalf("reverted title = %q, want the original %q", reverted.GetTitle(), original.GetTitle())
	}
	// A revert is an ordinary edit: it bumps the version again, one more
	// step forward, never backward.
	if reverted.GetVersion() != edited.GetVersion()+1 {
		t.Fatalf("version after revert = %d, want %d", reverted.GetVersion(), edited.GetVersion()+1)
	}

	// --- the revert wrote a NEW revision; the old one still exists ---
	// This is the assertion that matters most here: a naive "revert"
	// implementation deletes or rewrites the revision it reverts to, which
	// destroys the very history the diff view exists to show. The real
	// contract (internal/rpc/item.go's RevertRevision doc comment) is that a
	// revert commits through the identical edit path Update uses, so it
	// must leave TWO revisions behind: the original edit, and the revert
	// recorded as its own edit.
	listAfterRevert, err := login.Clients.Item.ListRevisions(ctx, &affv1.ItemServiceListRevisionsRequest{ItemId: original.GetId()})
	if err != nil {
		t.Fatalf("ItemService.ListRevisions (after revert): %v", err)
	}
	if len(listAfterRevert.GetRevisions()) != 2 {
		t.Fatalf("revisions after edit+revert = %d, want 2 (the edit row must not disappear)", len(listAfterRevert.GetRevisions()))
	}
	// Newest first (PLAN.md §12.4): the revert's own revision comes first.
	revertRevision := listAfterRevert.GetRevisions()[0]
	if revertRevision.GetId() == editRevision.GetId() {
		t.Fatal("the revert did not record a NEW revision row; it reused the edit's revision id")
	}
	var revertRecordedTitleChange bool
	for _, ch := range revertRevision.GetChanges() {
		if ch.GetField() == "title" {
			revertRecordedTitleChange = true
			if ch.GetOldValue() != editedTitle {
				t.Fatalf("revert revision title old_value = %q, want %q", ch.GetOldValue(), editedTitle)
			}
			if ch.GetNewValue() != original.GetTitle() {
				t.Fatalf("revert revision title new_value = %q, want %q", ch.GetNewValue(), original.GetTitle())
			}
		}
	}
	if !revertRecordedTitleChange {
		t.Fatalf("revert's own revision %+v recorded no title change", revertRevision)
	}
	// The edit's own revision is still the second entry — untouched, not
	// rewritten to look like a no-op.
	if listAfterRevert.GetRevisions()[1].GetId() != editRevision.GetId() {
		t.Fatal("the original edit's revision row is gone or reordered after the revert")
	}

	// --- confirm via a real Get: the live item reads back as reverted ---
	getResp, err := login.Clients.Item.Get(ctx, &affv1.ItemServiceGetRequest{Id: original.GetId()})
	if err != nil {
		t.Fatalf("ItemService.Get (after revert): %v", err)
	}
	if getResp.GetItem().GetTitle() != original.GetTitle() {
		t.Fatalf("Get after revert: title = %q, want the original %q", getResp.GetItem().GetTitle(), original.GetTitle())
	}
	if getResp.GetItem().GetItemKey() != originalGuid {
		t.Fatalf("Get after revert: guid = %q, want %q", getResp.GetItem().GetItemKey(), originalGuid)
	}
}
