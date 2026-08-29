// Package httpapi wires the panel's HTTP routes together.
package httpapi

import (
	"encoding/json"
	"log"
	"net/http"

	"relist-panel/internal/webassets"
)

// NewHandler builds the top-level HTTP handler for the panel.
func NewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)
	mux.Handle("/", webassets.Handler())
	return mux
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
		log.Printf("write health response: %v", err)
	}
}
