package feedvalidate

// Every document in this file is a string literal built by hand, not routed
// through internal/render. That independence is the point (see the package
// doc): a validator that shares code with the renderer it checks would miss
// a bug both sides agree on, and a validator that imports render's helpers
// (date layouts, escaping) could pass a document the renderer never
// actually produces. Every fact encoded below (the RFC 822 layout, the tag
// URI scheme, the required elements) is re-derived from PLAN.md, not copied
// from render's source.

import "testing"

func hasRule(findings []Finding, rule string) bool {
	for _, f := range findings {
		if f.Rule == rule {
			return true
		}
	}
	return false
}

// ---- RSS ----

const goodRSS = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom">
  <channel>
    <title>Test Feed</title>
    <link>https://anime.earlcameron.com/feeds/test.xml</link>
    <description>A test feed</description>
    <atom:link rel="self" type="application/rss+xml" href="https://anime.earlcameron.com/feeds/test.xml"/>
    <item>
      <title>First Item</title>
      <description>First item description &#x26; more</description>
      <link>https://anime.earlcameron.com/items/01ABC</link>
      <guid isPermaLink="false">tag:anime.earlcameron.com,2026:test/01ABC</guid>
      <pubDate>Thu, 04 Oct 2007 23:59:45 +0000</pubDate>
    </item>
    <item>
      <title>Second Item</title>
      <description>Second item description</description>
      <link>https://anime.earlcameron.com/items/01ABD</link>
      <guid isPermaLink="false">tag:anime.earlcameron.com,2026:test/01ABD</guid>
      <pubDate>Wed, 03 Oct 2007 23:59:45 +0000</pubDate>
    </item>
  </channel>
</rss>`

func TestRSS_KnownGood(t *testing.T) {
	findings := RSS([]byte(goodRSS))
	if len(findings) != 0 {
		t.Fatalf("expected zero findings, got %+v", findings)
	}
}

func TestRSS_WellFormedXML(t *testing.T) {
	broken := `<rss version="2.0"><channel><title>Test</title>`
	findings := RSS([]byte(broken))
	if !hasRule(findings, "§5.1 well-formed-xml") {
		t.Fatalf("expected §5.1 well-formed-xml finding, got %+v", findings)
	}
}

func TestRSS_ChannelRequiredElements(t *testing.T) {
	broken := `<rss version="2.0"><channel><title>Test</title><link>https://x.example/</link></channel></rss>`
	findings := RSS([]byte(broken))
	if !hasRule(findings, "§5.1 channel-required-elements") {
		t.Fatalf("expected §5.1 channel-required-elements finding, got %+v", findings)
	}
}

func TestRSS_ItemTitleOrDescription(t *testing.T) {
	broken := `<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom"><channel>
    <title>Test</title><link>https://x.example/</link><description>d</description>
    <atom:link rel="self" type="application/rss+xml" href="https://x.example/feed.xml"/>
    <item>
      <link>https://x.example/items/1</link>
      <guid isPermaLink="false">tag:x.example,2026:1</guid>
      <pubDate>Thu, 04 Oct 2007 23:59:45 +0000</pubDate>
    </item>
  </channel></rss>`
	findings := RSS([]byte(broken))
	if !hasRule(findings, "§5.1 item-title-or-description") {
		t.Fatalf("expected §5.1 item-title-or-description finding, got %+v", findings)
	}
}

func TestRSS_GuidIsPermaLinkExplicit(t *testing.T) {
	broken := `<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom"><channel>
    <title>Test</title><link>https://x.example/</link><description>d</description>
    <atom:link rel="self" type="application/rss+xml" href="https://x.example/feed.xml"/>
    <item>
      <title>Item</title>
      <link>https://x.example/items/1</link>
      <guid>tag:x.example,2026:1</guid>
      <pubDate>Thu, 04 Oct 2007 23:59:45 +0000</pubDate>
    </item>
  </channel></rss>`
	findings := RSS([]byte(broken))
	if !hasRule(findings, "§5.1 guid-isPermaLink-explicit") {
		t.Fatalf("expected §5.1 guid-isPermaLink-explicit finding, got %+v", findings)
	}
}

