// logic.go holds every pure computation this page needs, with no GWC and
// no network dependency, so it is exercised by plain `go test` on the host
// (TODOS.md "Tests: host-testable pure logic... validation-error-to-field
// mapping, jitter display, cost estimation, the six-state selection,
// conflict-resolution choices, candidate view switching").
package generatepage

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// IsVersionConflict reports whether err is the gRPC error shape an
// expected_version mismatch (PLAN.md §11) is expected to arrive as, so
// the editor's Save handler can distinguish "someone else changed this
// feed, offer a merge" from any other save failure. Aborted is gRPC's own
// recommended code for a lost optimistic-concurrency race;
// FailedPrecondition is accepted too since it is the code some
// implementations reach for instead — this checks both rather than
// assuming server-side convention exactly matches, so a genuine conflict
// is never silently rendered as a generic error banner.
func IsVersionConflict(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	switch st.Code() {
	case codes.Aborted, codes.FailedPrecondition:
		return true
	default:
		return false
	}
}

// ---------------------------------------------------------------------
// Six-state selection (D2-05, D-FLOW's per-screen state matrix)
// ---------------------------------------------------------------------

// SelectListState picks one of the six states for a list/panel, in a
// fixed precedence order documented inline: each earlier case is strictly
// more urgent to tell the operator about than the ones after it, because
// the truth of a later case cannot even be trusted once an earlier one
// holds (e.g. "empty" is meaningless to report while disconnected — the
// list might not be empty at all, we simply cannot ask).
func SelectListState(connected bool, disabledReason string, loading bool, err error, empty bool) ListState {
	switch {
	case !connected:
		// Nothing past this point can be trusted: nothing was fetched, an
		// error might just be "no socket", and "empty" cannot be
		// distinguished from "unknown".
		return ListDisconnected
	case disabledReason != "":
		// The kill switch (or an equivalent server-declared reason) means
		// this list's actions are inert even if its data is fine — this
		// must outrank "loading"/"error" from a stale in-flight request,
		// or the operator sees a spinner for a control that will never do
		// anything (PLAN.md §12.3: "a visible reason, not a dead
		// control").
		return ListDisabledWithReason
	case err != nil:
		return ListError
	case loading:
		return ListLoading
	case empty:
		return ListEmpty
	default:
		return ListPopulated
	}
}

// ---------------------------------------------------------------------
// Validation-error-to-field mapping (D2-14)
// ---------------------------------------------------------------------

// MapFieldErrors converts FeedServiceValidateSpecResponse's flat
// FieldError list into FieldErrors keyed by the editor's own field keys,
// so a form field can look itself up directly instead of scanning the
// whole list on every render. The server's field paths are prefixed
// "spec." for everything that lives on FeedSpec (PLAN.md's proto groups
// schedule/generation/novelty/budgets/sources under Feed.spec); this
// strips that prefix so "spec.cron" and a bare "slug" both become editor
// keys with no special-casing at the call site.
func MapFieldErrors(errs []*affv1.FieldError) FieldErrors {
	out := make(FieldErrors, len(errs))
	for _, e := range errs {
		if e == nil {
			continue
		}
		out[EditorFieldKey(e.GetField())] = e.GetMessage()
	}
	return out
}

// EditorFieldKey normalizes one server field path to the key the editor's
// components are keyed by: strip a leading "spec.", leave everything else
// (including array indices like "sources[0].url") untouched, since that
// is already the shape a per-row error lookup wants.
func EditorFieldKey(serverField string) string {
	return strings.TrimPrefix(serverField, "spec.")
}

// ---------------------------------------------------------------------
// Slug immutability (D2-07, PLAN.md §14.1)
// ---------------------------------------------------------------------

