# Devlog

A running narrative of how this project was built and why it looks the way it does.

**This is not the changelog.** `CHANGELOG.md` records *what changed* per version, for someone
deciding whether to upgrade. This file records *what was learned*, including the wrong turns — the
decisions that were made and then reversed, the research that overturned an assumption, and the
things that were nearly shipped wrong. Reversals are the point: a plan that only records its final
state loses the reasoning that makes it defensible six months later, and the same bad idea gets
proposed again by someone who cannot tell it was already considered.

Newest entries at the top.

---

## 2026-08-10 — The password rule that was choosing the weaker password

Cam supplied a fully-specified authentication architecture (NIST SP 800-63B +
OWASP). Most of it matched what was already built. Three parts did not, and one
of those was a genuine defect rather than a preference.

### The composition rule was inverted

`IsWeak` required a password to mix letters with digits or symbols. Applied to
two real candidates:

    "correct battery dinosaur tennis"   -> REJECTED (no digit, no symbol)
    "P@ssw0rd2026!"                     -> ACCEPTED

The rule was actively selecting the weaker password. Not failing to catch the
weak one — *preferring* it. That is precisely the finding behind NIST dropping
composition requirements, and seeing it happen in our own code was more
persuasive than the citation.

It is replaced by length (15–128) plus a compromised-password blocklist, and
there is now a test whose stated purpose is to stop anyone reinstating the old
rule, with the two candidates above as its fixtures.

Related: passwords never expire. Forced rotation fails the same way — a human
asked to change a passphrase on a schedule increments a digit.

### Parallelism 4 was not "more secure"

Argon2id `p=4` splits the same memory budget across four lanes. That is easier
for an attacker with GPUs to parallelise than for us on one droplet core. OWASP
says 1 for this memory profile. Corrected.

### The blocklist is deliberately offline

The obvious implementation is a k-anonymity range query against Have I Been
Pwned. Rejected: it puts a third party on the login path, so their outage
becomes an outage of the only way into this system, and it leaks a hash prefix
of the admin's password on every enrolment. A local list can do neither.

Repetitive and sequential strings are blocked too. That is not the composition
rule returning by the back door, and the distinction is worth keeping straight:
a composition rule dictates what a password must *contain*; this rejects strings
with almost no entropy whatever they contain. NIST lists them as blocklist
material explicitly.

### One place the supplied design was not adopted

Session lifetimes stayed at 12h absolute / 60m idle rather than the suggested
7d/24h. Those figures are right for a consumer PWA; this is a single-admin
console that can rewrite every published feed, so re-authenticating twice a day
is cheap. Recorded in §4 as policy rather than cryptography, so it can be
relaxed without anyone wondering whether something structural depends on it.

### What was already right

Opaque 256-bit session tokens stored only as SHA-256, `__Host-` cookie with no
`Domain`, `Origin` exact-match at the WebSocket upgrade, and no JWT anywhere.
The one genuinely missing mechanism was **periodic session revalidation on a
live socket** — without it, authenticate at 12:01, session expires at 18:00, and
the socket is still serving RPCs at 23:00 because nothing re-checked.

---

## 2026-08-10 — Phase A built in parallel waves; what six-agent fan-out actually costs

Five waves of six Sonnet subagents took the repository from a specification to
most of Phase A: store, three renderers, publish plane, sanitizer, sources,
scheduler, generation pipeline, novelty, budget, auth, OTel. Roughly 15k lines
with tests, all gates green.

The interesting part is not the throughput. It is what went wrong.

### Isolation needs more than "own your files"

I told each agent to touch only its own files. That is necessary and it is not
sufficient, three times over:

- **The package namespace is shared.** Three agents independently wrote a test
  helper called `testChannel` in package `render` and collided. They noticed and
  renamed, but only because the build broke loudly.
- **The build graph is shared.** I ran `go mod tidy` while agents were writing,
  and it stripped `oklog/ulid` because nothing imported it *at that instant*,
  breaking an agent mid-task. Later, an agent following "do not modify go.mod"
  faithfully restored go.mod and deleted go.sum — a locally correct action that
  was globally wrong, because a sibling had legitimately added a dependency.
- **A spec can collide with itself.** I asked one agent for a function called
  `Validate` in two files of the same package. That one was mine.

The rule that actually works: the coordinator owns `go.mod` exclusively, adds
every dependency *before* dispatch, and agents are told never to delete a file
they did not create.

### Cheap fuzzing found more than careful review did

Three fuzz targets found real bugs that reading would not have:

- `urlnorm` idempotence: multi-slash paths needed the trailing-slash strip run to
  a fixed point, and a host that was only a port normalised to a host-less string
  that then failed on a second pass. §9.6's byte-equality check is only sound if
  normalization is stable, so both would have silently rejected *good* links.
- `sanitize` idempotence: attribute entity round-tripping turned `&amp;` into
  `&amp;amp;` on a second pass.

Both classes are invisible to review because each pass looks correct in
isolation.

### An over-strict test is a bug in the test

