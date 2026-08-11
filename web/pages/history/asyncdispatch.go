//go:build js

package history

import (
	"errors"
	"time"

	"github.com/monstercameron/GoWebComponents/v5/ui"
)

// errLoadTimedOut is what a page shows when every attempt went unanswered.
// Deliberately distinct from a server rejection: nothing said no, nothing
// said anything.
var errLoadTimedOut = errors.New("history: the server did not answer in time")

// asyncdispatch.go exists because of one hard-won fact about GWC's
// ui.UseReducer:
//
//	**Dispatch called from a goroutine updates the reducer state but does
//	not schedule a re-render.**
//
// The state is genuinely there — the next render, whenever something else
// causes one, shows it. Nothing causes one on its own.
//
// This is why /history sat on "Loading…" forever. Both tabs load their data
// in a goroutine and Dispatch the result from inside it, so the runs were
// fetched, decoded and stored, and the screen kept showing the spinner. It
// looked exactly like a hung request, which is what sent the first
// investigation after the transport instead of the renderer. The tell was
// that clicking Refresh — any event at all, even one whose own load had not
// returned yet — immediately painted the rows that had arrived seconds
// earlier.
//
// ui.UseState does NOT have this problem: setting it from a goroutine
// re-renders (which is why /settings, built on UseState, never showed this).
// So the fix is to pair the reducer with a state cell and touch it on every
// dispatch that originates off the event loop.
//
// Use this for EVERY dispatch made from a goroutine. Dispatching directly is
// still correct from an event handler, where a render is already coming.

// renderPump forces a re-render for reducer updates made off the event loop.
type renderPump struct {
	tick ui.State[int]
}

// useRenderPump registers the pump's state cell. Call it unconditionally,
// once per component, alongside the reducer it belongs to — it is a hook.
func useRenderPump() renderPump {
	return renderPump{tick: ui.UseState(0)}
}

// bump schedules a render. Safe to call from a goroutine; that is its
// entire purpose.
//
// Update, not Set(Get()+1). A goroutine's Get() returns the value captured at
// the render that spawned it, so two bumps between renders both compute the
// same number, and setting a state cell to the value it already holds is a
// no-op that schedules nothing. That is a subtle way to write a render pump
// that does not pump — and it is what the first version of this did.
func (p renderPump) bump() {
	p.tick.Update(func(n int) int { return n + 1 })
}

// loadRetryDelay / loadMaxAttempts bound the read retry below.
//
// 2s is far longer than a healthy control-plane read over a loopback
// WebSocket (single-digit milliseconds) and short enough that a wedged first
// call is invisible to the operator rather than a half-minute of spinner.
const (
	loadRetryDelay  = 2 * time.Second
	loadMaxAttempts = 3

	// searchDebounce is how long the item search waits for typing to stop.
	// Every keystroke otherwise ran a full FTS5 query whose result was
	// discarded by the next one. 250ms is below the threshold where a search
	// field feels laggy and above a fast typist's inter-key interval.
	searchDebounce = 250 * time.Millisecond
)

// retryRead runs a READ rpc, giving up on each attempt after loadRetryDelay
// and trying again, up to loadMaxAttempts. It calls onResult exactly once.
//
// This exists because of the transport wedge documented in
// web/wsconn/clients.go's guardUnary: the first call issued right after login,
// while the tunnel is being replaced with the authenticated one, can be handed
// to a transport that never answers and cannot be interrupted (the browser
// WebSocket conn's deadlines are no-ops). The next call, issued a moment
// later, succeeds immediately. Retrying is therefore not a workaround for
// flakiness — it is the difference between a page that loads and a page that
// shows a spinner until someone clicks something.
//
// READS ONLY. Every caller must be idempotent, because a wedged attempt may
// still be in flight and may still reach the server. Never wrap a create,
// update, delete, promote or import in this.
func retryRead[T any](call func() (T, error), onResult func(T, error)) {
	go func() {
		var zero T
		var lastErr error
		for attempt := 0; attempt < loadMaxAttempts; attempt++ {
			type res struct {
				val T
				err error
			}
			done := make(chan res, 1)
			go func() {
				v, err := call()
				done <- res{v, err}
			}()
			select {
			case r := <-done:
				// A real answer, success or failure, ends it. Only silence
				// is retried: a server that said no will say no again.
				onResult(r.val, r.err)
				return
			case <-time.After(loadRetryDelay):
				lastErr = errLoadTimedOut
			}
		}
		onResult(zero, lastErr)
	}()
}
