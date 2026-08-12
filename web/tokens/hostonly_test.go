package tokens

import (
	"runtime"
	"testing"
)

// skipIfWasm skips a test that can only observe what it needs to observe on a
// host build.
//
// This exists because web/ is now covered under GOOS=js GOARCH=wasm as well as
// on the host (scripts/coverage-wasm.sh), and a handful of tests in this
// package assert on the STRING that css.Emit produced. On the host, GWC's css
// package buffers rules so css.Harvest() can return them. In a browser there
// is nothing to buffer — the rules go straight into the document's stylesheet
// — so Harvest() returns "" and these tests fail against an empty string while
// the code under test is working perfectly.
//
// Skipping is the honest answer rather than rewriting them to inspect the DOM:
// what they check (the shape of the emitted CSS text) is a property of the
// rules this package DECLARES, which is identical in both builds. Re-asserting
// it through a browser stylesheet would test GWC's css package, not this one.
//
// A runtime check rather than a build tag, so the test still compiles under
// both and cannot rot unnoticed in the build nobody runs.
func skipIfWasm(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "js" {
		t.Skip("css.Harvest() buffers nothing in a browser build — rules go straight to the " +
			"document stylesheet, so there is no emitted string to assert on here")
	}
}
