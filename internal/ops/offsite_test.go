package ops

import (
	"bytes"
	"crypto/rand"
	"io"
	"testing"
)

func randomKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, KeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		t.Fatalf("generating key: %v", err)
	}
	return key
}

func TestEncryptDecryptRoundTripsMultiMegabyteStream(t *testing.T) {
	key := randomKey(t)

	// Bigger than chunkSize so at least three chunks are exercised, plus a
	// partial final chunk.
	plaintext := make([]byte, 3*chunkSize+12345)
	if _, err := io.ReadFull(rand.Reader, plaintext); err != nil {
		t.Fatalf("generating plaintext: %v", err)
	}

	var ciphertext bytes.Buffer
	if err := Encrypt(bytes.NewReader(plaintext), &ciphertext, key); err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// Ciphertext must not equal the plaintext, and must not obviously
	// contain long runs of it (a weak sanity check that something happened).
	if bytes.Contains(ciphertext.Bytes(), plaintext[:4096]) {
		t.Fatal("ciphertext appears to contain a large run of raw plaintext")
	}

	var decrypted bytes.Buffer
	if err := Decrypt(bytes.NewReader(ciphertext.Bytes()), &decrypted, key); err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	if !bytes.Equal(decrypted.Bytes(), plaintext) {
		t.Fatalf("round trip mismatch: got %d bytes, want %d bytes", decrypted.Len(), len(plaintext))
	}
}

func TestEncryptDecryptEmptyStream(t *testing.T) {
	key := randomKey(t)
	var ciphertext, decrypted bytes.Buffer
	if err := Encrypt(bytes.NewReader(nil), &ciphertext, key); err != nil {
		t.Fatalf("Encrypt empty: %v", err)
	}
	if err := Decrypt(bytes.NewReader(ciphertext.Bytes()), &decrypted, key); err != nil {
		t.Fatalf("Decrypt empty: %v", err)
	}
	if decrypted.Len() != 0 {
		t.Fatalf("decrypted %d bytes from an empty stream", decrypted.Len())
	}
}

func TestDecryptFailsWithWrongKey(t *testing.T) {
	key := randomKey(t)
	wrongKey := randomKey(t)

	plaintext := []byte("the quick brown fox jumps over the lazy dog, repeated for good measure")

	var ciphertext bytes.Buffer
	if err := Encrypt(bytes.NewReader(plaintext), &ciphertext, key); err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	var decrypted bytes.Buffer
	err := Decrypt(bytes.NewReader(ciphertext.Bytes()), &decrypted, wrongKey)
	if err == nil {
		t.Fatal("Decrypt with the wrong key: want error, got nil")
	}
}

func TestDecryptFailsOnCorruptedCiphertext(t *testing.T) {
	key := randomKey(t)
	plaintext := bytes.Repeat([]byte("data"), 1000)

	var ciphertext bytes.Buffer
	if err := Encrypt(bytes.NewReader(plaintext), &ciphertext, key); err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	corrupted := ciphertext.Bytes()
	// Flip a byte well inside the first chunk's ciphertext body (past the
	// 16-byte header), which must fail the GCM tag check.
	corrupted[chunkHeaderSize+5] ^= 0xFF

	var decrypted bytes.Buffer
	if err := Decrypt(bytes.NewReader(corrupted), &decrypted, key); err == nil {
		t.Fatal("Decrypt of corrupted ciphertext: want error, got nil")
	}
}

func TestEncryptRejectsWrongKeyLength(t *testing.T) {
	if err := Encrypt(bytes.NewReader(nil), &bytes.Buffer{}, []byte("too-short")); err == nil {
		t.Fatal("Encrypt with a short key: want error, got nil")
	}
}
