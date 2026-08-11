package generatepage

import (
	"errors"
	"testing"
	"time"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
)

// ---------------------------------------------------------------------
// Six-state selection
// ---------------------------------------------------------------------

func TestSelectListStatePrecedence(t *testing.T) {
	cases := []struct {
		name      string
		connected bool
		reason    string
		loading   bool
		err       error
		empty     bool
		want      ListState
	}{
		{"disconnected wins over everything", false, "killed", true, errors.New("x"), true, ListDisconnected},
		{"disabled-with-reason beats error/loading", true, "kill switch off", true, errors.New("x"), true, ListDisabledWithReason},
		{"error beats loading/empty", true, "", true, errors.New("x"), true, ListError},
		{"loading beats empty", true, "", true, nil, true, ListLoading},
		{"empty", true, "", false, nil, true, ListEmpty},
		{"populated", true, "", false, nil, false, ListPopulated},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SelectListState(c.connected, c.reason, c.loading, c.err, c.empty)
			if got != c.want {
				t.Fatalf("got %s, want %s", got, c.want)
			}
		})
	}
}

// ---------------------------------------------------------------------
// Validation-error-to-field mapping
// ---------------------------------------------------------------------

func TestMapFieldErrorsStripsSpecPrefix(t *testing.T) {
	errs := []*affv1.FieldError{
		{Field: "spec.cron", Message: "invalid cron expression"},
		{Field: "slug", Message: "already in use"},
		{Field: "spec.sources[0].url", Message: "not absolute"},
	}
	got := MapFieldErrors(errs)

	if msg, ok := got.For("cron"); !ok || msg != "invalid cron expression" {
		t.Fatalf("cron: got (%q, %v)", msg, ok)
	}
	if msg, ok := got.For("slug"); !ok || msg != "already in use" {
		t.Fatalf("slug: got (%q, %v)", msg, ok)
	}
	if msg, ok := got.For("sources[0].url"); !ok || msg != "not absolute" {
		t.Fatalf("sources[0].url: got (%q, %v)", msg, ok)
	}
	if _, ok := got.For("title"); ok {
		t.Fatalf("expected no error for untouched field")
	}
	if !got.HasAny() {
		t.Fatalf("expected HasAny true")
	}
}

func TestMapFieldErrorsEmpty(t *testing.T) {
	got := MapFieldErrors(nil)
	if got.HasAny() {
		t.Fatalf("expected no errors")
	}
}

// ---------------------------------------------------------------------
// Jitter display
// ---------------------------------------------------------------------

func TestJitteredRunsOffsetsWithoutReordering(t *testing.T) {
	base := time.Date(2026, 8, 10, 7, 0, 0, 0, time.UTC)
	nominal := []time.Time{base, base.Add(24 * time.Hour), base.Add(48 * time.Hour)}

	got := JitteredRuns(nominal, 300) // +5 minutes

	for i, n := range nominal {
		want := n.Add(5 * time.Minute)
		if !got[i].Equal(want) {
			t.Fatalf("run %d: got %v, want %v", i, got[i], want)
		}
		if got[i].Equal(n) {
			t.Fatalf("run %d: jittered time must differ from nominal (D2-09: never show the nominal time)", i)
		}
	}
	for i := 1; i < len(got); i++ {
		if !got[i].After(got[i-1]) {
			t.Fatalf("jittered runs must stay strictly increasing, got %v then %v", got[i-1], got[i])
		}
	}
}

func TestJitteredRunsZeroOffsetIsIdentity(t *testing.T) {
	base := time.Date(2026, 8, 10, 7, 0, 0, 0, time.UTC)
	got := JitteredRuns([]time.Time{base}, 0)
	if !got[0].Equal(base) {
		t.Fatalf("zero offset should not move the time")
	}
}

func TestFormatNextRunsUsesFormatter(t *testing.T) {
	base := time.Date(2026, 8, 10, 7, 0, 0, 0, time.UTC)
	jittered := JitteredRuns([]time.Time{base}, 60)
	got := FormatNextRuns(fallbackFormatters{}, jittered, "UTC")
	if len(got) != 1 || got[0] == "" {
		t.Fatalf("expected one non-empty formatted run, got %v", got)
	}
}

func TestCronReadback(t *testing.T) {
	tr := fallbackTranslator{}
	cases := []struct {
		cron string
		key  string
	}{
		{"0 7 * * *", "generate.editor.cron.readback.daily"},
		{"30 9 * * 1", "generate.editor.cron.readback.weekly"},
		{"0 0 1 * *", "generate.editor.cron.readback.monthly"},
		{"* * * * *", "generate.editor.cron.readback.everyMinute"},
		{"*/5 * * * *", "generate.editor.cron.readback.raw"}, // step values: honest fallback
	}
	for _, c := range cases {
		got := CronReadback(tr, c.cron, "America/New_York")
		if len(got) < len(c.key) || got[:len(c.key)] != c.key {
			t.Fatalf("cron %q: got %q, want prefix %q", c.cron, got, c.key)
		}
	}
}

