package render

import (
	"strconv"
	"strings"
)

// TagURI builds the Tag URI used as the RSS guid (isPermaLink="false") and the
// Atom id, byte-identical between the two (PLAN.md §5.1, §5.2). Atom consumers
// compare ids character-by-character and case-sensitively, and a guid must be
// stable forever, so both formats are pointed at this single function rather
// than each deriving its own string: one code path means "the guid never
// changes" is true by construction, not by two implementations staying in
// sync by discipline.
//
// itemKey is expected to be an opaque, randomly-assigned ULID (uppercase
// Crockford base32) and is never case-folded — folding it would make the
// RSS guid and Atom id diverge from the value actually stored with the item.
func TagURI(host string, year int, slug string, itemKey string) string {
	host = stripHostDecoration(host)
	var b strings.Builder
	b.WriteString("tag:")
	b.WriteString(host)
	b.WriteByte(',')
	b.WriteString(strconv.Itoa(year))
	b.WriteByte(':')
	b.WriteString(slug)
	b.WriteByte('/')
	b.WriteString(itemKey)
	return b.String()
}

// stripHostDecoration removes a leading scheme and a trailing slash, so
// "https://anime.earlcameron.com/" and "anime.earlcameron.com" produce the
// same tag authority — callers should not have to pre-clean config values.
func stripHostDecoration(host string) string {
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	host = strings.TrimSuffix(host, "/")
	return host
}
