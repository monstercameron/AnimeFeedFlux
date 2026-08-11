package settings

import (
	"google.golang.org/protobuf/types/known/timestamppb"
	"sort"
	"time"
)

// SessionRow is this page's view of one AuthService.ListSessions row
// (proto/aff/v1/auth.proto's Session message), independent of the
// generated protobuf type so this file stays host-testable without a
// build tag and without importing gen/aff/v1 here (client.go, which does
// need the generated types for the RPC call itself, converts one to the
// other).
type SessionRow struct {
	ID         string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
	IP         string
	UserAgent  string
	RevokedAt  time.Time // zero value means still live
	IsCurrent  bool
}

// Revoked reports whether this row has already been revoked (RevokedAt
// set) — auth.proto's own comment: "Set once revoked, so the UI can still
// show a recently-revoked row rather than having it vanish."
func (r SessionRow) Revoked() bool {
	return !r.RevokedAt.IsZero()
}

// SortSessionsForDisplay orders rows most-recently-active first, with the
// caller's own session ("this device") pinned to the top regardless of
// its LastSeenAt — an admin reviewing "who else is logged in" (D4-04)
// wants their own row identified first, not buried by timestamp.
func SortSessionsForDisplay(rows []SessionRow) []SessionRow {
	out := make([]SessionRow, len(rows))
	copy(out, rows)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].IsCurrent != out[j].IsCurrent {
			return out[i].IsCurrent
		}
		// Revoked rows sink below every live one.
		//
		// They are still shown — auth.proto is explicit that a
		// recently-revoked row should not vanish — but interleaved by
		// last-seen they buried the live sessions completely: after one
		// `aff admin reset` this panel listed 126 rows of which exactly one
		// was active, sorted so the dead ones came first. The question this
		// panel answers is "who else is logged in", and an answer you have
		// to scroll 125 rows to find is not one.
		if out[i].Revoked() != out[j].Revoked() {
			return !out[i].Revoked()
		}
		return out[i].LastSeenAt.After(out[j].LastSeenAt)
	})
	return out
}

// MaxRevokedShown bounds how many already-revoked rows are listed after the
// live ones. Enough to see what was just ended (the reason auth.proto keeps
// them visible at all), few enough that a bulk revoke does not turn this
// panel into a log.
const MaxRevokedShown = 10

// TrimRevoked keeps every live session and at most MaxRevokedShown revoked
// ones, returning the trimmed list and how many revoked rows were dropped so
// the caller can say so rather than silently truncating — a list that hides
// rows without admitting it is how someone concludes a session is gone when
// it is merely off-screen.
//
// Input must already be sorted by SortSessionsForDisplay, which is what puts
// the revoked rows last.
func TrimRevoked(rows []SessionRow) (kept []SessionRow, hidden int) {
	revoked := 0
	kept = make([]SessionRow, 0, len(rows))
	for _, r := range rows {
		if !r.Revoked() {
			kept = append(kept, r)
			continue
		}
		revoked++
		if revoked <= MaxRevokedShown {
			kept = append(kept, r)
		}
	}
	if revoked > MaxRevokedShown {
		hidden = revoked - MaxRevokedShown
	}
	return kept, hidden
}

// RevocableSessionCount is how many rows RevokeAllSessions would actually
// end — used to decide whether the revoke-all kebab item should even be
// enabled/shown-with-a-count (revoking zero-or-one session, the caller's
// own, is a poor fit for a "revoke ALL OTHER devices" affordance).
func RevocableSessionCount(rows []SessionRow) int {
	n := 0
	for _, r := range rows {
		if !r.Revoked() {
			n++
		}
	}
	return n
}

// RecoveryCodesLowThreshold mirrors auth.proto's own documented UX rule
// ("The UI nags at <=2 remaining") so the regenerate-recovery-codes nag
// on this page and the elevated recovery flow's equivalent nag (owned by
// a different package) apply the identical number rather than each
// picking one independently.
const RecoveryCodesLowThreshold = 2

// RecoveryCodesLow reports whether remaining recovery codes should
// trigger the "regenerate soon" nag (Security section, D4-03).
func RecoveryCodesLow(remaining int) bool {
	return remaining <= RecoveryCodesLowThreshold
}

// protoTime converts an optional protobuf timestamp to a Go time, mapping
// "absent" to the ZERO time rather than to the Unix epoch.
//
// timestamppb.Timestamp.AsTime() returns time.Unix(0, 0) for a nil message,
// and time.Time.IsZero() reports false for 1970 — it is only true for the
// year-1 zero value. Any "is this set?" check written as !t.IsZero() against
// a converted optional timestamp is therefore wrong by default, which is
// exactly how every session came to report itself revoked. Anything reading
// an optional timestamp off the wire should come through here.
func protoTime(ts *timestamppb.Timestamp) time.Time {
	if ts == nil || !ts.IsValid() {
		return time.Time{}
	}
	return ts.AsTime()
}
