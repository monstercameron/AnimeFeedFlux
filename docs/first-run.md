# First run: fresh clone to a working feed

For whoever is doing this next — probably future Cam, on a machine where nothing is set up. This
is the CLI path: clone, configure, create an admin, create a feed, sample it, promote an item,
fetch the URL. It does not need a droplet, DNS, or Docker — that's `deploy/RUNBOOK.md`'s job, and
this doc is explicitly about *before* any of that: getting one feed running anywhere, including a
laptop.

Every command below was checked against `cmd/aff/dispatch.go` and the RPC/CLI source it dispatches
to as of this writing (2026-08-10). Where a step can't be exercised without a deployed host, DNS, or
a real Slack workspace, it's marked **UNVERIFIED** and given as the intended procedure, not a tested
one. `PLAN.md` is the reasoning; this doc is only the commands, kept close enough to the source that
a command that stops existing should make this doc visibly wrong, not silently wrong.

**2026-08-10: this whole sequence was actually run**, end to end, against a fresh scratch database
and its own ports (never the running dev instance's `.devrun/aff.db`), with a deliberately fake
provider key so no real generation call could succeed. Steps 0 through 8 (hooks, env, `admin init`,
TOTP, web build, server build/start, `aff login`, `system settings`, `feed create`/`enable`) were
executed and passed after the corrections below were applied. Step 9 (`aff sample`) was executed far
enough to confirm the *safe-failure* shape — a 401 from the provider, no candidates, nothing
promotable, nothing spent — deliberately not far enough to spend real money. Steps 10 (promote) and
11's Slack/ArticleFlux subscription remain unexercised for the reasons already stated per-step below.
Several things in this doc were wrong before that run; every one of them is called out inline where
it was found, not just listed here, since the surrounding text still needs to reflect reality.

Related: `deploy/RUNBOOK.md` (deploying and operating a live droplet), `PLAN.md` §16 (config
surface, authoritative), §7 (recipes), §12.2 (recovery).

## 0. One-time per clone: hooks

Git hooks live in `.githooks/` but `core.hooksPath` and the commit template are per-clone settings
in `.git/config`, which is **not tracked** — a committed hooks directory cannot enable itself. Run
this once after cloning, or none of the pre-commit/pre-push guards exist and you will not find out
until something they'd have caught ships:

```sh
sh scripts/setup-hooks.sh
```

Verify: `git config core.hooksPath` should print `.githooks`.

## 1. Required environment variables

Environment only — there is no config file (`PLAN.md` §16). The server validates all of these at
boot and fails fast, naming every problem at once, if any required one is missing or malformed.

| Variable | Required | What to set it to for a first local run |
|---|---|---|
| `AFF_DB_PATH` | yes | a path to a SQLite file that does not need to exist yet, e.g. `./aff.db` — the server creates and migrates it on first boot |
| `AFF_PUBLIC_BASE_URL` | yes | an absolute URL with scheme, e.g. `http://localhost:9310` — this is baked into every item guid forever, so if you later deploy for real, use the real public URL from the start rather than migrating off a placeholder |
| `AFF_PUBLISH_ADDR` | yes | e.g. `127.0.0.1:9310` |
| `AFF_ADMIN_ADDR` | yes | e.g. `127.0.0.1:9311` — this is also what `aff` dials via `--server`/`AFF_ADMIN_ADDR` |
| `AFF_ALLOWED_ORIGINS` | yes | comma list of `scheme://host` for the admin WebSocket `Origin` check, e.g. `http://localhost:9311` — a bare hostname matches nothing |
| `AFF_SECRET_KEY` | yes | `openssl rand -base64 48` — encrypts the TOTP secret at rest; also required by `aff admin init`/`aff admin reset` since those touch the DB directly |
| `SCHEMAFLUX_API_KEY` | yes | the LLM provider credential SchemaFlux reads. **Not** `OPENAI_API_KEY` — that name is a trap; SchemaFlux does not look for it. Sampling and generation fail without a real key; nothing else in this walkthrough needs it |

Everything else in `PLAN.md` §16's table (`AFF_GENERATION_ENABLED`, `AFF_MAX_CONCURRENT_RUNS`,
`AFF_PROVIDER_MAX_INFLIGHT`, `AFF_SCHEDULE_JITTER`, `AFF_CACHE_MAX_BYTES`, `AFF_LOG_LEVEL`,
`AFF_OTEL_*`, `OTEL_*`, `AFF_BACKUP_DIR`, `AFF_SLACK_WEBHOOK_URL`, `AFF_LIVE_LLM`) is optional with a
sane default; leave them unset for a first run.

