# Contributing to AnimeFeedFlux

This document is short on purpose. Almost everything about how this repository works is already in
`PLAN.md`; what follows is the part you need before your first change.

## Read this first: there is no code yet

The repository is a specification and a build order. If you are picking up work, you are starting a
task from `TODOS.md`, not modifying an existing implementation. Check the phase ordering before you
begin — tasks are in dependency order, and Phase A must be real before Phase B means anything.

## The document set, and which one wins

Two documents, one specification. Each owns something the other must not duplicate, because
duplicated facts drift and drifted facts get implemented.

| Doc | Owns |
|---|---|
| **`PLAN.md`** | The spec of record — architecture, compliance rules, data model, RPC surface, user flows (§22), testing strategy (§17), risks (§20), open questions (§21) |
| **`TODOS.md`** | Build order — atomic tasks in dependency order, standing rules, flow sanity suites, operational runbook |

`PLAN.md` wins. If `TODOS.md` contradicts it, `TODOS.md` is wrong and gets fixed. **If the
implementation contradicts `PLAN.md`, the plan is wrong — and it gets corrected in the same change,
not later.** A spec that has quietly drifted from the code is worse than no spec, because it still
gets trusted.

Refer to things by their stable identifiers — `§5.5`, `§9.6`, `A4-12`, `BF-11`, `J10`, `RULE-4` —
rather than by prose. They are all greppable, which is the point. `§9.1`…`§9.8` are the eight
generation steps; they are numbered deliberately so they can be cited.

## Working a task

1. Find the task in `TODOS.md`. **Read the plan section it cites before starting.** Several tasks
   look trivial until you read why they exist — `A1-10` is "add a column" until you read §5.1 and
   discover it is the difference between a guid that survives an edit and one that spams every
   subscriber.
2. One task, one commit-sized change, with the stated check passing.
3. Mark it `[x]` only when its check passes. **Code existing is not done.**
4. If a task is wrong, fix the task. If the plan is wrong, fix the plan in the same change.

## Standing rules

These apply to every change and are repeated at the top of `TODOS.md`:

- The default test run never calls a paid API. Paid tests sit behind `AFF_LIVE_LLM=1`.
- No secret enters the repo, an image layer, a log line, or a recipe.
- Model output and upstream RSS are hostile input.
- **Typed is not valid.** SchemaFlux guarantees shape; every business rule is still ours.
- Two date formatters exist — RFC 822 for RSS, RFC 3339 for Atom and JSON Feed. Never cross them.
- Every write that changes a feed's published representation invalidates its cache.
- Never backdate an item.

## When the spec is silent

The plan is deliberately detailed and it is still not complete — no plan is. This is the rule for
the edge, and it exists because **work produced by guessing looks finished.**

If the plan does not answer your question, the answer is one of:

- **It is a real decision** → write it down in `PLAN.md` with the reasoning, then implement it. An
  undocumented decision is one that gets re-litigated in six months by someone who cannot tell it
  was a decision at all.
- **It is genuinely ambiguous and consequential** → ask before building. §21 exists for exactly
  this, and each open question there names what it blocks.
- **It does not matter** → pick the boring option and move on.

What you must not do is guess silently on something load-bearing. A wrong variable name is
discovered in review; a wrong assumption about whether sampling writes to the database is discovered
in production.

## Testing expectations

`PLAN.md` §17 is the strategy; `TODOS.md` carries the tasks. The parts most easily skipped, and
therefore stated plainly:

- **Flow sanity tests (`BF-*`, §17.5) are not optional.** They drive a whole user flow and then
  assert on resulting *system state*, not on a mock's call log. This is where this design's real
  failures live: a sample that writes, a promote that skips cache invalidation, a correction that
  reuses a guid.
- Adversarial generator tests must **reject** rather than publish. Malformed JSON, a URL absent from
  the candidate set, a near-duplicate, `<script>` in the body, an answer leaked into the summary, a
  backdated timestamp.
- `-race` runs in CI on ubuntu, because it cannot run on windows/arm64. It is a gate.
- Coverage is a **ratchet, not a target** — it may not go down. Chasing a percentage produces tests
  written to touch lines.

## Commits and pull requests

- Explain **why**, not what. The diff already says what.
- Reference the task ID and the plan section.
- If the change corrects the plan, say what was wrong with it and why the new version is right.
- Do not skip hooks or bypass signing.

## Security

Do not open a public issue for a vulnerability affecting a running instance. See
[`SECURITY.md`](SECURITY.md).

## Code of conduct

By participating you agree to abide by [`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md).
