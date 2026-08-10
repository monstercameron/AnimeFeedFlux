//go:build js && wasm

package settings

import (
	"time"

	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// renderAbout is the About section (D4-11): version, build, uptime, last
// successful run per feed. See doc.go assumption #1: "last successful
// run" is shown as each feed's last_built_at (labeled accordingly, not
// as "last success") since no RPC returns a per-feed last-SUCCEEDED-run
// timestamp distinct from that.
func renderAbout() ui.Node {
	loading := ui.UseState(true)
	errState := ui.UseState(error(nil))
	version := ui.UseState((*affv1.SystemServiceVersionResponse)(nil))

	feedsLoading := ui.UseState(true)
	feeds := ui.UseState([]*affv1.Feed(nil))

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
	}, "about-mount")

	state := ComputeScreenState(ScreenInputs{
		Loading:   loading.Get() || feedsLoading.Get(),
		Err:       errState.Get(),
		ItemCount: 1,
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

	feedRows := make([]ui.Node, 0, len(feeds.Get()))
	for _, f := range feeds.Get() {
		lastBuild := t("settings.about.feed.neverBuilt")
		if ts := f.GetLastBuiltAt(); ts != nil {
			lastBuild = fmts().RelativeTime(ts.AsTime(), time.Now())
		}
		feedRows = append(feedRows, h.Tr(
			h.Td(h.Text(f.GetSlug())),
			h.Td(h.Text(lastBuild)),
		))
	}

	body := h.Div(
		h.ClassStr("af-settings-section"),
		h.H2(h.Text(t("settings.about.title"))),
		versionBody,
		h.H3(h.Text(t("settings.about.feed.lastBuildTitle"))),
		h.Table(
			h.Thead(h.Tr(
				h.Th(h.Text(t("settings.about.feed.col.slug"))),
				h.Th(h.Text(t("settings.about.feed.col.lastBuild"))),
			)),
			h.Tbody(feedRows),
		),
	)

	return screenWrapper(state, errState.Get(), body)
}

func formatUptime(startedAt time.Time) string {
	parts := BreakDuration(Uptime(startedAt, time.Now()))
	return t("settings.about.uptime.parts", parts.Days, parts.Hours, parts.Minutes)
}
