package flowtest

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
)

// TestNewStandsUpAWorkingWorld exercises every piece New wires together —
// migrated store, real HTTP publish plane, fakes — so a future change that
// silently breaks one of them (a bad migration, a Deps field left zero) fails
// here instead of surfacing as a confusing error in an unrelated flow test.
func TestNewStandsUpAWorkingWorld(t *testing.T) {
	w := New(t)

	// The store is open and migrated: a real write must succeed.
	feed, err := w.CreateFeed(t.Context(), model.Feed{
		Slug: "harness-check", Title: "Harness Check", Kind: model.KindGenerative,
		Timezone: "UTC", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}
	if feed.ID == 0 {
		t.Fatal("CreateFeed did not assign an id")
	}

	n, err := w.ItemCount(t.Context(), feed.ID)
	if err != nil {
		t.Fatalf("ItemCount: %v", err)
	}
	if n != 0 {
		t.Fatalf("fresh feed has %d items, want 0", n)
	}

	// The publish plane is live over real HTTP and answers for a feed that
	// exists, even with zero items.
	resp := w.FetchFeed(t, "harness-check", "xml")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /feeds/harness-check.xml = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response body: %v", err)
	}
	if len(body) == 0 {
		t.Fatal("feed document is empty")
	}

	// A feed that was never created 404s rather than the handler panicking
	// or hanging — proves GetFeed's not-found mapping reaches the real
	// store, not just the fakeBackend publish/server_test.go already covers.
	resp2 := w.FetchFeed(t, "does-not-exist", "xml")
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /feeds/does-not-exist.xml = %d, want 404", resp2.StatusCode)
	}
}

// TestSampleNeverWritesAnItem is a smoke test for the harness's own Sample
// wrapper, independent of any specific flow's assertions: proves the wiring
// itself (genDeps, storeAdapter.RecentTitles, PutSample) works end to end
// before J3's own test file leans on it.
func TestSampleNeverWritesAnItem(t *testing.T) {
	w := New(t)
	ctx := t.Context()

	feed, err := w.CreateFeed(ctx, model.Feed{
		Slug: "harness-sample", Title: "Harness Sample", Kind: model.KindGenerative,
		Timezone: "UTC", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}

	w.Provider.QueueResult(validGenerateResult("A perfectly fine trivia question"))

	before, _ := w.ItemCount(ctx, feed.ID)
	outcome, err := w.Sample(ctx, feed, validSampleSpec())
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	after, _ := w.ItemCount(ctx, feed.ID)
	if before != after {
		t.Fatalf("item count changed from %d to %d across a sample", before, after)
	}
	if outcome.SampleID == 0 {
		t.Fatal("Sample did not report a sample id")
	}
	if _, err := w.SampleRow(ctx, outcome.SampleID); err != nil {
		t.Fatalf("reading back the sample row: %v", err)
	}
}

// TestWorldTeardownLeavesNoFiles proves Close() releases every SQLite
// connection (writer, reader, and their -wal/-shm sidecars) completely — on
// Windows, os.RemoveAll fails with a sharing violation if any handle into
// the directory is still open, which is exactly the failure mode a harness
// that leaks a connection would have.
func TestWorldTeardownLeavesNoFiles(t *testing.T) {
	dir, err := os.MkdirTemp("", "flowtest-harness-teardown-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}

	w, err := NewWithDir(t, dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		t.Fatalf("NewWithDir: %v", err)
	}

	// Do a little real work first so there is something to release: an open
	// write transaction's connection is a stricter test than an idle one.
	if _, err := w.CreateFeed(t.Context(), model.Feed{
		Slug: "teardown-check", Title: "Teardown Check", Kind: model.KindGenerative,
		Timezone: "UTC",
	}); err != nil {
		w.Close()
		_ = os.RemoveAll(dir)
		t.Fatalf("CreateFeed: %v", err)
	}

	dbPath := filepath.Join(dir, "aff.db")
	if _, err := os.Stat(dbPath); err != nil {
		w.Close()
		_ = os.RemoveAll(dir)
		t.Fatalf("expected %s to exist before teardown: %v", dbPath, err)
	}

	w.Close()

	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("removing %s after Close: %v (a locked handle means Close left a connection open)", dir, err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temp dir %s still exists after RemoveAll", dir)
	}
}
