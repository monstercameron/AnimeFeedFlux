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

## 2026-08-11 — The container was fine; the harness had never run

Docker on this project was in the state that reads as done and isn't: a multi-stage build into
distroless, a compose file with real hardening, an nginx vhost, deploy scripts, two CI workflows,
and `scripts/check-container.sh` written specifically to verify C0-08/C0-09/C0-19. Every box ticked
except the three that needed a running daemon. Nothing had ever been built.

Starting the daemon and running the script found four things, and the shape of them is the lesson:
**none were defects in the container, and all four would have surfaced first on the droplet.**

**The one that cost the most time looked exactly like the trap the code warns about.** The container
came up and died with SQLite `unable to open database file (14)`. The Dockerfile has a paragraph
about named-volume ownership — a directory created as root yields a volume the nonroot process
cannot write, failing at first write, long after healthy — and this is precisely what that failure
looks like. It was not that. MSYS rewrites anything path-shaped in a command-line argument before
the program sees it, so `-v vol:/var/lib/animefeedflux` reached docker as
`-v vol:C:/Program Files/Git/var/lib/animefeedflux`, and the container booted with nothing mounted
where `AFF_DB_PATH` points.

What separated the two was having a second instrument. `scripts/check-compose.sh` — written an hour
earlier for an unrelated reason — passed against the same image at the same moment. Compose reads
its paths from YAML, which MSYS never touches. One tool failing and another succeeding against the
identical artifact localised the fault to the argument path in about a minute; without it the next
hour would have gone into volume ownership, which was never wrong.

**The same run proved the script had never executed.** It omitted `AFF_PUBLIC_BASE_URL` and
`AFF_ALLOWED_ORIGINS`, both required by `config.Load`. A script that cannot have worked had been
sitting in the tree as the thing that verifies the container.

**C0-09 was reporting a pass it had not earned.** It keyed on the exit code of
`docker run <arm64-image> healthcheck`, and `healthcheck` is a CLIENT — in a one-shot container with
no server it exits nonzero with a dial error whether or not the binary could execute at all. So a
perfectly emulated arm64 run was being read as the architecture mismatch reproducing. The honest
finding is the opposite: Docker Desktop emulates arm64 via QEMU and the mismatch does NOT reproduce
here. It classifies on the loader's output now, and says plainly that the signature to expect on the
droplet is `exec format error` at run, not at build. A check that passes for the wrong reason is
worse than no check, because it retires the question.

**Two real defects, both invisible without running it.** `.dockerignore`'s `*.db` never matched
`.devrun/aff.db`: Docker's patterns are `filepath.Match`-style, so `*` does not cross a `/` and a
pattern is not applied per-directory the way `.gitignore` applies one. `AFF_SECRET_KEY`, the dev
credentials and a live database were going into the build context. And the image seeded its data
directory by copying the build stage's `/tmp` — a neat trick for getting an owned empty directory
into an image with no shell, except `/tmp` is where `apk`, `go build` and the web build all work, so
every fresh named volume started life containing whatever they happened to leave.

**The design decision worth keeping.** The obvious way to test compose is a second compose file with
test-shaped values. That was rejected: the hardening lines — `read_only`, `cap_drop: ALL`,
`no-new-privileges`, the tmpfs, the limits — are exactly what needs testing, and a copy diverges
from production the first time either file changes, quietly turning the test green against a
configuration nobody runs. `deploy/compose.test.yaml` is an overlay that changes four things
(build-not-image, container name, volume name, base URL) and inherits every hardening line from the
file that actually ships. Making that possible cost one variable in production
(`${AFF_ENV_FILE:-/etc/...}`) and nothing else. `check-compose.sh` then asserts the security options
back out of `docker inspect` rather than trusting the merge, because a merge that silently dropped
`read_only` would leave every other check passing.

The remaining gap is unchanged and worth stating: the image builds and boots on a laptop. It has
never run on the droplet, Docker is not installed there, no deploy has happened, and no rollback has
ever been exercised. C0 is closed. C2 and C5 are not, and closing them is not a code problem.

---

## 2026-08-11 — Three bugs stacked behind one another, under "reroute on expiry"

Cam asked for a redirect to /login when the session expires, because the app stops being useful.
That is a five-line change on top of working machinery. The machinery was not working, and finding
out why took peeling three layers, each of which looked like the answer.

**Layer one: the redirect did not exist.** The route guard runs on `BeforeEnter` — during navigation
evaluation, and only there. That is right for "may this person open this page" and blind to the case
that actually happens: the session dying under a page already open. Nothing re-ran the guard when
the state changed, and nothing anywhere navigated on expiry. Even the expiry modal's "Sign in"
button did not go to the login screen; `AcknowledgeExpiry` clears the hold and applies the
transition, leaving the admin on the same dead page with the modal gone.

Fixed by re-running `guard.Decide` — the same pure function, against the same route table entry —
when the session state changes rather than only when the path does. Reusing the decision instead of
writing a second one is what makes the negative cases free: DISCONNECTED is authed-ish, so a dropped
socket still shows the reconnect banner and stays put, and ELEVATED still goes to /recover.

**Layer two: nothing ever emitted the event.** `EvSessionExpired` has a transition, a hold, a modal,
and catalogue copy in two languages. `web/wsconn` emitted exactly two events, both derived from
grpc-go connectivity state. A session dying server-side is invisible to connectivity — the socket
stays perfectly healthy and the RPCs over it start returning `Unauthenticated`. So the entire expiry
apparatus was complete, tested at its edges, and unreachable. It is worth sitting with that: four
files of correct code, and the one line that would have triggered any of it was never written.

The detection had to distinguish a dead session from a wrong password, since both are
`Unauthenticated` and treating them alike would sign an admin out for a typo in Change Password.
That is possible only because of an unrelated decision made for a different reason: §12.1 forbids a
login oracle, so every credential failure returns one generic message while session failures name
themselves. A privacy rule three months old is what makes this safe today.

**Layer three, and the one I would have sworn was impossible: the state still did not change.**
Detection fired, the event was emitted, `applyEvent` ran — markers proved all of it — and the
watcher kept observing AUTH. The cause is already documented in this repository, in
`web/ui/pump.go`: GWC v5.0.1 queues state updates made off-loop and defers the drain while a render
is in flight, re-booking only if one is not already scheduled. My emit ran on the RPC's goroutine.
Same defect that made Save look hung, found the same way, one file over. Emitting through
`time.AfterFunc(0, …)` puts the write on the JS timer queue where it applies immediately.

And then the navigation itself needed the same treatment for a different reason:
`Router.NavigateReplace` opens a guard attempt and returns **silently** if one is already in flight,
which it is when you call it from inside the render the router just triggered. Detected, noticed,
notice rendered, URL unmoved. Deferred to the next tick, it works.

**What the browser found that the tests could not.** Every unit test I wrote for this passed on the
first run — the decision table, the error classification, the state machine. All of them were right.
Not one of them could see any of the three bugs, because all three live in the seam between correct
components: who calls whom, on which goroutine, in which phase of the framework's cycle. This is the
third time on this project that the answer came from driving a real browser (see `tokens.Emit` never
being called, and `h.Value` on a `<select>`).

