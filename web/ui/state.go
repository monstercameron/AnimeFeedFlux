package ui

import (
	"github.com/monstercameron/GoWebComponents/v5/css"
	"github.com/monstercameron/GoWebComponents/v5/html"
	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"

	"github.com/monstercameron/AnimeFeedFlux/web/tokens"
)

// ListState is one of the six states every list/panel in Phase D must render
// (PLAN.md §12.6, TODOS.md D0-14/D0-23, D-FLOW's per-screen state matrix).
type ListState int

const (
	StateLoading ListState = iota
	StateEmpty
	StatePopulated
	StateError
	StateDisabledWithReason
	StateDisconnected
)

func (s ListState) String() string {
	switch s {
	case StateLoading:
		return "loading"
	case StateEmpty:
		return "empty"
	case StatePopulated:
		return "populated"
	case StateError:
		return "error"
	case StateDisabledWithReason:
		return "disabled-with-reason"
	case StateDisconnected:
		return "disconnected"
	default:
		return "unknown"
	}
}

// ListStateInput is the pure input SelectListState resolves. A caller wires
// its actual loading flag / fetch error / connectivity flag / kill-switch
// reason / row count into this struct — SelectListState has no DOM or
// network dependency, so it is host-testable (TODOS.md "Tests" §, state
// selection).
type ListStateInput struct {
	// DisabledReasonKey, when non-empty, means the surface this list backs
	// is disabled (e.g. the generation kill switch) and WHY — the reason a
	// disabled control must always carry (PLAN.md §12.3: "the kill switch
	// disables it with a visible reason rather than leaving a dead
	// control").
	DisabledReasonKey string
	Disconnected      bool
	ErrorKey          string
	Loading           bool
	ItemCount         int
}

// SelectListState resolves the six-state matrix's precedence:
//
//  1. DisabledWithReason — a kill switch or similar block outranks
//     everything else in the list, because it changes what the operator can
//     DO here, not just what they can currently see.
//  2. Disconnected — the data on screen (if any) may be stale and no new
//     data can arrive; this must be visible even over a populated list,
//     which is why it is checked before Error/Loading/Empty rather than
//     folded into them.
//  3. Error — the last fetch failed.
//  4. Loading — a fetch is in flight (and none of the above apply).
//  5. Empty — the fetch succeeded with zero rows.
//  6. Populated — the fetch succeeded with at least one row.
//
// This ordering is a documented modeling choice (D-FLOW does not rank the
// six itself), analogous to web/appstate's own documented precedence note
// for the overlapping DISCONNECTED/KILLED application states.
func SelectListState(in ListStateInput) ListState {
	switch {
	case in.DisabledReasonKey != "":
		return StateDisabledWithReason
	case in.Disconnected:
		return StateDisconnected
	case in.ErrorKey != "":
		return StateError
	case in.Loading:
		return StateLoading
	case in.ItemCount == 0:
		return StateEmpty
	default:
		return StatePopulated
	}
}

// StatePanelProps drives StatePanel: resolve the state once with
// SelectListState, then supply the one render callback that actually gets
// used. Populated is a func() []Node (not a static slice) so the caller does
// not need to build its row nodes on every render regardless of state.
type StatePanelProps struct {
	T                  T
	State              ListState
	ErrorKey           string
	ErrorArgs          []any
	OnRetry            func() // shown on the error view when non-nil
	DisabledReasonKey  string
	DisabledReasonArgs []any  // interpolation args for DisabledReasonKey (e.g. a kill-switch reason naming a budget figure or a time)
	ReconnectingKey    string // shown on the disconnected view; defaults to a generic message if empty
	EmptyKey           string
	Populated          func() []Node
	// OnErrorFallback lets a caller add a details/log link next to the retry
	// button; optional.
}

// StatePanel renders the ONE view matching props.State — the shared
// implementation of loading/empty/populated/error/disabled-with-reason/
// disconnected so no list hand-rolls its own inconsistent version of these
// (D0-14/D0-23/D6-14: every string here is an i18n key).
func StatePanel(p StatePanelProps) Node {
	switch p.State {
	case StateLoading:
		return loadingView(p.T)
	case StateError:
		return errorView(p.T, p.ErrorKey, p.ErrorArgs, p.OnRetry)
	case StateDisabledWithReason:
		return disabledWithReasonView(p.T, p.DisabledReasonKey, p.DisabledReasonArgs)
	case StateDisconnected:
		return disconnectedView(p.T, p.ReconnectingKey)
	case StateEmpty:
		return emptyView(p.T, p.EmptyKey)
	default: // StatePopulated
		if p.Populated == nil {
			return nil
		}
		return h.Fragment(p.Populated())
	}
}