**The pepper is real but not in that table — say it plainly because it has already bitten:**
`internal/config/config.go` also reads `AFF_PASSWORD_PEPPER` (optional; an HMAC-SHA256 secret mixed
into the admin password hash) and `AFF_PASSWORD_PEPPER_VERSION`. If you set a pepper, it lives
**only** in the environment, **never** in the database — that's the entire point of it, a stolen DB
file alone must not be enough. Consequences that are easy to get wrong:

- If the pepper is lost, **no password verifies**, ever — not "extra security," a hard lockout, and
  `aff admin reset` cannot fix it either, because reset still hashes under whatever pepper is
  currently set.
- Back it up **separately** from the database. Backing it up alongside the DB (same disk, same
  backup archive, same secrets file that also contains a DB snapshot) defeats the entire reason it
  exists — the two secrets must never be recoverable from the same compromise.
- It is optional. Skipping it for a first local run is fine; treat it as a decision to make
  deliberately before anything real depends on the DB, not a knob to flip later without a plan for
  where the value lives.

**Windows only — timezones will silently block feed creation without this.** The server binary does
not embed `time/tzdata` (confirmed by grepping for it — nothing imports it), and Windows has no
system IANA timezone database the way Linux/macOS do. Confirmed by running it: with no `ZONEINFO` set
and no `GOROOT` env var in the shell, `aff feed create` with any real zone (e.g.
`"timezone": "America/New_York"`, step 8 below) fails with `timezone: timezone is not a recognised
IANA zone` — this is validated **server-side**, so the fix has to be set in the server's environment,
not the CLI's. The standard Go toolchain ships a usable copy at
`$(go env GOROOT)/lib/time/zoneinfo.zip`; point `ZONEINFO` at it before starting the server (step 5):

```sh
export ZONEINFO="$(go env GOROOT)/lib/time/zoneinfo.zip"
```

`"UTC"` as the timezone needs no zone database lookup and works with or without this set, if avoiding
the dependency entirely is preferable for a quick first run. This is a real gap, not a doc-only
issue — worth an eventual `_ "time/tzdata"` blank import in `cmd/animefeedflux` so a fresh Windows
clone doesn't need this env var at all, but that's a Go source change outside this doc's scope.

Set the required ones (`sh`/Git Bash syntax — use `$env:NAME = "..."` per line in PowerShell):

```sh
export AFF_DB_PATH=./aff.db
export AFF_PUBLIC_BASE_URL=http://localhost:9310
export AFF_PUBLISH_ADDR=127.0.0.1:9310
export AFF_ADMIN_ADDR=127.0.0.1:9311
export AFF_ALLOWED_ORIGINS=http://localhost:9311
export AFF_SECRET_KEY=$(openssl rand -base64 48)
export SCHEMAFLUX_API_KEY=your-key   # not-a-real-key: paste the real one here
```

## 2. Initialise the database and create the admin

There is no separate "init the DB" step — `aff admin init` opens `AFF_DB_PATH` directly (local
filesystem access, no server needed) and runs migrations as part of opening it. So this one command
does both:

```sh
aff admin init
```

It refuses to run if an admin already exists (use `aff admin reset` instead if you're locked out —
`PLAN.md` §12.2 covers that path; it resets password, TOTP, *and* recovery codes together, which is
a deliberately different, stronger tradeoff than the recovery-code path below). It prompts for a new
password (rejected if weak), then prints, **once**:

