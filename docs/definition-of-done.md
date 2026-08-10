# Definition of done — status audit (v1, PLAN.md §19)

Audited against the tree on 2026-08-10. Nothing here is ticked from memory — every line below cites
the file, test, or TODOS.md task it rests on. Re-run the audit whenever a `C5-*` or `U2-*` task
changes state; this document goes stale as fast as `TODOS.md` does.

## The headline finding

**Seven of the nine items are gated on things that have never happened, and six of those seven share
one root cause: nothing has ever been deployed.** `CHANGELOG.md` says it plainly — "none of it is
running anywhere... no staging host, no production deploy" — and `TODOS.md`'s `C5-*` block (DNS,
vhosts, IP allowlist, first deploy, rollback drill) is entirely unticked. A single production
deployment (`C5-01`…`C5-07`) makes `DOD-1`, `DOD-6`, and `DOD-9` immediately checkable, and starts the
clock on `DOD-3` (7 days), `DOD-4` (30 days), and `DOD-7` (one billing month). That ordering — one
event, six items unblocked or started — is the actual structure of what's left, and the checklist
below is ordered to put it up front. Do not read the per-item detail as nine independent problems;
read it as one infrastructure event plus two cheap local drills plus one wording decision.

The two items that need **no deployment at all** are cheaper than the infrastructure work and should
be done first, today, regardless of when a deploy happens.

## Work it down in this order

### 1. `DOD-8` — Backup restore drill (no deployment needed)

**Check (§19.8):** "A backup has been restored into a scratch instance and serves identical feeds."

