package httpapi_test

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"relist-panel/internal/dockerhost"
	"relist-panel/internal/httpapi"
)

type hostEngine struct{ calls atomic.Int32 }

func (h *hostEngine) Report(context.Context) (dockerhost.Report, error) {
	h.calls.Add(1)
	return dockerhost.Report{Version: "28.0", Usage: []dockerhost.Usage{}}, nil
}
func (h *hostEngine) Prune(_ context.Context, k dockerhost.Kind) (dockerhost.PruneResult, error) {
	h.calls.Add(1)
	return dockerhost.PruneResult{Kind: k, SpaceReclaimed: 1024, Deleted: []string{}}, nil
}

func TestDockerHostRoutesRequireAuthAndConfirmations(t *testing.T) {
	s := newTestServer(t)
	s.stopWorker()
	engine := &hostEngine{}
	ctx, cancel := context.WithCancel(context.Background())
	handler, wait := httpapi.NewHandler(ctx, s.db, s.runner, s.dataDir, nil, nil, nil, nil, nil, s.images, engine)
	t.Cleanup(func() { cancel(); wait() })
	s.handler = handler
	for _, route := range []struct{ method, path string }{{"GET", "/api/docker"}, {"POST", "/api/docker/prune"}, {"POST", "/api/docker/recover"}} {
		response := s.request(t, route.method, route.path, map[string]string{"kind": "containers", "confirm": "PRUNE"})
		if response.StatusCode != 401 {
			t.Errorf("unauthenticated %s: %d", route.path, response.StatusCode)
		}
		response.Body.Close()
	}
	if engine.calls.Load() != 0 {
		t.Fatal("unauthenticated request reached Docker")
	}
	setupAndLogin(t, s)
	response := s.request(t, http.MethodGet, "/api/docker", nil)
	if response.StatusCode != 200 || !strings.Contains(readBody(t, response), `"version":"28.0"`) {
		t.Fatal("report unavailable")
	}
	response = s.request(t, http.MethodPost, "/api/docker/prune", map[string]string{"kind": "containers"})
	if response.StatusCode != 400 {
		t.Fatalf("no confirmation: %d", response.StatusCode)
	}
	response.Body.Close()
	response = s.request(t, http.MethodPost, "/api/docker/prune", map[string]string{"kind": "containers", "confirm": "PRUNE"})
	if response.StatusCode != 200 || !strings.Contains(readBody(t, response), `"spaceReclaimed":1024`) {
		t.Fatalf("confirmed prune failed: %d %s", response.StatusCode, readBody(t, response))
	}
	if engine.calls.Load() != 2 {
		t.Fatal("refused request reached Docker")
	}
}
