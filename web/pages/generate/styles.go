//go:build js && wasm

// styles.go declares every rule the /generate page's own markup needs — the
// three-pane layout (render.go), the rail's feed list (render_rail.go), the
// editor's fields/fieldsets/conflict panel (render_editor.go), and the
// sampler's controls/candidate views (render_sampler.go). PLAN.md §12.6:
// "all styling is authored in Go... GWC v5's typed css package" — every
// color/space/radius/duration here reads a tokens.* Var, never a literal.
//
// Two things this file is NOT responsible for: web/ui primitives (Button,
// Input, Select, Toggle, Tabs, StatePanel — this page already adopted them;
// restyling them here would be the "two sources of truth" mistake this
// task's brief calls out) and index.html/web/shell (owned elsewhere). What
// remains is this page's own layout and the handful of plain tags
// (fieldset/legend/textarea/label) it renders that have no web/ui
// equivalent — styled the same way web/pages/auth/styles.go styles login's
// plain tags: by class name, and by descendant selector scoped under the
// one class the relevant pane always carries.
//
// Called once from an init() below — css.Global dedupes by (selector+rules)
// content, so a second call is a no-op, not a duplicate rule (see the
// vendored css package's doc comment on Global, and web/pages/auth/styles.go's
// identical note).
package generatepage

import (
	"github.com/monstercameron/GoWebComponents/v5/css"

	"github.com/monstercameron/AnimeFeedFlux/web/tokens"
	wui "github.com/monstercameron/AnimeFeedFlux/web/ui"
)

func emitGenerateStyles() {
	// .af-generate: the three-pane grid — rail | editor | sampler. Below
	// wui.NarrowMaxWidth the panes stack (D5-01/§12.6: "responsive
	// breakpoints land in the same commit as the layout"), since a fixed
	// three-column grid below ~640px would squeeze every pane to
	// illegibility rather than genuinely adapting.
	css.Global(".af-generate",
		css.Raw("display", "grid"),
		css.Raw("grid-template-columns", "18rem minmax(0, 1fr) 22rem"),
		css.Gap(tokens.Space(4)),
		css.Raw("align-items", "start"),
		bodyFont(),
		css.FontSize(tokens.FontSize(tokens.TextBase)),
		css.TextColor(tokens.Color(tokens.RoleText)),
	)
	css.Global(".af-generate", narrowMedia(
		css.Raw("grid-template-columns", "1fr"),
	)...)

	// .af-generate--not-wired: the zero-configuration placeholder (render.go's
	// renderNotWired) — a single centered notice rather than a blank grid.
	css.Global(".af-generate--not-wired",
		css.Raw("display", "block"),
		css.Padding(tokens.Space(6)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		bodyFont(),
	)

	// These three classes were the three-pane layout's own standalone panes
	// (a hairline-bordered, RadiusSm, surface-filled "card" each — the
	// `paneBase` this comment used to describe). The workbench rebuild
	// (render_workbench.go) nested every one of them — the feed list inside
	// `.af-gen__feeds`'s <details>, the recipe form inside `.af-gen__recipe`'s
	// <details>, the sampler inside `.af-gen__preview`'s own column — without
	// dropping their old panel styling, which is exactly the "box nested
	// inside a box" docs/design-direction.md's "Rules, not cards" argues
	// against: every one of those three sections got a bordered, radius'd,
	// surface-filled card floating inside a section that already draws its
	// own boundary. Flagged directly ("I still don't like this page")
	// 2026-08-15 and traced here. Fixed: these are layout-only now — no
	// border, no radius, no fill, no independent scroll region — and flow as
	// plain content within whichever workbench section already contains
	// them. The workbench's own sections (`.af-gen__feeds`/`.af-gen__work`/
	// `.af-gen__recipe`, below) are the only boundaries this page draws now.
	paneLayout := []css.Rule{
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(4)),
	}
	css.Global(".af-generate__rail", paneLayout...)
	css.Global(".af-generate__editor", paneLayout...)
	css.Global(".af-generate__sampler", paneLayout...)
	// The "no feed selected"/"select or save a feed" placeholder bodies
	// (renderEditorEmpty/renderSamplerEmpty) reuse the same pane classes, so
	// their muted copy needs no separate rule beyond the shared pane look.
	css.Global(".af-generate__editor p, .af-generate__sampler p",
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
	)

	emitRailStyles()
	emitEditorStyles()
	emitSamplerStyles()
}

// --- Rail ------------------------------------------------------------------

