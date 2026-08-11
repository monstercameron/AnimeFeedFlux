package tokens

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/monstercameron/GoWebComponents/v5/css"
)

// This file is the systematic check called for by D5-04 (PLAN.md §12.6,
// TODOS.md D5-04): every semantic foreground/background PAIRING the token
// layer actually defines, checked for WCAG 2 contrast in BOTH themes, with
// failures named as "<fg> on <bg> = X.XX:1, need Y:1" rather than a bare
// pass/fail. Fixing a failure means changing a hex value in theme.go, not
// loosening the ratio asserted here.

// pairing is one semantic foreground/background combination the app can
// actually put text or a UI boundary against, taken from how the roles are
// documented in theme.go (RoleTextInverse "on a filled accent/danger/etc.
// surface", RoleBorder "input borders", RoleFocusRing "visible-focus
// outline", etc.) rather than invented independently of that doc.
type pairing struct {
	fg, bg string
	min    float64 // minimum WCAG contrast ratio
	reason string  // why this ratio applies
}

const (
	bodyText = 4.5 // WCAG AA normal text
	uiBound  = 3.0 // WCAG AA large text / UI component boundaries (focus rings, input borders)
)

// pairings enumerates every foreground/background combination the token
// layer defines a real use for. Roles that never sit behind or in front of
// text/UI-boundary content (RoleScrim) are exempted below, explicitly, with
// a reason — not folded into this list with a lowered threshold.
func pairings() []pairing {
	return []pairing{
		// Body / secondary text on the three surface levels.
		{RoleText, RoleBg, bodyText, "primary text on page background"},
		{RoleText, RoleSurface, bodyText, "primary text on card/panel"},
		{RoleText, RoleSurfaceRaised, bodyText, "primary text on modal/popover/menu"},
		{RoleTextMuted, RoleBg, bodyText, "secondary/help text on page background"},
		{RoleTextMuted, RoleSurface, bodyText, "secondary/help text on card/panel"},
		{RoleTextMuted, RoleSurfaceRaised, bodyText, "secondary/help text on modal/popover/menu"},

		// Text/icon on a filled semantic surface (buttons, badges, banners).
		{RoleAccentFg, RoleAccent, bodyText, "accent button/badge text"},
		{RoleAccentFg, RoleAccentStrong, bodyText, "accent button text, hover/active fill"},
		{RoleWarningFg, RoleWarning, bodyText, "warning banner/badge text"},
		{RoleDangerFg, RoleDanger, bodyText, "danger banner/badge text"},
		{RoleSuccessFg, RoleSuccess, bodyText, "success banner/badge text"},
		{RoleLiveFg, RoleLive, bodyText, "live/running badge text (phosphor)"},

		// RoleTextInverse is documented as the generic "text on a filled
		// accent/danger/etc. surface" role, distinct from the dedicated *Fg
		// roles above — check it against every fill it could land on.
		{RoleTextInverse, RoleAccent, bodyText, "generic inverse text on accent fill"},
		{RoleTextInverse, RoleAccentStrong, bodyText, "generic inverse text on accent hover/active fill"},
		{RoleTextInverse, RoleWarning, bodyText, "generic inverse text on warning fill"},
		{RoleTextInverse, RoleDanger, bodyText, "generic inverse text on danger fill"},
		{RoleTextInverse, RoleSuccess, bodyText, "generic inverse text on success fill"},
		{RoleTextInverse, RoleLive, bodyText, "generic inverse text on live/running fill"},

		// UI component boundaries: hairline/emphasized borders and the focus
		// ring, against every surface level they can sit on. 3:1, not 4.5:1 —
		// but not exempt, because a focus ring that fails this is a keyboard
		// user's only position indicator.
		{RoleBorder, RoleBg, uiBound, "hairline divider / input border on page background"},
		{RoleBorder, RoleSurface, uiBound, "hairline divider / input border on card/panel"},
		{RoleBorder, RoleSurfaceRaised, uiBound, "hairline divider / input border on modal/popover/menu"},
		{RoleBorderStrong, RoleBg, uiBound, "emphasized border on page background"},
		{RoleBorderStrong, RoleSurface, uiBound, "emphasized border on card/panel"},
		{RoleBorderStrong, RoleSurfaceRaised, uiBound, "emphasized border on modal/popover/menu"},
		{RoleFocusRing, RoleBg, uiBound, "focus ring on page background"},
		{RoleFocusRing, RoleSurface, uiBound, "focus ring on card/panel"},
		{RoleFocusRing, RoleSurfaceRaised, uiBound, "focus ring on modal/popover/menu"},
	}
}

