package testutil

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// StaticResponse is one canned HTTP response, keyed by request URL in
// StaticClient.
type StaticResponse struct {
	Status int
	Header http.Header
	Body   []byte
}

// staticTransport serves StaticResponse values from an in-memory map,
// keyed by the request's full URL string.
type staticTransport struct {
	responses map[string]StaticResponse
}

// RoundTrip implements http.RoundTripper. An unmapped URL is a test bug, not
// a network condition, so it fails loudly with the URL in the error rather
// than falling through to whatever the real network would do — a silently
// missing fixture returning an unexpected 404 is a worse failure mode than a
// crash naming exactly what's missing (RULE-1: no network in tests).
func (t *staticTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	key := req.URL.String()
	resp, ok := t.responses[key]
	if !ok {
		return nil, fmt.Errorf("testutil: StaticClient has no fixture for URL %q (registered: %v)", key, mapKeys(t.responses))
	}

	status := resp.Status
	if status == 0 {
		status = http.StatusOK
	}
	header := resp.Header
	if header == nil {
		header = http.Header{}
	}

	return &http.Response{
		StatusCode:    status,
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        header.Clone(),
		Body:          io.NopCloser(bytes.NewReader(resp.Body)),
		ContentLength: int64(len(resp.Body)),
		Request:       req,
	}, nil
}

func mapKeys(m map[string]StaticResponse) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// StaticClient returns an *http.Client whose Transport serves the given
// canned responses keyed by exact request URL. This is the form most tests
// want: build the map inline next to the test that uses it, no fixture
// files to keep in sync.
func StaticClient(responses map[string]StaticResponse) *http.Client {
	return &http.Client{Transport: &staticTransport{responses: responses}}
}

// fileTransport serves responses read from files in dir, one file per
// distinct request URL.
type fileTransport struct {
	dir string
}

// fixtureFilename maps a request URL to a filename deterministically via the
// hex SHA-256 of the URL string, suffixed ".http". A hash (rather than a
// sanitised copy of the URL) sidesteps every OS path-length and
// reserved-character restriction a long query string could hit, at the cost
// of the filename not being human-readable — acceptable since fixtures are
// generated and referenced by URL in test code, never browsed by name.
func fixtureFilename(url string) string {
	sum := sha256.Sum256([]byte(url))
	return hex.EncodeToString(sum[:]) + ".http"
}

// RoundTrip implements http.RoundTripper, reading the fixture file for the
// request's URL and failing loudly, naming the URL, if it isn't there
// (RULE-1: no network in tests, and no silent wrong-status fallback).
func (t *fileTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	key := req.URL.String()
	path := filepath.Join(t.dir, fixtureFilename(key))

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("testutil: FileClient has no fixture for URL %q (looked for %s): %w", key, path, err)
	}
	defer f.Close()

	status, header, body, err := parseFixture(f)
	if err != nil {
		return nil, fmt.Errorf("testutil: parsing fixture %s for URL %q: %w", path, key, err)
	}

	return &http.Response{
		StatusCode:    status,
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}, nil
}

// parseFixture reads the documented fixture file format:
//
//	Status: 200
//	Header-Name: value
//	Header-Name-2: value
//	<blank line>
//	<body, verbatim to EOF>
//
// The Status line and every header line are optional — a fixture containing
// only a body (no leading blank-terminated block at all) defaults to 200
// with no extra headers, so the common case ("just serve this XML") needs
// no header block.
func parseFixture(r io.Reader) (status int, header http.Header, body []byte, err error) {
	status = http.StatusOK
	header = http.Header{}

	br := bufio.NewReader(r)
	peek, err := br.Peek(64)
	if err != nil && err != io.EOF {
		return 0, nil, nil, err
	}
	if !looksLikeHeaderBlock(peek) {
		rest, rerr := io.ReadAll(br)
		if rerr != nil {
			return 0, nil, nil, rerr
		}
		return status, header, rest, nil
	}

	for {
		line, rerr := br.ReadString('\n')
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" {
			break
		}
		name, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			return 0, nil, nil, fmt.Errorf("malformed header line %q", trimmed)
		}
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if strings.EqualFold(name, "Status") {
			status, err = strconv.Atoi(value)
			if err != nil {
				return 0, nil, nil, fmt.Errorf("malformed Status value %q: %w", value, err)
			}
		} else {
			header.Add(name, value)
		}
		if rerr == io.EOF {
			break
		}
	}

	rest, rerr := io.ReadAll(br)
	if rerr != nil {
		return 0, nil, nil, rerr
	}
	return status, header, rest, nil
}

// looksLikeHeaderBlock reports whether the first line of a fixture file
// looks like "Name: value" — the shape every header-block line takes — so a
// plain-body fixture with no header block (the common case) is never
// misparsed as one just because its first line happens to contain a colon
// deeper into a URL or similar. It only inspects the first line, deliberately
// conservative: false positives would eat real body content as headers.
func looksLikeHeaderBlock(peek []byte) bool {
	firstLine := peek
	if i := bytes.IndexByte(peek, '\n'); i >= 0 {
		firstLine = peek[:i]
	}
	firstLine = bytes.TrimRight(firstLine, "\r")
	name, _, ok := bytes.Cut(firstLine, []byte(":"))
	if !ok {
		return false
	}
	name = bytes.TrimSpace(name)
	if len(name) == 0 {
		return false
	}
	for _, c := range name {
		if c == ' ' || c == '\t' {
			return false
		}
	}
	return true
}

// FileClient returns an *http.Client whose Transport serves responses from
// files in dir, one file per distinct request URL, named by
// fixtureFilename(url) — see its doc for the exact rule. Use StaticClient
// instead when a test only needs a handful of inline responses; FileClient
// is for suites that want fixtures checked in as reviewable files (e.g. a
// captured real upstream payload).
func FileClient(dir string) *http.Client {
	return &http.Client{Transport: &fileTransport{dir: dir}}
}
