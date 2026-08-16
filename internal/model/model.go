// Package model holds the domain types shared by the store, the generators and
// the renderers.
//
// It deliberately depends on nothing else in this repository. Renderers, the
// store and the generation pipeline all speak these types, so putting behaviour
// here would couple three layers together through the one package they all
// import.
package model

import "time"

// FeedKind distinguishes the three sorts of feed (PLAN.md §1, §14.2).
type FeedKind string

const (
	// KindGenerative: the model is the source. Content is ours, permalinked to
	// us. The risk is repetition.
	KindGenerative FeedKind = "generative"
	// KindGrounded: the model is only an editor over fetched articles. The risk
	// is hallucinated URLs, prevented structurally in §9.6.
	KindGrounded FeedKind = "grounded"
	// KindAggregate: no generator at all; merges member feeds (§14.2).
	KindAggregate FeedKind = "aggregate"
)

// Origin records how an item came to exist.
type Origin string

const (
	OriginGenerated  Origin = "generated"
	OriginSampled    Origin = "sampled"
	OriginManual     Origin = "manual"
	OriginCorrection Origin = "correction"
)

// Feed is a published feed's identity and settings.
//
// The per-feed identity fields exist because ten feeds should not all unfurl
// with the same picture or claim the same author (§14.1).
type Feed struct {
	ID          int64
	Slug        string
	Title       string
	Description string
	Language    string
	Kind        FeedKind

	Author     string
	Copyright  string
	OGImage    string
	TTLMinutes int

	Enabled     bool
	Timezone    string
	LastBuiltAt time.Time

	// CreatedAt fixes this feed's Tag URI epoch. §5.1's guid embeds a year,
	// and it must be the year the FEED was created, not the current year:
	// deriving it from time.Now would change every guid in every feed on New
	// Year's Day, and a guid must be stable forever.
	CreatedAt time.Time

	Version int64
}

// Item is one published entry.
// ItemFormats is the stage-2 output: the same raw item, formatted once per
// output surface so each is optimized for how that surface actually
// renders (§9, two-stage generation, 2026-08-15). JSON tags are the
// persistence format (items.formats_json) — stable, additive-only.
//
// The mapping each renderer applies, with the raw-field fallback:
//
//	FeedHTML  -> content:encoded / atom content   (falls back to BodyHTML)
//	CardText  -> <description> / og:description,
//	             i.e. the Slack card              (falls back to SummaryText)
//	EmbedText -> the /embed/{slug} widget line    (falls back to SummaryText)
//	PageHTML  -> the item permalink page body     (falls back to BodyHTML)
type ItemFormats struct {
	FeedHTML  string `json:"feed_html,omitempty"`
	CardText  string `json:"card_text,omitempty"`
	EmbedText string `json:"embed_text,omitempty"`
	PageHTML  string `json:"page_html,omitempty"`
}

// Empty reports whether no variant is set at all — the "render everything
// from raw fields" state.
func (f ItemFormats) Empty() bool {
	return f.FeedHTML == "" && f.CardText == "" && f.EmbedText == "" && f.PageHTML == ""
}

// The four Render* accessors below are THE way a renderer reads item
// content: variant when stage 2 produced one, raw field otherwise. Putting
// the fallback here — rather than as an if at every render call site —
// means a renderer cannot accidentally prefer a raw field over its variant
// or, worse, a variant meant for a different surface.

// RenderCardText is the plain-text teaser surface: RSS <description>,
// atom <summary>, JSON Feed summary, og:description — what Slack's card
// shows (§5.5).
func (it Item) RenderCardText() string {
	if it.Formats.CardText != "" {
		return it.Formats.CardText
	}
	return it.SummaryText
}

// RenderFeedHTML is the rich body feed readers show: content:encoded and
// atom content.
func (it Item) RenderFeedHTML() string {
	if it.Formats.FeedHTML != "" {
		return it.Formats.FeedHTML
	}
	return it.BodyHTML
}

