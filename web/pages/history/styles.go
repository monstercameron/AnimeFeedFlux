//go:build js && wasm

// styles.go declares every rule the /history page's own markup needs: the
// two-tab shell (root.go), the Runs/Items tables and filters (runs_ui.go,
// items_ui.go), the item create/edit form (forms_ui.go), and the typed
// delete confirmation (confirm_ui.go). PLAN.md §12.6: "all styling is
// authored in Go... GWC v5's typed css package" — every color/space/radius/
// duration here reads a tokens.* Var, never a literal.
//
// Unlike /generate, this page renders NO web/ui primitives at all (doc.go:
// nothing here imports web/ui) — every button/input/select/table is a plain
// h.Button/h.Input/h.Select/h.Table with, in most cases, no class of its
// own. So this file follows web/pages/auth/styles.go's pattern rather than
// web/pages/generate/styles.go's: style the explicit history-* classes
// directly, and style the plain tags by a descendant selector scoped under
// ".history-page" (the one class the page's root <main> always carries) —
// exactly how auth/styles.go styles login/recover's plain tags under
// ".af-auth-page". Rules for an explicit class that also needs to win
// against its own generic-tag rule (e.g. ".history-kebab-trigger" against
// the generic "button" rule) are written as two classes
// (".history-page .history-kebab-trigger") so their specificity — two
// classes beats one class plus one type selector — wins regardless of
// declaration order; see the inline comments below at each such case.
//
// Called once from an init() below — css.Global dedupes by (selector+rules)
// content, so a second call is a no-op, not a duplicate rule (see the
// vendored css package's doc comment on Global, and
// web/pages/auth/styles.go's identical note).
package history

import (
	"github.com/monstercameron/GoWebComponents/v5/css"

	"github.com/monstercameron/AnimeFeedFlux/web/tokens"
	wui "github.com/monstercameron/AnimeFeedFlux/web/ui"
)

func emitHistoryStyles() {
	css.Global(".history-page",
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(4)),
		css.W(css.Length("100%")),
		bodyFont(),
		css.FontSize(tokens.FontSize(tokens.TextBase)),
		css.TextColor(tokens.Color(tokens.RoleText)),
	)
	css.Global(".history-page h1",
		css.FontSize(tokens.FontSize(tokens.TextXl)),
		css.FontWeight.Bold,
		css.Raw("margin", "0"),
	)
	css.Global(".history-page h2",
		css.FontSize(tokens.FontSize(tokens.TextLg)),
		css.FontWeight.Semibold,
		css.Raw("margin", "0"),
	)
	css.Global(".history-page h3",
		css.FontSize(tokens.FontSize(tokens.TextMd)),
		css.FontWeight.Semibold,
		css.Raw("margin", "0"),
	)
	css.Global(".history-page label",
		css.Display.InlineFlex,
		css.Items.Center,
		css.Gap(tokens.Space(1)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.FontWeight.Medium,
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
	)

	emitPlainControlStyles()
	emitTabsStyles()
	emitScreenStateStyles()
	emitFilterAndTableStyles()
	emitKebabStyles()
	emitFormStyles()
	emitRevisionAndDiffStyles()
	emitConfirmStyles()
	emitDisabledStyles()
}

// --- Plain tags (this page's own analogue of auth/styles.go) ---------------