// SlugEditable reports whether the slug field may still be changed.
//
// ASSUMPTION (recorded per this task's instructions, since it did not
// hold cleanly against §12.3): the Feed proto has no explicit "has this
// ever published" flag. "Immutable after first publish" is inferred here
// as "the feed has never completed a build" — LastBuiltAt unset — which
// is the closest available signal (a feed with a populated LastBuiltAt
// has rendered at least one build, and any XML that left this process
// could have embedded the slug in a guid per §5.1). A brand-new,
// never-saved feed (nil, or Id == 0) is always editable.
func SlugEditable(feed *affv1.Feed) bool {
	if feed == nil || feed.GetId() == 0 {
		return true
	}
	return feed.GetLastBuiltAt() == nil
}

// SlugImmutableReason returns the i18n key (PLAN.md §14.1: "the UI must
// say WHY") explaining the lock, so the editor never just greys the field
// out silently.
func SlugImmutableReason(t Translator) string {
	if t == nil {
		t = DefaultTranslator
	}
	return t.T("generate.editor.slug.immutableReason")
}

// ---------------------------------------------------------------------
// Cron + timezone plain-English readback (D2-08) and jittered next runs
// (D2-09, PLAN.md §14.3)
// ---------------------------------------------------------------------

// CronReadback produces a short plain-English description of a standard
// 5-field cron expression ("minute hour day-of-month month day-of-week"),
// falling back to a literal echo of the fields for anything beyond the
// small set of shapes actually used by feed schedules (hourly, daily,
// weekly, monthly) rather than mis-describing an expression it does not
// actually understand — a wrong plain-English readback is worse than an
// honest "cron: ..." echo, because it would be trusted.
func CronReadback(t Translator, cron, timezone string) string {
	if t == nil {
		t = DefaultTranslator
	}
	fields := strings.Fields(cron)
	if len(fields) != 5 {
		return t.T("generate.editor.cron.readback.raw", cron, timezone)
	}
	minute, hour, dom, month, dow := fields[0], fields[1], fields[2], fields[3], fields[4]

	if dom == "*" && month == "*" && dow == "*" {
		if minute == "*" && hour == "*" {
			return t.T("generate.editor.cron.readback.everyMinute", timezone)
		}
		if m, hh, ok := parseClock(minute, hour); ok {
			return t.T("generate.editor.cron.readback.daily", clockString(hh, m), timezone)
		}
	}
	if dom == "*" && month == "*" && dow != "*" {
		if m, hh, ok := parseClock(minute, hour); ok {
			if name, ok := weekdayName(dow); ok {
				return t.T("generate.editor.cron.readback.weekly", name, clockString(hh, m), timezone)
			}
		}
	}
	if dom != "*" && month == "*" && dow == "*" {
		if m, hh, ok := parseClock(minute, hour); ok {
			return t.T("generate.editor.cron.readback.monthly", dom, clockString(hh, m), timezone)
		}
	}
	return t.T("generate.editor.cron.readback.raw", cron, timezone)
}

func parseClock(minute, hour string) (m, h int, ok bool) {
	mi, err1 := strconv.Atoi(minute)
	hi, err2 := strconv.Atoi(hour)
	if err1 != nil || err2 != nil || mi < 0 || mi > 59 || hi < 0 || hi > 23 {
		return 0, 0, false
	}
	return mi, hi, true
}

func clockString(hour, minute int) string {
	return fmt.Sprintf("%02d:%02d", hour, minute)
}

func weekdayName(dow string) (string, bool) {
	names := map[string]string{
		"0": "Sunday", "1": "Monday", "2": "Tuesday", "3": "Wednesday",
		"4": "Thursday", "5": "Friday", "6": "Saturday", "7": "Sunday",
	}
	name, ok := names[dow]
	return name, ok
}

