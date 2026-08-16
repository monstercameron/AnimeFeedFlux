<h1 align="center">AnimeFeedFlux</h1>

<p align="center">
  <strong>Feeds that write themselves.</strong><br>
  Declare a recipe — "a daily anime trivia question", "today's anime news, ranked" —<br>
  and it publishes on a schedule as spec-compliant RSS, Atom, and JSON Feed.<br>
  The consumer is Slack, or any reader you already use.
</p>

<p align="center">
  <img alt="Version 0.2.0" src="https://img.shields.io/badge/version-0.2.0-blue">
  <img alt="Status: deployed and serving" src="https://img.shields.io/badge/status-deployed%20%26%20serving-brightgreen">
  <a href="https://github.com/monstercameron/AnimeFeedFlux/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/monstercameron/AnimeFeedFlux/actions/workflows/ci.yml/badge.svg?branch=dev"></a>
  <img alt="Go 1.26" src="https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white">
  <img alt="Client: Go to WebAssembly" src="https://img.shields.io/badge/client-Go%20%E2%86%92%20WebAssembly-654FF0?logo=webassembly&logoColor=white">
  <img alt="Transport: gRPC over WebSocket" src="https://img.shields.io/badge/transport-gRPC%20over%20WebSocket-2EA44F">
  <img alt="SQLite FTS5" src="https://img.shields.io/badge/SQLite-FTS5-003B57?logo=sqlite&logoColor=white">
  <img alt="MIT licence" src="https://img.shields.io/badge/License-MIT-yellow.svg">
</p>

---

## Status: `v0.2.0` — deployed and serving

The whole system is live: the engine, the publish plane, the gRPC control plane, and the
WebAssembly admin UI all run in production, in Docker behind nginx on a single host. Feeds render
and serve as spec-compliant RSS/Atom/JSON Feed; the admin UI covers first-run setup (password +
mandatory TOTP + recovery codes), recipe editing with live preview sampling, per-feed scheduling
(fixed cadence, ad hoc, or *watch* — "check on a schedule, post only when something happened"),
provider/key management, run history, and spend tracking.

Releases ship themselves. Tagging `vX.Y.Z` on `main` runs a verify → build → publish pipeline
(full test suite with the race detector, `govulncheck`, then a container image to GHCR), and the
production host pulls the new tag via a secret-gated deploy hook with a health-gated rollout and a
recorded rollback point — no inbound SSH from CI. The v0.2.0 release exercised the failure path
for real: the verify gate failed on a flaky test, nothing was published or deployed until it was
diagnosed and green (`DEVLOG.md`, 2026-08-16).

What is genuinely not proven yet: the **live Slack proof** (`TODOS.md` C3). Every Slack constraint
below is enforced mechanically and covered by tests, but a real Slack workspace polling a real feed
for a week has not happened. That distinction — enforced vs. observed — is kept honest on purpose.

## The bet

Any RSS reader can subscribe to a feed. Nothing stops that feed from being *written on demand*.

AnimeFeedFlux is a feed **generator**. A recipe carries a prompt, a schedule, a model, and a budget;
the scheduler runs it; an LLM produces the items; the result is published at a stable URL that any
reader can subscribe to. The product surface is one sentence: **a URL that returns valid XML and
never lies.**

