package generate

import (
	"strings"
	"testing"
	"time"
)

func TestRender_AllVariables(t *testing.T) {
	tmpl := `Today: {{.Today}}
Weekday: {{.Weekday}}
Season: {{.Season}}
Feed: {{.FeedTitle}}
Items: {{.ItemsPerRun}}
Recent:{{range .RecentTitles}} {{.}}{{end}}
Candidates:{{range .Candidates}} {{.Title}}|{{.URL}}|{{.Published}}|{{.Excerpt}}{{end}}`

	data := Data{
		Today:        "2026-08-10",
		Weekday:      "Monday",
		Season:       "Summer 2026",
		FeedTitle:    "Anime Trivia Daily",
		ItemsPerRun:  1,
		RecentTitles: []string{"Old Title One", "Old Title Two"},
		Candidates: []SourceArticle{
			{Title: "Some News", URL: "https://example.com/n", Published: "2026-08-09", Excerpt: "excerpt text"},
		},
	}

	out, err := Render(tmpl, data)
	if err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	for _, want := range []string{
		"Today: 2026-08-10",
		"Weekday: Monday",
		"Season: Summer 2026",
		"Feed: Anime Trivia Daily",
		"Items: 1",
		"Old Title One",
		"Old Title Two",
		"Some News|https://example.com/n|2026-08-09|excerpt text",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered output missing %q; got:\n%s", want, out)
		}
	}
}

func TestValidate_RejectsUnknownVariable(t *testing.T) {
	err := ValidateTemplate(`Hello {{.Nonexistent}}`)
	if err == nil {
		t.Fatal("expected an error for a template referencing an unknown field")
	}
}

func TestValidate_RejectsUnknownVariableInsideRange(t *testing.T) {
	// This is the case a naive Validate (Parse-only, or Execute against an
	// empty-slice dummy) would miss: the bad field only surfaces once the
	// range body actually executes.
	err := ValidateTemplate(`{{range .Candidates}}{{.Bogus}}{{end}}`)
	if err == nil {
		t.Fatal("expected an error for an unknown field referenced inside a range body")
	}
}

func TestValidate_AcceptsRealVariableSet(t *testing.T) {
	tmpl := `{{.Today}} {{.Weekday}} {{.Season}} {{.FeedTitle}} {{.ItemsPerRun}}
{{range .RecentTitles}}{{.}}{{end}}
{{range .Candidates}}{{.Title}} {{.URL}} {{.Published}} {{.Excerpt}}{{end}}`

	if err := ValidateTemplate(tmpl); err != nil {
		t.Fatalf("expected the real variable set to validate cleanly, got: %v", err)
	}
}

func TestValidate_RejectsMalformedTemplate(t *testing.T) {
	err := ValidateTemplate(`{{.Today`)
	if err == nil {
		t.Fatal("expected an error for a malformed template")
	}
}

func TestHash_StableAndSensitiveToContent(t *testing.T) {
	a := Hash("rendered prompt A")
	b := Hash("rendered prompt A")
	c := Hash("rendered prompt B")

	if a != b {
		t.Errorf("Hash is not stable: %q != %q", a, b)
	}
	if a == c {
		t.Errorf("Hash did not change with content: %q == %q", a, c)
	}
	if len(a) != 64 { // sha256 hex digest length
		t.Errorf("Hash length = %d, want 64", len(a))
	}
}

func TestSeason_Boundaries(t *testing.T) {
	tests := []struct {
		date time.Time
		want string
	}{
		{time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC), "Winter 2026"},
		{time.Date(2026, time.March, 31, 0, 0, 0, 0, time.UTC), "Winter 2026"},
		{time.Date(2026, time.April, 1, 0, 0, 0, 0, time.UTC), "Spring 2026"},
		{time.Date(2026, time.June, 30, 0, 0, 0, 0, time.UTC), "Spring 2026"},
		{time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC), "Summer 2026"},
		{time.Date(2026, time.September, 30, 0, 0, 0, 0, time.UTC), "Summer 2026"},
		{time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC), "Fall 2026"},
		{time.Date(2026, time.December, 31, 0, 0, 0, 0, time.UTC), "Fall 2026"},
		{time.Date(2027, time.January, 1, 0, 0, 0, 0, time.UTC), "Winter 2027"},
	}

	for _, tt := range tests {
		t.Run(tt.want+"_"+tt.date.Format("2006-01-02"), func(t *testing.T) {
			if got := Season(tt.date); got != tt.want {
				t.Errorf("Season(%s) = %q, want %q", tt.date.Format("2006-01-02"), got, tt.want)
			}
		})
	}
}
