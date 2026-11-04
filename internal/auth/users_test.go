package auth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

var (
	owner  = User{ID: 1, Email: "admin@example.com", IsAdmin: true}
	second = User{ID: 2, Email: "second@example.com", IsAdmin: true}
)

// serveAs calls the routes directly as user, the way they run behind auth
// and authz (which alone keeps them admin-only).
func (f *userFixture) serveAs(t *testing.T, user User, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var encoded []byte
	if body != nil {
		encoded, _ = json.Marshal(body)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	request = request.WithContext(WithUser(request.Context(), user))
	recorder := httptest.NewRecorder()
	f.mux.ServeHTTP(recorder, request)
	return recorder
}

func (f *userFixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func TestCreateUserReturnsInviteOnce(t *testing.T) {
	f := newUserFixture(t)
	response := f.serveAs(t, owner, http.MethodPost, "/api/users", map[string]any{"email": " Bob@Example.com ", "isAdmin": false})
	if response.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", response.Code, response.Body.String())
	}
	body := decodeBody(t, response)
	user, _ := body["user"].(map[string]any)
	token, _ := body["token"].(string)
	if user["email"] != "bob@example.com" || user["status"] != "invited" || user["isAdmin"] != false || user["isOwner"] != false {
		t.Fatalf("user = %v", user)
	}
	if len(token) != 43 || body["kind"] != "invite" {
		t.Fatalf("token %q kind %v", token, body["kind"])
	}
	var kind, hash string
	var expires int64
	if err := f.db.QueryRow("SELECT kind, token_hash, expires_at FROM user_tokens").Scan(&kind, &hash, &expires); err != nil {
		t.Fatal(err)
	}
	if kind != "invite" || hash != hashToken(token) || expires != f.now.Add(7*24*time.Hour).Unix() {
		t.Fatalf("token row = %s %s %d", kind, hash, expires)
	}
	if f.count(t, "SELECT COUNT(*) FROM user_tokens WHERE token_hash = ?", token) != 0 {
		t.Fatal("the raw token is stored")
	}
}

func TestCreateUserRejectsDuplicateEmailAnyCase(t *testing.T) {
	f := newUserFixture(t)
	f.serveAs(t, owner, http.MethodPost, "/api/users", map[string]any{"email": "bob@example.com"})
	response := f.serveAs(t, owner, http.MethodPost, "/api/users", map[string]any{"email": "BOB@example.com"})
	if response.Code != http.StatusConflict || decodeBody(t, response)["error"] != "A user with this email already exists" {
		t.Fatalf("duplicate = %d %s", response.Code, response.Body.String())
	}
	if n := f.count(t, "SELECT COUNT(*) FROM users"); n != 2 {
		t.Fatalf("users = %d, want 2", n)
	}
}

func TestCreateUserValidatesEmail(t *testing.T) {
	f := newUserFixture(t)
	if response := f.serveAs(t, owner, http.MethodPost, "/api/users", map[string]any{"email": "not an email"}); response.Code != http.StatusBadRequest {
		t.Fatalf("bad email = %d", response.Code)
	}
}

func TestListAndGetUsers(t *testing.T) {
	f := newUserFixture(t)
	f.addUser(t, 2, "invited@example.com", false, true)
	f.addUser(t, 3, "disabled@example.com", true, false)
	if _, err := f.db.Exec("UPDATE users SET disabled_at = 5 WHERE id = 3"); err != nil {
		t.Fatal(err)
	}

	response := f.serveAs(t, owner, http.MethodGet, "/api/users", nil)
	var users []map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &users); err != nil || len(users) != 3 {
		t.Fatalf("list = %s, %v", response.Body.String(), err)
	}
	want := []struct {
		status  string
		owner   bool
		isAdmin bool
	}{{"active", true, true}, {"invited", false, false}, {"disabled", false, true}}
	for i, w := range want {
		if users[i]["status"] != w.status || users[i]["isOwner"] != w.owner || users[i]["isAdmin"] != w.isAdmin {
			t.Errorf("users[%d] = %v, want %+v", i, users[i], w)
		}
	}
	if users[0]["createdAt"] == nil || users[1]["lastLoginAt"] != nil {
		t.Errorf("timestamps = %v / %v", users[0]["createdAt"], users[1]["lastLoginAt"])
	}

	if got := f.serveAs(t, owner, http.MethodGet, "/api/users/2", nil); got.Code != http.StatusOK || decodeBody(t, got)["email"] != "invited@example.com" {
		t.Fatalf("get = %d %s", got.Code, got.Body.String())
	}
	if got := f.serveAs(t, owner, http.MethodGet, "/api/users/99", nil); got.Code != http.StatusNotFound {
		t.Fatalf("get missing = %d", got.Code)
	}
}

