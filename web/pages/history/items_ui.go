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
	T            Catalog
	Disconnected bool
}

type itemsLoadState struct {
	loading bool
	err     error
	items   []*affv1.Item
	cursor  *PageCursor
	editing int64 // 0 = none, -1 = creating new, >0 = editing that item id
	viewRev int64 // item id whose revision panel is open, 0 = none
}

type itemsAction struct {
	kind   string
	err    error
	items  []*affv1.Item
	nextTk string
	itemID int64
	item   *affv1.Item
}

func itemsReducer(s itemsLoadState, a itemsAction) itemsLoadState {
	switch a.kind {
	case "load-start":
		s.loading = true
		s.err = nil
	case "load-ok":
		s.loading = false
		s.items = a.items
		s.cursor.Advance(a.nextTk)
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
			s.viewRev = a.itemID
		}
	}
	return s
}

// ItemsTab renders the Items tab (PLAN.md §12.4, TODOS.md D3-08..D3-19).
func ItemsTab(props ItemsTabProps) ui.Node {
	filter := ui.UseState(ItemFilter{DeletedFilter: affv1.DeletedFilter_DELETED_FILTER_EXCLUDE_DELETED})
	store := ui.UseReducer(itemsReducer, itemsLoadState{cursor: NewPageCursor()})
	selection := ui.UseRef(NewSelection())
	revisions := ui.UseRef(NewRevisionStore())
	// forceRerender lets imperative selection mutations (which do not go
	// through the reducer) still trigger a repaint of the checkboxes.
	forceRerender := ui.UseState(0)

	load := func(pageToken string) {
		store.Dispatch(itemsAction{kind: "load-start"})
		go func() {
			req := BuildItemListRequest(filter.Get(), pageToken, DefaultPageSize)
			resp, err := props.Client.List(context.Background(), req)
			if err != nil {
				store.Dispatch(itemsAction{kind: "load-err", err: err})
				return
			}
			store.Dispatch(itemsAction{kind: "load-ok", items: resp.Items, nextTk: resp.NextPageToken})
		}()
	}

	ui.UseEffect(func() func() {
		store.Get().cursor.Reset()
		selection.Get().Clear()
		load("")
		return nil
	}, filter.Get())

	handleQuery := func(ev ui.InputEvent) {
		f := filter.Get()
		f.Query = ev.GetValue()
		filter.Set(f)
	}

	handleDeletedFilter := func(ev ui.InputEvent) {
		f := filter.Get()
		f.DeletedFilter = deletedFilterFromValue(ev.GetValue())
		filter.Set(f)
	}

	s := store.Get()

	newestPublished := newestPublishedAt(s.items)

	saveItem := func(before ItemSnapshot, req *affv1.Item, expectedVersion int64, isCreate bool) {
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
					revisions.Get().Record(updated.Id, before, snapshotOf(updated), time.Now())
				}
			}
			if err != nil || updated == nil {
				return
			}
			store.Dispatch(itemsAction{kind: "upsert", item: updated})
		}()
	}

	deleteItem := func(it *affv1.Item) {
		go func() {
			_, err := props.Client.Delete(context.Background(), &affv1.ItemServiceDeleteRequest{ItemId: it.Id, ExpectedVersion: it.Version})
			if err != nil {
				return
			}
			it2 := cloneItem(it)
			it2.DeletedAt = nowTimestamp()
			store.Dispatch(itemsAction{kind: "upsert", item: it2})
		}()
	}

	restoreItem := func(it *affv1.Item) {
		go func() {
			resp, err := props.Client.Restore(context.Background(), &affv1.ItemServiceRestoreRequest{ItemId: it.Id, ExpectedVersion: it.Version})
			if err != nil || resp.Item == nil {
				return
			}
			store.Dispatch(itemsAction{kind: "upsert", item: resp.Item})
		}()
	}

	publishCorrection := func(it *affv1.Item, title, summary, body string) {
		go func() {
			resp, err := props.Client.PublishCorrection(context.Background(), &affv1.ItemServicePublishCorrectionRequest{
				CorrectsItemId: it.Id,
				Title:          title,
				SummaryText:    summary,
				BodyHtml:       body,
			})
			if err != nil || resp.Item == nil {
				return
			}
			store.Dispatch(itemsAction{kind: "upsert", item: resp.Item})
		}()
	}

	revertRevision := func(it *affv1.Item, revIndex int) {
		target, ok := RevertTarget(revisions.Get(), it.Id, revIndex)
		if !ok {
			return
		}
		req := cloneItem(it)
		req.Title = target.Title
		req.SummaryText = target.SummaryText
		req.BodyHtml = target.BodyHTML
		req.Link = target.Link
		req.Tags = target.Tags
		req.PublishedAt = timestampOrNil(target.PublishedAt)
		saveItem(snapshotOf(it), req, it.Version, false)
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
			h.Input(h.ID("history-items-query"), h.Type("search"), h.Value(filter.Get().Query), h.OnInput(handleQuery)),
			h.Label(h.For("history-items-deleted"), props.T.T("history.items.filter_deleted", nil)),
			h.Select(h.ID("history-items-deleted"), h.OnChange(handleDeletedFilter),
				h.Option(h.Value("exclude"), props.T.T("history.items.deleted.exclude", nil)),
				h.Option(h.Value("only"), props.T.T("history.items.deleted.only", nil)),
				h.Option(h.Value("all"), props.T.T("history.items.deleted.all", nil)),
			),
			h.Button(h.Type("button"), h.OnClick(func() { store.Dispatch(itemsAction{kind: "start-create"}) }), props.T.T("history.items.create", nil)),
		),
		h.If(s.editing != 0, ui.CreateElement(ItemForm, ItemFormProps{
			T:               props.T,
			Item:            findItem(s.items, s.editing),
			IsCreate:        s.editing == -1,
			NewestPublished: newestPublished,
			OnCancel:        func() { store.Dispatch(itemsAction{kind: "cancel-edit"}) },
			OnSave:          saveItem,
		})),
		h.If(selection.Get().Count() > 0, h.Div(h.ClassStr("history-bulkbar"),
			h.Textf(props.T.T("history.items.selected_count", nil), selection.Get().Count()),
			h.Button(h.Type("button"), h.OnClick(bulkDelete), props.T.T("history.items.bulk_delete", nil)),
			h.Button(h.Type("button"), h.OnClick(bulkRestore), props.T.T("history.items.bulk_restore", nil)),
		)),
		renderScreenState(props.T, screen, func() ui.Node {
			return itemsTable(props.T, s, selection.Get(), revisions.Get(), toggleSelect, toggleSelectAll, visibleIDs,
				func(it *affv1.Item) { store.Dispatch(itemsAction{kind: "start-edit", itemID: it.Id}) },
				deleteItem, restoreItem, publishCorrection,
				func(it *affv1.Item) { store.Dispatch(itemsAction{kind: "toggle-revisions", itemID: it.Id}) },
				s.viewRev, revertRevision,
			)
		}),
		h.Div(
			h.ClassStr("history-pager"),
			h.Button(h.Type("button"), h.Disabled(!s.cursor.HasPrevious()), h.OnClick(func() {
				s.cursor.Back()
				load(s.cursor.Current())
			}), props.T.T("history.pager.previous", nil)),
			h.Button(h.Type("button"), h.OnClick(func() { load(s.cursor.Current()) }), props.T.T("history.pager.refresh", nil)),
		),
	)
}

