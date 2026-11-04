package auth

import (
	"net/http"
	"testing"
	"time"
)

const newPassword = "a brand new password"

// invite creates bob@example.com (id 2) and returns his invite token.
func (f *userFixture) invite(t *testing.T) string {
	t.Helper()
	response := f.serveAs(t, owner, http.MethodPost, "/api/users", map[string]any{"email": "bob@example.com"})
	if response.Code != http.StatusCreated {
		t.Fatalf("invite = %d %s", response.Code, response.Body.String())
	}
	return decodeBody(t, response)["token"].(string)
}

func expectInvalidLink(t *testing.T, f *userFixture, method, token string) {
	t.Helper()
	var body any
	if method == http.MethodPost {
		body = map[string]string{"password": newPassword}
	}
	response := f.do(method, "/api/invites/"+token, "", body)
	if response.Code != http.StatusNotFound || decodeBody(t, response)["error"] != "This link is no longer valid" {
		t.Fatalf("%s invite = %d %s, want the invalid-link 404", method, response.Code, response.Body.String())
	}
}

func TestInviteShowsEmailAndKind(t *testing.T) {
	f := newUserFixture(t)
	token := f.invite(t)
	body := decodeBody(t, f.do(http.MethodGet, "/api/invites/"+token, "", nil))
	if body["email"] != "bob@example.com" || body["kind"] != "invite" {
		t.Fatalf("invite = %v", body)
	}
}

func TestAcceptInviteSetsPasswordAndSignsIn(t *testing.T) {
	f := newUserFixture(t)
	token := f.invite(t)
	response := f.do(http.MethodPost, "/api/invites/"+token, "", map[string]string{"password": newPassword})
	body := decodeBody(t, response)
	if response.Code != http.StatusOK || body["authenticated"] != true || body["email"] != "bob@example.com" {
		t.Fatalf("accept = %d %v", response.Code, body)
	}
	session := sessionCookie(t, response)
	if who := decodeBody(t, f.do(http.MethodGet, "/api/whoami", session, nil)); who["id"] != float64(2) {
		t.Fatalf("signed in as %v", who)
	}
	if login := f.login("bob@example.com", newPassword); login.Code != http.StatusOK {
		t.Fatalf("login with the new password = %d", login.Code)
	}
	if status := decodeBody(t, f.serveAs(t, owner, http.MethodGet, "/api/users/2", nil))["status"]; status != "active" {
		t.Fatalf("status = %v", status)
	}
}

func TestInviteCannotBeReused(t *testing.T) {
	f := newUserFixture(t)
	token := f.invite(t)
	if response := f.do(http.MethodPost, "/api/invites/"+token, "", map[string]string{"password": newPassword}); response.Code != http.StatusOK {
		t.Fatalf("first accept = %d", response.Code)
	}
	expectInvalidLink(t, f, http.MethodPost, token)
	expectInvalidLink(t, f, http.MethodGet, token)
}

func TestInviteExpires(t *testing.T) {
	f := newUserFixture(t)
	token := f.invite(t)
	f.now = f.now.Add(7*24*time.Hour + time.Second)
	expectInvalidLink(t, f, http.MethodGet, token)
	expectInvalidLink(t, f, http.MethodPost, token)
}

func TestUnknownInviteIsInvalid(t *testing.T) {
	f := newUserFixture(t)
	expectInvalidLink(t, f, http.MethodGet, "nope")
}

func TestInviteRefusedForDisabledUser(t *testing.T) {
	f := newUserFixture(t)
	token := f.invite(t)
	if _, err := f.db.Exec("UPDATE users SET disabled_at = 5 WHERE id = 2"); err != nil {
		t.Fatal(err)
	}
	before := f.count(t, "SELECT COUNT(*) FROM sessions")
	expectInvalidLink(t, f, http.MethodGet, token)
	expectInvalidLink(t, f, http.MethodPost, token)
	if after := f.count(t, "SELECT COUNT(*) FROM sessions"); after != before {
		t.Fatalf("sessions %d -> %d", before, after)
	}
}

func TestResetTurnsTwoFactorOffAndEndsOtherSessions(t *testing.T) {
	f := newUserFixture(t)
	f.addUser(t, 2, "bob@example.com", false, false)
	if _, err := f.db.Exec("UPDATE users SET totp_secret = 'JBSWY3DPEHPK3PXP', totp_last_step = 4 WHERE id = 2"); err != nil {
		t.Fatal(err)
	}
	link := decodeBody(t, f.serveAs(t, owner, http.MethodPost, "/api/users/2/reset", nil))
	token := link["token"].(string)
	// Sessions made after the reset link was issued still end when it is used.
	f.addSession(t, 2, "a")
	f.addSession(t, 2, "b")

	if body := decodeBody(t, f.do(http.MethodGet, "/api/invites/"+token, "", nil)); body["kind"] != "reset" {
		t.Fatalf("kind = %v", body["kind"])
	}
	response := f.do(http.MethodPost, "/api/invites/"+token, "", map[string]string{"password": newPassword})
	if response.Code != http.StatusOK {
		t.Fatalf("accept reset = %d %s", response.Code, response.Body.String())
	}
	if n := f.count(t, "SELECT COUNT(*) FROM users WHERE id = 2 AND totp_secret IS NULL AND totp_last_step IS NULL AND totp_pending_secret IS NULL"); n != 1 {
		t.Fatal("two-factor is still on")
	}
	if n := f.count(t, "SELECT COUNT(*) FROM sessions WHERE user_id = 2"); n != 1 {
		t.Fatalf("sessions = %d, want only the new one", n)
	}
	if n := f.count(t, "SELECT COUNT(*) FROM sessions WHERE token = ?", sessionCookie(t, response)); n != 1 {
		t.Fatal("the remaining session is not the new one")
	}
}

func TestWeakPasswordKeepsTokenUsable(t *testing.T) {
	f := newUserFixture(t)
	token := f.invite(t)
	response := f.do(http.MethodPost, "/api/invites/"+token, "", map[string]string{"password": "short"})
	if response.Code != http.StatusBadRequest || decodeBody(t, response)["error"] != "password must be at least 8 characters" {
		t.Fatalf("weak = %d %s", response.Code, response.Body.String())
	}
	if response := f.do(http.MethodPost, "/api/invites/"+token, "", map[string]string{"password": newPassword}); response.Code != http.StatusOK {
		t.Fatalf("then valid = %d", response.Code)
	}
}

func TestInviteRoutesArePublic(t *testing.T) {
	f := newUserFixture(t)
	// f.do goes through Middleware without a cookie.
	expectInvalidLink(t, f, http.MethodGet, "unknown")
}