// RenderPageHTML is the item permalink page's body.
func (it Item) RenderPageHTML() string {
	if it.Formats.PageHTML != "" {
		return it.Formats.PageHTML
	}
	return it.BodyHTML
}

// RenderEmbedText is the one-line text the /embed/{slug} widget shows.
func (it Item) RenderEmbedText() string {
	if it.Formats.EmbedText != "" {
		return it.Formats.EmbedText
	}
	return it.SummaryText
}

type Item struct {
	ID     int64
	FeedID int64

	// ItemKey is an opaque ULID assigned once and never changed. The RSS guid
	// and the Atom id are both derived from it, so a key that changed on edit
	// would resurface the item as a duplicate in every subscriber's inbox
	// (§5.1). It is never derived from content.
	ItemKey string

	// ContentHash is for idempotency only — a repeated run must not publish the
	// same trivia question twice. Deliberately separate from identity.
	ContentHash string

	Title string

	// SummaryText is PLAIN TEXT. It becomes <description> and og:description.
	// Slack renders a snippet and mangles rich HTML, so markup lives in
	// BodyHTML (§5.5).
	SummaryText string

	// BodyHTML is the full HTML, emitted as content:encoded.
	BodyHTML string

	// AnswerHTML is trivia only. It must never reach SummaryText or
	// og:description, or the channel preview spoils the question (§5.5).
	AnswerHTML string

	// Formats holds the per-surface presentation variants stage 2 of
	// generation produced (§9, revised 2026-08-15). Every field is
	// optional: an empty variant means "render this surface from the raw
	// fields above", which is also the state of every item generated
	// before stage 2 existed, an item whose formatting call failed, and an
	// item edited after generation (editing clears variants rather than
	// serving stale ones).
	Formats ItemFormats

	// Link is the item's destination: our permalink for generative items, the
	// publisher's article for grounded ones.
	Link       string
	SourceName string

	// PublishedAt is unique within a feed and strictly increasing. Slack drops
	// items that share a timestamp, silently (§5.5).
	PublishedAt time.Time

	Tags   []string
	Origin Origin

	CreatedAt time.Time
	UpdatedAt time.Time
	EditedAt  time.Time
	DeletedAt time.Time

	Version int64
}

// IsDeleted reports whether the item has been soft-deleted. There is no hard
// delete: the permalink returns 410 forever and the guid is never reused
// (§12.4).
func (i Item) IsDeleted() bool { return !i.DeletedAt.IsZero() }

// HasAnswer reports whether this is a trivia item with a hidden answer.
func (i Item) HasAnswer() bool { return i.AnswerHTML != "" }

// Channel is everything a renderer needs to emit one feed document.
//
// It carries the resolved absolute URLs rather than a base URL plus rules,
// because working them out is the caller's job once, not each renderer's job
// three times — three implementations would be three chances to disagree.
type Channel struct {
	Feed Feed

	// SelfURL is this document's own absolute URL, for atom:link rel="self".
	SelfURL string
	// HTMLURL is the human-facing page for the feed.
	HTMLURL string
	// Host is the bare hostname used to build Tag URIs.
	Host string
	// TagYear is the year component of the Tag URI authority. Fixed per feed
	// and never recomputed from "now", or every guid would change on New Year.
	TagYear int

	Items []Item

	// BuildTime is what lastBuildDate and Atom's feed-level updated report.
	BuildTime time.Time

	// Generator identifies the software, e.g. "AnimeFeedFlux v0.0.2-dev".
	Generator string
	// DocsURL is the RSS spec URL for the <docs> element.
	DocsURL string
}

// NewestPublished returns the most recent PublishedAt among items, or
// BuildTime if there are none.
func (c Channel) NewestPublished() time.Time {
	newest := time.Time{}
	for _, it := range c.Items {
		if it.PublishedAt.After(newest) {
			newest = it.PublishedAt
		}
	}
	if newest.IsZero() {
		return c.BuildTime
	}
	return newest
}
