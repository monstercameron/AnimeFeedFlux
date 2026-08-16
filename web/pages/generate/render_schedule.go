//go:build js && wasm

package generatepage

import (
	"strconv"
	"time"

	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/feedspec"
	wui "github.com/monstercameron/AnimeFeedFlux/web/ui"
)

// render_schedule.go is the schedule builder that replaced the cron field.
//
// # What was wrong with the cron field
//
// It asked the operator to encode their intent in a syntax that cannot hold
// it. "Every other Thursday at 4pm" has NO cron expression — `0 16 * * 4/2`
// does not mean every second Thursday, it means every second weekday number
// starting from Thursday, which is Thursday and Saturday. So the previous
// control was not merely unfriendly; for a whole class of ordinary schedules
// it was a box in which the correct answer could not be written, and any
// attempt produced a feed that ran on the wrong days without complaining.
//
// # Shape
//
// One row of controls that reads as a sentence — "Every [2] [weeks] on
// [Thu] at [16:00] starting [2026-08-06]" — plus two things the cron field
// never had and could not have:
//
//   - a readback, so the settings are restated in words, and
//   - the next five ACTUAL firing times, computed in the browser from
//     internal/schedule, so "which Thursdays" is answered by reading dates
//     rather than by trusting the label.
//
// The preview is what makes this trustworthy rather than merely prettier.
// TODOS.md D2-09 asked for exactly it and concluded no RPC could provide the
// times; the missing piece was never an RPC, since the scheduling engine is
// ordinary Go that compiles to wasm.

// scheduleProps carries everything the builder needs. It takes the whole
// draft feed rather than a ScheduleDraft so the component stays stateless:
// the feed being edited is the single source of truth, the draft is derived
// on every render, and every control writes straight back to the feed. No
// second copy of the schedule to fall out of sync with the first.
type scheduleProps struct {
	T        Translator
	Spec     *affv1.FeedSpec
	Now      time.Time
	OnChange func(func(*affv1.Feed))
	// ShowCron reveals the escape hatch. Held by the parent so toggling it
	// does not remount this component and lose focus.
	ShowCron   bool
	OnToggle   func(bool)
	CronErrKey string
	CronErr    []any
	TZErrKey   string
	TZErr      []any
}

// renderSchedule draws the builder.
func renderSchedule(p scheduleProps) ui.Node {
	t := p.T
	tz := p.Spec.GetTimezone()
	if tz == "" {
		tz = "UTC"
	}
	draft := DraftFromProto(p.Spec.GetRecurrence(), p.Now)

	// apply writes a mutated draft back to the feed as a recurrence. Every
	// control funnels through this, so there is exactly one place the feed is
	// updated and exactly one shape it is updated into.
	//
	// The draft is re-derived INSIDE the mutation, from the feed as it stands
	// when the change is applied — not from the `draft` computed above at
	// render time. That closes a lost-update window: every control writes the
	// WHOLE recurrence, so if two changes landed without a render between them
	// (a fast click after a select, an automated run) the second would write a
	// draft that never saw the first, silently reverting it. Deriving late
	// makes each change a read-modify-write against current state instead.
	apply := func(mutate func(*ScheduleDraft)) {
		p.OnChange(func(f *affv1.Feed) {
			s := ensureSpec(f)
			d := DraftFromProto(s.GetRecurrence(), p.Now)
			mutate(&d)
			s.Recurrence = d.ToProto()
		})
	}

	// The schedule MODE (§7 revision 2026-08-15): scheduled (default), ad
	// hoc (manual-only — the builder below is irrelevant and hidden), or
	// watch (the schedule is a check cadence; quiet runs are expected).
	mode := p.Spec.GetScheduleMode()
	modeOpts := []struct{ value, labelKey string }{
		{"", "generate.editor.schedule.mode.scheduled"},
		{"adhoc", "generate.editor.schedule.mode.adhoc"},
		{"watch", "generate.editor.schedule.mode.watch"},
	}
	modeSel := make([]any, 0, len(modeOpts)+2)
	modeSel = append(modeSel,
		h.ID("generate-schedule-mode"),
		h.OnChange(func(e ui.ChangeEvent) {
			v := e.GetValue()
			p.OnChange(func(f *affv1.Feed) { ensureSpec(f).ScheduleMode = v })
		}),
	)
	for _, o := range modeOpts {
		selected := o.value == mode || (o.value == "" && mode == "scheduled")
		modeSel = append(modeSel, h.Option(h.Value(o.value), h.SelectedIf(selected), h.Text(t.T(o.labelKey))))
	}
	modeHelpKey := "generate.editor.schedule.mode.help.scheduled"
	switch mode {
	case "adhoc":
		modeHelpKey = "generate.editor.schedule.mode.help.adhoc"
	case "watch":
		modeHelpKey = "generate.editor.schedule.mode.help.watch"
	}

	builderHidden := mode == "adhoc"

	return h.Fieldset(
		h.ClassStr("af-schedule"),
		h.Legend(h.Text(t.T("generate.editor.schedule"))),

		h.Div(h.ClassStr("af-schedule__row"),
			h.Label(h.For("generate-schedule-mode"), h.Text(t.T("generate.editor.schedule.mode"))),
			h.Select(modeSel...),
		),
		h.P(h.ClassStr("af-field-help"), h.Text(t.T(modeHelpKey))),

		h.Show(!builderHidden, renderScheduleBuilder(p, t, tz, draft, apply)),
	)
}

