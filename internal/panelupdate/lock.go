package panelupdate

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
)

// reasonUpdate is the reason recorded on every queued Deployment the update
// lock cancels.
const reasonUpdate = "panel update"

// updatingMessage is the 503 body while the update lock is on.
const updatingMessage = "The panel is updating"

// QueueControl is the part of the Deployment queue the update lock drives;
// *deployments.Queue has it, and so does the Migration lock's own interface.
//
// Hold and Release are one flag shared with the Migration lock. Releasing it
// here cannot release a hold the Migration owns because the two locks exclude
// each other: the Preflight gate and the quiesce refuse while a Migration lock
// is held, and the Migration refuses to start while this lock is on. The one
// race left, a Migration taking its lock while a run finishes, is closed by
// unlock, which does not Release while a Migration lock is held.
type QueueControl interface {
	Hold()
	Release()
	// CancelAllQueued cancels the queued Deployments: a held queue still runs
	// what is already queued.
	CancelAllQueued(ctx context.Context, reason string) (int, error)
	WaitIdle(ctx context.Context) error
}

// ScanWaiter waits for the scan worker's current task; *vulnscan.Service has
// it. Holding the Deployment queue already stops the worker starting another.
type ScanWaiter interface {
	WaitIdle(ctx context.Context) error
}

// Locked reports whether the update lock is on.
func (s *Service) Locked() bool { return s.lockOn.Load() }

// lock turns the update lock on: the flag the middleware reads, the held
// queue, and no queued Deployment left to start once the running one ends.
func (s *Service) lock(ctx context.Context) error {
	s.lockMu.Lock()
	defer s.lockMu.Unlock()
	s.lockOn.Store(true)
	if s.d.Queue == nil {
		return nil
	}
	s.d.Queue.Hold()
	_, err := s.d.Queue.CancelAllQueued(ctx, reasonUpdate)
	return err
}

// unlock turns the update lock off. The queue is released only when no
// Migration holds it.
func (s *Service) unlock(ctx context.Context) {
	s.lockMu.Lock()
	defer s.lockMu.Unlock()
	if !s.lockOn.Swap(false) || s.d.Queue == nil {
		return
	}
	if s.d.MigrationLocked != nil {
		if held, err := s.d.MigrationLocked(ctx); err != nil || held {
			if err != nil {
				log.Printf("panelupdate: leave the queue held, the Migration state is unreadable: %v", err)
			}
			return
		}
	}
	s.d.Queue.Release()
}

// StartLocked runs before the HTTP server serves and before any worker starts.
// A newest run that is not over turns the lock on, so neither the new panel
// before its health deadline nor the old one after a rollback takes a write or
// runs a Deployment the restored panel.db would not know about.
func (s *Service) StartLocked(ctx context.Context) error {
	st, ok, err := NewestRun(s.d.DataDir)
	if err != nil {
		// Fail closed: a run directory that cannot be listed may be a live run.
		_ = s.lock(ctx)
		return err
	}
	if !ok || IsTerminal(st.State) {
		return nil
	}
	log.Printf("panelupdate: run %s is %s: starting with the update lock on", st.Run, st.State)
	return s.lock(ctx)
}

// Middleware refuses every /api request with a 503 while the lock is on, like
// the Migration lock, except these: /healthz, /api/auth/status, /api/auth/login
// and /api/auth/logout, GET /api/panel-update/* and POST /api/panel-update/cancel, and everything that is not
// under /api (the SPA shell, which renders the lock page). A password or 2FA
// change would be lost or fought over by the restore, so it waits.
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.Locked() || allowedWhileUpdating(r.Method, r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": updatingMessage})
	})
}

func allowedWhileUpdating(method, path string) bool {
	switch {
	case path == "/healthz", path == "/api/auth/status", path == "/api/auth/login", path == "/api/auth/logout":
		return true
	case path == "/api/panel-update/cancel":
		return method == http.MethodPost
	case strings.HasPrefix(path, "/api/panel-update/"):
		return method == http.MethodGet || method == http.MethodHead
	}
	return path != "/api" && !strings.HasPrefix(path, "/api/")
}
