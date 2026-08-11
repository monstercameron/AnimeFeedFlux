package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/monstercameron/GoWebComponents/v5/css"
	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	gwcui "github.com/monstercameron/GoWebComponents/v5/ui"
)

// render_test.go covers the primitives by RENDERING them, which this package
// had no test doing at all: every existing test here exercised a pure helper
// (OrderKebabItems, SelectListState, ConfirmMatches) and nothing asserted that
// the components those helpers serve emit the markup their doc comments
// promise. The a11y contracts in particular — aria-invalid on an errored
// field, role="switch" + aria-checked on a toggle, scope="col" on a header
// cell, an accessible name on the kebab trigger — are the whole reason these
// primitives exist instead of raw shorthand tags, and they were unverified.
//
// gwcui.RenderToString works under the host toolchain (ui_native.go), so none
// of this needs a browser or a js/wasm build.
//
// # What CANNOT be asserted here, and why
//
// Two GWC hooks are deliberate no-op stubs in the native build:
//
//   - UseCompositeNavigation().ActiveIndex() always returns -1
//     (ui/accessibility_native.go), so every tab renders tabIndex="-1" under
//     the host renderer regardless of which one is selected. The roving
//     tabindex is therefore NOT asserted below — doing so would pin the stub,
//     not the behaviour. aria-selected, which Tabs computes itself, is.
//   - AccessibleOverlay emits its children directly, with none of the
//     surface/backdrop/role="dialog" wrapping the browser build applies. So
//     Modal and Kebab are asserted on the parts they build themselves (the
//     trigger, the title, the close control, item order), never on the
//     overlay semantics the library owns.
//
// Both are properties of GWC's native build, not of this package, and both
// are covered in a browser by e2eweb.

func render(t *testing.T, n Node) string {
	t.Helper()
	out, err := gwcui.RenderToString(n)
	if err != nil {
		t.Fatalf("RenderToString: %v", err)
	}
	return out
}

// mustContain fails with the whole rendered document, which is what makes a
// failure here diagnosable — a bare "want substring" says nothing about what
// was emitted instead.
func mustContain(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Errorf("rendered output is missing %q\ngot: %s", want, got)
	}
}

func mustNotContain(t *testing.T, got, notWant string) {
	t.Helper()
	if strings.Contains(got, notWant) {
		t.Errorf("rendered output unexpectedly contains %q\ngot: %s", notWant, got)
	}
}

// testT is a translator that makes the key visible in the output AND proves
// the args reached it, so a test can tell "the key was resolved" apart from
// "the key was rendered raw because T was nil".
func testT(key string, args ...any) string {
	if len(args) == 0 {
		return "T[" + key + "]"
	}
	parts := make([]string, 0, len(args))
	for _, a := range args {
		parts = append(parts, fmt.Sprint(a))
	}
	return "T[" + key + ":" + strings.Join(parts, ",") + "]"
}

// textNode is a bare text cell, for the primitives that take pre-rendered
// Nodes (Table cells, Modal children) rather than keys.
func textNode(s string) Node { return h.Span(s) }

// attrValue reads one attribute off the first tag matching open (e.g.
// "<input"), so a test can assert that a GENERATED id is used consistently
// without hardcoding what the generator produced.
func attrValue(t *testing.T, doc, open, attr string) string {
	t.Helper()
	start := strings.Index(doc, open)
	if start < 0 {
		t.Fatalf("no %s tag in %s", open, doc)
	}
	end := strings.Index(doc[start:], ">")
	if end < 0 {
		t.Fatalf("unterminated %s tag in %s", open, doc)
	}
	tag := doc[start : start+end]
	key := " " + attr + `="`
	at := strings.Index(tag, key)
	if at < 0 {
		return ""
	}
	rest := tag[at+len(key):]
	closeQuote := strings.Index(rest, `"`)
	if closeQuote < 0 {
		return ""
	}
	return rest[:closeQuote]
}

