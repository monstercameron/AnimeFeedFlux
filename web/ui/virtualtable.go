//go:build js && wasm

package ui

import (
	"strconv"
	"syscall/js"

	"github.com/monstercameron/GoWebComponents/v5/css"
	"github.com/monstercameron/GoWebComponents/v5/html"
	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/AnimeFeedFlux/web/tokens"
)

// VirtualTable is Table for lists that can grow without bound.
//
// Table builds every row. That is correct for a fixed six-row panel and
// wrong for the active-sessions list, which gains a row on every sign-in and
// had already reached ~90 on a dev box after one afternoon — ninety rows
// built, reconciled and laid out for the dozen a person can see, on a page
// that also has to stay responsive on a 2 GB droplet.
//
// # Why this is not a <table>
//
// GWC's html.VirtualList represents the rows it did NOT build as two spacer
// <div>s sized to their combined height, which is what keeps the scrollbar
// honest. A <div> is not a valid child of <tbody>: the browser hoists it out
// of the table, the spacers stop occupying row space, and the scroll
// position becomes a lie. So this primitive is a CSS-grid list carrying
// explicit ARIA table roles — role="table"/"row"/"columnheader"/"cell" — which
// is the shape assistive technology actually consumes, and the same tradeoff
// every virtualized grid on the web makes.
//
// Rows are a fixed height for the same reason VirtualListProps.ItemHeight is
// fixed: measuring each row is the O(dataset) cost virtualization exists to
// remove. Cell content that would wrap is clipped with an ellipsis rather
// than allowed to change a row's height and desynchronise the spacers from
// reality.
type VirtualTableProps struct {
	T          T
	ID         string
	CaptionKey string
	Columns    []TableColumn
	// Rows maps column ID -> rendered cell content, in display order.
	// RowKeys gives a stable identity per row so scrolling reuses fibers
	// rather than rebuilding the window (VirtualListProps.Key).
	Rows    []map[string]Node
	RowKeys []string

	// RowHeight overrides the default row height in CSS pixels. Set it when
	// the caller's cells are taller than one line of TextSm; a value that
	// disagrees with the rendered height makes the scrollbar drift.
	RowHeight float64
	// ViewportHeight is the scroller's visible height in CSS pixels. Zero
	// uses defaultVirtualViewport.
	ViewportHeight float64
	// Unvirtualized renders every row — the explicit opt-out, for a print
	// or export view where everything must be in the DOM.
	Unvirtualized bool
}

// defaultVirtualRowHeight is one line of TextSm plus the Space(2) padding
// above and below that virtualTableCell applies. Stated as one number here
// because VirtualList needs it in advance and CSS needs it applied; the two
// must agree or the spacers lie.
const defaultVirtualRowHeight = 37.0

// defaultVirtualViewport shows roughly ten rows. Past that the panel starts
// pushing the controls beneath it off the screen, and this is a list an
// operator scans for one entry rather than reads end to end.
const defaultVirtualViewport = 380.0

// VirtualTable renders the header row plus only the visible slice of body
// rows, inside its own scroller.
func VirtualTable(p VirtualTableProps) Node {
	rowHeight := p.RowHeight
	if rowHeight <= 0 {
		rowHeight = defaultVirtualRowHeight
	}
	viewport := p.ViewportHeight
	if viewport <= 0 {
		viewport = defaultVirtualViewport
	}

	// scrollTop is the only piece of state here. VirtualList is pure — it is
	// told where the scroller is rather than reading the DOM — so something
	// has to hold that, and the component that owns the scroller is the
	// honest place for it.
	//
	// The value is read through syscall/js rather than off the event,
	// because GWC v5.0.1's event type exposes value/key/pointer accessors
	// and nothing for scroll offset (checked: runtime.GoEvent has
	// GetClientX/GetPageX/... but no GetScrollTop, and DOMRef exposes only
	// Focus/Blur). Reading one number off a known element id is the smallest
	// escape hatch available, and this package is js/wasm-only anyway.
	scrollerID := p.ID + "-scroller"
	scrollTop := ui.UseState(0.0)
	onScroll := ui.UseEvent(func(ui.InputEvent) {
		doc := js.Global().Get("document")
		if !doc.Truthy() {
			return
		}
		el := doc.Call("getElementById", scrollerID)
		if !el.Truthy() {
			return
		}
		scrollTop.Set(el.Get("scrollTop").Float())
	})

	template := gridTemplate(p.Columns)

	headerCells := make([]Node, 0, len(p.Columns))
	for _, col := range p.Columns {
		headerCells = append(headerCells, h.Div(
			html.Attr("role", "columnheader"),
			virtualHeaderCell(),
			resolve(p.T, col.LabelKey),
		))
	}

	body := html.VirtualList(html.VirtualListProps{
		ItemCount:      len(p.Rows),
		ItemHeight:     rowHeight,
		ViewportHeight: viewport,
		ScrollTop:      scrollTop.Get(),
		Unvirtualized:  p.Unvirtualized,
		Key: func(i int) any {
			if i < len(p.RowKeys) {
				return p.RowKeys[i]
			}
			return i
		},
		Render: func(i int) ui.Node {
			cells := make([]Node, 0, len(p.Columns))
			for _, col := range p.Columns {
				cells = append(cells, h.Div(
					html.Attr("role", "cell"),
					virtualBodyCell(col.Mono),
					p.Rows[i][col.ID],
				))
			}
			args := make([]any, 0, len(cells)+2)
			args = append(args, html.Attr("role", "row"), virtualRow(template, rowHeight))
			for _, c := range cells {
				args = append(args, c)
			}
			return h.Div(args...)
		},
	})

	return h.Div(
		html.ID(p.ID),
		html.Attr("role", "table"),
		html.Attr("aria-rowcount", strconv.Itoa(len(p.Rows))),
		css.Class([]css.Rule{
			css.W(css.Length("100%")),
			css.Raw("border", "1px solid "+string(tokens.Color(tokens.RoleBorder))),
			css.Rounded(tokens.Radius(tokens.RadiusSm)),
			css.Raw("overflow", "hidden"),
		}),
		// The caption stays in the accessibility tree and off the screen,
		// exactly as Table's does — the panel around this already carries a
		// visible heading.
		h.Div(srOnlyClass(), resolve(p.T, p.CaptionKey)),
		h.Div(headerRowArgs(template, rowHeight, headerCells)...),
		// The scroller. tabindex makes it reachable by keyboard, which a
		// scrollable region needs in order to be scrollable without a mouse.
		h.Div(
			html.ID(scrollerID),
			html.TabIndex(0),
			h.OnScroll(onScroll),
			css.Class(append([]css.Rule{
				css.Raw("overflow-y", "auto"),
				css.Raw("height", strconv.FormatFloat(viewport, 'f', 0, 64)+"px"),
			}, focusVisible()...)),
			body,
		),
	)
}

