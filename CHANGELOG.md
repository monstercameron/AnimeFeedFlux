# Changelog

Notable changes to AnimeFeedFlux. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

This file answers **"what changed, and do I need to care?"** For *why* a thing changed — including
the decisions that were made and then reversed — see [`DEVLOG.md`](DEVLOG.md). Keeping the two apart
is deliberate: a changelog that carries reasoning becomes unreadable, and a narrative that carries
version diffs becomes unmaintainable.

No software is released yet. The project is in planning — see `README.md` → "Status".

`0.0.1-dev` versions the **specification**, not an implementation. The `-dev` pre-release tag is
deliberate: it sorts below any `0.0.1` release under semver precedence, so tagging a real build
later cannot be shadowed by this one. The number will stay in the `0.0.x-dev` range for as long as
the repository contains no code, and the first version that means "you can run this" is the one cut
at the end of Phase C.

## [Unreleased]

## [0.0.1-dev] — 2026-08-09

### Added

- `PLAN.md` — the specification of record: two-plane architecture, feed-format compliance researched
  against the RSS 2.0 spec, the RSS Best Practices Profile, RFC 4287 and JSON Feed 1.1, Slack as a
  first-class consumer, data model, RPC surface, admin UI, operations, testing strategy, phased
  milestones, risks, open questions, and the ten user flows with their sanity assertions.
- `TODOS.md` — atomic build order across phases A–E, each task citing a plan section; standing
  rules; flow sanity suites; fuzz, soak and load tasks; an operational runbook and drills.
- Repository scaffolding — licence, contribution and security policy, code of conduct, agent
  orientation, issue and pull-request templates, dependabot, line-ending policy.
- `DEVLOG.md` — the narrative record, including the reversals: a content-derived guid replaced by an
  opaque ULID, Docker rejected on evidence and then adopted for learning value, `PurgeDeleted`
  specified and cut, multi-feed scaling deferred.

### Decided

- **Slack's RSS app is stricter than the RSS specification** and fails silently. It requires a date
  tag, in-sequence items, and no duplicate timestamps, and it advances a bookmark past the newest
  item it has seen. This forced distinct strictly-increasing timestamps behind a database
  constraint, a no-backdating rule, plain-text `description` with HTML in `content:encoded`,
  OpenGraph tags for unfurls, and corrections instead of silent edits.
- **Item identity is an opaque ULID, not a content hash.** A title-derived guid is stable under edit
  only by convention; an opaque key makes it true by construction. Idempotency moved to a separate
  `content_hash`.
- **Hallucinated links are prevented structurally.** Grounded items must carry a link byte-equal to
  a URL actually fetched, with both sides normalized by the same function.
- **SchemaFlux** supplies typed LLM operations; the business-rule validation layer stays ours,
  because typed is not valid.
- **Core engine first, UI last.** Every RPC is exercised by the CLI before a component exists.
- **Docker was rejected on the evidence and then adopted anyway**, for the learning value of a real
  container pipeline. The trade — a second deployment model on a 2 GB box — is recorded in §15
  rather than left to be rediscovered.
- **No hard delete.** A `PurgeDeleted` RPC was specified and then removed: it contradicted the
  promise that a permalink resolves forever.
