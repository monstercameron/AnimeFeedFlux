package wsconn

import (
	"testing"

	"github.com/monstercameron/AnimeFeedFlux/web/backoff"
)

// TestReconnectConfigFromPolicy locks the mapping from
// backoff.DefaultPolicy onto grpctunnel.ReconnectConfig — the "backoff
// sequence unchanged" check TODOS.md asks for, made possible on the host
// (no GOOS=js needed) because reconnect.go carries no build tag. A change
// here that isn't also a deliberate change to backoff.DefaultPolicy itself
// means Connect started dialing with a different reconnect curve than the
// rest of the shell believes it has.
func TestReconnectConfigFromPolicy(t *testing.T) {
	got := reconnectConfigFromPolicy(backoff.DefaultPolicy)

	if got.InitialDelay != backoff.DefaultPolicy.Base {
		t.Errorf("InitialDelay = %v, want backoff.DefaultPolicy.Base = %v", got.InitialDelay, backoff.DefaultPolicy.Base)
	}
	if got.MaxDelay != backoff.DefaultPolicy.Max {
		t.Errorf("MaxDelay = %v, want backoff.DefaultPolicy.Max = %v", got.MaxDelay, backoff.DefaultPolicy.Max)
	}
	if got.Multiplier != backoff.DefaultPolicy.Factor {
		t.Errorf("Multiplier = %v, want backoff.DefaultPolicy.Factor = %v", got.Multiplier, backoff.DefaultPolicy.Factor)
	}
	// Jitter/MinConnectTimeout must stay at grpctunnel's zero-value
	// defaults (reconnect.go's doc comment explains why: backoff.Policy
	// already models full jitter itself).
	if got.Jitter != 0 {
		t.Errorf("Jitter = %v, want 0 (backoff.Policy already models jitter)", got.Jitter)
	}
	if got.MinConnectTimeout != 0 {
		t.Errorf("MinConnectTimeout = %v, want 0 (grpctunnel default)", got.MinConnectTimeout)
	}
}
