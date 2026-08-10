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
	T               Catalog
	Item            *affv1.Item
	IsCreate        bool
	NewestPublished time.Time
	OnCancel        func()
	OnSave          func(before ItemSnapshot, req *affv1.Item, expectedVersion int64, isCreate bool)
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

	proposed := parseDateTimeLocal(publishedAt.Get())
	decision := ValidatePublishedAt(proposed, props.NewestPublished, backdateOverride.Get())

	save := func() {
		if decision.Blocked {
			return
		}
		item := &affv1.Item{
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
		before := initial
		props.OnSave(before, item, expectedVersion, props.IsCreate)
	}

	return h.Div(
		h.ClassStr("history-item-form"),
		h.H2(formTitleKey(props.T, props.IsCreate)),
		h.Unless(props.IsCreate, h.Div(h.ClassStr("history-guid-notice"),
			h.P(props.T.T("history.items.guid_never_changes", nil)),
			h.P(itemKeyOf(props.Item)),
		)),
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

		h.Div(h.ClassStr("history-form-actions"),
			h.Button(h.Type("button"), h.Disabled(decision.Blocked), h.OnClick(save), props.T.T("history.save", nil)),
			h.Button(h.Type("button"), h.OnClick(props.OnCancel), props.T.T("history.cancel", nil)),
		),
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
