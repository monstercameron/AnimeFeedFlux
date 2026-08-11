# Drills

Companion to `TODOS.md` `U2` ("Drills, performed not described") and, weekly/monthly/quarterly,
`U1`. `PLAN.md` §15 (operations), §19 (definition of done), and §12.2 (recovery) carry the reasoning;
this file carries only the commands, the observable outcome at each step, and — the actual point —
how each drill would fail *silently* if you only glanced at it.

**Update 2026-08-10: five of these have now actually been run**, against a fully isolated scratch
instance (`.devrun/drill/` — its own DB at `.devrun/drill/db/drill.db`, its own ports 18081/18082,
its own `AFF_SECRET_KEY`; the live dev server on `.devrun/aff.db`/8081/8082 was never touched) seeded
with `cmd/affseed` (3 feeds, 53 items, through real store/rpc code — not hand-inserted rows). Results
are recorded inline in each drill's section below, tagged **PERFORMED 2026-08-10**. Everything not
tagged that way is still exactly what it was: transcribed from source, never executed. `U2-02`
(rollback) and the alerting half of `U2-06` (staleness) remain fully blocked — no deployed host, no
webhook sink — and are unchanged.

One correction to the previous version of this file: it said "there is no `aff recover` command."
That was true when written and is no longer true — `aff recover` and `aff auth
change-password`/`reenroll-totp` now exist (`cmd/aff/recover_cmd.go`, `cmd/aff/auth_cmd.go`) and were
exercised directly, below (`U2-03`).

Commands verified to exist in `cmd/aff/dispatch.go`'s top-level switch, as of this writing:
`login`, `recover`, `auth` (`change-password|reenroll-totp`), `feed`, `recipe`, `sample`, `promote`,
`run`, `runs`, `item`, `system`, `admin` (`init|reset|reset-password`), `backup`, `restore`, `verify`,
`encrypt`, `decrypt`, `prune`, `stale`, `doctor`.

**A syntax trap worth recording, hit while running these drills**: `aff`'s flag package stops parsing
flags at the first non-flag argument. `aff system kill-switch off --server ADDR --session-file PATH`
fails with `want at most one argument (on or off)` because `off` is positional and everything after
it is then *also* read as positional. Global flags must come **before** the trailing positional
argument: `aff system kill-switch --server ADDR --session-file PATH off`. This is not a bug — it's
how Go's `flag` package always works — but it is easy to trip over and the error message doesn't say
why.

---

## Drill index

| ID | Drill | §-anchor | Status |
|---|---|---|---|
| U2-01 | Restore into a scratch instance | §19 item 8, §15 | **PERFORMED 2026-08-10** against seeded data; mechanism confirmed, real-production-data re-run still open |
| U2-02 | Rollback to previous tag | §18 (C5) | Blocked — no deployed image, no GHCR tag history |
| U2-03 | Recovery-code login recovery | §19 item 6, §12.2 | **PERFORMED 2026-08-10**, complete |
| U2-04 | Break-glass `aff admin reset` | §12.2 | **PERFORMED 2026-08-10**, complete |
| U2-05 | Kill-switch, feeds still serve | §13 | **PERFORMED 2026-08-10 — found a real defect**, see below |
| U2-06 | Staleness alert fires | §15 | Read half (`aff stale`) performable; alert half blocked — no webhook sink |
| `aff doctor` (no U2 ticket) | Healthy vs. corrupted DB | — | **PERFORMED 2026-08-10**, complete |
| U1-* | Recurring operations | §12.4, §15, §13, §12.5, §19 | Still blocked — need `U0` feeds live and production history |

---

## U2-01 — Restore drill

*Restore a backup into a scratch instance; confirm identical feeds. `TODOS.md` `U2-01`, `C4-06`;
`PLAN.md` §15 "Backups", §19 item 8; commands cross-checked against `deploy/RUNBOOK.md`'s "Restoring
a backup" section, which this expands with silent-failure checks that section doesn't spell out.*

**PERFORMED 2026-08-10.** Against `.devrun/drill/db/drill.db` (own scratch DB, seeded with 3
feeds/53 items via `cmd/affseed`, never `.devrun/aff.db`):

```
aff backup --db .devrun/drill/db/drill.db --out .devrun/drill/backups --keep 14
  -> wrote: .devrun\drill\backups\drill-1786368421392073100.db (268.0 KiB), integrity: ok
aff verify .devrun/drill/backups/drill-1786368421392073100.db
  -> ok
aff restore --from <backup> --to .devrun/drill/db/scratch-restore.db --yes
  -> restored: ..., verified: ok
```

Started a *second* server process (own ports 18091/18092) pointed at `scratch-restore.db`, then:

