// Off-box shipping for backups (PLAN.md §15). The lesson this codifies is not
// hypothetical: fourteen verified ArticleFlux backups, their source database,
// and the key that decrypts them all lived on one DigitalOcean volume, so the
// single event they insured against — losing the volume — took all three at
// once. A backup on the same disk as the database defends against `rm`, not
// against loss; it has to leave the box, and it has to leave encrypted since
// wherever it lands is a second place secrets can leak from.
package ops

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
)

// chunkSize bounds how much plaintext is held in memory at once, so
// Encrypt/Decrypt can process a multi-gigabyte database without loading it
// whole. AES-GCM authenticates the whole input it is given in one call, so
// "chunked" here means many independent sealed chunks, each its own nonce
// and authentication tag, framed on the wire — not a single GCM stream.
const chunkSize = 1 << 20 // 1 MiB

// KeySize is the required AES-256 key length in bytes.
const KeySize = 32

// chunkHeaderSize is nonce (12 bytes, AES-GCM's standard size) plus a
// big-endian uint32 ciphertext length.
const chunkHeaderSize = 12 + 4

// Encrypt reads in to completion, sealing it as a sequence of independent
// AES-256-GCM chunks written to out: each chunk is a fresh random 96-bit
// nonce, a 4-byte big-endian ciphertext length, then the ciphertext
// (including its 16-byte GCM tag). A random nonce per chunk, rather than a
// counter, means Encrypt has no state to get wrong across process restarts —
// nonce reuse under the same key is the one mistake that breaks GCM's
// guarantees outright.
func Encrypt(in io.Reader, out io.Writer, key []byte) error {
	gcm, err := newGCM(key)
	if err != nil {
		return err
	}

	buf := make([]byte, chunkSize)
	for {
		n, rerr := io.ReadFull(in, buf)
		if n > 0 {
			if err := encryptChunk(gcm, out, buf[:n]); err != nil {
				return err
			}
		}
		if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
			return nil
		}
		if rerr != nil {
			return fmt.Errorf("ops: reading plaintext: %w", rerr)
		}
	}
}

func encryptChunk(gcm cipher.AEAD, out io.Writer, plaintext []byte) error {
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("ops: generating nonce: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	var header [chunkHeaderSize]byte
	copy(header[:len(nonce)], nonce)
	binary.BigEndian.PutUint32(header[len(nonce):], uint32(len(ciphertext)))

	if _, err := out.Write(header[:]); err != nil {
		return fmt.Errorf("ops: writing chunk header: %w", err)
	}
	if _, err := out.Write(ciphertext); err != nil {
		return fmt.Errorf("ops: writing chunk body: %w", err)
	}
	return nil
}

// Decrypt reverses Encrypt, streaming: it reads and authenticates one chunk
// at a time rather than buffering the whole ciphertext, and stops (with no
// error) at a clean end of stream. A wrong key, or any corrupted chunk,
// fails GCM's tag check on that chunk and Decrypt returns an error instead
// of emitting unauthenticated plaintext.
func Decrypt(in io.Reader, out io.Writer, key []byte) error {
	gcm, err := newGCM(key)
	if err != nil {
		return err
	}
	nonceSize := gcm.NonceSize()

	header := make([]byte, nonceSize+4)
	for {
		_, err := io.ReadFull(in, header)
		if err == io.EOF {
			return nil // clean end of stream, between chunks
		}
		if err != nil {
			return fmt.Errorf("ops: reading chunk header: %w", err)
		}

		nonce := append([]byte(nil), header[:nonceSize]...)
		ctLen := binary.BigEndian.Uint32(header[nonceSize:])

		ciphertext := make([]byte, ctLen)
		if _, err := io.ReadFull(in, ciphertext); err != nil {
			return fmt.Errorf("ops: reading chunk body: %w", err)
		}

		plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
		if err != nil {
			return fmt.Errorf("ops: decrypting chunk (wrong key or corrupted data): %w", err)
		}
		if _, err := out.Write(plaintext); err != nil {
			return fmt.Errorf("ops: writing plaintext: %w", err)
		}
	}
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("ops: key must be %d bytes for AES-256, got %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("ops: aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("ops: gcm: %w", err)
	}
	return gcm, nil
}

// Decrypt returns no distinct sentinel for "wrong key" versus "corrupted
// ciphertext" — GCM cannot tell them apart, and pretending otherwise would
// be a lie. Both fail gcm.Open's authentication check identically, which is
// the point of authenticated encryption.
