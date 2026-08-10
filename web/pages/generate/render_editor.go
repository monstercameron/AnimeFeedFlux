//go:build js && wasm

// render_editor.go implements the middle editor pane (PLAN.md §12.3,
// TODOS.md D2-06..16): slug/title/description/language/kind, cron+
// timezone with a plain-English readback, model/params, prompt templates
// with the variable list inline, novelty settings, budgets, grounded
// sources, field-scoped validation errors, and the conflict-resolution UI.
//
// # Hook-ordering discipline in this file
//
// GWC's On* handlers (h.OnClick/h.OnInput/h.OnChange) each consume one
// positional hook slot in whatever fiber is rendering when they are
// built (html/sugar.go's toHandler -> ui.UseEvent -> internal/runtime's
// GoUseFunc, confirmed by reading the pinned module directly — every
// GoUseFunc call does `hooks.index++` unconditionally). That means two
// real hazards this file has to design around, not just avoid by
// accident:
//
//  1. A genuine Go `if`/`switch` that skips building a hook-bearing
//     subtree entirely on some renders (as opposed to h.If/h.Show, which
//     evaluate their node argument eagerly every render and only decide
//     DOM presence) changes the hook count for whatever fiber contains
//     it. renderEditor's nil-vs-populated Draft split is exactly this
//     shape, so it is resolved by dispatching to two different child
//     components via ui.CreateElement — each gets its own fiber, so
//     which one is mounted this render does not disturb the other's (or
//     the parent's) hook sequence.
//  2. A variable-length list whose per-item render function itself calls
//     an On* handler must use html/shorthand.MapKeyedComponent (which
//     wraps each item in its own ui.CreateElement'd fiber), never the
//     plain MapKeyed/MapKeyedIndexed, or adding/removing a row shifts
//     every hook slot after it in the CALLING fiber. The grounded-sources
//     list and the conflict per-field list both do this.
package generatepage

