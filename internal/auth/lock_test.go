package auth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"relist-panel/internal/database"
)

func TestLockExpiresAfterOneMinute(t *testing.T) {
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	var clockMu sync.Mutex
	service := newServiceWithClock(db, func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
	})
	advance := func(d time.Duration) {
		clockMu.Lock()
		defer clockMu.Unlock()
		now = now.Add(d)
	}

	mux := http.NewServeMux()
	service.RegisterRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	post := func(path string, body map[string]string) *http.Response {
		t.Helper()
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode body: %v", err)
		}
		response, err := http.Post(server.URL+path, "application/json", bytes.NewReader(encoded))
		if err != nil {
			t.Fatalf("post %s: %v", path, err)
		}
		return response
	}

	setup := post("/api/auth/setup", map[string]string{
		"email":    "admin@example.com",
		"password": "correct horse battery staple",
	})
	setup.Body.Close()
	if setup.StatusCode != http.StatusCreated {
		t.Fatalf("setup status = %d, want %d", setup.StatusCode, http.StatusCreated)
	}

	for attempt := 1; attempt <= maxFailedAttempts; attempt++ {
		response := post("/api/auth/login", map[string]string{
			"email":    "admin@example.com",
			"password": "wrong password",
		})
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("wrong attempt %d status = %d, want %d", attempt, response.StatusCode, http.StatusUnauthorized)
		}
	}

	advance(59 * time.Second)
	locked := post("/api/auth/login", map[string]string{
		"email":    "admin@example.com",
		"password": "correct horse battery staple",
	})
	locked.Body.Close()
	if locked.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("at +59s status = %d, want %d", locked.StatusCode, http.StatusTooManyRequests)
	}

	advance(time.Second)
	unlocked := post("/api/auth/login", map[string]string{
		"email":    "admin@example.com",
		"password": "correct horse battery staple",
	})
	unlocked.Body.Close()
	if unlocked.StatusCode != http.StatusOK {
		t.Fatalf("at +60s status = %d, want %d", unlocked.StatusCode, http.StatusOK)
	}
}
