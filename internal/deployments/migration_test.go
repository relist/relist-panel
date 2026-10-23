package deployments

import (
	"context"
	"errors"
	"testing"
	"time"

	"relist-panel/internal/composes"
)

func TestCancelAllQueuedCancelsEveryCompose(t *testing.T) {
	h := newHarness(t)
	a := h.enqueue(h.a, ModeDeploy)
	b := h.enqueue(h.b, ModeDeploy)
	n, err := h.q.CancelAllQueued(context.Background(), "server migration")
	if err != nil || n != 2 {
		t.Fatalf("CancelAllQueued() = %d, %v; want 2", n, err)
	}
	for _, d := range []Deployment{h.get(h.a, a.Number), h.get(h.b, b.Number)} {
		if d.Status != StatusCanceled || d.Reason != "server migration" {
			t.Fatalf("deployment = %s %q, want canceled with the reason", d.Status, d.Reason)
		}
	}
}

func TestWaitIdleReturnsAfterTheRunningDeployment(t *testing.T) {
	h := newHarness(t)
	h.gitSource(h.a)
	release := make(chan struct{})
	h.build.onBuild = func(ctx context.Context, opts composes.BuildOptions) error {
		<-release
		return nil
	}
	d := h.enqueue(h.a, ModeBuild)
	h.start()
	h.waitEvent("build:web", 1)
	if id, number, ok := h.q.Running(); !ok || id != h.a || number != d.Number {
		t.Fatalf("Running() = %d, %d, %v", id, number, ok)
	}
	returned := make(chan error, 1)
	go func() { returned <- h.q.WaitIdle(context.Background()) }()
	select {
	case <-returned:
		t.Fatal("WaitIdle returned while a Deployment was building")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("WaitIdle() error = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("WaitIdle did not return")
	}
	if _, _, ok := h.q.Running(); ok {
		t.Fatal("Running() reports a Deployment after WaitIdle")
	}
}

func TestCancelUnfinished(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	queued := h.enqueue(h.a, ModeDeploy)
	running := h.enqueue(h.b, ModeDeploy)
	if err := h.store.Start(ctx, running.ID, []string{"deploy"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	n, err := h.store.CancelUnfinished(ctx, "server migration", time.Now())
	if err != nil || n != 2 {
		t.Fatalf("CancelUnfinished() = %d, %v; want 2", n, err)
	}
	for _, d := range []Deployment{h.get(h.a, queued.Number), h.get(h.b, running.Number)} {
		if d.Status != StatusCanceled || d.Reason != "server migration" || d.FinishedAt == nil {
			t.Fatalf("deployment = %+v", d)
		}
	}
}

func TestHoldRefusesEnqueueUntilRelease(t *testing.T) {
	h := newHarness(t)
	h.newQueue()
	h.q.Hold()
	if _, _, err := h.q.Enqueue(context.Background(), h.a, Request{Mode: ModeDeploy}); !errors.Is(err, ErrHeld) {
		t.Fatalf("Enqueue while held error = %v, want ErrHeld", err)
	}
	h.q.Release()
	if _, created, err := h.q.Enqueue(context.Background(), h.a, Request{Mode: ModeDeploy}); err != nil || !created {
		t.Fatalf("Enqueue after Release = %v, %v", created, err)
	}
}