func emitPlainControlStyles() {
	css.Global(`.history-page input:not([type="checkbox"])`,
		css.Display.Block,
		css.Raw("box-sizing", "border-box"),
		css.Padding(tokens.Space(2)),
		css.PaddingX(tokens.Space(3)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Border(css.Px(1), tokens.Color(tokens.RoleBorder)),
		css.Bg(tokens.Color(tokens.RoleBg)),
		css.TextColor(tokens.Color(tokens.RoleText)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		bodyFont(),
		transitionColors(),
	)
	css.Global(".history-page select",
		css.Display.Block,
		css.Padding(tokens.Space(2)),
		css.PaddingX(tokens.Space(3)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Border(css.Px(1), tokens.Color(tokens.RoleBorder)),
		css.Bg(tokens.Color(tokens.RoleBg)),
		css.TextColor(tokens.Color(tokens.RoleText)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		bodyFont(),
	)
	css.Global(".history-page textarea",
		css.Display.Block,
		css.W(css.Length("100%")),
		css.Raw("box-sizing", "border-box"),
		css.Raw("min-height", "5rem"),
		css.Raw("resize", "vertical"),
		css.Padding(tokens.Space(2)),
		css.PaddingX(tokens.Space(3)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Border(css.Px(1), tokens.Color(tokens.RoleBorder)),
		css.Bg(tokens.Color(tokens.RoleBg)),
		css.TextColor(tokens.Color(tokens.RoleText)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		bodyFont(),
		transitionColors(),
	)
	// Every button on the page starts from one height. Padding alone was
	// producing 32px in a table cell, 35px in the pager and 24px in a menu,
	// because the padding was applied to whatever line-height the content
	// happened to have.
	css.Global(".history-page button",
		css.Display.InlineFlex,
		css.Items.Center,
		css.Justify.Center,
		css.Gap(tokens.Space(2)),
		css.Raw("box-sizing", "border-box"),
		css.Raw("min-height", historyControlHeight),
		css.PaddingY(css.Zero),
		css.PaddingX(tokens.Space(3)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Border(css.Px(1), tokens.Color(tokens.RoleBorder)),
		css.Bg(tokens.Color(tokens.RoleSurface)),
		css.TextColor(tokens.Color(tokens.RoleText)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.FontWeight.Medium,
		css.Cursor.Pointer,
		bodyFont(),
		transitionColors(),
	)
	css.Global(".history-page button:disabled",
		css.OpacityNum(css.Num(0.55)),
		css.Raw("cursor", "not-allowed"),
	)
	css.Global(".history-page button:not(:disabled):hover", css.OpacityNum(css.Num(0.9)))
	css.Global(`.history-page input:focus-visible, .history-page select:focus-visible, .history-page textarea:focus-visible, .history-page button:focus-visible, .history-page [tabindex]:focus-visible`,
		focusOutline()...,
	)
}

// --- Tabs --------------------------------------------------------------------

func emitTabsStyles() {
	css.Global(".history-tabs",
		css.Display.Flex,
		css.Gap(tokens.Space(1)),
		css.Raw("border-bottom", "1px solid "+string(tokens.Color(tokens.RoleBorder))),
	)
	// .history-tab is a plain <button>, so this must out-specificity
	// ".history-page button" above — two classes vs one class + one type
	// wins regardless of declaration order (see this file's package doc
	// comment).
	css.Global(".history-page .history-tab",
		css.Raw("border", "none"),
		css.Raw("border-radius", "0"),
		css.Raw("border-bottom", "2px solid transparent"),
		css.Bg(css.Transparent),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		css.PaddingX(tokens.Space(4)),
	)
	css.Global(".history-page .history-tab--active",
		css.TextColor(tokens.Color(tokens.RoleText)),
		css.Raw("border-bottom", "2px solid "+string(tokens.Color(tokens.RoleAccent))),
	)
}

// --- Six-state screen readouts (ScreenState/ListState banners) -------------

func emitScreenStateStyles() {
	css.Global(".history-state",
		css.Display.Block,
		css.Padding(tokens.Space(3)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
	)
	css.Global(".history-state--loading, .history-state--empty",
		css.Bg(tokens.Color(tokens.RoleSurface)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
	)
	css.Global(".history-state--empty", css.FontStyle.Italic)
	css.Global(".history-state--error",
		css.Bg(tokens.Color(tokens.RoleDanger)),
		css.TextColor(tokens.Color(tokens.RoleDangerFg)),
	)
	css.Global(".history-state--disabled",
		css.Bg(tokens.Color(tokens.RoleWarning)),
		css.TextColor(tokens.Color(tokens.RoleWarningFg)),
	)
	css.Global(".history-state--disconnected",
		css.Bg(tokens.Color(tokens.RoleDanger)),
		css.TextColor(tokens.Color(tokens.RoleDangerFg)),
	)
	css.Global(".history-action-error, .history-revert-error",
		css.Padding(tokens.Space(2)),
		css.PaddingX(tokens.Space(3)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Bg(tokens.Color(tokens.RoleDanger)),
		css.TextColor(tokens.Color(tokens.RoleDangerFg)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
	)
}

// --- Filters, tables, pager --------------------------------------------------

// historyControlHeight is the one height every interactive control on this
// page is drawn at: filter fields, the row actions, the pager. Stated once so
// that a select, a date input and a button cannot each pick their own — left
// alone they chose 29px, 37px and 35px, side by side in the same row.
const historyControlHeight = "34px"

// historyRowButtonHeight is the smaller size used INSIDE table rows, where a
// full-height control on every row makes the table twice as tall as its data.
// 32px, matching web/ui.Kebab's square 32x32 trigger: the row's Expand button
// and the ⋯ beside it sit in the same cell, and two heights there is exactly
// the mismatch this is meant to remove.
const historyRowButtonHeight = "32px"

func emitFilterAndTableStyles() {
	css.Global(".history-runs, .history-items",
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(3)),
	)
	css.Global(".history-filters",
		css.Display.Flex,
		css.FlexWrap.Wrap,
		// Align to the BOTTOM, not the centre: the fields are now
		// label-above-control pairs of differing label lengths, and centring
		// them puts the controls themselves on four different baselines.
		css.Items.End,
		css.Gap(tokens.Space(3)),
		css.PaddingY(tokens.Space(3)),
	)
	// One label-above-control pair.
	css.Global(".history-filters__field",
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(1)),
	)
	// The micro-label treatment the rest of the app already uses for field
	// labels, so a filter reads as a filter and not as body copy.
	css.Global(".history-filters__field label",
		css.FontSize(tokens.FontSize(tokens.TextXs)),
		css.FontWeight.Medium,
		css.Raw("text-transform", "uppercase"),
		css.Tracking(css.Ems(0.08)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
	)
	// These controls previously carried NO rules at all, so they rendered as
	// raw browser widgets — a white box with a system border in the middle of
	// a dark page. Matched to /generate's strip controls rather than invented
	// again here, so the two filter rows in the app look like one app.
	// ONE height for every control in the row.
	//
	// Left to their own devices these disagree badly: a <select> sized itself
	// to 29px, a <input type="date"> to 37px, and a text input to 35px — three
	// heights side by side in the same row, on both tabs. The browser's
	// intrinsic sizing for each widget is different, and padding alone cannot
	// reconcile them, so the height is stated outright and box-sizing makes
	// the border part of it.
	css.Global(".history-filters select, .history-filters input, .history-filters button",
		css.Raw("box-sizing", "border-box"),
		css.Raw("height", historyControlHeight),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.PaddingY(css.Zero),
		css.PaddingX(tokens.Space(2)),
		css.Raw("min-width", "11rem"),
		css.Border(css.Px(1), tokens.Color(tokens.RoleBorder)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Bg(tokens.Color(tokens.RoleBg)),
		css.TextColor(tokens.Color(tokens.RoleText)),
		bodyFont(),
	)
	// The row's action button sits at the controls' baseline, not the labels'.
	css.Global(".history-filters__action",
		css.Display.Flex,
		css.Items.End,
	)
	// Two classes, to out-specify ".history-filters button" above — that rule
	// is a class plus a type selector, so a single class loses to it and the
	// button kept the 11rem min-width meant for the fields.
	css.Global(".history-filters .history-filters__new",
		css.Raw("min-width", "0"),
		css.PaddingX(tokens.Space(4)),
		css.Cursor.Pointer,
	)
	css.Global(".history-filters__clear",
		css.PaddingY(tokens.Space(1)),
		css.PaddingX(tokens.Space(3)),
		css.Raw("border", "0"),
		css.Bg(css.Transparent),
		css.TextColor(tokens.Color(tokens.RoleAccent)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.Cursor.Pointer,
	)
	// D5-01/§12.6: a filter row of label+input pairs wraps mid-pair at
	// narrow widths unless it switches to one pair per line — the same
	// switch point web/ui's own primitives use (wui.NarrowMaxWidth), kept
	// in sync rather than a second magic number.
	css.Global(".history-filters", narrowMedia(
		css.FlexDir.Col,
		css.Items.Start,
	)...)
	css.Global(".history-bulkbar",
		css.Display.Flex,
		css.Items.Center,
		css.Gap(tokens.Space(3)),
		css.Padding(tokens.Space(2)),
		css.PaddingX(tokens.Space(3)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Bg(tokens.Color(tokens.RoleSurfaceRaised)),
		css.Border(css.Px(1), tokens.Color(tokens.RoleBorder)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
	)
	// Status pills. Text-first, tinted second: a reader who cannot separate
	// the hues still reads "Published" and "Deleted".
	css.Global(".history-status",
		css.Display.InlineBlock,
		css.PaddingY(css.Px(2)),
		css.PaddingX(tokens.Space(2)),
		css.Rounded(tokens.Radius(tokens.RadiusFull)),
		css.FontSize(tokens.FontSize(tokens.TextXs)),
		css.FontWeight.Medium,
	)
	css.Global(".history-status--published",
		css.TextColor(tokens.Color(tokens.RoleSuccess)),
		css.Raw("border", "1px solid "+string(tokens.Color(tokens.RoleSuccess))),
	)
	css.Global(".history-status--deleted",
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		css.Raw("border", "1px solid "+string(tokens.Color(tokens.RoleBorder))),
	)
	// A6-08: money is what an operator scans the runs table for, and it read
	// as one of seven equal numeric columns. Tabular figures so the decimal
	// points line up, and full-strength ink instead of the muted body tone.
	css.Global(".history-runs-table td:nth-child(8)",
		css.TextColor(tokens.Color(tokens.RoleText)),
		css.FontWeight.Medium,
		css.Raw("font-variant-numeric", "tabular-nums"),
	)
	// Row controls are deliberately smaller than the page's controls: at full
	// height, a button in every one of twenty-five rows doubles the table's
	// height and the buttons, not the data, become the thing you see.
	css.Global(".history-table button",
		css.Raw("min-height", historyRowButtonHeight),
		css.FontSize(tokens.FontSize(tokens.TextXs)),
		css.PaddingX(tokens.Space(2)),
	)
	// The actions column hugs its buttons instead of stretching across the
	// leftover width, which is what put Expand and ⋯ an inch away from the
	// row they belong to.
	css.Global(".history-table td:last-child:not(.history-run-log-cell), .history-table th:last-child",
		css.Raw("width", "1%"),
		css.Raw("white-space", "nowrap"),
	)
	css.Global(".history-pager, .history-revisions-pager",
		css.Display.Flex,
		css.Gap(tokens.Space(2)),
		// The numbered buttons make this row long enough to overflow on a
		// narrow viewport, where it previously held three controls.
		css.Items.Center,
		css.Raw("flex-wrap", "wrap"),
	)

	// A numbered page button. Square-ish and narrower than the word buttons
	// beside it, so a row of numbers reads as a group rather than as eight
	// more actions.
	css.Global(".history-page .history-pager__page",
		css.Raw("min-width", historyControlHeight),
		css.PaddingX(tokens.Space(2)),
		css.Justify.Center,
	)
	// The current page is not a button you press, it is where you are — so it
	// is marked, and disabled, rather than merely tinted.
	css.Global(".history-page .history-pager__page--current",
		css.Bg(tokens.Color(tokens.RoleAccent)),
		css.TextColor(tokens.Color(tokens.RoleAccentFg)),
		css.FontWeight.Semibold,
		css.OpacityNum(css.Num(1)),
	)
	// A page that exists but has not been fetched: shown, so the operator can
	// see how far the table goes, visibly not clickable, because an opaque
	// cursor cannot be jumped to before it has been handed over.
	css.Global(".history-page .history-pager__page--unreached",
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		css.Raw("border-style", "dashed"),
	)
	css.Global(".history-pager__gap",
		css.Display.InlineFlex,
		css.Items.Center,
		css.PaddingX(tokens.Space(1)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
	)
	css.Global(".history-pager__status",
		css.Display.InlineFlex,
		css.Items.Center,
		css.PaddingX(tokens.Space(2)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		// Pushed to the far end so the controls group together and the
		// readout does not sit between two buttons that get pressed in
		// sequence.
		css.Raw("margin-left", "auto"),
	)

	// The table itself is the horizontal-scroll container (no wrapper div
	// in this page's markup, unlike web/ui.Table's own D5-01 container) —
	// overflow-x on a <table> box works without an extra element because a
	// table is block-level by default, so this needs no markup change.
	// `overflow-x: auto` on a <table> does nothing on its own: a table's
	// used display is `table`, and overflow is ignored on that box, so the
	// nine-column runs table simply grew past its container and pushed the
	// whole DOCUMENT into horizontal scroll. Measured 2026-08-10 at the
	// 320px floor the direction doc calls non-negotiable: viewport 320,
	// document scrollWidth 752, with `.history-runs-table` itself 728px
	// wide. `display: block` is what makes the overflow property apply, and
	// it is why the settings table (web/pages/settings/styles.go) carries
	// the same pair — the table scrolls inside its own box so the page
	// never does.
	css.Global(".history-table",
		css.W(css.Length("100%")),
		css.MaxWidth(css.Length("100%")),
		css.Raw("display", "block"),
		css.OverflowX.Auto,
		css.Raw("border-collapse", "collapse"),
		bodyFont(),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
	)
	css.Global(".history-table thead",
		css.Raw("border-bottom", "1px solid "+string(tokens.Color(tokens.RoleBorderStrong))),
	)
	css.Global(".history-table th",
		css.Raw("text-align", "left"),
		css.PaddingY(tokens.Space(2)),
		css.PaddingX(tokens.Space(3)),
		css.FontWeight.Semibold,
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		css.FontSize(tokens.FontSize(tokens.TextXs)),
	)
	css.Global(".history-table td",
		css.PaddingY(tokens.Space(2)),
		css.PaddingX(tokens.Space(3)),
		css.Raw("border-bottom", "1px solid "+string(tokens.Color(tokens.RoleBorder))),
		css.TextColor(tokens.Color(tokens.RoleText)),
		css.Raw("vertical-align", "top"),
		// docs/design-direction.md's type section: "All numerals tabular…
		// every column of counts, costs, tokens and timestamps must align
		// vertically" — set on every cell (not only the columns with a
		// dedicated mono rule below) since the published-date column is
		// plain body text that still carries digits.
		css.Raw("font-variant-numeric", "tabular-nums"),
	)
	// Numeric/cost/token columns and the expanded run log both read as
	// instrument values (tokens/theme.go's design brief) — tabular numerals
	// and mono, right-aligned like a ledger (docs/design-direction.md's
	// type section: "every column of counts, costs, tokens and timestamps
	// must align vertically"). Added/rejected, tokens and cost are
	// respectively the 5th, 6th and 7th <td> in the Runs table (see
	// runs_ui.go's Thead order: #, status, trigger, duration,
	// added/rejected, tokens, cost, error, kebab) — nth-child is the only
	// way to reach them without a class, since runRow renders them as plain
	// h.Td(string) with no wrapper of its own.
	css.Global(".history-runs-table td:nth-child(5), .history-runs-table td:nth-child(6), .history-runs-table td:nth-child(7)",
		monoFont(),
		css.Raw("text-align", "right"),
		css.Raw("font-variant-numeric", "tabular-nums"),
	)
	css.Global(".history-runs-table th:nth-child(5), .history-runs-table th:nth-child(6), .history-runs-table th:nth-child(7)",
		css.Raw("text-align", "right"),
	)
	// The numbered gutter (docs/design-direction.md's anchor: rows are
	// numbered because the sequence carries information) — narrow,
	// tabular, muted, right-aligned like a sprocket-hole column rather than
	// a data column of its own.
	css.Global(".history-th-num, .history-td-num",
		css.W(css.Length("1%")),
		css.Raw("white-space", "nowrap"),
		css.Raw("text-align", "right"),
	)
	css.Global(".history-td-num",
		monoFont(),
		css.Raw("font-variant-numeric", "tabular-nums"),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
	)
	// A failed run must be findable by scanning (J5), not by reading the
	// status cell's text — the whole row's left edge carries a rule-weight
	// mark in the same danger/accent roles the rest of the page already
	// uses for "this needs attention" / "this is live", never a new color.
	css.Global(".history-run-row--failed td:first-child",
		css.Raw("box-shadow", "inset 3px 0 0 "+string(tokens.Color(tokens.RoleDanger))),
	)
	css.Global(".history-run-row--running td:first-child",
		css.Raw("box-shadow", "inset 3px 0 0 "+string(tokens.Color(tokens.RoleAccent))),
	)
	// The actions cell (Expand + the kebab trigger) renders as two sibling
	// buttons with no wrapper of its own — without a gap they visually run
	// together into what reads as one "ExpandActions" control. This is the
	// last <td> in both tables (Runs: 9th column; Items: 6th), so it can be
	// reached without a markup change.
	// :not(.history-run-log-cell) — an expanded row has exactly ONE cell, so
	// it is :last-child as well, and without this exclusion the run-log
	// panel was laid out as a flex row of its own children and collapsed to
	// a fraction of the table width.
	css.Global(".history-table td:last-child:not(.history-run-log-cell)",
		css.Display.Flex,
		css.Items.Center,
		css.Gap(tokens.Space(2)),
	)
	// The expanded row's cell spans the whole table and is a plain block.
	css.Global(".history-run-log-cell",
		css.Raw("display", "table-cell"),
		css.W(css.Length("100%")),
	)
	// PLAN.md/J5: reject reasons are "the whole point of the runs view" —
	// this panel sits inside a colspan'd <td> (runs_ui.go's expanded row),
	// which sizes to its CONTENT by default, so without an explicit width
	// it shrinks to a narrow floating box instead of using the row's full
	// measure. W(100%) is what actually gives it room.
	css.Global(".history-run-log",
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(2)),
		css.W(css.Length("100%")),
		css.Raw("box-sizing", "border-box"),
		css.Padding(tokens.Space(4)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Bg(tokens.Color(tokens.RoleBg)),
		css.Border(css.Px(1), tokens.Color(tokens.RoleBorder)),
	)
	// The facts header of an expanded run (runs_ui.go's runDetailFacts):
	// status, trigger, absolute start/finish, duration, counts, tokens, cost,
	// and the error verbatim. A responsive grid rather than fixed columns, so
	// it reflows instead of overflowing the panel at narrow widths.
	css.Global(".history-run-facts",
		css.Display.Grid,
		css.Raw("grid-template-columns", "repeat(auto-fit, minmax(180px, 1fr))"),
		css.Gap(tokens.Space(2)),
		css.Raw("margin-bottom", string(tokens.Space(4))),
	)
	css.Global(".history-run-fact",
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(1)),
		css.Padding(tokens.Space(2)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Bg(tokens.Color(tokens.RoleSurface)),
		css.Border(css.Px(1), tokens.Color(tokens.RoleBorder)),
		// A grid item's automatic minimum is its content, so a long error
		// message would widen its track past the panel without this.
		css.Raw("min-width", "0"),
	)
	// The error message needs the full width: it is the one value here that
	// is a paragraph rather than a figure.
	css.Global(".history-run-fact--wide",
		css.Raw("grid-column", "1 / -1"),
	)
	css.Global(".history-run-fact__label",
		css.FontSize(tokens.FontSize(tokens.TextXs)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
	)
	css.Global(".history-run-fact__value",
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.TextColor(tokens.Color(tokens.RoleText)),
	)
	// Provider errors carry their own line breaks and quoting. pre-wrap keeps
	// them verbatim while still wrapping, and the scroll bound stops a
	// pathological stack trace from making the panel unusable.
	css.Global(".history-run-fact pre",
		css.Margin(css.Zero),
		css.Raw("white-space", "pre-wrap"),
		css.Raw("overflow-wrap", "anywhere"),
		css.Raw("max-height", "16rem"),
		css.Raw("overflow-y", "auto"),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
	)
	css.Global(".history-run-facts__note",
		css.FontSize(tokens.FontSize(tokens.TextXs)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		css.Raw("margin-bottom", string(tokens.Space(4))),
	)

	// Reject reasons are "the whole point of the runs view" (J5: diagnose a
	// bad run) — given a real list layout with room to breathe, not a
	// cramped inline run of text squeezed into a table cell.
	css.Global(".history-run-log ul",
		css.Raw("list-style", "none"),
		css.Margin(css.Zero),
		css.Padding(css.Zero),
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(1)),
	)
	css.Global(".history-run-log li",
		css.Padding(tokens.Space(2)),
		css.PaddingX(tokens.Space(3)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Bg(tokens.Color(tokens.RoleSurface)),
		css.Border(css.Px(1), tokens.Color(tokens.RoleBorder)),
		css.Raw("white-space", "pre-wrap"),
		css.Raw("overflow-wrap", "anywhere"),
	)
	css.Global(".history-run-log pre",
		css.Raw("white-space", "pre-wrap"),
		css.Raw("overflow-wrap", "anywhere"),
		css.FontSize(tokens.FontSize(tokens.TextXs)),
		css.TextColor(tokens.Color(tokens.RoleText)),
		monoFont(),
	)
}

// --- Kebab menus (hand-rolled — no Open state, so revealed by :hover/
// :focus-within rather than a JS toggle; see this file's package doc
// comment and items_ui.go/runs_ui.go, which render the menu unconditionally
// in the DOM with no hidden/open class of its own) -------------------------

func emitKebabStyles() {
	// The hand-rolled kebab's rules are gone with it: these tables now use the
	// shared web/ui.Kebab, which brings its own styling, a real open state and
	// fixed positioning. The old menu opened on :hover/:focus-within and was
	// absolutely positioned inside a table that is its own horizontal-scroll
	// container, so it could not be opened by touch and the last rows' menus
	// were clipped by the table's edge instead of floating over it.
}

// --- Item create/edit form, correction panel --------------------------------

func emitFormStyles() {
	// Single-line fields stop at a readable measure. A title or a URL input
	// stretched to 1400px is harder to read, not easier, and it made this
	// form look nothing like the rest of the app — the text AREAS keep the
	// full width, because what goes in them is a document.
	css.Global(".history-item-form input, .history-correction-panel input",
		css.Raw("max-width", "42rem"),
	)
	css.Global(".history-item-form select, .history-correction-panel select",
		css.Raw("max-width", "42rem"),
	)
	// Row selection checkboxes: a real target rather than the browser's 13px
	// default.
	css.Global(".history-select-row",
		css.W(css.Px(18)),
		css.H(css.Px(18)),
		css.Cursor.Pointer,
	)
	css.Global(".history-form-hint",
		css.FontSize(tokens.FontSize(tokens.TextXs)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		css.Margin(css.Zero),
	)
	css.Global(".history-item-form, .history-correction-panel",
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(2)),
		css.Padding(tokens.Space(4)),
		css.Rounded(tokens.Radius(tokens.RadiusLg)),
		css.Bg(tokens.Color(tokens.RoleSurface)),
		css.Border(css.Px(1), tokens.Color(tokens.RoleBorder)),
	)
	css.Global(".history-guid-notice",
		css.Padding(tokens.Space(2)),
		css.PaddingX(tokens.Space(3)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Bg(tokens.Color(tokens.RoleBg)),
		css.Raw("border-left", "3px solid "+string(tokens.Color(tokens.RoleAccent))),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
	)
	css.Global(".history-backdate-block",
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(2)),
		css.Padding(tokens.Space(3)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Bg(tokens.Color(tokens.RoleWarning)),
		css.TextColor(tokens.Color(tokens.RoleWarningFg)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
	)
	css.Global(".history-backdate-warning",
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.TextColor(tokens.Color(tokens.RoleWarning)),
	)
	css.Global(".history-form-error",
		css.Padding(tokens.Space(2)),
		css.PaddingX(tokens.Space(3)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Bg(tokens.Color(tokens.RoleDanger)),
		css.TextColor(tokens.Color(tokens.RoleDangerFg)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
	)
	css.Global(".history-form-actions",
		css.Display.Flex,
		css.Gap(tokens.Space(2)),
		css.Raw("padding-top", string(tokens.Space(2))),
	)
}

// --- Revision history and its diff view -------------------------------------

func emitRevisionAndDiffStyles() {
	css.Global(".history-revisions",
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(2)),
		css.Padding(tokens.Space(3)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Bg(tokens.Color(tokens.RoleBg)),
		css.Border(css.Px(1), tokens.Color(tokens.RoleBorder)),
	)
	css.Global(".history-diff",
		css.Display.Flex,
		css.FlexDir.Col,
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Raw("overflow", "hidden"),
		css.Border(css.Px(1), tokens.Color(tokens.RoleBorder)),
		monoFont(),
		css.FontSize(tokens.FontSize(tokens.TextXs)),
	)
	css.Global(".history-diff-line",
		css.PaddingX(tokens.Space(2)),
		css.Raw("white-space", "pre-wrap"),
		css.Raw("overflow-wrap", "anywhere"),
	)
	css.Global(".history-diff-equal",
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
	)
	css.Global(".history-diff-delete",
		css.Bg(css.RGBA(241, 85, 95, 0.12)),
		css.TextColor(tokens.Color(tokens.RoleDanger)),
	)
	css.Global(".history-diff-insert",
		css.Bg(css.RGBA(62, 207, 142, 0.12)),
		css.TextColor(tokens.Color(tokens.RoleSuccess)),
	)
}

// --- Typed-confirmation dialog ----------------------------------------------

func emitConfirmStyles() {
	css.Global(".history-confirm-dialog",
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(3)),
		css.W(css.Length("100%")),
		css.MaxWidth(css.Length("24rem")),
		css.Padding(tokens.Space(6)),
		css.Rounded(tokens.Radius(tokens.RadiusLg)),
		css.Bg(tokens.Color(tokens.RoleSurfaceRaised)),
		css.Raw("box-shadow", tokens.Shadow(tokens.ShadowLg)),
		css.TextColor(tokens.Color(tokens.RoleText)),
	)
	css.Global(".history-confirm-actions",
		css.Display.Flex,
		css.Gap(tokens.Space(2)),
		css.Justify.End,
	)
	// The literal typed-confirmation word (e.g. "DELETE") and the confirm
	// button both live in danger territory — the confirm button is the one
	// place on this page ButtonDanger's plain-button equivalent belongs
	// (D0-16: the destructive action is already isolated behind this
	// dialog, so naming it plainly is the point, same reasoning as
	// web/ui/button.go's ButtonDanger doc comment).
	css.Global(".history-page .history-confirm-actions button:not(:disabled):first-child",
		css.Bg(tokens.Color(tokens.RoleDanger)),
		css.Border(css.Px(1), tokens.Color(tokens.RoleDanger)),
		css.TextColor(tokens.Color(tokens.RoleDangerFg)),
	)
}

// --- Whole-page disabled/not-wired notices ----------------------------------

func emitDisabledStyles() {
	css.Global(".history-page--disabled, .history-page--not-wired",
		css.Display.Flex,
		css.FlexDir.Col,
		css.Gap(tokens.Space(2)),
	)
	css.Global(".history-disabled-reason",
		css.Padding(tokens.Space(3)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Bg(tokens.Color(tokens.RoleWarning)),
		css.TextColor(tokens.Color(tokens.RoleWarningFg)),
		css.FontSize(tokens.FontSize(tokens.TextBase)),
	)
}

// bodyFont/monoFont/transitionColors/focusOutline mirror web/ui/base.go's
// identical helpers, duplicated for the same reason web/shell/styles.go and
// web/pages/auth/styles.go duplicate them: unexported in package ui, and
// this page is not on this task's allowed-edit list for web/ui.
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

func init() {
	emitHistoryStyles()
}
