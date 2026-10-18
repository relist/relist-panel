package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"relist-panel/internal/httpapi"
	"relist-panel/internal/migration"
)

func newMigrationServer(t *testing.T) *testServer {
	t.Helper()
	s := newTestServer(t)
	s.stopWorker()
	ctx, cancel := context.WithCancel(context.Background())
	handler, wait := httpapi.NewHandler(ctx, s.db, s.runner, s.dataDir, nil, nil, s.volumeRunner, nil, nil, s.images, &migration.Options{})
	t.Cleanup(func() { cancel(); wait() })
	s.handler = handler
	return s
}

func TestMigrationRoutesAuthWiring(t *testing.T) {
	s := newMigrationServer(t)

	response := s.request(t, http.MethodPost, "/api/migration/tokens", map[string]string{"lifetime": "24h"})
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("token create without a cookie = %d, want 401", response.StatusCode)
	}
	response.Body.Close()

	setupAndLogin(t, s)
	response = s.request(t, http.MethodPost, "/api/migration/tokens", map[string]string{"lifetime": "24h"})
	var tok struct{ Token string }
	json.NewDecoder(response.Body).Decode(&tok)
	response.Body.Close()
	if response.StatusCode != http.StatusCreated || tok.Token == "" {
		t.Fatalf("token create = %d", response.StatusCode)
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
