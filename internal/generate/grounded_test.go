package generate

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/sources"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parsing test time %q: %v", s, err)
	}
	return ts
}

func TestBuildCandidateBlock_DeterministicOrdering(t *testing.T) {
	cands := []sources.Candidate{
		{Title: "Older, Z url", URL: "https://z.example.com/a", Excerpt: "e1", Published: mustTime(t, "2026-08-01T00:00:00Z")},
		{Title: "Newest", URL: "https://a.example.com/newest", Excerpt: "e2", Published: mustTime(t, "2026-08-09T00:00:00Z")},
		{Title: "Older, A url", URL: "https://a.example.com/a", Excerpt: "e3", Published: mustTime(t, "2026-08-01T00:00:00Z")},
	}

	out1 := BuildCandidateBlock(cands, 10)

	// Same input, different starting slice order -> same output. Also
	// asserts BuildCandidateBlock does not mutate the caller's slice order.
	reordered := []sources.Candidate{cands[2], cands[0], cands[1]}
	out2 := BuildCandidateBlock(reordered, 10)

	if out1 != out2 {
		t.Fatalf("expected deterministic output regardless of input order:\n--- out1 ---\n%s\n--- out2 ---\n%s", out1, out2)
	}

	// Newest first.
	idxNewest := strings.Index(out1, "Newest")
	idxOlderA := strings.Index(out1, "Older, A url")
	idxOlderZ := strings.Index(out1, "Older, Z url")
	if idxNewest == -1 || idxOlderA == -1 || idxOlderZ == -1 {
		t.Fatalf("expected all three titles present in block:\n%s", out1)
	}
	if !(idxNewest < idxOlderA && idxOlderA < idxOlderZ) {
		t.Fatalf("expected order Newest, Older-A(url tiebreak), Older-Z; got:\n%s", out1)
	}

	// Original slice given to BuildCandidateBlock is untouched.
	if cands[0].Title != "Older, Z url" {
		t.Fatalf("BuildCandidateBlock must not mutate the caller's slice, got first element %q", cands[0].Title)
	}
}

func TestBuildCandidateBlock_Cap(t *testing.T) {
	var cands []sources.Candidate
	for i := 0; i < 5; i++ {
		cands = append(cands, sources.Candidate{
			Title:     "Article",
			URL:       "https://example.com/" + string(rune('a'+i)),
			Published: mustTime(t, "2026-08-01T00:00:00Z").Add(time.Duration(i) * time.Hour),
		})
	}

	out := BuildCandidateBlock(cands, 2)
	count := strings.Count(out, "Title: Article")
	if count != 2 {
		t.Fatalf("expected block capped at 2 entries, got %d in:\n%s", count, out)
	}
}

func TestBuildCandidateBlock_Empty(t *testing.T) {
	out := BuildCandidateBlock(nil, 10)
	if !strings.Contains(strings.ToLower(out), "no candidate") {
		t.Fatalf("expected an explicit empty-set message, got %q", out)
	}
}

func TestRankingSystemPrompt_MentionsCoreRules(t *testing.T) {
	p := RankingSystemPrompt()
	lower := strings.ToLower(p)

	if !strings.Contains(lower, "url") || !strings.Contains(lower, "candidate list") {
		t.Errorf("expected the prompt to constrain links to the supplied candidate list, got:\n%s", p)
	}
	if !strings.Contains(lower, "own words") && !strings.Contains(lower, "summariz") {
		t.Errorf("expected the prompt to require summarizing rather than reproducing, got:\n%s", p)
	}
	if !strings.Contains(lower, "copy") && !strings.Contains(lower, "reproduce") {
		t.Errorf("expected the prompt to explicitly forbid reproducing source text, got:\n%s", p)
	}
}

func TestDegradeOnSourceFailure_ProceedsWithOneOfThree(t *testing.T) {
	live := []sources.Candidate{
		{Title: "A", URL: "https://good.example.com/1"},
		{Title: "B", URL: "https://good.example.com/2"},
	}
	results := []SourceResult{
		{URL: "https://dead1.example.com/feed.xml", Err: errors.New("connection refused")},
		{URL: "https://good.example.com/feed.xml", Candidates: live},
		{URL: "https://dead2.example.com/feed.xml", Err: errors.New("404 not found")},
	}

	usable, degraded, err := DegradeOnSourceFailure(results)
	if err != nil {
		t.Fatalf("expected no error with one live source, got %v", err)
	}
	if len(usable) != 2 {
		t.Fatalf("expected the live source's 2 candidates to be usable, got %d", len(usable))
	}
	if len(degraded) != 2 {
		t.Fatalf("expected 2 degraded sources named, got %d: %v", len(degraded), degraded)
	}
	for _, want := range []string{"https://dead1.example.com/feed.xml", "https://dead2.example.com/feed.xml"} {
		found := false
		for _, d := range degraded {
			if d == want {
				found = true
			}
		}
		if !found {
			t.Errorf("expected degraded list to name %q, got %v", want, degraded)
		}
	}
}