**The thing left undone, because it is Cam's call.** `/generate` reports genuine draft dirtiness.
`/history` and `/settings` both report `wired` — which is not "page has unsaved work" and is not even
"page is mounted", but "Init was called", true forever after boot. So the unsaved-work hold fires on
*every* expiry on those pages, and the automatic redirect never runs there: you get a modal claiming
your unsaved changes are being kept when there are none. Both files admit the substitute in their own
comments. Making them accurate means either accepting that a half-typed settings field is lost on
expiry, or tracking dirtiness per panel — a data-loss trade, not a cleanup, so it is written down
rather than decided here.

---

## 2026-08-11 — The browser half of this app is at 26% coverage, and nothing could see it

Cam asked whether the >80% figure held per file. It does not, and answering properly turned up
something worse than the 51 host files sitting under 80%: **62 files under `web/` were not being
measured at all**, and their real coverage is 25.7%.

The mechanism is quiet enough to be worth stating precisely. Those files carry
`//go:build js && wasm`. A host build does not compile them, so they are not "0% covered" in any
profile — they are absent from the package entirely, contributing neither statements nor misses. The
number that results looks fine and is measuring the leftovers: `web/pages/settings` reports 81.7% on
the host from its handful of untagged helpers (`screenstate.go`, `format.go`, `validate.go`), while
every render function, every panel and every control in it sits at 15.5%. `web/shell` reports 100%
and is actually at 5.6%. `web/wsconn` reports 100% and is actually at 4.1%.

`scripts/coverage-wasm.sh` now measures it, which took two Windows-specific workarounds worth
recording because both look like dead ends at first. `GOROOT/lib/wasm/go_js_wasm_exec` is a bash
script and `go test -exec` needs something Windows can execute, so the script generates a `.cmd`
shim. And the wasm process cannot generate its own coverage report: Go's js syscall layer has no
`O_DIRECTORY`, so the in-process step that reads back the counter directory fails — *after* having
written every counter file correctly. Asking for raw counters with `-test.gocoverdir` and converting
them on the host with `go tool covdata textfmt` sidesteps the read entirely. So `go test` reports
FAIL on that step while the run itself succeeded, and the script distinguishes the two.

**Running the existing tests under wasm found four failures that the host build hides**, which is
the more interesting half. Three are environment mismatches with honest answers (a source-tree walk
cannot run where directory reads are unsupported; two tests assert on the string `css.Harvest()`
returns, and in a browser the rules go straight into the document stylesheet so there is nothing to
harvest). The fourth is real: `web/ui`'s `TestInputWithoutIDStillWiresLabelToField` calls
`Input(...)` directly, outside any component, so it reaches `gwcui.UseId()` with no fiber. The native
build tolerates that. The browser build — the runtime this code actually ships to — panics with
`GoUseId called outside component context`. The test was passing only because the host renderer is
more forgiving than production. It now renders through `ui.CreateElement` and passes in both builds.

**Why the 62 files cannot simply be brought over 80%.** I spiked it: render one panel under wasm via
`gwcui.RenderToString`. It panics with `GoUseFunc dom adapter is nil` — any component wiring an event
handler needs a DOM adapter, and GWC installs one only from `ensureInitialized()`, which builds a
`jsdom.NewWASMDOMAdapter()` against a real `document`. Node has none, GWC exposes no seam to install a
test adapter (the config lives behind `internal/runtime`), and jsdom is not present in this
environment. So unit-testing the render layer needs a jsdom-class harness that boots GWC's real
runtime — a project in its own right, before a single assertion gets written. That is a decision for
Cam, not something to start unilaterally at the end of a coverage pass.

The alternative worth weighing is architectural rather than infrastructural: much of the render code
is a pure function of props to a node tree and is only `js && wasm`-tagged because it shares a file
with something that touches `syscall/js`. `web/ui` is the existence proof — it is untagged, it uses
`RenderToString` on the host, and it sits at 84%. Splitting the page packages the same way would make
the render layer host-testable with no new infrastructure at all. It is also a refactor across 62
files, which is why it is written down here rather than done.

---

## 2026-08-11 — The repository was not at 62% coverage; it was measuring the wrong thing

Cam asked for coverage above 80% across the repository. The first measurement said 61.9%, and the
stored ratchet baseline said 80.1 — which meant the CI ratchet had been failing, loudly, and the
number in the baseline file described a population that no longer existed.

The entire gap was `gen/aff/v1`: 3,574 statements of protoc output at 0%, against 12,851 statements
of hand-written code at 79.2%. Nothing had regressed. Generated code had been added to a metric that
had never been scoped to exclude it.

There are only two honest responses to that, and one of them is a trap. The trap is to "fix" the
number by writing a reflection walk that calls all 1,191 generated getters — perfectly achievable,
would have taken twenty minutes, would have moved the headline number over 80% in one commit, and
would have asserted nothing whatsoever. `scripts/coverage-ratchet.sh`'s own opening paragraph
already argues against exactly this: "chasing a target percentage produces tests written to touch
lines rather than assert behaviour." Taking the easy route here would have been writing tests
against the file that warns you not to. So: generated code is excluded from the measurement (in the
Makefile, in CI, and documented in the ratchet script so the three cannot drift apart), and the
remaining work was real tests for real gaps.

**Where the gaps actually were.** Every library package was already 80–100%. What was thin was the
edges nobody exercises from a unit test: `cmd/affseed` at 0%, `aff admin reset-password` at 0%,
`aff admin reset` at 21%, `animefeedflux healthcheck` at 0%. That is a recognisable shape — the code
you only run when something has gone wrong, or when you are setting something up, is the code that
never gets a test, and it is also the code whose failure costs the most.

**Writing the first test for cmd/affseed found a bug three layers deep.** `--force` had never
worked. Not "worked with a caveat" — every `affseed --force` run against a seeded database exited 1.
And it failed three times in a row as each blocker was cleared: the feed slug already existed; then
every `published_at` collided, because they are derived from a day-truncated `now` and two runs on
the same date compute identical timestamps; then the correction's `content_hash` collided, because
its wording was a fixed string. Each fix revealed the next. The flag's own help text said "seed even
if the database already has feeds" and the refusal message promised it "will happily add more
feeds/items/runs" — both describing behaviour that did not exist in any version of this command.

That is the argument for testing a dev-only tool, made better than I could have made it in advance:
nobody had ever run `--force` successfully, and nobody had noticed, because the failure mode of a
seeder is "I'll just delete the database and start over."

**A flake fixed while it was in the way.** `TestSchedulerFiresOncePerDayAndRunsBackup` failed about
one run in three, which matters more than it sounds: it fails inside CI's coverage step, so a red
run means the ratchet never executes at all. The test advances a fake clock in a loop, then polls
without advancing. The scheduler re-registers its next wait only after the real VACUUM finishes, and
computes it from the clock as it stands at that moment — so on a loaded machine the next target can
land beyond the loop's advance budget, and a poll that only waits can never reach a target in the
fake future. The poll now advances too. Eight consecutive runs green.

**Final state:** 81.6% over hand-written code (12,889 statements), up from 79.2%, with the ratchet
floor raised to 81.0. The all-in number including generated code is about 64% and will drift with
every `.proto` change, which is precisely why it is not the number anything gates on.

---

## 2026-08-11 — A second locale cost an afternoon, because §12.6 had already been paid for

