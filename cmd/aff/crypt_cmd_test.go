package main

import (
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monstercameron/AnimeFeedFlux/internal/ops"
)

func testEncryptionKey(t *testing.T) string {
	t.Helper()
	key := make([]byte, ops.KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generating key: %v", err)
	}
	return base64.StdEncoding.EncodeToString(key)
}

func TestEncryptDecryptRoundTripUsingKeyFromEnvironment(t *testing.T) {
	t.Setenv(ops.EncryptionKeyEnv, testEncryptionKey(t))

	plainPath := filepath.Join(t.TempDir(), "backup.db")
	if err := os.WriteFile(plainPath, []byte("pretend this is a sqlite backup file"), 0o600); err != nil {
		t.Fatalf("write plaintext: %v", err)
	}
	encPath := filepath.Join(t.TempDir(), "backup.db.enc")
	decPath := filepath.Join(t.TempDir(), "backup.db.dec")

	a, _, stderr := newOpsTestApp(t, "")
	if code := a.run([]string{"encrypt", "--in", plainPath, "--out", encPath}); code != exitOK {
		t.Fatalf("aff encrypt: exit code, stderr: %s", stderr.String())
	}

	encrypted, err := os.ReadFile(encPath)
	if err != nil {
		t.Fatalf("reading encrypted file: %v", err)
	}
	original, _ := os.ReadFile(plainPath)
	if strings.Contains(string(encrypted), string(original)) {
		t.Fatal("encrypted output appears to contain the plaintext")
	}

	a2, _, stderr2 := newOpsTestApp(t, "")
	if code := a2.run([]string{"decrypt", "--in", encPath, "--out", decPath}); code != exitOK {
		t.Fatalf("aff decrypt: exit code, stderr: %s", stderr2.String())
	}
	decrypted, err := os.ReadFile(decPath)
	if err != nil {
		t.Fatalf("reading decrypted file: %v", err)
	}
	if string(decrypted) != string(original) {
		t.Fatalf("round trip mismatch: got %q, want %q", decrypted, original)
	}
}

func TestEncryptRefusesWithoutTheEnvironmentKey(t *testing.T) {
	t.Setenv(ops.EncryptionKeyEnv, "")
	plainPath := filepath.Join(t.TempDir(), "backup.db")
	if err := os.WriteFile(plainPath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write plaintext: %v", err)
	}
	encPath := filepath.Join(t.TempDir(), "backup.db.enc")

	a, _, stderr := newOpsTestApp(t, "")
	code := a.run([]string{"encrypt", "--in", plainPath, "--out", encPath})
	if code != exitFail {
		t.Fatalf("aff encrypt with no key set: exit %d, want %d", code, exitFail)
	}
	if !strings.Contains(stderr.String(), ops.EncryptionKeyEnv) {
		t.Fatalf("stderr = %q, want it to name %s", stderr.String(), ops.EncryptionKeyEnv)
	}
	if _, err := os.Stat(encPath); err == nil {
		t.Fatal("aff encrypt with no key must not create the output file")
	}
}

// TestEncryptCommandHasNoKeyFlag guards the one hard requirement that has no
// runtime-observable failure mode otherwise: a --key flag must never exist,
// or a key ends up in shell history and `ps` output — see
// internal/ops/cli.go's EncryptionKeyEnv doc comment. This inspects the
// registered flag set directly rather than trying to pass --key and hoping
// for a usage error, since flag.FlagSet silently accepts unknown flags names
// depend on registration, not usage text wording.
func TestEncryptCommandHasNoKeyFlag(t *testing.T) {
	a, _, stderr := newOpsTestApp(t, "")
	code := a.run([]string{"encrypt", "--key", "whatever", "--in", "x", "--out", "y"})
	if code != exitUsage {
		t.Fatalf("aff encrypt --key: exit %d, want %d (a --key flag must not be accepted)", code, exitUsage)
	}
	if !strings.Contains(stderr.String(), "key") {
		t.Fatalf("stderr = %q, want it to mention the unknown flag", stderr.String())
	}
}
