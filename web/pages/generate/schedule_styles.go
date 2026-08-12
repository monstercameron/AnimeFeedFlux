//go:build js && wasm

package generatepage

import (
	"github.com/monstercameron/GoWebComponents/v5/css"

	"github.com/monstercameron/AnimeFeedFlux/web/tokens"
)

// schedule_styles.go styles the schedule builder (render_schedule.go).
//
// In its own file rather than appended to styles.go because that file is
// large, actively edited, and organised around the workbench layout — a
// hundred lines about a control that did not exist yet would bury both.
//
// Every value is a token. The one visual decision worth stating: the builder
// reads as a SENTENCE, so its rows are inline-flex with baseline alignment
// rather than the label-above-field stack the rest of the editor uses. "Every
// [2] [weeks] on [Thu]" only works if the controls sit on the line where the
// words are; stacked, it becomes a form again and the whole point is lost.

func emitScheduleStyles() {
	css.Global(".af-schedule",
		css.Display.Flex,
		css.Raw("flex-direction", "column"),
		css.Gap(tokens.Space(3)),
		css.Border(css.Px(1), tokens.Color(tokens.RoleBorder)),
		css.Rounded(tokens.Radius(tokens.RadiusMd)),
		css.Padding(tokens.Space(4)),
	)

	// A row of the sentence. Wraps, because at a narrow width the honest
	// failure is a second line, not a horizontal scrollbar.
	css.Global(".af-schedule__row",
		css.Display.Flex,
		css.FlexWrap.Wrap,
		css.Items.Center,
		css.Gap(tokens.Space(2)),
	)

	css.Global(".af-schedule__label",
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
	)

	// The interval box holds one or two digits and nothing else. Left at its
	// natural width, a number input in this app stretches to the 30rem field
	// width the editor sets, which puts twenty empty characters between
	// "Every" and "weeks" and breaks the sentence.
	css.Global(".af-schedule__interval",
		css.Raw("width", "4.5rem"),
		css.Raw("min-width", "4.5rem"),
	)

	// Weekday chips: seven small toggles, all visible at once.
	css.Global(".af-schedule__chips",
		css.Display.Flex,
		css.FlexWrap.Wrap,
		css.Gap(tokens.Space(1)),
	)
	css.Global(".af-schedule__chip",
		css.Raw("appearance", "none"),
		css.Border(css.Px(1), tokens.Color(tokens.RoleBorder)),
		css.Rounded(tokens.Radius(tokens.RadiusFull)),
		css.Bg(css.Color("transparent")),
		css.TextColor(tokens.Color(tokens.RoleTextMuted)),
		css.FontSize(tokens.FontSize(tokens.TextXs)),
		css.PaddingX(tokens.Space(3)),
		css.PaddingY(tokens.Space(1)),
		css.Cursor.Pointer,
		// A real tap target. These are the smallest controls on the page and
		// the ones most likely to be used on a laptop trackpad in a hurry.
		css.Raw("min-height", "2rem"),
		css.Raw("transition", "background-color "+string(tokens.Duration(tokens.DurationFast))+" "+string(tokens.Easing(tokens.EasingStd))),
	)
	css.Global(".af-schedule__chip:hover",
		css.TextColor(tokens.Color(tokens.RoleText)),
	)
	// Selected days are FILLED, not merely tinted: at this size a colour-only
	// difference between seven adjacent chips is easy to misread, and which
	// days a feed runs on is not a detail to squint at. aria-pressed carries
	// the same fact for anyone not looking.
	css.Global(".af-schedule__chip--on",
		css.Bg(tokens.Color(tokens.RoleAccent)),
		css.TextColor(tokens.Color(tokens.RoleAccentFg)),
		css.Border(css.Px(1), tokens.Color(tokens.RoleAccent)),
	)
	css.Global(".af-schedule__chip:focus-visible",
		css.Raw("outline", "2px solid "+string(tokens.Color(tokens.RoleFocusRing))),
		css.Raw("outline-offset", "2px"),
	)

	// The readback: the settings restated as a sentence. Ink, not muted —
	// this is the line that tells the operator what they just built, so it
	// outranks the labels around it.
	css.Global(".af-schedule__readback",
		css.FontSize(tokens.FontSize(tokens.TextSm)),
		css.TextColor(tokens.Color(tokens.RoleText)),
		css.Margin(css.Zero),
	)

	// The preview. Surface-filled and set apart, because it is the one part of
	// this control that is an ANSWER rather than an input.
	css.Global(".af-schedule__preview",
		css.Bg(tokens.Color(tokens.RoleSurface)),
		css.Rounded(tokens.Radius(tokens.RadiusSm)),
		css.Padding(tokens.Space(3)),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
	)
	css.Global(".af-schedule__preview ul",
		css.Margin(css.Zero),
		css.Raw("padding-inline-start", string(tokens.Space(5))),
	)
	// Dates in the data face: they are a column of timestamps to compare down,
	// and tabular numerals are what keep "Thu 3 Sep" and "Thu 17 Sep" aligned.
	css.Global(".af-schedule__preview li",
		css.Raw("font-variant-numeric", "tabular-nums"),
	)
	css.Global(".af-schedule__preview--error",
		css.TextColor(tokens.Color(tokens.RoleDanger)),
	)

	// The cron escape hatch, set below a rule so it reads as "and if you must"
	// rather than as another field of equal standing.
	css.Global(".af-schedule__advanced",
		css.Raw("border-top", "1px solid "+string(tokens.Color(tokens.RoleBorder))),
		css.Raw("padding-top", string(tokens.Space(3))),
		css.FontSize(tokens.FontSize(tokens.TextSm)),
	)
}
