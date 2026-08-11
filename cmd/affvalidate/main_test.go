package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validRSS is a minimal, clean RSS 2.0 document that passes every rule in
// internal/feedvalidate.RSS, including the Slack-compatibility ones (§5.5):
// a date tag on every item, strictly descending and unique pubDates, and a
// plain-text description. Tests below mutate a copy of this to exercise one
// failure mode at a time, the same way internal/feedvalidate's own tests do,
// rather than depending on any golden file under internal/render (which this
// package must stay independent of — see internal/feedvalidate's package
// doc).
const validRSS = `<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom">
  <channel>
    <title>Trivia Daily</title>
    <link>https://feeds.example.com/trivia-daily</link>
    <description>One anime trivia question a day.</description>
    <atom:link rel="self" type="application/rss+xml" href="https://feeds.example.com/trivia-daily.xml"/>
    <item>
      <title>Item 2</title>
      <link>https://feeds.example.com/items/2</link>
      <description>Summary two.</description>
      <guid isPermaLink="false">tag:feeds.example.com,2026:trivia-daily/2</guid>
      <pubDate>Sun, 09 Aug 2026 12:00:02 +0000</pubDate>
    </item>
    <item>
      <title>Item 1</title>
      <link>https://feeds.example.com/items/1</link>
      <description>Summary one.</description>
      <guid isPermaLink="false">tag:feeds.example.com,2026:trivia-daily/1</guid>
      <pubDate>Sun, 09 Aug 2026 12:00:01 +0000</pubDate>
    </item>
  </channel>
</rss>
`

// dupPubDateRSS is validRSS with its two items stamped identically. This is
// §5.5's sharp case: Slack advances a bookmark past the newest pubDate it has
// seen, so a duplicate stamp makes the second item invisible forever, and
// silently — no error posts anywhere else in the pipeline. Catching it here,
// before publish, is the entire reason `make validate` gates on this rule.
var dupPubDateRSS = strings.Replace(validRSS, "12:00:01 +0000", "12:00:02 +0000", 1)

// item1Block and item2Block are validRSS's two <item> elements in isolation,
// used to build an out-of-order feed below without hand-duplicating markup.
var (
	item2Block = `    <item>
      <title>Item 2</title>
      <link>https://feeds.example.com/items/2</link>
      <description>Summary two.</description>
      <guid isPermaLink="false">tag:feeds.example.com,2026:trivia-daily/2</guid>
      <pubDate>Sun, 09 Aug 2026 12:00:02 +0000</pubDate>
    </item>
`
	item1Block = `    <item>
      <title>Item 1</title>
      <link>https://feeds.example.com/items/1</link>
      <description>Summary one.</description>
      <guid isPermaLink="false">tag:feeds.example.com,2026:trivia-daily/1</guid>
      <pubDate>Sun, 09 Aug 2026 12:00:01 +0000</pubDate>
    </item>
`
)

// outOfOrderRSS lists its older item first, violating §5.5's "strictly
// newest-first" requirement even though no timestamp is duplicated.
var outOfOrderRSS = strings.Replace(validRSS, item2Block+item1Block, item1Block+item2Block, 1)

func TestRun_CleanDocumentExitsZero(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "feed.xml", validRSS)

	stdout, stderr, code := runCapture(t, []string{path})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, path+": ok") {
		t.Fatalf("stdout = %q, want it to report %q ok", stdout, path)
	}
}

func TestRun_DuplicatePubDateFailsWithNonZeroExit(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "feed.xml", dupPubDateRSS)

	stdout, _, code := runCapture(t, []string{path})
	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero for a duplicate-pubDate feed (Slack silently drops the item)")
	}
	if !strings.Contains(stdout, "pubdate-strictly-descending-unique") {
		t.Fatalf("stdout = %q, want it to name the duplicate-pubDate rule", stdout)
	}
}

func TestRun_OutOfOrderItemsFails(t *testing.T) {
	// §5.5: items must be emitted strictly newest-first; Slack (and any
	// bookmark-based reader) assumes this ordering rather than re-sorting.
	dir := t.TempDir()
	path := writeFile(t, dir, "feed.xml", outOfOrderRSS)

	stdout, _, code := runCapture(t, []string{path})
	if code == 0 {
		t.Fatal("exit code = 0, want non-zero for out-of-order pubDates")
	}
	if !strings.Contains(stdout, "pubdate-strictly-descending-unique") {
		t.Fatalf("stdout = %q, want it to name the ordering rule", stdout)
	}
}