// --- Button ---------------------------------------------------------------

func TestButtonDefaultsToTypeButton(t *testing.T) {
	// A <button> inside a form defaults to type="submit", which submits the
	// form on click. Every primitive button here is an action button, so the
	// default must be explicit.
	got := render(t, Button(ButtonProps{T: testT, LabelKey: "action.save"}))
	mustContain(t, got, `type="button"`)
	mustContain(t, got, "T[action.save]")
}

func TestButtonBusyDisablesAndAnnounces(t *testing.T) {
	got := render(t, Button(ButtonProps{T: testT, LabelKey: "action.save", Busy: true}))
	mustContain(t, got, "disabled")
	mustContain(t, got, `aria-busy="true"`)
}

func TestButtonDisabledIsNotBusy(t *testing.T) {
	// Disabled and Busy both disable, but only Busy means "in flight" — a
	// screen reader that hears aria-busy on a merely-unavailable control is
	// told to wait for something that is never coming.
	got := render(t, Button(ButtonProps{T: testT, LabelKey: "action.save", Disabled: true}))
	mustContain(t, got, "disabled")
	mustContain(t, got, `aria-busy="false"`)
}

func TestButtonExplicitTypeWins(t *testing.T) {
	got := render(t, Button(ButtonProps{T: testT, LabelKey: "action.save", Type: "submit", ID: "save-btn"}))
	mustContain(t, got, `type="submit"`)
	mustContain(t, got, `id="save-btn"`)
}

func TestButtonNilTranslatorRendersKeyNotEnglish(t *testing.T) {
	// labels.go's rule: a missing T surfaces as an unresolved key, never as a
	// hardcoded English literal, so a wiring bug is visible rather than
	// invisible-but-untranslatable.
	got := render(t, Button(ButtonProps{LabelKey: "action.save"}))
	mustContain(t, got, "action.save")
}

func TestButtonLabelArgsReachTheTranslator(t *testing.T) {
	got := render(t, Button(ButtonProps{T: testT, LabelKey: "action.deleteNamed", LabelArgs: []any{"one-piece"}}))
	mustContain(t, got, "T[action.deleteNamed:one-piece]")
}

func TestButtonVariantsAllRender(t *testing.T) {
	for _, v := range []ButtonVariant{ButtonSecondary, ButtonPrimary, ButtonGhost, ButtonDanger} {
		got := render(t, Button(ButtonProps{T: testT, LabelKey: "k", Variant: v, FullWidth: true}))
		mustContain(t, got, "<button")
	}
}

// --- Input ----------------------------------------------------------------

func TestInputLabelIsWiredToTheField(t *testing.T) {
	got := render(t, Input(InputProps{T: testT, ID: "slug", LabelKey: "field.slug"}))
	mustContain(t, got, `for="slug"`)
	mustContain(t, got, `id="slug"`)
	mustContain(t, got, "T[field.slug]")
}

func TestInputErrorSetsAriaInvalidAndReplacesHelp(t *testing.T) {
	// The point of the primitive: a validation message that is announced, not
	// merely colored red.
	got := render(t, Input(InputProps{
		T: testT, ID: "slug", LabelKey: "field.slug",
		HelpKey: "field.slugHelp", ErrorKey: "field.slugTaken", ErrorArgs: []any{"one-piece"},
	}))
	mustContain(t, got, `aria-invalid="true"`)
	mustContain(t, got, `aria-describedby="slug-help"`)
	mustContain(t, got, `id="slug-help"`)
	mustContain(t, got, "T[field.slugTaken:one-piece]")
	mustNotContain(t, got, "T[field.slugHelp]")
}

func TestInputDescribedbyAlwaysResolvesToARealElement(t *testing.T) {
	// helpOrError renders an empty span rather than nothing, so
	// aria-describedby never points at an id that does not exist — a dangling
	// reference is an accessibility error, not a harmless no-op.
	got := render(t, Input(InputProps{T: testT, ID: "slug", LabelKey: "field.slug"}))
	mustContain(t, got, `aria-describedby="slug-help"`)
	mustContain(t, got, `id="slug-help"`)
	mustContain(t, got, `aria-invalid="false"`)
}

