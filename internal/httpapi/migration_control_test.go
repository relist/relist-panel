package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"relist-panel/internal/httpapi"
	"relist-panel/internal/migration"
)

// newControlServer builds the panel with a control mux and serves that mux on
// a Unix socket, the way main does. It returns a client for the socket.
func newControlServer(t *testing.T) (*testServer, *http.Client) {
	t.Helper()
	s := newTestServer(t)
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
	return s, client
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

func TestWebTokensStillNeedTheCookie(t *testing.T) {
	s, _ := newControlServer(t)
	setupAndLogin(t, s)
	delete(s.cookies, "relist_session")
	for name := range s.cookies {
		delete(s.cookies, name)
	}
	response := postJSON(t, s, "/api/migration/tokens", map[string]string{"lifetime": "24h"})
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("token on the web without a cookie = %d, want 401", response.StatusCode)
	}
}

func TestControlConnectNeedsSelfURL(t *testing.T) {
	_, c := newControlServer(t)
	code, body := controlDo(t, c, http.MethodPost, "/api/migration/pull/connect", `{"sourceUrl":"https://old.example.com","token":"x"}`)
	if code != http.StatusBadRequest || !strings.Contains(body, "selfUrl") {
		t.Fatalf("connect without selfUrl = %d %s, want 400 naming selfUrl", code, body)
	}
}
