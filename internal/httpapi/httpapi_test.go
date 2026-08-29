package httpapi_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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
	if protectedResponse.StatusCode != http.StatusOK {
		t.Fatalf("protected route with session status = %d, want %d", protectedResponse.StatusCode, http.StatusOK)
	}
}

func TestAuthenticatedAdminCanCreateAndListProjects(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)

	createResponse := postJSON(t, server, "/api/projects", map[string]string{
		"name":        "Storefront",
		"description": "The customer-facing application",
	})
	defer createResponse.Body.Close()
	if createResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create project status = %d, want %d", createResponse.StatusCode, http.StatusCreated)
	}

	var created projectResponse
	if err := json.NewDecoder(createResponse.Body).Decode(&created); err != nil {
		t.Fatalf("decode created project: %v", err)
	}
	if created.ID <= 0 || created.Name != "Storefront" || created.Description != "The customer-facing application" {
		t.Fatalf("created project = %+v", created)
	}
	if created.EnvironmentCount != 1 || created.CreatedAt == "" || created.LastUpdated == "" {
		t.Fatalf("created project metadata = %+v", created)
	}

	listResponse := server.request(t, http.MethodGet, "/api/projects", nil)
	defer listResponse.Body.Close()
	if listResponse.StatusCode != http.StatusOK {
		t.Fatalf("list projects status = %d, want %d", listResponse.StatusCode, http.StatusOK)
	}
	var projects []projectResponse
	if err := json.NewDecoder(listResponse.Body).Decode(&projects); err != nil {
		t.Fatalf("decode projects list: %v", err)
	}
	if len(projects) != 1 || projects[0].ID != created.ID || projects[0].Name != "Storefront" {
		t.Fatalf("projects list = %+v, want the created project", projects)
	}
}

func TestAuthenticatedAdminCanEditProject(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)

	first := createProject(t, server, "Storefront", "Customer-facing application")
	second := createProject(t, server, "Operations", "Internal tooling")

	path := "/api/projects/" + strconv.FormatInt(first.ID, 10)
	updateResponse := server.request(t, http.MethodPut, path, map[string]string{
		"name":        "Storefront API",
		"description": "Updated customer-facing application",
	})
	defer updateResponse.Body.Close()
	if updateResponse.StatusCode != http.StatusOK {
		t.Fatalf("update project status = %d, want %d", updateResponse.StatusCode, http.StatusOK)
	}

	var updated projectResponse
	if err := json.NewDecoder(updateResponse.Body).Decode(&updated); err != nil {
		t.Fatalf("decode updated project: %v", err)
	}
	if updated.ID != first.ID || updated.Name != "Storefront API" || updated.Description != "Updated customer-facing application" {
		t.Fatalf("updated project = %+v", updated)
	}
	if updated.EnvironmentCount != 1 || updated.CreatedAt == "" || updated.LastUpdated == "" {
		t.Fatalf("updated project metadata = %+v", updated)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, updated.CreatedAt)
	if err != nil {
		t.Fatalf("parse project creation time: %v", err)
	}
	lastUpdated, err := time.Parse(time.RFC3339Nano, updated.LastUpdated)
	if err != nil {
		t.Fatalf("parse project update time: %v", err)
	}
	if !lastUpdated.After(createdAt) {
		t.Fatalf("project last updated = %s, want after created at %s", lastUpdated, createdAt)
	}

	sameResponse := server.request(t, http.MethodPut, path, map[string]string{
		"name":        "Storefront API",
		"description": "Updated customer-facing application",
	})
	sameResponse.Body.Close()
	if sameResponse.StatusCode != http.StatusOK {
		t.Fatalf("idempotent project update status = %d, want %d", sameResponse.StatusCode, http.StatusOK)
	}

	getResponse := server.request(t, http.MethodGet, path, nil)
	defer getResponse.Body.Close()
	if getResponse.StatusCode != http.StatusOK {
		t.Fatalf("get updated project status = %d, want %d", getResponse.StatusCode, http.StatusOK)
	}
	var fetched projectResponse
	if err := json.NewDecoder(getResponse.Body).Decode(&fetched); err != nil {
		t.Fatalf("decode fetched project: %v", err)
	}
	if fetched.ID != updated.ID || fetched.Name != updated.Name || fetched.Description != updated.Description {
		t.Fatalf("fetched project = %+v, want %+v", fetched, updated)
	}

	duplicateResponse := server.request(t, http.MethodPut, "/api/projects/"+strconv.FormatInt(second.ID, 10), map[string]string{
		"name":        "Storefront API",
		"description": "Duplicate names are rejected",
	})
	defer duplicateResponse.Body.Close()
	if duplicateResponse.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate project update status = %d, want %d", duplicateResponse.StatusCode, http.StatusConflict)
	}
}

