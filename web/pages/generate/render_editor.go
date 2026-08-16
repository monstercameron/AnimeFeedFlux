//go:build js && wasm

// render_editor.go implements the middle editor pane (PLAN.md §12.3,
// TODOS.md D2-06..16): slug/title/description/language/kind, cron+
// timezone with a plain-English readback, model/params, prompt templates
// with the variable list inline, novelty settings, budgets, grounded
// sources, field-scoped validation errors, and the conflict-resolution UI.
//
// # Adopted from web/ui (this task)
//
// Every single-line field (slug/title/language/cron/timezone/model/
// temperature/itemsPerRun/feedWindow/noveltyThreshold/budgets/source url
// and kind) is wui.Input or wui.Select instead of a hand-rolled h.Input —
// see fieldErrorKV below for how a server FieldError message (already
// resolved text, not a key) is threaded through wui.Input's key+args
// ErrorKey/ErrorArgs slot via the existing generate.common.errorText
// passthrough key. description/systemPrompt/userPrompt stay hand-rolled
// h.Textarea: web/ui has no multi-line-text primitive (a real gap for this
// page — see this task's final report). Buttons are wui.Button throughout.
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
//
// A THIRD hazard this task's adoption of web/ui surfaced, checked rather
// than assumed (verified by reading web/ui/input.go, select.go, button.go,
// toggle.go directly): wui.Input/Select/Button/Toggle each skip
// registering their On* hook ENTIRELY when Disabled (or Busy, for Button)
// is true — `if p.OnChange != nil && !p.Disabled { ... }` — unlike this
// page's own hand-rolled convention (render_sampler.go's original comment:
// "DisabledIf... only its DOM presence toggles, [OnClick] is always
// registered"). That means any wui field/button whose Disabled value can
// legitimately flip between renders of the SAME fiber is a hook-count
// hazard exactly like #2 above, even though it is not a list. Only the
// slug field (SlugEditable can flip as the operator selects a different
// feed into the SAME mounted editor fiber) and the Validate/Save actions
// (Disabled/Busy track Connected/Saving/FieldErrs) have a Disabled that
// varies here; both are isolated into their own child fiber below
// (renderSlugField, renderEditorActions) so that variability can never
// shift a sibling's hook slot. Every other field/button on this page has
// a permanently-false Disabled, so no isolation is needed for them.
package generatepage

import (
	"time"

	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	wui "github.com/monstercameron/AnimeFeedFlux/web/ui"
)