// JitteredRuns applies a feed's deterministic per-slug jitter offset
// (PLAN.md §14.3: "hash(slug) spread across a configurable window") to a
// set of nominal cron fire times, returning the times actually expected
// to fire. TODOS.md D2-09 is explicit that the rail must display these,
// not the nominal ones, "otherwise the readback lies."
//
// ASSUMPTION this exposed a real gap (recorded per this task's
// instructions): the aff.v1.Feed message carries jitter_offset_seconds
// but no "next N nominal fire times" field at all — computing those
// requires evaluating the cron expression against a clock, which belongs
// server-side (internal/scheduler already owns exactly this) and is not
// exposed by any RPC in proto/aff/v1 today. This function is therefore
// deliberately shaped to take nominal times as a parameter rather than
// compute them itself, so it is ready the moment such a field (or RPC)
// exists; until then the render layer has nothing correct to pass it and
// must say so rather than fabricate times (see render_rail.go).
func JitteredRuns(nominal []time.Time, jitterOffsetSeconds int32) []time.Time {
	out := make([]time.Time, len(nominal))
	offset := time.Duration(jitterOffsetSeconds) * time.Second
	for i, n := range nominal {
		out[i] = n.Add(offset)
	}
	return out
}

// FormatNextRuns renders JitteredRuns' output through Formatters (PLAN.md
// §12.6: never fmt.Sprintf for a date the operator reads).
func FormatNextRuns(fmtr Formatters, jittered []time.Time, timezone string) []string {
	if fmtr == nil {
		fmtr = DefaultFormatters
	}
	out := make([]string, len(jittered))
	for i, t := range jittered {
		out[i] = fmtr.DateTime(t, timezone)
	}
	return out
}

// ---------------------------------------------------------------------
// Cost estimation (D2-25, D2-26)
// ---------------------------------------------------------------------

// EstimateSampleCostUSD is the pre-click estimate PLAN.md §12.3 requires
// ("the Sample button shows estimated cost BEFORE it is clicked"). No
// prompt has been rendered yet at this point, so token counts are not
// known; this uses the same character/4-per-token rule of thumb common
// for English text against the templates as they currently stand, and the
// caller is required to label it an estimate (PLAN.md §8.1's rule for
// every cost figure this project shows applies here too, not just to
// post-sample numbers). ok is false when model has no price-table entry —
// callers should render "no estimate available" rather than a fabricated
// $0.00, which would look like a free sample.
func EstimateSampleCostUSD(prices []*affv1.PriceEntry, model string, promptCharsIn, promptCharsOutPerItem int, sampleSize int32) (usd float64, ok bool) {
	var entry *affv1.PriceEntry
	for _, p := range prices {
		if p != nil && p.GetModel() == model {
			entry = p
			break
		}
	}
	if entry == nil || sampleSize <= 0 {
		return 0, false
	}
	tokensIn := float64(promptCharsIn) / 4.0
	tokensOutPerItem := float64(promptCharsOutPerItem) / 4.0
	tokensOut := tokensOutPerItem * float64(sampleSize)
	usd = (tokensIn/1000.0)*entry.GetUsdPer_1KTokensIn() + (tokensOut/1000.0)*entry.GetUsdPer_1KTokensOut()
	return usd, true
}

// SumCandidateCostUSD adds up estimated_cost_usd across returned
// candidates, for "cost per sample" (PLAN.md §12.3) once a sample has
// actually run and each candidate carries a server-computed estimate.
func SumCandidateCostUSD(candidates []*affv1.SampleCandidate) float64 {
	var total float64
	for _, c := range candidates {
		if c != nil {
			total += c.GetEstimatedCostUsd()
		}
	}
	return total
}

// ---------------------------------------------------------------------
// Kill switch / disabled-with-reason (D2-27)
// ---------------------------------------------------------------------

// SampleDisabledReason returns the i18n key for why the Sample button is
// inert, or "" if it is not. PLAN.md §12.3: "Kill switch disables sampling
// with a VISIBLE REASON, not a dead control" — globalKilled takes
// precedence over a per-feed reason because a global outage explains
// every feed at once and a per-feed message under it would be redundant
// noise, not more information.
func SampleDisabledReason(t Translator, globalKilled bool, feedDisabled bool, feedConsecutiveFailures int32, autoDisableThreshold int32) string {
	if t == nil {
		t = DefaultTranslator
	}
	switch {
	case globalKilled:
		return t.T("generate.sampler.disabled.globalKillSwitch")
	case feedDisabled:
		return t.T("generate.sampler.disabled.feedDisabled")
	case autoDisableThreshold > 0 && feedConsecutiveFailures >= autoDisableThreshold:
		return t.T("generate.sampler.disabled.autoDisabled", feedConsecutiveFailures)
	default:
		return ""
	}
}

