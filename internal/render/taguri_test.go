package render

import "testing"

func TestTagURI_ExactForm(t *testing.T) {
	got := TagURI("anime.earlcameron.com", 2026, "anime-trivia-daily", "01J8XYZABCDEFGHJKMNPQRSTVW")
	want := "tag:anime.earlcameron.com,2026:anime-trivia-daily/01J8XYZABCDEFGHJKMNPQRSTVW"
	if got != want {
		t.Fatalf("TagURI() = %q, want %q", got, want)
	}
}

func TestTagURI_SchemeStripped(t *testing.T) {
	withScheme := TagURI("https://anime.earlcameron.com", 2026, "anime-trivia-daily", "01J8XYZABCDEFGHJKMNPQRSTVW")
	bare := TagURI("anime.earlcameron.com", 2026, "anime-trivia-daily", "01J8XYZABCDEFGHJKMNPQRSTVW")
	if withScheme != bare {
		t.Fatalf("scheme changed output: %q vs %q", withScheme, bare)
	}
}

func TestTagURI_TrailingSlashStripped(t *testing.T) {
	withSlash := TagURI("https://anime.earlcameron.com/", 2026, "anime-trivia-daily", "01J8XYZABCDEFGHJKMNPQRSTVW")
	bare := TagURI("anime.earlcameron.com", 2026, "anime-trivia-daily", "01J8XYZABCDEFGHJKMNPQRSTVW")
	if withSlash != bare {
		t.Fatalf("trailing slash changed output: %q vs %q", withSlash, bare)
	}
}

func TestTagURI_Deterministic(t *testing.T) {
	first := TagURI("anime.earlcameron.com", 2026, "anime-trivia-daily", "01J8XYZABCDEFGHJKMNPQRSTVW")
	for i := 0; i < 50; i++ {
		got := TagURI("anime.earlcameron.com", 2026, "anime-trivia-daily", "01J8XYZABCDEFGHJKMNPQRSTVW")
		if got != first {
			t.Fatalf("TagURI() not deterministic: call %d = %q, first = %q", i, got, first)
		}
	}
}

func TestTagURI_ItemKeyChangesURI(t *testing.T) {
	a := TagURI("anime.earlcameron.com", 2026, "anime-trivia-daily", "01J8XYZABCDEFGHJKMNPQRSTVW")
	b := TagURI("anime.earlcameron.com", 2026, "anime-trivia-daily", "01J8XYZABCDEFGHJKMNPQRSTVX")
	if a == b {
		t.Fatalf("changing itemKey did not change TagURI() output: %q", a)
	}
}

func TestTagURI_CasePreserved(t *testing.T) {
	got := TagURI("anime.earlcameron.com", 2026, "anime-trivia-daily", "01j8xyzABCDEFGHJKMNPQRSTVW")
	want := "tag:anime.earlcameron.com,2026:anime-trivia-daily/01j8xyzABCDEFGHJKMNPQRSTVW"
	if got != want {
		t.Fatalf("TagURI() mangled case: got %q, want %q", got, want)
	}
}
