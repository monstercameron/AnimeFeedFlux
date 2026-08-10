package history

import (
	"strings"
	"testing"
)

func opsOf(lines []DiffLine) []DiffOp {
	out := make([]DiffOp, len(lines))
	for i, l := range lines {
		out[i] = l.Op
	}
	return out
}

func TestDiffLinesIdentical(t *testing.T) {
	got := DiffLines("a\nb\nc", "a\nb\nc")
	for _, l := range got {
		if l.Op != DiffEqual {
			t.Fatalf("expected all-equal diff, got %+v", got)
		}
	}
}

func TestDiffLinesSimpleChange(t *testing.T) {
	got := DiffLines("a\nb\nc", "a\nX\nc")
	// Expect: equal(a), delete(b), insert(X), equal(c) — order may vary
	// slightly by LCS tie-breaking, so check membership instead of exact
	// sequence.
	var hasDelete, hasInsert, equalCount int
	for _, l := range got {
		switch l.Op {
		case DiffDelete:
			hasDelete++
			if l.Text != "b" {
				t.Errorf("unexpected delete text %q", l.Text)
			}
		case DiffInsert:
			hasInsert++
			if l.Text != "X" {
				t.Errorf("unexpected insert text %q", l.Text)
			}
		case DiffEqual:
			equalCount++
		}
	}
	if hasDelete != 1 || hasInsert != 1 || equalCount != 2 {
		t.Fatalf("got %+v, want 1 delete, 1 insert, 2 equal", got)
	}
}

func TestDiffLinesAppendOnly(t *testing.T) {
	got := DiffLines("a\nb", "a\nb\nc")
	last := got[len(got)-1]
	if last.Op != DiffInsert || last.Text != "c" {
		t.Fatalf("expected trailing insert of c, got %+v", got)
	}
}

func TestDiffLinesEmptyToNonEmpty(t *testing.T) {
	got := DiffLines("", "hello")
	if len(got) != 1 || got[0].Op != DiffInsert || got[0].Text != "hello" {
		t.Fatalf("got %+v, want single insert", got)
	}
}

func TestDiffLinesCapFallback(t *testing.T) {
	big := strings.Repeat("line\n", diffLineCap+10)
	got := DiffLines(big, big+"more")
	if len(got) != 2 {
		t.Fatalf("expected 2-line fallback diff for oversized input, got %d lines", len(got))
	}
	if got[0].Op != DiffDelete || got[1].Op != DiffInsert {
		t.Fatalf("expected delete-then-insert fallback, got %+v", opsOf(got))
	}
}

func TestFieldDiffChanged(t *testing.T) {
	unchanged := FieldDiff{Lines: DiffLines("same", "same")}
	if unchanged.Changed() {
		t.Fatalf("expected Changed() = false for identical text")
	}
	changed := FieldDiff{Lines: DiffLines("a", "b")}
	if !changed.Changed() {
		t.Fatalf("expected Changed() = true for differing text")
	}
}