The sanitizer fuzz initially asserted that "javascript:" never appears anywhere
in the output, and failed on the plain text input "JAVASCRiPt:". Making the
sanitizer satisfy it would have corrupted legitimate prose — an article about XSS
must survive with its text intact. The property was rewritten structurally: every
surviving tag is in the allowlist, the only attribute is href on `a`, every href
scheme is http or https. Same protection, no false positives.

### Reading the dependency beat trusting the plan

§8 assumed SchemaFlux would report token usage and expose embeddings. Reading the
v1.1.0 surface found it does neither, and that a per-call `Client` does not
isolate state the way the plan claimed — `client.Context(ctx)` is required.
Recorded in §8.1 rather than discovered at A5. Cost is now labelled an estimate,
and the novelty gate calls go-openai directly as a documented exception.

### Ticking is part of the work, and I got it wrong

A batch of 37 completed tasks silently stayed unticked because I chained the tick
script behind `&&` after `staticcheck`, which failed on an unused constant and
short-circuited it. Cam caught it. `AGENTS.md` now says: run the verification,
read it, then tick — never chain the two.

---

## 2026-08-09 — Specification built, reviewed three times, and tagged `v0.0.1-dev`

Eleven commits, one day, no code. The repository went from empty to a specification, a build order,
and full scaffolding. What follows is the arc, not a commit list — the log itself is in git.

### The plan came out of research, not memory

The first draft (`d9fb7c5`) was written from what I already knew about RSS. That was wrong, and the
second pass fixed it by actually reading the RSS 2.0 specification, the RSS Advisory Board's Best
Practices Profile, RFC 4287, and JSON Feed 1.1. Three decisions changed as a direct result:

- **`guid`'s `isPermaLink` defaults to `true`.** A silent default. Left implicit, every guid would
  have claimed to be a permalink.
- **RSS uses RFC 822 dates; Atom uses RFC 3339.** Two formatters, and crossing them is a whole class
  of bug. The plan now forbids it by rule and asserts it in a test.
- **The Best Practices Profile prefers hexadecimal character references** over named entities, and
  notes RSS has **no base-URL mechanism** — so relative URLs are unusable and every href must be
  absolutized before storage.

The lesson worth keeping: for anything with a written specification, read it. The cost was two
WebFetch calls; the saving was three bugs that would each have surfaced only in someone else's
reader, weeks later.

### Slack turned out to be stricter than the spec, and quietly

Researching Slack's RSS app was the highest-value hour of the day. Its documented behaviour imposes
four requirements beyond valid RSS: a date tag on every item, items in sequence, **no duplicate
timestamps**, and a feed that passes the W3C validator.

The duplicate-timestamp rule was a live bug in the plan. A grounded news run publishing three items
in one pass would naturally have stamped them identically, and Slack would have kept one and dropped
two — with **no error anywhere**. It does not fail loudly; it just stops posting. That single fact
reshaped the design:

- distinct, strictly-increasing timestamps enforced by `UNIQUE(feed_id, published_at)` — a
  constraint, not a convention;
- a no-backdating rule, because Slack advances a bookmark past the newest item it has seen and a
  backdated item is therefore invisible forever;
- **corrections instead of edits**, because an edit does not change the guid or date and so is never
  re-delivered;
- plain-text `description` with the HTML moved to `content:encoded`, since Slack renders a snippet
  and mangles rich markup;
- OpenGraph tags on permalinks so the unfurl is not a bare URL;
- trivia answers kept out of `description` and `og:description`, or every question is spoiled in the
  channel preview.

A consumer whose failure mode is silence deserves its own test suite. It got one, plus a milestone
(C3) that sits deliberately *before* production deploy.

### Three adversarial review rounds, converging

Ran the plan past an adversarial reviewer three times: **16 findings, then 7, then 3.** Clear
convergence, and the third round confirmed the earlier fixes held. Most of it was real; the two
findings I rejected are noted below.

**Round 1 — the structural one.** The `guid` was derived from `sha256(slug | title | date)` while
the plan simultaneously promised it never changes on edit. True only by convention: any later code
path that re-derived it — a renderer refactor, a repair script — would mint a new guid after a title
edit and resurface the item as a duplicate in *every* subscriber's inbox. Items now carry an opaque
ULID, so the property is true by construction, and idempotency moved to a separate `content_hash`.
Separating identity from deduplication was the right factoring and I had them conflated.

Round 1 also caught that I had multi-feed scaling work gating backups and deploy, which contradicts
my own §14.4 claim that 1–10 feeds need none of it. Deferred to E1.

**Round 2 — the one I would have shipped.** The grounded link-integrity check compared
*asymmetrically normalized* URLs: candidates kept their `utm_*` and `fbclid` parameters while the
output path stripped them. A model faithfully echoing a real article URL would fail byte-equality
and be silently dropped. It fails safe — no hallucination gets through — but it would have starved
the news feed while appearing to work, and the symptom would have looked like the model
misbehaving. Normalization now happens once, at fetch, and both sides use the same function. There
is a test for exactly this.

