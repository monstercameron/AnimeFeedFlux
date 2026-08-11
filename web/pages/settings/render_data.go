//go:build js && wasm

package settings

import (
	"encoding/base64"
	"strconv"
	"time"

	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	affui "github.com/monstercameron/AnimeFeedFlux/web/ui"
)

// renderData is the Data section (D4-10): per-feed recipe TOML
// export/import (doc.go assumption #2: the RPC surface is per-feed, not
// a single whole-database export), on-demand backup download, DB size,
// item counts, and vacuum.
func renderData() ui.Node {
	disconnected := isDisconnected()

	statsLoading := ui.UseState(true)
	statsErr := ui.UseState(error(nil))
	stats := ui.UseState((*affv1.SystemServiceStatsResponse)(nil))

	feedsLoading := ui.UseState(true)
	feeds := ui.UseState([]*affv1.Feed(nil))
	selectedFeedID := ui.UseState(int64(0))

	exportedTOML := ui.UseState("")
	exportErr := ui.UseState(error(nil))

	importTOML := ui.UseState("")
	importVisible := ui.UseState(false)
	importSubmitting := ui.UseState(false)
	importErr := ui.UseState(error(nil))
	importOK := ui.UseState(false)

	backupURL := ui.UseState("")
	backupFilename := ui.UseState("")
	backupErr := ui.UseState(error(nil))
	backupLoading := ui.UseState(false)

	vacuumVisible := ui.UseState(false)
	vacuumRunning := ui.UseState(false)
	vacuumErr := ui.UseState(error(nil))
	vacuumResult := ui.UseState((*affv1.SystemServiceVacuumResponse)(nil))

	// reloadTick drives the error view's Retry control: the load effect is
	// keyed on it rather than on a constant, so bumping it re-runs the load.
	// §12.6 — an error with no way out is a dead end.
	reloadTick := ui.UseState(0)
	ui.UseEffect(func() func() {
		go func() {
			statsLoading.Set(true)
			resp, err := deps.System.Stats(bgContext(), &affv1.SystemServiceStatsRequest{})
			statsLoading.Set(false)
			if err != nil {
				statsErr.Set(err)
				return
			}
			stats.Set(resp)
		}()
		go func() {
			feedsLoading.Set(true)
			resp, err := deps.Feed.List(bgContext(), &affv1.FeedServiceListRequest{PageSize: 200})
			feedsLoading.Set(false)
			if err != nil {
				// Folded into the section's own error state. Swallowed, this
				// rendered as an empty feed picker — indistinguishable from
				// "you have no feeds", on the page whose whole job is
				// exporting and importing them.
				statsErr.Set(err)
				return
			}
			feeds.Set(resp.GetFeeds())
			if len(resp.GetFeeds()) > 0 {
				selectedFeedID.Set(resp.GetFeeds()[0].GetId())
			}
		}()
		return nil
	}, reloadTick.Get())

	doExport := func() {
		if disconnected {
			return
		}
		exportErr.Set(nil)
		go func() {
			resp, err := deps.Feed.ExportTOML(bgContext(), &affv1.FeedServiceExportTOMLRequest{FeedId: selectedFeedID.Get()})
			if err != nil {
				exportErr.Set(err)
				return
			}
			exportedTOML.Set(resp.GetToml())
		}()
	}

	doImport := func() {
		if importSubmitting.Get() || disconnected {
			return
		}
		importSubmitting.Set(true)
		importErr.Set(nil)
		importOK.Set(false)
		go func() {
			resp, err := deps.Feed.ImportTOML(bgContext(), &affv1.FeedServiceImportTOMLRequest{
				Toml:   importTOML.Get(),
				FeedId: selectedFeedID.Get(),
				// The version the operator is importing ON TOP OF. This was
				// omitted entirely, so the request always carried 0 while
				// every feed row starts at version 1 — the server's
				// optimistic-concurrency check rejected every import into an
				// existing feed, which is every import that matters.
				ExpectedVersion: selectedFeedVersion(feeds.Get(), selectedFeedID.Get()),
			})
			importSubmitting.Set(false)
			if err != nil {
				importErr.Set(err)
				return
			}
			importOK.Set(true)
			importTOML.Set("")
			// The feed's version has just moved; keep the local copy in step
			// so a second import in the same sitting does not conflict.
			if f := resp.GetFeed(); f != nil {
				feeds.Update(func(prev []*affv1.Feed) []*affv1.Feed {
					next := make([]*affv1.Feed, len(prev))
					copy(next, prev)
					for i, existing := range next {
						if existing.GetId() == f.GetId() {
							next[i] = f
						}
					}
					return next
				})
			}
		}()
	}

	doBackup := func() {
		if disconnected {
			return
		}
		backupLoading.Set(true)
		backupErr.Set(nil)
		go func() {
			resp, err := deps.System.Backup(bgContext(), &affv1.SystemServiceBackupRequest{})
			backupLoading.Set(false)
			if err != nil {
				backupErr.Set(err)
				return
			}
			backupFilename.Set(resp.GetFilename())
			backupURL.Set("data:application/octet-stream;base64," + base64.StdEncoding.EncodeToString(resp.GetDbFile()))
		}()
	}

	doVacuum := func() {
		if vacuumRunning.Get() || disconnected {
			return
		}
		vacuumRunning.Set(true)
		vacuumErr.Set(nil)
		vacuumResult.Set(nil)
		go func() {
			resp, err := deps.System.Vacuum(bgContext(), &affv1.SystemServiceVacuumRequest{})
			vacuumRunning.Set(false)
			if err != nil {
				vacuumErr.Set(err)
				return
			}
			vacuumResult.Set(resp)
			// The database file just shrank (or didn't) — refresh the
			// stats panel above so its DB size line reflects reality
			// rather than the pre-vacuum number sitting there stale.
			go func() {
				if r, err := deps.System.Stats(bgContext(), &affv1.SystemServiceStatsRequest{}); err == nil {
					stats.Set(r)
				}
			}()
		}()
	}

	overallState := ComputeScreenState(ScreenInputs{
		Loading:      statsLoading.Get() || feedsLoading.Get(),
		Err:          statsErr.Get(),
		Disconnected: disconnected,
		ItemCount:    1,
	})

	var statsBody ui.Node
	if s := stats.Get(); s != nil {
		statsBody = h.Div(
			h.P(h.Text(t("settings.data.stats.feedCount", s.GetFeedCount()))),
			h.P(h.Text(t("settings.data.stats.itemCount", s.GetItemCount()))),
			h.P(h.Text(t("settings.data.stats.dbSize", fmts().ByteSize(s.GetDbSizeBytes())))),
		)
	} else {
		statsBody = h.Fragment()
	}

	importItem := disconnectedKebabItem(affui.KebabItem{
		ID: "settings-recipe-import", LabelKey: "settings.data.recipe.import.action",
		Danger: true, OnSelect: func() { importVisible.Set(true) },
	}, disconnected)

	body := h.Div(
		h.ClassStr("af-settings-section"),
		h.H2(h.Text(t("settings.data.title"))),
		statsBody,

		h.Section(
			h.ClassStr("af-settings-card"),
			h.H3(h.Text(t("settings.data.recipe.title"))),
			// NOTE (adoption gap — see the ticket report): affui.Select's
			// SelectOption.LabelKey is resolved through T as an i18n KEY
			// (web/ui/select.go), but a feed's slug is DATA — an operator
			// identifier from the database, not translatable interface
			// text (the same "identifiers and operator surface, not
			// prose" distinction TODOS.md D6-19 and web/i18n/adapter.go's
			// FormatByteSize comment both draw for unit abbreviations).
			// Passing a slug as LabelKey would have T's resolve() treat it
			// as a lookup key, rendering the slug back out unchanged only
			// by coincidence of the fallback translator's "no entry ->
			// return the key" behavior — the real catalogue's OnMissing
			// hook would then log every feed slug as a "missing i18n key"
			// (web/i18n/catalog.go's logMissingKey), which is exactly
			// wrong for something that was never supposed to be looked up
			// at all. Select has no "pass this value through literally"
			// escape hatch, so the feed picker stays a plain <select> with
			// literal option values (not i18n-lint literals — h.Text/
			// resolve() only see i18n keys and RPC data here, no hardcoded
			// English prose) instead.
			h.Label(h.Text(t("settings.data.recipe.feed")),
				h.Select(h.Value(int64ToStr(selectedFeedID.Get())),
					h.OnChange(func(e ui.ChangeEvent) { selectedFeedID.Set(parseInt64(e.GetValue())) }),
					feedSelectOptions(feeds.Get()),
				)),
			affui.Button(affui.ButtonProps{
				T: t, LabelKey: "settings.data.recipe.export", Variant: affui.ButtonSecondary,
				Disabled: disconnected, OnClick: doExport,
			}),
			h.Show(exportErr.Get() != nil, h.P(h.Role("alert"), h.Aria("live", "assertive"), h.ClassStr("af-error"), h.Text(t("settings.data.recipe.exportError")))),
			h.Show(exportedTOML.Get() != "", h.Textarea(
				h.ID("settings-recipe-export"), h.Rows(10),
				h.Aria("label", t("settings.data.recipe.exportTitle")),
				// Output, not a field: it looked editable and edits to it went
				// nowhere.
				h.Attr("readonly", "readonly"),
				h.Value(exportedTOML.Get()))),

			h.H4(h.Text(t("settings.data.recipe.importTitle"))),
			h.Textarea(
				h.ID("settings-recipe-import"), h.Rows(10),
				h.Aria("label", t("settings.data.recipe.importTitle")),
				h.Value(importTOML.Get()), h.DisabledIf(disconnected),
				h.OnInput(func(e ui.InputEvent) { importTOML.Set(e.GetValue()) })),
			kebabUI(kebabUIProps{
				ID: "settings-recipe-kebab", LabelKey: "kebab.actionsFor",
				LabelArgs: []any{t("settings.data.recipe.title")},
				Items:     []affui.KebabItem{importItem},
			}),
			confirmUI(confirmUIProps{
				ID: "settings-recipe-import-confirm", TitleKey: "settings.data.recipe.import.confirmTitle",
				MessageKey:     "settings.data.recipe.import.prompt",
				RequiredPhrase: t(ConfirmationWordKey(ActionImportTOML)),
				Open:           importVisible.Get(), Busy: importSubmitting.Get(),
				OnConfirm: func() { importVisible.Set(false); doImport() },
				OnCancel:  func() { importVisible.Set(false) },
			}),
			h.Show(importErr.Get() != nil, h.P(h.Role("alert"), h.Aria("live", "assertive"), h.ClassStr("af-error"), h.Text(t("settings.data.recipe.importError")))),
			h.Show(importOK.Get(), h.P(h.Role("status"), h.Aria("live", "polite"), h.ClassStr("af-success"), h.Text(t("settings.data.recipe.importSuccess")))),
		),

		h.Section(
			h.ClassStr("af-settings-card"),
			h.H3(h.Text(t("settings.data.backup.title"))),
			affui.Button(affui.ButtonProps{
				T: t, LabelKey: "settings.data.backup.generate", Variant: affui.ButtonSecondary,
				Disabled: disconnected, Busy: backupLoading.Get(), OnClick: doBackup,
			}),
			h.Show(disconnected, h.P(h.Role("status"), h.ClassStr("af-warning"), h.Text(t("settings.common.disconnectedReason")))),
			h.Show(backupErr.Get() != nil, h.P(h.Role("alert"), h.Aria("live", "assertive"), h.ClassStr("af-error"), h.Text(t("settings.data.backup.error")))),
			h.Show(backupURL.Get() != "", h.A(h.Href(backupURL.Get()), h.Attr("download", backupFilename.Get()), h.Text(t("settings.data.backup.download")))),
		),

		renderVacuumSection(vacuumSectionState{
			disconnected: disconnected,
			currentSize:  currentDBSizeBytes(stats.Get()),
			visible:      vacuumVisible.Get(),
			running:      vacuumRunning.Get(),
			err:          vacuumErr.Get(),
			result:       vacuumResult.Get(),
			setVisible:   vacuumVisible.Set,
			onConfirm:    doVacuum,
		}),
	)

	return screenWrapperRetry(overallState, statsErr.Get(), func() { reloadTick.Update(func(n int) int { return n + 1 }) }, body)
}

