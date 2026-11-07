package panelupdate

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"
)

const watchRun = "20261101T110000Z"

func (r *lockRig) ack(t *testing.T) PanelAck {
	t.Helper()
	a, err := ReadAck(RunDir(r.dir, watchRun))
	if err != nil {
		t.Fatalf("read panel.json: %v", err)
	}
	return a
}

func TestWatcherAcksReadyWhenIdle(t *testing.T) {
	r := newLockRig(t)
	r.writeRun(t, watchRun, StateQuiescing)
	r.updater.state[UpdaterName(watchRun)] = UpdaterState{Exists: true, Running: true}
	r.s.watchOnce(t.Context())
	if !r.s.Locked() || !slices.Contains(r.queue.list(), "hold") || !slices.Contains(r.queue.list(), "cancel") {
		t.Fatalf("locked = %v events = %v", r.s.Locked(), r.queue.list())
	}
	// The idle wait runs in a goroutine; the next tick sees its result.
	waitFor(t, func() bool {
		r.s.watchOnce(t.Context())
		return r.ack(t).State == AckReady
	})
	a := r.ack(t)
	if a.Run != watchRun || a.Heartbeat < 1 || a.WaitingOn != "" {
		t.Fatalf("ack = %+v", a)
	}
}

func TestWatcherAcksWaitingWithDeployment(t *testing.T) {
	r := newLockRig(t)
	r.queue.idle = make(chan struct{})
	waiting := "Deployment api #3"
	r.s.d.WaitingOn = func() string { return waiting }
	r.writeRun(t, watchRun, StateQuiescing)
	r.updater.state[UpdaterName(watchRun)] = UpdaterState{Exists: true, Running: true}

	r.s.watchOnce(t.Context())
	first := r.ack(t)
	if first.State != AckWaiting || first.WaitingOn != "Deployment api #3" {
		t.Fatalf("ack = %+v", first)
	}
	r.s.watchOnce(t.Context())
	second := r.ack(t)
	if second.Heartbeat <= first.Heartbeat || second.State != AckWaiting {
		t.Fatalf("heartbeat did not advance: %+v then %+v", first, second)
	}
	close(r.queue.idle)
	waiting = ""
	waitFor(t, func() bool {
		r.s.watchOnce(t.Context())
		return r.ack(t).State == AckReady
	})
	if r.ack(t).Heartbeat <= second.Heartbeat {
		t.Fatal("heartbeat did not advance after ready")
	}
}

// blockingWaiter is a Traefik apply that runs until released.
type blockingWaiter struct{ release chan struct{} }

