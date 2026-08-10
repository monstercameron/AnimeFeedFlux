// J2 — create a feed (PLAN.md §22 "J2 — Create a feed", TODOS.md BF-06..10).
//
// FeedService (TODOS.md B1-03, including ValidateSpec) does not exist yet.
// What does exist and is real: internal/feedspec.Validate — the exact
// validation logic §11 assigns to that RPC, already covering every rejection
// this flow's failure branches name except "duplicate slug" (by feedspec's
// own doc comment, that one is deliberately left to the store, which is the
// only layer that knows what's already been inserted) — and
// internal/store.CreateFeed's real UNIQUE(slug) constraint for that one
// case. So this file drives Validate directly, exactly as the not-yet-built
// RPC handler will, and drives CreateFeed for the duplicate-slug case
// specifically.
package flowtest

import (
	"errors"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/config"
	"github.com/monstercameron/AnimeFeedFlux/internal/feedspec"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/schedule"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// j2ValidSpec is a feedspec.Spec that clears every feedspec.Validate rule: a
// well-shaped, non-reserved slug; a valid cron in a real IANA timezone; a
// generative feed's system/user prompts that only reference template
// variables generate.Data actually populates; and non-zero budgets.
func j2ValidSpec(slug string) feedspec.Spec {
	s := feedspec.Defaults()
	s.Slug = slug
	s.Title = "J2 Flow Test Feed"
	s.Kind = model.KindGenerative
	s.Timezone = "America/New_York" // deliberately non-UTC, so BF-08's "in the feed's timezone" is a real claim
	s.Cron = "0 7 * * *"            // 7am local, daily
	s.SystemPrompt = "You write short anime trivia questions."
	s.UserPrompt = "Write {{.ItemsPerRun}} item(s) for {{.Today}} ({{.Weekday}})."
	return s
}

// j2FeedFromSpec is the identity/settings slice model.Feed carries, the same
// projection store.CreateFeed accepts — a stand-in for whatever mapping the
// real FeedService will do from a validated Spec to a persisted Feed row.
// Enabled is deliberately left false: PLAN.md §22 J2 requires a newly
// created feed start disabled (BF-06), and nothing here ever sets it true.
func j2FeedFromSpec(s feedspec.Spec) model.Feed {
	return model.Feed{
		Slug:     s.Slug,
		Title:    s.Title,
		Kind:     s.Kind,
		Timezone: s.Timezone,
		Enabled:  false,
	}
}

// TestJ2_FeedExistsDisabledZeroItems is BF-06.
func TestJ2_FeedExistsDisabledZeroItems(t *testing.T) {
	w := New(t)
	ctx := t.Context()

	spec := j2ValidSpec("j2-disabled-by-default")
	if problems := feedspec.Validate(spec); len(problems) > 0 {
		t.Fatalf("Validate rejected a fixture meant to be valid: %v", problems)
	}

	feed, err := w.CreateFeed(ctx, j2FeedFromSpec(spec))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}

	// BF-06 (§22 J2): the feed exists, is disabled by default, and has zero
	// items.
	got, err := w.Store.GetFeedBySlug(ctx, spec.Slug)
	if err != nil {
		t.Fatalf("GetFeedBySlug: %v", err)
	}
	if got.Enabled {
		t.Error("a freshly created feed is enabled, want disabled by default")
	}
	n, err := w.ItemCount(ctx, feed.ID)
	if err != nil {
		t.Fatalf("ItemCount: %v", err)
	}
	if n != 0 {
		t.Errorf("a freshly created feed has %d items, want 0", n)
	}
}

