package httpapi_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"relist-panel/internal/composes"
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

func TestProjectCreateRejectsSlugCollisions(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	createProject(t, server, "My App", "First project")

	slugCollisionResponse := postJSON(t, server, "/api/projects", map[string]string{
		"name":        "my-app",
		"description": "Slug-colliding project",
	})
	defer slugCollisionResponse.Body.Close()
	if slugCollisionResponse.StatusCode != http.StatusConflict {
		t.Fatalf("slug-colliding project status = %d, want %d", slugCollisionResponse.StatusCode, http.StatusConflict)
	}
	var failure map[string]string
	if err := json.NewDecoder(slugCollisionResponse.Body).Decode(&failure); err != nil {
		t.Fatalf("decode slug collision failure: %v", err)
	}
	if failure["error"] != "a project with a conflicting name already exists" {
		t.Fatalf("slug collision failure = %+v, want the slug conflict error", failure)
	}

	createProject(t, server, "???", "Punctuation-only project")
	fallbackCollisionResponse := postJSON(t, server, "/api/projects", map[string]string{
		"name":        "!!!???",
		"description": "Collides through the slug fallback",
	})
	defer fallbackCollisionResponse.Body.Close()
	if fallbackCollisionResponse.StatusCode != http.StatusConflict {
		t.Fatalf("punctuation-only slug collision status = %d, want %d", fallbackCollisionResponse.StatusCode, http.StatusConflict)
	}
}

func TestProjectRenameRejectsSlugCollisions(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	first := createProject(t, server, "My App", "First project")
	second := createProject(t, server, "Operations", "Internal tooling")
	secondPath := "/api/projects/" + strconv.FormatInt(second.ID, 10)

	collisionResponse := server.request(t, http.MethodPut, secondPath, map[string]string{
		"name":        "my-app",
		"description": "Renames into a slug collision",
	})
	defer collisionResponse.Body.Close()
	if collisionResponse.StatusCode != http.StatusConflict {
		t.Fatalf("rename into a slug collision status = %d, want %d", collisionResponse.StatusCode, http.StatusConflict)
	}

	freeResponse := server.request(t, http.MethodPut, secondPath, map[string]string{
		"name":        "Operations Hub",
		"description": "Renames to a free name",
	})
	defer freeResponse.Body.Close()
	if freeResponse.StatusCode != http.StatusOK {
		t.Fatalf("rename to a free name status = %d, want %d", freeResponse.StatusCode, http.StatusOK)
	}

	selfResponse := server.request(t, http.MethodPut, "/api/projects/"+strconv.FormatInt(first.ID, 10), map[string]string{
		"name":        "my-app",
		"description": "Renames to another spelling of its own slug",
	})
	defer selfResponse.Body.Close()
	if selfResponse.StatusCode != http.StatusOK {
		t.Fatalf("rename to own slug variant status = %d, want %d", selfResponse.StatusCode, http.StatusOK)
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

func TestEnvironmentCreateRejectsSlugCollisionsWithinProject(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	project := createProject(t, server, "Storefront", "Customer-facing application")
	otherProject := createProject(t, server, "Operations", "Internal tooling")

	defaultCollisionResponse := postJSON(t, server, projectEnvironmentsPath(project.ID), map[string]string{"name": "Production!!!"})
	defer defaultCollisionResponse.Body.Close()
	if defaultCollisionResponse.StatusCode != http.StatusConflict {
		t.Fatalf("slug collision with the default environment status = %d, want %d", defaultCollisionResponse.StatusCode, http.StatusConflict)
	}
	var failure map[string]string
	if err := json.NewDecoder(defaultCollisionResponse.Body).Decode(&failure); err != nil {
		t.Fatalf("decode slug collision failure: %v", err)
	}
	if failure["error"] != "an environment with a conflicting name already exists" {
		t.Fatalf("slug collision failure = %+v, want the slug conflict error", failure)
	}

	createResponse := postJSON(t, server, projectEnvironmentsPath(project.ID), map[string]string{"name": "Staging"})
	defer createResponse.Body.Close()
	if createResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create environment status = %d, want %d", createResponse.StatusCode, http.StatusCreated)
	}

	slugCollisionResponse := postJSON(t, server, projectEnvironmentsPath(project.ID), map[string]string{"name": "staging!!!"})
	defer slugCollisionResponse.Body.Close()
	if slugCollisionResponse.StatusCode != http.StatusConflict {
		t.Fatalf("slug-colliding environment status = %d, want %d", slugCollisionResponse.StatusCode, http.StatusConflict)
	}

	otherProjectResponse := postJSON(t, server, projectEnvironmentsPath(otherProject.ID), map[string]string{"name": "staging!!!"})
	defer otherProjectResponse.Body.Close()
	if otherProjectResponse.StatusCode != http.StatusCreated {
		t.Fatalf("same environment slug in another project status = %d, want %d", otherProjectResponse.StatusCode, http.StatusCreated)
	}
}

func TestAuthenticatedAdminCanManageEnvironmentVariables(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	project := createProject(t, server, "Storefront", "Customer-facing application")

	environmentsResponse := server.request(t, http.MethodGet, projectEnvironmentsPath(project.ID), nil)
	defer environmentsResponse.Body.Close()
	if environmentsResponse.StatusCode != http.StatusOK {
		t.Fatalf("list environments status = %d, want %d", environmentsResponse.StatusCode, http.StatusOK)
	}
	var environments []environmentResponse
	if err := json.NewDecoder(environmentsResponse.Body).Decode(&environments); err != nil {
		t.Fatalf("decode environments: %v", err)
	}
	if len(environments) != 1 {
		t.Fatalf("environments = %+v, want one default environment", environments)
	}
	production := environments[0]

	stagingResponse := postJSON(t, server, projectEnvironmentsPath(project.ID), map[string]string{"name": "staging"})
	defer stagingResponse.Body.Close()
	if stagingResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create staging environment status = %d, want %d", stagingResponse.StatusCode, http.StatusCreated)
	}
	var staging environmentResponse
	if err := json.NewDecoder(stagingResponse.Body).Decode(&staging); err != nil {
		t.Fatalf("decode staging environment: %v", err)
	}

	createResponse := postJSON(t, server, environmentVariablesPath(project.ID, production.ID), map[string]string{
		"key":   "DATABASE_URL",
		"value": "postgres://production",
	})
	defer createResponse.Body.Close()
	if createResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create environment variable status = %d, want %d", createResponse.StatusCode, http.StatusCreated)
	}
	var variable environmentVariableResponse
	if err := json.NewDecoder(createResponse.Body).Decode(&variable); err != nil {
		t.Fatalf("decode created environment variable: %v", err)
	}
	if variable.ID <= 0 || variable.EnvironmentID != production.ID || variable.Key != "DATABASE_URL" || variable.Value != "postgres://production" {
		t.Fatalf("created environment variable = %+v", variable)
	}

	sameKeyInOtherEnvironment := postJSON(t, server, environmentVariablesPath(project.ID, staging.ID), map[string]string{
		"key":   "DATABASE_URL",
		"value": "postgres://staging",
	})
	defer sameKeyInOtherEnvironment.Body.Close()
	if sameKeyInOtherEnvironment.StatusCode != http.StatusCreated {
		t.Fatalf("same key in another environment status = %d, want %d", sameKeyInOtherEnvironment.StatusCode, http.StatusCreated)
	}

	listResponse := server.request(t, http.MethodGet, environmentVariablesPath(project.ID, production.ID), nil)
	defer listResponse.Body.Close()
	if listResponse.StatusCode != http.StatusOK {
		t.Fatalf("list environment variables status = %d, want %d", listResponse.StatusCode, http.StatusOK)
	}
	var variables []environmentVariableResponse
	if err := json.NewDecoder(listResponse.Body).Decode(&variables); err != nil {
		t.Fatalf("decode environment variables: %v", err)
	}
	if len(variables) != 1 || variables[0] != variable {
		t.Fatalf("environment variables = %+v, want %+v", variables, []environmentVariableResponse{variable})
	}

	secondCreateResponse := postJSON(t, server, environmentVariablesPath(project.ID, production.ID), map[string]string{
		"key":   "PORT",
		"value": "8080",
	})
	defer secondCreateResponse.Body.Close()
	if secondCreateResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create second environment variable status = %d, want %d", secondCreateResponse.StatusCode, http.StatusCreated)
	}
	var secondVariable environmentVariableResponse
	if err := json.NewDecoder(secondCreateResponse.Body).Decode(&secondVariable); err != nil {
		t.Fatalf("decode second environment variable: %v", err)
	}

	duplicateResponse := postJSON(t, server, environmentVariablesPath(project.ID, production.ID), map[string]string{
		"key":   "DATABASE_URL",
		"value": "another value",
	})
	defer duplicateResponse.Body.Close()
	if duplicateResponse.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate environment variable status = %d, want %d", duplicateResponse.StatusCode, http.StatusConflict)
	}

	duplicateUpdateResponse := server.request(t, http.MethodPut, variablePath(project.ID, production.ID, variable.ID), map[string]string{
		"key":   "PORT",
		"value": "another value",
	})
	defer duplicateUpdateResponse.Body.Close()
	if duplicateUpdateResponse.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate environment variable update status = %d, want %d", duplicateUpdateResponse.StatusCode, http.StatusConflict)
	}

	updateResponse := server.request(t, http.MethodPut, variablePath(project.ID, production.ID, variable.ID), map[string]string{
		"key":   "DATABASE_URL_ROTATED",
		"value": "postgres://rotated",
	})
	defer updateResponse.Body.Close()
	if updateResponse.StatusCode != http.StatusOK {
		t.Fatalf("update environment variable status = %d, want %d", updateResponse.StatusCode, http.StatusOK)
	}
	var updated environmentVariableResponse
	if err := json.NewDecoder(updateResponse.Body).Decode(&updated); err != nil {
		t.Fatalf("decode updated environment variable: %v", err)
	}
	if updated.ID != variable.ID || updated.EnvironmentID != production.ID || updated.Key != "DATABASE_URL_ROTATED" || updated.Value != "postgres://rotated" {
		t.Fatalf("updated environment variable = %+v", updated)
	}

	deleteResponse := server.request(t, http.MethodDelete, variablePath(project.ID, production.ID, variable.ID), nil)
	defer deleteResponse.Body.Close()
	if deleteResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("delete environment variable status = %d, want %d", deleteResponse.StatusCode, http.StatusNoContent)
	}

	deleteSecondResponse := server.request(t, http.MethodDelete, variablePath(project.ID, production.ID, secondVariable.ID), nil)
	defer deleteSecondResponse.Body.Close()
	if deleteSecondResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("delete second environment variable status = %d, want %d", deleteSecondResponse.StatusCode, http.StatusNoContent)
	}

	remainingResponse := server.request(t, http.MethodGet, environmentVariablesPath(project.ID, production.ID), nil)
	defer remainingResponse.Body.Close()
	if remainingResponse.StatusCode != http.StatusOK {
		t.Fatalf("list environment variables after delete status = %d, want %d", remainingResponse.StatusCode, http.StatusOK)
	}
	var remaining []environmentVariableResponse
	if err := json.NewDecoder(remainingResponse.Body).Decode(&remaining); err != nil {
		t.Fatalf("decode environment variables after delete: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("environment variables after delete = %+v, want empty list", remaining)
	}

	deleteAgainResponse := server.request(t, http.MethodDelete, variablePath(project.ID, production.ID, variable.ID), nil)
	defer deleteAgainResponse.Body.Close()
	if deleteAgainResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("deleting missing environment variable status = %d, want %d", deleteAgainResponse.StatusCode, http.StatusNotFound)
	}
}