func TestRSS_PubDateRFC822(t *testing.T) {
	broken := `<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom"><channel>
    <title>Test</title><link>https://x.example/</link><description>d</description>
    <atom:link rel="self" type="application/rss+xml" href="https://x.example/feed.xml"/>
    <item>
      <title>Item</title>
      <link>https://x.example/items/1</link>
      <guid isPermaLink="false">tag:x.example,2026:1</guid>
      <pubDate>Thu, 4 Oct 07 23:59:45 GMT</pubDate>
    </item>
  </channel></rss>`
	findings := RSS([]byte(broken))
	if !hasRule(findings, "§5.1 pubdate-rfc822") {
		t.Fatalf("expected §5.1 pubdate-rfc822 finding, got %+v", findings)
	}
}

func TestRSS_PubDateStrictlyDescendingUnique(t *testing.T) {
	broken := `<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom"><channel>
    <title>Test</title><link>https://x.example/</link><description>d</description>
    <atom:link rel="self" type="application/rss+xml" href="https://x.example/feed.xml"/>
    <item>
      <title>First</title>
      <link>https://x.example/items/1</link>
      <guid isPermaLink="false">tag:x.example,2026:1</guid>
      <pubDate>Thu, 04 Oct 2007 23:59:45 +0000</pubDate>
    </item>
    <item>
      <title>Second</title>
      <link>https://x.example/items/2</link>
      <guid isPermaLink="false">tag:x.example,2026:2</guid>
      <pubDate>Thu, 04 Oct 2007 23:59:45 +0000</pubDate>
    </item>
  </channel></rss>`
	findings := RSS([]byte(broken))
	if !hasRule(findings, "§5.5 pubdate-strictly-descending-unique") {
		t.Fatalf("expected §5.5 pubdate-strictly-descending-unique finding, got %+v", findings)
	}
}

func TestRSS_AtomLinkSelf(t *testing.T) {
	broken := `<rss version="2.0"><channel>
    <title>Test</title><link>https://x.example/</link><description>d</description>
  </channel></rss>`
	findings := RSS([]byte(broken))
	if !hasRule(findings, "§5.1 atom-link-self") {
		t.Fatalf("expected §5.1 atom-link-self finding, got %+v", findings)
	}
}

func TestRSS_NoRelativeURLs(t *testing.T) {
	broken := `<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom"><channel>
    <title>Test</title><link>https://x.example/</link><description>d</description>
    <atom:link rel="self" type="application/rss+xml" href="https://x.example/feed.xml"/>
    <item>
      <title>Item</title>
      <link>/items/1</link>
      <guid isPermaLink="false">tag:x.example,2026:1</guid>
      <pubDate>Thu, 04 Oct 2007 23:59:45 +0000</pubDate>
    </item>
  </channel></rss>`
	findings := RSS([]byte(broken))
	if !hasRule(findings, "§5.1 no-relative-urls") {
		t.Fatalf("expected §5.1 no-relative-urls finding, got %+v", findings)
	}
}

func TestRSS_DescriptionPlainText(t *testing.T) {
	broken := `<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom"><channel>
    <title>Test</title><link>https://x.example/</link><description>d</description>
    <atom:link rel="self" type="application/rss+xml" href="https://x.example/feed.xml"/>
    <item>
      <title>Item</title>
      <description><![CDATA[<b>bold</b> text]]></description>
      <link>https://x.example/items/1</link>
      <guid isPermaLink="false">tag:x.example,2026:1</guid>
      <pubDate>Thu, 04 Oct 2007 23:59:45 +0000</pubDate>
    </item>
  </channel></rss>`
	// NOTE: this is a literal, unescaped "<b>" — real markup, as opposed to
	// the hex character reference (&#x26; etc.) the good-doc test uses for
	// a literal "<" character that isn't forming a tag. Both decode
	// differently through encoding/xml's chardata handling (CDATA passes
	// the tag through untouched; a hex ref of "<" alone would too, so this
	// check runs against the raw pre-decode bytes rather than the decoded
	// string — see descriptionTagPattern's doc comment.
	findings := RSS([]byte(broken))
	if !hasRule(findings, "§5.5 description-plain-text") {
		t.Fatalf("expected §5.5 description-plain-text finding, got %+v", findings)
	}
}

// ---- Atom ----

const goodAtom = `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <id>tag:anime.earlcameron.com,2026:test</id>
  <title>Test Feed</title>
  <updated>2026-08-10T12:00:00Z</updated>
  <link rel="self" href="https://anime.earlcameron.com/feeds/test.atom"/>
  <entry>
    <id>tag:anime.earlcameron.com,2026:test/01ABC</id>
    <title>First Entry</title>
    <updated>2026-08-10T12:00:00Z</updated>
    <content type="html">Hello</content>
    <link rel="alternate" href="https://anime.earlcameron.com/items/01ABC"/>
  </entry>
</feed>`

