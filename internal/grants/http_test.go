package grants

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func serve(t *testing.T, db *sql.DB, method, path string, body any) (int, string) {
	t.Helper()
	mux := http.NewServeMux()
	New(db).RegisterRoutes(mux)
	var reader *bytes.Reader
	if body != nil {
		encoded, _ := json.Marshal(body)
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(method, path, reader))
	return recorder.Code, recorder.Body.String()
}

func TestUserGrantsRoundTrip(t *testing.T) {
	db := seed(t)
	code, body := serve(t, db, http.MethodPut, "/api/users/2/grants", []map[string]any{
		{"projectId": pP, "environmentId": nil, "role": "editor"},
		{"projectId": pP, "environmentId": e1, "role": "none"},
		{"projectId": pQ, "environmentId": nil, "role": "viewer"},
	})
	want := `[{"userId":2,"email":"member@example.com","projectId":1,"environmentId":null,"role":"editor"},` +
		`{"userId":2,"email":"member@example.com","projectId":1,"environmentId":11,"role":"none"},` +
		`{"userId":2,"email":"member@example.com","projectId":2,"environmentId":null,"role":"viewer"}]` + "\n"
	if code != http.StatusOK || body != want {
		t.Fatalf("PUT = %d %s\nwant %s", code, body, want)
	}
	if code, got := serve(t, db, http.MethodGet, "/api/users/2/grants", nil); code != http.StatusOK || got != want {
		t.Fatalf("GET = %d %s", code, got)
	}
	// Replacing with fewer drops the rest.
	if code, got := serve(t, db, http.MethodPut, "/api/users/2/grants", []map[string]any{}); code != http.StatusOK || got != "[]\n" {
		t.Fatalf("PUT [] = %d %s", code, got)
	}
}

func TestUserGrantsUnknownUser(t *testing.T) {
	db := seed(t)
	if code, _ := serve(t, db, http.MethodGet, "/api/users/99/grants", nil); code != http.StatusNotFound {
		t.Fatalf("GET unknown = %d", code)
	}
	if code, _ := serve(t, db, http.MethodPut, "/api/users/99/grants", []map[string]any{}); code != http.StatusNotFound {
		t.Fatalf("PUT unknown = %d", code)
	}
}

func TestGrantValidation(t *testing.T) {
	db := seed(t)
	give(t, db, member, row{pR, 0, "viewer"})
	cases := []struct {
		name string
		body any
		want string
	}{
		{"unknown role", []map[string]any{{"projectId": pP, "role": "owner"}}, "role must be none, viewer, operator or editor"},
		{"none on a project", []map[string]any{{"projectId": pP, "role": "none"}}, "No access is only for an Environment"},
		{"foreign environment", []map[string]any{{"projectId": pP, "environmentId": r1, "role": "viewer"}}, "environment 31 does not belong to project 1"},
		{"unknown project", []map[string]any{{"projectId": 77, "role": "viewer"}}, "project 77 not found"},
		{"twice", []map[string]any{{"projectId": pP, "role": "viewer"}, {"projectId": pP, "role": "editor"}}, "grant for the same target listed twice"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, body := serve(t, db, http.MethodPut, "/api/users/2/grants", c.body)
			var got map[string]string
			_ = json.Unmarshal([]byte(body), &got)
			if code != http.StatusBadRequest || got["error"] != c.want {
				t.Fatalf("PUT = %d %s, want 400 %q", code, body, c.want)
			}
			// The old Grants stay.
			if _, after := serve(t, db, http.MethodGet, "/api/users/2/grants", nil); after != `[{"userId":2,"email":"member@example.com","projectId":3,"environmentId":null,"role":"viewer"}]`+"\n" {
				t.Fatalf("grants after a refused PUT = %s", after)
			}
		})
	}
	code, body := serve(t, db, http.MethodPut, "/api/projects/1/grants", []map[string]any{{"userId": 99, "role": "viewer"}})
	if code != http.StatusBadRequest || body != `{"error":"user 99 not found"}`+"\n" {
		t.Fatalf("project PUT unknown user = %d %s", code, body)
	}
}

func TestProjectGrantsReplaceOnlyThatProject(t *testing.T) {
	db := seed(t)
	give(t, db, member, row{pR, 0, "viewer"}, row{pP, 0, "viewer"})
	code, body := serve(t, db, http.MethodPut, "/api/projects/1/grants", []map[string]any{
		{"userId": 3, "environmentId": nil, "role": "operator"},
		{"userId": 3, "environmentId": e2, "role": "editor"},
	})
	want := `[{"userId":3,"email":"other@example.com","projectId":1,"environmentId":null,"role":"operator"},` +
		`{"userId":3,"email":"other@example.com","projectId":1,"environmentId":12,"role":"editor"}]` + "\n"
	if code != http.StatusOK || body != want {
		t.Fatalf("PUT = %d %s", code, body)
	}
	if code, got := serve(t, db, http.MethodGet, "/api/projects/1/grants", nil); code != http.StatusOK || got != want {
		t.Fatalf("GET = %d %s", code, got)
	}
	// User 2 lost their Grant on P, kept the one on R.
	if _, got := serve(t, db, http.MethodGet, "/api/users/2/grants", nil); got != `[{"userId":2,"email":"member@example.com","projectId":3,"environmentId":null,"role":"viewer"}]`+"\n" {
		t.Fatalf("user 2 = %s", got)
	}
	if code, _ := serve(t, db, http.MethodGet, "/api/projects/77/grants", nil); code != http.StatusNotFound {
		t.Fatalf("GET unknown project = %d", code)
	}
}