// ---------------------------------------------------------------------
// Sample request validation
// ---------------------------------------------------------------------

// ValidSampleSize reports whether n is within PLAN.md §12.3's 1-5 range
// (D2-17).
func ValidSampleSize(n int32) bool {
	return n >= 1 && n <= 5
}

// ---------------------------------------------------------------------
// Candidate view switching (D2-19..22)
// ---------------------------------------------------------------------

// CandidateViewContent returns the text to display for one
// SampleCandidate under one CandidateView. RawFields deliberately lists
// every validated field the generation contract (PLAN.md §9) defines,
// labelled, rather than dumping a Go struct — the point of the raw view is
// "the validated JSON", i.e. the fields as data, not Go internals.
func CandidateViewContent(v CandidateView, c *affv1.SampleCandidate) string {
	if c == nil {
		return ""
	}
	switch v {
	case ViewRendered:
		body := c.GetSummaryText()
		if body == "" {
			body = c.GetBodyHtml()
		}
		return c.GetTitle() + "\n\n" + body
	case ViewRawFields:
		var b strings.Builder
		fmt.Fprintf(&b, "title: %s\n", c.GetTitle())
		fmt.Fprintf(&b, "summary_text: %s\n", c.GetSummaryText())
		fmt.Fprintf(&b, "body_html: %s\n", c.GetBodyHtml())
		if c.GetAnswerHtml() != "" {
			fmt.Fprintf(&b, "answer_html: %s\n", c.GetAnswerHtml())
		}
		fmt.Fprintf(&b, "link: %s\n", c.GetLink())
		if c.GetSourceName() != "" {
			fmt.Fprintf(&b, "source_name: %s\n", c.GetSourceName())
		}
		return b.String()
	case ViewFeedXML:
		return c.GetRenderedXml()
	case ViewEmbed:
		// The embed's document source. renderCandidateDetail displays this
		// view as a live sandboxed iframe rather than as text (the embed is a
		// visual surface, and its source tells an operator nothing they can
		// judge) — this branch is what a caller gets who wants the bytes:
		// the i18n callsite tests, and anything that needs a text fallback.
		return c.GetEmbedPreviewHtml()
	case ViewSlackCard:
		return c.GetSlackPreviewText()
	default:
		return ""
	}
}

// ---------------------------------------------------------------------
// Novelty and link verdicts (D2-23, D2-24)
// ---------------------------------------------------------------------

// NoveltySummary renders a NoveltyVerdict as one line, always naming the
// nearest existing item when there is one so PLAN.md §12.3's "shows the
// verdict with the nearest existing item" holds even on a pass (near-miss
// but still accepted), not only on a hard reject.
func NoveltySummary(t Translator, fmtr Formatters, v *affv1.NoveltyVerdict) string {
	if t == nil {
		t = DefaultTranslator
	}
	if fmtr == nil {
		fmtr = DefaultFormatters
	}
	if v == nil {
		return t.T("generate.sampler.novelty.unknown")
	}
	if v.GetIsNovel() && v.GetNearestItemId() == 0 {
		return t.T("generate.sampler.novelty.novel")
	}
	if v.GetIsNovel() {
		return t.T("generate.sampler.novelty.novelNear", fmtr.Percent(v.GetSimilarity()), v.GetNearestItemTitle())
	}
	return t.T("generate.sampler.novelty.rejected", fmtr.Percent(v.GetSimilarity()), v.GetNearestItemTitle())
}

// FailedLinks filters a candidate's link verdicts down to the failures
// PLAN.md §12.3 requires be flagged "with the URL" — a failure is only
// actionable if the operator can see which URL it was.
func FailedLinks(verdicts []*affv1.LinkVerdict) []*affv1.LinkVerdict {
	var out []*affv1.LinkVerdict
	for _, lv := range verdicts {
		if lv != nil && !lv.GetOk() {
			out = append(out, lv)
		}
	}
	return out
}