// gridTemplate lays every column out on one shared grid track list so the
// header and every body row line up — the alignment a real <table> gives for
// free and a div list has to be told.
func gridTemplate(cols []TableColumn) string {
	if len(cols) == 0 {
		return "1fr"
	}
	out := ""
	for range cols {
		if out != "" {
			out += " "
		}
		// minmax(0, 1fr) rather than 1fr: a bare fr track has an `auto`
		// minimum, so one long user-agent string would widen its column and
		// push the rest out of the row instead of being ellipsised.
		out += "minmax(0, 1fr)"
	}
	return out
}

func virtualRow(template string, height float64) html.PropOption {
	return css.Class(virtualRowRules(template, height))
}

// virtualRowRules is the row geometry as rules, so a caller that needs to
// add more (the header's bottom border) can build a SINGLE class from them
// rather than emitting a second, colliding class prop.
func virtualRowRules(template string, height float64) []css.Rule {
	return []css.Rule{
		css.Display.Grid,
		css.Raw("grid-template-columns", template),
		css.Items.Center,
		// Fixed, and it must equal VirtualListProps.ItemHeight — the spacers
		// are computed from that number, so a row that renders taller makes
		// the scrollbar and the content disagree.
		css.Raw("height", strconv.FormatFloat(height, 'f', 0, 64)+"px"),
	}
}

func virtualHeaderCell() html.PropOption {
	return css.Class([]css.Rule{
		css.PaddingX(tokens.Space(3)),
		css.Raw("text-align", "left"),
		css.FontWeight.Semibold,
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		css.FontSize(tokens.FontSize(tokens.TextXs)),
		bodyFont(),
	})
}

func virtualBodyCell(mono bool) html.PropOption {
	rules := []css.Rule{
		css.PaddingX(tokens.Space(3)),
		css.TextColor(tokens.Color(tokens.RoleText)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.Raw("border-bottom", "1px solid "+string(tokens.Color(tokens.RoleBorder))),
		// One line, clipped. A wrapping cell changes the row's height, which
		// desynchronises the spacers from the scrollbar — see VirtualTable's
		// doc comment.
		css.Raw("white-space", "nowrap"),
		css.Raw("overflow", "hidden"),
		css.Raw("text-overflow", "ellipsis"),
		css.Raw("height", "100%"),
		css.Display.Flex,
		css.Items.Center,
	}
	if mono {
		rules = append(rules, monoFont())
	} else {
		rules = append(rules, bodyFont())
	}
	return css.Class(rules)
}

// headerRowArgs assembles the header row's props and cells into the single
// []any h.Div's variadic parameter needs — Go does not allow a spread
// alongside fixed leading arguments.
func headerRowArgs(template string, height float64, cells []Node) []any {
	out := make([]any, 0, len(cells)+2)
	out = append(out,
		html.Attr("role", "row"),
		// ONE css.Class for this element, not two.
		//
		// This previously passed virtualRow(...) and a second css.Class for
		// the border. Both emit a `class` prop, the later one wins, and the
		// grid rule was silently dropped — so the header laid out as a block
		// and its five labels stacked vertically down the left edge while
		// every body row underneath was a correct grid. Merged, because two
		// class props on one element is not a thing the framework combines.
		css.Class(append(virtualRowRules(template, height),
			css.Raw("border-bottom", "1px solid "+string(tokens.Color(tokens.RoleBorderStrong))),
		)),
	)
	for _, c := range cells {
		out = append(out, c)
	}
	return out
}
