package authz

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"relist-panel/internal/auth"
	"relist-panel/internal/database"
)

// matrixPath fills a pattern's wildcards with the seeded ids: Project 1, its
// only Environment 11, Compose 101; any other wildcard takes "1".
func matrixPath(pattern string) (method, path string) {
	method, rest, found := strings.Cut(pattern, " ")
	if !found {
		method, rest = http.MethodGet, pattern
	}
	segs := strings.Split(rest, "/")
	for i, seg := range segs {
		if !strings.HasPrefix(seg, "{") {
			continue
		}
		name := strings.Trim(seg, "{}")
		switch {
		case name == "projectID" || (name == "id" && segs[i-1] == "projects"):
			segs[i] = "1"
		case name == "environmentID" || (name == "id" && segs[i-1] == "environments"):
			segs[i] = "11"
		case name == "composeID":
			segs[i] = "101"
		default:
			segs[i] = "1"
		}
	}
	return method, strings.Join(segs, "/")
}

func TestRoleMatrix(t *testing.T) {
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		INSERT INTO users (id, email, password_hash, is_admin, created_at) VALUES
			(1, 'owner@example.com', 'x', 0, 1), (3, 'admin@example.com', 'x', 1, 1),
			(10, 'none@example.com', 'x', 0, 1), (11, 'noaccess@example.com', 'x', 0, 1),
			(12, 'viewer@example.com', 'x', 0, 1), (13, 'operator@example.com', 'x', 0, 1),
			(14, 'editor@example.com', 'x', 0, 1), (15, 'projecteditor@example.com', 'x', 0, 1);
		INSERT INTO projects (id, name, description, created_at, updated_at) VALUES (1, 'a', '', 1, 1);
		INSERT INTO environments (id, project_id, name, created_at) VALUES (11, 1, 'a1', 1);
		INSERT INTO composes (id, environment_id, name, yaml_content, created_at, updated_at) VALUES (101, 11, 'c', '', 1, 1);
		INSERT INTO grants (user_id, project_id, environment_id, role) VALUES
			(11, 1, NULL, 'editor'), (11, 1, 11, 'none'),
			(12, 1, 11, 'viewer'),
			(13, 1, 11, 'operator'),
			(14, 1, 11, 'editor'),
			(15, 1, NULL, 'editor'), (15, 1, 11, 'viewer');
	`); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	patterns := append([]string{}, adminOnly...)
	for pattern := range Routes {
		patterns = append(patterns, pattern)
	}
	for _, pattern := range patterns {
		mux.HandleFunc(pattern, ok)
	}
	handler := New(db, func(*http.Request) (auth.User, bool, error) { return auth.User{}, false, nil }).Middleware(mux, mux)

	const (
		gone   = http.StatusNotFound
		denied = http.StatusForbidden
		passed = http.StatusNoContent
	)
	// Per subject: the code for a Viewer, Operator, Editor, Project Editor,
	// scoped admin-only and global admin-only route.
	subjects := []struct {
		name string
		user auth.User
		want [6]int
	}{
		{"no grant", auth.User{ID: 10}, [6]int{gone, gone, gone, gone, gone, denied}},
		{"no access on the environment", auth.User{ID: 11}, [6]int{gone, gone, gone, gone, gone, denied}},
		{"viewer", auth.User{ID: 12}, [6]int{passed, denied, denied, denied, denied, denied}},
		{"operator", auth.User{ID: 13}, [6]int{passed, passed, denied, denied, denied, denied}},
		{"editor", auth.User{ID: 14}, [6]int{passed, passed, passed, denied, denied, denied}},
		{"project editor, viewer on the environment", auth.User{ID: 15}, [6]int{passed, denied, denied, passed, denied, denied}},
		{"admin", auth.User{ID: 3, IsAdmin: true}, [6]int{passed, passed, passed, passed, passed, passed}},
		{"owner", auth.User{ID: auth.OwnerID, IsAdmin: true}, [6]int{passed, passed, passed, passed, passed, passed}},
	}
	column := func(pattern string) int {
		switch Routes[pattern].Level {
		case LevelViewer:
			return 0
		case LevelOperator:
			return 1
		case LevelEditor:
			return 2
		case LevelProjectEditor:
			return 3
		case LevelPublic, LevelSignedIn:
			return -1
		}
		if s, _ := scopeOf(pattern, ""); strings.Contains(pattern, "/projects/{") || s.ProjectID != 0 {
			return 4
		}
		return 5
	}
	for _, subject := range subjects {
		for _, pattern := range patterns {
			method, path := matrixPath(pattern)
			request := httptest.NewRequest(method, path, nil)
			request = request.WithContext(auth.WithUser(request.Context(), subject.user))
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			want := passed
			if c := column(pattern); c >= 0 {
				want = subject.want[c]
			}
			if pattern == "/" {
				// The catch-all is never authorized; the stub answers it.
				want = passed
			}
			if recorder.Code != want {
				t.Errorf("%s: %s = %d, want %d", subject.name, fmt.Sprintf("%s %s", method, path), recorder.Code, want)
			}
		}
	}
}

// TestKeyRoutesHaveTheSpecLevel pins the routes the spec's success criteria
// name, independently of Routes: the matrix above reads its expectation from
// Routes, so only this test notices a route moved to the wrong level.
func TestKeyRoutesHaveTheSpecLevel(t *testing.T) {
	const c = "/api/projects/{projectID}/environments/{environmentID}/composes/{composeID}"
	pins := map[string]Level{
		"GET " + c:                     LevelViewer,
		"GET " + c + "/logs":           LevelViewer,
		"GET " + c + "/variables":      LevelViewer,
		"POST " + c + "/restart":       LevelOperator,
		"POST " + c + "/stop":          LevelOperator,
		"POST " + c + "/deployments":   LevelOperator,
		"GET " + c + "/terminal":       LevelEditor,
		"GET " + c + "/volumes/read":   LevelEditor,
		"POST " + c + "/source/upload": LevelEditor,
		"POST /api/projects/{projectID}/environments/{environmentID}/variables": LevelEditor,
		"POST /api/projects/{projectID}/environments":                           LevelProjectEditor,
		"PUT /api/projects/{id}":                                                LevelProjectEditor,
		"GET /api/projects":                                                     LevelSignedIn,
	}
	for pattern, want := range pins {
		rule, ok := Routes[pattern]
		if !ok || rule.Level != want {
			t.Errorf("Routes[%q] = %v (present %v), want level %v", pattern, rule.Level, ok, want)
		}
	}
	// Only an admin writes a Compose's YAML (ADR-0032, Editors and the file).
	adminOnly := []string{"POST /api/projects", "DELETE /api/projects/{id}", "GET /api/users", "PUT /api/users/{id}/grants", "GET /api/docker",
		"PUT " + c, "POST " + c + "/preview", "POST /api/projects/{projectID}/environments/{environmentID}/composes"}
	for _, pattern := range adminOnly {
		if _, ok := Routes[pattern]; ok {
			t.Errorf("%q must stay admin-only (absent from Routes)", pattern)
		}
	}
}
