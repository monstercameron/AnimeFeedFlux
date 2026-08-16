package feedspec

import "testing"

// TestScheduleModeValidation pins the mode contract: the two real modes and
// both spellings of the default pass, a typo is rejected (never silently
// treated as scheduled — a feed surprising its operator by firing is the
// exact thing the mode exists to prevent), and ad hoc exempts the schedule
// fields nothing will ever read.
func TestScheduleModeValidation(t *testing.T) {
	base := Defaults()
	base.Slug = "watcher"
	base.Title = "Watcher"
	base.SystemPrompt = "s"
	base.UserPrompt = "u"

	valid := func(mode string) []Problem {
		s := base
		s.ScheduleMode = mode
		return Validate(s)
	}

	for _, mode := range []string{"", ScheduleModeScheduled, ScheduleModeAdhoc, ScheduleModeWatch} {
		if problems := valid(mode); len(problems) != 0 {
			t.Fatalf("mode %q: unexpected problems %v", mode, problems)
		}
	}

	if problems := valid("addhoc"); len(problems) == 0 {
		t.Fatal("a typo'd schedule_mode passed validation")
	} else if problems[0].Reason != ReasonScheduleModeUnknown {
		t.Fatalf("typo'd mode rejected as %q, want %q", problems[0].Reason, ReasonScheduleModeUnknown)
	}

	// A broken cron blocks a scheduled feed and a watch feed, but never an
	// ad-hoc one — nothing evaluates it there.
	broken := base
	broken.Cron = "not a cron"
	if problems := Validate(broken); len(problems) == 0 {
		t.Fatal("broken cron passed on a scheduled feed")
	}
	broken.ScheduleMode = ScheduleModeWatch
	if problems := Validate(broken); len(problems) == 0 {
		t.Fatal("broken cron passed on a watch feed — watch still fires on it")
	}
	broken.ScheduleMode = ScheduleModeAdhoc
	if problems := Validate(broken); len(problems) != 0 {
		t.Fatalf("broken cron blocked an ad-hoc feed: %v", problems)
	}
}

// TestScheduleModeRoundTripsThroughTOML: the mode is part of the recipe and
// must survive export/import like every other field.
func TestScheduleModeRoundTripsThroughTOML(t *testing.T) {
	s := Defaults()
	s.Slug = "watcher"
	s.Title = "Watcher"
	s.ScheduleMode = ScheduleModeWatch

	raw, err := Export(s)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	back, err := Import(raw)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if back.ScheduleMode != ScheduleModeWatch {
		t.Fatalf("schedule_mode did not round trip: %q", back.ScheduleMode)
	}
}