import (
	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

type editorProps struct {
	Connected      bool
	Draft          *affv1.Feed
	Loaded         *affv1.Feed
	IsNew          bool
	FieldErrs      FieldErrors
	Saving         bool
	SaveErr        error
	ConflictTheirs *affv1.Feed
	Resolution     ConflictResolution

	OnFieldChange func(mutate func(*affv1.Feed))
	OnValidate    func()
	OnSave        func()

	OnResolveConflict func(resolution ConflictResolution, keepMine map[string]bool)
	SetResolution     func(ConflictResolution)
	PerFieldKeepMine  map[string]bool
	SetPerFieldChoice func(map[string]bool)
}

// renderEditor is a props-only dispatcher with NO hooks of its own (it
// calls neither an On* handler nor any Use* function directly) — only
// ui.CreateElement, which just builds a vnode and consumes no hook slot.
// That makes it safe for the two branches below to have wildly different
// hook counts: each mounts as its own child fiber, so switching between
// them (e.g. the operator selects a feed) never touches this function's
// own (nonexistent) hook sequence, and each child manages its own
// sequence independently.
func renderEditor(p editorProps) ui.Node {
	if p.Draft == nil {
		return ui.CreateElement(renderEditorEmpty, editorEmptyProps{})
	}
	return ui.CreateElement(renderEditorForm, p)
}

type editorEmptyProps struct{}

func renderEditorEmpty(editorEmptyProps) ui.Node {
	return h.Section(h.ClassStr("af-generate__editor"), h.P(h.Text(deps.I18n.T("generate.editor.noSelection"))))
}

func renderEditorForm(p editorProps) ui.Node {
	t := deps.I18n
	d := p.Draft
	spec := d.GetSpec()
	if spec == nil {
		spec = &affv1.FeedSpec{}
	}

	slugEditable := SlugEditable(d)

	fieldError := func(key string) ui.Node {
		msg, ok := p.FieldErrs.For(key)
		return h.If(ok, h.Div(h.ClassStr("af-field-error"), h.Text(msg)))
	}

	return h.Section(
		h.ClassStr("af-generate__editor"),
		// renderConflictGate is always invoked (h.If evaluates its node
		// argument eagerly) so its own hook count is fixed every render;
		// it is itself hook-free (see its doc comment) and dispatches to
		// an isolated child fiber only when ConflictTheirs != nil.
		renderConflictGate(t, p),

		h.Div(h.ClassStr("af-field"),
			h.Label(h.Text(t.T("generate.editor.slug"))),
			h.Input(h.Value(d.GetSlug()), h.DisabledIf(!slugEditable),
				h.OnInput(func(v string) { p.OnFieldChange(func(f *affv1.Feed) { f.Slug = v }) })),
			h.If(!slugEditable, h.Div(h.ClassStr("af-field-hint"), h.Text(SlugImmutableReason(t)))),
			fieldError("slug"),
		),
		h.Div(h.ClassStr("af-field"),
			h.Label(h.Text(t.T("generate.editor.title"))),
			h.Input(h.Value(d.GetTitle()), h.OnInput(func(v string) { p.OnFieldChange(func(f *affv1.Feed) { f.Title = v }) })),
			fieldError("title"),
		),
		h.Div(h.ClassStr("af-field"),
			h.Label(h.Text(t.T("generate.editor.description"))),
			h.Textarea(h.Value(d.GetDescription()), h.OnInput(func(v string) { p.OnFieldChange(func(f *affv1.Feed) { f.Description = v }) })),
			fieldError("description"),
		),
		h.Div(h.ClassStr("af-field"),
			h.Label(h.Text(t.T("generate.editor.language"))),
			h.Input(h.Value(d.GetLanguage()), h.OnInput(func(v string) { p.OnFieldChange(func(f *affv1.Feed) { f.Language = v }) })),
			fieldError("language"),
		),
		h.Div(h.ClassStr("af-field"),
			h.Label(h.Text(t.T("generate.editor.kind"))),
			renderKindSelect(t, d, p),
			fieldError("kind"),
		),

		h.Fieldset(
			h.Legend(h.Text(t.T("generate.editor.schedule"))),
			h.Div(h.ClassStr("af-field"),
				h.Label(h.Text(t.T("generate.editor.cron"))),
				h.Input(h.Value(spec.GetCron()), h.OnInput(func(v string) {
					p.OnFieldChange(func(f *affv1.Feed) { ensureSpec(f).Cron = v })
				})),
				fieldError("cron"),
			),
			h.Div(h.ClassStr("af-field"),
				h.Label(h.Text(t.T("generate.editor.timezone"))),
				h.Input(h.Value(spec.GetTimezone()), h.OnInput(func(v string) {
					p.OnFieldChange(func(f *affv1.Feed) { ensureSpec(f).Timezone = v })
				})),
				fieldError("timezone"),
			),
			h.P(h.ClassStr("af-field-hint"), h.Text(CronReadback(t, spec.GetCron(), spec.GetTimezone()))),
			// D2-09: the next three runs, jittered — see render_rail.go's
			// matching comment and logic.go's JitteredRuns doc comment for
			// why this pane cannot compute them today (no nominal
			// next-fire-time is available from any RPC in proto/aff/v1).
			h.P(h.ClassStr("af-field-hint"), h.Text(t.T("generate.editor.nextRunsUnavailable"))),
		),

		h.Fieldset(
			h.Legend(h.Text(t.T("generate.editor.modelParams"))),
			h.Div(h.ClassStr("af-field"),
				h.Label(h.Text(t.T("generate.editor.model"))),
				h.Input(h.Value(spec.GetModel()), h.OnInput(func(v string) {
					p.OnFieldChange(func(f *affv1.Feed) { ensureSpec(f).Model = v })
				})),
				fieldError("model"),
			),
			h.Div(h.ClassStr("af-field"),
				h.Label(h.Text(t.T("generate.editor.temperature"))),
				h.Input(h.Type("number"), h.Value(floatStr(spec.GetTemperature())), h.OnInput(func(v string) {
					p.OnFieldChange(func(f *affv1.Feed) { ensureSpec(f).Temperature = parseFloatOr(v, spec.GetTemperature()) })
				})),
				fieldError("temperature"),
			),
			h.Div(h.ClassStr("af-field"),
				h.Label(h.Text(t.T("generate.editor.itemsPerRun"))),
				h.Input(h.Type("number"), h.Value(intStr(spec.GetItemsPerRun())), h.OnInput(func(v string) {
					p.OnFieldChange(func(f *affv1.Feed) { ensureSpec(f).ItemsPerRun = int32(parseIntOr(v, int(spec.GetItemsPerRun()))) })
				})),
				fieldError("items_per_run"),
			),
			h.Div(h.ClassStr("af-field"),
				h.Label(h.Text(t.T("generate.editor.feedWindow"))),
				h.Input(h.Type("number"), h.Value(intStr(spec.GetFeedWindow())), h.OnInput(func(v string) {
					p.OnFieldChange(func(f *affv1.Feed) { ensureSpec(f).FeedWindow = int32(parseIntOr(v, int(spec.GetFeedWindow()))) })
				})),
				fieldError("feed_window"),
			),
		),

		h.Fieldset(
			h.Legend(h.Text(t.T("generate.editor.prompts"))),
			h.P(h.ClassStr("af-field-hint"), h.Text(t.T("generate.editor.promptVariablesHint"))),
			h.Ul(h.ClassStr("af-prompt-vars"),
				h.Li(h.Text("{{.Today}}")), h.Li(h.Text("{{.Weekday}}")), h.Li(h.Text("{{.Season}}")),
				h.Li(h.Text("{{.FeedTitle}}")), h.Li(h.Text("{{.ItemsPerRun}}")),
				h.Li(h.Text("{{.RecentTitles}}")), h.Li(h.Text("{{.Candidates}}")),
			),
			h.Div(h.ClassStr("af-field"),
				h.Label(h.Text(t.T("generate.editor.systemPrompt"))),
				h.Textarea(h.Value(spec.GetSystemPromptTemplate()), h.OnInput(func(v string) {
					p.OnFieldChange(func(f *affv1.Feed) { ensureSpec(f).SystemPromptTemplate = v })
				})),
				fieldError("system_prompt_template"),
			),
			h.Div(h.ClassStr("af-field"),
				h.Label(h.Text(t.T("generate.editor.userPrompt"))),
				h.Textarea(h.Value(spec.GetUserPromptTemplate()), h.OnInput(func(v string) {
					p.OnFieldChange(func(f *affv1.Feed) { ensureSpec(f).UserPromptTemplate = v })
				})),
				fieldError("user_prompt_template"),
			),
		),

		h.Fieldset(
			h.Legend(h.Text(t.T("generate.editor.noveltyAndBudgets"))),
			h.Div(h.ClassStr("af-field"),
				h.Label(h.Text(t.T("generate.editor.noveltyThreshold"))),
				h.Input(h.Type("number"), h.Value(floatStr(spec.GetNovelty().GetSimilarityThreshold())), h.OnInput(func(v string) {
					p.OnFieldChange(func(f *affv1.Feed) {
						sp := ensureSpec(f)
						if sp.Novelty == nil {
							sp.Novelty = &affv1.NoveltySettings{}
						}
						sp.Novelty.SimilarityThreshold = parseFloatOr(v, sp.Novelty.SimilarityThreshold)
					})
				})),
				fieldError("novelty.similarity_threshold"),
			),
			h.Div(h.ClassStr("af-field"),
				h.Label(h.Text(t.T("generate.editor.dailyTokenBudget"))),
				h.Input(h.Type("number"), h.Value(int64Str(spec.GetDailyTokenBudget())), h.OnInput(func(v string) {
					p.OnFieldChange(func(f *affv1.Feed) {
						ensureSpec(f).DailyTokenBudget = int64(parseIntOr(v, int(spec.GetDailyTokenBudget())))
					})
				})),
				fieldError("daily_token_budget"),
			),
			h.Div(h.ClassStr("af-field"),
				h.Label(h.Text(t.T("generate.editor.dailyRunBudget"))),
				h.Input(h.Type("number"), h.Value(intStr(spec.GetDailyRunBudget())), h.OnInput(func(v string) {
					p.OnFieldChange(func(f *affv1.Feed) {
						ensureSpec(f).DailyRunBudget = int32(parseIntOr(v, int(spec.GetDailyRunBudget())))
					})
				})),
				fieldError("daily_run_budget"),
			),
		),

		// renderSourcesGate: same "always call, hook-free dispatcher"
		// shape as renderConflictGate — always invoked (fieldset always
		// present structurally), isolates the grounded-only content into
		// its own fiber only when the kind is actually GROUNDED.
		renderSourcesGate(t, d, p, spec),

		h.Div(h.ClassStr("af-editor__actions"),
			h.If(p.SaveErr != nil, h.Div(h.ClassStr("af-form-error"), h.Textf("%v", p.SaveErr))),
			h.Button(h.Type("button"), h.DisabledIf(!p.Connected), h.OnClick(func() { p.OnValidate() }),
				h.Text(t.T("generate.editor.validate"))),
			h.Button(h.Type("button"), h.DisabledIf(!p.Connected || p.Saving || p.FieldErrs.HasAny()), h.OnClick(func() { p.OnSave() }),
				h.Text(t.T("generate.editor.save"))),
		),
	)
}

// renderKindSelect's OnChange is a single, always-present hook (the
// Select element itself never appears/disappears), so no isolation is
// needed here; the per-option h.Option nodes carry no On* handlers of
// their own (only Value/AttrIf, neither of which is a hook), so
// html/shorthand.MapKeyed (not the *Component variant) is the correct,
// cheaper choice for this particular list.
func renderKindSelect(t Translator, d *affv1.Feed, p editorProps) ui.Node {
	kinds := []affv1.FeedKind{affv1.FeedKind_FEED_KIND_GENERATIVE, affv1.FeedKind_FEED_KIND_GROUNDED, affv1.FeedKind_FEED_KIND_AGGREGATE}
	// Go forbids mixing explicit arguments with a trailing `slice...`
	// spread against a single `...any` parameter (the call must be either
	// all individual values or exactly one spread slice) — so the
	// argument list is assembled by hand instead of written as a mixed
	// call. Every h.Xxx(fixedArgs..., mappedNodes...) call in this
	// package follows the same shape for the same reason.
	selectArgs := []any{
		h.OnChange(func(v string) {
			p.OnFieldChange(func(f *affv1.Feed) { f.Kind = kindFromString(v) })
		}),
	}
	selectArgs = append(selectArgs, anyNodes(h.MapKeyed(kinds, func(k affv1.FeedKind) any { return k }, func(k affv1.FeedKind) ui.Node {
		return h.Option(h.Value(k.String()), h.AttrIf(d.GetKind() == k, "selected", "selected"), h.Text(t.T(kindKey(k))))
	}))...)
	return h.Select(selectArgs...)
}

func kindFromString(v string) affv1.FeedKind {
	if n, ok := affv1.FeedKind_value[v]; ok {
		return affv1.FeedKind(n)
	}
	return affv1.FeedKind_FEED_KIND_UNSPECIFIED
}

func kindKey(k affv1.FeedKind) string {
	switch k {
	case affv1.FeedKind_FEED_KIND_GENERATIVE:
		return "generate.editor.kind.generative"
	case affv1.FeedKind_FEED_KIND_GROUNDED:
		return "generate.editor.kind.grounded"
	case affv1.FeedKind_FEED_KIND_AGGREGATE:
		return "generate.editor.kind.aggregate"
	default:
		return "generate.editor.kind.unspecified"
	}
}

// renderSourcesGate has no hooks of its own — only ui.CreateElement — so
// it may be called on every render (kind flips between GROUNDED and not)
// without disturbing renderEditorForm's own hook sequence.
func renderSourcesGate(t Translator, d *affv1.Feed, p editorProps, spec *affv1.FeedSpec) ui.Node {
	if d.GetKind() != affv1.FeedKind_FEED_KIND_GROUNDED {
		return h.Fragment()
	}
	return ui.CreateElement(renderSources, sourcesProps{T: t, Draft: p.Draft, FieldErrs: p.FieldErrs, OnFieldChange: p.OnFieldChange, Sources: spec.GetSources()})
}

type sourcesProps struct {
	T             Translator
	Draft         *affv1.Feed
	FieldErrs     FieldErrors
	OnFieldChange func(func(*affv1.Feed))
	Sources       []*affv1.SourceSpec
}

// sourceRow bundles one source with its position so each row's own
// component (via MapKeyedComponent) can mutate the right slice slot
// without capturing a shared loop variable.
type sourceRow struct {
	Index  int
	Source *affv1.SourceSpec
}

func renderSources(p sourcesProps) ui.Node {
	t := p.T
	rows := make([]sourceRow, len(p.Sources))
	for i, s := range p.Sources {
		rows[i] = sourceRow{Index: i, Source: s}
	}
	sourceListArgs := []any{h.ClassStr("af-sources")}
	sourceListArgs = append(sourceListArgs, anyNodes(h.MapKeyedComponent(rows, func(r sourceRow) any { return r.Index }, func(r sourceRow) ui.Node {
		return renderSourceRow(t, p, r)
	}))...)
	return h.Fieldset(
		h.Legend(h.Text(t.T("generate.editor.sources"))),
		h.Ul(sourceListArgs...),
		h.Button(h.Type("button"), h.OnClick(func() {
			p.OnFieldChange(func(f *affv1.Feed) {
				sp := ensureSpec(f)
				sp.Sources = append(sp.Sources, &affv1.SourceSpec{})
			})
		}), h.Text(t.T("generate.editor.addSource"))),
	)
}

// renderSourceRow is the per-item render func MapKeyedComponent mounts as
// its OWN fiber (see mapItemComponent in the pinned module's html/sugar.go)
// — so its two OnInput and one OnClick hooks are isolated from every other
// row and from renderSources' own (hook-free) fiber, and adding/removing a
// row cannot shift anyone else's hook slots.
func renderSourceRow(t Translator, p sourcesProps, r sourceRow) ui.Node {
	i := r.Index
	msg, hasErr := p.FieldErrs.For(sourceFieldKey(i))
	return h.Li(
		h.Input(h.Value(r.Source.GetUrl()), h.OnInput(func(v string) {
			p.OnFieldChange(func(f *affv1.Feed) { ensureSpec(f).Sources[i].Url = v })
		})),
		h.Input(h.Value(r.Source.GetKind()), h.Placeholder(t.T("generate.editor.sourceKindPlaceholder")), h.OnInput(func(v string) {
			p.OnFieldChange(func(f *affv1.Feed) { ensureSpec(f).Sources[i].Kind = v })
		})),
		h.Button(h.Type("button"), h.OnClick(func() {
			p.OnFieldChange(func(f *affv1.Feed) {
				sp := ensureSpec(f)
				sp.Sources = append(sp.Sources[:i], sp.Sources[i+1:]...)
			})
		}), h.Text(t.T("generate.editor.removeSource"))),
		h.If(hasErr, h.Div(h.ClassStr("af-field-error"), h.Text(msg))),
	)
}

func sourceFieldKey(i int) string {
	return "sources[" + intStr(int32(i)) + "].url"
}

// renderConflictGate mirrors renderSourcesGate's "hook-free dispatcher"
// shape: ConflictTheirs flips nil/non-nil as saves succeed or collide, so
// the actual conflict UI (with its OnClick handlers) lives in its own
// child fiber, mounted only while a conflict is live.
func renderConflictGate(t Translator, p editorProps) ui.Node {
	if p.ConflictTheirs == nil {
		return h.Fragment()
	}
	return ui.CreateElement(renderConflict, p)
}

func renderConflict(p editorProps) ui.Node {
	t := deps.I18n
	diffs := DiffFeedFields(p.Draft, p.ConflictTheirs)
	return h.Div(
		h.ClassStr("af-conflict"),
		h.P(h.ClassStr("af-conflict__headline"), h.Text(t.T("generate.editor.conflict.headline"))),
		h.Div(h.ClassStr("af-conflict__choices"),
			h.Button(h.Type("button"), h.OnClick(func() { p.OnResolveConflict(ResolveKeepMine, nil) }),
				h.Text(t.T("generate.editor.conflict.keepMine"))),
			h.Button(h.Type("button"), h.OnClick(func() { p.OnResolveConflict(ResolveTakeTheirs, nil) }),
				h.Text(t.T("generate.editor.conflict.takeTheirs"))),
		),
		// renderConflictPerFieldGate is itself hook-free; safe to call
		// whether or not diffs is empty.
		renderConflictPerFieldGate(t, p, diffs),
	)
}

func renderConflictPerFieldGate(t Translator, p editorProps, diffs []FieldDiff) ui.Node {
	if len(diffs) == 0 {
		return h.Fragment()
	}
	return ui.CreateElement(renderConflictPerField, conflictPerFieldProps{T: t, P: p, Diffs: diffs})
}

type conflictPerFieldProps struct {
	T     Translator
	P     editorProps
	Diffs []FieldDiff
}

func renderConflictPerField(props conflictPerFieldProps) ui.Node {
	t := props.T
	p := props.P
	rowsArgs := []any{h.ClassStr("af-conflict__rows")}
	rowsArgs = append(rowsArgs, anyNodes(h.MapKeyedComponent(props.Diffs, func(d FieldDiff) any { return d.FieldKey }, func(d FieldDiff) ui.Node {
		return renderConflictFieldRow(t, p, d)
	}))...)
	return h.Div(
		h.ClassStr("af-conflict__fields"),
		h.P(h.Text(t.T("generate.editor.conflict.perFieldHint"))),
		h.Div(rowsArgs...),
		h.Button(h.Type("button"), h.OnClick(func() {
			p.OnResolveConflict(ResolvePerField, p.PerFieldKeepMine)
		}), h.Text(t.T("generate.editor.conflict.applyPerField"))),
	)
}

// renderConflictFieldRow runs in its own MapKeyedComponent-provided fiber
// (see renderSourceRow's doc comment for why that matters).
func renderConflictFieldRow(t Translator, p editorProps, d FieldDiff) ui.Node {
	return h.Div(
		h.ClassStr("af-conflict__field"),
		h.Div(h.Text(d.FieldKey)),
		h.Div(h.Textf("%s: %s", t.T("generate.editor.conflict.mine"), d.Mine)),
		h.Div(h.Textf("%s: %s", t.T("generate.editor.conflict.theirs"), d.Theirs)),
		h.Button(h.Type("button"), h.OnClick(func() {
			choice := cloneBoolMap(p.PerFieldKeepMine)
			choice[d.FieldKey] = true
			p.SetPerFieldChoice(choice)
		}), h.Text(t.T("generate.editor.conflict.keepMineField"))),
	)
}

func cloneBoolMap(m map[string]bool) map[string]bool {
	out := make(map[string]bool, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}

func ensureSpec(f *affv1.Feed) *affv1.FeedSpec {
	if f.Spec == nil {
		f.Spec = &affv1.FeedSpec{}
	}
	return f.Spec
}
