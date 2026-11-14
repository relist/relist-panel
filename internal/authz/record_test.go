package authz

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"relist-panel/internal/audit"
	"relist-panel/internal/auth"
)

type captured struct {
	mu     sync.Mutex
	events []audit.Event
	err    error
}

func (c *captured) record(_ context.Context, e audit.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
	return c.err
}

func (c *captured) take() []audit.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.events
	c.events = nil
	return out
}

// recordingFixture serves handlers through the middleware over the real
// Routes, recording into captured.
func recordingFixture(t *testing.T, register func(mux *http.ServeMux)) (*fixture, *captured) {
	t.Helper()
	f := &fixture{db: testDB(t), mux: http.NewServeMux()}
	register(f.mux)
	f.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	c := &captured{}
	lookup := func(*http.Request) (auth.User, bool, error) { return auth.User{}, false, nil }
	f.handler = newWithRoutes(f.db, lookup, Routes).WithRecorder(c.record).Middleware(f.mux, f.mux)
	return f, c
}

func answer(status int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }
}

func TestEveryActionIsRecorded(t *testing.T) {
	f, c := recordingFixture(t, func(mux *http.ServeMux) {
		for pattern := range Actions {
			mux.HandleFunc(pattern, answer(http.StatusNoContent))
		}
	})
	for pattern, action := range Actions {
		method, path := matrixPath(pattern)
		if got := f.serve(admin, method, path).Code; got != http.StatusNoContent {
			t.Errorf("%s %s = %d", method, path, got)
			continue
		}
		events := c.take()
		if len(events) != 1 {
			t.Errorf("%s: %d events", pattern, len(events))
			continue
		}
		e := events[0]
		scope, _ := scopeOf(pattern, path)
		if e.Action != action || e.Outcome != audit.OutcomeOK || e.Actor != "owner@example.com" || e.ActorUserID != 1 ||
			e.ProjectID != scope.ProjectID || e.EnvironmentID != scope.EnvironmentID || e.ComposeID != scope.ComposeID ||
			e.Target == "" || e.IP != "192.0.2.1" || e.At.IsZero() {
			t.Errorf("%s: event %+v", pattern, e)
		}
	}
}

func TestNonSuccessIsNotRecorded(t *testing.T) {
	f, c := recordingFixture(t, func(mux *http.ServeMux) {
		mux.HandleFunc("PUT "+compose, answer(http.StatusBadRequest))
	})
	if got := f.serve(admin, http.MethodPut, composePath(pA, eA1, cC, "")).Code; got != http.StatusBadRequest {
		t.Fatalf("status = %d", got)
	}
	if events := c.take(); len(events) != 0 {
		t.Fatalf("events = %+v", events)
	}
}

func TestRefusalIsRecordedDenied(t *testing.T) {
	f, c := recordingFixture(t, func(mux *http.ServeMux) {
		mux.HandleFunc("PUT "+compose, answer(http.StatusNoContent))
		mux.HandleFunc("GET "+compose, answer(http.StatusNoContent))
		mux.HandleFunc("POST /api/users", answer(http.StatusCreated))
	})
	grant(t, f.db, pA, eA1, "viewer")
	if got := f.serve(member, http.MethodPut, composePath(pA, eA1, cC, "")).Code; got != http.StatusForbidden {
		t.Fatalf("viewer PUT = %d", got)
	}
	if got := f.serve(member, http.MethodPost, "/api/users").Code; got != http.StatusForbidden {
		t.Fatalf("member POST users = %d", got)
	}
	events := c.take()
	if len(events) != 2 || events[0].Action != "compose.update" || events[0].Outcome != audit.OutcomeDenied ||
		events[0].Actor != "member@example.com" || events[0].Target != "a / a1 / c" ||
		events[1].Action != "user.create" || events[1].Outcome != audit.OutcomeDenied {
		t.Fatalf("events = %+v", events)
	}
	// A refused plain GET is not an event.
	if got := f.serve(member, http.MethodGet, composePath(pA, eA2, cD, "")).Code; got != http.StatusNotFound {
		t.Fatalf("hidden GET = %d", got)
	}
	if events := c.take(); len(events) != 0 {
		t.Fatalf("GET events = %+v", events)
	}
}