// renderScheduleBuilder is the frequency/day/time builder — everything that
// only matters when the schedule actually fires (scheduled and watch modes;
// an ad-hoc feed hides it rather than showing controls nothing reads).
func renderScheduleBuilder(p scheduleProps, t Translator, tz string, draft ScheduleDraft, apply func(func(*ScheduleDraft))) ui.Node {
	return h.Div(
		h.ClassStr("af-schedule__builder"),

		// "Every [N] [unit]"
		h.Div(h.ClassStr("af-schedule__row"),
			h.Label(h.For("generate-schedule-interval"), h.Text(t.T("generate.editor.schedule.every"))),
			h.Input(
				h.ID("generate-schedule-interval"),
				h.Type("number"), h.Attr("min", "1"), h.Attr("max", "52"),
				h.ClassStr("af-schedule__interval"),
				h.Value(strconv.Itoa(draft.Interval)),
				h.OnChange(func(e ui.ChangeEvent) {
					n, err := strconv.Atoi(e.GetValue())
					if err != nil || n < 1 {
						n = 1
					}
					apply(func(d *ScheduleDraft) { d.Interval = n })
				}),
			),
			h.Select(
				h.ID("generate-schedule-unit"),
				h.Aria("label", t.T("generate.editor.schedule.repeats")),
				h.OnChange(func(e ui.ChangeEvent) {
					apply(func(d *ScheduleDraft) { d.Unit = ScheduleUnit(e.GetValue()) })
				}),
				unitOptions(t, draft),
			),
		),

		// Weekly: which days. Included conditionally rather than wrapped in
		// h.Show for the same reason cronEscapeHatch is — see its doc comment.
		// Every row here sets display:flex, which beats the `hidden` attribute
		// h.Show relies on, so a "hidden" row is simply a visible row.
		weeklyRow(t, draft, apply),

		monthlyRows(t, draft, apply),

		// Time of day + timezone.
		h.Div(h.ClassStr("af-schedule__row"),
			h.Label(h.For("generate-schedule-time"), h.Text(t.T("generate.editor.schedule.timeOfDay"))),
			h.Input(
				h.ID("generate-schedule-time"),
				// A real time input: the browser renders it in the operator's
				// own 12/24-hour convention while always handing back 24-hour
				// "HH:MM", so the control reads naturally without the app
				// having to guess a locale convention.
				h.Type("time"),
				h.Value(formatClock(draft.Hour, draft.Minute)),
				h.OnChange(func(e ui.ChangeEvent) {
					hh, mm, ok := parseTimeOfDay(e.GetValue())
					if !ok {
						return
					}
					apply(func(d *ScheduleDraft) { d.Hour, d.Minute = hh, mm })
				}),
			),
			wui.Input(wui.InputProps{
				T: wui.T(t.T), ID: "generate-editor-timezone", LabelKey: "generate.editor.timezone",
				Value: p.Spec.GetTimezone(), ErrorKey: p.TZErrKey, ErrorArgs: p.TZErr,
				OnChange: func(v string) { p.OnChange(func(f *affv1.Feed) { ensureSpec(f).Timezone = v }) },
			}),
		),

		// The anchor, shown only when the interval makes it meaningful.
		// At interval 1 it changes nothing, and a control that cannot change
		// anything is a control that invites a wrong theory about what it does.
		anchorRow(t, draft, apply),

		// Readback, then the dates.
		h.P(h.ClassStr("af-schedule__readback"), h.Text(ScheduleReadback(t, draft, tz))),
		renderSchedulePreview(t, draft, tz, p.Now),

		// The escape hatch.
		h.Div(h.ClassStr("af-schedule__advanced"),
			h.Label(
				h.Input(h.Type("checkbox"), h.ID("generate-schedule-advanced"),
					h.Checked(p.ShowCron),
					h.OnChange(func(ui.ChangeEvent) { p.OnToggle(!p.ShowCron) })),
				h.Text(t.T("generate.editor.schedule.advanced")),
			),
			cronEscapeHatch(p),
		),
	)
}

