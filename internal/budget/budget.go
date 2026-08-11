package budget

import "time"

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
// MonthlyUSDCeiling is the DOD-7 ceiling: total estimated spend across every
// feed, over a calendar month (see MonthStart), must stay under it. It is
// deliberately separate from GlobalDailyUSD rather than derived from it — a
// month is not "31 days at the daily cap" (that is 31x the intended monthly
// number), and the two are checked independently so the monthly ceiling
// binds even on a day the daily cap has not been touched yet (e.g. the day
// mid-month spend crosses the monthly line on the first call of the
// morning, long before that day's own daily cap would ever trip).
//
// MonthlyWarnPct, if set to a value in (0, 1], is the fraction of
// MonthlyUSDCeiling at which an allowed call is flagged with Decision.Warn
// so an operator gets a heads-up before generation goes dark for the rest of
// the month rather than discovering it only when a run is refused. Zero
// disables the warning; it never causes a call to be denied — a warning is
// advisory only.
//
// A zero value for any field means "no cap" for that dimension, so a caller
// can enforce only the caps it cares about without special-casing zero
// elsewhere. In particular, a caller that never sets MonthlyUSDCeiling gets
// exactly today's daily-only behaviour, unchanged.
type Limits struct {
	PerFeedDailyTokens int
	PerFeedDailyRuns   int
	GlobalDailyTokens  int
	GlobalDailyUSD     float64
	MonthlyUSDCeiling  float64
	MonthlyWarnPct     float64
}

// Spend is an accumulated total for some scope (one feed, or every feed) over
// some window (a day, or a month — see Request.MonthlySpend), used as the
// "so far" side of a Check call.
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
	ReasonNone               Reason = ""
	ReasonFeedTokenCap       Reason = "feed_daily_token_cap"
	ReasonFeedRunCap         Reason = "feed_daily_run_cap"
	ReasonGlobalTokenCap     Reason = "global_daily_token_cap"
	ReasonGlobalUSDCap       Reason = "global_daily_usd_cap"
	ReasonMonthlyUSDCap      Reason = "monthly_usd_cap"
	ReasonMonthlyApproaching Reason = "monthly_usd_approaching"
)

// Decision is the result of a Check call.
type Decision struct {
	Allow  bool
	Reason Reason

	// Warn is true when Allow is true but this call pushes month-to-date
	// spend across Limits.MonthlyWarnPct of MonthlyUSDCeiling for the first
	// time — i.e. spend was under the threshold before this call and is at
	// or over it including this call. It is computed on the crossing edge
	// only (before < threshold <= after), not on every call once past the
	// threshold, so a caller that logs/alerts on Warn gets one signal per
	// crossing rather than a warning on every remaining call of the month.
	// Warn is never true alongside Allow=false — a denied call reports its
	// denial via Reason instead.
	Warn       bool
	WarnReason Reason
}

// Request describes one prospective call to be checked against budget
// before it is made.
type Request struct {
	Kind      Kind
	Projected int     // estimated total tokens (in+out) this call will use
	USD       float64 // estimated USD cost of this call, priced from Projected

	// MonthlySpend is the accumulated ESTIMATED spend across every feed for
	// the current calendar month so far (see MonthStart), not including this
	// call. It is the monthly analogue of the globalSpend parameter — caller
	// supplied, this package does no time-window computation itself except
	// via the MonthStart helper offered for that purpose. Left at its zero
	// value, MonthlyUSDCeiling can still deny (0+USD > ceiling) but only
	// once USD alone exceeds the ceiling, so a caller that wants the monthly
	// ceiling to mean anything MUST populate this field with real
	// month-to-date spend.
	MonthlySpend Spend
}

// MonthStart returns the first instant (00:00:00 UTC) of the calendar month
// containing t, in UTC.
//
// Calendar month, not a rolling 30-day window, and UTC rather than a
// per-feed local timezone: the ceiling this exists to protect (DOD-7) is a
// provider invoice, and provider bills reset on a calendar-month boundary,
// so a calendar boundary is the only one whose number is directly
// comparable to that invoice. UTC matches the existing daily convention —
// the daily boundary this package's callers already use (see
// cmd/animefeedflux/wire.go's midnightUTC) is UTC midnight, not a per-feed
// local day, and feeds do not carry a timezone for budget purposes (only
// for cron display). The cost of a calendar boundary is a hard reset at
// UTC midnight on the 1st that can surprise an operator mid-spend; that is
// an accepted, documented trade against the alternative (a rolling window)
// never lining up with what the bill says.
func MonthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
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
//
// The monthly ceiling (DOD-7) is checked independently of the daily caps
// above it: it is its own `if`, gated only on l.MonthlyUSDCeiling, so it
// denies whenever month-to-date spend plus this call would cross the line
// regardless of whether any daily cap is configured or how much of today's
// daily allowance remains. That independence is what lets it bind before
// the daily cap is exhausted, which is the case that matters — the day
// mid-month spend would otherwise cross the monthly line while every daily
// check still says yes.
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
	if l.MonthlyUSDCeiling > 0 && req.MonthlySpend.USD+req.USD > l.MonthlyUSDCeiling {
		return Decision{Allow: false, Reason: ReasonMonthlyUSDCap}
	}

	d := Decision{Allow: true, Reason: ReasonNone}
	if l.MonthlyUSDCeiling > 0 && l.MonthlyWarnPct > 0 {
		threshold := l.MonthlyWarnPct * l.MonthlyUSDCeiling
		before := req.MonthlySpend.USD
		after := before + req.USD
		if before < threshold && after >= threshold {
			d.Warn = true
			d.WarnReason = ReasonMonthlyApproaching
		}
	}
	return d
}