type editorProps struct {
	Connected bool
	Draft     *affv1.Feed
	Loaded    *affv1.Feed
	IsNew     bool
	FieldErrs FieldErrors
	Saving    bool
	SaveErr   error
	// ValidateErr is a transport/server failure from Validate itself
	// (TODOS.md D0-10) — distinct from FieldErrs, which is Validate's
	// normal, successful response reporting per-field problems.
	ValidateErr    error
	ConflictTheirs *affv1.Feed

	// Models/ModelsUnavailable/ModelsReason feed the Model field's menu —
	// the provider's own list, so the value that every scheduled run uses is
	// chosen rather than typed. See modelpicker.go.
	Models            []*affv1.ProviderModel
	ModelsUnavailable bool
	ModelsReason      string
	Resolution        ConflictResolution

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

// renderScheduleSection is the feed's scheduler as a FIRST-CLASS section of
// the main column — always visible for a loaded feed, never inside the
// collapsed recipe drawer. Lifted out on 2026-08-15 after the operator
// could not find it there ("the scheduling needs to be user configurable"):
// the one control that decides WHEN a feed does anything was two
// interactions deep, below the fold, behind a disclosure.
//
// It owns the cron-escape-hatch reveal state for the same reason the old
// call site did (toggling must not remount the schedule component), and the
// hook runs unconditionally before the no-draft branch per the positional
// hook rule this package documents throughout.
func renderScheduleSection(p editorProps) ui.Node {
	showCron := ui.UseState(false)
	if p.Draft == nil {
		return h.Fragment()
	}
	spec := p.Draft.GetSpec()
	if spec == nil {
		spec = &affv1.FeedSpec{}
	}
	cronErrKey, cronErrArgs := fieldErrorKV(p.FieldErrs, "cron")
	tzErrKey, tzErrArgs := fieldErrorKV(p.FieldErrs, "timezone")
	return h.Section(
		h.ClassStr("af-gen__schedule"),
		ui.CreateElement(renderSchedule, scheduleProps{
			T: deps.I18n, Spec: spec, Now: time.Now(), OnChange: p.OnFieldChange,
			ShowCron: showCron.Get(), OnToggle: func(v bool) { showCron.Set(v) },
			CronErrKey: cronErrKey, CronErr: cronErrArgs,
			TZErrKey: tzErrKey, TZErr: tzErrArgs,
		}),
	)
}

// fieldErrorKV adapts one FieldErrors lookup (a server-supplied, already
// resolved message string — MapFieldErrors in logic.go copies
// FieldError.Message verbatim, it is not a catalogue key) into the
// key+args pair wui.Input/wui.Select's ErrorKey/ErrorArgs expect: the
// existing generate.common.errorText passthrough key ("{arg1}") with the
// message as its one argument, the same pattern errors.go's
// mutationErrorText already uses for a resolved error string. Returns
// ("", nil) when the field has no error, so a plain field call site can
// pass the result straight through without a branch of its own.
func fieldErrorKV(fe FieldErrors, key string) (string, []any) {
	msg, ok := fe.For(key)
	if !ok {
		return "", nil
	}
	return "generate.common.errorText", []any{msg}
}

func renderEditorForm(p editorProps) ui.Node {
	t := deps.I18n
	wt := wui.T(t.T)
	d := p.Draft
	spec := d.GetSpec()
	if spec == nil {
		spec = &affv1.FeedSpec{}
	}

	// fieldError still backs the three fields with no wui.Input equivalent
	// (description/systemPrompt/userPrompt stay h.Textarea — web/ui has no
	// multi-line primitive; see this file's package doc comment).
	fieldError := func(key string) ui.Node {
		msg, ok := p.FieldErrs.For(key)
		return h.If(ok, h.Div(h.ClassStr("af-field-error"), h.Text(msg)))
	}

	titleErrKey, titleErrArgs := fieldErrorKV(p.FieldErrs, "title")
	langErrKey, langErrArgs := fieldErrorKV(p.FieldErrs, "language")
	tempErrKey, tempErrArgs := fieldErrorKV(p.FieldErrs, "temperature")
	itemsErrKey, itemsErrArgs := fieldErrorKV(p.FieldErrs, "items_per_run")
	windowErrKey, windowErrArgs := fieldErrorKV(p.FieldErrs, "feed_window")
	noveltyErrKey, noveltyErrArgs := fieldErrorKV(p.FieldErrs, "novelty.similarity_threshold")
	tokenBudgetErrKey, tokenBudgetErrArgs := fieldErrorKV(p.FieldErrs, "daily_token_budget")
	runBudgetErrKey, runBudgetErrArgs := fieldErrorKV(p.FieldErrs, "daily_run_budget")
	slugErrKey, slugErrArgs := fieldErrorKV(p.FieldErrs, "slug")

	onFieldChange := p.OnFieldChange

	return h.Section(
		h.ClassStr("af-generate__editor"),
		// renderConflictGate is always invoked (h.If evaluates its node
		// argument eagerly) so its own hook count is fixed every render;
		// it is itself hook-free (see its doc comment) and dispatches to
		// an isolated child fiber only when ConflictTheirs != nil.
		renderConflictGate(t, p),

		// Isolated: SlugEditable can flip between renders of this same
		// fiber (a different selected feed loads in) — see this file's
		// package doc comment's third hazard.
		ui.CreateElement(renderSlugField, slugFieldProps{
			T: t, Value: d.GetSlug(), Editable: SlugEditable(d),
			ErrorKey: slugErrKey, ErrorArgs: slugErrArgs, OnFieldChange: onFieldChange,
		}),

		wui.Input(wui.InputProps{
			T: wt, ID: "generate-editor-title", LabelKey: "generate.editor.title", Value: d.GetTitle(),
			ErrorKey: titleErrKey, ErrorArgs: titleErrArgs,
			OnChange: func(v string) { onFieldChange(func(f *affv1.Feed) { f.Title = v }) },
		}),
		h.Div(h.ClassStr("af-field"),
			// for/id pairing, not a bare <label>: without it the field has no
			// accessible name at all, unlike every wui.Input on this page.
			h.Label(h.For("generate-editor-description"), h.Text(t.T("generate.editor.description"))),
			h.Textarea(h.ID("generate-editor-description"), h.Value(d.GetDescription()), h.OnInput(func(v string) { onFieldChange(func(f *affv1.Feed) { f.Description = v }) })),
			fieldError("description"),
		),
		wui.Input(wui.InputProps{
			T: wt, ID: "generate-editor-language", LabelKey: "generate.editor.language", Value: d.GetLanguage(),
			ErrorKey: langErrKey, ErrorArgs: langErrArgs,
			OnChange: func(v string) { onFieldChange(func(f *affv1.Feed) { f.Language = v }) },
		}),
		renderKindSelect(t, d, p),

		// The schedule builder is NOT here anymore: it is a first-class,
		// always-visible section of the main column
		// (renderScheduleSection, mounted by render.go), lifted out of this
		// drawer on 2026-08-15 after the operator could not find it —
		// "the scheduling needs to be user configurable" — two interactions
		// deep inside a collapsed disclosure.

		h.Fieldset(
			h.Legend(h.Text(t.T("generate.editor.modelParams"))),
			// A menu, not a text box. This is the field that gets SAVED onto
			// the feed and used by every scheduled run, so it is the one where
			// a typo costs the most: §8 classifies "model not found" as a
			// recipe-scoped Fatal that disables the feed, and a typo looks
			// exactly like a deprecation until a run fails at 4am. It degrades
			// to the old text input when the provider cannot be reached — see
			// modelpicker.go.
			h.Div(h.ClassStr("af-field"),
				h.Label(h.For("generate-editor-model"), h.Text(t.T("generate.editor.model"))),
				renderModelPicker(modelPickerProps{
					ID:          "generate-editor-model",
					Class:       "af-editor__model",
					LabelKey:    "generate.editor.model",
					Value:       spec.GetModel(),
					OnChange:    func(v string) { onFieldChange(func(f *affv1.Feed) { ensureSpec(f).Model = v }) },
					Models:      p.Models,
					Unavailable: p.ModelsUnavailable,
					Reason:      p.ModelsReason,
				}),
				fieldError("model"),
			),
			wui.Input(wui.InputProps{
				T: wt, ID: "generate-editor-temperature", LabelKey: "generate.editor.temperature", Type: "number",
				Value: floatStr(spec.GetTemperature()), ErrorKey: tempErrKey, ErrorArgs: tempErrArgs,
				OnChange: func(v string) {
					onFieldChange(func(f *affv1.Feed) { ensureSpec(f).Temperature = parseFloatOr(v, spec.GetTemperature()) })
				},
			}),
			wui.Input(wui.InputProps{
				T: wt, ID: "generate-editor-items-per-run", LabelKey: "generate.editor.itemsPerRun", Type: "number",
				Value: intStr(spec.GetItemsPerRun()), ErrorKey: itemsErrKey, ErrorArgs: itemsErrArgs,
				OnChange: func(v string) {
					onFieldChange(func(f *affv1.Feed) { ensureSpec(f).ItemsPerRun = int32(parseIntOr(v, int(spec.GetItemsPerRun()))) })
				},
			}),
			wui.Input(wui.InputProps{
				T: wt, ID: "generate-editor-feed-window", LabelKey: "generate.editor.feedWindow", Type: "number",
				Value: intStr(spec.GetFeedWindow()), ErrorKey: windowErrKey, ErrorArgs: windowErrArgs,
				OnChange: func(v string) {
					onFieldChange(func(f *affv1.Feed) { ensureSpec(f).FeedWindow = int32(parseIntOr(v, int(spec.GetFeedWindow()))) })
				},
			}),
		),

		h.Fieldset(
			h.Legend(h.Text(t.T("generate.editor.noveltyAndBudgets"))),
			wui.Input(wui.InputProps{
				T: wt, ID: "generate-editor-novelty-threshold", LabelKey: "generate.editor.noveltyThreshold", Type: "number",
				Value: floatStr(spec.GetNovelty().GetSimilarityThreshold()), ErrorKey: noveltyErrKey, ErrorArgs: noveltyErrArgs,
				OnChange: func(v string) {
					onFieldChange(func(f *affv1.Feed) {
						sp := ensureSpec(f)
						if sp.Novelty == nil {
							sp.Novelty = &affv1.NoveltySettings{}
						}
						sp.Novelty.SimilarityThreshold = parseFloatOr(v, sp.Novelty.SimilarityThreshold)
					})
				},
			}),
			wui.Input(wui.InputProps{
				T: wt, ID: "generate-editor-daily-token-budget", LabelKey: "generate.editor.dailyTokenBudget", Type: "number",
				Value: int64Str(spec.GetDailyTokenBudget()), ErrorKey: tokenBudgetErrKey, ErrorArgs: tokenBudgetErrArgs,
				OnChange: func(v string) {
					onFieldChange(func(f *affv1.Feed) {
						ensureSpec(f).DailyTokenBudget = int64(parseIntOr(v, int(spec.GetDailyTokenBudget())))
					})
				},
			}),
			wui.Input(wui.InputProps{
				T: wt, ID: "generate-editor-daily-run-budget", LabelKey: "generate.editor.dailyRunBudget", Type: "number",
				Value: intStr(spec.GetDailyRunBudget()), ErrorKey: runBudgetErrKey, ErrorArgs: runBudgetErrArgs,
				OnChange: func(v string) {
					onFieldChange(func(f *affv1.Feed) {
						ensureSpec(f).DailyRunBudget = int32(parseIntOr(v, int(spec.GetDailyRunBudget())))
					})
				},
			}),
		),

		// renderSourcesGate: same "always call, hook-free dispatcher"
		// shape as renderConflictGate — always invoked (fieldset always
		// present structurally), isolates the grounded-only content into
		// its own fiber only when the kind is actually GROUNDED.
		renderSourcesGate(t, d, p, spec),

		// Isolated: Save/Validate's Disabled (and Save's Busy) track
		// Connected/Saving/FieldErrs, which can flip across renders of
		// this same fiber — see this file's package doc comment's third
		// hazard.
		ui.CreateElement(renderEditorActions, editorActionsProps{
			T: t, Connected: p.Connected, Saving: p.Saving, HasFieldErr: p.FieldErrs.HasAny(),
			ValidateErr: p.ValidateErr, SaveErr: p.SaveErr, OnValidate: p.OnValidate, OnSave: p.OnSave,
		}),
	)
}

// slugFieldProps/renderSlugField isolate the one field on this page whose
// Disabled value genuinely varies across renders of the SAME editor fiber
// (SlugEditable flips as the operator selects a different feed without the
// Draft-nil/non-nil split in renderEditor remounting the whole form) — see
// this file's package doc comment.
type slugFieldProps struct {
	T             Translator
	Value         string
	Editable      bool
	ErrorKey      string
	ErrorArgs     []any
	OnFieldChange func(func(*affv1.Feed))
}

func renderSlugField(p slugFieldProps) ui.Node {
	wt := wui.T(p.T.T)
	helpKey := ""
	if !p.Editable {
		helpKey = "generate.editor.slug.immutableReason"
	}
	onFieldChange := p.OnFieldChange
	return wui.Input(wui.InputProps{
		T: wt, ID: "generate-editor-slug", LabelKey: "generate.editor.slug",
		Value: p.Value, Disabled: !p.Editable, HelpKey: helpKey,
		ErrorKey: p.ErrorKey, ErrorArgs: p.ErrorArgs,
		OnChange: func(v string) { onFieldChange(func(f *affv1.Feed) { f.Slug = v }) },
	})
}

// editorActionsProps/renderEditorActions isolate Validate/Save — see this
// file's package doc comment's third hazard (their Disabled/Busy vary
// across renders of the same fiber).
type editorActionsProps struct {
	T           Translator
	Connected   bool
	Saving      bool
	HasFieldErr bool
	ValidateErr error
	SaveErr     error
	OnValidate  func()
	OnSave      func()
}

func renderEditorActions(p editorActionsProps) ui.Node {
	t := p.T
	wt := wui.T(t.T)
	return h.Div(h.ClassStr("af-editor__actions"),
		h.If(p.ValidateErr != nil, h.Div(h.ClassStr("af-form-error"), h.Role("alert"), h.Text(mutationErrorText(t, p.ValidateErr)))),
		h.If(p.SaveErr != nil, h.Div(h.ClassStr("af-form-error"), h.Role("alert"), h.Text(mutationErrorText(t, p.SaveErr)))),
		wui.Button(wui.ButtonProps{
			T: wt, ID: "generate-editor-validate", LabelKey: "generate.editor.validate",
			Variant: wui.ButtonSecondary, Disabled: !p.Connected, OnClick: p.OnValidate,
		}),
		wui.Button(wui.ButtonProps{
			T: wt, ID: "generate-editor-save", LabelKey: "generate.editor.save",
			Variant: wui.ButtonPrimary, Disabled: !p.Connected || p.HasFieldErr, Busy: p.Saving, OnClick: p.OnSave,
		}),
	)
}

// renderKindSelect's OnChange is a single, always-present hook (Disabled is
// never set here, so wui.Select never skips registering it — see this
// file's package doc comment's third hazard), so no isolation is needed;
// safe to call inline in renderEditorForm's own fiber.
func renderKindSelect(t Translator, d *affv1.Feed, p editorProps) ui.Node {
	wt := wui.T(t.T)
	kinds := []affv1.FeedKind{affv1.FeedKind_FEED_KIND_GENERATIVE, affv1.FeedKind_FEED_KIND_GROUNDED, affv1.FeedKind_FEED_KIND_AGGREGATE}
	options := make([]wui.SelectOption, len(kinds))
	for i, k := range kinds {
		options[i] = wui.SelectOption{Value: k.String(), LabelKey: kindKey(k)}
	}
	errKey, errArgs := fieldErrorKV(p.FieldErrs, "kind")
	onFieldChange := p.OnFieldChange
	return wui.Select(wui.SelectProps{
		T: wt, ID: "generate-editor-kind", LabelKey: "generate.editor.kind",
		Value: d.GetKind().String(), Options: options, ErrorKey: errKey, ErrorArgs: errArgs,
		OnChange: func(v string) { onFieldChange(func(f *affv1.Feed) { f.Kind = kindFromString(v) }) },
	})
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
	wt := wui.T(t.T)
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
		wui.Button(wui.ButtonProps{T: wt, LabelKey: "generate.editor.addSource", Variant: wui.ButtonSecondary, OnClick: func() {
			p.OnFieldChange(func(f *affv1.Feed) {
				sp := ensureSpec(f)
				sp.Sources = append(sp.Sources, &affv1.SourceSpec{})
			})
		}}),
	)
}

