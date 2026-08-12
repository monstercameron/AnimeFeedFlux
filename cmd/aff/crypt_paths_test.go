package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monstercameron/AnimeFeedFlux/internal/ops"
)

// crypt_paths_test.go covers the branches of `aff encrypt` / `aff decrypt`
// that the existing round-trip test does not reach: the overwrite gate, the
// usage errors, and the failure modes around opening files.
//
// The overwrite gate is the one that matters. These two commands are the
// off-box shipping step between "backup taken" and "backup safe" (PLAN.md
// §15), so both sides of a mistake are expensive: silently overwriting an
// --out that already holds yesterday's encrypted snapshot destroys a backup,
// and refusing to proceed when the operator did say --yes stalls the nightly
// job. Neither shows up until the day you need the backup.

func newCryptApp(t *testing.T, stdin string) (*app, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	a := &app{
		Stdout:      stdout,
		Stderr:      stderr,
		Stdin:       strings.NewReader(stdin),
		SessionFile: filepath.Join(t.TempDir(), "session.json"),
	}
	return a, stdout, stderr
}

func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestCryptRequiresBothInAndOut(t *testing.T) {
	t.Setenv(ops.EncryptionKeyEnv, testEncryptionKey(t))
	in := writeTempFile(t, "in.bin", "payload")

	cases := []struct {
		name string
		args []string
	}{
		{"neither", []string{"encrypt"}},
		{"only --in", []string{"encrypt", "--in", in}},
		{"only --out", []string{"encrypt", "--out", filepath.Join(t.TempDir(), "out.bin")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, _, stderr := newCryptApp(t, "")
			if code := a.run(tc.args); code != exitUsage {
				t.Errorf("exit = %d, want %d (stderr: %s)", code, exitUsage, stderr.String())
			}
			if !strings.Contains(stderr.String(), "required") {
				t.Errorf("stderr = %q, want it to name the missing flags", stderr.String())
			}
		})
	}
}

func TestCryptRejectsPositionalArguments(t *testing.T) {
	t.Setenv(ops.EncryptionKeyEnv, testEncryptionKey(t))
	a, _, stderr := newCryptApp(t, "")
	code := a.run([]string{"encrypt", "--in", "a", "--out", "b", "stray"})
	if code != exitUsage {
		t.Errorf("exit = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr.String(), "positional") {
		t.Errorf("stderr = %q, want it to explain that positional args are not accepted", stderr.String())
	}
}

// TestCryptRefusesToOverwriteWithoutConsent is the backup-destroying case.
func TestCryptRefusesToOverwriteWithoutConsent(t *testing.T) {
	t.Setenv(ops.EncryptionKeyEnv, testEncryptionKey(t))
	in := writeTempFile(t, "in.bin", "payload")
	existing := writeTempFile(t, "out.bin", "PRECIOUS EXISTING BACKUP")

	// Answering anything but y/yes must abort, and must leave the file byte
	// for byte as it was.
	a, _, stderr := newCryptApp(t, "n\n")
	if code := a.run([]string{"encrypt", "--in", in, "--out", existing}); code != exitFail {
		t.Errorf("exit = %d, want %d when the operator declines", code, exitFail)
	}
	if !strings.Contains(stderr.String(), "aborted") {
		t.Errorf("stderr = %q, want it to say the file was not touched", stderr.String())
	}
	got, err := os.ReadFile(existing)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "PRECIOUS EXISTING BACKUP" {
		t.Error("the existing --out file was modified despite the operator declining")
	}
}

func TestCryptOverwritesWhenConfirmed(t *testing.T) {
	t.Setenv(ops.EncryptionKeyEnv, testEncryptionKey(t))
	in := writeTempFile(t, "in.bin", "payload")
	existing := writeTempFile(t, "out.bin", "old contents")

	a, stdout, stderr := newCryptApp(t, "y\n")
	if code := a.run([]string{"encrypt", "--in", in, "--out", existing}); code != exitOK {
		t.Fatalf("exit = %d, want %d (stderr: %s)", code, exitOK, stderr.String())
	}
	got, err := os.ReadFile(existing)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) == "old contents" {
		t.Error("the file was not overwritten after the operator confirmed")
	}
	if !strings.Contains(stdout.String(), "wrote:") {
		t.Errorf("stdout = %q, want a report of what was written", stdout.String())
	}
}