func TestOwnerCannotBeDisabledDemotedOrDeleted(t *testing.T) {
	f := newUserFixture(t)
	f.addUser(t, 2, "second@example.com", true, false)
	cases := []struct {
		method, message string
		body            any
	}{
		{http.MethodPatch, "The owner cannot be disabled or demoted", map[string]any{"disabled": true}},
		{http.MethodPatch, "The owner cannot be disabled or demoted", map[string]any{"isAdmin": false}},
		{http.MethodDelete, "The owner cannot be deleted", nil},
	}
	for _, c := range cases {
		response := f.serveAs(t, second, c.method, "/api/users/1", c.body)
		if response.Code != http.StatusForbidden || decodeBody(t, response)["error"] != c.message {
			t.Errorf("%s %v = %d %s", c.method, c.body, response.Code, response.Body.String())
		}
	}
	if n := f.count(t, "SELECT COUNT(*) FROM users WHERE id = 1 AND disabled_at IS NULL AND is_admin = 1"); n != 1 {
		t.Fatal("the owner row changed")
	}
}

func TestPatchUserTogglesAdminAndDisabled(t *testing.T) {
	f := newUserFixture(t)
	f.addUser(t, 3, "bob@example.com", false, false)
	response := f.serveAs(t, owner, http.MethodPatch, "/api/users/3", map[string]any{"isAdmin": true})
	if response.Code != http.StatusOK || decodeBody(t, response)["isAdmin"] != true {
		t.Fatalf("make admin = %d %s", response.Code, response.Body.String())
	}
	f.serveAs(t, owner, http.MethodPatch, "/api/users/3", map[string]any{"disabled": true})
	response = f.serveAs(t, owner, http.MethodPatch, "/api/users/3", map[string]any{"disabled": false})
	if response.Code != http.StatusOK || decodeBody(t, response)["status"] != "active" {
		t.Fatalf("re-enable = %d %s", response.Code, response.Body.String())
	}
}

func TestDisableEndsSessions(t *testing.T) {
	f := newUserFixture(t)
	f.addUser(t, 3, "bob@example.com", false, false)
	f.addSession(t, 3, "a")
	f.addSession(t, 3, "b")
	response := f.serveAs(t, owner, http.MethodPatch, "/api/users/3", map[string]any{"disabled": true})
	if response.Code != http.StatusOK || decodeBody(t, response)["status"] != "disabled" {
		t.Fatalf("disable = %d %s", response.Code, response.Body.String())
	}
	if n := f.count(t, "SELECT COUNT(*) FROM sessions WHERE user_id = 3"); n != 0 {
		t.Fatalf("sessions = %d, want 0", n)
	}
}

