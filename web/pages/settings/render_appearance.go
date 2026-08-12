//go:build js && wasm

package settings

import (
	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/state"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	afi18n "github.com/monstercameron/AnimeFeedFlux/web/i18n"
	"github.com/monstercameron/AnimeFeedFlux/web/shell"
)

// renderAppearance is the Appearance section: interface language, and the
// colour theme that moved here out of the header on 2026-08-11.
//
// # This is the one section that touches no RPC
//
// Every other section on /settings reads and writes server state, so every
// other section carries the six-state matrix (loading / empty / error /
// disabled / disconnected / populated) and a Save button. This one reads and
// writes localStorage, synchronously, with no network involved — which is
// why it deliberately does NOT call screenWrapper and does NOT gate on
// isDisconnected().
//
// That is not an oversight to be tidied up later. Disabling these controls
// while the control plane is down would be actively wrong: the language and
// the theme are exactly the two things that still work when nothing else
// does, and an operator staring at a DISCONNECTED banner they cannot read is
// the one person who most needs to change the language. D0-10's "refuse
// mutations while disconnected" is about mutations the SERVER owns; there is
// no such mutation here.
//
// # Why there is no Save button
//
// Both controls apply on change. A preference with an Apply step invites the
// state where the control shows one thing and the app is in another, and for
// a language selector that state is unusually hostile: you would be choosing
// a language, seeing nothing happen, and having to find a button labelled in
// the language you are trying to leave.
func renderAppearance() ui.Node {
	// Subscribing to the same atom the shell root does means this section
	// re-renders when the language changes — including when it changes
	// because of a click on the control below. Without it the <select> would
	// keep its own idea of the current value, which is fine until something
	// else changes the locale and this panel silently disagrees.
	locale := state.UseAtomKey(shell.LocaleAtom)
	// The theme has no atom (it is applied via a document attribute, not a
	// render), so its current value is component state seeded from storage.
	// Seeded ONCE per mount, which is correct: nothing outside this component
	// changes the theme preference while the panel is open.
	themePref := ui.UseState(shell.StoredThemePref())

	current := locale.Get()

	// h.SelectedIf on the OPTION, not h.Value on the <select>.
	//
	// h.Value on a <select> does not work in this renderer, and it fails in
	// the most misleading way available: the element renders with no `value`
	// attribute at all and falls back to selectedIndex 0, so the control
	// displays the FIRST option regardless of state. Verified in a browser
	// against this very panel — with the app fully switched to Spanish
	// (document.documentElement.lang === "es", every string translated), the
	// language control still read "English". A control that misreports the
	// setting it exists to show is worse than no control, and the same trap
	// applies to the theme <select> below.
	languageOptions := make([]ui.Node, 0, len(afi18n.SupportedLocales()))
	for _, opt := range afi18n.SupportedLocales() {
		languageOptions = append(languageOptions, h.Option(
			h.Value(opt.Tag),
			h.SelectedIf(opt.Tag == current),
			// Endonyms, never translated — see i18n.LocaleOption. Someone
			// stranded in a language they cannot read finds their own by
			// looking for the word they DO recognise.
			h.Text(opt.Endonym),
		))
	}

	themeOptions := []ui.Node{
		h.Option(h.Value(shell.ThemeSystem), h.SelectedIf(themePref.Get() == shell.ThemeSystem), h.Text(t("settings.appearance.theme.system"))),
		h.Option(h.Value(shell.ThemeLight), h.SelectedIf(themePref.Get() == shell.ThemeLight), h.Text(t("settings.appearance.theme.light"))),
		h.Option(h.Value(shell.ThemeDark), h.SelectedIf(themePref.Get() == shell.ThemeDark), h.Text(t("settings.appearance.theme.dark"))),
	}

	languageCard := h.Div(
		h.ClassStr("af-settings-card"),
		h.H3(h.Text(t("settings.appearance.language.title"))),
		h.P(h.ClassStr("af-field-help"), h.Text(t("settings.appearance.language.help"))),
		h.Label(
			h.For("settings-appearance-language"),
			h.Text(t("settings.appearance.language.label")),
		),
		h.Select(
			h.ID("settings-appearance-language"),
			h.OnChange(func(e ui.ChangeEvent) {
				shell.SelectLocale(e.GetValue())
			}),
			languageOptions,
		),
		// Shown only when a translated catalogue is active. On English there
		// is nothing to disclose — English is the source language, not a
		// translation of anything.
		h.Show(current != afi18n.DefaultLocale, h.P(
			h.ClassStr("af-field-help"),
			h.Text(t("settings.appearance.language.machineNote")),
		)),
	)

	themeCard := h.Div(
		h.ClassStr("af-settings-card"),
		h.H3(h.Text(t("settings.appearance.theme.title"))),
		h.P(h.ClassStr("af-field-help"), h.Text(t("settings.appearance.theme.help"))),
		h.Label(
			h.For("settings-appearance-theme"),
			h.Text(t("settings.appearance.theme.label")),
		),
		h.Select(
			h.ID("settings-appearance-theme"),
			h.OnChange(func(e ui.ChangeEvent) {
				// Record and apply in one step (shell.SelectTheme), then
				// mirror into local state so the control reflects what just
				// happened without re-reading storage.
				shell.SelectTheme(e.GetValue())
				themePref.Set(e.GetValue())
			}),
			themeOptions,
		),
	)

	return h.Div(
		h.ClassStr("af-settings-section"),
		h.H2(h.Text(t("settings.appearance.title"))),
		h.P(h.ClassStr("af-field-help"), h.Text(t("settings.appearance.help"))),
		languageCard,
		themeCard,
	)
}
