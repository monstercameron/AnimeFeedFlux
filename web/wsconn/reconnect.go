// reconnect.go has no build tag (unlike conn.go/clients.go) specifically so
// the mapping from web/backoff.DefaultPolicy onto grpctunnel's own
// ReconnectConfig is host-testable: grpctunnel.ReconnectConfig itself is
// declared in api.go, a file in the GoGRPCBridge module that carries no
// build tag either, so referencing it here does not pull in any js-only
// code. This is the "make sure adding clients does not create a second
// connection path that bypasses [backoff.DefaultPolicy]" requirement's
// test seam: Connect (conn.go, js-only) calls reconnectConfigFromPolicy
// exactly once, so there is exactly one place backoff.DefaultPolicy feeds
// into the tunnel's reconnect behavior, and this function is that seam
// made independently checkable.
package wsconn

import (
	"github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel"

	"github.com/monstercameron/AnimeFeedFlux/web/backoff"
)

// reconnectConfigFromPolicy maps a web/backoff.Policy onto grpctunnel's
// ReconnectConfig field-for-field. Jitter and MinConnectTimeout are left at
// grpctunnel's own zero-value defaults deliberately: backoff.Policy models
// full-jitter itself (see that package's doc comment), so mapping anything
// onto grpctunnel's separate Jitter field would double-apply jitter rather
// than compose with it.
func reconnectConfigFromPolicy(p backoff.Policy) grpctunnel.ReconnectConfig {
	return grpctunnel.ReconnectConfig{
		InitialDelay: p.Base,
		MaxDelay:     p.Max,
		Multiplier:   p.Factor,
	}
}