// renderSourceRow is the per-item render func MapKeyedComponent mounts as
// its OWN fiber (see mapItemComponent in the pinned module's html/sugar.go)
// — so its OnInput/OnClick hooks are isolated from every other row and
// from renderSources' own (hook-free) fiber, and adding/removing a row
// cannot shift anyone else's hook slots. None of this row's controls ever
// set Disabled, so it does not additionally need this file's third-hazard
// isolation on top of MapKeyedComponent's own per-row isolation.
func renderSourceRow(t Translator, p sourcesProps, r sourceRow) ui.Node {
	i := r.Index
	wt := wui.T(t.T)
	idxStr := intStr(int32(i))
	urlErrKey, urlErrArgs := fieldErrorKV(p.FieldErrs, sourceFieldKey(i))
	onFieldChange := p.OnFieldChange
	return h.Li(
		wui.Input(wui.InputProps{
			T: wt, ID: "generate-editor-source-url-" + idxStr, LabelKey: "generate.editor.sourceUrl",
			Value: r.Source.GetUrl(), ErrorKey: urlErrKey, ErrorArgs: urlErrArgs,
			OnChange: func(v string) { onFieldChange(func(f *affv1.Feed) { ensureSpec(f).Sources[i].Url = v }) },
		}),
		wui.Input(wui.InputProps{
			T: wt, ID: "generate-editor-source-kind-" + idxStr, LabelKey: "generate.editor.sourceKind",
			PlaceholderKey: "generate.editor.sourceKindPlaceholder", Value: r.Source.GetKind(),
			OnChange: func(v string) { onFieldChange(func(f *affv1.Feed) { ensureSpec(f).Sources[i].Kind = v }) },
		}),
		wui.Button(wui.ButtonProps{T: wt, LabelKey: "generate.editor.removeSource", Variant: wui.ButtonSecondary, OnClick: func() {
			onFieldChange(func(f *affv1.Feed) {
				sp := ensureSpec(f)
				sp.Sources = append(sp.Sources[:i], sp.Sources[i+1:]...)
			})
		}}),
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
	wt := wui.T(t.T)
	diffs := DiffFeedFields(p.Draft, p.ConflictTheirs)
	return h.Div(
		h.ClassStr("af-conflict"),
		h.P(h.ClassStr("af-conflict__headline"), h.Text(t.T("generate.editor.conflict.headline"))),
		h.Div(h.ClassStr("af-conflict__choices"),
			wui.Button(wui.ButtonProps{T: wt, LabelKey: "generate.editor.conflict.keepMine", Variant: wui.ButtonSecondary, OnClick: func() { p.OnResolveConflict(ResolveKeepMine, nil) }}),
			wui.Button(wui.ButtonProps{T: wt, LabelKey: "generate.editor.conflict.takeTheirs", Variant: wui.ButtonSecondary, OnClick: func() { p.OnResolveConflict(ResolveTakeTheirs, nil) }}),
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
	wt := wui.T(t.T)
	p := props.P
	rowsArgs := []any{h.ClassStr("af-conflict__rows")}
	rowsArgs = append(rowsArgs, anyNodes(h.MapKeyedComponent(props.Diffs, func(d FieldDiff) any { return d.FieldKey }, func(d FieldDiff) ui.Node {
		return renderConflictFieldRow(t, p, d)
	}))...)
	return h.Div(
		h.ClassStr("af-conflict__fields"),
		h.P(h.Text(t.T("generate.editor.conflict.perFieldHint"))),
		h.Div(rowsArgs...),
		wui.Button(wui.ButtonProps{T: wt, LabelKey: "generate.editor.conflict.applyPerField", Variant: wui.ButtonPrimary, OnClick: func() {
			p.OnResolveConflict(ResolvePerField, p.PerFieldKeepMine)
		}}),
	)
}

// renderConflictFieldRow runs in its own MapKeyedComponent-provided fiber
// (see renderSourceRow's doc comment for why that matters).
func renderConflictFieldRow(t Translator, p editorProps, d FieldDiff) ui.Node {
	wt := wui.T(t.T)
	return h.Div(
		h.ClassStr("af-conflict__field"),
		h.Div(h.Text(d.FieldKey)),
		h.Div(h.Text(t.T("generate.common.labelValue", t.T("generate.editor.conflict.mine"), d.Mine))),
		h.Div(h.Text(t.T("generate.common.labelValue", t.T("generate.editor.conflict.theirs"), d.Theirs))),
		wui.Button(wui.ButtonProps{T: wt, LabelKey: "generate.editor.conflict.keepMineField", Variant: wui.ButtonSecondary, OnClick: func() {
			choice := cloneBoolMap(p.PerFieldKeepMine)
			choice[d.FieldKey] = true
			p.SetPerFieldChoice(choice)
		}}),
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