Cam asked whether the settings had a language selector. They did not, and the reason recorded in
three places was emphatic: `catalog.go` said `en` was "the only locale this app ships", `TODOS.md`
2107 said "one locale ships (`en`); the point is not other languages", and `PLAN.md` §12.6 said the
i18n work "is not about shipping other languages — it is about where strings live." He asked for the
selector anyway. Building it is what showed those statements were describing the *motivation*
correctly and the *capability* wrongly.

Nothing structural was missing. `Bundle.Register` has always taken a locale. Plural rules are
already selected per locale family (and Spanish falls into the default one/other branch, so it
needed no new rule). `FormatNumber` and `FormatDate` already take a locale and already produce
`1.234,56` for `es` through `x/text`. `gwci18n.Provider` already had a `CurrentLocale` prop. Every
piece of multi-locale machinery was present, tested, and being handed one constant.

That constant was the whole thing. `web/main.go`'s two translator adapters and `adapter.go`'s five
formatters all called `Translate(afi18n.DefaultLocale, ...)` — the parameter named "which locale do
you want" received the answer "the only one" at every site. The fix is a package-level atomic in
`web/i18n/locale.go` that those sites read at call time instead. Read at CALL time, not at wiring
time, is the part with teeth: `wirePages` runs once at boot and hands every page a translator it
keeps for the life of the tab, so a locale captured in the adapter struct would have pinned the app
to whatever language it started in. Same trap in `NewLabelResolver`, which returns a closure.

**What made "every page and every control" cheap.** The re-render is one subscription, in
`renderShellRoot`. GWC's reconciler has no props-equality bailout for function components — I read
`internal/runtime` to confirm rather than assuming, because the whole design depends on it — so
re-rendering the component that mounts the Provider re-runs the entire tree beneath it. No page
subscribes to anything. A page added next year gets the behaviour without knowing the mechanism
exists. The alternative I did not take was a `window.location.reload()` on switch, which is what a
lot of apps do; it would have worked, and it would have thrown away unsaved form state to change a
display preference.

**The bug the browser found and no test could have.** `h.Value` on a `<select>` does nothing in this
renderer — the element ships with no `value` attribute and falls back to `selectedIndex` 0. So with
the app fully switched to Spanish (`<html lang="es">`, every string translated, preference stored),
the language control itself read "English". A control that misreports the setting it exists to show
is worse than no control. The fix is `h.SelectedIf` on each `<option>`, which is already the idiom in
`web/pages/generate/render_workbench.go` and `web/pages/history/filters_ui.go` — I had copied the
wrong precedent. Two other selects in the app still have the original pattern and the same latent
defect: `web/pages/settings/render_data.go`'s feed picker and `web/pages/history/items_ui.go`'s
deleted-items filter, both of which will display their first option regardless of state.

Worth stating plainly: this was found by driving a real browser, not by a test. The unit tests were
all green — key parity, placeholder parity, plural forms, negotiation — and every one of them was
green *because they test the catalogue*, which was correct. The defect was in how a DOM element
receives a value. That is the second time on this project that a screenshot or a browser session has
found what the suite could not (see the 2026-08-10 entry on `tokens.Emit` never being called).

**What I got wrong on the way.** I removed the theme control from the header, because Cam chose "one
preference, one home" and Appearance is that home. The comment I deleted argued the opposite and
argued it well: the header control was there so an operator working at night could reach it BEFORE
signing in, since `/settings` is behind the session. That cost is now real and it extends to the
language too — `/login` renders in whatever was last stored (which does work: verified) with no way
to change either from that screen. If it turns out to matter, the fix is to render the Appearance
controls on the auth routes, not to put a second theme switch back in the header. Recorded here
because the argument was good and the next person to notice deserves to know it was weighed.

**On the translations themselves.** They are model-written and unreviewed by a native speaker, and
the catalogue says so in its own doc comment and in the UI, in Spanish, under the selector. The one
judgement call worth flagging: the typed-confirmation words are translated (REGENERAR, REVOCAR TODO,
IMPORTAR, COMPACTAR). Safe because the gate compares input against the same catalogue lookup it
displays, and necessary because the mechanism is a comprehension check — asking a Spanish speaker to
type "REGENERATE" asks them to copy a shape, and a gate you pass without reading is not a gate.

---

## 2026-08-11 — "There is no feed CRUD", and the framework bug hiding under it

Cam's report was one sentence and it was right in a bigger way than it sounded. Delete had never
been wired to anything — the RPC was written, version-checked and tested months of work ago, and no
screen ever called it. Create was worse: it was wired, and it could not succeed, because the new-feed
draft carried no cron and no timezone and the validator rejects both. So of the four letters in
CRUD, two did not work, and the list that would have made that obvious was nested inside a
disclosure labelled "Recipe settings".

The interesting part was underneath. Chasing why Save hung, I built the same three-layer
instrumentation as the /history hunt — and this time it paid off immediately, because the layers
disagreed in a way that pointed straight at the answer. A marker written directly to `window` from
the save goroutine showed the whole handler running: goroutine entered, RPC called, RPC returned,
result applied. The database showed the feed row written. The screen showed "Saving…" forever. Go
ran; nothing rendered.

That is a framework bug, not an app bug. GWC queues state updates made off the event loop and books
a drain; the drain politely defers itself while a render pass is in flight, and `PostAsync` will not
book a second drain while one is outstanding. If the booked drain is ever lost, every subsequent
update joins a queue that nothing will come back for. The state is stored, correctly, and never
shown. What confirmed it was an accident: an unrelated heartbeat goroutine I added for
instrumentation made the bug disappear, because regular renders give the deferred drain its turn.

The workaround is a scoped heartbeat — `web/ui/pump.go` — that runs only while a mutation is in
flight. Two details of it were learned the hard way. It needs a grace period after the operation
reports done, because a mutation's last act is usually a refetch whose update lands afterwards
(without the grace, the feed was created and the list still did not show it). And it must be a hook,
so the state cell belongs to the component's fiber rather than to a closure that outlives it.

Two smaller lessons worth keeping. `h.Tag` is variadic, so passing it a `[]any` makes the slice one
argument: GWC stringified it and put `0x58930000` in the page's body text — a pointer rendered as
copy is a loud symptom of a quiet mistake, which is that the element also got none of its props. And
`h.Show` keeps its child in the DOM with `hidden`, so a test that scrapes text finds strings that are
not on screen: I spent a few minutes convinced sign-out was failing on every page load because my
probe read a hidden alert.


**Postscript, same day.** I reported feed CRUD as done and verified end to end. The reply was "where
is the option to CRUD a feed?????" — and he was right. Every operation worked; not one of them was
visible. Save sat at the bottom of a collapsed disclosure, Delete inside a ⋯ inside a row inside a
second collapsed disclosure, and the feed list collapsed itself the moment a feed was selected, so
the management surface vanished exactly when work began.

What made me miss it is worth writing down, because it is a trap in how I verify UI. My CRUD test
began with `for (const d of document.querySelectorAll('details')) d.open = true;`. I wrote that line
to get at the form, and in doing so I deleted the only part of the test that could have caught the
actual complaint. The test proved the operations were *possible*, then reported that as proof they
were *usable*. There is now a second test that touches nothing but visible controls and fails if a
panel has to be forced open — and it is the one I trust.

Two real defects fell out of doing it properly. Deleting a feed burned its slug forever, because the
delete is soft and slugs are unique across deleted rows too: recreate the feed you just deleted and
the server tells you it already exists, about a feed in no list that cannot be restored. And a new
feed was created disabled — while a disabled feed cannot even be previewed — so the first thing a
brand-new feed did was tell you three times that it was switched off, without ever saying that you
had to switch it on.

