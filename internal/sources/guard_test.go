package sources

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// fetchOnce built a request straight from the stored target and handed it to
// an unconfigured client, so nothing stopped a fetch reaching the cloud
// metadata service or the admin bridge on loopback (A8-41).
//
// No test here resolves a name or opens a socket: checkTarget works on the URL
// alone and checkIP on a literal, which is also why the address check lives in
// the dialer rather than in a DNS lookup (RULE-1).

func TestCheckTargetRejectsNonHTTPSchemes(t *testing.T) {
	for _, raw := range []string{
		"file:///etc/passwd",
		"gopher://example.com/",
		"ftp://example.com/feed.xml",
		"data:text/plain,hello",
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		if err := checkTarget(u); !errors.Is(err, ErrBlockedTarget) {
			t.Errorf("%q was allowed (err=%v)", raw, err)
		}
	}
}

func TestCheckTargetRejectsBlockedIPLiterals(t *testing.T) {
	for _, raw := range []string{
		"http://127.0.0.1:9311/",   // the admin bridge
		"http://169.254.169.254/",  // cloud instance metadata
		"http://10.0.0.5/internal", // private
		"http://192.168.1.1/",      // private
		"http://172.16.0.1/",       // private
		"http://[::1]:9311/",       // loopback, v6
		"http://[fd00::1]/",        // unique-local, v6
		"http://0.0.0.0/",          // unspecified
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		if err := checkTarget(u); !errors.Is(err, ErrBlockedTarget) {
			t.Errorf("%q was allowed (err=%v)", raw, err)
		}
	}
}

func TestCheckTargetAllowsOrdinaryFeeds(t *testing.T) {
	for _, raw := range []string{
		"https://example.com/feed.xml",
		"http://news.example.org/rss",
		"https://93.184.216.34/feed.xml", // a public literal
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		if err := checkTarget(u); err != nil {
			t.Errorf("%q was refused: %v", raw, err)
		}
	}
}

// checkIP is what the dialer calls on the address a connection is actually
// going to — the check that closes both "a name resolves to a private
// address" and the rebinding variant.
func TestCheckIPClassifies(t *testing.T) {
	blocked := []string{"127.0.0.1", "::1", "169.254.169.254", "10.1.2.3", "192.168.0.1", "172.20.0.1", "fd12::1", "0.0.0.0", "224.0.0.1"}
	for _, s := range blocked {
		if err := checkIP(net.ParseIP(s)); err == nil {
			t.Errorf("%s was allowed", s)
		}
	}
	for _, s := range []string{"93.184.216.34", "1.1.1.1", "2606:4700::1111"} {
		if err := checkIP(net.ParseIP(s)); err != nil {
			t.Errorf("%s was refused: %v", s, err)
		}
	}
}

// A trusted upstream redirecting at a blocked address is the case that makes
// this more than an operator shooting themselves: Go follows up to ten
// redirects, so a source the operator DOES trust can 302 the fetcher anywhere.
func TestGuardedClientRefusesRedirectToBlockedTarget(t *testing.T) {
	c := GuardedClient(0)
	if c.CheckRedirect == nil {
		t.Fatal("GuardedClient has no redirect policy, so a 302 goes anywhere")
	}
	u, err := url.Parse("http://169.254.169.254/latest/meta-data/")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.CheckRedirect(&http.Request{URL: u}, nil); !errors.Is(err, ErrBlockedTarget) {
		t.Errorf("a redirect to the metadata service was allowed (err=%v)", err)
	}
}

// Replacing Go's default policy would also remove its hop limit, so the limit
// is restated and has to hold.
func TestGuardedClientStopsAfterTenRedirects(t *testing.T) {
	c := GuardedClient(0)
	u, err := url.Parse("https://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	via := make([]*http.Request, maxRedirects)
	for i := range via {
		via[i] = &http.Request{URL: u}
	}
	err = c.CheckRedirect(&http.Request{URL: u}, via)
	if err == nil || !strings.Contains(err.Error(), "stopped after") {
		t.Errorf("the redirect chain was not bounded: %v", err)
	}
}

func TestGuardedClientHasATimeout(t *testing.T) {
	if GuardedClient(0).Timeout != DefaultFetchTimeout {
		t.Error("GuardedClient has no timeout; one slow upstream stalls a run indefinitely")
	}
}
