package i18n

import (
	"strings"
	"testing"

	gwci18n "github.com/monstercameron/GoWebComponents/v5/i18n"
)

// allDeclaredAuthKeys and allDeclaredCommonKeys are the Go constants this
// package hands out, collected once so catalogue-completeness tests below
// (D6-22/D6-23's per-package version — see catalog.go's doc comment on
// what gwci18n itself does NOT check) don't hand-maintain the list twice.
var allDeclaredAuthKeys = []string{
	KeyLoginTitle, KeyLoginPasswordStepLabel, KeyLoginPasswordLabel, KeyLoginContinue,
	KeyLoginTOTPStepLabel, KeyLoginTOTPLabel, KeyLoginTOTPHint, KeyLoginSubmit, KeyLoginRecoverLink,
	KeyRecoverTitle, KeyRecoverIntro, KeyRecoverCodeStepLabel, KeyRecoverCodeLabel, KeyRecoverCodeHint,
	KeyRecoverCodeSubmit, KeyRecoverRemainingCodes, KeyRecoverLowCodesWarning, KeyRecoverChooseActionHeading,
	KeyRecoverChooseActionIntro, KeyRecoverChoosePasswordReset, KeyRecoverChooseReenrollTOTP,
	KeyRecoverNewPasswordLabel, KeyRecoverConfirmPasswordLabel, KeyRecoverPasswordMismatch,
	KeyRecoverPasswordTooShort, KeyRecoverPasswordTooLong, KeyRecoverResetSubmit, KeyRecoverReenrollHeading,
	KeyRecoverReenrollIntro, KeyRecoverReenrollSubmit, KeyRecoverReenrollProvisioned, KeyRecoverDoneHeading,
	KeyRecoverDoneBody, KeyRecoverDoneGoToLogin, KeyRecoverBreakGlassHeading, KeyRecoverBreakGlassBody,
	KeyRecoverBreakGlassCommandNote,
}

var allDeclaredCommonKeys = []string{
	KeyCommonGenericAuthError, KeyCommonBackoffNotice, KeyCommonBack, KeyCommonSubmitting,
	KeyActionSave, KeyActionCancel, KeyActionRetry, KeyActionClose, KeyActionDismiss, KeyActionConfirmDestroy,
	KeyStateLoading, KeyStateEmpty, KeyStateError, KeyStateDisabled, KeyStateDisconnected,
	KeyConfirmTypePhrase, KeyConfirmTypeLabel, KeyKebabActionsFor,
}

// allDeclaredShellKeys mirrors allDeclaredCommonKeys for keys_shell.go
// (D6-09).
var allDeclaredShellKeys = []string{
	KeyShellBannerDisconnected, KeyShellBannerReconnectingIn,
	KeyShellExpiryTitle, KeyShellExpiryBody, KeyShellExpiryLogin,
	KeyShellGuardRedirectNotice, KeyShellNotImplemented,
}

// TestEveryDeclaredKeyResolves is the D6-22 half ("every key referenced by
// code exists in the catalogue... catches a typo'd key, which otherwise
// ships as a visible raw key in the UI"): every Go constant this package
// exports must Translate to something other than gwci18n's own
// "namespace.key" missing-key fallback text.
func TestEveryDeclaredKeyResolves(t *testing.T) {
	b := NewBundle()
	check := func(ns string, keys []string) {
		t.Helper()
		for _, k := range keys {
			got := b.Translate(DefaultLocale, ns, k, gwci18n.Arguments{"count": 1, "min": 15, "max": 128, "seconds": 1}, DefaultLocale)
			if got == ns+"."+k {
				t.Errorf("key %s.%s did not resolve (fell back to raw key)", ns, k)
			}
			if got == "" {
				t.Errorf("key %s.%s resolved to an empty string (D6-07: never an empty string)", ns, k)
			}
		}
	}
	check(NSAuth, allDeclaredAuthKeys)
	check(NSCommon, allDeclaredCommonKeys)
	check(NSShell, allDeclaredShellKeys)
}

