//go:build devui

package auth

import (
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // TOTP (RFC 6238) is defined over HMAC-SHA1
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"strconv"
	"strings"
	"time"
)

// Dev-only login prefill. Built ONLY with `-tags devui`:
//
//	DEV=1 sh scripts/build-web.sh
//
// Never in a release build — see devfill_off.go for why this is a build tag
// rather than a runtime flag.
//
// The values arrive via -ldflags at build time, not from source, so a
// checked-out tree contains no credential even in the dev variant.
// web/build.sh supplies them from the environment when DEV=1.
// Base64 because -ldflags word-splits on spaces and a passphrase has them by
// design. web/build.sh encodes; this decodes once at init.
var (
	devPasswordB64   string
	devTOTPSecretB64 string

	devPassword   string
	devTOTPSecret string
)

func init() {
	if b, err := base64.StdEncoding.DecodeString(devPasswordB64); err == nil {
		devPassword = string(b)
	}
	if b, err := base64.StdEncoding.DecodeString(devTOTPSecretB64); err == nil {
		devTOTPSecret = string(b)
	}
}

// devLoginPrefill reports dev credentials plus a CURRENTLY VALID TOTP code.
//
// The code is computed here rather than baked in because a TOTP rotates every
// 30 seconds — a prefilled constant would be stale before the page finished
// loading, which is worse than an empty field: it looks like a working
// one-click login that rejects you.
//
// Returns ok=false when the ldflags were not supplied, so a `-tags devui`
// build without credentials behaves exactly like a release build rather than
// prefilling an empty password and appearing broken.
func devLoginPrefill(now time.Time) (password, totpCode string, ok bool) {
	if devPassword == "" {
		return "", "", false
	}
	code, err := totpAt(devTOTPSecret, now)
	if err != nil {
		// A malformed secret is a dev-setup mistake. Still prefill the
		// password so the failure is visibly "the code is empty" rather than
		// silently nothing happening at all.
		return devPassword, "", true
	}
	return devPassword, code, true
}

// totpAt implements RFC 6238 for the parameters `aff admin init` enrolls:
// SHA-1, 6 digits, 30-second period.
func totpAt(secret string, now time.Time) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).
		DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", err
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(now.Unix()/30))

	mac := hmac.New(sha1.New, key)
	mac.Write(counter[:])
	sum := mac.Sum(nil)

	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return leftPad(strconv.FormatUint(uint64(v%1_000_000), 10), 6), nil
}

func leftPad(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return strings.Repeat("0", n-len(s)) + s
}

// devRefreshTOTP recomputes an untouched prefilled TOTP code against `now`,
// so a dev-build login page that has been open longer than one 30-second
// step still submits a valid code.
//
// It replaces the code ONLY when the form still holds exactly what
// devLoginPrefill put there for some step at or near `now` — i.e. the
// operator has not typed over it. Anything hand-entered is returned
// untouched, so this cannot silently overwrite a real code someone is
// testing with. The password is passed through unchanged; it does not
// expire.
//
//lint:ignore U1000 called from the js-tagged login.go
func devRefreshTOTP(password, code string, now time.Time) (string, string) {
	if devPassword == "" || devTOTPSecret == "" {
		return password, code
	}
	// Untouched means "equal to the code this build would have prefilled at
	// some point in the recent past". The current step and the two before it
	// cover a page that has been sitting open for up to ~90 seconds, which
	// is the realistic window between a mount and a click; older than that
	// and the value is more likely typed than prefilled, so it is left
	// alone.
	for back := 0; back <= 2; back++ {
		was, err := totpAt(devTOTPSecret, now.Add(-time.Duration(back)*30*time.Second))
		if err != nil {
			return password, code
		}
		if code == was {
			fresh, err := totpAt(devTOTPSecret, now)
			if err != nil {
				return password, code
			}
			return password, fresh
		}
	}
	return password, code
}