func TestDeleteEventKeepsNames(t *testing.T) {
	var f *fixture
	f, c := recordingFixture(t, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE "+compose, func(w http.ResponseWriter, r *http.Request) {
			if _, err := f.db.ExecContext(r.Context(), "DELETE FROM composes WHERE id = 101"); err != nil {
				t.Error(err)
			}
			w.WriteHeader(http.StatusNoContent)
		})
	})
	if got := f.serve(admin, http.MethodDelete, composePath(pA, eA1, cC, "")).Code; got != http.StatusNoContent {
		t.Fatalf("status = %d", got)
	}
	events := c.take()
	if len(events) != 1 || events[0].Target != "a / a1 / c" || events[0].Action != "compose.delete" {
		t.Fatalf("events = %+v", events)
	}
}

func TestAuditWriteFailureKeepsTheResponse(t *testing.T) {
	f, c := recordingFixture(t, func(mux *http.ServeMux) {
		mux.HandleFunc("PUT "+compose, answer(http.StatusNoContent))
	})
	c.err = errors.New("database is locked")
	if got := f.serve(admin, http.MethodPut, composePath(pA, eA1, cC, "")).Code; got != http.StatusNoContent {
		t.Fatalf("status = %d", got)
	}
}

func TestPlainGetIsNotWrapped(t *testing.T) {
	var sawOriginal bool
	f, c := recordingFixture(t, func(mux *http.ServeMux) {
		mux.HandleFunc("GET "+compose, func(w http.ResponseWriter, r *http.Request) {
			_, sawOriginal = w.(*httptest.ResponseRecorder)
			audit.Emit(r.Context(), "secret.read", map[string]any{"keys": []string{"TOKEN"}})
			w.WriteHeader(http.StatusOK)
		})
	})
	f.serve(admin, http.MethodGet, composePath(pA, eA1, cC, ""))
	if !sawOriginal {
		t.Fatal("a plain GET got a wrapped ResponseWriter")
	}
	events := c.take()
	if len(events) != 1 || events[0].Action != "secret.read" || events[0].Target != "a / a1 / c" || events[0].ComposeID != cC {
		t.Fatalf("events = %+v", events)
	}
}

// A refusal on a forged path must not write the ids or names the path
// claims: parentsMatch never confirmed them, and the event would show a
// hidden Compose's name on a Project the member can read.
func TestForgedRefusalRecordsNoScope(t *testing.T) {
	f, c := recordingFixture(t, func(mux *http.ServeMux) {
		mux.HandleFunc("POST "+compose+"/restart", answer(http.StatusNoContent))
	})
	grant(t, f.db, pA, eA1, "editor")
	path := composePath(pA, eA1, cD, "/restart") // Compose D lives in A2
	if got := f.serve(member, http.MethodPost, path).Code; got != http.StatusNotFound {
		t.Fatalf("forged restart = %d", got)
	}
	events := c.take()
	if len(events) != 1 {
		t.Fatalf("events = %+v", events)
	}
	e := events[0]
	if e.Outcome != audit.OutcomeDenied || e.ProjectID != 0 || e.EnvironmentID != 0 || e.ComposeID != 0 || e.Target != path {
		t.Fatalf("denied event = %+v, want no scope and the path as target", e)
	}
}

const volumeRead = "GET " + compose + "/volumes/read"

