//go:build js && wasm

// render_sampler.go implements the right sampler pane (PLAN.md §12.3,
// TODOS.md D2-17..29): sample size/temperature, streaming output with a
// working cancel, the four candidate views, novelty and link verdicts,
// cost/budget, the kill-switch reason, and Promote/Discard — the pane
// PLAN.md calls "the part that matters" and where "nothing publishes
// implicitly" is the one rule this file must never violate: Promote is
// the only path that writes an item, and it always goes through
// ItemService.PromoteSample against a server-issued sample_id/
// candidate_id pair, never a locally-constructed item.
//
// # Adopted from web/ui (this task)
//
// The candidate list's six-state switch renders through wui.StatePanel/
// uiListState (nodeutil.go) — the precedence decision is still
// SelectListState in logic.go, unchanged and host-tested. The per-candidate
// tab strip and the four-view tab strip are both wui.Tabs (roving-tabindex
// arrow-key navigation, which the previous hand-rolled `<button>` loops had
// none of). Sample-size/temperature-override are wui.Input; Sample/Cancel/
// Promote/Discard are wui.Button.
//
// See render_editor.go's package-level doc comment for this file's hook-
// ordering discipline (hook-free dispatchers + ui.CreateElement for real
// Go-level branches, MapKeyedComponent for any variable-length list whose
// rows carry their own On* handler, and the THIRD hazard web/ui's own
// Disabled-skips-the-hook behavior introduces) — all four apply here:
// the "no feed selected/saved yet" early state, the six-state candidate
// list switch, the candidate tab strip (0-5 items, changing live as a
// stream comes in — genuinely variable, unlike the four fixed view tabs),
// and the Sample/Cancel controls (Disabled tracks Connected/Sampling/
// DisabledReason, isolated below for the same reason render_editor.go
// isolates Validate/Save).
package generatepage

import (
	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	wui "github.com/monstercameron/AnimeFeedFlux/web/ui"
)

type samplerProps struct {
	Connected      bool
	Feed           *affv1.Feed
	DisabledReason string

	SampleSize      int32
	SetSampleSize   func(int32)
	TempOverride    float64
	SetTempOverride func(float64)

	Candidates    []*affv1.SampleCandidate
	Sampling      bool
	SampleErr     error
	SelectedIndex int
	SetSelected   func(int)
	View          CandidateView
	SetView       func(CandidateView)

	RemainingBudget float64
	Prices          []*affv1.PriceEntry

	// ActionErr is the last Promote/Discard mutation failure (TODOS.md
	// D0-10) — distinct from SampleErr, which is the sample stream's own
	// failure and drives the six-state candidate list, not a
	// post-candidate action on it.
	ActionErr error

	OnSample  func()
	OnCancel  func()
	OnPromote func(candidateID string)
	OnDiscard func()
}

// renderSampler is a hook-free dispatcher (see render_editor.go's doc
// comment for the pattern): the "no feed" state and the real sampler body
// have very different hook counts, so each is its own child fiber.
func renderSampler(p samplerProps) ui.Node {
	if p.Feed == nil || p.Feed.GetId() == 0 {
		return ui.CreateElement(renderSamplerEmpty, samplerEmptyProps{})
	}
	return ui.CreateElement(renderSamplerBody, p)
}

type samplerEmptyProps struct{}

func renderSamplerEmpty(samplerEmptyProps) ui.Node {
	return h.Section(h.ClassStr("af-generate__sampler"), h.P(h.Text(deps.I18n.T("generate.sampler.selectOrSaveFeed"))))
}

func renderSamplerBody(p samplerProps) ui.Node {
	t := deps.I18n
	fmtr := deps.Formatters

	state := SelectListState(p.Connected, p.DisabledReason, p.Sampling, p.SampleErr, !p.Sampling && len(p.Candidates) == 0)

	return h.Section(
		h.ClassStr("af-generate__sampler"),
		// Isolated: Cancel's Disabled tracks Sampling, and web/ui.Button
		// skips registering its OnClick hook entirely while Disabled is
		// true, so this block is its own child fiber rather than risking a
		// hook-slot shift in whatever is added after it here.
		ui.CreateElement(renderSamplerControls, samplerControlsProps{
			T: t, Fmtr: fmtr, RemainingBudget: p.RemainingBudget,
			DisabledReason: p.DisabledReason, Sampling: p.Sampling, OnCancel: p.OnCancel,
		}),

		renderCandidateListGate(state, p),
	)
}

