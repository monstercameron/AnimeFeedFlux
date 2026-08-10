# Contributing to AnimeFeedFlux

This document is short on purpose. Almost everything about how this repository works is already in
`PLAN.md`; what follows is the part you need before your first change.

## Read this first: there is no code yet

The repository is a specification and a build order. If you are picking up work, you are starting a
task from `TODOS.md`, not modifying an existing implementation. Check the phase ordering before you
begin — tasks are in dependency order, and Phase A must be real before Phase B means anything.

## The document set, and which one wins

Four documents, one specification. Each owns something the others must not duplicate, because
duplicated facts drift and drifted facts get implemented.

| Doc | Owns |
|---|---|
| **`PLAN.md`** | The spec of record — architecture, compliance rules, data model, RPC surface, user flows (§22), testing strategy (§17), risks (§20), open questions (§21) |
| **`TODOS.md`** | Build order — atomic tasks in dependency order, standing rules, flow sanity suites, operational runbook |
| **`DEVLOG.md`** | The narrative — dated entries on what was learned and what was reversed. Never a source of requirements; if the devlog and the plan disagree, the plan is right and the devlog is history |
| **`CHANGELOG.md`** | What changed per version, for someone deciding whether to upgrade |

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

## Branches

**`dev` for work, `main` for releases, nothing else.** No feature branches — this is a
single-maintainer repository, and a branch per task buys ceremony rather than isolation.

| Branch | Is | Push to it |
|---|---|---|
| `dev` | The working branch. Everything lands here first. | Freely |
| `main` | What is released and deployed. | Only as a deliberate promotion |

### Promotion, `dev` → `main`

Once the Phase C pipeline exists (§15.3), **a push to `main` builds an image and deploys it to
production.** That makes an accidental push to `main` not an untidy history but a deployment nobody
decided to make — so promotion is explicit, and it is Cam's call. Never promote on your own
initiative.

```bash
git checkout main
git merge --ff-only dev        # main must never diverge from dev
AFF_PROMOTE=1 git push origin main
git checkout dev
```

`--ff-only` is the important flag. If it refuses, `main` contains something `dev` does not, which
means production is running code that never sat on the working branch — fix that rather than
forcing it through with a merge commit.

`main` is also protected **server-side**, because a hook is client-side and one clone that never ran
`setup-hooks.sh` has none of it: force pushes are refused, deletion is refused, linear history is
required, and the rules apply to admins too. Those three are exactly the operations that cannot be
undone, which is why they are enforced where a bypass is not possible. Once CI exists, required
status checks join them (§17.2).

The `pre-push` hook enforces three further things locally, so none of them depend on remembering:

- pushing any ref other than `main` **to** `main` is refused (a refspec typo is how a feature branch
  becomes production);
- promoting without `AFF_PROMOTE=1` is refused, so it never happens as a side effect of `git push`;
- promoting a commit that is not contained in `dev` is refused.

### Hooks

Hook configuration lives in `.git/config`, which is per-clone and not tracked, so a committed file
cannot enable itself. Opt in once per clone:

```bash
sh scripts/setup-hooks.sh
```

That sets `core.hooksPath` to [`.githooks/`](.githooks) and the commit template, and prints what is
now active. **Verify it took** (`git config core.hooksPath`) — a hook everyone believes is running
and isn't is worse than no hook at all.

`pre-commit` refuses staged secrets and databases, then — only when Go files are staged — runs, in
order: `gofmt -l` on the staged files, `go build`, `go vet`, `staticcheck` if installed, and
`go test -short ./...`. Format first, because it is the cheapest and the most likely to fail.

Two deliberate choices:

- **Formatting is checked, never auto-applied.** A hook that rewrites files after they were staged
  commits something you did not read, and the diff you reviewed is not the diff that lands. It tells
  you the `gofmt -w` command to run.
- **`staticcheck` warns and continues if it is not installed**, because a hook that hard-fails on a
  tool someone has not got is a hook they turn off. CI is the authoritative lint gate (§17.2). If it
  *is* installed, it must pass.

It deliberately does **not** run the race detector, the feed validator, or the soak; those are CI's
job, and putting them here would make the hook slow enough that people bypass it. While the
repository has no `go.mod`, the Go checks skip themselves.

`AFF_SKIP_HOOKS=1` exists for genuine emergencies. **A failing test is not an emergency** — it is the
hook working.