## 2026-08-10 — Seventeen reviewers, and the two bugs that wasted the most time were both mine

A fleet of sixteen correctness reviewers plus one adversarial design critic went over every page.
The findings are in `TODOS.md` (A5/A6); what belongs here is what the exercise taught, which is not
what I expected going in.

**The dominant defect class was not broken code. It was settings that are stored, shown, and read by
nobody.** Eleven of them: the public base URL, the cache-control default, four feed-identity
defaults, three per-feed budget defaults, the staleness threshold, and the price table. Every one
persisted correctly, round-tripped through the UI correctly, and changed nothing about the running
system. Each showed "Saved." — true of the database, false of the product. That is worse than an
unimplemented feature, because an unimplemented feature does not claim to be in effect. Four
independent reviewers found four instances without knowing about each other's, which is how I know
it was a pattern rather than an oversight: the UI layer was built against the settings proto, and
nobody ever went back to make the runtime read it.

**The `/history` cold-load hang cost hours, and the two things that made it expensive were both
self-inflicted.** The first attempt at the eventual fix did not compile — a hook declared after its
use — and `web/build.sh` aborts on a failed build, so the browser kept being served the previous
bundle. The fix looked like it had been tried and failed. It had not been tried at all. Second, I
trusted a debug marker that "never advanced" and concluded the RPC was wedged in the transport,
which sent me into GoGRPCBridge's dialer, into gRPC deadline semantics, and into writing a watchdog
for a hang that did not exist. The marker was itself a stale render. The rule I want to keep: when
an instrument disagrees with another instrument, stop reasoning and instrument the layer between
them. What finally settled it was three separate probes — an off-loop `time.Sleep` loop proving
renders work, a stage marker in `guardUnary` proving three RPCs completed end to end, and a log line
in `RunServer.History` proving the request never arrived — and then an isolation test that put the
two concurrent requests back and reproduced the hang immediately.

**The most embarrassing find was in a primitive nobody suspected.** `web/ui/toggle.go` rendered its
`DisabledReasonKey` whenever the key was set, ignoring `Disabled` entirely. So every screen with a
toggle carried a permanent "Reconnecting to the server — these controls are unavailable until it
comes back" underneath a control that worked fine. I spent a round chasing a stuck DISCONNECTED
state, wrote a self-healing correction into the transport for it, and then found the real cause was
nine lines of markup. The transport change was reverted, because a fix whose premise turned out to
be false does not get to stay just because it compiled.

**On the reviewers themselves.** Sixteen at once against one dev server was noisy — several read a
stale bundle and reported a bug I had already fixed, one overwrote a scratch file another was using,
and their summaries needed checking rather than trusting. But the hit rate on real defects was high,
and three of the blockers (item creation impossible, new feeds unsaveable, import always rejected)
were things a human would only find by trying to do the task rather than by reading the code. The
design critic earned its place too: its central complaint — one systemic layout decision repeated
across five tabs, rather than five separate bugs — was correct, and fixing it once fixed all five.

## 2026-08-10 — Two hours of "broken UI" that was neither broken nor UI

The `/generate` rebuild was working. The browser said otherwise, and it took an embarrassingly long
time to stop believing the browser.

**Trap one: the dev server serves a snapshot, not a directory.** `internal/publish.NewStaticHandler`
reads every asset into memory at construction. That is a deliberate, good decision for a production
server — no per-request stat, no partially-written `.wasm` served mid-build — and it means a running
dev server keeps serving the bundle as it was *at its own start time*. Rebuilding changed the file
on disk and nothing in the browser. Several rounds of "the fix did not take" were the fix never
being loaded.

**Trap two: `pkill` does not kill Windows processes from git-bash.** It exits 0 having killed
nothing. So the restart script's kill was a no-op, the old server kept port 8082, the new server
failed to bind — and the script's own health check then passed, against the old server. A restart
that reports success while changing nothing is worse than one that fails loudly.

Both are now handled by `.devrun/restart.sh`, which rebuilds, kills through PowerShell, waits for
the process to actually be gone, and starts the replacement. Its comment explains why each step is
there, because both traps are invisible and both present as application bugs.

**What the detour did surface, on the way, was a real bug of exactly the shape being chased.** Every
`fetch.UseResource` loader on `/generate` ran once at mount, and on a hard load the mount happens
while the session state is still `appstate.Anon` — the WebSocket has not finished its handshake, so
the calls fail against a socket that cannot carry them, and nothing re-runs them. The page had been
loading no feeds, no settings and no model list whenever it was opened directly rather than
navigated to. The first attempt to fix it keyed a re-fetch on `connected := state != Disconnected`,
which is a bug in the same family: `Anon` is the zero value, so that boolean is *already true*
before there is any session and therefore never changes when one appears. The effect is now keyed on
the session state itself.

Worth noting what caught the smallest defect in the batch: a missing i18n key for the temperature
field's placeholder was found by `web/i18n/callsite_test.go` — the test written a few sessions ago
after shipping a raw `common.connectionUnreachable` key to the screen. It has now paid for itself
twice.

## 2026-08-10 — New brand, and the second half of a bug we thought we'd fixed

### The dark theme was never on

Earlier today's entry records finding that `tokens.Emit()` had zero callers, so every
`var(--color-…)` resolved to nothing and the app rendered as browser defaults. That was fixed, and
it was only half the bug. The dark palette lives under `:root[data-theme="dark"]`, and **nothing in
this application had ever called `ui.SetTheme`** — the attribute that selector needs was never set
on any element, ever. So `DarkTheme()`, its WCAG-corrected swatches and its own passing unit tests
were unreachable for every operator, including one whose OS is set to dark.

It was caught the same way as the first half, which is the part worth remembering: a screenshot in
dark mode came back **byte-identical** to one in light mode. Neither half of this bug is visible
from the Go side — both are "the code is correct and reaches nobody" — and in both cases the tests
passed throughout, because they assert on the rules the token layer *generates*, which is true
whether or not anything selects them. Two instances in one day of the same failure shape (built,
tested, unreachable) is a pattern, not a coincidence; it is the same one `D0-13`'s note records for
`web/ui.Toast` and friends.

The fix (`web/shell/theme.go`) is three-state — `Match system` (default), `Light`, `Dark` — rather
than a two-state toggle. A toggle has to guess an initial value, gets it wrong for half of all
users, and silently stops tracking the OS for all of them; "system" is the only state that can
express *I have not chosen*. It applies before the first render rather than from a component
effect, because a component effect paints one frame of light theme first, which on a login screen
in a dark room is a white flash.

The control went in the header rather than in Settings, and the argument that settled it is
structural, not aesthetic: `/settings` is behind the session, and `/login` is where an operator
working at night meets this application first. A theme control you cannot reach until after you
have signed in arrives one screen too late.

### The mark could not be a vector, so the wordmark could not be an image

The new brand is a kitsune crest — a fox over three broadcast arcs, in a hex shield. Cam supplied
it as three 1536×1024 renders on a flat grey card: no alpha, no vector.

Keying the grey out took three attempts, and the failures are the useful part. A single border-median
grey is not good enough — the backdrop carries a vignette that darkens it by ~0.05 toward the edges,
which is larger than any sane luminance floor, so a constant reference keys the entire frame as
subject. A trimmed per-channel degree-4 polynomial fit over the achromatic pixels solves that.

