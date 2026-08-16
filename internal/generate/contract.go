// Package generate re-validates model output in Go before it is allowed to
// become a model.Item (PLAN.md §9, §8).
//
// SchemaFlux's structured output guarantees that a Candidate has the right
// SHAPE — a string is a string, a slice is a slice. It guarantees nothing
// about whether the content is any good: a struct with a hallucinated URL in
// its Link field is perfectly typed and completely wrong. Every rule in this
// file is therefore ours to enforce, not delegated to the schema, and none
// of it is optional per §8's "do not conflate typed with valid".
package generate

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/sanitize"
	"github.com/monstercameron/AnimeFeedFlux/internal/urlnorm"
)

// Reason tokens are short, stable identifiers for why a candidate was
// rejected. They are grouped and counted on the run (PLAN.md §10,
// runs.reject_reasons_json), so they must never be full sentences — a
// sentence changes wording between commits and breaks the grouping.
const (
	ReasonInvalidUTF8          = "invalid_utf8"
	ReasonControlChars         = "control_chars"
	ReasonTitleRequired        = "title_required"
	ReasonTitleTooShort        = "title_too_short"
	ReasonTitleTooLong         = "title_too_long"
	ReasonTitleTrailingPunct   = "title_trailing_punctuation"
	ReasonSummaryRequired      = "summary_required"
	ReasonSummaryHardCap       = "summary_exceeds_hard_cap"
	ReasonSummaryContainsHTML  = "summary_contains_html"
	ReasonBodyRequired         = "body_html_required"
	ReasonBodyRelativeLink     = "body_html_relative_link"
	ReasonAnswerLeaked         = "answer_leaked_into_summary"
	ReasonTagsTooMany          = "tags_too_many"
	ReasonTagsNotLowercase     = "tags_not_lowercase"
	ReasonLinkRequiredGrounded = "link_required_grounded"
	ReasonLinkInvalid          = "link_invalid"
	ReasonLinkNotCandidate     = "link_not_in_candidate_set"
)

// Sentinel errors returned by CheckLink. Their Error() text is exactly the
// matching Reason token above, so Validate can map a CheckLink failure onto
// a Rejection without a second switch that could drift out of sync.
var (
	ErrLinkEmpty           = errors.New(ReasonLinkRequiredGrounded)
	ErrLinkInvalid         = errors.New(ReasonLinkInvalid)
	ErrLinkNotInCandidates = errors.New(ReasonLinkNotCandidate)
)

const (
	titleMinLen    = 10
	titleMaxLen    = 200
	summaryHardCap = 500
	maxTags        = 6
)

// Candidate is the raw, model-produced value before Go-side revalidation.
// It mirrors the generation schema in PLAN.md §9 exactly, field for field,
// so a diff against that section catches drift immediately.
type Candidate struct {
	Title       string
	SummaryText string
	BodyHTML    string
	AnswerHTML  string // trivia only
	Link        string
	SourceName  string
	Tags        []string
}

// Options carries the per-run context Validate needs but a Candidate does
// not carry itself: which feed kind's rules apply, and — for grounded
// feeds — the candidate URL set the model's Link must match.
type Options struct {
	Kind model.FeedKind

	// CandidateURLs is the fetched-source set for grounded feeds (PLAN.md
	// §9 step 1). It is expected to already be normalized (step 1 says
	// normalization happens once, at acquisition); CheckLink normalizes it
	// again regardless, because Normalize is idempotent (urlnorm.go) and a
	// caller that forgot the earlier pass must not silently break.
	CandidateURLs []string
}

// Rejection records one broken rule. Field is informational (for a human
// reading the run log); Reason is the stable token that gets grouped and
// counted.
type Rejection struct {
	Field  string
	Reason string
}

func (r Rejection) String() string {
	return fmt.Sprintf("%s: %s", r.Field, r.Reason)
}

var trailingPunct = map[rune]bool{
	'.': true, ',': true, '!': true, '?': true, ';': true, ':': true,
}

// htmlTagRe matches anything that looks like a tag opening, used to reject
// markup that leaked into a field that must be plain text. It is
// deliberately loose (any '<' immediately followed by a letter or '/') —
// this is a rejection check, not a sanitizer, so a false positive just
// means a slightly-too-eager reject rather than a security hole; the
// asymmetric failure mode (false negative letting markup through) is the
// one that actually matters, and this pattern does not have it.
var htmlTagRe = regexp.MustCompile(`<\s*/?\s*[a-zA-Z]`)

