// mutationerror.go carries D0-10's error-classification logic. No build
// tag deliberately — this is the pure half of "queue or refuse mutations
// while DISCONNECTED, never fail silently": web/wsconn's guarded clients
// (web/wsconn/clients.go) already refuse every RPC with the distinguishable
// wsconn.ErrDisconnected sentinel the instant the socket is not Ready, but
// that sentinel only closes the loop D0-10 actually cares about — "a page
// cannot tell [a disconnected refusal] apart from a bare error and will
// otherwise show 'save failed' for 'you are offline'" — if something
// downstream of the RPC call actually branches on it. IsDisconnectedError
// takes the sentinel as a parameter (rather than importing web/wsconn,
// which is js-only per its own //go:build tag) specifically so this
// classification is host-testable the same way web/guard and web/appstate
// are: verified once here with `go test ./web/...`, not only provable by
// compiling a WASM binary and running it in a browser.
package shell

import "errors"

// IsDisconnectedError reports whether err represents a refusal because the
// control-plane socket was not Ready, as opposed to any other failure
// (validation, a server-side rejection, ...). sentinel is the disconnect
// marker to check against (mutationerror_js.go pins it to the real
// wsconn.ErrDisconnected for the actual WASM build); a nil err or a nil
// sentinel is never classified as a disconnection.
func IsDisconnectedError(err, sentinel error) bool {
	return err != nil && sentinel != nil && errors.Is(err, sentinel)
}