- a `otpauth://` provisioning URI — scan it with an authenticator app (Google Authenticator, Authy,
  1Password, etc.) right now; it is not retrievable again
- ten single-use recovery codes

**Store the password in a password manager.** Neither the password nor the TOTP secret nor the
recovery codes can be displayed again after this command returns.

Confirmed by running it: at a real interactive terminal, the password prompt suppresses echo as
expected. If stdin is piped instead (scripting this for a test, or CI) it falls back to a plain read
and prints `aff: stdin is not a terminal, echo cannot be suppressed` to stderr before reading the
password in cleartext from stdin — expected and harmless for a scripted run, but do not pipe a real
password into this on a machine where the shell history or process list is visible to anyone else.

## 3. Enroll TOTP and store recovery codes

"Enrolling TOTP" *is* scanning the URI from step 2 — there's no separate enrollment command for a
fresh `admin init`. Confirm it worked as soon as you can, since a broken enrollment found later means
a full `aff admin reset`.

**Ordering correction, found by actually running this doc in order: you cannot do that "as soon as
you can" yet.** `aff login` is a gRPC call to `AuthService.Login` — it needs `AFF_ADMIN_ADDR` to have
something listening, and nothing does until step 5 (build and start the server) has run. Confirmed by
running `aff login` before starting the server: it fails to connect, not with an auth error. Do steps
4–5 first, then come back here and run:

```sh
aff login
```

It will prompt for the password, then a TOTP code — type the 6-digit code your authenticator app is
currently showing. Success means enrollment is good and a session is saved.

**Correction: `$AFF_SESSION_FILE` is not a real environment variable — grepping the source for it
finds nothing that reads it.** The default session path is `--session-file`'s stated default, the OS
user config dir (`os.UserConfigDir()/aff/session.json` — on Windows,
`%APPDATA%\aff\session.json`), and the *only* way to override it is the `--session-file PATH` flag
on every command, not an env var. This matters more than a naming nit: that default path is one fixed
location **per OS user, shared across every `aff` invocation on the machine regardless of which
`AFF_DB_PATH`/`AFF_ADMIN_ADDR` it's pointed at.** Logging into a scratch/test instance with the
default session path silently overwrites the session file for any other instance (e.g. a real running
dev server) on the same machine. Pass `--session-file` explicitly whenever more than one `aff`-backed
instance exists on the same box, e.g. `aff login --session-file .devrun/session.json`, and every
following command in that session needs the same flag.

**Recovery codes: store them somewhere that is not this machine**, and understand the constraint
before you need it (`PLAN.md` §12.2 — no email infra, one admin, exactly two ways back in):

- Each recovery code buys **exactly one** sensitive action: a password reset *or* a TOTP
  re-enrollment — never both from the same code. The elevated session a code opens ends the instant
  either action succeeds.
- The realistic lockout is "new phone, lost authenticator" — needing *both* a fresh TOTP enrollment
  and a fresh password in one sitting. That costs **two** codes out of the ten, not one. Budget for
  that when deciding how many codes you can afford to lose track of before you're down to your last
  couple (the dashboard nags at ≤2 remaining, once there is a dashboard to nag).
- The other way back in, if codes run out or are lost, is `aff admin reset` — but that requires
  direct filesystem/SSH access to the box the DB lives on. If this deploys somewhere you don't have
  shell access to later, the recovery codes are the *only* way back in. Store them accordingly.

## 4. Build the web bundle

```sh
sh scripts/build-web.sh
```

This compiles the admin GWC/WASM UI into `web/dist/` (override with `SERVE_DIR=...`).

**STALE NOTE CORRECTED 2026-08-10: the server now serves it.** `internal/publish/static.go`'s
`StaticHandler` is constructed and mounted on the admin listener in
`cmd/animefeedflux/wire.go` (`publish.NewStaticHandler(cfg.AdminStaticDir)`, wired into `adminMux`) —
this used to be true (nothing outside `internal/publish` referenced `NewStaticHandler`) but is not
anymore; grepping today finds the wiring call. Confirmed by running it: after `sh scripts/build-web.sh`
staged `web/dist/` and the server started with `AFF_ADMIN_STATIC_DIR=web/dist`, the admin listener had
a static bundle available rather than API-only. A missing/unbuilt `web/dist/` is still non-fatal — the
server logs a clear startup warning and serves the control-plane API alone — so building the bundle
remains optional for a CLI-only walkthrough, but it is no longer inert. Every step below still goes
through the `aff` CLI rather than the browser UI, since that's what this doc is testing, not because
the UI is unreachable.

