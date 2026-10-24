package main

import (
	"net/http"
	"testing"
	"time"
)

func TestNewServerHasHeaderAndIdleTimeouts(t *testing.T) {
	h := http.NewServeMux()
	srv := newServer(":8080", h)

	if srv.Addr != ":8080" || srv.Handler != h {
		t.Errorf("Addr, Handler = %q, %v; want :8080 and the given handler", srv.Addr, srv.Handler)
	}
	if srv.ReadHeaderTimeout != 10*time.Second {
		t.Errorf("ReadHeaderTimeout = %v, want 10s", srv.ReadHeaderTimeout)
	}
	if srv.IdleTimeout != 2*time.Minute {
		t.Errorf("IdleTimeout = %v, want 2m", srv.IdleTimeout)
	}
	// Archive downloads, log streams and the terminal websocket outlive any
	// fixed whole-request limit.
	if srv.ReadTimeout != 0 || srv.WriteTimeout != 0 {
		t.Errorf("ReadTimeout, WriteTimeout = %v, %v; want none", srv.ReadTimeout, srv.WriteTimeout)
	}
}