// anchorHrefRe extracts href attribute values from raw (pre-sanitize) HTML.
// This intentionally duplicates a tiny slice of what sanitize.HTML already
// parses, because sanitize's tokenizer is unexported AND because sanitize's
// job is different from ours: sanitize FAILS OPEN INTO SILENCE — an
// href that fails safeHref is dropped and the <a> survives without one
// (package doc, "fail closed" refers to the markup, not to telling the
// caller). A relative href must instead REJECT THE WHOLE ITEM (§5.1: RSS
// has no base-URL mechanism), which only this package can decide, so the
// raw input has to be inspected before sanitize gets to it and quietly
// throws the evidence away.
var anchorHrefRe = regexp.MustCompile(`(?is)<a\b[^>]*?\bhref\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)

// stripTagsRe strips markup for the plain-text comparison CheckAnswerLeak
// needs. It is not a sanitizer — the result is only used for a substring
// check, never stored or rendered.
var stripTagsRe = regexp.MustCompile(`(?s)<[^>]*>`)

// Validate re-checks a model Candidate against every business rule in
// PLAN.md §9, sanitizes BodyHTML, and returns either a usable model.Item or
// the list of rules it broke. The returned error is reserved for conditions
// that indicate a caller/programmer mistake (an unrecognized Kind); a
// candidate that simply fails validation is reported via the Rejection
// slice, not err — that is the expected, everyday outcome the run counts.
func Validate(in Candidate, opts Options) (model.Item, []Rejection, error) {
	switch opts.Kind {
	case model.KindGenerative, model.KindGrounded, model.KindAggregate:
	default:
		return model.Item{}, nil, fmt.Errorf("generate: unknown feed kind %q", opts.Kind)
	}

	var rejections []Rejection
	reject := func(field, reason string) {
		rejections = append(rejections, Rejection{Field: field, Reason: reason})
	}

	// --- Encoding, before anything else ---
	//
	// Invalid UTF-8 or an XML-illegal control character must never reach a
	// renderer. Fuzzing found that both produced a feed no parser accepts —
	// escaping cannot save them, because there is no character reference for
	// U+0000 and XML 1.0 simply forbids it. JSON is worse in a quieter way:
	// encoding/json silently substitutes U+FFFD, so the subscriber sees mangled
	// text rather than an error anyone notices.
	//
	// The renderer strips these defensively as a last resort, but rejecting here
	// is what keeps the bad bytes out of the DATABASE, where they would
	// otherwise sit forever behind a permanent guid.
	for _, f := range []struct{ name, val string }{
		{"title", in.Title},
		{"summary_text", in.SummaryText},
		{"body_html", in.BodyHTML},
		{"answer_html", in.AnswerHTML},
	} {
		if !utf8.ValidString(f.val) {
			reject(f.name, ReasonInvalidUTF8)
		} else if strings.ContainsFunc(f.val, isForbiddenControl) {
			reject(f.name, ReasonControlChars)
		}
	}

	// --- Title ---
	title := strings.TrimSpace(in.Title)
	if title == "" {
		reject("title", ReasonTitleRequired)
	} else {
		n := utf8.RuneCountInString(title)
		if n < titleMinLen {
			reject("title", ReasonTitleTooShort)
		}
		if n > titleMaxLen {
			reject("title", ReasonTitleTooLong)
		}
		runes := []rune(title)
		if trailingPunct[runes[len(runes)-1]] {
			reject("title", ReasonTitleTrailingPunct)
		}
	}

	// --- SummaryText ---
	summary := strings.TrimSpace(in.SummaryText)
	if summary == "" {
		reject("summary_text", ReasonSummaryRequired)
	} else {
		if utf8.RuneCountInString(summary) > summaryHardCap {
			reject("summary_text", ReasonSummaryHardCap)
		}
		if htmlTagRe.MatchString(summary) {
			reject("summary_text", ReasonSummaryContainsHTML)
		}
	}

	// --- BodyHTML ---
	rawBody := in.BodyHTML
	var sanitizedBody string
	if strings.TrimSpace(rawBody) == "" {
		reject("body_html", ReasonBodyRequired)
	} else {
		for _, m := range anchorHrefRe.FindAllStringSubmatch(rawBody, -1) {
			href := firstNonEmpty(m[1], m[2], m[3])
			if !isAbsoluteURL(href) {
				reject("body_html", ReasonBodyRelativeLink)
				break // one is enough to fail the item; no need to pile on
			}
		}
		sanitizedBody = sanitize.HTML(rawBody)
	}

	// --- AnswerHTML / answer-leak (§5.5: Slack has no spoiler mechanism) ---
	//
	// AnswerHTML is sanitized on exactly the same terms as BodyHTML above, and
	// for exactly the same reason: both are model-authored markup, and both are
	// written into a document COMPLETELY UNESCAPED downstream — render/
	// permalink.go emits it inside <details>, render/rss.go concatenates it into
	// content:encoded. Until this call existed only BodyHTML was sanitized, so
	// `sanitize.HTML` had one call site in the entire tree and an `answer_html`
	// carrying <script> reached the public page and every subscriber's reader
	// verbatim. permalink.go's own doc comment claimed both fields "have already
	// been through the generation pipeline's sanitiser" — true of one, false of
	// the other, which is what made it invisible.
	//
	// The leak check below deliberately runs on the RAW value, not the
	// sanitized one: sanitize drops <script>/<style> text entirely, so checking
	// the sanitized form could miss an answer hidden in markup this package is
	// trying to catch leaking into the summary. Rejecting on the raw text is
	// the conservative direction (more rejections, never fewer).
	answerHTML := sanitize.HTML(in.AnswerHTML)
	if answerText := plainText(in.AnswerHTML); answerText != "" && summary != "" {
		if strings.Contains(strings.ToLower(summary), strings.ToLower(answerText)) {
			reject("summary_text", ReasonAnswerLeaked)
		}
	}
	// Same absolute-URL rule the body is held to, for the same §5.1 reason —
	// both land in content:encoded, and RSS has no base-URL mechanism, so a
	// relative href in an answer is exactly as unresolvable as one in a body.
	for _, m := range anchorHrefRe.FindAllStringSubmatch(in.AnswerHTML, -1) {
		href := firstNonEmpty(m[1], m[2], m[3])
		if !isAbsoluteURL(href) {
			reject("answer_html", ReasonBodyRelativeLink)
			break
		}
	}

	// --- Tags ---
	if len(in.Tags) > maxTags {
		reject("tags", ReasonTagsTooMany)
	}
	for _, t := range in.Tags {
		if t != strings.ToLower(t) {
			reject("tags", ReasonTagsNotLowercase)
			break
		}
	}

	// --- Link ---
	link := strings.TrimSpace(in.Link)
	switch opts.Kind {
	case model.KindGrounded:
		if err := CheckLink(link, opts.CandidateURLs); err != nil {
			reject("link", linkErrorReason(err))
		}
	default:
		// Optional for generative/aggregate; if present it is not held to
		// the candidate-set rule (there is no candidate set), but it still
		// has to be a real absolute URL to be usable at all.
		if link != "" && !isAbsoluteURL(link) {
			reject("link", ReasonLinkInvalid)
		}
	}

	if len(rejections) > 0 {
		return model.Item{}, rejections, nil
	}

	return model.Item{
		Title:       title,
		SummaryText: summary,
		BodyHTML:    sanitizedBody,
		AnswerHTML:  answerHTML,
		Link:        link,
		SourceName:  strings.TrimSpace(in.SourceName),
		Tags:        in.Tags,
	}, nil, nil
}

// CheckLink implements PLAN.md §9.6's grounded link-integrity gate: link
// must be byte-equal, after normalization, to a normalized URL in
// candidates. Both sides go through urlnorm.Normalize — the SAME function —
// because §9.6 is explicit that normalizing asymmetrically (e.g. trusting
// candidates were already normalized while re-normalizing only the model's
// output, or vice versa) silently rejects a faithful echo of a real
// candidate and looks exactly like the model hallucinating a link when the
// bug is ours.
func CheckLink(link string, candidates []string) error {
	if link == "" {
		return ErrLinkEmpty
	}
	normLink, err := urlnorm.Normalize(link)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrLinkInvalid, err)
	}
	for _, c := range candidates {
		normC, err := urlnorm.Normalize(c)
		if err != nil {
			// An unnormalizable candidate can never legitimately match
			// anything; skip it rather than fail the whole check.
			continue
		}
		if normLink == normC {
			return nil
		}
	}
	return fmt.Errorf("%w: %q", ErrLinkNotInCandidates, link)
}

// linkErrorReason maps a CheckLink error back onto its stable reason token.
// CheckLink's errors are wrapped with extra detail (the offending URL) for
// human logs, so this unwraps rather than using err.Error() directly, which
// would otherwise leak that detail into the reason token and break the
// grouping/counting in runs.reject_reasons_json (PLAN.md §10).
func linkErrorReason(err error) string {
	switch {
	case errors.Is(err, ErrLinkEmpty):
		return ReasonLinkRequiredGrounded
	case errors.Is(err, ErrLinkNotInCandidates):
		return ReasonLinkNotCandidate
	case errors.Is(err, ErrLinkInvalid):
		return ReasonLinkInvalid
	default:
		return ReasonLinkInvalid
	}
}

func isAbsoluteURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return u.Scheme != "" && u.Host != ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// plainText strips markup for the answer-leak substring check. It is
// deliberately crude — it does not decode entities — because it only needs
// to catch the common case (the model writing the answer as plain text
// inside <p>answer</p>), and an under-detection here fails toward the safer
// side of a human catching it in the Sampler UI (§12.3), not toward
// silently corrupting stored content.
func plainText(htmlStr string) string {
	return strings.TrimSpace(stripTagsRe.ReplaceAllString(htmlStr, ""))
}

// isForbiddenControl reports whether r is a control character XML 1.0 cannot
// carry. Tab, newline and carriage return are the three that are allowed.
func isForbiddenControl(r rune) bool {
	// 0x09 tab, 0x0A newline, 0x0D carriage return are the only control
	// characters XML 1.0 permits. Written as codepoints rather than escapes so
	// the intent survives a copy-paste.
	if r == 0x09 || r == 0x0A || r == 0x0D {
		return false
	}
	return r < 0x20 || (r >= 0x7F && r <= 0x9F) || r == 0xFFFE || r == 0xFFFF
}