// renderSchedulePreview lists the next firings, or says why it cannot.
func renderSchedulePreview(t Translator, d ScheduleDraft, tz string, now time.Time) ui.Node {
	runs, err := d.PreviewRuns(tz, now, 5)
	if err != nil {
		return h.P(h.ClassStr("af-schedule__preview af-schedule__preview--error"),
			h.Text(t.T("generate.editor.schedule.previewError", err.Error())))
	}
	if len(runs) == 0 {
		return h.P(h.ClassStr("af-schedule__preview af-schedule__preview--error"),
			h.Text(t.T("generate.editor.schedule.previewNone")))
	}

	// Rendered through the locale-aware formatter, not time.Format with a
	// hardcoded layout: PLAN.md §12.6 is explicit that a date the operator
	// reads never goes through fmt/Format directly, and a preview whose dates
	// stay English while the rest of the page is Spanish is exactly the seam
	// that rule exists to close.
	fmtr := deps.Formatters
	if fmtr == nil {
		fmtr = DefaultFormatters
	}
	items := make([]ui.Node, 0, len(runs))
	for _, r := range runs {
		items = append(items, h.Li(h.Text(fmtr.DateTime(r, tz))))
	}

	return h.Div(h.ClassStr("af-schedule__preview"),
		h.Span(h.ClassStr("af-schedule__label"), h.Text(t.T("generate.editor.schedule.preview"))),
		h.Ul(items),
		h.P(h.ClassStr("af-field-hint"), h.Text(t.T("generate.editor.schedule.previewHelp"))),
	)
}

// weekdayChips renders the seven days as toggles rather than a multi-select.
//
// A multi-select is the wrong control here: it hides how many are chosen
// until you open it, needs ctrl-click to add a second, and on touch is close
// to unusable. Seven chips show the whole answer at a glance.
func weekdayChips(t Translator, d ScheduleDraft, apply func(func(*ScheduleDraft))) ui.Node {
	chips := make([]ui.Node, 0, 7)
	for wd := time.Sunday; wd <= time.Saturday; wd++ {
		day := wd
		on := containsDay(d.Weekdays, day)
		chips = append(chips, h.Button(
			h.Type("button"),
			h.ClassStr(h.ClassMap(map[string]bool{
				"af-schedule__chip":     true,
				"af-schedule__chip--on": on,
			})),
			// aria-pressed carries the state for anyone not looking at the
			// fill, the same treatment the theme control used.
			h.Aria("pressed", boolAttrStr(on)),
			h.OnClick(func() {
				apply(func(dd *ScheduleDraft) { dd.Weekdays = ToggleWeekday(dd.Weekdays, day) })
			}),
			h.Text(t.T("generate.editor.schedule.weekday.short."+feedspec.WeekdayName(day))),
		))
	}
	return h.Div(h.ClassStr("af-schedule__chips"), h.Role("group"), chips)
}

func unitOptions(t Translator, d ScheduleDraft) ui.Node {
	opts := make([]ui.Node, 0, len(ScheduleUnits))
	for _, u := range ScheduleUnits {
		key := "generate.editor.schedule.unit." + string(u) + ".plural"
		if d.Interval == 1 {
			key = "generate.editor.schedule.unit." + string(u) + ".singular"
		}
		opts = append(opts, h.Option(h.Value(string(u)), h.SelectedIf(d.Unit == u), h.Text(t.T(key))))
	}
	return h.Fragment(opts)
}

