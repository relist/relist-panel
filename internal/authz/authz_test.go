package authz

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"relist-panel/internal/auth"
	"relist-panel/internal/database"
	"relist-panel/internal/grants"
)

// Seeded: Project A (1) with Environments A1 (11) and A2 (12); Project B (2)
// with B1 (21); Compose C (101) under A1; Compose D (102) under A2.
const (
	pA, pB      = 1, 2
	eA1, eA2    = 11, 12
	eB1         = 21
	cC, cD      = 101, 102
	memberID    = 2
	composeBase = "/api/projects/{projectID}/environments/{environmentID}/composes/{composeID}"
)

var testRoutes = map[string]Rule{
	"GET /api/auth/account":                              {Level: LevelSignedIn},
	"GET /api/projects":                                  {Level: LevelSignedIn},
	"GET /api/projects/{id}":                             {Level: LevelViewer},
	"PUT /api/projects/{id}":                             {Level: LevelProjectEditor},
	"GET " + composeBase:                                 {Level: LevelViewer},
	"POST " + composeBase + "/restart":                   {Level: LevelOperator},
	"PUT " + composeBase:                                 {Level: LevelEditor},
	"POST /api/projects/{projectID}/environments":        {Level: LevelProjectEditor},
	"DELETE /api/projects/{projectID}/environments/{id}": {Level: LevelProjectEditor},
	"GET /api/projects/{projectID}/environments/{environmentID}/variables": {Level: LevelViewer},
}

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
		INSERT INTO users (id, email, password_hash, is_admin, created_at) VALUES (1, 'owner@example.com', 'x', 1, 1), (2, 'member@example.com', 'x', 0, 1);
		INSERT INTO projects (id, name, description, created_at, updated_at) VALUES (1, 'a', '', 1, 1), (2, 'b', '', 1, 1);
		INSERT INTO environments (id, project_id, name, created_at) VALUES (11, 1, 'a1', 1), (12, 1, 'a2', 1), (21, 2, 'b1', 1);
		INSERT INTO composes (id, environment_id, name, yaml_content, created_at, updated_at) VALUES (101, 11, 'c', '', 1, 1), (102, 12, 'd', '', 1, 1);
	`); err != nil {
		t.Fatal(err)
	}
	return db
}

func grant(t *testing.T, db *sql.DB, project, environment int64, role string) {
	t.Helper()
	var env any
	if environment != 0 {
		env = environment
	}
	if _, err := db.Exec("INSERT INTO grants (user_id, project_id, environment_id, role) VALUES (?, ?, ?, ?)", memberID, project, env, role); err != nil {
		t.Fatal(err)
	}
}

// stubMux registers every pattern of testRoutes plus one admin-only pattern,
// each answering 204 with the Scope the middleware put in the context.
func stubMux() *http.ServeMux {
	mux := http.NewServeMux()
	handler := func(w http.ResponseWriter, r *http.Request) {
		scope, _ := ScopeFrom(r.Context())
		w.Header().Set("X-Scope", fmt.Sprintf("%d/%d/%d", scope.ProjectID, scope.EnvironmentID, scope.ComposeID))
		w.WriteHeader(http.StatusNoContent)
	}
	for pattern := range testRoutes {
		mux.HandleFunc(pattern, handler)
	}
	mux.HandleFunc("GET /api/admin-only", handler)
	mux.HandleFunc("DELETE /api/projects/{id}", handler)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	return mux
}

type fixture struct {
	db      *sql.DB
	mux     *http.ServeMux
	handler http.Handler
	// current is what the re-check's session lookup sees.
	current *auth.User
}

func newFixture(t *testing.T) *fixture {
	f := &fixture{db: testDB(t), mux: stubMux()}
	lookup := func(*http.Request) (auth.User, bool, error) {
		if f.current == nil {
			return auth.User{}, false, nil
		}
		return *f.current, true, nil
	}
	f.handler = newWithRoutes(f.db, lookup, testRoutes).Middleware(f.mux, f.mux)
	return f
}

func (f *fixture) serve(user *auth.User, method, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	if user != nil {
		request = request.WithContext(auth.WithUser(request.Context(), *user))
	}
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, request)
	return recorder
}

var (
	member = &auth.User{ID: memberID, Email: "member@example.com"}
	admin  = &auth.User{ID: 1, Email: "owner@example.com", IsAdmin: true}
)

func composePath(project, env, composeID int64, rest string) string {
	return fmt.Sprintf("/api/projects/%d/environments/%d/composes/%d%s", project, env, composeID, rest)
}

func expect(t *testing.T, f *fixture, user *auth.User, method, path string, want int) {
	t.Helper()
	if got := f.serve(user, method, path).Code; got != want {
		t.Errorf("%s %s = %d, want %d", method, path, got, want)
	}
}

func TestUnknownPatternIsAdminOnly(t *testing.T) {
	f := newFixture(t)
	expect(t, f, member, http.MethodGet, "/api/admin-only", http.StatusForbidden)
	expect(t, f, admin, http.MethodGet, "/api/admin-only", http.StatusNoContent)
	var zero Level
	if zero != LevelAdmin {
		t.Fatal("the zero Level must be LevelAdmin")
	}
}

func TestSignedInAndUnroutedAndPublicPass(t *testing.T) {
	f := newFixture(t)
	expect(t, f, member, http.MethodGet, "/api/auth/account", http.StatusNoContent)
	expect(t, f, member, http.MethodGet, "/api/projects", http.StatusNoContent)
	expect(t, f, member, http.MethodGet, "/api/nope", http.StatusNotFound)
	expect(t, f, nil, http.MethodGet, "/api/admin-only", http.StatusNoContent)
}

func TestNoGrantIsNotFound(t *testing.T) {
	f := newFixture(t)
	response := f.serve(member, http.MethodGet, "/api/projects/1")
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"not found"`) {
		t.Fatalf("GET /api/projects/1 = %d %s", response.Code, response.Body.String())
	}
	// An admin-only scoped route the member cannot see is 404 too.
	expect(t, f, member, http.MethodDelete, "/api/projects/1", http.StatusNotFound)
}

