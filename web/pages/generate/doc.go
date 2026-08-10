// Package generatepage implements the admin `/generate` surface (PLAN.md
// §12.3, TODOS.md D2-01..30 and D6-11): the three-pane rail/editor/sampler
// working screen where a feed's recipe is authored, sampled against the
// live provider, and promoted — never published implicitly.
//
// # File layout and why it is split this way
//
// This package is split into a host-testable half and a browser-only half,
// because TODOS.md's "Tests" instruction asks for host-testable pure logic
// (validation-error-to-field mapping, jitter display, cost estimation, the
// six-state selection, conflict-resolution choices, candidate view
// switching) with the browser-dependent rendering behind `//go:build js`:
//
//   - No build tag (host-testable, `go test ./web/...` runs these):
//     i18n.go (the Translator/Formatters lookup interfaces this package
//     needs, and a fallback implementation), types.go (view-model types
//     that do not require GWC), logic.go (every pure computation above),
//     logic_test.go.
//   - `//go:build js && wasm` (browser-only, NOT covered by `go test`):
//     deps.go (wiring seam — see below), render.go, render_rail.go,
//     render_editor.go, render_sampler.go (the actual GWC component tree).
//
// This mirrors web/shell's own split (appstate/guard/backoff pure,
// shell/wsconn js-only) rather than inventing a new convention.
//
// # The wiring seam this package could NOT close, and why
//
// PLAN.md §12.3 assumes the editor and sampler simply call FeedService,
// SampleService, RunService, SystemService, and ItemService RPCs. In this
// repository as it stands, web/wsconn.Conn (the one control-plane
// connection web/shell.Mount dials) exposes only Conn.Auth
// (affv1.AuthServiceClient) — there is no exported way for a page package
// to reach the underlying *grpc.ClientConn or to obtain the other five
// typed clients this page needs. Both web/wsconn and web/shell are off
// limits to this change (four other agents own them concurrently), so this
// package cannot add the missing `Conn.Feed`/`Conn.Sample`/... fields
// itself.
//
// The seam this package defines instead: Deps (deps.go) carries the five
// typed clients (affv1.FeedServiceClient, SampleServiceClient,
// RunServiceClient, SystemServiceClient, ItemServiceClient) plus the
// Translator/Formatters this page needs, and Init(Deps) installs them in a
// package-level variable — the same "js-only package var set once at boot,
// read everywhere" shape web/shell/app.go already uses for its own `conn`.
// generatepage self-registers "/generate" with shell.RegisterPage from an
// init() func, so simply blank-importing this package is enough to make
// the route exist; but until whatever wave adds the missing Conn fields
// also calls generatepage.Init(...) from web/main.go (or wherever the
// Conn is constructed), Render shows the six-state matrix's own
// "disconnected"/"error" treatment instead of a blank page or a panic —
// see render.go's renderNotWired. This is reported explicitly rather than
// silently worked around, per this task's instructions.
//
// # i18n
//
// TODOS.md D2/D6-11 requires zero user-visible string literals, but this
// package must not import web/i18n (owned by another concurrent agent).
// i18n.go defines the minimal Translator interface this page needs — one
// method, `T(key string, args ...any) string`, keys namespaced
// `generate.*` — plus a Formatters interface for locale-aware date/time/
// currency formatting (PLAN.md §12.6: "never fmt.Sprintf"). A
// fallbackTranslator/fallbackFormatters pair (English, RFC3339-ish/USD)
// ships so this package compiles, renders, and tests standalone; Deps.I18n
// / Deps.Formatters let the real wave-owned implementations be substituted
// without this package importing them.
package generatepage