func stateContainer(rules ...css.Rule) html.PropOption {
	base := []css.Rule{
		css.Display.Flex,
		css.FlexDir.Col,
		css.Items.Center,
		css.Justify.Center,
		css.Gap(tokens.Space(3)),
		css.Padding(tokens.Space(8)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		bodyFont(),
	}
	return css.Class(append(base, rules...))
}

func loadingView(t T) Node {
	return h.Div(
		html.Role("status"),
		html.Aria("live", "polite"),
		stateContainer(),
		spinner(),
		resolve(t, "state.loading"),
	)
}

// spinner is a CSS-only spinner, motion-gated behind MotionOK (prefers-
// reduced-motion:no-preference) so it costs nothing for a user whose OS
// preference is unknown and is neutralized outright when they asked for
// reduced motion — it just renders as a static ring instead.
func spinner() Node {
	spin := css.Keyframes("aff-spin",
		css.At("from", css.Raw("transform", "rotate(0deg)")),
		css.At("to", css.Raw("transform", "rotate(360deg)")),
	)
	rules := []css.Rule{
		css.W(css.Px(20)),
		css.H(css.Px(20)),
		css.Rounded(tokens.Radius(tokens.RadiusFull)),
		css.Border(css.Px(2), tokens.Color(tokens.RoleBorder)),
		css.Raw("border-top-color", string(tokens.Color(tokens.RoleAccent))),
	}
	rules = append(rules, css.Media(css.MotionOK, append([]css.Rule{spin, css.Animation(tokens.Duration(tokens.DurationSlow), css.Linear)}, css.Raw("animation-iteration-count", "infinite"))...)...)
	return h.Span(css.Class(rules), html.Aria("hidden", "true"))
}

func emptyView(t T, key string) Node {
	if key == "" {
		key = "state.empty"
	}
	return h.Div(stateContainer(), resolve(t, key))
}

func errorView(t T, key string, args []any, onRetry func()) Node {
	if key == "" {
		key = "state.error"
	}
	children := []Node{
		h.Div(html.Role("alert"), css.Class([]css.Rule{css.TextColor(tokens.Color(tokens.RoleDanger))}), resolve(t, key, args...)),
	}
	if onRetry != nil {
		children = append(children, Button(ButtonProps{T: t, LabelKey: "action.retry", Variant: ButtonSecondary, OnClick: onRetry}))
	}
	return h.Div(stateContainer(), children)
}

func disabledWithReasonView(t T, reasonKey string, reasonArgs []any) Node {
	return h.Div(
		stateContainer(css.TextColor(tokens.Color(tokens.RoleWarning))),
		h.Span(css.Class([]css.Rule{css.FontWeight.Semibold}), resolve(t, "state.disabled")),
		// The reason itself — the whole point of this state (PLAN.md §12.3):
		// a dead control with no explanation is indistinguishable from a bug.
		h.Span(resolve(t, reasonKey, reasonArgs...)),
	)
}

func disconnectedView(t T, reconnectingKey string) Node {
	if reconnectingKey == "" {
		reconnectingKey = "state.disconnected"
	}
	return h.Div(
		html.Role("status"),
		html.Aria("live", "polite"),
		stateContainer(),
		signalDot(),
		resolve(t, reconnectingKey),
	)
}

// signalDot is the reconnect banner's pulsing indicator — the one
// deliberately-noticed motion moment in this package, justified because it
// literally IS the state being reported (a WebSocket that is trying to come
// back), not decoration. Gated behind MotionOK like spinner.
func signalDot() Node {
	pulse := css.Keyframes("aff-pulse",
		css.At("0%", css.OpacityNum(css.Num(1))),
		css.At("50%", css.OpacityNum(css.Num(0.35))),
		css.At("100%", css.OpacityNum(css.Num(1))),
	)
	rules := []css.Rule{
		css.W(css.Px(8)),
		css.H(css.Px(8)),
		css.Rounded(tokens.Radius(tokens.RadiusFull)),
		css.Bg(tokens.Color(tokens.RoleDanger)),
	}
	rules = append(rules, css.Media(css.MotionOK, append([]css.Rule{pulse, css.Animation(tokens.Duration(tokens.DurationSlow), css.EaseInOut)}, css.Raw("animation-iteration-count", "infinite"))...)...)
	return h.Span(css.Class(rules), html.Aria("hidden", "true"))
}
