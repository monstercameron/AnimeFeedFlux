package testutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withTempCwd chdirs into a fresh temp directory for the test's duration,
// so goldenPath's relative "testdata/golden/..." lands there instead of in
// this repo — these tests exercise the helper's own file I/O and must not
// leave files behind.
func withTempCwd(t *testing.T) {
	t.Helper()

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("chdir into temp dir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatalf("restoring cwd: %v", err)
		}
	})
}

func writeGolden(t *testing.T, name string, data []byte) {
	t.Helper()
	path := goldenPath(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("test setup: mkdir: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("test setup: write golden: %v", err)
	}
}

func TestAssert_Match(t *testing.T) {
	withTempCwd(t)
	writeGolden(t, "match", []byte("hello\nworld\n"))

	Assert(t, "match", []byte("hello\nworld\n"))
}

func TestAssertGolden_MismatchNamesGoldenPath(t *testing.T) {
	withTempCwd(t)
	writeGolden(t, "mismatch", []byte("line one\nline two\nline three\n"))

	err := assertGolden("mismatch", []byte("line one\nCHANGED\nline three\n"))
	if err == nil {
		t.Fatal("expected a mismatch error, got nil")
	}

	msg := err.Error()
	wantPath := goldenPath("mismatch")
	if !strings.Contains(msg, wantPath) {
		t.Fatalf("error message %q does not name the golden path %q", msg, wantPath)
	}
	if !strings.Contains(msg, "line 2") {
		t.Fatalf("error message %q does not identify the differing line", msg)
	}
	if !strings.Contains(msg, "CHANGED") {
		t.Fatalf("error message %q does not show the differing content as context", msg)
	}
	if !strings.Contains(msg, "-update") {
		t.Fatalf("error message %q does not include the re-record command", msg)
	}
}

func TestAssertGolden_MissingFile(t *testing.T) {
	withTempCwd(t)

	err := assertGolden("does-not-exist", []byte("anything\n"))
	if err == nil {
		t.Fatal("expected an error for a missing golden file, got nil")
	}
	if !strings.Contains(err.Error(), goldenPath("does-not-exist")) {
		t.Fatalf("error message %q does not name the missing golden path", err.Error())
	}
}

func TestAssert_UpdateCreatesThenMatches(t *testing.T) {
	withTempCwd(t)

	old := *update
	*update = true
	t.Cleanup(func() { *update = old })

	content := []byte("freshly recorded content\n")
	Assert(t, "created", content)

	path := goldenPath("created")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected -update to create %q: %v", path, err)
	}
	if string(got) != string(content) {
		t.Fatalf("golden file %q = %q, want %q", path, got, content)
	}

	// Switch back to comparison mode: the just-recorded golden must now
	// match the same bytes without rewriting anything.
	*update = false
	Assert(t, "created", content)
}

func TestAssertGolden_TrailingNewlineDiffers(t *testing.T) {
	withTempCwd(t)
	writeGolden(t, "trailing", []byte("content"))

	// Byte-exactness: a trailing newline the golden lacks must still fail,
	// even though every visible line "matches". This is the whole point of
	// comparing raw bytes instead of trimmed/normalized text.
	if err := assertGolden("trailing", []byte("content\n")); err == nil {
		t.Fatal("expected a trailing-newline difference to fail, got nil error")
	}
}
