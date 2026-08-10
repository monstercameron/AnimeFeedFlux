// Package urlnorm canonicalises candidate and item URLs so that the link-integrity
// check in §9.6 of PLAN.md — comparing a model's output byte-for-byte against the
// fetched candidate set — is comparing like with like.
package urlnorm

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// ErrNotAbsolute is returned when raw has no scheme or no host. RSS has no
// base-URL mechanism (§5.1), so a relative or scheme-less URL can never be
// resolved by a subscriber and is rejected outright rather than silently
// passed through.
var ErrNotAbsolute = errors.New("urlnorm: URL is not absolute (missing scheme or host)")

// trackingParamPrefixes and trackingParamNames enumerate query parameters that
// identify the click/campaign, not the resource. They are stripped so the same
// article reached via different marketing links normalises to one URL (§9 step 4).
var trackingParamPrefixes = []string{"utm_"}

var trackingParamNames = map[string]struct{}{
	"fbclid":  {},
	"gclid":   {},
	"mc_cid":  {},
	"mc_eid":  {},
	"igshid":  {},
	"ref":     {},
	"ref_src": {},
}

func isTrackingParam(name string) bool {
	if _, ok := trackingParamNames[name]; ok {
		return true
	}
	for _, prefix := range trackingParamPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// Normalize canonicalises raw into a stable form: lowercased scheme and host,
// default ports removed, tracking query parameters stripped, remaining query
// parameters sorted deterministically, fragment dropped, and a trailing slash
// removed from a non-empty path.
//
// Normalize MUST be idempotent — Normalize(Normalize(u)) == Normalize(u) for
// every u — because §9.6 normalizes candidate URLs once at acquisition and
// compares the model's echoed link against them byte-for-byte. If a second
// pass could change the output (e.g. by re-adding or re-ordering something
// the first pass left alone), a value that is already normalized could still
// fail that comparison, and the failure would look like a hallucinated link
// rather than what it actually is: normalization drift. Every transformation
// below is therefore a projection onto a fixed point, not a one-way edit.
func Normalize(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("urlnorm: parse %q: %w", raw, err)
	}
	if u.Scheme == "" || u.Hostname() == "" {
		return "", fmt.Errorf("%w: %q", ErrNotAbsolute, raw)
	}

	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = normalizeHost(u.Scheme, u.Host)

	// Path case is left alone: unlike scheme/host, path segments can be
	// case-sensitive on the origin server. Trailing slashes are stripped down
	// to a fixed point (not just one) so a pathological multi-slash path
	// (e.g. "///") can't require a second normalization pass to settle.
	for len(u.Path) > 1 && strings.HasSuffix(u.Path, "/") {
		u.Path = strings.TrimSuffix(u.Path, "/")
	}

	if u.RawQuery != "" {
		values := u.Query()
		for name := range values {
			if isTrackingParam(name) {
				values.Del(name)
			}
		}
		u.RawQuery = encodeSortedQuery(values)
	}

	u.Fragment = ""
	u.RawFragment = ""

	return u.String(), nil
}

// normalizeHost lowercases the host and strips a default port for the scheme,
// so http://Example.com:80 and http://example.com converge on one value.
func normalizeHost(scheme, host string) string {
	hostname := host
	port := ""
	if i := strings.LastIndex(host, ":"); i != -1 {
		// Guard against bare IPv6 hosts (no brackets) where ':' is part of the
		// address, not a port separator. url.Parse always brackets IPv6 hosts
		// that carry a port, so a trailing ":<digits>" here is always a port.
		if maybePort := host[i+1:]; maybePort != "" && isAllDigits(maybePort) {
			hostname = host[:i]
			port = maybePort
		}
	}
	hostname = strings.ToLower(hostname)
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if port == "" {
		return hostname
	}
	return hostname + ":" + port
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// encodeSortedQuery renders values with keys (and, within a key, values)
// sorted, so two URLs differing only in original parameter order converge on
// the same normalized string. Empty input yields "" rather than url.Values{}
// zero-value formatting quirks.
func encodeSortedQuery(values url.Values) string {
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	first := true
	for _, k := range keys {
		vs := append([]string(nil), values[k]...)
		sort.Strings(vs)
		for _, v := range vs {
			if !first {
				b.WriteByte('&')
			}
			first = false
			b.WriteString(url.QueryEscape(k))
			b.WriteByte('=')
			b.WriteString(url.QueryEscape(v))
		}
	}
	return b.String()
}

// MustNormalize normalises raw and panics on error. It exists for tests and
// package-level constants, where an unnormalisable URL is a programmer error,
// not a runtime condition to handle.
func MustNormalize(raw string) string {
	n, err := Normalize(raw)
	if err != nil {
		panic(err)
	}
	return n
}

// Equal reports whether a and b normalise to the same URL. It returns false,
// not an error, if either fails to normalise — from the caller's perspective
// an unnormalisable URL is simply not equal to anything.
func Equal(a, b string) bool {
	na, err := Normalize(a)
	if err != nil {
		return false
	}
	nb, err := Normalize(b)
	if err != nil {
		return false
	}
	return na == nb
}