func TestAuthenticatedAdminCanManageComposes(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	project := createProject(t, server, "Storefront", "Customer-facing application")

	environmentsResponse := server.request(t, http.MethodGet, projectEnvironmentsPath(project.ID), nil)
	defer environmentsResponse.Body.Close()
	var environments []environmentResponse
	if err := json.NewDecoder(environmentsResponse.Body).Decode(&environments); err != nil {
		t.Fatalf("decode environments: %v", err)
	}
	production := environments[0]

	stagingResponse := postJSON(t, server, projectEnvironmentsPath(project.ID), map[string]string{"name": "staging"})
	defer stagingResponse.Body.Close()
	var staging environmentResponse
	if err := json.NewDecoder(stagingResponse.Body).Decode(&staging); err != nil {
		t.Fatalf("decode staging environment: %v", err)
	}

	emptyListResponse := server.request(t, http.MethodGet, composesPath(project.ID, production.ID), nil)
	defer emptyListResponse.Body.Close()
	if emptyListResponse.StatusCode != http.StatusOK {
		t.Fatalf("list composes status = %d, want %d", emptyListResponse.StatusCode, http.StatusOK)
	}
	var emptyComposes []composeResponse
	if err := json.NewDecoder(emptyListResponse.Body).Decode(&emptyComposes); err != nil {
		t.Fatalf("decode empty composes: %v", err)
	}
	if len(emptyComposes) != 0 {
		t.Fatalf("composes before creation = %+v, want empty list", emptyComposes)
	}

	createResponse := postJSON(t, server, composesPath(project.ID, production.ID), map[string]string{
		"name":        "web",
		"yamlContent": "services:\n  web:\n    image: nginx\n",
	})
	defer createResponse.Body.Close()
	if createResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create compose status = %d, want %d", createResponse.StatusCode, http.StatusCreated)
	}
	var compose composeResponse
	if err := json.NewDecoder(createResponse.Body).Decode(&compose); err != nil {
		t.Fatalf("decode created compose: %v", err)
	}
	if compose.ID <= 0 || compose.EnvironmentID != production.ID || compose.Name != "web" ||
		compose.YAMLContent != "services:\n  web:\n    image: nginx\n" || compose.CreatedAt == "" || compose.UpdatedAt == "" {
		t.Fatalf("created compose = %+v", compose)
	}

	sameNameInOtherEnvironment := postJSON(t, server, composesPath(project.ID, staging.ID), map[string]string{
		"name":        "web",
		"yamlContent": "services:\n  web:\n    image: nginx:staging\n",
	})
	defer sameNameInOtherEnvironment.Body.Close()
	if sameNameInOtherEnvironment.StatusCode != http.StatusCreated {
		t.Fatalf("same compose name in another environment status = %d, want %d", sameNameInOtherEnvironment.StatusCode, http.StatusCreated)
	}

	listResponse := server.request(t, http.MethodGet, composesPath(project.ID, production.ID), nil)
	defer listResponse.Body.Close()
	if listResponse.StatusCode != http.StatusOK {
		t.Fatalf("list composes status = %d, want %d", listResponse.StatusCode, http.StatusOK)
	}
	var composesList []composeResponse
	if err := json.NewDecoder(listResponse.Body).Decode(&composesList); err != nil {
		t.Fatalf("decode composes list: %v", err)
	}
	if len(composesList) != 1 || composesList[0] != compose {
		t.Fatalf("composes list = %+v, want %+v", composesList, []composeResponse{compose})
	}

	getResponse := server.request(t, http.MethodGet, composePath(project.ID, production.ID, compose.ID), nil)
	defer getResponse.Body.Close()
	if getResponse.StatusCode != http.StatusOK {
		t.Fatalf("get compose status = %d, want %d", getResponse.StatusCode, http.StatusOK)
	}
	var fetched composeResponse
	if err := json.NewDecoder(getResponse.Body).Decode(&fetched); err != nil {
		t.Fatalf("decode fetched compose: %v", err)
	}
	if fetched != compose {
		t.Fatalf("fetched compose = %+v, want %+v", fetched, compose)
	}

	duplicateResponse := postJSON(t, server, composesPath(project.ID, production.ID), map[string]string{
		"name":        " WEB ",
		"yamlContent": "services: {}\n",
	})
	defer duplicateResponse.Body.Close()
	if duplicateResponse.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate compose name status = %d, want %d", duplicateResponse.StatusCode, http.StatusConflict)
	}

	slugCollisionResponse := postJSON(t, server, composesPath(project.ID, production.ID), map[string]string{
		"name":        "web!!!",
		"yamlContent": "services: {}\n",
	})
	defer slugCollisionResponse.Body.Close()
	if slugCollisionResponse.StatusCode != http.StatusConflict {
		t.Fatalf("slug-colliding compose name status = %d, want %d", slugCollisionResponse.StatusCode, http.StatusConflict)
	}

	missingNameResponse := postJSON(t, server, composesPath(project.ID, production.ID), map[string]string{
		"name":        "  ",
		"yamlContent": "services: {}\n",
	})
	defer missingNameResponse.Body.Close()
	if missingNameResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing compose name status = %d, want %d", missingNameResponse.StatusCode, http.StatusBadRequest)
	}

	updateResponse := server.request(t, http.MethodPut, composePath(project.ID, production.ID, compose.ID), map[string]string{
		"yamlContent": "services:\n  web:\n    image: nginx:1.27\n",
	})
	defer updateResponse.Body.Close()
	if updateResponse.StatusCode != http.StatusOK {
		t.Fatalf("update compose status = %d, want %d", updateResponse.StatusCode, http.StatusOK)
	}
	var updated composeResponse
	if err := json.NewDecoder(updateResponse.Body).Decode(&updated); err != nil {
		t.Fatalf("decode updated compose: %v", err)
	}
	if updated.ID != compose.ID || updated.Name != compose.Name || updated.YAMLContent != "services:\n  web:\n    image: nginx:1.27\n" {
		t.Fatalf("updated compose = %+v", updated)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, updated.CreatedAt)
	if err != nil {
		t.Fatalf("parse compose creation time: %v", err)
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, updated.UpdatedAt)
	if err != nil {
		t.Fatalf("parse compose update time: %v", err)
	}
	if !updatedAt.After(createdAt) {
		t.Fatalf("compose updated at = %s, want after created at %s", updatedAt, createdAt)
	}

	missingUpdateResponse := server.request(t, http.MethodPut, composePath(project.ID, production.ID, 999), map[string]string{
		"yamlContent": "services: {}\n",
	})
	defer missingUpdateResponse.Body.Close()
	if missingUpdateResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("update missing compose status = %d, want %d", missingUpdateResponse.StatusCode, http.StatusNotFound)
	}

	wrongEnvironmentResponse := server.request(t, http.MethodGet, composePath(project.ID, staging.ID, compose.ID), nil)
	defer wrongEnvironmentResponse.Body.Close()
	if wrongEnvironmentResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("compose fetched via sibling environment status = %d, want %d", wrongEnvironmentResponse.StatusCode, http.StatusNotFound)
	}
}

func TestComposesRequireExistingProjectAndEnvironment(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)

	missingProjectResponse := server.request(t, http.MethodGet, composesPath(999, 1), nil)
	defer missingProjectResponse.Body.Close()
	if missingProjectResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("missing project composes status = %d, want %d", missingProjectResponse.StatusCode, http.StatusNotFound)
	}

	project := createProject(t, server, "Storefront", "Customer-facing application")
	environmentsResponse := server.request(t, http.MethodGet, projectEnvironmentsPath(project.ID), nil)
	defer environmentsResponse.Body.Close()
	var environments []environmentResponse
	if err := json.NewDecoder(environmentsResponse.Body).Decode(&environments); err != nil {
		t.Fatalf("decode environments: %v", err)
	}

	missingEnvironmentResponse := server.request(t, http.MethodGet, composesPath(project.ID, 999), nil)
	defer missingEnvironmentResponse.Body.Close()
	if missingEnvironmentResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("missing environment composes status = %d, want %d", missingEnvironmentResponse.StatusCode, http.StatusNotFound)
	}

	otherProject := createProject(t, server, "Operations", "Internal tooling")
	wrongProjectResponse := server.request(t, http.MethodGet, composesPath(otherProject.ID, environments[0].ID), nil)
	defer wrongProjectResponse.Body.Close()
	if wrongProjectResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("environment under another project status = %d, want %d", wrongProjectResponse.StatusCode, http.StatusNotFound)
	}

	missingComposeResponse := server.request(t, http.MethodGet, composePath(project.ID, environments[0].ID, 999), nil)
	defer missingComposeResponse.Body.Close()
	if missingComposeResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("missing compose status = %d, want %d", missingComposeResponse.StatusCode, http.StatusNotFound)
	}
}

