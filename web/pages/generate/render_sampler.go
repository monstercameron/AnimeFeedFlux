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
// See render_editor.go's package-level doc comment for this file's hook-
// ordering discipline (hook-free dispatchers + ui.CreateElement for real
// Go-level branches, MapKeyedComponent for any variable-length list whose
// rows carry their own On* handler) — the same two hazards apply here:
// the "no feed selected/saved yet" early state, the six-state candidate
// list switch, and the candidate tab strip (0-5 items, changing live as a
// stream comes in).
package generatepage

import (
	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
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

	estUSD, haveEst := EstimateSampleCostUSD(p.Prices, p.Feed.GetSpec().GetModel(),
		len(p.Feed.GetSpec().GetSystemPromptTemplate())+len(p.Feed.GetSpec().GetUserPromptTemplate()),
		400, p.SampleSize)
	estText := t.T("generate.sampler.estimateUnavailable")
	if haveEst {
		estText = t.T("generate.sampler.estimatedCost", fmtr.Currency(estUSD))
	}

	return h.Section(
		h.ClassStr("af-generate__sampler"),
		h.Div(h.ClassStr("af-sampler__controls"),
			h.Div(h.ClassStr("af-field"),
				h.Label(h.Text(t.T("generate.sampler.size"))),
				h.Input(h.Type("number"), h.Value(intStr(p.SampleSize)), h.OnInput(func(v string) {
					n := int32(parseIntOr(v, int(p.SampleSize)))
					if ValidSampleSize(n) {
						p.SetSampleSize(n)
					}
				})),
			),
			h.Div(h.ClassStr("af-field"),
				h.Label(h.Text(t.T("generate.sampler.temperatureOverride"))),
				h.Input(h.Type("number"), h.Value(floatStr(p.TempOverride)), h.OnInput(func(v string) {
					p.SetTempOverride(parseFloatOr(v, p.TempOverride))
				})),
			),
			h.Div(h.ClassStr("af-sampler__budget"),
				h.Text(estText),
				h.Br(),
				h.Text(t.T("generate.sampler.remainingBudget", fmtr.Currency(p.RemainingBudget))),
			),
			h.If(p.DisabledReason != "", h.Div(
				h.ClassStr("af-sampler__disabled-reason"),
				h.Text(p.DisabledReason),
			)),
			h.Button(
				h.Type("button"),
				h.DisabledIf(!p.Connected || p.Sampling || p.DisabledReason != "" || !ValidSampleSize(p.SampleSize)),
				h.OnClick(func() { p.OnSample() }),
				h.Text(t.T("generate.sampler.sampleButton", estText)),
			),
			// h.If evaluates the Button node eagerly every render (see
			// render_editor.go's doc comment) — this OnClick hook is
			// always registered, only its DOM presence toggles on
			// p.Sampling, which keeps this fiber's hook count fixed.
			h.If(true, h.Button(h.Type("button"), h.DisabledIf(!p.Sampling), h.OnClick(func() { p.OnCancel() }), h.Text(t.T("generate.sampler.cancel")))),
		),

		renderCandidateListGate(state, p),
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
func renderCandidateListGate(state ListState, p samplerProps) ui.Node {
	t := deps.I18n
	switch state {
	case ListDisconnected:
		return h.P(h.ClassStr("af-sampler__status"), h.Text(t.T("generate.sampler.disconnected")))
	case ListDisabledWithReason:
		return h.P(h.ClassStr("af-sampler__status"), h.Text(p.DisabledReason))
	case ListError:
		return h.P(h.ClassStr("af-sampler__status af-sampler__status--error"), h.Textf("%v", p.SampleErr))
	case ListLoading:
		return h.P(h.ClassStr("af-sampler__status"), h.Text(t.T("generate.sampler.streaming")))
	case ListEmpty:
		return h.P(h.ClassStr("af-sampler__status"), h.Text(t.T("generate.sampler.empty")))
	default:
		return ui.CreateElement(renderCandidateResults, p)
	}
}

func renderCandidateResults(p samplerProps) ui.Node {
	idx := p.SelectedIndex
	if idx < 0 || idx >= len(p.Candidates) {
		idx = 0
	}
	current := p.Candidates[idx]

	tabs := make([]candidateTab, len(p.Candidates))
	for i, c := range p.Candidates {
		tabs[i] = candidateTab{Index: i, Candidate: c, Selected: i == idx, OnSelect: p.SetSelected}
	}

	tabsArgs := []any{h.ClassStr("af-sampler__tabs")}
	tabsArgs = append(tabsArgs, anyNodes(h.MapKeyedComponent(tabs, func(tb candidateTab) any { return tb.Candidate.GetCandidateId() }, renderCandidateTab))...)

	return h.Div(
		h.ClassStr("af-sampler__results"),
		h.Div(tabsArgs...),
		// current's identity/content changes as selection or streaming
		// progresses, but renderCandidateDetail's own hook count never
		// depends on that content (fixed 4 view tabs + Promote + Discard;
		// see its doc comment) — no isolation needed beyond it being its
		// own component below, which keeps its hooks off this function's
		// (also hook-free, aside from the isolated MapKeyedComponent
		// rows above) fiber.
		ui.CreateElement(renderCandidateDetail, candidateDetailProps{T: deps.I18n, Fmtr: deps.Formatters, Candidate: current, View: p.View, SetView: p.SetView, OnPromote: p.OnPromote, OnDiscard: p.OnDiscard}),
	)
}

// candidateTab is MapKeyedComponent's per-row item for the tab strip —
// bundling Index/Selected/OnSelect this way (rather than a shared loop
// variable) is what makes each tab's own OnClick hook see the right
// index once it lands in its own isolated fiber.
type candidateTab struct {
	Index     int
	Candidate *affv1.SampleCandidate
	Selected  bool
	OnSelect  func(int)
}

func renderCandidateTab(tb candidateTab) ui.Node {
	return h.Button(
		h.Type("button"),
		h.ClassStr(h.ClassMap(map[string]bool{"af-sampler__tab": true, "af-sampler__tab--active": tb.Selected})),
		h.OnClick(func() { tb.OnSelect(tb.Index) }),
		h.Textf("%d", tb.Index+1),
	)
}

type candidateDetailProps struct {
	T         Translator
	Fmtr      Formatters
	Candidate *affv1.SampleCandidate
	View      CandidateView
	SetView   func(CandidateView)
	OnPromote func(string)
	OnDiscard func()
}

// renderCandidateDetail's hook count is fixed regardless of Candidate's
// content: CandidateViews is a constant 4-element slice (never filtered),
// so its per-view tab loop always registers exactly 4 OnClick hooks in
// the same order; the link-verdicts block carries no On* handlers at all
// (h.If just toggles DOM presence of plain text/class nodes); and Promote
// /Discard are two more, always present. Total: 6, every render,
// regardless of which candidate or view is selected.
func renderCandidateDetail(p candidateDetailProps) ui.Node {
	t := p.T
	fmtr := p.Fmtr
	c := p.Candidate
	failed := FailedLinks(c.GetLinkVerdicts())

	viewTabsArgs := []any{h.ClassStr("af-candidate__view-tabs")}
	viewTabsArgs = append(viewTabsArgs, anyNodes(h.MapKeyed(CandidateViews, func(v CandidateView) any { return v }, func(v CandidateView) ui.Node {
		return h.Button(
			h.Type("button"),
			h.ClassStr(h.ClassMap(map[string]bool{"af-candidate__view-tab": true, "af-candidate__view-tab--active": v == p.View})),
			h.OnClick(func() { p.SetView(v) }),
			h.Text(t.T(v.TranslationKey())),
		)
	}))...)

	return h.Div(
		h.ClassStr("af-candidate"),
		h.Div(viewTabsArgs...),
		h.Pre(h.ClassStr("af-candidate__content"), h.Text(CandidateViewContent(p.View, c))),

		h.Div(h.ClassStr("af-candidate__novelty"), h.Text(NoveltySummary(t, c.GetNovelty()))),

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
			h.If(len(failed) > 0, h.P(h.ClassStr("af-candidate__links-failed"), h.Textf("%s: %d", t.T("generate.sampler.failedLinks"), len(failed)))),
		)),

		h.Div(h.ClassStr("af-candidate__cost"),
			h.Textf("%s: %s (tokens in=%d out=%d)", t.T("generate.sampler.candidateCost"), fmtr.Currency(c.GetEstimatedCostUsd()), c.GetTokensIn(), c.GetTokensOut()),
		),

		h.Div(h.ClassStr("af-candidate__actions"),
			h.Button(h.Type("button"), h.OnClick(func() { p.OnPromote(c.GetCandidateId()) }), h.Text(t.T("generate.sampler.promote"))),
			h.Button(h.Type("button"), h.OnClick(func() { p.OnDiscard() }), h.Text(t.T("generate.sampler.discard"))),
		),
	)
}