type samplerControlsProps struct {
	T               Translator
	Fmtr            Formatters
	RemainingBudget float64
	DisabledReason  string
	Sampling        bool
	OnCancel        func()
}

// renderSamplerControls is what is LEFT of the sampler's old control block.
//
// Size, temperature override, the cost estimate and the Sample button all
// moved to the strip (render_workbench.go): they are inputs that change what
// a preview produces, and the workbench's one rule is that those live on the
// row with the button that spends the money. Repeating them here gave the
// page two Sample buttons and two size fields whose values had to agree.
//
// What stays is everything that is ABOUT a run rather than an input to one:
// the remaining budget, the reason the button is disabled, and Cancel — which
// cannot move to the strip because the strip's single button is already the
// Preview action mid-flight.
func renderSamplerControls(p samplerControlsProps) ui.Node {
	t := p.T
	wt := wui.T(t.T)
	fmtr := p.Fmtr

	return h.Div(h.ClassStr("af-sampler__controls"),
		// The remaining-budget figure that used to sit here is gone until
		// something can fill it. It was initialised to 0.0 and updated by no
		// response on any code path — SampleStream's proto carries no field
		// for it — so it read "Remaining budget: $0.0000" forever, which an
		// operator can only take as "the budget is spent". A number that is
		// always wrong is worse than an absent one. Restoring it needs a
		// proto field; see TODOS A5-13.
		h.Show(p.RemainingBudget > 0, h.Div(h.ClassStr("af-sampler__budget"),
			h.Text(t.T("generate.sampler.remainingBudget", fmtr.Currency(p.RemainingBudget))),
		)),
		// Cancel appears only while a sample is actually in flight. A
		// permanently visible, permanently disabled button at the top of the
		// output pane was the largest control on the page and did nothing
		// 99% of the time.
		h.Show(p.Sampling, wui.Button(wui.ButtonProps{
			T: wt, ID: "generate-sampler-cancel", LabelKey: "generate.sampler.cancel",
			Variant: wui.ButtonSecondary, OnClick: p.OnCancel,
		})),
		h.If(p.DisabledReason != "", h.Div(
			h.ClassStr("af-sampler__disabled-reason"),
			h.Text(p.DisabledReason),
		)),
	)
}

// renderCandidateListGate is hook-free (only h.P/h.Text for the five
// non-populated states, all fixed and always evaluated by the switch
// running exactly once per render with a fixed structure per case — see
// below — and a CreateElement dispatch for the populated case). Unlike
// h.If, a Go `switch` genuinely only evaluates one case's body, so the
// requirement here is narrower and already satisfied: every case in this
// switch builds zero On*-handler nodes directly (ListPopulated dispatches
// through CreateElement instead of building hook-bearing nodes inline),
// so no case can leave a stray hook behind that another case skips.
//
// wui.StatePanel replaces the previous hand-rolled per-state switch (see
// this file's package doc comment); like render_rail.go's use of it, this
// is safe called inline because none of StatePanel's own branches directly
// register an On* hook in THIS fiber (its error view's retry button is
// never built — OnRetry is always nil here — and its populated view
// delegates entirely to the CreateElement-isolated renderCandidateResults
// below).
func renderCandidateListGate(state ListState, p samplerProps) ui.Node {
	t := deps.I18n
	wt := wui.T(t.T)
	// p.DisabledReason is already-resolved prose, not a key
	// (SampleDisabledReason resolves it and may interpolate a failure
	// count), so it goes through the passthrough key with the text as an
	// ARGUMENT — the same treatment ErrorKey/ErrorArgs get just below.
	//
	// It used to be passed as DisabledReasonKey directly, on the theory that
	// an unrecognised key renders as itself. It does not, quite: the shell's
	// translator prefixes its namespace first, so the panel rendered
	// "generate.This feed is disabled." to the operator. wui.StatePanelProps
	// has had a DisabledReasonArgs field the whole time; the comment that
	// used to sit here claiming otherwise was wrong.
	return wui.StatePanel(wui.StatePanelProps{
		T:                  wt,
		State:              uiListState(state),
		ErrorKey:           "generate.common.errorText",
		ErrorArgs:          []any{mutationErrorText(t, p.SampleErr)},
		EmptyKey:           "generate.sampler.empty",
		ReconnectingKey:    "generate.sampler.disconnected",
		DisabledReasonKey:  "generate.common.errorText",
		DisabledReasonArgs: []any{p.DisabledReason},
		Populated: func() []ui.Node {
			return []ui.Node{ui.CreateElement(renderCandidateResults, p)}
		},
	})
}