// currentDBSizeBytes reads the DB size off the already-loaded Stats
// response, or 0 before it has loaded — EstimateVacuumDuration's zero
// bucket (VacuumEstimateBrief) is a safe, non-alarming default for that
// brief window rather than blocking the warning text on a second RPC.
func currentDBSizeBytes(s *affv1.SystemServiceStatsResponse) int64 {
	if s == nil {
		return 0
	}
	return s.GetDbSizeBytes()
}

// vacuumEstimateKey maps EstimateVacuumDuration's coarse bucket to the
// i18n key for its blocking-time warning (D4-10: "tell the user it will
// block, and roughly for how long given the reported size").
func vacuumEstimateKey(sizeBytes int64) string {
	switch EstimateVacuumDuration(sizeBytes) {
	case VacuumEstimateModerate:
		return "settings.data.vacuum.estimate.moderate"
	case VacuumEstimateLong:
		return "settings.data.vacuum.estimate.long"
	default:
		return "settings.data.vacuum.estimate.brief"
	}
}

// vacuumSectionState carries renderVacuumSection's inputs as a struct
// (rather than a long positional argument list) since it mixes read state,
// a setter, and a callback.
type vacuumSectionState struct {
	disconnected bool
	currentSize  int64
	visible      bool
	running      bool
	err          error
	result       *affv1.SystemServiceVacuumResponse
	setVisible   func(bool)
	onConfirm    func()
}

