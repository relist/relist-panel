package panelupdate

import (
	"context"
	"log"
	"sync/atomic"
)

// quiesceState is the panel's answer to one run's quiesce request.
type quiesceState struct {
	run    string
	locked bool
	// idle turns true once the Deployment queue and the scan queue have
	// finished what was running.
	idle   atomic.Bool
	cancel context.CancelFunc
}

// dropQuiesce forgets the quiesce request of a run that is over.
func (s *Service) dropQuiesce() {
	s.qsMu.Lock()
	defer s.qsMu.Unlock()
	if s.qs != nil && s.qs.cancel != nil {
		s.qs.cancel()
	}
	s.qs = nil
}

// answerQuiesce is what the watcher does for a run that is not over (spec "The
// panel's side"). While the run is quiescing it takes the update lock, once,
// and writes panel.json each tick: `waiting` with what it waits on until the
// queues are idle, then `ready`; `refused` while a Migration lock is held, in
// which case the lock is not taken. In every other state it only keeps the lock
// on, so a panel that restarted into a run stays locked.
func (s *Service) answerQuiesce(ctx context.Context, st RunStatus) {
	if st.State != StateQuiescing {
		if !s.Locked() {
			if err := s.lock(ctx); err != nil {
				log.Printf("panelupdate: take the update lock for run %s: %v", st.Run, err)
			}
		}
		return
	}
	dir := RunDir(s.d.DataDir, st.Run)
	ack := PanelAck{Run: st.Run, Heartbeat: s.beat.Add(1)}

	if s.d.MigrationLocked != nil {
		locked, err := s.d.MigrationLocked(ctx)
		if err != nil {
			log.Printf("panelupdate: read the Migration state: %v", err)
			return
		}
		if locked && !s.Locked() {
			ack.State, ack.Reason = AckRefused, ErrMigrationLocked.Error()
			s.writeAck(dir, ack)
			return
		}
	}

	s.qsMu.Lock()
	q := s.qs
	if q == nil || q.run != st.Run {
		if q != nil && q.cancel != nil {
			q.cancel()
		}
		q = &quiesceState{run: st.Run}
		s.qs = q
	}
	s.qsMu.Unlock()

	if !q.locked {
		if err := s.lock(ctx); err != nil {
			// Deployments could not all be cancelled: not ready yet; the
			// lock is on and the next tick tries again.
			log.Printf("panelupdate: cancel the queued Deployments for run %s: %v", st.Run, err)
			ack.State, ack.WaitingOn = AckWaiting, "cancelling the queued Deployments"
			s.writeAck(dir, ack)
			return
		}
		q.locked = true
		waitCtx, cancel := context.WithCancel(ctx)
		q.cancel = cancel
		go s.waitIdle(waitCtx, q)
	}
	if q.idle.Load() {
		ack.State = AckReady
	} else {
		ack.State = AckWaiting
		if s.d.WaitingOn != nil {
			ack.WaitingOn = s.d.WaitingOn()
		}
	}
	s.writeAck(dir, ack)
}

// waitIdle waits for the running Deployment and the running scan, then marks
// the quiesce ready. The queue is held already, so nothing new starts.
func (s *Service) waitIdle(ctx context.Context, q *quiesceState) {
	if s.d.Queue != nil {
		if err := s.d.Queue.WaitIdle(ctx); err != nil {
			log.Printf("panelupdate: wait for the running Deployment: %v", err)
			return
		}
	}
	if s.d.Scans != nil {
		if err := s.d.Scans.WaitIdle(ctx); err != nil {
			log.Printf("panelupdate: wait for the running scan: %v", err)
			return
		}
	}
	if s.d.TraefikApply != nil {
		if err := s.d.TraefikApply.WaitIdle(ctx); err != nil {
			log.Printf("panelupdate: wait for the running Traefik apply: %v", err)
			return
		}
	}
	if s.d.RegistryCleanup != nil {
		if err := s.d.RegistryCleanup.WaitIdle(ctx); err != nil {
			log.Printf("panelupdate: wait for the running Registry cleanup: %v", err)
			return
		}
	}
	q.idle.Store(true)
}

func (s *Service) writeAck(dir string, a PanelAck) {
	if err := WriteAck(dir, a); err != nil {
		log.Printf("panelupdate: write panel.json: %v", err)
	}
}