func weekdayOptions(t Translator, selected time.Weekday) ui.Node {
	opts := make([]ui.Node, 0, 7)
	for wd := time.Sunday; wd <= time.Saturday; wd++ {
		opts = append(opts, h.Option(
			h.Value(strconv.Itoa(int(wd))), h.SelectedIf(wd == selected),
			h.Text(t.T("generate.editor.schedule.weekday."+feedspec.WeekdayName(wd)))))
	}
	return h.Fragment(opts)
}

func setPosOptions(t Translator, selected int) ui.Node {
	type pos struct {
		v   int
		key string
	}
	all := []pos{
		{1, "generate.editor.schedule.ordinal.first"},
		{2, "generate.editor.schedule.ordinal.second"},
		{3, "generate.editor.schedule.ordinal.third"},
		{4, "generate.editor.schedule.ordinal.fourth"},
		{-1, "generate.editor.schedule.ordinal.last"},
	}
	opts := make([]ui.Node, 0, len(all))
	for _, p := range all {
		opts = append(opts, h.Option(h.Value(strconv.Itoa(p.v)), h.SelectedIf(p.v == selected), h.Text(t.T(p.key))))
	}
	return h.Fragment(opts)
}

func monthDayOptions(t Translator, selected int) ui.Node {
	opts := make([]ui.Node, 0, 32)
	for day := 1; day <= 31; day++ {
		opts = append(opts, h.Option(h.Value(strconv.Itoa(day)), h.SelectedIf(day == selected),
			h.Text(strconv.Itoa(day))))
	}
	// "Last day" as an option rather than a 32nd number: it is the only way
	// to say "end of the month" without the 31st silently skipping February,
	// April, June, September and November.
	opts = append(opts, h.Option(h.Value("-1"), h.SelectedIf(selected == -1),
		h.Text(t.T("generate.editor.schedule.lastDayOption"))))
	return h.Fragment(opts)
}

func containsDay(days []time.Weekday, d time.Weekday) bool {
	for _, x := range days {
		if x == d {
			return true
		}
	}
	return false
}

func boolAttrStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// parseTimeOfDay reads an <input type="time"> value, "HH:MM".
//
// Named for what it parses rather than "parseClock", which logic.go already
// uses for a different job (cron field text).
func parseTimeOfDay(v string) (hour, minute int, ok bool) {
	if len(v) < 4 {
		return 0, 0, false
	}
	parts := []rune(v)
	colon := -1
	for i, r := range parts {
		if r == ':' {
			colon = i
			break
		}
	}
	if colon < 1 {
		return 0, 0, false
	}
	hh, err1 := strconv.Atoi(string(parts[:colon]))
	mm, err2 := strconv.Atoi(string(parts[colon+1:]))
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, 0, false
	}
	return hh, mm, true
}

// cronEscapeHatch renders the cron field, or nothing at all.
//
// NOT h.Show. h.Show hides by setting the `hidden` ATTRIBUTE, which the user
// agent implements as display:none and which therefore loses to any rule that
// sets a display of its own — and wui.Input's wrapper sets one. The result was
// visible in the first browser screenshot of this control: an unchecked
// "use a cron expression instead" box with the cron field sitting open
// underneath it, which is the opposite of what the checkbox says.
//
// web/shell/expiry.go hit the identical trap and records the same conclusion:
// a control that is not supposed to be there should not be in the DOM. This
// branch is safe to take because nothing inside it calls a hook.
func cronEscapeHatch(p scheduleProps) ui.Node {
	if !p.ShowCron {
		return h.Fragment()
	}
	t := p.T
	return h.Fragment(
		h.P(h.ClassStr("af-field-hint"), h.Text(t.T("generate.editor.schedule.advancedHelp"))),
		wui.Input(wui.InputProps{
			T: wui.T(t.T), ID: "generate-editor-cron", LabelKey: "generate.editor.cron",
			Value: p.Spec.GetCron(), ErrorKey: p.CronErrKey, ErrorArgs: p.CronErr,
			OnChange: func(v string) {
				// Writing a cron expression CLEARS the structured recurrence,
				// because Spec.Firing prefers the structured one — leaving both
				// set would mean the operator edits cron while the feed keeps
				// running on the recurrence, with the UI showing the cron they
				// typed. One schedule per feed, always.
				p.OnChange(func(f *affv1.Feed) {
					s := ensureSpec(f)
					s.Cron = v
					s.Recurrence = nil
				})
			},
		}),
	)
}

