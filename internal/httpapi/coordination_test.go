package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"relist-panel/internal/dockerhost"
)

func mustAcquire(t *testing.T, acquire func() (func(), error)) func() {
	t.Helper()
	release, err := acquire()
	if err != nil {
		t.Fatal(err)
	}
	return release
}

// pending runs acquire on its own goroutine and reports its outcome.
func pending(acquire func() (func(), error)) <-chan func() {
	got := make(chan func(), 1)
	go func() {
		release, err := acquire()
		if err != nil {
			release = nil
		}
		got <- release
	}()
	return got
}

func assertWaiting(t *testing.T, got <-chan func(), what string) {
	t.Helper()
	select {
	case <-got:
		t.Fatalf("%s did not wait", what)
	case <-time.After(50 * time.Millisecond):
	}
}

func assertAcquired(t *testing.T, got <-chan func(), what string) func() {
	t.Helper()
	select {
	case release := <-got:
		if release == nil {
			t.Fatalf("%s failed", what)
		}
		return release
	case <-time.After(2 * time.Second):
		t.Fatalf("%s did not acquire", what)
		return nil
	}
}

func (c *coordinator) volumeEntries() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.volumes)
}

func TestSharedVolumeHoldsCoexist(t *testing.T) {
	c := newCoordinator()
	ctx := context.Background()
	first := mustAcquire(t, func() (func(), error) { return c.acquireVolumesShared(ctx, 1) })
	second := mustAcquire(t, func() (func(), error) { return c.acquireVolumesShared(ctx, 1) })
	first()
	second()
	if n := c.volumeEntries(); n != 0 {
		t.Fatalf("volume entries = %d after every release, want 0", n)
	}
}

func TestExclusiveVolumeHoldWaitsForSharedHolds(t *testing.T) {
	c := newCoordinator()
	ctx := context.Background()
	shared := mustAcquire(t, func() (func(), error) { return c.acquireVolumesShared(ctx, 1) })
	exclusive := pending(func() (func(), error) { return c.AcquireVolumes(ctx, 1) })
	assertWaiting(t, exclusive, "exclusive hold")

	// Another Project is independent.
	other := mustAcquire(t, func() (func(), error) { return c.AcquireVolumes(ctx, 2) })
	other()

	shared()
	release := assertAcquired(t, exclusive, "exclusive hold after the shared hold ended")
	shared2 := pending(func() (func(), error) { return c.acquireVolumesShared(ctx, 1) })
	assertWaiting(t, shared2, "shared hold under an exclusive hold")
	release()
	assertAcquired(t, shared2, "shared hold after the exclusive hold ended")()
}

func TestWaitingExclusiveVolumeHoldBlocksNewSharedHolds(t *testing.T) {
	c := newCoordinator()
	ctx := context.Background()
	shared := mustAcquire(t, func() (func(), error) { return c.acquireVolumesShared(ctx, 1) })
	exclusive := pending(func() (func(), error) { return c.AcquireVolumes(ctx, 1) })
	assertWaiting(t, exclusive, "exclusive hold")
	late := pending(func() (func(), error) { return c.acquireVolumesShared(ctx, 1) })
	assertWaiting(t, late, "a shared hold behind a waiting exclusive hold")

	shared()
	releaseExclusive := assertAcquired(t, exclusive, "exclusive hold")
	assertWaiting(t, late, "the late shared hold under the exclusive hold")
	releaseExclusive()
	assertAcquired(t, late, "the late shared hold")()
	if n := c.volumeEntries(); n != 0 {
		t.Fatalf("volume entries = %d, want 0", n)
	}
}

// A waiter whose lock was freed must still be counted: otherwise the entry is
// dropped under it and the next exclusive hold overlaps its read.
func TestSharedWaiterKeepsTheVolumeLockAfterTheExclusiveHoldEnds(t *testing.T) {
	assertExclusiveWaits := func(c *coordinator, what string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		if release, err := c.AcquireVolumes(ctx, 1); err == nil {
			release()
			t.Fatalf("%s: an exclusive hold overlapped a shared hold", what)
		}
	}
	ctx := context.Background()

	c := newCoordinator()
	exclusive := mustAcquire(t, func() (func(), error) { return c.AcquireVolumes(ctx, 1) })
	shared := pending(func() (func(), error) { return c.acquireVolumesShared(ctx, 1) })
	assertWaiting(t, shared, "shared hold under an exclusive hold")
	exclusive()
	release := assertAcquired(t, shared, "shared hold")
	assertExclusiveWaits(c, "waiting reader")
	release()

	c = newCoordinator()
	first := mustAcquire(t, func() (func(), error) { return c.acquireVolumesShared(ctx, 1) })
	writer := pending(func() (func(), error) { return c.AcquireVolumes(ctx, 1) })
	assertWaiting(t, writer, "exclusive hold")
	late := pending(func() (func(), error) { return c.acquireVolumesShared(ctx, 1) })
	assertWaiting(t, late, "late shared hold")
	first()
	assertAcquired(t, writer, "exclusive hold")()
	release = assertAcquired(t, late, "late shared hold")
	assertExclusiveWaits(c, "late reader")
	release()
	if n := c.volumeEntries(); n != 0 {
		t.Fatalf("volume entries = %d, want 0", n)
	}
}

