package main

import (
	"net/http"

	"relist-panel/internal/migration"
)

// serveControl serves mux on the control socket at path (ADR-0019). An error
// leaves the panel without the socket, never without its web server: the
// caller logs it and goes on.
func serveControl(path string, mux http.Handler) (*http.Server, error) {
	l, err := migration.ListenControl(path)
	if err != nil {
		return nil, err
	}
	srv := &http.Server{Handler: mux}
	go srv.Serve(l)
	return srv, nil
}
