package migrationctl

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// serve runs h on a Unix socket in a temporary directory and returns a client
// for it.
func serve(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	path := filepath.Join(t.TempDir(), "control.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(h)
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	return New(path)
}

func TestClientDecodesProgress(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/migration/pull/progress" {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		w.Write([]byte(`{"phase":"pulling","sourceUrl":"https://old.example.com","items":[{"id":"volume-a","size":10,"done":4,"state":"running"}]}`))
	})
	p, err := c.Progress(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p.Phase != "pulling" || p.SourceURL != "https://old.example.com" || len(p.Items) != 1 || p.Items[0].Done != 4 {
		t.Fatalf("progress = %+v", p)
	}
}

func TestClientReturnsAPIError(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"error":"busy"}`))
	})
	err := c.Start(context.Background(), []string{"/srv"})
	var api *APIError
	if !errors.As(err, &api) || api.Status != http.StatusConflict || api.Message != "busy" {
		t.Fatalf("err = %v, want APIError 409 busy", err)
	}
	if api.Error() != "busy" {
		t.Fatalf("Error() = %q, want the server's message", api.Error())
	}
}

func TestClientNoPanel(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), "missing.sock"))
	if _, err := c.Progress(context.Background()); !errors.Is(err, ErrNoPanel) {
		t.Fatalf("err = %v, want ErrNoPanel", err)
	}
}

func TestClientDropIsNoPanel(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		conn.Close()
	})
	if _, err := c.Progress(context.Background()); !errors.Is(err, ErrNoPanel) {
		t.Fatalf("err = %v, want ErrNoPanel", err)
	}
}

func TestClientResumeWarning(t *testing.T) {
	warn := true
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/migration/resume" {
			t.Errorf("path %s", r.URL.Path)
		}
		if warn {
			w.Write([]byte(`{"warning":"x"}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	if got, err := c.Resume(context.Background()); err != nil || got != "x" {
		t.Fatalf("Resume = %q, %v; want the warning", got, err)
	}
	warn = false
	if got, err := c.Resume(context.Background()); err != nil || got != "" {
		t.Fatalf("Resume = %q, %v; want no warning", got, err)
	}
}

func TestClientImplementsController(t *testing.T) {
	var _ Controller = New("x")
}
