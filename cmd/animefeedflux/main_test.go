package main

import (
	"testing"
)

// The A0 stub handler this file used to test (publishMux/healthz) is gone.
// It was superseded by the real publish plane, and once main.go served the real
// one, the stub was exercised ONLY by these tests — a parallel implementation
// that kept passing while production ran different code. Tests against a
// handler nobody serves are worse than no tests: they report health for
// something that is not deployed.
//
// The real coverage now lives in server_test.go, against buildPublishHandler
// and a real SQLite store.

func TestExitCodesAreDistinct(t *testing.T) {
	// A container restart loop looks identical from the outside whatever the
	// cause, so the codes have to differ: 2 means "you configured it wrong",
	// and restarting will not help.
	if exitOK == exitRuntimeFail || exitOK == exitBadConfig || exitRuntimeFail == exitBadConfig {
		t.Fatalf("exit codes must be distinct: ok=%d runtime=%d config=%d",
			exitOK, exitRuntimeFail, exitBadConfig)
	}
}
