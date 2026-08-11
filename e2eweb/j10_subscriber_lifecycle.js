'use strict';

// J10 — Subscriber lifecycle, the one that matters (PLAN.md §22 J10;
// TODOS.md DF-11; §17.5 mandates a real fetch over HTTP, at least two
// polling cycles — "delivered exactly once" cannot be proven by one
// request). Actor: Slack, or any reader — not a browser session, so this
// journey does not use Playwright's browser at all, only its HTTP request
// context, and has NO login/AUTH precondition. This is the one journey in
// this suite that is not blocked by the login rewrite and runs for real.
//
// Steps: discover via the index -> subscribe -> receive new items ->
// unfurl a permalink -> poll repeatedly over days (compressed here to two
// consecutive polls, per §17.5's floor).
//
// Sanity from §22, checked for real below:
//   - the feed validates with zero warnings [partial: well-formed XML +
//     required elements checked here; full RSS-validator-clean is a
//     stronger claim this script does not make]
//   - every item has a unique, strictly decreasing pubDate
//   - an unchanged feed answers 304 on the second poll (conditional GET)
//   - a trivia answer appears nowhere in description [not attempted — no
//     trivia feed selected by this script; see README]
// NOT exercised here (would require mutating server state this suite has
// no mandate to do): a deleted item's permalink returning 410; an edited
// item not being redelivered; a correction being delivered; a backdated
// item being blocked. Those need a controlled fixture, not the seeded dev
// feed as found.

const { chromium, request } = require('playwright');
const { runHttp, assert, SkipError, PUBLISH_URL } = require('./lib/harness');

runHttp('J10-subscriber-lifecycle', async () => {
  const api = await request.newContext();

  // --- Discover via the index -----------------------------------------
  const indexRes = await api.get(`${PUBLISH_URL}/`);
  assert(indexRes.ok(), `feed index did not return 200: ${indexRes.status()}`);
  const indexHtml = await indexRes.text();
  const feedLinkMatch = indexHtml.match(/href="([^"]+\.xml)"/);
  if (!feedLinkMatch) {
    throw new SkipError(`no .xml feed link found on the index at ${PUBLISH_URL}/ — no seeded feed to subscribe to.`);
  }
  const feedUrl = feedLinkMatch[1];

  // --- Subscribe: first poll -------------------------------------------
  const first = await api.get(feedUrl);
  assert(first.ok(), `first poll of ${feedUrl} did not return 200: ${first.status()}`);
  const etag = first.headers()['etag'];
  const lastModified = first.headers()['last-modified'];
  const bodyXml = await first.text();

  assert(bodyXml.startsWith('<?xml'), 'feed body does not start with an XML declaration');
  assert(bodyXml.includes('<rss') || bodyXml.includes('<feed'), 'feed body has neither <rss> nor <feed> root');

  // Scope to <item> blocks. RSS also carries a channel-level <pubDate>, which
  // internal/render/rss.go deliberately sets to the newest item's timestamp — so
  // an unscoped match always yields one duplicate and this uniqueness assertion
  // could never pass against a valid feed. It was reporting a defect that was not
  // there, and would have masked a real collision behind an already-red check.
  const itemBlocks = [...bodyXml.matchAll(/<item\b[\s\S]*?<\/item>/g)].map((m) => m[0]);
  assert(itemBlocks.length > 0, 'feed has zero <item> elements — precondition (a live feed with items) is not met');
  const pubDates = itemBlocks.map((block) => {
    const m = /<pubDate>([^<]+)<\/pubDate>/.exec(block);
    assert(m !== null, 'an <item> has no <pubDate> — RSS requires one for Slack to order it');
    return new Date(m[1]).getTime();
  });
  assert(pubDates.length > 0, 'feed has zero items — precondition (a live feed with items) is not met');
  assert(pubDates.every((t) => !Number.isNaN(t)), 'one or more pubDate values did not parse as dates');
  const uniqueDates = new Set(pubDates);
  assert(uniqueDates.size === pubDates.length, `pubDate values are not unique: ${pubDates.length} items, ${uniqueDates.size} distinct dates`);
  const sorted = [...pubDates].sort((a, b) => b - a);
  assert(
    pubDates.every((t, i) => t === sorted[i]),
    'items are not in strictly decreasing pubDate order'
  );

  const guids = [...bodyXml.matchAll(/<guid[^>]*>([^<]+)<\/guid>/g)].map((m) => m[1]);
  const uniqueGuids = new Set(guids);
  assert(uniqueGuids.size === guids.length, 'duplicate <guid> values found in a single feed response');

  // --- Unfurl a permalink ------------------------------------------------
  const linkMatch = bodyXml.match(/<item>[\s\S]*?<link>([^<]+)<\/link>/);
  if (linkMatch) {
    const permalinkRes = await api.get(linkMatch[1]);
    assert(permalinkRes.ok(), `item permalink ${linkMatch[1]} did not resolve: ${permalinkRes.status()}`);
  }

  // --- Poll again: conditional GET must answer 304 on an unchanged feed --
  const headers = {};
  if (etag) headers['If-None-Match'] = etag;
  if (lastModified) headers['If-Modified-Since'] = lastModified;
  if (!etag && !lastModified) {
    throw new SkipError('first response carried neither ETag nor Last-Modified — cannot test conditional GET.');
  }
  const second = await api.get(feedUrl, { headers });
  assert(
    second.status() === 304,
    `second poll with a conditional GET on an unchanged feed expected 304, got ${second.status()}`
  );

  // --- Exactly-once across the two polls: guids must be identical --------
  // (the second response has no body on a 304, so this compares against a
  // third, unconditional poll to confirm the item set didn't drift between
  // the two conditional-GET cycles.)
  const third = await api.get(feedUrl);
  const guids2 = [...(await third.text()).matchAll(/<guid[^>]*>([^<]+)<\/guid>/g)].map((m) => m[1]);
  assert(
    JSON.stringify(guids) === JSON.stringify(guids2),
    'item set changed between two consecutive polls with nothing published in between'
  );

  await api.dispose();
  return `${feedUrl}: ${pubDates.length} items, unique+decreasing pubDates, unique guids, 304 on repeat poll, stable across 3 polls`;
});