// ---------------------------------------------------------------------
// Cost estimation
// ---------------------------------------------------------------------

func TestEstimateSampleCostUSD(t *testing.T) {
	prices := []*affv1.PriceEntry{
		{Model: "gpt-4o-mini", UsdPer_1KTokensIn: 0.15, UsdPer_1KTokensOut: 0.60},
	}
	usd, ok := EstimateSampleCostUSD(prices, "gpt-4o-mini", 4000, 800, 3)
	if !ok {
		t.Fatalf("expected an estimate for a priced model")
	}
	if usd <= 0 {
		t.Fatalf("expected positive estimate, got %v", usd)
	}

	if _, ok := EstimateSampleCostUSD(prices, "unknown-model", 4000, 800, 3); ok {
		t.Fatalf("expected no estimate for an unpriced model")
	}
	if _, ok := EstimateSampleCostUSD(prices, "gpt-4o-mini", 4000, 800, 0); ok {
		t.Fatalf("expected no estimate for sampleSize 0")
	}
}

func TestSumCandidateCostUSD(t *testing.T) {
	candidates := []*affv1.SampleCandidate{
		{EstimatedCostUsd: 0.01},
		{EstimatedCostUsd: 0.02},
		nil,
	}
	got := SumCandidateCostUSD(candidates)
	if got != 0.03 {
		t.Fatalf("got %v, want 0.03", got)
	}
}

func TestSampleDisabledReasonPrecedence(t *testing.T) {
	tr := fallbackTranslator{}
	if r := SampleDisabledReason(tr, true, true, 0, 0); r == "" {
		t.Fatalf("expected global kill switch reason")
	}
	if r := SampleDisabledReason(tr, false, true, 0, 0); r == "" {
		t.Fatalf("expected feed-disabled reason")
	}
	if r := SampleDisabledReason(tr, false, false, 5, 3); r == "" {
		t.Fatalf("expected auto-disabled reason once threshold reached")
	}
	if r := SampleDisabledReason(tr, false, false, 2, 3); r != "" {
		t.Fatalf("expected no reason below threshold, got %q", r)
	}
}

func TestValidSampleSize(t *testing.T) {
	for n := int32(-1); n <= 6; n++ {
		want := n >= 1 && n <= 5
		if ValidSampleSize(n) != want {
			t.Fatalf("n=%d: got %v, want %v", n, ValidSampleSize(n), want)
		}
	}
}

// ---------------------------------------------------------------------
// Candidate view switching
// ---------------------------------------------------------------------

func TestCandidateViewContentSwitchesFields(t *testing.T) {
	c := &affv1.SampleCandidate{
		Title:            "Question of the day",
		SummaryText:      "A plain-text summary",
		BodyHtml:         "<p>body</p>",
		Link:             "https://example.com/a",
		RenderedXml:      "<item><title>Question of the day</title></item>",
		SlackPreviewText: "Question of the day — A plain-text summary",
	}

	rendered := CandidateViewContent(ViewRendered, c)
	if !contains(rendered, c.Title) || !contains(rendered, c.SummaryText) {
		t.Fatalf("rendered view missing title/summary: %q", rendered)
	}

	raw := CandidateViewContent(ViewRawFields, c)
	if !contains(raw, "link: https://example.com/a") {
		t.Fatalf("raw view missing link field: %q", raw)
	}

	xml := CandidateViewContent(ViewFeedXML, c)
	if xml != c.RenderedXml {
		t.Fatalf("feed XML view got %q, want exact %q", xml, c.RenderedXml)
	}

	slack := CandidateViewContent(ViewSlackCard, c)
	if slack != c.SlackPreviewText {
		t.Fatalf("slack view got %q, want exact %q", slack, c.SlackPreviewText)
	}

	if CandidateViewContent(ViewRendered, nil) != "" {
		t.Fatalf("nil candidate should render empty, not panic")
	}
}

func TestCandidateViewsOrderEndsOnSlack(t *testing.T) {
	if len(CandidateViews) != 4 {
		t.Fatalf("expected 4 views")
	}
	if CandidateViews[len(CandidateViews)-1] != ViewSlackCard {
		t.Fatalf("PLAN.md §12.3 calls the Slack preview 'the one that matters' — it must be last, not buried first")
	}
}

