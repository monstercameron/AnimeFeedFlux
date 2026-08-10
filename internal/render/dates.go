package render

import "time"

// rfc822Layout is time.RFC1123Z with the weekday's trailing comma dropped in
// favor of an explicit one, matching the RSS Advisory Board's Best Practices
// Profile example ("Thu, 04 Oct 2007 23:59:45 +0000") exactly: a four-digit
// year, no military timezone abbreviation (which RFC1123 would print for a
// non-UTC input via "MST"), and the numeric "+0000" offset always spelled
// out rather than "UTC" or "Z".
const rfc822Layout = "Mon, 02 Jan 2006 15:04:05 -0700"

// RFC822 renders t as an RSS 2.0 pubDate/lastBuildDate. It always converts
// to UTC first, so the offset is always literally "+0000" and never a
// three-letter zone abbreviation — those are explicitly disallowed by the
// profile because they're ambiguous (e.g. "CST" means different things on
// different continents).
func RFC822(t time.Time) string {
	return t.UTC().Format(rfc822Layout)
}

// rfc3339Layout mirrors time.RFC3339 but is spelled out explicitly rather
// than reused, so this file has no accidental coupling to the standard
// library's layout constant if that ever changes shape.
const rfc3339Layout = "2006-01-02T15:04:05Z07:00"

// RFC3339 renders t as an Atom `updated`/`published` or JSON Feed
// `date_published`/`date_modified` value. It always converts to UTC first:
// Go's "Z07:00" verb prints a numeric "+00:00" offset for a UTC time unless
// the time's location is literally UTC, in which case it prints "Z" — so
// normalizing to UTC here is what guarantees the required uppercase "Z"
// rather than "+00:00".
func RFC3339(t time.Time) string {
	return t.UTC().Format(rfc3339Layout)
}
