//go:build js && wasm

package generatepage

import (
	"strconv"
	"syscall/js"

	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	wui "github.com/monstercameron/AnimeFeedFlux/web/ui"
)

// render_workbench.go is /generate's layout, rebuilt.
//
// # What was wrong
//
// The page was three columns of roughly equal weight — an 18rem feed rail, a
// recipe form, a 22rem sampler. That spends a permanent quarter of the screen
// on a list of three feeds, gives the prompt and its output a third each, and
// interleaves the fields an operator retunes every few minutes (prompts,
// model, effort) with the ones they set once and never touch again (slug,
// cron, timezone, budgets, feed window, sources). The loop the page exists
// for — write, preview, judge, adjust — was the thing it made hardest.
//
// # The shape now
//
// One thesis: THE PROMPT AND ITS OUTPUT, SIDE BY SIDE, FILLING THE SCREEN.
// Everything else gets out of the way.
//
//	┌──────────────────────────────────────────────────────────┐
//	│ [feed ▾]  model ▾  effort ▾  size ▾        [Preview ~$—] │  sticky strip
//	├───────────────────────────────┬──────────────────────────┤
//	│ SYSTEM  (mono, grows)         │ PREVIEW                  │
//	│ USER    (mono, grows)         │  rendered / raw / xml /  │
//	│ {{.Today}} {{.Season}} chips  │  slack, promote, discard │
//	├───────────────────────────────┴──────────────────────────┤
//	│ ▸ Recipe settings — slug, schedule, budgets, sources      │
//	└──────────────────────────────────────────────────────────┘
//
// The prompts are the CONTENT of this screen, so they wear the mono face at a
// size meant for writing rather than scanning; the chrome around them stays
// the UI sans.
//
// # The signature: template-variable chips
//
// §7 gives prompts a fixed set of template variables, and the editor used to
// list them as static text above the fields. A list you read and then retype
// is the worst version of that: the failure mode is a typo like {{.Titles}},
// which `Parse` accepts and only `Execute` rejects — so it surfaces as a
// validation error AFTER a paid provider call. The chips insert the exact
// identifier at the cursor. It is the one flourish on the page, it is lifted
// from the product's own mechanics rather than decoration, and it removes a
// class of error rather than illustrating one.

// promptVariables are §7's template variables, in the order they appear
// there. Kept in sync with internal/generate.Data by hand — that type's own
// doc comment names this drift as the thing to watch.
var promptVariables = []string{
	"{{.Today}}", "{{.Weekday}}", "{{.Season}}",
	"{{.FeedTitle}}", "{{.ItemsPerRun}}",
	"{{.RecentTitles}}", "{{.Candidates}}",
}

// workbenchProps is everything the layout needs that it does not own.
type workbenchProps struct {
	Strip   ui.Node
	Tape    ui.Node
	Stakes  ui.Node
	Prompts ui.Node
	Preview ui.Node
	// Schedule is the always-visible scheduler section
	// (renderScheduleSection) — deliberately NOT part of Recipe, which
	// lives behind the collapsed drawer below it.
	Schedule ui.Node
	Recipe   ui.Node

	// Feeds is the permanent sidebar (render_rail.go's renderRail) — every
	// feed, compact, always visible, independently scrolling. Not a
	// disclosure: see renderWorkbench's doc comment for why the earlier
	// stacked-and-collapsible version of this kept being wrong in a new way
	// every time it was patched.
	Feeds ui.Node
	// RecipeOpen forces the recipe drawer open. Creating a feed REQUIRES
	// fields that live in it (slug, title, schedule), so leaving it shut on a
	// new draft asks the operator to guess where the form is.
	RecipeOpen bool
	// FeedConfirm is the delete confirmation modal. Rendered at the top level
	// so it is not inside a collapsed <details>, which would hide the dialog
	// that the collapsed thing just opened.
	FeedConfirm ui.Node
}

// stakesProps is the one-line readout of what the selected feed actually is.
type stakesProps struct {
	Feed    *affv1.Feed
	Enabled bool
}