func renderCandidateResults(p samplerProps) ui.Node {
	idx := p.SelectedIndex
	if idx < 0 || idx >= len(p.Candidates) {
		idx = 0
	}
	current := p.Candidates[idx]

	return h.Div(
		h.ClassStr("af-sampler__results"),
		// Isolated: candidate count is 0-5 and changes live as the stream
		// delivers more candidates — a genuinely variable-length tab strip
		// (see this file's package doc comment), so it gets its own child
		// fiber rather than being called inline the way the four FIXED
		// view tabs are in renderCandidateDetail below.
		ui.CreateElement(renderCandidateTabStrip, candidateTabsProps{
			T: deps.I18n, Candidates: p.Candidates, Selected: p.SelectedIndex, SetSelected: p.SetSelected,
		}),
		// current's identity/content changes as selection or streaming
		// progresses, but renderCandidateDetail's own hook count never
		// depends on that content (see its doc comment) — no isolation
		// needed beyond it being its own component below, which keeps its
		// hooks off this function's (also hook-free, aside from the
		// isolated candidate-tabs fiber above) fiber.
		ui.CreateElement(renderCandidateDetail, candidateDetailProps{T: deps.I18n, Fmtr: deps.Formatters, Candidate: current, View: p.View, SetView: p.SetView, OnPromote: p.OnPromote, OnDiscard: p.OnDiscard, ActionErr: p.ActionErr}),
	)
}

// candidateTabsProps/renderCandidateTabStrip wrap the per-candidate
// wui.Tabs call in its own fiber (see renderCandidateResults' call site
// comment above for why: 0-5 tabs, changing live).
type candidateTabsProps struct {
	T           Translator
	Candidates  []*affv1.SampleCandidate
	Selected    int
	SetSelected func(int)
}

func renderCandidateTabStrip(p candidateTabsProps) ui.Node {
	wt := wui.T(p.T.T)
	idx := p.Selected
	if idx < 0 || idx >= len(p.Candidates) {
		idx = 0
	}
	activeID := ""
	if len(p.Candidates) > 0 {
		activeID = p.Candidates[idx].GetCandidateId()
	}
	tabs := make([]wui.Tab, len(p.Candidates))
	for i, c := range p.Candidates {
		tabs[i] = wui.Tab{ID: c.GetCandidateId(), LabelKey: candidateTabLabelKey(i), PanelID: "generate-sampler-candidate-panel"}
	}
	candidates := p.Candidates
	setSelected := p.SetSelected
	return wui.Tabs(wui.TabsProps{
		T: wt, ID: "generate-sampler-candidate-tabs", LabelKey: "generate.sampler.candidateTabs.label",
		Tabs: tabs, ActiveID: activeID,
		OnChange: func(id string) {
			for i, c := range candidates {
				if c.GetCandidateId() == id {
					setSelected(i)
					return
				}
			}
		},
	})
}

type candidateDetailProps struct {
	T         Translator
	Fmtr      Formatters
	Candidate *affv1.SampleCandidate
	View      CandidateView
	SetView   func(CandidateView)
	OnPromote func(string)
	OnDiscard func()
	ActionErr error
}

