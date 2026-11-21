package databases

import (
	"context"
	"errors"
	"fmt"
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

// Jobs names the job running on Database id: "", "backup", "restore" or
// "update_image".
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

// gate refuses a new job while a server Migration or a Panel update holds
// its lock.
func (s *Service) gate(ctx context.Context) error {
	if s.locked(ctx) || (s.d.UpdateLocked != nil && s.d.UpdateLocked()) {
		return ErrLocked
	}
	return nil
}

func (s *Service) dbDir(id int64) string {
	return filepath.Join(s.d.DataDir, ".databases", fmt.Sprint(id))
}

// Startup fails every Backup and restore a restart interrupted, and removes
// their half-written files. Call it before ReconcileAll.
func (s *Service) Startup(ctx context.Context) error {
	if _, err := s.d.Store.FailRunning(ctx, interrupted); err != nil {
		return err
	}
	if s.d.DataDir == "" {
		return nil
	}
	var errs []error
	for _, pattern := range []string{"backups/*.partial", "uploads/*"} {
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