func TestNoveltySummaryDistinguishesNovelNearAndRejected(t *testing.T) {
	tr := fallbackTranslator{}
	novel := NoveltySummary(tr, DefaultFormatters, &affv1.NoveltyVerdict{IsNovel: true})
	novelNear := NoveltySummary(tr, DefaultFormatters, &affv1.NoveltyVerdict{IsNovel: true, Similarity: 0.7, NearestItemId: 5, NearestItemTitle: "close one"})
	rejected := NoveltySummary(tr, DefaultFormatters, &affv1.NoveltyVerdict{IsNovel: false, Similarity: 0.97, NearestItemId: 5, NearestItemTitle: "dupe"})
	if novel == novelNear || novel == rejected || novelNear == rejected {
		t.Fatalf("expected three distinct summaries, got %q / %q / %q", novel, novelNear, rejected)
	}
}

func TestFailedLinksFiltersAndKeepsURL(t *testing.T) {
	verdicts := []*affv1.LinkVerdict{
		{Url: "https://ok.example/1", Ok: true},
		{Url: "https://bad.example/2", Ok: false},
	}
	failed := FailedLinks(verdicts)
	if len(failed) != 1 || failed[0].GetUrl() != "https://bad.example/2" {
		t.Fatalf("got %+v", failed)
	}
}

// ---------------------------------------------------------------------
// Conflict resolution
// ---------------------------------------------------------------------

func TestDiffFeedFieldsOnlyDiffering(t *testing.T) {
	mine := &affv1.Feed{Title: "Mine title", Spec: &affv1.FeedSpec{Cron: "0 7 * * *", ItemsPerRun: 1}}
	theirs := &affv1.Feed{Title: "Their title", Spec: &affv1.FeedSpec{Cron: "0 7 * * *", ItemsPerRun: 2}}

	diffs := DiffFeedFields(mine, theirs)
	got := map[string]FieldDiff{}
	for _, d := range diffs {
		got[d.FieldKey] = d
	}
	if _, ok := got["cron"]; ok {
		t.Fatalf("cron did not differ, should not appear in diff")
	}
	if d, ok := got["title"]; !ok || d.Mine != "Mine title" || d.Theirs != "Their title" {
		t.Fatalf("title diff wrong: %+v", got["title"])
	}
	if d, ok := got["items_per_run"]; !ok || d.Mine != "1" || d.Theirs != "2" {
		t.Fatalf("items_per_run diff wrong: %+v", got["items_per_run"])
	}
}

func TestApplyResolutionTakeTheirsDiscardsMine(t *testing.T) {
	mine := &affv1.Feed{Title: "Mine", Version: 3, Spec: &affv1.FeedSpec{}}
	theirs := &affv1.Feed{Title: "Theirs", Version: 4, Spec: &affv1.FeedSpec{}}
	got := ApplyResolution(mine, theirs, ResolveTakeTheirs, nil)
	if got.GetTitle() != "Theirs" || got.GetVersion() != 4 {
		t.Fatalf("got %+v", got)
	}
}

func TestApplyResolutionKeepMineRetargetsVersion(t *testing.T) {
	mine := &affv1.Feed{Title: "Mine", Version: 3, Spec: &affv1.FeedSpec{}}
	theirs := &affv1.Feed{Title: "Theirs", Version: 4, Spec: &affv1.FeedSpec{}}
	got := ApplyResolution(mine, theirs, ResolveKeepMine, nil)
	if got.GetTitle() != "Mine" {
		t.Fatalf("expected mine's title, got %q", got.GetTitle())
	}
	if got.GetVersion() != 4 {
		t.Fatalf("a resubmit must target theirs' version, got %d", got.GetVersion())
	}
}

func TestApplyResolutionPerFieldNeverSilentlyClobbers(t *testing.T) {
	mine := &affv1.Feed{Title: "Mine title", Description: "Mine desc", Version: 3, Spec: &affv1.FeedSpec{}}
	theirs := &affv1.Feed{Title: "Their title", Description: "Their desc", Version: 4, Spec: &affv1.FeedSpec{}}

	// Operator explicitly keeps mine's title only; everything else defaults
	// to theirs, per doc comment ("absent key defaults to keeping theirs").
	got := ApplyResolution(mine, theirs, ResolvePerField, map[string]bool{"title": true})

	if got.GetTitle() != "Mine title" {
		t.Fatalf("expected chosen field to keep mine, got %q", got.GetTitle())
	}
	if got.GetDescription() != "Their desc" {
		t.Fatalf("expected untouched field to default to theirs (never a silent clobber the other way), got %q", got.GetDescription())
	}
	if got.GetVersion() != 4 {
		t.Fatalf("merged result must retarget theirs' version for the retry, got %d", got.GetVersion())
	}
}

