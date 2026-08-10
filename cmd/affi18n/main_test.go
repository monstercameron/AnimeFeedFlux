package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

// runCapture runs the CLI's run() dispatcher and captures stdout/stderr.
// run() takes *os.File (matching cmd/affvalidate's convention in this
// repo), so real pipes stand in for the usual io.Writer test doubles.
func runCapture(t *testing.T, args []string) (stdout, stderr string, code int) {
	t.Helper()

	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	code = run(args, outW, errW)
	outW.Close()
	errW.Close()

	outBytes, _ := io.ReadAll(outR)
	errBytes, _ := io.ReadAll(errR)
	return string(outBytes), string(errBytes), code
}

func TestRun_NoArgs(t *testing.T) {
	_, stderr, code := runCapture(t, nil)
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "usage:") {
		t.Fatalf("stderr = %q, want usage text", stderr)
	}
}

func TestRun_UnknownCommand(t *testing.T) {
	_, stderr, code := runCapture(t, []string{"bogus"})
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "bogus") {
		t.Fatalf("stderr = %q, want it to name the unknown command", stderr)
	}
}

func TestRun_Lint_FindsLiteral(t *testing.T) {
	t.Chdir("testdata/proj")

	stdout, stderr, code := runCapture(t, []string{"lint", "web"})
	if code != 1 {
		t.Fatalf("code = %d, want 1 (fixture has one hardcoded literal); stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "Hardcoded literal that should be caught") {
		t.Fatalf("stdout = %q, want the flagged literal text", stdout)
	}
	if !strings.Contains(stdout, "web/app.go:") {
		t.Fatalf("stdout = %q, want a clickable file:line reference", stdout)
	}
}

func TestRun_Lint_CleanDirIsZeroExit(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir+"/web", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/web/doc.go", []byte("package web\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	stdout, _, code := runCapture(t, []string{"lint", "web"})
	if code != 0 {
		t.Fatalf("code = %d, want 0; stdout=%q", code, stdout)
	}
}

func TestRun_Check_ReportsMissingAndUnused(t *testing.T) {
	t.Chdir("testdata/proj")

	stdout, stderr, code := runCapture(t, []string{"check", "--catalogue=catalogue.json", "web"})
	if code != 1 {
		t.Fatalf("code = %d, want 1; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, `"app.unused.ref"`) {
		t.Fatalf("stdout = %q, want the missing key reported", stdout)
	}
	if !strings.Contains(stdout, `"app.orphan"`) {
		t.Fatalf("stdout = %q, want the unused key reported", stdout)
	}
}

func TestRun_Check_RequiresCatalogueFlag(t *testing.T) {
	_, stderr, code := runCapture(t, []string{"check"})
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "--catalogue") {
		t.Fatalf("stderr = %q, want it to name the missing flag", stderr)
	}
}

func TestRun_Ratchet_OKAtBaseline(t *testing.T) {
	t.Chdir("testdata/proj")

	_, stderr, code := runCapture(t, []string{"ratchet", "--baseline=baseline_ok.txt", "web"})
	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%q", code, stderr)
	}
}

func TestRun_Ratchet_FailsAboveBaseline(t *testing.T) {
	t.Chdir("testdata/proj")

	_, stderr, code := runCapture(t, []string{"ratchet", "--baseline=baseline_bad.txt", "web"})
	if code != 1 {
		t.Fatalf("code = %d, want 1; stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "may not rise") {
		t.Fatalf("stderr = %q, want the ratchet-may-not-rise message", stderr)
	}
}

func TestRun_Pseudo_Args(t *testing.T) {
	stdout, _, code := runCapture(t, []string{"pseudo", "Save", "Cancel"})
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("stdout lines = %v, want 2", lines)
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "[") || !strings.HasSuffix(l, "]") {
			t.Errorf("line %q not bracketed", l)
		}
	}
}
