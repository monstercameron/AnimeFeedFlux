package sources

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Server-side request forgery guards for the source fetcher.
//
// fetchOnce built a request straight from the stored target and handed it to
// an unconfigured http.Client, so nothing stopped a fetch reaching
// http://169.254.169.254/ (the cloud metadata service) or
// http://127.0.0.1:9311/ (the admin bridge itself). Redirects are what made it
// more than an operator shooting themselves: Go's default client follows up to
// ten, so a source URL the operator DOES trust can 302 the fetcher anywhere,
// and the body flows into the grounding path and can surface in a published
// feed (A8-41).
//
// Every check below therefore runs on the ORIGINAL target and again on every
// redirect hop. Checking only the first URL is the classic version of this bug.

// DefaultFetchTimeout bounds a single fetch, redirects included. No
// construction site set http.Client.Timeout, so one slow upstream could stall
// a run for as long as it liked.
const DefaultFetchTimeout = 30 * time.Second

// maxRedirects is Go's own default, restated because the CheckRedirect hook
// below replaces the default policy and would otherwise remove the limit.
const maxRedirects = 10

// ErrBlockedTarget is returned for a URL the fetcher refuses to request.
var ErrBlockedTarget = errors.New("sources: blocked target")

// checkTarget reports whether u may be fetched, on the URL alone.
//
// It does NOT resolve the host. Two reasons: a DNS lookup here would put a
// network call in the default `go test ./...`, which RULE-1 forbids; and
// resolving here and connecting later is a time-of-check/time-of-use gap a
// rebinding record walks straight through. The address is therefore checked
// where it is actually used — at dial time, by GuardedClient's dialer, on the
// very address the connection goes to.
//
// The scheme allowlist is absolute and belongs here: file:// would read the
// server's disk, and gopher:// and friends have been used to smuggle
// arbitrary bytes at other services. An IP LITERAL is also checked here, so
// an obviously-blocked target is refused before any connection is attempted.
func checkTarget(u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%w: scheme %q is not http or https", ErrBlockedTarget, u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("%w: no host", ErrBlockedTarget)
	}
	if ip := net.ParseIP(host); ip != nil {
		if err := checkIP(ip); err != nil {
			return fmt.Errorf("%w: %s is %v", ErrBlockedTarget, ip, err)
		}
	}
	return nil
}

// checkIP rejects the address ranges that are not "somewhere on the internet".
//
// Loopback covers the admin bridge on 127.0.0.1; link-local unicast covers
// 169.254.169.254, which is where every major cloud serves instance
// credentials; private and unique-local cover the rest of the internal
// network the droplet can see.
func checkIP(ip net.IP) error {
	switch {
	case ip.IsLoopback():
		return errors.New("loopback")
	case ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast():
		return errors.New("link-local (cloud metadata lives here)")
	case ip.IsPrivate():
		return errors.New("private")
	case ip.IsUnspecified():
		return errors.New("unspecified")
	case ip.IsMulticast():
		return errors.New("multicast")
	case ip.IsInterfaceLocalMulticast():
		return errors.New("interface-local multicast")
	}
	return nil
}

// GuardedClient returns an http.Client that refuses blocked targets on every
// redirect hop, checks the resolved address at dial time, and bounds the whole
// exchange.
//
// The dialer is where the address check belongs. It sees the address the
// connection is actually being made to, after resolution, which closes both
// the "name resolves to a private address" case and the DNS-rebinding variant
// where a name answers publicly once and privately on the next lookup.
//
// CheckRedirect matters just as much: Go follows up to ten redirects by
// default, so a source URL the operator DOES trust can 302 the fetcher at the
// metadata service, and by then the URL under test is not the configured one.
func GuardedClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = DefaultFetchTimeout
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("sources: stopped after %d redirects", maxRedirects)
			}
			return checkTarget(req.URL)
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, _, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				if ip := net.ParseIP(host); ip != nil {
					if cerr := checkIP(ip); cerr != nil {
						return nil, fmt.Errorf("%w: %s is %v", ErrBlockedTarget, ip, cerr)
					}
				}
				return dialer.DialContext(ctx, network, addr)
			},
		},
	}
}

// blockedTargetMessage is used by fetch.go to keep a refusal legible in a run
// log without leaking the whole resolved address list into a feed.
func blockedTargetMessage(target string, err error) string {
	return strings.TrimSpace(fmt.Sprintf("refused to fetch %s: %v", target, err))
}