func TestViewerCannotRestart(t *testing.T) {
	f := newFixture(t)
	grant(t, f.db, pA, 0, "viewer")
	expect(t, f, member, http.MethodGet, composePath(pA, eA1, cC, ""), http.StatusNoContent)
	response := f.serve(member, http.MethodPost, composePath(pA, eA1, cC, "/restart"))
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "You don't have permission to do this") {
		t.Fatalf("restart = %d %s", response.Code, response.Body.String())
	}
	// Visible but admin-only: 403.
	expect(t, f, member, http.MethodDelete, "/api/projects/1", http.StatusForbidden)
}

func TestEnvironmentGrantOverridesProject(t *testing.T) {
	f := newFixture(t)
	grant(t, f.db, pA, 0, "editor")
	grant(t, f.db, pA, eA2, "viewer")
	expect(t, f, member, http.MethodPut, composePath(pA, eA1, cC, ""), http.StatusNoContent)
	expect(t, f, member, http.MethodPut, composePath(pA, eA2, cD, ""), http.StatusForbidden)
	expect(t, f, member, http.MethodGet, composePath(pA, eA2, cD, ""), http.StatusNoContent)
}

func TestNoAccessEnvironmentIsNotFound(t *testing.T) {
	f := newFixture(t)
	grant(t, f.db, pA, 0, "editor")
	grant(t, f.db, pA, eA2, "none")
	expect(t, f, member, http.MethodGet, composePath(pA, eA2, cD, ""), http.StatusNotFound)
	expect(t, f, member, http.MethodGet, "/api/projects/1/environments/12/variables", http.StatusNotFound)
	expect(t, f, member, http.MethodDelete, "/api/projects/1/environments/12", http.StatusNotFound)
	expect(t, f, member, http.MethodDelete, "/api/projects/1/environments/11", http.StatusNoContent)
}

