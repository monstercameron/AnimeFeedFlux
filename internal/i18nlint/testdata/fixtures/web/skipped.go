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

	_ = i18n.T("feed.create.title")
	_ = i18n.MustT("feed.create.subtitle")

	_ = fmt.Errorf("scheduler: run %d failed: %w", 1, nil)
	_ = errors.New("store: connection refused")
	log.Printf("worker: tick at %s", "now")
	panic("unreachable: invariant violated")

	return nil
}
