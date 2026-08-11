package settings

import (
	"fmt"
	"testing"
	"time"
)

func TestScaleBytes(t *testing.T) {
	cases := []struct {
		name      string
		n         int64
		wantUnit  ByteUnit
		wantValue float64
	}{
		{"negative clamps to zero bytes", -5, UnitBytes, 0},
		{"zero", 0, UnitBytes, 0},
		{"under 1KB stays bytes", 1023, UnitBytes, 1023},
		{"exactly 1KB", 1024, UnitKB, 1},
		{"1.5MB", 1024 * 1024 * 3 / 2, UnitMB, 1.5},
		{"1GB", 1024 * 1024 * 1024, UnitGB, 1},
		{"large value caps at TB", 5 * 1024 * 1024 * 1024 * 1024, UnitTB, 5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ScaleBytes(c.n)
			if got.Unit != c.wantUnit || got.Value != c.wantValue {
				t.Errorf("ScaleBytes(%d) = {%v %v}, want {%v %v}", c.n, got.Value, got.Unit, c.wantValue, c.wantUnit)
			}
		})
	}
}

func TestFormatByteSize_UsesInjectedFormatter(t *testing.T) {
	got := FormatByteSize(1024, func(v float64) string { return fmt.Sprintf("N=%.0f", v) })
	want := "N=1 KB"
	if got != want {
		t.Errorf("FormatByteSize = %q, want %q", got, want)
	}
}

func TestBreakDuration(t *testing.T) {
	cases := []struct {
		name string
		d    time.Duration
		want DurationParts
	}{
		{"zero", 0, DurationParts{}},
		{"negative clamps to zero", -time.Hour, DurationParts{}},
		{"minutes only", 45 * time.Minute, DurationParts{Minutes: 45}},
		{"hours and minutes", 2*time.Hour + 30*time.Minute, DurationParts{Hours: 2, Minutes: 30}},
		{"days hours minutes", 25*time.Hour + 10*time.Minute, DurationParts{Days: 1, Hours: 1, Minutes: 10}},
		{"sub-minute precision discarded", 90 * time.Second, DurationParts{Minutes: 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := BreakDuration(c.d); got != c.want {
				t.Errorf("BreakDuration(%v) = %+v, want %+v", c.d, got, c.want)
			}
		})
	}
}

func TestEstimateVacuumDuration(t *testing.T) {
	cases := []struct {
		name string
		n    int64
		want VacuumDurationEstimate
	}{
		{"negative clamps to zero, brief", -5, VacuumEstimateBrief},
		{"zero is brief", 0, VacuumEstimateBrief},
		{"just under 100MB is brief", 100*1024*1024 - 1, VacuumEstimateBrief},
		{"exactly 100MB is moderate", 100 * 1024 * 1024, VacuumEstimateModerate},
		{"just under 1GB is moderate", 1024*1024*1024 - 1, VacuumEstimateModerate},
		{"exactly 1GB is long", 1024 * 1024 * 1024, VacuumEstimateLong},
		{"multi-GB is long", 5 * 1024 * 1024 * 1024, VacuumEstimateLong},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := EstimateVacuumDuration(c.n); got != c.want {
				t.Errorf("EstimateVacuumDuration(%d) = %v, want %v", c.n, got, c.want)
			}
		})
	}
}

func TestBreakVacuumDuration(t *testing.T) {
	cases := []struct {
		name string
		d    time.Duration
		want VacuumDurationParts
	}{
		{"zero", 0, VacuumDurationParts{}},
		{"negative clamps to zero", -time.Second, VacuumDurationParts{}},
		{"seconds only", 45 * time.Second, VacuumDurationParts{Seconds: 45}},
		{"minutes and seconds", 2*time.Minute + 5*time.Second, VacuumDurationParts{Minutes: 2, Seconds: 5}},
		{"sub-second precision discarded", 1500 * time.Millisecond, VacuumDurationParts{Seconds: 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := BreakVacuumDuration(c.d); got != c.want {
				t.Errorf("BreakVacuumDuration(%v) = %+v, want %+v", c.d, got, c.want)
			}
		})
	}
}

func TestUptime_ClampsFutureStart(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)
	if got := Uptime(future, now); got != 0 {
		t.Errorf("Uptime with a start time after now = %v, want 0", got)
	}
	past := now.Add(-2 * time.Hour)
	if got := Uptime(past, now); got != 2*time.Hour {
		t.Errorf("Uptime = %v, want 2h", got)
	}
}
