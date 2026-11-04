package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"relist-panel/internal/auth"
)

func changePassword(t *testing.T, server *testServer, current, next string) *http.Response {
	t.Helper()
	return postJSON(t, server, "/api/auth/password", map[string]string{"currentPassword": current, "newPassword": next})
}

func loginAs(t *testing.T, server *testServer, password string) int {
	t.Helper()
	response := postJSON(t, server, "/api/auth/login", map[string]string{"email": "admin@example.com", "password": password})
	response.Body.Close()
	return response.StatusCode
}

func TestChangePasswordNeedsTheCurrentOne(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	response := changePassword(t, server, "not the password", "a brand new password")
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("change with a wrong current password = %d, want 401", response.StatusCode)
	}
	if code := loginAs(t, server, "correct horse battery staple"); code != http.StatusOK {
		t.Fatalf("the old password stopped working after a refused change: %d", code)
	}
}

func TestChangePasswordValidatesTheNewOne(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	response := changePassword(t, server, "correct horse battery staple", "short")
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("change to a short password = %d, want 400", response.StatusCode)
	}
}

func TestChangePasswordSwapsItAndEndsOtherSessions(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	other := *server.cookies[auth.SessionCookieName]
	if code := loginAs(t, server, "correct horse battery staple"); code != http.StatusOK {
		t.Fatal("second login failed")
	}
	current := *server.cookies[auth.SessionCookieName]

	response := changePassword(t, server, "correct horse battery staple", "a brand new password")
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("change = %d, want 204", response.StatusCode)
	}
	account := server.request(t, http.MethodGet, "/api/auth/account", nil)
	account.Body.Close()
	if account.StatusCode != http.StatusOK {
		t.Fatalf("the session that changed the password ended: %d", account.StatusCode)
	}
	server.cookies[auth.SessionCookieName] = &other
	account = server.request(t, http.MethodGet, "/api/auth/account", nil)
	account.Body.Close()
	if account.StatusCode != http.StatusUnauthorized {
		t.Fatalf("another session survived the change: %d", account.StatusCode)
	}
	server.cookies[auth.SessionCookieName] = &current

	if code := loginAs(t, server, "correct horse battery staple"); code != http.StatusUnauthorized {
		t.Fatalf("the old password still works: %d", code)
	}
	if code := loginAs(t, server, "a brand new password"); code != http.StatusOK {
		t.Fatalf("the new password does not work: %d", code)
	}
}

// An admin seeded from RELIST_ADMIN_PASSWORD before create-admin keeps its
// flag until the password is changed.
func TestAccountSaysWhenThePasswordIsStillTheOneFromEnv(t *testing.T) {
	server := newTestServer(t)
	if _, err := auth.CreateAdmin(context.Background(), server.db, "admin@example.com", "correct horse battery staple", time.Now); err != nil {
		t.Fatal(err)
	}
	if _, err := server.db.Exec("UPDATE users SET password_from_env = 1"); err != nil {
		t.Fatal(err)
	}
	if code := loginAs(t, server, "correct horse battery staple"); code != http.StatusOK {
		t.Fatalf("login as the seeded admin = %d", code)
	}
	fromEnv := func() bool {
		response := server.request(t, http.MethodGet, "/api/auth/account", nil)
		defer response.Body.Close()
		var body struct {
			PasswordFromEnv bool `json:"passwordFromEnv"`
		}
		json.NewDecoder(response.Body).Decode(&body)
		return body.PasswordFromEnv
	}
	if !fromEnv() {
		t.Fatal("passwordFromEnv = false while the password is the one in the env")
	}
	response := changePassword(t, server, "correct horse battery staple", "a brand new password")
	response.Body.Close()
	if fromEnv() {
		t.Fatal("passwordFromEnv = true after the password was changed")
	}
}

func TestChangePasswordNeedsTheCodeWhenTwoFactorIsOn(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	secret := enableTwoFactor(t, server)
	response := changePassword(t, server, "correct horse battery staple", "a brand new password")
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("change without a code = %d, want 401", response.StatusCode)
	}
	// The enable step is spent; the next one is within the allowed skew.
	response = postJSON(t, server, "/api/auth/password", map[string]string{"currentPassword": "correct horse battery staple",
		"newPassword": "a brand new password", "code": codeAtTime(t, secret, time.Now().Add(30*time.Second))})
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("change with password and code = %d, want 204", response.StatusCode)
	}
}
