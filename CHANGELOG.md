# Changelog

Notable changes to AnimeFeedFlux. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

This file answers **"what changed, and do I need to care?"** For *why* a thing changed — including
the decisions that were made and then reversed — see [`DEVLOG.md`](DEVLOG.md). Keeping the two apart
is deliberate: a changelog that carries reasoning becomes unreadable, and a narrative that carries
version diffs becomes unmaintainable.

No software is released yet — nothing built here has been deployed anywhere, and no feed has ever
been published. See `README.md` → "Status" for what is actually built versus what is still planned.

`0.0.1-dev` versioned the **specification**, not an implementation; `-dev` sorts below any real
release under semver precedence, so tagging a real build later cannot be shadowed by an early one.
The number stays in the `0.0.x-dev` range through the build phases, and the first version that means
"you can run this" is the one cut at the end of Phase C.

## [Unreleased]

Everything below landed on `dev` since `0.0.2-dev`. It is a large amount of code — the engine,
Phase A, and the headless control surface, Phase B, are both substantially built — but **none of it
is running anywhere.** There is no staging host, no production deploy, and Slack has never been
pointed at a real instance. If you're deciding whether to upgrade or deploy: there is nothing yet to
deploy *to*; this is still development-only.

### Added — Phase A, the core engine

- **Store** (`internal/store`): schema, migrations, a reader/writer split, WAL boot ordering, and
  FTS5 search, all with migration tests. Complete.
- **Renderers** (`internal/render`): RSS 2.0, Atom, JSON Feed, and permalink HTML from seeded items,
  checked against golden files and three fuzz targets for well-formedness. Complete.
- **Compliance** (`internal/feedvalidate`): external-validator-clean output and Slack-compatibility
  checks — the date tag, item ordering, and no-duplicate-timestamp rules Slack's RSS app enforces
  silently. Complete.
- **Generation** (`internal/generate`): SchemaFlux wired for typed model output, trivia and grounded
  news paths exercised end-to-end on recorded cassettes, plus a soak test. Most of it is done; a
  handful of tasks remain.
- **Novelty** (`internal/novelty`): embedding-based dedup and retry, proven against a seeded
  near-duplicate corpus. Nearly complete.
- **Grounded news** (`internal/sources`, `internal/urlnorm`): source fetch, candidate
  normalization, and the structural link-integrity rule — a published link must be byte-equal to a
  URL actually fetched, never model-generated. Most of it is done.
- **Scheduler** (`internal/schedule`): cron with timezone/DST handling, jitter, a worker pool,
  budget accounting, and a kill switch. Most of it is done.
- **Sampling**: a dry-run pipeline that returns items, rendered XML, and validator verdicts without
  writing to the store. Complete.
- **Publish plane** (`internal/publish`): conditional GET, HEAD, gzip, caching, the 404/410/405
  cases, rate limiting, and a poll-load check proving it stays read-only under load. Complete.
- **Cross-cutting**: fuzz targets for the renderers, the HTML sanitizer, and URL normalization; the
  generation soak test; the publish-plane poll-load check; all wired into CI. Complete.

### Added — Phase B, the control surface (still headless)

- **Auth** (`internal/auth`): argon2id password hashing, TOTP, recovery codes, sessions, and
  backoff, plus `aff admin init`. Most of it is done.
- **RPC services** (`internal/rpc`, `gen/aff/v1`): all six services, an auth interceptor,
  optimistic concurrency, and pagination. Complete.
- **Bridge** (`internal/bridge`): GoGRPCBridge wired in, `Origin` checking, keepalive pairing, and
  streaming RPCs verified. Complete.
- **CLI** (`cmd/aff`): drives create, sample, promote, run, and history end to end. Nearly complete.
- **Flow sanity tests** (`internal/flowtest`): headless suites that drive a whole user flow and
  assert on resulting system state rather than a mock's call log. Mostly green.

### Added — Phase C, shipping (just started)

- `Dockerfile`: multi-stage build, `CGO_ENABLED=0`, distroless runtime, no shell in the final image.
- `deploy/`: a `compose.yaml` and nginx config, for later use — nothing here is running against
  them yet. The CI/CD pipeline (GHCR push, tag scheme), staging host, Slack proof, ops runbook, and
  production deploy are all still ahead.

### Added — repository

- CI workflow (`.github/workflows/ci.yml`) covering docs/hygiene checks, hook and script tests,
  `go build`/`go vet`/`staticcheck`/`go test -race`/`govulncheck`, a coverage ratchet, the fuzz
  targets, and `make validate` against the external feed validator — gated by a single `CI gate`
  aggregating job, so branch protection never needs updating when a job is added.
- New real dependencies: `modernc.org/sqlite` (pure-Go SQLite with FTS5), `schemaflux`,
  `GoGRPCBridge`, `GoWebComponents`, `go.opentelemetry.io/otel` and its exporters, `golang.org/x/crypto`
  and `github.com/pquerna/otp` (auth), `github.com/BurntSushi/toml`, `google.golang.org/grpc`.

### Decided

- **SQLite driver: `modernc.org/sqlite`** (A0-16). `CGO_ENABLED=0` and a `distroless/static` runtime
  rule out a cgo driver; `modernc` is pure Go and ships FTS5.

## [0.0.2-dev] — 2026-08-09

Repository process, not product. Still no application code.

### Added

- `DEVLOG.md` — the narrative record, including reversals.
- Commit message convention, derived from the existing history rather than imported, with
  `.gitmessage` as an opt-in template. Conventional Commits was considered and rejected.
- `dev`/`main` branch model. `dev` is the working branch and the GitHub default; `main` is what gets
  released and, once Phase C lands, deployed.
- Git hooks in `.githooks/`, opt-in per clone via `scripts/setup-hooks.sh`. `pre-commit` refuses
  staged secrets and databases, then runs gofmt, build, vet, staticcheck and `go test -short` on
  staged Go. `pre-push` guards `dev` → `main` promotion behind `AFF_PROMOTE=1`.
- `scripts/test-hooks.sh` — 18 cases across both hooks, in a throwaway repository.
- Server-side protection on `main`: force push, deletion and non-linear history refused, admins
  included.
- Repository description and topics.

### Fixed

- The credential guard matched bare *mentions* of key variable names, which blocked its own commit
  and would have blocked `PLAN.md` §16 and `SECURITY.md`. It now matches an assignment with a
  plausible value, with narrow named exclusions for hook tooling.
- `.gitattributes` did not reach extensionless hook files, so they would have checked out with CRLF
  and failed as `bad interpreter: /bin/sh^M`.
- `AGENTS.md` still described a two-document spec and a repository containing only `PLAN.md` and
  `TODOS.md`.

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