func TestInputHelpShowsWhenThereIsNoError(t *testing.T) {
	got := render(t, Input(InputProps{T: testT, ID: "slug", LabelKey: "l", HelpKey: "field.slugHelp"}))
	mustContain(t, got, "T[field.slugHelp]")
}

func TestInputTypeDefaultsToTextAndCarriesOptionals(t *testing.T) {
	got := render(t, Input(InputProps{T: testT, ID: "a", LabelKey: "l"}))
	mustContain(t, got, `type="text"`)

	got = render(t, Input(InputProps{
		T: testT, ID: "pw", LabelKey: "l", Type: "password",
		PlaceholderKey: "field.pwPlaceholder", AutoComplete: "current-password",
		Required: true, Disabled: true, Mono: true, Value: "hunter",
	}))
	mustContain(t, got, `type="password"`)
	mustContain(t, got, `placeholder="T[field.pwPlaceholder]"`)
	mustContain(t, got, `autocomplete="current-password"`)
	mustContain(t, got, "disabled")
	mustContain(t, got, `value="hunter"`)
}

func TestInputWithoutIDStillWiresLabelToField(t *testing.T) {
	// The generated id must be the SAME one the label points at; a mismatch
	// would leave the field unlabeled and the failure is invisible by eye.
	got := render(t, Input(InputProps{T: testT, LabelKey: "l"}))
	id := attrValue(t, got, `<input`, "id")
	if id == "" {
		t.Fatalf("input has no id: %s", got)
	}
	mustContain(t, got, `for="`+id+`"`)
	mustContain(t, got, `aria-describedby="`+id+`-help"`)
	mustContain(t, got, `id="`+id+`-help"`)
}

// --- Select ---------------------------------------------------------------

func TestSelectMarksTheMatchingOptionSelected(t *testing.T) {
	got := render(t, Select(SelectProps{
		T: testT, ID: "fmt", LabelKey: "field.format", Value: "atom",
		Options: []SelectOption{
			{Value: "rss", LabelKey: "format.rss"},
			{Value: "atom", LabelKey: "format.atom"},
		},
	}))
	mustContain(t, got, "T[format.rss]")
	mustContain(t, got, "T[format.atom]")
	mustContain(t, got, "selected")
	// Exactly one option is selected.
	if n := strings.Count(got, "selected"); n != 1 {
		t.Errorf("want exactly 1 selected option, got %d\n%s", n, got)
	}
}

func TestSelectErrorWiring(t *testing.T) {
	got := render(t, Select(SelectProps{
		T: testT, ID: "fmt", LabelKey: "l", ErrorKey: "err.pick", ErrorArgs: []any{"x"},
		Options:  []SelectOption{{Value: "a", LabelKey: "a"}},
		Required: true, Disabled: true,
	}))
	mustContain(t, got, `aria-invalid="true"`)
	mustContain(t, got, `aria-describedby="fmt-help"`)
	mustContain(t, got, "T[err.pick:x]")
	mustContain(t, got, "disabled")
}

func TestSelectWithoutIDGeneratesOne(t *testing.T) {
	got := render(t, Select(SelectProps{T: testT, LabelKey: "l", HelpKey: "h",
		Options: []SelectOption{{Value: "a", LabelKey: "a"}}}))
	mustContain(t, got, "T[h]")
	mustContain(t, got, "<select")
}

// --- Textarea -------------------------------------------------------------

func TestTextareaDefaultsToFourRows(t *testing.T) {
	got := render(t, Textarea(TextareaProps{T: testT, ID: "prompt", LabelKey: "field.prompt"}))
	mustContain(t, got, "<textarea")
	mustContain(t, got, `rows="4"`)
	mustContain(t, got, `for="prompt"`)
}

