package panelupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// ErrUpdateRunning refuses a quiesce while another run's lock is on.
var ErrUpdateRunning = errors.New("a Panel update is already running")

// ErrMigrationLocked refuses a quiesce while a server Migration holds its lock.
var ErrMigrationLocked = errors.New("a server Migration holds the lock; Panel update waits until it is over")

// Run is one update run that has quiesced and backed up. Its directory holds
// status.json and the backup; the Updater reads both.
type Run struct {
	ID  string
	Dir string
}

// Backup names the copies in a run directory, so the Updater can put every
// Install file back where it was. Paths are the Install's own.
type Backup struct {
	Config []BackupFile `json:"config"`
	// Env is nil when the Install had no .env.
	Env *BackupFile `json:"env"`
}

// BackupFile is one backed-up file: where it lives on the host, and its copy
// in the run directory.
type BackupFile struct {
	Path string `json:"path"`
	Copy string `json:"copy"`
}

const (
	backupDB   = "panel.db"
	backupMeta = "backup.json"
)

// DatabaseBackup is the path of the run's copy of panel.db.
func (r Run) DatabaseBackup() string { return filepath.Join(r.Dir, backupDB) }

// ReadBackup reads a run directory's backup.json.
func ReadBackup(dir string) (Backup, error) {
	var b Backup
	data, err := os.ReadFile(filepath.Join(dir, backupMeta))
	if err != nil {
		return b, err
	}
	return b, json.Unmarshal(data, &b)
}

// QuiesceAndBackup is Apply steps 1 and 2. It writes status.json as
// quiescing, turns the update lock on, waits for the running Deployment and
// scan task, writes backing-up, and backs up into the run's directory: panel.db
// (VACUUM INTO), every Install config file and .env. The previous runs'
// directories go only once this backup is complete.
//
// On success the lock stays on and the run is guarded against the watcher until
// the caller hands it over with MarkStarted, or gives up with AbortRun. On
// failure the run is already interrupted and the lock off.
func (s *Service) QuiesceAndBackup(ctx context.Context, in Install, from, to string) (Run, error) {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	if s.Locked() {
		return Run{}, ErrUpdateRunning
	}
	if s.d.MigrationLocked != nil {
		locked, err := s.d.MigrationLocked(ctx)
		if err != nil {
			return Run{}, fmt.Errorf("read the Migration state: %w", err)
		}
		if locked {
			return Run{}, ErrMigrationLocked
		}
	}
	now := s.d.Now()
	run := Run{ID: NewRunID(now)}
	run.Dir = RunDir(s.d.DataDir, run.ID)
	if err := os.MkdirAll(RunsDir(s.d.DataDir), 0o700); err != nil {
		return Run{}, err
	}
	// The watcher must leave the run alone while it has no Updater yet.
	s.setGuard(run.ID)
	if err := os.Mkdir(run.Dir, 0o700); err != nil {
		s.setGuard("")
		return Run{}, err
	}
	st := RunStatus{Run: run.ID, State: StateQuiescing, FromImage: in.Image, FromVersion: from, ToVersion: to, StartedAt: now.Unix()}
	if err := WriteStatus(run.Dir, st, now); err != nil {
		s.setGuard("")
		_ = os.RemoveAll(run.Dir)
		return Run{}, err
	}
	fail := func(err error) (Run, error) {
		s.AbortRun(run, err.Error())
		return Run{}, err
	}
	if err := s.lock(ctx); err != nil {
		return fail(fmt.Errorf("cancel the queued Deployments: %w", err))
	}
	if s.d.Queue != nil {
		if err := s.d.Queue.WaitIdle(ctx); err != nil {
			return fail(fmt.Errorf("wait for the running Deployment: %w", err))
		}
	}
	if s.d.Scans != nil {
		if err := s.d.Scans.WaitIdle(ctx); err != nil {
			return fail(fmt.Errorf("wait for the running scan: %w", err))
		}
	}
	if err := s.SetRunState(run, StateBackingUp, ""); err != nil {
		return fail(err)
	}
	if err := s.backup(ctx, run, in); err != nil {
		return fail(fmt.Errorf("back up: %w", err))
	}
	s.pruneRuns(run.ID)
	return run, nil
}

