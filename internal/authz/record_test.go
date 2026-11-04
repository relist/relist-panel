package authz

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
