package history

import "time"

// The load effects on both tabs depend on a filter, whether the socket is
// ready, and whether the feed list has settled. Passing those as three
// variadic deps to ui.UseEffect costs a deps slice per render, and wasm
// amplifies that allocation about fourteen-fold over native — the library's
// own perf log measures the untyped hook walk at 1.48 ms/pass against 0.57 ms
// for the typed *Of variants (TODOS.md A8-50).
//
// UseEffectOf takes ONE comparable dep, so the three collapse into a struct.
// A struct of comparables is comparable, which is what makes this legal.
//
// The filters hold *time.Time, and the key holds the INSTANT instead. That is
// a deliberate improvement rather than a translation: comparing pointers means
// a filter rebuilt with the same dates compares unequal and re-fires a load
// that fetches exactly what is already on screen.

// itemsLoadKey is the dependency set of the Items tab's load effect.
type itemsLoadKey struct {
	query           string
	feedID          int64
	origin          int32
	deletedFilter   int32
	publishedAfter  int64
	publishedBefore int64
	ready           bool
	feedsSettled    bool
}

func newItemsLoadKey(f ItemFilter, ready, feedsSettled bool) itemsLoadKey {
	return itemsLoadKey{
		query:           f.Query,
		feedID:          f.FeedID,
		origin:          int32(f.Origin),
		deletedFilter:   int32(f.DeletedFilter),
		publishedAfter:  instantKey(f.PublishedAfter),
		publishedBefore: instantKey(f.PublishedBefore),
		ready:           ready,
		feedsSettled:    feedsSettled,
	}
}

// runsLoadKey is the same for the Runs tab.
type runsLoadKey struct {
	feedID        int64
	status        int32
	startedAfter  int64
	startedBefore int64
	ready         bool
	feedsSettled  bool
}

func newRunsLoadKey(f RunFilter, ready, feedsSettled bool) runsLoadKey {
	return runsLoadKey{
		feedID:        f.FeedID,
		status:        int32(f.Status),
		startedAfter:  instantKey(f.StartedAfter),
		startedBefore: instantKey(f.StartedBefore),
		ready:         ready,
		feedsSettled:  feedsSettled,
	}
}

// instantKey reduces an optional time to a comparable scalar.
//
// Unset is 0 rather than a sentinel, and that is safe here because these are
// user-entered filter dates: nobody filters on the Unix epoch, and if they
// did, the consequence is one skipped refetch of a page already displayed.
func instantKey(t *time.Time) int64 {
	if t == nil {
		return 0
	}
	return t.UnixNano()
}
