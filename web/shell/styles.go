//go:build js && wasm

package shell

import (
	"github.com/monstercameron/GoWebComponents/v5/css"

	"github.com/monstercameron/AnimeFeedFlux/web/tokens"
)

// emitShellStyles declares every rule this package's own components need —
// the shell root (#app), the DISCONNECTED banner, and the session-expiry
// modal — plus the two leftover classes (.af-content/.af-placeholder) that
// pages.go's placeholder body still emits for any route without a real page
// registered yet. These are the four rule groups that used to live in
// web/static/index.html's hand-written <style> block (PLAN.md/this task's
// brief: "all styling lives in the WASM, authored with GWC's typed CSS
// library").
//
// Called from Mount, immediately after tokens.Emit() — component rules read
// tokens.Color/tokens.Space/etc., which resolve to var(--color-…) /
// var(--space-…) references. CSS custom-property lookups are resolved by the
// cascade at computed-value time, not by the TEXTUAL order two rules happen
// to appear in the stylesheet, so these do not have to be emitted after
// tokens.Emit()'s :root block to work — but Mount still emits tokens first,
// deliberately, as the one obviously-correct order rather than relying on
// that resolution detail. Verified in a real page (not just compiled): see
// this task's report for what document.styleSheets showed once
// tokens.Emit() actually had a caller.
func emitShellStyles() {
	// #app: the flex-column shell root. This used to be the <style> block's
	// "#app { min-height: 100%; display: flex; flex-direction: column; }" —
	// moved here because it is this package's own root, not a document-level
	// reset every page needs before WASM boots (contrast with index.html's
	// surviving html,body margin/height reset, which the noscript fallback
	// and the pre-WASM paint both need regardless of what mounts into #app).
	css.Global("#app",
		css.Display.Flex,
		css.FlexDir.Col,
		css.MinHeight(css.Length("100%")),
		bodyFont(),
		css.FontSize(tokens.FontSize(tokens.TextBase)),
		css.TextColor(tokens.Color(tokens.RoleText)),
		css.Bg(tokens.Color(tokens.RoleBg)),
	)

	// .af-banner: the DISCONNECTED reconnect banner (banner.go). Danger-role
	// fill (this is the one "something is wrong" surface in the whole shell,
	// not a warning) — the old <style> block hardcoded #7a1f1f/white, which
	// does not adapt to the dark palette; this reads tokens.RoleDanger/
	// RoleDangerFg so both themes stay within the token layer's own
	// WCAG-checked contrast pairs.
	css.Global(".af-banner",
		css.PaddingY(tokens.Space(2)),
		css.PaddingX(tokens.Space(4)),
		css.Bg(tokens.Color(tokens.RoleDanger)),
		css.TextColor(tokens.Color(tokens.RoleDangerFg)),
		css.TextAlign.Center,
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		bodyFont(),
	)
	css.Global(".af-banner--hidden", css.Display.None)
	css.Global(".af-banner--visible", css.Display.Block)

	// .af-notice: the "here is why you are looking at this screen" line
	// (sessionroute.go's renderRedirectNotice). Deliberately NOT the banner's
	// danger fill: an expired session is ordinary and expected, not a fault,
	// and painting it the same red as "the control plane is unreachable"
	// would teach the operator to read that red as noise. Surface fill with a
	// muted accent rule instead — informational weight.
	css.Global(".af-notice",
		css.PaddingY(tokens.Space(2)),
		css.PaddingX(tokens.Space(4)),
		css.Bg(tokens.Color(tokens.RoleSurface)),
		css.TextColor(tokens.Color(tokens.RoleText)),
		css.BorderBottom(css.Px(1), tokens.Color(tokens.RoleAccent)),
		css.TextAlign.Center,
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		bodyFont(),
	)

	// .af-expiry-modal: D0-08's blocking "you have unsaved work" hold.
	// expiry.go never renders this with the --hidden class (a closed modal
	// renders h.Fragment(), nothing at all — see that file's own doc comment
	// on why, a real bug fixed earlier in this task's history), so
	// ".af-expiry-modal--hidden" is not declared here: the old <style>
	// block's "display:none" for it was dead CSS the moment expiry.go
	// stopped emitting the class, and reintroducing it would just be a rule
	// nothing ever selects.
	css.Global(".af-expiry-modal",
		css.Position.Fixed,
		css.Inset(css.Zero),
		css.Bg(tokens.Color(tokens.RoleScrim)),
		css.Display.Flex,
		css.Items.Center,
		css.Justify.Center,
		css.ZIndex(1000),
	)
	css.Global(".af-expiry-modal > *",
		css.Bg(tokens.Color(tokens.RoleSurfaceRaised)),
		css.TextColor(tokens.Color(tokens.RoleText)),
		css.Padding(tokens.Space(6)),
		// Radius 3px, uniformly (docs/design-direction.md "Layout"). RadiusSm
		// (0.25rem = 4px) is the closest token web/tokens exposes today — this
		// package does not own web/tokens, so a true 3px token is out of
		// reach here; noted for whoever next tunes that scale.
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.MaxWidth(css.Length("24rem")),
		css.Raw("box-shadow", tokens.Shadow(tokens.ShadowLg)),
		bodyFont(),
	)
	css.Global(".af-expiry-modal p",
		css.FontSize(tokens.FontSize(tokens.TextBase)),
	)
	css.Global(".af-expiry-modal button",
		css.Display.InlineFlex,
		css.Items.Center,
		css.Justify.Center,
		css.Raw("margin-top", string(tokens.Space(4))),
		css.Padding(tokens.Space(2)),
		css.PaddingX(tokens.Space(4)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)), // 3px-uniform, see above
		css.Border(css.Px(1), tokens.Color(tokens.RoleAccent)),
		css.Bg(tokens.Color(tokens.RoleAccent)),
		css.TextColor(tokens.Color(tokens.RoleAccentFg)),
		css.FontSize(tokens.FontSize(tokens.TextBase)),
		css.FontWeight.Medium,
		css.Cursor.Pointer,
		bodyFont(),
		transitionColors(),
	)
	css.Global(".af-expiry-modal button:hover", css.OpacityNum(css.Num(0.9)))
	css.Global(".af-expiry-modal button:focus-visible",
		css.Raw("outline", "2px solid "+string(tokens.Color(tokens.RoleFocusRing))),
		css.Raw("outline-offset", "2px"),
	)

	// .af-content: the content frame every route's page body renders inside
	// (pages.go's renderShellWrapper wraps renderPageBody in <main
	// class="af-content">). "Rules, not cards" (design-direction.md
	// "Layout"): no border, no fill of its own — the header's own
	// border-bottom above it is the one hairline that separates chrome from
	// content, so a second rule here would be a redundant box around a box.
	css.Global(".af-content", css.Raw("flex", "1"), css.Padding(tokens.Space(6)))

	// .af-placeholder: pages.go's fallback body for any route without a
	// real page registered yet.
	css.Global(".af-placeholder",
		css.OpacityNum(css.Num(0.7)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		bodyFont(),
	)

	// [hidden] must beat every class in this stylesheet, and that is not
	// belt-and-braces — it is load-bearing given how this framework hides
	// things. h.Show(false, node) does NOT drop the node: it clones it with
	// `hidden` set (GoWebComponents/v5 html/sugar.go's Show) and relies on
	// the UA sheet's `[hidden] { display: none }`. That rule is a single
	// type-less selector, so ANY class rule here that sets `display` wins
	// against it on specificity, and the "hidden" node renders in full.
	// .af-header__rule (display:inline-block) is exactly that shape, and
	// every future `css.Display.*` rule in this app is one h.Show away from
	// the same silent bug. Declared once, globally, so it cannot be
	// rediscovered per component.
	css.Global("[hidden]", css.Raw("display", "none !important"))

	emitHeaderStyles()
}