## 5. Build and start the server

```sh
make build
./bin/animefeedflux
```

(`make build` requires `CGO_ENABLED=0`, which the Makefile sets itself — no cgo toolchain needed.)

**Windows correction: `make` itself is not guaranteed to be present.** Confirmed on a real Windows
box with Go and Git Bash installed but no `make` on `PATH` — `make build` fails with `command not
found` before anything Go-related even runs. Without `make`, run what the target does directly
(same `CGO_ENABLED=0`, no cgo toolchain needed either way):

```sh
CGO_ENABLED=0 go build -trimpath -o bin/animefeedflux.exe ./cmd/animefeedflux
./bin/animefeedflux.exe
```

(Omitting the `Makefile`'s `-ldflags` just means `version`/`commit`/`build_date` in the startup log
read `dev`/`unknown`/`unknown` instead of real values — cosmetic only, confirmed by running it this
way.)

It logs a `starting` line with the resolved addresses, then serves the publish plane
(`AFF_PUBLISH_ADDR`) and admin/control plane (`AFF_ADMIN_ADDR`) until `SIGINT`/`SIGTERM`. Leave it
running in this terminal; do the rest from a second one with the same environment variables set (at
minimum `AFF_ADMIN_ADDR` and `AFF_DB_PATH`, since `aff admin`/local commands and `aff <rpc-command>`
both read them).

Confirm it's actually up and correct, not just that the process didn't crash:

```sh
curl -s http://localhost:9310/healthz
aff doctor
```

`aff doctor` (needs `AFF_DB_PATH`) checks DB open/integrity/migrations, WAL size, provider-key
presence (by env var name only, never the value), and disk space in one pass, and exits non-zero if
anything's unhealthy.

## 6. Log in from the CLI

Already done in step 3 (the physical login command runs there, not here — see step 3's ordering
correction for why: it needs the server, which only exists from step 5 onward). This section stays as
a placeholder so the step numbers in the rest of this doc and in `TODOS.md`'s `U0-*` items don't shift.
If picking this doc up mid-way and not already logged in, run step 3's `aff login` (with
`--session-file` if more than one instance is in play) before continuing.

## 7. Global publishing/spend settings

**STALE NOTE CORRECTED 2026-08-10: this is no longer a gap.** `PLAN.md` §12.5/§13 describe global
settings (publishing defaults — base URL, author, copyright, `og:image` — and the global daily spend
ceiling) backed by `SystemService.GetSettings`/`UpdateSettings` (`proto/aff/v1/system.proto`). An
earlier version of this doc said `aff system` didn't expose that RPC — it now does:
`cmd/aff/system_cmd.go`'s `cmdSystem` wires up `settings get`/`settings set` alongside `stats`,
`kill-switch`, `backup`, `version` (`cmd/aff/dispatch.go:95`). Confirmed by running it:

```sh
aff system settings get
```

```sh
aff system settings set \
  --base-url "http://127.0.0.1:9310" \
  --author "Cam" \
  --copyright "Cam" \
  --spend-ceiling-usd 5
```

