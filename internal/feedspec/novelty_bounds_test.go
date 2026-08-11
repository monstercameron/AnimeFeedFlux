package feedspec

import "testing"

// Novelty had no range validation at all, and one of its fields buys provider
// calls: generate.Run computes maxAttempts = 1 + MaxNoveltyRetries and calls
// the model once per attempt. The §13 budget gate runs BEFORE dispatch and
// never during — deliberately, so that a refusal costs nothing — which means
// whatever max_retries says gets spent by a run the gate already approved. A
// typo of 300 for 3 was a single run able to exhaust a daily ceiling.
func TestValidateBoundsNoveltyMaxRetries(t *testing.T) {
	for _, tc := range []struct {
		name    string
		retries int
		wantBad bool
	}{
		{"default", 3, false},
		{"none", 0, false},
		{"at the cap", noveltyMaxRetriesMax, false},
		{"one past the cap", noveltyMaxRetriesMax + 1, true},
		{"a typo for 3", 300, true},
		{"negative", -1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := validGenerative()
			s.Novelty.MaxRetries = tc.retries
			if got := hasProblem(Validate(s), "novelty.max_retries", ReasonNoveltyMaxRetriesRange); got != tc.wantBad {
				t.Errorf("max_retries=%d flagged=%v, want %v", tc.retries, got, tc.wantBad)
			}
		})
	}
}

// exclude_last is paid for in input tokens on every call — it is how many
// recent titles ride along in the prompt.
func TestValidateBoundsNoveltyExcludeLast(t *testing.T) {
	for _, tc := range []struct {
		name    string
		n       int
		wantBad bool
	}{
		{"default", 50, false},
		{"none", 0, false},
		{"at the cap", noveltyExcludeLastMax, false},
		{"past the cap", noveltyExcludeLastMax + 1, true},
		{"negative", -5, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := validGenerative()
			s.Novelty.ExcludeLast = tc.n
			if got := hasProblem(Validate(s), "novelty.exclude_last", ReasonNoveltyExcludeLastRange); got != tc.wantBad {
				t.Errorf("exclude_last=%d flagged=%v, want %v", tc.n, got, tc.wantBad)
			}
		})
	}
}

// A threshold outside [0,1] makes the novelty gate degenerate. At or below
// zero every candidate reads as a repeat, so every run burns all its retries
// and skips: a feed that spends daily and publishes nothing.
//
// Zero itself stays valid, and that is not an oversight — a recipe with no
// novelty section decodes to zero, and the composition root substitutes its
// own default rather than treating everything as a repeat. Rejecting zero
// would refuse every spec that simply omits the section.
func TestValidateBoundsNoveltyThreshold(t *testing.T) {
	for _, tc := range []struct {
		name      string
		threshold float64
		wantBad   bool
	}{
		{"default", 0.92, false},
		{"unset means default, not always-duplicate", 0, false},
		{"exactly one", 1.0, false},
		{"above one, nothing is ever a repeat", 1.5, true},
		{"negative", -0.1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := validGenerative()
			s.Novelty.Threshold = tc.threshold
			if got := hasProblem(Validate(s), "novelty.threshold", ReasonNoveltyThresholdRange); got != tc.wantBad {
				t.Errorf("threshold=%v flagged=%v, want %v", tc.threshold, got, tc.wantBad)
			}
		})
	}
}