func TestAtom_KnownGood(t *testing.T) {
	findings := Atom([]byte(goodAtom))
	if len(findings) != 0 {
		t.Fatalf("expected zero findings, got %+v", findings)
	}
}

func TestAtom_WellFormedXML(t *testing.T) {
	broken := `<feed xmlns="http://www.w3.org/2005/Atom"><id>x</id>`
	findings := Atom([]byte(broken))
	if !hasRule(findings, "§5.2 well-formed-xml") {
		t.Fatalf("expected §5.2 well-formed-xml finding, got %+v", findings)
	}
}

func TestAtom_FeedRequiredSingletons(t *testing.T) {
	broken := `<feed xmlns="http://www.w3.org/2005/Atom">
  <id>tag:x.example,2026:test</id>
  <title>One</title>
  <title>Two</title>
  <updated>2026-08-10T12:00:00Z</updated>
  <entry>
    <id>tag:x.example,2026:test/1</id>
    <title>Entry</title>
    <updated>2026-08-10T12:00:00Z</updated>
    <content type="html">Hello</content>
  </entry>
</feed>`
	findings := Atom([]byte(broken))
	if !hasRule(findings, "§5.2 feed-required-singletons") {
		t.Fatalf("expected §5.2 feed-required-singletons finding, got %+v", findings)
	}
}

func TestAtom_EntryRequiredSingletons(t *testing.T) {
	broken := `<feed xmlns="http://www.w3.org/2005/Atom">
  <id>tag:x.example,2026:test</id>
  <title>Test</title>
  <updated>2026-08-10T12:00:00Z</updated>
  <entry>
    <id>tag:x.example,2026:test/1</id>
    <title>Entry</title>
    <content type="html">Hello</content>
  </entry>
</feed>`
	findings := Atom([]byte(broken))
	if !hasRule(findings, "§5.2 entry-required-singletons") {
		t.Fatalf("expected §5.2 entry-required-singletons finding, got %+v", findings)
	}
}

func TestAtom_IDUnique(t *testing.T) {
	broken := `<feed xmlns="http://www.w3.org/2005/Atom">
  <id>tag:x.example,2026:test</id>
  <title>Test</title>
  <updated>2026-08-10T12:00:00Z</updated>
  <entry>
    <id>tag:x.example,2026:test</id>
    <title>Entry</title>
    <updated>2026-08-10T12:00:00Z</updated>
    <content type="html">Hello</content>
  </entry>
</feed>`
	findings := Atom([]byte(broken))
	if !hasRule(findings, "§5.2 id-unique") {
		t.Fatalf("expected §5.2 id-unique finding, got %+v", findings)
	}
}

func TestAtom_UpdatedRFC3339(t *testing.T) {
	broken := `<feed xmlns="http://www.w3.org/2005/Atom">
  <id>tag:x.example,2026:test</id>
  <title>Test</title>
  <updated>2026-08-10 12:00:00Z</updated>
  <entry>
    <id>tag:x.example,2026:test/1</id>
    <title>Entry</title>
    <updated>2026-08-10T12:00:00Z</updated>
    <content type="html">Hello</content>
  </entry>
</feed>`
	findings := Atom([]byte(broken))
	if !hasRule(findings, "§5.2 updated-rfc3339") {
		t.Fatalf("expected §5.2 updated-rfc3339 finding, got %+v", findings)
	}
}

func TestAtom_EntryContentOrAlternateLink(t *testing.T) {
	broken := `<feed xmlns="http://www.w3.org/2005/Atom">
  <id>tag:x.example,2026:test</id>
  <title>Test</title>
  <updated>2026-08-10T12:00:00Z</updated>
  <entry>
    <id>tag:x.example,2026:test/1</id>
    <title>Entry</title>
    <updated>2026-08-10T12:00:00Z</updated>
  </entry>
</feed>`
	findings := Atom([]byte(broken))
	if !hasRule(findings, "§5.2 entry-content-or-alternate-link") {
		t.Fatalf("expected §5.2 entry-content-or-alternate-link finding, got %+v", findings)
	}
}