func TestAuthenticatedAdminCanDeployCompose(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	project := createProject(t, server, "Deploy Co", "")

	environmentsResponse := server.request(t, http.MethodGet, projectEnvironmentsPath(project.ID), nil)
	defer environmentsResponse.Body.Close()
	var environments []environmentResponse
	if err := json.NewDecoder(environmentsResponse.Body).Decode(&environments); err != nil {
		t.Fatalf("decode environments: %v", err)
	}
	production := environments[0]

	variableResponse := postJSON(t, server, environmentVariablesPath(project.ID, production.ID), map[string]string{
		"key": "API_KEY", "value": "secret",
	})
	defer variableResponse.Body.Close()
	if variableResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create environment variable status = %d, want %d", variableResponse.StatusCode, http.StatusCreated)
	}

	yamlContent := "services:\n  web:\n    image: nginx\n    networks:\n      - custom\nnetworks:\n  custom:\n    driver: bridge\n"
	createResponse := postJSON(t, server, composesPath(project.ID, production.ID), map[string]string{
		"name":        "web",
		"yamlContent": yamlContent,
	})
	defer createResponse.Body.Close()
	var compose composeResponse
	if err := json.NewDecoder(createResponse.Body).Decode(&compose); err != nil {
		t.Fatalf("decode created compose: %v", err)
	}
	if compose.Status != (composeStatusResponse{}) {
		t.Fatalf("status before deploy = %+v, want zero value", compose.Status)
	}

	deployResponse := server.request(t, http.MethodPost, composeDeployPath(project.ID, production.ID, compose.ID), nil)
	defer deployResponse.Body.Close()
	if deployResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(deployResponse.Body)
		t.Fatalf("deploy status = %d, want %d, body = %s", deployResponse.StatusCode, http.StatusOK, body)
	}
	var deployed composeResponse
	if err := json.NewDecoder(deployResponse.Body).Decode(&deployed); err != nil {
		t.Fatalf("decode deployed compose: %v", err)
	}
	if deployed.Status.Total == 0 {
		t.Fatalf("status after deploy = %+v, want a non-zero total", deployed.Status)
	}

	if len(server.runner.calls) != 2 {
		t.Fatalf("runner calls = %+v, want exactly an up followed by a status call", server.runner.calls)
	}
	upCall := server.runner.calls[0]
	if upCall.method != "up" {
		t.Fatalf("first runner call = %q, want %q", upCall.method, "up")
	}
	wantProjectName := "deploy-co-production-web"
	if upCall.request.ProjectName != wantProjectName {
		t.Fatalf("deploy project name = %q, want %q", upCall.request.ProjectName, wantProjectName)
	}

	resolvedYAML, err := os.ReadFile(upCall.request.ComposeFilePath)
	if err != nil {
		t.Fatalf("read resolved compose file: %v", err)
	}
	if !strings.Contains(string(resolvedYAML), "relist-network") {
		t.Fatalf("resolved compose yaml = %s, want it to contain the shared network", resolvedYAML)
	}
	if !strings.Contains(string(resolvedYAML), "custom") {
		t.Fatalf("resolved compose yaml = %s, want the user's own network preserved", resolvedYAML)
	}

	envFile, err := os.ReadFile(upCall.request.EnvFilePath)
	if err != nil {
		t.Fatalf("read generated env file: %v", err)
	}
	if !strings.Contains(string(envFile), "API_KEY=secret") {
		t.Fatalf("generated env file = %q, want it to contain the environment variable", envFile)
	}

	// A subsequent GET reflects the live status without redeploying.
	getResponse := server.request(t, http.MethodGet, composePath(project.ID, production.ID, compose.ID), nil)
	defer getResponse.Body.Close()
	var fetched composeResponse
	if err := json.NewDecoder(getResponse.Body).Decode(&fetched); err != nil {
		t.Fatalf("decode fetched compose: %v", err)
	}
	if fetched.Status != deployed.Status {
		t.Fatalf("fetched status = %+v, want %+v", fetched.Status, deployed.Status)
	}
	if len(server.runner.calls) != 3 || server.runner.calls[2].method != "status" {
		t.Fatalf("runner calls after GET = %+v, want a third status call", server.runner.calls)
	}
}

func TestComposeDeployValidatesInputAndPropagatesRunnerErrors(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	project := createProject(t, server, "Deploy Co", "")

	environmentsResponse := server.request(t, http.MethodGet, projectEnvironmentsPath(project.ID), nil)
	defer environmentsResponse.Body.Close()
	var environments []environmentResponse
	if err := json.NewDecoder(environmentsResponse.Body).Decode(&environments); err != nil {
		t.Fatalf("decode environments: %v", err)
	}
	production := environments[0]

	emptyResponse := postJSON(t, server, composesPath(project.ID, production.ID), map[string]string{"name": "empty"})
	defer emptyResponse.Body.Close()
	var empty composeResponse
	if err := json.NewDecoder(emptyResponse.Body).Decode(&empty); err != nil {
		t.Fatalf("decode empty compose: %v", err)
	}

	deployEmptyResponse := server.request(t, http.MethodPost, composeDeployPath(project.ID, production.ID, empty.ID), nil)
	defer deployEmptyResponse.Body.Close()
	if deployEmptyResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("deploy with no yaml status = %d, want %d", deployEmptyResponse.StatusCode, http.StatusBadRequest)
	}

	invalidResponse := postJSON(t, server, composesPath(project.ID, production.ID), map[string]string{
		"name":        "broken",
		"yamlContent": "services: [this is not valid yaml",
	})
	defer invalidResponse.Body.Close()
	var broken composeResponse
	if err := json.NewDecoder(invalidResponse.Body).Decode(&broken); err != nil {
		t.Fatalf("decode broken compose: %v", err)
	}

	deployInvalidResponse := server.request(t, http.MethodPost, composeDeployPath(project.ID, production.ID, broken.ID), nil)
	defer deployInvalidResponse.Body.Close()
	if deployInvalidResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("deploy with invalid yaml status = %d, want %d", deployInvalidResponse.StatusCode, http.StatusBadRequest)
	}

	if len(server.runner.calls) != 0 {
		t.Fatalf("runner should not have been called for invalid deploys, got %+v", server.runner.calls)
	}

	missingComposeResponse := server.request(t, http.MethodPost, composeDeployPath(project.ID, production.ID, 999), nil)
	defer missingComposeResponse.Body.Close()
	if missingComposeResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("deploy missing compose status = %d, want %d", missingComposeResponse.StatusCode, http.StatusNotFound)
	}

	workingResponse := postJSON(t, server, composesPath(project.ID, production.ID), map[string]string{
		"name":        "web",
		"yamlContent": "services:\n  web:\n    image: nginx\n",
	})
	defer workingResponse.Body.Close()
	var working composeResponse
	if err := json.NewDecoder(workingResponse.Body).Decode(&working); err != nil {
		t.Fatalf("decode working compose: %v", err)
	}

	server.runner.upErr = errors.New("docker daemon unreachable")
	deployFailureResponse := server.request(t, http.MethodPost, composeDeployPath(project.ID, production.ID, working.ID), nil)
	defer deployFailureResponse.Body.Close()
	if deployFailureResponse.StatusCode != http.StatusBadGateway {
		t.Fatalf("deploy with failing runner status = %d, want %d", deployFailureResponse.StatusCode, http.StatusBadGateway)
	}
}

