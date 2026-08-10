<h1 align="center">AnimeFeedFlux</h1>

<p align="center">
  <strong>Feeds that write themselves.</strong><br>
  Declare a recipe — "a daily anime trivia question", "today's anime news, ranked" —<br>
  and it publishes on a schedule as spec-compliant RSS, Atom, and JSON Feed.<br>
  The consumer is Slack, or any reader you already use.
</p>

<p align="center">
  <img alt="Version 0.0.2-dev" src="https://img.shields.io/badge/version-0.0.2--dev-blue">
  <img alt="Status: building, not deployed" src="https://img.shields.io/badge/status-building%2C%20not%20deployed-orange">
  <a href="https://github.com/monstercameron/AnimeFeedFlux/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/monstercameron/AnimeFeedFlux/actions/workflows/ci.yml/badge.svg?branch=dev"></a>
  <img alt="Go 1.26" src="https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white">
  <img alt="Client: Go to WebAssembly" src="https://img.shields.io/badge/client-Go%20%E2%86%92%20WebAssembly-654FF0?logo=webassembly&logoColor=white">
  <img alt="Transport: gRPC over WebSocket" src="https://img.shields.io/badge/transport-gRPC%20over%20WebSocket-2EA44F">
  <img alt="SQLite FTS5" src="https://img.shields.io/badge/SQLite-FTS5-003B57?logo=sqlite&logoColor=white">
  <img alt="MIT licence" src="https://img.shields.io/badge/License-MIT-yellow.svg">
</p>

---

## Status: `v0.0.2-dev` — building, not deployed

This is no longer a specification with no code behind it: the core engine (store, renderers,
generation, novelty, grounded news, scheduler, publish plane) and the headless control surface
(auth, RPC, CLI, browser bridge) are both substantially built and covered by tests, including fuzz
targets, a soak test, and a poll-load check that run in CI. A container build exists. None of that
adds up to a running product yet — **nothing is deployed anywhere, no feed has ever been published,
and the Slack integration has never been exercised against a live instance.** There is still no
demo badge above, for that reason: there is nothing to point it at.

See "Build order" below for what is done, in progress, and not started, phase by phase. The
version still tags progress against the plan rather than a release; the first version meaning "you
can run this" is still cut at the end of Phase C, which has barely started (container image only —
no CI/CD pipeline, no staging host, no Slack proof, no production deploy).

If you are here expecting a running feed, it is not there yet. If you are here to read the design
or the code against the plan it was built from, that is exactly what this is.

## The bet

Any RSS reader can subscribe to a feed. Nothing stops that feed from being *written on demand*.

AnimeFeedFlux is a feed **generator**. A recipe carries a prompt, a schedule, a model, and a budget;
the scheduler runs it; an LLM produces the items; the result is published at a stable URL that any
reader can subscribe to. The product surface is one sentence: **a URL that returns valid XML and
never lies.**

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
  compiled to WebAssembly (not started — Phase D)
- **[GoGRPCBridge](https://github.com/monstercameron/GoGRPCBridge)** and `google.golang.org/grpc` —
  real gRPC from the browser, wired and tested (Phase B)
- **[SchemaFlux](https://github.com/monstercameron/schemaflux)** — typed LLM operations, so the model
  returns a Go value instead of text to parse
- **`modernc.org/sqlite`** — pure-Go SQLite with FTS5; chosen so the binary stays `CGO_ENABLED=0`
  and can run in a `distroless/static` image (§15.1)
- **`go.opentelemetry.io/otel`** and friends — tracing/metrics, exported over OTLP
- **`golang.org/x/crypto`** and **`github.com/pquerna/otp`** — argon2id password hashing and TOTP,
  for the auth system in `internal/auth`
- **`github.com/BurntSushi/toml`** — recipe/config parsing
- **`github.com/oklog/ulid/v2`** — opaque item identity (§5.1)
- Go, SQLite with FTS5, nginx, one droplet — the droplet, nginx vhosts, and TLS are still Phase C
  work; nothing above is deployed to them yet

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

Core engine first, UI last. The plan is for the engine to be complete, deployed, and delivering
feeds to Slack before a single UI component is written — so every RPC the UI will call has already
been exercised by the CLI, and no screen gets built twice because a service changed shape. That
ordering is holding: Phases A and B are the ones with real progress, and Phase D has not been
touched.

Task counts below are `[x]` checkboxes in `TODOS.md`, not a judgment of quality — see `PLAN.md` §18
for what each phase's milestones actually require.

| Phase | What | Status |
|---|---|---|
| **A** | Core engine | In progress. Most milestones (store, renderers, compliance, sampling, publish plane, fuzz/soak/load) are done; skeleton and generation have a handful of tasks left. |
| **B** | Control surface | In progress. RPC services and the browser bridge are done; auth and the CLI are nearly done; the headless flow-sanity suite is mostly green. |
| **C** | Ship it | Just started. A Dockerfile and compose setup exist; the CI/CD pipeline, staging host, Slack proof, ops runbook, and production deploy have not begun. |
| **D** | UI | Not started. |
| **E** | After | Not started. |

Exact counts, from `TODOS.md`, as of this writing:

| Phase | Milestone | Done |
|---|---|---|
| A | A0 Skeleton | 43/60 |
| A | A1 Store | 26/26 |
| A | A2 Renderers | 21/21 |
| A | A3 Compliance | 8/8 |
| A | A4 Generation | 32/38 |
| A | A5 Novelty | 10/12 |
| A | A6 Grounded news | 16/19 |
| A | A7 Scheduler | 17/21 |
| A | A8 Sampling | 9/9 |
| A | A9 Publish plane | 24/24 |
| A | AF Fuzz/soak/load | 17/17 |
| B | B0 Auth | 56/70 |
| B | B1 RPC services | 19/19 |
| B | B2 Bridge | 7/7 |
| B | B3 CLI | 10/11 |
| B | BF Flow sanity (headless) | 47/53 |
| C | C0 Container | 16/19 |
| C | C1–C5 | 0 of 63 |
| D | D0–D5 | 0 of 99 |
| E | — | 0 of 42 |

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