func TestRun_MissingPubDateFailsAndNamesTheItem(t *testing.T) {
	dir := t.TempDir()
	doc := strings.Replace(validRSS, "<pubDate>Sun, 09 Aug 2026 12:00:02 +0000</pubDate>", "", 1)
	path := writeFile(t, dir, "feed.xml", doc)

	stdout, _, code := runCapture(t, []string{path})
	if code == 0 {
		t.Fatal("exit code = 0, want non-zero when an item has no pubDate (§5.5: no date tag, no Slack post)")
	}
	if !strings.Contains(stdout, "item[0]") {
		t.Fatalf("stdout = %q, want the offending item named so an operator can act", stdout)
	}
}

func TestRun_RelativeLinkFails(t *testing.T) {
	dir := t.TempDir()
	doc := strings.Replace(validRSS, "https://feeds.example.com/items/2", "/items/2", 1)
	path := writeFile(t, dir, "feed.xml", doc)

	stdout, _, code := runCapture(t, []string{path})
	if code == 0 {
		t.Fatal("exit code = 0, want non-zero for a relative item link")
	}
	if !strings.Contains(stdout, "no-relative-urls") {
		t.Fatalf("stdout = %q, want it to name the absolute-URL rule", stdout)
	}
}

func TestRun_WarningAloneFailsTheBuild(t *testing.T) {
	// §5.6: "CI asserts zero errors *and* zero warnings" — a Warning-level
	// finding must gate the exit code exactly like an Error. Dropping the
	// atom:link self-reference produces only a Warning in feedvalidate.RSS.
	dir := t.TempDir()
	doc := strings.Replace(validRSS, `<atom:link rel="self" type="application/rss+xml" href="https://feeds.example.com/trivia-daily.xml"/>`, "", 1)
	path := writeFile(t, dir, "feed.xml", doc)

	stdout, _, code := runCapture(t, []string{path})
	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero: a Warning-level finding must still fail the gate. stdout=%q", stdout)
	}
	if !strings.Contains(stdout, "[warning]") {
		t.Fatalf("stdout = %q, want a [warning]-level finding reported", stdout)
	}
}

func TestRun_AnswerLeakIntoDescriptionFails(t *testing.T) {
	// §5.5's trivia spoiler rule: description must be plain text with no raw
	// HTML markup, because Slack has no spoiler mechanism and renders
	// description verbatim.
	dir := t.TempDir()
	doc := strings.Replace(validRSS, "<description>Summary two.</description>",
		"<description>Summary two. &lt;b&gt;escaped is fine&lt;/b&gt; but <b>this is not</b></description>", 1)
	path := writeFile(t, dir, "feed.xml", doc)

	stdout, _, code := runCapture(t, []string{path})
	if code == 0 {
		t.Fatal("exit code = 0, want non-zero for a raw HTML tag inside <description>")
	}
	if !strings.Contains(stdout, "description-plain-text") {
		t.Fatalf("stdout = %q, want it to name the plain-text description rule", stdout)
	}
}

func TestRun_MalformedXMLFails(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "feed.xml", "<rss><channel><title>unterminated")

	_, _, code := runCapture(t, []string{path})
	if code == 0 {
		t.Fatal("exit code = 0, want non-zero for a document that does not parse as XML")
	}
}

func TestRun_MultipleFilesAllReported(t *testing.T) {
	dir := t.TempDir()
	good := writeFile(t, dir, "good.xml", validRSS)
	bad := writeFile(t, dir, "bad.xml", dupPubDateRSS)

	stdout, _, code := runCapture(t, []string{good, bad})
	if code == 0 {
		t.Fatal("exit code = 0, want non-zero when any of several files has a finding")
	}
	if !strings.Contains(stdout, good+": ok") {
		t.Fatalf("stdout = %q, want the clean file reported ok even though another file failed", stdout)
	}
	if !strings.Contains(stdout, bad+":") || !strings.Contains(stdout, "pubdate-strictly-descending-unique") {
		t.Fatalf("stdout = %q, want the bad file's finding reported", stdout)
	}
}

func TestRun_NoArgsIsUsageError(t *testing.T) {
	_, stderr, code := runCapture(t, nil)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 for missing arguments", code)
	}
	if !strings.Contains(stderr, "usage:") {
		t.Fatalf("stderr = %q, want a usage message", stderr)
	}
}