// exemptRoles lists roles this test deliberately does NOT contrast-check,
// with the reason why — per the brief, an exemption needs a reason, not a
// lowered threshold.
//
//   - RoleScrim is a translucent modal-backdrop fill (css.RGBA with alpha
//     <1) composited over whatever content is behind it, which varies at
//     runtime. There is no fixed background to compute a ratio against, and
//     it never carries text of its own — it exists to dim, not to be read.
var exemptRoles = map[string]string{
	RoleScrim: "translucent overlay composited over variable content, never carries text",
}

func TestColorContrast_AA_BothThemes(t *testing.T) {
	themes := []struct {
		name  string
		theme css.Theme
	}{
		{"light", LightTheme()},
		{"dark", DarkTheme()},
	}

	for _, tc := range themes {
		t.Run(tc.name, func(t *testing.T) {
			for _, p := range pairings() {
				if _, ok := exemptRoles[p.fg]; ok {
					// exemptRoles is documentation, not a filter: no exempt
					// role should ever appear as a pairing's fg/bg in the
					// first place (see pairings' doc comment), so this is a
					// self-check that the list stayed in sync.
					t.Fatalf("pairing uses %q, which is on the exempt list — remove it from pairings() instead", p.fg)
				}
				fgColor, ok := tc.theme.Colors[p.fg]
				if !ok {
					t.Fatalf("theme %q missing role %q referenced by pairing (%s)", tc.name, p.fg, p.reason)
				}
				bgColor, ok := tc.theme.Colors[p.bg]
				if !ok {
					t.Fatalf("theme %q missing role %q referenced by pairing (%s)", tc.name, p.bg, p.reason)
				}
				ratio, err := contrastRatio(fgColor, bgColor)
				if err != nil {
					t.Fatalf("theme %q pairing --color-%s on --color-%s: %v", tc.name, p.fg, p.bg, err)
				}
				if ratio < p.min {
					t.Errorf("%s theme: --color-%s on --color-%s = %.2f:1, need %.1f:1 (%s)",
						tc.name, p.fg, p.bg, ratio, p.min, p.reason)
				}
			}
		})
	}
}

// TestExemptRoles_AreDeclaredRoles guards the exemption list itself: an
// exemption for a role name that no longer exists (typo, renamed role) would
// silently exempt nothing and stop meaning what its comment says.
func TestExemptRoles_AreDeclaredRoles(t *testing.T) {
	for role := range exemptRoles {
		if _, ok := LightTheme().Colors[role]; !ok {
			t.Errorf("exemptRoles references role %q, which LightTheme does not declare", role)
		}
		if _, ok := DarkTheme().Colors[role]; !ok {
			t.Errorf("exemptRoles references role %q, which DarkTheme does not declare", role)
		}
	}
}

// --- WCAG 2 contrast math -------------------------------------------------