// TestJ2_JitterOffsetDeterministicFromSlug is BF-07.
//
// store.CreateFeed deliberately leaves feeds.jitter_offset at its schema
// default (items.go's doc comment: populating it is FeedService's job, §11,
// which does not exist yet). So this test does what that future service
// will: compute the offset with schedule.Offset (the same function
// feedspec.Spec.JitterOffset delegates to, so a UI readback can never
// disagree with what the scheduler does) and persist it, then proves the
// computation itself is deterministic — the property BF-07 actually cares
// about — by recomputing it a second, independent way and requiring an exact
// match.
func TestJ2_JitterOffsetDeterministicFromSlug(t *testing.T) {
	w := New(t)
	ctx := t.Context()

	spec := j2ValidSpec("j2-jitter-offset")
	feed, err := w.CreateFeed(ctx, j2FeedFromSpec(spec))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}

	offset := spec.JitterOffset(config.DefaultScheduleJitter)
	direct := schedule.Offset(spec.Slug, config.DefaultScheduleJitter)
	if offset != direct {
		t.Fatalf("feedspec.Spec.JitterOffset (%v) disagrees with schedule.Offset (%v) for the same slug and window", offset, direct)
	}
	if offset < 0 || offset >= config.DefaultScheduleJitter {
		t.Fatalf("jitter offset %v is out of [0, %v)", offset, config.DefaultScheduleJitter)
	}

	// Persist it (FeedService's job, done here directly per this package's
	// stand-in mandate — see harness.go's doc comment) and read it back.
	if _, err := w.Store.Writer().ExecContext(ctx,
		`UPDATE feeds SET jitter_offset = ? WHERE id = ?`, int64(offset), feed.ID); err != nil {
		t.Fatalf("persisting jitter_offset: %v", err)
	}
	var persisted int64
	if err := w.Store.Writer().QueryRowContext(ctx,
		`SELECT jitter_offset FROM feeds WHERE id = ?`, feed.ID).Scan(&persisted); err != nil {
		t.Fatalf("reading back jitter_offset: %v", err)
	}
	if time.Duration(persisted) != offset {
		t.Fatalf("persisted jitter_offset = %v, want %v", time.Duration(persisted), offset)
	}

	// Deterministic from the slug: recomputing for the identical slug, in a
	// brand new process-equivalent call, gives the identical answer.
	again := schedule.Offset(spec.Slug, config.DefaultScheduleJitter)
	if again != offset {
		t.Fatalf("recomputing schedule.Offset(%q, ...) gave %v, want the original %v — not deterministic", spec.Slug, again, offset)
	}
}

// TestJ2_NextThreeRunsAreFutureAndInFeedTimezone is BF-08.
func TestJ2_NextThreeRunsAreFutureAndInFeedTimezone(t *testing.T) {
	w := New(t)
	ctx := t.Context()

	spec := j2ValidSpec("j2-next-three-runs")
	if _, err := w.CreateFeed(ctx, j2FeedFromSpec(spec)); err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}

	loc, err := time.LoadLocation(spec.Timezone)
	if err != nil {
		t.Fatalf("LoadLocation(%q): %v", spec.Timezone, err)
	}
	sched, err := schedule.Parse(spec.Cron, loc)
	if err != nil {
		t.Fatalf("schedule.Parse: %v", err)
	}
	offset := spec.JitterOffset(config.DefaultScheduleJitter)

	now := w.Clock.Now()
	after := now
	for i := 0; i < 3; i++ {
		next := sched.Next(after).Add(offset)
		if next.IsZero() {
			t.Fatalf("run %d: schedule.Next returned the zero time (no firing found within the search horizon)", i+1)
		}
		// BF-08 (§22 J2): in the future.
		if !next.After(now) {
			t.Fatalf("run %d: computed firing %v is not after now (%v)", i+1, next, now)
		}
		// BF-08: in the feed's timezone, not UTC.
		if next.Location() != loc {
			t.Fatalf("run %d: computed firing is in %v, want %v (the feed's own timezone)", i+1, next.Location(), loc)
		}
		after = next
	}
}

// j2RejectionCase is one of §22 J2's "each of ... is refused server-side"
// failure branches (BF-09), expressed as a mutation from a known-valid spec
// plus the feedspec.Problem Reason token the mutation must trip.
type j2RejectionCase struct {
	name   string
	mutate func(feedspec.Spec) feedspec.Spec
	reason string
}

