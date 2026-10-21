package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"relist-panel/internal/composes"
	"relist-panel/internal/httpapi"
	"relist-panel/internal/migration"
	"relist-panel/internal/terminal"
)

// newControlServer builds the panel with a control mux and serves that mux on
// a Unix socket, the way main does. It returns a client for the socket.
func newControlServer(t *testing.T) (*testServer, *http.Client) {
	t.Helper()
	s := newTestServer(t)
	return s, s.restartWithControl(t)
}

// restartWithControl rebuilds the handler with Migration options and a
// control socket, as a panel restart would: Migration state files are read
// again. It returns a client for the new socket.
func (s *testServer) restartWithControl(t *testing.T) *http.Client {
	t.Helper()
	s.stopWorker()
	ctx, cancel := context.WithCancel(context.Background())
	control := http.NewServeMux()
	handler, wait := httpapi.NewHandler(ctx, s.db, s.runner, s.dataDir, nil, nil, s.volumeRunner, nil, nil, s.images,
		&migration.Options{Restart: func() {}, Control: control})
	var once sync.Once
	s.stopWorker = func() { once.Do(func() { cancel(); wait() }) }
	t.Cleanup(s.stopWorker)
	s.handler = handler

	path := filepath.Join(t.TempDir(), "control.sock")
	l, err := migration.ListenControl(path)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: control}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}}
	return client
}

func controlDo(t *testing.T, c *http.Client, method, path, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, "http://panel"+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var b bytes.Buffer
	b.ReadFrom(resp.Body)
	return resp.StatusCode, b.String()
}

func TestControlPullProgressNeedsNoCookie(t *testing.T) {
	s, c := newControlServer(t)
	setupAndLogin(t, s)
	if code, body := controlDo(t, c, http.MethodGet, "/api/migration/pull/progress", ""); code != http.StatusOK {
		t.Fatalf("progress over the socket = %d %s, want 200", code, body)
	}
}

func TestControlCreatesATokenWithoutACookie(t *testing.T) {
	s, c := newControlServer(t)
	setupAndLogin(t, s)
	code, body := controlDo(t, c, http.MethodPost, "/api/migration/tokens", `{"lifetime":"24h"}`)
	if code != http.StatusCreated {
		t.Fatalf("token over the socket = %d %s, want 201", code, body)
	}
	var out struct{ Token string }
	json.Unmarshal([]byte(body), &out)
	if out.Token == "" {
		t.Fatalf("no token in %s", body)
	}
	if code, _ := controlDo(t, c, http.MethodGet, "/api/migration/status", ""); code != http.StatusOK {
		t.Fatalf("status over the socket = %d, want 200", code)
	}
}

func TestWebHasNoTokenRoute(t *testing.T) {
	s, _ := newControlServer(t)
	setupAndLogin(t, s)
	for _, r := range []struct{ method, path string }{
		{http.MethodPost, "/api/migration/tokens"},
		{http.MethodDelete, "/api/migration/tokens/current"},
		{http.MethodPost, "/api/migration/resume"},
	} {
		response := s.request(t, r.method, r.path, map[string]string{"lifetime": "24h", "confirm": "RESUME"})
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s on the web signed in = %d, want 404", r.method, r.path, response.StatusCode)
		}
	}
}

func TestControlConnectNeedsSelfURL(t *testing.T) {
	_, c := newControlServer(t)
	code, body := controlDo(t, c, http.MethodPost, "/api/migration/pull/connect", `{"sourceUrl":"https://old.example.com","token":"x"}`)
	if code != http.StatusBadRequest || !strings.Contains(body, "selfUrl") {
		t.Fatalf("connect without selfUrl = %d %s, want 400 naming selfUrl", code, body)
	}
}

// Starting a migration closes the Terminal Sessions open on the old host
// (the lock's step 1), naming why.
func TestMigrationStartClosesTerminalSessions(t *testing.T) {
	server, projectID, environmentID, composeID, projectName := newTerminalSessionServer(t)
	scriptAppContainer(server, projectName, terminal.Container{ID: "c1", Name: "relist-c3-app-1", Number: 1}, "/bin/bash")
	// Stopped, so the lock has nothing to stop: this handler has no
	// migration Docker port.
	server.runner.setStatus(projectName, composes.ComposeStatus{})
	server.stopWorker()
	ctx, cancel := context.WithCancel(context.Background())
	control := http.NewServeMux()
	handler, wait := httpapi.NewHandler(ctx, server.db, server.runner, server.dataDir, nil, nil, server.volumeRunner, nil,
		&terminal.Options{Exec: server.terminal, MaxSessions: 10, PingInterval: time.Second, PingTimeout: time.Second, KillGrace: 50 * time.Millisecond},
		server.images, &migration.Options{Restart: func() {}, Control: control})
	var once sync.Once
	server.stopWorker = func() { once.Do(func() { cancel(); wait() }) }
	t.Cleanup(server.stopWorker)
	server.handler = handler

	conn, _ := dialReady(t, server, terminalSessionPath(projectID, environmentID, composeID, "service=app"))

	rec := httptest.NewRecorder()
	control.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/migration/tokens", strings.NewReader(`{"lifetime":"1h"}`)))
	var tok struct{ Token string }
	json.Unmarshal(rec.Body.Bytes(), &tok)
	response := postJSON(t, server, "/api/migration/claim", map[string]string{"token": tok.Token, "claimedBy": "https://new"})
	var claimed struct{ SessionKey string }
	json.NewDecoder(response.Body).Decode(&claimed)
	response.Body.Close()
	request := httptest.NewRequest(http.MethodPost, "http://relist-panel.test/api/migration/start", strings.NewReader(`{"hostPaths":[]}`))
	request.Header.Set("Authorization", "Bearer "+claimed.SessionKey)
	if response := server.serve(request); response.StatusCode >= 300 {
		t.Fatalf("start = %d", response.StatusCode)
	}

	readCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	for {
		_, _, err := conn.Read(readCtx)
		if err == nil {
			continue
		}
		if status := websocket.CloseStatus(err); status != websocket.StatusGoingAway {
			t.Fatalf("session ended with %v, want a going-away close naming the migration", err)
		}
		if !strings.Contains(err.Error(), "migrated") {
			t.Fatalf("close reason = %v, want it to name the migration", err)
		}
		return
	}
}
