<h1 align="center">AnimeFeedFlux</h1>

<p align="center">
  <strong>Feeds that write themselves.</strong><br>
  Declare a recipe — "a daily anime trivia question", "today's anime news, ranked" —<br>
  and it publishes on a schedule as spec-compliant RSS, Atom, and JSON Feed.<br>
  The consumer is Slack, or any reader you already use.
</p>

<p align="center">
  <img alt="Version 0.0.1-dev" src="https://img.shields.io/badge/version-0.0.1--dev-blue">
  <img alt="Status: planning" src="https://img.shields.io/badge/status-planning%20%E2%80%94%20no%20code%20yet-orange">
  <img alt="Go 1.26" src="https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white">
  <img alt="Client: Go to WebAssembly" src="https://img.shields.io/badge/client-Go%20%E2%86%92%20WebAssembly-654FF0?logo=webassembly&logoColor=white">
  <img alt="Transport: gRPC over WebSocket" src="https://img.shields.io/badge/transport-gRPC%20over%20WebSocket-2EA44F">
  <img alt="SQLite FTS5" src="https://img.shields.io/badge/SQLite-FTS5-003B57?logo=sqlite&logoColor=white">
  <img alt="MIT licence" src="https://img.shields.io/badge/License-MIT-yellow.svg">
</p>

---

## Status: `v0.0.1-dev` — planning. There is no code yet.

This repository currently contains a specification and a build order, nothing else. No `go.mod`, no
binary, no running instance. The badges above describe what is designed, not what is built, and
there are deliberately **no CI or demo badges** because there is nothing for them to report on yet.

The version tags the **specification**. `-dev` is deliberate: it sorts below any future `0.0.1`
under semver precedence, so a real build cannot be shadowed by this tag. The first version meaning
"you can run this" is cut at the end of Phase C.

If you are here expecting software, come back after Phase C. If you are here to read a plan before
it becomes software, that is exactly what this is.

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

Built on the same stack as the rest of the Flux projects:

- **[GoWebComponents](https://github.com/monstercameron/GoWebComponents)** — the admin UI, Go
  compiled to WebAssembly
- **[GoGRPCBridge](https://github.com/monstercameron/GoGRPCBridge)** — real gRPC from the browser
- **[SchemaFlux](https://github.com/monstercameron/schemaflux)** — typed LLM operations, so the model
  returns a Go value instead of text to parse
- Go, SQLite with FTS5, nginx, one droplet

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

Core engine first, UI last. The engine is complete, deployed, and delivering feeds to Slack before a
single component is written — so every RPC the UI will call has already been exercised by the CLI,
and no screen gets built twice because a service changed shape.

| Phase | What | Ends with |
|---|---|---|
| **A** | Core engine | Feeds render, validate, and generate. Proven by tests. |
| **B** | Control surface | Auth, RPC, bridge, CLI. Fully operable without a browser. |
| **C** | Ship it | Container, pipeline, staging, **Slack proof**, ops, production |
| **D** | UI | Five GoWebComponents pages against RPCs already proven in production |
| **E** | After | ArticleFlux integration; multi-feed work, deferred until it is needed |

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