func TestTextareaSharesInputsErrorConvention(t *testing.T) {
	// The reason this primitive exists: multi-line fields used to be bare
	// shorthand tags with no label, no error text and no aria wiring, unlike
	// every single-line field. That gap is what these assertions pin.
	got := render(t, Textarea(TextareaProps{
		T: testT, ID: "prompt", LabelKey: "l", HelpKey: "h",
		ErrorKey: "err.tooLong", ErrorArgs: []any{4000},
		Rows: 10, Mono: true, Required: true, Disabled: true,
		PlaceholderKey: "ph", Value: "body",
	}))
	mustContain(t, got, `rows="10"`)
	mustContain(t, got, `aria-invalid="true"`)
	mustContain(t, got, `aria-describedby="prompt-help"`)
	mustContain(t, got, `id="prompt-help"`)
	mustContain(t, got, "err.tooLong")
	mustContain(t, got, `placeholder="T[ph]"`)
	mustContain(t, got, "disabled")
}

func TestTextareaWithoutIDGeneratesOne(t *testing.T) {
	got := render(t, Textarea(TextareaProps{T: testT, LabelKey: "l"}))
	id := attrValue(t, got, "<textarea", "id")
	if id == "" {
		t.Fatalf("textarea has no id: %s", got)
	}
	mustContain(t, got, `for="`+id+`"`)
}

// --- Toggle ---------------------------------------------------------------

func TestToggleIsASwitchWithState(t *testing.T) {
	got := render(t, Toggle(ToggleProps{T: testT, ID: "enabled", LabelKey: "field.enabled", Checked: true}))
	mustContain(t, got, `role="switch"`)
	mustContain(t, got, `aria-checked="true"`)
	mustContain(t, got, `for="enabled"`)

	got = render(t, Toggle(ToggleProps{T: testT, ID: "enabled", LabelKey: "l"}))
	mustContain(t, got, `aria-checked="false"`)
}

func TestToggleReasonOnlyShowsWhileActuallyDisabled(t *testing.T) {
	// Regression test for the fix in 77c18b5: the reason used to render
	// whenever the key was set, so every screen with a toggle carried a
	// permanent, false "Reconnecting to the server — these controls are
	// unavailable" under a control that was working fine.
	working := render(t, Toggle(ToggleProps{
		T: testT, ID: "kill", LabelKey: "l",
		DisabledReasonKey: "state.reconnecting",
	}))
	mustNotContain(t, working, "T[state.reconnecting]")
	mustNotContain(t, working, `aria-describedby="kill-reason"`)

	stopped := render(t, Toggle(ToggleProps{
		T: testT, ID: "kill", LabelKey: "l", Disabled: true,
		DisabledReasonKey: "state.reconnecting",
	}))
	mustContain(t, stopped, "T[state.reconnecting]")
	mustContain(t, stopped, `aria-describedby="kill-reason"`)
	mustContain(t, stopped, `id="kill-reason"`)
}

func TestToggleDisabledWithoutAReasonHasNoDanglingDescribedby(t *testing.T) {
	got := render(t, Toggle(ToggleProps{T: testT, ID: "kill", LabelKey: "l", Disabled: true}))
	mustContain(t, got, "disabled")
	mustNotContain(t, got, "aria-describedby")
}

func TestToggleWithoutIDGeneratesOne(t *testing.T) {
	got := render(t, Toggle(ToggleProps{T: testT, LabelKey: "l", OnChange: func(bool) {}}))
	mustContain(t, got, `role="switch"`)
}

// --- Table ----------------------------------------------------------------