func TestAuthenticatedAdminCanDeleteProject(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	project := createProject(t, server, "Temporary", "Will be removed")
	path := "/api/projects/" + strconv.FormatInt(project.ID, 10)

	deleteResponse := server.request(t, http.MethodDelete, path, nil)
	defer deleteResponse.Body.Close()
	if deleteResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("delete project status = %d, want %d", deleteResponse.StatusCode, http.StatusNoContent)
	}

	getResponse := server.request(t, http.MethodGet, path, nil)
	defer getResponse.Body.Close()
	if getResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted project status = %d, want %d", getResponse.StatusCode, http.StatusNotFound)
	}

	deleteAgainResponse := server.request(t, http.MethodDelete, path, nil)
	defer deleteAgainResponse.Body.Close()
	if deleteAgainResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("deleting missing project status = %d, want %d", deleteAgainResponse.StatusCode, http.StatusNotFound)
	}

	listResponse := server.request(t, http.MethodGet, "/api/projects", nil)
	defer listResponse.Body.Close()
	if listResponse.StatusCode != http.StatusOK {
		t.Fatalf("list after delete status = %d, want %d", listResponse.StatusCode, http.StatusOK)
	}
	var projects []projectResponse
	if err := json.NewDecoder(listResponse.Body).Decode(&projects); err != nil {
		t.Fatalf("decode projects after delete: %v", err)
	}
	if len(projects) != 0 {
		t.Fatalf("projects after delete = %+v, want empty list", projects)
	}
}

func TestProjectNamesAreUniqueAndNamesAreRequired(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	createProject(t, server, "Storefront", "First project")

	duplicateResponse := postJSON(t, server, "/api/projects", map[string]string{
		"name":        " storefront ",
		"description": "Duplicate project",
	})
	defer duplicateResponse.Body.Close()
	if duplicateResponse.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate project status = %d, want %d", duplicateResponse.StatusCode, http.StatusConflict)
	}

	missingNameResponse := postJSON(t, server, "/api/projects", map[string]string{
		"description": "Missing name",
	})
	defer missingNameResponse.Body.Close()
	if missingNameResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing project name status = %d, want %d", missingNameResponse.StatusCode, http.StatusBadRequest)
	}
}

func TestProjectCreationProvisionsProductionEnvironment(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	project := createProject(t, server, "Storefront", "Customer-facing application")

	response := server.request(t, http.MethodGet, projectEnvironmentsPath(project.ID), nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("list environments status = %d, want %d", response.StatusCode, http.StatusOK)
	}

	var environments []environmentResponse
	if err := json.NewDecoder(response.Body).Decode(&environments); err != nil {
		t.Fatalf("decode environments: %v", err)
	}
	if len(environments) != 1 {
		t.Fatalf("environments = %+v, want one default environment", environments)
	}
	if environments[0].ID <= 0 || environments[0].ProjectID != project.ID || environments[0].Name != "production" || environments[0].CreatedAt == "" {
		t.Fatalf("default environment = %+v", environments[0])
	}
}

func TestAuthenticatedAdminCanCreateAndDeleteEnvironments(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	project := createProject(t, server, "Storefront", "Customer-facing application")

	createResponse := postJSON(t, server, projectEnvironmentsPath(project.ID), map[string]string{"name": "staging"})
	defer createResponse.Body.Close()
	if createResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create environment status = %d, want %d", createResponse.StatusCode, http.StatusCreated)
	}

	var staging environmentResponse
	if err := json.NewDecoder(createResponse.Body).Decode(&staging); err != nil {
		t.Fatalf("decode created environment: %v", err)
	}
	if staging.ID <= 0 || staging.ProjectID != project.ID || staging.Name != "staging" || staging.CreatedAt == "" {
		t.Fatalf("created environment = %+v", staging)
	}

	listResponse := server.request(t, http.MethodGet, projectEnvironmentsPath(project.ID), nil)
	defer listResponse.Body.Close()
	if listResponse.StatusCode != http.StatusOK {
		t.Fatalf("list environments status = %d, want %d", listResponse.StatusCode, http.StatusOK)
	}
	var environments []environmentResponse
	if err := json.NewDecoder(listResponse.Body).Decode(&environments); err != nil {
		t.Fatalf("decode environment list: %v", err)
	}
	if len(environments) != 2 || environments[0].Name != "production" || environments[1].Name != "staging" {
		t.Fatalf("environment list = %+v, want production and staging", environments)
	}

	duplicateResponse := postJSON(t, server, projectEnvironmentsPath(project.ID), map[string]string{"name": " STAGING "})
	defer duplicateResponse.Body.Close()
	if duplicateResponse.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate environment status = %d, want %d", duplicateResponse.StatusCode, http.StatusConflict)
	}

	otherProject := createProject(t, server, "Operations", "Internal tooling")
	otherProjectResponse := postJSON(t, server, projectEnvironmentsPath(otherProject.ID), map[string]string{"name": "staging"})
	defer otherProjectResponse.Body.Close()
	if otherProjectResponse.StatusCode != http.StatusCreated {
		t.Fatalf("same environment name in another project status = %d, want %d", otherProjectResponse.StatusCode, http.StatusCreated)
	}

	deleteResponse := server.request(t, http.MethodDelete, environmentPath(project.ID, staging.ID), nil)
	defer deleteResponse.Body.Close()
	if deleteResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("delete environment status = %d, want %d", deleteResponse.StatusCode, http.StatusNoContent)
	}

	remainingResponse := server.request(t, http.MethodGet, projectEnvironmentsPath(project.ID), nil)
	defer remainingResponse.Body.Close()
	if remainingResponse.StatusCode != http.StatusOK {
		t.Fatalf("list environments after delete status = %d, want %d", remainingResponse.StatusCode, http.StatusOK)
	}
	var remaining []environmentResponse
	if err := json.NewDecoder(remainingResponse.Body).Decode(&remaining); err != nil {
		t.Fatalf("decode environments after delete: %v", err)
	}
	if len(remaining) != 1 || remaining[0].Name != "production" {
		t.Fatalf("environments after delete = %+v, want production only", remaining)
	}

	deleteAgainResponse := server.request(t, http.MethodDelete, environmentPath(project.ID, staging.ID), nil)
	defer deleteAgainResponse.Body.Close()
	if deleteAgainResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("deleting missing environment status = %d, want %d", deleteAgainResponse.StatusCode, http.StatusNotFound)
	}

	deleteLastResponse := server.request(t, http.MethodDelete, environmentPath(project.ID, remaining[0].ID), nil)
	defer deleteLastResponse.Body.Close()
	if deleteLastResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("delete last environment status = %d, want %d", deleteLastResponse.StatusCode, http.StatusNoContent)
	}
}

