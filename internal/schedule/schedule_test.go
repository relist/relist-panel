package schedule

import (
	"slices"
	"testing"
	"time"
)

func TestDueWeekdaysAndMakeUp(t *testing.T) {
	loc := time.UTC
	// 2026-11-09 is a Monday.
	mon3 := time.Date(2026, 11, 9, 3, 0, 0, 0, loc)
	weekly := Rule{Days: []string{"mon", "thu"}, Minute: 180}
	never := (*time.Time)(nil)
	last := never
	yesterday := mon3.AddDate(0, 0, -1)
	last = &yesterday
	if !Due(mon3, weekly, last) {
		t.Fatal("Monday 03:00 with mon listed should be due")
	}
	if Due(mon3.Add(-time.Minute), weekly, last) {
		t.Fatal("before the time is not due")
	}
	tue := mon3.AddDate(0, 0, 1)
	if Due(tue, weekly, last) {
		t.Fatal("Tuesday is not listed")
	}
	// Panel was down at 03:00 Monday: made up once at 09:00.
	later := mon3.Add(6 * time.Hour)
	if !Due(later, weekly, last) {
		t.Fatal("missed Monday run should be made up")
	}
	ran := later
	if Due(later.Add(time.Hour), weekly, &ran) {
		t.Fatal("made up only once")
	}
	if Due(mon3, weekly, never) {
		t.Fatal("a schedule that never ran is armed by the caller, never due")
	}
	daily := Rule{Days: []string{"daily"}, Minute: 180}
	if !Due(tue, daily, last) {
		t.Fatal("daily is due every day")
	}
}

func TestNextSkipsUnlistedDays(t *testing.T) {
	tue := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	next := Next(tue, Rule{Days: []string{"mon", "thu"}, Minute: 180})
	if next == nil || !next.Equal(time.Date(2026, 11, 12, 3, 0, 0, 0, time.UTC)) {
		t.Fatalf("next = %v, want Thursday 03:00", next)
	}
	daily := Next(tue, Rule{Days: []string{"daily"}, Minute: 180})
	if daily == nil || !daily.Equal(time.Date(2026, 11, 11, 3, 0, 0, 0, time.UTC)) {
		t.Fatalf("daily next = %v", daily)
	}
	if Next(tue, Rule{}) != nil {
		t.Fatal("no days has no next")
	}
}

// On the day clocks spring forward, 03:00 is still 03:00 on the wall.
func TestScheduleTimeOnDSTDay(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	morning := time.Date(2026, 3, 8, 1, 0, 0, 0, ny) // clocks jump 02:00 → 03:00
	next := Next(morning, Rule{Days: []string{"daily"}, Minute: 180})
	if next == nil || next.Hour() != 3 || next.Minute() != 0 || next.Day() != 8 {
		t.Fatalf("next = %v, want 03:00 that day", next)
	}
}

// A time inside the hour skipped at spring-forward runs when the gap ends.
func TestScheduleTimeInTheSkippedHour(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	morning := time.Date(2026, 3, 8, 0, 30, 0, 0, ny) // clocks jump 02:00 → 03:00
	next := Next(morning, Rule{Days: []string{"daily"}, Minute: 150})
	if next == nil || next.Hour() != 3 || next.Minute() != 0 || next.Day() != 8 {
		t.Fatalf("next = %v, want 03:00 that day", next)
	}
	yesterdayNY := morning.AddDate(0, 0, -1)
	// Before the gap ends it is not due yet.
	if Due(time.Date(2026, 3, 8, 1, 45, 0, 0, ny), Rule{Days: []string{"daily"}, Minute: 150}, &yesterdayNY) {
		t.Fatal("due at 01:45, before the skipped 02:30")
	}
}

func TestNormalizeDays(t *testing.T) {
	for _, c := range []struct {
		in   []string
		want []string
		err  string
	}{
		{in: []string{"thu", "mon", "mon"}, want: []string{"mon", "thu"}},
		{in: []string{"mon", "daily"}, want: []string{"daily"}},
		{in: []string{"daily", "mon"}, want: []string{"daily"}},
		{in: []string{}, err: "choose Daily or at least one weekday"},
		{in: []string{"sat", "xyz"}, err: "days must be daily or weekdays mon to sun"},
	} {
		got, err := NormalizeDays(c.in)
		if c.err != "" {
			if err == nil || err.Error() != c.err {
				t.Errorf("NormalizeDays(%v) error = %v, want %q", c.in, err, c.err)
			}
			continue
		}
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("NormalizeDays(%v) = %v, %v; want %v", c.in, got, err, c.want)
		}
	}
}

func TestTimeZone(t *testing.T) {
	if got := TimeZone(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); got != "UTC (UTC+00:00)" {
		t.Fatalf("TimeZone = %q", got)
	}
}
