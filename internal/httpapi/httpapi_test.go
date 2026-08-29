package httpapi_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"relist-panel/internal/database"
	"relist-panel/internal/httpapi"
)

func TestHealthz(t *testing.T) {
	server := newTestServer(t)
	response := server.request(t, http.MethodGet, "/healthz", nil)
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if got := strings.TrimSpace(string(body)); got != `{"status":"ok"}` {
		t.Fatalf("body = %q, want %q", got, `{"status":"ok"}`)
	}
}

func TestAuthSetupAndLogin(t *testing.T) {
	server := newTestServer(t)

	status := getAuthStatus(t, server)
	if !status.SetupRequired || status.Authenticated {
		t.Fatalf("initial auth status = %+v, want setup required and unauthenticated", status)
	}

	setupResponse := postJSON(t, server, "/api/auth/setup", map[string]string{
		"email":    "Admin@example.com",
		"password": "correct horse battery staple",
	})
	setupResponse.Body.Close()
	if setupResponse.StatusCode != http.StatusCreated {
		t.Fatalf("setup status = %d, want %d", setupResponse.StatusCode, http.StatusCreated)
	}
	status = getAuthStatus(t, server)
	if status.SetupRequired || status.Authenticated {
		t.Fatalf("post-setup auth status = %+v, want configured and unauthenticated", status)
	}

	var passwordHash string
	if err := server.db.QueryRow("SELECT password_hash FROM admin_credentials").Scan(&passwordHash); err != nil {
		t.Fatalf("read password hash: %v", err)
	}
	if passwordHash == "correct horse battery staple" {
		t.Fatal("password was stored in plaintext")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte("correct horse battery staple")); err != nil {
		t.Fatalf("stored password hash does not verify: %v", err)
	}

	secondSetup := postJSON(t, server, "/api/auth/setup", map[string]string{
		"email":    "other@example.com",
		"password": "another valid password",
	})
	secondSetup.Body.Close()
	if secondSetup.StatusCode != http.StatusConflict {
		t.Fatalf("second setup status = %d, want %d", secondSetup.StatusCode, http.StatusConflict)
	}

	wrongLogin := postJSON(t, server, "/api/auth/login", map[string]string{
		"email":    "admin@example.com",
		"password": "wrong",
	})
	wrongLogin.Body.Close()
	if wrongLogin.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong login status = %d, want %d", wrongLogin.StatusCode, http.StatusUnauthorized)
	}

	loginResponse := postJSON(t, server, "/api/auth/login", map[string]string{
		"email":    "admin@example.com",
		"password": "correct horse battery staple",
	})
	loginResponse.Body.Close()
	if loginResponse.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want %d", loginResponse.StatusCode, http.StatusOK)
	}
	cookies := loginResponse.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login cookies = %d, want one session cookie", len(cookies))
	}
	sessionCookie := cookies[0]
	if sessionCookie.Name != "relist_session" || sessionCookie.Value == "" {
		t.Fatalf("session cookie = %+v", sessionCookie)
	}
	if !sessionCookie.HttpOnly || sessionCookie.SameSite != http.SameSiteLaxMode || sessionCookie.Path != "/" {
		t.Fatalf("session cookie security attributes = %+v", sessionCookie)
	}

	status = getAuthStatus(t, server)
	if status.SetupRequired || !status.Authenticated {
		t.Fatalf("authenticated auth status = %+v, want configured and authenticated", status)
	}

	protectedResponse := server.request(t, http.MethodGet, "/api/projects", nil)
	protectedResponse.Body.Close()
	if protectedResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("protected route with session status = %d, want %d after middleware", protectedResponse.StatusCode, http.StatusNotFound)
	}
}

func TestAuthMiddlewareRejectsUnauthenticatedRoutes(t *testing.T) {
	server := newTestServer(t)

	response := server.request(t, http.MethodGet, "/api/projects", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated protected route status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
}

func TestLogoutRevokesSessionAndClearsCookie(t *testing.T) {
	server := newTestServer(t)

	setupResponse := postJSON(t, server, "/api/auth/setup", map[string]string{
		"email":    "admin@example.com",
		"password": "correct horse battery staple",
	})
	setupResponse.Body.Close()
	loginResponse := postJSON(t, server, "/api/auth/login", map[string]string{
		"email":    "admin@example.com",
		"password": "correct horse battery staple",
	})
	loginResponse.Body.Close()

	logoutResponse := postJSON(t, server, "/api/auth/logout", nil)
	defer logoutResponse.Body.Close()
	if logoutResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status = %d, want %d", logoutResponse.StatusCode, http.StatusNoContent)
	}
	if cookies := logoutResponse.Cookies(); len(cookies) != 1 || cookies[0].Name != "relist_session" || cookies[0].MaxAge >= 0 {
		t.Fatalf("logout cookie = %+v, want an expired session cookie", cookies)
	}

	status := getAuthStatus(t, server)
	if status.Authenticated {
		t.Fatalf("auth status after logout = %+v, want unauthenticated", status)
	}
	protectedResponse := server.request(t, http.MethodGet, "/api/projects", nil)
	protectedResponse.Body.Close()
	if protectedResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("protected route after logout status = %d, want %d", protectedResponse.StatusCode, http.StatusUnauthorized)
	}
}

func TestRootServesEmbeddedFrontend(t *testing.T) {
	server := newTestServer(t)

	response := server.request(t, http.MethodGet, "/", nil)
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "Relist Panel") {
		t.Fatalf("embedded frontend does not contain the application shell")
	}
}

type authStatus struct {
	SetupRequired bool `json:"setupRequired"`
	Authenticated bool `json:"authenticated"`
}

type testServer struct {
	handler http.Handler
	db      *sql.DB
	cookies map[string]*http.Cookie
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return &testServer{
		handler: httpapi.NewHandler(db),
		db:      db,
		cookies: make(map[string]*http.Cookie),
	}
}

func (s *testServer) request(t *testing.T, method, path string, value any) *http.Response {
	t.Helper()
	var body io.Reader
	if value != nil {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("encode JSON: %v", err)
		}
		body = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, "http://relist-panel.test"+path, body)
	if value != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for _, cookie := range s.cookies {
		request.AddCookie(cookie)
	}

	recorder := httptest.NewRecorder()
	s.handler.ServeHTTP(recorder, request)
	response := recorder.Result()
	for _, cookie := range response.Cookies() {
		if cookie.MaxAge < 0 || cookie.Value == "" {
			delete(s.cookies, cookie.Name)
			continue
		}
		s.cookies[cookie.Name] = cookie
	}
	return response
}

func getAuthStatus(t *testing.T, server *testServer) authStatus {
	t.Helper()
	response := server.request(t, http.MethodGet, "/api/auth/status", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("auth status response = %d, want %d", response.StatusCode, http.StatusOK)
	}
	var status authStatus
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		t.Fatalf("decode auth status: %v", err)
	}
	return status
}

func postJSON(t *testing.T, server *testServer, path string, value any) *http.Response {
	t.Helper()
	return server.request(t, http.MethodPost, path, value)
}