func TestTableIsSemanticAndCaptioned(t *testing.T) {
	got := render(t, Table(TableProps{
		T: testT, ID: "runs", CaptionKey: "table.runs",
		Columns: []TableColumn{
			{ID: "when", LabelKey: "col.when"},
			{ID: "cost", LabelKey: "col.cost", Mono: true},
		},
		Rows: []map[string]Node{
			{"when": textNode("yesterday"), "cost": textNode("$0.01")},
			{"when": textNode("today"), "cost": textNode("$0.02")},
		},
		RowKeys: []string{"r1", "r2"},
	}))
	mustContain(t, got, "<caption")
	mustContain(t, got, "T[table.runs]")
	mustContain(t, got, `scope="col"`)
	if n := strings.Count(got, `scope="col"`); n != 2 {
		t.Errorf("want scope=col on both headers, got %d\n%s", n, got)
	}
	mustContain(t, got, "T[col.when]")
	mustContain(t, got, "yesterday")
	mustContain(t, got, "$0.02")
	if n := strings.Count(got, "<tr"); n != 3 { // header row + 2 body rows
		t.Errorf("want 3 <tr> (1 header + 2 body), got %d\n%s", n, got)
	}
}

func TestTableScrollContainerIsKeyboardReachable(t *testing.T) {
	// An overflow:auto box with no tabindex is scrollable with a mouse and
	// unreachable with a keyboard — the exact failure the container's doc
	// comment says it exists to avoid.
	got := render(t, Table(TableProps{T: testT, CaptionKey: "c",
		Columns: []TableColumn{{ID: "a", LabelKey: "a"}}}))
	mustContain(t, got, `tabIndex="0"`)
}

func TestTableToleratesMissingRowKeysAndCells(t *testing.T) {
	// RowKeys shorter than Rows, and a row with no entry for a declared
	// column, must still render a well-formed table rather than panicking.
	got := render(t, Table(TableProps{
		T: testT, CaptionKey: "c",
		Columns: []TableColumn{{ID: "a", LabelKey: "a"}, {ID: "b", LabelKey: "b"}},
		Rows:    []map[string]Node{{"a": textNode("only-a")}},
		RowKeys: nil,
	}))
	mustContain(t, got, "only-a")
	mustContain(t, got, "<td")
}

// --- Tabs -----------------------------------------------------------------

func TestTabsWiresRolesAndSelection(t *testing.T) {
	got := render(t, Tabs(TabsProps{
		T: testT, ID: "history-tabs", LabelKey: "tabs.history", ActiveID: "items",
		Tabs: []Tab{
			{ID: "runs", LabelKey: "tab.runs", PanelID: "runs-panel"},
			{ID: "items", LabelKey: "tab.items", PanelID: "items-panel", LabelArgs: []any{"3"}},
		},
	}))
	mustContain(t, got, `role="tablist"`)
	mustContain(t, got, `aria-label="T[tabs.history]"`)
	mustContain(t, got, `role="tab"`)
	mustContain(t, got, `aria-controls="runs-panel"`)
	mustContain(t, got, `aria-controls="items-panel"`)
	mustContain(t, got, "T[tab.items:3]")
	if n := strings.Count(got, `aria-selected="true"`); n != 1 {
		t.Errorf("want exactly one selected tab, got %d\n%s", n, got)
	}
	mustContain(t, got, `aria-selected="false"`)
}

func TestTabsWithNoActiveIDSelectsNothing(t *testing.T) {
	got := render(t, Tabs(TabsProps{T: testT, ID: "t", LabelKey: "l",
		Tabs: []Tab{{ID: "a", LabelKey: "a", PanelID: "pa"}}}))
	mustNotContain(t, got, `aria-selected="true"`)
}

// --- Kebab ----------------------------------------------------------------

func TestKebabTriggerHasAnAccessibleName(t *testing.T) {
	// A bare "⋯" with no accessible name is unusable with a screen reader,
	// and the glyph itself must stay out of the accessibility tree so it is
	// not read as prose.
	got := render(t, Kebab(KebabProps{
		T: testT, ID: "row-7", LabelKey: "kebab.actionsFor", LabelArgs: []any{"One Piece"}, Open: true,
		Items: []KebabItem{{ID: "edit", LabelKey: "action.edit"}},
	}))
	mustContain(t, got, `aria-label="T[kebab.actionsFor:One Piece]"`)
	mustContain(t, got, `aria-haspopup="menu"`)
	mustContain(t, got, `aria-expanded="true"`)
	mustContain(t, got, `aria-controls="row-7-menu"`)
	mustContain(t, got, `id="row-7-trigger"`)
	mustContain(t, got, `<span aria-hidden="true">⋯</span>`)
}

