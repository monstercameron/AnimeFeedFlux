package settings

// ScreenState is one of the six states TODOS.md's D-FLOW matrix requires
// every list and panel to implement: "loading · empty · populated · error
// · disabled-with-reason · disconnected." Mirrors web/pages/history's
// ScreenState exactly (same names, same precedence) rather than inventing
// a second convention for the same idea.
type ScreenState int

const (
	ScreenLoading ScreenState = iota
	ScreenEmpty
	ScreenPopulated
	ScreenError
	ScreenDisabledWithReason
	ScreenDisconnected
)

// ScreenInputs is everything one settings panel needs to resolve to a
// ScreenState, factored out of the component body so the six-state matrix
// is a pure, unit-testable function rather than something only provable
// by mounting a component and forcing each condition through the DOM.
type ScreenInputs struct {
	// DisabledReason is non-empty when the whole panel is intentionally
	// unusable right now (e.g. Provider's key-rotation controls while the
	// kill switch is on, or any section while ELEVATED — D1-11 forbids
	// /settings entirely under ELEVATED, but a panel-level guard is kept
	// here too since ELEVATED is enforced at the route level by a
	// different package). Takes precedence over everything else.
	DisabledReason string
	// Disconnected mirrors the WebSocket DISCONNECTED application state.
	// Per D-FLOW's own rule, disconnection only forces its own dedicated
	// state when there is no data to show read-only; once a panel has
	// loaded data, staying disconnected keeps showing it (Populated) with
	// the shell's reconnect banner layered on top — mutation controls are
	// disabled by the caller checking Disconnected directly, not by this
	// function.
	Disconnected bool
	Loading      bool
	Err          error
	// ItemCount is 0/1 semantics for a single-value panel (e.g. Provider,
	// Generation, Publishing each render exactly one settings.Settings)
	// and a real count for list panels (active sessions, price-table
	// rows).
	ItemCount int
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
