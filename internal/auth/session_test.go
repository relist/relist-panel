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

// signIn logs the Owner in at the fixture's current time and returns the
// session cookie token.
func (f *userFixture) signIn(t *testing.T) string {
	t.Helper()
	return sessionCookie(t, f.login("admin@example.com", userPassword))
}

func (f *userFixture) whoamiAt(t *testing.T, at time.Time, token string) int {
	t.Helper()
	f.now = at
	return f.do(http.MethodGet, "/api/whoami", token, nil).Code
}

func (f *userFixture) lastSeen(t *testing.T, token string) time.Time {
	t.Helper()
	var unix int64
	if err := f.db.QueryRow("SELECT last_seen_at FROM sessions WHERE token_hash = ?", HashSessionToken(token)).Scan(&unix); err != nil {
		t.Fatalf("read last_seen_at: %v", err)
	}
	return time.Unix(unix, 0).UTC()
}

func TestSessionEndsAfterIdleTimeout(t *testing.T) {
	f := newUserFixture(t)
	t0 := f.now
	token := f.signIn(t)
	if code := f.whoamiAt(t, t0.Add(24*time.Hour), token); code != http.StatusUnauthorized {
		t.Fatalf("whoami after 24 idle hours = %d, want 401", code)
	}
}

func TestActivityExtendsIdleWindow(t *testing.T) {
	f := newUserFixture(t)
	t0 := f.now
	token := f.signIn(t)
	for _, at := range []time.Duration{23 * time.Hour, 46 * time.Hour} {
		if code := f.whoamiAt(t, t0.Add(at), token); code != http.StatusOK {
			t.Fatalf("whoami at t0+%s = %d, want 200", at, code)
		}
	}
	if code := f.whoamiAt(t, t0.Add(46*time.Hour+24*time.Hour), token); code != http.StatusUnauthorized {
		t.Fatalf("whoami 24 idle hours after the last request = %d, want 401", code)
	}
}

func TestSessionEndsAtMaxLifetimeDespiteActivity(t *testing.T) {
	f := newUserFixture(t)
	t0 := f.now
	token := f.signIn(t)
	for at := 12 * time.Hour; at < 7*24*time.Hour; at += 12 * time.Hour {
		if code := f.whoamiAt(t, t0.Add(at), token); code != http.StatusOK {
			t.Fatalf("whoami at t0+%s = %d, want 200", at, code)
		}
	}
	if code := f.whoamiAt(t, t0.Add(7*24*time.Hour-time.Second), token); code != http.StatusOK {
		t.Fatalf("whoami a second before the cap = %d, want 200", code)
	}
	if code := f.whoamiAt(t, t0.Add(7*24*time.Hour), token); code != http.StatusUnauthorized {
		t.Fatalf("whoami at the 7-day cap = %d, want 401", code)
	}
}

func TestTouchIsThrottled(t *testing.T) {
	f := newUserFixture(t)
	t0 := f.now
	token := f.signIn(t)
	if code := f.whoamiAt(t, t0.Add(9*time.Minute+59*time.Second), token); code != http.StatusOK {
		t.Fatalf("whoami = %d", code)
	}
	if got := f.lastSeen(t, token); !got.Equal(t0) {
		t.Fatalf("last_seen_at after 9m59s = %s, want unchanged %s", got, t0)
	}
	if code := f.whoamiAt(t, t0.Add(10*time.Minute), token); code != http.StatusOK {
		t.Fatalf("whoami = %d", code)
	}
	if got, want := f.lastSeen(t, token), t0.Add(10*time.Minute); !got.Equal(want) {
		t.Fatalf("last_seen_at after 10m = %s, want %s", got, want)
	}
}

func TestFutureLastSeenIsNotTouched(t *testing.T) {
	f := newUserFixture(t)
	t0 := f.now
	token := f.signIn(t)
	if _, err := f.db.Exec("UPDATE sessions SET last_seen_at = ?", t0.Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if code := f.whoamiAt(t, t0, token); code != http.StatusOK {
		t.Fatalf("whoami with last_seen_at in the future = %d, want 200", code)
	}
	if got, want := f.lastSeen(t, token), t0.Add(time.Hour); !got.Equal(want) {
		t.Fatalf("last_seen_at = %s, want untouched %s", got, want)
	}
}

func TestFailedTouchStillServesRequest(t *testing.T) {
	f := newUserFixture(t)
	t0 := f.now
	token := f.signIn(t)
	if _, err := f.db.Exec("CREATE TRIGGER block_touch BEFORE UPDATE ON sessions BEGIN SELECT RAISE(ABORT, 'no'); END;"); err != nil {
		t.Fatal(err)
	}
	if code := f.whoamiAt(t, t0.Add(11*time.Minute), token); code != http.StatusOK {
		t.Fatalf("whoami when the touch fails = %d, want 200", code)
	}
}

func TestTwoFactorRoutesHonourIdleTimeout(t *testing.T) {
	f := newUserFixture(t)
	t0 := f.now
	token := f.signIn(t)
	f.now = t0.Add(24 * time.Hour)
	if code := f.do(http.MethodGet, "/api/auth/account", token, nil).Code; code != http.StatusUnauthorized {
		t.Fatalf("account after 24 idle hours = %d, want 401", code)
	}
}

func TestSessionCookieLifetime(t *testing.T) {
	f := newUserFixture(t)
	response := f.login("admin@example.com", userPassword)
	var cookie *http.Cookie
	for _, c := range response.Result().Cookies() {
		if c.Name == SessionCookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("no session cookie")
	}
	if cookie.MaxAge != 604800 {
		t.Fatalf("MaxAge = %d, want 604800", cookie.MaxAge)
	}
	var expires int64
	if err := f.db.QueryRow("SELECT expires_at FROM sessions WHERE token_hash = ?", HashSessionToken(cookie.Value)).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	if cookie.Expires.Unix() != expires {
		t.Fatalf("cookie Expires = %d, row expires_at = %d", cookie.Expires.Unix(), expires)
	}
}
