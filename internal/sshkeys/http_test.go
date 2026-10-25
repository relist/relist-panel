package sshkeys

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func newMux(t *testing.T) *http.ServeMux {
	t.Helper()
	db, _, _ := newDB(t)
	mux := http.NewServeMux()
	New(db).RegisterRoutes(mux)
	return mux
}

func do(mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestCreateListRenameDelete(t *testing.T) {
	mux := newMux(t)

	rec := do(mux, "POST", "/api/ssh-keys", `{"name":"ci"}`)
	if rec.Code != 201 {
		t.Fatalf("create status = %d: %s", rec.Code, rec.Body)
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if used, ok := created["usedBy"].([]any); !ok || len(used) != 0 {
		t.Fatalf("usedBy = %#v, want empty array", created["usedBy"])
	}
	if created["name"] != "ci" || created["publicKey"] == "" {
		t.Fatalf("created = %#v", created)
	}
	id := int64(created["id"].(float64))
	path := "/api/ssh-keys/" + itoa(id)

	rec = do(mux, "GET", "/api/ssh-keys", "")
	var list []map[string]any
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &list) != nil || len(list) != 1 {
		t.Fatalf("list = %d %s", rec.Code, rec.Body)
	}

	rec = do(mux, "PATCH", path, `{"name":"deploy"}`)
	var renamed map[string]any
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &renamed) != nil || renamed["name"] != "deploy" {
		t.Fatalf("rename = %d %s", rec.Code, rec.Body)
	}

	if rec = do(mux, "DELETE", path, ""); rec.Code != 204 {
		t.Fatalf("delete status = %d", rec.Code)
	}
	rec = do(mux, "GET", "/api/ssh-keys", "")
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("list after delete = %s", rec.Body)
	}
}

func TestNoResponseContainsPrivateKey(t *testing.T) {
	mux := newMux(t)
	rec := do(mux, "POST", "/api/ssh-keys", `{"name":"ci"}`)
	var created struct{ ID int64 }
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	for _, r := range []*httptest.ResponseRecorder{
		rec,
		do(mux, "GET", "/api/ssh-keys", ""),
		do(mux, "PATCH", "/api/ssh-keys/"+itoa(created.ID), `{"name":"x"}`),
	} {
		body := r.Body.String()
		if strings.Contains(body, "PRIVATE KEY") || strings.Contains(body, "privateKey") {
			t.Fatalf("response leaks private key: %s", body)
		}
	}
}

func TestStatusCodes(t *testing.T) {
	mux := newMux(t)
	do(mux, "POST", "/api/ssh-keys", `{"name":"ci"}`)
	other := do(mux, "POST", "/api/ssh-keys", `{"name":"other"}`)
	var o struct{ ID int64 }
	_ = json.Unmarshal(other.Body.Bytes(), &o)

	cases := []struct {
		name, method, path, body string
		want                     int
	}{
		{"blank name", "POST", "/api/ssh-keys", `{"name":" "}`, 400},
		{"newline in name", "POST", "/api/ssh-keys", `{"name":"a\nb"}`, 400},
		{"101 character name", "POST", "/api/ssh-keys", `{"name":"` + strings.Repeat("x", 101) + `"}`, 400},
		{"rename to newline", "PATCH", "/api/ssh-keys/" + itoa(o.ID), `{"name":"a\nb"}`, 400},
		{"rename to 101 characters", "PATCH", "/api/ssh-keys/" + itoa(o.ID), `{"name":"` + strings.Repeat("x", 101) + `"}`, 400},
		{"duplicate", "POST", "/api/ssh-keys", `{"name":"ci"}`, 409},
		{"rename to other case", "PATCH", "/api/ssh-keys/" + itoa(o.ID), `{"name":"CI"}`, 409},
		{"patch missing", "PATCH", "/api/ssh-keys/999", `{"name":"z"}`, 404},
		{"delete missing", "DELETE", "/api/ssh-keys/999", "", 404},
		{"non-numeric patch", "PATCH", "/api/ssh-keys/abc", `{"name":"z"}`, 400},
		{"non-numeric delete", "DELETE", "/api/ssh-keys/abc", "", 400},
		{"bad json post", "POST", "/api/ssh-keys", `nope`, 400},
		{"bad json patch", "PATCH", "/api/ssh-keys/" + itoa(o.ID), `nope`, 400},
	}
	for _, c := range cases {
		if rec := do(mux, c.method, c.path, c.body); rec.Code != c.want {
			t.Errorf("%s: status = %d, want %d (%s)", c.name, rec.Code, c.want, rec.Body)
		}
	}
	rec := do(mux, "POST", "/api/ssh-keys", `{"name":"ci"}`)
	var e map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	if e["error"] != "An SSH key with this name already exists" {
		t.Errorf("error = %q", e["error"])
	}
}

func itoa(id int64) string { return strconv.FormatInt(id, 10) }
