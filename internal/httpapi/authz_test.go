package httpapi_test

import (
	"net/http"
	"testing"
	"time"

	"relist-panel/internal/auth"
)

// signInAsUser adds a User with id and a live session, and makes the server
// send that session's cookie from now on.
func signInAsUser(t *testing.T, server *testServer, id int64, email string, isAdmin bool) {
	t.Helper()
	if _, err := server.db.Exec(
		"INSERT INTO users (id, email, password_hash, is_admin, created_at) VALUES (?, ?, 'x', ?, 1)",
		id, email, isAdmin,
	); err != nil {
		t.Fatalf("add user: %v", err)
	}
	token := "session-" + email
	if _, err := server.db.Exec(
		"INSERT INTO sessions (token, user_id, expires_at, created_at) VALUES (?, ?, ?, 1)",
		token, id, time.Now().Add(time.Hour).Unix(),
	); err != nil {
		t.Fatalf("add session: %v", err)
	}
	server.cookies = map[string]*http.Cookie{auth.SessionCookieName: {Name: auth.SessionCookieName, Value: token}}
}

func TestDemotionTakesEffectOnNextRequest(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	signInAsUser(t, server, 2, "second@example.com", true)
	if response := server.request(t, http.MethodGet, "/api/users", nil); response.StatusCode != http.StatusOK {
		t.Fatalf("admin GET /api/users = %d", response.StatusCode)
	}
	if _, err := server.db.Exec("UPDATE users SET is_admin = 0 WHERE id = 2"); err != nil {
		t.Fatal(err)
	}
	if response := server.request(t, http.MethodGet, "/api/users", nil); response.StatusCode != http.StatusForbidden {
		t.Fatalf("demoted GET /api/users = %d, want 403", response.StatusCode)
	}
	if response := server.request(t, http.MethodGet, "/api/auth/account", nil); response.StatusCode != http.StatusOK {
		t.Fatalf("demoted GET /api/auth/account = %d, want 200", response.StatusCode)
	}
}

func TestUsersRoutesAreAdminOnly(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	signInAsUser(t, server, 2, "member@example.com", false)
	routes := []struct{ method, path string }{
		{http.MethodGet, "/api/users"},
		{http.MethodPost, "/api/users"},
		{http.MethodGet, "/api/users/1"},
		{http.MethodPatch, "/api/users/1"},
		{http.MethodDelete, "/api/users/1"},
		{http.MethodPost, "/api/users/1/reset"},
	}
	for _, route := range routes {
		response := server.request(t, route.method, route.path, map[string]any{})
		response.Body.Close()
		if response.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s as a member = %d, want 403", route.method, route.path, response.StatusCode)
		}
	}
}