// io.Copy into the response must keep the underlying writer's ReadFrom (a
// file sent with sendfile) and still count as a 200.
func TestStatusWriterKeepsReaderFrom(t *testing.T) {
	var isReaderFrom bool
	f, c := recordingFixture(t, func(mux *http.ServeMux) {
		mux.HandleFunc(volumeRead, func(w http.ResponseWriter, r *http.Request) {
			_, isReaderFrom = w.(io.ReaderFrom)
			if _, err := io.Copy(w, strings.NewReader("hello")); err != nil {
				t.Error(err)
			}
		})
	})
	response := f.serve(admin, http.MethodGet, composePath(pA, eA1, cC, "/volumes/read"))
	if !isReaderFrom {
		t.Error("the wrapped writer is not an io.ReaderFrom")
	}
	if response.Code != http.StatusOK || response.Body.String() != "hello" {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	if events := c.take(); len(events) != 1 || events[0].Action != "volume.read" || events[0].Detail["incomplete"] != nil {
		t.Fatalf("events = %+v", events)
	}
}

// brokenWriter fails every Write after the headers, as a client that hung
// up midway through a download.
type brokenWriter struct {
	header http.Header
	status int
}

func (w *brokenWriter) Header() http.Header       { return w.header }
func (w *brokenWriter) WriteHeader(status int)    { w.status = status }
func (w *brokenWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestDownloadCutShortIsMarkedIncomplete(t *testing.T) {
	f, c := recordingFixture(t, func(mux *http.ServeMux) {
		mux.HandleFunc(volumeRead, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("part"))
		})
	})
	request := httptest.NewRequest(http.MethodGet, composePath(pA, eA1, cC, "/volumes/read"), nil)
	request = request.WithContext(auth.WithUser(request.Context(), *admin))
	f.handler.ServeHTTP(&brokenWriter{header: http.Header{}}, request)
	events := c.take()
	if len(events) != 1 || events[0].Outcome != audit.OutcomeOK || events[0].Detail["incomplete"] != true {
		t.Fatalf("events = %+v, want one ok event marked incomplete", events)
	}
}

// HEAD sends no body, so it is not a download.
func TestHeadIsNotRecorded(t *testing.T) {
	f, c := recordingFixture(t, func(mux *http.ServeMux) {
		mux.HandleFunc(volumeRead, answer(http.StatusOK))
	})
	if got := f.serve(admin, http.MethodHead, composePath(pA, eA1, cC, "/volumes/read")).Code; got != http.StatusOK {
		t.Fatalf("HEAD = %d", got)
	}
	if events := c.take(); len(events) != 0 {
		t.Fatalf("events = %+v", events)
	}
}

// A Range request is still a download (skipping it would let a file be read
// piecewise with no event), and the range it asked for is in the detail.
func TestRangeDownloadNamesTheRange(t *testing.T) {
	f, c := recordingFixture(t, func(mux *http.ServeMux) {
		mux.HandleFunc(volumeRead, answer(http.StatusPartialContent))
	})
	request := httptest.NewRequest(http.MethodGet, composePath(pA, eA1, cC, "/volumes/read"), nil)
	request.Header.Set("Range", "bytes=100-199")
	request = request.WithContext(auth.WithUser(request.Context(), *admin))
	f.handler.ServeHTTP(httptest.NewRecorder(), request)
	events := c.take()
	if len(events) != 1 || events[0].Detail["range"] != "bytes=100-199" {
		t.Fatalf("events = %+v", events)
	}
}

// A member who repeats a refused change records at most deniedPerMinute
// denied events a minute for that action.
func TestRepeatedRefusalsAreCapped(t *testing.T) {
	f, c := recordingFixture(t, func(mux *http.ServeMux) {
		mux.HandleFunc("PUT "+compose, answer(http.StatusNoContent))
	})
	grant(t, f.db, pA, eA1, "viewer")
	for range deniedPerMinute + 5 {
		if got := f.serve(member, http.MethodPut, composePath(pA, eA1, cC, "")).Code; got != http.StatusForbidden {
			t.Fatalf("viewer PUT = %d", got)
		}
	}
	if got := len(c.take()); got != deniedPerMinute {
		t.Fatalf("denied events = %d, want %d", got, deniedPerMinute)
	}
}