func TestRun_UnreadableFileIsAnErrorNotSilent(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist.xml")

	_, stderr, code := runCapture(t, []string{missing})
	if code == 0 {
		t.Fatal("exit code = 0, want non-zero for a file that cannot be read")
	}
	if !strings.Contains(stderr, missing) {
		t.Fatalf("stderr = %q, want it to name the unreadable path", stderr)
	}
}

func TestRun_UnrecognizedExtensionIsAnError(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "feed.txt", validRSS)

	_, stderr, code := runCapture(t, []string{path})
	if code == 0 {
		t.Fatal("exit code = 0, want non-zero for an unrecognized extension")
	}
	if !strings.Contains(stderr, "unrecognized feed") {
		t.Fatalf("stderr = %q, want an unrecognized-format message", stderr)
	}
}

func TestRun_IndexGoldenIsSkippedNotFailed(t *testing.T) {
	// index_*.golden is the feed index HTML page, not a feed and not a
	// per-item permalink (§5.5's OG/unfurl contract is per-item); it must be
	// skipped, not treated as a validation failure or an unrecognized format.
	dir := t.TempDir()
	path := writeFile(t, dir, "index_basic.golden", "<!doctype html><html></html>")

	stdout, _, code := runCapture(t, []string{path})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0: an index golden must be skipped, not failed", code)
	}
	if !strings.Contains(stdout, "skipped") {
		t.Fatalf("stdout = %q, want a skipped notice", stdout)
	}
}

// validPermalink is a minimal permalink page carrying every OG/unfurl tag
// internal/render/permalink.go emits, so it passes feedvalidate.Permalink
// cleanly. This is what Slack's unfurler fetches for a shared link (§5.5
// "Link unfurling") — unlike RSS/Atom/JSON, that page was previously skipped
// entirely by `make validate` (see the golden-dispatch history in main.go),
// leaving the entire unfurl surface mechanically unchecked.
const validPermalink = `<!DOCTYPE html>
<html lang="en-us">
<head>
<meta charset="utf-8">
<title>Sample Item</title>
<meta name="description" content="Summary text.">
<meta property="og:title" content="Sample Item">
<meta property="og:description" content="Summary text.">
<meta property="og:image" content="https://feeds.example.com/og.png">
<meta property="og:type" content="article">
<meta property="og:url" content="https://feeds.example.com/items/1">
<meta property="article:published_time" content="2026-08-09T12:00:00Z">
<meta name="twitter:card" content="summary_large_image">
</head>
<body>
<article><h1>Sample Item</h1><p>Body.</p></article>
</body>
</html>
`

func TestRun_PermalinkGoldenValidatesCleanly(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "permalink_basic.golden", validPermalink)

	stdout, stderr, code := runCapture(t, []string{path})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 for a clean permalink page; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, path+": ok") {
		t.Fatalf("stdout = %q, want it to report %q ok", stdout, path)
	}
}

func TestRun_PermalinkGoldenMissingOGTagsFails(t *testing.T) {
	// §5.5 "Link unfurling": a permalink page missing og: tags means Slack's
	// unfurl renders a bare URL, silently — no error posts anywhere else in
	// the pipeline. This is the reason the golden is validated rather than
	// skipped now.
	dir := t.TempDir()
	path := writeFile(t, dir, "permalink_bare.golden", "<!doctype html><html><head></head><body></body></html>")

	stdout, _, code := runCapture(t, []string{path})
	if code == 0 {
		t.Fatal("exit code = 0, want non-zero for a permalink page with no OG tags")
	}
	if !strings.Contains(stdout, "permalink-og-tags-present") {
		t.Fatalf("stdout = %q, want it to name the missing-OG-tags rule", stdout)
	}
}

func TestRun_GoldenPrefixDispatch(t *testing.T) {
	dir := t.TempDir()
	rss := writeFile(t, dir, "rss_case.golden", validRSS)

	stdout, _, code := runCapture(t, []string{rss})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 for a valid rss_*.golden file; stdout=%q", code, stdout)
	}
}

// --- helpers ----------------------------------------------------------------

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

// runCapture invokes run() with in-memory buffers so tests can assert on
// stdout/stderr content without touching real file descriptors.
func runCapture(t *testing.T, args []string) (stdout, stderr string, code int) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	code = run(args, &outBuf, &errBuf)
	return outBuf.String(), errBuf.String(), code
}