func TestForgedParentIsNotFound(t *testing.T) {
	f := newFixture(t)
	grant(t, f.db, pA, 0, "editor")
	grant(t, f.db, pB, 0, "editor")
	expect(t, f, member, http.MethodGet, "/api/projects/2/environments/11/variables", http.StatusNotFound)
	expect(t, f, member, http.MethodGet, composePath(pA, eA2, cC, ""), http.StatusNotFound)
	expect(t, f, member, http.MethodGet, composePath(pB, eB1, cC, ""), http.StatusNotFound)
	expect(t, f, member, http.MethodGet, "/api/projects/1/environments/abc/variables", http.StatusNotFound)
	expect(t, f, member, http.MethodGet, composePath(pA, eA1, 999, ""), http.StatusNotFound)
	expect(t, f, member, http.MethodGet, "/api/projects/77", http.StatusNotFound)
	// The real ones pass and carry their scope.
	response := f.serve(member, http.MethodGet, composePath(pA, eA1, cC, ""))
	if response.Code != http.StatusNoContent || response.Header().Get("X-Scope") != "1/11/101" {
		t.Fatalf("real path = %d scope %q", response.Code, response.Header().Get("X-Scope"))
	}
}

func TestProjectEditorLevel(t *testing.T) {
	f := newFixture(t)
	grant(t, f.db, pA, 0, "viewer")
	grant(t, f.db, pA, eA1, "editor")
	expect(t, f, member, http.MethodPost, "/api/projects/1/environments", http.StatusForbidden)
	expect(t, f, member, http.MethodPut, "/api/projects/1", http.StatusForbidden)
	if _, err := f.db.Exec("UPDATE grants SET role = 'editor' WHERE environment_id IS NULL"); err != nil {
		t.Fatal(err)
	}
	expect(t, f, member, http.MethodPost, "/api/projects/1/environments", http.StatusNoContent)
	expect(t, f, member, http.MethodPut, "/api/projects/1", http.StatusNoContent)
}

func TestDatabaseErrorIsNotPassed(t *testing.T) {
	f := newFixture(t)
	f.db.Close()
	response := f.serve(member, http.MethodGet, "/api/projects/1")
	var body map[string]string
	_ = json.Unmarshal(response.Body.Bytes(), &body)
	if response.Code != http.StatusInternalServerError || body["error"] != "authorization is temporarily unavailable" {
		t.Fatalf("closed database = %d %s", response.Code, response.Body.String())
	}
}

func TestStillAllowedSeesRevokedGrant(t *testing.T) {
	f := newFixture(t)
	grant(t, f.db, pA, 0, "editor")
	f.current = member
	var still func(context.Context) (bool, error)
	f.mux.HandleFunc("GET "+composeBase+"/terminal", func(w http.ResponseWriter, r *http.Request) {
		still = StillAllowed(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	testRoutes["GET "+composeBase+"/terminal"] = Rule{Level: LevelEditor}
	t.Cleanup(func() { delete(testRoutes, "GET "+composeBase+"/terminal") })

	expect(t, f, member, http.MethodGet, composePath(pA, eA1, cC, "/terminal"), http.StatusNoContent)
	if still == nil {
		t.Fatal("no StillAllowed in the context")
	}
	if ok, err := still(context.Background()); !ok || err != nil {
		t.Fatalf("still before = %v, %v", ok, err)
	}
	if _, err := f.db.Exec("UPDATE grants SET role = 'viewer'"); err != nil {
		t.Fatal(err)
	}
	if ok, err := still(context.Background()); ok || err != nil {
		t.Fatalf("still after demotion to viewer = %v, %v", ok, err)
	}
	if _, err := f.db.Exec("UPDATE grants SET role = 'editor'"); err != nil {
		t.Fatal(err)
	}
	f.current = nil // signed out or disabled
	if ok, err := still(context.Background()); ok || err != nil {
		t.Fatalf("still after the session ended = %v, %v", ok, err)
	}
}

func TestCanReadSecrets(t *testing.T) {
	if CanReadSecrets(context.Background(), eA1) {
		t.Fatal("no Access in the context must not read secrets")
	}
	if !CanReadSecrets(WithAccess(context.Background(), grants.Access{Admin: true}), eA1) {
		t.Fatal("an admin reads secrets")
	}
	f := newFixture(t)
	grant(t, f.db, pA, 0, "operator")
	grant(t, f.db, pA, eA2, "editor")
	access, err := grants.Load(context.Background(), f.db, memberID, false)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithAccess(context.Background(), access)
	if CanReadSecrets(ctx, eA1) || !CanReadSecrets(ctx, eA2) {
		t.Fatalf("operator on A1 / editor on A2 = %v / %v", CanReadSecrets(ctx, eA1), CanReadSecrets(ctx, eA2))
	}
}