// An SSH key, Certificate or Builder event names the thing, read before the
// handler, so a delete still says what it deleted.
func TestHostThingEventsKeepTheirName(t *testing.T) {
	var f *fixture
	deleting := func(table string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if _, err := f.db.ExecContext(r.Context(), "DELETE FROM "+table); err != nil {
				t.Error(err)
			}
			w.WriteHeader(http.StatusNoContent)
		}
	}
	f, c := recordingFixture(t, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /api/ssh-keys/{id}", deleting("ssh_keys"))
		mux.HandleFunc("DELETE /api/certificates/{id}", deleting("certificates"))
		mux.HandleFunc("POST /api/builders/{id}/prune", answer(http.StatusNoContent))
		mux.HandleFunc("PUT /api/builders/default", answer(http.StatusNoContent))
	})
	if _, err := f.db.Exec(`
		INSERT INTO ssh_keys (id, name, private_key, public_key, created_at) VALUES (5, 'deploy key', 'x', 'x', 1);
		INSERT INTO certificates (id, name, certificate_pem, private_key_pem, sans, issuer, not_after, created_at, updated_at) VALUES (6, 'wildcard', 'x', 'x', '[]', 'x', 1, 1, 1);
		INSERT INTO builders (id, name, cpus_milli, memory_mb, created_at, updated_at) VALUES (7, 'small', 1000, 1024, 1, 1);
	`); err != nil {
		t.Fatal(err)
	}
	for _, call := range []struct{ method, path, want string }{
		{http.MethodDelete, "/api/ssh-keys/5", "deploy key"},
		{http.MethodDelete, "/api/certificates/6", "wildcard"},
		{http.MethodPost, "/api/builders/7/prune", "small"},
		{http.MethodPut, "/api/builders/default", "/api/builders/default"},
	} {
		if got := f.serve(admin, call.method, call.path).Code; got != http.StatusNoContent {
			t.Fatalf("%s %s = %d", call.method, call.path, got)
		}
		if events := c.take(); len(events) != 1 || events[0].Target != call.want {
			t.Errorf("%s %s: events = %+v, want target %q", call.method, call.path, events, call.want)
		}
	}
}

// A user route of an auth group names the group, and the password reveal is
// an audited read.
func TestAuthGroupUserEventsNameTheGroup(t *testing.T) {
	f, c := recordingFixture(t, func(mux *http.ServeMux) {
		mux.HandleFunc("DELETE /api/auth-groups/{id}/users/{uid}", answer(http.StatusNoContent))
		mux.HandleFunc("GET /api/auth-groups/{id}/users/{uid}/password", answer(http.StatusOK))
	})
	if _, err := f.db.Exec(`INSERT INTO auth_groups (id, name, created_at, updated_at) VALUES (6, 'Staging', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	for _, call := range []struct{ method, path, action string }{
		{http.MethodDelete, "/api/auth-groups/6/users/2", "auth_group_user.delete"},
		{http.MethodGet, "/api/auth-groups/6/users/2/password", "auth_group_user.password_read"},
	} {
		if got := f.serve(admin, call.method, call.path).Code; got >= 300 {
			t.Fatalf("%s %s = %d", call.method, call.path, got)
		}
		events := c.take()
		if len(events) != 1 || events[0].Target != "Staging" || events[0].Action != call.action {
			t.Errorf("%s %s: events = %+v", call.method, call.path, events)
		}
	}
}

// range is recorded only for a partial download (206), and cut short: the
// header is the caller's text.
func TestRangeIsRecordedOnlyOnPartialContent(t *testing.T) {
	f, c := recordingFixture(t, func(mux *http.ServeMux) {
		mux.HandleFunc(volumeRead, answer(http.StatusOK))
		mux.HandleFunc("PUT "+compose, answer(http.StatusNoContent))
		mux.HandleFunc("GET /api/traefik/access-logs/{name}", answer(http.StatusPartialContent))
	})
	serve := func(method, path, byteRange string) audit.Event {
		request := httptest.NewRequest(method, path, nil)
		request.Header.Set("Range", byteRange)
		request = request.WithContext(auth.WithUser(request.Context(), *admin))
		f.handler.ServeHTTP(httptest.NewRecorder(), request)
		events := c.take()
		if len(events) != 1 {
			t.Fatalf("%s %s: events = %+v", method, path, events)
		}
		return events[0]
	}
	if e := serve(http.MethodGet, composePath(pA, eA1, cC, "/volumes/read"), "bytes=0-9"); e.Detail["range"] != nil {
		t.Errorf("full download recorded range: %+v", e.Detail)
	}
	if e := serve(http.MethodPut, composePath(pA, eA1, cC, ""), "bytes=0-9"); e.Detail["range"] != nil {
		t.Errorf("PUT recorded range: %+v", e.Detail)
	}
	long := "bytes=" + strings.Repeat("1", 500)
	if e := serve(http.MethodGet, "/api/traefik/access-logs/a.log", long); len(fmt.Sprint(e.Detail["range"])) != maxRangeDetail {
		t.Errorf("range = %d bytes, want %d", len(fmt.Sprint(e.Detail["range"])), maxRangeDetail)
	}
}
