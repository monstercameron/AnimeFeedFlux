package history

import (
	"testing"
	"time"
)

func TestRevisionStoreRecordAndList(t *testing.T) {
	s := NewRevisionStore()
	before := ItemSnapshot{Title: "Old title", SummaryText: "Old summary"}
	after := ItemSnapshot{Title: "New title", SummaryText: "Old summary"}
	at := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)

	s.Record(42, before, after, at)

	revs := s.List(42)
	if len(revs) != 1 {
		t.Fatalf("len(List(42)) = %d, want 1", len(revs))
	}
	if !revs[0].At.Equal(at) {
		t.Errorf("At = %v, want %v", revs[0].At, at)
	}
	if revs[0].Before.Title != "Old title" || revs[0].After.Title != "New title" {
		t.Errorf("unexpected revision snapshots: %+v", revs[0])
	}

	if got := s.List(99); got != nil {
		t.Errorf("List for unknown item = %+v, want nil", got)
	}
}

func TestRevisionDiffOmitsUnchangedFields(t *testing.T) {
	before := ItemSnapshot{Title: "A", SummaryText: "same", Link: "https://x/1"}
	after := ItemSnapshot{Title: "B", SummaryText: "same", Link: "https://x/1"}
	rev := Revision{Before: before, After: after}

	diffs := rev.Diff()
	if len(diffs) != 1 {
		t.Fatalf("len(diffs) = %d, want 1 (only title changed): %+v", len(diffs), diffs)
	}
	if diffs[0].Field != "title" {
		t.Errorf("diffs[0].Field = %q, want title", diffs[0].Field)
	}
}

func TestRevertTarget(t *testing.T) {
	s := NewRevisionStore()
	before1 := ItemSnapshot{Title: "v1"}
	after1 := ItemSnapshot{Title: "v2"}
	before2 := ItemSnapshot{Title: "v2"}
	after2 := ItemSnapshot{Title: "v3"}
	now := time.Now()

	s.Record(1, before1, after1, now)
	s.Record(1, before2, after2, now)

	snap, ok := RevertTarget(s, 1, 0)
	if !ok || snap.Title != "v1" {
		t.Fatalf("RevertTarget(1,0) = %+v ok=%v, want v1/true", snap, ok)
	}
	snap, ok = RevertTarget(s, 1, 1)
	if !ok || snap.Title != "v2" {
		t.Fatalf("RevertTarget(1,1) = %+v ok=%v, want v2/true", snap, ok)
	}

	if _, ok := RevertTarget(s, 1, 2); ok {
		t.Fatalf("RevertTarget out of range should return ok=false")
	}
	if _, ok := RevertTarget(s, 999, 0); ok {
		t.Fatalf("RevertTarget for unknown item should return ok=false")
	}
}
