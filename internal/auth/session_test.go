package auth

import (
	"net/http"
	"testing"
	"time"
)

func TestSessionTokenStoredAsHash(t *testing.T) {
	f := newUserFixture(t)
	response := f.login("admin@example.com", userPassword)
	token := sessionCookie(t, response)

	if n := f.count(t, "SELECT COUNT(*) FROM sessions WHERE token_hash = ?", HashSessionToken(token)); n != 1 {
		t.Fatalf("rows under the token's hash = %d, want 1", n)
	}
	if n := f.count(t, "SELECT COUNT(*) FROM sessions WHERE token_hash = ?", token); n != 0 {
		t.Fatalf("rows under the raw token = %d, want 0", n)
	}
}

func TestRawTokenRowDoesNotAuthenticate(t *testing.T) {
	f := newUserFixture(t)
	if _, err := f.db.Exec(
		"INSERT INTO sessions (token_hash, user_id, expires_at, created_at, last_seen_at) VALUES ('raw-token', 1, ?, ?, ?)",
		f.now.Add(time.Hour).Unix(), f.now.Unix(), f.now.Unix(),
	); err != nil {
		t.Fatalf("insert raw row: %v", err)
	}
	if response := f.do(http.MethodGet, "/api/whoami", "raw-token", nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("whoami with a raw-token row = %d, want 401", response.Code)
	}
}

func TestLogoutDeletesHashedRow(t *testing.T) {
	f := newUserFixture(t)
	token := sessionCookie(t, f.login("admin@example.com", userPassword))
	if response := f.do(http.MethodPost, "/api/auth/logout", token, nil); response.Code != http.StatusNoContent {
		t.Fatalf("logout = %d", response.Code)
	}
	if n := f.count(t, "SELECT COUNT(*) FROM sessions"); n != 0 {
		t.Fatalf("sessions after logout = %d, want 0", n)
	}
}
