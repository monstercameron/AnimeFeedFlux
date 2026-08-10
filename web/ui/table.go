package ui

import (
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

	return h.Table(
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
