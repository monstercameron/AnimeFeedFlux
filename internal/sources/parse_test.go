package sources

import (
	"os"
	"strings"
	"testing"
	"time"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return b
}

func TestParse_RSS(t *testing.T) {
	body := readFixture(t, "rss_sample.xml")
	cands, err := Parse(body, "Anime News Wire")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(cands) != 2 {
		t.Fatalf("got %d candidates, want 2", len(cands))
	}

	first := cands[0]
	if first.Title != "Studio Announces New Season" {
		t.Errorf("Title = %q", first.Title)
	}
	if first.URL != "https://example.com/articles/new-season?utm_source=newsletter&utm_medium=email" {
		t.Errorf("URL = %q (Parse should not normalize; that's the fetch layer's job)", first.URL)
	}
	if first.SourceName != "Anime News Wire" {
		t.Errorf("SourceName = %q", first.SourceName)
	}
	wantDate := time.Date(2007, 10, 4, 23, 59, 45, 0, time.UTC)
	if !first.Published.Equal(wantDate) {
		t.Errorf("Published (RFC822) = %v, want %v", first.Published, wantDate)
	}

	second := cands[1]
	wantDate2 := time.Date(2007, 10, 5, 8, 30, 0, 0, time.UTC)
	if !second.Published.Equal(wantDate2) {
		t.Errorf("Published (RFC3339) = %v, want %v", second.Published, wantDate2)
	}
}

func TestParse_Atom(t *testing.T) {
	body := readFixture(t, "atom_sample.xml")
	cands, err := Parse(body, "Anime Atom Wire")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(cands) != 2 {
		t.Fatalf("got %d candidates, want 2", len(cands))
	}

	first := cands[0]
	if first.Title != "Voice Cast Revealed" {
		t.Errorf("Title = %q", first.Title)
	}
	if first.URL != "https://example.com/articles/voice-cast?utm_campaign=fall" {
		t.Errorf("URL = %q", first.URL)
	}
	if first.Excerpt != "The full cast list is out." {
		t.Errorf("Excerpt = %q", first.Excerpt)
	}
	wantDate := time.Date(2007, 10, 5, 12, 0, 0, 0, time.UTC)
	if !first.Published.Equal(wantDate) {
		t.Errorf("Published (RFC822, from <published>) = %v, want %v", first.Published, wantDate)
	}

	second := cands[1]
	if second.Excerpt != "A shot-by-shot look at the new trailer." {
		t.Errorf("Excerpt (fallback to content) = %q", second.Excerpt)
	}
	wantDate2 := time.Date(2007, 10, 6, 9, 0, 0, 0, time.UTC)
	if !second.Published.Equal(wantDate2) {
		t.Errorf("Published (RFC3339, from <updated> fallback) = %v, want %v", second.Published, wantDate2)
	}
}

func TestParse_BodyTooLarge(t *testing.T) {
	huge := make([]byte, MaxParseBytes+1)
	for i := range huge {
		huge[i] = ' '
	}
	_, err := Parse(huge, "x")
	if err == nil {
		t.Fatal("expected error for oversized body, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Errorf("error = %v, want ErrBodyTooLarge-shaped", err)
	}
}

// TestParse_BillionLaughs asserts the classic entity-expansion-bomb payload
// never expands and never hangs: encoding/xml with an explicit, empty
// Entity map refuses any custom entity reference outright (§4).
func TestParse_BillionLaughs(t *testing.T) {
	body := readFixture(t, "billion_laughs.xml")

	done := make(chan struct{})
	var cands []Candidate
	var err error
	go func() {
		cands, err = Parse(body, "Hostile Feed")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Parse hung on billion-laughs payload")
	}

	if err == nil {
		// If it didn't error, it must not have expanded anything either.
		for _, c := range cands {
			if len(c.Title) > 1000 {
				t.Fatalf("entity expansion occurred: title length = %d", len(c.Title))
			}
		}
	}
}

func TestParse_Unrecognized(t *testing.T) {
	_, err := Parse([]byte(`<?xml version="1.0"?><notafeed/>`), "x")
	if err == nil {
		t.Fatal("expected error for unrecognized feed type")
	}
}