// The deploy timestamp lands on success only: a Compose starts (and a failed
// deploy leaves it) never-deployed — null on the wire, NULL in the row — and a
// successful deploy stamps it, after which nothing but another successful
// deploy moves it. The stamp is a row column, so it survives a panel restart.
func TestComposeDeployRecordsDeployedAtOnSuccessOnly(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	project := createProject(t, server, "Stamp Co", "")
	production := productionEnvironment(t, server, project)

	createResponse := postJSON(t, server, composesPath(project.ID, production.ID), map[string]string{
		"name":        "web",
		"yamlContent": "services:\n  web:\n    image: nginx\n",
	})
	defer createResponse.Body.Close()
	var compose composeResponse
	if err := json.NewDecoder(createResponse.Body).Decode(&compose); err != nil {
		t.Fatalf("decode created compose: %v", err)
	}

	getAndCheck := func(label string, wantNil bool) *string {
		t.Helper()
		response := server.request(t, http.MethodGet, composePath(project.ID, production.ID, compose.ID), nil)
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s: GET status = %d, want %d", label, response.StatusCode, http.StatusOK)
		}
		var fetched composeResponse
		if err := json.NewDecoder(response.Body).Decode(&fetched); err != nil {
			t.Fatalf("%s: decode fetched compose: %v", label, err)
		}
		if wantNil && fetched.LastDeployedAt != nil {
			t.Fatalf("%s: lastDeployedAt = %q, want null", label, *fetched.LastDeployedAt)
		}
		if !wantNil && fetched.LastDeployedAt == nil {
			t.Fatalf("%s: lastDeployedAt = null, want a timestamp", label)
		}
		return fetched.LastDeployedAt
	}
	rowDeployedAt := func() sql.NullInt64 {
		t.Helper()
		var deployedAt sql.NullInt64
		if err := server.db.QueryRow("SELECT deployed_at FROM composes WHERE id = ?", compose.ID).Scan(&deployedAt); err != nil {
			t.Fatalf("read deployed_at row: %v", err)
		}
		return deployedAt
	}

	// Never deployed: null on the wire, NULL in the row.
	getAndCheck("newly created", true)
	if rowDeployedAt().Valid {
		t.Fatal("newly created compose deployed_at = a value, want NULL")
	}

	// A failed deploy never reaches the write.
	server.runner.upErr = errors.New("docker daemon unreachable")
	failureResponse := server.request(t, http.MethodPost, composeDeployPath(project.ID, production.ID, compose.ID), nil)
	defer failureResponse.Body.Close()
	if failureResponse.StatusCode != http.StatusBadGateway {
		t.Fatalf("failed deploy status = %d, want %d", failureResponse.StatusCode, http.StatusBadGateway)
	}
	getAndCheck("after a failed deploy", true)
	if rowDeployedAt().Valid {
		t.Fatal("deployed_at after a failed deploy = a value, want NULL")
	}

	// A successful deploy stamps it — response, GET and row agree.
	server.runner.upErr = nil
	before := time.Now().UTC()
	deployResponse := server.request(t, http.MethodPost, composeDeployPath(project.ID, production.ID, compose.ID), nil)
	defer deployResponse.Body.Close()
	if deployResponse.StatusCode != http.StatusOK {
		t.Fatalf("deploy status = %d, want %d", deployResponse.StatusCode, http.StatusOK)
	}
	var deployed composeResponse
	if err := json.NewDecoder(deployResponse.Body).Decode(&deployed); err != nil {
		t.Fatalf("decode deployed compose: %v", err)
	}
	if deployed.LastDeployedAt == nil {
		t.Fatal("deploy response lastDeployedAt = null, want a timestamp")
	}
	stamp, err := time.Parse(time.RFC3339, *deployed.LastDeployedAt)
	if err != nil {
		t.Fatalf("parse lastDeployedAt %q: %v", *deployed.LastDeployedAt, err)
	}
	if stamp.Before(before.Add(-time.Minute)) || time.Now().UTC().Before(stamp) {
		t.Fatalf("deploy stamp %v is outside the request window [%v, now]", stamp, before)
	}
	if got := getAndCheck("after a successful deploy", false); *got != *deployed.LastDeployedAt {
		t.Fatalf("GET lastDeployedAt = %q, want the deploy response's %q", *got, *deployed.LastDeployedAt)
	}
	row := rowDeployedAt()
	if !row.Valid || !time.Unix(row.Int64, 0).UTC().Equal(stamp) {
		t.Fatalf("row deployed_at = %+v, want the stamped %v", row, stamp)
	}

	// Stop, restart and save never touch it.
	stopResponse := server.request(t, http.MethodPost, composeStopPath(project.ID, production.ID, compose.ID), nil)
	defer stopResponse.Body.Close()
	if stopResponse.StatusCode != http.StatusOK {
		t.Fatalf("stop status = %d, want %d", stopResponse.StatusCode, http.StatusOK)
	}
	if got := getAndCheck("after a stop", false); *got != *deployed.LastDeployedAt {
		t.Fatalf("stop changed lastDeployedAt to %q", *got)
	}

	restartResponse := server.request(t, http.MethodPost, composeRestartPath(project.ID, production.ID, compose.ID), nil)
	defer restartResponse.Body.Close()
	if restartResponse.StatusCode != http.StatusOK {
		t.Fatalf("restart status = %d, want %d", restartResponse.StatusCode, http.StatusOK)
	}
	if got := getAndCheck("after a restart", false); *got != *deployed.LastDeployedAt {
		t.Fatalf("restart changed lastDeployedAt to %q", *got)
	}

	saveResponse := server.request(t, http.MethodPut, composePath(project.ID, production.ID, compose.ID), map[string]string{
		"yamlContent": "services:\n  web:\n    image: nginx:1.27\n",
	})
	defer saveResponse.Body.Close()
	if saveResponse.StatusCode != http.StatusOK {
		t.Fatalf("save status = %d, want %d", saveResponse.StatusCode, http.StatusOK)
	}
	if got := getAndCheck("after a save", false); *got != *deployed.LastDeployedAt {
		t.Fatalf("save changed lastDeployedAt to %q", *got)
	}

	// The stamp is row state, not server memory: a fresh handler over the same
	// database — a panel restart — still serves it.
	restartedRunner := newFakeComposeRunner()
	restarted := &testServer{
		handler: httpapi.NewHandler(server.db, restartedRunner, server.dataDir, newFakeHostHistory(), newFakeServiceFacts()),
		db:      server.db,
		dataDir: server.dataDir,
		runner:  restartedRunner,
		cookies: server.cookies,
	}
	restartedGet := restarted.request(t, http.MethodGet, composePath(project.ID, production.ID, compose.ID), nil)
	defer restartedGet.Body.Close()
	if restartedGet.StatusCode != http.StatusOK {
		t.Fatalf("GET after restart status = %d, want %d", restartedGet.StatusCode, http.StatusOK)
	}
	var refetched composeResponse
	if err := json.NewDecoder(restartedGet.Body).Decode(&refetched); err != nil {
		t.Fatalf("decode refetched compose: %v", err)
	}
	if refetched.LastDeployedAt == nil || *refetched.LastDeployedAt != *deployed.LastDeployedAt {
		t.Fatalf("lastDeployedAt after a panel restart = %v, want %q", refetched.LastDeployedAt, *deployed.LastDeployedAt)
	}
}

func TestAuthenticatedAdminCanStopCompose(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	project := createProject(t, server, "Stop Co", "")

	environmentsResponse := server.request(t, http.MethodGet, projectEnvironmentsPath(project.ID), nil)
	defer environmentsResponse.Body.Close()
	var environments []environmentResponse
	if err := json.NewDecoder(environmentsResponse.Body).Decode(&environments); err != nil {
		t.Fatalf("decode environments: %v", err)
	}
	production := environments[0]

	createResponse := postJSON(t, server, composesPath(project.ID, production.ID), map[string]string{
		"name":        "web",
		"yamlContent": "services:\n  web:\n    image: nginx\n",
	})
	defer createResponse.Body.Close()
	var compose composeResponse
	if err := json.NewDecoder(createResponse.Body).Decode(&compose); err != nil {
		t.Fatalf("decode created compose: %v", err)
	}

	deployResponse := server.request(t, http.MethodPost, composeDeployPath(project.ID, production.ID, compose.ID), nil)
	defer deployResponse.Body.Close()
	if deployResponse.StatusCode != http.StatusOK {
		t.Fatalf("deploy status = %d, want %d", deployResponse.StatusCode, http.StatusOK)
	}
	var deployed composeResponse
	if err := json.NewDecoder(deployResponse.Body).Decode(&deployed); err != nil {
		t.Fatalf("decode deployed compose: %v", err)
	}
	if deployed.Status.Total == 0 {
		t.Fatalf("status after deploy = %+v, want a non-zero total", deployed.Status)
	}

	stopResponse := server.request(t, http.MethodPost, composeStopPath(project.ID, production.ID, compose.ID), nil)
	defer stopResponse.Body.Close()
	if stopResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(stopResponse.Body)
		t.Fatalf("stop status = %d, want %d, body = %s", stopResponse.StatusCode, http.StatusOK, body)
	}
	var stopped composeResponse
	if err := json.NewDecoder(stopResponse.Body).Decode(&stopped); err != nil {
		t.Fatalf("decode stopped compose: %v", err)
	}
	if stopped.Status != (composeStatusResponse{}) {
		t.Fatalf("status after stop = %+v, want zero value (stopped)", stopped.Status)
	}

	lastCall := server.runner.calls[len(server.runner.calls)-1]
	if lastCall.method != "status" || server.runner.calls[len(server.runner.calls)-2].method != "down" {
		t.Fatalf("runner calls = %+v, want a down followed by a status call", server.runner.calls)
	}
	if server.runner.calls[len(server.runner.calls)-2].request.ProjectName != "stop-co-production-web" {
		t.Fatalf("stop project name = %q, want %q", server.runner.calls[len(server.runner.calls)-2].request.ProjectName, "stop-co-production-web")
	}

	// A subsequent GET reflects the stopped status without redeploying.
	getResponse := server.request(t, http.MethodGet, composePath(project.ID, production.ID, compose.ID), nil)
	defer getResponse.Body.Close()
	var fetched composeResponse
	if err := json.NewDecoder(getResponse.Body).Decode(&fetched); err != nil {
		t.Fatalf("decode fetched compose: %v", err)
	}
	if fetched.Status != (composeStatusResponse{}) {
		t.Fatalf("fetched status = %+v, want zero value (stopped)", fetched.Status)
	}
}

func TestComposeStopValidatesInputAndPropagatesRunnerErrors(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	project := createProject(t, server, "Stop Co", "")

	environmentsResponse := server.request(t, http.MethodGet, projectEnvironmentsPath(project.ID), nil)
	defer environmentsResponse.Body.Close()
	var environments []environmentResponse
	if err := json.NewDecoder(environmentsResponse.Body).Decode(&environments); err != nil {
		t.Fatalf("decode environments: %v", err)
	}
	production := environments[0]

	undeployedResponse := postJSON(t, server, composesPath(project.ID, production.ID), map[string]string{
		"name":        "undeployed",
		"yamlContent": "services:\n  web:\n    image: nginx\n",
	})
	defer undeployedResponse.Body.Close()
	var undeployed composeResponse
	if err := json.NewDecoder(undeployedResponse.Body).Decode(&undeployed); err != nil {
		t.Fatalf("decode undeployed compose: %v", err)
	}

	stopUndeployedResponse := server.request(t, http.MethodPost, composeStopPath(project.ID, production.ID, undeployed.ID), nil)
	defer stopUndeployedResponse.Body.Close()
	if stopUndeployedResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("stop undeployed compose status = %d, want %d", stopUndeployedResponse.StatusCode, http.StatusBadRequest)
	}
	if len(server.runner.calls) != 0 {
		t.Fatalf("runner should not have been called for an undeployed compose, got %+v", server.runner.calls)
	}

	missingComposeResponse := server.request(t, http.MethodPost, composeStopPath(project.ID, production.ID, 999), nil)
	defer missingComposeResponse.Body.Close()
	if missingComposeResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("stop missing compose status = %d, want %d", missingComposeResponse.StatusCode, http.StatusNotFound)
	}

	workingResponse := postJSON(t, server, composesPath(project.ID, production.ID), map[string]string{
		"name":        "web",
		"yamlContent": "services:\n  web:\n    image: nginx\n",
	})
	defer workingResponse.Body.Close()
	var working composeResponse
	if err := json.NewDecoder(workingResponse.Body).Decode(&working); err != nil {
		t.Fatalf("decode working compose: %v", err)
	}
	deployResponse := server.request(t, http.MethodPost, composeDeployPath(project.ID, production.ID, working.ID), nil)
	deployResponse.Body.Close()
	if deployResponse.StatusCode != http.StatusOK {
		t.Fatalf("deploy status = %d, want %d", deployResponse.StatusCode, http.StatusOK)
	}

	server.runner.downErr = errors.New("docker daemon unreachable")
	stopFailureResponse := server.request(t, http.MethodPost, composeStopPath(project.ID, production.ID, working.ID), nil)
	defer stopFailureResponse.Body.Close()
	if stopFailureResponse.StatusCode != http.StatusBadGateway {
		t.Fatalf("stop with failing runner status = %d, want %d", stopFailureResponse.StatusCode, http.StatusBadGateway)
	}
}

