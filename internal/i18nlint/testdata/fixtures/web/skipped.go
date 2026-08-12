package web

import (
	_ "example.com/some/import/path"

	"example.com/i18n"
	h "example.com/shorthand"
)

type Tagged struct {
	ID string `json:"id" db:"id"`
}

func renderSkipped(cond bool) h.Node {
	_ = h.ClassStr("primary-button")
	_ = h.ClassNames("row", "row--active")
	_ = h.ClassMap(map[string]bool{
		"af-banner":          true,
		"af-banner--visible": cond,
	})
	_ = h.When(cond, "is-active")
	_ = h.ID("feed-list")
	_ = h.For("feed-name")
	_ = h.Name("feed_name")
	_ = h.Href("/feeds/example.xml")
	_ = h.Src("/static/logo.svg")
	_ = h.Role("navigation")
	_ = h.Lang("en")
	_ = h.Type("text")
	_ = h.Value("42")
	_ = h.Tag("div", h.ClassStr("wrapper"))

	// ARIA attributes whose value is a token or an id reference, not prose.
	_ = h.Aria("live", "assertive")
	_ = h.Aria("hidden", "true")
	_ = h.Aria("current", "page")
	_ = h.Aria("describedby", "feed-name-hint")
	_ = h.Aria("labelledby", "feed-name-label")
	_ = h.Aria("controls", "feed-panel")
	// A non-literal attribute name is left alone: the tool cannot tell what
	// it resolves to, and guessing either way is worse than the miss.
	_ = h.Aria(ariaAttr, "Close this dialog")

	_ = i18n.T("feed.create.title")
	_ = i18n.MustT("feed.create.subtitle")

	_ = fmt.Errorf("scheduler: run %d failed: %w", 1, nil)
	_ = errors.New("store: connection refused")
	log.Printf("worker: tick at %s", "now")
	panic("unreachable: invariant violated")

	return nil
}
