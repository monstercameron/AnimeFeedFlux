package ui

import (
	"math"
	"strconv"

	"github.com/monstercameron/GoWebComponents/v5/css"
	"github.com/monstercameron/GoWebComponents/v5/html"
	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"

	"github.com/monstercameron/AnimeFeedFlux/web/tokens"
)

// TableColumn is one column header. LabelKey is an i18n key (D0-22).
type TableColumn struct {
	ID       string
	LabelKey string
	Mono     bool // render this column's cells in the data/mono font (costs, tokens, ids)

	// Weight is this column's share of the row, relative to its siblings.
	// Zero means 1 — every column equal, which is what every table did
	// before this field existed.
	//
	// It exists because equal shares are wrong whenever the columns hold
	// wildly different amounts of text. The sessions table is the case that
	// forced it: a full user-agent string and a "Yes"/"No" each got a fifth
	// of the row, so the device was unreadably clipped while three columns
	// sat mostly empty.
	//
	// A float rather than a CSS length on purpose. It is interpolated into
	// an emitted stylesheet, and a string field would put arbitrary CSS one
	// careless caller away from the emitter; a number cannot carry a brace.
	// Non-finite and negative values fall back to 1 for the same reason.
	Weight float64
}

// TableProps configures Table. CaptionKey is required — a table with no
// caption is a table screen readers cannot name (a11y floor). Rows/Cells are
// pre-rendered Nodes (not strings) so a cell can hold a Kebab, a Toggle, or
// any other primitive, not just text.
type TableProps struct {
	T          T
	ID         string
	CaptionKey string
	Columns    []TableColumn
	// Rows maps column ID -> rendered cell content for each row. RowKeys
	// gives stable React-style keys (e.g. the row's guid) in row order.
	Rows    []map[string]Node
	RowKeys []string
}

// Table is the shared data-table primitive: semantic <table>/<caption>/
// <th scope="col">, a stable key per row, and token-driven styling. Pair it
// with the six state components in state.go for loading/empty/error — Table
// itself only renders the populated case.
//
// # D5-01: narrow-width behaviour
//
// A seven-column data table does not become usable by shrinking — text
// wraps mid-word, numeric columns lose alignment, and a stacked
// label/value presentation would mean re-deriving a bespoke per-column
// template for every table (and re-deriving it again the next time a
// column is added). Table instead renders unshrunk inside its own
// horizontally-scrolling container: the container, not the page, scrolls
// sideways. That keeps the table's structure (and its <th scope="col">
// semantics) intact at any viewport, and it is the same behaviour at 320px
// as at 1440px — there is no separate "mobile table" to keep in sync with
// the real one. The container is also a keyboard scroll target
// (tabindex="0" + a visible focus ring), because an overflow:auto box a
// keyboard user cannot reach is only scrollable with a mouse.
func Table(p TableProps) Node {
	headerCells := make([]Node, 0, len(p.Columns))
	for _, col := range p.Columns {
		headerCells = append(headerCells, h.Th(
			html.Attr("scope", "col"),
			thClass(),
			resolve(p.T, col.LabelKey),
		))
	}

	rows := make([]Node, 0, len(p.Rows))
	for i, row := range p.Rows {
		key := ""
		if i < len(p.RowKeys) {
			key = p.RowKeys[i]
		}
		cells := make([]Node, 0, len(p.Columns))
		for _, col := range p.Columns {
			cells = append(cells, h.Td(tdClass(col.Mono), row[col.ID]))
		}
		rows = append(rows, html.WithKey(h.Tag("tr", nil, cells), key))
	}

	table := h.Table(
		html.ID(p.ID),
		css.Class([]css.Rule{
			css.W(css.Length("100%")),
			css.Raw("border-collapse", "collapse"),
			bodyFont(),
			css.FontSize(tokens.FontSize(tokens.TextSm)),
		}),
		h.Tag("caption", srOnlyClass(), resolve(p.T, p.CaptionKey)),
		h.Thead(css.Class([]css.Rule{
			css.Raw("border-bottom", "1px solid "+string(tokens.Color(tokens.RoleBorderStrong))),
		}), h.Tag("tr", nil, headerCells)),
		h.Tbody(nil, rows),
	)

	scrollRules := []css.Rule{
		css.OverflowX.Auto,
		css.MaxWidth(css.Length("100%")),
		css.Raw("-webkit-overflow-scrolling", "touch"),
	}
	scrollRules = append(scrollRules, focusVisible()...)
	return h.Div(
		html.TabIndex(0),
		css.Class(scrollRules),
		table,
	)
}

func thClass() html.PropOption {
	return css.Class([]css.Rule{
		css.Raw("text-align", "left"),
		css.PaddingY(tokens.Space(2)),
		css.PaddingX(tokens.Space(3)),
		css.FontWeight.Semibold,
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		css.FontSize(tokens.FontSize(tokens.TextXs)),
	})
}

func tdClass(mono bool) html.PropOption {
	rules := []css.Rule{
		css.PaddingY(tokens.Space(2)),
		css.PaddingX(tokens.Space(3)),
		css.Raw("border-bottom", "1px solid "+string(tokens.Color(tokens.RoleBorder))),
		css.TextColor(tokens.Color(tokens.RoleText)),
	}
	if mono {
		rules = append(rules, monoFont())
	} else {
		rules = append(rules, bodyFont())
	}
	return css.Class(rules)
}

// srOnlyClass visually hides content while keeping it in the accessibility
// tree — used for Table's required caption, which this design brief keeps
// off-screen rather than displayed (the panel around the table already
// carries a visible heading; the caption exists for assistive tech).
func srOnlyClass() html.PropOption {
	return css.Class([]css.Rule{
		css.Raw("position", "absolute"),
		css.W(css.Px(1)),
		css.H(css.Px(1)),
		css.Padding(css.Zero),
		css.Margin(css.Length("-1px")),
		css.Raw("overflow", "hidden"),
		css.Raw("clip", "rect(0,0,0,0)"),
		css.Raw("white-space", "nowrap"),
		css.Raw("border", "0"),
	})
}

// gridTemplate lays every column out on one shared grid track list so the
// header and every body row line up — the alignment a real <table> gives for
// free and a div list has to be told.
func gridTemplate(cols []TableColumn) string {
	if len(cols) == 0 {
		return "1fr"
	}
	out := ""
	for _, col := range cols {
		if out != "" {
			out += " "
		}
		// minmax(0, Nfr) rather than a bare fr track: a bare one has an
		// `auto` minimum, so one long user-agent string would widen its
		// column and push the rest out of the row instead of being clipped.
		out += "minmax(0, " + columnFraction(col.Weight) + "fr)"
	}
	return out
}

// columnFraction renders a column's weight as a grid fr multiplier.
//
// Anything that is not a usable positive number becomes "1" — both the
// documented default for a zero Weight and the only safe answer for the rest.
// This string is interpolated straight into an emitted stylesheet, and NaN or
// ±Inf would format as a token that makes the whole grid-template-columns
// declaration invalid, taking every column's alignment with it rather than
// just this one's.
func columnFraction(weight float64) string {
	if math.IsNaN(weight) || math.IsInf(weight, 0) || weight <= 0 {
		return "1"
	}
	return strconv.FormatFloat(weight, 'f', -1, 64)
}