func TestAuthenticatedAdminCanRestartCompose(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	project := createProject(t, server, "Restart Co", "")

	environmentsResponse := server.request(t, http.MethodGet, projectEnvironmentsPath(project.ID), nil)
	defer environmentsResponse.Body.Close()
	var environments []environmentResponse
	if err := json.NewDecoder(environmentsResponse.Body).Decode(&environments); err != nil {
		t.Fatalf("decode environments: %v", err)
	}
	production := environments[0]

	createResponse := postJSON(t, server, composesPath(project.ID, production.ID), map[string]string{
		"name":        "web",
		"yamlContent": "services:\n  web:\n    image: nginx\n",
	})
	defer createResponse.Body.Close()
	var compose composeResponse
	if err := json.NewDecoder(createResponse.Body).Decode(&compose); err != nil {
		t.Fatalf("decode created compose: %v", err)
	}

	deployResponse := server.request(t, http.MethodPost, composeDeployPath(project.ID, production.ID, compose.ID), nil)
	defer deployResponse.Body.Close()
	if deployResponse.StatusCode != http.StatusOK {
		t.Fatalf("deploy status = %d, want %d", deployResponse.StatusCode, http.StatusOK)
	}

	restartResponse := server.request(t, http.MethodPost, composeRestartPath(project.ID, production.ID, compose.ID), nil)
	defer restartResponse.Body.Close()
	if restartResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(restartResponse.Body)
		t.Fatalf("restart status = %d, want %d, body = %s", restartResponse.StatusCode, http.StatusOK, body)
	}
	var restarted composeResponse
	if err := json.NewDecoder(restartResponse.Body).Decode(&restarted); err != nil {
		t.Fatalf("decode restarted compose: %v", err)
	}
	if restarted.Status.Total == 0 {
		t.Fatalf("status after restart = %+v, want a non-zero total (containers still up)", restarted.Status)
	}

	lastCall := server.runner.calls[len(server.runner.calls)-1]
	if lastCall.method != "status" || server.runner.calls[len(server.runner.calls)-2].method != "restart" {
		t.Fatalf("runner calls = %+v, want a restart followed by a status call", server.runner.calls)
	}
	if server.runner.calls[len(server.runner.calls)-2].request.ProjectName != "restart-co-production-web" {
		t.Fatalf("restart project name = %q, want %q", server.runner.calls[len(server.runner.calls)-2].request.ProjectName, "restart-co-production-web")
	}
}

func TestComposeRestartValidatesInputAndPropagatesRunnerErrors(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	project := createProject(t, server, "Restart Co", "")

	environmentsResponse := server.request(t, http.MethodGet, projectEnvironmentsPath(project.ID), nil)
	defer environmentsResponse.Body.Close()
	var environments []environmentResponse
	if err := json.NewDecoder(environmentsResponse.Body).Decode(&environments); err != nil {
		t.Fatalf("decode environments: %v", err)
	}
	production := environments[0]

	undeployedResponse := postJSON(t, server, composesPath(project.ID, production.ID), map[string]string{
		"name":        "undeployed",
		"yamlContent": "services:\n  web:\n    image: nginx\n",
	})
	defer undeployedResponse.Body.Close()
	var undeployed composeResponse
	if err := json.NewDecoder(undeployedResponse.Body).Decode(&undeployed); err != nil {
		t.Fatalf("decode undeployed compose: %v", err)
	}

	restartUndeployedResponse := server.request(t, http.MethodPost, composeRestartPath(project.ID, production.ID, undeployed.ID), nil)
	defer restartUndeployedResponse.Body.Close()
	if restartUndeployedResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("restart undeployed compose status = %d, want %d", restartUndeployedResponse.StatusCode, http.StatusBadRequest)
	}
	if len(server.runner.calls) != 0 {
		t.Fatalf("runner should not have been called for an undeployed compose, got %+v", server.runner.calls)
	}

	missingComposeResponse := server.request(t, http.MethodPost, composeRestartPath(project.ID, production.ID, 999), nil)
	defer missingComposeResponse.Body.Close()
	if missingComposeResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("restart missing compose status = %d, want %d", missingComposeResponse.StatusCode, http.StatusNotFound)
	}

	workingResponse := postJSON(t, server, composesPath(project.ID, production.ID), map[string]string{
		"name":        "web",
		"yamlContent": "services:\n  web:\n    image: nginx\n",
	})
	defer workingResponse.Body.Close()
	var working composeResponse
	if err := json.NewDecoder(workingResponse.Body).Decode(&working); err != nil {
		t.Fatalf("decode working compose: %v", err)
	}
	deployResponse := server.request(t, http.MethodPost, composeDeployPath(project.ID, production.ID, working.ID), nil)
	deployResponse.Body.Close()
	if deployResponse.StatusCode != http.StatusOK {
		t.Fatalf("deploy status = %d, want %d", deployResponse.StatusCode, http.StatusOK)
	}

	server.runner.restartErr = errors.New("docker daemon unreachable")
	restartFailureResponse := server.request(t, http.MethodPost, composeRestartPath(project.ID, production.ID, working.ID), nil)
	defer restartFailureResponse.Body.Close()
	if restartFailureResponse.StatusCode != http.StatusBadGateway {
		t.Fatalf("restart with failing runner status = %d, want %d", restartFailureResponse.StatusCode, http.StatusBadGateway)
	}
}

func TestAuthenticatedAdminCanDeleteDeployedCompose(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	project := createProject(t, server, "Delete Co", "")

	environmentsResponse := server.request(t, http.MethodGet, projectEnvironmentsPath(project.ID), nil)
	defer environmentsResponse.Body.Close()
	var environments []environmentResponse
	if err := json.NewDecoder(environmentsResponse.Body).Decode(&environments); err != nil {
		t.Fatalf("decode environments: %v", err)
	}
	production := environments[0]

	createResponse := postJSON(t, server, composesPath(project.ID, production.ID), map[string]string{
		"name":        "web",
		"yamlContent": "services:\n  web:\n    image: nginx\n",
	})
	defer createResponse.Body.Close()
	var compose composeResponse
	if err := json.NewDecoder(createResponse.Body).Decode(&compose); err != nil {
		t.Fatalf("decode created compose: %v", err)
	}

	deployResponse := server.request(t, http.MethodPost, composeDeployPath(project.ID, production.ID, compose.ID), nil)
	deployResponse.Body.Close()
	if deployResponse.StatusCode != http.StatusOK {
		t.Fatalf("deploy status = %d, want %d", deployResponse.StatusCode, http.StatusOK)
	}
	if len(server.runner.calls) != 2 || server.runner.calls[0].method != "up" || server.runner.calls[1].method != "status" {
		t.Fatalf("runner calls after deploy = %+v, want up followed by status", server.runner.calls)
	}
	composeDir := filepath.Dir(server.runner.calls[0].request.ComposeFilePath)
	if _, err := os.Stat(filepath.Join(composeDir, "docker-compose.yml")); err != nil {
		t.Fatalf("deployed compose file: %v", err)
	}

	deleteResponse := server.request(t, http.MethodDelete, composePath(project.ID, production.ID, compose.ID), nil)
	defer deleteResponse.Body.Close()
	if deleteResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d", deleteResponse.StatusCode, http.StatusNoContent)
	}
	if body, err := io.ReadAll(deleteResponse.Body); err != nil {
		t.Fatalf("read delete response: %v", err)
	} else if len(body) != 0 {
		t.Fatalf("delete response body = %q, want empty", body)
	}

	if len(server.runner.calls) != 3 || server.runner.calls[2].method != "down-with-volumes" {
		t.Fatalf("runner calls after delete = %+v, want down-with-volumes only", server.runner.calls)
	}
	if server.runner.calls[2].request.ProjectName != "delete-co-production-web" {
		t.Fatalf("delete project name = %q, want %q", server.runner.calls[2].request.ProjectName, "delete-co-production-web")
	}
	if _, err := os.Stat(composeDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("compose directory still exists or returned unexpected error: %v", err)
	}

	fetchedResponse := server.request(t, http.MethodGet, composePath(project.ID, production.ID, compose.ID), nil)
	defer fetchedResponse.Body.Close()
	if fetchedResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("get deleted compose status = %d, want %d", fetchedResponse.StatusCode, http.StatusNotFound)
	}
}

func TestAuthenticatedAdminCanDeleteUndeployedCompose(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	project := createProject(t, server, "Delete Co", "")

	environmentsResponse := server.request(t, http.MethodGet, projectEnvironmentsPath(project.ID), nil)
	defer environmentsResponse.Body.Close()
	var environments []environmentResponse
	if err := json.NewDecoder(environmentsResponse.Body).Decode(&environments); err != nil {
		t.Fatalf("decode environments: %v", err)
	}
	production := environments[0]

	createResponse := postJSON(t, server, composesPath(project.ID, production.ID), map[string]string{
		"name":        "web",
		"yamlContent": "services:\n  web:\n    image: nginx\n",
	})
	defer createResponse.Body.Close()
	var compose composeResponse
	if err := json.NewDecoder(createResponse.Body).Decode(&compose); err != nil {
		t.Fatalf("decode created compose: %v", err)
	}

	composeDir := filepath.Join(server.dataDir, "delete-co", "production", "web")
	if err := os.MkdirAll(composeDir, 0o750); err != nil {
		t.Fatalf("create compose directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(composeDir, "stale-file"), []byte("stale"), 0o640); err != nil {
		t.Fatalf("write stale compose file: %v", err)
	}

	deleteResponse := server.request(t, http.MethodDelete, composePath(project.ID, production.ID, compose.ID), nil)
	defer deleteResponse.Body.Close()
	if deleteResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d", deleteResponse.StatusCode, http.StatusNoContent)
	}
	if len(server.runner.calls) != 0 {
		t.Fatalf("runner calls for undeployed delete = %+v, want none", server.runner.calls)
	}
	if _, err := os.Stat(composeDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("undeployed compose directory still exists or returned unexpected error: %v", err)
	}

	fetchedResponse := server.request(t, http.MethodGet, composePath(project.ID, production.ID, compose.ID), nil)
	defer fetchedResponse.Body.Close()
	if fetchedResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("get deleted undeployed compose status = %d, want %d", fetchedResponse.StatusCode, http.StatusNotFound)
	}
}

