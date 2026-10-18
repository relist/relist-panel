package migration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"relist-panel/internal/composes"
	"relist-panel/internal/config"
	"relist-panel/internal/domains"
)

// reasonMigration is the reason recorded on every Deployment a Migration
// cancels.
const reasonMigration = "server migration"

// UnstartedError reports Composes that did not start again on Resume. The
// lock is cleared all the same.
type UnstartedError struct{ Err error }

func (e *UnstartedError) Error() string { return e.Err.Error() }
func (e *UnstartedError) Unwrap() error { return e.Err }

// ErrNotReady refuses an action that needs the manifest before it exists.
var ErrNotReady = errors.New("the migration source is not ready yet")

// QueueControl is the part of the deployment queue the lock drives.
type QueueControl interface {
	CancelAllQueued(ctx context.Context, reason string) (int, error)
	WaitIdle(ctx context.Context) error
	Running() (composeID int64, number int, ok bool)
}

// StatusReader reports a Compose's Containers; composes.ComposeRunner has it.
type StatusReader interface {
	Status(ctx context.Context, req composes.RunRequest) (composes.ComposeStatus, error)
}

// DomainLister lists every panel-managed Domain; *domains.Store has it.
type DomainLister interface {
	ListAll(ctx context.Context) ([]domains.Domain, error)
}

type SourceDeps struct {
	Store    *Store
	Builder  Builder
	Queue    QueueControl
	Docker   Docker
	Status   StatusReader
	Composes ComposeTargets
	Domains  DomainLister
	Now      func() time.Time

	// DB is the panel database the database item copies; DataDir is the data
	// directory the data-dir item packs. ReportedDataDir is the path preflight
	// reports, DataDir when empty; only the in-process integration test sets
	// it. Install is what preflight reports of the install's .env.
	DB              *sql.DB
	DataDir         string
	ReportedDataDir string
	Install         config.InstallSettings
}

// Source is the old host's side of a Migration.
type Source struct {
	d SourceDeps

	// run serialises the lock sequence: Start, ResumeLock and Resume.
	run sync.Mutex

	// cancelStart cancels the Start in flight, so Resume never waits for a
	// running Deployment. Guarded by mu.
	cancelStart context.CancelFunc

	// startMu guards the background Start the start route launches.
	startMu  sync.Mutex
	starting bool
	startErr error

	// mu guards the cached lock, which the middleware reads on every request.
	mu      sync.Mutex
	loaded  bool
	step    Step
	target  string
	digests map[string]string
}

func NewSource(d SourceDeps) *Source {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Source{d: d, digests: map[string]string{}}
}

func (s *Source) setLock(ctx context.Context, step Step, st LockState) error {
	if err := s.d.Store.SetLock(ctx, step, st); err != nil {
		return err
	}
	s.mu.Lock()
	s.loaded, s.step, s.target = true, step, st.TargetURL
	s.mu.Unlock()
	return nil
}

// cached returns the lock step and target URL, reading the database once.
func (s *Source) cached(ctx context.Context) (Step, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded {
		step, st, err := s.d.Store.Lock(ctx)
		if err != nil {
			return StepNone, "", err
		}
		s.loaded, s.step, s.target = true, step, st.TargetURL
	}
	return s.step, s.target, nil
}

// Start locks the old host and returns the manifest. Every step is persisted
// before it acts, so a second call, or ResumeLock after a restart, carries on
// from where the last one stopped. hostPaths is used by the first call only.
func (s *Source) Start(ctx context.Context, hostPaths []string) (Manifest, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.mu.Lock()
	s.cancelStart = cancel
	s.mu.Unlock()
	s.run.Lock()
	defer s.run.Unlock()
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	return s.start(ctx, hostPaths)
}