func TestEnvironmentNamesAreRequiredAndProjectsMustExist(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	createProject(t, server, "Storefront", "Customer-facing application")

	missingNameResponse := postJSON(t, server, "/api/projects/1/environments", map[string]string{"name": "  "})
	defer missingNameResponse.Body.Close()
	if missingNameResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing environment name status = %d, want %d", missingNameResponse.StatusCode, http.StatusBadRequest)
	}

	missingProjectResponse := postJSON(t, server, "/api/projects/999/environments", map[string]string{"name": "staging"})
	defer missingProjectResponse.Body.Close()
	if missingProjectResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("missing project environment status = %d, want %d", missingProjectResponse.StatusCode, http.StatusNotFound)
	}

	listResponse := server.request(t, http.MethodGet, "/api/projects/999/environments", nil)
	defer listResponse.Body.Close()
	if listResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("missing project environment list status = %d, want %d", listResponse.StatusCode, http.StatusNotFound)
	}
}

func TestAuthenticatedProjectsListStartsEmpty(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)

	response := server.request(t, http.MethodGet, "/api/projects", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("projects list status = %d, want %d", response.StatusCode, http.StatusOK)
	}

	var projects []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(response.Body).Decode(&projects); err != nil {
		t.Fatalf("decode projects list: %v", err)
	}
	if len(projects) != 0 {
		t.Fatalf("projects list = %+v, want empty list", projects)
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

type projectResponse struct {
	ID               int64  `json:"id"`
	Name             string `json:"name"`
	Description      string `json:"description"`
	EnvironmentCount int    `json:"environmentCount"`
	CreatedAt        string `json:"createdAt"`
	LastUpdated      string `json:"lastUpdated"`
}

type environmentResponse struct {
	ID        int64  `json:"id"`
	ProjectID int64  `json:"projectId"`
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
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

func setupAndLogin(t *testing.T, server *testServer) {
	t.Helper()
	setupResponse := postJSON(t, server, "/api/auth/setup", map[string]string{
		"email":    "admin@example.com",
		"password": "correct horse battery staple",
	})
	setupResponse.Body.Close()
	if setupResponse.StatusCode != http.StatusCreated {
		t.Fatalf("setup status = %d, want %d", setupResponse.StatusCode, http.StatusCreated)
	}

	loginResponse := postJSON(t, server, "/api/auth/login", map[string]string{
		"email":    "admin@example.com",
		"password": "correct horse battery staple",
	})
	loginResponse.Body.Close()
	if loginResponse.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want %d", loginResponse.StatusCode, http.StatusOK)
	}
}

func createProject(t *testing.T, server *testServer, name, description string) projectResponse {
	t.Helper()
	response := postJSON(t, server, "/api/projects", map[string]string{
		"name":        name,
		"description": description,
	})
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create project status = %d, want %d", response.StatusCode, http.StatusCreated)
	}

	var project projectResponse
	if err := json.NewDecoder(response.Body).Decode(&project); err != nil {
		t.Fatalf("decode created project: %v", err)
	}
	return project
}

func projectEnvironmentsPath(projectID int64) string {
	return "/api/projects/" + strconv.FormatInt(projectID, 10) + "/environments"
}

func environmentPath(projectID, environmentID int64) string {
	return projectEnvironmentsPath(projectID) + "/" + strconv.FormatInt(environmentID, 10)
}
