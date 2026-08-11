//go:build js

package history

import (
	"strings"
	"time"

	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// ItemFormProps drives the create/edit form (PLAN.md §12.4, TODOS.md
// D3-11..D3-14). Item is nil when IsCreate is true.
type ItemFormProps struct {
	T    Catalog
	Item *affv1.Item
	// Feeds populates the create form's feed picker. An item belongs to a
	// feed and the server rejects feed_id == 0, so without this the form
	// could not produce a saveable item at all.
	Feeds           []*affv1.Feed
	IsCreate        bool
	NewestPublished time.Time
	// Err is the last save mutation's failure (TODOS.md D0-10) —
	// distinguished into disconnected/rejected/unexpected by
	// mutationErrorText rather than shown as an undifferentiated failure;
	// the form stays open and the operator's edits are preserved either
	// way (no field is cleared on a failed save).
	Err      error
	OnCancel func()
	OnSave   func(req *affv1.Item, expectedVersion int64, isCreate bool)
}

// ItemForm is a controlled form: every field is held in component state
// and written back on every input, so a re-render (e.g. from the parent
// list refreshing after another mutation) cannot clobber in-progress
// edits the way an uncontrolled/half-controlled input would (05 HTML
// Authoring's controlled-input warning).
func ItemForm(props ItemFormProps) ui.Node {
	initial := formStateFromItem(props.Item)

	title := ui.UseState(initial.Title)
	summary := ui.UseState(initial.SummaryText)
	body := ui.UseState(initial.BodyHTML)
	link := ui.UseState(initial.Link)
	tags := ui.UseState(strings.Join(initial.Tags, ", "))
	publishedAt := ui.UseState(formatDateTimeLocal(initial.PublishedAt))
	backdateOverride := ui.UseState(false)
	// Defaults to the item's own feed when editing, and to the first
	// available feed when creating — a create form that starts on "nothing
	// selected" makes the commonest case (one feed) an extra step.
	feedID := ui.UseState(initialFeedID(props))

	proposed := parseDateTimeLocal(publishedAt.Get())
	decision := ValidatePublishedAt(proposed, props.NewestPublished, backdateOverride.Get())

	save := func() {
		if decision.Blocked {
			return
		}
		item := &affv1.Item{
			FeedId:      feedID.Get(),
			Title:       title.Get(),
			SummaryText: summary.Get(),
			BodyHtml:    body.Get(),
			Link:        link.Get(),
			Tags:        splitTags(tags.Get()),
			PublishedAt: timestampOrNil(proposed),
		}
		var expectedVersion int64
		if props.Item != nil {
			item.Id = props.Item.Id
			item.Origin = props.Item.Origin
			expectedVersion = props.Item.Version
		}
		props.OnSave(item, expectedVersion, props.IsCreate)
	}

	return h.Div(
		h.ClassStr("history-item-form"),
		h.H2(formTitleKey(props.T, props.IsCreate)),
		h.Unless(props.IsCreate, h.Div(h.ClassStr("history-guid-notice"),
			h.P(props.T.T("history.items.guid_never_changes", nil)),
			h.P(itemKeyOf(props.Item)),
		)),
		// Only on create: an existing item cannot be moved between feeds —
		// its guid and its position in a published feed are already out in
		// the world (§5.5), so the field is shown as fixed context instead.
		h.If(props.IsCreate, renderFeedPicker(props, feedID.Get(), feedID.Set)),

		h.Label(h.For("history-form-title"), props.T.T("history.items.field_title", nil)),
		h.Input(h.ID("history-form-title"), h.Value(title.Get()), h.OnInput(func(ev ui.InputEvent) { title.Set(ev.GetValue()) })),

		h.Label(h.For("history-form-summary"), props.T.T("history.items.field_summary", nil)),
		h.Textarea(h.ID("history-form-summary"), h.OnInput(func(ev ui.InputEvent) { summary.Set(ev.GetValue()) }), h.Text(summary.Get())),

		h.Label(h.For("history-form-body"), props.T.T("history.items.field_body", nil)),
		h.Textarea(h.ID("history-form-body"), h.OnInput(func(ev ui.InputEvent) { body.Set(ev.GetValue()) }), h.Text(body.Get())),

		h.Label(h.For("history-form-link"), props.T.T("history.items.field_link", nil)),
		h.Input(h.ID("history-form-link"), h.Type("url"), h.Value(link.Get()), h.OnInput(func(ev ui.InputEvent) { link.Set(ev.GetValue()) })),

		h.Label(h.For("history-form-tags"), props.T.T("history.items.field_tags", nil)),
		h.Input(h.ID("history-form-tags"), h.Value(tags.Get()), h.OnInput(func(ev ui.InputEvent) { tags.Set(ev.GetValue()) })),

		h.Label(h.For("history-form-published"), props.T.T("history.items.field_published_at", nil)),
		h.Input(h.ID("history-form-published"), h.Type("datetime-local"), h.Value(publishedAt.Get()), h.OnInput(func(ev ui.InputEvent) { publishedAt.Set(ev.GetValue()) })),

		h.If(decision.Blocked, h.Div(h.ClassStr("history-backdate-block"),
			h.P(props.T.T("history.items.backdate_blocked", nil)),
			h.Label(
				h.Input(h.Type("checkbox"), h.Checked(backdateOverride.Get()), h.OnChange(func() { backdateOverride.Set(!backdateOverride.Get()) })),
				props.T.T("history.items.backdate_override_confirm", nil),
			),
		)),
		h.If(decision.WarnKey != "", h.P(h.ClassStr("history-backdate-warning"), props.T.T(decision.WarnKey, nil))),

		h.If(props.Err != nil, h.Div(h.ClassStr("history-form-error"), mutationErrorText(props.T, props.Err))),

		h.Div(h.ClassStr("history-form-actions"),
			h.Button(h.Type("button"), h.Disabled(decision.Blocked), h.OnClick(save), props.T.T("history.save", nil)),
			h.Button(h.Type("button"), h.OnClick(props.OnCancel), props.T.T("history.cancel", nil)),
		),
	)
}

// initialFeedID picks the feed the form starts on.
func initialFeedID(props ItemFormProps) int64 {
	if props.Item != nil && props.Item.GetFeedId() != 0 {
		return props.Item.GetFeedId()
	}
	if len(props.Feeds) > 0 {
		return props.Feeds[0].GetId()
	}
	return 0
}

// renderFeedPicker is the create form's feed selector. With no feeds loaded
// it says so rather than rendering an empty menu that silently produces an
// unsaveable item.
func renderFeedPicker(props ItemFormProps, selected int64, onSelect func(int64)) ui.Node {
	if len(props.Feeds) == 0 {
		return h.P(h.ClassStr("history-form-error"), props.T.T("history.items.field_feed_none", nil))
	}
	opts := make([]any, 0, len(props.Feeds)+3)
	opts = append(opts,
		h.ID("history-form-feed"),
		h.OnChange(ui.UseEvent(func(ev ui.InputEvent) { onSelect(parseInt64(ev.GetValue())) })),
	)
	for _, f := range props.Feeds {
		id := f.GetId()
		opts = append(opts, h.Option(h.Value(int64Str(id)), h.SelectedIf(id == selected), h.Text(f.GetTitle())))
	}
	return h.Fragment(
		h.Label(h.For("history-form-feed"), props.T.T("history.items.field_feed", nil)),
		h.Select(opts...),
	)
}

func formTitleKey(t Catalog, isCreate bool) string {
	if isCreate {
		return t.T("history.items.create_title", nil)
	}
	return t.T("history.items.edit_title", nil)
}

func itemKeyOf(it *affv1.Item) string {
	if it == nil {
		return ""
	}
	return it.ItemKey
}

func formStateFromItem(it *affv1.Item) ItemSnapshot {
	if it == nil {
		return ItemSnapshot{PublishedAt: time.Now()}
	}
	return snapshotOf(it)
}

func splitTags(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// formatDateTimeLocal / parseDateTimeLocal bridge time.Time and the
// <input type="datetime-local"> value format, which is neither RFC 3339
// nor locale-formatted — it is a fixed wire format the browser control
// itself defines, so this is not a "use fmt.Sprintf for a user-visible
// date" violation: nothing here is shown to the operator as text, it is
// the input element's own protocol. i18n.FormatDate (used everywhere the
// date is actually displayed, e.g. formatTimestamp) is what renders the
// human-visible date.
func formatDateTimeLocal(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return t.Format("2006-01-02T15:04")
}

func parseDateTimeLocal(s string) time.Time {
	t, err := time.Parse("2006-01-02T15:04", s)
	if err != nil {
		return time.Time{}
	}
	return t
}
