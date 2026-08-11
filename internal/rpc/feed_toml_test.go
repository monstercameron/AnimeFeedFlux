package rpc

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
)

// ExportTOML/ImportTOML are §7's versioning and disaster-recovery path: the
// thing an operator runs before a risky edit and reaches for after one. A
// round trip that silently drops a field is worse than no export at all,
// because the loss is only discovered when the backup is restored.

func TestRecipeRoundTripsThroughExportAndImport(t *testing.T) {
	st := feedOpenTestStore(t)
	s := NewFeedServer(st, nil, nil)

	original := mustCreateFeed(t, s, feedTestFeed("trivia-daily"))

	exported, err := s.ExportTOML(t.Context(), &affv1.FeedServiceExportTOMLRequest{FeedId: original.GetId()})
	if err != nil {
		t.Fatalf("ExportTOML: %v", err)
	}
	if exported.GetToml() == "" {
		t.Fatal("export produced an empty payload")
	}
	// The identity fields live outside the stored spec blob and are stitched
	// in on export; if that stitching breaks, a restored feed comes back
	// untitled and unslugged.
	for _, want := range []string{"trivia-daily", original.GetTitle()} {
		if !strings.Contains(exported.GetToml(), want) {
			t.Errorf("export is missing %q:\n%s", want, exported.GetToml())
		}
	}

	// Importing it as a NEW feed (no feed_id) recreates the recipe under a
	// different slug — which is also how "clone this feed" works.
	clonePayload := strings.ReplaceAll(exported.GetToml(), "trivia-daily", "trivia-clone")
	created, err := s.ImportTOML(t.Context(), &affv1.FeedServiceImportTOMLRequest{Toml: clonePayload})
	if err != nil {
		t.Fatalf("ImportTOML (create): %v", err)
	}
	if created.GetFeed().GetSlug() != "trivia-clone" {
		t.Errorf("imported slug = %q, want trivia-clone", created.GetFeed().GetSlug())
	}
	if created.GetFeed().GetId() == original.GetId() {
		t.Error("import with no feed_id overwrote the source feed instead of creating one")
	}

	// And re-exporting the clone produces a payload that imports again —
	// the property that makes this usable as a backup format at all.
	reExported, err := s.ExportTOML(t.Context(), &affv1.FeedServiceExportTOMLRequest{FeedId: created.GetFeed().GetId()})
	if err != nil {
		t.Fatalf("ExportTOML (clone): %v", err)
	}
	second := strings.ReplaceAll(reExported.GetToml(), "trivia-clone", "trivia-third")
	if _, err := s.ImportTOML(t.Context(), &affv1.FeedServiceImportTOMLRequest{Toml: second}); err != nil {
		t.Fatalf("a re-exported recipe did not import: %v", err)
	}
}