// emitHeaderStyles declares .af-header and its descendants (header.go): the
// brand lockup, the three-item nav, and the sign-out control. Split out of
// emitShellStyles' body only for readability — still called from Mount via
// that function, same "tokens first" ordering guarantee.
func emitHeaderStyles() {
	// .af-header: the top bar. Bg matches the page (RoleBg, not a filled
	// RoleSurface) — "reserve filled surfaces for genuinely raised things"
	// (design-direction.md "Layout") — separated from the content below it
	// by exactly one hairline in the `rule` role (RoleBorder), per "Separate
	// with hairlines in rule."
	css.Global(".af-header",
		css.Display.Flex,
		css.FlexWrap.Wrap,
		css.Items.Center,
		css.Justify.Between,
		css.Gap(tokens.Space(4)),
		css.PaddingX(tokens.Space(4)),
		css.PaddingY(tokens.Space(3)),
		css.Bg(tokens.Color(tokens.RoleBg)),
		css.TextColor(tokens.Color(tokens.RoleText)),
		css.BorderBottom(css.Px(1), tokens.Color(tokens.RoleBorder)),
		bodyFont(),
	)

	// .af-header__brand: the mark+wordmark lockup (header.go's
	// afLogoMarkSVG + the HTML wordmark spans — see that file's doc comment
	// on why the wordmark is HTML, not baked into the SVG). Rendered as
	// either a <div> (ANON/ELEVATED, not linkable) or an <a> (everyone
	// else) — both share this class, so the rule must not assume either
	// tag.
	css.Global(".af-header__brand",
		css.Display.Flex,
		css.Items.Center,
		css.Gap(tokens.Space(2)),
		css.Raw("text-decoration", "none"),
		css.TextColor(tokens.Color(tokens.RoleText)),
	)
	css.Global("a.af-header__brand", css.Cursor.Pointer)
	// .af-header__mark: the crest, an <img> since 2026-08-10 (see
	// header.go's markSrc). Width AND height are both stated: the asset is
	// square, but a raster <img> with only one dimension set reflows the
	// whole header the instant it decodes, and the header is the first
	// thing painted on every route. drop-shadow reinstates the glow the
	// source render carries -- it is deliberately NOT baked into the PNG,
	// because a baked glow is one fixed colour and radius on every surface,
	// whereas this one is a token and follows the theme.
	css.Global(".af-header__mark",
		css.Display.Block,
		css.W(css.Px(26)),
		css.H(css.Px(26)),
		css.Raw("flex", "0 0 auto"),
		// The token is a var() reference, so it cannot carry a concatenated
		// alpha suffix (`var(--x)55` is not a colour) — color-mix is how a
		// custom property gets transparency without a second token.
		css.Raw("filter", "drop-shadow(0 0 4px color-mix(in srgb, "+string(tokens.Color(tokens.RoleAccent))+" 45%, transparent))"),
	)
	// .af-header__rule: the vertical column-rule divider between mark and
	// wordmark — design-direction.md's "Signature" device borrowed at
	// miniature scale. Moved off `danger` onto `accent` 2026-08-10 with the
	// brand swap: a divider drawn in the destructive-action colour was
	// borrowing the one swatch reserved for "this deletes something", and
	// the old mark it was matched to no longer exists.
	css.Global(".af-header__rule",
		css.Display.InlineBlock,
		css.W(css.Px(2)),
		css.H(css.Px(20)),
		css.Bg(tokens.Color(tokens.RoleAccent)),
		css.Raw("flex", "0 0 auto"),
	)
	// .af-header__wordmark: "AnimeFeedFlux" in `currentColor` via ordinary
	// CSS inheritance from .af-header__brand's own `color` — the same
	// theme-inversion effect web/static/logo.svg's `fill="currentColor"`
	// documents, achieved through HTML instead of SVG text (see
	// afLogoMarkSVG's doc comment for why). Tight tracking + the same
	// system sans the rest of the shell uses (design-direction.md "Type":
	// "Display and body: the system sans stack, set tight (-0.01em) at
	// headings") — this IS effectively a small heading, the one place the
	// brand name is spelled out.
	css.Global(".af-header__wordmark",
		css.FontSize(tokens.FontSize(tokens.TextMd)),
		css.FontWeight.Bold,
		css.Tracking(css.Ems(-0.01)),
		bodyFont(),
	)
	css.Global(".af-header__wordmark-strong", css.FontWeight.Bold)
	// "Feed" is set in the accent rather than dimmed, following the brand
	// artwork's own three-tone wordmark (Anime / Feed / Flux in ink, blue,
	// violet). Only the middle tone is reproduced: the artwork's violet has
	// no token, and inventing a one-off swatch for a single word is how a
	// palette stops being a palette.
	css.Global(".af-header__wordmark-light",
		css.FontWeight.Normal,
		css.TextColor(tokens.Color(tokens.RoleAccent)),
	)
	css.Global("a.af-header__brand:focus-visible",
		css.Raw("outline", "2px solid "+string(tokens.Color(tokens.RoleFocusRing))),
		css.Raw("outline-offset", "2px"),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
	)

	// .af-header__nav: the three destinations. Micro-label treatment
	// (design-direction.md "Type": "uppercase, 0.08em tracking, muted" —
	// "these are the timesheet's printed column headers") fits a fixed
	// three-item wayfinding row better than body prose would.
	css.Global(".af-header__nav-list",
		css.Display.Flex,
		css.Items.Center,
		css.Gap(tokens.Space(5)),
		css.Raw("list-style", "none"),
		css.Margin(css.Zero),
		css.Padding(css.Zero),
	)
	css.Global(".af-header__nav-link",
		css.Display.InlineFlex,
		css.Items.Center,
		css.PaddingY(tokens.Space(1)),
		css.Raw("text-decoration", "none"),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.TextTransform.Uppercase,
		css.Tracking(css.Ems(0.08)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		css.BorderBottom(css.Px(2), css.Transparent),
		transitionColors(),
	)
	// Active route: ink text, a pencil-accent underline rule — the one
	// place this header borrows the timesheet's "column rule" device, in
	// the accent (pencil/link) color, never redline (redline is reserved
	// for exactly one boundary per view, and it is not this one).
	css.Global(".af-header__nav-link--active",
		css.TextColor(tokens.Color(tokens.RoleText)),
		css.BorderBottom(css.Px(2), tokens.Color(tokens.RoleAccent)),
	)
	css.Global("a.af-header__nav-link:hover",
		css.TextColor(tokens.Color(tokens.RoleAccent)),
	)
	css.Global("a.af-header__nav-link:focus-visible",
		css.Raw("outline", "2px solid "+string(tokens.Color(tokens.RoleFocusRing))),
		css.Raw("outline-offset", "2px"),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
	)
	// --inert: the DISCONNECTED rendering (header.go's renderHeaderNavItem,
	// interactive=false). A <span>, not an <a> — pointer-events:none here
	// is redundant defense-in-depth, not the primary mechanism (the
	// primary mechanism is "there is no href and no click handler at all").
	css.Global(".af-header__nav-link--inert",
		css.OpacityNum(css.Num(0.5)),
		css.Cursor.NotAllowed,
		css.Raw("pointer-events", "none"),
	)
	// --here: the DISCONNECTED variant still marks which route is current,
	// just without the accent (an accent-colored underline on an inert
	// control would itself look like "this still works").
	css.Global(".af-header__nav-link--inert.af-header__nav-link--here",
		css.TextColor(tokens.Color(tokens.RoleText)),
	)

	// .af-header__signout: the always-visible sign-out control. A quiet
	// text button, not a filled one — the header's one interactive control
	// that is not navigation gets the least visual weight in the bar, not
	// the most.
	css.Global(".af-header__signout-btn",
		css.Bg(css.Transparent),
		css.Border(css.Px(0), css.Transparent),
		css.PaddingY(tokens.Space(1)),
		css.PaddingX(tokens.Space(2)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.TextTransform.Uppercase,
		css.Tracking(css.Ems(0.08)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		css.Cursor.Pointer,
		bodyFont(),
		transitionColors(),
	)
	css.Global(".af-header__signout-btn:hover:not(:disabled)",
		css.TextColor(tokens.Color(tokens.RoleText)),
	)
	css.Global(".af-header__signout-btn:disabled",
		css.OpacityNum(css.Num(0.5)),
		css.Cursor.NotAllowed,
	)
	css.Global(".af-header__signout-btn:focus-visible",
		css.Raw("outline", "2px solid "+string(tokens.Color(tokens.RoleFocusRing))),
		css.Raw("outline-offset", "2px"),
	)
	// The .af-header__theme* rules that stood here were removed 2026-08-11
	// with the control they styled (header.go's renderThemeControl). The
	// segmented-control treatment they defined was not lost: it moved to
	// web/pages/settings/styles.go as .af-appearance__segment*, which is
	// where the theme and language controls now live.
	//
	// One layout detail went with them and is worth stating: that group
	// carried margin-inline-start:auto, which is what pushed it and sign-out
	// to the right edge. .af-header is justify-content:space-between and now
	// has exactly three children (brand, nav, sign-out), so the same
	// right-alignment falls out of the flex distribution without anyone
	// asking for it.

	css.Global(".af-header__signout-error",
		css.FontSize(tokens.FontSize(tokens.TextXs)),
		css.TextColor(tokens.Color(tokens.RoleDanger)), // redline — this is the one boundary this view marks
		css.Raw("margin-top", string(tokens.Space(1))),
		css.Raw("margin-block-end", "0"),
		bodyFont(),
	)
}

// bodyFont / transitionColors mirror web/ui/base.go's identical helpers.
// Duplicated rather than imported: those are unexported in package ui, and
// this package is not on the allowed-edit list for web/ui, so this small,
// self-contained copy is the right shape rather than widening this task's
// footprint into a package it does not own.
func bodyFont() css.Rule { return css.Font(tokens.FontFamily(tokens.FontSans)) }

func transitionColors() css.Rule {
	return css.Transition(css.PropColors, tokens.Duration(tokens.DurationFast), tokens.Easing(tokens.EasingStd))
}