func (b blockingWaiter) WaitIdle(ctx context.Context) error {
	select {
	case <-b.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// A Traefik apply restarts Traefik, which the update's health check goes
// through, so the quiesce waits for it too (ADR-0030).
func TestWatcherWaitsForTraefikApply(t *testing.T) {
	r := newLockRig(t)
	apply := blockingWaiter{release: make(chan struct{})}
	r.s.d.TraefikApply = apply
	r.writeRun(t, watchRun, StateQuiescing)
	r.updater.state[UpdaterName(watchRun)] = UpdaterState{Exists: true, Running: true}
	r.s.watchOnce(t.Context())
	time.Sleep(20 * time.Millisecond)
	r.s.watchOnce(t.Context())
	if got := r.ack(t).State; got != AckWaiting {
		t.Fatalf("ack = %s while a Traefik apply runs", got)
	}
	close(apply.release)
	waitFor(t, func() bool {
		r.s.watchOnce(t.Context())
		return r.ack(t).State == AckReady
	})
}

// A Registry cleanup may hold the registry read-only; the update waits for
// it so the panel never restarts mid-GC (ADR-0033).
func TestQuiesceWaitsForRegistryCleanup(t *testing.T) {
	r := newLockRig(t)
	cleanup := blockingWaiter{release: make(chan struct{})}
	r.s.d.RegistryCleanup = cleanup
	r.writeRun(t, watchRun, StateQuiescing)
	r.updater.state[UpdaterName(watchRun)] = UpdaterState{Exists: true, Running: true}
	r.s.watchOnce(t.Context())
	time.Sleep(20 * time.Millisecond)
	r.s.watchOnce(t.Context())
	if got := r.ack(t).State; got != AckWaiting {
		t.Fatalf("ack = %s while a Registry cleanup runs", got)
	}
	close(cleanup.release)
	waitFor(t, func() bool {
		r.s.watchOnce(t.Context())
		return r.ack(t).State == AckReady
	})
}

func TestWatcherRefusesUnderMigrationLock(t *testing.T) {
	r := newLockRig(t)
	r.migr = true
	r.writeRun(t, watchRun, StateQuiescing)
	r.updater.state[UpdaterName(watchRun)] = UpdaterState{Exists: true, Running: true}
	r.s.watchOnce(t.Context())
	a := r.ack(t)
	if a.State != AckRefused || !strings.Contains(a.Reason, "Migration") || r.s.Locked() {
		t.Fatalf("ack = %+v locked = %v", a, r.s.Locked())
	}
	if slices.Contains(r.queue.list(), "hold") {
		t.Fatal("held the queue under a Migration lock")
	}
}

// The Updater can die after the backup and before `up`: the old panel is still
// running and locked, and must not stay locked.
func TestWatcherInterruptsAfterBackup(t *testing.T) {
	r := newLockRig(t)
	r.writeRun(t, watchRun, StateBackingUp)
	if err := r.s.StartLocked(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.updater.state[UpdaterName(watchRun)] = UpdaterState{Exists: true} // exited
	if !r.s.watchOnce(t.Context()) {
		t.Fatal("not settled")
	}
	st, _, _ := NewestRun(r.dir)
	if st.State != StateInterrupted || !strings.Contains(st.Error, StateBackingUp) {
		t.Fatalf("status = %+v", st)
	}
	if r.s.Locked() {
		t.Fatal("lock still on")
	}
	if got := r.queue.list(); got[len(got)-1] != "release" {
		t.Fatalf("events = %v", got)
	}
}

func TestWatcherAbortedRunReleasesLock(t *testing.T) {
	r := newLockRig(t)
	r.writeRun(t, watchRun, StateQuiescing)
	r.updater.state[UpdaterName(watchRun)] = UpdaterState{Exists: true, Running: true}
	r.s.watchOnce(t.Context())
	if !r.s.Locked() {
		t.Fatal("lock not taken")
	}
	r.writeRun(t, watchRun, StateAborted)
	r.updater.state[UpdaterName(watchRun)] = UpdaterState{Exists: true}
	if !r.s.watchOnce(t.Context()) || r.s.Locked() {
		t.Fatalf("aborted run left the lock on: locked = %v", r.s.Locked())
	}
	if rows := r.rows(t); len(rows) != 1 || !strings.Contains(rows[0], ":aborted:") {
		t.Fatalf("rows = %v", rows)
	}
}

func TestWatcherLaterStatesWriteNoAck(t *testing.T) {
	r := newLockRig(t)
	r.writeRun(t, watchRun, StateWaitingHealth)
	r.updater.state[UpdaterName(watchRun)] = UpdaterState{Exists: true, Running: true}
	r.s.watchOnce(t.Context())
	if !r.s.Locked() {
		t.Fatal("a panel that sees a run past quiescing must be locked")
	}
	if _, err := ReadAck(RunDir(r.dir, watchRun)); err == nil {
		t.Fatal("wrote panel.json after quiescing")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 500; i++ {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not met")
}

// A run is started by a process the panel does not control (install.sh --update
// runs the Updater with the panel not asked), so the watcher cannot sleep long
// when it sees no run: the next tick has to notice a new one within 2 seconds.
func TestWatchPollsEveryTwoSecondsEvenWhenIdle(t *testing.T) {
	r := newLockRig(t)
	var waits []time.Duration
	ctx, cancel := context.WithCancel(t.Context())
	r.s.d.After = func(d time.Duration) <-chan time.Time {
		waits = append(waits, d)
		if len(waits) == 3 {
			cancel()
		}
		ch := make(chan time.Time, 1)
		ch <- time.Time{}
		return ch
	}
	// No run at all, then a settled terminal run: both are idle states.
	r.s.Watch(ctx)
	r.writeRun(t, watchRun, StateSucceeded)
	ctx2, cancel2 := context.WithCancel(t.Context())
	waits = nil
	r.s.d.After = func(d time.Duration) <-chan time.Time {
		waits = append(waits, d)
		if len(waits) == 3 {
			cancel2()
		}
		ch := make(chan time.Time, 1)
		ch <- time.Time{}
		return ch
	}
	r.s.Watch(ctx2)
	for _, d := range waits {
		if d > watchEvery {
			t.Fatalf("slept %s between ticks, want at most %s", d, watchEvery)
		}
	}
}