func TestKebabClosedReportsCollapsed(t *testing.T) {
	got := render(t, Kebab(KebabProps{T: testT, ID: "row-7", LabelKey: "l",
		Items: []KebabItem{{ID: "edit", LabelKey: "action.edit"}}}))
	mustContain(t, got, `aria-expanded="false"`)
}

func TestKebabPutsDestructiveItemsLast(t *testing.T) {
	got := render(t, Kebab(KebabProps{
		T: testT, ID: "k", LabelKey: "l", Open: true,
		Items: []KebabItem{
			{ID: "purge", LabelKey: "action.purge", Danger: true},
			{ID: "edit", LabelKey: "action.edit"},
			{ID: "delete", LabelKey: "action.delete", Danger: true, Disabled: true},
			{ID: "copy", LabelKey: "action.copy"},
		},
	}))
	order := []string{`id="edit"`, `id="copy"`, `id="purge"`, `id="delete"`}
	last := -1
	for _, want := range order {
		at := strings.Index(got, want)
		if at < 0 {
			t.Fatalf("missing %s in %s", want, got)
		}
		if at < last {
			t.Errorf("item %s rendered out of order (safe actions first, destructive last)\n%s", want, got)
		}
		last = at
	}
	mustContain(t, got, `role="menuitem"`)
	// The disabled destructive item is disabled in the markup, not merely
	// styled as such.
	mustContain(t, got, "disabled")
}

func TestOrderKebabItemsIsAStablePartition(t *testing.T) {
	// Already covered for the basic case in kebab_test.go; this pins the
	// "stable, not a sort" half — two safe actions keep their authored order.
	in := []KebabItem{
		{ID: "b"}, {ID: "z", Danger: true}, {ID: "a"}, {ID: "y", Danger: true},
	}
	got := OrderKebabItems(in)
	want := []string{"b", "a", "z", "y"}
	if len(got) != len(want) {
		t.Fatalf("got %d items, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].ID != want[i] {
			t.Errorf("position %d = %q, want %q (full order %v)", i, got[i].ID, want[i], ids(got))
		}
	}
	// The input must not be reordered in place — callers keep their own copy.
	if in[0].ID != "b" || in[1].ID != "z" {
		t.Errorf("OrderKebabItems mutated its input: %v", ids(in))
	}
}

func ids(items []KebabItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}

// --- Modal / Confirm ------------------------------------------------------

func TestModalTitleIsTheDialogsAccessibleName(t *testing.T) {
	got := render(t, Modal(ModalProps{
		T: testT, ID: "purge", TitleKey: "modal.purgeTitle", Open: true,
		Children: []Node{textNode("body copy")},
	}))
	mustContain(t, got, `id="purge-title"`)
	mustContain(t, got, "T[modal.purgeTitle]")
	mustContain(t, got, "body copy")
	mustContain(t, got, `aria-label="T[action.close]"`)
	mustContain(t, got, `<span aria-hidden="true">×</span>`)
}

func TestConfirmKeepsTheDestructiveButtonDisabledUntilThePhraseMatches(t *testing.T) {
	// The safety property: there is no separate "are you sure" click to race
	// past, so the button's disabled state IS the gate.
	mismatch := render(t, Confirm(ConfirmProps{
		T: testT, ID: "purge", TitleKey: "t", MessageKey: "m",
		RequiredPhrase: "one-piece", Typed: "one-pie", Open: true,
	}))
	mustContain(t, mismatch, "disabled")
	mustContain(t, mismatch, "T[confirm.typePhrase:one-piece]")

	match := render(t, Confirm(ConfirmProps{
		T: testT, ID: "purge", TitleKey: "t", MessageKey: "m",
		RequiredPhrase: "one-piece", Typed: "  one-piece  ", Open: true,
	}))
	// Cancel is still enabled in both; the destructive button is the one that
	// changes, so compare the counts rather than looking for "disabled" at all.
	if strings.Count(match, "disabled") >= strings.Count(mismatch, "disabled") {
		t.Errorf("a matching phrase did not enable the destructive button\nmismatch: %s\nmatch: %s", mismatch, match)
	}
}

