//go:build js && wasm

package ui

import (
	"time"

	"github.com/monstercameron/GoWebComponents/v5/ui"
)

// pump.go works around a GWC v5.0.1 defect that makes an in-flight mutation
// look like a hung one.
//
// # What goes wrong
//
// A state update made from a goroutine ("off-loop") does not apply
// immediately. GWC queues it and books a drain
// (internal/runtime/inbox.go: PostAsync → scheduler.SetTimeout(DrainAsyncInbox,
// 0)), and the drain defers itself while a render pass is in flight, re-booking
// rather than running. Crucially, PostAsync only books a drain when one is not
// already marked scheduled — so if a booked drain never actually runs, every
// later post joins a queue that nothing will ever come back for. The update is
// stored and simply never rendered.
//
// Measured on /generate: pressing Save ran the whole handler — the goroutine
// entered, the Create RPC returned, the feed row was written to the database —
// and the button stayed on "Saving…" forever, because every state update after
// the RPC sat in that queue. A JS-side marker written straight to `window` from
// the same goroutine proved the Go code ran to completion; nothing about it
// reached the screen. It is not headless-only (reproduced in a headed browser
// with background throttling disabled) and it is not the transport.
//
// The tell was that adding an unrelated goroutine that touched state once a
// second made the whole thing work: with renders happening regularly, the
// deferred drain gets its turn.
//
// # What this does
//
// Pump keeps that heartbeat alive for exactly as long as an operation is
// running, and stops it afterwards. It is a workaround, and it is deliberately
// narrow: no permanent timer, no polling when the page is idle, and nothing to
// remove from call sites but one wrapper when the framework is fixed.
//
// Use it around any state-mutating async work whose result the operator waits
// on — saves, deletes, toggles. Reads that populate a list on first paint do
// not need it: their updates are followed by the rest of page load, which
// produces the renders that flush them anyway.

// pumpInterval is how often the pump nudges the runtime while work is in
// flight. Short enough that a completed save feels immediate, long enough
// that it is a handful of renders rather than a spin loop.
const pumpInterval = 150 * time.Millisecond

// pumpMaxTicks bounds the pump so a call that never returns cannot leave a
// timer running for the life of the tab. At 150ms this is 90 seconds, which
// is longer than any control-plane mutation and shorter than "forever".
const pumpMaxTicks = 600

// pumpGrace is how long the heartbeat keeps beating after the operation
// says it is done.
//
// A mutation almost always kicks off a refetch as its last act ("saved —
// now reload the list"), and that refetch's own state update lands after
// the operation returns. Stopping the pump the instant work() returned put
// exactly that update back into the queue nothing comes to drain: the feed
// was created, the button un-stuck, and the list still did not show it.
// Two seconds covers a follow-on read against a local control plane with
// room to spare.
const pumpGrace = 2 * time.Second

// Pump is a render heartbeat scoped to one in-flight operation.
type Pump struct {
	tick    ui.State[int]
	running ui.State[bool]
}

// UsePump registers the pump's state. It is a hook: call it unconditionally,
// once per component, in the same position on every render.
func UsePump() Pump {
	p := Pump{
		tick:    ui.UseState(0),
		running: ui.UseState(false),
	}
	// The heartbeat itself. Keyed on the running flag, so it exists only
	// while work is outstanding.
	ui.UseEffectOf(func() func() {
		if !p.running.Get() {
			return nil
		}
		stopped := false
		go func() {
			for i := 0; i < pumpMaxTicks; i++ {
				time.Sleep(pumpInterval)
				if stopped || !p.running.Get() {
					return
				}
				p.tick.Update(func(n int) int { return n + 1 })
			}
		}()
		return func() { stopped = true }
	}, p.running.Get())
	return p
}

// Run executes work in a goroutine with the pump held up for its duration
// plus pumpGrace. work must call done() exactly once, including on its error
// paths, or the pump beats until the cap.
func (p Pump) Run(work func(done func())) {
	p.running.Set(true)
	go func() {
		var once bool
		work(func() {
			if once {
				return
			}
			once = true
			// Keep beating through the grace window so any refetch the
			// operation triggered renders too.
			go func() {
				time.Sleep(pumpGrace)
				p.running.Set(false)
			}()
		})
	}()
}
