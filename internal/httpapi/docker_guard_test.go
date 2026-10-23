package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"relist-panel/internal/dockerhost"
)

func TestProjectOperationsAndImageLoadHoldDockerMaintenanceGuard(t *testing.T) {
	c := newCoordinator()
	c.host = dockerhost.NewGuard()
	release, err := c.Acquire(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if prune, ok := c.host.TryPrune(); ok {
		prune()
		t.Fatal("prune overlapped Project mutation")
	}
	release()
	started := make(chan struct{})
	unblock := make(chan struct{})
	done := make(chan struct{})
	handler := c.middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { close(started); <-unblock }))
	go func() {
		defer close(done)
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("PUT", "/api/images", nil))
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("image load did not start")
	}
	if prune, ok := c.host.TryPrune(); ok {
		prune()
		t.Error("prune overlapped image load")
	}
	close(unblock)
	<-done
	prune, ok := c.host.TryPrune()
	if !ok {
		t.Fatal("image load leaked maintenance guard")
	}
	defer prune()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.Acquire(ctx, 2); err == nil {
		t.Fatal("Project mutation overlapped prune")
	}
}
