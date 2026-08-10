package publish

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
)

// testDepsForInvalidate returns Deps plus a pointer to a counter that
// increments on every ListItems call — the one dependency handleFeed cannot
// avoid calling on a genuine cache miss (GetFeed could in principle be
// cached upstream some day; ListItems is the render input). Tests use the
// counter, not the response code, to prove a request actually recomputed
// rather than hit the cache: rendering here is fully deterministic (fixed
// clock, fixed feed, no items), so a post-invalidation re-render produces
// byte-identical output and therefore the SAME ETag — a request carrying
// that ETag as If-None-Match gets 304 either way, which makes the status
// code useless as a cache-hit/miss signal in this specific test setup.
func testDepsForInvalidate() (Deps, *int) {
	feeds := map[string]model.Feed{
		"trivia": {ID: 1, Slug: "trivia", Title: "Trivia", Kind: model.KindGenerative, Enabled: true},
	}
	calls := 0
	deps := Deps{
		GetFeed: func(ctx context.Context, slug string) (model.Feed, bool, error) {
			f, ok := feeds[slug]
			return f, ok, nil
		},
		ListItems: func(ctx context.Context, feedID int64) ([]model.Item, error) {
			calls++
			return nil, nil
		},
		ListFeeds: func(ctx context.Context) ([]model.Feed, error) {
			out := make([]model.Feed, 0, len(feeds))
			for _, f := range feeds {
				out = append(out, f)
			}
			return out, nil
		},
		GetItem: func(ctx context.Context, itemKey string) (model.Feed, model.Item, bool, error) {
			return model.Feed{}, model.Item{}, false, nil
		},
		BaseURL:   "https://anime.example.com",
		Generator: "AnimeFeedFlux/test",
		DocsURL:   "https://www.rssboard.org/rss-specification",
		TagYear:   2026,
		Now:       func() time.Time { return time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC) },
	}
	return deps, &calls
}

// TestNewServerAndInvalidatorSharesTheHandlersCache is the load-bearing
// assertion for Part A: the Invalidator returned must invalidate the very
// cache the handler serves requests from, not a cache of its own that
// nothing ever reads.
func TestNewServerAndInvalidatorSharesTheHandlersCache(t *testing.T) {
	deps, calls := testDepsForInvalidate()
	handler, inv := NewServerAndInvalidator(deps)

	get := func() int {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/feeds/trivia.xml", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request: status = %d, body = %s", rec.Code, rec.Body.String())
		}
		return rec.Code
	}

	get()
	if *calls != 1 {
		t.Fatalf("first request: ListItems called %d times, want 1", *calls)
	}

	get() // should be a cache hit — no re-render, no ListItems call
	if *calls != 1 {
		t.Fatalf("second request before invalidation: ListItems called %d times, want still 1 (cache hit)", *calls)
	}

	inv.InvalidateFeed("trivia")

	get() // must be a genuine MISS: the entry is gone, so ListItems runs again
	if *calls != 2 {
		t.Fatalf("request after InvalidateFeed: ListItems called %d times, want 2 (cache should have been dropped)", *calls)
	}
}

// TestInvalidateFeedDropsEveryFormatAndAggregates asserts the two things
// PLAN.md §11 / cache.go's Invalidate promise: every format for a slug is
// dropped together, and (via the "index" key) a feed-level change is
// reflected on the aggregate/index view too.
func TestInvalidateFeedDropsEveryFormatAndAggregates(t *testing.T) {
	c := NewCache()
	entry := Entry{Body: []byte("x"), ETag: `"x"`, LastModified: "x", ContentType: "text/plain"}

	c.Put("trivia:xml", entry)
	c.Put("trivia:atom", entry)
	c.Put("trivia:json", entry)
	c.Put("trivia:item:abc", entry)
	c.Put("index", entry)
	// An unrelated feed's entries must survive.
	c.Put("news:xml", entry)

	inv := cacheInvalidator{c: c}
	inv.InvalidateFeed("trivia")

	for _, key := range []string{"trivia:xml", "trivia:atom", "trivia:json", "trivia:item:abc", "index"} {
		if _, ok := c.Get(key); ok {
			t.Errorf("key %q survived InvalidateFeed(\"trivia\")", key)
		}
	}
	if _, ok := c.Get("news:xml"); !ok {
		t.Error("InvalidateFeed(\"trivia\") dropped an unrelated feed's entry")
	}
}

// TestInvalidateAllDropsEverything covers the global-change path (PLAN.md
// §12.5's publishing defaults).
func TestInvalidateAllDropsEverything(t *testing.T) {
	c := NewCache()
	entry := Entry{Body: []byte("x"), ETag: `"x"`, LastModified: "x", ContentType: "text/plain"}
	c.Put("trivia:xml", entry)
	c.Put("news:xml", entry)
	c.Put("index", entry)

	inv := cacheInvalidator{c: c}
	inv.InvalidateAll()

	if s := c.Stats(); s.Entries != 0 {
		t.Errorf("InvalidateAll left %d entries", s.Entries)
	}
}

// TestInvalidatorConcurrentUse: many goroutines invalidating and reading
// concurrently must not race or deadlock — the store side (internal/store's
// Hooked) fires this from goroutines that are not synchronized with request
// handling in any other way.
func TestInvalidatorConcurrentUse(t *testing.T) {
	deps, _ := testDepsForInvalidate()
	handler, inv := NewServerAndInvalidator(deps)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Readers hammering the handler.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				req := httptest.NewRequest(http.MethodGet, "/feeds/trivia.xml", nil)
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
			}
		}()
	}

	// Writers invalidating concurrently.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				inv.InvalidateFeed("trivia")
			}
		}()
	}

	// InvalidateAll concurrently too — it takes the same lock as Invalidate
	// and Get, so this is the real concurrency test (go test -race is what
	// actually proves safety; this loop just gives -race something to see).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 100; j++ {
			inv.InvalidateAll()
		}
	}()

	go func() {
		time.Sleep(50 * time.Millisecond)
		close(stop)
	}()

	wg.Wait()
}
