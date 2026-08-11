//go:build js && wasm

package settings

import (
	"github.com/monstercameron/GoWebComponents/v5/css"

	"github.com/monstercameron/AnimeFeedFlux/web/tokens"
	wui "github.com/monstercameron/AnimeFeedFlux/web/ui"
)

// styles.go declares the rules this package's markup needs.
//
// **It did not exist until 2026-08-10.** `web/pages/settings` emitted nine
// distinct `af-*` class names and contained not one `css.Global` call, so
// every one of them selected nothing: /settings rendered as a single
// undifferentiated column of browser-default form controls running edge to
// edge, with no section boundaries, no measure, and — because the active-
// sessions table is wide and nothing constrained it — horizontal scroll on
// the whole document at 1280px. Found by screenshotting the real page while
// signed in; no test could have caught it, since a class with no rule is not
// a compile error and not a runtime one. This is the concrete shape of what
// TODOS.md D0-25 describes in aggregate.
//
// Everything reads a token, never a literal, so both themes and the token
// layer's WCAG-checked pairs apply here as they do everywhere else. The
// approach follows docs/design-direction.md's "Layout": rules, not cards —
// sections are separated by hairlines in `rule`, and a filled surface is
// reserved for things that are genuinely raised.
//
// Called once from an init() below; css.Global dedupes on (selector+rules)
// content, so a repeat call is a no-op rather than a duplicate rule — the
// same note web/pages/auth/styles.go and web/pages/history/styles.go carry.
func emitSettingsStyles() {
	// The page. A measure, not full bleed: these are forms and prose, and a
	// 1280px-wide text input is not easier to fill in than a 640px one, it
	// is just harder to associate with its label.
	css.Global(".af-settings",
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(6)),
		css.MaxWidth(css.Length("64rem")),
		css.W(css.Length("100%")),
		css.Raw("margin-inline", "auto"),
		css.Raw("box-sizing", "border-box"),
		css.TextColor(tokens.Color(tokens.RoleText)),
		bodyFont(),
	)
	css.Global(".af-settings h1",
		css.FontSize(tokens.FontSize(tokens.Text2xl)),
		css.FontWeight.Bold,
		css.Tracking(css.Ems(-0.01)),
		css.Raw("margin", "0"),
	)
	css.Global(".af-settings h2",
		css.FontSize(tokens.FontSize(tokens.TextLg)),
		css.FontWeight.Semibold,
		css.Raw("margin", "0"),
	)
	css.Global(".af-settings h3",
		css.FontSize(tokens.FontSize(tokens.TextMd)),
		css.FontWeight.Semibold,
		css.Raw("margin", "0"),
	)
	css.Global(".af-settings p",
		css.Raw("margin", "0"),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
	)

	// A section is one tab's whole body.
	css.Global(".af-settings-section",
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(6)),
	)

	// A card is one setting group inside a section. Separated by a hairline
	// above rather than boxed on all four sides: a column of bordered boxes
	// reads as a list of unrelated widgets, where a ruled stack reads as one
	// document with parts. `:first-child` drops the leading rule so the
	// section does not open with a line under its own heading.
	// A card is a GRID, not a stack.
	//
	// Fields cap at 30rem (see the input rule below, and it is right — a
	// value that is a URL or a TTL gains nothing from more), while the page
	// runs to 64rem. Stacked vertically that left every settings tab as one
	// narrow column of fields with two thirds of the screen empty beside it,
	// on every tab, in both themes: the single most visible design defect in
	// the app.
	//
	// auto-fit rather than a fixed column count, so this is one rule for
	// every card: a card with one field stays one column, a card with six
	// fills the width it has, and at narrow widths every card collapses back
	// to a stack without a media query. Wide children (headings, help text,
	// tables) opt out below.
	css.Global(".af-settings-card",
		css.Display.Grid,
		css.Raw("grid-template-columns", "repeat(auto-fit, minmax(20rem, 1fr))"),
		css.Raw("align-items", "start"),
		css.Gap(tokens.Space(3)),
		css.Raw("column-gap", string(tokens.Space(6))),
		css.Raw("padding-block-start", string(tokens.Space(6))),
		css.BorderTop(css.Px(1), tokens.Color(tokens.RoleBorder)),
	)
	// Everything that is about the card rather than one field in it spans the
	// full width: titles, prose, alerts, tables, and the card's own header
	// row. Without this a heading becomes a column and the grid reads as two
	// unrelated halves.
	css.Global(".af-settings-card > h2, .af-settings-card > h3, .af-settings-card > h4, "+
		".af-settings-card > p, .af-settings-card > table, .af-settings-card > .af-settings-card-header, "+
		".af-settings-card > .af-table-wrap, .af-settings-card > .af-profiles, "+
		".af-settings-card > .af-cost-chart, .af-settings-card > .af-settings-actions",
		css.Raw("grid-column", "1 / -1"),
	)
	css.Global(".af-settings-section > .af-settings-card:first-child",
		css.Raw("padding-block-start", "0"),
		css.Raw("border-top", "0"),
	)
	// The header row of a card: title on the left, its kebab on the right.
	css.Global(".af-settings-card-header",
		css.Display.Flex,
		css.Items.Center,
		css.Justify.Between,
		css.Gap(tokens.Space(3)),
	)

	// Form controls. The generic element selectors are scoped under
	// .af-settings so they cannot leak into another page's markup.
	css.Global(".af-settings label",
		css.Display.Block,
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.FontWeight.Medium,
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		css.Raw("margin-block-end", string(tokens.Space(1))),
	)
	css.Global(".af-settings input, .af-settings select, .af-settings textarea",
		css.Display.Block,
		css.W(css.Length("100%")),
		// A form field wider than about 30rem stops helping: the eye loses
		// the line back to the label, and none of these values (a URL, a
		// passphrase, a TTL) is long enough to need more.
		css.MaxWidth(css.Length("30rem")),
		css.Raw("box-sizing", "border-box"),
		css.Padding(tokens.Space(2)),
		css.Border(css.Px(1), tokens.Color(tokens.RoleBorder)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Bg(tokens.Color(tokens.RoleBg)),
		css.TextColor(tokens.Color(tokens.RoleText)),
		css.FontSize(tokens.FontSize(tokens.TextBase)),
		bodyFont(),
	)
	css.Global(".af-settings input:focus-visible, .af-settings select:focus-visible, .af-settings textarea:focus-visible",
		css.Raw("outline", "2px solid "+string(tokens.Color(tokens.RoleFocusRing))),
		css.Raw("outline-offset", "1px"),
	)

	// The active-sessions table (§12.5). It is the one genuinely wide thing
	// on this page, and it is what put the whole document into horizontal
	// scroll: a table sizes to its content and nothing was stopping it. It
	// scrolls inside its own box instead, so the page never does.
	css.Global(".af-settings table",
		css.W(css.Length("100%")),
		css.Raw("border-collapse", "collapse"),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.Raw("display", "block"),
		css.Raw("overflow-x", "auto"),
		// Roughly twenty rows. Past that this is a log, not a list, and an
		// operator scanning for one device should not have to scroll the
		// page to reach the controls underneath it.
		css.Raw("max-height", "28rem"),
		css.Raw("overflow-y", "auto"),
	)
	css.Global(".af-settings th",
		css.TextAlign.Left,
		css.FontWeight.Medium,
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		css.PaddingY(tokens.Space(2)),
		css.PaddingX(tokens.Space(3)),
		css.Raw("white-space", "nowrap"),
		css.BorderBottom(css.Px(1), tokens.Color(tokens.RoleBorder)),
	)
	css.Global(".af-settings td",
		css.PaddingY(tokens.Space(2)),
		css.PaddingX(tokens.Space(3)),
		css.BorderBottom(css.Px(1), tokens.Color(tokens.RoleBorder)),
		css.Raw("white-space", "nowrap"),
	)

	// Buttons hug their label. .af-settings-section and .af-settings-card are
	// both flex columns, whose default `stretch` made every action button —
	// Save, Change password, Re-enroll — as wide as the whole 64rem measure,
	// which reads as a banner rather than a control and puts the click target
	// nowhere near the text that names it.
	css.Global(".af-settings button",
		css.Raw("align-self", "flex-start"),
	)

	// A model field: label + menu (or, when the provider list could not be
	// fetched, label + text input), plus the reason it fell back.
	css.Global(".af-model-field",
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(1)),
	)

	css.Global(".af-field-help",
		css.FontSize(tokens.FontSize(tokens.TextXs)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		css.Raw("margin", "0"),
	)

	// The OpenAI-compatible endpoint list. One row per endpoint: name, base
	// URL, the NAME of the environment variable holding its key, whether
	// that variable is set, and remove.
	css.Global(".af-profiles",
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(2)),
		css.Items.Start,
	)
	css.Global(".af-profiles__row",
		css.Display.Grid,
		css.Raw("grid-template-columns", "minmax(0,1fr) minmax(0,2fr) minmax(0,1.2fr) auto auto"),
		css.Gap(tokens.Space(2)),
		css.Items.Center,
		css.W(css.Length("100%")),
	)
	// These inputs sit in a row, so the 30rem field cap that is right for a
	// stacked form would leave three columns of wildly different widths.
	css.Global(".af-profiles__row input",
		css.MaxWidth(css.Length("100%")),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
	)
	css.Global(".af-profiles__key",
		css.FontSize(tokens.FontSize(tokens.TextXs)),
		css.Raw("white-space", "nowrap"),
	)
	css.Global(".af-profiles__remove",
		css.Raw("border", "0"),
		css.Bg(css.Color("transparent")),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		css.FontSize(tokens.FontSize(tokens.TextLg)),
		css.Cursor.Pointer,
		css.Raw("line-height", "1"),
		css.PaddingX(tokens.Space(2)),
	)
	css.Global(".af-profiles__remove:hover", css.TextColor(tokens.Color(tokens.RoleDanger)))

	// --- Spend chart -------------------------------------------------------
	//
	// The hero number carries the answer (§13 asks "am I within the
	// ceiling", which is a number); the columns answer the follow-up. Text
	// stays in the text roles — only the marks take the series colour.
	// A control inside a card header sizes to its content. The generic
	// ".af-settings select" rule gives fields width:100% up to 30rem, which
	// is right for a stacked form and absurd for a range picker sitting
	// beside a heading — it took half the panel.
	css.Global(".af-settings-card-header select",
		css.W(css.Auto),
		css.MaxWidth(css.Length("14rem")),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
	)

	css.Global(".af-cost__hero",
		css.Display.Flex,
		css.Items.Baseline,
		css.Gap(tokens.Space(3)),
		css.Raw("flex-wrap", "wrap"),
	)
	css.Global(".af-cost__total",
		css.FontSize(tokens.FontSize(tokens.Text2xl)),
		css.FontWeight.Bold,
		css.TextColor(tokens.Color(tokens.RoleText)),
		css.Tracking(css.Ems(-0.01)),
		monoFont(),
	)
	css.Global(".af-cost__caption",
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
	)
	css.Global(".af-cost__plot",
		css.W(css.Length("100%")),
		css.Raw("height", "150px"),
	)
	css.Global(".af-cost__svg",
		css.W(css.Length("100%")),
		css.Raw("height", "100%"),
		css.Raw("display", "block"),
		css.Raw("overflow", "visible"),
	)
	css.Global(".af-cost__bar",
		css.Raw("fill", string(tokens.Color(tokens.RoleAccent))),
		// The hover target is the mark itself here; the transition is what
		// makes which bar is active legible without a crosshair layer.
		css.Raw("transition", "fill "+string(tokens.Duration(tokens.DurationFast))+" "+string(tokens.Easing(tokens.EasingStd))),
	)
	css.Global(".af-cost__bar--hovered",
		css.Raw("fill", string(tokens.Color(tokens.RoleAccentStrong))),
	)
	// Recessive: the baseline anchors the columns, it is not data.
	css.Global(".af-cost__baseline",
		css.Raw("fill", string(tokens.Color(tokens.RoleBorder))),
	)
	css.Global(".af-cost__empty",
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
	)

	// A revoked session's state, in the muted role: it is a fact about a row
	// that is no longer actionable, not a warning to act on.
	css.Global(".af-session-revoked",
		css.FontSize(tokens.FontSize(tokens.TextXs)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
	)

	// Status lines. Each maps to the role that already means that thing, so
	// "this went wrong" is the same colour here as it is in the shell's
	// reconnect banner and in history's failed-run marker.
	css.Global(".af-warning",
		css.TextColor(tokens.Color(tokens.RoleWarning)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
	)
	css.Global(".af-error",
		css.TextColor(tokens.Color(tokens.RoleDanger)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
	)
	css.Global(".af-success",
		css.TextColor(tokens.Color(tokens.RoleSuccess)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
	)

	// The .af-settings-signout rule was removed 2026-08-10 along with the
	// control it styled — see web/pages/settings/wiring.go.

	// The whole-page error variant (deps.go's fallback body).
	css.Global(".af-settings--error",
		css.TextColor(tokens.Color(tokens.RoleDanger)),
	)

	// Narrow viewports (§12.6/D5-01: breakpoints land in the same commit as
	// the layout). The measure and the field cap both stop mattering there;
	// the table's own scroller above is what keeps the document itself from
	// scrolling sideways.
	css.Global(".af-settings", narrowMedia(css.Gap(tokens.Space(4)))...)
	css.Global(".af-settings-card", narrowMedia(css.Raw("padding-block-start", string(tokens.Space(4))))...)
	css.Global(".af-settings input, .af-settings select, .af-settings textarea",
		narrowMedia(css.MaxWidth(css.Length("100%")))...)
	// A five-column endpoint row does not survive 320px; it stacks.
	css.Global(".af-profiles__row", narrowMedia(
		css.Raw("grid-template-columns", "minmax(0,1fr)"),
	)...)
}

// narrowMedia scopes rules to wui.NarrowMaxWidth and below, reusing the same
// exported constant web/pages/generate and web/pages/history already use so
// this app has one responsive switch point rather than three.
func narrowMedia(rules ...css.Rule) []css.Rule {
	return css.Media(css.MaxW(wui.NarrowMaxWidth), rules...)
}

func bodyFont() css.Rule { return css.Font(tokens.FontFamily(tokens.FontSans)) }

// monoFont is for figures read as data — the spend total is a number
// someone compares against a ceiling, and tabular digits keep it from
// jittering as it changes.
func monoFont() css.Rule { return css.Font(tokens.FontFamily(tokens.FontMono)) }

func init() { emitSettingsStyles() }
