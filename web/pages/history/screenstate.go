package history

// ScreenState is one of the six states TODOS.md's D-FLOW matrix requires
// every list and panel to implement: "loading · empty · populated · error
// · disabled-with-reason · disconnected." Runs and Items each resolve to
// exactly one of these per render.
type ScreenState int

const (
	ScreenLoading ScreenState = iota
	ScreenEmpty
	ScreenPopulated
	ScreenError
	ScreenDisabledWithReason
	ScreenDisconnected
)

// ScreenInputs is everything one list needs to resolve to a ScreenState,
// factored out of the component body so the six-state matrix is a pure,
// unit-testable function rather than something only provable by mounting
// a component and forcing each condition through the DOM.
type ScreenInputs struct {
	// DisabledReason is non-empty when the whole panel is intentionally
	// unusable right now (e.g. the KILLED application state disabling
	// generate/sample-adjacent actions) and takes precedence over
	// everything else — an operator needs to see the reason, not a stale
	// error or an empty-looking list.
	DisabledReason string
	// Disconnected mirrors the WebSocket DISCONNECTED application state
	// (TODOS.md D-FLOW). Per D-FLOW's own rule — "Read-only view of loaded
	// data, reconnect banner" — disconnection only forces its own
	// dedicated state when there is no data to show read-only; once a
	// list has data, staying disconnected keeps showing it (Populated)
	// with the shell's reconnect banner layered on top, not a blank
	// disconnected placeholder.
	Disconnected bool
	Loading      bool
	Err          error
	ItemCount    int
}

// ComputeScreenState applies that precedence: disabled-with-reason, then
// disconnected-with-no-data, then error, then loading, then empty, then
// populated.
func ComputeScreenState(in ScreenInputs) ScreenState {
	switch {
	case in.DisabledReason != "":
		return ScreenDisabledWithReason
	case in.Disconnected && in.ItemCount == 0:
		return ScreenDisconnected
	case in.Err != nil:
		return ScreenError
	case in.Loading:
		return ScreenLoading
	case in.ItemCount == 0:
		return ScreenEmpty
	default:
		return ScreenPopulated
	}
}