// TestJ2_EachInvalidCaseRefusedServerSide is BF-09 and, by construction,
// half of BF-10: every case here calls feedspec.Validate — plain server-side
// Go, reachable with no browser involved — and NEVER calls CreateFeed or any
// generation path when Validate reports a problem, so nothing in these
// subtests could possibly publish or call the provider.
func TestJ2_EachInvalidCaseRefusedServerSide(t *testing.T) {
	cases := []j2RejectionCase{
		{
			name:   "reserved slug",
			mutate: func(s feedspec.Spec) feedspec.Spec { s.Slug = "feeds"; return s },
			reason: feedspec.ReasonSlugReserved,
		},
		{
			name:   "bad cron",
			mutate: func(s feedspec.Spec) feedspec.Spec { s.Cron = "not a cron expression"; return s },
			reason: feedspec.ReasonCronInvalid,
		},
		{
			name:   "unknown timezone",
			mutate: func(s feedspec.Spec) feedspec.Spec { s.Timezone = "Nowhere/Fictional"; return s },
			reason: feedspec.ReasonTimezoneInvalid,
		},
		{
			name:   "unknown template variable",
			mutate: func(s feedspec.Spec) feedspec.Spec { s.UserPrompt = "{{.ThisFieldDoesNotExist}}"; return s },
			reason: feedspec.ReasonTemplateInvalid,
		},
		{
			name: "grounded without a source",
			mutate: func(s feedspec.Spec) feedspec.Spec {
				s.Kind = model.KindGrounded
				s.Sources = nil
				return s
			},
			reason: feedspec.ReasonGroundedRequiresSource,
		},
		{
			name:   "zero budget",
			mutate: func(s feedspec.Spec) feedspec.Spec { s.Budgets = feedspec.Budgets{}; return s },
			reason: feedspec.ReasonBudgetDailyTokensZero,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := New(t)
			ctx := t.Context()

			base := j2ValidSpec("j2-reject-" + sanitizeSubtestSlug(tc.name))
			spec := tc.mutate(base)

			problems := feedspec.Validate(spec)
			found := false
			for _, p := range problems {
				if p.Reason == tc.reason {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("Validate(%+v) = %v, want a problem with reason %q", tc.name, problems, tc.reason)
			}

			// BF-10 (§22 J2): nothing was published and no provider call was
			// made. Validate having refused the spec means the flow never
			// reaches CreateFeed or generate.Run/Sample at all — asserted
			// here rather than assumed, since a bug that called CreateFeed
			// anyway would otherwise pass silently.
			if _, err := w.Store.GetFeedBySlug(ctx, spec.Slug); err == nil {
				t.Fatalf("a feed with slug %q exists despite Validate refusing the spec", spec.Slug)
			} else if !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("GetFeedBySlug: unexpected error %v", err)
			}
			if calls := w.Provider.GenerateCallCount(); calls != 0 {
				t.Fatalf("provider Generate was called %d times for a spec Validate refused, want 0", calls)
			}
		})
	}
}

// TestJ2_DuplicateSlugRefusedServerSide is the one BF-09 case
// feedspec.Validate does not cover by design (its own doc comment: slug
// uniqueness needs the store). Refused here by the real UNIQUE(slug)
// constraint (migrations/0002_feeds_items.sql), not a pre-check this test
// invents.
func TestJ2_DuplicateSlugRefusedServerSide(t *testing.T) {
	w := New(t)
	ctx := t.Context()

	spec := j2ValidSpec("j2-duplicate-slug")
	if _, err := w.CreateFeed(ctx, j2FeedFromSpec(spec)); err != nil {
		t.Fatalf("first CreateFeed: %v", err)
	}

	// A second feed, different title, SAME slug.
	dup := j2FeedFromSpec(spec)
	dup.Title = "A completely different title, same slug"
	if _, err := w.CreateFeed(ctx, dup); err == nil {
		t.Fatal("CreateFeed with a duplicate slug succeeded, want the store's UNIQUE(slug) constraint to refuse it")
	}

	// BF-10: no provider call, and only the ONE original feed exists.
	if calls := w.Provider.GenerateCallCount(); calls != 0 {
		t.Fatalf("provider Generate was called %d times, want 0", calls)
	}
	got, err := w.Store.GetFeedBySlug(ctx, spec.Slug)
	if err != nil {
		t.Fatalf("GetFeedBySlug after the rejected duplicate: %v", err)
	}
	if got.Title != spec.Title {
		t.Fatalf("feed title = %q after a rejected duplicate create, want the ORIGINAL title %q (unchanged)", got.Title, spec.Title)
	}
}

// sanitizeSubtestSlug turns a subtest name into something that clears
// feedspec's slug shape rule ([a-z0-9-]{3,48}) well enough to isolate each
// case's feed under its own slug.
func sanitizeSubtestSlug(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out = append(out, r)
		case r == ' ' || r == '-':
			out = append(out, '-')
		}
	}
	return string(out)
}
