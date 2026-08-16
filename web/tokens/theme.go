// Package tokens is the single place AnimeFeedFlux's admin UI decides what it
// looks like: color, type scale, spacing, radii, elevation, and the light/dark
// split (PLAN.md §12.6, TODOS.md D0-11/D0-12). Every web/ui primitive reads
// its look from here through css.Var references — a component that hardcodes
// a hex value or a "dark: ..." branch is a component that drifts from this
// file and is wrong in one theme.
//
// # Design brief
//
// The palette below follows docs/design-direction.md — "the animation
// timesheet": a genga/douga sheet printed on pale green-grey stock, ruled in
// red, marked up in blue pencil. Every hex traces to that document's palette
// table (paper/surface/ink/muted/rule/redline/pencil/phosphor); dark mode is
// not an inversion of it but "the same sheet, lit from beneath" — the same
// roles, restated as the light-table version of the same stock. Cost
// figures, cron readouts and token counts read as instrument values via
// tabular numerals (declared once, at :root, by Emit — see below) rather
// than a monospace face change.
//
// A handful of the doc's literal swatches fail WCAG contrast in the specific
// role they'd need to fill here (a filled badge/button background needing to
// carry readable text on it) and are adjusted from the documented hex — see
// the comments on RoleBorder, RoleDanger and RoleWarning/RoleLive below for
// the exact before/after and the ratio that forced it. Nothing here loosens
// a threshold; contrast_test.go enumerates every real pairing and fails
// loudly with the ratio if a value regresses.
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

	// RoleLive/RoleLiveFg are new with the timesheet direction: "phosphor" is
	// documented there as "live / running / attention", distinct from
	// RoleWarning's "budget nearing limit, stale feed" — a run that is
	// actively generating right now (the thing that is "on air") is not the
	// same fact as a budget that needs attention, even though both currently
	// share the phosphor swatch (see LightTheme/DarkTheme comments). Kept as
	// its own role, not folded into RoleWarning, so a future divergence in
	// color doesn't require re-deriving which call sites meant which.
	RoleLive   = "live"
	RoleLiveFg = "live-fg"
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

