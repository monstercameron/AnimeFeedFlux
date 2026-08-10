package publish

// PLAN.md §17.4 (AF-15..17): the poll-load check. This is not a benchmark
// for speed — readers polling on a schedule is the only traffic this plane
// will ever really see, and the thing that can quietly go wrong under that
// traffic is a cache that still does real work (SQLite, renderers) on what
// should be a 304. The assertions below are ordered to catch exactly that.

import (
	"bytes"
	"net/http"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newLoadTestServer builds the same fixture as server_test.go's
// newTestServer, but without a *testing.T so it can be shared by the
// benchmarks below (b.Helper() lives on *testing.B, not a common TB the
// existing helper accepts). Kept minimal — no soft-deleted item, since the
// load test only ever hits the one live feed document.
func newLoadTestServer() (http.Handler, *fakeBackend) {
	b := newFakeBackend()
	feed, items := testFeedAndItems()
	b.feeds[feed.Slug] = feed
	for _, it := range items {
		b.items[it.ItemKey] = itemRow{feed: feed, item: it}
	}
	now := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	return NewServer(b.deps(now)), b
}

// TestPollLoad drives many concurrent conditional GETs against one feed and
// checks that the cache behaves the way §5.4 promises under real concurrency,
// not just in the single-goroutine tests in server_test.go.
func TestPollLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("poll-load check is expensive; skipped under -short")
	}

	const (
		clients           = 200
		requestsPerClient = 50
	)
	const path = "/feeds/trivia-daily.xml"

	h, b := newTestServer(t)

	// Warm the cache synchronously first. Real traffic works the same way —
	// one render primes the entry, then every poller from every client only
	// ever sees it warm — and it lets every request below, including each
	// goroutine's "first" one, be a cache hit, which is what makes the
	// backend-call assertion below meaningful rather than racy.
	warm := do(h, http.MethodGet, path, nil)
	if warm.Code != http.StatusOK {
		t.Fatalf("warm-up request status = %d, want 200; body=%s", warm.Code, warm.Body.String())
	}
	wantBody := append([]byte(nil), warm.Body.Bytes()...)
	wantETag := warm.Header().Get("ETag")
	if wantETag == "" {
		t.Fatal("warm-up response missing ETag")
	}
	baseGetFeed, baseListItems, _, _ := b.calls()

	var mem0 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&mem0)

	var total, notModified, mismatches int64

	var wg sync.WaitGroup
	wg.Add(clients)
	for i := 0; i < clients; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < requestsPerClient; j++ {
				var headers map[string]string
				if j > 0 {
					// Every request after the client's own first carries the
					// validator from the warm-up render, so it must 304
					// regardless of which goroutine populated the cache.
					headers = map[string]string{"If-None-Match": wantETag}
				}
				rec := do(h, http.MethodGet, path, headers)
				atomic.AddInt64(&total, 1)
				switch rec.Code {
				case http.StatusNotModified:
					atomic.AddInt64(&notModified, 1)
					if rec.Body.Len() != 0 || rec.Header().Get("ETag") != wantETag {
						atomic.AddInt64(&mismatches, 1)
					}
				case http.StatusOK:
					if rec.Header().Get("ETag") != wantETag || !bytes.Equal(rec.Body.Bytes(), wantBody) {
						atomic.AddInt64(&mismatches, 1)
					}
				default:
					atomic.AddInt64(&mismatches, 1)
				}
			}
		}()
	}
	wg.Wait()

	// Correctness under concurrency: nothing served a wrong status, a
	// mismatched ETag, or a body that drifted from the single-threaded
	// render that produced wantBody.
	if mismatches != 0 {
		t.Fatalf("%d/%d responses under load had a wrong status/body/ETag", mismatches, total)
	}

	ratio := float64(notModified) / float64(total)
	if ratio < 0.9 {
		t.Fatalf("304 ratio = %.4f (total=%d notModified=%d), want >= 0.9 — 304s should dominate this workload",
			ratio, total, notModified)
	}
	t.Logf("poll-load: total=%d notModified=%d ratio=%.4f", total, notModified, ratio)

	// The assertion that matters most: a cache that quietly re-rendered on
	// every "hit" would still have produced correct bytes above, so only
	// the backend call counts can catch it. §5.4 — a hit must not reach
	// SQLite, let alone the LLM; GetFeed/ListItems are this test's stand-in
	// for "touched the backend" (publish has no direct store dependency to
	// instrument, by design — see the Deps doc comment in server.go).
	getFeedAfter, listItemsAfter, _, _ := b.calls()
	if getFeedAfter != baseGetFeed || listItemsAfter != baseListItems {
		t.Fatalf("cache hits reached the backend: GetFeed %d->%d, ListItems %d->%d",
			baseGetFeed, getFeedAfter, baseListItems, listItemsAfter)
	}

	runtime.GC()
	var mem1 runtime.MemStats
	runtime.ReadMemStats(&mem1)
	// Smoke test for a leak, not a precise measurement — GC pacing and test
	// process noise mean heap size is inherently fuzzy across a run. The
	// threshold is deliberately generous (a real per-request leak across
	// 10,000 cache-hit requests, none of which allocate a new Entry, would
	// blow way past this; ordinary GC slack will not) so this only fires on
	// an actual leak, never on noise. A flaky memory assertion is worse than
	// none.
	const heapGrowthCeiling = 50 << 20 // 50 MiB
	if mem1.HeapAlloc > mem0.HeapAlloc && mem1.HeapAlloc-mem0.HeapAlloc > heapGrowthCeiling {
		t.Fatalf("heap grew by %d bytes over %d requests, want < %d bytes (possible leak)",
			mem1.HeapAlloc-mem0.HeapAlloc, total, heapGrowthCeiling)
	}
}

