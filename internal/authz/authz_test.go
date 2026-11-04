package authz

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"relist-panel/internal/auth"
)

func testMux() *http.ServeMux {
	mux := http.NewServeMux()
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	mux.HandleFunc("GET /api/auth/account", ok)
	mux.HandleFunc("GET /api/projects", ok)
	mux.HandleFunc("GET /api/projects/{id}", ok)
	return mux
}

func serve(mux *http.ServeMux, user *auth.User, method, path string) int {
	request := httptest.NewRequest(method, path, nil)
	if user != nil {
		request = request.WithContext(auth.WithUser(request.Context(), *user))
	}
	recorder := httptest.NewRecorder()
	Middleware(mux, mux).ServeHTTP(recorder, request)
	return recorder.Code
}

func TestNonAdminReachesOnlySignedInRoutes(t *testing.T) {
	mux := testMux()
	admin := &auth.User{ID: 2, Email: "a@example.com", IsAdmin: true}
	member := &auth.User{ID: 3, Email: "m@example.com"}
	cases := []struct {
		user *auth.User
		path string
		want int
	}{
		{admin, "/api/auth/account", http.StatusOK},
		{admin, "/api/projects", http.StatusOK},
		{admin, "/api/projects/3", http.StatusOK},
		{member, "/api/auth/account", http.StatusOK},
		{member, "/api/projects", http.StatusForbidden},
		{member, "/api/projects/3", http.StatusForbidden},
	}
	for _, c := range cases {
		if got := serve(mux, c.user, http.MethodGet, c.path); got != c.want {
			t.Errorf("%s as admin=%v = %d, want %d", c.path, c.user.IsAdmin, got, c.want)
		}
	}
}

func TestUnroutedRequestPassesThrough(t *testing.T) {
	member := &auth.User{ID: 3, Email: "m@example.com"}
	if got := serve(testMux(), member, http.MethodGet, "/api/nope"); got != http.StatusNotFound {
		t.Fatalf("GET /api/nope = %d, want 404", got)
	}
}

func TestRequestWithoutUserPassesThrough(t *testing.T) {
	if got := serve(testMux(), nil, http.MethodGet, "/api/projects"); got != http.StatusOK {
		t.Fatalf("no user = %d, want 200 (auth already decided)", got)
	}
}
