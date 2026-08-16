package prompttmpl

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"text/template"
	"time"
)

// SourceArticle is one fetched candidate article, as listed in {{.Candidates}}
// for grounded feeds (PLAN.md §7, §9 step 1). By the time it reaches the
// template its URL has already been through urlnorm.Normalize once at
// acquisition — this package does not re-normalize it, it only renders it.
type SourceArticle struct {
	Title     string
	URL       string
	Published string
	Excerpt   string
}

// Data is the exact variable set PLAN.md §7 promises the prompt editor:
//
//	{{.Today}}          date in the feed's timezone, YYYY-MM-DD
//	{{.Weekday}}        e.g. "Tuesday"
//	{{.Season}}         "Winter 2026" — the anime season for .Today
//	{{.FeedTitle}}
//	{{.ItemsPerRun}}
//	{{.RecentTitles}}   the exclusion list (generative)
//	{{.Candidates}}     fetched source articles (grounded)
//
// Adding a field here without a matching line in PLAN.md §7 (or vice versa)
// is the drift this struct exists to prevent — the editor's variable list is
// meant to be generated from, or at least checked against, this type.
type Data struct {
	Today        string
	Weekday      string
	Season       string
	FeedTitle    string
	ItemsPerRun  int
	RecentTitles []string
	Candidates   []SourceArticle
}

// Render executes tmpl against data. It is the same code path ValidateTemplate
// uses, so "this passed ValidateTemplate" and "this is what actually renders"
// can never diverge into two implementations that quietly disagree.
func Render(tmpl string, data Data) (string, error) {
	t, err := template.New("prompt").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("prompt: parse: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("prompt: execute: %w", err)
	}
	return buf.String(), nil
}

// ValidateTemplate reports whether tmpl only references real Data fields.
//
// text/template's Parse does NOT check that {{.Foo}} names a real struct
// field — that only surfaces when the template is executed, and
// missingkey=error (the usual fix people reach for) only governs map
// lookups, not struct fields, so it does not help here. The only reliable
// check is to actually EXECUTE the template against a context, which is
// what this does — against a dummy Data that is fully populated, crucially
// including at least one element in every slice. An empty slice would let
// {{range .Candidates}}{{.Bogus}}{{end}} pass Validate cleanly, because the
// range body never runs over zero elements and a bad field inside it is
// never evaluated; a real recipe's Candidates is rarely empty, so that gap
// would surface as a broken prompt at 4am (§7) instead of at save time.
func ValidateTemplate(tmpl string) error {
	if _, err := Render(tmpl, dummyData()); err != nil {
		return err
	}
	return nil
}

func dummyData() Data {
	return Data{
		Today:        "2026-08-10",
		Weekday:      "Monday",
		Season:       "Summer 2026",
		FeedTitle:    "Sample Feed",
		ItemsPerRun:  1,
		RecentTitles: []string{"A previously published title"},
		Candidates: []SourceArticle{
			{
				Title:     "A sample candidate article",
				URL:       "https://example.com/article",
				Published: "2026-08-09",
				Excerpt:   "A short excerpt of the article.",
			},
		},
	}
}

// Hash returns the sha256 hex digest of a rendered prompt. Stored per item
// as prompt_hash (PLAN.md §7, §10) so a quality regression can be traced
// back to the exact prompt that produced it, even after the template is
// later edited.
func Hash(rendered string) string {
	sum := sha256.Sum256([]byte(rendered))
	return hex.EncodeToString(sum[:])
}

// anime season quarters: Winter = Jan-Mar, Spring = Apr-Jun, Summer =
// Jul-Sep, Fall = Oct-Dec. This is the industry's standard broadcast-season
// grouping, not a calendar-season (meteorological or astronomical) one.
var seasonNames = [4]string{"Winter", "Spring", "Summer", "Fall"}

// Season maps a date to its anime broadcast season, e.g. "Winter 2026" for
// any date in January-March 2026.
func Season(t time.Time) string {
	quarter := (int(t.Month()) - 1) / 3 // 0..3
	return fmt.Sprintf("%s %d", seasonNames[quarter], t.Year())
}