// ---------------------------------------------------------------------
// Optimistic-concurrency conflict resolution (D2-16, PLAN.md §11)
// ---------------------------------------------------------------------

// feedScalarFields lists the Feed/FeedSpec fields ApplyResolution knows
// how to diff and merge. Kept as an ordered slice (not a map) so
// DiffFeedFields returns diffs in a stable, reviewable order.
var feedScalarFields = []string{
	"title", "description", "language", "author", "copyright", "og_image",
	"ttl_minutes", "enabled",
	"cron", "timezone", "schedule_mode", "items_per_run", "feed_window", "model",
	"temperature", "web_search", "system_prompt_template", "user_prompt_template",
	"daily_token_budget", "daily_run_budget",
	// Known gap: Recurrence is a message, not a scalar, so builder edits
	// that change ONLY the recurrence do not register here — neither for
	// dirty detection nor per-field conflict resolution. Tracked as a
	// TODOS.md follow-up rather than a stringly codec bolted on in passing.
}

func feedFieldValue(f *affv1.Feed, key string) string {
	if f == nil {
		return ""
	}
	spec := f.GetSpec()
	switch key {
	case "title":
		return f.GetTitle()
	case "description":
		return f.GetDescription()
	case "language":
		return f.GetLanguage()
	case "author":
		return f.GetAuthor()
	case "copyright":
		return f.GetCopyright()
	case "og_image":
		return f.GetOgImage()
	case "ttl_minutes":
		return strconv.Itoa(int(f.GetTtlMinutes()))
	case "enabled":
		return strconv.FormatBool(f.GetEnabled())
	case "cron":
		return spec.GetCron()
	case "timezone":
		return spec.GetTimezone()
	case "schedule_mode":
		return spec.GetScheduleMode()
	case "web_search":
		return strconv.FormatBool(spec.GetWebSearch())
	case "items_per_run":
		return strconv.Itoa(int(spec.GetItemsPerRun()))
	case "feed_window":
		return strconv.Itoa(int(spec.GetFeedWindow()))
	case "model":
		return spec.GetModel()
	case "temperature":
		return strconv.FormatFloat(spec.GetTemperature(), 'g', -1, 64)
	case "system_prompt_template":
		return spec.GetSystemPromptTemplate()
	case "user_prompt_template":
		return spec.GetUserPromptTemplate()
	case "daily_token_budget":
		return strconv.FormatInt(spec.GetDailyTokenBudget(), 10)
	case "daily_run_budget":
		return strconv.Itoa(int(spec.GetDailyRunBudget()))
	default:
		return ""
	}
}

// DiffFeedFields compares the operator's in-progress draft (mine) against
// the server's current, conflicting version (theirs), returning one entry
// per field that actually differs — an unchanged field is not shown,
// since it is not part of the conflict (PLAN.md §11/TODOS.md D2-16: "a
// real merge choice", which means showing only what actually needs one).
func DiffFeedFields(mine, theirs *affv1.Feed) []FieldDiff {
	var out []FieldDiff
	for _, key := range feedScalarFields {
		mv := feedFieldValue(mine, key)
		tv := feedFieldValue(theirs, key)
		if mv != tv {
			out = append(out, FieldDiff{FieldKey: key, Mine: mv, Theirs: tv})
		}
	}
	return out
}

