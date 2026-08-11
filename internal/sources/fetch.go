package sources

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/monstercameron/AnimeFeedFlux/internal/obs"
	"github.com/monstercameron/AnimeFeedFlux/internal/urlnorm"
)

// Fetcher retrieves upstream feed bodies over conditional GET, politely
// (§9.1: "conditional GET against *them* too, storing their ETag/Last-
// Modified") and defensively (§4: body size cap).
type Fetcher struct {
	// Client performs the request. Tests inject testutil.StaticClient so no
	// call ever touches the network (RULE-1).
	Client *http.Client
	// MaxBytes caps the response body. A feed that lies about or omits
	// Content-Length must not be trusted to self-limit, so the cap is
	// enforced by io.LimitReader regardless of what the server claims.
	MaxBytes int64
}

// Result carries what Fetch learned about one upstream URL.
type Result struct {
	Body         []byte
	ETag         string
	LastModified string
	StatusCode   int
	// NotModified is true on a 304 response: the caller's cached copy is
	// still current and Body is empty. §5.4/§9.1's conditional-GET discipline
	// exists so a reader's relentless polling never touches the LLM or,
	// here, a full re-parse of an unchanged upstream feed.
	NotModified bool
}

// Fetch retrieves url, sending If-None-Match / If-Modified-Since when the
// caller supplies a previous ETag / Last-Modified — we are polite to
// upstream feeds for the same reason §5.4 wants readers to be polite to us:
// a conditional GET that upstream can answer with a bare 304 costs it
// nothing, while an unconditional GET forces it to regenerate and resend a
// body we already have.
// Fetch's own network call gets a "sources.fetch" span (PLAN.md §15.0a /
// TODOS A6-17): grounded feeds fetch upstream articles, and that fetch is
// the one part of a grounded run with no visibility otherwise -- a slow or
// failing upstream is invisible until a feed goes stale.
//
// Attributes are deliberately bounded: "host" (never the full URL, which is
// unbounded by construction -- a query string or path segment can carry
// anything an upstream publisher put there), the HTTP status code, and a
// closed-set outcome via obs.Outcome. The full URL never reaches a span
// attribute here, matching the cardinality discipline obs.CardinalityGuard
// enforces for metric labels (see internal/obs/metrics_test.go's
// TestCardinalityGuardRejectsUnboundedValue and
// TestLooksUnboundedHeuristics' "url" case) even though span attributes
// themselves are not run through that guard.
//
// Fetch never parses, so its span carries no item count -- item count is
// FetchCandidates' to report (A6-17: "PARTIAL" note on that ticket). The
// network work itself lives in fetchOnce so both Fetch and FetchCandidates
// can wrap it in their own span, each annotated with what that caller
// actually knows: Fetch closes its span the instant the HTTP round trip
// (and body read) is done, with no item count, because it has not parsed
// anything yet; FetchCandidates keeps its span open through Parse so the
// same "sources.fetch" span carries the item count too, instead of ending
// before parsing happens and leaving item count permanently unattached.
func (f *Fetcher) Fetch(ctx context.Context, target string, prevETag, prevModified string) (Result, error) {
	ctx, span := obs.Start(ctx, "sources.fetch", obs.KindRun)
	defer span.End()

	result, statusCode, outcome, err := f.fetchOnce(ctx, target, prevETag, prevModified)
	annotateFetchSpan(span, target, statusCode, outcome, -1, err)
	return result, err
}

// fetchOnce performs the conditional-GET network call target, with no span
// of its own -- span ownership belongs to the caller (Fetch or
// FetchCandidates), since only the caller knows what else (if anything)
// happens in the same unit of work before the span should end.
func (f *Fetcher) fetchOnce(ctx context.Context, target string, prevETag, prevModified string) (result Result, statusCode int, outcome obs.Outcome, err error) {
	outcome = obs.OutcomeFailed

	// Checked BEFORE the request, and again on every redirect hop by the
	// client's CheckRedirect (see guard.go). Checking only here would leave
	// the interesting half open: a trusted upstream can 302 the fetcher at the
	// metadata service or the admin bridge, and by then the URL under test is
	// not the one the operator configured (A8-41).
	parsed, err := url.Parse(target)
	if err != nil {
		return Result{}, 0, outcome, fmt.Errorf("sources: parsing %q: %w", target, err)
	}
	if err := checkTarget(parsed); err != nil {
		return Result{}, 0, outcome, errors.New(blockedTargetMessage(target, err))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return Result{}, 0, outcome, fmt.Errorf("sources: building request for %q: %w", target, err)
	}
	if prevETag != "" {
		req.Header.Set("If-None-Match", prevETag)
	}
	if prevModified != "" {
		req.Header.Set("If-Modified-Since", prevModified)
	}

	resp, err := f.Client.Do(req)
	if err != nil {
		return Result{}, 0, outcome, fmt.Errorf("sources: fetching %q: %w", target, err)
	}
	defer resp.Body.Close()
	statusCode = resp.StatusCode

	result = Result{
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		StatusCode:   resp.StatusCode,
	}

	if resp.StatusCode == http.StatusNotModified {
		result.NotModified = true
		outcome = obs.OutcomeSuccess
		return result, statusCode, outcome, nil
	}

	max := f.MaxBytes
	if max <= 0 {
		max = MaxParseBytes
	}
	// Read one byte past the cap so a body that is exactly at the limit is
	// distinguishable from one that exceeds it.
	limited := io.LimitReader(resp.Body, max+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return Result{}, statusCode, outcome, fmt.Errorf("sources: reading body of %q: %w", target, err)
	}
	if int64(len(body)) > max {
		err = fmt.Errorf("%w: %q exceeded %d bytes", ErrBodyTooLarge, target, max)
		return Result{}, statusCode, outcome, err
	}

	result.Body = body
	outcome = obs.OutcomeSuccess
	return result, statusCode, outcome, nil
}

