# Open questions for Cam — one-pass decision sheet

Status: **all open**. Nothing below has been decided or ticked by this document. Each entry states
what is actually being asked, what the code does today (verified), what each option costs, and a
recommendation with reasoning — so each can be decided in under a minute. Answering a question here
does not tick it: record the decision in `PLAN.md` §21 (content questions) or in the `OQ-*` line
itself (OQ-06), per `OQ-05`.

---

## OQ-01 — Which three feeds launch first?

**Asked:** confirm or replace the assumed launch set: `anime-trivia-daily`, `anime-fact-daily`,
`anime-news-daily` (grounded).

**Today:** nothing is created yet. `TODOS.md` `U0-05`…`U0-07` (create the three feeds) are unticked;
this is pure content/product scope, not a code gap. The mechanism (feed kinds, schedules, grounded
vs. generative) supports any slug/kind combination already built in Phase A/B — nothing here is
gated on unwritten code.

**Cost of each option:** keeping the three named feeds costs nothing further (already the working
assumption everywhere in `PLAN.md`/`TODOS.md`). Swapping any of the three costs only renaming before
`U0-05`…`U0-07` are run — cheap now, more expensive after real prompt iteration and validator runs
have accumulated against a slug.

**Recommendation:** keep the three as named unless Cam has a specific reason to swap one — they're
already load-bearing across the plan (grounded-source design in OQ-03, cadence in OQ-04, DoD checks
DOD-1–DOD-4 all cite them by name). If undecided, this is the default that ships.

**Unblocks:** `U0-05`…`U0-07` (create the feeds) — the first concrete build step after this doc.

---

## OQ-03 — Grounded sources beyond ANN and Crunchyroll News RSS

**Asked:** is the two-source grounded set (ANN + Crunchyroll News RSS) final, or are others wanted
for `anime-news-daily`?

**Today:** only ANN and Crunchyroll are named anywhere in `PLAN.md`. Grounding is enforced generically
via `internal/generate/contract.go`'s `CheckLink` against whatever candidate set a run fetches — the
enforcement mechanism does not care how many sources feed it, so adding a source is a config/fetcher
addition, not a redesign.

**Cost of each option:** staying at two is zero additional cost — it's what's built and tested today.
Adding a source means: a new fetcher (or RSS feed URL) feeding the candidate set, and that source's
output style now needs the same "byte-equal link or reject" scrutiny that ANN/Crunchyroll get. More
sources also raise the odds of near-duplicate stories across sources feeding the novelty gate (DOD-4
territory), which isn't broken but is more surface to watch.