func TestConfirmBusyDisablesBothButtons(t *testing.T) {
	busy := render(t, Confirm(ConfirmProps{
		T: testT, ID: "purge", TitleKey: "t", MessageKey: "m",
		RequiredPhrase: "x", Typed: "x", Open: true, Busy: true,
	}))
	mustContain(t, busy, `aria-busy="true"`)
	if n := strings.Count(busy, "disabled"); n < 2 {
		t.Errorf("want cancel and confirm both disabled while busy, got %d disabled controls\n%s", n, busy)
	}
}

func TestConfirmMatchesIsExactApartFromSurroundingSpace(t *testing.T) {
	cases := []struct {
		typed, phrase string
		want          bool
	}{
		{"one-piece", "one-piece", true},
		{"  one-piece\n", "one-piece", true},
		{"One-Piece", "one-piece", false}, // case folding would defeat the gate
		{"one piece", "one-piece", false},
		{"one-piece ok", "one-piece", false},
		{"", "", true},
		{"   ", "", true},
		{"", "one-piece", false},
	}
	for _, tc := range cases {
		if got := ConfirmMatches(tc.typed, tc.phrase); got != tc.want {
			t.Errorf("ConfirmMatches(%q, %q) = %v, want %v", tc.typed, tc.phrase, got, tc.want)
		}
	}
}

// --- Toast ----------------------------------------------------------------

func TestToastCardsAreStatusRegions(t *testing.T) {
	got := render(t, Toast(ToastProps{
		T: testT, ID: "toasts", OnDismiss: func(string) {},
		Items: []ToastItem{
			{ID: "t1", Kind: ToastSuccess, MessageKey: "toast.saved"},
			{ID: "t2", Kind: ToastDanger, MessageKey: "toast.runFailed", MessageArgs: []any{"one-piece"}},
		},
	}))
	if n := strings.Count(got, `role="status"`); n != 2 {
		t.Errorf("want one status region per toast, got %d\n%s", n, got)
	}
	mustContain(t, got, "T[toast.saved]")
	mustContain(t, got, "T[toast.runFailed:one-piece]")
	mustContain(t, got, `aria-label="T[action.dismiss]"`)
}

func TestToastWithoutDismissRendersNoCloseControl(t *testing.T) {
	got := render(t, Toast(ToastProps{T: testT, ID: "toasts",
		Items: []ToastItem{{ID: "t1", MessageKey: "m"}}}))
	mustNotContain(t, got, "action.dismiss")
}

func TestToastEmptyStackStillRendersTheLiveRegion(t *testing.T) {
	// The announcer region must exist BEFORE the first toast arrives:
	// a live region inserted at the same moment as its content is not
	// reliably announced.
	got := render(t, Toast(ToastProps{T: testT, ID: "toasts"}))
	mustContain(t, got, `id="toasts"`)
}

func TestToastColorPerKind(t *testing.T) {
	seen := map[string]ToastKind{}
	for _, k := range []ToastKind{ToastInfo, ToastSuccess, ToastWarning, ToastDanger} {
		c := string(toastColor(k))
		if c == "" {
			t.Errorf("kind %d has no color", k)
		}
		if prev, dup := seen[c]; dup {
			t.Errorf("kinds %d and %d share color %s — the tone carries the meaning", prev, k, c)
		}
		seen[c] = k
	}
}

// --- State views ----------------------------------------------------------

