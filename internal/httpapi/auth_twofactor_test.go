package httpapi_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestFiveFailedLoginsLockTheAccount(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)

	for attempt := 1; attempt <= 5; attempt++ {
		response := postJSON(t, server, "/api/auth/login", map[string]string{
			"email":    "admin@example.com",
			"password": "wrong password",
		})
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("wrong attempt %d status = %d, want %d", attempt, response.StatusCode, http.StatusUnauthorized)
		}
	}

	locked := postJSON(t, server, "/api/auth/login", map[string]string{
		"email":    "admin@example.com",
		"password": "correct horse battery staple",
	})
	defer locked.Body.Close()
	if locked.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("correct password while locked status = %d, want %d", locked.StatusCode, http.StatusTooManyRequests)
	}

	retryAfter, err := strconv.Atoi(locked.Header.Get("Retry-After"))
	if err != nil {
		t.Fatalf("Retry-After = %q, want an integer: %v", locked.Header.Get("Retry-After"), err)
	}
	if retryAfter < 1 || retryAfter > 60 {
		t.Fatalf("Retry-After = %d, want a value between 1 and 60", retryAfter)
	}

	var body map[string]string
	if err := json.NewDecoder(locked.Body).Decode(&body); err != nil {
		t.Fatalf("decode locked body: %v", err)
	}
	if !strings.HasPrefix(body["error"], "too many attempts") {
		t.Fatalf("locked error = %q, want a prefix of %q", body["error"], "too many attempts")
	}
}

func TestSuccessfulLoginResetsTheFailureCount(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)

	wrongLogin := func(attempt int) {
		t.Helper()
		response := postJSON(t, server, "/api/auth/login", map[string]string{
			"email":    "admin@example.com",
			"password": "wrong password",
		})
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("wrong attempt %d status = %d, want %d", attempt, response.StatusCode, http.StatusUnauthorized)
		}
	}

	for attempt := 1; attempt <= 4; attempt++ {
		wrongLogin(attempt)
	}
	if got := failedAttempts(t, server); got != 4 {
		t.Fatalf("failed_attempts after four wrong logins = %d, want 4", got)
	}

	correct := postJSON(t, server, "/api/auth/login", map[string]string{
		"email":    "admin@example.com",
		"password": "correct horse battery staple",
	})
	correct.Body.Close()
	if correct.StatusCode != http.StatusOK {
		t.Fatalf("correct login status = %d, want %d", correct.StatusCode, http.StatusOK)
	}
	if got := failedAttempts(t, server); got != 0 {
		t.Fatalf("failed_attempts after a successful login = %d, want 0", got)
	}

	for attempt := 5; attempt <= 8; attempt++ {
		wrongLogin(attempt)
	}
	if got := failedAttempts(t, server); got != 4 {
		t.Fatalf("failed_attempts after four more wrong logins = %d, want 4", got)
	}
}

// failedAttempts reads the admin row's consecutive-failure counter directly,
// the same way TestAuthSetupAndLogin reads the stored password hash.
func failedAttempts(t *testing.T, server *testServer) int {
	t.Helper()
	var count int
	if err := server.db.QueryRow("SELECT failed_attempts FROM admin_credentials").Scan(&count); err != nil {
		t.Fatalf("read failed_attempts: %v", err)
	}
	return count
}

func TestUnknownEmailIsNotCounted(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)

	for attempt := 1; attempt <= 6; attempt++ {
		response := postJSON(t, server, "/api/auth/login", map[string]string{
			"email":    "nobody@example.com",
			"password": "correct horse battery staple",
		})
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("unknown-email attempt %d status = %d, want %d", attempt, response.StatusCode, http.StatusUnauthorized)
		}
	}
	if got := failedAttempts(t, server); got != 0 {
		t.Fatalf("failed_attempts after unknown-email attempts = %d, want 0", got)
	}

	response := postJSON(t, server, "/api/auth/login", map[string]string{
		"email":    "admin@example.com",
		"password": "correct horse battery staple",
	})
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("real admin login status = %d, want %d", response.StatusCode, http.StatusOK)
	}
}
