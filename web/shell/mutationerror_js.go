//go:build js && wasm

package shell

import "github.com/monstercameron/AnimeFeedFlux/web/wsconn"

// ErrDisconnected re-exports wsconn.ErrDisconnected so page packages that
// already depend on web/shell for SessionAtom/RegisterPage/Conn can
// classify a mutation error against it without also importing web/wsconn
// directly — web/pages/settings/client.go, for one, deliberately keeps
// wsconn out of that package's own dependency graph (see its doc comment),
// and this avoids forcing every page to reach past this package to get at
// the one sentinel D0-10 needs them to check.
var ErrDisconnected = wsconn.ErrDisconnected

// IsDisconnected is IsDisconnectedError (mutationerror.go) pinned to the
// real wsconn.ErrDisconnected — the seam a page's own error-rendering is
// meant to call so a refusal because the socket is down renders as "you
// are offline", not the same "save failed" text a genuine server
// rejection would get. wsconn.Conn already refuses every guarded RPC with
// this sentinel (web/wsconn/clients.go's guardCall, applied uniformly to
// Auth/Feed/Item/Run/Sample/System), so the sentinel reaching here is
// already guaranteed distinguishable; what was missing before this change
// is any caller on the page side actually branching on it — see this
// package's report on how much of that gap this task's allowed file list
// let it close (settings/wiring.go's sign-out control does; the
// pre-existing forms in render_security.go/render_provider.go/
// items_ui.go/forms_ui.go, all out of this task's allowed paths, do not
// yet).
func IsDisconnected(err error) bool {
	return IsDisconnectedError(err, ErrDisconnected)
}
