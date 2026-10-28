package certificates

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"relist-panel/internal/database"
)

type testService struct {
	mux     *http.ServeMux
	store   *Store
	dataDir string
}

func newService(t *testing.T) testService {
	t.Helper()
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	dataDir := t.TempDir()
	s := New(db, dataDir)
	s.now = func() time.Time { return testNow }
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	return testService{mux: mux, store: s.store, dataDir: dataDir}
}

func (ts testService) do(method, path string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	if body == nil {
		raw = nil
	}
	req := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
	rec := httptest.NewRecorder()
	ts.mux.ServeHTTP(rec, req)
	return rec
}

func pairBody(t *testing.T, name string, sans ...string) map[string]string {
	p := newPair(t, pairOptions{sans: sans})
	return map[string]string{"name": name, "certificate": p.cert, "privateKey": p.key}
}

func assertNoPEM(t *testing.T, body string) {
	t.Helper()
	if strings.Contains(body, "PRIVATE KEY") || strings.Contains(body, "BEGIN CERTIFICATE") {
		t.Errorf("response carries PEM: %s", body)
	}
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	return out
}

func (ts testService) file(id float64) string {
	return filepath.Join(DynamicDir(ts.dataDir), fmt.Sprintf("cert-%d.yml", int64(id)))
}

func TestCreateReturnsCertificateWithoutKey(t *testing.T) {
	ts := newService(t)
	rec := ts.do("POST", "/api/certificates", pairBody(t, "shop", "shop.example.com"))
	if rec.Code != 201 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	assertNoPEM(t, rec.Body.String())
	got := decode(t, rec)
	if got["name"] != "shop" || got["issuer"] != "leaf" || got["notAfter"] == "" {
		t.Errorf("body = %v", got)
	}
	if sans, _ := got["sans"].([]any); len(sans) != 1 || sans[0] != "shop.example.com" {
		t.Errorf("sans = %v", got["sans"])
	}
	if _, err := os.Stat(ts.file(got["id"].(float64))); err != nil {
		t.Errorf("Traefik file: %v", err)
	}
}

func TestCreateFieldErrors(t *testing.T) {
	ts := newService(t)
	if rec := ts.do("POST", "/api/certificates", pairBody(t, "taken", "x.example.com")); rec.Code != 201 {
		t.Fatal(rec.Body)
	}
	mismatched := pairBody(t, "mismatch", "x.example.com")
	mismatched["privateKey"] = pairBody(t, "other", "x.example.com")["privateKey"]
	cases := []struct {
		name   string
		body   map[string]string
		status int
		field  string
	}{
		{"bad name", pairBody(t, "my cert", "x.example.com"), 400, "name"},
		{"mismatched key", mismatched, 400, "privateKey"},
		{"duplicate name", pairBody(t, "taken", "x.example.com"), 409, "name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := ts.do("POST", "/api/certificates", tc.body)
			if rec.Code != tc.status || decode(t, rec)["field"] != tc.field {
				t.Errorf("got %d %s, want %d on %s", rec.Code, rec.Body, tc.status, tc.field)
			}
		})
	}
}

func TestListOmitsKeys(t *testing.T) {
	ts := newService(t)
	ts.do("POST", "/api/certificates", pairBody(t, "b", "b.example.com"))
	ts.do("POST", "/api/certificates", pairBody(t, "a", "a.example.com"))
	rec := ts.do("GET", "/api/certificates", nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	assertNoPEM(t, rec.Body.String())
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) != 2 || list[0]["name"] != "a" {
		t.Errorf("list = %s", rec.Body)
	}
}

func TestReplace(t *testing.T) {
	ts := newService(t)
	created := decode(t, ts.do("POST", "/api/certificates", pairBody(t, "shop", "old.example.com")))
	id := created["id"].(float64)
	body := pairBody(t, "renamed", "new.example.com")
	rec := ts.do("PUT", fmt.Sprintf("/api/certificates/%d", int64(id)), body)
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	assertNoPEM(t, rec.Body.String())
	got := decode(t, rec)
	if got["name"] != "shop" {
		t.Errorf("name = %v, want the name to be ignored", got["name"])
	}
	if sans, _ := got["sans"].([]any); len(sans) != 1 || sans[0] != "new.example.com" {
		t.Errorf("sans = %v", got["sans"])
	}
	raw, _ := os.ReadFile(ts.file(id))
	firstLine := strings.Split(strings.TrimSpace(body["certificate"]), "\n")[1]
	if !strings.Contains(string(raw), firstLine) {
		t.Error("Traefik file still holds the old certificate")
	}
	if rec := ts.do("PUT", "/api/certificates/999", body); rec.Code != 404 {
		t.Errorf("unknown id: %d", rec.Code)
	}
}

func TestDelete(t *testing.T) {
	ts := newService(t)
	created := decode(t, ts.do("POST", "/api/certificates", pairBody(t, "shop", "shop.example.com")))
	id := created["id"].(float64)
	if rec := ts.do("DELETE", fmt.Sprintf("/api/certificates/%d", int64(id)), nil); rec.Code != 204 {
		t.Fatalf("status = %d", rec.Code)
	}
	if _, err := os.Stat(ts.file(id)); !os.IsNotExist(err) {
		t.Errorf("Traefik file still there: %v", err)
	}
	if rec := ts.do("DELETE", fmt.Sprintf("/api/certificates/%d", int64(id)), nil); rec.Code != 404 {
		t.Errorf("unknown id: %d", rec.Code)
	}
}

func TestReplaceWhenFileWriteFails(t *testing.T) {
	ts := newService(t)
	created := decode(t, ts.do("POST", "/api/certificates", pairBody(t, "shop", "old.example.com")))
	id := created["id"].(float64)

	dir := DynamicDir(ts.dataDir)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	body := pairBody(t, "", "new.example.com")
	if rec := ts.do("PUT", fmt.Sprintf("/api/certificates/%d", int64(id)), body); rec.Code != 500 {
		t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body)
	}

	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := Reconcile(context.Background(), ts.store, ts.dataDir); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(ts.file(id))
	if !strings.Contains(string(raw), strings.Split(strings.TrimSpace(body["certificate"]), "\n")[1]) {
		t.Error("Reconcile did not write the replaced certificate")
	}
}

// A Reconcile that listed before another write landed must not then remove
// that write's file: Reconciles run one at a time.
func TestReconcileDoesNotDropAConcurrentWrite(t *testing.T) {
	ts := newService(t)
	ctx := context.Background()
	late := make(chan error, 1)
	var paused atomic.Bool
	reconcileListed = func() {
		if paused.Swap(true) {
			return
		}
		func() {
			go func() {
				if _, err := ts.store.Create(ctx, "late", parsed(t, "late.example.com")); err != nil {
					late <- err
					return
				}
				late <- Reconcile(ctx, ts.store, ts.dataDir)
			}()
			// Without the lock the late write finishes here, before this
			// Reconcile writes its stale list; with it, it waits.
			time.Sleep(200 * time.Millisecond)
		}()
	}

	if err := Reconcile(ctx, ts.store, ts.dataDir); err != nil {
		t.Fatal(err)
	}
	if err := <-late; err != nil {
		t.Fatal(err)
	}
	reconcileListed = func() {}
	entries, _ := os.ReadDir(DynamicDir(ts.dataDir))
	if len(entries) != 1 {
		t.Fatalf("%d files, want the late certificate's file", len(entries))
	}
}
