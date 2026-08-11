// Package history implements the /history admin surface (PLAN.md §12.4,
// TODOS.md D3, D6-12): two tabs over one page, Runs and Items.
//
// Layout of this package:
//
//   - Pure, host-testable logic carries no build tag: request builders for
//     RunService.History / ItemService.List / ItemService.ListRevisions
//     (runs_filter.go, items_filter.go, revisions.go), pagination boundaries
//     (pagination.go), the no-backdating guard as a pure predicate
//     (backdating.go), revision diff computation (diff.go, revisions.go —
//     RevisionFieldDiffs turns an *affv1.ItemRevision's already-recorded
//     per-field Changes into line-level diffs), bulk-selection state
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
// D3-15 ("Items: revision history with a diff view and revert") is wired to
// the real RPCs: ItemService.ListRevisions backs the history list
// (items_ui.go's loadRevisions) and ItemService.RevertRevision backs revert
// (items_ui.go's revertRevision) — both go through props.Client directly,
// with expected_version always the currently-loaded item's version so a
// revert racing an edit surfaces as IsVersionConflict (mutationerror.go)
// rather than silently clobbering. This replaces an earlier client-side,
// in-session RevisionStore stopgap that stood in for these RPCs before they
// existed server-side; that stopgap is gone, along with the "revisions are
// visible for this session only" notice it required — real history is
// visible after a reload now, because it always was, server-side.
//
// What the RPCs still cannot give this UI: item_revisions never records a
// `tags` change (internal/rpc/item.go's itemDiff has no `tags` case, and
// RevertRevision's field-apply switch has no `tags` case either — see its
// doc comment), so an edit that only changes tags produces no revision row
// at all, and reverting to an older revision cannot restore a prior tags
// value even though the edit form lets an operator change tags. This is a
// server-side gap, not something this page can work around: the fix is
// adding a `tags` case to itemDiff/itemApplyRevisionField, which is outside
// this page's allowed paths.
package history