func itemsTable(t Catalog, s itemsLoadState, sel *Selection, revs *RevisionStore,
	toggleSelect func(int64), toggleSelectAll func(), visibleIDs []int64,
	onEdit func(*affv1.Item), onDelete func(*affv1.Item), onRestore func(*affv1.Item),
	onCorrection func(*affv1.Item, string, string, string), onToggleRev func(*affv1.Item), viewRevID int64,
	onRevert func(*affv1.Item, int)) ui.Node {

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
				return itemRow(t, it, sel.IsSelected(it.Id), toggleSelect, onEdit, onDelete, onRestore, onCorrection, onToggleRev, viewRevID == it.Id, revs, onRevert)
			}),
		),
	)
}

func itemRow(t Catalog, it *affv1.Item, selected bool, toggleSelect func(int64),
	onEdit func(*affv1.Item), onDelete func(*affv1.Item), onRestore func(*affv1.Item),
	onCorrection func(*affv1.Item, string, string, string), onToggleRev func(*affv1.Item),
	showRev bool, revs *RevisionStore, onRevert func(*affv1.Item, int)) ui.Node {

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
					h.Button(h.Type("button"), h.ClassStr("history-kebab-trigger"), t.T("history.kebab", nil)),
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
		h.If(showRev, h.Tr(h.Td(h.Attr("colspan", 6), revisionPanel(t, it, revs, onRevert)))),
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

func revisionPanel(t Catalog, it *affv1.Item, revs *RevisionStore, onRevert func(*affv1.Item, int)) ui.Node {
	list := revs.List(it.Id)
	return h.Div(h.ClassStr("history-revisions"),
		h.P(t.T("history.items.revisions_session_notice", nil)),
		h.IfElse(len(list) == 0,
			h.P(t.T("history.items.no_revisions", nil)),
			h.Ul(h.MapKeyed(list, func(r Revision) any { return r.At.UnixNano() }, func(r Revision) ui.Node {
				idx := indexOfRevision(list, r)
				return h.Li(
					h.Span(formatTimestampTime(r.At)),
					h.Ul(h.MapKeyed(r.Diff(), func(d FieldDiff) any { return d.Field }, func(d FieldDiff) ui.Node {
						return h.Li(h.Strong(d.Field), diffLinesNode(d.Lines))
					})),
					h.Button(h.Type("button"), h.OnClick(func() { onRevert(it, idx) }), t.T("history.items.revert", nil)),
				)
			})),
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

func indexOfRevision(list []Revision, target Revision) int {
	for i, r := range list {
		if r.At.Equal(target.At) {
			return i
		}
	}
	return -1
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

func formatTimestampTime(t time.Time) string {
	return i18n.FormatDate("en", t, i18n.DateOptions{Style: i18n.DateStyleMedium})
}

// nowTimestamp/timestampOrNil are small bridges between time.Time and the
// wire *timestamppb.Timestamp type used by the optimistic local updates
// above (soft-delete's DeletedAt, and reconstructing an Item to submit
// through Update after a revert).
func nowTimestamp() *timestamppb.Timestamp {
	return timestamppb.New(time.Now())
}

func timestampOrNil(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}
