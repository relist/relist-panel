package panelupdate

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"
)

const (
	watchEvery = 2 * time.Second
	// watchIdle is how long the watcher sleeps once the newest run is over and
	// settled; a new run wakes it sooner.
	watchIdle = time.Minute
)

// ErrMigrationLocked is the reason the panel refuses a quiesce request while a
// server Migration holds its lock.
var ErrMigrationLocked = errors.New("a server Migration holds the lock; Panel update waits until it is over")

// UpdaterState is what the Engine says of an Updater Container.
type UpdaterState struct {
	// Exists is false when there is no Container of that name.
	Exists bool
	// Running is true while the Container can still write status.json.
	Running bool
}

// UpdaterContainers finds and removes the Updater Container by name;
// dockerexec implements it over the Engine API.
type UpdaterContainers interface {
	UpdaterState(ctx context.Context, name string) (UpdaterState, error)
	// RemoveUpdater removes a stopped Container; one already gone is not an
	// error.
	RemoveUpdater(ctx context.Context, name string) error
	// ListUpdaters lists every Container named relist-panel-updater-*, of any
	// run.
	ListUpdaters(ctx context.Context) ([]UpdaterListing, error)
	// StartUpdater creates and starts the Updater Container, the Engine API
	// equivalent of the spec's `docker run -d`.
	StartUpdater(ctx context.Context, spec UpdaterSpec) error
}

// UpdaterListing is one Updater Container found by name.
type UpdaterListing struct {
	Name    string
	Running bool
}

// UpdaterSpec is the Updater Container to start. It has no Compose labels, so
// `up` never treats it as an orphan of the project. The adapter adds the
// Docker socket.
type UpdaterSpec struct {
	Name  string
	Image string // the panel's own image, by ID
	Run   string // the run id: the command is `panel-update apply --run <Run>`
	// InstallDir and DataDir are mounted read-write at their own paths, and
	// DataDir is the Updater's RELIST_DATA_DIR.
	InstallDir string
	DataDir    string
	Network    string
}

// Watch follows the newest run until ctx is done: every 2 s while it is not
// over, and rarely once it is. A terminal state is copied to panel_updates, the
// lock goes off and the exited Updater Container goes. A run that is not over
// whose Updater Container is missing or exited is written as interrupted.
func (s *Service) Watch(ctx context.Context) {
	for {
		wait := watchEvery
		if s.watchOnce(ctx) {
			wait = watchIdle
		}
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-s.d.After(wait):
		}
	}
}

// watchOnce looks at the newest run once. It returns true when there is
// nothing left to do for it.
func (s *Service) watchOnce(ctx context.Context) (settled bool) {
	st, ok, err := NewestRun(s.d.DataDir)
	if err != nil {
		log.Printf("panelupdate: read the newest run: %v", err)
		return false
	}
	if !ok {
		return true
	}
	if IsTerminal(st.State) {
		s.dropQuiesce()
		return s.settle(ctx, st)
	}
	if s.checkUpdater(ctx, st) {
		s.dropQuiesce()
		return true
	}
	s.answerQuiesce(ctx, st)
	return false
}

// settle copies a finished run to panel_updates, releases the lock and removes
// the Updater Container. It returns false while any of that still has to be
// retried.
func (s *Service) settle(ctx context.Context, st RunStatus) bool {
	key := st.Run + "/" + st.State
	if s.settledKey() == key && !s.Locked() {
		return true
	}
	if err := s.d.Store.RecordRun(ctx, st, s.d.Now()); err != nil {
		log.Printf("panelupdate: record run %s: %v", st.Run, err)
		return false
	}
	s.unlock(ctx)
	if s.d.Updater != nil {
		name := UpdaterName(st.Run)
		state, err := s.d.Updater.UpdaterState(ctx, name)
		if err == nil && state.Exists && !state.Running {
			err = s.d.Updater.RemoveUpdater(ctx, name)
		}
		if err == nil && state.Exists && state.Running {
			return false // still exiting
		}
		if err != nil {
			log.Printf("panelupdate: remove %s: %v", name, err)
			return false
		}
	}
	s.setSettled(key)
	return true
}

// checkUpdater marks a run that is not over as interrupted when its Updater
// Container is gone or has exited.
func (s *Service) checkUpdater(ctx context.Context, st RunStatus) bool {
	if s.d.Updater == nil {
		return false
	}
	state, err := s.d.Updater.UpdaterState(ctx, UpdaterName(st.Run))
	if err != nil {
		log.Printf("panelupdate: inspect the Updater of run %s: %v", st.Run, err)
		return false
	}
	if state.Running {
		return false
	}
	// The Updater may have written its last state just before it exited.
	if again, ok, err := NewestRun(s.d.DataDir); err != nil || !ok || again.Run != st.Run || IsTerminal(again.State) {
		return false
	}
	reason := "the Updater Container is gone"
	if state.Exists {
		reason = "the Updater Container exited before the run was over"
	}
	st.State, st.Error = StateInterrupted, fmt.Sprintf("%s (last state: %s)", reason, st.State)
	if err := WriteStatus(RunDir(s.d.DataDir, st.Run), st, s.d.Now()); err != nil {
		log.Printf("panelupdate: write run %s as interrupted: %v", st.Run, err)
		return false
	}
	log.Printf("panelupdate: run %s interrupted: %s", st.Run, st.Error)
	return s.settle(ctx, st)
}

func (s *Service) settledKey() string {
	s.settleMu.Lock()
	defer s.settleMu.Unlock()
	return s.settled
}

func (s *Service) setSettled(key string) {
	s.settleMu.Lock()
	s.settled = key
	s.settleMu.Unlock()
}

// recordNewest copies the newest run to panel_updates when it is over.
func (s *Service) recordNewest(ctx context.Context) {
	st, ok, err := NewestRun(s.d.DataDir)
	if err != nil || !ok || !IsTerminal(st.State) {
		return
	}
	if err := s.d.Store.RecordRun(ctx, st, s.d.Now()); err != nil {
		log.Printf("panelupdate: record run %s: %v", st.Run, err)
	}
}
