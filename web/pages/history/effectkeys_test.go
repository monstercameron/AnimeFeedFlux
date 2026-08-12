package history

import (
	"testing"
	"time"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// The keys must be comparable — UseEffectOf's type parameter requires it, and
// a non-comparable field would fail to compile rather than at run time, so
// this test is really about the SEMANTICS: equal filters must produce equal
// keys, or the load effect re-fires forever.
func TestItemsLoadKeyIsStableAcrossEqualFilters(t *testing.T) {
	when := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	other := when // a distinct pointer to the same instant

	a := newItemsLoadKey(ItemFilter{Query: "q", FeedID: 3, PublishedAfter: &when}, true, true)
	b := newItemsLoadKey(ItemFilter{Query: "q", FeedID: 3, PublishedAfter: &other}, true, true)

	// The pointers differ; the instants do not. Keying on the pointer would
	// re-fire a load that fetches exactly what is already on screen, every
	// time the filter struct was rebuilt.
	if a != b {
		t.Fatalf("two filters with the same instant produced different keys:\n%+v\n%+v", a, b)
	}
}

func TestItemsLoadKeyChangesWithEveryInput(t *testing.T) {
	early := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	late := early.Add(time.Hour)
	base := ItemFilter{Query: "q", FeedID: 3, Origin: affv1.Origin_ORIGIN_GENERATED,
		DeletedFilter: affv1.DeletedFilter_DELETED_FILTER_EXCLUDE_DELETED, PublishedAfter: &early, PublishedBefore: &late}
	baseKey := newItemsLoadKey(base, true, true)

	changed := base
	changed.Query = "other"
	if newItemsLoadKey(changed, true, true) == baseKey {
		t.Error("a different query produced the same key")
	}
	changed = base
	changed.FeedID = 4
	if newItemsLoadKey(changed, true, true) == baseKey {
		t.Error("a different feed produced the same key")
	}
	changed = base
	changed.Origin = affv1.Origin_ORIGIN_MANUAL
	if newItemsLoadKey(changed, true, true) == baseKey {
		t.Error("a different origin produced the same key")
	}
	changed = base
	changed.DeletedFilter = affv1.DeletedFilter_DELETED_FILTER_ONLY_DELETED
	if newItemsLoadKey(changed, true, true) == baseKey {
		t.Error("a different deleted filter produced the same key")
	}
	changed = base
	changed.PublishedAfter = &late
	if newItemsLoadKey(changed, true, true) == baseKey {
		t.Error("a different start date produced the same key")
	}
	changed = base
	changed.PublishedBefore = nil
	if newItemsLoadKey(changed, true, true) == baseKey {
		t.Error("clearing the end date produced the same key")
	}
	// Ready and feedsSettled gate the FIRST load; missing them here would
	// mean a page opened before the socket came up never loads at all.
	if newItemsLoadKey(base, false, true) == baseKey {
		t.Error("readiness is not part of the key")
	}
	if newItemsLoadKey(base, true, false) == baseKey {
		t.Error("feed settlement is not part of the key")
	}
}

func TestRunsLoadKeyChangesWithEveryInput(t *testing.T) {
	early := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	late := early.Add(time.Hour)
	base := RunFilter{FeedID: 3, Status: affv1.RunStatus_RUN_STATUS_FAILED, StartedAfter: &early, StartedBefore: &late}
	baseKey := newRunsLoadKey(base, true, true)

	changed := base
	changed.FeedID = 4
	if newRunsLoadKey(changed, true, true) == baseKey {
		t.Error("a different feed produced the same key")
	}
	changed = base
	changed.Status = affv1.RunStatus_RUN_STATUS_SUCCEEDED
	if newRunsLoadKey(changed, true, true) == baseKey {
		t.Error("a different status produced the same key")
	}
	changed = base
	changed.StartedAfter = &late
	if newRunsLoadKey(changed, true, true) == baseKey {
		t.Error("a different start date produced the same key")
	}
	changed = base
	changed.StartedBefore = nil
	if newRunsLoadKey(changed, true, true) == baseKey {
		t.Error("clearing the end date produced the same key")
	}
	if newRunsLoadKey(base, false, true) == baseKey {
		t.Error("readiness is not part of the key")
	}
	if newRunsLoadKey(base, true, false) == baseKey {
		t.Error("feed settlement is not part of the key")
	}
}

func TestInstantKeyTreatsNilAsUnset(t *testing.T) {
	if got := instantKey(nil); got != 0 {
		t.Fatalf("instantKey(nil) = %d, want 0", got)
	}
	when := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	if got := instantKey(&when); got != when.UnixNano() {
		t.Fatalf("instantKey = %d, want %d", got, when.UnixNano())
	}
}
