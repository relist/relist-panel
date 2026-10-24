package deployments

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"relist-panel/internal/composes"
	"relist-panel/internal/dockerhost"
)

type apiComposes struct{ *fakeComposes }

func (apiComposes) ComposeRunRequest(context.Context, int64, int64, int64) (composes.RunRequest, error) {
	return composes.RunRequest{}, nil
}

// lockedBuffer lets the test read what the worker goroutine logs.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) count(sub string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Count(l.b.String(), sub)
}

func captureLog(t *testing.T) *lockedBuffer {
	t.Helper()
	buf := &lockedBuffer{}
	log.SetOutput(buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return buf
}

func TestDeployAnswers503WhileDockerOperationsArePaused(t *testing.T) {
	h := newHarness(t)
	guard := dockerhost.NewGuard()
	guard.Block()
	h.gate.onAcquire = func(ctx context.Context) error {
		release, err := guard.Acquire(ctx)
		if err == nil {
			release()
		}
		return err
	}
	h.newQueue()
	mux := http.NewServeMux()
	NewAPI(h.q, apiComposes{h.comp}).RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/projects/1/environments/1/composes/"+strconv.FormatInt(h.a, 10)+"/deployments", strings.NewReader(`{"mode":"deploy"}`))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["error"] != dockerhost.ErrMaintenanceBlocked.Error() {
		t.Fatalf("body = %s (%v)", rec.Body, err)
	}
}

func TestWorkerLogsPausedDockerOnceAndKeepsDeploymentsQueued(t *testing.T) {
	h := newHarness(t)
	guard := dockerhost.NewGuard()
	h.newQueue()
	h.q.d.Maintenance = guard
	d := h.enqueue(h.a, ModeDeploy)
	guard.Block()
	buf := captureLog(t)
	oldDelay := retryDelay
	retryDelay = 10 * time.Millisecond
	t.Cleanup(func() { retryDelay = oldDelay })
	h.start()

	time.Sleep(150 * time.Millisecond) // many polls
	if n := buf.count(dockerhost.ErrMaintenanceBlocked.Error()); n != 1 {
		t.Fatalf("logged %d times during one blocked period, want 1", n)
	}
	if got := h.get(h.a, d.Number); got.Status != StatusQueued {
		t.Fatalf("Deployment status = %s, want queued", got.Status)
	}

	// Recovering lets the Deployment run; a later block logs again.
	guard.Recover()
	if got := h.waitSettled(h.a, d.Number); got.Status != StatusSucceeded {
		t.Fatalf("deployment: %+v", got)
	}
	d2 := h.enqueue(h.a, ModeDeploy)
	guard.Block()
	eventually(t, "second pause logged", func() bool { return buf.count(dockerhost.ErrMaintenanceBlocked.Error()) == 2 })
	time.Sleep(100 * time.Millisecond)
	if n := buf.count(dockerhost.ErrMaintenanceBlocked.Error()); n != 2 {
		t.Fatalf("logged %d times over two blocked periods, want 2", n)
	}
	if got := h.get(h.a, d2.Number); got.Status != StatusQueued {
		t.Fatalf("second Deployment status = %s, want queued", got.Status)
	}
}
