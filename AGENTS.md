# AGENTS.md — orientation for agents working on AnimeFeedFlux

This file is a **router, not a specification.** `CONTRIBUTING.md` says four documents form one spec
and each owns something the others must not duplicate, "because duplicated facts drift and drifted
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
4. **`DEVLOG.md`** — why things are the way they are, including what was tried and reversed. Read it
   before proposing a change to a decision that looks arbitrary; several already are the *second*
   answer to a question. It is **history, not requirements** — if it disagrees with `PLAN.md`, the
   plan is right.

Refer to things by their stable identifiers — `§5.5`, `§9.6`, `A4-12`, `BF-11`, `J10`, `RULE-4`.
They are greppable, which is the point.

## Before anything else: check what actually exists

The repository now has real code, and it is **partial**. Phase A is largely built (store, renderers,
publish plane, sanitizer, sources, scheduler primitives, generation contract) and Phase B has begun
(auth). Phases C, D and E are mostly not built.

Do not assume a package, table, RPC, or binary named in the plan exists. Inspect the tree first —
`go list ./...` and `git ls-files '*.go'` take a second and are authoritative. The plan describes the
destination; `TODOS.md` describes how far along the road anyone has got, and its per-phase checkbox
counts are the fastest honest answer.

## The things agents get wrong here

- **Branches: `dev` for work, `main` for releases, nothing else.** No feature branches. Promotion
  `dev` → `main` is a release that **deploys**, and it is Cam's call — never promote on your own
  initiative. Mechanics and the reasoning: `CONTRIBUTING.md` → "Branches".
- **Run `sh scripts/setup-hooks.sh` once per clone.** Hook config is per-clone and untracked, so the
  hooks are *not* live until you opt in. `pre-commit` runs gofmt, build, vet, staticcheck and short
  unit tests on staged Go; `pre-push` refuses accidental pushes to `main`. Never `--no-verify`, and
  `AFF_SKIP_HOOKS=1` is for emergencies — a red test is not one. After touching `.githooks/`, run
  `sh scripts/test-hooks.sh` (18 cases); an untested hook is a guard that has quietly stopped
  catching anything.
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
- **The changelog and devlog are part of the change, not paperwork afterwards.** The common failure
  is landing a reversal with no devlog entry, so six months later the decision looks arbitrary and
  gets reverted back. See "What you must update alongside your change" below.

## What you must update alongside your change

Part of finishing a task, not a follow-up. A change that lands without these is incomplete, and the
records are worthless the moment they are known to be partial.

| You did this | Then you must |
|---|---|
| Completed a task | Tick it `[x]` in `TODOS.md` — **only** when its stated check passes |
| Added, split, or dropped a task | Edit `TODOS.md`. Dropped means `[-]` **with a reason**, never deletion |
| Changed observable behaviour, a format, an interface, or a default | Add to `CHANGELOG.md` under `[Unreleased]` |
| Reversed a decision, or research overturned an assumption | Add a `DEVLOG.md` entry — see the trigger below |
| Nearly shipped a bug, or tried an approach and abandoned it | Add a `DEVLOG.md` entry |
| Found the implementation contradicts the plan | Fix `PLAN.md` **in the same change**, and note what it had wrong |
| Answered an open question | Record the answer and date in `PLAN.md` §21, and tick the `OQ-*` task |
| Cut a release | Move `[Unreleased]` to a version heading, tag it |

**Devlog trigger, precisely:** write an entry when something was **learned**, not when something was
merely done. Routine progress belongs in commits. And write the wrong turn down, not only the
destination — a record that keeps only its final state loses the reasoning, and the same rejected
idea gets proposed again by someone who cannot tell it was already considered. Newest entry on top.

**Changelog trigger, precisely:** it answers "what changed, and do I need to care?" for someone
outside this repository. Refactors and internal cleanups do not belong there; a renamed field in a
published feed does. Reasoning belongs in the devlog, not here — a changelog carrying rationale
becomes unreadable.

**Commit messages have a convention**, and `git log` is the primary record of why this project looks
the way it does. Imperative subject under 72 characters, **no `type:` prefix** (those are for
dependabot only), a body explaining *why* and what was wrong before, and stable identifiers so the
log stays greppable. Written down in `CONTRIBUTING.md` → "Commit messages", with `.gitmessage` as an
opt-in template.

The changelog and devlog are **not interchangeable**, and the distinction is `CONTRIBUTING.md`'s
no-duplicated-facts rule applied to history: the changelog is *what*, the devlog is *why*. Neither
is a source of requirements. If either disagrees with `PLAN.md`, the plan is right and the record is
history.

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

## go.mod belongs to the coordinator, and "revert it" is not the safe default

If you are an agent working alongside others: do not edit `go.mod`/`go.sum`, and **do not revert
them either.** Three separate build breakages came from an agent faithfully running
`git checkout -- go.mod go.sum` or deleting `go.sum` to honour "do not touch go.mod" — each time
undoing a dependency a sibling had legitimately needed.

The right move when those files change under you is to **leave them alone and say so in your
report**. A locally correct action can be globally destructive when the build graph is shared, and
the build graph is always shared.

The coordinator adds every dependency before dispatch and owns those two files exclusively.

**On instructions arriving mid-task:** one agent correctly refused a coordinator message that
claimed a dependency had been added, verified the claim itself, and found a way to do the job with
what was already present. That scepticism is right and should be kept. Verify a claim about the
tree against the tree — `go list -m <module>` costs nothing — and if it does not hold, do the task
another way and report the discrepancy rather than acting on an unverifiable assertion.

## Marking tasks done

`TODOS.md` is only useful if it is accurate, and the failure mode is silent
under-marking rather than over-marking.

- Tick a task the moment its stated check passes, in the same change.
- **Never chain a tick behind `&&` after a verification command.** A batch of
  ticks was once lost exactly that way: `staticcheck ... && python tick.py`
  short-circuited on an unused constant, the script never ran, and thirty-seven
  completed tasks silently stayed open. Run the verification, read it, then tick.
- Audit periodically rather than trusting the running count. Group the checkbox
  lines by prefix and compare against what is actually on disk; a phase showing
  zero done while its package exists and passes is the tell.