func TestResetActiveUserEndsSessionsAndIssuesResetToken(t *testing.T) {
	f := newUserFixture(t)
	f.addUser(t, 3, "bob@example.com", false, false)
	f.addSession(t, 3, "a")
	response := f.serveAs(t, owner, http.MethodPost, "/api/users/3/reset", nil)
	body := decodeBody(t, response)
	if response.Code != http.StatusOK || body["kind"] != "reset" {
		t.Fatalf("reset = %d %v", response.Code, body)
	}
	expires, err := time.Parse(time.RFC3339, body["expiresAt"].(string))
	if err != nil || !expires.Equal(f.now.Add(24*time.Hour)) {
		t.Fatalf("expiresAt = %v, %v", body["expiresAt"], err)
	}
	if n := f.count(t, "SELECT COUNT(*) FROM sessions WHERE user_id = 3"); n != 0 {
		t.Fatalf("sessions = %d", n)
	}
	if n := f.count(t, "SELECT COUNT(*) FROM user_tokens WHERE user_id = 3 AND used_at IS NULL"); n != 1 {
		t.Fatalf("open tokens = %d", n)
	}
}

func TestResetInvitedUserReissuesInvite(t *testing.T) {
	f := newUserFixture(t)
	created := decodeBody(t, f.serveAs(t, owner, http.MethodPost, "/api/users", map[string]any{"email": "bob@example.com"}))
	old := created["token"].(string)
	user := created["user"].(map[string]any)

	response := f.serveAs(t, owner, http.MethodPost, "/api/users/"+jsonID(user["id"])+"/reset", nil)
	body := decodeBody(t, response)
	if response.Code != http.StatusOK || body["kind"] != "invite" || body["token"] == old {
		t.Fatalf("reissue = %d %v", response.Code, body)
	}
	if n := f.count(t, "SELECT COUNT(*) FROM user_tokens WHERE token_hash = ?", hashToken(old)); n != 0 {
		t.Fatal("the old invite still exists")
	}
}

func TestResetDisabledUserIsRefused(t *testing.T) {
	f := newUserFixture(t)
	f.addUser(t, 3, "bob@example.com", false, false)
	f.db.Exec("UPDATE users SET disabled_at = 5 WHERE id = 3")
	response := f.serveAs(t, owner, http.MethodPost, "/api/users/3/reset", nil)
	if response.Code != http.StatusConflict || decodeBody(t, response)["error"] != "Enable this user before sending a link" {
		t.Fatalf("reset disabled = %d %s", response.Code, response.Body.String())
	}
}

func TestOwnerCannotBeResetByAnotherAdmin(t *testing.T) {
	f := newUserFixture(t)
	f.addUser(t, 2, "second@example.com", true, false)
	response := f.serveAs(t, second, http.MethodPost, "/api/users/1/reset", nil)
	if response.Code != http.StatusForbidden || decodeBody(t, response)["error"] != "Only the owner can reset the owner" {
		t.Fatalf("reset owner by admin = %d %s", response.Code, response.Body.String())
	}
	if response := f.serveAs(t, owner, http.MethodPost, "/api/users/1/reset", nil); response.Code != http.StatusOK {
		t.Fatalf("reset owner by owner = %d %s", response.Code, response.Body.String())
	}
}

func TestDeleteUserCascades(t *testing.T) {
	f := newUserFixture(t)
	created := decodeBody(t, f.serveAs(t, owner, http.MethodPost, "/api/users", map[string]any{"email": "bob@example.com"}))
	id := jsonID(created["user"].(map[string]any)["id"])
	f.addSession(t, 2, "s")
	if response := f.serveAs(t, owner, http.MethodDelete, "/api/users/"+id, nil); response.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", response.Code, response.Body.String())
	}
	if f.count(t, "SELECT COUNT(*) FROM users WHERE id = 2")+f.count(t, "SELECT COUNT(*) FROM user_tokens")+f.count(t, "SELECT COUNT(*) FROM sessions WHERE user_id = 2") != 0 {
		t.Fatal("rows left behind")
	}
	if response := f.serveAs(t, owner, http.MethodDelete, "/api/users/"+id, nil); response.Code != http.StatusNotFound {
		t.Fatalf("delete again = %d", response.Code)
	}
}

func jsonID(v any) string {
	encoded, _ := json.Marshal(v)
	return string(encoded)
}
