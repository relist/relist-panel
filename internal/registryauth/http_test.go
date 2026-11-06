package registryauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func itoa(id int64) string { return strconv.FormatInt(id, 10) }

type fakeTester struct {
	err  error
	seen []Auth
}

func (f *fakeTester) Login(_ context.Context, a Auth) error {
	f.seen = append(f.seen, a)
	return f.err
}

func newMux(t *testing.T, tester Tester) (*http.ServeMux, *Store) {
	t.Helper()
	store := newStore(t)
	mux := http.NewServeMux()
	New(store, tester, func() time.Time { return t0 }).RegisterRoutes(mux)
	return mux, store
}

func do(mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

func TestListNeverReturnsSecret(t *testing.T) {
	mux, _ := newMux(t, &fakeTester{})
	if rec := do(mux, "POST", "/api/registry/external", `{"host":"https://GHCR.io/","username":"ci","secret":"s3cret"}`); rec.Code != 201 {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	} else if strings.Contains(rec.Body.String(), "s3cret") {
		t.Fatalf("create answered the secret: %s", rec.Body)
	}
	rec := do(mux, "GET", "/api/registry/external", "")
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "s3cret") || strings.Contains(rec.Body.String(), `"secret"`) {
		t.Fatalf("list = %d %s", rec.Code, rec.Body)
	}
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) != 1 || list[0]["host"] != "ghcr.io" || list[0]["username"] != "ci" {
		t.Fatalf("list = %v, %v", list, err)
	}
}

func TestCreateRejectsBadHost(t *testing.T) {
	mux, _ := newMux(t, &fakeTester{})
	for _, body := range []string{`{"host":"a b","username":"u","secret":"s"}`, `{"host":"ghcr.io","username":"","secret":"s"}`, `{"host":"ghcr.io","username":"u","secret":""}`} {
		if rec := do(mux, "POST", "/api/registry/external", body); rec.Code != 422 {
			t.Errorf("%s = %d %s, want 422", body, rec.Code, rec.Body)
		}
	}
}

func TestCreateDuplicate(t *testing.T) {
	mux, _ := newMux(t, &fakeTester{})
	do(mux, "POST", "/api/registry/external", `{"host":"ghcr.io","username":"u","secret":"s"}`)
	if rec := do(mux, "POST", "/api/registry/external", `{"host":"GHCR.IO","username":"v","secret":"t"}`); rec.Code != 409 {
		t.Fatalf("duplicate = %d %s, want 409", rec.Code, rec.Body)
	}
}

func TestUpdateKeepsSecretWhenEmpty(t *testing.T) {
	mux, store := newMux(t, &fakeTester{})
	do(mux, "POST", "/api/registry/external", `{"host":"ghcr.io","username":"u","secret":"s"}`)
	list, _ := store.List(context.Background())
	path := "/api/registry/external/" + itoa(list[0].ID)
	if rec := do(mux, "PUT", path, `{"host":"ghcr.io","username":"v","secret":""}`); rec.Code != 200 {
		t.Fatalf("update = %d %s", rec.Code, rec.Body)
	}
	got, _ := store.Get(context.Background(), list[0].ID)
	if got.Username != "v" || got.Secret != "s" {
		t.Fatalf("got %+v", got)
	}
	if rec := do(mux, "PUT", "/api/registry/external/999", `{"host":"ghcr.io","username":"v","secret":""}`); rec.Code != 404 {
		t.Fatalf("missing = %d", rec.Code)
	}
	if rec := do(mux, "DELETE", path, ""); rec.Code != 204 {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body)
	}
}

func TestTestRecordsResult(t *testing.T) {
	tester := &fakeTester{err: errors.New("unauthorized: incorrect username or password")}
	mux, store := newMux(t, tester)
	do(mux, "POST", "/api/registry/external", `{"host":"ghcr.io","username":"u","secret":"s"}`)
	list, _ := store.List(context.Background())
	rec := do(mux, "POST", "/api/registry/external/"+itoa(list[0].ID)+"/test", "")
	var got struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.OK || !strings.Contains(got.Error, "unauthorized") {
		t.Fatalf("test = %d %s", rec.Code, rec.Body)
	}
	if len(tester.seen) != 1 || tester.seen[0] != (Auth{Host: "ghcr.io", Username: "u", Secret: "s"}) {
		t.Fatalf("tester saw %+v", tester.seen)
	}
	stored, _ := store.Get(context.Background(), list[0].ID)
	if stored.LastTestOK || stored.LastTestAt == nil || !strings.Contains(stored.LastTestError, "unauthorized") {
		t.Fatalf("stored %+v", stored)
	}
	listed := do(mux, "GET", "/api/registry/external", "").Body.String()
	if !strings.Contains(listed, `"lastTestOk":false`) || !strings.Contains(listed, "unauthorized") {
		t.Fatalf("list = %s", listed)
	}
	tester.err = nil
	do(mux, "POST", "/api/registry/external/"+itoa(list[0].ID)+"/test", "")
	if stored, _ = store.Get(context.Background(), list[0].ID); !stored.LastTestOK || stored.LastTestError != "" {
		t.Fatalf("after pass %+v", stored)
	}
}

func TestUpdateHostNeedsSecretAgain(t *testing.T) {
	mux, store := newMux(t, &fakeTester{})
	do(mux, "POST", "/api/registry/external", `{"host":"ghcr.io","username":"u","secret":"s"}`)
	list, _ := store.List(context.Background())
	path := "/api/registry/external/" + itoa(list[0].ID)
	if rec := do(mux, "PUT", path, `{"host":"registry.example.com","username":"u","secret":""}`); rec.Code != 422 ||
		!strings.Contains(rec.Body.String(), "enter the token or password again when changing the host") {
		t.Fatalf("host change, blank secret = %d %s", rec.Code, rec.Body)
	}
	if got, _ := store.Get(context.Background(), list[0].ID); got.Host != "ghcr.io" {
		t.Fatalf("host = %q after a refused edit", got.Host)
	}
	if rec := do(mux, "PUT", path, `{"host":"registry.example.com","username":"u","secret":"new"}`); rec.Code != 200 {
		t.Fatalf("host change with secret = %d %s", rec.Code, rec.Body)
	}
}

func TestUpdateClearsLastTestWhenLoginChanges(t *testing.T) {
	mux, store := newMux(t, &fakeTester{})
	ctx := context.Background()
	do(mux, "POST", "/api/registry/external", `{"host":"ghcr.io","username":"u","secret":"s"}`)
	list, _ := store.List(ctx)
	id := list[0].ID
	path := "/api/registry/external/" + itoa(id)
	for _, body := range []string{
		`{"host":"ghcr.io","username":"v","secret":""}`,
		`{"host":"ghcr.io","username":"v","secret":"new"}`,
	} {
		if err := store.SetTestResult(ctx, id, false, "denied", t0); err != nil {
			t.Fatal(err)
		}
		if rec := do(mux, "PUT", path, body); rec.Code != 200 {
			t.Fatalf("%s = %d %s", body, rec.Code, rec.Body)
		}
		if got, _ := store.Get(ctx, id); got.LastTestAt != nil || got.LastTestError != "" {
			t.Fatalf("%s left last test %+v", body, got)
		}
	}
	if err := store.SetTestResult(ctx, id, true, "", t0); err != nil {
		t.Fatal(err)
	}
	do(mux, "PUT", path, `{"host":"ghcr.io","username":"v","secret":""}`)
	if got, _ := store.Get(ctx, id); got.LastTestAt == nil || !got.LastTestOK {
		t.Fatalf("unchanged login lost its result: %+v", got)
	}
}
