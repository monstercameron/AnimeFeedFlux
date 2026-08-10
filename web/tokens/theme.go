// Package tokens is the single place AnimeFeedFlux's admin UI decides what it
// looks like: color, type scale, spacing, radii, elevation, and the light/dark
// split (PLAN.md §12.6, TODOS.md D0-11/D0-12). Every web/ui primitive reads
// its look from here through css.Var references — a component that hardcodes
// a hex value or a "dark: ..." branch is a component that drifts from this
// file and is wrong in one theme.
//
// # Design brief
//
// AnimeFeedFlux's admin console is a one-operator control room for an
// unattended pipeline that spends real money generating and publishing RSS
// items nobody watches in real time. The register is "signal room", not
// "dashboard SaaS": a calm, dense, numbers-honest surface where cost figures,
// cron readouts, and token counts are set in a monospace data face so they
// read as instrument values, not prose, and the one accent color ("signal
// teal") is reserved for what is live/active/interactive — so a glance tells
// you what is on air versus what is just describing state. Warning/danger/
// success stay apart from the accent so "this is happening" is never
// confused with "this needs attention" or "this cost money."
//
// # Light and dark, decided once
//
// Both palettes are declared here as two css.Theme values and emitted as
// :root custom properties: the light palette unconditionally on :root, the
// dark palette scoped under :root[data-theme="dark"]. Components never
// branch on theme; they reference the semantic Var (tokens.Color("bg"), or
// css.Var("color-bg") directly) and the cascade picks the right value.
// Switching themes at runtime is GWC's ui.UseTheme("dark")/SetTheme, which
// sets data-theme on <html> (= the :root element itself) — see the doc
// comment on Emit for why that matters for the selector this file emits.
package tokens

import (
	"strconv"

	"github.com/monstercameron/GoWebComponents/v5/css"
)

// Semantic color-role names. These are the keys of Theme.Colors below and the
// suffix of the emitted custom property: role "bg" emits "--color-bg". Use
// these constants (or Color(role)) instead of literal strings so a typo is a
// compile-time break in this package rather than a silently-dead css.Var
// reference at a call site.
const (
	RoleBg            = "bg"             // page background
	RoleSurface       = "surface"        // card/panel background
	RoleSurfaceRaised = "surface-raised" // modal/popover/menu background
	RoleBorder        = "border"         // hairline dividers, input borders
	RoleBorderStrong  = "border-strong"  // emphasized borders (focus adjacency, table header rule)
	RoleText          = "text"           // primary text
	RoleTextMuted     = "text-muted"     // secondary/help text, captions
	RoleTextInverse   = "text-inverse"   // text on a filled accent/danger/etc. surface
	RoleAccent        = "accent"         // the one "live/interactive" color
	RoleAccentStrong  = "accent-strong"  // accent hover/active
	RoleAccentFg      = "accent-fg"      // text/icon on an accent-filled surface
	RoleWarning       = "warning"        // budget nearing limit, stale feed
	RoleWarningFg     = "warning-fg"
	RoleDanger        = "danger" // destructive actions, kill switch off, errors
	RoleDangerFg      = "danger-fg"
	RoleSuccess       = "success" // published, connected, within budget
	RoleSuccessFg     = "success-fg"
	RoleFocusRing     = "focus-ring" // visible-focus outline color (a11y floor)
	RoleScrim         = "scrim"      // modal/overlay backdrop
)

// Font-size scale names (Theme.FontSizes keys / "--text-<name>").
const (
	TextXs      = "xs"      // captions, table meta
	TextSm      = "sm"      // secondary labels, help text
	TextBase    = "base"    // default UI text (deliberately just under 1rem — a dense
	TextMd      = "md"      // console reads small text all day)
	TextLg      = "lg"      // section headings within a panel
	TextXl      = "xl"      // page/panel titles
	Text2xl     = "2xl"     // rare — a single hero number (e.g. today's spend)
	TextDisplay = "display" // reserved, used sparingly
)

// Radius scale names (Theme.Radii keys / "--radius-<name>").
const (
	RadiusSm   = "sm"
	RadiusMd   = "md"
	RadiusLg   = "lg"
	RadiusXl   = "xl"
	RadiusFull = "full"
)

// Shadow scale names, declared as custom properties by Emit (not part of
// css.Theme, which has no elevation scale).
const (
	ShadowSm = "shadow-sm"
	ShadowMd = "shadow-md"
	ShadowLg = "shadow-lg"
)

// Motion tokens: durations and an easing, declared as custom properties.
const (
	DurationFast = "duration-fast" // micro-interactions: hover, focus ring
	DurationBase = "duration-base" // toasts, menu open/close
	DurationSlow = "duration-slow" // modal enter/exit
	EasingStd    = "easing-standard"
)

