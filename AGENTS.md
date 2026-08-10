# AGENTS.md — orientation for agents working on AnimeFeedFlux

This file is a **router, not a specification.** `CONTRIBUTING.md` says two documents form one spec
and each owns something the other must not duplicate, "because duplicated facts drift and drifted
facts get implemented." That applies to this file too, so nothing here is restated from elsewhere —
it tells you which document answers which question, and stops.

## Read these, in this order

1. **`CONTRIBUTING.md`** — how to work here: the document set and which one wins, what to do when
   the spec is silent, the standing rules, testing expectations, commits. Start here every time.
2. **`PLAN.md`** — the spec of record, and it **wins**. Architecture (§2), compliance (§5), recipes
   (§7), generation (§9), data model (§10), RPCs (§11), UI (§12), operations (§15), testing (§17),
   milestones (§18), risks (§20), open questions (§21), user flows (§22). If the implementation
   contradicts it, the plan is wrong and gets corrected *in the same change*.
3. **`TODOS.md`** — build order: phases A–E in dependency order, standing rules, the flow sanity
   suites (`BF-*`, `DF-*`), and the operational runbook.

Refer to things by their stable identifiers — `§5.5`, `§9.6`, `A4-12`, `BF-11`, `J10`, `RULE-4`.
They are greppable, which is the point.

## Before anything else: check what actually exists

**As of this writing there is no code in this repository** — only `PLAN.md` and `TODOS.md`. Do not
assume a package, table, RPC, or binary named in the plan has been built. Inspect the tree first.
The plan describes the destination; `TODOS.md` describes how far along the road anyone has got.

## The things agents get wrong here

- **Phase order is a dependency order, not a preference.** Core engine first, UI last (§18). Do not
  start Phase D because it is more fun; every RPC the UI calls is meant to be exercised by the CLI
  first, so the UI is built once against settled semantics rather than twice.
- **Typed is not valid.** SchemaFlux returns a typed Go value, which guarantees *shape* only. Every
  business rule in §9 is still ours and still runs. A struct containing a hallucinated URL is
  perfectly typed and completely wrong.
- **Sampling must never write an item.** `BF-11` exists because a sampler that publishes is the
  worst bug this design can have, and it would look like a feature working.
- **The guid is derived from an opaque ULID, never from the title.** If you find yourself
  re-deriving a guid anywhere — a renderer, a repair script — stop. That resurfaces every edited
  item as a duplicate in every subscriber's inbox (§5.1).
- **Two date formatters exist and must never be crossed.** RFC 822 for RSS, RFC 3339 for Atom and
  JSON Feed (`RULE-5`).
- **Never backdate an item.** Slack's bookmark makes it invisible forever, silently (§5.5).
- **Normalize both sides of the grounded link check with the same function.** Asymmetric
  normalization rejects genuine links and looks like the model misbehaving (§9.6).
- **Items and their run row commit in one transaction.** Splitting them lets a crash produce live
  items beside a run marked failed, and the history then lies (§9).
- **Cache invalidation covers feed-level writes too**, not just item writes — `FeedService.Update`,
  `SetEnabled`, and `SetMembers` change rendered output without touching `items` (§11).
- **There is no hard delete.** `Delete` is soft; the permalink returns `410` forever. A
  `PurgeDeleted` RPC was deliberately removed — do not reintroduce it (§12.4).
- **The publish plane has no writer, and that is the point.** Do not hand it a writable handle for
  convenience (§2).
- **Never commit secrets or the database.** `.env`, `*.key`, `*.db*`, and backup directories are
  working state, not source.
- **Creating the GitHub repository, adding a remote, or pushing is Cam's call.** Do not do it on
  your own initiative.

## Verifying your work

`go vet`, the linter, and `go test ./...` before you claim anything is done — and note what those do
**not** prove:

- The default test run deliberately makes **no network calls and needs no API key** (`RULE-1`). A
  green run says nothing about provider behaviour. Paid tests are behind `AFF_LIVE_LLM=1`.
- **`-race` cannot run on windows/arm64**, so it only happens in CI on ubuntu. `-shuffle=on` locally
  is genuinely weaker — it reorders tests without instrumenting memory access.
- Feed correctness is not proven by unit tests. `make validate` runs the external feed validator and
  gates on **warnings**, not just errors (§5.6).
- Flow sanity tests (`BF-*`) assert on resulting system state, not on a mock's call log. They are
  the regression suite (§17.5).

"CI will tell me" is not verification.
