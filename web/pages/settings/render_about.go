//go:build js && wasm

package settings

import (
	"time"

	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	affui "github.com/monstercameron/AnimeFeedFlux/web/ui"
)

// renderAbout is the About section (D4-11): version, build, uptime, last
// successful run per feed. See doc.go assumption #1: "last successful
// run" is shown as each feed's last_built_at (labeled accordingly, not
// as "last success") since no RPC returns a per-feed last-SUCCEEDED-run
// timestamp distinct from that.
func renderAbout() ui.Node {
	disconnected := isDisconnected()

	loading := ui.UseState(true)
	errState := ui.UseState(error(nil))
	version := ui.UseState((*affv1.SystemServiceVersionResponse)(nil))

	feedsLoading := ui.UseState(true)
	feeds := ui.UseState([]*affv1.Feed(nil))

	// reloadTick drives the error view's Retry control: the load effect is
	// keyed on it rather than on a constant, so bumping it re-runs the load.
	// §12.6 — an error with no way out is a dead end.
	reloadTick := ui.UseState(0)
	ui.UseEffect(func() func() {
		go func() {
			loading.Set(true)
			resp, err := deps.System.Version(bgContext(), &affv1.SystemServiceVersionRequest{})
			loading.Set(false)
			if err != nil {
				errState.Set(err)
				return
			}
			version.Set(resp)
		}()
		go func() {
			feedsLoading.Set(true)
			resp, err := deps.Feed.List(bgContext(), &affv1.FeedServiceListRequest{PageSize: 200})
			feedsLoading.Set(false)
			if err == nil {
				feeds.Set(resp.GetFeeds())
			}
		}()
		return nil
	}, reloadTick.Get())

	state := ComputeScreenState(ScreenInputs{
		Loading:      loading.Get() || feedsLoading.Get(),
		Err:          errState.Get(),
		Disconnected: disconnected,
		ItemCount:    1,
	})

	var versionBody ui.Node
	if v := version.Get(); v != nil {
		versionBody = h.Div(
			h.P(h.Text(t("settings.about.version", v.GetVersion()))),
			h.P(h.Text(t("settings.about.build", v.GetBuild()))),
			h.P(h.Text(t("settings.about.uptime", formatUptime(v.GetStartedAt().AsTime())))),
		)
	} else {
		versionBody = h.Fragment()
	}

	feedRows := make([]map[string]affui.Node, 0, len(feeds.Get()))
	feedRowKeys := make([]string, 0, len(feeds.Get()))
	for _, f := range feeds.Get() {
		lastBuild := t("settings.about.feed.neverBuilt")
		if ts := f.GetLastBuiltAt(); ts != nil {
			lastBuild = fmts().RelativeTime(ts.AsTime(), time.Now())
		}
		feedRows = append(feedRows, map[string]affui.Node{
			"slug":      h.Text(f.GetSlug()),
			"lastBuild": h.Text(lastBuild),
		})
		feedRowKeys = append(feedRowKeys, int64ToStr(f.GetId()))
	}

	body := h.Div(
		h.ClassStr("af-settings-section"),
		h.H2(h.Text(t("settings.about.title"))),
		versionBody,
		h.H3(h.Text(t("settings.about.feed.lastBuildTitle"))),
		affui.Table(affui.TableProps{
			T: t, ID: "settings-about-feeds", CaptionKey: "settings.about.feed.caption",
			Columns: []affui.TableColumn{
				{ID: "slug", LabelKey: "settings.about.feed.col.slug", Mono: true},
				{ID: "lastBuild", LabelKey: "settings.about.feed.col.lastBuild"},
			},
			Rows: feedRows, RowKeys: feedRowKeys,
		}),
	)

	return screenWrapperRetry(state, errState.Get(), func() { reloadTick.Update(func(n int) int { return n + 1 }) }, body)
}

func formatUptime(startedAt time.Time) string {
	parts := BreakDuration(Uptime(startedAt, time.Now()))
	return t("settings.about.uptime.parts", parts.Days, parts.Hours, parts.Minutes)
}