func TestImportOverAnExistingFeedRespectsTheVersion(t *testing.T) {
	// Import is a whole-recipe replace, so it is exactly the operation two
	// tabs can clobber each other with — §11's expected_version applies.
	st := feedOpenTestStore(t)
	s := NewFeedServer(st, nil, nil)

	feed := mustCreateFeed(t, s, feedTestFeed("trivia-daily"))
	exported, err := s.ExportTOML(t.Context(), &affv1.FeedServiceExportTOMLRequest{FeedId: feed.GetId()})
	if err != nil {
		t.Fatalf("ExportTOML: %v", err)
	}

	_, err = s.ImportTOML(t.Context(), &affv1.FeedServiceImportTOMLRequest{
		FeedId: feed.GetId(), Toml: exported.GetToml(),
		ExpectedVersion: feed.GetVersion() + 1, // stale
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("a stale expected_version returned %v, want FailedPrecondition", err)
	}

	if _, err := s.ImportTOML(t.Context(), &affv1.FeedServiceImportTOMLRequest{
		FeedId: feed.GetId(), Toml: exported.GetToml(),
		ExpectedVersion: feed.GetVersion(),
	}); err != nil {
		t.Fatalf("ImportTOML with the current version: %v", err)
	}
}

func TestImportCanRenameOnlyBeforeTheFirstPublish(t *testing.T) {
	// The slug is embedded in every item's Tag URI guid (§14.1), so renaming
	// after publication would make every subscriber re-download the whole
	// feed as new items. Before anything is published there is no guid to
	// break, and a rename is a legitimate correction.
	st := feedOpenTestStore(t)
	s := NewFeedServer(st, nil, nil)

	feed := mustCreateFeed(t, s, feedTestFeed("trivia-daily"))
	exported, err := s.ExportTOML(t.Context(), &affv1.FeedServiceExportTOMLRequest{FeedId: feed.GetId()})
	if err != nil {
		t.Fatalf("ExportTOML: %v", err)
	}
	renamed := strings.ReplaceAll(exported.GetToml(), "trivia-daily", "renamed-feed")

	resp, err := s.ImportTOML(t.Context(), &affv1.FeedServiceImportTOMLRequest{
		FeedId: feed.GetId(), Toml: renamed, ExpectedVersion: feed.GetVersion(),
	})
	if err != nil {
		t.Fatalf("renaming an unpublished feed: %v", err)
	}
	if resp.GetFeed().GetSlug() != "renamed-feed" {
		t.Fatalf("slug = %q, want renamed-feed", resp.GetFeed().GetSlug())
	}

	// Publish one item, and the same edit is now refused.
	if _, err := st.InsertItem(t.Context(), model.Item{
		FeedID: feed.GetId(), ItemKey: "item-1", ContentHash: "h1",
		Title: "Published", SummaryText: "s", BodyHTML: "<p>b</p>",
		PublishedAt: time.Now().UTC(), Origin: model.OriginGenerated,
	}); err != nil {
		t.Fatalf("InsertItem: %v", err)
	}
	current, err := s.Get(t.Context(), &affv1.FeedServiceGetRequest{Id: feed.GetId()})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	reExported, err := s.ExportTOML(t.Context(), &affv1.FeedServiceExportTOMLRequest{FeedId: feed.GetId()})
	if err != nil {
		t.Fatalf("ExportTOML: %v", err)
	}
	published := strings.ReplaceAll(reExported.GetToml(), "renamed-feed", "renamed-once-more")

	_, err = s.ImportTOML(t.Context(), &affv1.FeedServiceImportTOMLRequest{
		FeedId: feed.GetId(), Toml: published, ExpectedVersion: current.GetFeed().GetVersion(),
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("renaming a published feed returned %v, want FailedPrecondition", err)
	}
}

func TestImportRejectsAKindChange(t *testing.T) {
	st := feedOpenTestStore(t)
	s := NewFeedServer(st, nil, nil)

	mustCreateFeed(t, s, feedTestFeed("member-feed"))
	feed := mustCreateFeed(t, s, feedTestFeed("trivia-daily"))
	agg := mustCreateFeed(t, s, feedTestAggregate("everything", "Everything", []string{"member-feed"}))

	aggExport, err := s.ExportTOML(t.Context(), &affv1.FeedServiceExportTOMLRequest{FeedId: agg.GetId()})
	if err != nil {
		t.Fatalf("ExportTOML: %v", err)
	}
	// Same payload, aimed at the generative feed: kind is immutable after
	// creation, because the two kinds mean different things about who owns
	// the items.
	payload := strings.ReplaceAll(aggExport.GetToml(), "everything", "trivia-daily")
	_, err = s.ImportTOML(t.Context(), &affv1.FeedServiceImportTOMLRequest{
		FeedId: feed.GetId(), Toml: payload, ExpectedVersion: feed.GetVersion(),
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("a kind change returned %v, want InvalidArgument", err)
	}
}

func TestExportAndImportRejectBadInput(t *testing.T) {
	st := feedOpenTestStore(t)
	s := NewFeedServer(st, nil, nil)

	if _, err := s.ExportTOML(t.Context(), &affv1.FeedServiceExportTOMLRequest{FeedId: 99999}); status.Code(err) != codes.NotFound {
		t.Errorf("exporting an unknown feed returned %v, want NotFound", err)
	}

	if _, err := s.ImportTOML(t.Context(), &affv1.FeedServiceImportTOMLRequest{Toml: "this is not a recipe"}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("importing garbage returned %v, want InvalidArgument", err)
	}

	// A syntactically valid payload that fails validation must be refused
	// too — import is not a way around the checks Create applies.
	if _, err := s.ImportTOML(t.Context(), &affv1.FeedServiceImportTOMLRequest{Toml: `{"slug":"","title":""}`}); err == nil {
		t.Error("importing an invalid recipe as a new feed succeeded")
	}
}
