package generatepage

import (
	"strings"
	"testing"
	"time"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// applyFeedField is the editor's entire write path: every keystroke in the
// recipe form arrives here as a (key, value) pair. Two properties matter and
// neither was tested — a key must reach the field it names, and a value that
// does not parse must leave the previous one alone rather than zeroing it.

func TestApplyFeedFieldWritesTheNamedField(t *testing.T) {
	cases := []struct {
		key   string
		value string
		check func(*affv1.Feed) bool
	}{
		{"title", "Daily Trivia", func(f *affv1.Feed) bool { return f.GetTitle() == "Daily Trivia" }},
		{"description", "about anime", func(f *affv1.Feed) bool { return f.GetDescription() == "about anime" }},
		{"language", "en-US", func(f *affv1.Feed) bool { return f.GetLanguage() == "en-US" }},
		{"author", "Cam", func(f *affv1.Feed) bool { return f.GetAuthor() == "Cam" }},
		{"copyright", "(c) 2026", func(f *affv1.Feed) bool { return f.GetCopyright() == "(c) 2026" }},
		{"og_image", "https://example.com/x.png", func(f *affv1.Feed) bool { return f.GetOgImage() == "https://example.com/x.png" }},
		{"ttl_minutes", "45", func(f *affv1.Feed) bool { return f.GetTtlMinutes() == 45 }},
		{"enabled", "true", func(f *affv1.Feed) bool { return f.GetEnabled() }},
		{"cron", "0 9 * * *", func(f *affv1.Feed) bool { return f.GetSpec().GetCron() == "0 9 * * *" }},
		{"timezone", "America/New_York", func(f *affv1.Feed) bool { return f.GetSpec().GetTimezone() == "America/New_York" }},
		{"items_per_run", "5", func(f *affv1.Feed) bool { return f.GetSpec().GetItemsPerRun() == 5 }},
		{"feed_window", "30", func(f *affv1.Feed) bool { return f.GetSpec().GetFeedWindow() == 30 }},
		{"model", "gpt-4o", func(f *affv1.Feed) bool { return f.GetSpec().GetModel() == "gpt-4o" }},
		{"temperature", "0.7", func(f *affv1.Feed) bool { return f.GetSpec().GetTemperature() == 0.7 }},
		{"system_prompt_template", "be terse", func(f *affv1.Feed) bool { return f.GetSpec().GetSystemPromptTemplate() == "be terse" }},
		{"user_prompt_template", "{{.Today}}", func(f *affv1.Feed) bool { return f.GetSpec().GetUserPromptTemplate() == "{{.Today}}" }},
		{"daily_token_budget", "120000", func(f *affv1.Feed) bool { return f.GetSpec().GetDailyTokenBudget() == 120000 }},
		{"daily_run_budget", "3", func(f *affv1.Feed) bool { return f.GetSpec().GetDailyRunBudget() == 3 }},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			f := &affv1.Feed{}
			applyFeedField(f, tc.key, tc.value)
			if !tc.check(f) {
				t.Errorf("applyFeedField(%q, %q) did not land: %+v", tc.key, tc.value, f)
			}
		})
	}
}

