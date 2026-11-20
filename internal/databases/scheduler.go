package databases

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"slices"
	"time"

	"relist-panel/internal/safely"
	"relist-panel/internal/schedule"
)

// JobKindRotate is the job slot Rotation takes on a Database for the length
// of its deletes.
const JobKindRotate = "rotate"

const skippedNotRunning = "skipped: the Database was not running"

// RunScheduler checks each minute whether a Backup schedule is due, until ctx
// ends (the Registry cleaner's loop).
func (s *Service) RunScheduler(ctx context.Context) {
	s.runSchedulerEvery(ctx, time.Minute, s.tick)
}

// runSchedulerEvery runs tick, then again every interval, until ctx ends. A
// tick that panics is logged and the loop goes on (internal/safely).
func (s *Service) runSchedulerEvery(ctx context.Context, every time.Duration, tick func(context.Context)) {
	for {
		safely.Do("databases: backup schedules", func() { tick(ctx) })
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

// tryJob takes Database id's job slot without waiting; false when busy.
func (s *Service) tryJob(id int64, kind string) bool {
	return s.beginJob(id, kind) == nil
}

func ruleOf(sc Schedule) schedule.Rule { return schedule.Rule{Days: sc.Days, Minute: sc.Minute} }

// tick runs every enabled schedule that is due, one after another, then
// rotates. A schedule that cannot run now (a job on its Database, a Migration
// or Panel update lock) keeps its last_run_at, so a later tick tries again.
func (s *Service) tick(ctx context.Context) {
	list, err := s.d.Store.ListEnabledSchedules(ctx)
	if err != nil {
		log.Printf("databases: read backup schedules: %v", err)
		return
	}
	now := s.d.Now()
	for _, sc := range list {
		if ctx.Err() != nil {
			return
		}
		if sc.LastRunAt == nil {
			// Never ran: start from now instead of running at once.
			if err := s.d.Store.SetScheduleRun(ctx, sc.DatabaseID, now, sc.LastError); err != nil {
				log.Printf("databases: arm backup schedule: %v", err)
			}
			continue
		}
		if !schedule.Due(now, ruleOf(sc), sc.LastRunAt) || s.gate(ctx) != nil {
			continue
		}
		if !s.tryJob(sc.DatabaseID, JobKindBackup) {
			continue
		}
		s.tickOne(ctx, sc, now)
	}
	s.rotateAll(ctx)
}

// tickOne runs sc inside the job slot tick took, and frees it.
func (s *Service) tickOne(ctx context.Context, sc Schedule, now time.Time) {
	s.wg.Add(1)
	defer s.wg.Done()
	defer s.endJob(sc.DatabaseID)
	ctx = context.WithoutCancel(ctx)
	s.containRows(ctx, sc.DatabaseID, "scheduled backup", func() { s.runTick(ctx, sc, now) })
}

// runTick is tickOne's work, with the job slot held.
func (s *Service) runTick(ctx context.Context, sc Schedule, now time.Time) {
	d, err := s.d.Store.Get(ctx, sc.DatabaseID)
	if err != nil || d.DeletedAt != nil {
		return
	}
	if _, err := DumperFor(d.Engine); err != nil {
		return
	}
	st, err := s.d.Host.Inspect(ctx, ContainerName(d.ID))
	if err != nil {
		log.Printf("databases: scheduled backup of %s: %v", d.Name, err)
		return
	}
	if !st.Exists || !st.Running {
		if err := s.d.Store.SetScheduleRun(ctx, sc.DatabaseID, now, skippedNotRunning); err != nil {
			log.Printf("databases: record backup schedule: %v", err)
		}
		return
	}
	s.runScheduled(ctx, sc)
}

// runScheduled makes one scheduled Backup per database and rotates. It runs
// inside the job slot, synchronously. last_run_at is set before anything
// runs, so a panel that stops mid-run does not repeat it; last_error ends as
// "" or the first failure.
func (s *Service) runScheduled(ctx context.Context, sc Schedule) {
	now := s.d.Now()
	if err := s.d.Store.SetScheduleRun(ctx, sc.DatabaseID, now, ""); err != nil {
		log.Printf("databases: record backup schedule: %v", err)
		return
	}
	firstErr, made := s.scheduledBackups(ctx, sc)
	if sc.DestinationID != nil {
		// Copy first, so a fresh copy exists before old files go.
		if d, err := s.d.Store.Get(ctx, sc.DatabaseID); err == nil {
			for _, b := range made {
				if u := s.upload(ctx, d, b, *sc.DestinationID); u.Status == UploadFailed && firstErr == "" {
					firstErr = s.uploadFailure(ctx, u)
				}
			}
		}
	}
	if err := s.rotateLocal(ctx, sc.DatabaseID, sc.Local); err != nil {
		log.Printf("databases: rotate backups of database %d: %v", sc.DatabaseID, err)
	}
	if err := s.rotateRemote(ctx, sc.DatabaseID, sc); err != nil {
		log.Printf("databases: rotate remote backups of database %d: %v", sc.DatabaseID, err)
	}
	if firstErr != "" {
		if err := s.d.Store.SetScheduleRun(ctx, sc.DatabaseID, now, firstErr); err != nil {
			log.Printf("databases: record backup schedule: %v", err)
		}
	}
}

// scheduledBackups dumps what sc selects. It returns the first failure, ""
// if every Backup succeeded, and the Backups that succeeded.
func (s *Service) scheduledBackups(ctx context.Context, sc Schedule) (string, []Backup) {
	d, err := s.d.Store.Get(ctx, sc.DatabaseID)
	if err != nil {
		return err.Error(), nil
	}
	dp, err := DumperFor(d.Engine)
	if err != nil {
		return err.Error(), nil
	}
	have, err := s.listLogical(ctx, d, dp)
	if err != nil {
		return err.Error(), nil
	}
	names := have
	var gone []string
	if !sc.All {
		names = nil
		want := slices.Clone(sc.Databases)
		slices.Sort(want)
		for _, n := range slices.Compact(want) {
			if slices.Contains(have, n) {
				names = append(names, n)
			} else {
				gone = append(gone, n)
			}
		}
	}
	if len(names) == 0 && len(gone) == 0 {
		return ErrNothingToBackUp.Error(), nil
	}
	var firstErr string
	var made []Backup
	fail := func(msg string) {
		if firstErr == "" {
			firstErr = msg
		}
	}
	for _, n := range gone { // a selected database that no longer exists
		msg := ErrUnknownLogical{Names: []string{n}}.Error()
		if b, err := s.d.Store.InsertBackup(ctx, Backup{DatabaseID: d.ID, Trigger: TriggerScheduled, LogicalName: n}); err == nil {
			_ = s.d.Store.FinishBackup(ctx, b.ID, Backup{Status: JobFailed, Error: msg})
		}
		fail(msg)
	}
	for _, n := range names {
		if b := s.backupNow(ctx, d, dp, n, TriggerScheduled, nil); b.Status != JobSucceeded {
			fail(n + ": " + b.Error)
		} else {
			made = append(made, b)
		}
	}
	return firstErr, made
}

// rotateAll applies local Rotation for every enabled schedule whose Database
// is free this tick; a busy one waits for the next.
func (s *Service) rotateAll(ctx context.Context) {
	if s.gate(ctx) != nil {
		return
	}
	list, err := s.d.Store.ListEnabledSchedules(ctx)
	if err != nil {
		return
	}
	for _, sc := range list {
		if ctx.Err() != nil {
			return
		}
		if !s.tryJob(sc.DatabaseID, JobKindRotate) {
			continue
		}
		if err := s.rotateLocal(context.WithoutCancel(ctx), sc.DatabaseID, sc.Local); err != nil {
			log.Printf("databases: rotate backups of database %d: %v", sc.DatabaseID, err)
		}
		if err := s.rotateRemote(context.WithoutCancel(ctx), sc.DatabaseID, sc); err != nil {
			log.Printf("databases: rotate remote backups of database %d: %v", sc.DatabaseID, err)
		}
		s.endJob(sc.DatabaseID)
	}
}

// rotateLocal deletes the local files of Database id's scheduled Backups that
// break k. The row goes with the file, unless a copy in a Backup destination
// is still live: then the row stays, as Remote only.
func (s *Service) rotateLocal(ctx context.Context, id int64, k Keep) error {
	all, err := s.d.Store.ListBackups(ctx, id)
	if err != nil {
		return err
	}
	byID := map[int64]Backup{}
	var items []RotItem
	for _, b := range all {
		if b.Trigger != TriggerScheduled || b.Status == JobRunning || b.Pinned || b.LocalRemovedAt != nil {
			continue
		}
		byID[b.ID] = b
		items = append(items, RotItem{ID: b.ID, Group: b.LogicalName, At: b.StartedAt, OK: b.Status == JobSucceeded})
	}
	var errs []error
	for _, bid := range Rotate(items, k, s.d.Now()) {
		b := byID[bid]
		if b.FileName != "" {
			if err := os.Remove(filepath.Join(s.dbDir(id), "backups", filepath.Base(b.FileName))); err != nil && !os.IsNotExist(err) {
				errs = append(errs, err)
				continue
			}
		}
		// A live copy in a destination keeps the row, as Remote only: deleting
		// the row would lose track of the object.
		live, err := s.d.Store.LiveUpload(ctx, bid)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if live != nil {
			if err := s.d.Store.SetLocalRemoved(ctx, bid, s.d.Now()); err != nil {
				errs = append(errs, err)
			}
			continue
		}
		if err := s.d.Store.DeleteBackup(ctx, bid); err != nil && !errors.Is(err, ErrNotFound) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