The glow was the real problem. It is wide, soft, and only half-hued at its edges, so keying it
yields a grey smudge if you keep the neutral part and a blue one if you unpremultiply anyway —
both of which look like a dirty rectangle on any surface that is not the source's own grey card.
**The glow is not extracted.** It is a `drop-shadow` reading the accent token instead, which is
strictly better: it follows the theme rather than being one fixed colour and radius everywhere.

The lockup could not be salvaged at all, and that turned out to be a design decision rather than a
setback. Its wordmark is dark navy sitting *inside* a bright blue bloom — loosen the key and the
mark ships inside a cloud twice its size; tighten it and the text dissolves before the cloud does.
And it is dark navy, so it would be invisible on the dark theme even if it had extracted perfectly.
So the wordmark stays HTML text beside the crest: crisp at any size, and it follows `color` into
dark mode. `web/shell/header.go` was already doing this for an unrelated reason (a GWC v5.0.1
reconciler gap around SVG `<text>`), which is a nice accident.

### Two smaller things the screenshots turned up

`/login` was rendering the brand lockup twice — once in the header, once in the auth card, 200px
apart — despite `header.go`'s own doc comment stating that ANON shows "the mark alone". The comment
described intent nobody had implemented.

And `h.Show(false, node)` does not remove a node. It clones it with `hidden` set and relies on the
UA sheet's `[hidden] { display: none }` — a single type-less selector that **any** class rule
setting `display` outranks. `.af-header__rule` is exactly that shape. This is a whole class of
silent bug (the hidden thing is visible) waiting on every future `css.Display.*` rule in the app,
closed with one global `!important`.

---

## 2026-08-10 — Documentation-backfill pass over serving/auth/transport/ops: PLAN.md was two designs behind on login

Three findings from a doc-vs-code audit scoped to `internal/publish`, `internal/auth`,
`internal/bridge`, `internal/rpc`, `internal/ops`, `internal/obs`, `internal/config`,
`cmd/animefeedflux`, `cmd/aff`. No Go was touched.

**PLAN.md's §2.1 sequence diagram and §4 described a login flow the code no longer implements.**
The diagram had `AuthService.Login`'s RPC response itself carrying the `__Host-` session cookie back
over the already-open anonymous socket. The real, deliberately-built mechanism (`internal/bridge/
ticket.go`, `web/wsconn/ticket.go`) is materially different: `Login` returns a single-use, 20-second
login ticket in a gRPC response *header* (never the raw session token — landing that in WASM memory
is exactly what §4 forbids), the client stashes it in `sessionStorage` and forces a full page
reload, and the cookie is set only on the 101 Switching Protocols response of the *reconnect* that
presents the ticket — spliced into the raw upgrade bytes via a hijacked `net.Conn`, because
`Set-Cookie` cannot go through `http.ResponseWriter` once `gorilla/websocket` has hijacked the
connection. §4 had no §4a describing any of this at all. Added one, backfilling the mechanism, the
verified dead end that ruled out a simpler design (Chromium does not apply `Set-Cookie` from a
WebSocket upgrade response to its cookie jar — confirmed against both the live server and an
isolated Node WS server), and the residual cost the code comments already admit: a plain page
refresh after login has no cookie and no ticket to present, so it silently drops to anonymous —
"one transport, no HTTP side door" and "durable session across a plain refresh" are in tension here,
not resolved.

**Two TODOS.md notes describing production gaps had already been closed by other work and nobody
re-ticked them.** `C4-15` (per-feed error counts on `/healthz`) was still `[ ]` with a note saying
the wiring "requires editing `cmd/animefeedflux/wire.go` (owned by another agent, not edited here)"
— but that wiring is in the tree: `wire.go`'s `HealthFeeds` closure already calls
`ops.LiveFeedErrorCounts` and populates `FeedHealthInput.ErrorCount` for real, reachable from
`runAll`. Ticked, with the evidence. `B3-11`'s note said "PLAN.md A7's scheduler (mapping a stored
`FeedSpec` into a live `generate.Spec`) does not exist yet" — also stale: `wire.go`'s
`generateSpecFrom` + `wireRunExecutor.ExecuteRun` do exactly that mapping for `RunNow`, wired into
`rpc.NewFeedServer` at `wire.go:1055`. The task was already correctly ticked; only the explanatory
note was wrong, so it was corrected in place rather than left implying the CLI e2e test's
fixed-spec stand-in is covering for a real gap that no longer exists.

**One undocumented magic number recorded:** `AFF_PROVIDER_MAX_INFLIGHT` defaults to 4 against a
3-run worker pool (`AFF_MAX_CONCURRENT_RUNS`), and the code carried no comment saying why they
differ. Reasoning backfilled into PLAN.md §14.3: the same semaphore also gates interactive
`Sample`/`SampleStream` calls, so sizing it to exactly the scheduled-run cap would make every sample
block behind three in-flight scheduled runs.

