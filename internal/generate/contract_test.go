package generate

import (
	"strings"
	"testing"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
)

// validCandidate returns a Candidate that passes every rule, so each test
// case can mutate exactly the field under test rather than repeating a full
// valid payload.
func validCandidate() Candidate {
	return Candidate{
		Title:       "A perfectly reasonable trivia title",
		SummaryText: "A short plain-text summary of the item",
		BodyHTML:    `<p>Full body with an <a href="https://example.com/x">absolute link</a>.</p>`,
		AnswerHTML:  "",
		Link:        "",
		SourceName:  "",
		Tags:        []string{"anime", "trivia"},
	}
}

func rejectionReasons(rejections []Rejection) []string {
	out := make([]string, len(rejections))
	for i, r := range rejections {
		out[i] = r.Reason
	}
	return out
}

func containsReason(rejections []Rejection, reason string) bool {
	for _, r := range rejections {
		if r.Reason == reason {
			return true
		}
	}
	return false
}

func TestValidate_Rules(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(c *Candidate)
		kind       model.FeedKind
		candidates []string
		wantReason string
	}{
		{
			name:       "title empty",
			mutate:     func(c *Candidate) { c.Title = "" },
			wantReason: ReasonTitleRequired,
		},
		{
			name:       "title too short",
			mutate:     func(c *Candidate) { c.Title = "short" },
			wantReason: ReasonTitleTooShort,
		},
		{
			name:       "title too long",
			mutate:     func(c *Candidate) { c.Title = strings.Repeat("a", 201) },
			wantReason: ReasonTitleTooLong,
		},
		{
			name:       "title trailing punctuation",
			mutate:     func(c *Candidate) { c.Title = "A perfectly reasonable trivia title." },
			wantReason: ReasonTitleTrailingPunct,
		},
		{
			name:       "summary empty",
			mutate:     func(c *Candidate) { c.SummaryText = "" },
			wantReason: ReasonSummaryRequired,
		},
		{
			name:       "summary over hard cap",
			mutate:     func(c *Candidate) { c.SummaryText = strings.Repeat("a", 501) },
			wantReason: ReasonSummaryHardCap,
		},
		{
			name:       "summary contains html",
			mutate:     func(c *Candidate) { c.SummaryText = "A summary with <b>markup</b> in it" },
			wantReason: ReasonSummaryContainsHTML,
		},
		{
			name:       "body html empty",
			mutate:     func(c *Candidate) { c.BodyHTML = "" },
			wantReason: ReasonBodyRequired,
		},
		{
			name: "body html relative link",
			mutate: func(c *Candidate) {
				c.BodyHTML = `<p>See <a href="/relative/path">this</a>.</p>`
			},
			wantReason: ReasonBodyRelativeLink,
		},
		{
			name:       "tags too many",
			mutate:     func(c *Candidate) { c.Tags = []string{"a", "b", "c", "d", "e", "f", "g"} },
			wantReason: ReasonTagsTooMany,
		},
		{
			name:       "tags not lowercase",
			mutate:     func(c *Candidate) { c.Tags = []string{"Anime"} },
			wantReason: ReasonTagsNotLowercase,
		},
		{
			name:       "link required for grounded",
			mutate:     func(c *Candidate) { c.Link = "" },
			kind:       model.KindGrounded,
			candidates: []string{"https://example.com/article"},
			wantReason: ReasonLinkRequiredGrounded,
		},
		{
			name:       "link not in candidate set",
			mutate:     func(c *Candidate) { c.Link = "https://example.com/somewhere-else" },
			kind:       model.KindGrounded,
			candidates: []string{"https://example.com/article"},
			wantReason: ReasonLinkNotCandidate,
		},
		{
			name:       "generative link must still be absolute if present",
			mutate:     func(c *Candidate) { c.Link = "/not-absolute" },
			kind:       model.KindGenerative,
			wantReason: ReasonLinkInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validCandidate()
			tt.mutate(&c)
			kind := tt.kind
			if kind == "" {
				kind = model.KindGenerative
			}
			_, rejections, err := Validate(c, Options{Kind: kind, CandidateURLs: tt.candidates})
			if err != nil {
				t.Fatalf("Validate returned unexpected error: %v", err)
			}
			if !containsReason(rejections, tt.wantReason) {
				t.Fatalf("want reason %q, got %v", tt.wantReason, rejectionReasons(rejections))
			}
		})
	}
}

