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
	// The backup is a copy of the whole database — admin password hash,
	// encrypted TOTP secret, every session and recovery-code hash — so the
	// server re-proves credentials rather than trusting the live session
	// (A8-40). Same two fields as the change-password form.
	backupPassword := ui.UseState("")
	backupTOTP := ui.UseState("")

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
		if backupPassword.Get() == "" || backupTOTP.Get() == "" {
			return
		}
		backupLoading.Set(true)
		backupErr.Set(nil)
		// A stale URL alongside a fresh failure reads as success; drop it
		// before the round trip so only one of the two can be on screen.
		backupURL.Set("")
		go func() {
			resp, err := deps.System.Backup(bgContext(), &affv1.SystemServiceBackupRequest{
				CurrentPassword: backupPassword.Get(),
				TotpCode:        backupTOTP.Get(),
			})
			backupLoading.Set(false)
			if err != nil {
				backupErr.Set(err)
				return
			}
			// Never leave the re-proof sitting in the DOM after it has been
			// spent — the codes are single-use and the password is not.
			backupPassword.Set("")
			backupTOTP.Set("")
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
		// The three figures are what this page is ABOUT — how much there is
		// and how big it has become — and they were three muted sentences at
		// the top that read like a caption on something else. Now they are the
		// first thing the eye lands on: the number large and in the data face,
		// its name small underneath.
		statsBody = h.Div(
			h.ClassStr("af-data-stats"),
			renderDataStat(int64ToStr(int64(s.GetFeedCount())), t("settings.data.stats.feeds")),
			renderDataStat(int64ToStr(s.GetItemCount()), t("settings.data.stats.items")),
			renderDataStat(fmts().ByteSize(s.GetDbSizeBytes()), t("settings.data.stats.size")),
		)
	} else {
		statsBody = h.Fragment()
	}

	body := h.Div(
		h.ClassStr("af-settings-section"),
		h.H2(h.Text(t("settings.data.title"))),
		statsBody,

		// Export: pick a feed, press the button. The picker and the button sit
		// on one line at the same height, instead of a labelled select beside
		// a 333px-wide slab on a different baseline.
		renderDataOp(dataOpProps{
			TitleKey: "settings.data.recipe.title",
			HelpKey:  "settings.data.recipe.help",
			Controls: h.Fragment(
				// NOTE (adoption gap): affui.Select resolves SelectOption.LabelKey
				// through T as an i18n KEY, but a feed slug is DATA. Passing a slug
				// there would make the real catalogue log every slug as a missing
				// key, so the feed picker stays a plain <select>.
				h.Select(h.ClassStr("af-data-op__select"),
					h.Aria("label", t("settings.data.recipe.feed")),
					h.Value(int64ToStr(selectedFeedID.Get())),
					h.OnChange(func(e ui.ChangeEvent) { selectedFeedID.Set(parseInt64(e.GetValue())) }),
					feedSelectOptions(feeds.Get()),
				),
				affui.Button(affui.ButtonProps{
					T: t, LabelKey: "settings.data.recipe.export", Variant: affui.ButtonSecondary,
					Disabled: disconnected, OnClick: doExport,
				}),
			),
			Body: h.Fragment(
				h.Show(exportErr.Get() != nil, h.P(h.Role("alert"), h.Aria("live", "assertive"), h.ClassStr("af-error"), h.Text(t("settings.data.recipe.exportError")))),
				h.Show(exportedTOML.Get() != "", h.Textarea(
					h.ID("settings-recipe-export"), h.Rows(10),
					h.ClassStr("af-data-op__field"),
					h.Aria("label", t("settings.data.recipe.exportTitle")),
					// Output, not a field: it looked editable and edits to it
					// went nowhere.
					h.Attr("readonly", "readonly"),
					h.Value(exportedTOML.Get()))),
			),
		}),

		// Import: the action is a labelled button, not an unexplained glyph.
		//
		// The kebab rule (§12.6 / D0-15) is about destructive actions inside a
		// LIST OF ROWS, where a row full of buttons invites a mis-click. Here
		// the operation IS the card, and hiding its only control behind an
		// unlabeled dot-dot-dot is why this page looked like it could not do
		// anything. The safety is the typed confirmation, which is unchanged.
		renderDataOp(dataOpProps{
			TitleKey: "settings.data.recipe.importTitle",
			HelpKey:  "settings.data.recipe.import.help",
			Controls: affui.Button(affui.ButtonProps{
				T: t, LabelKey: "settings.data.recipe.import.action", Variant: affui.ButtonDanger,
				Disabled: disconnected || importTOML.Get() == "", Busy: importSubmitting.Get(),
				OnClick: func() { importVisible.Set(true) },
			}),
			Body: h.Fragment(
				h.Textarea(
					h.ID("settings-recipe-import"), h.Rows(10),
					h.ClassStr("af-data-op__field"),
					h.Aria("label", t("settings.data.recipe.importTitle")),
					h.Attr("placeholder", t("settings.data.recipe.import.placeholder")),
					h.Value(importTOML.Get()), h.DisabledIf(disconnected),
					h.OnInput(func(e ui.InputEvent) { importTOML.Set(e.GetValue()) })),
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
		}),

		renderDataOp(dataOpProps{
			TitleKey: "settings.data.backup.title",
			HelpKey:  "settings.data.backup.help",
			Body: h.Fragment(
				h.Show(disconnected, h.P(h.Role("status"), h.ClassStr("af-warning"), h.Text(t("settings.common.disconnectedReason")))),
				h.P(h.Role("status"), h.ClassStr("af-warning"), h.Text(t("settings.data.backup.credentialsWarning"))),
				h.Form(
					h.OnSubmit(func(e ui.FormEvent) { e.PreventDefault(); doBackup() }),
					affui.Input(affui.InputProps{
						T: t, ID: "settings-backup-password", LabelKey: "settings.data.backup.currentPassword",
						Type: "password", Value: backupPassword.Get(), Disabled: backupLoading.Get(),
						AutoComplete: "current-password", OnChange: func(v string) { backupPassword.Set(v) },
					}),
					affui.Input(affui.InputProps{
						T: t, ID: "settings-backup-totp", LabelKey: "settings.data.backup.totp",
						Value: backupTOTP.Get(), Disabled: backupLoading.Get(), Mono: true,
						OnChange: func(v string) { backupTOTP.Set(v) },
					}),
					affui.Button(affui.ButtonProps{
						T: t, LabelKey: "settings.data.backup.generate", Variant: affui.ButtonSecondary,
						Type: "submit", Disabled: disconnected, Busy: backupLoading.Get(),
					}),
				),
				h.Show(backupErr.Get() != nil, h.P(h.Role("alert"), h.Aria("live", "assertive"), h.ClassStr("af-error"), h.Text(t("settings.data.backup.error")))),
				h.Show(backupURL.Get() != "", h.A(h.ClassStr("af-data-op__download"),
					h.Href(backupURL.Get()), h.Attr("download", backupFilename.Get()),
					h.Text(t("settings.data.backup.download")))),
			),
		}),

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

	return renderDataOp(dataOpProps{
		TitleKey: "settings.data.vacuum.title",
		HelpKey:  "settings.data.vacuum.description",
		Controls: affui.Button(affui.ButtonProps{
			T: t, LabelKey: "settings.data.vacuum.action", Variant: affui.ButtonDanger,
			Disabled: st.disconnected, Busy: st.running,
			OnClick: func() { st.setVisible(true) },
		}),
		Body: h.Fragment(
			h.P(h.ClassStr("af-warning"), h.Text(t(vacuumEstimateKey(st.currentSize)))),
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
		),
	})
}

// dataOpProps is one database operation: what it is, what it costs, the
// control that runs it, and whatever it needs or produces.
type dataOpProps struct {
	TitleKey string
	HelpKey  string
	// Controls sit on the title's line, right-aligned. Body sits underneath at
	// full width — inputs, output, errors, results.
	Controls ui.Node
	Body     ui.Node
}

// renderDataOp draws one operation row.
//
// Every operation on this page has the same shape — a name, a consequence and
// one action — and each was laid out differently: a select beside a full-width
// button, a bare textarea beside an unlabeled glyph, a lone button, and two
// paragraphs beside another glyph. One shape repeated is what lets an operator
// look at this page and see that it does four things.
func renderDataOp(p dataOpProps) ui.Node {
	return h.Section(
		h.ClassStr("af-data-op"),
		h.Div(h.ClassStr("af-data-op__head"),
			h.Div(h.ClassStr("af-data-op__label"),
				h.H3(h.Text(t(p.TitleKey))),
				h.P(h.ClassStr("af-field-help"), h.Text(t(p.HelpKey))),
			),
			h.Div(h.ClassStr("af-data-op__action"), p.Controls),
		),
		p.Body,
	)
}

// renderDataStat is one figure in the stat strip.
func renderDataStat(value, label string) ui.Node {
	return h.Div(h.ClassStr("af-data-stat"),
		h.Div(h.ClassStr("af-data-stat__value"), h.Text(value)),
		h.Div(h.ClassStr("af-data-stat__label"), h.Text(label)),
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
