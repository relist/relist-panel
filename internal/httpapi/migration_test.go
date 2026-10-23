package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"relist-panel/internal/httpapi"
	"relist-panel/internal/migration"
)

func newMigrationServer(t *testing.T) *testServer {
	t.Helper()
	s := newTestServer(t)
	s.restartWithMigration(t)
	return s
}

// restartWithMigration rebuilds the handler with Migration options, as a
// panel restart would: Migration state files are read again.
func (s *testServer) restartWithMigration(t *testing.T) {
	t.Helper()
	s.stopWorker()
	ctx, cancel := context.WithCancel(context.Background())
	handler, wait := httpapi.NewHandler(ctx, httpapi.Deps{DB: s.db, Runner: s.runner, DataDir: s.dataDir, VolumeRunner: s.volumeRunner,
		Images: s.images, Migration: &migration.Options{Restart: func() {}}})
	var once sync.Once
	s.stopWorker = func() { once.Do(func() { cancel(); wait() }) }
	t.Cleanup(s.stopWorker)
	s.handler = handler
}

func writeMigrationFile(t *testing.T, s *testServer, name, content string) {
	t.Helper()
	dir := filepath.Join(s.dataDir, ".migration")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPullRoutesAreNotOnTheWeb(t *testing.T) {
	s := newMigrationServer(t)
	response := s.request(t, http.MethodGet, "/api/migration/pull/progress", nil)
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("progress before setup = %d, want 401: no route lets itself past the cookie", response.StatusCode)
	}
	setupAndLogin(t, s)
	for _, r := range []struct{ method, path string }{
		{http.MethodGet, "/api/migration/pull/progress"},
		{http.MethodPost, "/api/migration/pull/connect"},
		{http.MethodPost, "/api/migration/pull/recheck"},
		{http.MethodPost, "/api/migration/pull/start"},
		{http.MethodPost, "/api/migration/pull/retry"},
		{http.MethodPost, "/api/migration/pull/abort"},
	} {
		response := s.request(t, r.method, r.path, nil)
		body := readBody(t, response)
		if response.StatusCode != http.StatusNotFound || strings.Contains(body, "<html") {
			t.Errorf("%s %s signed in = %d %.40q, want a 404 that is not the app", r.method, r.path, response.StatusCode, body)
		}
	}
}

func TestSetupBlockedDuringMigration(t *testing.T) {
	s := newTestServer(t)
	writeMigrationFile(t, s, "state.json", `{"phase":"paused","sourceUrl":"https://old","manifest":{"items":[]},"items":{}}`)
	s.restartWithMigration(t)
	response := postJSON(t, s, "/api/auth/setup", map[string]string{"email": "admin@example.com", "password": "correct horse battery staple"})
	body := readBody(t, response)
	if response.StatusCode != http.StatusConflict || !strings.Contains(body, "A migration is in progress") {
		t.Fatalf("setup during a migration = %d %s", response.StatusCode, body)
	}
}

func TestSetupBlockedWhileConnected(t *testing.T) {
	s := newTestServer(t)
	writeMigrationFile(t, s, "state.json", `{"phase":"connected","sourceUrl":"https://old","sessionKey":"k"}`)
	s.restartWithMigration(t)
	response := postJSON(t, s, "/api/auth/setup", map[string]string{"email": "admin@example.com", "password": "correct horse battery staple"})
	body := readBody(t, response)
	if response.StatusCode != http.StatusConflict || !strings.Contains(body, "abort it") {
		t.Fatalf("setup while connected = %d %s", response.StatusCode, body)
	}
}

func TestStartConflictCarriesBlockingMessage(t *testing.T) {
	_, control := newControlServer(t)
	code, body := controlDo(t, control, http.MethodPost, "/api/migration/pull/start", `{"hostPaths":[]}`)
	if code != http.StatusConflict || !strings.Contains(body, "connect to a source first") {
		t.Fatalf("start before connect = %d %s", code, body)
	}
	code, body = controlDo(t, control, http.MethodPost, "/api/migration/pull/connect",
		`{"sourceUrl":"http://old.example.com","token":"x","selfUrl":"https://new.example.com"}`)
	if code != http.StatusBadRequest || !strings.Contains(body, "The source must use https://") {
		t.Fatalf("connect over http = %d %s", code, body)
	}
}

func TestProgressAfterRestartReadsSummary(t *testing.T) {
	s := newTestServer(t)
	writeMigrationFile(t, s, "summary.json", `{"deployments":[{"composeId":1,"number":3}],"warnings":[],"dns":["shop.example.com"]}`)
	control := s.restartWithControl(t)
	code, body := controlDo(t, control, http.MethodGet, "/api/migration/pull/progress", "")
	var p struct {
		Phase   string
		Summary struct{ DNS []string }
	}
	json.Unmarshal([]byte(body), &p)
	if code != http.StatusOK || p.Phase != "done" || len(p.Summary.DNS) != 1 {
		t.Fatalf("progress after restart = %d %+v", code, p)
	}
}

func TestMigrationRoutesAuthWiring(t *testing.T) {
	s, control := newControlServer(t)

	response := s.request(t, http.MethodPost, "/api/migration/tokens", map[string]string{"lifetime": "24h"})
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("token create on the web without a cookie = %d, want 401", response.StatusCode)
	}
	response.Body.Close()

	setupAndLogin(t, s)
	response = s.request(t, http.MethodPost, "/api/migration/tokens", map[string]string{"lifetime": "24h"})
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("token create on the web signed in = %d, want 404: tokens come from the terminal", response.StatusCode)
	}
	code, body := controlDo(t, control, http.MethodPost, "/api/migration/tokens", `{"lifetime":"24h"}`)
	var tok struct{ Token string }
	json.Unmarshal([]byte(body), &tok)
	if code != http.StatusCreated || tok.Token == "" {
		t.Fatalf("token create over the socket = %d %s", code, body)
	}

	cookies := s.cookies
	s.cookies = map[string]*http.Cookie{}
	response = s.request(t, http.MethodPost, "/api/migration/claim", map[string]string{"token": tok.Token, "claimedBy": "https://new"})
	var claimed struct{ SessionKey string }
	json.NewDecoder(response.Body).Decode(&claimed)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("claim without a cookie = %d, want 200", response.StatusCode)
	}
	request := httptest.NewRequest(http.MethodPost, "http://relist-panel.test/api/migration/start", strings.NewReader(`{"hostPaths":[]}`))
	request.Header.Set("Authorization", "Bearer "+claimed.SessionKey)
	if response := s.serve(request); response.StatusCode != http.StatusAccepted && response.StatusCode != http.StatusOK {
		t.Fatalf("start with the session key and no cookie = %d", response.StatusCode)
	}
	s.cookies = cookies

	deadline := time.Now().Add(10 * time.Second)
	for {
		response = s.request(t, http.MethodGet, "/api/projects", nil)
		response.Body.Close()
		if response.StatusCode == http.StatusServiceUnavailable {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("projects under the lock = %d, want 503", response.StatusCode)
		}
		time.Sleep(5 * time.Millisecond)
	}
	response = s.request(t, http.MethodGet, "/api/migration/status", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status under the lock = %d, want 200", response.StatusCode)
	}
	response.Body.Close()
}
