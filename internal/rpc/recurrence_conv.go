package rpc

import (
	"time"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/feedspec"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// recurrence_conv.go converts a structured schedule between the wire form
// (proto enums, a Timestamp anchor) and the recipe form (words and a plain
// date). Both directions live here so a field added to one and forgotten in
// the other is a diff a reviewer can see in one place.

// protoWeekdays maps the wire enum to the recipe's day names.
//
// The enum numbers Sunday as 1, not 0, so "unset" and "Sunday" stay
// distinguishable — proto3's zero value is indistinguishable from an absent
// field, and a weekly schedule that silently became "Sundays" because a field
// was dropped in transit is exactly the class of bug that argues for the
// offset.
var protoWeekdays = map[affv1.Weekday]string{
	affv1.Weekday_WEEKDAY_SUNDAY:    "sunday",
	affv1.Weekday_WEEKDAY_MONDAY:    "monday",
	affv1.Weekday_WEEKDAY_TUESDAY:   "tuesday",
	affv1.Weekday_WEEKDAY_WEDNESDAY: "wednesday",
	affv1.Weekday_WEEKDAY_THURSDAY:  "thursday",
	affv1.Weekday_WEEKDAY_FRIDAY:    "friday",
	affv1.Weekday_WEEKDAY_SATURDAY:  "saturday",
}

var weekdayProtos = func() map[string]affv1.Weekday {
	out := make(map[string]affv1.Weekday, len(protoWeekdays))
	for k, v := range protoWeekdays {
		out[v] = k
	}
	return out
}()

var protoFrequencies = map[affv1.Frequency]string{
	affv1.Frequency_FREQUENCY_DAILY:   "daily",
	affv1.Frequency_FREQUENCY_WEEKLY:  "weekly",
	affv1.Frequency_FREQUENCY_MONTHLY: "monthly",
}

var frequencyProtos = func() map[string]affv1.Frequency {
	out := make(map[string]affv1.Frequency, len(protoFrequencies))
	for k, v := range protoFrequencies {
		out[v] = k
	}
	return out
}()

// recurrenceFromProto returns nil when the message is absent or carries no
// frequency, which is how "this feed uses cron" is represented on the wire.
//
// Nil rather than a zero-valued Recurrence matters: a zero Recurrence would
// take precedence over the feed's cron string (Spec.Firing prefers the
// structured form) and then fail to resolve, turning every cron feed that
// round-tripped through an Update into a feed that never runs.
func recurrenceFromProto(pr *affv1.Recurrence) *feedspec.Recurrence {
	if pr == nil {
		return nil
	}
	freq, ok := protoFrequencies[pr.GetFrequency()]
	if !ok {
		return nil
	}

	days := make([]string, 0, len(pr.GetWeekdays()))
	for _, d := range pr.GetWeekdays() {
		if name, ok := protoWeekdays[d]; ok {
			days = append(days, name)
		}
	}

	anchor := ""
	if ts := pr.GetAnchor(); ts != nil && ts.IsValid() {
		// Formatted as a plain UTC date. The anchor is a calendar date, not
		// an instant, and the editor sends midnight UTC for the date the
		// operator picked; re-interpreting it in the feed's zone happens on
		// the way back in (feedspec.Recurrence.Resolve), so the string stays
		// the date that was chosen.
		anchor = ts.AsTime().UTC().Format(feedspec.AnchorLayout)
	}

	return &feedspec.Recurrence{
		Frequency:   freq,
		Interval:    int(pr.GetInterval()),
		Weekdays:    days,
		MonthDay:    int(pr.GetMonthDay()),
		SetPosition: int(pr.GetSetPosition()),
		Hour:        int(pr.GetHour()),
		Minute:      int(pr.GetMinute()),
		Anchor:      anchor,
	}
}

func recurrenceToProto(r *feedspec.Recurrence) *affv1.Recurrence {
	if r == nil {
		return nil
	}

	days := make([]affv1.Weekday, 0, len(r.Weekdays))
	for _, name := range r.Weekdays {
		if d, ok := weekdayProtos[name]; ok {
			days = append(days, d)
		}
	}

	var anchor *timestamppb.Timestamp
	if r.Anchor != "" {
		if t, err := time.Parse(feedspec.AnchorLayout, r.Anchor); err == nil {
			anchor = timestamppb.New(t)
		}
		// A malformed anchor is dropped rather than propagated: it means the
		// interval loses its phase, which the editor shows immediately in its
		// preview. Failing the whole conversion would make a feed unreadable
		// over one cosmetic field.
	}

	return &affv1.Recurrence{
		Frequency:   frequencyProtos[r.Frequency],
		Interval:    int32(r.Interval),
		Weekdays:    days,
		MonthDay:    int32(r.MonthDay),
		SetPosition: int32(r.SetPosition),
		Hour:        int32(r.Hour),
		Minute:      int32(r.Minute),
		Anchor:      anchor,
	}
}