// annotateFetchSpan sets the bounded attributes every sources.fetch span
// carries. items < 0 means "no count to report" (Fetch's case -- it never
// parses); items >= 0 attaches the item count FetchCandidates alone knows,
// which is the whole point of A6-17: a conditional GET that never actually
// revalidates is invisible without knowing whether it produced items.
func annotateFetchSpan(span oteltrace.Span, target string, statusCode int, outcome obs.Outcome, items int, err error) {
	attrs := []attribute.KeyValue{
		attribute.String("host", fetchHost(target)),
		attribute.String(obs.FieldOutcome, string(outcome)),
	}
	if statusCode != 0 {
		attrs = append(attrs, attribute.Int(obs.FieldStatus, statusCode))
	}
	if items >= 0 {
		attrs = append(attrs, attribute.Int("items", items))
	}
	span.SetAttributes(attrs...)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
}

// fetchHost extracts the bounded host component from target for the
// sources.fetch span, never the full URL (this file's Fetch doc comment).
// An unparsable target still gets a stable, bounded token rather than the
// raw (potentially unbounded) input string leaking through.
func fetchHost(target string) string {
	u, err := url.Parse(target)
	if err != nil || u.Host == "" {
		return "unknown"
	}
	return u.Host
}

// FetchCandidates fetches url, parses it with Parse, and normalizes every
// candidate's URL exactly once here, at fetch time.
//
// This is load-bearing (PLAN.md §9 step 1 / §9.6): the grounded link-
// integrity check later compares the model's echoed link byte-for-byte
// against the fetched candidate set. If candidates were left carrying
// utm_/fbclid tracking parameters while the model's output got stripped of
// them downstream, a perfectly faithful echo of a real candidate would fail
// that byte-equality check and be silently dropped — starving the news feed
// while looking exactly like the model hallucinating a link. Normalizing
// once, here, at the single point every candidate URL is minted, makes that
// asymmetry structurally impossible rather than a discipline to remember at
// every call site.
//
// FetchCandidates keeps its own "sources.fetch" span open through Parse
// (A6-17), unlike a bare Fetch call: item count is only known once parsing
// has happened, and Fetch's span closes before that. Using fetchOnce here
// instead of calling Fetch means exactly one "sources.fetch" span is
// produced per call, carrying url/status/304-vs-200/item-count together --
// not two spans with the count permanently missing from either.
func (f *Fetcher) FetchCandidates(ctx context.Context, url, sourceName, prevETag, prevModified string) ([]Candidate, Result, error) {
	ctx, span := obs.Start(ctx, "sources.fetch", obs.KindRun)
	statusCode := 0
	outcome := obs.OutcomeFailed
	items := -1 // stays -1 (not reported) unless we reach a count below.
	var spanErr error
	defer func() {
		annotateFetchSpan(span, url, statusCode, outcome, items, spanErr)
		span.End()
	}()

	res, sc, oc, err := f.fetchOnce(ctx, url, prevETag, prevModified)
	statusCode, outcome = sc, oc
	if err != nil {
		spanErr = err
		return nil, res, err
	}
	if res.NotModified {
		items = 0
		return nil, res, nil
	}

	cands, err := Parse(res.Body, sourceName)
	if err != nil {
		spanErr = err
		return nil, res, err
	}

	normalized := make([]Candidate, 0, len(cands))
	for _, c := range cands {
		if c.URL == "" {
			continue
		}
		n, err := urlnorm.Normalize(c.URL)
		if err != nil {
			// A candidate with an unnormalizable (relative/scheme-less) URL
			// can never be linked to from RSS (§5.1: "no relative URLs
			// anywhere") and can never satisfy byte-equality against
			// anything, so it is dropped rather than passed through broken.
			continue
		}
		c.URL = n
		normalized = append(normalized, c)
	}
	items = len(normalized)
	return normalized, res, nil
}

// Dedupe drops candidates whose URL normalizes to one already seen, keeping
// the first occurrence. Candidates are expected to already carry normalized
// URLs (via FetchCandidates), but Dedupe normalizes defensively so a caller
// that hands it raw candidates still gets tracking-parameter-insensitive
// deduplication rather than a subtly wrong result.
func Dedupe(cands []Candidate) []Candidate {
	seen := make(map[string]struct{}, len(cands))
	out := make([]Candidate, 0, len(cands))
	for _, c := range cands {
		key, err := urlnorm.Normalize(c.URL)
		if err != nil {
			key = c.URL
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, c)
	}
	return out
}