func TestDegradeOnSourceFailure_ErrorsOnlyWhenAllFail(t *testing.T) {
	results := []SourceResult{
		{URL: "https://dead1.example.com/feed.xml", Err: errors.New("timeout")},
		{URL: "https://dead2.example.com/feed.xml", Err: errors.New("timeout")},
		{URL: "https://dead3.example.com/feed.xml", Err: errors.New("timeout")},
	}

	usable, degraded, err := DegradeOnSourceFailure(results)
	if err == nil {
		t.Fatal("expected an error when every source failed")
	}
	if usable != nil {
		t.Fatalf("expected no usable candidates when every source failed, got %v", usable)
	}
	if len(degraded) != 3 {
		t.Fatalf("expected all 3 sources named as degraded, got %v", degraded)
	}
}

func TestDegradeOnSourceFailure_EmptySourceWithoutErrorIsNotFailure(t *testing.T) {
	// A source that responded but had nothing new (e.g. 304, or an empty
	// feed) is not a failure — only Err != nil counts as one.
	results := []SourceResult{
		{URL: "https://quiet.example.com/feed.xml", Candidates: nil},
		{URL: "https://dead.example.com/feed.xml", Err: errors.New("timeout")},
	}

	usable, degraded, err := DegradeOnSourceFailure(results)
	if err != nil {
		t.Fatalf("expected no error: one source succeeded (even with zero candidates), got %v", err)
	}
	if len(usable) != 0 {
		t.Fatalf("expected zero usable candidates, got %d", len(usable))
	}
	if len(degraded) != 1 || degraded[0] != "https://dead.example.com/feed.xml" {
		t.Fatalf("expected only the errored source reported as degraded, got %v", degraded)
	}
}

func TestExcerptOf_NoMidWordSplit(t *testing.T) {
	in := "one two three four five six seven eight nine ten"
	out := ExcerptOf(in, 12)

	out = strings.TrimSuffix(out, "…")
	words := strings.Fields(in)
	for _, w := range words {
		if strings.HasSuffix(out, w[:len(w)-1]) && !strings.HasSuffix(out, w) {
			t.Fatalf("excerpt appears to have split a word: %q from input %q", out, in)
		}
	}
	// Every word present in the excerpt must be a WHOLE word from the input.
	for _, w := range strings.Fields(out) {
		found := false
		for _, iw := range words {
			if w == iw {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("excerpt contains fragment %q not present as a whole word in input", w)
		}
	}
	if len([]rune(out)) > 12 {
		// Allowed to exceed only via the "one giant word" fallback path,
		// which does not apply to this input.
		t.Errorf("expected excerpt within the requested cap for space-separated input, got %q (%d runes)", out, len([]rune(out)))
	}
}

func TestExcerptOf_SingleLongWordDoesNotBreakMidWord(t *testing.T) {
	in := "supercalifragilisticexpialidocious"
	out := ExcerptOf(in, 10)
	if out != in {
		t.Fatalf("expected the whole unbreakable token preserved rather than split, got %q", out)
	}
}

func TestExcerptOf_StripsHTML(t *testing.T) {
	in := `<p>Hello <b>world</b>, this is <a href="https://example.com">a link</a>.</p>`
	out := ExcerptOf(in, 200)
	if strings.ContainsAny(out, "<>") {
		t.Fatalf("expected all HTML markup stripped, got %q", out)
	}
	if !strings.Contains(out, "Hello") || !strings.Contains(out, "world") || !strings.Contains(out, "a link") {
		t.Fatalf("expected the underlying text preserved, got %q", out)
	}
}

func TestExcerptOf_ShortStringUnchanged(t *testing.T) {
	in := "A short excerpt."
	out := ExcerptOf(in, 200)
	if out != in {
		t.Fatalf("expected a short string returned unchanged, got %q", out)
	}
}

func TestExcerptOf_ZeroMax(t *testing.T) {
	if got := ExcerptOf("anything", 0); got != "" {
		t.Fatalf("expected empty string for max<=0, got %q", got)
	}
}