// renderVacuumSection is the Data section's vacuum control (D4-10): behind
// the kebab with typed confirmation like the page's other destructive
// actions (confirm.go's ActionVacuum), warns how long the exclusive lock
// will likely block given the CURRENT database size before the admin
// confirms, and shows before/after sizes plus the actual elapsed duration
// on return — "did that accomplish anything" is the only reason to ever
// run it. A FailedPrecondition from the server (a generation run is in
// flight) renders the server's own actionable message, same convention
// screenWrapper's settings.common.state.errorDetail already uses, rather
// than a generic "vacuum failed".
func renderVacuumSection(st vacuumSectionState) ui.Node {
	vacuumItem := disconnectedKebabItem(affui.KebabItem{
		ID: "settings-vacuum", LabelKey: "settings.data.vacuum.action",
		Danger: true, OnSelect: func() { st.setVisible(true) },
	}, st.disconnected)

	var resultBody ui.Node = h.Fragment()
	if r := st.result; r != nil {
		parts := BreakVacuumDuration(time.Duration(r.GetDurationMs()) * time.Millisecond)
		resultBody = h.Div(
			h.Role("status"), h.Aria("live", "polite"), h.ClassStr("af-success"),
			h.P(h.Text(t("settings.data.vacuum.result.sizes",
				fmts().ByteSize(r.GetSizeBeforeBytes()), fmts().ByteSize(r.GetSizeAfterBytes())))),
			h.P(h.Text(t("settings.data.vacuum.result.duration", parts.Minutes, parts.Seconds))),
		)
	}

	return h.Section(
		h.ClassStr("af-settings-card"),
		h.H3(h.Text(t("settings.data.vacuum.title"))),
		h.P(h.Text(t("settings.data.vacuum.description"))),
		h.P(h.ClassStr("af-warning"), h.Text(t(vacuumEstimateKey(st.currentSize)))),
		kebabUI(kebabUIProps{
			ID: "settings-vacuum-kebab", LabelKey: "kebab.actionsFor",
			LabelArgs: []any{t("settings.data.vacuum.title")},
			Items:     []affui.KebabItem{vacuumItem},
		}),
		confirmUI(confirmUIProps{
			ID: "settings-vacuum-confirm", TitleKey: "settings.data.vacuum.confirmTitle",
			MessageKey:     "settings.data.vacuum.confirmPrompt",
			MessageArgs:    []any{t(vacuumEstimateKey(st.currentSize))},
			RequiredPhrase: t(ConfirmationWordKey(ActionVacuum)),
			Open:           st.visible, Busy: st.running,
			OnConfirm: func() { st.setVisible(false); st.onConfirm() },
			OnCancel:  func() { st.setVisible(false) },
		}),
		h.Show(st.running, h.P(h.Role("status"), h.Aria("live", "polite"), h.Text(t("settings.data.vacuum.running")))),
		h.Show(st.err != nil, h.P(h.Role("alert"), h.Aria("live", "assertive"), h.ClassStr("af-error"), h.Text(t("settings.data.vacuum.error", vacuumErrMessage(st.err))))),
		resultBody,
	)
}

// vacuumErrMessage returns err's message with a nil-safe empty string —
// the {arg1} interpolation target for settings.data.vacuum.error, same
// "render the server's own message" convention render.go's screenWrapper
// uses for settings.common.state.errorDetail.
func vacuumErrMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func feedSelectOptions(feeds []*affv1.Feed) []ui.Node {
	opts := make([]ui.Node, 0, len(feeds))
	for _, f := range feeds {
		opts = append(opts, h.Option(h.Value(int64ToStr(f.GetId())), h.Text(f.GetSlug())))
	}
	return opts
}

func int64ToStr(v int64) string {
	return strconv.FormatInt(v, 10)
}

// selectedFeedVersion finds the version of the feed an import targets.
//
// Returning 0 for an unknown id is deliberate: 0 never matches a real row
// (feeds start at version 1), so the server refuses rather than writing over
// a feed this page has no current copy of.
func selectedFeedVersion(feeds []*affv1.Feed, id int64) int64 {
	for _, f := range feeds {
		if f.GetId() == id {
			return f.GetVersion()
		}
	}
	return 0
}