Round 2 also found that a crash between "items committed" and "run closed" would leave live items
beside a run the watchdog marks `interrupted` — history lying about what happened, which is the
exact failure the plan cites when it forbids editing runs. Items and their run row now commit in one
transaction.

**Round 3 — deleting something.** A `PurgeDeleted` RPC I had specified contradicted three of the
plan's own promises: only runs and embeddings are ever pruned, the guid is never freed, and the
permalink 410s forever. Purging leaves nothing to 410 on. Cut it outright rather than reconciling
it — no definition-of-done item needed it.

**What I rejected:** a suggestion to treat cron jitter as premature scaling work (it is nearly free
and retrofitting it changes scheduler semantics), and a framing quibble about `SameSite=Strict`
versus `Origin` checking that I resolved by rewording rather than redesigning.

### Docker: rejected on evidence, then adopted anyway

Asked whether to deploy with Docker, I inspected the droplet instead of guessing — and the plan was
wrong about the platform. `Earl-Cameron-dot-com` runs **nginx, not Caddy** (the plan said Caddy in
six places), with three Go services and seven sibling timer units under systemd, no Docker
installed, 2 GB RAM, 4 GB swap already configured, Go 1.26.5 on-box.

I recommended against Docker: a second deployment model, a second log destination, and 100–200 MB of
2 GB for a daemon, against a static binary that systemd already sandboxes comparably.

Cam overrode it for the learning value of a real container pipeline. That is a legitimate reason my
analysis did not weigh, so §15 was rewritten for Docker **and records the trade explicitly** rather
than quietly flipping. Three things came out of doing it properly:

- **The build machine is ARM64 Windows; the droplet is amd64.** A local build produces an image that
  builds and pushes fine and then dies at `docker run` with an exec-format error. Building in CI
  removes the trap rather than requiring discipline — and keeps the WASM link, the memory spike, off
  a 2 GB box.
- **Named volumes inherit ownership from the image path; bind mounts do not.** Distroless runs
  non-root, so a root-owned data directory yields a volume SQLite cannot write.
- **Docker writes DNAT rules ahead of the host firewall chain**, so publishing to `0.0.0.0` exposes
  a port *past* `ufw`. Here that would put the admin plane on the internet.

Also inherited a hard-won lesson from `articleflux.service`: `StartLimitIntervalSec` and
`StartLimitBurst` are `[Unit]` keys, not `[Service]` — misplaced, systemd ignores them silently and
the rate limit does not exist. And from a real incident on that box: fourteen verified backups, the
source database, and the decryption key all lived on one volume, so the single event they insured
against took all three. Backups here go off-box, encrypted.

### SchemaFlux, and the line it does not cross

Adopted `github.com/monstercameron/schemaflux` as the LLM layer, which deletes real scope: schema
plumbing, parsing, retries, cost accounting. Reading its README rather than assuming gave three
facts worth having — only OpenAI is live-verified among its seven providers, process-wide state
means we must build an explicit `Client` per call since model varies per recipe, and its cassettes
may replace the planned fake provider.

The important line, written into §8: **typed is not valid.** SchemaFlux guarantees the *shape* of a
value. A struct containing a hallucinated URL is perfectly typed and completely wrong. Every
business rule stays ours.

Also rejected its `Deduplicate` for the novelty gate — it asks the model about pairs, so O(n²)
*model calls* against a 500-item window, versus one embedding and a dot product.

### Core engine first, UI last

Restructured milestones into phases A–E on Cam's direction. The argument that convinced me while
writing it: every RPC the UI calls gets exercised by the CLI first, so the UI is built once against
settled semantics instead of co-evolving with a changing API. And the product is delivering feeds to
Slack long before it has a front end — a UI built earlier would be polishing an admin surface for a
system not yet producing anything worth administering.

### The reference audit

Auditing every `§` cross-reference mechanically found four defects that reading would have missed:
`TODOS.md` cited `§9.1`–`§9.6` when §9 was an unnumbered list (the *second* time a dangling §9.x
reference appeared — now fixed at the source by making the eight generation steps citable anchors);
the load-bearing nginx directives were filed under Risks instead of deployment config; §21's open
questions had no tasks at all and would have been resolved by accident; and `D-FLOW` duplicated the
journey list it should have referenced.

**Ten user flows** (`J1`–`J10`) were promoted from a sketch into canonical §22 definitions with
sanity assertions — deliberately *system-state* invariants rather than unit assertions, because that
is the level this design's real failures live at. Each is automated twice: headless at Phase B as
the regression suite, and as a UI walkthrough at Phase D. The single most important is `BF-11`:
sampling leaves the item count unchanged. A sampler that publishes would look like a feature
working.

### Where it stands

452 → ~520 atomic tasks across five phases, every one citing a plan section. Zero lines of Go.
Tagged `v0.0.1-dev`, which versions the specification and sorts below any future `0.0.1`.

**Open before Phase A can finish:** `OQ-02`, public versus private feeds. Private needs
per-subscriber URL tokens and changes the caching design, so it cannot be decided late.

**Next:** `A0-01`.