func emitRailStyles() {
	css.Global(".af-rail__header",
		css.Display.Flex,
		css.Items.Center,
		css.Justify.Between,
		css.Gap(tokens.Space(2)),
		css.W(css.Length("100%")),
		css.Raw("min-width", "0"),
		css.Raw("padding-bottom", string(tokens.Space(2))),
		css.BorderBottom(css.Px(1), tokens.Color(tokens.RoleBorder)),
	)
	css.Global(".af-rail__header-title",
		css.Raw("flex", "1 1 auto"),
		css.Raw("min-width", "0"),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.FontWeight.Semibold,
		css.TextColor(tokens.Color(tokens.RoleText)),
		css.Raw("overflow", "hidden"),
		css.Raw("text-overflow", "ellipsis"),
		css.Raw("white-space", "nowrap"),
	)
	// The kill-switch banner is the one place §12.3 insists a disabled
	// control's REASON never reads as an afterthought — a filled danger
	// surface plus a redline-weight left rule (louder than the ordinary
	// 1px hairline everything else on this page uses) rather than the
	// same quiet treatment as any other status line, since a dead control
	// with no legible reason "is indistinguishable from a broken app".
	css.Global(".af-rail__kill-banner",
		css.Display.Flex,
		css.Items.Center,
		css.Gap(tokens.Space(2)),
		css.Padding(tokens.Space(3)),
		css.PaddingX(tokens.Space(4)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Raw("border-left", "3px solid "+string(tokens.Color(tokens.RoleDangerFg))),
		css.Bg(tokens.Color(tokens.RoleDanger)),
		css.TextColor(tokens.Color(tokens.RoleDangerFg)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.FontWeight.Semibold,
	)
	css.Global(".af-rail__action-error",
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.TextColor(tokens.Color(tokens.RoleDanger)),
	)
	// The list itself is edge to edge — no gap between rows, because the
	// gap is what made each row read as a floating card. What actually
	// separates one row from the next now is the ruled bottom hairline each
	// row draws on itself (below), the same device a real timesheet uses
	// between frames.
	//
	// This list is not internally capped or paginated — two earlier passes
	// tried both (a nested `overflow-y: auto` scrollbox, then client-side
	// pagination) to answer "what if you had 20 feeds", and both were
	// symptom patches on a single-column skeleton that put a feed roster
	// and a feed's own work area in the same vertical flow. Now the whole
	// list — `.af-gen__sidebar`, below — is its own column with its own
	// scroll, sibling to `.af-gen__main`, so it just scrolls, the way an
	// inbox list does, needing neither a cap nor a pager.
	css.Global(".af-rail__list",
		css.Raw("list-style", "none"),
		css.Padding(css.Zero),
		css.Margin(css.Zero),
		css.Display.Flex,
		css.FlexDir.Col,
		css.W(css.Length("100%")),
		css.Raw("min-width", "0"),
	)
	// A row is a ruled frame in a sheet, not a card floating in a list
	// (docs/design-direction.md: "Rules, not cards"). No border-box, no
	// radius, no per-row fill — a bottom hairline is the only rule between
	// rows, full width, and the left edge carries a 3px accent bar (kept
	// transparent at rest) for selection/staleness — the vertical "column
	// divider" the direction doc reserves for exactly one boundary per view.
	css.Global(".af-rail__row",
		css.Display.Flex,
		css.Items.Center,
		css.Gap(tokens.Space(2)),
		css.W(css.Length("100%")),
		css.Raw("min-width", "0"),
		css.Raw("box-sizing", "border-box"),
		css.PaddingY(tokens.Space(2)),
		css.PaddingX(tokens.Space(3)),
		css.Raw("border-left", "3px solid transparent"),
		css.BorderBottom(css.Px(1), tokens.Color(tokens.RoleBorder)),
		css.Cursor.Pointer,
		transitionColors(),
	)
	css.Global(".af-rail__list > .af-rail__row:first-child",
		css.BorderTop(css.Px(1), tokens.Color(tokens.RoleBorder)),
	)
	css.Global(".af-rail__row:hover", css.Bg(tokens.Color(tokens.RoleSurface)))
	css.Global(".af-rail__row--selected",
		css.Raw("border-left", "3px solid "+string(tokens.Color(tokens.RoleAccent))),
		css.Bg(tokens.Color(tokens.RoleSurface)),
	)
	css.Global(".af-rail__row--disabled", css.OpacityNum(css.Num(0.6)))
	css.Global(".af-rail__row--stale",
		css.Raw("border-left", "3px solid "+string(tokens.Color(tokens.RoleWarning))),
	)
	css.Global(".af-rail__row--selected.af-rail__row--stale",
		// Selection and staleness both claim the same left rule; a
		// selected+stale row keeps the accent (its selection is the more
		// transient fact of the two) but the stale flag chip below still
		// carries the warning color, so the information is never lost —
		// only which rule wins the one column this project reserves for a
		// single boundary.
		css.Raw("border-left", "3px solid "+string(tokens.Color(tokens.RoleAccent))),
	)
	// The compact row is a single line pair (title, then meta), laid out as
	// a row (`.af-rail__row` above): status dot, then body (title-line +
	// meta stacked), then actions pinned right. The dot replaces the old
	// stale-flag CHIP as the row's health indicator — one glance, no line
	// spent on a pill.
	css.Global(".af-rail__row-dot",
		css.Raw("flex", "0 0 auto"),
		css.W(css.Px(8)),
		css.H(css.Px(8)),
		css.Rounded(tokens.Radius(tokens.RadiusFull)),
		css.Bg(tokens.Color(tokens.RoleTextMuted)),
	)
	css.Global(".af-rail__row--stale .af-rail__row-dot", css.Bg(tokens.Color(tokens.RoleWarning)))
	css.Global(".af-rail__row--disabled .af-rail__row-dot", css.Bg(tokens.Color(tokens.RoleBorder)))
	css.Global(".af-rail__row-body",
		css.Display.Flex,
		css.FlexDir.Col,
		css.Raw("flex", "1 1 auto"),
		css.Raw("min-width", "0"),
		css.Gap(css.Length("0.125rem")),
	)
	// The number reads first — "row 1 of the sheet", the same way a genga
	// sheet's frame numbers lead each row (docs/design-direction.md's
	// anchor).
	css.Global(".af-rail__row-title-line",
		css.Display.Flex,
		css.Items.Baseline,
		css.Gap(tokens.Space(2)),
		css.W(css.Length("100%")),
		css.Raw("min-width", "0"),
	)
	css.Global(".af-rail__row-number",
		css.Raw("flex", "0 0 auto"),
		css.MinWidth(css.Length("1.5em")),
		css.Raw("text-align", "right"),
		css.FontSize(tokens.FontSize(tokens.TextXs)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		css.Raw("font-variant-numeric", "tabular-nums"),
		monoFont(),
	)
	css.Global(".af-rail__row-title",
		css.FontWeight.Semibold,
		css.TextColor(tokens.Color(tokens.RoleText)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.Raw("flex", "1 1 auto"),
		css.Raw("min-width", "0"),
		css.Raw("overflow", "hidden"),
		css.Raw("text-overflow", "ellipsis"),
		css.Raw("white-space", "nowrap"),
	)
	// The meta line combines slug + relative last-build into one string
	// (generate.rail.compactMeta) rather than the old separate slug/spend/
	// next-run lines — those either duplicated the strip's own stakes line
	// or, for "next run", always read "unavailable" and earned no space.
	css.Global(".af-rail__row-meta",
		css.Display.Block,
		css.FontSize(tokens.FontSize(tokens.TextXs)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		css.Raw("overflow", "hidden"),
		css.Raw("text-overflow", "ellipsis"),
		css.Raw("white-space", "nowrap"),
		monoFont(),
	)
	css.Global(".af-rail__row-actions",
		css.Display.Flex,
		css.Items.Center,
		css.Gap(tokens.Space(1)),
		css.Raw("flex", "0 0 auto"),
	)

	emitTapeStyles()
}

// --- The tape ----------------------------------------------------------------
//
// docs/design-direction.md, "Signature: the tape" — a horizontal strip per
// feed, ticks positioned by published_at, spent boldness relative to
// everything around it. render_rail.go's renderTape builds one of these per
// row; every rule here is shape only (strip height, baseline, tick
// dimensions/color) — the per-tick horizontal position is inline (h.Style)
// since it is per-item DATA, not a class-level style.
func emitTapeStyles() {
	// "Spend the boldness here. Everything around it stays quiet."
	// (docs/design-direction.md). The tape was a 20px sliver squeezed
	// between the title line and the meta line — legible, but nowhere near
	// bold enough to be the thing this whole visual direction is built
	// around. Given its own row and a visible edge-to-edge baseline instead
	// of a 1px hairline glimpsed at the bottom of a cramped strip. Settled
	// at 1.75rem, not the first pass's 2.25rem — that read well in
	// isolation but cost too much of the row list's now-capped height
	// (`.af-rail__list`'s `max-height`, added the same day for "what if you
	// had 20 feeds") for what it added over 1.75rem; this is the width
	// where it still reads as the boldest mark in the row without being the
	// reason only three rows fit in the scroll window.
	css.Global(".af-tape",
		css.Raw("position", "relative"),
		css.W(css.Length("100%")),
		css.H(css.Length("1.75rem")),
		css.Raw("margin", string(tokens.Space(1))+" 0"),
	)
	css.Global(".af-tape__baseline",
		css.Raw("position", "absolute"),
		css.Raw("left", "0"),
		css.Raw("right", "0"),
		css.Raw("bottom", "0"),
		css.H(css.Px(2)),
		css.Bg(tokens.Color(tokens.RoleBorder)),
	)
	// Ticks are the one bold, saturated mark on this whole page — the
	// accent color, full height of the strip, thin enough that a dense run
	// of them still reads as individual marks rather than a solid bar
	// (docs/design-direction.md: "ticks march forward and never collide").
	css.Global(".af-tape__tick",
		css.Raw("position", "absolute"),
		css.Raw("top", "0"),
		css.Raw("bottom", "0"),
		css.W(css.Px(3)),
		css.Raw("transform", "translateX(-1.5px)"),
		css.Bg(tokens.Color(tokens.RoleAccent)),
		css.Rounded(tokens.Radius(tokens.RadiusFull)),
	)
}

// --- Editor ------------------------------------------------------------------

func emitEditorStyles() {
	css.Global(".af-generate__editor fieldset",
		css.Padding(tokens.Space(3)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Border(css.Px(1), tokens.Color(tokens.RoleBorder)),
		css.Raw("margin", "0"),
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(3)),
	)
	css.Global(".af-generate__editor legend",
		css.PaddingX(tokens.Space(2)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.FontWeight.Semibold,
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
	)

	css.Global(".af-field",
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(1)),
	)
	css.Global(".af-field label",
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.FontWeight.Medium,
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
	)
	css.Global(".af-field textarea",
		css.Display.Block,
		css.W(css.Length("100%")),
		css.Raw("box-sizing", "border-box"),
		css.Raw("min-height", "6rem"),
		css.Raw("resize", "vertical"),
		css.Padding(tokens.Space(2)),
		css.PaddingX(tokens.Space(3)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Border(css.Px(1), tokens.Color(tokens.RoleBorder)),
		css.Bg(tokens.Color(tokens.RoleBg)),
		css.TextColor(tokens.Color(tokens.RoleText)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		monoFont(),
		transitionColors(),
	)
	css.Global(".af-field textarea:focus-visible", focusOutline()...)
	css.Global(".af-field-error",
		css.FontSize(tokens.FontSize(tokens.TextXs)),
		css.TextColor(tokens.Color(tokens.RoleDanger)),
	)
	css.Global(".af-field-hint",
		css.FontSize(tokens.FontSize(tokens.TextXs)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		css.Raw("margin", "0"),
	)
	css.Global(".af-form-error",
		css.Padding(tokens.Space(2)),
		css.PaddingX(tokens.Space(3)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Bg(tokens.Color(tokens.RoleDanger)),
		css.TextColor(tokens.Color(tokens.RoleDangerFg)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
	)
	css.Global(".af-editor__actions",
		css.Display.Flex,
		css.FlexWrap.Wrap,
		css.Items.Center,
		css.Gap(tokens.Space(2)),
		css.Raw("border-top", "1px solid "+string(tokens.Color(tokens.RoleBorder))),
		css.Raw("padding-top", string(tokens.Space(3))),
	)
	// The Validate/Save-error banners (.af-form-error, shared with the
	// candidate-list error styling above) sit inside this row too — forcing
	// them to their own line rather than squeezing between the two buttons.
	css.Global(".af-editor__actions > .af-form-error",
		css.Raw("flex", "1 1 100%"),
	)

	// Grounded sources list.
	css.Global(".af-sources",
		css.Raw("list-style", "none"),
		css.Padding(css.Zero),
		css.Margin(css.Zero),
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(2)),
	)
	css.Global(".af-sources li",
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(2)),
		css.Padding(tokens.Space(2)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Border(css.Px(1), tokens.Color(tokens.RoleBorder)),
	)

	// Version-conflict panel: warning-scoped, not danger — a save collision
	// is an operator decision to make, not a failure.
	css.Global(".af-conflict",
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(3)),
		css.Padding(tokens.Space(3)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Bg(tokens.Color(tokens.RoleSurfaceRaised)),
		css.Border(css.Px(1), tokens.Color(tokens.RoleWarning)),
	)
	css.Global(".af-conflict__headline",
		css.FontWeight.Semibold,
		css.TextColor(tokens.Color(tokens.RoleWarning)),
		css.Raw("margin", "0"),
	)
	css.Global(".af-conflict__choices",
		css.Display.Flex,
		css.Gap(tokens.Space(2)),
		css.FlexWrap.Wrap,
	)
	css.Global(".af-conflict__fields",
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(2)),
	)
	css.Global(".af-conflict__rows",
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(2)),
	)
	css.Global(".af-conflict__field",
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(css.Length("0.125rem")),
		css.Padding(tokens.Space(2)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Bg(tokens.Color(tokens.RoleBg)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
	)
}

// --- Sampler -----------------------------------------------------------------

func emitSamplerStyles() {
	css.Global(".af-sampler__controls",
		css.Display.Flex,
		// A row, not a column: what is left here is a caption and an
		// occasional Cancel, and stacking two short items wasted the top of
		// the output pane on chrome.
		css.Items.Center,
		css.Justify.Between,
		css.Raw("flex-wrap", "wrap"),
		css.Gap(tokens.Space(3)),
		css.Raw("padding-bottom", string(tokens.Space(3))),
		css.Raw("border-bottom", "1px solid "+string(tokens.Color(tokens.RoleBorder))),
	)
	css.Global(".af-sampler__budget",
		css.FontSize(tokens.FontSize(tokens.TextXs)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		monoFont(),
	)
	// §12.3: "the kill switch disabled state must show its REASON
	// prominently — a dead control with no explanation is indistinguishable
	// from a broken app." This sits directly above the (now-disabled)
	// Sample button, filled and left-ruled the same weight as the rail's
	// kill banner, not a quiet caption easy to miss on the way to a
	// greyed-out button.
	css.Global(".af-sampler__disabled-reason",
		css.Display.Block,
		css.Padding(tokens.Space(3)),
		css.PaddingX(tokens.Space(4)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Raw("border-left", "3px solid "+string(tokens.Color(tokens.RoleWarningFg))),
		css.Bg(tokens.Color(tokens.RoleWarning)),
		css.TextColor(tokens.Color(tokens.RoleWarningFg)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.FontWeight.Semibold,
	)
	css.Global(".af-sampler__results",
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(3)),
	)

	css.Global(".af-candidate",
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(3)),
	)
	css.Global(".af-candidate__content",
		css.Display.Block,
		css.W(css.Length("100%")),
		css.Raw("box-sizing", "border-box"),
		css.Raw("max-height", "16rem"),
		css.OverflowY.Auto,
		css.Padding(tokens.Space(3)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Bg(tokens.Color(tokens.RoleBg)),
		css.Border(css.Px(1), tokens.Color(tokens.RoleBorder)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.Raw("white-space", "pre-wrap"),
		css.Raw("overflow-wrap", "anywhere"),
		monoFont(),
	)
	// The embed preview's frame. It matches .af-candidate__content's box —
	// same width, radius, border — so switching tabs does not make the panel
	// jump, but it is a fixed height rather than a max-height: the framed
	// document sizes itself to its frame (the embed's own list scrolls
	// inside it, PLAN.md §6.1), so a height that collapsed to content would
	// be a frame with nothing to render into.
	//
	// No background of its own: the embed paints its own, in whichever
	// scheme the operator's browser reports, and a token background behind
	// it would show as a mismatched border ring in one of the two themes.
	css.Global(".af-candidate__embed",
		css.Display.Block,
		css.W(css.Length("100%")),
		css.Raw("box-sizing", "border-box"),
		css.Raw("height", "22rem"),
		css.Raw("border", "0"),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Raw("outline", "1px solid "+string(tokens.Color(tokens.RoleBorder))),
		css.Raw("outline-offset", "-1px"),
	)
	css.Global(".af-candidate__novelty",
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.TextColor(tokens.Color(tokens.RoleText)),
	)
	css.Global(".af-candidate__links",
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(1)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
	)
	css.Global(".af-link-verdict",
		monoFont(),
		css.FontSize(tokens.FontSize(tokens.TextXs)),
		css.TextColor(tokens.Color(tokens.RoleSuccess)),
		css.Raw("overflow-wrap", "anywhere"),
	)
	css.Global(".af-link-verdict--failed", css.TextColor(tokens.Color(tokens.RoleDanger)))
	css.Global(".af-candidate__links-failed",
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.FontWeight.Medium,
		css.TextColor(tokens.Color(tokens.RoleDanger)),
	)
	css.Global(".af-candidate__cost",
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		monoFont(),
	)
	css.Global(".af-candidate__action-error",
		css.Padding(tokens.Space(2)),
		css.PaddingX(tokens.Space(3)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Bg(tokens.Color(tokens.RoleDanger)),
		css.TextColor(tokens.Color(tokens.RoleDangerFg)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
	)
	css.Global(".af-candidate__actions",
		css.Display.Flex,
		css.Gap(tokens.Space(2)),
		css.FlexWrap.Wrap,
	)
}

// bodyFont/monoFont/transitionColors/focusOutline mirror web/ui/base.go's
// identical helpers. Duplicated rather than imported: those are unexported
// in package ui, and this page is not on this task's allowed-edit list for
// web/ui — same reasoning as web/shell/styles.go and
// web/pages/auth/styles.go's identical duplication note.
func bodyFont() css.Rule { return css.Font(tokens.FontFamily(tokens.FontSans)) }
func monoFont() css.Rule { return css.Font(tokens.FontFamily(tokens.FontMono)) }

func transitionColors() css.Rule {
	return css.Transition(css.PropColors, tokens.Duration(tokens.DurationFast), tokens.Easing(tokens.EasingStd))
}

func focusOutline() []css.Rule {
	return []css.Rule{
		css.Raw("outline", "2px solid "+string(tokens.Color(tokens.RoleFocusRing))),
		css.Raw("outline-offset", "2px"),
	}
}

// narrowMedia scopes rules to wui.NarrowMaxWidth and below, matching
// web/ui/responsive.go's own narrowMedia — reusing the exported constant
// rather than a second magic number keeps this page's one responsive switch
// point (§12.6/D5-01) in sync with web/ui's.
func narrowMedia(rules ...css.Rule) []css.Rule {
	return css.Media(css.MaxW(wui.NarrowMaxWidth), rules...)
}

// emitURLPanelStyles styles the subscribe-URL panel (render_urls.go): a
// label / URL / copy-button row per format. The URL itself is mono and
// allowed to scroll rather than wrap, because a wrapped URL is harder to
// read back and this is the string an operator is checking character by
// character before pasting it into Slack.
func emitURLPanelStyles() {
	css.Global(".af-urls",
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(2)),
		css.Padding(tokens.Space(4)),
		css.Border(css.Px(1), tokens.Color(tokens.RoleBorder)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Bg(tokens.Color(tokens.RoleSurface)),
	)
	css.Global(".af-urls h3",
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.FontWeight.Semibold,
		css.Raw("margin", "0"),
	)
	css.Global(".af-urls__row",
		css.Display.Grid,
		// A fixed label column keeps the four URLs left-aligned with each
		// other, which is what makes them comparable at a glance.
		css.Raw("grid-template-columns", "6rem minmax(0, 1fr) auto"),
		css.Items.Center,
		css.Gap(tokens.Space(2)),
	)
	css.Global(".af-urls__label",
		css.FontSize(tokens.FontSize(tokens.TextXs)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
	)
	css.Global(".af-urls__value",
		monoFont(),
		css.FontSize(tokens.FontSize(tokens.TextXs)),
		css.TextColor(tokens.Color(tokens.RoleText)),
		css.Raw("overflow-x", "auto"),
		css.Raw("white-space", "nowrap"),
		css.PaddingY(tokens.Space(1)),
		css.PaddingX(tokens.Space(2)),
		css.Bg(tokens.Color(tokens.RoleBg)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
	)
	css.Global(".af-urls__copy",
		css.PaddingY(tokens.Space(1)),
		css.PaddingX(tokens.Space(3)),
		css.FontSize(tokens.FontSize(tokens.TextXs)),
		css.Border(css.Px(1), tokens.Color(tokens.RoleBorder)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Bg(tokens.Color(tokens.RoleBg)),
		css.TextColor(tokens.Color(tokens.RoleText)),
		css.Cursor.Pointer,
		bodyFont(),
		css.Raw("white-space", "nowrap"),
	)
	css.Global(".af-urls__copy:hover", css.Bg(tokens.Color(tokens.RoleSurfaceRaised)))
	css.Global(".af-urls__copy:focus-visible",
		css.Raw("outline", "2px solid "+string(tokens.Color(tokens.RoleFocusRing))),
		css.Raw("outline-offset", "1px"),
	)
	css.Global(".af-urls__unset",
		css.FontSize(tokens.FontSize(tokens.TextXs)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		css.Raw("margin", "0"),
	)
	// Narrow: the label column stops earning its width, so the row stacks.
	css.Global(".af-urls__row", narrowMedia(
		css.Raw("grid-template-columns", "minmax(0, 1fr) auto"),
	)...)
	css.Global(".af-urls__label", narrowMedia(
		css.Raw("grid-column", "1 / -1"),
	)...)
}

func init() {
	emitGenerateStyles()
	emitWorkbenchStyles()
	emitURLPanelStyles()
	emitScheduleStyles()
}

// emitWorkbenchStyles is /generate's rebuilt layout (render_workbench.go).
//
// The whole design is one decision: the prompt and its output get the screen,
// and everything else is either on one control row or behind a disclosure.
// See render_workbench.go's doc comment for what the previous three-column
// arrangement got wrong.
func emitWorkbenchStyles() {
	// Two columns, siblings: a persistent, compact, independently-scrolling
	// sidebar beside a main column holding everything about whichever ONE
	// feed is loaded. Each has its own height and its own overflow — this
	// is the structural change render_workbench.go's doc comment describes;
	// every earlier pass kept a single stacked column and only restyled
	// what was inside it.
	css.Global(".af-gen",
		css.Display.Flex,
		css.Items.Start,
		css.Gap(tokens.Space(5)),
		css.W(css.Length("100%")),
		bodyFont(),
		css.TextColor(tokens.Color(tokens.RoleText)),
	)
	css.Global(".af-gen", narrowMedia(
		css.FlexDir.Col,
	)...)
	// The sidebar is a real column with its own scroll: pinned to the
	// viewport height minus the page chrome above it, so it neither grows
	// past the fold nor drags the main column's scroll position with it.
	css.Global(".af-gen__sidebar",
		css.Raw("flex", "0 0 20rem"),
		css.Raw("min-width", "0"),
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(2)),
		css.Raw("position", "sticky"),
		css.Raw("top", "0"),
		css.Raw("align-self", "flex-start"),
		css.Raw("height", "100vh"),
		css.OverflowY.Auto,
		css.PaddingX(tokens.Space(3)),
		css.Raw("border-right", "1px solid "+string(tokens.Color(tokens.RoleBorder))),
	)
	css.Global(".af-gen__sidebar", narrowMedia(
		css.Raw("flex", "1 1 auto"),
		css.Raw("min-width", "0"),
		css.W(css.Length("100%")),
		// The wide rule's align-self:flex-start (so the sticky sidebar does
		// not stretch to the page's full height) also disables cross-axis
		// stretch in THIS column flex layout — cross axis here is WIDTH, not
		// height, so leaving it set left the sidebar sized to its own
		// content instead of the viewport, which is what pushed the whole
		// page wider than 390px. Restore stretch so it fills the column.
		css.Raw("align-self", "stretch"),
		css.Raw("position", "static"),
		css.Raw("height", "auto"),
		css.Raw("max-height", "40vh"),
		css.Raw("border-right", "0"),
		css.BorderBottom(css.Px(1), tokens.Color(tokens.RoleBorder)),
	)...)
	// The main column carries everything about the one loaded feed, and
	// scrolls independently of the sidebar — this is what closes the
	// "two scrolls with dependencies between them" bug: sibling scroll
	// regions, not one nested inside the other on the same axis.
	css.Global(".af-gen__main",
		css.Raw("flex", "1 1 auto"),
		css.Raw("min-width", "0"),
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(4)),
	)

	// The strip is sticky because the Preview button is the page's verb: it
	// must be reachable from anywhere in a long prompt without scrolling
	// back to find it.
	css.Global(".af-gen__strip",
		css.Display.Flex,
		css.Items.Center,
		css.Justify.Between,
		css.Raw("flex-wrap", "wrap"),
		css.Gap(tokens.Space(3)),
		css.Raw("position", "sticky"),
		css.Raw("top", "0"),
		css.ZIndex(5),
		css.PaddingY(tokens.Space(3)),
		css.Bg(tokens.Color(tokens.RoleBg)),
		css.BorderBottom(css.Px(1), tokens.Color(tokens.RoleBorder)),
	)
	// Three zones now, not two: which feed (identity), how it will be
	// generated (params, one joined cluster), and the verb (right). Flagged
	// directly ("wtf, use the front end design skill") after the first
	// redesign pass left this row as seven-odd identically-bordered boxes
	// in a line — fixing the box-nesting elsewhere on the page did nothing
	// for the one row every operator looks at first and most often.
	css.Global(".af-gen__strip-identity, .af-gen__strip-right",
		css.Display.Flex,
		css.Items.Center,
		css.Gap(tokens.Space(2)),
		css.Raw("flex-wrap", "wrap"),
	)
	// The act zone is separated from the configure zone by a rule and real
	// space, not only by the button's fill: the divider says "everything
	// left of here describes the run, everything right of here starts it".
	css.Global(".af-gen__strip-right",
		css.Raw("margin-left", "auto"),
		css.Gap(tokens.Space(3)),
		css.Raw("padding-left", string(tokens.Space(5))),
		css.Raw("border-left", "1px solid "+string(tokens.Color(tokens.RoleBorder))),
	)
	css.Global(".af-gen__strip-right", narrowMedia(
		css.Raw("margin-left", "0"),
		css.Raw("border-left", "0"),
		css.Raw("padding-left", "0"),
	)...)
	// The feed picker is the strip's TITLE, not one of its form fields — it
	// names the thing every other control acts on. No box: a bottom rule
	// only, set at the strip's largest, boldest type, so it reads at a
	// glance the way a document's own title does rather than competing with
	// a temperature field for the eye.
	css.Global(".af-gen__strip-identity select:first-child",
		css.Raw("appearance", "none"),
		css.Raw("border", "0"),
		css.Raw("border-radius", "0"),
		css.BorderBottom(css.Px(2), tokens.Color(tokens.RoleBorderStrong)),
		css.Bg(css.Transparent),
		css.PaddingY(tokens.Space(1)),
		css.PaddingX(css.Zero),
		css.TextColor(tokens.Color(tokens.RoleText)),
		css.FontSize(tokens.FontSize(tokens.TextLg)),
		css.FontWeight.Semibold,
		css.Raw("max-width", "16rem"),
		css.Raw("text-overflow", "ellipsis"),
	)
	css.Global(".af-gen__strip-identity select:first-child:focus-visible",
		css.Raw("outline", "2px solid "+string(tokens.Color(tokens.RoleFocusRing))),
		css.Raw("outline-offset", string(tokens.Space(1))),
	)
	// New/Save keep their existing button treatment (`.af-gen__new,
	// .af-gen__save` below); the ⋯ menu keeps web/ui's own Kebab styling —
	// neither needed a new rule here, only the title-sized feed picker did.
	// The instrument cluster: model / effort / candidates / temperature as
	// ONE joined control, not four floating boxes. A single border and fill
	// around the whole group; each cell inside is borderless and separated
	// only by a hairline — the same "rule, not a box" device the rest of
	// the page uses, applied here to say these four are one control with
	// four dials, because that is what they actually are: every one of them
	// tunes the SAME upcoming call.
	css.Global(".af-gen__strip-params",
		css.Display.Flex,
		css.Items.Stretch,
		css.Border(css.Px(1), tokens.Color(tokens.RoleBorder)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Bg(tokens.Color(tokens.RoleSurface)),
		css.Raw("flex-wrap", "wrap"),
		css.Raw("overflow", "hidden"),
	)
	css.Global(".af-gen__strip-cell",
		css.Display.Flex,
		css.Items.Center,
		css.Raw("border-right", "1px solid "+string(tokens.Color(tokens.RoleBorder))),
	)
	css.Global(".af-gen__strip-cell:last-child", css.Raw("border-right", "0"))
	css.Global(".af-gen__strip-params select, .af-gen__strip-params input",
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.PaddingY(tokens.Space(2)),
		css.PaddingX(tokens.Space(3)),
		css.Raw("border", "0"),
		css.Raw("border-radius", "0"),
		css.Bg(css.Transparent),
		css.TextColor(tokens.Color(tokens.RoleText)),
	)
	css.Global(".af-gen__strip-params select:focus-visible, .af-gen__strip-params input:focus-visible",
		css.Raw("outline", "2px solid "+string(tokens.Color(tokens.RoleFocusRing))),
		css.Raw("outline-offset", "-2px"),
	)
	css.Global(".af-gen__model", css.W(css.Length("11rem")), monoFont())
	// The temperature override holds one number; sized to it so it does not
	// read as an equal partner to the model menu beside it.
	css.Global(".af-gen__temp", css.W(css.Length("4.5rem")), monoFont())
	// Retry keeps the quiet text-action treatment: it is a recovery control
	// that only exists when the feed list failed, not one of the verbs.
	css.Global(".af-gen__retry",
		css.Raw("min-height", "24px"),
		css.PaddingX(tokens.Space(1)),
		css.Raw("border", "0"),
		css.Bg(css.Transparent),
		css.TextColor(tokens.Color(tokens.RoleAccent)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.Cursor.Pointer,
	)
	// New feed and Save read as buttons, not as text links: they are two of
	// the four CRUD verbs and they were losing to a select box beside them.
	css.Global(".af-gen__new, .af-gen__save",
		css.Raw("min-height", "28px"),
		css.PaddingY(tokens.Space(1)),
		css.PaddingX(tokens.Space(3)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Border(css.Px(1), tokens.Color(tokens.RoleBorder)),
		css.Bg(tokens.Color(tokens.RoleBg)),
		css.TextColor(tokens.Color(tokens.RoleText)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.Cursor.Pointer,
	)
	// Save with unsaved work is the one control on the strip that wants the
	// eye; Save with nothing to save fades back to a status word.
	css.Global(".af-gen__save:not(:disabled)",
		css.TextColor(tokens.Color(tokens.RoleAccent)),
		css.Border(css.Px(1), tokens.Color(tokens.RoleAccent)),
		css.FontWeight.Medium,
	)
	css.Global(".af-gen__save:disabled",
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		css.Raw("cursor", "default"),
	)

	// The one primary action on the page, and the only filled control.
	css.Global(".af-gen__preview-btn",
		css.PaddingY(tokens.Space(2)),
		css.PaddingX(tokens.Space(5)),
		css.Raw("border", "0"),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Bg(tokens.Color(tokens.RoleAccent)),
		css.TextColor(tokens.Color(tokens.RoleAccentFg)),
		css.FontWeight.Semibold,
		css.FontSize(tokens.FontSize(tokens.TextBase)),
		css.Cursor.Pointer,
	)
	css.Global(".af-gen__preview-btn:disabled",
		css.OpacityNum(css.Num(0.5)),
		css.Raw("cursor", "not-allowed"),
	)
	// The estimate sits beside the button it prices, in the data face.
	css.Global(".af-gen__estimate",
		css.FontSize(tokens.FontSize(tokens.TextXs)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		monoFont(),
	)
	css.Global(".af-gen__strip-reason",
		css.W(css.Length("100%")),
		css.FontSize(tokens.FontSize(tokens.TextXs)),
		css.TextColor(tokens.Color(tokens.RoleWarning)),
		css.Raw("margin", "0"),
	)

	// The stakes line: quiet, monospaced, directly under the strip so it is
	// read on the way to the prompt rather than hunted for afterwards.
	css.Global(".af-gen__stakes",
		css.Display.Flex,
		css.Raw("flex-wrap", "wrap"),
		css.Items.Center,
		css.Gap(tokens.Space(3)),
		css.FontSize(tokens.FontSize(tokens.TextXs)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		monoFont(),
	)
	css.Global(".af-gen__stakes-slug",
		css.TextColor(tokens.Color(tokens.RoleText)),
	)
	// The one part of this line that is a warning rather than a fact.
	css.Global(".af-gen__stakes-flag",
		css.TextColor(tokens.Color(tokens.RoleWarning)),
		css.FontWeight.Medium,
	)

	// The run-status line: what "Run now" did, next to the feed it did it to.
	// Sits with the stakes line under the strip, because that is where the
	// answer to "did that work?" belongs — not on another page.
	css.Global(".af-run-status",
		css.Display.Flex,
		css.Items.Center,
		css.Raw("flex-wrap", "wrap"),
		css.Gap(tokens.Space(3)),
		css.PaddingY(tokens.Space(2)),
		css.PaddingX(tokens.Space(3)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Border(css.Px(1), tokens.Color(tokens.RoleBorder)),
		css.Bg(tokens.Color(tokens.RoleSurface)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
	)
	css.Global(".af-run-status__text", css.TextColor(tokens.Color(tokens.RoleText)))
	css.Global(".af-run-status__text--ok", css.TextColor(tokens.Color(tokens.RoleSuccess)))
	css.Global(".af-run-status__text--warn", css.TextColor(tokens.Color(tokens.RoleWarning)))
	css.Global(".af-run-status__text--error", css.TextColor(tokens.Color(tokens.RoleDanger)))
	css.Global(".af-run-status__link",
		css.TextColor(tokens.Color(tokens.RoleAccent)),
		css.Raw("text-decoration", "underline"),
	)
	css.Global(".af-run-status__dismiss",
		css.Raw("margin-inline-start", "auto"),
		css.Raw("min-height", "24px"),
		css.Raw("min-width", "24px"),
		css.Raw("border", "0"),
		css.Bg(css.Transparent),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		css.Cursor.Pointer,
	)
	// The recipe form's model menu, matching the width of the fields around
	// it rather than the strip's compact one.
	css.Global(".af-editor__model",
		css.Raw("box-sizing", "border-box"),
		css.W(css.Length("100%")),
		css.Raw("max-width", "30rem"),
		css.Raw("min-height", "34px"),
		css.PaddingX(tokens.Space(2)),
		css.Border(css.Px(1), tokens.Color(tokens.RoleBorder)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Bg(tokens.Color(tokens.RoleBg)),
		css.TextColor(tokens.Color(tokens.RoleText)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		monoFont(),
	)

	// Two equal columns: neither the prompt nor its output is the junior
	// partner. The min-height keeps the preview pane from collapsing to
	// nothing before the first sample, so the page does not visibly reflow
	// the moment a result arrives.
	//
	// The gap between them carries the redline — docs/design-direction.md's
	// "vertical column divider, used sparingly... the loudest thing on the
	// page... exactly one boundary per view". This page's other redline use
	// (the rail row's left accent bar) is emphasis on a selected/stale row,
	// not a column boundary; this is the one genuine pane-to-pane divider,
	// and it did not exist before this pass — the two columns previously
	// shared a plain gap with nothing marking where the prompt ends and its
	// output begins.
	css.Global(".af-gen__work",
		css.Display.Grid,
		css.Raw("grid-template-columns", "minmax(0, 1fr) 2px minmax(0, 1fr)"),
		css.Gap(tokens.Space(5)),
		css.Items.Start,
	)
	css.Global(".af-gen__work-divider",
		css.Raw("align-self", "stretch"),
		css.W(css.Px(2)),
		css.Bg(tokens.Color(tokens.RoleDanger)),
		css.Raw("opacity", "0.55"),
	)
	css.Global(".af-gen__work-divider", narrowMedia(
		css.Display.None,
	)...)
	css.Global(".af-gen__prompts, .af-gen__preview",
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(4)),
		// Fill the viewport rather than sitting in a 22rem band with a screen
		// of dead space beneath it: the prompt is the content of this page and
		// it should get the height. The strip, the recipe row and the page
		// chrome are what the subtraction accounts for.
		css.Raw("min-height", "calc(100vh - 16rem)"),
	)
	// The prompts stack: both fields grow, the chips stay put at the bottom.
	css.Global(".af-gen__prompts .af-gen__field",
		css.Raw("flex", "1 1 auto"),
	)
	css.Global(".af-gen__prompt",
		css.Raw("flex", "1 1 auto"),
	)

	css.Global(".af-gen__field",
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(1)),
	)
	css.Global(".af-gen__field label",
		css.FontSize(tokens.FontSize(tokens.TextXs)),
		css.FontWeight.Medium,
		css.Raw("text-transform", "uppercase"),
		css.Tracking(css.Ems(0.08)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
	)
	// The prompts are the CONTENT of this screen, not a form field: mono,
	// set for writing rather than scanning, and tall enough to see a whole
	// instruction at once.
	css.Global(".af-gen__prompt",
		css.W(css.Length("100%")),
		css.Raw("box-sizing", "border-box"),
		css.Raw("min-height", "9rem"),
		css.Raw("resize", "vertical"),
		css.Padding(tokens.Space(3)),
		css.Border(css.Px(1), tokens.Color(tokens.RoleBorder)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Bg(tokens.Color(tokens.RoleSurface)),
		css.TextColor(tokens.Color(tokens.RoleText)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.Raw("line-height", "1.6"),
		monoFont(),
	)
	css.Global(".af-gen__prompt:focus-visible",
		css.Raw("outline", "2px solid "+string(tokens.Color(tokens.RoleFocusRing))),
		css.Raw("outline-offset", "1px"),
	)
	css.Global(".af-gen__hint",
		css.FontSize(tokens.FontSize(tokens.TextXs)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		css.Raw("margin", "0"),
	)

	// --- The signature: template-variable chips -------------------------
	css.Global(".af-gen__chips",
		css.Display.Flex,
		css.Items.Center,
		css.Raw("flex-wrap", "wrap"),
		css.Gap(tokens.Space(1)),
	)
	css.Global(".af-gen__chips-label",
		css.FontSize(tokens.FontSize(tokens.TextXs)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		css.Raw("margin-inline-end", string(tokens.Space(1))),
	)
	// 24px minimum height: these are the page's most-clicked controls and
	// they measured 20px, under the floor where a pointer target stops being
	// comfortable. The type stays small — the padding does the work, so the
	// chips still read as chips rather than as a row of buttons.
	css.Global(".af-gen__chip",
		css.FontSize(tokens.FontSize(tokens.TextXs)),
		monoFont(),
		css.Raw("min-height", "24px"),
		css.PaddingY(css.Px(4)),
		css.PaddingX(tokens.Space(2)),
		css.Rounded(tokens.Radius(tokens.RadiusFull)),
		css.Border(css.Px(1), tokens.Color(tokens.RoleBorder)),
		css.Bg(tokens.Color(tokens.RoleBg)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		css.Cursor.Pointer,
		css.Raw("transition", "color "+string(tokens.Duration(tokens.DurationFast))+" "+string(tokens.Easing(tokens.EasingStd))+
			", border-color "+string(tokens.Duration(tokens.DurationFast))+" "+string(tokens.Easing(tokens.EasingStd))),
	)
	css.Global(".af-gen__chip:hover",
		css.TextColor(tokens.Color(tokens.RoleAccent)),
		css.Border(css.Px(1), tokens.Color(tokens.RoleAccent)),
	)
	css.Global(".af-gen__chip:focus-visible",
		css.Raw("outline", "2px solid "+string(tokens.Color(tokens.RoleFocusRing))),
		css.Raw("outline-offset", "1px"),
	)

	// The set-once fields, behind a disclosure.
	// The feeds disclosure is the management surface, so its summary reads as
	// a section heading rather than as the quiet footnote the recipe drawer's
	// does — it is the thing an operator opens on purpose, not the thing they
	// tolerate at the bottom of the page.
	css.Global(".af-gen__feeds > summary",
		css.Cursor.Pointer,
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.FontWeight.Medium,
		css.TextColor(tokens.Color(tokens.RoleText)),
		css.PaddingY(tokens.Space(1)),
	)
	css.Global(".af-gen__recipe",
		css.BorderTop(css.Px(1), tokens.Color(tokens.RoleBorder)),
		css.Raw("padding-block-start", string(tokens.Space(3))),
	)
	css.Global(".af-gen__recipe > summary",
		css.Cursor.Pointer,
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.FontWeight.Medium,
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		css.PaddingY(tokens.Space(1)),
	)
	css.Global(".af-gen__recipe > summary:hover", css.TextColor(tokens.Color(tokens.RoleText)))
	css.Global(".af-gen__empty",
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
	)

	// Below the narrow switch point the two columns stack: at that width a
	// side-by-side prompt and preview are both too narrow to read, and the
	// order (write, then look) is the one that survives stacking.
	css.Global(".af-gen__work", narrowMedia(
		css.Raw("grid-template-columns", "minmax(0, 1fr)"),
	)...)
}