// renderStakes states the facts that decide whether pressing Preview is a
// good idea: which feed is really loaded, whether it is enabled, what
// schedule it runs on, and what its daily budget is.
//
// These live in the recipe form, which the workbench collapsed to a
// disclosure at the bottom of the page — correct for the fields nobody edits
// twice, wrong for these four, which are exactly what an operator needs
// BEFORE writing a prompt and spending money on it. §12.3 also requires a
// disabled feed to be visible rather than something you discover after a run
// that never happens.
func renderStakes(p stakesProps) ui.Node {
	t := deps.I18n
	if p.Feed == nil {
		return h.Fragment()
	}
	spec := p.Feed.GetSpec()
	parts := []any{h.ClassStr("af-gen__stakes")}
	parts = append(parts, h.Span(h.ClassStr("af-gen__stakes-slug"), h.Text(p.Feed.GetSlug())))
	if !p.Enabled {
		parts = append(parts, h.Span(h.ClassStr("af-gen__stakes-flag"),
			h.Text(t.T("generate.workbench.stakes.disabled"))))
	}
	if cron := spec.GetCron(); cron != "" {
		parts = append(parts, h.Span(h.Text(t.T("generate.workbench.stakes.schedule", cron, spec.GetTimezone()))))
	}
	if b := spec.GetDailyTokenBudget(); b > 0 {
		parts = append(parts, h.Span(h.Text(t.T("generate.workbench.stakes.budget",
			strconv.FormatInt(b, 10), strconv.Itoa(int(spec.GetDailyRunBudget()))))))
	}
	return h.Div(parts...)
}

// renderWorkbench composes the page. It holds no state of its own: every
// region is built by the caller from Render's existing state, so this
// function is pure layout and can be reasoned about as such.
//
// # A genuinely different shape, not a repainted one
//
// This page went through several passes that all kept the SAME skeleton — a
// sticky strip, then a "Feeds" section, then the prompt/preview work area,
// then a recipe disclosure, everything stacked in one column — and only
// changed how each piece was styled: box borders removed, a tape enlarged,
// the strip regrouped, the feed list capped and then paginated. Every one of
// those was a real fix for a real bug, and none of them was what was asked
// for, which was called out directly: "you kept giving me the same
// fundamental layout." The list capping/pagination churn was itself a
// symptom — a single stacked column with a "Feeds" SECTION was fighting the
// fact that a feed roster and a feed's own work area are two different
// things an operator looks at differently (scan the roster vs. focus on
// one), and no amount of restyling that one section was going to resolve
// that; it needed to stop being a section.
//
// So: two columns, not one. A persistent, compact, independently-scrolling
// sidebar (`.af-gen__sidebar`, render_rail.go) — every feed, always visible,
// never a disclosure to open or a list to page through — beside a main
// column (`.af-gen__main`) holding everything about whichever ONE feed is
// loaded: the strip, its tape, its stakes, the prompt/preview work area, its
// recipe settings. Each column has its OWN height and its OWN scroll
// (styles.go), which is also what closes the "two scrolls with dependencies
// between them" bug for good: sibling columns with independent scroll
// regions is the normal, unambiguous version of that pattern (an inbox next
// to a reading pane) — the broken version was one scroll region nested
// INSIDE another, on the same axis, with the outer one's sticky chrome
// depending on it.
func renderWorkbench(p workbenchProps) ui.Node {
	return h.Div(
		h.ClassStr("af-gen"),
		p.Feeds,
		h.Div(h.ClassStr("af-gen__main"),
			h.Div(h.ClassStr("af-gen__strip"), p.Strip),
			p.Tape,
			p.Stakes,
			p.FeedConfirm,
			h.Div(h.ClassStr("af-gen__work"),
				h.Section(h.ClassStr("af-gen__prompts"), p.Prompts),
				h.Div(h.ClassStr("af-gen__work-divider"), h.Aria("hidden", "true")),
				h.Section(h.ClassStr("af-gen__preview"), p.Preview),
			),
			// The scheduler, ALWAYS visible for a loaded feed (2026-08-15):
			// the one control deciding WHEN a feed does anything does not
			// belong behind a disclosure — the operator literally could not
			// find it there. Set-once identity fields stay in the drawer
			// below; the schedule is not set-once, it is the thing an
			// operator comes back to adjust.
			p.Schedule,
			// The recipe stays collapsed for an EXISTING feed — those are
			// set-once fields, and having them open is what made this page a
			// form with a preview bolted on. A new draft is the exception;
			// see RecipeOpen.
			renderRecipeDisclosure(p),
		),
	)
}