The hooks are themselves tested. `sh scripts/test-hooks.sh` builds a throwaway repository and
exercises 16 cases across both hooks — secrets blocked, documentation mentioning a key variable
allowed, broken Go blocked, docs-only commits skipping the Go checks, and every branch of the
promotion guard. **Run it after touching `.githooks/`.** Hooks are code, and the failure mode of an
untested one is a guard everybody believes is running that has quietly stopped catching anything.

## Commit messages

The convention here is descriptive of what the history already does, not imported from elsewhere.
`git log` is the primary record of *why* this project looks the way it does, and it is read far more
often than it is written.

### Subject

```
<imperative verb> <what>[: <elaboration>][; <second concern>]
```

- **Imperative mood, sentence case, no trailing period.** "Add DEVLOG.md", not "Added" or "Adds".
- **Aim for ≤72 characters.** The existing history runs 29–72.
- **No `type:` prefixes.** This repository does not use Conventional Commits, and a hand-written
  `feat:` here is inconsistent with every commit around it. The one exception is **bot commits** —
  dependabot is configured to prefix `deps`, `deps(ci)`, `deps(docker)`, which is how automated
  noise stays filterable and distinguishable from decisions a human made.
- Use `:` to elaborate on one concern, `;` to separate genuinely distinct ones. If you need more than
  two semicolons, the commit is doing too much.
- Name the artefact when the change is scoped to one: "Add PLAN.md section 14: multi-feed operation".

### Body

Wrap at 72–80 columns. Blank line after the subject. Then:

- **Explain why, not what.** The diff already says what. A body that restates the diff in prose is
  worse than no body, because it costs a reader time to discover it taught them nothing.
- **State what was wrong before**, if this is a fix or a reversal. "X contradicted Y" beats "improve
  X". The reader six months from now is trying to work out whether it is safe to change it back.
- **Reference stable identifiers** — `A4-12`, `§9.6`, `J10`, `BF-11`, `RULE-4`. They are greppable.
- **Record rejected alternatives** when you considered one seriously. A commit that says why the
  obvious approach was not taken prevents someone re-taking it.
- Prose paragraphs for reasoning; bullets for enumerating changes. Do not use bullets for everything —
  a wall of bullets loses the argument that connects them.
- Plain ASCII in the body. Some tooling on this box mangles anything else.

### Trailers

- `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>` on commits Claude authored.

### Worked example

The commit that introduced this guide:

```
Document the commit message convention in CONTRIBUTING.md

The convention was consistent across twelve commits but written down
nowhere, so it survived only as long as someone kept reading the log
before writing. This makes it explicit and derives it from the existing
history rather than importing one: imperative subjects under 72 chars,
no type: prefixes for humans, why-not-what bodies, stable identifiers.

Prefixes are kept for dependabot only. Mixing "deps(ci): bump x" with
bare human subjects is not inconsistency, it is the thing that makes
bot noise filterable.

Adds .gitmessage as a template. It is not wired up automatically -
git config is per-clone and not tracked - so CONTRIBUTING names the
one command.
```

### Template

[`.gitmessage`](.gitmessage) carries the above as commented prompts. It is **not** wired up
automatically — `git config` is per-clone and not tracked — so opt in once per clone:

```bash
git config commit.template .gitmessage
```

### Discipline

- **Never amend or reset a pushed commit.** Add a new one.
- Commit or push only when asked. If you are on the default branch, branch first.
- Do not skip hooks (`--no-verify`) or bypass signing.
- Stage deliberately — `git commit -- <paths>` beats `git add -A` when the tree has unrelated work.

## Pull requests

The template asks for the same things: why, the task ID, the plan section, what you actually ran,
and which records you updated. See [`.github/PULL_REQUEST_TEMPLATE.md`](.github/PULL_REQUEST_TEMPLATE.md).

## Keeping the devlog

Add an entry when something was **learned**, not when something was merely done. Good triggers: a
decision was reversed, research overturned an assumption, a bug was nearly shipped, or an approach
was tried and abandoned.

Write the wrong turn down, not just the destination. A plan that records only its final state loses
the reasoning that makes it defensible later, and the same bad idea gets proposed again by someone
who cannot tell it was already considered and rejected. Routine progress belongs in commits.

## Security

Do not open a public issue for a vulnerability affecting a running instance. See
[`SECURITY.md`](SECURITY.md).

## Code of conduct

By participating you agree to abide by [`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md).
