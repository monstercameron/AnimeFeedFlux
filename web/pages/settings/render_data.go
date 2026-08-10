//go:build js && wasm

package settings

import (
	"encoding/base64"
	"strconv"

	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// renderData is the Data section (D4-10): per-feed recipe TOML
// export/import (doc.go assumption #2: the RPC surface is per-feed, not
// a single whole-database export), on-demand backup download, DB size,
// item counts. Vacuum has no backing RPC as of this change — see doc.go.
func renderData() ui.Node {
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
	importErr := ui.UseState(error(nil))
	importOK := ui.UseState(false)

	backupURL := ui.UseState("")
	backupFilename := ui.UseState("")
	backupErr := ui.UseState(error(nil))
	backupLoading := ui.UseState(false)

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
				return
			}
			feeds.Set(resp.GetFeeds())
			if len(resp.GetFeeds()) > 0 {
				selectedFeedID.Set(resp.GetFeeds()[0].GetId())
			}
		}()
		return nil
	}, "data-mount")

	doExport := func() {
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
		importErr.Set(nil)
		importOK.Set(false)
		go func() {
			_, err := deps.Feed.ImportTOML(bgContext(), &affv1.FeedServiceImportTOMLRequest{
				Toml:   importTOML.Get(),
				FeedId: selectedFeedID.Get(),
			})
			if err != nil {
				importErr.Set(err)
				return
			}
			importOK.Set(true)
			importTOML.Set("")
		}()
	}

	doBackup := func() {
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

	feedOptions := make([]ui.Node, 0, len(feeds.Get()))
	for _, f := range feeds.Get() {
		feedOptions = append(feedOptions, h.Option(h.Value(int64ToStr(f.GetId())), h.Text(f.GetSlug())))
	}

	overallState := ComputeScreenState(ScreenInputs{
		Loading:   statsLoading.Get() || feedsLoading.Get(),
		Err:       statsErr.Get(),
		ItemCount: 1,
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

	body := h.Div(
		h.ClassStr("af-settings-section"),
		h.H2(h.Text(t("settings.data.title"))),
		statsBody,

		h.Section(
			h.ClassStr("af-settings-card"),
			h.H3(h.Text(t("settings.data.recipe.title"))),
			h.Label(h.Text(t("settings.data.recipe.feed")),
				h.Select(h.Value(int64ToStr(selectedFeedID.Get())),
					h.OnChange(func(e ui.ChangeEvent) { selectedFeedID.Set(parseInt64(e.GetValue())) }),
					feedOptions,
				)),
			h.Button(h.Type("button"), h.OnClick(doExport), h.Text(t("settings.data.recipe.export"))),
			h.Show(exportErr.Get() != nil, h.P(h.ClassStr("af-error"), h.Text(t("settings.data.recipe.exportError")))),
			h.Show(exportedTOML.Get() != "", h.Textarea(h.Rows(10), h.Value(exportedTOML.Get()))),

			h.H4(h.Text(t("settings.data.recipe.importTitle"))),
			h.Textarea(h.Rows(10), h.Value(importTOML.Get()),
				h.OnInput(func(e ui.InputEvent) { importTOML.Set(e.GetValue()) })),
			kebabMenu([]kebabItem{{
				label:   t("settings.data.recipe.import.action"),
				danger:  true,
				onClick: func() { importVisible.Set(true) },
			}}),
			confirmModal(confirmModalProps{
				Visible:   importVisible.Get(),
				PromptKey: "settings.data.recipe.import.prompt",
				Word:      t(ConfirmationWordKey(ActionImportTOML)),
				OnConfirm: func() { importVisible.Set(false); doImport() },
				OnCancel:  func() { importVisible.Set(false) },
			}),
			h.Show(importErr.Get() != nil, h.P(h.ClassStr("af-error"), h.Text(t("settings.data.recipe.importError")))),
			h.Show(importOK.Get(), h.P(h.ClassStr("af-success"), h.Text(t("settings.data.recipe.importSuccess")))),
		),

		h.Section(
			h.ClassStr("af-settings-card"),
			h.H3(h.Text(t("settings.data.backup.title"))),
			h.Button(h.Type("button"), h.DisabledIf(backupLoading.Get()), h.OnClick(doBackup), h.Text(t("settings.data.backup.generate"))),
			h.Show(backupErr.Get() != nil, h.P(h.ClassStr("af-error"), h.Text(t("settings.data.backup.error")))),
			h.Show(backupURL.Get() != "", h.A(h.Href(backupURL.Get()), h.Attr("download", backupFilename.Get()), h.Text(t("settings.data.backup.download")))),
		),

		h.Section(
			h.ClassStr("af-settings-card"),
			h.H3(h.Text(t("settings.data.vacuum.title"))),
			h.P(h.Text(t("settings.data.vacuum.unavailable"))),
		),
	)

	return screenWrapper(overallState, statsErr.Get(), body)
}

func int64ToStr(v int64) string {
	return strconv.FormatInt(v, 10)
}
