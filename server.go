package main

import (
	"net/http"
	"time"
)

// newServer is the panel's HTTP server. ReadHeaderTimeout drops a connection
// that never finishes its request line and headers, and IdleTimeout reaps idle
// keep-alives. There is deliberately no ReadTimeout or WriteTimeout: image
// uploads, archive downloads, log streams and the terminal websocket all run
// for as long as the transfer does.
func newServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
}