// renderCandidateDetail's hook count is fixed regardless of Candidate's
// content: the view tab strip is wui.Tabs over the constant 4-element
// CandidateViews (never filtered — one UseCompositeNavigation hook plus 4
// always-present per-tab OnClick hooks, every render); the link-verdicts
// block carries no On* handlers at all (h.If just toggles DOM presence of
// plain text/class nodes); and Promote/Discard are two more, always
// present and never Disabled (so web/ui.Button never skips their hook —
// see render_editor.go's package doc comment's third hazard). Total: 7,
// every render, regardless of which candidate or view is selected.
func renderCandidateDetail(p candidateDetailProps) ui.Node {
	t := p.T
	wt := wui.T(t.T)
	fmtr := p.Fmtr
	c := p.Candidate
	failed := FailedLinks(c.GetLinkVerdicts())

	viewTabs := make([]wui.Tab, len(CandidateViews))
	for i, v := range CandidateViews {
		viewTabs[i] = wui.Tab{ID: viewTabID(v), LabelKey: v.TranslationKey(), PanelID: "generate-sampler-view-panel"}
	}
	activeViewID := viewTabID(p.View)
	setView := p.SetView
	viewTabsNode := wui.Tabs(wui.TabsProps{
		T: wt, ID: "generate-sampler-view-tabs", LabelKey: "generate.sampler.viewTabs.label",
		Tabs: viewTabs, ActiveID: activeViewID,
		OnChange: func(id string) { setView(viewFromTabID(id)) },
	})

	return h.Div(
		h.ClassStr("af-candidate"),
		h.Role("tabpanel"),
		h.Aria("labelledby", c.GetCandidateId()),
		h.ID("generate-sampler-candidate-panel"),

		viewTabsNode,
		h.Div(
			h.ID("generate-sampler-view-panel"),
			h.Role("tabpanel"),
			h.Aria("labelledby", activeViewID),
			h.Pre(h.ClassStr("af-candidate__content"), h.Text(CandidateViewContent(p.View, c))),
		),

		h.Div(h.ClassStr("af-candidate__novelty"), h.Text(NoveltySummary(t, deps.Formatters, c.GetNovelty()))),

		h.If(len(c.GetLinkVerdicts()) > 0, h.Div(
			h.ClassStr("af-candidate__links"),
			h.P(h.Text(t.T("generate.sampler.groundedSources"))),
			h.Ul(
				anyNodes(h.MapKeyed(c.GetLinkVerdicts(), func(lv *affv1.LinkVerdict) any { return lv.GetUrl() }, func(lv *affv1.LinkVerdict) ui.Node {
					return h.Li(
						h.ClassStr(h.ClassMap(map[string]bool{"af-link-verdict": true, "af-link-verdict--failed": !lv.GetOk()})),
						h.Text(lv.GetUrl()),
					)
				}))...,
			),
			h.If(len(failed) > 0, h.P(h.ClassStr("af-candidate__links-failed"), h.Text(t.T("generate.common.labelValue", t.T("generate.sampler.failedLinks"), formatCount(len(failed)))))),
		)),

		h.Div(h.ClassStr("af-candidate__cost"),
			h.Text(t.T("generate.sampler.candidateCostDetail", t.T("generate.sampler.candidateCost"), fmtr.Currency(c.GetEstimatedCostUsd()), formatCount(int(c.GetTokensIn())), formatCount(int(c.GetTokensOut())))),
		),

		h.If(p.ActionErr != nil, h.Div(h.ClassStr("af-candidate__action-error"), h.Role("alert"), h.Text(mutationErrorText(t, p.ActionErr)))),
		h.Div(h.ClassStr("af-candidate__actions"),
			wui.Button(wui.ButtonProps{T: wt, ID: "generate-sampler-promote", LabelKey: "generate.sampler.promote", Variant: wui.ButtonPrimary, OnClick: func() { p.OnPromote(c.GetCandidateId()) }}),
			wui.Button(wui.ButtonProps{T: wt, ID: "generate-sampler-discard", LabelKey: "generate.sampler.discard", Variant: wui.ButtonSecondary, OnClick: func() { p.OnDiscard() }}),
		),
	)
}
