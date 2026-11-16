package authz

import "testing"

func TestScopeOf(t *testing.T) {
	const c = "/api/projects/{projectID}/environments/{environmentID}/composes/{composeID}"
	cases := []struct {
		pattern, path string
		want          Scope
		ok            bool
	}{
		{"GET /api/projects/{id}", "/api/projects/7", Scope{7, 0, 0, 0}, true},
		{"DELETE /api/projects/{projectID}/environments/{id}", "/api/projects/7/environments/9", Scope{7, 9, 0, 0}, true},
		{"GET " + c + "/services/{service}/metrics", "/api/projects/7/environments/9/composes/11/services/web/metrics", Scope{7, 9, 11, 0}, true},
		{"GET /api/projects/{id}", "/api/projects/abc", Scope{}, false},
		{"GET /api/projects/{id}", "/api/projects/0", Scope{}, false},
		{"GET /api/projects/{id}", "/api/projects/-3", Scope{}, false},
		{"GET /api/users/{id}", "/api/users/4", Scope{}, true},
		{"GET /api/projects", "/api/projects", Scope{}, true},
		{"/", "/anything/at/all", Scope{}, true},
	}
	for _, tc := range cases {
		got, ok := scopeOf(tc.pattern, tc.path)
		if got != tc.want || ok != tc.ok {
			t.Errorf("scopeOf(%q, %q) = %+v, %v; want %+v, %v", tc.pattern, tc.path, got, ok, tc.want, tc.ok)
		}
	}
}

func TestScopeReadsDatabaseID(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          Scope
		ok            bool
	}{
		{"GET /api/databases/{id}", "/api/databases/5", Scope{DatabaseID: 5}, true},
		{"POST /api/databases/{id}/query", "/api/databases/12/query", Scope{DatabaseID: 12}, true},
		{"GET /api/databases/{id}", "/api/databases/x", Scope{}, false},
		{"GET /api/databases/{id}", "/api/databases/0", Scope{}, false},
		{"GET /api/databases/{id}", "/api/databases/-2", Scope{}, false},
	}
	for _, tc := range cases {
		got, ok := scopeOf(tc.pattern, tc.path)
		if got != tc.want || ok != tc.ok {
			t.Errorf("scopeOf(%q, %q) = %+v, %v; want %+v, %v", tc.pattern, tc.path, got, ok, tc.want, tc.ok)
		}
	}
}
