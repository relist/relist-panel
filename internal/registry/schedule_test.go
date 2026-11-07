package registry

import (
	"testing"
	"time"
)

func TestDueWeekdaysAndMakeUp(t *testing.T) {
	loc := time.UTC
	// 2026-11-09 is a Monday.
	mon3 := time.Date(2026, 11, 9, 3, 0, 0, 0, loc)
	weekly := CleanupSettings{Enabled: true, Keep: 1, Days: []string{"mon", "thu"}, Minute: 180}
	if !Due(mon3, weekly) {
		t.Fatal("Monday 03:00 with mon listed should be due")
	}
	if Due(mon3.Add(-time.Minute), weekly) {
		t.Fatal("before the time is not due")
	}
	tue := mon3.AddDate(0, 0, 1)
	if Due(tue, weekly) {
		t.Fatal("Tuesday is not listed")
	}
	// Panel was down at 03:00 Monday: made up once at 09:00.
	later := mon3.Add(6 * time.Hour)
	if !Due(later, weekly) {
		t.Fatal("missed Monday run should be made up")
	}
	ran := later
	weekly.LastRunAt = &ran
	if Due(later.Add(time.Hour), weekly) {
		t.Fatal("made up only once")
	}
	if Due(mon3, CleanupSettings{Enabled: false, Days: []string{"daily"}, Minute: 180}) {
		t.Fatal("disabled is never due")
	}
	daily := CleanupSettings{Enabled: true, Days: []string{"daily"}, Minute: 180}
	if !Due(tue, daily) {
		t.Fatal("daily is due every day")
	}
}

func TestNextSkipsUnlistedDays(t *testing.T) {
	tue := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	next := Next(tue, CleanupSettings{Enabled: true, Days: []string{"mon", "thu"}, Minute: 180})
	if next == nil || !next.Equal(time.Date(2026, 11, 12, 3, 0, 0, 0, time.UTC)) {
		t.Fatalf("next = %v, want Thursday 03:00", next)
	}
	daily := Next(tue, CleanupSettings{Enabled: true, Days: []string{"daily"}, Minute: 180})
	if daily == nil || !daily.Equal(time.Date(2026, 11, 11, 3, 0, 0, 0, time.UTC)) {
		t.Fatalf("daily next = %v", daily)
	}
	if Next(tue, CleanupSettings{Days: []string{"daily"}}) != nil {
		t.Fatal("disabled has no next")
	}
}

// On the day clocks spring forward, 03:00 is still 03:00 on the wall.
func TestScheduleTimeOnDSTDay(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	morning := time.Date(2026, 3, 8, 1, 0, 0, 0, ny) // clocks jump 02:00 → 03:00
	next := Next(morning, CleanupSettings{Enabled: true, Days: []string{"daily"}, Minute: 180})
	if next == nil || next.Hour() != 3 || next.Minute() != 0 || next.Day() != 8 {
		t.Fatalf("next = %v, want 03:00 that day", next)
	}
}