// renderRecipeDisclosure wraps the recipe form, opened when the draft needs
// it. Args are spread, not passed as a slice — see renderFeedsDisclosure.
func renderRecipeDisclosure(p workbenchProps) ui.Node {
	t := deps.I18n
	args := []any{h.ClassStr("af-gen__recipe")}
	if p.RecipeOpen {
		args = append(args, h.Attr("open", "open"))
	}
	args = append(args,
		h.Tag("summary", nil, h.Text(t.T("generate.workbench.recipeSettings"))),
		p.Recipe,
	)
	return h.Tag("details", args...)
}

// promptFieldProps is one prompt editor.
type promptFieldProps struct {
	ID       string
	LabelKey string
	HintKey  string
	Value    string
	OnChange func(string)
	ErrNode  ui.Node
	// Chips adds the variable chips under this field. Only the user prompt
	// gets them: the system prompt is standing instruction text, and §7's
	// variables are about the per-run data the user prompt interpolates.
	Chips bool
}

// fieldErrNode renders a server FieldError for key as af-field-error text, or
// nothing if the field has none. The workbench's own system/user prompt
// fields (this file) are the only place those two errors show now — the
// recipe drawer's copy of the same two fields (render_editor.go) was removed
// as a redundant duplicate of these, so its fieldError("system_prompt_
// template"/"user_prompt_template") calls went with it.
func fieldErrNode(fe FieldErrors, key string) ui.Node {
	msg, ok := fe.For(key)
	return h.If(ok, h.Div(h.ClassStr("af-field-error"), h.Text(msg)))
}

// renderPromptField is a labelled mono textarea, optionally with the
// variable chips beneath it.
func renderPromptField(p promptFieldProps) ui.Node {
	t := deps.I18n
	body := []any{
		h.ClassStr("af-gen__field"),
		h.Label(h.For(p.ID), h.Text(t.T(p.LabelKey))),
		h.Textarea(
			h.ID(p.ID),
			h.ClassStr("af-gen__prompt"),
			h.Value(p.Value),
			h.Attr("spellcheck", "false"),
			h.OnInput(p.OnChange),
		),
		h.P(h.ClassStr("af-gen__hint"), h.Text(t.T(p.HintKey))),
		p.ErrNode,
	}
	if p.Chips {
		body = append(body, renderVariableChips(p.ID, p.OnChange))
	}
	return h.Div(body...)
}

// renderVariableChips is the signature element: click a variable, it lands at
// the cursor.
//
// The insert is done through syscall/js against the textarea's own
// selectionStart/selectionEnd rather than by rebuilding the value in Go,
// because only the DOM knows where the caret is — a Go-side append would
// always add at the end, which is wrong the moment someone is editing the
// middle of a prompt, and moving the caret afterwards is the difference
// between a control that helps and one that has to be undone.
func renderVariableChips(targetID string, onChange func(string)) ui.Node {
	t := deps.I18n
	chips := make([]any, 0, len(promptVariables)+2)
	chips = append(chips,
		h.ClassStr("af-gen__chips"),
		h.Span(h.ClassStr("af-gen__chips-label"), h.Text(t.T("generate.workbench.insertVariable"))),
	)
	for _, v := range promptVariables {
		v := v
		chips = append(chips, h.Button(
			h.Type("button"),
			h.ClassStr("af-gen__chip"),
			h.Aria("label", t.T("generate.workbench.insertNamed", v)),
			h.OnClick(ui.UseEvent(func() {
				if next, ok := insertAtCursor(targetID, v); ok {
					onChange(next)
				}
			})),
			h.Text(v), //nolint:i18n -- a template identifier, must never be translated
		))
	}
	return h.Div(chips...)
}

