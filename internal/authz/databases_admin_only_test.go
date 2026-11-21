package authz

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"relist-panel/internal/auth"
	"relist-panel/internal/database"
)

// Databases are Admin-only (ADR-0049): every route under /api/databases,
// including the ones phase 8 opened to grantees, refuses a signed-in member.
func TestDatabaseRoutesAreAdminOnly(t *testing.T) {
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO users (id, email, password_hash, is_admin, created_at) VALUES (20, 'member@example.com', 'x', 0, 1)`); err != nil {
		t.Fatal(err)
	}
	patterns := []string{
		"GET /api/databases", "GET /api/databases/engines", "GET /api/databases/{id}", "GET /api/databases/{id}/credentials",
		"GET /api/databases/{id}/logical-databases", "POST /api/databases/{id}/query", "GET /api/databases/{id}/query/schema",
		"POST /api/databases/{id}/redis/command", "GET /api/databases/{id}/redis/keys", "GET /api/databases/{id}/redis/key",
		"POST /api/databases", "DELETE /api/databases/{id}", "POST /api/databases/{id}/recreate",
	}
	mux := http.NewServeMux()
	for _, p := range patterns {
		mux.HandleFunc(p, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	}
	handler := New(db, func(*http.Request) (auth.User, bool, error) { return auth.User{}, false, nil }).Middleware(mux, mux)
	for _, p := range patterns {
		for _, u := range []struct {
			user auth.User
			want int
		}{{auth.User{ID: 20}, http.StatusForbidden}, {auth.User{ID: 3, IsAdmin: true}, http.StatusNoContent}} {
			method, path := matrixPath(p)
			req := httptest.NewRequest(method, path, nil)
			req = req.WithContext(auth.WithUser(req.Context(), u.user))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != u.want {
				t.Errorf("%s as %+v = %d, want %d", p, u.user, rec.Code, u.want)
			}
		}
		if _, ok := Routes[p]; ok {
			t.Errorf("Routes[%q] is set: Database routes must stay out of Routes, which makes them Admin-only", p)
		}
	}
	// Any route of the Databases family, not only the ones above.
	for p := range Routes {
		_, path, _ := strings.Cut(p, " ")
		for _, prefix := range []string{"/api/databases", "/api/direct-accesses", "/api/backup-destinations"} {
			if path == prefix || strings.HasPrefix(path, prefix+"/") {
				t.Errorf("Routes[%q] is set: Databases are Admin-only (ADR-0049)", p)
			}
		}
	}
}
