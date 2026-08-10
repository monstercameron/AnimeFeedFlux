// Package history implements the /history admin surface (PLAN.md §12.4,
// TODOS.md D3, D6-12): two tabs over one page, Runs and Items.
//
// Layout of this package:
//
//   - Pure, host-testable logic carries no build tag: request builders for
//     RunService.History / ItemService.List (runs_filter.go, items_filter.go),
//     pagination boundaries (pagination.go), the no-backdating guard as a
//     pure predicate (backdating.go), revision diff computation (diff.go)
//     plus the client-side revision store that stands in for a missing RPC
//     (revisions.go — see the doc comment there), bulk-selection state
//     (selection.go), and the six-state screen matrix (screenstate.go).
//   - Browser-dependent rendering sits behind `//go:build js` (root.go,
//     runs_ui.go, items_ui.go, forms_ui.go): GWC v5 components, hooks, and
//     DOM-bound state. None of it is host-testable, which is why the logic
//     above is factored out instead of buried in component bodies.
//
// GWC v5 API actually used: `ui` (Render/CreateElement/UseState/UseReducer/
// UseEffect/UseEvent/UseContext, AccessibleOverlay for the modal surfaces,
// UseAnnouncer for live-region announcements on mutation), `html/shorthand`
// for markup, `css`/`css/u` for styling (no shared web/ui or web/tokens
// package exists yet — five other agents own those directories concurrently
// and none had landed anything under web/pages, web/route, web/ui, or
// web/tokens as of this change), and the vendored `i18n` package
// (github.com/monstercameron/GoWebComponents/v5/i18n) for locale-aware
// FormatDate/FormatNumber — never fmt.Sprintf for a user-visible date or
// number.
//
// Interfaces this page needs satisfied by other agents' work:
//
//   - Catalog (catalog.go) — the minimal i18n lookup this page needs
//     (T(key string, args map[string]any) string). web/i18n is owned by
//     another agent and is deliberately not imported; whatever that
//     package ends up exposing should be adapted to satisfy Catalog, or
//     this interface should be reconciled with its real shape once it
//     lands. Every user-visible string in this package is a call to
//     Catalog.T with a "history.*" key — there is no literal English text
//     in the js-build files.
//   - RunsClient / ItemsClient (client.go) — this page depends on the
//     generated affv1.RunServiceClient / affv1.ItemServiceClient
//     interfaces directly (gen/aff/v1 is generated code, not one of the
//     five owned directories), so whatever wires web/wsconn's *Conn to
//     this page just needs to hand over those two client values.
//
// §12.4 assumption that did NOT hold, reported per the task brief: the
// plan requires "Items: revision history with a diff view and revert"
// (D3-15), but proto/aff/v1/item.proto's ItemService has no RPC to list or
// revert item_revisions rows — Update's own comment says edits "are
// recorded in item_revisions with a diff view and revert (PLAN.md §12.4)"
// but no ListRevisions/RevertRevision RPC exists to read them back. This
// package therefore keeps its own client-side, in-session RevisionStore
// (revisions.go) that snapshots an item's editable fields immediately
// before each successful Update and computes the diff locally; "revert"
// re-submits that prior snapshot through the ordinary Update RPC. This is
// honest about what it can promise: revisions recorded before the current
// page session started are not recoverable this way. A real fix needs a
// server-side item_revisions read (and ideally revert) RPC; until then the
// UI says so explicitly rather than pretending session-local history is
// the durable one item_revisions already stores server-side.
package history
