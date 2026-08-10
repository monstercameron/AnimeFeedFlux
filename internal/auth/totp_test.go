package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

func TestEnrollProducesValidURLAndSecret(t *testing.T) {
	secret, url, err := Enroll("admin@example.com", "AnimeFeedFlux")
	if err != nil {
		t.Fatalf("Enroll returned error: %v", err)
	}
	if secret == "" {
		t.Fatal("Enroll returned empty secret")
	}
	if !strings.HasPrefix(url, "otpauth://totp/") {
		t.Fatalf("otpauth URL has unexpected form: %q", url)
	}
	if !strings.Contains(url, "AnimeFeedFlux") {
		t.Fatalf("otpauth URL missing issuer: %q", url)
	}

	code, err := generateCodeAt(secret, time.Now())
	if err != nil {
		t.Fatalf("generateCodeAt: %v", err)
	}

	if _, ok := ValidateCode(secret, code, time.Now()); !ok {
		t.Fatal("ValidateCode rejected a code generated for the same instant")
	}
}

func TestEnrollRequiresAccountAndIssuer(t *testing.T) {
	if _, _, err := Enroll("", "Issuer"); err == nil {
		t.Fatal("expected error for empty accountName")
	}
	if _, _, err := Enroll("account", ""); err == nil {
		t.Fatal("expected error for empty issuer")
	}
}

func TestValidateCodeDriftWindow(t *testing.T) {
	secret, _, err := Enroll("admin@example.com", "AnimeFeedFlux")
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}

	now := time.Unix(1_700_000_000, 0).UTC()

	prevStepTime := now.Add(-time.Duration(totpPeriod) * time.Second)
	nextStepTime := now.Add(time.Duration(totpPeriod) * time.Second)
	twoAwayTime := now.Add(2 * time.Duration(totpPeriod) * time.Second)

	prevCode, err := generateCodeAt(secret, prevStepTime)
	if err != nil {
		t.Fatalf("generateCodeAt prev: %v", err)
	}
	nextCode, err := generateCodeAt(secret, nextStepTime)
	if err != nil {
		t.Fatalf("generateCodeAt next: %v", err)
	}
	twoAwayCode, err := generateCodeAt(secret, twoAwayTime)
	if err != nil {
		t.Fatalf("generateCodeAt two-away: %v", err)
	}

	if _, ok := ValidateCode(secret, prevCode, now); !ok {
		t.Error("code from previous step should validate within the drift window")
	}
	if _, ok := ValidateCode(secret, nextCode, now); !ok {
		t.Error("code from next step should validate within the drift window")
	}
	if _, ok := ValidateCode(secret, twoAwayCode, now); ok {
		t.Error("code two steps away should NOT validate")
	}
}

func TestValidateCodeReturnsDistinctSteps(t *testing.T) {
	secret, _, err := Enroll("admin@example.com", "AnimeFeedFlux")
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}

	base := time.Unix(1_700_000_000, 0).UTC()
	t1 := base
	t2 := base.Add(time.Duration(totpPeriod) * time.Second)

	code1, err := generateCodeAt(secret, t1)
	if err != nil {
		t.Fatalf("generateCodeAt t1: %v", err)
	}
	code2, err := generateCodeAt(secret, t2)
	if err != nil {
		t.Fatalf("generateCodeAt t2: %v", err)
	}

	step1, ok1 := ValidateCode(secret, code1, t1)
	step2, ok2 := ValidateCode(secret, code2, t2)

	if !ok1 || !ok2 {
		t.Fatalf("expected both codes to validate, got ok1=%v ok2=%v", ok1, ok2)
	}
	if step1 == step2 {
		t.Fatalf("expected distinct steps for codes in different windows, both were %d", step1)
	}
	if step2 != step1+1 {
		t.Fatalf("expected step2 == step1+1, got step1=%d step2=%d", step1, step2)
	}
}

func TestValidateCodeRejectsGarbage(t *testing.T) {
	secret, _, err := Enroll("admin@example.com", "AnimeFeedFlux")
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if _, ok := ValidateCode(secret, "", time.Now()); ok {
		t.Error("empty code should not validate")
	}
	if _, ok := ValidateCode(secret, "not-a-code", time.Now()); ok {
		t.Error("malformed code should not validate")
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := []byte("this is a passphrase, not 32B")
	secret := "JBSWY3DPEHPK3PXP"

	ciphertext, err := EncryptSecret(secret, key)
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}

	got, err := DecryptSecret(ciphertext, key)
	if err != nil {
		t.Fatalf("DecryptSecret: %v", err)
	}
	if got != secret {
		t.Fatalf("round trip mismatch: got %q want %q", got, secret)
	}
}

func TestDecryptWithWrongKeyFails(t *testing.T) {
	key := []byte("correct-key")
	wrongKey := []byte("wrong-key")
	secret := "JBSWY3DPEHPK3PXP"

	ciphertext, err := EncryptSecret(secret, key)
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}

	if _, err := DecryptSecret(ciphertext, wrongKey); err == nil {
		t.Fatal("expected decryption with wrong key to fail")
	}
}

func TestEncryptSecretNonceIsRandom(t *testing.T) {
	key := []byte("some-secret-key-value")
	secret := "JBSWY3DPEHPK3PXP"

	c1, err := EncryptSecret(secret, key)
	if err != nil {
		t.Fatalf("EncryptSecret 1: %v", err)
	}
	c2, err := EncryptSecret(secret, key)
	if err != nil {
		t.Fatalf("EncryptSecret 2: %v", err)
	}

	if string(c1) == string(c2) {
		t.Fatal("two encryptions of the same secret produced identical ciphertext; nonce not random")
	}
}

func TestEncryptSecretWith32ByteKey(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	secret := "JBSWY3DPEHPK3PXP"

	ciphertext, err := EncryptSecret(secret, key)
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}
	got, err := DecryptSecret(ciphertext, key)
	if err != nil {
		t.Fatalf("DecryptSecret: %v", err)
	}
	if got != secret {
		t.Fatalf("round trip mismatch with 32-byte key: got %q want %q", got, secret)
	}
}

// generateCodeAt is a small test helper producing the TOTP code for secret at
// time t, using the same period/digits/algorithm as Enroll/ValidateCode.
func generateCodeAt(secret string, t time.Time) (string, error) {
	return totp.GenerateCodeCustom(secret, t, totp.ValidateOpts{
		Period:    totpPeriod,
		Skew:      totpSkew,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
}
