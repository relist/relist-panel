package databases

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrBusy       = errors.New("another backup, restore or update is running on this Database")
	ErrNotRunning = errors.New("start the Database first")
	ErrNoScope    = errors.New("choose all databases or at least one")
)

// ErrUnknownLogical names the selected logical databases that do not exist.
type ErrUnknownLogical struct{ Names []string }

func (e ErrUnknownLogical) Error() string { return "no such database: " + strings.Join(e.Names, ", ") }

// The jobs a Database can be running.
const (
	JobKindBackup      = "backup"
	JobKindRestore     = "restore"
	JobKindUpdateImage = "update_image"
	// JobKindDropDatabase is a drop of a logical database with Back up first.
	JobKindDropDatabase = "drop_database"
	// JobKindResetPassword is a superuser password reset.
	JobKindResetPassword = "reset_password"
	// JobKindChangeVersion is a Change version to a higher major version.
	JobKindChangeVersion = "change_version"
)

// interrupted is the error of a job the panel lost by stopping.
const interrupted = "the panel stopped during this run"

// beginJob takes Database id's job slot, or ErrBusy.
func (s *Service) beginJob(id int64, kind string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.jobs[id] != "" {
		return ErrBusy
	}
	s.jobs[id] = kind
	return nil
}

func (s *Service) endJob(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.jobs, id)
	close(s.jobsChanged)
	s.jobsChanged = make(chan struct{})
}

// Jobs names the job running on Database id: "", "backup", "restore",
// "update_image", "drop_database", "reset_password" or "change_version".
func (s *Service) Jobs(id int64) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.jobs[id]
}

// Busy reports whether any Database has a job running.
func (s *Service) Busy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.jobs) > 0
}

// WaitIdle blocks until no Database has a job running, or ctx ends. A Panel
// update's quiesce and a server Migration wait on it; the update lock and the
// Migration lock keep new jobs from starting.
func (s *Service) WaitIdle(ctx context.Context) error {
	for {
		s.mu.Lock()
		if len(s.jobs) == 0 {
			s.mu.Unlock()
			return nil
		}
		changed := s.jobsChanged
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

// ErrStarting refuses a new job until the start-up recovery has run.
var ErrStarting = errors.New("the panel is still starting; try again in a moment")

// HoldJobs refuses every new job with ErrStarting until Recover ends. The
// server calls it before its routes go live: Startup fails every job still
// marked running and the sweep removes the volumes of changes it failed, so
// a job started before them would be failed under its own feet.
func (s *Service) HoldJobs() { s.holding.Store(true) }

// Recover is the start-up recovery: Startup, ReconcileAll, then the sweep
// of failed version changes' volumes. It logs what fails and lets jobs
// start again at the end, whatever happened.
func (s *Service) Recover(ctx context.Context) {
	defer s.holding.Store(false)
	if err := s.Startup(ctx); err != nil {
		log.Printf("databases: finish interrupted jobs: %v", err)
	}
	if err := s.ReconcileAll(ctx); err != nil {
		log.Printf("databases: reconcile at start: %v", err)
	}
	if err := s.SweepFailedVersionVolumes(ctx); err != nil {
		log.Printf("databases: sweep failed version changes: %v", err)
	}
}

// gate refuses a new job while a server Migration or a Panel update holds
// its lock.
func (s *Service) gate(ctx context.Context) error {
	if s.holding.Load() {
		return ErrStarting
	}
	if s.locked(ctx) || (s.d.UpdateLocked != nil && s.d.UpdateLocked()) {
		return ErrLocked
	}
	return nil
}

func (s *Service) dbDir(id int64) string {
	return filepath.Join(s.d.DataDir, ".databases", fmt.Sprint(id))
}

// Startup fails every Backup and restore a restart interrupted, and removes
// their half-written files. It finishes an in-place version change cut off
// after its switch and fails every other. Call it before ReconcileAll.
func (s *Service) Startup(ctx context.Context) error {
	if _, err := s.d.Store.FailRunning(ctx, interrupted); err != nil {
		return err
	}
	if err := s.d.Store.FailRunningUploads(ctx, interrupted); err != nil {
		return err
	}
	// An in-place change past its switch is finished; any other still
	// names the old version and volume in the record, so ReconcileAll
	// recreates the old container and SweepFailedVersionVolumes then removes
	// the half-made volume.
	if err := s.resumeInterrupted(ctx); err != nil {
		return err
	}
	if err := s.d.Store.FailRunningVersionChanges(ctx, interrupted); err != nil {
		return err
	}
	if s.d.DataDir == "" {
		return nil
	}
	var errs []error
	for _, pattern := range []string{"backups/*.partial", "backups/*.age.tmp", "uploads/*"} {
		files, err := filepath.Glob(filepath.Join(s.d.DataDir, ".databases", "*", pattern))
		if err != nil {
			return err
		}
		for _, f := range files {
			if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}
