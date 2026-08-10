package budget

// Kind distinguishes why tokens are about to be spent. It exists purely so
// callers can label a call for logging/metrics — Check treats every Kind
// identically, because sampling draws from the same budget as scheduled
// generation (PLAN.md §13). Without that, the safety net has a hole exactly
// where the interactive, easy-to-repeat action (a manual sample/dry-run
// click) is — the one most likely to be mashed by a human in a loop.
type Kind string

const (
	KindScheduled Kind = "scheduled" // cron-triggered generation
	KindSample    Kind = "sample"    // interactive /sample or CLI --dry-run
)

// Limits are the caps enforced before a call is made. PerFeedDailyTokens and
// PerFeedDailyRuns cap what a single feed may spend/run in a day;
// GlobalDailyTokens and GlobalDailyUSD sit on top of every feed combined,
// because the failure mode §13 calls out is N feeds each individually within
// budget but the sum blowing past what the operator intended.
//
// A zero value for any field means "no cap" for that dimension, so a caller
// can enforce only the caps it cares about without special-casing zero
// elsewhere.
type Limits struct {
	PerFeedDailyTokens int
	PerFeedDailyRuns   int
	GlobalDailyTokens  int
	GlobalDailyUSD     float64
}

// Spend is an accumulated total for some scope (one feed, or every feed) over
// some window (a day), used as the "so far" side of a Check call.
type Spend struct {
	TokensIn  int
	TokensOut int
	Runs      int
	USD       float64
}

// Tokens returns the combined input+output token count.
func (s Spend) Tokens() int { return s.TokensIn + s.TokensOut }

// Reason is a stable token identifying why a Check call denied a request.
// It is a token rather than a formatted string so callers (dashboard,
// metrics, logs) can switch on it without string-matching prose that might
// be reworded later.
type Reason string

const (
	ReasonNone           Reason = ""
	ReasonFeedTokenCap   Reason = "feed_daily_token_cap"
	ReasonFeedRunCap     Reason = "feed_daily_run_cap"
	ReasonGlobalTokenCap Reason = "global_daily_token_cap"
	ReasonGlobalUSDCap   Reason = "global_daily_usd_cap"
)

// Decision is the result of a Check call.
type Decision struct {
	Allow  bool
	Reason Reason
}

// Request describes one prospective call to be checked against budget
// before it is made.
type Request struct {
	Kind      Kind
	Projected int     // estimated total tokens (in+out) this call will use
	USD       float64 // estimated USD cost of this call, priced from Projected
}

// Check decides whether a call may proceed, given the limits, the current
// spend for the feed making the call, the current spend across every feed,
// and the projected token count of the call about to be made.
//
// Enforcement happens BEFORE the call, never after (§13): projected is added
// to the existing spend and compared to each cap, so a call that would push
// spend over a limit is denied rather than allowed and reconciled later.
//
// Per-feed caps are checked before the global cap so a denial names the
// tightest binding reason first, but the global ceiling is what ultimately
// protects the account regardless of which feed asks — every feed's request
// passes through the same global comparison, Kind included.
func Check(l Limits, feedSpend, globalSpend Spend, projected int) Decision {
	return CheckRequest(l, feedSpend, globalSpend, Request{Projected: projected})
}

// CheckRequest is Check with an explicit Request, letting callers pass Kind
// and a pre-priced USD estimate. Sampling and scheduled requests are checked
// through this exact same path with no branch on Kind — that is what makes
// "sampling shares the scheduled budget" true rather than just documented.
func CheckRequest(l Limits, feedSpend, globalSpend Spend, req Request) Decision {
	if l.PerFeedDailyRuns > 0 && feedSpend.Runs+1 > l.PerFeedDailyRuns {
		return Decision{Allow: false, Reason: ReasonFeedRunCap}
	}
	if l.PerFeedDailyTokens > 0 && feedSpend.Tokens()+req.Projected > l.PerFeedDailyTokens {
		return Decision{Allow: false, Reason: ReasonFeedTokenCap}
	}
	if l.GlobalDailyTokens > 0 && globalSpend.Tokens()+req.Projected > l.GlobalDailyTokens {
		return Decision{Allow: false, Reason: ReasonGlobalTokenCap}
	}
	if l.GlobalDailyUSD > 0 && globalSpend.USD+req.USD > l.GlobalDailyUSD {
		return Decision{Allow: false, Reason: ReasonGlobalUSDCap}
	}
	return Decision{Allow: true, Reason: ReasonNone}
}
