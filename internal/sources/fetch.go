package sources

import (
	"context"
	"fmt"
	"io"
	"net/http"

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
func (f *Fetcher) Fetch(ctx context.Context, url string, prevETag, prevModified string) (Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Result{}, fmt.Errorf("sources: building request for %q: %w", url, err)
	}
	if prevETag != "" {
		req.Header.Set("If-None-Match", prevETag)
	}
	if prevModified != "" {
		req.Header.Set("If-Modified-Since", prevModified)
	}

	resp, err := f.Client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("sources: fetching %q: %w", url, err)
	}
	defer resp.Body.Close()

	result := Result{
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		StatusCode:   resp.StatusCode,
	}

	if resp.StatusCode == http.StatusNotModified {
		result.NotModified = true
		return result, nil
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
		return Result{}, fmt.Errorf("sources: reading body of %q: %w", url, err)
	}
	if int64(len(body)) > max {
		return Result{}, fmt.Errorf("%w: %q exceeded %d bytes", ErrBodyTooLarge, url, max)
	}

	result.Body = body
	return result, nil
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
func (f *Fetcher) FetchCandidates(ctx context.Context, url, sourceName, prevETag, prevModified string) ([]Candidate, Result, error) {
	res, err := f.Fetch(ctx, url, prevETag, prevModified)
	if err != nil {
		return nil, res, err
	}
	if res.NotModified {
		return nil, res, nil
	}

	cands, err := Parse(res.Body, sourceName)
	if err != nil {
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