// weeklyRow is the day-of-week picker, present only for weekly schedules.
func weeklyRow(t Translator, d ScheduleDraft, apply func(func(*ScheduleDraft))) ui.Node {
	if d.Unit != UnitWeek {
		return h.Fragment()
	}
	return h.Div(h.ClassStr("af-schedule__row"),
		h.Span(h.ClassStr("af-schedule__label"), h.Text(t.T("generate.editor.schedule.onDays"))),
		weekdayChips(t, d, apply),
	)
}

// monthlyRows is the day-of-month / nth-weekday picker, present only for
// monthly and yearly schedules.
func monthlyRows(t Translator, d ScheduleDraft, apply func(func(*ScheduleDraft))) ui.Node {
	if d.Unit != UnitMonth && d.Unit != UnitYear {
		return h.Fragment()
	}

	var dayRow ui.Node = h.Fragment()
	if d.Mode == MonthlyOnDay {
		dayRow = h.Div(h.ClassStr("af-schedule__row"),
			h.Label(h.For("generate-schedule-monthday"), h.Text(t.T("generate.editor.schedule.dayOfMonth"))),
			h.Select(
				h.ID("generate-schedule-monthday"),
				h.OnChange(func(e ui.ChangeEvent) {
					n, err := strconv.Atoi(e.GetValue())
					if err != nil {
						n = 1
					}
					apply(func(dd *ScheduleDraft) { dd.MonthDay = n })
				}),
				monthDayOptions(t, d.MonthDay),
			),
		)
	} else {
		dayRow = h.Div(h.ClassStr("af-schedule__row"),
			h.Label(h.For("generate-schedule-setpos"), h.Text(t.T("generate.editor.schedule.onThe"))),
			h.Select(
				h.ID("generate-schedule-setpos"),
				h.OnChange(func(e ui.ChangeEvent) {
					n, err := strconv.Atoi(e.GetValue())
					if err != nil {
						n = 1
					}
					apply(func(dd *ScheduleDraft) { dd.SetPosition = n })
				}),
				setPosOptions(t, d.SetPosition),
			),
			h.Select(
				h.ID("generate-schedule-monthweekday"),
				h.Aria("label", t.T("generate.editor.schedule.onDays")),
				h.OnChange(func(e ui.ChangeEvent) {
					n, err := strconv.Atoi(e.GetValue())
					if err != nil {
						return
					}
					apply(func(dd *ScheduleDraft) { dd.MonthWeekday = time.Weekday(n) })
				}),
				weekdayOptions(t, d.MonthWeekday),
			),
		)
	}

	return h.Fragment(
		h.Div(h.ClassStr("af-schedule__row"),
			h.Label(h.For("generate-schedule-monthmode"), h.Text(t.T("generate.editor.schedule.monthlyMode"))),
			h.Select(
				h.ID("generate-schedule-monthmode"),
				h.OnChange(func(e ui.ChangeEvent) {
					apply(func(dd *ScheduleDraft) { dd.Mode = MonthlyMode(e.GetValue()) })
				}),
				h.Option(h.Value(string(MonthlyOnDay)), h.SelectedIf(d.Mode == MonthlyOnDay),
					h.Text(t.T("generate.editor.schedule.monthlyMode.day"))),
				h.Option(h.Value(string(MonthlyOnWeekday)), h.SelectedIf(d.Mode == MonthlyOnWeekday),
					h.Text(t.T("generate.editor.schedule.monthlyMode.weekday"))),
			),
		),
		dayRow,
	)
}

// anchorRow is the "starting on" date, present only when the interval makes
// it meaningful. At interval 1 it changes nothing, and a control that cannot
// change anything invites a wrong theory about what it does.
func anchorRow(t Translator, d ScheduleDraft, apply func(func(*ScheduleDraft))) ui.Node {
	if d.Interval <= 1 {
		return h.Fragment()
	}
	return h.Fragment(
		h.Div(h.ClassStr("af-schedule__row"),
			h.Label(h.For("generate-schedule-anchor"), h.Text(t.T("generate.editor.schedule.startingOn"))),
			h.Input(
				h.ID("generate-schedule-anchor"),
				h.Type("date"),
				h.Value(d.Anchor),
				h.OnChange(func(e ui.ChangeEvent) {
					v := e.GetValue()
					apply(func(dd *ScheduleDraft) { dd.Anchor = v })
				}),
			),
		),
		h.P(h.ClassStr("af-field-hint"), h.Text(t.T("generate.editor.schedule.startingHelp"))),
	)
}