// contrastRatio computes the WCAG 2 contrast ratio between two css.Color
// values. Both must resolve to opaque RGB (hex or rgb()/rgba() with alpha
// 1) — anything else is a caller error, since contrast against a
// translucent color is meaningless without a known backdrop (see
// exemptRoles).
func contrastRatio(a, b css.Color) (float64, error) {
	ra, ga, ba, err := parseOpaqueRGB(a)
	if err != nil {
		return 0, fmt.Errorf("foreground %q: %w", a, err)
	}
	rb, gb, bb, err := parseOpaqueRGB(b)
	if err != nil {
		return 0, fmt.Errorf("background %q: %w", b, err)
	}
	la := relativeLuminance(ra, ga, ba)
	lb := relativeLuminance(rb, gb, bb)
	lighter, darker := la, lb
	if lighter < darker {
		lighter, darker = darker, lighter
	}
	return (lighter + 0.05) / (darker + 0.05), nil
}

// relativeLuminance implements the WCAG 2 formula (§1.4.3), channels in
// [0,255].
func relativeLuminance(r, g, b int) float64 {
	lin := func(c int) float64 {
		cs := float64(c) / 255
		if cs <= 0.03928 {
			return cs / 12.92
		}
		return math.Pow((cs+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

// parseOpaqueRGB parses the subset of css.Color this token file actually
// emits: #rgb, #rrggbb hex (css.Hex's output), and rgba(r,g,b,a) (css.RGBA's
// output) provided alpha is 1 — anything with alpha < 1 has no fixed
// backdrop and returns an error, which is intentional: it forces such a
// role onto the exemptRoles list with a stated reason instead of being
// silently measured against an arbitrary assumed background.
func parseOpaqueRGB(c css.Color) (r, g, b int, err error) {
	s := strings.TrimSpace(string(c))
	switch {
	case strings.HasPrefix(s, "#"):
		return parseHex(s)
	case strings.HasPrefix(s, "rgba(") || strings.HasPrefix(s, "rgb("):
		return parseRGBFunc(s)
	default:
		return 0, 0, 0, fmt.Errorf("unrecognized color format %q", s)
	}
}

func parseHex(s string) (r, g, b int, err error) {
	h := strings.TrimPrefix(s, "#")
	expand := func(c byte) string { return string([]byte{c, c}) }
	switch len(h) {
	case 3, 4: // #rgb / #rgba
		rv, err1 := strconv.ParseInt(expand(h[0]), 16, 0)
		gv, err2 := strconv.ParseInt(expand(h[1]), 16, 0)
		bv, err3 := strconv.ParseInt(expand(h[2]), 16, 0)
		for _, e := range []error{err1, err2, err3} {
			if e != nil {
				return 0, 0, 0, e
			}
		}
		return int(rv), int(gv), int(bv), nil
	case 6, 8: // #rrggbb / #rrggbbaa
		rv, err1 := strconv.ParseUint(h[0:2], 16, 32)
		gv, err2 := strconv.ParseUint(h[2:4], 16, 32)
		bv, err3 := strconv.ParseUint(h[4:6], 16, 32)
		for _, e := range []error{err1, err2, err3} {
			if e != nil {
				return 0, 0, 0, e
			}
		}
		return int(rv), int(gv), int(bv), nil
	default:
		return 0, 0, 0, fmt.Errorf("hex color %q has unsupported length %d", s, len(h))
	}
}

func parseRGBFunc(s string) (r, g, b int, err error) {
	inner := s[strings.Index(s, "(")+1 : strings.LastIndex(s, ")")]
	parts := strings.Split(inner, ",")
	if len(parts) < 3 {
		return 0, 0, 0, fmt.Errorf("rgb() color %q has fewer than 3 components", s)
	}
	nums := make([]float64, len(parts))
	for i, p := range parts {
		v, e := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if e != nil {
			return 0, 0, 0, e
		}
		nums[i] = v
	}
	if len(nums) >= 4 && nums[3] != 1 {
		return 0, 0, 0, fmt.Errorf("rgba() color %q has alpha %.2f < 1, no fixed backdrop to contrast against", s, nums[3])
	}
	return int(nums[0]), int(nums[1]), int(nums[2]), nil
}
