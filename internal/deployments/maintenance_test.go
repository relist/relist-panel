package deployments

import (
	"context"
	"testing"
	"time"

	"relist-panel/internal/composes"
	"relist-panel/internal/dockerhost"
)

func TestMaintenanceGuardCoversBuildThroughDeploy(t *testing.T) {
	h := newHarness(t)
	h.newQueue()
	guard := dockerhost.NewGuard()
	h.q.d.Maintenance = guard
	h.zipSource(h.a)
	h.comp.services = []string{"web"}
	started := make(chan struct{})
	unblock := make(chan struct{})
	h.build.onBuild = func(ctx context.Context, _ composes.BuildOptions) error {
		close(started)
		select {
		case <-unblock:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	h.comp.onDeploy = func(context.Context, int64, composes.DeployOptions) ([]string, error) {
		if release, ok := guard.TryPrune(); ok {
			release()
			t.Error("prune overlapped Deploy")
		}
		return nil, nil
	}
	d := h.enqueue(h.a, ModeBuild)
	h.start()
	recv(t, started, "Build")
	if release, ok := guard.TryPrune(); ok {
		release()
		t.Error("prune overlapped Build")
	}
	close(unblock)
	if got := h.waitSettled(h.a, d.Number); got.Status != StatusSucceeded {
		t.Fatalf("deployment: %+v", got)
	}
	eventually(t, "maintenance guard release", func() bool {
		release, ok := guard.TryPrune()
		if ok {
			release()
		}
		return ok
	})
}

func TestMaintenanceDoesNotStartDeploymentUntilPruneEnds(t *testing.T) {
	h := newHarness(t)
	h.newQueue()
	guard := dockerhost.NewGuard()
	h.q.d.Maintenance = guard
	d := h.enqueue(h.a, ModeDeploy)
	release, _ := guard.TryPrune()
	h.start()
	time.Sleep(25 * time.Millisecond)
	if got := h.get(h.a, d.Number); got.Status != StatusQueued {
		t.Errorf("Deployment started during prune: %s", got.Status)
	}
	release()
	if got := h.waitSettled(h.a, d.Number); got.Status != StatusSucceeded {
		t.Fatalf("deployment: %+v", got)
	}
}