// ---------------------------------------------------------------------
// Slug immutability, staleness, dirty-check
// ---------------------------------------------------------------------

func TestSlugEditable(t *testing.T) {
	if !SlugEditable(nil) {
		t.Fatalf("nil (new) feed must be editable")
	}
	newFeed := &affv1.Feed{Id: 0}
	if !SlugEditable(newFeed) {
		t.Fatalf("feed with no id must be editable")
	}
	neverBuilt := &affv1.Feed{Id: 1}
	if !SlugEditable(neverBuilt) {
		t.Fatalf("a saved-but-never-built feed must still be editable")
	}
	built := &affv1.Feed{Id: 1, LastBuiltAt: timestamppb.Now()}
	if SlugEditable(built) {
		t.Fatalf("a feed that has built must be slug-locked (PLAN.md §14.1)")
	}
}

func TestIsStale(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	old := now.Add(-2 * time.Hour)
	if IsStale(false, &old, now, 60) {
		t.Fatalf("a disabled feed is never flagged stale")
	}
	if IsStale(true, nil, now, 60) {
		t.Fatalf("a feed that never built is 'new', not stale")
	}
	if !IsStale(true, &old, now, 60) {
		t.Fatalf("expected stale: 2h old build against a 60m threshold")
	}
	recent := now.Add(-5 * time.Minute)
	if IsStale(true, &recent, now, 60) {
		t.Fatalf("a recent build under threshold must not be flagged stale")
	}
}

func TestDraftDirty(t *testing.T) {
	loaded := &affv1.Feed{Title: "A", Spec: &affv1.FeedSpec{}}
	same := &affv1.Feed{Title: "A", Spec: &affv1.FeedSpec{}}
	changed := &affv1.Feed{Title: "B", Spec: &affv1.FeedSpec{}}

	if DraftDirty(loaded, same) {
		t.Fatalf("identical draft must not be dirty")
	}
	if !DraftDirty(loaded, changed) {
		t.Fatalf("changed draft must be dirty")
	}
}

func TestPersistedSampleUsable(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	fresh := PersistedSampleState{
		SavedAtUnix: now.Add(-1 * time.Hour).Unix(),
		FeedSlug:    "one-piece",
		SampleID:    "s1",
		Candidates:  []*affv1.SampleCandidate{{CandidateId: "c1"}},
	}
	if !PersistedSampleUsable(fresh, "one-piece", now) {
		t.Fatalf("a 1h-old snapshot for the matching slug must be usable")
	}
	if PersistedSampleUsable(fresh, "naruto", now) {
		t.Fatalf("a snapshot for a different feed slug must not be usable")
	}
	stale := fresh
	stale.SavedAtUnix = now.Add(-25 * time.Hour).Unix()
	if PersistedSampleUsable(stale, "one-piece", now) {
		t.Fatalf("a snapshot older than the 24h retention window must not be usable")
	}
	empty := PersistedSampleState{FeedSlug: "one-piece", SampleID: "s1"}
	if PersistedSampleUsable(empty, "one-piece", now) {
		t.Fatalf("a snapshot with no candidates must not be usable")
	}
	noID := PersistedSampleState{FeedSlug: "one-piece", Candidates: []*affv1.SampleCandidate{{CandidateId: "c1"}}}
	if PersistedSampleUsable(noID, "one-piece", now) {
		t.Fatalf("a snapshot with no sample ID must not be usable")
	}
}

func TestParseFloatOrIntOrFallBackOnBadInput(t *testing.T) {
	if got := parseFloatOr("not a number", 1.5); got != 1.5 {
		t.Fatalf("got %v, want fallback 1.5", got)
	}
	if got := parseFloatOr("2.5", 1.5); got != 2.5 {
		t.Fatalf("got %v, want 2.5", got)
	}
	if got := parseIntOr("", 7); got != 7 {
		t.Fatalf("got %v, want fallback 7", got)
	}
	if got := parseIntOr("42", 7); got != 42 {
		t.Fatalf("got %v, want 42", got)
	}
}

func TestIsVersionConflict(t *testing.T) {
	if IsVersionConflict(nil) {
		t.Fatalf("nil error is not a conflict")
	}
	if IsVersionConflict(errors.New("boom")) {
		t.Fatalf("a plain error is not a version conflict")
	}
	if !IsVersionConflict(status.Error(codes.Aborted, "version mismatch")) {
		t.Fatalf("Aborted must be treated as a conflict")
	}
	if !IsVersionConflict(status.Error(codes.FailedPrecondition, "version mismatch")) {
		t.Fatalf("FailedPrecondition must be treated as a conflict")
	}
	if IsVersionConflict(status.Error(codes.Internal, "boom")) {
		t.Fatalf("an unrelated code is not a conflict")
	}
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