// TestCryptYesFlagSkipsThePrompt matters for the unattended path: the nightly
// job has no terminal to answer a prompt, so --yes has to be the whole
// consent mechanism.
func TestCryptYesFlagSkipsThePrompt(t *testing.T) {
	t.Setenv(ops.EncryptionKeyEnv, testEncryptionKey(t))
	in := writeTempFile(t, "in.bin", "payload")
	existing := writeTempFile(t, "out.bin", "old contents")

	// Empty stdin: if the command consults it at all, confirm() returns an
	// error and the command fails, which is exactly what --yes must prevent.
	a, _, stderr := newCryptApp(t, "")
	if code := a.run([]string{"encrypt", "--in", in, "--out", existing, "--yes"}); code != exitOK {
		t.Fatalf("exit = %d with --yes, want %d (stderr: %s)", code, exitOK, stderr.String())
	}
}

func TestCryptReportsAMissingInputFile(t *testing.T) {
	t.Setenv(ops.EncryptionKeyEnv, testEncryptionKey(t))
	missing := filepath.Join(t.TempDir(), "does-not-exist.bin")
	out := filepath.Join(t.TempDir(), "out.bin")

	a, _, stderr := newCryptApp(t, "")
	if code := a.run([]string{"encrypt", "--in", missing, "--out", out}); code != exitFail {
		t.Errorf("exit = %d for a missing --in, want %d", code, exitFail)
	}
	if !strings.Contains(stderr.String(), "opening") {
		t.Errorf("stderr = %q, want it to name the file it could not open", stderr.String())
	}
}

func TestCryptReportsAnUncreatableOutputFile(t *testing.T) {
	t.Setenv(ops.EncryptionKeyEnv, testEncryptionKey(t))
	in := writeTempFile(t, "in.bin", "payload")
	// A path whose parent directory does not exist.
	out := filepath.Join(t.TempDir(), "no-such-dir", "out.bin")

	a, _, stderr := newCryptApp(t, "")
	if code := a.run([]string{"encrypt", "--in", in, "--out", out}); code != exitFail {
		t.Errorf("exit = %d for an uncreatable --out, want %d", code, exitFail)
	}
	if !strings.Contains(stderr.String(), "creating") {
		t.Errorf("stderr = %q, want it to name the file it could not create", stderr.String())
	}
}

// TestDecryptRejectsCiphertextEncryptedUnderADifferentKey pins the failure an
// operator is most likely to hit for real: restoring a backup with the wrong
// key. It must fail loudly rather than emit garbage plaintext.
func TestDecryptRejectsCiphertextEncryptedUnderADifferentKey(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain.bin")
	if err := os.WriteFile(plain, []byte("the database"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cipher := filepath.Join(dir, "cipher.bin")

	t.Setenv(ops.EncryptionKeyEnv, testEncryptionKey(t))
	a, _, stderr := newCryptApp(t, "")
	if code := a.run([]string{"encrypt", "--in", plain, "--out", cipher}); code != exitOK {
		t.Fatalf("encrypt failed: %s", stderr.String())
	}

	// A different key entirely.
	t.Setenv(ops.EncryptionKeyEnv, testEncryptionKey(t))
	b, _, stderrB := newCryptApp(t, "")
	roundTripped := filepath.Join(dir, "roundtripped.bin")
	if code := b.run([]string{"decrypt", "--in", cipher, "--out", roundTripped}); code != exitFail {
		t.Errorf("decrypt with the wrong key exited %d, want %d", code, exitFail)
	}
	if stderrB.Len() == 0 {
		t.Error("decrypt with the wrong key reported nothing on stderr")
	}
}

func TestCryptJSONOutputReportsTheWrittenFile(t *testing.T) {
	t.Setenv(ops.EncryptionKeyEnv, testEncryptionKey(t))
	in := writeTempFile(t, "in.bin", "payload")
	out := filepath.Join(t.TempDir(), "out.bin")

	a, stdout, stderr := newCryptApp(t, "")
	a.JSON = true
	if code := a.run([]string{"encrypt", "--json", "--in", in, "--out", out}); code != exitOK {
		t.Fatalf("exit = %d (stderr: %s)", code, stderr.String())
	}
	for _, want := range []string{`"out"`, `"bytes"`} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("JSON output missing %s:\n%s", want, stdout.String())
		}
	}
}