func (s *Source) start(ctx context.Context, hostPaths []string) (Manifest, error) {
	step, st, err := s.d.Store.Lock(ctx)
	if err != nil {
		return Manifest{}, err
	}
	if step == StepNone {
		_, claimedBy, err := s.d.Store.TokenStatus(ctx)
		if err != nil {
			return Manifest{}, err
		}
		st = LockState{HostPaths: hostPaths, ClaimedBy: claimedBy, LockedAt: s.d.Now().UTC().Unix()}
		if st.HostPaths == nil {
			st.HostPaths = []string{}
		}
		step = StepLocked
		if err := s.setLock(ctx, step, st); err != nil {
			return Manifest{}, err
		}
	}
	if step == StepLocked {
		if _, err := s.d.Queue.CancelAllQueued(ctx, reasonMigration); err != nil {
			return Manifest{}, fmt.Errorf("cancel queued deployments: %w", err)
		}
		step = StepStopping
		if err := s.setLock(ctx, step, st); err != nil {
			return Manifest{}, err
		}
	}
	if step == StepStopping {
		if err := s.d.Queue.WaitIdle(ctx); err != nil {
			return Manifest{}, err
		}
		if st.RunningComposeIDs == nil {
			running, err := s.runningComposes(ctx)
			if err != nil {
				return Manifest{}, err
			}
			st.RunningComposeIDs = running
			if err := s.setLock(ctx, step, st); err != nil {
				return Manifest{}, err
			}
		}
		if err := s.stopComposes(ctx, st.RunningComposeIDs); err != nil {
			return Manifest{}, err
		}
		step = StepMaintenance
		if err := s.setLock(ctx, step, st); err != nil {
			return Manifest{}, err
		}
	}
	if step == StepMaintenance {
		if err := s.ensureMaintenance(ctx, true); err != nil {
			return Manifest{}, err
		}
		m, err := s.d.Builder.Build(ctx, st.RunningComposeIDs, st.HostPaths)
		if err != nil {
			return Manifest{}, fmt.Errorf("build manifest: %w", err)
		}
		st.Manifest = &m
		step = StepReady
		if err := s.setLock(ctx, step, st); err != nil {
			return Manifest{}, err
		}
	}
	if st.Manifest == nil {
		return Manifest{}, fmt.Errorf("migration lock at %q has no manifest", step)
	}
	return *st.Manifest, nil
}

// runningComposes lists the Composes with running Containers, never nil so
// an empty list still records that the check ran.
func (s *Source) runningComposes(ctx context.Context) ([]int64, error) {
	ids, err := s.d.Builder.composeIDs(ctx)
	if err != nil {
		return nil, err
	}
	running := []int64{}
	for _, id := range ids {
		target, err := s.d.Composes.Target(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("compose %d: %w", id, err)
		}
		status, err := s.d.Status.Status(ctx, target.Request)
		if err != nil {
			// A never-deployed Compose has no file for `ps` to read.
			continue
		}
		if status.Running > 0 {
			running = append(running, id)
		}
	}
	return running, nil
}

func (s *Source) stopComposes(ctx context.Context, ids []int64) error {
	for _, id := range ids {
		target, err := s.d.Composes.Target(ctx, id)
		if err != nil {
			return fmt.Errorf("compose %d: %w", id, err)
		}
		if err := s.d.Docker.ComposeStop(ctx, target.Request); err != nil {
			return fmt.Errorf("stop %s: %w", target.Compose.Name, err)
		}
	}
	return nil
}

// ensureMaintenance starts the maintenance container when there are Domains
// to cover, replacing a running one when force is set.
func (s *Source) ensureMaintenance(ctx context.Context, force bool) error {
	ds, err := s.d.Domains.ListAll(ctx)
	if err != nil {
		return fmt.Errorf("list domains: %w", err)
	}
	if len(ds) == 0 {
		return nil
	}
	if !force {
		up, err := s.d.Docker.MaintenanceRunning(ctx)
		if err != nil || up {
			return err
		}
	}
	if err := s.d.Docker.RunMaintenance(ctx, MaintenanceLabels(ds)); err != nil {
		return fmt.Errorf("start the maintenance page: %w", err)
	}
	return nil
}

