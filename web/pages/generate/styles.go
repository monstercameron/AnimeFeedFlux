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

	// The three panes: each its own scrolling card. maxHeightPane keeps a
	// tall rail/editor/sampler from pushing the page's own scrollbar off
	// past the viewport — this is a dense admin console someone reads at
	// 2am, not a page that should require scrolling past a header to find
	// the sampler's Promote button.
	// docs/design-direction.md's layout section: "Rules, not cards" and
	// "Radius 3px, uniformly" — panes get a hairline border, no shadow, and
	// the smallest radius step this project's token scale has (RadiusSm),
	// rather than the shadowed/rounded "card" look the previous RadiusLg
	// gave them.
	paneBase := []css.Rule{
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(4)),
		css.Padding(tokens.Space(4)),
		css.Bg(tokens.Color(tokens.RoleSurface)),
		css.Border(css.Px(1), tokens.Color(tokens.RoleBorder)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Raw("max-height", "calc(100vh - 8rem)"),
		css.OverflowY.Auto,
	}
	css.Global(".af-generate__rail", paneBase...)
	css.Global(".af-generate__editor", paneBase...)
	css.Global(".af-generate__sampler", paneBase...)
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
	)
	css.Global(".af-rail__header h2",
		css.FontSize(tokens.FontSize(tokens.TextLg)),
		css.FontWeight.Semibold,
		css.Raw("margin", "0"),
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
	css.Global(".af-rail__list",
		css.Raw("list-style", "none"),
		css.Padding(css.Zero),
		css.Margin(css.Zero),
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(2)),
	)
	// A row is a rule-bounded frame, not a filled card — only the hairline
	// border and, on the bottom, the tape's own baseline give it edges;
	// selection reads as a redline-strength left rule (the vertical column
	// divider docs/design-direction.md reserves for "exactly one boundary
	// per view" — here, which frame is loaded into the editor) rather than
	// an inset glow.
	css.Global(".af-rail__row",
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(1)),
		css.Padding(tokens.Space(2)),
		css.PaddingX(tokens.Space(3)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Raw("border", "1px solid "+string(tokens.Color(tokens.RoleBorder))),
		css.Raw("border-left", "3px solid transparent"),
		css.Bg(tokens.Color(tokens.RoleBg)),
		css.Cursor.Pointer,
		transitionColors(),
	)
	css.Global(".af-rail__row:hover", css.Bg(tokens.Color(tokens.RoleSurfaceRaised)))
	css.Global(".af-rail__row--selected",
		css.Raw("border-left", "3px solid "+string(tokens.Color(tokens.RoleAccent))),
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
	// The row head: numbered gutter + title/slug, in that order, so the
	// number reads first — "row 1 of the sheet", the same way a genga
	// sheet's frame numbers lead each row (docs/design-direction.md's
	// anchor).
	css.Global(".af-rail__row-head",
		css.Display.Flex,
		css.Items.Start,
		css.Gap(tokens.Space(2)),
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
	css.Global(".af-rail__row-headtext",
		css.Display.Flex,
		css.FlexDir.Col,
		css.Raw("min-width", "0"),
	)
	css.Global(".af-rail__row-title",
		css.FontWeight.Semibold,
		css.TextColor(tokens.Color(tokens.RoleText)),
	)
	css.Global(".af-rail__row-slug",
		css.FontSize(tokens.FontSize(tokens.TextXs)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		monoFont(),
	)
	css.Global(".af-rail__stale-flag",
		css.Display.InlineFlex,
		css.Raw("align-self", "flex-start"),
		css.PaddingX(tokens.Space(2)),
		css.Rounded(tokens.Radius(tokens.RadiusFull)),
		css.Bg(tokens.Color(tokens.RoleWarning)),
		css.TextColor(tokens.Color(tokens.RoleWarningFg)),
		css.FontSize(tokens.FontSize(tokens.TextXs)),
		css.FontWeight.Semibold,
	)
	css.Global(".af-rail__row-meta",
		css.Display.Flex,
		css.FlexDir.Row,
		css.FlexWrap.Wrap,
		css.Items.Baseline,
		css.Gap(tokens.Space(3)),
		css.FontSize(tokens.FontSize(tokens.TextXs)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		css.Raw("font-variant-numeric", "tabular-nums"),
		monoFont(),
	)
	css.Global(".af-rail__row-spend",
		css.Raw("margin-left", "auto"),
		css.TextColor(tokens.Color(tokens.RoleText)),
	)
	css.Global(".af-rail__row-actions",
		css.Display.Flex,
		css.Items.Center,
		css.Gap(tokens.Space(2)),
		css.Raw("margin-top", string(tokens.Space(1))),
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
	css.Global(".af-tape",
		css.Raw("position", "relative"),
		css.W(css.Length("100%")),
		css.H(css.Length("1.25rem")),
		css.Raw("margin", string(tokens.Space(1))+" 0"),
	)
	css.Global(".af-tape__baseline",
		css.Raw("position", "absolute"),
		css.Raw("left", "0"),
		css.Raw("right", "0"),
		css.Raw("bottom", "2px"),
		css.H(css.Px(1)),
		css.Bg(tokens.Color(tokens.RoleBorder)),
	)
	// Ticks are the one bold, saturated mark on this whole page — the
	// accent color, full height of the strip, thin enough that a dense run
	// of them still reads as individual marks rather than a solid bar
	// (docs/design-direction.md: "ticks march forward and never collide").
	css.Global(".af-tape__tick",
		css.Raw("position", "absolute"),
		css.Raw("top", "0"),
		css.Raw("bottom", "2px"),
		css.W(css.Px(2)),
		css.Raw("transform", "translateX(-1px)"),
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

	// Prompt template variable identifiers — mono pills, not prose (§12.6:
	// these must never be translated, see render_editor.go's own comment).
	css.Global(".af-prompt-vars",
		css.Raw("list-style", "none"),
		css.Padding(css.Zero),
		css.Margin(css.Zero),
		css.Display.Flex,
		css.FlexWrap.Wrap,
		css.Gap(tokens.Space(2)),
	)
	css.Global(".af-prompt-vars li",
		css.PaddingX(tokens.Space(2)),
		css.PaddingY(css.Length("0.125rem")),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Bg(tokens.Color(tokens.RoleBg)),
		css.Border(css.Px(1), tokens.Color(tokens.RoleBorder)),
		css.FontSize(tokens.FontSize(tokens.TextXs)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		monoFont(),
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
}

// emitWorkbenchStyles is /generate's rebuilt layout (render_workbench.go).
//
// The whole design is one decision: the prompt and its output get the screen,
// and everything else is either on one control row or behind a disclosure.
// See render_workbench.go's doc comment for what the previous three-column
// arrangement got wrong.
func emitWorkbenchStyles() {
	css.Global(".af-gen",
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(4)),
		css.W(css.Length("100%")),
		bodyFont(),
		css.TextColor(tokens.Color(tokens.RoleText)),
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
	css.Global(".af-gen__strip-left, .af-gen__strip-right",
		css.Display.Flex,
		css.Items.Center,
		css.Gap(tokens.Space(2)),
		css.Raw("flex-wrap", "wrap"),
	)
	css.Global(".af-gen__strip select, .af-gen__strip input",
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.PaddingY(tokens.Space(1)),
		css.PaddingX(tokens.Space(2)),
		css.Border(css.Px(1), tokens.Color(tokens.RoleBorder)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Bg(tokens.Color(tokens.RoleBg)),
		css.TextColor(tokens.Color(tokens.RoleText)),
	)
	css.Global(".af-gen__model", css.W(css.Length("14rem")), monoFont())
	// The temperature override holds one number; sized to it so it does not
	// read as an equal partner to the model menu beside it.
	css.Global(".af-gen__temp", css.W(css.Length("5rem")), monoFont())
	css.Global(".af-gen__new",
		css.Raw("min-height", "24px"),
		css.PaddingX(tokens.Space(1)),
		css.Raw("border", "0"),
		css.Bg(css.Transparent),
		css.TextColor(tokens.Color(tokens.RoleAccent)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.Cursor.Pointer,
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

	// Two equal columns: neither the prompt nor its output is the junior
	// partner. The min-height keeps the preview pane from collapsing to
	// nothing before the first sample, so the page does not visibly reflow
	// the moment a result arrives.
	css.Global(".af-gen__work",
		css.Display.Grid,
		css.Raw("grid-template-columns", "minmax(0, 1fr) minmax(0, 1fr)"),
		css.Gap(tokens.Space(5)),
		css.Items.Start,
	)
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
