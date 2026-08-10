// Package settings implements AnimeFeedFlux's admin `/settings` surface
// (PLAN.md §12.5, TODOS.md D4-01..11 and D6-13): six sections — Security,
// Provider, Generation, Publishing, Data, About — all reachable only from
// the AUTH application state (PLAN.md §12, D-FLOW).
//
// # File layout and why it is split this way
//
// Mirrors web/pages/generate and web/pages/history's own split (itself
// modeled on web/shell's appstate/guard/backoff-vs-shell/wsconn split):
// pure, host-testable logic in files with no build tag, browser-only GWC
// rendering behind `//go:build js && wasm`.
//
// No build tag (host-testable, `go test ./web/...` runs these):
//
//   - i18n.go — the Translator/Formatters lookup interfaces this package
//     needs, plus a fallback implementation so the package compiles,
//     tests, and (once rendered) degrades to legible English rather than
//     a blank page before the real catalogue is wired in.
//   - screenstate.go — the six-state matrix (loading/empty/populated/
//     error/disabled-with-reason/disconnected) every list and panel in
//     this page implements (PLAN.md §12.6, TODOS.md D-FLOW).
//   - validate.go — publishing-field validation (absolute URL, correct
//     scheme) and the password-length client-side convenience check.
//     Mirrors PLAN.md §7's "the UI's copy is a convenience, never the
//     gate": FeedService.ValidateSpec-style server validation is the
//     real gate for feed fields, and for settings fields the equivalent
//     gate is SystemService.UpdateSettings itself — these pure functions
//     only decide what renders against which field before that round
//     trip, and again if the server rejects it.
//   - confirm.go — typed-confirmation matching for irreversible actions
//     (PLAN.md §12.6) and the revoke-all/regenerate-recovery-codes
//     warning conditions.
//   - format.go — byte-size and uptime/duration formatting built on top
//     of the injected Formatters, never fmt.Sprintf directly on a
//     locale-sensitive value (PLAN.md §12.6).
//   - sessions.go — pure view-model shaping for the active-sessions list
//     (which row is "this device", revoke-one vs revoke-all framing).
//   - client.go — the minimal per-RPC-group interfaces this package
//     depends on (SystemClient, AuthClient, FeedClient), satisfied
//     structurally by gen/aff/v1's generated clients (gen/aff/v1 carries
//     no build tag itself) without this package importing web/wsconn.
//
// `//go:build js && wasm` (browser-only, NOT covered by `go test`):
//
//   - deps.go — the wiring seam (see below): Deps carries the three
//     client interfaces plus Translator/Formatters; Init(Deps) installs
//     them in a package-level variable, and an init() func self-registers
//     "/settings" with shell.RegisterPage so blank-importing this package
//     from web/main.go is enough to make the route exist.
//   - render.go, render_security.go, render_provider.go,
//     render_generation.go, render_publishing.go, render_data.go,
//     render_about.go — the actual GWC component tree, one file per
//     section plus the tab/section shell in render.go.
//
// # The wiring seam this package could NOT close, and why
//
// Same gap web/pages/generate's doc.go already reports: web/wsconn.Conn
// (the one control-plane connection web/shell.Mount dials) exposes only
// Conn.Auth (affv1.AuthServiceClient) as of this change — there is no
// exported way for a page package to reach affv1.SystemServiceClient or
// affv1.FeedServiceClient, both of which this page needs (Security uses
// Auth; Provider/Generation/Publishing/About use System; Data's TOML
// export/import uses Feed). web/wsconn and web/shell are off limits to
// this change (owned by other concurrently-running agents per this task's
// hard rules), so this package cannot add the missing Conn fields itself.
//
// The seam instead: Init(Deps) in deps.go takes SystemClient, AuthClient,
// and FeedClient values directly. Until whatever wave adds
// Conn.System/Conn.Feed also calls settings.Init(settings.Deps{...})
// from web/main.go (or wherever Conn is constructed), Render shows this
// package's own "not wired" treatment via the six-state matrix's error
// state rather than a blank page or a panic. Reported explicitly per this
// task's instructions rather than silently worked around.
//
// # i18n
//
// TODOS.md D4/D6-13 requires zero user-visible string literals under
// `settings.*` keys, but this package must not import web/i18n (owned by
// another concurrent agent — see i18n.go's package doc comment for the
// exact minimal interface this package needs instead: Translator's single
// `T(key string, args ...any) string` and Formatters' DateTime/Currency/
// RelativeTime/ByteSize methods).
//
// # §12.5 assumptions that did not hold, or were underspecified
//
//  1. "Last successful run per feed" (About section, D4-11) has no
//     dedicated RPC field. proto/aff/v1/feed.proto's Feed message carries
//     `last_built_at` (set whenever a run last changed the feed's
//     published representation) but not a separate "last run that
//     specifically succeeded" timestamp — RunService would have that, per
//     run, but querying it per feed for an About panel is a much heavier
//     round trip than this section's scope implies. About therefore shows
//     `last_built_at` from FeedService.List labeled as such (i18n key
//     settings.about.lastBuild, not settings.about.lastSuccess) rather
//     than silently presenting a proxy as the thing PLAN.md §12.5 named.
//  2. Data's "recipe TOML export/import" (D4-10) is per-feed in the RPC
//     surface (FeedServiceExportTOMLRequest/ImportTOML both key off
//     feed_id), not a single whole-database export. The Data section's
//     export/import UI is a feed picker plus one TOML text area, not a
//     single "export everything" button — there is no RPC that would back
//     a single-file multi-recipe export.
//  3. SystemService.UpdateSettings carries no expected_version (the
//     proto's own comment explains why: `settings` is a flat singleton
//     with no per-row version column, unlike Feed/Item). This page
//     therefore has no optimistic-concurrency conflict UI for Settings
//     writes the way D2-16 asks for Feed edits — a second admin tab
//     saving Settings after this one silently wins, which is the
//     documented behavior of the RPC this page calls, not a gap in this
//     page.
//  4. "Vacuum" (D4-10) has no backing RPC anywhere in proto/aff/v1 —
//     SystemService has Stats/SetGenerationEnabled/GetSettings/
//     UpdateSettings/Version/Backup and nothing else. The Data section
//     therefore renders the vacuum control as present-but-unavailable
//     (settings.data.vacuum.unavailable) rather than wiring it to
//     nothing or omitting the section D4-10 explicitly asks for.
//     Whichever wave adds a SystemService.Vacuum RPC should also update
//     render_data.go's renderData to call it — proto/aff/v1 is outside
//     this change's allowed paths (create/edit ONLY web/pages/settings/),
//     so this package cannot add the RPC itself.
package settings