func TestComposeDeletePreservesComposeWhenRunnerFails(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	project := createProject(t, server, "Delete Co", "")

	environmentsResponse := server.request(t, http.MethodGet, projectEnvironmentsPath(project.ID), nil)
	defer environmentsResponse.Body.Close()
	var environments []environmentResponse
	if err := json.NewDecoder(environmentsResponse.Body).Decode(&environments); err != nil {
		t.Fatalf("decode environments: %v", err)
	}
	production := environments[0]

	createResponse := postJSON(t, server, composesPath(project.ID, production.ID), map[string]string{
		"name":        "web",
		"yamlContent": "services:\n  web:\n    image: nginx\n",
	})
	defer createResponse.Body.Close()
	var compose composeResponse
	if err := json.NewDecoder(createResponse.Body).Decode(&compose); err != nil {
		t.Fatalf("decode created compose: %v", err)
	}

	deployResponse := server.request(t, http.MethodPost, composeDeployPath(project.ID, production.ID, compose.ID), nil)
	deployResponse.Body.Close()
	if deployResponse.StatusCode != http.StatusOK {
		t.Fatalf("deploy status = %d, want %d", deployResponse.StatusCode, http.StatusOK)
	}
	composeDir := filepath.Dir(server.runner.calls[0].request.ComposeFilePath)

	server.runner.downWithVolumesErr = errors.New("docker daemon unreachable")
	deleteResponse := server.request(t, http.MethodDelete, composePath(project.ID, production.ID, compose.ID), nil)
	defer deleteResponse.Body.Close()
	if deleteResponse.StatusCode != http.StatusBadGateway {
		t.Fatalf("delete with failing runner status = %d, want %d", deleteResponse.StatusCode, http.StatusBadGateway)
	}
	var failure map[string]string
	if err := json.NewDecoder(deleteResponse.Body).Decode(&failure); err != nil {
		t.Fatalf("decode delete failure: %v", err)
	}
	if failure["error"] != "delete failed: docker daemon unreachable" {
		t.Fatalf("delete failure = %+v, want the runner error", failure)
	}
	if len(server.runner.calls) != 3 || server.runner.calls[2].method != "down-with-volumes" {
		t.Fatalf("runner calls after failed delete = %+v, want down-with-volumes", server.runner.calls)
	}
	if _, err := os.Stat(filepath.Join(composeDir, "docker-compose.yml")); err != nil {
		t.Fatalf("compose file after failed delete: %v", err)
	}

	fetchedResponse := server.request(t, http.MethodGet, composePath(project.ID, production.ID, compose.ID), nil)
	defer fetchedResponse.Body.Close()
	if fetchedResponse.StatusCode != http.StatusOK {
		t.Fatalf("get compose after failed delete status = %d, want %d", fetchedResponse.StatusCode, http.StatusOK)
	}
}

func TestAuthenticatedAdminCanViewComposeLogs(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	project := createProject(t, server, "Logs Co", "")

	environmentsResponse := server.request(t, http.MethodGet, projectEnvironmentsPath(project.ID), nil)
	defer environmentsResponse.Body.Close()
	var environments []environmentResponse
	if err := json.NewDecoder(environmentsResponse.Body).Decode(&environments); err != nil {
		t.Fatalf("decode environments: %v", err)
	}
	production := environments[0]

	createResponse := postJSON(t, server, composesPath(project.ID, production.ID), map[string]string{
		"name":        "web",
		"yamlContent": "services:\n  web:\n    image: nginx\n",
	})
	defer createResponse.Body.Close()
	var compose composeResponse
	if err := json.NewDecoder(createResponse.Body).Decode(&compose); err != nil {
		t.Fatalf("decode created compose: %v", err)
	}

	deployResponse := server.request(t, http.MethodPost, composeDeployPath(project.ID, production.ID, compose.ID), nil)
	deployResponse.Body.Close()
	if deployResponse.StatusCode != http.StatusOK {
		t.Fatalf("deploy status = %d, want %d", deployResponse.StatusCode, http.StatusOK)
	}

	const wantLogs = "web | ready\nweb | listening\n"
	server.runner.logsOutput = wantLogs
	logsResponse := server.request(t, http.MethodGet, composeLogsPath(project.ID, production.ID, compose.ID), nil)
	defer logsResponse.Body.Close()
	if logsResponse.StatusCode != http.StatusOK {
		t.Fatalf("logs status = %d, want %d", logsResponse.StatusCode, http.StatusOK)
	}
	var logs struct {
		Logs string `json:"logs"`
	}
	if err := json.NewDecoder(logsResponse.Body).Decode(&logs); err != nil {
		t.Fatalf("decode logs: %v", err)
	}
	if logs.Logs != wantLogs {
		t.Fatalf("logs = %q, want %q", logs.Logs, wantLogs)
	}

	if len(server.runner.calls) != 3 || server.runner.calls[2].method != "logs" {
		t.Fatalf("runner calls = %+v, want deploy calls followed by logs", server.runner.calls)
	}
	if server.runner.calls[2].request.ProjectName != "logs-co-production-web" {
		t.Fatalf("logs project name = %q, want %q", server.runner.calls[2].request.ProjectName, "logs-co-production-web")
	}
}

func TestComposeLogsValidatesDeploymentAndPropagatesRunnerErrors(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	project := createProject(t, server, "Logs Co", "")

	environmentsResponse := server.request(t, http.MethodGet, projectEnvironmentsPath(project.ID), nil)
	defer environmentsResponse.Body.Close()
	var environments []environmentResponse
	if err := json.NewDecoder(environmentsResponse.Body).Decode(&environments); err != nil {
		t.Fatalf("decode environments: %v", err)
	}
	production := environments[0]

	undeployedResponse := postJSON(t, server, composesPath(project.ID, production.ID), map[string]string{
		"name":        "undeployed",
		"yamlContent": "services:\n  web:\n    image: nginx\n",
	})
	defer undeployedResponse.Body.Close()
	var undeployed composeResponse
	if err := json.NewDecoder(undeployedResponse.Body).Decode(&undeployed); err != nil {
		t.Fatalf("decode undeployed compose: %v", err)
	}

	undeployedLogsResponse := server.request(t, http.MethodGet, composeLogsPath(project.ID, production.ID, undeployed.ID), nil)
	defer undeployedLogsResponse.Body.Close()
	if undeployedLogsResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("undeployed logs status = %d, want %d", undeployedLogsResponse.StatusCode, http.StatusBadRequest)
	}
	if len(server.runner.calls) != 0 {
		t.Fatalf("runner calls for undeployed logs = %+v, want none", server.runner.calls)
	}

	workingResponse := postJSON(t, server, composesPath(project.ID, production.ID), map[string]string{
		"name":        "web",
		"yamlContent": "services:\n  web:\n    image: nginx\n",
	})
	defer workingResponse.Body.Close()
	var working composeResponse
	if err := json.NewDecoder(workingResponse.Body).Decode(&working); err != nil {
		t.Fatalf("decode working compose: %v", err)
	}
	deployResponse := server.request(t, http.MethodPost, composeDeployPath(project.ID, production.ID, working.ID), nil)
	deployResponse.Body.Close()
	if deployResponse.StatusCode != http.StatusOK {
		t.Fatalf("deploy status = %d, want %d", deployResponse.StatusCode, http.StatusOK)
	}

	server.runner.logsErr = errors.New("docker daemon unreachable")
	logsFailureResponse := server.request(t, http.MethodGet, composeLogsPath(project.ID, production.ID, working.ID), nil)
	defer logsFailureResponse.Body.Close()
	if logsFailureResponse.StatusCode != http.StatusBadGateway {
		t.Fatalf("logs with failing runner status = %d, want %d", logsFailureResponse.StatusCode, http.StatusBadGateway)
	}
	var failure map[string]string
	if err := json.NewDecoder(logsFailureResponse.Body).Decode(&failure); err != nil {
		t.Fatalf("decode logs failure: %v", err)
	}
	if failure["error"] != "logs failed: docker daemon unreachable" {
		t.Fatalf("logs failure = %+v, want the runner error", failure)
	}
	if len(server.runner.calls) != 3 || server.runner.calls[2].method != "logs" {
		t.Fatalf("runner calls after failed logs = %+v, want logs", server.runner.calls)
	}
}

func TestComposeActionsKeepOriginalIdentityAfterProjectRename(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	project := createProject(t, server, "Rename Co", "")
	production := productionEnvironment(t, server, project)

	createResponse := postJSON(t, server, composesPath(project.ID, production.ID), map[string]string{
		"name":        "web",
		"yamlContent": "services:\n  web:\n    image: nginx\n",
	})
	defer createResponse.Body.Close()
	var compose composeResponse
	if err := json.NewDecoder(createResponse.Body).Decode(&compose); err != nil {
		t.Fatalf("decode created compose: %v", err)
	}

	deployResponse := server.request(t, http.MethodPost, composeDeployPath(project.ID, production.ID, compose.ID), nil)
	defer deployResponse.Body.Close()
	if deployResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(deployResponse.Body)
		t.Fatalf("deploy status = %d, want %d, body = %s", deployResponse.StatusCode, http.StatusOK, body)
	}
	assertRunIdentity(t, "deploy", server.runner.calls[0].request, server.dataDir, "rename-co", "production", "web")

	renameResponse := server.request(t, http.MethodPut, "/api/projects/"+strconv.FormatInt(project.ID, 10), map[string]string{
		"name":        "Renamed Co",
		"description": "Renamed after the deploy",
	})
	defer renameResponse.Body.Close()
	if renameResponse.StatusCode != http.StatusOK {
		t.Fatalf("rename project status = %d, want %d", renameResponse.StatusCode, http.StatusOK)
	}

	// Status must still be read from the original path: the deployed files
	// did not move with the rename.
	listResponse := server.request(t, http.MethodGet, composesPath(project.ID, production.ID), nil)
	defer listResponse.Body.Close()
	var composesList []composeResponse
	if err := json.NewDecoder(listResponse.Body).Decode(&composesList); err != nil {
		t.Fatalf("decode composes list: %v", err)
	}
	if len(composesList) != 1 || composesList[0].Status != (composeStatusResponse{Running: 1, Total: 1}) {
		t.Fatalf("composes list after rename = %+v, want the running stack found at its original path", composesList)
	}
	lastCall := server.runner.calls[len(server.runner.calls)-1]
	if lastCall.method != "status" {
		t.Fatalf("last runner call after rename = %q, want %q", lastCall.method, "status")
	}
	assertRunIdentity(t, "status after rename", lastCall.request, server.dataDir, "rename-co", "production", "web")

	stopResponse := server.request(t, http.MethodPost, composeStopPath(project.ID, production.ID, compose.ID), nil)
	defer stopResponse.Body.Close()
	if stopResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(stopResponse.Body)
		t.Fatalf("stop after project rename status = %d, want %d, body = %s", stopResponse.StatusCode, http.StatusOK, body)
	}
	assertRunIdentity(t, "stop", server.runner.calls[len(server.runner.calls)-2].request, server.dataDir, "rename-co", "production", "web")

	restartResponse := server.request(t, http.MethodPost, composeRestartPath(project.ID, production.ID, compose.ID), nil)
	defer restartResponse.Body.Close()
	if restartResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(restartResponse.Body)
		t.Fatalf("restart after project rename status = %d, want %d, body = %s", restartResponse.StatusCode, http.StatusOK, body)
	}
	assertRunIdentity(t, "restart", server.runner.calls[len(server.runner.calls)-2].request, server.dataDir, "rename-co", "production", "web")

	logsResponse := server.request(t, http.MethodGet, composeLogsPath(project.ID, production.ID, compose.ID), nil)
	defer logsResponse.Body.Close()
	if logsResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(logsResponse.Body)
		t.Fatalf("logs after project rename status = %d, want %d, body = %s", logsResponse.StatusCode, http.StatusOK, body)
	}
	assertRunIdentity(t, "logs", server.runner.calls[len(server.runner.calls)-1].request, server.dataDir, "rename-co", "production", "web")

	deleteResponse := server.request(t, http.MethodDelete, composePath(project.ID, production.ID, compose.ID), nil)
	defer deleteResponse.Body.Close()
	if deleteResponse.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(deleteResponse.Body)
		t.Fatalf("delete after project rename status = %d, want %d, body = %s", deleteResponse.StatusCode, http.StatusNoContent, body)
	}
	lastCall = server.runner.calls[len(server.runner.calls)-1]
	if lastCall.method != "down-with-volumes" {
		t.Fatalf("last runner call after delete = %q, want %q (no orphaned teardown skip)", lastCall.method, "down-with-volumes")
	}
	assertRunIdentity(t, "delete", lastCall.request, server.dataDir, "rename-co", "production", "web")
}