// ApplyResolution builds the Feed to submit as the retried
// FeedServiceUpdateRequest, given the operator's ConflictResolution
// choice. It always starts from theirs (the server's current row, whose
// Version is the correct expected_version for the retry — PLAN.md §11)
// and layers the operator's intent on top, so a field neither side
// touched is never accidentally reset. perFieldKeepMine is consulted only
// for ResolvePerField, keyed by FieldDiff.FieldKey; a key absent from it
// defaults to keeping theirs, the safer default when the operator hasn't
// explicitly chosen for that field.
func ApplyResolution(mine, theirs *affv1.Feed, resolution ConflictResolution, perFieldKeepMine map[string]bool) *affv1.Feed {
	if theirs == nil {
		return mine
	}
	switch resolution {
	case ResolveTakeTheirs:
		return theirs
	case ResolveKeepMine:
		if mine == nil {
			return theirs
		}
		merged := cloneFeed(mine)
		merged.Version = theirs.GetVersion()
		return merged
	case ResolvePerField:
		merged := cloneFeed(theirs)
		for _, key := range feedScalarFields {
			if perFieldKeepMine[key] {
				applyFeedField(merged, key, feedFieldValue(mine, key))
			}
		}
		merged.Version = theirs.GetVersion()
		return merged
	default:
		return theirs
	}
}

// cloneFeed deep-copies f via proto.Clone rather than a Go struct copy:
// generated message types embed a protoimpl.MessageState (a sync.Mutex
// among other bookkeeping), which `go vet` correctly refuses to let a
// plain `*f` copy — proto.Clone is the library's own answer to "I need an
// independent copy of this message" and is already a transitive
// dependency of every generated client in this module, not a new one.
func cloneFeed(f *affv1.Feed) *affv1.Feed {
	if f == nil {
		return &affv1.Feed{Spec: &affv1.FeedSpec{}}
	}
	clone := proto.Clone(f).(*affv1.Feed)
	if clone.Spec == nil {
		clone.Spec = &affv1.FeedSpec{}
	}
	return clone
}

func applyFeedField(f *affv1.Feed, key, value string) {
	if f.Spec == nil {
		f.Spec = &affv1.FeedSpec{}
	}
	switch key {
	case "title":
		f.Title = value
	case "description":
		f.Description = value
	case "language":
		f.Language = value
	case "author":
		f.Author = value
	case "copyright":
		f.Copyright = value
	case "og_image":
		f.OgImage = value
	case "ttl_minutes":
		if n, err := strconv.Atoi(value); err == nil {
			f.TtlMinutes = int32(n)
		}
	case "enabled":
		f.Enabled = value == "true"
	case "cron":
		f.Spec.Cron = value
	case "timezone":
		f.Spec.Timezone = value
	case "schedule_mode":
		f.Spec.ScheduleMode = value
	case "web_search":
		f.Spec.WebSearch = value == "true"
	case "items_per_run":
		if n, err := strconv.Atoi(value); err == nil {
			f.Spec.ItemsPerRun = int32(n)
		}
	case "feed_window":
		if n, err := strconv.Atoi(value); err == nil {
			f.Spec.FeedWindow = int32(n)
		}
	case "model":
		f.Spec.Model = value
	case "temperature":
		if v, err := strconv.ParseFloat(value, 64); err == nil {
			f.Spec.Temperature = v
		}
	case "system_prompt_template":
		f.Spec.SystemPromptTemplate = value
	case "user_prompt_template":
		f.Spec.UserPromptTemplate = value
	case "daily_token_budget":
		if n, err := strconv.ParseInt(value, 10, 64); err == nil {
			f.Spec.DailyTokenBudget = n
		}
	case "daily_run_budget":
		if n, err := strconv.Atoi(value); err == nil {
			f.Spec.DailyRunBudget = int32(n)
		}
	}
}

// ---------------------------------------------------------------------
// Unsaved-changes guard (D2-15)
// ---------------------------------------------------------------------

// DraftDirty reports whether draft differs from the last-loaded/saved
// snapshot, for the unsaved-changes navigation guard and D0-08's
// session-expiry hold (web/shell.RegisterDirtyCheck). Comparing by the
// same field set DiffFeedFields uses keeps "dirty" and "conflicting field"
// defined identically rather than as two subtly different notions of
// "changed".
func DraftDirty(loaded, draft *affv1.Feed) bool {
	return len(DiffFeedFields(draft, loaded)) > 0
}