func (s *Service) backup(ctx context.Context, run Run, in Install) error {
	files, err := s.ReadInstallFiles(ctx, in)
	if err != nil {
		return err
	}
	if err := s.d.Store.VacuumInto(ctx, run.DatabaseBackup()); err != nil {
		return err
	}
	var meta Backup
	for i, path := range in.ConfigFiles {
		name := fmt.Sprintf("config-%d%s", i, filepath.Ext(path))
		if err := os.WriteFile(filepath.Join(run.Dir, name), files.Config[i], 0o600); err != nil {
			return err
		}
		meta.Config = append(meta.Config, BackupFile{Path: path, Copy: name})
	}
	if len(files.Env) > 0 {
		if err := os.WriteFile(filepath.Join(run.Dir, "env"), files.Env, 0o600); err != nil {
			return err
		}
		meta.Env = &BackupFile{Path: in.EnvFile(), Copy: "env"}
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(run.Dir, backupMeta), data, 0o600)
}

// pruneRuns removes every run directory but keep, which has just been backed
// up completely, and but the newest run that ended rollback-failed: its backup
// is what the manual restore commands point at. That one goes only once a
// later run has succeeded.
func (s *Service) pruneRuns(keep string) {
	ids, err := runIDs(s.d.DataDir)
	if err != nil {
		log.Printf("panelupdate: list old runs: %v", err)
		return
	}
	state := func(id string) string {
		st, err := ReadStatus(RunDir(s.d.DataDir, id))
		if err != nil {
			return ""
		}
		return st.State
	}
	// Walk from the newest: a succeeded run settles every older failure.
	retain, settled := "", false
	for i := len(ids) - 1; i >= 0; i-- {
		switch state(ids[i]) {
		case StateSucceeded:
			settled = true
		case StateRollbackFailed:
			if !settled && ids[i] != keep && retain == "" {
				retain = ids[i]
			}
		}
	}
	for _, id := range ids {
		if id == keep || id == retain {
			continue
		}
		if err := os.RemoveAll(RunDir(s.d.DataDir, id)); err != nil {
			log.Printf("panelupdate: remove run %s: %v", id, err)
		}
	}
}

// SetRunState rewrites the run's status.json with a new state, keeping the
// rest of it.
func (s *Service) SetRunState(run Run, state, errText string) error {
	st, err := ReadStatus(run.Dir)
	if err != nil {
		return err
	}
	st.Run, st.State, st.Error = run.ID, state, errText
	return WriteStatus(run.Dir, st, s.d.Now())
}

// MarkStarted hands a run over to its Updater: the watcher now treats a
// missing or exited Updater Container as a dead run.
func (s *Service) MarkStarted(run Run) {
	s.setGuard("")
	s.poke()
}

// AbortRun ends a run that never reached its Updater, or whose backup failed:
// it is written as interrupted and the lock goes off. Task 6 uses it when the
// Updater cannot start.
func (s *Service) AbortRun(run Run, reason string) {
	ctx := context.Background()
	if err := s.SetRunState(run, StateInterrupted, reason); err != nil {
		log.Printf("panelupdate: write run %s as interrupted: %v", run.ID, err)
	}
	s.recordNewest(ctx)
	s.unlock(ctx)
	s.setGuard("")
	s.poke()
}

func (s *Service) setGuard(run string) {
	s.guardMu.Lock()
	s.guard = run
	s.guardMu.Unlock()
}

func (s *Service) guarded(run string) bool {
	s.guardMu.Lock()
	defer s.guardMu.Unlock()
	return s.guard != "" && s.guard == run
}

func (s *Service) poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