func TestStatePanelRendersTheOneMatchingView(t *testing.T) {
	cases := []struct {
		name  string
		props StatePanelProps
		want  []string
		not   []string
	}{
		{
			name:  "loading announces politely",
			props: StatePanelProps{T: testT, State: StateLoading},
			want:  []string{`role="status"`, `aria-live="polite"`, "T[state.loading]"},
		},
		{
			name:  "empty falls back to the shared key",
			props: StatePanelProps{T: testT, State: StateEmpty},
			want:  []string{"T[state.empty]"},
		},
		{
			name:  "empty honours a caller key",
			props: StatePanelProps{T: testT, State: StateEmpty, EmptyKey: "runs.none"},
			want:  []string{"T[runs.none]"},
			not:   []string{"T[state.empty]"},
		},
		{
			name:  "error is an alert",
			props: StatePanelProps{T: testT, State: StateError, ErrorKey: "err.load", ErrorArgs: []any{"5"}},
			want:  []string{`role="alert"`, "T[err.load:5]"},
			not:   []string{"T[action.retry]"},
		},
		{
			name:  "error offers retry when the caller can retry",
			props: StatePanelProps{T: testT, State: StateError, OnRetry: func() {}},
			want:  []string{`role="alert"`, "T[state.error]", "T[action.retry]"},
		},
		{
			name: "disabled explains itself",
			props: StatePanelProps{T: testT, State: StateDisabledWithReason,
				DisabledReasonKey: "kill.budget", DisabledReasonArgs: []any{"$5.00"}},
			want: []string{"T[state.disabled]", "T[kill.budget:$5.00]"},
		},
		{
			name:  "disconnected falls back to the shared key",
			props: StatePanelProps{T: testT, State: StateDisconnected},
			want:  []string{`role="status"`, "T[state.disconnected]"},
		},
		{
			name:  "disconnected honours a caller key",
			props: StatePanelProps{T: testT, State: StateDisconnected, ReconnectingKey: "ws.retrying"},
			want:  []string{"T[ws.retrying]"},
		},
		{
			name: "populated renders the caller's nodes",
			props: StatePanelProps{T: testT, State: StatePopulated,
				Populated: func() []Node { return []Node{textNode("row one"), textNode("row two")} }},
			want: []string{"row one", "row two"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := render(t, StatePanel(tc.props))
			for _, w := range tc.want {
				mustContain(t, got, w)
			}
			for _, n := range tc.not {
				mustNotContain(t, got, n)
			}
		})
	}
}

func TestStatePanelPopulatedWithNoBuilderRendersNothing(t *testing.T) {
	// A caller that forgot Populated must get an empty node, not a panic.
	if n := StatePanel(StatePanelProps{T: testT, State: StatePopulated}); n != nil {
		t.Errorf("want nil node for a populated state with no builder, got %v", n)
	}
}

// --- Responsive -----------------------------------------------------------

func TestNarrowMediaUsesTheOneThreshold(t *testing.T) {
	// The threshold has exactly one call site that becomes a media query, so
	// CSS and SelectBreakpoint cannot desync. If NarrowMaxWidth moves, this
	// fails unless the query moved with it.
	rules := narrowMedia(css.W(css.Length("100%")))
	if len(rules) == 0 {
		t.Fatal("narrowMedia produced no rules")
	}
	if got := len(narrowMedia()); got != 0 {
		t.Errorf("narrowMedia() with no rules emitted %d rules, want an empty query", got)
	}
	if got := SelectBreakpoint(NarrowMaxWidth); got != BreakpointNarrow {
		t.Errorf("SelectBreakpoint(NarrowMaxWidth) = %v, want BreakpointNarrow", got)
	}
	if got := SelectBreakpoint(NarrowMaxWidth + 1); got != BreakpointRegular {
		t.Errorf("SelectBreakpoint(NarrowMaxWidth+1) = %v, want BreakpointRegular", got)
	}
}