// Font-family custom properties. No external font is loaded (no CDN, no new
// dep): both stacks are system fonts, chosen for the data-vs-prose split
// described in the design brief above.
const (
	FontSans = "font-sans" // UI chrome, labels, prose
	FontMono = "font-mono" // costs, tokens, cron expressions, GUIDs, timestamps
)

// LightTheme is the default (unscoped :root) palette.
func LightTheme() css.Theme {
	return css.Theme{
		Colors: map[string]css.Color{
			RoleBg:            css.Hex("F7F6F2"),
			RoleSurface:       css.Hex("FFFFFF"),
			RoleSurfaceRaised: css.Hex("FFFFFF"),
			RoleBorder:        css.Hex("DEDCD3"),
			RoleBorderStrong:  css.Hex("C6C2B4"),
			RoleText:          css.Hex("191B1D"),
			RoleTextMuted:     css.Hex("63676D"),
			RoleTextInverse:   css.Hex("F7F6F2"),
			RoleAccent:        css.Hex("0C7C74"),
			RoleAccentStrong:  css.Hex("085B55"),
			RoleAccentFg:      css.Hex("FFFFFF"),
			RoleWarning:       css.Hex("92400E"),
			RoleWarningFg:     css.Hex("FFFFFF"),
			RoleDanger:        css.Hex("B3212B"),
			RoleDangerFg:      css.Hex("FFFFFF"),
			RoleSuccess:       css.Hex("157A4F"),
			RoleSuccessFg:     css.Hex("FFFFFF"),
			RoleFocusRing:     css.Hex("0C7C74"),
			RoleScrim:         css.RGBA(11, 13, 16, 0.45),
		},
		FontSizes: map[string]css.Length{
			TextXs:      css.Rem(0.75),
			TextSm:      css.Rem(0.8125),
			TextBase:    css.Rem(0.9375),
			TextMd:      css.Rem(1),
			TextLg:      css.Rem(1.125),
			TextXl:      css.Rem(1.375),
			Text2xl:     css.Rem(1.75),
			TextDisplay: css.Rem(2.25),
		},
		Radii: map[string]css.Length{
			RadiusSm:   css.Rem(0.25),
			RadiusMd:   css.Rem(0.5),
			RadiusLg:   css.Rem(0.75),
			RadiusXl:   css.Rem(1),
			RadiusFull: css.Length("9999px"),
		},
		Spacing: spacingScale(),
	}
}

// DarkTheme overrides the roles that actually change under
// :root[data-theme="dark"]. Type scale, radii and spacing are geometry, not
// color, and are intentionally identical in both themes — decided once, per
// D0-12, rather than risk a second silently-drifting copy.
func DarkTheme() css.Theme {
	return css.Theme{
		Colors: map[string]css.Color{
			RoleBg:            css.Hex("0B0D10"),
			RoleSurface:       css.Hex("12151A"),
			RoleSurfaceRaised: css.Hex("1A1F26"),
			RoleBorder:        css.Hex("262C34"),
			RoleBorderStrong:  css.Hex("38414C"),
			RoleText:          css.Hex("E7E9EA"),
			RoleTextMuted:     css.Hex("8A929B"),
			RoleTextInverse:   css.Hex("0B0D10"),
			RoleAccent:        css.Hex("34D6C7"),
			RoleAccentStrong:  css.Hex("1FB6A8"),
			RoleAccentFg:      css.Hex("04211E"),
			RoleWarning:       css.Hex("F2A93B"),
			RoleWarningFg:     css.Hex("2B1B02"),
			RoleDanger:        css.Hex("F1555F"),
			RoleDangerFg:      css.Hex("2B0508"),
			RoleSuccess:       css.Hex("3ECF8E"),
			RoleSuccessFg:     css.Hex("042015"),
			RoleFocusRing:     css.Hex("34D6C7"),
			RoleScrim:         css.RGBA(0, 0, 0, 0.65),
		},
		FontSizes: LightTheme().FontSizes,
		Radii:     LightTheme().Radii,
		Spacing:   spacingScale(),
	}
}

func spacingScale() map[int]css.Length {
	spacing := map[int]css.Length{0: css.Zero}
	for _, n := range []int{1, 2, 3, 4, 5, 6, 8, 10, 12, 16, 20, 24, 32, 40, 48, 56, 64, 80, 96} {
		spacing[n] = css.Rem(float64(n) * 0.25)
	}
	return spacing
}

// Color returns css.Var("color-<role>") — a reference to a semantic role
// declared by Emit, e.g. tokens.Color(tokens.RoleAccent).
func Color(role string) css.Color { return css.Var("color-" + role) }