func TestApplyFeedFieldLeavesTheOldValueWhenTheNewOneDoesNotParse(t *testing.T) {
	// Half-typed numbers are the normal state of a number input: "1" on the
	// way to "12", "0." on the way to "0.5", "" while the operator clears it.
	// Zeroing the field on each of those would silently rewrite a budget as
	// the operator typed, and a zero budget stops generation.
	numeric := []struct {
		key   string
		seed  func(*affv1.Feed)
		check func(*affv1.Feed) bool
	}{
		{"ttl_minutes", func(f *affv1.Feed) { f.TtlMinutes = 60 }, func(f *affv1.Feed) bool { return f.GetTtlMinutes() == 60 }},
		{"items_per_run", func(f *affv1.Feed) { f.Spec.ItemsPerRun = 5 }, func(f *affv1.Feed) bool { return f.GetSpec().GetItemsPerRun() == 5 }},
		{"feed_window", func(f *affv1.Feed) { f.Spec.FeedWindow = 30 }, func(f *affv1.Feed) bool { return f.GetSpec().GetFeedWindow() == 30 }},
		{"temperature", func(f *affv1.Feed) { f.Spec.Temperature = 0.8 }, func(f *affv1.Feed) bool { return f.GetSpec().GetTemperature() == 0.8 }},
		{"daily_token_budget", func(f *affv1.Feed) { f.Spec.DailyTokenBudget = 100 }, func(f *affv1.Feed) bool { return f.GetSpec().GetDailyTokenBudget() == 100 }},
		{"daily_run_budget", func(f *affv1.Feed) { f.Spec.DailyRunBudget = 2 }, func(f *affv1.Feed) bool { return f.GetSpec().GetDailyRunBudget() == 2 }},
	}
	for _, tc := range numeric {
		for _, bad := range []string{"", "abc", "-", "1.2.3"} {
			f := &affv1.Feed{Spec: &affv1.FeedSpec{}}
			tc.seed(f)
			applyFeedField(f, tc.key, bad)
			if !tc.check(f) {
				t.Errorf("applyFeedField(%q, %q) overwrote the previous value: %+v", tc.key, bad, f)
			}
		}
	}
}

func TestApplyFeedFieldToleratesAMissingSpecAndAnUnknownKey(t *testing.T) {
	// A feed arriving with no Spec is the create case, and an unknown key is
	// what a renamed field looks like mid-refactor — neither may panic.
	f := &affv1.Feed{}
	applyFeedField(f, "cron", "0 9 * * *")
	if f.GetSpec().GetCron() != "0 9 * * *" {
		t.Errorf("a nil Spec was not created: %+v", f)
	}

	before := f.GetTitle()
	applyFeedField(f, "no_such_field", "value")
	if f.GetTitle() != before {
		t.Error("an unknown key wrote something")
	}
}

func TestEnabledIsOnlyTrueForTheExactString(t *testing.T) {
	// Anything other than "true" is false, including "TRUE" and "1" — the
	// only producer is a checkbox that sends "true"/"false", and being
	// liberal here would make the kill switch's state depend on the caller's
	// spelling.
	for value, want := range map[string]bool{
		"true": true, "false": false, "TRUE": false, "1": false, "": false,
	} {
		f := &affv1.Feed{Enabled: !want}
		applyFeedField(f, "enabled", value)
		if f.GetEnabled() != want {
			t.Errorf("enabled=%q gave %v, want %v", value, f.GetEnabled(), want)
		}
	}
}

// --- fallback formatters ----------------------------------------------------

func TestFallbackFormatters(t *testing.T) {
	// These are what render before the real catalogue is wired. They are
	// deliberately locale-naive, but they must never render something an
	// operator would misread as a different figure.
	var f Formatters = fallbackFormatters{}

	if got := f.Currency(0.0125); got != "$0.0125" {
		t.Errorf("Currency(0.0125) = %q, want $0.0125 — four decimals, because a run can cost less than a cent", got)
	}
	if got := f.Currency(0); got != "$0.0000" {
		t.Errorf("Currency(0) = %q, want $0.0000", got)
	}
	if got := f.Percent(0.8734); got != "87%" {
		t.Errorf("Percent(0.8734) = %q, want 87%%", got)
	}
	if got := f.Percent(1); got != "100%" {
		t.Errorf("Percent(1) = %q, want 100%%", got)
	}

	when := time.Date(2026, 8, 11, 14, 30, 0, 0, time.UTC)
	if got := f.DateTime(when, "UTC"); !strings.Contains(got, "2026-08-11") {
		t.Errorf("DateTime = %q, want the date in it", got)
	}
	// An unknown zone falls back to UTC rather than failing: a bad timezone
	// in a recipe must not blank the rail.
	if got := f.DateTime(when, "Mars/Olympus"); !strings.Contains(got, "2026-08-11") {
		t.Errorf("DateTime with an unknown zone = %q, want a UTC rendering", got)
	}
	if got := f.DateTime(when, ""); !strings.Contains(got, "UTC") {
		t.Errorf("DateTime with no zone = %q, want UTC", got)
	}

	now := when.Add(3 * time.Hour)
	cases := map[time.Duration]string{
		-time.Hour:       "just now", // a clock-skewed future timestamp
		30 * time.Second: "moments ago",
		5 * time.Minute:  "5m ago",
		2 * time.Hour:    "2h ago",
		50 * time.Hour:   "2d ago",
	}
	for ago, want := range cases {
		if got := f.RelativeTime(now.Add(-ago), now); got != want {
			t.Errorf("RelativeTime(%s ago) = %q, want %q", ago, got, want)
		}
	}
}

