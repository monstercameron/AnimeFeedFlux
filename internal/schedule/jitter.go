package schedule

import (
	"hash/fnv"
	"time"
)

// Offset returns a deterministic offset in [0, window) derived from slug.
//
// Why it exists (PLAN.md §14.3, "cron thundering herd"): people write
// `0 12 * * *` for every feed, so without jitter twenty feeds fire in the
// same second and spike the provider's rate limits and cost together. Adding
// hash(slug)-derived jitter smears the load while keeping each feed's
// schedule exact-ish and — critically — reproducible: the same slug and
// window must give the same offset across processes and restarts (the UI's
// "next three runs" readback has to match what the scheduler actually does),
// so the source is a hash, never math/rand.
func Offset(slug string, window time.Duration) time.Duration {
	if window <= 0 {
		return 0
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(slug)) // hash.Hash.Write never returns an error
	return time.Duration(h.Sum64() % uint64(window))
}
