package settings

import (
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
		return out[i].LastSeenAt.After(out[j].LastSeenAt)
	})
	return out
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