**Recommendation:** launch with just ANN + Crunchyroll. Two sources is enough to prove the grounded
pipeline in production; add more only after DOD-1–DOD-4 are satisfied and there's a concrete reason
(e.g. a gap ANN/Crunchyroll don't cover). Expanding sources pre-launch adds risk to an already-large
first deploy for no launch-blocking benefit.

**Unblocks:** `U0-07` (create `anime-news-daily`).

---

## OQ-04 — News cadence: one digest item per day, or N separate items?

**Asked:** does `anime-news-daily` post one digest item covering multiple stories, or does each story
become its own item (currently assumed: 3 separate items/day)?

**Today:** nothing is built either way — this is a prompt/recipe shape decision, not a mechanism
gap. Both shapes are expressible in the existing item model; nothing in `internal/generate` or the
publish plane assumes one form over the other.

**Cost of each option:**
- **3 separate items/day (current assumption):** reads better in Slack — each story is its own
  message with its own unfurl, matching how Slack users actually scan a channel. Costs more per-run
  budget/token spend (three generations instead of one) and triples the item volume the feed-window
  cap (§5.4) and novelty gate (§9.5) have to manage for this feed specifically.
- **One digest/day:** cheaper (one generation call) and quieter (one message/day in Slack instead of
  three), but a digest reads worse in Slack specifically — a wall of stories in one message loses the
  per-story unfurl and skimmability that separate items get.

**Recommendation:** keep the assumption (3 separate items). The cost difference is modest (3x one
feed's generation calls, not the whole system's budget) and Slack readability was the stated design
goal for this feed. If spend becomes a real constraint post-launch, this is cheap to revisit — it's a
recipe/cadence setting, not structural.

**Unblocks:** `A6-13`.

---

## OQ-05 — Bookkeeping: record each answer in PLAN.md §21

**Asked:** not a design question — a housekeeping instruction to write each of OQ-01/03/04/06's
answer back into `PLAN.md` §21 once decided (date + reason), the way OQ-02 already was on
2026-08-10.

**Today:** §21 has one decided entry (item 2, "Are the published feeds public?") with a dated,
reasoned writeup; items 1, 3, 4 are still open questions in prose form, matching OQ-01/03/04 above.

**Cost:** none — it's a documentation step, not a decision, and happens automatically once Cam
answers 01/03/04/06.

**Recommendation:** no action needed from Cam directly; this ticks itself once the others are
answered and PLAN.md §21 is updated to match (out of this document's edit scope — PLAN.md is
off-limits here per this task's constraints).

**Unblocks:** nothing new — it's the record-keeping step, not a blocker.

---

## OQ-06 — Does a recovery-code elevated session grant one privileged action or two?

**Asked:** when an admin uses a recovery code to get an elevated session, should that one session be
able to perform both a password reset **and** a TOTP re-enrollment, or only one of the two (today's
behavior)?

**Today, verified:** it buys exactly one action. `internal/rpc/auth.go`:
- `ChangePassword` (line 540) calls `s.endElevatedSession(ctx, cs)` at line 574 on success.
- `ReenrollTOTP` (line 681) calls the same `s.endElevatedSession(ctx, cs)` at line 716 on success.

Both end the elevated session the moment either succeeds — a recovery code cannot chain "reset
password" then "re-enroll TOTP" in one elevated window. The UI already models this honestly:
`web/pages/auth/recover.go` presents an explicit choose-one screen (`ChoosePasswordReset` /
`ChooseReenrollTOTP`, lines 178–192), not a flow that implies both are available.

**Cost of each option:**
- **Keep one action per code (status quo):** safer default — a recovery code is a bearer credential,
  and the shorter the elevated window, the less an attacker who obtained one code can do with it.
  Costs the user a second code at the realistic lockout scenario ("new phone, lost authenticator"),
  where both a password reset and a TOTP re-enrollment are plausibly wanted at once, out of a finite
  set of codes (`RegenerateRecoveryCodes` exists but itself needs an elevated session to call).
- **Let one recovery session cover both actions:** matches the realistic lockout better, at the cost
  of a longer-lived elevated window per code use — the exact thing a bearer credential in the wrong
  hands would want. This is a real code change (`endElevatedSession` would need to move from
  "immediately after either action" to "immediately after both, or after an explicit end/timeout"),
  not a documentation fix.

**Recommendation:** keep the status quo (one action per code). The security argument is the stronger
one — recovery codes are exactly the credential class where the safe default should not bend to
convenience — and the realistic mitigation for "lost phone + want both" is regenerating and using a
second code, which the system already supports end-to-end. If Cam disagrees because the finite-code
cost feels wrong in practice, the alternative is a scoped one: extend the elevated window only long
enough to chain a second specific action, not indefinitely.

**Unblocks:** nothing currently shipped is blocked (the UI already models "choose one" honestly), but
this should be settled before `U2-03`'s recovery drill is treated as final, so the drill exercises
the behavior Cam actually wants, not just whatever shipped first.

---

## Trivia spoiler hiding — not chosen (see `docs/spoiler-design.md`)

**Asked:** four options exist for how a trivia item's answer should be kept hidden from a reader who
chooses not to see it yet, now that the original plan (an HTML `<details>`/`<summary>` disclosure
widget) is confirmed not to survive ArticleFlux's sanitizing reader — the sanitizer unwraps
`details`/`summary` (not in its allowlist) and strips `style` unconditionally, so the tag is gone but
the answer text remains, inline, unhidden, with no toggle.

**Options (full detail and per-consumer verification in `docs/spoiler-design.md`):**

| # | Option | Cost | Verified result |
|---|---|---|---|
| 1 | Whitespace/scroll distance before the answer | Cheapest to build | Weakest — defeated by any reader that renders the full item in one block (confirmed for ArticleFlux's `articleBody`) |
| 2 | Answer as a separate, later item, linked to the question | Highest — real item-model change: doubles item volume, interacts with §5.4 window cap, §9.5 novelty dedup, and the Slack strictly-increasing-`pubDate` rule | Works everywhere (Slack, ArticleFlux, generic readers) |
| 3 | Answer only behind the permalink; feed carries question + link | Medium — no schema change, but a UX regression for in-app full-content readers (works against ArticleFlux's own "keep the reader inside the app" design goal) | Strong hiding everywhere, at the cost of forcing a click-through |
| 4 | Accept visibility; write the item to read acceptably with both question and answer shown at once | Lowest — wording only | Concedes the actual product goal ("hidden until chosen") for every full-content reader, by design rather than by defect |

**Recommendation:** option 3 (permalink-only answer) is the best fit for this project specifically —
Slack's `description` is already question-only by existing §5.5 design, so option 3 changes nothing
for the primary named consumer (Slack) and only asks ArticleFlux readers to click through, which is a
real but bounded UX cost. Option 2 is the "actually works everywhere natively" answer but is
disproportionate engineering (doubles item volume, touches dedup and the pubDate-uniqueness rule) for
a single feed's trivia mechanic. Option 1 should be ruled out — it's confirmed-defeated by the one
full-content reader this project already names as a target consumer. Option 4 is the fallback if Cam
decides the spoiler mechanic isn't worth any structural cost at all.

**Unblocks:** finalizing `anime-trivia-daily`'s item template/prompt design — currently blocked on
not knowing which shape the answer's placement takes, which affects both the generation prompt and
(for option 2) the item/schedule model.

---

## DOD-5 / DOD-7 — wording decisions where the checklist criterion references something that doesn't exist

Full detail in `docs/definition-of-done.md`; summarized here because they're the same species of
decision as the OQ-* list (a decision for Cam, not a code task) even though they aren't numbered
`OQ-*`.

### DOD-5 — "zero invented URLs," checked by "an audit over the full item table"

**Asked:** §19.5's check method says an audit should be run "over the full item table" comparing
each grounded item's link against its generation-time candidate set. Verified: **no such audit is
possible under the current schema** — `migrations/0002_feeds_items.sql` has no column that persists
a per-item candidate-set snapshot, so there is nothing to retroactively query. What exists instead is
synchronous *prevention*: `internal/generate/contract.go`'s `CheckLink` rejects any non-candidate URL
before an item is ever committed (proven by `TestCheckLink_RejectsURLAbsentFromCandidateSet` and
`TestRun_Grounded_LinkNotInCandidates_DroppedAndCounted`), so a bad link never reaches the table to
be audited in the first place.

**Options:**
- **(a) Amend §19.5's wording** to state the check as generation-time enforcement (provable by the
  contract test suite plus "no item ever exists whose link is absent from that run's candidate set"
  as a standing code-path invariant), dropping "audit over the full item table." Zero new code.
- **(b) Add a schema column** that persists the candidate-set snapshot per grounded item, enabling a
  literal retrospective audit. Real feature — a write on every grounded item — for a guarantee the
  synchronous check already provides for free.

**Recommendation:** (a). The bar (zero invented URLs) is right and unchanged; only the check
*method* named in the wording is unachievable as written. Storing a full candidate-URL list per item
just to audit something already enforced synchronously is schema weight without a new guarantee.

**Unblocks:** `DOD-5` can be marked satisfied (pending the wording amendment) without any further
build work — the enforcement it actually needs already exists and is tested.

### DOD-7 — "monthly spend under the ceiling," but no monthly ceiling exists

**Asked:** §19.7 says total monthly spend must stay under "the configured ceiling." Verified: **no
monthly ceiling exists anywhere in the system.** `internal/budget/budget.go`'s `Limits` struct has
only daily fields (`PerFeedDailyTokens`, `PerFeedDailyRuns`, `GlobalDailyTokens`, `GlobalDailyUSD`),
and `PLAN.md` §13 itself only ever specifies a **daily** global spend ceiling. Per-feed cost
attribution in `runs` (`migrations/0002_feeds_items.sql:42-62`, `tokens_in`/`tokens_out`/
`est_cost_usd`) is solid and already readable via `store.SpendSince`; there is simply no second,
monthly-scoped limit to be "under."

**Options:**
- **Amend §19.7's wording** to state what's real: daily spend stays under the configured daily
  ceilings (already enforced), reviewed monthly by summing `runs.est_cost_usd` per feed (`U1-04`,
  an operational habit, not an enforced limit). Zero new code.
- **Build an actual monthly ceiling** (new `Limits` field, new enforcement path) if a real
  second-order monthly cap is wanted independent of the daily ones. New scope, not a documentation
  fix.

**Recommendation:** amend the wording. A daily ceiling that's actually enforced plus a monthly
human review of the same numbers covers the real risk (runaway spend) without adding a second
enforcement mechanism that has no design anywhere else in `PLAN.md`.

**Unblocks:** `DOD-7` can be marked satisfied (pending the wording amendment and one calendar month
of production data to review) without new budget-engine work.

---

## Summary — one line each

- **OQ-01** (launch feeds): keep the three named — zero cost, already load-bearing across the plan.
- **OQ-03** (grounded sources): launch with ANN + Crunchyroll only; add more post-launch if a gap
  shows up.
- **OQ-04** (news cadence): keep 3 separate items/day — Slack readability was the stated goal, cost
  is modest.
- **OQ-05** (bookkeeping): no decision needed — self-resolves once 01/03/04/06 are answered and
  §21/OQ-06 are updated.
- **OQ-06** (recovery code scope): keep one action per code — the safer default for a bearer
  credential; use a second regenerated code for the "need both" lockout case.
- **Trivia spoiler**: option 3 (permalink-only answer) — closest to zero cost for Slack (already
  question-only), bounded UX cost for ArticleFlux, rules out the confirmed-defeated option 1.
- **DOD-5 wording**: amend to "generation-time enforcement," don't build a retrospective-audit
  schema column.
- **DOD-7 wording**: amend to "daily ceiling enforced, monthly reviewed," don't build a second
  monthly limit.
