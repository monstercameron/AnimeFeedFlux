package testdata

import h "example.com/shorthand"

func renderInTestdataDir() h.Node {
	return h.Text("This literal lives under a testdata/ directory and must never be flagged")
}