- `curl http://127.0.0.1:18091/feeds/daily-anime-trivia.xml` vs. the same request against the
  original on 18081: `diff` showed differences **only** in port-derived fields — `<link>`,
  `<atom:link href>`, guid `tag:` prefixes, `<lastBuildDate>` (the restored server's own boot time) —
  every item's title/summary/order was identical.
- `aff item list --feed 1 --page-size 50 --json` against source and restored: **`diff` was empty.**
  This is the check that actually matters per the section below — content, not "it opened."
- As a bonus, the session token minted against the *source* DB was still valid against the restored
  copy on the different port, confirming the `sessions` table round-tripped too, not just
  `feeds`/`items`.

**What this proves and what it doesn't**: the `VACUUM INTO` + independent-`verify` + content-diff
mechanism is sound end to end — no silent truncation, no WAL-copy trap, byte-identical item content
after a real restore onto a fresh file. **What it doesn't prove**: this was seed/placeholder content
inserted through `cmd/affseed` (real store/rpc code paths, not hand-rolled SQL, but still synthetic),
not data accumulated from actual generation/promotion runs against a live provider over time. Treat
this as "the mechanism works," not as a substitute for re-running this drill once `U0`'s launch feeds
have real production history — the failure mode this drill guards against (a `-wal`/`-shm` copy
silently missing the newest transaction) is exactly the kind of thing that could look identical on
synthetic vs. real data but isn't guaranteed to.

**Preconditions**

- A completed nightly backup file (or one taken on demand with `aff backup`), and, if
  `AFF_BACKUP_ENCRYPTION_KEY` is set on the source, the matching key to decrypt it.
- A Docker host to restore *onto* — does not need to be the droplet; `deploy/RUNBOOK.md`'s scratch
  procedure explicitly says "any Docker host."
- **Blocked today**: this drill needs a source database that has actually accumulated at least one
  real generated or promoted item to compare against — nothing is deployed, so there is no such
  database yet. The command sequence below is checked against `cmd/aff` and is correct as written;
  running it against an *empty* just-migrated database would technically "pass" while proving nothing
  about restore fidelity, which is exactly the kind of false-positive this drill exists to catch. Do
  not perform this drill for real, or check the box, against a database with fewer than a handful of
  real items in it.

**Steps**

1. Take (or locate) a backup and, separately, capture what "correct" looks like from the *source*
   database before touching anything:
   ```sh
   aff backup --out ./drill --keep 14 --db /path/to/live/animefeedflux.db
   aff item list --feed <feed-id> --page-size 50 --json > /tmp/before.json
   ```
   Expected: `aff backup` prints `integrity: ok` and a path under `./drill`. `aff item list` prints a
   JSON array — this is the content baseline the restored copy must match, not just "the file opens."

2. Verify the backup file's integrity **independently of restoring it** — this is the step that
   catches the WAL-mode trap `PLAN.md` §15 names explicitly (a raw file copy of `.db`/`-wal`/`-shm`
   at three different instants "opens cleanly and is missing a transaction"; `VACUUM INTO` plus this
   check is the whole reason that trap doesn't apply here):
   ```sh
   aff verify ./drill/animefeedflux-<date>.db
   ```
   Expected: `<path>: ok`. **If this step is skipped**, the restore below can succeed against a file
   that is already truncated, and nothing downstream will say so — a `VACUUM INTO` snapshot that
   itself got corrupted in transit (partial copy off the box, encryption/decryption bug) opens fine
   and serves a feed with a gap nobody notices until a subscriber asks "where did last Tuesday's item
   go."

3. If the backup is encrypted (nightly backups are, once `AFF_BACKUP_ENCRYPTION_KEY` is configured —
   `C4-03`'s note that the copy still never leaves the box is about *transport*, not encryption, which
   is already wired), decrypt it first:
   ```sh
   AFF_BACKUP_ENCRYPTION_KEY=<key> aff decrypt --in ./drill/animefeedflux-<date>.db.enc --out ./drill/animefeedflux-<date>.db
   ```
   Expected: `wrote: ./drill/animefeedflux-<date>.db` with a plausible byte size (compare to the
   source DB's size — a wildly smaller output is the first sign of silent truncation).

4. Restore into a **scratch** destination, never the live path:
   ```sh
   aff restore --from ./drill/animefeedflux-<date>.db --to ./drill/scratch.db --yes
   ```
   Expected: `restored: ./drill/scratch.db`, `verified: ok`. `aff restore` itself calls
   `ops.Verify` on the source before writing and re-verifies the destination after — that is *not* a
   substitute for step 2 or step 6, because "the file passes SQLite's own integrity check" says
   nothing about whether it has the same items the live database had.

5. Point a real server process at the scratch file and let it actually render feeds — a restore drill
   that stops at `aff verify`/`aff doctor` never proves the render path works against the restored
   schema:
   ```sh
   AFF_DB_PATH=./drill/scratch.db AFF_PUBLIC_BASE_URL=http://localhost:19310 \
     AFF_PUBLISH_ADDR=127.0.0.1:19310 AFF_ADMIN_ADDR=127.0.0.1:19311 \
     AFF_ALLOWED_ORIGINS=http://localhost:19311 AFF_SECRET_KEY=$(openssl rand -base64 48) \
     SCHEMAFLUX_API_KEY=not-a-real-key-not-called-in-this-drill ./bin/animefeedflux &
   curl -s http://localhost:19310/feeds/<slug>.xml
   ```
   Expected: valid RSS, item count and titles matching `/tmp/before.json`.

6. **The check that actually matters — content, not "it opened":** diff the restored feed's items
   against the pre-restore baseline, not just eyeball it.
   ```sh
   AFF_DB_PATH=./drill/scratch.db aff item list --feed <feed-id> --page-size 50 --json > /tmp/after.json
   diff /tmp/before.json /tmp/after.json
   ```
   Expected: empty diff (or a diff explainable by activity between the backup and the snapshot, if the
   source was live during backup — the nightly job is scheduled precisely so this window is quiet).

**How this silently fails**

- **The single failure mode this whole drill exists for**: a restored database opens cleanly, `aff
  doctor` and `aff verify` both say "ok", the server boots, `/healthz` is green — and the newest item
  is missing, because the backup was taken from a `-wal`/`-shm` file-copy rather than `VACUUM INTO`
  (this shouldn't be possible given `C4-01`'s implementation, but the drill exists to prove that, not
  assume it). Step 6's diff is the only step that would catch this; steps 1–5 alone would all report
  success.
- A decrypt (step 3) using the *wrong* key can, depending on cipher mode, produce a file that still
  passes a superficial open but fails `aff verify`'s integrity check — which is why step 2 happens
  *before* decrypt is trusted and step "verify after restore" (built into `aff restore`) happens
  again after.
- Restoring against a database with zero or near-zero content (see the precondition blocker above)
  makes every step here report success trivially — an empty diff against an empty baseline proves
  nothing.

---

## U2-02 — Rollback drill

*Deploy, roll back to the previous tag, confirm service. `TODOS.md` `U2-02`; `PLAN.md` §18 (C5 "done
when... rollback actually performed"), §15.2–§15.3; commands from `deploy/RUNBOOK.md` "Rollback".*

**Preconditions**

- A running deployment with at least two distinct `sha-` tags in GHCR history (i.e. at least one
  prior release to roll back to).
- **Blocked today, entirely**: nothing is deployed (`C0`–`C5` in `TODOS.md` are open), so there is no
  running container, no image history, and no `.previous-tag` file for this drill to operate on.
  Every step below is transcribed from `deploy/RUNBOOK.md` and `scripts/rollback.sh`'s documented
  behavior, not exercised.

**Steps**

1. Note the currently-running tag before touching anything, so "rolled back" has a starting point to
   compare against:
   ```sh
   curl -s https://anime.earlcameron.com/healthz | jq .          # or docker inspect for the image digest
   sh scripts/rollback.sh --list
   ```
   Expected: the list shows recent `sha-` tags and recent commits on `main`, with the currently
   deployed one identifiable.

2. Trigger the rollback:
   ```sh
   sh scripts/rollback.sh              # previous tag
   # or, explicitly:
   sh scripts/rollback.sh sha-abc1234
   ```
   Expected: this re-triggers the release workflow with an explicit `image_tag` input (per
   `deploy/RUNBOOK.md`, `scripts/rollback.sh` does **not** touch `main` — it redeploys an existing
   immutable tag). Watch the GitHub Actions run to completion.

3. Confirm the deploy step's own health gate passed, not just that the workflow returned green:
   ```sh
   gh run view --log <run-id> | grep -i healthy
   ```
   Expected: the deploy job waited on the container healthcheck and only reported success after it
   went healthy (`PLAN.md` §15.3 — "the deploy step waits on the container healthcheck and fails the
   job if it never goes healthy").

4. Verify end to end, the same script a normal release uses:
   ```sh
   sh scripts/deploy-verify.sh
   ```
   Expected: exits 0 against the **public** HTTPS path.

5. **The check that actually matters**: confirm the rolled-back version is serving the *previous*
   behavior, not merely that some container is healthy. Compare `aff system version`'s reported
   build/commit against the tag you rolled back to, and re-fetch a feed to confirm its content matches
   what the previous version would have produced (e.g., if the rollback undoes a renderer change,
   confirm the old rendering is what's served).
   ```sh
   aff system version
   curl -s https://anime.earlcameron.com/feeds/<slug>.xml | head -50
   ```

**How this silently fails**

- A rollback that "succeeded" per the healthcheck but is still serving the **new** image is the
  specific failure `deploy/RUNBOOK.md` calls out: "a rollback that succeeded per the healthcheck but
  serves the wrong content is still a broken rollback." The healthcheck only proves the container
  answers `/healthz` — it says nothing about which image is inside it, so step 5 is not optional.
- After a rollback, `main` still points at the bad commit (`deploy/RUNBOOK.md` is explicit about
  this). A drill that stops at "service is back" without noting that `main` and production now
  disagree sets up the *next* promotion to silently re-deploy the bug that was just rolled back —
  this is a real, documented gotcha, not a hypothetical.
- `GITHUB_TOKEN`-authenticated pushes do not trigger other workflows (`PLAN.md` §15.2, and this
  user's own standing note `github-token-push-triggers-nothing.md`) — if a future rollback mechanism
  were rebuilt around "push a tag and let something react," it would silently do nothing. The current
  `scripts/rollback.sh` approach (re-trigger the workflow directly with an `image_tag` input) sidesteps
  this, but it's worth re-verifying if that script is ever touched.

---

## U2-03 — Recovery drill

*Lock yourself out, recover with a code, reset, re-login. `TODOS.md` `U2-03`; `PLAN.md` §12.2, §19
item 6; flow assertions at `PLAN.md` §22 "J7", tested headless at `TODOS.md` `BF-31`…`BF-35`.*

**PERFORMED 2026-08-10 — the CLI path this section previously said didn't exist now does, and the
drill is complete.** `aff recover` and `aff auth change-password`/`reenroll-totp` were added
(`cmd/aff/recover_cmd.go`, `cmd/aff/auth_cmd.go`) since this file was last written. Run against the
`.devrun/drill/` scratch instance (own DB, own ports, own admin — never a real admin's credentials):

- **Action 1 — password reset.** `aff recover`, chose `1` (password), spent recovery code
  `KYQEE-...`: server replied "Recovery code accepted. **9** recovery code(s) remain." (started at
  10), then prompted for and accepted a new password. `aff login` with the OLD password/TOTP then
  failed ("authentication failed"); `aff login` with the NEW password + the SAME (unchanged) TOTP
  secret succeeded.
- **Reuse check.** Immediately re-ran `aff recover` with the SAME already-spent code:
  "recovery failed" (the generic anti-oracle message), and the next successful use still reported
  count-minus-one from 9, not from some lower number — confirming the failed reuse did not silently
  decrement anything.
- **Action 2 — TOTP re-enrollment**, spending a *different*, unused code: "Recovery code accepted.
  **8** recovery code(s) remain," then a fresh `otpauth://` URI. The OLD TOTP secret immediately
  stopped authenticating (`aff login` → "authentication failed"); the NEW one worked. `aff login` was
  required again, as documented.
- **One code, one action, confirmed in code**: `internal/rpc/auth.go`'s `ChangePassword` (line ~602)
  and `ReenrollTOTP` (line ~744) both call `revokeOtherSessions` + `endElevatedSession` the instant
  either succeeds *when the caller is elevated* — there is no path from one recovery-code use to both
  actions. This is the concrete evidence behind `OQ-06` (still open, not resolved by running this
  drill — this only confirms what the current behavior *is*): the realistic "lost phone" scenario
  (need both a new password and a new TOTP enrollment) costs **two** recovery codes in one sitting,
  observed directly above (10 → 9 → 8), not inferred from reading the code.

**What this doesn't cover**: `BF-32` ("does the elevated session actually expire at 10 minutes, and
is it actually scoped to nothing but these two RPCs") was not separately probed here — the CLI's
own design (elevated token never written to `session.json`, consumed within one process invocation)
makes it structurally hard to even attempt a second RPC with it, which is suggestive but not the same
as `BF-32`'s explicit test. Auth-event logging (`BF-35`) was not independently queried against the
DB in this pass either (no `sqlite3` CLI available in this environment) — trust the unit tests for
that assertion until a direct query is run.

**Steps (the procedure as actually run above)**

1. Simulate lockout: on a scratch database with a real admin enrolled, discard the TOTP secret and/or
   forget the password (do not do this against the only copy of a real admin's credentials).
2. Navigate to `/recover` (once served) or call `AuthService.RecoverWithCode` directly with one of the
   ten codes printed at `aff admin init` time.
   Expected: a 10-minute elevated session, per `PLAN.md` §12.2.
3. Choose exactly one of `ChangePassword` or `ReenrollTOTP` — the UI/RPC ends the elevated session the
   instant either succeeds (`internal/rpc/auth.go`'s `endElevatedSession`), so this is a real
   fork, not a checklist you can do both items on.
4. Confirm forced re-login: the elevated session should be gone, requiring a fresh
   `AuthService.Login` with the new credential.
5. Confirm every other session was revoked (`BF-33`) and the recovery-code count decremented by
   exactly one (`BF-34`), and that the attempt landed in `auth_events` (`BF-35`).

**How this silently fails, once it can be performed**

- Ending elevation on **either** action succeeding, not both, means a "new phone, lost authenticator"
  lockout — needing a fresh password *and* a fresh TOTP enrollment in the same sitting — silently
  costs **two** recovery codes, not one, exactly as `OQ-06` (open, undecided) describes. A drill run
  that only exercises one action and declares victory would miss this cost entirely; run the drill
  covering the realistic "lost phone" case (both actions needed) at least once, specifically to see
  the two-codes-consumed reality rather than reading about it.
- A recovery session that silently outlives its 10-minute window, or that can reach an RPC outside
  `ChangePassword`/`ReenrollTOTP`, would be invisible unless deliberately probed — `BF-32` (an
  existing but unchecked `TODOS.md` item) is exactly this test and should pass before this drill is
  trusted.
- Because there is no CLI/UI path yet, the only way to "perform" this today is a raw gRPC client
  against `AuthService` — and a hand-rolled client is itself a source of false confidence: it might
  prove the RPC works while saying nothing about whether a real human, using the real interface, could
  find and complete this flow under actual lockout stress. Treat a raw-gRPC pass as partial evidence,
  not as satisfying this drill.

---

## U2-04 — Break-glass drill

*`aff admin reset` over SSH. `TODOS.md` `U2-04`; `PLAN.md` §12.2 path 2; command at
`cmd/aff/admin_cmd.go`'s `cmdAdminReset`.*

**PERFORMED 2026-08-10** against `.devrun/drill/db/drill.db` (SSH itself out of scope locally — the
command only ever needed local filesystem + `AFF_SECRET_KEY`, exactly as this section predicted).

```
aff admin reset --db .devrun/drill/db/drill.db
  -> Admin credentials reset. All existing sessions were revoked.
  -> fresh otpauth:// URI + 10 fresh recovery codes, printed once
```

Confirmed all three assertions:

1. A session token captured *before* the reset, reused after: `rpc error: code = Unauthenticated
   desc = session expired`. Old sessions are dead.
2. The OLD password + OLD TOTP combo: `aff login: authentication failed`. Old credentials are dead.
3. The NEW password + NEW TOTP combo: `Logged in.` New credentials work immediately.

No surprises versus the documented behavior below — password, TOTP, and recovery codes were all
reset together, exactly as `cmdAdminReset`'s doc comment says, and step 3 (confirm old access is
gone) was not skipped despite that being called out below as the step most likely to be skipped under
real pressure.

**Preconditions**

- Direct filesystem access to the SQLite file (`--db` or `AFF_DB_PATH`) and `AFF_SECRET_KEY` set to
  the value the live encryption of the TOTP secret was done under. This command does **not** need a
  running server or network access to it — it opens the DB file directly, which is the entire point
  of break-glass (`PLAN.md` §12.2: "requiring local DB access").
- An admin row must already exist (`cmdAdminReset` refuses with "no admin account exists yet; run
  `aff admin init` instead" if not).
- **Performable today**, unlike `U2-01`/`U2-02`/`U2-03` — this only needs a local database, which can
  be a scratch one created with `aff admin init`. Not yet performed, but nothing external blocks it.

**Steps**

1. On the host with filesystem access to the live (or scratch) DB:
   ```sh
   aff admin reset --db /path/to/animefeedflux.db
   ```
   Expected: a password prompt (no echo). Enter a new password meeting the policy (15–128 chars, not
   on the compromised-password blocklist — `SEC-11`/`SEC-15`). On success, the command prints, once:
   a fresh `otpauth://` provisioning URI and ten fresh recovery codes, and the message "Admin
   credentials reset. All existing sessions were revoked."

2. Scan the new URI into an authenticator app immediately — it is not retrievable again — and store
   the ten codes off-machine, exactly as `docs/first-run.md` §3 describes for `admin init`.

3. Confirm every other session is actually gone, not just that the command claimed so:
   ```sh
   # from a terminal that still holds an OLD session file/cookie, pre-dating the reset:
   aff runs --page-size 1
   ```
   Expected: `Unauthenticated` — the old session must be rejected, since `cmdAdminReset` calls
   `st.RevokeAllSessions` and clears its own local session file (`clearSession(a.SessionFile)`) as
   part of the same command.

4. Log in fresh with the new password and TOTP code:
   ```sh
   aff login
   ```
   Expected: `Logged in.`

**How this silently fails**

- `aff admin reset` resets password, TOTP, **and** recovery codes all at once — a deliberately
  stronger tradeoff than the narrower `aff admin reset-password` (`PLAN.md` §12.2 path 3, which
  touches only the password). If someone runs this drill expecting the narrower behavior — "just
  reset my password, TOTP is fine" — they will discover only after the fact that their existing
  authenticator enrollment is now dead and they must re-scan. The drill should explicitly confirm
  which of the two commands was intended before running it; the two are easy to confuse under stress,
  which is exactly the condition break-glass is used in.
- If `AFF_SECRET_KEY` used for the reset differs from the one the *server process* uses, the reset
  succeeds (it only needs the key to encrypt the new TOTP secret) but the running server, using its
  own env's key, cannot decrypt the secret it verifies TOTP codes against — this looks identical to
  "TOTP codes stopped working" and is easy to misdiagnose as a broken authenticator app rather than a
  key mismatch. Confirm both the CLI invocation and the running server's `AFF_SECRET_KEY` come from
  the same source before trusting a failed TOTP check after reset.
- Step 3 is the step most likely to be skipped in a real emergency (the operator is focused on getting
  back in, not on proving the *old* access is gone) — but a break-glass reset whose whole point is
  "lock out whoever/whatever had access before" is unverified without it.

---

## U2-05 — Kill-switch drill

*Disable generation, confirm feeds still serve. `TODOS.md` `U2-05`; `PLAN.md` §13.*

**PERFORMED 2026-08-10 — publish-plane half passed cleanly; the generation-refusal half found a real
defect.** Run against `.devrun/drill/` (3 feeds, 53 seeded items).

**The part that worked**: `aff system kill-switch off` (note: global flags must precede the trailing
`on`/`off` — see the syntax-trap note near the top of this file), then before/after comparison of
`GET /feeds/daily-anime-trivia.xml` — identical `200 OK`, identical 19 `<item>` count, identical
`Last-Modified`, with the switch off. The publish plane genuinely does not consult generation state,
exactly as `PLAN.md` §2's architecture claims. `aff system kill-switch on` afterward correctly
reported `generation is now ENABLED` and did not latch.

**The part that didn't**: with the switch off, `aff run daily-anime-trivia` (manual trigger) did
**not** refuse. It ran for 5.5 seconds, and the server log shows a real outbound call —
`"Generate operation started"` → `"LLM request failed"` with a live HTTP 401 from the OpenAI
endpoint (the drill's API key is a placeholder) — and the run recorded `status=FAILED,
reason=fatal` with a real `error_kind=ERROR_KIND_FATAL` from the *provider*, not a
`RUN_STATUS_SKIPPED`/`generation_disabled` refusal. Contrast with `aff sample` against the same feed
with the switch still off: `rpc error: code = FailedPrecondition desc = generation is disabled:
generation_disabled` — instant, no provider call, exactly as designed.

Traced the cause: `FeedService.RunNow` (`internal/rpc/feed.go:993`) hands off to
`wireRunExecutor.ExecuteRun` (`cmd/animefeedflux/wire.go:659`), which calls `generate.Run` directly
with no kill-switch check anywhere in between — no `settings.GetEnabled()` lookup, no budget-style
gate. Compare `sampleBudget.CheckSample` (same file), which explicitly loads settings and returns
`Decision{Allow: false, Reason: "generation_disabled"}` when `!settings.GetEnabled()` — the sample
path has the check, the manual-run path does not. The scheduled-run path (`internal/schedule/
runner.go`'s `Gate`, per its own comment "the kill switch plus budget check") also has it. So of the
three ways generation can start — scheduled, sampled, manually triggered — only the manual-run path
skips the check.

This contradicts two explicit claims: `cmd/aff/run_cmd.go`'s own doc comment ("It is a normal run
through the same budget/kill-switch gates as a scheduled one ... the CLI does not skip them") and
`PLAN.md` §13's "honored by both scheduled runs and sampling" (which, read literally, already omits
manual runs — but the drill's job was to check the actual behavior, not just the wording, and the
actual behavior is the same gap). **This is exactly the failure mode `U2-05`'s "how this silently
fails" section (below) warned about — a kill switch that reports correctly but doesn't actually gate
every path — just on the opposite side from what that section anticipated** (it worried about the
switch bleeding into the *publish* plane or a *scheduled* run slipping through; the actual gap is the
*manual* run path). This file may not touch Go code, so the fix is out of scope here — flagged in
`TODOS.md` `U2-05` for a real fix, not left silently worked around.

**Preconditions**

- A running server with at least one enabled feed that has already published at least one item (a
  feed with zero items would trivially "still serve" an empty channel, proving nothing about the
  actual claim — "existing feeds keep serving").
- **Performable locally today** against a dev instance built per `docs/first-run.md` — does not need a
  deployed host. Not yet performed.

**Steps**

1. Confirm current state and the feed's content before flipping anything:
   ```sh
   aff system kill-switch
   curl -s http://localhost:9310/feeds/<slug>.xml | grep -c '<item>'
   ```
   Expected: "generation is ENABLED", and a non-zero item count.

2. Flip the kill switch off:
   ```sh
   aff system kill-switch off
   ```
   Expected: "generation is now DISABLED (kill switch is on)".

3. Confirm the publish plane is unaffected — this is the actual claim under test, not the switch
   itself:
   ```sh
   curl -si http://localhost:9310/feeds/<slug>.xml | head -5
   curl -s http://localhost:9310/feeds/<slug>.xml | grep -c '<item>'
   ```
   Expected: `200 OK`, same item count as step 1, unchanged `ETag`/`Last-Modified` if nothing else
   wrote in the interim (`PLAN.md` §5.4 — a cache hit never touches SQLite; the kill switch shouldn't
   even be consulted on this path).

4. Confirm generation is actually refused, not just cosmetically reported as off — trigger a manual
   run and confirm it is skipped rather than attempted:
   ```sh
   aff run <slug>
   ```
   Expected: the run is refused or terminates with a `skipped` outcome and a budget/kill-switch
   reason (`PLAN.md` §13: "Budget refusals increment `aff_runs_total{outcome="skipped"}`, not an
   error" — `A7-21`), and critically **no provider call is made** (`BF-15` makes the same assertion
   for the kill switch during sampling).

5. Re-enable and confirm generation resumes:
   ```sh
   aff system kill-switch on
   aff run <slug>
   ```
   Expected: a normal run, `outcome: success` (or a legitimate content-side rejection, not a
   kill-switch refusal).

**How this silently fails**

- The dangerous silent failure here is the **inverse** of what the switch is supposed to do: a kill
  switch that also stops the publish plane from serving cached content is a self-inflicted outage,
  not a safety control. If step 3's `curl` returns anything other than the same content as step 1,
  the kill switch has bled into a code path it was never supposed to touch (`PLAN.md` §2's whole
  architectural point — the publish plane holds a read-only handle and has no reason to consult
  generation state at all) — and that's a defect, not a passing drill.
- A kill switch that reports "DISABLED" but still lets a scheduled cron run slip through (rather than
  a manually triggered one) would not be caught by step 4 alone if step 4 only tests `aff run`
  (manual trigger). `PLAN.md` §13 explicitly says the kill switch is "honored by both scheduled runs
  and sampling" — a full drill should also either wait through one scheduled cron tick with the
  switch off, or inspect the scheduler's own gate in code, to be sure the manual path isn't the only
  one enforcing it.
- Re-enabling (step 5) and skipping the "confirm generation actually resumes" half of the drill would
  miss a kill switch that latches — i.e., a bug where `on` updates the reported state but the
  in-memory gate some other component cached does not observe the change until a restart.

---

## U2-06 — Staleness drill

*Stop a feed generating and confirm the alert actually fires. `TODOS.md` `U2-06`; `PLAN.md` §15
"Staleness is the real failure mode."*

**Preconditions**

- A running server with `AFF_SLACK_WEBHOOK_URL` set to a real, observable sink — a private Slack
  channel or a webhook-capture endpoint (e.g. a temporary `webhook.site` URL) you can watch, since
  the entire premise of this drill is confirming the alert *fires*, not just that the watchdog's
  internal state says stale.
- At least one enabled feed with a known cron/timezone, so its "should have run by" time is
  predictable.
- **Blocked today**: no feed has ever run on a real schedule against a real webhook sink — the drill
  is fully specifiable but has zero real-world evidence behind it. `aff stale` itself (the read path)
  can be exercised locally today against a scratch DB with a feed whose schedule has already lapsed;
  the *alerting* half additionally needs `AFF_SLACK_WEBHOOK_URL` wired to something being watched,
  which nothing currently is.

**Steps**

1. Confirm the baseline: the feed is not currently flagged stale.
   ```sh
   aff stale --db /path/to/animefeedflux.db
   ```
   Expected: `no stale feeds (N checked)`, exit 0.

2. Stop the feed from generating without disabling it outright — the realistic failure this drill
   models is "the scheduler silently stopped firing," not "an operator disabled the feed on purpose."
   The cleanest way to simulate this without touching Go code: flip the global kill switch (`U2-05`)
   or stop the server process entirely, and let real wall-clock time pass the feed's next scheduled
   run plus the grace window (`ops.DefaultStaleGrace = 2.0` schedule intervals, per
   `internal/ops/cli.go` — for a daily feed that is roughly two days, which makes this drill slow to
   run for real; a feed with a short cron, e.g. hourly, set up specifically for this drill is the
   practical way to keep it to minutes).

3. Re-check with `aff stale` once past the grace window:
   ```sh
   aff stale --db /path/to/animefeedflux.db --grace 2.0
   ```
   Expected: the feed is listed as stale, with `last_success_at`/age/threshold populated (or "never
   succeeded" if it has no successful run at all — `internal/ops/stale_cmd.go`'s explicit branch for
   that case).

4. Confirm `/healthz` reflects the same state, since that (not `aff stale`, which is a manual
   diagnostic) is what an external uptime checker actually watches:
   ```sh
   curl -s http://localhost:9310/healthz | jq .
   ```
   Expected: the feed's staleness surfaced there too. **This is currently an open gap** —
   `TODOS.md`'s `C4-08` is explicitly unchecked with the note "nothing wires `ops.Check`'s stale list
   into the `/healthz` handler; not surfaced there." If this step fails, that is the known,
   already-documented reason, not a new bug to chase.

5. Confirm the Slack/webhook side actually posted something, by watching the sink directly — not by
   inferring it from logs:
   ```sh
   docker logs --since 1h animefeedflux | grep -i 'backup alert\|stale\|NotifyOpsAlert\|NotifyBackupAlert'
   ```
   and separately, check the webhook sink itself for an actual received POST.

6. Let the feed generate again (re-enable / restart / wait for its next scheduled fire) and confirm
   the watchdog clears:
   ```sh
   aff stale --db /path/to/animefeedflux.db
   ```
   Expected: back to `no stale feeds`.

**How this silently fails**

- **This is the drill's whole reason for existing, stated in `PLAN.md` §15 directly**: "a generator
  that silently stops is worse than one that crashes." A watchdog that computes the right internal
  state (`aff stale` reports correctly) but whose `Notify` call is misconfigured, throttled, or
  pointed at a dead webhook URL produces exactly this failure mode one level up — the *system*
  believes it alerted, and nobody was told. Steps 3 and 5 must both pass; a drill that only checks
  step 3 (the internal computation) and infers step 5 from "the code calls `Notify`" has not actually
  tested anything a code reader couldn't already see.
- A stale feed that is a **skipped** run under a hard budget cap (`PLAN.md` §13) looks identical from
  the watchdog's point of view to a feed whose scheduler stopped firing entirely — "no `run.finished`
  line for the expected window" either way. `deploy/RUNBOOK.md`'s own stale-feed triage section
  already lists this ambiguity as the top two candidate causes; a drill that doesn't also check `aff
  system stats`/budget state alongside `aff stale` will correctly detect staleness but potentially
  misattribute the cause, which matters for whether the fix is "check the API key" or "raise the
  budget."
- `C4-08`'s open gap (staleness not surfaced on `/healthz`) means an external uptime checker watching
  only `/healthz`'s top-level status, not per-feed detail, would see green through an entire stale
  period if `/healthz`'s liveness check and its staleness surfacing are different things — confirm
  which one `/healthz` actually reports before trusting it as the alerting channel this drill is
  meant to validate.

---

## `aff doctor` — healthy vs. corrupted (no `U2` ticket, but requested and performed)

*`PLAN.md` §15's health check; command at `cmd/aff/doctor_cmd.go`. Not a numbered `U2` drill, but the
same "performed not described" standard applies, and doctor is load-bearing for reading the other
drills' silent-failure sections, so it's recorded here.*

**PERFORMED 2026-08-10.** Against the healthy `.devrun/drill/db/drill.db`:

```
[ok  ] database opens
[ok  ] integrity check
[ok  ] migrations current: 5 applied
[ok  ] feeds running on schedule: 3 feed(s) checked
[ok  ] WAL size: 111272 bytes
[ok  ] provider key present: SCHEMAFLUX_API_KEY is set
[ok  ] disk space: 176615768064 bytes free
exit 0
```

(First run omitted `SCHEMAFLUX_API_KEY` from the shell and correctly reported `[FAIL] provider key
present` with `exit 1` — a good sign doctor actually checks what it claims, not a doctor bug.)

Then copied the same backup file and truncated it to ~50KB with `head -c 50000` to simulate real
corruption (not just an empty/missing file):

```
aff verify .devrun/drill/db/corrupt.db
  -> FAILED — ops: integrity_check on ...: database disk image is malformed (11)
aff doctor --db .devrun/drill/db/corrupt.db
[FAIL] database opens: ops: connecting to ... read-only: database disk image is malformed (11)
[FAIL] integrity check: skipped: database did not open
[FAIL] migrations current: skipped: database did not open
[FAIL] feeds running on schedule: skipped: database did not open
[ok  ] WAL size: no -wal file (freshly checkpointed)
[ok  ] provider key present: SCHEMAFLUX_API_KEY is set
[ok  ] disk space: 176615100416 bytes free
exit 1
```

Doctor correctly distinguishes the two cases and exits non-zero on the corrupted one, with dependent
checks (`integrity check`, `migrations current`, `feeds running on schedule`) correctly reported as
`skipped` rather than silently passing once the open itself failed — a doctor that kept running
independent-looking checks against a DB handle that never actually opened would be the silent-failure
risk here, and it doesn't.

---

## U1 — Recurring operations

These are read-only or low-risk checks meant to run on a cadence once feeds are live, not one-time
drills. Listed here with the same rigor because "read the run history" is itself a task with a wrong
way to do it silently (skimming the dashboard instead of querying the actual reject reasons, e.g.).
None of these can be performed yet — they all assume `U0`'s launch feeds are live and accumulating
real history (`TODOS.md` Phase C is open) — but the commands are checked against `cmd/aff` now so
they're ready the moment there's history to read.

- **`U1-01` Weekly: failures and skipped-for-novelty runs.**
  ```sh
  aff runs --status failed --page-size 50
  aff runs --status skipped --page-size 50
  ```
  Silent-failure risk: `aff runs` paginates (`--page-token`) — reading only the first page on a feed
  with more than a page-size worth of failures in the window under-counts silently. Follow
  `next_page_token` until it's empty before concluding "N failures this week."

- **`U1-02` Weekly: skim published trivia for factual errors.**
  ```sh
  aff item list --origin generated --page-size 50 --json
  ```
  Silent-failure risk: this is inherently a human judgment call with no oracle (`PLAN.md` §20 says so
  plainly — "a nonzero error rate is the honest expectation, not a bug to close"). The actual risk is
  treating "I skimmed it" as equivalent to "I read the answer field," since `item list`'s default
  rendering may truncate `body_html`/`answer_html` — confirm the CLI's full-item view (`aff item get
  <id>`) is what's actually being read, not a summary line.

- **`U1-03` Weekly: confirm every feed built and none is stale.**
  ```sh
  aff stale
  ```
  Silent-failure risk: same as `U2-06` — `aff stale` reports the watchdog's own computed state, not
  whether an *alert* fired. For this weekly check that's fine (it's a manual pull, not relying on the
  push alert), but don't conflate "I ran `aff stale` and it was clean" with "the alerting path is
  proven to work" — that's `U2-06`'s job, not this one's.

- **`U1-04` Monthly: spend against the ceiling and per-feed attribution.**
  ```sh
  aff system stats
  aff runs --page-size 200 --json | jq '[.[] | {feed: .feed_id, cost: .est_cost_usd}] | group_by(.feed) | map({feed: .[0].feed, total: (map(.cost) | add)})'
  ```
  Silent-failure risk: `aff system stats`' spend figures are explicitly labeled estimates
  (`PLAN.md` §8.1 — SchemaFlux's `Generating[T]` reports no real usage/cost today, so this is
  token-count-derived, not provider-billed truth). A monthly review that treats "$X today" as an
  exact figure rather than an estimate is trusting a number the system itself does not claim to be
  exact — cross-check against the provider's own billing dashboard periodically, not just this number.

- **`U1-05` Monthly: nightly backup ran and the off-box copy exists.**
  ```sh
  docker logs --since 30d animefeedflux | grep -i 'backup'
  ```
  and separately, confirm a file actually exists wherever `OffsiteDir` points (or wherever the manual
  off-box shipping step described in `PLAN.md` §15 lands it — see the honest caveat already recorded
  in `TODOS.md` `C4-03`: `OffsiteDir` is currently still a local directory on the same volume, **not**
  transported off the box by anything in this codebase; grep confirms encryption happens, not
  offsite transport). Silent-failure risk: this is the single most likely U1 item to give false
  confidence, because `C4-03` already documents that the "off the box" half of the requirement is not
  actually implemented — a monthly check that only confirms "a `.enc` file exists in `OffsiteDir`"
  will pass every month while the real single-point-of-failure risk (`PLAN.md` §20, the
  fourteen-verified-ArticleFlux-backups-on-one-volume incident this design explicitly cites) remains
  unaddressed. Until an actual transport exists, this check should also confirm the backup is
  reachable from *outside* the droplet's own disk, and should fail loudly (not just "no file found")
  if it isn't.

- **`U1-06` Monthly: remaining recovery-code count.**
  Blocked by the same gap as `U2-03`: there is no `aff` command that reports remaining recovery-code
  count today (it would need a `RegenerateRecoveryCodes`-adjacent read, or a direct query — neither is
  wired into the CLI). Until that exists, this is a manual database check, not a CLI drill:
  ```sh
  sqlite3 /path/to/animefeedflux.db "select count(*) from recovery_codes where used_at is null"
  ```
  Silent-failure risk: this requires stopping the live writer or accepting a `mode=ro` read against a
  WAL database while the service is running — safe as a read, but confirm the query is run with
  `PRAGMA query_only=ON` or against a `mode=ro` connection so there's no chance of it being mistaken
  for a safe write path later.

- **`U1-07` Quarterly: price table against published prices.**
  No CLI surface for the price table exists yet either (`docs/first-run.md` §7 documents the same gap
  for `SystemService.UpdateSettings` generally — `aff system` only wires `stats|kill-switch|backup|
  version`). Manual: compare `internal/pricing` (wherever the table lives in-repo or in `settings`)
  against the provider's current published pricing page by hand.

- **`U1-08` Quarterly: audit grounded links for rot (advisory only).**
  ```sh
  aff item list --origin generated --json | jq -r '.[] | select(.link != null) | .link' | xargs -I{} curl -s -o /dev/null -w '%{http_code} {}\n' {}
  ```
  Silent-failure risk: `PLAN.md` §19 item 5 is explicit that this is advisory, not gating — a drill
  that starts treating 404s here as defects would be re-litigating a decision already made. The real
  risk is the opposite: silently expanding scope by acting on this data as if it were a DOD blocker.

- **`U1-09` Quarterly: novelty gate still catching repeats.**
  No direct CLI query for "recent novelty rejections" beyond `aff runs --status skipped` plus reading
  reject reasons (`aff runs` prints/JSON-includes `reject_reasons_json`, per the `runs` schema in
  `PLAN.md` §10). Silent-failure risk: a novelty gate whose threshold has drifted too *loose* (never
  rejecting) looks identical to "the model just isn't repeating itself" from this data alone — this
  check only catches a gate that's stopped firing at all if there's independent evidence the corpus
  *should* contain near-duplicates (e.g. deliberately re-running a known-similar prompt periodically
  and confirming it's still caught, which is closer to `A5-08`'s seeded-corpus test than a passive
  quarterly read).

- **`U1-10` On any model deprecation notice: re-pin and re-sample every recipe.**
  ```sh
  aff feed list --json | jq -r '.[].slug' | xargs -I{} aff sample {} --size 2
  ```
  after editing each recipe's `model` field via `aff feed update --spec-file ... --expected-version
  <n>`. Silent-failure risk: re-sampling and eyeballing output quality is not the same as confirming
  the *new* model still respects the schema/novelty/link-integrity gates identically — watch
  specifically for a `Invalid`-taxonomy increase (`PLAN.md` §8) in the runs immediately following a
  model swap, not just "did it produce plausible-looking trivia."