// ResumeLock runs at boot. An interrupted Start carries on; a finished one
// makes sure the maintenance page is up and nothing was restarted.
func (s *Source) ResumeLock(ctx context.Context) error {
	s.run.Lock()
	defer s.run.Unlock()
	step, st, err := s.d.Store.Lock(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.loaded, s.step, s.target = true, step, st.TargetURL
	s.mu.Unlock()
	switch step {
	case StepNone:
		return nil
	case StepLocked, StepStopping, StepMaintenance:
		_, err := s.start(ctx, st.HostPaths)
		return err
	}
	if err := s.ensureMaintenance(ctx, false); err != nil {
		return err
	}
	for _, id := range st.RunningComposeIDs {
		target, err := s.d.Composes.Target(ctx, id)
		if err != nil {
			return fmt.Errorf("compose %d: %w", id, err)
		}
		if status, err := s.d.Status.Status(ctx, target.Request); err == nil && status.Running > 0 {
			if err := s.d.Docker.ComposeStop(ctx, target.Request); err != nil {
				return fmt.Errorf("stop %s: %w", target.Compose.Name, err)
			}
		}
	}
	return nil
}

// Resume is Cancel migration before finish and Resume this server after it:
// the maintenance page goes, the Composes that were running start again, and
// the lock, token and session are cleared.
func (s *Source) Resume(ctx context.Context) error {
	s.mu.Lock()
	if s.cancelStart != nil {
		s.cancelStart()
	}
	s.mu.Unlock()
	s.run.Lock()
	defer s.run.Unlock()
	step, st, err := s.d.Store.Lock(ctx)
	if err != nil {
		return err
	}
	// One Compose that will not start must not keep the whole server
	// locked: start what can be started, unlock, then report the rest.
	var failed []error
	if step != StepNone {
		if err := s.d.Docker.RemoveMaintenance(ctx); err != nil {
			return fmt.Errorf("remove the maintenance page: %w", err)
		}
		for _, id := range st.RunningComposeIDs {
			target, err := s.d.Composes.Target(ctx, id)
			if err != nil {
				log.Printf("migration: resume: compose %d: %v", id, err)
				continue
			}
			if err := s.d.Docker.ComposeStart(ctx, target.Request); err != nil {
				failed = append(failed, fmt.Errorf("%s did not start, deploy it again: %w", target.Compose.Name, err))
			}
		}
	}
	if err := s.d.Store.ClearAll(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	s.loaded, s.step, s.target = true, StepNone, ""
	s.digests = map[string]string{}
	s.mu.Unlock()
	if len(failed) > 0 {
		return &UnstartedError{Err: errors.Join(failed...)}
	}
	return nil
}

// Finish records the new host's URL and leaves the old host Migrated.
func (s *Source) Finish(ctx context.Context, targetURL string) error {
	s.run.Lock()
	defer s.run.Unlock()
	step, st, err := s.d.Store.Lock(ctx)
	if err != nil {
		return err
	}
	if step != StepReady && step != StepMigrated {
		return ErrNotReady
	}
	st.TargetURL = targetURL
	if st.MigratedAt == 0 {
		st.MigratedAt = s.d.Now().UTC().Unix()
	}
	if err := s.setLock(ctx, StepMigrated, st); err != nil {
		return err
	}
	// The session ends here: nobody may stream this server's data or undo
	// the Migration with the key any more. The lock keeps who claimed it.
	return s.d.Store.RevokeSession(ctx)
}

// Middleware answers 503 for every route a locked panel must not serve. The
// SPA shell stays reachable so it can render the lock page, and so do the
// Migration's own routes and signing in.
func (s *Source) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if allowedWhileLocked(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		step, target, err := s.cached(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "migration state is temporarily unavailable")
			return
		}
		switch step {
		case StepNone:
			next.ServeHTTP(w, r)
		case StepMigrated:
			writeJSONError(w, http.StatusServiceUnavailable, "This server was migrated to "+target)
		default:
			writeJSONError(w, http.StatusServiceUnavailable, "This server is being migrated")
		}
	})
}

func allowedWhileLocked(path string) bool {
	switch path {
	case "/healthz", "/api/auth/status", "/api/auth/login", "/api/auth/logout":
		return true
	}
	return strings.HasPrefix(path, "/api/migration/") || (path != "/api" && !strings.HasPrefix(path, "/api/"))
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("migration: write response: %v", err)
	}
}