// LightTheme is the default (unscoped :root) palette — the timesheet stock
// in daylight. Every value traces to docs/design-direction.md's palette
// table (paper/surface/ink/muted/rule/redline/pencil/phosphor); the few
// swatches that needed adjusting to clear WCAG contrast in the role they
// fill here are called out at their role below, with the documented value,
// the value used, and the ratio.
func LightTheme() css.Theme {
	return css.Theme{
		Colors: map[string]css.Color{
			// paper — page ground (stock).
			RoleBg: css.Hex("E8EDE6"),
			// surface — raised panels, one step brighter than paper.
			RoleSurface: css.Hex("F3F6F1"),
			// Not a named swatch in the direction doc — the doc gives two
			// stock levels (paper, surface) but the existing role set (kept
			// per the rework brief) needs a third for "genuinely raised"
			// things (modals, menus) per the Layout section. Derived by
			// continuing the paper→surface step half as far again, staying
			// in the same pale green-grey family rather than reaching for
			// pure white.
			RoleSurfaceRaised: css.Hex("F9FBF7"),
			// rule, adjusted. Direction doc: #A8B5A5. As documented it is a
			// hairline/border color, but at that value it only clears
			// 1.80:1–2.05:1 against paper/surface/surface-raised — nowhere
			// near the 3:1 UI-boundary floor a divider or input border needs
			// to be visible. Darkened (rgb scaled ×0.70) to #757E73, which
			// clears the worst-case pairing (surface-raised) at 3.55:1.
			RoleBorder: css.Hex("757E73"),
			// muted, reused: an emphasized border needs more presence than
			// the hairline `rule` without reaching for `redline` (reserved,
			// per the doc, for exactly one loud boundary per view). muted
			// already clears body-text contrast (4.75:1+), so it clears the
			// lower 3:1 UI-boundary bar with room to spare.
			RoleBorderStrong: css.Hex("5C6B5E"),
			// ink — primary text.
			RoleText: css.Hex("1A1F1C"),
			// muted — secondary/help text.
			RoleTextMuted: css.Hex("5C6B5E"),
			// paper, reused as the generic light-on-fill foreground: at
			// paper's near-white value it clears 4.5:1 against every filled
			// surface below (accent, accent-strong, the adjusted warning/
			// danger) once those are at the ratios noted on their own roles.
			RoleTextInverse: css.Hex("E8EDE6"),
			// pencil — accent, links, focus. Repointed 2026-08-10 from the
			// muted teal-blue #2E6E8E to the brand crest's own electric
			// blue: the mark is the loudest blue on any screen it appears
			// on, and an accent a few degrees off it reads as a mismatch
			// rather than as a second colour. Chosen at the same contrast
			// headroom the old value had (paper text on it: 4.75:1 here vs
			// 4.73:1 before), so nothing in contrast_test.go's floor moves.
			RoleAccent: css.Hex("2A5FD8"),
			// pencil, darkened (×0.80) for the pressed/hover state — same
			// "darker than the base fill" convention as before, in both
			// themes.
			RoleAccentStrong: css.Hex("224CAD"),
			RoleAccentFg:     css.Hex("E8EDE6"),
			// phosphor, adjusted. Direction doc: #B8860B. At that value,
			// paper text on it is only 2.74:1 — too light a gold to carry
			// light text at all (a dark-ink foreground would clear it, but
			// then fails everywhere else this role's *Fg text lands: 2.9–3.0
			// against pencil/redline/muted). Darkened (×0.70) to #805D07,
			// which clears paper-on-phosphor at 5.07:1 while keeping the
			// paper foreground uniform across every filled role.
			RoleWarning:   css.Hex("805D07"),
			RoleWarningFg: css.Hex("E8EDE6"),
			// redline, adjusted. Direction doc: #C8452F. Paper text on it is
			// 4.07:1 — short of the 4.5:1 body-text floor by a visible
			// margin. Darkened (×0.90) to #B43E2A, 4.83:1.
			RoleDanger:   css.Hex("B43E2A"),
			RoleDangerFg: css.Hex("E8EDE6"),
			// muted, reused: the palette has no dedicated success green: the
			// closest hue rooted in the anchor's pale-green-grey stock is
			// the sage `muted` already in the table, used here as a fill
			// rather than a text color.
			RoleSuccess:   css.Hex("5C6B5E"),
			RoleSuccessFg: css.Hex("E8EDE6"),
			// pencil — the focus ring reads straight off the accent role, so
			// it moves with it (see RoleAccent).
			RoleFocusRing: css.Hex("2A5FD8"),
			// Scrim is a translucent dimming overlay, not a swatch in the
			// doc's table (see contrast_test.go's exemptRoles). Based on
			// `ink`, the palette's darkest tone, rather than an arbitrary
			// black.
			RoleScrim: css.RGBA(0x1A, 0x1F, 0x1C, 0.45),
			// phosphor — "live / running / attention"; same adjusted value
			// as RoleWarning (see that role's comment), reused because both
			// are the same swatch in the direction doc.
			RoleLive:   css.Hex("805D07"),
			RoleLiveFg: css.Hex("E8EDE6"),
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
//
// Per the direction doc, this is not an inversion of LightTheme — it is "the
// same sheet, lit from beneath": the same eight named swatches, restated at
// their documented dark values. Every dark-theme fill (accent, warning,
// danger) already clears contrast against a near-black `paper` foreground as
// documented, so none of light theme's swatch adjustments are needed here —
// see each role's light-theme comment for the ratio that forced the light
// value away from the doc.
func DarkTheme() css.Theme {
	return css.Theme{
		Colors: map[string]css.Color{
			// paper — the light table, lit from beneath.
			RoleBg: css.Hex("12171A"),
			// surface — raised panels.
			RoleSurface: css.Hex("1A2126"),
			// Derived the same way as LightTheme's surface-raised: continuing
			// the paper→surface step, here a full step further (dark fills
			// need more separation than light ones to read as "raised" —
			// see contrast figures in contrast_test.go).
			RoleSurfaceRaised: css.Hex("222B32"),
			// rule, adjusted. Direction doc: #33403A. As documented it clears
			// only 1.29:1–1.66:1 against paper/surface/surface-raised — a
			// border effectively invisible against the ground it's meant to
			// divide. Lightened (rgb scaled ×2.10) to #6B8679, which clears
			// the worst-case pairing (surface-raised) at 3.64:1.
			RoleBorder: css.Hex("6B8679"),
			// muted, reused — same rationale as LightTheme.RoleBorderStrong.
			RoleBorderStrong: css.Hex("93A396"),
			// ink — primary text.
			RoleText: css.Hex("E6ECE5"),
			// muted — secondary/help text.
			RoleTextMuted: css.Hex("93A396"),
			// paper (dark value, near-black) as the generic dark-on-fill
			// foreground: every dark-theme fill below is bright (the "lit
			// from beneath" table brightens accents relative to light mode),
			// so the same near-black clears 4.5:1 against all of them — see
			// each fill's own comment for the exact ratio.
			RoleTextInverse: css.Hex("12171A"),
			// pencil — the brand blue, brightened for the light table. Same
			// repoint as LightTheme.RoleAccent; near-black text on it clears
			// 5.71:1 (the old #5FA8CE cleared 6.86:1, still well past the
			// 4.5:1 floor).
			RoleAccent: css.Hex("5B8CFF"),
			// pencil, darkened for the pressed/hover state. The usual ×0.85
			// step lands on #4D77D9, which carries near-black text at only
			// 4.26:1 — under the 4.5:1 floor, and a hover state that fails
			// contrast is worse than one that fails to look pressed, since
			// it is the state a button is in while being read. Softened to
			// ×0.90 (#5480E6, 4.82:1) instead of darkening the foreground,
			// which would fork *Fg away from the single near-black every
			// other dark-theme fill shares.
			RoleAccentStrong: css.Hex("5480E6"),
			RoleAccentFg:     css.Hex("12171A"),
			// phosphor — unlike the light-theme value, the documented dark
			// swatch already clears paper-on-phosphor at 8.60:1, so it is
			// used unadjusted here.
			RoleWarning:   css.Hex("E3A857"),
			RoleWarningFg: css.Hex("12171A"),
			// redline — the documented dark swatch clears paper-on-redline
			// at 5.16:1 unadjusted (unlike the light-theme value, which
			// needed darkening — see LightTheme.RoleDanger).
			RoleDanger:   css.Hex("E2604A"),
			RoleDangerFg: css.Hex("12171A"),
			// muted, reused — same rationale as LightTheme.RoleSuccess.
			RoleSuccess:   css.Hex("93A396"),
			RoleSuccessFg: css.Hex("12171A"),
			// pencil — focus ring, brightened for the light table.
			RoleFocusRing: css.Hex("5FA8CE"),
			// A dimming overlay stays a literal near-black here rather than
			// reusing `ink` (which in dark mode IS the near-white text
			// color, the opposite of what a scrim needs).
			RoleScrim: css.RGBA(0, 0, 0, 0.65),
			// phosphor — same value as RoleWarning; see that role's comment.
			RoleLive:   css.Hex("E3A857"),
			RoleLiveFg: css.Hex("12171A"),
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

// Every helper below returns a fixed string for a fixed argument, and the
// arguments are a closed set of constants declared in this file. They were
// rebuilding that string on every call — a concatenation, and for Space a
// strconv.Itoa as well — and web/ui's per-render rule slices call them dozens
// of times per element, on every element, on every render (TODOS.md A8-47).
//
// So the answers are computed once, at init, and looked up. The maps are
// written only by init and read-only afterwards, which is what makes an
// unsynchronised map safe here: a mutable cache would need a lock or a
// sync.Map, and the load would cost more than the concatenation it replaced.
//
// A name outside the closed set still works — it falls through to the old
// path — because these helpers are also used with the Shadow/Duration/Font
// constants and with names composed elsewhere, and returning a zero value for
// an unrecognised one would turn a typo into an invisible missing style
// rather than a visible wrong one.
var (
	colorVars    = map[string]css.Color{}
	fontSizeVars = map[string]css.Length{}
	radiusVars   = map[string]css.Length{}
	spaceVars    = map[int]css.Length{}
)

func init() {
	for _, role := range []string{
		RoleBg, RoleSurface, RoleSurfaceRaised, RoleBorder, RoleBorderStrong,
		RoleText, RoleTextMuted, RoleTextInverse,
		RoleAccent, RoleAccentStrong, RoleAccentFg,
		RoleWarning, RoleWarningFg, RoleDanger, RoleDangerFg,
		RoleSuccess, RoleSuccessFg, RoleFocusRing, RoleScrim,
		RoleLive, RoleLiveFg,
	} {
		colorVars[role] = css.Var("color-" + role)
	}
	for _, name := range []string{TextXs, TextSm, TextBase, TextMd, TextLg, TextXl, TextDisplay} {
		fontSizeVars[name] = css.Length(varRef("text-" + name))
	}
	for _, name := range []string{RadiusSm, RadiusMd, RadiusLg, RadiusXl, RadiusFull} {
		radiusVars[name] = css.Length(varRef("radius-" + name))
	}
	// The same scale spacingScale() declares, plus 0, which it also carries.
	for _, n := range []int{0, 1, 2, 3, 4, 5, 6, 8, 10, 12, 16, 20, 24, 32, 40, 48, 56, 64, 80, 96} {
		spaceVars[n] = css.Length(varRef("space-" + strconv.Itoa(n)))
	}
}

// Color returns css.Var("color-<role>") — a reference to a semantic role
// declared by Emit, e.g. tokens.Color(tokens.RoleAccent).
func Color(role string) css.Color {
	if v, ok := colorVars[role]; ok {
		return v
	}
	return css.Var("color-" + role)
}

// FontSize returns css.Var("text-<name>") wrapped as a Length via CSS's own
// var() (custom properties are untyped at the CSS level; the wrapping
// property, e.g. font-size, decides how the string is interpreted).
func FontSize(name string) css.Length {
	if v, ok := fontSizeVars[name]; ok {
		return v
	}
	return css.Length(varRef("text-" + name))
}

// Radius returns css.Var("radius-<name>").
func Radius(name string) css.Length {
	if v, ok := radiusVars[name]; ok {
		return v
	}
	return css.Length(varRef("radius-" + name))
}

// Space returns css.Var("space-<n>") for a spacing scale index (0,1,2,3,4,5,
// 6,8,10,12,16,20,24,32,40,48,56,64,80,96 — Tailwind's numeric scale, each
// step 0.25rem).
func Space(n int) css.Length {
	if v, ok := spaceVars[n]; ok {
		return v
	}
	return css.Length(varRef("space-" + strconv.Itoa(n)))
}

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
	// color-scheme is what the BROWSER paints native widget chrome with —
	// the parts CSS cannot reach, above all a <select>'s dropdown popup and
	// its <option> rows. The shell's <meta name="color-scheme"> only
	// declares that both schemes exist; with the app's theme driven by
	// data-theme (not prefers-color-scheme), the UA otherwise keeps using
	// the OS scheme, which is why dark mode showed bright-white option
	// lists on /generate (reported 2026-08-15). Tied to the same selector
	// pair as the palettes so it can never disagree with them.
	css.Root(css.Raw("color-scheme", "light"))

	darkRules := append(DarkTheme().RootRules(), extraTokens(DarkTheme())...)
	darkRules = append(darkRules, css.Raw("color-scheme", "dark"))
	css.Global(`:root[data-theme="dark"]`, darkRules...)

	// Dropdown option colors, decided once, here — the second half of the
	// color-scheme fix above. Pages style their <select> elements freely
	// (the /generate strip deliberately keeps them transparent over its own
	// surface), and every <option> then inherits a THEMED text color over a
	// TRANSPARENT background. The element itself renders fine; the popup
	// does not: the browser paints the popup's base itself, so a
	// transparent-background option mixes OUR foreground with the UA's
	// background — which is exactly how dark mode produced light-gray text
	// on a bright-white list (verified live with computed styles,
	// 2026-08-15). Pinning BOTH sides of every option to the same token
	// pair means the popup can never combine colors from two different
	// theme systems, whatever the page did to the <select> around it.
	css.Global("select option",
		css.Raw("background-color", string(Color(RoleSurface))),
		css.Raw("color", string(Color(RoleText))),
	)

	// Tabular numerals, decided once, here. The direction doc calls this
	// non-negotiable for "every column of counts, costs, tokens and
	// timestamps" — the instrument-panel feel a page gets from numerals that
	// hold their width and stay aligned in a column. Declaring it globally on
	// :root, theme-independent (both light and dark inherit the same rule),
	// is what stops each page under web/pages/* from reaching for its own
	// font-variant-numeric or a mono-font workaround: every digit in the app
	// is tabular by default, and a component only needs to override this if
	// it genuinely wants proportional numerals (rare enough to opt out of,
	// not in to).
	css.Root(css.FontVariantNumeric.TabularNums)

	// prefers-reduced-motion, decided once, here. Every component transition
	// or animation goes through Duration(tokens.DurationFast/Base/Slow) rather
	// than a literal ms value, so collapsing these three custom properties to
	// near-zero under the media query neutralizes motion everywhere at once —
	// a component cannot forget to opt in, because it never had a duration of
	// its own to gate. 0.01ms rather than 0ms per RawDuration's doc: code
	// awaiting `transitionend` does not hang for exactly the users who asked
	// for less motion. EasingStd is left alone deliberately: an easing curve
	// applied to an effectively-instant transition has no perceptible motion
	// of its own to neutralize.
	css.Root(css.Media(css.ReducedMotion,
		css.Custom(DurationFast, "0.01ms"),
		css.Custom(DurationBase, "0.01ms"),
		css.Custom(DurationSlow, "0.01ms"),
	)...)
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
