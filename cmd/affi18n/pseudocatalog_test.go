package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	gwci18n "github.com/monstercameron/GoWebComponents/v5/i18n"
)

// This command exists to catch a placeholder-mangling regression in the REAL
// catalogue, and it had no test of its own — so the check that guards every
// string in the app was itself unguarded.

func TestPseudoCatalogRunsCleanAgainstTheShippedCatalogue(t *testing.T) {
	stdout, stderr := captureFiles(t)

	code := runPseudoCatalog(stdout.file, stderr.file)

	out := stdout.read(t)
	errOut := stderr.read(t)
	if code != 0 {
		t.Fatalf("pseudo-catalog exited %d against the shipped catalogue\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	if errOut != "" {
		t.Errorf("a clean run wrote to stderr: %q", errOut)
	}
	if !strings.Contains(out, "0 placeholder mismatches") {
		t.Errorf("the summary line is missing:\n%s", out)
	}

	// It has to actually have inspected something: a catalogue that failed to
	// load would also produce zero mismatches, and that is the failure mode
	// this test exists to rule out.
	lines := strings.Count(out, "\n")
	if lines < 50 {
		t.Errorf("only %d lines of output — the catalogue looks empty:\n%s", lines, out)
	}
	if !strings.Contains(out, " = ") {
		t.Errorf("no 'key = text' rows in the output:\n%s", out)
	}

	// Output is sorted, so two runs are diffable — the whole point of
	// emitting it as a checkable artifact.
	if !isSorted(entryKeys(out)) {
		t.Error("entries are not in sorted order, so two runs cannot be diffed")
	}
}

func TestPrimaryTextPrefersTextThenPluralThenSelectThenDefault(t *testing.T) {
	// Which representative string is picked does not change whether the
	// placeholder check is meaningful — every form of a well-formed message
	// shares a placeholder set — but picking NOTHING would silently skip an
	// entry, and a skipped entry is an unchecked one.
	cases := []struct {
		name string
		msg  gwci18n.Message
		want string
	}{
		{"text wins", gwci18n.Message{Text: "plain", Default: "fallback"}, "plain"},
		{"plural when there is no text", gwci18n.Message{Plural: map[gwci18n.PluralCategory]string{gwci18n.PluralOther: "{arg1} items"}}, "{arg1} items"},
		{"select when there is neither", gwci18n.Message{Select: map[string]string{"male": "he"}}, "he"},
		{"default last", gwci18n.Message{Default: "fallback"}, "fallback"},
		{"nothing at all", gwci18n.Message{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := primaryText(tc.msg); got != tc.want {
				t.Errorf("primaryText = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCountByte(t *testing.T) {
	// The mismatch check is a byte count, deliberately — it must not try to
	// parse the placeholder syntax, or it becomes a second, competing
	// implementation of the thing it is checking.
	cases := []struct {
		in   string
		b    byte
		want int
	}{
		{"", '%', 0},
		{"100%", '%', 1},
		{"%d of %d (%%)", '%', 4},
		{"{{.Today}} and {{.Season}}", '{', 4},
		{"no braces here", '{', 0},
		{"héllo %s", '%', 1}, // multi-byte runes must not confuse a byte count
	}
	for _, tc := range cases {
		if got := countByte(tc.in, tc.b); got != tc.want {
			t.Errorf("countByte(%q, %q) = %d, want %d", tc.in, string(tc.b), got, tc.want)
		}
	}
}

// --- helpers ----------------------------------------------------------------

// runPseudoCatalog writes to *os.File, so the test hands it real temp files
// rather than buffers.
type capturedFile struct {
	file *os.File
	path string
}

func (c capturedFile) read(t *testing.T) string {
	t.Helper()
	if err := c.file.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	b, err := os.ReadFile(c.path)
	if err != nil {
		t.Fatalf("reading %s: %v", c.path, err)
	}
	return string(b)
}

func captureFiles(t *testing.T) (stdout, stderr capturedFile) {
	t.Helper()
	dir := t.TempDir()
	mk := func(name string) capturedFile {
		path := filepath.Join(dir, name)
		f, err := os.Create(path)
		if err != nil {
			t.Fatalf("creating %s: %v", path, err)
		}
		t.Cleanup(func() { _ = f.Close() })
		return capturedFile{file: f, path: path}
	}
	return mk("stdout.txt"), mk("stderr.txt")
}

func entryKeys(out string) []string {
	var keys []string
	for _, line := range strings.Split(out, "\n") {
		name, _, ok := strings.Cut(line, " = ")
		if ok {
			keys = append(keys, name)
		}
	}
	return keys
}

func isSorted(keys []string) bool {
	for i := 1; i < len(keys); i++ {
		if keys[i-1] > keys[i] {
			return false
		}
	}
	return true
}

func TestRunPseudoWithArguments(t *testing.T) {
	// Pseudolocalization has to widen the text (that is how a truncation bug
	// shows up in a screenshot) while leaving every placeholder byte-identical
	// — a pseudolocalizer that mangles a placeholder produces a false failure
	// on every run that touches it, which is how a pseudolocale build gets
	// turned off.
	stdout, stderr := captureFiles(t)
	code := runPseudo([]string{"Save recipe", "{{.Today}} — %d items"}, stdout.file, stderr.file)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr: %s", code, stderr.read(t))
	}

	lines := strings.Split(strings.TrimRight(stdout.read(t), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines for 2 arguments: %q", len(lines), lines)
	}
	if lines[0] == "Save recipe" {
		t.Error("the text came back unchanged, so nothing was pseudolocalized")
	}
	if len(lines[0]) <= len("Save recipe") {
		t.Errorf("pseudolocalized text %q is not wider than the original", lines[0])
	}
	if !strings.Contains(lines[1], "{{.Today}}") || !strings.Contains(lines[1], "%d") {
		t.Errorf("a placeholder was mangled: %q", lines[1])
	}
}

func TestRunPseudoReadsStdinLineByLine(t *testing.T) {
	// The no-argument form is what a pseudolocale build step pipes a whole
	// catalogue through, so it has to handle many lines, not just the first.
	dir := t.TempDir()
	path := filepath.Join(dir, "in.txt")
	if err := os.WriteFile(path, []byte("Save recipe\n{{.Today}}\n%d items\n"), 0o600); err != nil {
		t.Fatalf("writing stdin fixture: %v", err)
	}
	in, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening stdin fixture: %v", err)
	}
	t.Cleanup(func() { _ = in.Close() })

	real := os.Stdin
	os.Stdin = in
	t.Cleanup(func() { os.Stdin = real })

	stdout, stderr := captureFiles(t)
	if code := runPseudo(nil, stdout.file, stderr.file); code != 0 {
		t.Fatalf("exit code = %d, stderr: %s", code, stderr.read(t))
	}

	lines := strings.Split(strings.TrimRight(stdout.read(t), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d output lines for 3 input lines: %q", len(lines), lines)
	}
	if !strings.Contains(lines[1], "{{.Today}}") || !strings.Contains(lines[2], "%d") {
		t.Errorf("a placeholder was mangled while streaming: %q", lines)
	}
}
