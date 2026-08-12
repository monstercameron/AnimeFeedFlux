package web

import h "example.com/shorthand"

func renderFlagged(name string) h.Node {
	_ = h.Text("Disconnected — reconnecting…")
	_ = h.Textf("Rendered %d items", count)
	_ = h.Div("Bare child prose")
	_ = h.Button("Save", h.ClassStr("primary"))
	_ = h.Div(h.Title("Tooltip text"), h.Input(h.Placeholder("Search feeds"), h.Alt("icon description")))
	_ = h.Text("Hello, " + name)
	_ = h.Div(h.Aria("label", "Close this dialog"), h.Aria("description", "What this control does"))

	// nolint:i18n -- reason not required to be on this exact literal; this
	// one has no comment attached and must still be flagged.
	_ = h.Text("Unsuppressed literal")

	//nolint:i18n
	_ = h.Text("Bare nolint above this line, should still be flagged")

	return nil
}
