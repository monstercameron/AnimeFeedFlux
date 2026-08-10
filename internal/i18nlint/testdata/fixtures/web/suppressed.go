package web

import h "example.com/shorthand"

func renderSuppressed() h.Node {
	//nolint:i18n -- deliberately untranslated operator diagnostic string
	_ = h.Text("Raw diagnostic string for engineers only")

	_ = h.Text("Same line suppression") //nolint:i18n: seeded fixture, not real UI copy

	return nil
}
