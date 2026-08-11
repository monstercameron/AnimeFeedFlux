//go:build !devui

package auth

import "time"

// devLoginPrefill returns credentials to pre-populate the login form with.
//
// This is the DEFAULT build: it returns nothing, and the linker drops the
// whole path. That is why this is split across two files with opposing build
// tags rather than gated by a runtime flag — a credential behind an
// `if devMode` check is still a credential compiled into a 31 MB bundle that
// ships to a browser, and `scripts/check-wasm-secrets.sh` would be right to
// fail the build over it. Here the secret is not merely unreachable, it is
// absent from the binary.
//
// See devfill_on.go for the dev build and how to produce one.
//
//lint:ignore U1000 reached from login.go via newLoginFormWithDevPrefill, both js-tagged
func devLoginPrefill(time.Time) (password, totpCode string, ok bool) {
	return "", "", false
}

// devRefreshTOTP is the identity function in every non-devui build: there is
// no prefilled code to refresh, and no credential in the binary to compute
// one from. Whatever the operator typed is what gets submitted.
//
//lint:ignore U1000 called from the js-tagged login.go
func devRefreshTOTP(password, code string, _ time.Time) (string, string) {
	return password, code
}