**Status: satisfiable now but unverified.** `internal/ops/backup.go` implements `Backup`, `Verify`,
and `Restore` (VACUUM INTO–based), and `internal/ops/backup_test.go` proves the mechanics work —
`TestBackupOfLiveWALDatabaseRestoresByteIdenticalLogicalState`, `TestRestoreRefusesAnUnverifiedBackup`,
`TestBackupRetentionPrunesOldestFirst`. What has never happened is the *drill*: standing up a second
serving instance against the restored file and diffing its rendered feed output against the
original. `TODOS.md` `U2-01` ("Restore drill: restore a backup into a scratch instance; confirm
identical feeds. §19") is unticked. This is a same-machine exercise — create an instance, seed it,
back it up, restore into a scratch directory, serve both, diff — and needs no droplet, no DNS, no
Slack.

**Unblocked by:** running the drill once, locally.

### 2. `DOD-2` — Feed validation (mostly no deployment needed)

**Check (§19.2):** "All three validate clean, in all three formats, with zero validator warnings."

**Status: blocked**, but on the cheapest possible thing — the three feeds do not exist yet, not on
hosting. `internal/feedvalidate/validate.go` and its ~20 `Test*` functions (RSS/Atom/JSON Feed) are
complete and passing, and `make validate` (`Makefile:85-93`) runs the real `affvalidate` binary
against whatever rendered documents are handed to it — it operates on files, not URLs, so it does not
require the feeds to be publicly served. `TODOS.md` `U0-05`/`U0-06`/`U0-07` (create
`anime-trivia-daily`, `anime-fact-daily`, `anime-news-daily`) are unticked. Creating the three feeds
and generating real content against them is a local action (`aff create`, `aff sample`/`aff run`);
validating the output does not need `DOD-1`'s "live" to be true yet.

**Unblocked by:** creating and generating the three feeds locally (`U0-05`–`U0-07`), then
`make validate` against the real output. Doing this before deployment also front-loads prompt
iteration that `DOD-1` needs anyway.

### 3. `DOD-5` — Zero invented URLs (needs a wording decision, not infrastructure)

**Check (§19.5):** "an audit over the full item table shows every grounded item's link matched the
fetched candidate set **at generation time**."

**Status: flagged — the check as literally written cannot be performed, ever, under the current
schema, and this needs Cam's decision before it can be marked done at all.** The system does not
persist a candidate-set snapshot per item anywhere (`migrations/0002_feeds_items.sql` has no such
column, confirmed by search across `internal/store`). What exists instead is *prevention*, not
*audit*: `internal/generate/contract.go`'s `CheckLink` runs before an item is ever committed and
drops/counts anything not byte-equal to that run's candidate set
(`ReasonLinkNotCandidate`/`link_not_in_candidate_set`), proven by
`TestCheckLink_RejectsURLAbsentFromCandidateSet` and
`TestRun_Grounded_LinkNotInCandidates_DroppedAndCounted`. A bad link structurally never reaches the
`items` table, so there is nothing later to retroactively audit "against the fetched candidate set at
generation time" — that data was never kept.

This is not the same failure mode as the other eight items (infrastructure that hasn't happened). The
*bar* — zero invented URLs — is right and should not move. The *check method* named in §19.5 is
unachievable as written. Two honest ways to close the gap, neither implemented here per this
document's edit scope:

- **(a) Amend §19.5** to state the check as generation-time enforcement, proven by the contract test
  suite plus a standing invariant ("no item ever exists whose link is absent from that run's
  candidate set" — provable by code path, not by a later query), and drop "audit over the full item
  table" from the wording.
- **(b) Add a schema column** persisting the candidate-set snapshot per grounded item, if a literal
  retrospective audit is genuinely wanted over prevention. This is a real feature, not free, and adds
  a write on every grounded item for a guarantee the synchronous check already gives for free.

Recommendation: (a). Storing a full candidate URL list per item to audit a guarantee that is already
enforced synchronously is schema weight without a corresponding new guarantee.

**Unblocked by:** Cam deciding (a) vs (b) and PLAN.md being amended accordingly — not by deployment.
Once decided, exercising the enforcement against a real live grounded generation run (ANN +
Crunchyroll, real model) rather than only the synthetic candidates in `contract_test.go` is itself a
local action, no deployment required.

### 4. The one deployment event — unblocks `DOD-1`, `DOD-6`, `DOD-9` outright and starts the clock on `DOD-3`, `DOD-4`, `DOD-7`

Everything below this point needs `TODOS.md`'s `C5-*` block done: DNS (`C5-01`), nginx vhosts + TLS
(`C5-02`), the real home IP in the admin allowlist (`C5-03`, currently the literal placeholder
`allow 203.0.113.0/24;` in `deploy/nginx/admin.anime.earlcameron.com.conf:33`), confirmed
unreachability off-allowlist (`C5-04`), pinned `sha-` tag compose (`C5-05`), real secrets
(`C5-06`), and the first deploy itself (`C5-07`). None of `C5-01`…`C5-10` is ticked.

#### `DOD-1` — Three feeds live

**Check (§19.1):** the three named feeds are live.

**Status: blocked.** Same root cause as the rest of this section: `C5-07` ("First production deploy;
feeds live and validating") is unticked, and `CHANGELOG.md` confirms no host exists to be live on.
**Unblocked by:** the deployment, with the three feeds already created and iterated locally per item
2 above so they can go live with real, sampled prompts rather than untested ones.

#### `DOD-6` — Admin reachable only from the allowlisted IP, password + TOTP, drill passed

**Check (§19.6):** allowlisted-IP-only reachability, password + TOTP, one successful recovery-code
drill.

**Status: blocked, in two independent halves.** The auth half is solid and already drilled at the
unit level: `internal/rpc/auth.go` implements password-then-TOTP login, recovery-code consumption
that ends the elevated session on first use (`endElevatedSession`), and 15 dedicated adversarial test
files in `internal/sectest/` (`sec39_timing_test.go` through `sec50_no_secret_in_logs_test.go`,
covering TOTP replay, recovery-code replay, forged cookies, revoked sessions, idle timeout).
`TODOS.md` `U2-03` (recovery drill) is unticked as a *literal, performed* drill, but nothing about it
requires a deployment — it can be run locally today against `aff admin`, same as `DOD-8`.

The IP-allowlist half cannot be satisfied without a deployment: it is enforced at the nginx layer
only, and the config in the repo is a placeholder —
`deploy/nginx/admin.anime.earlcameron.com.conf:33`: `allow 203.0.113.0/24;   # PLACEHOLDER — set to
the home IP before enabling`. `C5-03` ("IP allowlist on the admin host (home IP)") and `C5-04`
("Confirm the admin host is unreachable from off-allowlist") are both unticked.

**Unblocked by:** the deployment, with the real home IP substituted for the placeholder, plus running
`U2-03` (which can and should happen before deployment too, as a local dry run).

#### `DOD-9` — Push to `main` reaches production; a rollback has been performed

**Check (§19.9):** a push to `main` deploys with no manual step, and a rollback to the previous image
tag has been performed successfully at least once.

**Status: blocked.** `.github/workflows/release.yml` fully implements this — build-and-push on
`push: [main]`, and a `workflow_dispatch` input (`image_tag`) that redeploys an existing tag, which is
the rollback path — but its own header comment says "Until the droplet secrets exist, the deploy job
reports skipped rather than failing," and `C5-08` (perform an actual rollback) and `C5-09` (confirm a
push reaches the running service with no manual step) are both unticked. The workflow is real code
waiting on a real target, not missing code.

**Unblocked by:** the deployment (which supplies the droplet secrets the workflow currently has
none of), then one push and one `workflow_dispatch` rollback — both exercisable the same day as the
deploy.

#### `DOD-3` — Slack: 7 days, every item posts exactly once, no dupes, no misses, no spoilers

**Check (§19.3):** over a 7-day window with Slack subscribed to all three feeds, every generated item
posts exactly once — no duplicates, no misses, no visible trivia answers.

**Status: blocked.** Every static/structural Slack-compliance test is done and ticked: `A3-03`
(strictly descending unique `pubDate`), `A3-04` (every item has a parseable date), `A3-05`
(`description` plain text, under the cap), `A3-06` (no answer text in `description`/`og:description`),
`A3-07` (OG tags present). What's missing is the live 7-day soak: `TODOS.md`'s `C3` block
("Slack proof") is entirely unticked, and `U0-09` says outright: "Intended procedure written as
UNVERIFIED... needs a real, publicly reachable host and a Slack workspace, neither of which exist."

**Unblocked by:** the deployment, then pointing a Slack workspace at the live feed and waiting 7
consecutive days.

#### `DOD-4` — 30 consecutive days of production trivia, no near-duplicate pairs

**Check (§19.4):** thirty consecutive days of *production* trivia contain no near-duplicate pairs
above the novelty threshold — explicitly not provable by the A5 canned-corpus harness, because that
only proves the mechanism works, not that a live model won't repeat itself.

**Status: blocked.** The novelty gate itself is built and tested against synthetic/canned data (A5);
no production trivia has ever been generated, so there is no 30-day window to inspect.

**Unblocked by:** the deployment, then 30 consecutive days of real scheduled `anime-trivia-daily`
runs.

#### `DOD-7` — Monthly spend under the ceiling, per-feed attribution in `runs`

**Check (§19.7):** total monthly spend stays under "the configured ceiling," with per-feed
attribution recorded in `runs`.

**Status: blocked, and the wording deserves a second look alongside the infrastructure gap.** The
attribution half is solid: `runs` (`migrations/0002_feeds_items.sql:42-62`) carries `feed_id`,
`tokens_in`, `tokens_out`, `est_cost_usd` per row, and `cmd/animefeedflux/wire.go`'s budget gate reads
it back per-feed and globally via `store.SpendSince`. But **there is no monthly ceiling anywhere in
the system to be "under."** `internal/budget/budget.go`'s `Limits` struct has only
`PerFeedDailyTokens`, `PerFeedDailyRuns`, `GlobalDailyTokens`, `GlobalDailyUSD` — all daily — and
PLAN.md §13 itself only ever specifies "a global **daily** spend ceiling" (§13, "on top of per-feed
caps, because the failure mode is N feeds each..."). `TODOS.md` `U1-04` ("Monthly: review spend
against the ceiling and per-feed attribution") is an *operational habit* — summing the daily figures
by hand once a month — not a second enforced limit.

§19.7's "the configured ceiling" reads as if a monthly number is configured somewhere; none is. This
is the same category of issue as `DOD-5`: not a wrong bar, but a check-method mismatch with what the
system actually enforces. Recommend amending §19.7 to say what's real: *daily* spend stays under the
configured daily ceilings (already enforced, §13), reviewed monthly by summing `runs.est_cost_usd`
per feed (`U1-04`) — i.e., the enforcement is daily, the review cadence is monthly, and there is no
separate monthly limit to build.

**Unblocked by:** the deployment, then one calendar month of real production runs to review — plus,
independently, Cam confirming the §19.7 wording fix above (or asking for an actual monthly ceiling to
be built, which is new scope, not a documentation fix).

## Tally

- **Satisfied: 0.**
- **Satisfiable now but unverified: 2** — `DOD-8` (restore drill, purely local), `DOD-2` (create the
  three feeds locally and validate real output; does not strictly require "live").
- **Blocked: 7** — `DOD-1`, `DOD-3`, `DOD-4`, `DOD-6`, `DOD-9` on the single production deployment
  (`C5-01`…`C5-09`) plus, for `DOD-3`/`DOD-4`, elapsed time afterward (7 and 30 days respectively);
  `DOD-5` and `DOD-7` additionally need a wording decision on the check method itself, independent of
  infrastructure.

**Single cheapest action that unblocks the most:** the first production deployment (`C5-01`…`C5-07`).
It is the one event standing between "blocked" and "in progress" for six of the seven blocked items.
Everything else — the two local drills and the two wording decisions — can and should happen before
or alongside it, since none of them wait on it.
