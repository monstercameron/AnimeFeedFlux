//go:build js

package history

import (
	"context"
	"time"

	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/i18n"
	"github.com/monstercameron/GoWebComponents/v5/ui"
	"google.golang.org/protobuf/types/known/timestamppb"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// ItemsTabProps is what the Items tab needs from its parent.
type ItemsTabProps struct {
	Client       ItemsClient
	Feeds        FeedsClient
	T            Catalog
	Disconnected bool
	// Ready — see RootProps.Ready. Loading before the socket can carry the
	// call leaves this tab on "Loading…" with no error and no retry.
	Ready bool
}

type itemsLoadState struct {
	loading bool
	err     error
	items   []*affv1.Item
	cursor  *PageCursor
	// nextTk is the token for the page AFTER the one displayed, "" on the
	// last page. Same fix as runs_ui.go's runsLoadState — see the "load-ok"
	// case there for what loading-advances-the-cursor broke.
	nextTk  string
	editing int64 // 0 = none, -1 = creating new, >0 = editing that item id
	viewRev int64 // item id whose revision panel is open, 0 = none

	// revisions/revisionsCursor/revisionsLoading/revisionsErr hold
	// ItemService.ListRevisions' state for the currently open panel
	// (viewRev) only — at most one panel is open at a time (toggle-revisions
	// below), so this stays singular rather than a per-item map. Reloaded
	// from scratch every time a different item's panel opens.
	revisions        []*affv1.ItemRevision
	revisionsCursor  *PageCursor
	revisionsLoading bool
	revisionsErr     error
}

type itemsAction struct {
	kind      string
	err       error
	items     []*affv1.Item
	nextTk    string
	itemID    int64
	item      *affv1.Item
	revisions []*affv1.ItemRevision
}

func itemsReducer(s itemsLoadState, a itemsAction) itemsLoadState {
	switch a.kind {
	case "load-start":
		s.loading = true
		s.err = nil
	case "load-ok":
		s.loading = false
		s.items = a.items
		// Record, do not advance — see runs_ui.go's identical case.
		s.nextTk = a.nextTk
	case "load-err":
		s.loading = false
		s.err = a.err
	case "upsert":
		found := false
		next := make([]*affv1.Item, len(s.items))
		for i, it := range s.items {
			if it.Id == a.item.Id {
				next[i] = a.item
				found = true
			} else {
				next[i] = it
			}
		}
		if !found {
			next = append([]*affv1.Item{a.item}, next...)
		}
		s.items = next
		s.editing = 0
	case "start-create":
		s.editing = -1
	case "start-edit":
		s.editing = a.itemID
	case "cancel-edit":
		s.editing = 0
	case "toggle-revisions":
		if s.viewRev == a.itemID {
			s.viewRev = 0
		} else {
			// Opening a (possibly different) item's panel: drop whatever
			// was cached for the previously open one rather than showing
			// its stale revisions/error/pagination under the new item.
			s.viewRev = a.itemID
			s.revisions = nil
			s.revisionsCursor = NewPageCursor()
			s.revisionsLoading = false
			s.revisionsErr = nil
		}
	case "revisions-load-start":
		if a.itemID != s.viewRev {
			break // a stale request for a panel that is no longer open.
		}
		s.revisionsLoading = true
		s.revisionsErr = nil
	case "revisions-load-ok":
		if a.itemID != s.viewRev {
			break
		}
		s.revisionsLoading = false
		s.revisions = a.revisions
		s.revisionsCursor.Advance(a.nextTk)
	case "revisions-load-err":
		if a.itemID != s.viewRev {
			break
		}
		s.revisionsLoading = false
		s.revisionsErr = a.err
	}
	return s
}

// ItemsTab renders the Items tab (PLAN.md §12.4, TODOS.md D3-08..D3-19).
func ItemsTab(props ItemsTabProps) ui.Node {
	filter := ui.UseState(ItemFilter{DeletedFilter: affv1.DeletedFilter_DELETED_FILTER_EXCLUDE_DELETED})
	store := ui.UseReducer(itemsReducer, itemsLoadState{cursor: NewPageCursor(), revisionsCursor: NewPageCursor()})
	selection := ui.UseRef(NewSelection())
	// forceRerender lets imperative selection mutations (which do not go
	// through the reducer) still trigger a repaint of the checkboxes.
	forceRerender := ui.UseState(0)
	// saveErr/rowActionErr are TODOS.md D0-10's mutation errors: saveErr for
	// the open create/edit form, rowActionErr for the per-row/bulk
	// delete/restore/correction actions. revertErr is kept separate from
	// rowActionErr rather than sharing its slot: a revert conflict
	// (IsVersionConflict) needs its own "reload and retry" affordance next
	// to the revision panel, not the generic row-action error banner, and
	// giving it a distinct slot means a stale bulk-delete error can never be
	// shown next to (or hide) a revert's conflict, or vice versa.
	saveErr := ui.UseState(error(nil))
	rowActionErr := ui.UseState(error(nil))
	revertErr := ui.UseState(error(nil))
	revertPending := ui.UseState(int64(0)) // revision id currently being reverted, 0 = none
	// See asyncdispatch.go: a reducer dispatch from a goroutine updates the
	// state and schedules no render, which is why this tab loaded its items
	// and kept showing the spinner until some unrelated click repainted it.
	pump := useRenderPump()

	load := func(pageToken string) {
		store.Dispatch(itemsAction{kind: "load-start"})
		pump.bump()
		req := BuildItemListRequest(filter.Get(), pageToken, DefaultPageSize)
		retryRead(
			func() (*affv1.ItemServiceListResponse, error) {
				return props.Client.List(context.Background(), req)
			},
			func(resp *affv1.ItemServiceListResponse, err error) {
				if err != nil {
					store.Dispatch(itemsAction{kind: "load-err", err: err})
					pump.bump()
					return
				}
				store.Dispatch(itemsAction{kind: "load-ok", items: resp.Items, nextTk: resp.NextPageToken})
				pump.bump()
			},
		)
	}

	// The feed list, needed by the create form: an item belongs to a feed,
	// and the form had no way to say which.
	feeds := ui.UseState([]*affv1.Feed(nil))
	ui.UseEffect(func() func() {
		if !props.Ready || props.Feeds == nil {
			return nil
		}
		go func() {
			resp, err := props.Feeds.List(context.Background(), &affv1.FeedServiceListRequest{PageSize: 200})
			if err != nil {
				return
			}
			feeds.Set(resp.GetFeeds())
		}()
		return nil
	}, props.Ready)

	// Keyed on Ready as well as the filter, so the first load happens when
	// the socket can actually carry it — and re-fires if the page was opened
	// before the session came up. See RootProps.Ready.
	ui.UseEffect(func() func() {
		if !props.Ready {
			return nil
		}
		store.Get().cursor.Reset()
		selection.Get().Clear()
		load("")
		return nil
	}, filter.Get(), props.Ready)

	// The search box is debounced: it used to set the filter on every
	// keystroke, and the filter is the load effect's dependency, so typing
	// "trivia" ran six full-text queries and threw five of the results away.
	// The committed value is what the effect keys on; the typed value is what
	// the input shows.
	queryDraft := ui.UseState("")
	querySeq := ui.UseRef(0)
	handleQuery := func(ev ui.InputEvent) {
		v := ev.GetValue()
		queryDraft.Set(v)
		querySeq.Set(querySeq.Get() + 1)
		mySeq := querySeq.Get()
		go func() {
			time.Sleep(searchDebounce)
			// A later keystroke supersedes this one.
			if querySeq.Get() != mySeq {
				return
			}
			f := filter.Get()
			if f.Query == v {
				return
			}
			f.Query = v
			filter.Set(f)
		}()
	}

	handleDeletedFilter := func(ev ui.InputEvent) {
		f := filter.Get()
		f.DeletedFilter = deletedFilterFromValue(ev.GetValue())
		filter.Set(f)
	}

	s := store.Get()

	newestPublished := newestPublishedAt(s.items)

	saveItem := func(req *affv1.Item, expectedVersion int64, isCreate bool, setErr func(error)) {
		setErr(nil)
		go func() {
			var updated *affv1.Item
			var err error
			if isCreate {
				var resp *affv1.ItemServiceCreateResponse
				resp, err = props.Client.Create(context.Background(), &affv1.ItemServiceCreateRequest{Item: req})
				if resp != nil {
					updated = resp.Item
				}
			} else {
				var resp *affv1.ItemServiceUpdateResponse
				resp, err = props.Client.Update(context.Background(), &affv1.ItemServiceUpdateRequest{Item: req, ExpectedVersion: expectedVersion})
				if resp != nil {
					updated = resp.Item
				}
			}
			if err != nil {
				setErr(err)
				return
			}
			if updated == nil {
				return
			}
			store.Dispatch(itemsAction{kind: "upsert", item: updated})
			pump.bump()
		}()
	}

	deleteItem := func(it *affv1.Item) {
		rowActionErr.Set(nil)
		go func() {
			_, err := props.Client.Delete(context.Background(), &affv1.ItemServiceDeleteRequest{ItemId: it.Id, ExpectedVersion: it.Version})
			if err != nil {
				rowActionErr.Set(err)
				return
			}
			it2 := cloneItem(it)
			it2.DeletedAt = nowTimestamp()
			store.Dispatch(itemsAction{kind: "upsert", item: it2})
			pump.bump()
		}()
	}

	restoreItem := func(it *affv1.Item) {
		rowActionErr.Set(nil)
		go func() {
			resp, err := props.Client.Restore(context.Background(), &affv1.ItemServiceRestoreRequest{ItemId: it.Id, ExpectedVersion: it.Version})
			if err != nil {
				rowActionErr.Set(err)
				return
			}
			if resp.Item == nil {
				return
			}
			store.Dispatch(itemsAction{kind: "upsert", item: resp.Item})
			pump.bump()
		}()
	}

	publishCorrection := func(it *affv1.Item, title, summary, body string) {
		rowActionErr.Set(nil)
		go func() {
			resp, err := props.Client.PublishCorrection(context.Background(), &affv1.ItemServicePublishCorrectionRequest{
				CorrectsItemId: it.Id,
				Title:          title,
				SummaryText:    summary,
				BodyHtml:       body,
			})
			if err != nil {
				rowActionErr.Set(err)
				return
			}
			if resp.Item == nil {
				return
			}
			store.Dispatch(itemsAction{kind: "upsert", item: resp.Item})
			pump.bump()
		}()
	}

	// loadRevisions fetches one page of item_id's revision history via the
	// real ItemService.ListRevisions RPC (TODOS.md D3-15) — the session-local
	// RevisionStore stopgap this page used to keep instead (revisions.go's
	// former doc comment) is gone; a reload no longer loses history.
	loadRevisions := func(itemID int64, pageToken string) {
		store.Dispatch(itemsAction{kind: "revisions-load-start", itemID: itemID})
		pump.bump()
		go func() {
			resp, err := props.Client.ListRevisions(context.Background(), BuildListRevisionsRequest(itemID, pageToken, DefaultPageSize))
			if err != nil {
				store.Dispatch(itemsAction{kind: "revisions-load-err", itemID: itemID, err: err})
				pump.bump()
				return
			}
			store.Dispatch(itemsAction{kind: "revisions-load-ok", itemID: itemID, revisions: resp.Revisions, nextTk: resp.NextPageToken})
			pump.bump()
		}()
	}

	toggleRevisions := func(it *affv1.Item) {
		opening := s.viewRev != it.Id
		store.Dispatch(itemsAction{kind: "toggle-revisions", itemID: it.Id})
		pump.bump()
		if opening {
			revertErr.Set(nil)
			loadRevisions(it.Id, "")
		}
	}

	// revertRevision calls ItemService.RevertRevision directly (TODOS.md
	// D3-15): PLAN.md §12.4 defines revert as "an ordinary edit, not a
	// rewind" — the server reconstructs the pre-revision state and commits
	// it itself, so the UI no longer needs to rebuild an *affv1.Item by hand
	// from a locally-cached snapshot the way the old RevisionStore-backed
	// revert did. expected_version is always the currently-loaded item's
	// version (PLAN.md §12.4: "a revert racing an edit is exactly when a
	// silent overwrite loses work"), and a mismatch surfaces as
	// IsVersionConflict below rather than retrying or clobbering silently.
	revertRevision := func(it *affv1.Item, revisionID int64) {
		revertErr.Set(nil)
		revertPending.Set(revisionID)
		go func() {
			resp, err := props.Client.RevertRevision(context.Background(), &affv1.ItemServiceRevertRevisionRequest{
				ItemId:          it.Id,
				RevisionId:      revisionID,
				ExpectedVersion: it.Version,
			})
			revertPending.Set(0)
			if err != nil {
				revertErr.Set(err)
				return
			}
			if resp.Item != nil {
				store.Dispatch(itemsAction{kind: "upsert", item: resp.Item})
				pump.bump()
			}
			// The revert itself just wrote a brand-new revision (it never
			// deletes or rewrites the one just reverted — internal/rpc/
			// item.go's RevertRevision doc comment), so refresh the open
			// panel rather than leaving the now-stale previously loaded
			// page displayed.
			loadRevisions(it.Id, "")
		}()
	}

	toggleSelect := func(id int64) {
		selection.Get().Toggle(id)
		forceRerender.Set(forceRerender.Get() + 1)
	}

	visibleIDs := make([]int64, len(s.items))
	for i, it := range s.items {
		visibleIDs[i] = it.Id
	}

	toggleSelectAll := func() {
		state := StateForVisible(selection.Get(), visibleIDs)
		SetAllVisible(selection.Get(), visibleIDs, state != SelectAllAll, false)
		forceRerender.Set(forceRerender.Get() + 1)
	}

	bulkDelete := func() {
		for _, id := range selection.Get().IDs() {
			for _, it := range s.items {
				if it.Id == id && it.DeletedAt == nil {
					deleteItem(it)
				}
			}
		}
		selection.Get().Clear()
		forceRerender.Set(forceRerender.Get() + 1)
	}

	bulkRestore := func() {
		for _, id := range selection.Get().IDs() {
			for _, it := range s.items {
				if it.Id == id && it.DeletedAt != nil {
					restoreItem(it)
				}
			}
		}
		selection.Get().Clear()
		forceRerender.Set(forceRerender.Get() + 1)
	}

	renderRevPanel := func(it *affv1.Item) ui.Node {
		return revisionPanel(revisionPanelProps{
			T:             props.T,
			State:         s,
			Item:          it,
			RevertPending: revertPending.Get(),
			RevertErr:     revertErr.Get(),
			OnRevert:      revertRevision,
			OnPrev: func() {
				s.revisionsCursor.Back()
				loadRevisions(it.Id, s.revisionsCursor.Current())
			},
			OnRefresh: func() { loadRevisions(it.Id, s.revisionsCursor.Current()) },
			OnReloadItems: func() {
				revertErr.Set(nil)
				load(s.cursor.Current())
			},
		})
	}

	screen := ComputeScreenState(ScreenInputs{
		Disconnected: props.Disconnected,
		Loading:      s.loading,
		Err:          s.err,
		ItemCount:    len(s.items),
	})

	return h.Section(
		h.ClassStr("history-items"),
		h.Div(
			h.ClassStr("history-filters"),
			h.Label(h.For("history-items-query"), props.T.T("history.items.filter_query", nil)),
			h.Input(h.ID("history-items-query"), h.Type("search"),
				h.Attr("placeholder", props.T.T("history.items.filter_query_placeholder", nil)),
				h.Value(queryDraft.Get()), h.OnInput(handleQuery)),
			h.Label(h.For("history-items-deleted"), props.T.T("history.items.filter_deleted", nil)),
			h.Select(h.ID("history-items-deleted"), h.OnChange(handleDeletedFilter),
				h.Option(h.Value("exclude"), props.T.T("history.items.deleted.exclude", nil)),
				h.Option(h.Value("only"), props.T.T("history.items.deleted.only", nil)),
				h.Option(h.Value("all"), props.T.T("history.items.deleted.all", nil)),
			),
			h.Button(h.Type("button"), h.OnClick(func() { saveErr.Set(nil); store.Dispatch(itemsAction{kind: "start-create"}) }), props.T.T("history.items.create", nil)),
		),
		h.If(s.editing != 0, ui.CreateElement(ItemForm, ItemFormProps{
			T:               props.T,
			Item:            findItem(s.items, s.editing),
			Feeds:           feeds.Get(),
			IsCreate:        s.editing == -1,
			NewestPublished: newestPublished,
			Err:             saveErr.Get(),
			OnCancel:        func() { saveErr.Set(nil); store.Dispatch(itemsAction{kind: "cancel-edit"}) },
			OnSave: func(req *affv1.Item, expectedVersion int64, isCreate bool) {
				saveItem(req, expectedVersion, isCreate, saveErr.Set)
			},
		})),
		h.If(rowActionErr.Get() != nil, h.Div(
			h.ClassStr("history-action-error"),
			mutationErrorText(props.T, rowActionErr.Get()),
		)),
		h.If(selection.Get().Count() > 0, h.Div(h.ClassStr("history-bulkbar"),
			h.Textf(props.T.T("history.items.selected_count", nil), selection.Get().Count()),
			h.Button(h.Type("button"), h.OnClick(bulkDelete), props.T.T("history.items.bulk_delete", nil)),
			h.Button(h.Type("button"), h.OnClick(bulkRestore), props.T.T("history.items.bulk_restore", nil)),
		)),
		renderScreenState(props.T, screen, func() ui.Node {
			return itemsTable(props.T, s, selection.Get(), toggleSelect, toggleSelectAll, visibleIDs,
				func(it *affv1.Item) { saveErr.Set(nil); store.Dispatch(itemsAction{kind: "start-edit", itemID: it.Id}) },
				deleteItem, restoreItem, publishCorrection,
				toggleRevisions, s.viewRev, renderRevPanel,
			)
		}),
		h.Div(
			h.ClassStr("history-pager"),
			h.Button(h.Type("button"), h.Disabled(!s.cursor.HasPrevious()), h.OnClick(func() {
				s.cursor.Back()
				load(s.cursor.Current())
			}), props.T.T("history.pager.previous", nil)),
			h.Button(h.Type("button"), h.Disabled(s.nextTk == ""), h.OnClick(func() {
				s.cursor.Advance(s.nextTk)
				load(s.cursor.Current())
			}), props.T.T("history.pager.next", nil)),
			h.Button(h.Type("button"), h.OnClick(func() { load(s.cursor.Current()) }), props.T.T("history.pager.refresh", nil)),
		),
	)
}

func itemsTable(t Catalog, s itemsLoadState, sel *Selection,
	toggleSelect func(int64), toggleSelectAll func(), visibleIDs []int64,
	onEdit func(*affv1.Item), onDelete func(*affv1.Item), onRestore func(*affv1.Item),
	onCorrection func(*affv1.Item, string, string, string), onToggleRev func(*affv1.Item), viewRevID int64,
	renderRevPanel func(*affv1.Item) ui.Node) ui.Node {

	allState := StateForVisible(sel, visibleIDs)

	return h.Table(
		h.ClassStr("history-table"),
		h.Thead(h.Tr(
			h.Th(h.Input(h.Type("checkbox"), h.Checked(allState == SelectAllAll), h.OnChange(func() { toggleSelectAll() }))),
			h.Th(t.T("history.items.col_title", nil)),
			h.Th(t.T("history.items.col_origin", nil)),
			h.Th(t.T("history.items.col_published", nil)),
			h.Th(t.T("history.items.col_status", nil)),
			h.Th(""),
		)),
		h.Tbody(
			// MapKeyedComponent (not MapKeyed): itemRow's correction panel
			// owns local UseState (open/title/summary/body), and hooks
			// must sit at a stable per-row position — MapKeyedComponent
			// gives each row its own fiber so that's legal (05 HTML
			// Authoring's "Interactive lists" section); plain MapKeyed
			// would call those hooks at a variable position inside this
			// component's own render body instead.
			h.MapKeyedComponent(s.items, func(it *affv1.Item) any { return it.Id }, func(it *affv1.Item) ui.Node {
				return itemRow(t, it, sel.IsSelected(it.Id), toggleSelect, onEdit, onDelete, onRestore, onCorrection, onToggleRev, viewRevID == it.Id, renderRevPanel)
			}),
		),
	)
}

func itemRow(t Catalog, it *affv1.Item, selected bool, toggleSelect func(int64),
	onEdit func(*affv1.Item), onDelete func(*affv1.Item), onRestore func(*affv1.Item),
	onCorrection func(*affv1.Item, string, string, string), onToggleRev func(*affv1.Item),
	showRev bool, renderRevPanel func(*affv1.Item) ui.Node) ui.Node {

	deleted := it.DeletedAt != nil
	statusKey := "history.items.status.published"
	if deleted {
		statusKey = "history.items.status.deleted"
	}

	return h.Fragment(
		h.Tr(
			h.Td(h.Input(h.Type("checkbox"), h.Checked(selected), h.OnChange(func() { toggleSelect(it.Id) }))),
			h.Td(it.Title),
			h.Td(originLabel(t, it.Origin)),
			h.Td(formatTimestamp(it.PublishedAt)),
			h.Td(t.T(statusKey, nil)),
			h.Td(
				h.Div(h.ClassStr("history-kebab"),
					kebabTrigger(t),
					h.Div(h.ClassStr("history-kebab-menu"),
						h.Unless(deleted, h.Button(h.Type("button"), h.OnClick(func() { onEdit(it) }), t.T("history.items.edit", nil))),
						h.Unless(deleted, correctionButton(t, it, onCorrection)),
						h.Button(h.Type("button"), h.OnClick(func() { onToggleRev(it) }), t.T("history.items.revisions", nil)),
						h.Unless(deleted, h.Button(h.Type("button"), h.ClassStr("history-kebab-danger"), h.OnClick(func() { onDelete(it) }), t.T("history.items.delete", nil))),
						h.If(deleted, h.Button(h.Type("button"), h.OnClick(func() { onRestore(it) }), t.T("history.items.restore", nil))),
					),
				),
			),
		),
		// String, and its own class — same defect and same fix as runs_ui.go's
		// expanded row; see that call site's comment.
		h.If(showRev, h.Tr(h.Td(h.ClassStr("history-run-log-cell"), h.Attr("colspan", "6"), renderRevPanel(it)))),
	)
}

func correctionButton(t Catalog, it *affv1.Item, onCorrection func(*affv1.Item, string, string, string)) ui.Node {
	// PLAN.md §12.4: "Publish a correction sits next to Delete, not three
	// menus away." It lives in the same kebab as delete (one menu, not
	// three), immediately above it, and the button text plus the panel
	// it opens both state that RSS has no retraction.
	open := ui.UseState(false)
	title := ui.UseState("Correction: " + it.Title)
	summary := ui.UseState(it.SummaryText)
	body := ui.UseState(it.BodyHtml)

	if !open.Get() {
		return h.Button(h.Type("button"), h.OnClick(func() { open.Set(true) }), t.T("history.items.publish_correction", nil))
	}

	return h.Div(h.ClassStr("history-correction-panel"),
		h.P(t.T("history.items.no_retraction_notice", nil)),
		h.Label(t.T("history.items.correction_title", nil)),
		h.Input(h.Value(title.Get()), h.OnInput(func(ev ui.InputEvent) { title.Set(ev.GetValue()) })),
		h.Label(t.T("history.items.correction_summary", nil)),
		h.Textarea(h.OnInput(func(ev ui.InputEvent) { summary.Set(ev.GetValue()) }), h.Text(summary.Get())),
		h.Label(t.T("history.items.correction_body", nil)),
		h.Textarea(h.OnInput(func(ev ui.InputEvent) { body.Set(ev.GetValue()) }), h.Text(body.Get())),
		h.Button(h.Type("button"), h.OnClick(func() {
			onCorrection(it, title.Get(), summary.Get(), body.Get())
			open.Set(false)
		}), t.T("history.items.publish_correction_confirm", nil)),
		h.Button(h.Type("button"), h.OnClick(func() { open.Set(false) }), t.T("history.cancel", nil)),
	)
}

// revisionPanelProps bundles what revisionPanel needs to render one item's
// revision history (PLAN.md §12.4: "revision history with a diff view and
// revert"). All of its data (State) lives in ItemsTab's own reducer state,
// fetched by ItemsTab's loadRevisions/revertRevision — revisionPanel itself
// is a pure render function with no hooks of its own. It has to be: it is
// called directly (not through ui.CreateElement) from itemRow for every
// row on every render, whether or not that row's panel is open (see
// itemRow's h.If comment — Go evaluates renderRevPanel(it) as a plain
// argument regardless of the h.If condition), so giving it hooks would
// break the moment two rows disagreed on whether their panel was open.
type revisionPanelProps struct {
	T     Catalog
	State itemsLoadState
	Item  *affv1.Item

	RevertPending int64 // non-zero = that revision id's RevertRevision call is in flight
	RevertErr     error
	OnRevert      func(*affv1.Item, int64)
	OnPrev        func()
	OnRefresh     func()
	// OnReloadItems re-fetches the Items list's current page after a
	// version conflict (IsVersionConflict), so the operator sees the
	// current version before deciding whether to retry the revert —
	// PLAN.md §12.4: "a conflict shown as a real choice rather than a
	// silent clobber."
	OnReloadItems func()
}

// revisionPanel renders one item's revision history: newest first
// (ItemService.ListRevisions' contract), each entry's per-field diff
// computed straight from its already-recorded Changes (RevisionFieldDiffs),
// and Revert next to the reminder that the guid never changes (PLAN.md
// §5.1) — repeated here, not only in the edit form, because this is where
// an operator actually doubts it: immediately before clicking Revert.
func revisionPanel(p revisionPanelProps) ui.Node {
	t := p.T
	s := p.State
	it := p.Item

	return h.Div(h.ClassStr("history-revisions"),
		h.P(h.ClassStr("history-guid-notice"), t.T("history.items.guid_never_changes", nil)),
		// PLAN.md §12.4: revert "is an ordinary edit, not a rewind" — it
		// creates a new revision recording the revert; it never deletes or
		// rewrites history. Stated here because "revert" otherwise reads
		// like undo, and an operator would reasonably assume the
		// intermediate state between now and the reverted-to revision is
		// gone.
		h.P(t.T("history.items.revert_notice", nil)),
		h.If(s.revisionsLoading, h.P(h.ClassStr("history-state history-state--loading"), t.T("history.state.loading", nil))),
		h.If(s.revisionsErr != nil, h.Div(h.ClassStr("history-action-error"), mutationErrorText(t, s.revisionsErr))),
		h.If(p.RevertErr != nil, h.Div(h.ClassStr("history-revert-error"),
			h.IfElse(IsVersionConflict(p.RevertErr),
				h.Fragment(
					h.P(t.T("history.items.revert_conflict", nil)),
					h.Button(h.Type("button"), h.OnClick(p.OnReloadItems), t.T("history.items.revert_conflict_reload", nil)),
				),
				h.P(mutationErrorText(t, p.RevertErr)),
			),
		)),
		h.IfElse(!s.revisionsLoading && len(s.revisions) == 0 && s.revisionsErr == nil,
			h.P(t.T("history.items.no_revisions", nil)),
			h.Ul(h.MapKeyed(s.revisions, func(r *affv1.ItemRevision) any { return r.GetId() }, func(r *affv1.ItemRevision) ui.Node {
				revID := r.GetId()
				return h.Li(
					h.Span(formatTimestamp(r.GetAt())),
					h.Ul(h.MapKeyed(RevisionFieldDiffs(r), func(d FieldDiff) any { return d.Field }, func(d FieldDiff) ui.Node {
						return h.Li(h.Strong(d.Field), diffLinesNode(d.Lines))
					})),
					h.Button(h.Type("button"), h.Disabled(p.RevertPending != 0), h.OnClick(func() { p.OnRevert(it, revID) }), t.T("history.items.revert", nil)),
				)
			})),
		),
		h.Div(h.ClassStr("history-revisions-pager"),
			h.Button(h.Type("button"), h.Disabled(!s.revisionsCursor.HasPrevious()), h.OnClick(p.OnPrev), t.T("history.pager.previous", nil)),
			h.Button(h.Type("button"), h.OnClick(p.OnRefresh), t.T("history.pager.refresh", nil)),
		),
	)
}

func diffLinesNode(lines []DiffLine) ui.Node {
	nodes := make([]ui.Node, 0, len(lines))
	for _, l := range lines {
		cls := "history-diff-line history-diff-equal"
		if l.Op == DiffDelete {
			cls = "history-diff-line history-diff-delete"
		} else if l.Op == DiffInsert {
			cls = "history-diff-line history-diff-insert"
		}
		nodes = append(nodes, h.Div(h.ClassStr(cls), l.Text))
	}
	return h.Div(h.ClassStr("history-diff"), nodes)
}

func originLabel(t Catalog, o affv1.Origin) string {
	switch o {
	case affv1.Origin_ORIGIN_GENERATED:
		return t.T("history.items.origin.generated", nil)
	case affv1.Origin_ORIGIN_SAMPLED:
		return t.T("history.items.origin.sampled", nil)
	case affv1.Origin_ORIGIN_MANUAL:
		return t.T("history.items.origin.manual", nil)
	case affv1.Origin_ORIGIN_CORRECTION:
		return t.T("history.items.origin.correction", nil)
	default:
		return ""
	}
}

func deletedFilterFromValue(v string) affv1.DeletedFilter {
	switch v {
	case "only":
		return affv1.DeletedFilter_DELETED_FILTER_ONLY_DELETED
	case "all":
		return affv1.DeletedFilter_DELETED_FILTER_INCLUDE_ALL
	default:
		return affv1.DeletedFilter_DELETED_FILTER_EXCLUDE_DELETED
	}
}

func findItem(items []*affv1.Item, id int64) *affv1.Item {
	if id <= 0 {
		return nil
	}
	for _, it := range items {
		if it.Id == id {
			return it
		}
	}
	return nil
}

func newestPublishedAt(items []*affv1.Item) time.Time {
	var newest time.Time
	for _, it := range items {
		if it.PublishedAt == nil {
			continue
		}
		ts := it.PublishedAt.AsTime()
		if ts.After(newest) {
			newest = ts
		}
	}
	return newest
}

func snapshotOf(it *affv1.Item) ItemSnapshot {
	snap := ItemSnapshot{
		Title:       it.Title,
		SummaryText: it.SummaryText,
		BodyHTML:    it.BodyHtml,
		Link:        it.Link,
		Tags:        it.Tags,
	}
	if it.PublishedAt != nil {
		snap.PublishedAt = it.PublishedAt.AsTime()
	}
	return snap
}

// cloneItem returns a shallow copy of the fields this page ever writes
// back through an RPC request (the mutation helpers above only ever
// re-set Title/SummaryText/BodyHtml/Link/Tags/PublishedAt/DeletedAt), not
// a struct-literal `*it` copy — Item embeds a protoimpl.MessageState with
// an internal sync.Mutex, so copying the whole struct by value copies a
// live lock (go vet's "assignment copies lock value" flags exactly this).
func cloneItem(it *affv1.Item) *affv1.Item {
	return &affv1.Item{
		Id:          it.Id,
		FeedId:      it.FeedId,
		ItemKey:     it.ItemKey,
		Title:       it.Title,
		SummaryText: it.SummaryText,
		BodyHtml:    it.BodyHtml,
		AnswerHtml:  it.AnswerHtml,
		Link:        it.Link,
		SourceName:  it.SourceName,
		Tags:        it.Tags,
		Origin:      it.Origin,
		PublishedAt: it.PublishedAt,
		CreatedAt:   it.CreatedAt,
		UpdatedAt:   it.UpdatedAt,
		EditedAt:    it.EditedAt,
		DeletedAt:   it.DeletedAt,
		Version:     it.Version,
	}
}

func formatTimestamp(ts *timestamppb.Timestamp) string {
	if ts == nil {
		return ""
	}
	return i18n.FormatDate("en", ts.AsTime(), i18n.DateOptions{Style: i18n.DateStyleMedium})
}

// nowTimestamp/timestampOrNil are small bridges between time.Time and the
// wire *timestamppb.Timestamp type used by the optimistic local updates
// above (soft-delete's DeletedAt, and the form's PublishedAt on save).
func nowTimestamp() *timestamppb.Timestamp {
	return timestamppb.New(time.Now())
}

func timestampOrNil(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}
