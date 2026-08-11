// Package testutil holds the test infrastructure specified in PLAN.md §17.1,
// shared across every suite in this tree so it is written once rather than
// reinvented per package:
//
//   - a golden-file comparator (this file), so a deliberate format change is
//     one `-update` flag and a reviewed diff, never hand-editing recorded
//     XML/JSON;
//   - deterministic fixtures (fixtures.go) — a seeded feed, items and
//     channel with known, stable values, so a schema change is one edit
//     here rather than twenty edits across the test suite, and golden files
//     stay stable run to run;
//   - an injected http.Client (httpclient.go) that serves testdata/ for
//     upstream fetches instead of dialing out, which is also load-bearing
//     for RULE-1 (A0-T07): no test in this tree may need a live network.
package testutil

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// update, set via `go test ./... -update`, rewrites golden files instead of
// comparing against them. Declared at package scope (not inside a test
// func) so it registers exactly once per test binary, the same pattern
// stdlib's own -test.update-golden-style flags use.
var update = flag.Bool("update", false, "rewrite golden files instead of comparing against them")

// diffContext is how many lines of surrounding context to print on either
// side of the first divergence — enough to orient the reader without
// dumping the whole file.
const diffContext = 2

// goldenPath returns the path to name's golden file relative to the calling
// package's directory. `go test` always runs with the package under test as
// the working directory, so a relative path here is what makes "relative to
// the calling package" true without resorting to runtime.Caller.
func goldenPath(name string) string {
	return filepath.Join("testdata", "golden", name+".golden")
}

// Assert compares got against the golden file for name, and with -update
// rewrites it instead. See AssertString for the string convenience form.
func Assert(t *testing.T, name string, got []byte) {
	t.Helper()
	if err := assertGolden(name, got); err != nil {
		t.Fatal(err)
	}
}

// AssertString is the string convenience form of Assert.
func AssertString(t *testing.T, name string, got string) {
	t.Helper()
	Assert(t, name, []byte(got))
}

// assertGolden holds the actual compare-or-update logic behind a plain
// error return, rather than taking a *testing.T directly, so this package's
// own tests can inspect the exact failure text without forking a
// subprocess to catch a t.Fatalf.
func assertGolden(name string, got []byte) error {
	path := goldenPath(name)

	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("testutil: creating golden dir %q: %w", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			return fmt.Errorf("testutil: writing golden file %q: %w", path, err)
		}
		return nil
	}

	want, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("testutil: golden file %q missing or unreadable: %w\nrecord it with: go test -update ./... (run from this package's directory)", path, err)
	}

	// Exact byte comparison, deliberately: no trimming, no CRLF/LF
	// normalization. This repo is developed on Windows and deployed to
	// Linux, and a helper that quietly normalized line endings would hide
	// exactly the class of encoding bug golden files exist to catch — a
	// renderer that emits "\r\n" into a feed on one platform and not the
	// other.
	if bytes.Equal(want, got) {
		return nil
	}

	return fmt.Errorf(
		"golden mismatch: %s\n%s\nre-record with: go test -update ./... (run from this package's directory, or add -run to limit which tests rewrite)",
		path, diffReport(want, got),
	)
}

// diffReport renders the first differing line and diffContext lines of
// surrounding context from both sides, so a failure is legible without
// pulling the golden file up in an editor.
func diffReport(want, got []byte) string {
	line := firstDiffLine(want, got)
	wantLines := strings.Split(string(want), "\n")
	gotLines := strings.Split(string(got), "\n")

	lo := line - diffContext
	if lo < 1 {
		lo = 1
	}

	var b strings.Builder
	fmt.Fprintf(&b, "first difference at line %d (want %d lines, got %d lines)\n", line, len(wantLines), len(gotLines))

	b.WriteString("--- want\n")
	for n := lo; n <= min(line+diffContext, len(wantLines)); n++ {
		fmt.Fprintf(&b, "%5d | %q\n", n, wantLines[n-1])
	}
	b.WriteString("--- got\n")
	for n := lo; n <= min(line+diffContext, len(gotLines)); n++ {
		fmt.Fprintf(&b, "%5d | %q\n", n, gotLines[n-1])
	}
	return b.String()
}

// firstDiffLine returns the 1-based line number of the first byte at which
// want and got diverge, counting newlines in got up to that byte offset. A
// difference confined to a trailing newline (one is a byte-for-byte prefix
// of the other) still resolves to a sensible line: the offset where the
// shorter side ran out.
func firstDiffLine(want, got []byte) int {
	n := len(want)
	if len(got) < n {
		n = len(got)
	}
	i := 0
	for i < n && want[i] == got[i] {
		i++
	}

	countFrom := got
	if i > len(countFrom) {
		countFrom = want
	}
	line := 1
	for _, c := range countFrom[:i] {
		if c == '\n' {
			line++
		}
	}
	return line
}