// TestNoOrphanCatalogueEntries is the D6-23 half ("every catalogue key is
// referenced by code — dead keys are how a catalogue grows to twice the
// size of the interface it describes"): every key actually stored in
// enCatalog's auth/common/shell namespaces must have a matching Go
// constant above. generate/history/settings are deliberately excluded
// here: their catalogue keys ARE the literal strings web/pages/generate,
// web/pages/history and web/pages/settings already hardcode (those
// packages cannot import this one — see keys_generate.go's doc comment),
// so there is no Go constant in THIS package to check against; orphan
// detection for those three namespaces is TestGenerateHistorySettingsResolve below instead,
// which iterates every entry actually present in generateMessages/historyMessages/
// settingsMessages and asserts each resolves to non-blank, non-fallback text — the same
// per-entry check TestEveryDeclaredKeyResolves does for the Go-constant-backed namespaces above,
// just driven from the catalogue's own keys instead of a hand-maintained Go slice, since these
// three namespaces have no such slice to drive it from (see this comment's paragraph above).
func TestNoOrphanCatalogueEntries(t *testing.T) {
	declared := map[string]bool{}
	for _, k := range allDeclaredAuthKeys {
		declared[NSAuth+"."+k] = true
	}
	for _, k := range allDeclaredCommonKeys {
		declared[NSCommon+"."+k] = true
	}
	for _, k := range allDeclaredShellKeys {
		declared[NSShell+"."+k] = true
	}
	for ns, msgs := range map[string]gwci18n.NamespaceCatalog{NSAuth: authMessages, NSCommon: commonMessages, NSShell: shellMessages} {
		for key := range msgs {
			if !declared[ns+"."+key] {
				t.Errorf("catalogue has key %s.%s with no matching Go constant in this package (dead key)", ns, key)
			}
		}
	}
}

// TestGenerateHistorySettingsResolve is the D6-11/D6-12/D6-13 sanity check
// for the three namespaces this package populates from OTHER packages'
// literal key strings rather than its own Go constants (see
// TestNoOrphanCatalogueEntries's doc comment): every entry must actually
// Translate to non-empty text, using the SAME namespace + key it is stored
// under (these namespaces' keys already embed their own namespace prefix,
// e.g. "generate.editor.slug" inside NSGenerate — see keys_generate.go).
func TestGenerateHistorySettingsResolve(t *testing.T) {
	b := NewBundle()
	args := gwci18n.Arguments{
		"arg1": 3, "arg2": 3, "arg3": 3, "word": "DELETE", "count": 3,
	}
	for ns, msgs := range map[string]gwci18n.NamespaceCatalog{
		NSGenerate: generateMessages, NSHistory: historyMessages, NSSettings: settingsMessages,
	} {
		for key := range msgs {
			got := b.Translate(DefaultLocale, ns, key, args, DefaultLocale)
			if got == "" {
				t.Errorf("%s.%s resolved to an empty string (D6-07: never an empty string)", ns, key)
			}
			if got == ns+"."+key {
				t.Errorf("%s.%s did not resolve (fell back to raw key) — Message entry is malformed (no Text/Plural/Select/Default)", ns, key)
			}
		}
	}
}

// TestMissingKeyRendersKeyAndLogs is D6-07: a key that was never
// registered must render as "namespace.key" — never an empty string — and
// must invoke OnMissing (verified indirectly: logMissingKey always returns
// that exact "namespace.key" shape, so a matching return value here is
// proof it ran, not just proof Translate's own default fallback ran).
func TestMissingKeyRendersKeyAndLogs(t *testing.T) {
	b := NewBundle()
	got := b.Translate(DefaultLocale, NSAuth, "login.thisKeyDoesNotExist", nil, DefaultLocale)
	want := "auth.login.thisKeyDoesNotExist"
	if got != want {
		t.Fatalf("missing key: got %q, want %q", got, want)
	}
	if got == "" {
		t.Fatal("missing key rendered as an empty string")
	}
}

// TestGenericAuthErrorIsSingular is D6-18's structural check: there must
// be exactly one auth-failure-message key in the whole catalogue, not one
// per cause (a "wrongPassword" or "unknownAccount" key sitting next to it
// would reintroduce the oracle D1-02 exists to close, even if unused by
// current page code — its mere presence invites a future call site to
// pick the "more specific" one).
func TestGenericAuthErrorIsSingular(t *testing.T) {
	suspect := []string{"wrongpassword", "wrongcode", "wrongtotp", "unknownaccount", "nosuchuser", "invalidcode", "invalidpassword", "codealreadyused", "unrecognizedcode"}
	for ns, msgs := range map[string]gwci18n.NamespaceCatalog{NSAuth: authMessages, NSCommon: commonMessages} {
		for key := range msgs {
			lower := strings.ToLower(key)
			for _, s := range suspect {
				if strings.Contains(lower, s) {
					t.Errorf("catalogue key %s.%s looks like a cause-specific auth-failure message; D1-02/D6-18 require exactly one generic one (%s.%s)", ns, key, NSCommon, KeyCommonGenericAuthError)
				}
			}
		}
	}
	if _, ok := commonMessages[KeyCommonGenericAuthError]; !ok {
		t.Fatal("the single generic auth-failure key is missing from commonMessages")
	}
}