// insertAtCursor splices text into the element's value at the caret and
// returns the new value. ok is false when the element is not on the page —
// a chip clicked against a field that has since unmounted must be a no-op,
// not a panic.
func insertAtCursor(id, text string) (string, bool) {
	doc := js.Global().Get("document")
	if !doc.Truthy() {
		return "", false
	}
	el := doc.Call("getElementById", id)
	if !el.Truthy() {
		return "", false
	}
	value := el.Get("value").String()
	start := el.Get("selectionStart").Int()
	end := el.Get("selectionEnd").Int()
	if start < 0 || start > len(value) || end < start || end > len(value) {
		// A caret position the DOM will not vouch for: append rather than
		// slice at an index that could panic.
		start, end = len(value), len(value)
	}
	next := value[:start] + text + value[end:]
	el.Set("value", next)
	// Put the caret after what was just inserted, so a second chip does not
	// land inside the first.
	caret := start + len(text)
	el.Call("setSelectionRange", caret, caret)
	el.Call("focus")
	return next, true
}

// stripProps is the sticky control row: what to generate, and the one button
// that does it.
type stripProps struct {
	Feeds       []*affv1.Feed
	FeedsErr    error
	OnRetryFeed func()
	Selected    string
	OnSelect    func(string)
	OnNew       func()
	Model       string
	OnModel     func(string)

	// --- The feed's own CRUD, on the strip ---------------------------------
	//
	// These are here because they were nowhere on screen. Save lived at the
	// bottom of a collapsed "Recipe settings" disclosure, Delete inside a ⋯
	// inside a row inside a collapsed "Feeds" disclosure, and New was a text
	// link the width of the word. The report was "where is the option to CRUD
	// a feed?" and the honest answer was: nowhere you could see.
	//
	// Dirty drives Save's enabled state, so the button doubles as the
	// unsaved-changes indicator; Selected gates the menu, since there is
	// nothing to run, disable or delete when no feed is loaded.
	// Creating means an unsaved new feed is being written, which the picker
	// and the page state both need to say out loud.
	Creating     bool
	Dirty        bool
	Saving       bool
	OnSave       func()
	OnDeleteFeed func()
	OnRunNow     func()
	// OnHistory opens this feed's run history. Past generations were only
	// reachable by going to History and re-picking the feed from a menu.
	OnHistory       func()
	FeedEnabled     bool
	OnToggleEnabled func()
	MenuOpen        bool
	OnMenuOpen      func(bool)

	// Models is the provider's own list (SystemService.ListModels). When it
	// is empty or ModelsUnavailable is set, the model control degrades to the
	// text input it used to be — a workbench that cannot pick a model while
	// OpenAI is down is worse than one that asks for the id.
	Models            []*affv1.ProviderModel
	ModelsUnavailable bool
	ModelsReason      string
	Effort            string
	OnEffort          func(string)

	// WebSearch is the recipe's web_search flag (spec.web_search) on the
	// strip's instrument cluster, beside the model it modifies — the drawer
	// alone was invisible, per the same report that put Save up here.
	// ShowWebSearch gates it to generative feeds: grounded link integrity
	// (§9) rejects model-searched URLs, so the server refuses the
	// combination and the control never appears.
	WebSearch     bool
	ShowWebSearch bool
	OnWebSearch   func(bool)
	Size          int32
	OnSize        func(int32)
	Temp          float64
	OnTemp        func(float64)
	Estimate      string
	Disabled      bool
	Reason        string
	Sampling      bool
	OnPreview     func()
}