A recipe can also declare **web search** (the provider's built-in search tool), giving the model
live web access for that feed's runs — which is what makes *watch* feeds real: without the
declaration a model has no web access at all, whatever the prompt asks. Generative feeds only; a
grounded feed's links must come from its fetched sources, so the combination is refused at save
time rather than allowed to fail at publish time.

## Two kinds of item, and why the distinction is the whole design

Conflating these is the main way this project fails, so they are separated at the architecture level
rather than by convention.

| | **Generative** | **Grounded** |
|---|---|---|
| Examples | trivia, fact-of-the-day, on-this-day | news, releases, seasonal roundups |
| The model is | the source | only an editor |
| Content lives | here, on our own permalinks | at the publisher; we summarize and link |
| Principal risk | repetition, and being wrong | hallucinated URLs |
| Handled by | an embedding novelty gate | structural link integrity |

**Hallucinated links are prevented structurally, not by prompting.** Upstream sources are fetched
first, their URLs normalized once, and only that candidate set is shown to the model. A published
link must be byte-equal to a URL that was actually fetched. An invented URL is not "unlikely" — it
is unpublishable.

## Slack is a first-class consumer, and it is stricter than the spec

Slack's RSS app fails *silently*: it does not error, it just stops posting. Being valid RSS is not
sufficient. It requires a date tag on every item, items in sequence, and **no duplicate timestamps**
— so a news run publishing three items at once would have had two silently dropped. It also keeps a
bookmark of the newest date it has seen, which means a backdated item is invisible forever and
**editing an item never re-delivers it**.

Those constraints shaped real decisions: distinct strictly-increasing timestamps enforced by a
database constraint, a no-backdating rule, plain-text `description` with the rich HTML in
`content:encoded`, OpenGraph tags for unfurls, and a correction mechanism instead of silent edits.
Trivia answers are kept out of `description` so the channel preview cannot spoil the question.

## Architecture

Two planes, and the split is the security boundary:

- **Publish plane** — plain HTTPS, `GET`/`HEAD` only, unauthenticated, holding a **read-only**
  database handle. It serves the feeds. A bug here cannot corrupt data, because the code path has no
  writer.
- **Control plane** — gRPC over WebSocket, authenticated, on a separate IP-allowlisted host. Every
  mutation is an RPC. There is no REST/JSON API.

Built on the same stack as the rest of the Flux projects. What's actually in `go.mod` today:

- **[GoWebComponents](https://github.com/monstercameron/GoWebComponents)** — the admin UI, Go
  compiled to WebAssembly; no JavaScript framework, no npm
- **[GoGRPCBridge](https://github.com/monstercameron/GoGRPCBridge)** and `google.golang.org/grpc` —
  real gRPC from the browser over WebSocket
- **[SchemaFlux](https://github.com/monstercameron/SchemaFlux)** — typed LLM operations, so the model
  returns a Go value instead of text to parse; v1.2.0's `WebSearch()` is what powers per-feed web
  search
- **`modernc.org/sqlite`** — pure-Go SQLite with FTS5; chosen so the binary stays `CGO_ENABLED=0`
  and can run in a `distroless/static` image (§15.1)
- **`go.opentelemetry.io/otel`** and friends — tracing/metrics, exported over OTLP
- **`golang.org/x/crypto`** and **`github.com/pquerna/otp`** — argon2id password hashing and TOTP,
  for the auth system in `internal/auth`
- **`github.com/BurntSushi/toml`** — recipe/config parsing
- **`github.com/oklog/ulid/v2`** — opaque item identity (§5.1)
- Go, SQLite with FTS5, nginx, one host — the container runs `CGO_ENABLED=0` in
  `distroless/static`, data lives in a named Docker volume, and both planes sit behind nginx
  vhosts with Let's Encrypt TLS (the control plane additionally behind an IP allowlist)

## Documents, and which one wins

| Doc | Owns |
|---|---|
| **`PLAN.md`** | The spec of record — architecture, compliance rules, data model, user flows (§22), risks, open questions |
| **`TODOS.md`** | Build order — atomic tasks in dependency order, each citing a plan section |
| **`DEVLOG.md`** | The narrative — what was learned, and the decisions that were reversed |
| **`CHANGELOG.md`** | What changed per version |

`PLAN.md` wins. If `TODOS.md` contradicts it, `TODOS.md` is wrong and gets fixed. **If the
implementation contradicts `PLAN.md`, the plan is wrong — and it gets corrected in the same change,
not later.** A spec that has quietly drifted from the code is worse than no spec, because it still
gets trusted.

Refer to things by their stable identifiers — `§5.5`, `§9.6`, `A4-12`, `BF-11`, `J10` — rather than
by prose. They are all greppable, which is the point.

## Build order

Core engine first, UI last. The engine was complete, deployed, and serving before the first UI
component was written — so every RPC the UI calls had already been exercised by the CLI, and no
screen got built twice because a service changed shape. That ordering held all the way through.

| Phase | What | Status |
|---|---|---|
| **A** | Core engine | Done — store, renderers, compliance, generation, novelty, grounded news, scheduler, sampling, publish plane, fuzz/soak/load. |
| **B** | Control surface | Done — auth, RPC services, browser bridge, CLI, headless flow-sanity suite. |
| **C** | Ship it | Done except the live Slack proof (C3) — container, CI/CD release pipeline, production deploy, pull-based self-update, ops runbook. |
| **D** | UI | Done — setup, generate workbench, history, settings, i18n (en/es), browser journey suite. |
| **E** | After | Where new work lands — two-stage per-surface formatting, schedule modes, per-feed web search, and the rest of the post-ship series live in `TODOS.md`. |

`TODOS.md` is the live ledger (roughly 720 tasks checked as of v0.2.0, with the open tail
enumerated rather than implied); exact per-milestone state belongs there, not here, because a
count in a README is stale the day after it is written.

## What this will not do

- No multi-tenancy. One admin, no authorization model — authentication is the entire defense.
- No reader UI. ArticleFlux is already one, and it will subscribe to these feeds.
- No republishing of full upstream article text. Summarize and link, always.
- No horizontal scaling. SQLite has one writer; separation means a second instance with its own
  database, not a cluster.

## Honesty about the hard part

Trivia will occasionally be factually wrong, and there is no cheap oracle for it. The mitigations are
narrow claims, a source citation where the model can give one, a report link, and a correction
mechanism that actually reaches subscribers. A nonzero error rate is the honest expectation rather
than a bug to be closed, and the design says so in `PLAN.md` §20 rather than quietly hoping.

## Licence

MIT. See [`LICENSE`](LICENSE).
