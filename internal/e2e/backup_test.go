package e2e

import (
	"context"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/llm"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/ops"
	"github.com/monstercameron/AnimeFeedFlux/internal/publish"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// TestBackupRestoreRoundTrip drives PLAN.md §19 item 8 ("a backup has been
// restored into a scratch instance and serves identical feeds") and §15's
// ops.Backup/Restore end to end: the backup itself is taken through the
// real public surface (SystemService.Backup, over the real bridge) against
// a live store whose writer is still open — never a copy of the .db file
// while a plain os.Copy would be a torn WAL-mode snapshot — then verified
// and restored via internal/ops (the same package cmd/aff/backup_cmd.go's
// CLI restore path calls; there is no RPC for restore, matching InitAdmin's
// own precedent of reproducing a filesystem-only cmd/aff operation directly
// against the owning internal package). The restored copy is then served
// from a SECOND, independent publish plane and fetched over real HTTP, and
// its bytes are compared against the original — not the row count, the
// actual thing a subscriber would receive.
func TestBackupRestoreRoundTrip(t *testing.T) {
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

	const slug = "e2e-backup-feed"
	createResp, err := login.Clients.Feed.Create(ctx, &affv1.FeedServiceCreateRequest{
		Feed: &affv1.Feed{
			Slug: slug, Kind: affv1.FeedKind_FEED_KIND_GENERATIVE,
			Title: "E2E Backup Fixture", Description: "d", Language: "en",
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
		Title:       "The item that must survive a backup and restore",
		SummaryText: "Summary that must round-trip byte-for-byte.",
		BodyHTML:    `<p>Body with an <a href="https://example.com/a">absolute link</a></p>`,
		AnswerHTML:  "<p>Answer.</p>",
	}}})
	sampleResp, err := login.Clients.Sample.Sample(ctx, &affv1.SampleServiceSampleRequest{FeedId: feedID, SampleSize: 1})
	if err != nil {
		t.Fatalf("SampleService.Sample: %v", err)
	}
	candidate := sampleResp.GetCandidates()[0]
	if _, err := login.Clients.Item.PromoteSample(ctx, &affv1.ItemServicePromoteSampleRequest{
		SampleId: sampleResp.GetSampleId(), CandidateId: candidate.GetCandidateId(),
	}); err != nil {
		t.Fatalf("ItemService.PromoteSample: %v", err)
	}

	// The bytes a subscriber gets BEFORE backup/restore — the baseline the
	// restored copy must match exactly.
	originalResp := app.FetchFeed(t, slug, "xml")
	originalBody, err := io.ReadAll(originalResp.Body)
	_ = originalResp.Body.Close()
	if err != nil {
		t.Fatalf("reading original feed body: %v", err)
	}
	if len(originalBody) == 0 {
		t.Fatal("original feed body is empty")
	}

	// --- backup: the real public surface, against a store whose writer
	// (app.Store) is still open — this suite never closes it mid-test, so
	// this genuinely is "a live store with an open writer", not a store
	// that happened to be idle. ---
	// Backup re-proves password + TOTP; a live session is deliberately not
	// enough, because the response is the whole database — every password
	// hash, the encrypted TOTP secret, every session and recovery-code hash
	// (A8-40). The code must come from a step Login above did not already
	// consume: replayed steps are refused (§4), and Login spent the one at
	// `now`, so this uses now+1 within the ±1 skew window.
	backupCode, err := TOTPCode(totpSecret, time.Now().Add(30*time.Second))
	if err != nil {
		t.Fatalf("computing Backup's totp code: %v", err)
	}
	backupResp, err := login.Clients.System.Backup(ctx, &affv1.SystemServiceBackupRequest{
		CurrentPassword: adminPassword,
		TotpCode:        backupCode,
	})
	if err != nil {
		t.Fatalf("SystemService.Backup: %v", err)
	}
	if len(backupResp.GetDbFile()) == 0 {
		t.Fatal("SystemService.Backup returned an empty db_file")
	}

	backupDir := t.TempDir()
	backupPath := filepath.Join(backupDir, backupResp.GetFilename())
	if err := os.WriteFile(backupPath, backupResp.GetDbFile(), 0o600); err != nil {
		t.Fatalf("writing backup file: %v", err)
	}

	// --- verify: SQLite's own integrity check, the guard against a
	// torn/corrupt snapshot passing a casual smoke test ---
	if err := ops.Verify(ctx, backupPath); err != nil {
		t.Fatalf("ops.Verify(backup): %v", err)
	}

	// --- restore: into a scratch path, exactly cmd/aff's restore CLI path
	// (internal/ops.Restore) ---
	restoredPath := filepath.Join(t.TempDir(), "restored.db")
	if err := ops.Restore(ctx, backupPath, restoredPath); err != nil {
		t.Fatalf("ops.Restore: %v", err)
	}

	// --- serve the restored copy from a SECOND, independent publish plane
	// and fetch it over real HTTP — the actual proof, not a file-exists
	// check ---
	restoredStore, err := store.Open(ctx, store.Options{Path: restoredPath})
	if err != nil {
		t.Fatalf("opening restored store: %v", err)
	}
	defer func() { _ = restoredStore.Close() }()
	reader := restoredStore.Reader()

	// Reuses app.go's own package-level read helpers (getFeedBySlug,
	// listItems, listFeeds, getItemByKey) against the RESTORED store's
	// reader — the same functions buildPublishPlane wires the original
	// App's publish handler through, so both servers render from an
	// identical read path and only the underlying database file differs.
	restoredDeps := publish.Deps{
		GetFeed: func(ctx context.Context, slug string) (model.Feed, bool, error) {
			return getFeedBySlug(ctx, reader, slug)
		},
		ListItems: func(ctx context.Context, feedID int64) ([]model.Item, error) { return listItems(ctx, reader, feedID) },
		ListFeeds: func(ctx context.Context) ([]model.Feed, error) { return listFeeds(ctx, reader) },
		GetItem: func(ctx context.Context, itemKey string) (model.Feed, model.Item, bool, error) {
			return getItemByKey(ctx, reader, itemKey)
		},
		// Deliberately the SAME BaseURL as the original App's publish plane:
		// BaseURL is a plain string baked into rendered <link>/self URLs, not
		// derived from the actual listener address, so forcing it equal is
		// what makes a literal byte-for-byte body comparison meaningful
		// despite the restored server listening on a different port.
		BaseURL:   app.PublishURL,
		Generator: "AnimeFeedFlux e2e",
		DocsURL:   "https://www.rssboard.org/rss-specification",
		TagYear:   2026,
		// The SAME injected clock, so lastBuildDate and every stamped
		// timestamp in the restored render match the original's exactly.
		Now: app.Clock.Now,
	}

	handler, _ := publish.NewServerAndInvalidator(restoredDeps)
	restoredSrv := httptest.NewServer(handler)
	defer restoredSrv.Close()

	restoredResp, err := restoredSrv.Client().Get(restoredSrv.URL + "/feeds/" + slug + ".xml")
	if err != nil {
		t.Fatalf("fetching restored feed: %v", err)
	}
	restoredBody, err := io.ReadAll(restoredResp.Body)
	_ = restoredResp.Body.Close()
	if err != nil {
		t.Fatalf("reading restored feed body: %v", err)
	}

	if string(restoredBody) != string(originalBody) {
		t.Fatalf("restored feed bytes differ from the original:\n--- original ---\n%s\n--- restored ---\n%s",
			originalBody, restoredBody)
	}
}