func TestComposeActionsKeepOriginalIdentityAfterEnvironmentRename(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	project := createProject(t, server, "Rename Env Co", "")
	production := productionEnvironment(t, server, project)

	createResponse := postJSON(t, server, composesPath(project.ID, production.ID), map[string]string{
		"name":        "web",
		"yamlContent": "services:\n  web:\n    image: nginx\n",
	})
	defer createResponse.Body.Close()
	var compose composeResponse
	if err := json.NewDecoder(createResponse.Body).Decode(&compose); err != nil {
		t.Fatalf("decode created compose: %v", err)
	}

	deployResponse := server.request(t, http.MethodPost, composeDeployPath(project.ID, production.ID, compose.ID), nil)
	defer deployResponse.Body.Close()
	if deployResponse.StatusCode != http.StatusOK {
		t.Fatalf("deploy status = %d, want %d", deployResponse.StatusCode, http.StatusOK)
	}

	// Environments have no rename endpoint yet, so the rename is written
	// directly; the invariant under test is that stored identity does not
	// follow it.
	if _, err := server.db.Exec("UPDATE environments SET name = 'canary' WHERE id = ?", production.ID); err != nil {
		t.Fatalf("rename environment: %v", err)
	}

	listResponse := server.request(t, http.MethodGet, composesPath(project.ID, production.ID), nil)
	defer listResponse.Body.Close()
	var composesList []composeResponse
	if err := json.NewDecoder(listResponse.Body).Decode(&composesList); err != nil {
		t.Fatalf("decode composes list: %v", err)
	}
	if len(composesList) != 1 || composesList[0].Status != (composeStatusResponse{Running: 1, Total: 1}) {
		t.Fatalf("composes list after rename = %+v, want the running stack found at its original path", composesList)
	}

	stopResponse := server.request(t, http.MethodPost, composeStopPath(project.ID, production.ID, compose.ID), nil)
	defer stopResponse.Body.Close()
	if stopResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(stopResponse.Body)
		t.Fatalf("stop after environment rename status = %d, want %d, body = %s", stopResponse.StatusCode, http.StatusOK, body)
	}
	assertRunIdentity(t, "stop", server.runner.calls[len(server.runner.calls)-2].request, server.dataDir, "rename-env-co", "production", "web")

	restartResponse := server.request(t, http.MethodPost, composeRestartPath(project.ID, production.ID, compose.ID), nil)
	defer restartResponse.Body.Close()
	if restartResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(restartResponse.Body)
		t.Fatalf("restart after environment rename status = %d, want %d, body = %s", restartResponse.StatusCode, http.StatusOK, body)
	}
	assertRunIdentity(t, "restart", server.runner.calls[len(server.runner.calls)-2].request, server.dataDir, "rename-env-co", "production", "web")

	logsResponse := server.request(t, http.MethodGet, composeLogsPath(project.ID, production.ID, compose.ID), nil)
	defer logsResponse.Body.Close()
	if logsResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(logsResponse.Body)
		t.Fatalf("logs after environment rename status = %d, want %d, body = %s", logsResponse.StatusCode, http.StatusOK, body)
	}
	assertRunIdentity(t, "logs", server.runner.calls[len(server.runner.calls)-1].request, server.dataDir, "rename-env-co", "production", "web")

	deleteResponse := server.request(t, http.MethodDelete, composePath(project.ID, production.ID, compose.ID), nil)
	defer deleteResponse.Body.Close()
	if deleteResponse.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(deleteResponse.Body)
		t.Fatalf("delete after environment rename status = %d, want %d, body = %s", deleteResponse.StatusCode, http.StatusNoContent, body)
	}
	lastCall := server.runner.calls[len(server.runner.calls)-1]
	if lastCall.method != "down-with-volumes" {
		t.Fatalf("last runner call after delete = %q, want %q (no orphaned teardown skip)", lastCall.method, "down-with-volumes")
	}
	assertRunIdentity(t, "delete", lastCall.request, server.dataDir, "rename-env-co", "production", "web")
}

func TestComposeCreateStoresDeployIdentity(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	project := createProject(t, server, "Identity Co", "")
	production := productionEnvironment(t, server, project)

	createResponse := postJSON(t, server, composesPath(project.ID, production.ID), map[string]string{
		"name":        "web",
		"yamlContent": "services:\n  web:\n    image: nginx\n",
	})
	defer createResponse.Body.Close()
	if createResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create compose status = %d, want %d", createResponse.StatusCode, http.StatusCreated)
	}
	var compose composeResponse
	if err := json.NewDecoder(createResponse.Body).Decode(&compose); err != nil {
		t.Fatalf("decode created compose: %v", err)
	}

	var projectSlug, envSlug string
	if err := server.db.QueryRow("SELECT project_slug, env_slug FROM composes WHERE id = ?", compose.ID).Scan(&projectSlug, &envSlug); err != nil {
		t.Fatalf("read stored compose identity: %v", err)
	}
	if projectSlug != "identity-co" || envSlug != "production" {
		t.Fatalf("stored compose identity = (%q, %q), want (%q, %q)", projectSlug, envSlug, "identity-co", "production")
	}
}

func TestBackfillLegacyIdentitiesFreezesCurrentParentSlugs(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	project := createProject(t, server, "Legacy Co", "")
	production := productionEnvironment(t, server, project)

	// A row created before identity was stored: no slugs, exactly what
	// migration 00007 leaves behind.
	now := time.Now().UTC().Truncate(time.Second)
	result, err := server.db.Exec(`
		INSERT INTO composes (environment_id, name, yaml_content, created_at, updated_at)
		VALUES (?, 'web', 'services: {}\n', ?, ?)
	`, production.ID, now.Unix(), now.Unix())
	if err != nil {
		t.Fatalf("insert legacy compose: %v", err)
	}
	legacyID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("read legacy compose id: %v", err)
	}

	if err := composes.BackfillLegacyIdentities(context.Background(), server.db); err != nil {
		t.Fatalf("backfill legacy identities: %v", err)
	}
	var projectSlug, envSlug string
	if err := server.db.QueryRow("SELECT project_slug, env_slug FROM composes WHERE id = ?", legacyID).Scan(&projectSlug, &envSlug); err != nil {
		t.Fatalf("read backfilled compose identity: %v", err)
	}
	if projectSlug != "legacy-co" || envSlug != "production" {
		t.Fatalf("backfilled compose identity = (%q, %q), want (%q, %q)", projectSlug, envSlug, "legacy-co", "production")
	}

	// Idempotent: once frozen, a later parent rename is never re-derived.
	renameResponse := server.request(t, http.MethodPut, "/api/projects/"+strconv.FormatInt(project.ID, 10), map[string]string{
		"name":        "Relocated Co",
		"description": "Renamed after the backfill",
	})
	defer renameResponse.Body.Close()
	if renameResponse.StatusCode != http.StatusOK {
		t.Fatalf("rename project status = %d, want %d", renameResponse.StatusCode, http.StatusOK)
	}
	if err := composes.BackfillLegacyIdentities(context.Background(), server.db); err != nil {
		t.Fatalf("second backfill legacy identities: %v", err)
	}
	if err := server.db.QueryRow("SELECT project_slug, env_slug FROM composes WHERE id = ?", legacyID).Scan(&projectSlug, &envSlug); err != nil {
		t.Fatalf("reread backfilled compose identity: %v", err)
	}
	if projectSlug != "legacy-co" || envSlug != "production" {
		t.Fatalf("compose identity after second backfill = (%q, %q), want it unchanged", projectSlug, envSlug)
	}
}

func TestEnvironmentVariablesRequireExistingProjectAndEnvironment(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)

	missingProjectResponse := server.request(t, http.MethodGet, environmentVariablesPath(999, 1), nil)
	defer missingProjectResponse.Body.Close()
	if missingProjectResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("missing project environment variables status = %d, want %d", missingProjectResponse.StatusCode, http.StatusNotFound)
	}

	project := createProject(t, server, "Storefront", "Customer-facing application")
	environmentsResponse := server.request(t, http.MethodGet, projectEnvironmentsPath(project.ID), nil)
	defer environmentsResponse.Body.Close()
	var environments []environmentResponse
	if err := json.NewDecoder(environmentsResponse.Body).Decode(&environments); err != nil {
		t.Fatalf("decode environments: %v", err)
	}

	missingEnvironmentResponse := server.request(t, http.MethodGet, environmentVariablesPath(project.ID, 999), nil)
	defer missingEnvironmentResponse.Body.Close()
	if missingEnvironmentResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("missing environment variables status = %d, want %d", missingEnvironmentResponse.StatusCode, http.StatusNotFound)
	}

	otherProject := createProject(t, server, "Operations", "Internal tooling")
	wrongProjectResponse := server.request(t, http.MethodGet, environmentVariablesPath(otherProject.ID, environments[0].ID), nil)
	defer wrongProjectResponse.Body.Close()
	if wrongProjectResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("environment under another project status = %d, want %d", wrongProjectResponse.StatusCode, http.StatusNotFound)
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