// ---------------------------------------------------------------------
// Sample-survives-refresh persistence (D2-29)
// ---------------------------------------------------------------------

// persistedSampleStorageKey lives in render.go: its only callers are
// `js && wasm`, and an untagged declaration is dead code in the host build
// that `staticcheck ./...` reports as U1000.

// sampleRetentionTTL is D2-29's "survive a page refresh for 24h" window.
const sampleRetentionTTL = 24 * time.Hour

// PersistedSampleState is the JSON envelope written to/read from
// localStorage so an in-flight (or just-finished) sample survives a page
// refresh. SavedAtUnix is stamped at write time so a stale entry (older
// than sampleRetentionTTL, or from a browser session left open past it)
// is treated as absent rather than resurrected.
type PersistedSampleState struct {
	SavedAtUnix       int64                    `json:"savedAtUnix"`
	FeedSlug          string                   `json:"feedSlug"`
	SampleID          string                   `json:"sampleId"`
	Candidates        []*affv1.SampleCandidate `json:"candidates"`
	SampleSize        int32                    `json:"sampleSize"`
	TempOverride      float64                  `json:"tempOverride"`
	SelectedCandidate int                      `json:"selectedCandidate"`
	SelectedView      CandidateView            `json:"selectedView"`
}

// PersistedSampleUsable reports whether a stored snapshot is both for the
// given feed slug and still inside the 24h retention window as of now, so
// the hydration effect and its tests can share one definition of "still
// good" instead of duplicating the slug/TTL check inline.
func PersistedSampleUsable(ps PersistedSampleState, slug string, now time.Time) bool {
	if ps.SampleID == "" || len(ps.Candidates) == 0 || ps.FeedSlug == "" || ps.FeedSlug != slug {
		return false
	}
	savedAt := time.Unix(ps.SavedAtUnix, 0)
	return now.Sub(savedAt) < sampleRetentionTTL && now.Sub(savedAt) >= 0
}

// ---------------------------------------------------------------------
// Small numeric <-> string helpers for controlled number inputs
// ---------------------------------------------------------------------
//
// The editor's number fields (items-per-run, budgets, temperature, ...)
// are controlled inputs (h.Value(...) + h.OnInput(...)), which need a
// string on the way out and a numeric field on the way in. These are kept
// tiny, pure, and host-testable rather than inlined at each call site so
// a bad user keystroke (an in-progress "-" or an empty field) has one
// well-defined behavior everywhere: parseXOr falls back to the field's
// current value rather than snapping to 0, which would fight the operator
// mid-edit.

// floatStr/intStr/int64Str format a field's current value back into its
// input. They are referenced only from the `js && wasm` render files, which
// staticcheck's host-target analysis cannot see — hence the explicit ignore
// rather than a deletion. Deleting them breaks the wasm build while leaving
// the host build green, which is the worst way to find out.
//
//lint:ignore U1000 used from the js-tagged render files
func floatStr(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

//lint:ignore U1000 used from the js-tagged render files
func intStr(v int32) string { return strconv.Itoa(int(v)) }

//lint:ignore U1000 used from the js-tagged render files
func int64Str(v int64) string { return strconv.FormatInt(v, 10) }

func parseFloatOr(s string, fallback float64) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fallback
	}
	return v
}

func parseIntOr(s string, fallback int) int {
	v, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return v
}

// ---------------------------------------------------------------------
// Staleness flag (D2-03, PLAN.md §15)
// ---------------------------------------------------------------------

// IsStale reports whether a feed should show the rail's stale flag: it is
// enabled, has built at least once, and that build is older than
// thresholdMinutes. A feed that has never built is "new", not "stale" —
// conflating the two would flag every freshly created feed on its own
// creation.
func IsStale(enabled bool, lastBuiltAt *time.Time, now time.Time, thresholdMinutes int32) bool {
	if !enabled || lastBuiltAt == nil || thresholdMinutes <= 0 {
		return false
	}
	return now.Sub(*lastBuiltAt) > time.Duration(thresholdMinutes)*time.Minute
}
