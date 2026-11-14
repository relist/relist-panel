package databases

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"relist-panel/internal/audit"
)

type apiRig struct {
	*rig
	h      http.Handler
	mu     sync.Mutex
	events []audit.Event
}

func newAPIRig(t *testing.T) *apiRig {
	t.Helper()
	a := &apiRig{rig: newRig(t)}
	mux := http.NewServeMux()
	NewAPI(a.svc).RegisterRoutes(mux)
	a.h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, _ := audit.Begin(r.Context(), audit.Event{}, nil, func(_ context.Context, e audit.Event) error {
			a.mu.Lock()
			defer a.mu.Unlock()
			a.events = append(a.events, e)
			return nil
		})
		mux.ServeHTTP(w, r.WithContext(ctx))
	})
	return a
}

func (a *apiRig) do(method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	a.h.ServeHTTP(w, req)
	a.svc.Wait()
	return w
}

func TestCreateAndList(t *testing.T) {
	a := newAPIRig(t)
	w := a.do("POST", "/api/databases", `{"name":"Shop","engine":"postgres","version":"18"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	w = a.do("GET", "/api/databases", "")
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	if strings.Contains(strings.ToLower(w.Body.String()), "password") {
		t.Fatalf("list leaks a password key: %s", w.Body)
	}
	var list []DatabaseJSON
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list) != 1 {
		t.Fatalf("list = %s, %v", w.Body, err)
	}
	got := list[0]
	if got.Host != "relist-db-1" || got.HostReadable != "shop.db.internal" || got.Status != StatusRunning ||
		got.Port != 5432 || got.User != "postgres" || got.DefaultDatabase != "postgres" || got.Image != "postgres:18" {
		t.Fatalf("db = %+v", got)
	}
	if w := a.do("GET", "/api/databases/engines", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"label":"PostgreSQL"`) {
		t.Fatalf("engines = %d %s", w.Code, w.Body)
	}
}

func TestCredentialsAudited(t *testing.T) {
	a := newAPIRig(t)
	a.do("POST", "/api/databases", `{"name":"Shop","engine":"postgres","version":"18"}`)
	d, _ := a.store.Get(context.Background(), 1)
	w := a.do("GET", "/api/databases/1/credentials", "")
	if w.Code != 200 {
		t.Fatalf("credentials = %d %s", w.Code, w.Body)
	}
	var got map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["password"] != d.Password {
		t.Fatalf("password = %q", got["password"])
	}
	u, err := url.Parse(got["url"])
	if err != nil {
		t.Fatal(err)
	}
	if pw, _ := u.User.Password(); pw != d.Password || u.Hostname() != "relist-db-1" {
		t.Fatalf("url = %s", got["url"])
	}
	if !strings.Contains(got["urlReadable"], "shop.db.internal") || got["env"] != "DATABASE_URL="+got["url"] {
		t.Fatalf("readable/env = %v", got)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	found := false
	for _, e := range a.events {
		if e.Action == "secret.read" {
			found = slices.Equal(e.Detail["keys"].([]string), []string{"password"})
		}
	}
	if !found {
		t.Fatalf("events = %+v", a.events)
	}
}

func TestDeleteWrongConfirm400(t *testing.T) {
	a := newAPIRig(t)
	a.do("POST", "/api/databases", `{"name":"Shop","engine":"redis","version":"8"}`)
	if w := a.do("DELETE", "/api/databases/1", `{"confirm":"nope"}`); w.Code != 400 {
		t.Fatalf("code = %d", w.Code)
	}
	if w := a.do("DELETE", "/api/databases/1", `{"confirm":"Shop"}`); w.Code != 204 {
		t.Fatalf("code = %d %s", w.Code, w.Body)
	}
	if w := a.do("GET", "/api/databases/1", ""); w.Code != 404 {
		t.Fatalf("after delete = %d", w.Code)
	}
}

func TestUnknownVersion400(t *testing.T) {
	a := newAPIRig(t)
	if w := a.do("POST", "/api/databases", `{"name":"A","engine":"postgres","version":"9"}`); w.Code != 400 {
		t.Fatalf("code = %d", w.Code)
	}
	if w := a.do("POST", "/api/databases", `{"name":"A","engine":"oracle","version":"1"}`); w.Code != 400 {
		t.Fatalf("code = %d", w.Code)
	}
}

func TestNegativeLimits400(t *testing.T) {
	a := newAPIRig(t)
	if w := a.do("POST", "/api/databases", `{"name":"A","engine":"redis","version":"8","memoryMb":-1}`); w.Code != 400 {
		t.Fatalf("code = %d %s", w.Code, w.Body)
	}
}

func TestDuplicateName409(t *testing.T) {
	a := newAPIRig(t)
	a.do("POST", "/api/databases", `{"name":"Shop","engine":"redis","version":"8"}`)
	if w := a.do("POST", "/api/databases", `{"name":"shop","engine":"redis","version":"8"}`); w.Code != 409 {
		t.Fatalf("code = %d %s", w.Code, w.Body)
	}
}

func TestMissing404(t *testing.T) {
	a := newAPIRig(t)
	for _, c := range [][2]string{{"GET", "/api/databases/9"}, {"POST", "/api/databases/9/start"}, {"GET", "/api/databases/9/credentials"}, {"GET", "/api/databases/x"}} {
		if w := a.do(c[0], c[1], ""); w.Code != 404 {
			t.Errorf("%v = %d", c, w.Code)
		}
	}
}

func TestActionsAndRename(t *testing.T) {
	a := newAPIRig(t)
	a.do("POST", "/api/databases", `{"name":"Shop","engine":"redis","version":"8"}`)
	for _, act := range []string{"stop", "start", "restart", "repair", "update-image"} {
		if w := a.do("POST", "/api/databases/1/"+act, ""); w.Code != 204 {
			t.Errorf("%s = %d %s", act, w.Code, w.Body)
		}
	}
	if w := a.do("PATCH", "/api/databases/1", `{"name":"Blog"}`); w.Code != 204 {
		t.Fatalf("rename = %d %s", w.Code, w.Body)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, e := range a.events {
		if e.Target == "" {
			t.Fatalf("event without target: %+v", e)
		}
	}
}

func TestLogsTailBounds(t *testing.T) {
	a := newAPIRig(t)
	a.do("POST", "/api/databases", `{"name":"Shop","engine":"redis","version":"8"}`)
	a.host.logs = "hello"
	for _, q := range []string{"tail=0", "tail=5000", "tail=abc"} {
		if w := a.do("GET", "/api/databases/1/logs?"+q, ""); w.Code != 400 {
			t.Errorf("%s = %d", q, w.Code)
		}
	}
	w := a.do("GET", "/api/databases/1/logs", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "hello") || !a.host.saw("logs relist-db-1 200") {
		t.Fatalf("logs = %d %s calls=%v", w.Code, w.Body, a.host.all())
	}
}

func TestLockedIs409(t *testing.T) {
	a := newAPIRig(t)
	a.locked = true
	if w := a.do("POST", "/api/databases", `{"name":"A","engine":"redis","version":"8"}`); w.Code != 409 {
		t.Fatalf("code = %d", w.Code)
	}
}