// TestPluralBothCategoriesPresent guards D6-06 ("plurals through the
// library's plural rules, not `if n == 1`") for the two plural messages
// this package ships: both "one" and "other" must resolve, and must
// actually differ in wording (otherwise a future edit could collapse them
// back into disguised string concatenation).
func TestPluralBothCategoriesPresent(t *testing.T) {
	b := NewBundle()
	for _, tc := range []struct {
		ns, key string
	}{
		{NSCommon, KeyCommonBackoffNotice},
		{NSAuth, KeyRecoverRemainingCodes},
	} {
		one := b.Translate(DefaultLocale, tc.ns, tc.key, gwci18n.Arguments{"count": 1}, DefaultLocale)
		many := b.Translate(DefaultLocale, tc.ns, tc.key, gwci18n.Arguments{"count": 5}, DefaultLocale)
		if one == "" || many == "" {
			t.Fatalf("%s.%s: plural category resolved to empty string (one=%q other=%q)", tc.ns, tc.key, one, many)
		}
		if !strings.Contains(one, "1") {
			t.Errorf("%s.%s: count=1 form %q does not interpolate {count}", tc.ns, tc.key, one)
		}
		if !strings.Contains(many, "5") {
			t.Errorf("%s.%s: count=5 form %q does not interpolate {count}", tc.ns, tc.key, many)
		}
	}
}

// TestOtherPluralsBothCategoriesPresent extends TestPluralBothCategoriesPresent
// to this wave's own plural messages, each of which uses a differently
// named PluralArg (shell's countdown is "seconds"; the positional-arg
// namespaces use "arg1" per keys_common.go's convention) — a plain
// count/one/other check would silently pass on a mistyped PluralArg by
// interpolating nothing, so each case supplies its own argument name.
func TestOtherPluralsBothCategoriesPresent(t *testing.T) {
	b := NewBundle()
	for _, tc := range []struct {
		ns, key, argName string
	}{
		{NSShell, KeyShellBannerReconnectingIn, "seconds"},
		{NSSettings, "settings.security.recoveryCodes.remaining", "arg1"},
		{NSSettings, "settings.data.stats.feedCount", "arg1"},
		{NSSettings, "settings.data.stats.itemCount", "arg1"},
	} {
		one := b.Translate(DefaultLocale, tc.ns, tc.key, gwci18n.Arguments{tc.argName: 1}, DefaultLocale)
		many := b.Translate(DefaultLocale, tc.ns, tc.key, gwci18n.Arguments{tc.argName: 5}, DefaultLocale)
		if one == "" || many == "" {
			t.Fatalf("%s.%s: plural category resolved to empty string (one=%q other=%q)", tc.ns, tc.key, one, many)
		}
		if one == many {
			t.Errorf("%s.%s: one/other forms are identical (%q) — not routed through real plural rules (D6-06)", tc.ns, tc.key, one)
		}
		if !strings.Contains(one, "1") {
			t.Errorf("%s.%s: count=1 form %q does not interpolate {%s}", tc.ns, tc.key, one, tc.argName)
		}
		if !strings.Contains(many, "5") {
			t.Errorf("%s.%s: count=5 form %q does not interpolate {%s}", tc.ns, tc.key, many, tc.argName)
		}
	}
}

// TestFeedContentNamespacesStayEmpty documents D6-17's boundary at the
// catalogue level: shell/generate/history/settings exist (D6-04) but carry
// nothing yet, so nothing here can accidentally be feed-authored content
// routed through translation. Also guards against a future edit silently
// deleting one of the required namespace keys from enCatalog.
func TestNamespacesAllExist(t *testing.T) {
	for _, ns := range []string{NSAuth, NSCommon, NSShell, NSGenerate, NSHistory, NSSettings} {
		if _, ok := enCatalog[ns]; !ok {
			t.Errorf("namespace %q missing from enCatalog", ns)
		}
	}
}
