package vulnscan

import (
	"context"
	"time"
)

// dailyMoment is today's daily run time in now's location.
func dailyMoment(now time.Time, minute int) time.Time {
	y, m, d := now.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, now.Location()).Add(time.Duration(minute) * time.Minute)
}

// dueDaily reports whether today's daily run has come and not yet happened.
// A run the panel missed while it was down is therefore made up once.
func dueDaily(now time.Time, st Settings) bool {
	if !st.Enabled || !st.Daily {
		return false
	}
	moment := dailyMoment(now, st.DailyMinute)
	return !now.Before(moment) && (st.LastDailyAt == nil || st.LastDailyAt.Before(moment))
}

// NextDaily is when the next daily run is due, nil when there is none.
func NextDaily(now time.Time, st Settings) *time.Time {
	if !st.Enabled || !st.Daily {
		return nil
	}
	next := dailyMoment(now, st.DailyMinute)
	if !next.After(now) {
		next = dailyMoment(now.AddDate(0, 0, 1), st.DailyMinute)
	}
	return &next
}

// RunScheduler checks once a minute whether the daily run is due, until ctx
// is done.
func (s *Service) RunScheduler(ctx context.Context) {
	for {
		s.tickDaily(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Minute):
		}
	}
}

func (s *Service) tickDaily(ctx context.Context) {
	st, err := s.d.Store.Settings(ctx)
	if err != nil {
		logf("read settings: %v", err)
		return
	}
	if !dueDaily(s.d.Now(), st) {
		return
	}
	if err := s.runDaily(ctx); err != nil {
		logf("daily run: %v", err)
	}
}

// runDaily refreshes the DB, queues every image in use, and drops the
// results of images nothing uses any more.
func (s *Service) runDaily(ctx context.Context) error {
	now := s.d.Now()
	if _, err := s.d.Store.Enqueue(ctx, KindDBUpdate, "", ReasonDaily, now); err != nil {
		return err
	}
	used, err := s.UsedImages(ctx)
	if err != nil {
		return err
	}
	keep := make([]string, 0, len(used))
	for _, image := range used {
		keep = append(keep, image.ImageID)
		if _, err := s.d.Store.Enqueue(ctx, KindImage, image.ImageID, ReasonDaily, now); err != nil {
			return err
		}
	}
	if _, err := s.d.Store.PruneExcept(ctx, keep); err != nil {
		return err
	}
	s.wakeUp()
	return s.d.Store.SetLastDaily(ctx, now)
}
