// Package schedule holds the day-and-time rules shared by the Registry
// cleanup and the Database backup schedules.
package schedule

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Rule is when a run is wanted: Days is ["daily"] or weekdays ("mon".."sun"),
// Minute the minute of the day in the panel's time zone.
type Rule struct {
	Days   []string
	Minute int
}

// Weekdays lists the weekday names in order.
var Weekdays = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

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
// Scanner's daily run, vulnscan/scheduler.go). A nil lastRunAt is never due:
// the caller arms the schedule instead.
func Due(now time.Time, r Rule, lastRunAt *time.Time) bool {
	if lastRunAt == nil || !listed(now, r.Days) {
		return false
	}
	moment := dayMoment(now, r.Minute)
	return !now.Before(moment) && lastRunAt.Before(moment)
}

// Next is when the next run is after now, nil when no listed day matches.
func Next(now time.Time, r Rule) *time.Time {
	for i := 0; i <= 7; i++ {
		day := now.AddDate(0, 0, i)
		if moment := dayMoment(day, r.Minute); listed(day, r.Days) && moment.After(now) {
			return &moment
		}
	}
	return nil
}

// NormalizeDays validates days: "daily" wins over weekdays, the rest are
// deduplicated and sorted mon to sun.
func NormalizeDays(in []string) ([]string, error) {
	if len(in) == 0 {
		return nil, errors.New("choose Daily or at least one weekday")
	}
	days := []string{}
	for _, d := range in {
		switch {
		case d == "daily":
			days = []string{"daily"}
		case slices.Contains(Weekdays, d):
			if !slices.Contains(days, d) && !slices.Contains(days, "daily") {
				days = append(days, d)
			}
		default:
			return nil, errors.New("days must be daily or weekdays mon to sun")
		}
	}
	slices.SortFunc(days, func(x, y string) int { return slices.Index(Weekdays, x) - slices.Index(Weekdays, y) })
	return days, nil
}

// TimeZone names the zone the schedule's time is in, as the Scanner's page
// does: the panel container's, which is UTC unless TZ is set.
func TimeZone(now time.Time) string {
	zone, offset := now.Zone()
	minutes := offset % 3600 / 60
	if minutes < 0 {
		minutes = -minutes
	}
	return fmt.Sprintf("%s (UTC%+03d:%02d)", zone, offset/3600, minutes)
}
