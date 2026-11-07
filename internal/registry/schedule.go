package registry

import (
	"slices"
	"strings"
	"time"
)

// dayMoment is the cleanup time on day's date, in day's location.
func dayMoment(day time.Time, minute int) time.Time {
	// The wall-clock time, so a DST change day keeps the time set. A time in
	// the hour skipped at spring-forward does not exist (Go would pick either
	// side of the gap): it runs at the first minute after the gap, as cron does.
	y, m, d := day.Date()
	for at := minute; at < 24*60; at++ {
		t := time.Date(y, m, d, at/60, at%60, 0, 0, day.Location())
		if t.Hour()*60+t.Minute() == at {
			return t
		}
	}
	return time.Date(y, m, d, minute/60, minute%60, 0, 0, day.Location())
}

// listed reports whether cleanup runs on day: every day for "daily", else
// on the listed weekdays ("mon".."sun").
func listed(day time.Time, days []string) bool {
	return slices.Contains(days, "daily") || slices.Contains(days, strings.ToLower(day.Weekday().String()[:3]))
}

// Due reports whether today's run has come and not yet happened. A run the
// panel missed while down is made up once, and only on a listed day (as the
// Scanner's daily run, vulnscan/scheduler.go).
func Due(now time.Time, c CleanupSettings) bool {
	if !c.Enabled || !listed(now, c.Days) {
		return false
	}
	moment := dayMoment(now, c.Minute)
	return !now.Before(moment) && (c.LastRunAt == nil || c.LastRunAt.Before(moment))
}

// Next is when the next scheduled run is, nil when cleanup is off.
func Next(now time.Time, c CleanupSettings) *time.Time {
	if !c.Enabled {
		return nil
	}
	for i := 0; i <= 7; i++ {
		day := now.AddDate(0, 0, i)
		if moment := dayMoment(day, c.Minute); listed(day, c.Days) && moment.After(now) {
			return &moment
		}
	}
	return nil
}