**Security properties named in the task brief were checked against code, not assumed:** the pepper
comes from `AFF_PASSWORD_PEPPER` via `internal/config`, never the DB (`Pepper()`'s zero-pepper no-op
is the fallback for "not configured", not a footgun); salt is 16
fresh CSPRNG bytes per credential (`password.go`'s `rand.Read`), never derived from id/email/a
constant; no code path reads `password_changed_at` to force a rotation — confirmed by its absence,
not by a comment saying so; the `__Host-session` cookie construction (`internal/auth/session.go`'s
`NewSessionCookie`/`ExpiredSessionCookie`) never sets `Domain`. All hold as designed; recorded with
their evidence rather than left as "everyone believes it, nobody checked."

## 2026-08-10 — The `/healthz` gap this file already flagged got closed the same day

The entry directly below records the deliberate decision to leave `internal/publish`'s
`DefaultHealthGrace` unwired to `AFF_STALE_GRACE_FACTOR`, because that package and `wire.go` were
"owned by another agent's in-flight change" at the time. That constraint lifted later the same day:
`cmd/animefeedflux/wire.go:1259` now sets `HealthGrace: ops.ResolveStaleGrace()` in the
`publish.Deps{...}` literal `buildPublishHandlerWithInvalidator` builds, so `/healthz`, the nightly
Slack webhook, and `aff doctor` all resolve the same grace factor from the same env var. The
divergence risk the entry below warns about — an operator staring at a "healthy" `/healthz` while a
stale-feed alert has already fired on a different threshold — no longer exists in the tree.
`TODOS.md` C4-08 carries the current "GAP CLOSED" note; `CHANGELOG.md`'s entry was corrected in
place rather than left asserting the old gap. This entry exists so the reasoning below (why the gap
was left open on purpose, not by oversight) isn't misread as still-current without a pointer forward.

## 2026-08-10 — Making the staleness grace factor configurable without letting `/healthz` and the Slack webhook disagree

`TODOS.md` C4-08/C4-15 asked for the staleness grace factor (how many multiples of a feed's own
cadence it may go quiet before being called stale) to be configurable rather than a hardcoded `2.0`,
since `C3-11` (the real observed Slack poll interval) is still unresolved and `2.0` is a documented
guess. `internal/publish/health.go`'s own doc comment already states the constraint this has to
respect: "the webhook and `/healthz` can never disagree about what 'stale' means" — both currently
default to the identical `2.0` (`ops`'s `SchedulerConfig`/`DoctorConfig` default and `publish`'s
`DefaultHealthGrace`), and that parity is load-bearing, not incidental (`health_test.go` asserts the
two constants match).

The nearly-shipped-wrong version of this change made `ops.NewScheduler`'s zero-value `Grace` default
read `AFF_STALE_GRACE_FACTOR` **without** doing anything about `/healthz`'s hardcoded default. That
looks like a clean, self-contained improvement in isolation — it is genuinely reachable, tested, and
live (neither `wire.go`'s `runAll` nor `cmd/aff/doctor_cmd.go` sets `Grace`/`StaleGrace` explicitly,
so the env var takes effect today for the nightly webhook and `aff doctor`) — but it silently breaks
the exact invariant `health.go`'s comment calls out: set the env var in production and the Slack
alert's threshold moves while `/healthz`'s stays at `2.0`, so an operator staring at a "healthy"
`/healthz` page could be one wire.go deploy behind an alert that already fired, or vice versa. That
is a worse failure than either surface being wrong on its own, because it looks internally consistent
from either surface alone and only shows up as a cross-surface contradiction nobody is checking for.

Landed instead: `internal/ops/cli.go` exposes `StaleGraceEnv`/`ResolveStaleGrace()` as the one place
that knows the env var name and the parse/validate/fallback logic, used by both `NewScheduler` and
`NewDoctorConfig`'s defaults (so the webhook and `aff doctor` are configurable and reachable today),
but `internal/publish`'s `DefaultHealthGrace` was deliberately left untouched — that package is owned
by another agent's in-flight change per this task's constraints, and `cmd/animefeedflux/wire.go`
(the only place that could pass the same resolved value into `Deps.HealthGrace`) is off-limits here
too. The gap is documented loudly in both `TODOS.md` (C4-08's entry) and a `CHANGELOG.md` note rather
than closed by quietly accepting the divergence risk: setting `AFF_STALE_GRACE_FACTOR` today is only
"fully" safe once `wire.go` adds one line (`HealthGrace: ops.ResolveStaleGrace()`) — until then, an
operator who sets it should know `/healthz` hasn't caught up.

## 2026-08-10 — The server ran for the first time, and nothing could log in

First boot of `cmd/animefeedflux`, and both ways into the admin plane were dead — the CLI at the
transport layer, the browser at the protocol layer. Two unrelated bugs that happened to produce the
same symptom.

**The CLI.** `cmd/aff` dials `AFF_ADMIN_ADDR` with a real `grpc.NewClient` (`cmd/aff/client.go`'s
`realDial`), but the admin listener (`cmd/animefeedflux/wire.go`) served only `bridge.NewServer`'s
`http.Server` — the WebSocket tunnel and, once built, the static UI. No `*grpc.Server` was bound to
that address anywhere in the tree. Every CLI command therefore died at the transport, before any RPC
was dispatched, and `aff login` printed `aff login: authentication failed` — byte-identical to
`internal/rpc/auth.go`'s `errAuthFailed` ("authentication failed", `codes.Unauthenticated`), which is
what a genuinely wrong password produces. `client.go`'s `isUnreachable` exists to tell these apart
(`codes.Unavailable`/`codes.DeadlineExceeded` from a dead port versus everything else), but its own
doc comment records the deliberate reason it can't always: PLAN.md §12.1 forbids distinguishing
failure causes in the generic case, so a non-status transport error is *treated* as a credential
rejection rather than reported as unreachable, on purpose, as the safe default. A dead port and a
wrong password produced the same output for the same reason the account-existence oracle rule exists
— and the one thing that would have proven the RPC never arrived, `auth_events` staying empty through
every attempt, took reading the database directly to notice, because the CLI's own output gave no
hint.

**The browser.** No CLI bug at all — a protocol-level chicken-and-egg. `bridge.NewServer`'s
`ServeHTTP` (internal/bridge/server.go) validates the session cookie *before* the WebSocket upgrade,
with no exemption for `AuthService.Login`: no cookie, no upgrade, a bare 401 — confirmed against
`internal/bridge/server_test.go`'s own `TestUpgradeWithoutCookie_Unauthorized`, which proves the exact
behavior and does not special-case Login either. A session is required to open the socket, and the
socket is the only place `AuthService.Login` — the one thing that can mint a session — lived. §4
forbids the token ever reaching JS/WASM-readable memory (no JWT in a response body, no token field a
WASM caller could read), which is exactly why Login answers with a header a real `*grpc.Server`
attaches via `grpc.SetHeader`, not a message field a WebSocket frame could carry cleanly. The missing
piece was a plain HTTP login endpoint outside the bridge entirely — designed in §4 (`__Host-` cookie,
`HttpOnly`, `Secure`, `SameSite=Strict`, no `Domain`) and never implemented anywhere. `internal/e2e`'s
own suite had already documented this exact gap rather than discovering it fresh: `internal/e2e/app.go`
opens a second, plain (non-bridge) `*grpc.Server` just to call Login, with a comment naming it
outright — *"a real production gap and not a shortcut this suite invented"* — and that comment sat
there, correct and ignored, while the suite stayed green the entire time, because a workaround inside
a test proves the workaround works, not that the gap it routes around has been closed.

**The fix, in one shape twice.** `*grpc.Server` implements `http.Handler` (grpc-go's own doc comment
on `Server.ServeHTTP`), so the admin listener now wraps its handler in `h2c.NewHandler` (cleartext
HTTP/2, which `grpc.NewClient`'s insecure transport speaks with prior knowledge) and a new
`adminRouter` in front of it: an HTTP/2 request whose `Content-Type` begins `application/grpc` goes to
a real `*grpc.Server` registered with the same six services the bridge uses; everything else falls
through unchanged. One listener, one port, §2's two-listener boundary untouched — `cmd/aff` gets a
real transport without a second admin port to secure. For the browser, `internal/bridge/httpauth.go`
adds `POST /auth/login` and `POST /auth/logout` as ordinary HTTP handlers, routed ahead of both the bridge
and the SPA static fallback in the new `adminMux`, backed by the *same* `AuthServer` every gRPC path
uses (via `bridgeAuthenticator`, which replays the call through `AuthServer.UnaryInterceptor()` with a
fabricated transport stream so `grpc.SetHeader`'s token capture still works outside a real RPC
dispatch) — never a second, independently-constructed auth path that could drift from the first.

**Two routing bugs surfaced in the same pass, same root cause (no admin router existed before this).**
Before `adminMux`, the admin listener's handler *was* the bridge with nothing in front of it, so there
was no place for the static UI to be mounted at all — the SPA's own `<base href="/">` had nowhere to
resolve, because nothing served `/` as anything but a WebSocket-upgrade attempt. And there was no
SPA-route fallback: `adminMux.ServeHTTP` now serves the shell for any path `m.static.Has` doesn't
recognize as a real asset, so a deep link or a refresh renders the client router's own not-found
instead of a bare 404 with no way back in.

The reusable shape, same as the "seven things" below but one layer further out: none of this is
visible to `go test ./...`, because the CLI's tests supply fakes for the network and the e2e suite's
workaround *is* the missing piece, faithfully exercised. All four gaps — the dead transport, the
upgrade-before-Login deadlock, the routing collision on `/`, and the missing SPA fallback — appeared
within ten minutes of running the actual binary for the first time. A green test suite proved every
component in isolation; running the thing was what found the seams between them.

## 2026-08-10 — A systematic sweep for "built, tested, reachable from nothing" found more of them

The login gap above and the "seven things" below were each found one at a time, by tracing a
specific suspicion back to a composition root. `docs/unreached-components.md` is what happens when
that search is made systematic instead: every exported identifier in `internal/`, `web/`, and
`gen/aff/v1` (2,233 declarations, 1,717 unique after dedup) checked for a real call site outside its
own file, narrowed to 88 zero-caller candidates, each read by hand against `PLAN.md` to separate a
real gap from a false positive (interface satisfaction, function-passed-by-reference, functional
options with sane defaults).

The count of things independently found "built, tested, and wired to nothing" was already close to
ten before this sweep — the seven below plus `ops.Scheduler` and `obs.Setup` (both since fixed,
`docs/unreached-components.md` notes) — and the sweep's own write-up refers to that running tally as
"the original ten" while cataloguing what it added on top: 8 dead-on-arrival findings and 6
recommended-for-deletion speculative ones in section A/B of the doc.

The single largest is **the entire `web/ui` component library** — `Button`, `Modal`, `Confirm`,
`Toast`, `Tabs`, `StatePanel`, `Kebab`, `Input`, `Select`, `Table`, `Toggle`: 1,724 non-test lines,
162 lines of its own tests, and zero production importers. `grep -rl "AnimeFeedFlux/web/ui\""` across
every page under `web/pages/{auth,generate,history,settings}`, including test files, returns nothing —
every page instead imports `GoWebComponents/v5/ui` directly under the same local alias (`ui`), which
is what made the gap easy to miss by name alone, and hand-rolls markup instead. Nothing is visibly
broken — the pages work by reimplementing equivalent markup — but every centralized guarantee the kit
exists to provide once (the shared `focusVisible()` a11y treatment, one reviewed implementation of
destructive-action confirmation) never reaches a real page, because no real page runs that code.

Other confirmed findings worth naming: `llm.Error.Scope` (`ScopeAccount`/`ScopeRecipe`) is set at
every fatal-classification site and read nowhere, so the one-bad-key-trips-the-global-kill-switch
design in §8 does not happen — each feed instead burns its own five-failure budget against the same
dead credential before disabling itself; `obs.WithRunID`/`obs.WithRequestID` are never called, so
every log line is missing the identifier that would let an operator filter logs down to one run
during an incident, despite the log/trace correlation contract in §15.0a existing and working
otherwise; and `store.ListAuthEvents`/`RecentFailures` — the read side of the audit trail
`internal/rpc/auth.go` writes at ~19 call sites — had no RPC and no UI reaching it at all (closed in
this same session by `SystemService.ListAuditEvents`, see `CHANGELOG.md`).

## 2026-08-10 — A tick audit un-ticked tasks for the first time, not just added them

Every previous audit pass in this project's history has *added* unticked tasks it found missing.
This is the first time one went the other way: re-checking `A9-20`..`A9-24` (publish-plane tracing
spans and metrics, §15.0a) and `B2-06` (verifying `SampleStream`/`RunService.Watch` actually stream
through the bridge, §11) against the tree found no supporting code at all — `internal/publish` has no
spans, and the streaming verification A9-checks depend on had never been written — despite all six
being marked `[x]`. They are `[ ]` again now, with the re-audit's finding recorded inline rather than
just the checkbox flipped.

Worth writing down on its own, separate from whatever caused the individual mismarks: an over-marked
task is worse than an unmarked one. An unticked box still gets looked at eventually, because it reads
as unfinished. A ticked box against work that was never done reads as settled, and nobody re-examines
a checked box — it takes an audit that starts from *reading the code* rather than *trusting the last
tick* to ever catch it. `TODOS.md`'s stated failure mode is "silent under-marking"; this session found
the opposite failure is real too, and arguably more expensive, because it hides rather than merely
delays.

## 2026-08-10 — The trivia spoiler does not survive a real reader; Slack only ever hid the bug, not the answer

PLAN.md §5.5 specified a trivia item's answer goes into `content:encoded` "behind a spoiler break,"
on the assumption that some `<details>`/`<summary>`-shaped disclosure widget would keep it hidden
until a reader chose to reveal it. That assumption was never checked against an actual sanitizing
reader until this session (`docs/spoiler-design.md`). It does not hold: `GoWebComponents/v5/sanitize`'s
`DefaultPolicy` — the sanitizer ArticleFlux's reading pane runs `content:encoded` through before
rendering it — does not allow `details` or `summary`, and disallowed tags are not dropped, they are
*unwrapped*: sanitized children survive, the tag around them does not. `<details><summary>Reveal
</summary>THE ANSWER</details>` comes out the other side as plain `THE ANSWER` text, inline, with no
toggle and no way to re-hide it.

The reason this shipped unnoticed: Slack was the only consumer this project ever actually tested
against (§5.5's own "practical check"), and Slack never renders `content:encoded` at all — it only
ever shows `description`, which was already question-only. The one channel the design was verified
against structurally cannot exhibit the bug, because the spoiler mechanism was never on the path
Slack reads. The failure only shows up in a full-content reader, which is ArticleFlux — the second
consumer PLAN.md §1 names by name — and nothing in this repository had ever emitted or sanitized a
`<details>` tag to find out. `docs/spoiler-design.md` records four options (whitespace/scroll
distance, a separate later item linked to the question, permalink-only answer, or accepting the
answer is visible and designing around that) with their real cost against all three consumer shapes;
none is adopted, and PLAN.md §5.5 now points at the document instead of restating the original
assumption as settled.

## 2026-08-10 — `DOD-4` puts a hard floor under how soon this project can be called done

Auditing `PLAN.md §19` against the tree (`docs/definition-of-done.md`) found the definition of done
is not nine independent checks — seven of the nine are gated on one thing that has never happened
(a production deployment), and two of those seven additionally require elapsed time *after* that
deployment: `DOD-3` needs 7 consecutive days of Slack delivery, and `DOD-4` needs **30 consecutive
days of production trivia generation with no near-duplicate pairs** — explicitly not provable by the
existing canned-corpus novelty harness, because that only proves the dedup mechanism works, not that
a live model run daily for a month won't repeat itself. The practical consequence is worth stating
plainly rather than leaving implicit: whatever day the first production deploy happens, `DOD-4` cannot
be satisfied before a month after it, no matter how fast everything else moves. It is a clock that has
not started, not a task that can be finished by working faster.

## 2026-08-10 — Seven things built, tested, and wired to nothing

The single most reusable lesson of this project so far, because it recurred
seven separate times across a single day of work rather than once:

- The **pepper** (`internal/auth/pepper.go`) — implemented, unit-tested, zero
  production callers.
- **Reset tokens** — single-use logic implemented and tested against the
  store; no proto RPC and no CLI command ever issued or consumed one.
- The **off-box backup encryptor** — chunked AES-256-GCM, tested against
  round-trip fixtures; `OffsiteDir` pointed at a local directory, so nothing
  it produced ever left the box.
- `item_revisions` — every `Update` wrote a row; nothing could read one back.
  `ListRevisions` and `RevertRevision` did not exist.
- The **novelty gate** — read `item_embeddings` for its dedup check; nothing
  had ever written a row to that table, so every candidate was compared
  against an empty corpus and always passed.
- The **i18n `Provider`** — `UseI18n()` calls were already written on the
  auth pages; nothing mounted the provider that fills it, so every call read
  an empty bundle and silently rendered its fallback default.
- The **entire admin UI** — four page packages, unit-tested, and
  `web/main.go` never called `shell.RegisterPage` for any of them. Every
  route rendered the shell's placeholder regardless of how complete the page
  behind it was — about eighty `TODOS.md` tasks blocked on wiring, not work.

Every one of these passed its own unit tests. That is not a coincidence or a
run of bad luck — a unit test cannot see an absent caller, by construction.
It exercises the function directly, which is exactly the thing a missing
composition root does not affect. "Tested" was read as "working" seven
times, and each time the gap was invisible until something existed that
would actually have had to call the code: `cmd/animefeedflux`'s composition
root, the adversarial `sectest` suite, `web/main.go`'s composition root, or
just someone reading `TODOS.md` against the tree instead of against the
last time it was ticked.

The placeholder screens made the UI case worse than a compile error would
have: a route that renders *something* looks like a legitimate, if unfinished,
page. A test asserting "the router has five routes" would have passed the
entire time it was broken. The fix in every case was the same shape —
find the one place nothing calls the tested thing, and make that call
unconditional rather than an opt-in step a future composition root can
forget. `ServeHTTP` now sets `Session.Token` from the cookie it already
validated with no wiring step to skip; `web/main.go`'s `Mount` takes a wire
callback invoked before route registration, not after.

The general rule this earns: a component is not "done" because its tests
pass. It is done when something that runs in production actually reaches
it, and that has to be checked by tracing the call graph outward from a
real entry point, not by trusting the component's own green tests.

## 2026-08-10 — `GoWebComponents@latest` is an abandoned v1

The admin UI dependency was first added as `GoWebComponents@latest`. That
resolves to an abandoned v1 proof-of-concept whose reconciler package is
unexported — nothing outside the module can mount a tree with it. A whole
shell got built on hand-rolled `syscall/js` before this was noticed and the
real library was found at a different module path entirely:
`GoWebComponents/v5` at `v5.0.1`, with the router, state, ui, css, a11y and
i18n packages the plan actually needed. `@latest` picking the wrong major
version silently, with no error to catch it, is the trap worth remembering —
checking the module path against the source, not the tag, is now the first
step before building on any dependency here.

A smaller version of the same trap: the docs describe a `Class(...)` helper
that does not exist in v5.0.1, only `ClassStr`. Three separate agents hit
that independently before anyone read the source instead of the manual.

## 2026-08-10 — A selector that can never match, caught only by rendering it

`css.DataTheme` composed with `Global(":root")` emitted
`[data-theme="dark"] :root` — a selector asking `:root` to be its own
descendant, which no document can ever satisfy. Dark mode would have shipped
looking correct in every code review and failed silently in every browser.
It was caught only because a test ran a real `css.Harvest()` over the
composed rule and read the emitted selector, rather than asserting that the
two pieces were present. Composing two style helpers that are each correct
in isolation is not evidence the composition is; the only check that works
is rendering the actual output.

## 2026-08-10 — The pepper was applied to the wrong thing, and said so

`pepper.go` documented `HMAC-SHA256(pepper, argon2idOutput)` — the pepper
applied *after* the memory-hard hash. The code that got wired up applied it
to the *password string*, *before* hashing instead. The agent that wired it
recorded the deviation honestly in a comment, and the constraint behind it
was real: `auth.Hash`/`Verify` only accept and return a PHC string, with no
exported access to the raw argon2id output, and `internal/auth` was
off-limits to that agent.

Neither order is cryptographically weaker — feeding the pepper through a
memory-hard KDF either way is fine. The actual defect was narrower and more
dangerous: the codebase held two mechanisms, `VerifyPeppered` implementing
the documented order with zero callers, and the wired one implementing a
different order the docs did not describe. The next person to read
`pepper.go` would believe the output was peppered and reason from something
untrue. A documented deviation that never gets reconciled decays into a lie
the moment someone reads the doc instead of the diff.

Fixed by implementing the documented order rather than editing the document
down to match the code — `HashPeppered`/`VerifyPasswordPeppered` now derive
the argon2id output exactly as `Hash`/`Verify` do, then apply
`Pepper`/`VerifyPeppered` to that. `Hash` and `Verify` themselves are
byte-for-byte untouched, so the no-pepper deployment path (the common case)
is unchanged by construction, not by testing.

## 2026-08-10 — Reverting `go.mod` is exactly as destructive as editing it

Three build breakages this session, previously assumed to be `go mod tidy`
running mid-build. The real cause was different and more instructive: agents
following "do not touch `go.mod`" ran `git checkout -- go.mod go.sum` to
undo what looked like their own accidental edit, and in doing so reverted
dependencies a *sibling* agent had legitimately added in a different task.
Each revert was locally correct — the file agents saw did look wrong to
them, in isolation — and globally destructive, because the build graph is
shared across everyone working at once. That is the same failure shape as
the earlier `go mod tidy` incident, just from the opposite direction: adding
and reverting are both writes, and both are prohibited for the same reason.

The rule is now explicit rather than implied: do not edit `go.mod`/`go.sum`
**and** do not revert them either — leave them alone entirely and report
what looks wrong. A locally correct action is not safe by default when the
thing it acts on is shared.

Worth keeping for the opposite reason too: one agent refused to act on an
unverifiable coordinator claim about a dependency, checked it against the
actual module cache itself, and solved the problem with what was already
present rather than trusting the claim. That scepticism is the behaviour
the corrected rule is trying to produce generally.

## 2026-08-10 — i18n was ruled out for the wrong reason

`D0-20` said: no i18n, single user, English, out of scope. Cam reversed it. The
reversal is worth recording because the original reasoning was not wrong so much
as answering a different question.

"Do we need other languages?" — no, and still no. But that is not what i18n buys
at one user and one locale. What it buys is that strings have an owner. Without a
catalogue the same label gets written three slightly different ways on three
screens, an error message drifts from the server text it is supposed to mirror,
and there is no way to see what the interface actually says without reading every
component. With one, the interface's vocabulary is an artefact that can be
reviewed and diffed.

The timing argument is the stronger one. Retrofitting i18n means touching every
component that was written without it — a large, low-status, all-at-once job that
never gets scheduled. Phase D has barely started, so the cost right now is close
to zero and falls with every screen not yet written. That is why `D6-*` says to
extract per surface *alongside* each screen rather than as a pass afterwards: a
cleanup pass over finished screens is exactly the retrofit being avoided.

Two boundaries fell out of writing it down, and neither was obvious beforehand:

- **Feed content must never go through the catalogue.** It is authored by the
  model in the feed's own configured language and is data, not interface. A
  well-meaning wrapper around item titles in the history screen would silently
  corrupt published output — and it would look like tidiness.
- **The generic login-failure string stays generic in every locale.** §12.1
  removes the account-existence oracle by using one message for every failure. A
  translator handed those keys in isolation would naturally make "no such
  account" and "wrong password" distinct, because that reads better, and would
  reintroduce the oracle without ever touching the auth code. The constraint has
  to live in the catalogue, not only in the login page.

The gate is a zero-literal ratchet, for the usual reason: a convention nobody can
check decays, and this one decays invisibly — a hardcoded string looks exactly
like a translated one until someone greps.

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