// TestCacheConcurrentAccess hammers Cache directly with concurrent
// Get/Put/Invalidate. -race only runs in CI (this machine is windows/arm64,
// which the Go race detector does not support), so this test's value is
// mostly realized there rather than here — but it costs nothing to run
// everywhere, and it's the only thing in this package that exercises
// Put/Invalidate concurrently with Get at all.
func TestCacheConcurrentAccess(t *testing.T) {
	if testing.Short() {
		t.Skip("concurrency stress; skipped under -short")
	}

	c := NewCache()
	entry, err := NewEntry([]byte("<rss></rss>"), "application/rss+xml; charset=utf-8", httpTime(time.Now()))
	if err != nil {
		t.Fatalf("NewEntry: %v", err)
	}

	const writers = 10
	const writesPerWriter = 500
	const readers = 50

	var writersWG sync.WaitGroup
	writersWG.Add(writers)
	for i := 0; i < writers; i++ {
		go func() {
			defer writersWG.Done()
			for j := 0; j < writesPerWriter; j++ {
				c.Put("trivia-daily:xml", entry)
				if j%10 == 0 {
					c.Invalidate("trivia-daily")
				}
			}
		}()
	}

	done := make(chan struct{})
	var readersWG sync.WaitGroup
	readersWG.Add(readers)
	for i := 0; i < readers; i++ {
		go func() {
			defer readersWG.Done()
			for {
				select {
				case <-done:
					return
				default:
					c.Get("trivia-daily:xml")
					c.Stats()
				}
			}
		}()
	}

	writersWG.Wait()
	close(done)
	readersWG.Wait()
}

// BenchmarkFeedServe304 and BenchmarkFeedServe200 are a baseline for later
// comparison, not a gate — nothing in this package asserts against them.
func BenchmarkFeedServe304(b *testing.B) {
	h, _ := newLoadTestServer()
	const path = "/feeds/trivia-daily.xml"
	warm := do(h, http.MethodGet, path, nil)
	etag := warm.Header().Get("ETag")
	headers := map[string]string{"If-None-Match": etag}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		do(h, http.MethodGet, path, headers)
	}
}

func BenchmarkFeedServe200(b *testing.B) {
	h, _ := newLoadTestServer()
	const path = "/feeds/trivia-daily.xml"
	do(h, http.MethodGet, path, nil) // warm the cache; measure the hit path, not the render

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		do(h, http.MethodGet, path, nil)
	}
}