func TestNumberFormattersRoundTripThroughTheirParsers(t *testing.T) {
	// floatStr/intStr/int64Str put a stored value back into an input, and
	// applyFeedField parses whatever comes back out. If the two disagree, a
	// field the operator never touched changes on the next save.
	if got := parseFloatOr(floatStr(0.7), -1); got != 0.7 {
		t.Errorf("0.7 round-tripped to %v", got)
	}
	if got := parseIntOr(intStr(45), -1); got != 45 {
		t.Errorf("45 round-tripped to %d", got)
	}
	if got := int64Str(1 << 40); got != "1099511627776" {
		t.Errorf("int64Str(1<<40) = %q", got)
	}
	// floatStr's two call sites are temperature and the novelty similarity
	// threshold, both 0..2. Across that range 'g' formatting must stay
	// plain-decimal — an input box showing "8e-01" for 0.8 is legible to
	// nobody, even though it would parse back correctly.
	for _, v := range []float64{0, 0.1, 0.35, 0.8, 1, 1.25, 2} {
		s := floatStr(v)
		if strings.ContainsAny(s, "eE") {
			t.Errorf("floatStr(%v) = %q, which is scientific notation in an input box", v, s)
		}
		if got := parseFloatOr(s, -1); got != v {
			t.Errorf("%v round-tripped to %v via %q", v, got, s)
		}
	}
}

// --- display enums ----------------------------------------------------------

func TestListStateAndCandidateViewNamesAreDistinct(t *testing.T) {
	states := []ListState{ListLoading, ListEmpty, ListPopulated, ListError, ListDisabledWithReason, ListDisconnected}
	seen := map[string]bool{}
	for _, s := range states {
		name := s.String()
		if name == "" || name == "unknown" {
			t.Errorf("state %d has no name", s)
		}
		if seen[name] {
			t.Errorf("two states share the name %q", name)
		}
		seen[name] = true
	}
	if got := ListState(99).String(); got != "unknown" {
		t.Errorf("an out-of-range state stringified as %q", got)
	}

	// Every view's tab label goes through i18n (D6-11), and the keys must
	// be distinct or two tabs render with the same label.
	keys := map[string]bool{}
	for _, v := range CandidateViews {
		k := v.TranslationKey()
		if !strings.HasPrefix(k, "generate.sampler.view.") {
			t.Errorf("view %d key %q is outside the generate.* namespace", v, k)
		}
		if keys[k] {
			t.Errorf("two views share the key %q", k)
		}
		keys[k] = true
	}
	if len(CandidateViews) != 5 {
		t.Errorf("CandidateViews has %d entries, want the plan's four plus the embed (§6.1)", len(CandidateViews))
	}
	if got := CandidateView(99).TranslationKey(); got != "generate.sampler.view.unknown" {
		t.Errorf("an out-of-range view keyed to %q", got)
	}
}

func TestSlugImmutableReasonAlwaysExplains(t *testing.T) {
	// §14.1: the UI must say WHY the field is locked, never just grey it out.
	if got := SlugImmutableReason(nil); got == "" {
		t.Error("a nil translator produced no reason at all")
	}
	if got := SlugImmutableReason(fallbackTranslator{}); got != "generate.editor.slug.immutableReason" {
		t.Errorf("reason key = %q", got)
	}
}
