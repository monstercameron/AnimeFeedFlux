package tokens

import (
	"strconv"
	"strings"
	"testing"

	"github.com/monstercameron/GoWebComponents/v5/css"
)

// TestEmit_CustomPropertiesSurvive is the check called for in this project's
// brief: verify, rather than assume, that this library version actually
// emits --custom-properties from the typed theme layer, and that the dark
// override lands on a selector that can match the element ui.SetTheme
// actually stamps (:root itself, not some ancestor of it).
func TestEmit_CustomPropertiesSurvive(t *testing.T) {
	skipIfWasm(t)
	css.Reset()
	Emit()
	out := css.Harvest()

	if !strings.Contains(out, ":root{") && !strings.Contains(out, ":root {") {
		t.Fatalf("expected an unscoped :root block, got:\n%s", out)
	}
	for _, want := range []string{
		"--color-bg:", "--color-accent:", "--color-danger:",
		"--text-base:", "--radius-md:", "--space-4:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("light :root block missing custom property %q in emitted CSS:\n%s", want, out)
		}
	}

	// The dark override must be scoped to :root[data-theme="dark"] — an
	// ancestor form like `[data-theme="dark"] :root` would never match,
	// because ui.SetTheme stamps the attribute on documentElement (:root)
	// itself, not on some wrapper around it.
	if !strings.Contains(out, `:root[data-theme="dark"]`) {
		t.Fatalf("expected a :root[data-theme=\"dark\"] block (the selector ui.SetTheme's attribute can actually match), got:\n%s", out)
	}
	if strings.Contains(out, `[data-theme="dark"] :root`) {
		t.Fatalf("emitted the dead ancestor form '[data-theme=\"dark\"] :root' — this can never match, since data-theme is set on :root itself:\n%s", out)
	}

	// Light and dark must disagree on at least the accent role, or the
	// override block is either missing or a no-op copy of light.
	lightIdx := strings.Index(out, "--color-accent:")
	darkBlockIdx := strings.Index(out, `:root[data-theme="dark"]`)
	if lightIdx < 0 || darkBlockIdx < 0 || lightIdx > darkBlockIdx {
		t.Fatalf("could not locate light accent value before the dark block in:\n%s", out)
	}
	darkSection := out[darkBlockIdx:]
	// Asserted against DarkTheme's own value rather than a hex literal
	// repeated here: this test is about the override reaching the emitted
	// sheet, not about which blue the brand happens to be, and a second
	// copy of the value only means a palette change fails here for a
	// reason that has nothing to do with what this test checks.
	wantDark := strings.ToLower(string(DarkTheme().Colors[RoleAccent]))
	if !strings.Contains(strings.ToLower(darkSection), "--color-accent:"+wantDark) {
		t.Errorf("dark block does not override --color-accent to DarkTheme's value (%s), dark section:\n%s", wantDark, darkSection)
	}
}

// TestEmit_Idempotent confirms Emit can be called more than once (e.g. by
// more than one component under test) without duplicating the stylesheet —
// Global emission is documented as deduped by (selector + rules) content.
func TestEmit_Idempotent(t *testing.T) {
	css.Reset()
	Emit()
	first := css.Harvest()
	Emit()
	second := css.Harvest()
	if first != second {
		t.Fatalf("calling Emit twice changed the harvested CSS:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

// TestSpacingRadiusFontHelpers checks the var()-reference helpers point at
// the same custom-property names Emit declares, so a component using
// tokens.Space(4) and Emit's own --space-4 can never drift apart.
func TestSpacingRadiusFontHelpers(t *testing.T) {
	if got, want := string(Space(4)), "var(--space-4)"; got != want {
		t.Errorf("Space(4) = %q, want %q", got, want)
	}
	if got, want := string(FontSize(TextBase)), "var(--text-base)"; got != want {
		t.Errorf("FontSize(base) = %q, want %q", got, want)
	}
	if got, want := string(Radius(RadiusMd)), "var(--radius-md)"; got != want {
		t.Errorf("Radius(md) = %q, want %q", got, want)
	}
	if got, want := string(Color(RoleAccent)), "var(--color-accent)"; got != want {
		t.Errorf("Color(accent) = %q, want %q", got, want)
	}
}

// The interning tables (A8-47) must return exactly what the concatenation
// they replaced returned. A table that drifts from the fallback is worse than
// no table: it produces a var reference to a custom property that was never
// declared, which CSS resolves to nothing and renders as an unstyled element.
func TestInternedTokensMatchTheirComputedForm(t *testing.T) {
	for _, role := range []string{
		RoleBg, RoleSurface, RoleSurfaceRaised, RoleBorder, RoleBorderStrong,
		RoleText, RoleTextMuted, RoleTextInverse,
		RoleAccent, RoleAccentStrong, RoleAccentFg,
		RoleWarning, RoleWarningFg, RoleDanger, RoleDangerFg,
		RoleSuccess, RoleSuccessFg, RoleFocusRing, RoleScrim,
		RoleLive, RoleLiveFg,
	} {
		if _, ok := colorVars[role]; !ok {
			t.Errorf("role %q is declared but not interned", role)
			continue
		}
		if got, want := Color(role), css.Var("color-"+role); got != want {
			t.Errorf("Color(%q) = %q, want %q", role, got, want)
		}
	}
	for _, name := range []string{TextXs, TextSm, TextBase, TextMd, TextLg, TextXl, TextDisplay} {
		if got, want := FontSize(name), css.Length(varRef("text-"+name)); got != want {
			t.Errorf("FontSize(%q) = %q, want %q", name, got, want)
		}
	}
	for _, name := range []string{RadiusSm, RadiusMd, RadiusLg, RadiusXl, RadiusFull} {
		if got, want := Radius(name), css.Length(varRef("radius-"+name)); got != want {
			t.Errorf("Radius(%q) = %q, want %q", name, got, want)
		}
	}
	for n := range spacingScale() {
		if got, want := Space(n), css.Length(varRef("space-"+strconv.Itoa(n))); got != want {
			t.Errorf("Space(%d) = %q, want %q", n, got, want)
		}
	}
}

// A name outside the closed set must still resolve, not return a zero value:
// these helpers are also called with composed names, and a zero would turn a
// typo into a silently missing style instead of a visibly wrong one.
func TestUninternedTokensStillResolve(t *testing.T) {
	if got, want := Color("not-a-declared-role"), css.Var("color-not-a-declared-role"); got != want {
		t.Errorf("Color = %q, want %q", got, want)
	}
	if got, want := Space(7), css.Length(varRef("space-7")); got != want {
		t.Errorf("Space(7) = %q, want %q", got, want)
	}
}