// TestDeepLinksServeTheAppShell pins the history-routing contract: the panel
// serves dist/index.html for any path that is not a real file (the frontend
// router resolves it client-side), while the API namespace keeps answering
// 401/404 and real build output keeps coming from /assets/.
func TestDeepLinksServeTheAppShell(t *testing.T) {
	server := newTestServer(t)

	healthz := server.request(t, http.MethodGet, "/healthz", nil)
	healthz.Body.Close()
	if healthz.StatusCode != http.StatusOK {
		t.Fatalf("healthz status = %d, want %d", healthz.StatusCode, http.StatusOK)
	}

	// A fresh load of a deep link while signed out still reaches the app shell,
	// never a 404 page — the sign-in flow takes over inside the app.
	deepLink := server.request(t, http.MethodGet, "/projects/1/env/1/compose/1", nil)
	defer deepLink.Body.Close()
	if deepLink.StatusCode != http.StatusOK {
		t.Fatalf("deep link status = %d, want %d", deepLink.StatusCode, http.StatusOK)
	}
	if got := deepLink.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Fatalf("deep link content type = %q, want text/html", got)
	}
	shell, err := io.ReadAll(deepLink.Body)
	if err != nil {
		t.Fatalf("read deep link body: %v", err)
	}
	if !strings.Contains(string(shell), "Relist Panel") {
		t.Fatalf("deep link body does not contain the app shell")
	}

	// Signed out, an unknown API path is authenticated away, as before —
	// including the namespace root itself, which must not fall through to the
	// public shell.
	unauthedAPI := server.request(t, http.MethodGet, "/api/unknown", nil)
	unauthedAPI.Body.Close()
	if unauthedAPI.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unknown API path while signed out status = %d, want %d", unauthedAPI.StatusCode, http.StatusUnauthorized)
	}
	unauthedRoot := server.request(t, http.MethodGet, "/api", nil)
	unauthedRoot.Body.Close()
	if unauthedRoot.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bare /api while signed out status = %d, want %d", unauthedRoot.StatusCode, http.StatusUnauthorized)
	}

	setupAndLogin(t, server)

	// Signed in, the same deep link reaches the shell too…
	authedDeepLink := server.request(t, http.MethodGet, "/projects/1", nil)
	defer authedDeepLink.Body.Close()
	if authedDeepLink.StatusCode != http.StatusOK {
		t.Fatalf("authenticated deep link status = %d, want %d", authedDeepLink.StatusCode, http.StatusOK)
	}

	// …while an unknown API path still 404s instead of serving HTML.
	authedAPI := server.request(t, http.MethodGet, "/api/unknown", nil)
	authedAPI.Body.Close()
	if authedAPI.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown API path status = %d, want %d", authedAPI.StatusCode, http.StatusNotFound)
	}

	// A real build asset is still served as a file, discovered from the shell
	// so the test survives Vite's content hashes.
	asset := assetPath(t, string(shell))
	assetResponse := server.request(t, http.MethodGet, asset, nil)
	defer assetResponse.Body.Close()
	if assetResponse.StatusCode != http.StatusOK {
		t.Fatalf("asset %s status = %d, want %d", asset, assetResponse.StatusCode, http.StatusOK)
	}
}

// assetPath pulls the first /assets/… reference out of the served shell.
func assetPath(t *testing.T, shell string) string {
	t.Helper()
	const marker = `/assets/`
	start := strings.Index(shell, marker)
	if start < 0 {
		t.Fatalf("shell does not reference any /assets/ file")
	}
	end := strings.IndexAny(shell[start:], `"'>`)
	if end < 0 {
		t.Fatalf("shell asset reference is not quoted: %s", shell[start:])
	}
	return shell[start : start+end]
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

type environmentVariableResponse struct {
	ID            int64  `json:"id"`
	EnvironmentID int64  `json:"environmentId"`
	Key           string `json:"key"`
	Value         string `json:"value"`
}

type composeResponse struct {
	ID             int64                 `json:"id"`
	EnvironmentID  int64                 `json:"environmentId"`
	Name           string                `json:"name"`
	YAMLContent    string                `json:"yamlContent"`
	CreatedAt      string                `json:"createdAt"`
	UpdatedAt      string                `json:"updatedAt"`
	LastDeployedAt *string               `json:"lastDeployedAt"`
	Status         composeStatusResponse `json:"status"`
}

type composeStatusResponse struct {
	Running int `json:"running"`
	Total   int `json:"total"`
}

type testServer struct {
	handler http.Handler
	db      *sql.DB
	dataDir string
	runner  *fakeComposeRunner
	history *fakeHostHistory
	facts   *fakeServiceFacts
	cookies map[string]*http.Cookie
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	dataDir := t.TempDir()
	runner := newFakeComposeRunner()
	history := newFakeHostHistory()
	facts := newFakeServiceFacts()
	return &testServer{
		handler: httpapi.NewHandler(db, runner, dataDir, history, facts),
		db:      db,
		dataDir: dataDir,
		runner:  runner,
		history: history,
		facts:   facts,
		cookies: make(map[string]*http.Cookie),
	}
}

// fakeComposeRunner is the ComposeRunner fake described by the spec's
// Testing Decisions: it records what it was asked to do instead of touching
// a real Docker daemon.
type fakeComposeRunner struct {
	calls              []fakeComposeRun
	statuses           map[string]composes.ComposeStatus
	upErr              error
	downErr            error
	downWithVolumesErr error
	restartErr         error
	logsOutput         string
	logsErr            error
}

type fakeComposeRun struct {
	method  string
	request composes.RunRequest
}

func newFakeComposeRunner() *fakeComposeRunner {
	return &fakeComposeRunner{statuses: make(map[string]composes.ComposeStatus)}
}

func (f *fakeComposeRunner) Up(_ context.Context, req composes.RunRequest) error {
	f.calls = append(f.calls, fakeComposeRun{method: "up", request: req})
	return f.upErr
}

func (f *fakeComposeRunner) Down(_ context.Context, req composes.RunRequest) error {
	f.calls = append(f.calls, fakeComposeRun{method: "down", request: req})
	if f.downErr != nil {
		return f.downErr
	}
	f.statuses[req.ProjectName] = composes.ComposeStatus{}
	return nil
}

func (f *fakeComposeRunner) DownWithVolumes(_ context.Context, req composes.RunRequest) error {
	f.calls = append(f.calls, fakeComposeRun{method: "down-with-volumes", request: req})
	if f.downWithVolumesErr != nil {
		return f.downWithVolumesErr
	}
	f.statuses[req.ProjectName] = composes.ComposeStatus{}
	return nil
}

func (f *fakeComposeRunner) Restart(_ context.Context, req composes.RunRequest) error {
	f.calls = append(f.calls, fakeComposeRun{method: "restart", request: req})
	return f.restartErr
}

func (f *fakeComposeRunner) Status(_ context.Context, req composes.RunRequest) (composes.ComposeStatus, error) {
	f.calls = append(f.calls, fakeComposeRun{method: "status", request: req})
	if status, ok := f.statuses[req.ProjectName]; ok {
		return status, nil
	}
	return composes.ComposeStatus{Running: 1, Total: 1}, nil
}

func (f *fakeComposeRunner) Logs(_ context.Context, req composes.RunRequest) (string, error) {
	f.calls = append(f.calls, fakeComposeRun{method: "logs", request: req})
	if f.logsErr != nil {
		return "", f.logsErr
	}
	return f.logsOutput, nil
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

func productionEnvironment(t *testing.T, server *testServer, project projectResponse) environmentResponse {
	t.Helper()
	response := server.request(t, http.MethodGet, projectEnvironmentsPath(project.ID), nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("list environments status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	var environments []environmentResponse
	if err := json.NewDecoder(response.Body).Decode(&environments); err != nil {
		t.Fatalf("decode environments: %v", err)
	}
	if len(environments) == 0 {
		t.Fatalf("environments = %+v, want the default environment", environments)
	}
	return environments[0]
}

// assertRunIdentity checks a runner call targeted the deploy identity derived
// from the given stored slugs (ADR-0002).
func assertRunIdentity(t *testing.T, label string, got composes.RunRequest, dataDir, projectSlug, envSlug, composeSlug string) {
	t.Helper()
	dir := filepath.Join(dataDir, projectSlug, envSlug, composeSlug)
	want := composes.RunRequest{
		ProjectName:     projectSlug + "-" + envSlug + "-" + composeSlug,
		ComposeFilePath: filepath.Join(dir, "docker-compose.yml"),
		EnvFilePath:     filepath.Join(dir, ".env"),
	}
	if got != want {
		t.Fatalf("%s run request = %+v, want %+v", label, got, want)
	}
}

func projectEnvironmentsPath(projectID int64) string {
	return "/api/projects/" + strconv.FormatInt(projectID, 10) + "/environments"
}

func environmentPath(projectID, environmentID int64) string {
	return projectEnvironmentsPath(projectID) + "/" + strconv.FormatInt(environmentID, 10)
}

func environmentVariablesPath(projectID, environmentID int64) string {
	return environmentPath(projectID, environmentID) + "/variables"
}

func variablePath(projectID, environmentID, variableID int64) string {
	return environmentVariablesPath(projectID, environmentID) + "/" + strconv.FormatInt(variableID, 10)
}

func composesPath(projectID, environmentID int64) string {
	return environmentPath(projectID, environmentID) + "/composes"
}

func composePath(projectID, environmentID, composeID int64) string {
	return composesPath(projectID, environmentID) + "/" + strconv.FormatInt(composeID, 10)
}

func composeDeployPath(projectID, environmentID, composeID int64) string {
	return composePath(projectID, environmentID, composeID) + "/deploy"
}

func composeStopPath(projectID, environmentID, composeID int64) string {
	return composePath(projectID, environmentID, composeID) + "/stop"
}

func composeRestartPath(projectID, environmentID, composeID int64) string {
	return composePath(projectID, environmentID, composeID) + "/restart"
}

func composeLogsPath(projectID, environmentID, composeID int64) string {
	return composePath(projectID, environmentID, composeID) + "/logs"
}
