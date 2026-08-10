// Package auth implements the authentication primitives described in PLAN.md §4:
// a single-admin, no-authorization-model system where authentication is the entire
// defense. This file covers TOTP (RFC 6238) enrollment/validation and at-rest
// encryption of the TOTP secret.
package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/hotp"
	"github.com/pquerna/otp/totp"
)

// totpPeriod and totpSkew match the values PLAN.md §4 commits to: 30s steps,
// ±1 step drift window. They are also the values baked into the otpauth URL
// (via totp.Generate's defaults), so any authenticator app scanning the QR
// code computes codes on the same cadence we validate against.
const (
	totpPeriod uint = 30
	totpSkew   uint = 1
)

// Enroll generates a new random TOTP secret and the otpauth:// URL an
// authenticator app (or QR code) uses to onboard it. The secret is returned
// in base32 form as pquerna/otp encodes it; callers MUST encrypt it with
// EncryptSecret before persisting, per PLAN.md §4 ("TOTP secret encrypted at
// rest ... a stolen DB file alone is not a second factor").
func Enroll(accountName, issuer string) (secret string, otpauthURL string, err error) {
	if accountName == "" {
		return "", "", errors.New("auth: accountName is required")
	}
	if issuer == "" {
		return "", "", errors.New("auth: issuer is required")
	}

	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: accountName,
		Period:      totpPeriod,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return "", "", fmt.Errorf("auth: generate totp key: %w", err)
	}

	return key.Secret(), key.String(), nil
}

// ValidateCode checks a submitted TOTP code against secret at time now,
// allowing ±1 step (totpSkew) of clock/user drift as required by PLAN.md §4.
// On success it returns the step (the 30s counter, i.e. unixSeconds/30) the
// code actually belongs to — NOT necessarily the step containing now, since a
// code from the adjacent step is accepted. Callers must record (account,
// step) pairs and reject a step already seen, per §4's replay-prevention
// requirement ("used steps are recorded and rejected on replay ... enforced
// by a primary key"). Returning the concrete step, rather than just true/false,
// is what gives that primary key something to key on.
func ValidateCode(secret, code string, now time.Time) (step int64, ok bool) {
	if code == "" {
		return 0, false
	}

	center := now.Unix() / int64(totpPeriod)

	// Check the current step first, then the ±skew neighbors. Order doesn't
	// affect correctness (each candidate step is checked independently), but
	// checking "now" first is the common case.
	candidates := make([]int64, 0, 1+2*int(totpSkew))
	candidates = append(candidates, center)
	for i := int64(1); i <= int64(totpSkew); i++ {
		candidates = append(candidates, center+i, center-i)
	}

	for _, c := range candidates {
		if c < 0 {
			continue
		}
		valid, err := hotp.ValidateCustom(code, uint64(c), secret, hotp.ValidateOpts{
			Digits:    otp.DigitsSix,
			Algorithm: otp.AlgorithmSHA1,
		})
		if err != nil {
			// Malformed code (wrong length) or invalid secret shape: no
			// candidate step can validate it, so stop rather than looping.
			return 0, false
		}
		if valid {
			return c, true
		}
	}

	return 0, false
}

// deriveKey turns the env-supplied AFF_SECRET_KEY into a 32-byte AES-256 key.
// PLAN.md §4 supplies AFF_SECRET_KEY as an opaque secret of operator-chosen
// length, not necessarily 32 bytes, so a fixed-size key is derived from it
// with SHA-256 whenever it isn't already exactly 32 bytes.
func deriveKey(key []byte) []byte {
	if len(key) == 32 {
		return key
	}
	sum := sha256.Sum256(key)
	return sum[:]
}

// EncryptSecret encrypts a TOTP secret with AES-256-GCM using a key derived
// from key (see deriveKey). A fresh random nonce is generated per call and
// prefixed to the ciphertext, so two encryptions of the same secret produce
// different outputs — required because AES-GCM nonce reuse under the same key
// breaks confidentiality and authenticity outright.
func EncryptSecret(secret string, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(deriveKey(key))
	if err != nil {
		return nil, fmt.Errorf("auth: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("auth: new gcm: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("auth: read nonce: %w", err)
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(secret), nil)
	return ciphertext, nil
}

// DecryptSecret reverses EncryptSecret. It fails closed: a wrong key, a
// truncated blob, or a tampered ciphertext all return an error rather than
// partial or garbage plaintext, since GCM authenticates the ciphertext.
func DecryptSecret(data []byte, key []byte) (string, error) {
	block, err := aes.NewCipher(deriveKey(key))
	if err != nil {
		return "", fmt.Errorf("auth: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("auth: new gcm: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", errors.New("auth: ciphertext too short")
	}
	nonce, ciphertext := data[:nonceSize], data[nonceSize:]

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("auth: decrypt: %w", err)
	}
	return string(plaintext), nil
}