// tempFieldValue renders the override, blank when unset.
func tempFieldValue(v float64) string {
	if v == 0 {
		return ""
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// saveLabel says what the button will do, and what state the draft is in.
func saveLabel(t Translator, dirty, saving bool) string {
	switch {
	case saving:
		return t.T("generate.workbench.saving")
	case dirty:
		return t.T("generate.workbench.saveChanges")
	default:
		return t.T("generate.workbench.saved")
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// feedMenuProps configures the strip's per-feed action menu.
type feedMenuProps struct {
	Selected        string
	FeedEnabled     bool
	Open            bool
	OnOpen          func(bool)
	OnRunNow        func()
	OnHistory       func()
	OnToggleEnabled func()
	OnDelete        func()
}

// renderFeedMenu is the ⋯ beside the feed picker: Run now, enable/disable,
// and Delete. Its own component so the kebab's hooks live in their own fiber
// rather than shifting the strip's slots when the menu appears and
// disappears with the selection.
func renderFeedMenu(p feedMenuProps) ui.Node {
	t := deps.I18n
	enableKey := "generate.workbench.menu.disable"
	if !p.FeedEnabled {
		enableKey = "generate.workbench.menu.enable"
	}
	return wui.Kebab(wui.KebabProps{
		T: wui.T(t.T), ID: "gen-strip-feed-menu",
		LabelKey: "generate.workbench.menu.label", LabelArgs: []any{p.Selected},
		Open:         p.Open,
		OnOpenChange: p.OnOpen,
		Items: []wui.KebabItem{
			{ID: "gen-strip-run-now", LabelKey: "generate.workbench.menu.runNow", OnSelect: p.OnRunNow},
			{ID: "gen-strip-history", LabelKey: "generate.workbench.menu.history", OnSelect: p.OnHistory},
			{ID: "gen-strip-toggle", LabelKey: enableKey, OnSelect: p.OnToggleEnabled},
			{ID: "gen-strip-delete", LabelKey: "generate.workbench.menu.delete", Danger: true, OnSelect: p.OnDelete},
		},
	})
}

// renderStripModel is the strip's model control: the shared picker
// (modelpicker.go) with the strip's own id and styling.
func renderStripModel(p stripProps) ui.Node {
	return renderModelPicker(modelPickerProps{
		ID:          "gen-strip-model",
		Class:       "af-gen__model",
		LabelKey:    "generate.workbench.model",
		Value:       p.Model,
		OnChange:    p.OnModel,
		Models:      p.Models,
		Unavailable: p.ModelsUnavailable,
		Reason:      p.ModelsReason,
	})
}

// stripModelTitle explains the degraded state on the fallback input rather
// than spending a row of the strip on a warning line.
func stripModelTitle(p stripProps) string {
	if p.ModelsReason != "" {
		return p.ModelsReason
	}
	return deps.I18n.T("generate.workbench.model")
}

// renderStrip puts every input that changes what a preview produces on one
// row, next to the button that produces it. Cause and effect within one
// glance; the recipe fields that do NOT change a single preview (schedule,
// budgets, window) are deliberately not here.
func renderStrip(p stripProps) ui.Node {
	t := deps.I18n

	feedOpts := make([]any, 0, len(p.Feeds)+3)
	feedOpts = append(feedOpts,
		h.ID("gen-strip-feed"),
		h.Aria("label", t.T("generate.workbench.feed")),
		h.OnChange(ui.UseEvent(func(e ui.InputEvent) { p.OnSelect(e.GetValue()) })),
	)
	// The placeholder is rendered UNCONDITIONALLY, not only while nothing is
	// selected. Dropping it once a feed is chosen shifts every option's index
	// by one, and the browser keeps its selection by INDEX across a re-render
	// — which is how the picker came to display the second feed's name while
	// the editor below it held the first feed's prompts.
	// The first option says what state the page is in.
	//
	// Pressing "New feed" used to leave the picker reading "Choose a feed…"
	// with empty prompts below it — indistinguishable from having chosen
	// nothing at all. The only tell that anything had happened was the Save
	// button turning blue, which nobody reads as "you are now creating a
	// feed".
	placeholder := t.T("generate.workbench.chooseFeed")
	if p.Creating {
		placeholder = t.T("generate.workbench.newFeedOption")
	}
	if p.FeedsErr != nil {
		// The strip replaced the rail, and the rail was where a failed feed
		// list said so. Without this the picker is simply empty, which reads
		// as "no feeds exist" — the one thing it does not mean.
		placeholder = t.T("generate.workbench.feedsUnavailable")
	}
	feedOpts = append(feedOpts, h.Option(h.Value(""), h.SelectedIf(p.Selected == ""), h.Text(placeholder)))
	for _, f := range p.Feeds {
		slug := f.GetSlug()
		feedOpts = append(feedOpts, h.Option(h.Value(slug), h.SelectedIf(slug == p.Selected),
			h.Text(f.GetTitle())))
	}

	effortOpts := make([]any, 0, 6)
	effortOpts = append(effortOpts,
		h.ID("gen-strip-effort"),
		h.Aria("label", t.T("generate.workbench.effort")),
		h.OnChange(ui.UseEvent(func(e ui.InputEvent) { p.OnEffort(e.GetValue()) })),
	)
	for _, tier := range []struct{ v, k string }{
		{"smart", "generate.workbench.effort.smart"},
		{"fast", "generate.workbench.effort.fast"},
		{"quick", "generate.workbench.effort.quick"},
	} {
		effortOpts = append(effortOpts, h.Option(h.Value(tier.v),
			h.SelectedIf(tier.v == p.Effort || (p.Effort == "" && tier.v == "smart")),
			h.Text(t.T(tier.k))))
	}

	sizeOpts := make([]any, 0, 8)
	sizeOpts = append(sizeOpts,
		h.ID("gen-strip-size"),
		h.Aria("label", t.T("generate.workbench.size")),
		h.OnChange(ui.UseEvent(func(e ui.InputEvent) {
			if n, err := strconv.Atoi(e.GetValue()); err == nil {
				p.OnSize(int32(n))
			}
		})),
	)
	for n := int32(1); n <= 5; n++ {
		v := strconv.Itoa(int(n))
		sizeOpts = append(sizeOpts, h.Option(h.Value(v), h.SelectedIf(n == p.Size),
			h.Text(t.T("generate.workbench.sizeN", v))))
	}

	return h.Fragment(
		// Zone 1: which sheet is loaded, and its CRUD. The feed select reads
		// as a title, not a form field — it names the thing every other
		// control on this strip acts on, so it gets the strip's largest
		// type rather than competing at the same size as a temperature box.
		h.Div(h.ClassStr("af-gen__strip-identity"),
			h.Select(feedOpts...),
			h.Button(h.Type("button"), h.ClassStr("af-gen__new"),
				h.OnClick(ui.UseEvent(func() { p.OnNew() })),
				h.Text(t.T("generate.workbench.newFeed"))),
			// Save sits beside the feed it saves, not at the far end of a
			// collapsed form. Disabled when there is nothing to save, so it
			// doubles as the unsaved-changes indicator.
			h.Button(h.Type("button"),
				h.ClassStr("af-gen__save"),
				h.Disabled(!p.Dirty || p.Saving),
				h.Aria("busy", boolStr(p.Saving)),
				h.OnClick(ui.UseEvent(func() {
					if p.OnSave != nil {
						p.OnSave()
					}
				})),
				h.Text(saveLabel(t, p.Dirty, p.Saving))),
			// Everything else you can do TO a feed, in one menu: run it now,
			// disable it, delete it.
			h.Show(p.Selected != "", ui.CreateElement(renderFeedMenu, feedMenuProps{
				Selected:        p.Selected,
				FeedEnabled:     p.FeedEnabled,
				Open:            p.MenuOpen,
				OnOpen:          p.OnMenuOpen,
				OnRunNow:        p.OnRunNow,
				OnHistory:       p.OnHistory,
				OnToggleEnabled: p.OnToggleEnabled,
				OnDelete:        p.OnDeleteFeed,
			})),
			// Built every render, shown only on failure: a control that
			// appears and disappears would shift this fiber's hook slots.
			h.Show(p.FeedsErr != nil, h.Button(h.Type("button"), h.ClassStr("af-gen__retry"),
				h.OnClick(ui.UseEvent(func() {
					if p.OnRetryFeed != nil {
						p.OnRetryFeed()
					}
				})),
				h.Text(t.T("generate.workbench.retryFeeds")))),
		),
		// Zone 2: how it will be generated — model, effort, candidate count,
		// temperature. Four fields that were four identical floating boxes
		// (the "wall of gray boxes" the redesign was called out for) are one
		// joined instrument cluster: a single bordered strip with a hairline
		// between cells instead of four separate borders and four gaps. It
		// reads as one control with four dials, which is what it actually
		// is — every one of these is a knob on the SAME upcoming call, not
		// four independent settings.
		h.Div(h.ClassStr("af-gen__strip-params"),
			h.Div(h.ClassStr("af-gen__strip-cell"), renderStripModel(p)),
			h.Div(h.ClassStr("af-gen__strip-cell"), h.Select(effortOpts...)),
			h.Div(h.ClassStr("af-gen__strip-cell"), h.Select(sizeOpts...)),
			// Temperature is a sample-time OVERRIDE, not a recipe field, so
			// it belongs on the strip with the other inputs that change what
			// a preview produces rather than in the collapsed recipe form.
			h.Div(h.ClassStr("af-gen__strip-cell"),
				h.Input(h.ID("gen-strip-temp"), h.Type("number"),
					h.ClassStr("af-gen__temp"),
					h.Attr("step", "0.1"), h.Attr("min", "0"), h.Attr("max", "2"),
					h.Aria("label", t.T("generate.workbench.temp")),
					// The title says what §8.1 says: SchemaFlux exposes no
					// temperature control, so this value is carried but not yet
					// applied. A knob that silently does nothing is worse than
					// one that admits it.
					h.Attr("title", t.T("generate.workbench.temp.inert")),
					// Zero means "no override", so the field shows empty rather
					// than a literal 0 that reads as temperature=0 — the one
					// value an operator might actually have meant to set.
					h.Value(tempFieldValue(p.Temp)),
					h.Attr("placeholder", t.T("generate.workbench.tempPlaceholder")),
					h.OnInput(func(v string) {
						if f, err := strconv.ParseFloat(v, 64); err == nil {
							p.OnTemp(f)
						}
					})),
			),
			// Built every render, shown only for generative feeds: an
			// appearing/disappearing cell would shift this fiber's hook
			// slots (same rule as the retry button above).
			h.Show(p.ShowWebSearch, h.Div(h.ClassStr("af-gen__strip-cell"),
				h.Label(h.ClassStr("af-gen__websearch"),
					h.Input(h.ID("gen-strip-websearch"), h.Type("checkbox"),
						h.Checked(p.WebSearch),
						h.Attr("title", t.T("generate.editor.webSearchHint")),
						h.OnChange(func(ui.ChangeEvent) {
							if p.OnWebSearch != nil {
								p.OnWebSearch(!p.WebSearch)
							}
						})),
					h.Text(t.T("generate.editor.webSearch"))),
			)),
		),
		h.Div(h.ClassStr("af-gen__strip-right"),
			// The cost sits ON the button's row, not in a panel elsewhere:
			// §12.3 requires the estimate to be visible at the moment of
			// spending, and a number in another column is not that.
			h.Show(p.Estimate != "", h.Span(h.ClassStr("af-gen__estimate"), h.Text(p.Estimate))),
			h.Button(
				h.Type("button"),
				h.ClassStr("af-gen__preview-btn"),
				h.Disabled(p.Disabled || p.Sampling),
				h.OnClick(ui.UseEvent(func() { p.OnPreview() })),
				h.Text(previewLabel(p.Sampling)),
			),
		),
		// A disabled control must say why (§12.3: "the kill switch disables
		// it with a visible reason rather than leaving a dead control").
		h.Show(p.Reason != "", h.P(h.ClassStr("af-gen__strip-reason"), h.Role("status"), h.Text(p.Reason))),
	)
}

func previewLabel(sampling bool) string {
	if sampling {
		return deps.I18n.T("generate.workbench.previewing")
	}
	return deps.I18n.T("generate.workbench.preview")
}

// previewEstimate is the cost shown beside the Preview button.
//
// It reuses the sampler's own EstimateSampleCostUSD rather than a second
// calculation: two estimates of the same call that could disagree is worse
// than one that is sometimes unavailable. Unavailable is a real answer here
// and says so — an empty price table (settings → Provider → Rates) is
// exactly why a run can report $0.0000 while spending money, and a blank
// where a number belongs would hide that.
func previewEstimate(p samplerProps) string {
	t := deps.I18n
	spec := p.Feed.GetSpec()
	usd, ok := EstimateSampleCostUSD(p.Prices, spec.GetModel(),
		len(spec.GetSystemPromptTemplate())+len(spec.GetUserPromptTemplate()), 400, p.SampleSize)
	if !ok {
		return t.T("generate.sampler.estimateUnavailable")
	}
	return t.T("generate.sampler.estimatedCost", deps.Formatters.Currency(usd))
}
