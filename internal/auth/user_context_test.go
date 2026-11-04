package auth

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"relist-panel/internal/database"
)

const userPassword = "correct horse battery staple"

// userFixture is a migrated panel with the Owner (id 1, admin@example.com)
// and a clock the test controls.
type userFixture struct {
	db      *sql.DB
	service *Service
	handler http.Handler
	now     time.Time
}

func newUserFixture(t *testing.T) *userFixture {
	t.Helper()
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := CreateAdmin(context.Background(), db, "admin@example.com", userPassword, time.Now); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	f := &userFixture{db: db, now: time.Unix(1_800_000_000, 0).UTC()}
	f.service = newServiceWithClock(db, func() time.Time { return f.now })
	mux := http.NewServeMux()
	f.service.RegisterRoutes(mux)
	mux.HandleFunc("GET /api/whoami", func(w http.ResponseWriter, r *http.Request) {
		user, ok := UserFrom(r.Context())
		if !ok {
			writeError(w, http.StatusInternalServerError, "no user")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": user.ID, "email": user.Email, "isAdmin": user.IsAdmin, "isOwner": user.IsOwner()})
	})
	f.handler = f.service.Middleware(mux)
	return f
}

// addUser inserts a User with password userPassword (or none when invited).
func (f *userFixture) addUser(t *testing.T, id int64, email string, isAdmin, invited bool) {
	t.Helper()
	var hash sql.NullString
	if !invited {
		h, err := bcrypt.GenerateFromPassword([]byte(userPassword), bcrypt.MinCost)
		if err != nil {
			t.Fatal(err)
		}
		hash = sql.NullString{String: string(h), Valid: true}
	}
	if _, err := f.db.Exec(
		"INSERT INTO users (id, email, password_hash, is_admin, created_at) VALUES (?, ?, ?, ?, 1)",
		id, email, hash, isAdmin,
	); err != nil {
		t.Fatalf("add user: %v", err)
	}
}

// addSession gives userID a live session and returns its token.
func (f *userFixture) addSession(t *testing.T, userID int64, token string) string {
	t.Helper()
	if _, err := f.db.Exec(
		"INSERT INTO sessions (token, user_id, expires_at, created_at) VALUES (?, ?, ?, ?)",
		token, userID, f.now.Add(time.Hour).Unix(), f.now.Unix(),
	); err != nil {
		t.Fatalf("add session: %v", err)
	}
	return token
}

func (f *userFixture) do(method, path, token string, body any) *httptest.ResponseRecorder {
	var reader *bytes.Reader
	if body != nil {
		encoded, _ := json.Marshal(body)
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}
	request := httptest.NewRequest(method, path, reader)
	if token != "" {
		request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: token})
	}
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, request)
	return recorder
}

func (f *userFixture) login(email, password string) *httptest.ResponseRecorder {
	return f.do(http.MethodPost, "/api/auth/login", "", map[string]string{"email": email, "password": password})
}

func sessionCookie(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == SessionCookieName && cookie.Value != "" {
			return cookie.Value
		}
	}
	t.Fatalf("no session cookie in %d %s", recorder.Code, recorder.Body.String())
	return ""
}

func decodeBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", recorder.Body.String(), err)
	}
	return body
}

func TestMiddlewarePutsUserInContext(t *testing.T) {
	f := newUserFixture(t)
	token := sessionCookie(t, f.login("admin@example.com", userPassword))

	response := f.do(http.MethodGet, "/api/whoami", token, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("whoami = %d %s", response.Code, response.Body.String())
	}
	body := decodeBody(t, response)
	if body["id"] != float64(1) || body["email"] != "admin@example.com" || body["isAdmin"] != true || body["isOwner"] != true {
		t.Fatalf("whoami = %v", body)
	}
}

func TestDisabledUserSessionIsRejected(t *testing.T) {
	f := newUserFixture(t)
	f.addUser(t, 2, "bob@example.com", false, false)
	token := f.addSession(t, 2, "bob-session")
	if response := f.do(http.MethodGet, "/api/whoami", token, nil); response.Code != http.StatusOK {
		t.Fatalf("before disable = %d", response.Code)
	}
	if _, err := f.db.Exec("UPDATE users SET disabled_at = 5 WHERE id = 2"); err != nil {
		t.Fatal(err)
	}

	response := f.do(http.MethodGet, "/api/whoami", token, nil)
	if response.Code != http.StatusUnauthorized || decodeBody(t, response)["error"] != "authentication required" {
		t.Fatalf("after disable = %d %s", response.Code, response.Body.String())
	}
}

func TestLoginRefusesInvitedAndDisabledLikeAWrongPassword(t *testing.T) {
	f := newUserFixture(t)
	f.addUser(t, 2, "invited@example.com", false, true)
	f.addUser(t, 3, "disabled@example.com", false, false)
	if _, err := f.db.Exec("UPDATE users SET disabled_at = 5 WHERE id = 3"); err != nil {
		t.Fatal(err)
	}

	for _, email := range []string{"invited@example.com", "disabled@example.com"} {
		response := f.login(email, userPassword)
		if response.Code != http.StatusUnauthorized || decodeBody(t, response)["error"] != "invalid email or password" {
			t.Fatalf("login %s = %d %s", email, response.Code, response.Body.String())
		}
	}
	var failed int
	if err := f.db.QueryRow("SELECT SUM(failed_attempts) FROM users WHERE id IN (2, 3)").Scan(&failed); err != nil || failed != 0 {
		t.Fatalf("failed_attempts = %d, %v, want 0", failed, err)
	}
}

func TestStatusReportsUser(t *testing.T) {
	f := newUserFixture(t)
	f.addUser(t, 2, "bob@example.com", false, false)
	token := sessionCookie(t, f.login("bob@example.com", userPassword))

	body := decodeBody(t, f.do(http.MethodGet, "/api/auth/status", token, nil))
	user, _ := body["user"].(map[string]any)
	if body["authenticated"] != true || user["id"] != float64(2) || user["email"] != "bob@example.com" || user["isAdmin"] != false || user["isOwner"] != false {
		t.Fatalf("status = %v", body)
	}
}

func TestLoginRecordsLastLogin(t *testing.T) {
	f := newUserFixture(t)
	if response := f.login("admin@example.com", userPassword); response.Code != http.StatusOK {
		t.Fatalf("login = %d", response.Code)
	}
	var last sql.NullInt64
	if err := f.db.QueryRow("SELECT last_login_at FROM users WHERE id = 1").Scan(&last); err != nil || !last.Valid || last.Int64 != f.now.Unix() {
		t.Fatalf("last_login_at = %v, %v, want %d", last, err, f.now.Unix())
	}
}
