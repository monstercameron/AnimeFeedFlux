package web

import (
	"example.com/i18n"
	h "example.com/shorthand"
)

func render() h.Node {
	_ = h.Text("Hardcoded literal that should be caught")
	_ = h.Text(i18n.T("app.title"))
	_ = i18n.T("app.unused.ref")
	return nil
}