`settings set` printed a clean before/after diff for exactly the fields passed
(`global_daily_spend_ceiling_usd: 0 -> 5`, etc.) and left everything else untouched. The full flag
list is in `aff --help`'s `system settings set` line: `--base-url --author --contact --copyright
--ttl-minutes --cache-control --og-image --spend-ceiling-usd --token-ceiling
--default-token-budget --default-run-budget --default-feed-window --staleness-minutes`. Do this before
step 8 (creating the first feed) if a global ceiling matters from the start — per-feed
`daily_token_budget`/`daily_run_budget` (step 8) still apply underneath it, so setting a global
ceiling here isn't a substitute for those, just a floor above all feeds combined.

The equivalent per-feed publishing fields (`--author`, `--copyright`, `--og-image`, `--ttl-minutes`)
also have a real CLI path — they're plain flags on `aff feed create`/`aff feed update` (step
8) — so "publishing defaults" work at both the global and per-feed level today.

## 8. Create the first feed from a recipe

A feed's recipe (`FeedSpec`) is too nested for one flag per field, so `feed create`/`feed update`
take it as JSON via `--spec-json` or `--spec-file`, parsed with `protojson` (camelCase field names,
enum values as their string names). Write a recipe file, e.g. `trivia-recipe.json`:

```json
{
  "cron": "0 7 * * *",
  "timezone": "America/New_York",
  "itemsPerRun": 1,
  "feedWindow": 50,
  "model": "gpt-4o-mini",
  "temperature": 0.9,
  "systemPromptTemplate": "You write one anime trivia question per day. Never repeat a listed title. Keep the answer out of the question text.",
  "userPromptTemplate": "Today is {{.Weekday}}, {{.Today}} ({{.Season}}). Write {{.ItemsPerRun}} trivia question(s) for \"{{.FeedTitle}}\". Do not repeat any of: {{.RecentTitles}}",
  "novelty": {
    "noveltyWindowItems": 500,
    "similarityThreshold": 0.86
  },
  "dailyTokenBudget": 200000,
  "dailyRunBudget": 5
}
```

(Field names, defaults, and the full template-variable list are `PLAN.md` §7's authority; this is
one reasonable starting point, not a fixed template — expect to iterate the prompt via sampling,
which is the whole reason `feed create`'s validation and `aff sample` exist separately from just
writing the migration by hand.)

Create the feed:

```sh
aff feed create \
  --slug anime-trivia-daily \
  --kind generative \
  --title "Anime Trivia Daily" \
  --description "One anime trivia question a day." \
  --author "Cam" \
  --copyright "Cam" \
  --spec-file trivia-recipe.json