func TestValidate_AcceptsValidCandidate(t *testing.T) {
	c := validCandidate()
	item, rejections, err := Validate(c, Options{Kind: model.KindGenerative})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rejections) != 0 {
		t.Fatalf("expected no rejections, got %v", rejectionReasons(rejections))
	}
	if item.Title != c.Title {
		t.Errorf("Title = %q, want %q", item.Title, c.Title)
	}
	// BodyHTML must come back through sanitize.HTML, not the raw input
	// verbatim — a raw compare would only prove we echoed the model,
	// not that we validated it.
	if !strings.Contains(item.BodyHTML, `rel="nofollow noopener"`) {
		t.Errorf("BodyHTML does not look sanitized: %q", item.BodyHTML)
	}
}

func TestValidate_GroundedAcceptsCandidateLink(t *testing.T) {
	c := validCandidate()
	c.Link = "https://example.com/article"
	_, rejections, err := Validate(c, Options{
		Kind:          model.KindGrounded,
		CandidateURLs: []string{"https://example.com/article"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rejections) != 0 {
		t.Fatalf("expected no rejections, got %v", rejectionReasons(rejections))
	}
}

// TestValidate_AnswerLeak uses a distinctive token so the assertion cannot
// pass by coincidental substring overlap with anything else in the fixture.
func TestValidate_AnswerLeak(t *testing.T) {
	c := validCandidate()
	c.SummaryText = "What studio animated the distinctive-answer-token show?"
	c.AnswerHTML = "<p>distinctive-answer-token</p>"

	_, rejections, err := Validate(c, Options{Kind: model.KindGenerative})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsReason(rejections, ReasonAnswerLeaked) {
		t.Fatalf("want %q, got %v", ReasonAnswerLeaked, rejectionReasons(rejections))
	}
}

func TestValidate_AnswerNotLeaked(t *testing.T) {
	c := validCandidate()
	c.SummaryText = "What studio animated this show?"
	c.AnswerHTML = "<p>distinctive-answer-token</p>"

	_, rejections, err := Validate(c, Options{Kind: model.KindGenerative})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if containsReason(rejections, ReasonAnswerLeaked) {
		t.Fatalf("did not expect answer-leak rejection, got %v", rejectionReasons(rejections))
	}
}

// TestCheckLink_AcceptsTrackingParamEcho is, per the task brief, "the §9.6
// trap and the single most important test in the file": a news RSS
// candidate routinely carries utm_*/fbclid, and if the model echoes that
// URL verbatim, asymmetric normalization would silently reject a perfectly
// faithful link and look exactly like a hallucination.
func TestCheckLink_AcceptsTrackingParamEcho(t *testing.T) {
	candidate := "https://news.example.com/story?utm_source=rss&utm_medium=feed&id=42"
	modelEcho := "https://news.example.com/story?utm_source=rss&utm_medium=feed&id=42"

	if err := CheckLink(modelEcho, []string{candidate}); err != nil {
		t.Fatalf("expected verbatim echo of a tracked candidate URL to be accepted, got: %v", err)
	}
}

// TestCheckLink_AcceptsStrippedEchoOfTrackedCandidate covers the case
// step 4 exists for: the model strips tracking params on the way out even
// though the candidate carried them. Both sides normalize to the same
// tracking-free URL, so this must also be accepted.
func TestCheckLink_AcceptsStrippedEchoOfTrackedCandidate(t *testing.T) {
	candidate := "https://news.example.com/story?utm_source=rss&id=42"
	modelEcho := "https://news.example.com/story?id=42"

	if err := CheckLink(modelEcho, []string{candidate}); err != nil {
		t.Fatalf("expected stripped echo to be accepted, got: %v", err)
	}
}

func TestCheckLink_RejectsURLAbsentFromCandidateSet(t *testing.T) {
	candidates := []string{"https://news.example.com/real-story"}
	link := "https://news.example.com/invented-story"

	err := CheckLink(link, candidates)
	if err == nil {
		t.Fatal("expected an error for a link absent from the candidate set")
	}
	if !strings.Contains(err.Error(), ReasonLinkNotCandidate) {
		t.Fatalf("error %v does not carry reason token %q", err, ReasonLinkNotCandidate)
	}
}

func TestCheckLink_RejectsEmptyLink(t *testing.T) {
	err := CheckLink("", []string{"https://example.com/x"})
	if err == nil {
		t.Fatal("expected an error for an empty link")
	}
	if !strings.Contains(err.Error(), ReasonLinkRequiredGrounded) {
		t.Fatalf("error %v does not carry reason token %q", err, ReasonLinkRequiredGrounded)
	}
}

// --- AnswerHTML sanitization ------------------------------------------------
//
// AnswerHTML used to be the one model-authored markup field that reached the
// wire unsanitized: Validate sanitized BodyHTML and assigned AnswerHTML
// through untouched, while render/permalink.go wrote it raw into the public
// page and render/rss.go concatenated it into content:encoded. These tests
// pin the fix so a future refactor of Validate cannot quietly reopen it.

func TestValidate_SanitizesAnswerHTML(t *testing.T) {
	c := validCandidate()
	c.AnswerHTML = `<p>Sunrise<script>alert(1)</script></p>`

	item, rejections, err := Validate(c, Options{Kind: model.KindGenerative})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rejections) > 0 {
		t.Fatalf("unexpected rejections: %v", rejectionReasons(rejections))
	}
	if strings.Contains(item.AnswerHTML, "script") {
		t.Fatalf("answer_html reached the item with script markup intact: %q", item.AnswerHTML)
	}
	// The legitimate text around it survives — this is a sanitizer, not a
	// rejection, so the item is still publishable.
	if !strings.Contains(item.AnswerHTML, "Sunrise") {
		t.Fatalf("sanitizing answer_html destroyed its real content: %q", item.AnswerHTML)
	}
}

func TestValidate_StripsEventHandlerAttributesFromAnswerHTML(t *testing.T) {
	c := validCandidate()
	c.AnswerHTML = `<p onmouseover="steal()">Cowboy Bebop</p>`

	item, rejections, err := Validate(c, Options{Kind: model.KindGenerative})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(rejections) > 0 {
		t.Fatalf("unexpected rejections: %v", rejectionReasons(rejections))
	}
	if strings.Contains(strings.ToLower(item.AnswerHTML), "onmouseover") {
		t.Fatalf("answer_html kept an event handler attribute: %q", item.AnswerHTML)
	}
}

func TestValidate_RejectsRelativeLinkInAnswerHTML(t *testing.T) {
	// Same §5.1 rule the body is held to: both land in content:encoded, and
	// RSS has no base-URL mechanism, so a relative href is unresolvable in
	// an answer for exactly the reason it is unresolvable in a body.
	c := validCandidate()
	c.AnswerHTML = `<p>See <a href="/spoilers">the answer</a>.</p>`

	_, rejections, err := Validate(c, Options{Kind: model.KindGenerative})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !containsReason(rejections, ReasonBodyRelativeLink) {
		t.Fatalf("expected %s for a relative href in answer_html, got %v",
			ReasonBodyRelativeLink, rejectionReasons(rejections))
	}
}

func TestValidate_AnswerLeakStillCheckedAgainstRawMarkup(t *testing.T) {
	// The leak check deliberately runs on the RAW answer, not the sanitized
	// one, so sanitization cannot mask a leak by deleting the evidence.
	c := validCandidate()
	c.SummaryText = "The studio is sunrise, obviously"
	c.AnswerHTML = `<p>sunrise</p>`

	_, rejections, err := Validate(c, Options{Kind: model.KindGenerative})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !containsReason(rejections, ReasonAnswerLeaked) {
		t.Fatalf("expected %s, got %v", ReasonAnswerLeaked, rejectionReasons(rejections))
	}
}