func TestAtom_NoEmptyHref(t *testing.T) {
	broken := `<feed xmlns="http://www.w3.org/2005/Atom">
  <id>tag:x.example,2026:test</id>
  <title>Test</title>
  <updated>2026-08-10T12:00:00Z</updated>
  <link rel="self" href=""/>
  <entry>
    <id>tag:x.example,2026:test/1</id>
    <title>Entry</title>
    <updated>2026-08-10T12:00:00Z</updated>
    <content type="html">Hello</content>
  </entry>
</feed>`
	findings := Atom([]byte(broken))
	if !hasRule(findings, "§5.2 no-empty-href") {
		t.Fatalf("expected §5.2 no-empty-href finding, got %+v", findings)
	}
}

// ---- JSON Feed ----

const goodJSONFeed = `{
  "version": "https://jsonfeed.org/version/1.1",
  "title": "Test Feed",
  "items": [
    {
      "id": "tag:anime.earlcameron.com,2026:test/01ABC",
      "content_html": "<p>Hello</p>",
      "date_published": "2026-08-10T12:00:00Z"
    }
  ]
}`

func TestJSONFeed_KnownGood(t *testing.T) {
	findings := JSONFeed([]byte(goodJSONFeed))
	if len(findings) != 0 {
		t.Fatalf("expected zero findings, got %+v", findings)
	}
}

func TestJSONFeed_WellFormedJSON(t *testing.T) {
	broken := `{"version": "https://jsonfeed.org/version/1.1", "title": "Test",`
	findings := JSONFeed([]byte(broken))
	if !hasRule(findings, "§5.3 well-formed-json") {
		t.Fatalf("expected §5.3 well-formed-json finding, got %+v", findings)
	}
}

func TestJSONFeed_VersionExact(t *testing.T) {
	broken := `{"version": "1.1", "title": "Test", "items": []}`
	findings := JSONFeed([]byte(broken))
	if !hasRule(findings, "§5.3 version-exact") {
		t.Fatalf("expected §5.3 version-exact finding, got %+v", findings)
	}
}

func TestJSONFeed_TitlePresent(t *testing.T) {
	broken := `{"version": "https://jsonfeed.org/version/1.1", "items": []}`
	findings := JSONFeed([]byte(broken))
	if !hasRule(findings, "§5.3 title-present") {
		t.Fatalf("expected §5.3 title-present finding, got %+v", findings)
	}
}

func TestJSONFeed_ItemIDString(t *testing.T) {
	broken := `{"version": "https://jsonfeed.org/version/1.1", "title": "Test", "items": [
    {"id": 12345, "content_html": "hi", "date_published": "2026-08-10T12:00:00Z"}
  ]}`
	findings := JSONFeed([]byte(broken))
	if !hasRule(findings, "§5.3 item-id-string") {
		t.Fatalf("expected §5.3 item-id-string finding, got %+v", findings)
	}
}

func TestJSONFeed_ItemContentPresent(t *testing.T) {
	broken := `{"version": "https://jsonfeed.org/version/1.1", "title": "Test", "items": [
    {"id": "1", "date_published": "2026-08-10T12:00:00Z"}
  ]}`
	findings := JSONFeed([]byte(broken))
	if !hasRule(findings, "§5.3 item-content-present") {
		t.Fatalf("expected §5.3 item-content-present finding, got %+v", findings)
	}
}

func TestJSONFeed_NoDeprecatedAuthor(t *testing.T) {
	broken := `{"version": "https://jsonfeed.org/version/1.1", "title": "Test", "author": {"name": "Cam"}, "items": [
    {"id": "1", "content_html": "hi", "date_published": "2026-08-10T12:00:00Z"}
  ]}`
	findings := JSONFeed([]byte(broken))
	if !hasRule(findings, "§5.3 no-deprecated-author") {
		t.Fatalf("expected §5.3 no-deprecated-author finding, got %+v", findings)
	}
}

func TestJSONFeed_DatePublishedRFC3339(t *testing.T) {
	broken := `{"version": "https://jsonfeed.org/version/1.1", "title": "Test", "items": [
    {"id": "1", "content_html": "hi", "date_published": "08/10/2026"}
  ]}`
	findings := JSONFeed([]byte(broken))
	if !hasRule(findings, "§5.3 date-published-rfc3339") {
		t.Fatalf("expected §5.3 date-published-rfc3339 finding, got %+v", findings)
	}
}
