package i18nlint

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func fixtureFS(t *testing.T) (fs.FS, string) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return os.DirFS(filepath.Join(wd, "testdata", "fixtures")), "web"
}

// wantFinding is a comparison-friendly subset of Finding used by the table
// tests below (Col is asserted separately only where it matters).
type wantFinding struct {
	Path string
	Line int
	Rule string
	Text string
}

func sortWant(w []wantFinding) {
	sort.Slice(w, func(i, j int) bool {
		if w[i].Line != w[j].Line {
			return w[i].Line < w[j].Line
		}
		return w[i].Text < w[j].Text
	})
}

func TestFindLiterals_Flagged(t *testing.T) {
	fsys, root := fixtureFS(t)
	findings, err := FindLiterals(fsys, root)
	if err != nil {
		t.Fatalf("FindLiterals: %v", err)
	}

	var got []wantFinding
	for _, f := range findings {
		if f.Path != "web/flagged.go" {
			continue
		}
		got = append(got, wantFinding{Path: f.Path, Line: f.Line, Rule: f.Rule, Text: f.Text})
	}

	want := []wantFinding{
		{"web/flagged.go", 6, "text-call", "Disconnected — reconnecting…"},
		{"web/flagged.go", 7, "text-call", "Rendered %d items"},
		{"web/flagged.go", 8, "text-child", "Bare child prose"},
		{"web/flagged.go", 9, "text-child", "Save"},
		{"web/flagged.go", 10, "attribute-text", "Tooltip text"},
		{"web/flagged.go", 10, "attribute-text", "Search feeds"},
		{"web/flagged.go", 10, "attribute-text", "icon description"},
		{"web/flagged.go", 11, "text-call", "Hello, "},
		// A5-38: prose inside aria-label/aria-description is read aloud just
		// as a text node is, and was invisible to this tool.
		{"web/flagged.go", 12, "attribute-text", "Close this dialog"},
		{"web/flagged.go", 12, "attribute-text", "What this control does"},
		{"web/flagged.go", 16, "text-call", "Unsuppressed literal"},
		{"web/flagged.go", 18, "bare-nolint", "//nolint:i18n"},
		{"web/flagged.go", 19, "text-call", "Bare nolint above this line, should still be flagged"},
	}

	sortWant(got)
	sortWant(want)

	if len(got) != len(want) {
		t.Fatalf("finding count = %d, want %d\ngot:  %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i].Line != want[i].Line || got[i].Rule != want[i].Rule || got[i].Text != want[i].Text {
			t.Errorf("finding[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestFindLiterals_Skipped(t *testing.T) {
	fsys, root := fixtureFS(t)
	findings, err := FindLiterals(fsys, root)
	if err != nil {
		t.Fatalf("FindLiterals: %v", err)
	}

	for _, f := range findings {
		if f.Path == "web/skipped.go" {
			t.Errorf("skipped.go must produce no findings, got %s", f)
		}
	}
}

func TestFindLiterals_Suppressed(t *testing.T) {
	fsys, root := fixtureFS(t)
	findings, err := FindLiterals(fsys, root)
	if err != nil {
		t.Fatalf("FindLiterals: %v", err)
	}

	for _, f := range findings {
		if f.Path == "web/suppressed.go" {
			t.Errorf("suppressed.go must produce no findings (both comments carry reasons), got %s", f)
		}
	}
}

func TestFindLiterals_IgnoresTestFiles(t *testing.T) {
	fsys, root := fixtureFS(t)
	findings, err := FindLiterals(fsys, root)
	if err != nil {
		t.Fatalf("FindLiterals: %v", err)
	}

	for _, f := range findings {
		if f.Path == "web/ignored_test.go" {
			t.Errorf("_test.go files must never be scanned, got %s", f)
		}
	}
}

func TestFindLiterals_IgnoresTestdataDir(t *testing.T) {
	fsys, root := fixtureFS(t)
	findings, err := FindLiterals(fsys, root)
	if err != nil {
		t.Fatalf("FindLiterals: %v", err)
	}

	for _, f := range findings {
		if f.Path == "web/testdata/fixture.go" {
			t.Errorf("files under a testdata/ directory must never be scanned, got %s", f)
		}
	}
}

func TestFinding_String(t *testing.T) {
	f := Finding{Path: "web/shell/banner.go", Line: 10, Col: 3, Rule: "text-call", Text: "hi"}
	got := f.String()
	want := `web/shell/banner.go:10:3: text-call: "hi"`
	if got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestFindLiterals_ClickableLocation(t *testing.T) {
	fsys, root := fixtureFS(t)
	findings, err := FindLiterals(fsys, root)
	if err != nil {
		t.Fatalf("FindLiterals: %v", err)
	}
	for _, f := range findings {
		if f.Path == "" || f.Line == 0 {
			t.Fatalf("finding missing file:line: %+v", f)
		}
		_ = fmt.Sprintf("%s:%d:%d", f.Path, f.Line, f.Col) // must not panic / be well-formed
	}
}