// FontSize returns css.Var("text-<name>") wrapped as a Length via CSS's own
// var() (custom properties are untyped at the CSS level; the wrapping
// property, e.g. font-size, decides how the string is interpreted).
func FontSize(name string) css.Length { return css.Length(varRef("text-" + name)) }

// Radius returns css.Var("radius-<name>").
func Radius(name string) css.Length { return css.Length(varRef("radius-" + name)) }

// Space returns css.Var("space-<n>") for a spacing scale index (0,1,2,3,4,5,
// 6,8,10,12,16,20,24,32,40,48,56,64,80,96 — Tailwind's numeric scale, each
// step 0.25rem).
func Space(n int) css.Length { return css.Length(varRef("space-" + strconv.Itoa(n))) }

// Shadow returns css.Var("<name>") for one of the ShadowSm/Md/Lg constants —
// pass straight to css.Raw("box-shadow", string(tokens.Shadow(tokens.ShadowMd))).
func Shadow(name string) string { return varRef(name) }

// Duration returns css.Var("<name>") for one of the Duration* constants, typed
// as a css.Duration for use with css.Transition/css.Animation.
func Duration(name string) css.Duration { return css.Duration(varRef(name)) }

// Easing returns css.Var("<name>") for EasingStd, typed as css.Easing.
func Easing(name string) css.Easing { return css.Easing(varRef(name)) }

// FontFamily returns css.Var("<name>") for FontSans/FontMono, typed as a
// css.FontStack for use with css.Font.
func FontFamily(name string) css.FontStack { return css.VarFontStack(name) }

func varRef(name string) string { return "var(--" + name + ")" }

// Emit declares the design tokens as CSS custom properties: the light theme
// unconditionally on :root, and the dark theme's overrides scoped under
// :root[data-theme="dark"]. Call this exactly once, early (the shell's root
// component, before anything renders that references a token) — Global
// emission is idempotent/deduped, but there is still only one correct place
// to decide the app's look.
//
// # Why this does NOT use css.DataTheme
//
// css.DataTheme(name, rules...) builds the ANCESTOR-scoped form
// "[data-theme=\"name\"] &" — correct for a component class nested inside a
// themed container. But ui.SetTheme/UseTheme (GWC's runtime theme switch)
// sets data-theme on document.documentElement, i.e. on <html> itself, which
// IS :root — not an ancestor of it. Composing DataTheme("dark", ...) with
// Global(":root", ...) would emit "[data-theme=\"dark\"] :root", a descendant
// selector that can never match its own ancestor and would silently leave
// the dark palette dead. Emit instead builds the attribute selector directly
// on :root ("`:root[data-theme=\"dark\"]`") so it matches the element the
// attribute is actually set on. This is the theming-layer analogue of the
// "style map drops --custom-properties" and "CSS emitter collapses repeated
// properties" traps called out in this project's brief: verified against
// v5.0.1 by theme_test.go rather than assumed from the docs.
func Emit() {
	css.Root(LightTheme().RootRules()...)
	css.Root(extraTokens(LightTheme())...)

	darkRules := append(DarkTheme().RootRules(), extraTokens(DarkTheme())...)
	css.Global(`:root[data-theme="dark"]`, darkRules...)
}

// extraTokens declares the custom properties css.Theme.RootRules does not
// cover: elevation, motion, and font stacks. They are theme-independent
// today (shadows/motion/fonts do not change between light and dark) but are
// still declared per-theme-call so a future theme CAN diverge them (e.g.
// stronger shadows in dark mode, where a soft box-shadow all but disappears
// against a near-black surface) without a call-site change.
func extraTokens(theme css.Theme) []css.Rule {
	_ = theme
	return []css.Rule{
		css.Custom(ShadowSm, "0 1px 2px 0 rgba(0,0,0,0.16)"),
		css.Custom(ShadowMd, "0 4px 12px -2px rgba(0,0,0,0.24)"),
		css.Custom(ShadowLg, "0 16px 32px -8px rgba(0,0,0,0.32)"),
		css.CustomDuration(DurationFast, css.Ms(120)),
		css.CustomDuration(DurationBase, css.Ms(200)),
		css.CustomDuration(DurationSlow, css.Ms(280)),
		css.Custom(EasingStd, string(css.EaseOut)),
		css.CustomFontStack(FontSans, css.FontStackOf(
			"-apple-system", "BlinkMacSystemFont", "Segoe UI", "Roboto", "Helvetica Neue", "Arial", "sans-serif",
		)),
		css.CustomFontStack(FontMono, css.FontStackOf(
			"ui-monospace", "Cascadia Code", "SFMono-Regular", "Consolas", "Liberation Mono", "monospace",
		)),
	}
}
