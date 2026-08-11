package i18n

import (
	"strings"
	"testing"

	gwci18n "github.com/monstercameron/GoWebComponents/v5/i18n"
)

// TestPseudolocaleEnvTogglesNewBundle is D6-24's end-to-end proof: setting
// AFI18N_PSEUDOLOCALE changes what NewBundle() actually renders, not just
// what PseudoCatalog() returns in isolation — the same env var a build or
// dev-server run would set.
func TestPseudolocaleEnvTogglesNewBundle(t *testing.T) {
	if PseudolocaleEnabled() {
		t.Fatal("pseudolocale should be disabled by default")
	}
	plain := NewBundle().Translate(DefaultLocale, NSCommon, KeyActionSave, nil, DefaultLocale)
	if plain != "Save" {
		t.Fatalf("plain bundle: got %q, want %q", plain, "Save")
	}

	t.Setenv(pseudolocaleEnv, "1")
	if !PseudolocaleEnabled() {
		t.Fatal("PseudolocaleEnabled() should be true once AFI18N_PSEUDOLOCALE=1 is set")
	}
	pseudo := NewBundle().Translate(DefaultLocale, NSCommon, KeyActionSave, nil, DefaultLocale)
	if pseudo == plain {
		t.Fatalf("pseudolocale bundle rendered the same text as the plain one (%q)", pseudo)
	}
	if !strings.HasPrefix(pseudo, "[") || !strings.HasSuffix(pseudo, "]") {
		t.Errorf("pseudolocalized text %q is not bracketed", pseudo)
	}
	if len(pseudo) <= len(plain) {
		t.Errorf("pseudolocalized text %q is not longer than plain text %q", pseudo, plain)
	}
}

// TestPseudolocaleFalsyValuesStayOff guards the truthy() helper's "off"
// spellings against a CI script that sets AFI18N_PSEUDOLOCALE=0/false to
// explicitly disable it (rather than leaving it unset) — a naive
// os.Getenv-non-empty check would treat "0" as enabled and silently flip
// every build pseudolocalized.
func TestPseudolocaleFalsyValuesStayOff(t *testing.T) {
	for _, v := range []string{"", "0", "false", "False", "off", "no"} {
		t.Setenv(pseudolocaleEnv, v)
		if PseudolocaleEnabled() {
			t.Errorf("AFI18N_PSEUDOLOCALE=%q should be treated as disabled", v)
		}
	}
}

// TestPseudoCatalogPreservesPlaceholders is internal/i18nlint/pseudo.go's
// own warning made concrete against the REAL catalogue: a placeholder-
// mangling pseudolocalizer produces a false failure on every real string
// that carries one, which is exactly how a pseudolocale build gets
// disabled. Every {argN}/{count}-style placeholder in the real catalogue
// must appear byte-for-byte in the pseudolocalized form.
func TestPseudoCatalogPreservesPlaceholders(t *testing.T) {
	pseudo := PseudoCatalog()
	checked := 0
	for ns, msgs := range enCatalog {
		for key, orig := range msgs {
			for _, ph := range placeholdersIn(orig.Text) {
				checked++
				got := pseudo[ns][key].Text
				if !strings.Contains(got, ph) {
					t.Errorf("%s.%s: placeholder %q missing from pseudolocalized text %q", ns, key, ph, got)
				}
			}
			for cat, text := range orig.Plural {
				for _, ph := range placeholdersIn(text) {
					checked++
					got := pseudo[ns][key].Plural[cat]
					if !strings.Contains(got, ph) {
						t.Errorf("%s.%s[%s]: placeholder %q missing from pseudolocalized text %q", ns, key, cat, ph, got)
					}
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no placeholders found across the whole catalogue — test is not exercising anything")
	}
}

// placeholdersIn returns every {name}-style token in s (this repo's
// catalogue never uses bare printf verbs — every interpolated key here
// uses gwci18n's {brace} form, per keys_common.go's convention).
func placeholdersIn(s string) []string {
	var out []string
	for {
		start := strings.IndexByte(s, '{')
		if start < 0 {
			return out
		}
		end := strings.IndexByte(s[start:], '}')
		if end < 0 {
			return out
		}
		out = append(out, s[start:start+end+1])
		s = s[start+end+1:]
	}
}

// TestCommonKeysResolveThroughEveryNamespace closes the reported
// common-key degrade: web/ui's shared primitives (StatePanel, Confirm,
// Button/Kebab via labels.go's resolve) render bare common.* keys through
// whatever T their page passes, and every page wave hands them a
// namespace-bound translator fixed to that page's OWN namespace (see
// mergeCommonIntoNamespace's doc comment in catalog.go for the full
// trace to web/main.go's bundleTranslator and the exact call sites in
// web/pages/generate/render_rail.go and web/pages/settings/render.go).
// Without the merge, a lookup like Translate(en, NSGenerate,
// "state.loading", ...) misses and falls back to raw "generate.state.
// loading" text; this asserts every common.* key resolves identically
// (same text) no matter which non-common namespace it's looked up
// against.
func TestCommonKeysResolveThroughEveryNamespace(t *testing.T) {
	b := NewBundle()
	commonKeys := []string{
		KeyActionSave, KeyActionCancel, KeyActionRetry, KeyActionClose, KeyActionDismiss, KeyActionConfirmDestroy,
		KeyStateLoading, KeyStateEmpty, KeyStateError, KeyStateDisabled, KeyStateDisconnected,
		KeyConfirmTypeLabel, KeyKebabActionsFor,
	}
	for _, ns := range []string{NSAuth, NSShell, NSGenerate, NSHistory, NSSettings} {
		want := map[string]string{}
		for _, key := range commonKeys {
			want[key] = b.Translate(DefaultLocale, NSCommon, key, gwci18n.Arguments{"arg1": "x"}, DefaultLocale)
		}
		for _, key := range commonKeys {
			got := b.Translate(DefaultLocale, ns, key, gwci18n.Arguments{"arg1": "x"}, DefaultLocale)
			if got == ns+"."+key {
				t.Errorf("%s.%s did not resolve (fell back to raw key) — common-key degrade is present", ns, key)
				continue
			}
			if got != want[key] {
				t.Errorf("%s.%s = %q, want the same text NSCommon.%s resolves to (%q)", ns, key, got, key, want[key])
			}
		}
	}
}
