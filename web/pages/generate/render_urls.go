//go:build js && wasm

package generatepage

import (
	"context"
	"strings"
	"time"

	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/GoWebComponents/v5/interop"
	"github.com/monstercameron/GoWebComponents/v5/ui"
)

// render_urls.go is the one place an operator gets the URLs this product
// exists to produce, in a form they can hand to something else.
//
// Everything /generate did before this was about AUTHORING a feed; nothing on
// the page told you where the finished thing lives. The URL is the entire
// deliverable (PLAN.md §1: "the product surface is a URL that returns valid
// XML and never lies") and reconstructing it by hand from a base URL in
// Settings and a slug in the editor — with the right extension per format — is
// exactly the kind of transcription that produces a Slack subscription to a
// feed that 404s.
//
// The index URL is listed first and always, feed or no feed selected: §14.1
// calls it "the page you paste into Slack when you have forgotten a slug",
// which is precisely the moment someone is looking at this panel.

// copyResetDelay is how long a copy button stays in its "Copied" state. Long
// enough to be seen, short enough that the button is back to naming its
// action before the operator returns to it.
const copyResetDelay = 2 * time.Second

// renderCopyRow is one labelled URL with a button that puts it on the
// clipboard.
//
// The URL itself is rendered in a <code> and is selectable, so the copy
// button is a convenience rather than the only way out — a clipboard write
// can be refused by the browser (permissions, a non-secure context) and the
// operator must still be able to get the URL when it is.
func renderCopyRow(labelKey, url string) ui.Node {
	t := deps.I18n
	copied := ui.UseState(false)
	failed := ui.UseState(false)

	onCopy := ui.UseEvent(func() {
		clip, err := interop.NavigatorClipboard()
		if err != nil {
			failed.Set(true)
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := clip.WriteText(ctx, url); err != nil {
				failed.Set(true)
				return
			}
			failed.Set(false)
			copied.Set(true)
			// Reset in the background so the button does not stay stuck on
			// "Copied" for the life of the page.
			time.Sleep(copyResetDelay)
			copied.Set(false)
		}()
	})

	label := t.T("generate.urls.copy", nil)
	switch {
	case failed.Get():
		label = t.T("generate.urls.copyFailed", nil)
	case copied.Get():
		label = t.T("generate.urls.copied", nil)
	}

	return h.Div(
		h.ClassStr("af-urls__row"),
		h.Div(h.ClassStr("af-urls__label"), h.Text(t.T(labelKey, nil))),
		h.Code(h.ClassStr("af-urls__value"), h.Text(url)),
		h.Button(
			h.Type("button"),
			h.ClassStr("af-urls__copy"),
			// The accessible name says WHICH url, because a screen-reader
			// user tabbing through four identical "Copy" buttons has no
			// other way to tell them apart.
			h.Aria("label", t.T("generate.urls.copyNamed", t.T(labelKey, nil))),
			h.OnClick(onCopy),
			h.Text(label),
		),
	)
}

// renderFeedURLs is the panel: the public index, then the selected feed's
// three formats. slug may be empty (nothing selected, or an unsaved draft),
// in which case only the index is shown — inventing a URL for a feed that
// does not exist yet would be a URL that 404s.
func renderFeedURLs(baseURL, slug string) ui.Node {
	t := deps.I18n
	base := strings.TrimRight(baseURL, "/")
	if base == "" {
		// Without a configured public base URL every URL here would be a
		// guess. Say so and point at where it is set, rather than printing
		// "/feeds/x.xml" and letting someone paste a relative path into
		// Slack.
		return h.Section(
			h.ClassStr("af-urls"),
			h.H3(h.Text(t.T("generate.urls.title", nil))),
			h.P(h.ClassStr("af-urls__unset"), h.Text(t.T("generate.urls.baseUnset", nil))),
		)
	}

	rows := []ui.Node{
		ui.CreateElement(func(struct{}) ui.Node { return renderCopyRow("generate.urls.index", base+"/") }, struct{}{}),
	}
	if slug != "" {
		for _, f := range []struct{ key, ext string }{
			{"generate.urls.rss", ".xml"},
			{"generate.urls.atom", ".atom"},
			{"generate.urls.json", ".json"},
		} {
			key, url := f.key, base+"/feeds/"+slug+f.ext
			rows = append(rows, ui.CreateElement(func(struct{}) ui.Node { return renderCopyRow(key, url) }, struct{}{}))
		}
	}

	args := []any{h.ClassStr("af-urls"), h.H3(h.Text(t.T("generate.urls.title", nil)))}
	for _, r := range rows {
		args = append(args, r)
	}
	return h.Section(args...)
}

// urlPanelProps is renderFeedURLs' component wrapper. It is a component
// (rather than a plain call) because each row inside owns hook state for its
// own copy button, and hooks need a stable owning fiber.
type urlPanelProps struct {
	BaseURL string
	Slug    string
}

func renderURLPanel(p urlPanelProps) ui.Node {
	return renderFeedURLs(p.BaseURL, p.Slug)
}

// savedSlug returns the slug of the feed as last loaded from the server, or
// "" when nothing is selected or the selection has never been saved.
func savedSlug(loaded *affv1.Feed) string {
	if loaded == nil {
		return ""
	}
	return loaded.GetSlug()
}