```

`--slug` is immutable after first publish (`PLAN.md` §14.1) — get it right now, not after
subscribers exist. Note the printed `version:` — every subsequent write to this feed
(`update`/`enable`/`disable`/`delete`) needs `--expected-version` set to the version last read, for
optimistic concurrency.

**New feeds are created disabled.** Confirmed by running it: `feed create`'s printed output showed
`enabled: false` with no flag to change that at create time — `cmdFeedCreate` doesn't set `Enabled` on
the `Feed` it sends, so it defaults to `false`. Enable it (use the version `feed create` printed):

```sh
aff feed enable --expected-version <version> <feed-id>
```

**Flag-order correction, found by running the example as originally written
(`aff feed enable <feed-id> --expected-version <version>`): it fails with `want exactly one feed id
argument`.** Every `aff` subcommand here is built on Go's standard `flag` package, which stops parsing
flags at the first non-flag argument — a flag written *after* the positional id/slug is treated as
another positional argument, not as a flag, and the subcommand's own `fs.NArg() != 1` check then
rejects it. The fix is mechanical: flags before the positional argument, always, for every command
below that takes one (`feed enable`/`disable`, `sample`, `promote`, and any other `aff <cmd> <id>
--flag` shape). This was wrong in three places in earlier drafts of this doc; all three are corrected
in place, not just this one.

## 9. Sample it — iterate the prompt before trusting it

Sampling runs the real generation pipeline against the live provider and **writes nothing**
(`PLAN.md` §12.3/§13 — it's billable, draws from the same budget as scheduled runs, but never
publishes):

```sh
aff sample --size 3 anime-trivia-daily
```

(Flags before the slug — see the flag-order correction in step 8; `aff sample anime-trivia-daily
--size 3` fails with `want exactly one feed slug argument`, confirmed by running it.)

Prints each candidate's title, summary, novelty verdict, any grounded-link check results, and an
estimated cost. Read the summary text for a leaked answer (trivia's spoiler rule, `PLAN.md` §5.5) —
this is the one point in the pipeline that catches it before anything is public. Edit the recipe
file and re-run `aff feed update ... --spec-file trivia-recipe.json --expected-version <n>` between
samples until a candidate looks right.

**Confirmed by running it, deliberately with a fake `SCHEMAFLUX_API_KEY` so nothing real could be
spent:** the failure mode is a clean, immediate RPC error surfaced straight from the provider —
`rpc error: code = FailedPrecondition desc = llm: fatal/account: generation failed: openai API error
(status 401, ... invalid_api_key) ...` — no candidates printed, nothing to promote, nothing charged.
That's the safe way to prove this step's plumbing (server routing, feed lookup, budget checks) works
before ever pointing it at a real key. Only swap in a real `SCHEMAFLUX_API_KEY` and re-run once
you're deliberately ready to spend — this is the first point in the whole walkthrough where real
money moves.

## 10. Promote an item

Promoting a sampled candidate persists it as a real item, `origin = sampled`, stamped **now**
(`PromoteSample` always stamps the current time — `PLAN.md` §5.5's no-backdating rule, because Slack
and every other reader only sees items dated strictly after the last one it saw):

```sh
aff promote --candidate <candidate-id> <sample-id>
```

(Flags before the positional `sample-id`, same flag-order rule as step 8/9 — `promote_cmd.go` parses
identically to `feed enable` and `sample`: `--candidate` after `<sample-id>` would not be recognised
as a flag. Not executed against a real sample in this pass, since that needs a real provider key —
see step 9 — but the parsing shape was verified directly against `cmd/aff/promote_cmd.go`, which is
the same `fs.Parse` args then `fs.NArg() != 1` pattern already confirmed broken-then-fixed twice
above.)

Both ids come from the `aff sample` output above — `sample-id` is on the summary line, each
candidate's own id is printed as `--- candidate N (<candidate-id>) ---`. `--candidate` is only
optional when the sample had exactly one candidate.

## 11. Subscribe to the resulting URL

```sh
curl -s http://localhost:9310/feeds/anime-trivia-daily.xml
```

That's the RSS. `.atom` and `.json` exist at the same path for Atom 1.0 and JSON Feed 1.1. Confirm
the promoted item is in it and the XML is well-formed before calling this done — `make validate`
(golden-file + W3C/RSS-Advisory-Board validator) is the real gate, but eyeballing the curl output
catches the obvious break.

**Subscribing an actual Slack workspace or ArticleFlux instance to this URL is UNVERIFIED here** —
both need a real, publicly reachable HTTPS host (`deploy/RUNBOOK.md` covers getting one live); a
`localhost` URL from this walkthrough is not reachable by Slack's pollers or a separate ArticleFlux
deployment. The intended procedure, once a real host exists:

- **Slack**: in the target channel, `/feed subscribe https://<public-host>/feeds/<slug>.xml`. Slack
  is stricter than the RSS spec (`PLAN.md` §5.5) and its failure mode is silent — it just never
  posts — so treat "it validates" as necessary, not sufficient; watch the channel for the next
  scheduled run.
- **ArticleFlux**: add the same URL as a source feed in its own admin UI. `PLAN.md` §18 lists this as
  phase E0, after the engine and Slack are both proven.

## What "done" looks like at the end of this doc

One feed (`anime-trivia-daily`), enabled, with one promoted item, serving valid RSS/Atom/JSON at
`http://localhost:9310/feeds/anime-trivia-daily.xml` (or the real public URL if `AFF_PUBLIC_BASE_URL`
was set to one), logged into via `aff`, with the admin password in a password manager and the
recovery codes stored off this machine. Repeating steps 8–10 for `anime-fact-daily` and
`anime-news-daily` (grounded — needs `sources` in the recipe JSON) is the same procedure; deciding
the exact prompts, sources, and news cadence is product work this doc deliberately doesn't do for
you (`TODOS.md` `U0-05`…`U0-08`, `OQ-01`/`OQ-03`).