func TestCanceledVolumeWaiterReturnsAndLeavesNoEntry(t *testing.T) {
	for _, exclusive := range []bool{true, false} {
		c := newCoordinator()
		held := mustAcquire(t, func() (func(), error) { return c.AcquireVolumes(context.Background(), 1) })
		ctx, cancel := context.WithCancel(context.Background())
		errs := make(chan error, 1)
		go func() {
			var err error
			if exclusive {
				_, err = c.AcquireVolumes(ctx, 1)
			} else {
				_, err = c.acquireVolumesShared(ctx, 1)
			}
			errs <- err
		}()
		time.Sleep(50 * time.Millisecond)
		cancel()
		select {
		case err := <-errs:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("exclusive=%v: err = %v, want context.Canceled", exclusive, err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("exclusive=%v: canceled waiter did not return", exclusive)
		}
		held()
		if n := c.volumeEntries(); n != 0 {
			t.Fatalf("exclusive=%v: volume entries = %d after cancel and release, want 0", exclusive, n)
		}
	}
}

// A canceled waiting exclusive hold must stop blocking shared holds.
func TestCanceledExclusiveWaiterUnblocksSharedHolds(t *testing.T) {
	c := newCoordinator()
	shared := mustAcquire(t, func() (func(), error) { return c.acquireVolumesShared(context.Background(), 1) })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); c.AcquireVolumes(ctx, 1) }()
	time.Sleep(50 * time.Millisecond)
	late := pending(func() (func(), error) { return c.acquireVolumesShared(context.Background(), 1) })
	assertWaiting(t, late, "a shared hold behind a waiting exclusive hold")
	cancel()
	<-done
	assertAcquired(t, late, "the shared hold once the exclusive waiter left")()
	shared()
}

func acquireWith(ctx context.Context, c *coordinator, kind string) (func(), error) {
	if kind == "shared" {
		return c.acquireVolumesShared(ctx, 1)
	}
	return c.AcquireVolumes(ctx, 1)
}

func TestVolumeHoldsTakeTheMaintenanceGuard(t *testing.T) {
	c := newCoordinator()
	c.host = dockerhost.NewGuard()
	for _, name := range []string{"shared", "exclusive"} {
		release := mustAcquire(t, func() (func(), error) { return acquireWith(context.Background(), c, name) })
		if prune, ok := c.host.TryPrune(); ok {
			prune()
			t.Fatalf("%s hold: prune overlapped", name)
		}
		release()
		prune, ok := c.host.TryPrune()
		if !ok {
			t.Fatalf("%s hold leaked the maintenance guard", name)
		}
		prune()
		prune, _ = c.host.TryPrune()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		if _, err := acquireWith(ctx, c, name); err == nil {
			t.Fatalf("%s hold overlapped prune", name)
		}
		cancel()
		prune()
	}
}

// Volume reads take only the shared volume hold, so a held mutation gate (a
// running Deploy step) does not stall them, while writes wait for both.
func TestMiddlewareClassesByMethod(t *testing.T) {
	const volumes = "/api/projects/1/environments/2/composes/3/volumes/"
	tests := []struct {
		method, path string
		gate, hold   string // "" none, "shared", "exclusive"
	}{
		{http.MethodGet, volumes + "list", "", "shared"},
		{http.MethodHead, volumes + "archive", "", "shared"},
		{http.MethodGet, volumes + "orphans", "", "shared"},
		{http.MethodPut, volumes + "content", "gate", "exclusive"},
		{http.MethodPut, volumes + "restore", "gate", "exclusive"},
		{http.MethodPost, volumes + "upload", "gate", "exclusive"},
		{http.MethodDelete, volumes + "content", "gate", "exclusive"},
		{http.MethodDelete, volumes + "orphans/x", "gate", "exclusive"},
		{http.MethodDelete, "/api/projects/1/environments/2/composes/3", "gate", "exclusive"},
		{http.MethodDelete, "/api/projects/1/environments/2", "gate", "exclusive"},
		{http.MethodDelete, "/api/projects/1", "gate", "exclusive"},
		{http.MethodPost, "/api/projects/1/environments/2/composes/3/stop", "gate", ""},
		{http.MethodPut, "/api/projects/1/environments/2/composes/3", "gate", ""},
		{http.MethodGet, "/api/projects/1/environments/2/composes/3", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			c := newCoordinator()
			served := make(chan struct{})
			handler := c.middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { close(served) }))
			ctx := context.Background()

			// Hold the other side first, then see whether the request waits.
			try := func(name string, hold func() (func(), error), blocks bool) {
				t.Helper()
				release := mustAcquire(t, hold)
				defer release()
				served = make(chan struct{})
				ctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
				defer cancel()
				done := make(chan struct{})
				go func() {
					defer close(done)
					handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(tt.method, tt.path, nil).WithContext(ctx))
				}()
				<-done
				select {
				case <-served:
					if blocks {
						t.Errorf("%s: handler ran while %s was held", name, name)
					}
				default:
					if !blocks {
						t.Errorf("%s: handler was blocked by %s", name, name)
					}
				}
			}
			try("shared hold", func() (func(), error) { return c.acquireVolumesShared(ctx, 1) }, tt.hold == "exclusive")
			try("exclusive hold", func() (func(), error) { return c.AcquireVolumes(ctx, 1) }, tt.hold != "")
			try("mutation gate", func() (func(), error) { return c.Acquire(ctx, 1) }, tt.gate != "")
		})
	}
}
