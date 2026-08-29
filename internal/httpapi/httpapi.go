// Package httpapi wires the panel's HTTP routes together.
package httpapi

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"

	"relist-panel/internal/auth"
	"relist-panel/internal/environments"
	"relist-panel/internal/projects"
	"relist-panel/internal/webassets"
)

// NewHandler builds the top-level HTTP handler for the panel.
func NewHandler(db *sql.DB) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)
	authService := auth.New(db)
	authService.RegisterRoutes(mux)
	projectsService := projects.New(db)
	projectsService.RegisterRoutes(mux)
	environmentsService := environments.New(db)
	environmentsService.RegisterRoutes(mux)
	mux.Handle("/", webassets.Handler())
	return authService.Middleware(mux)
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
		log.Printf("write health response: %v", err)
	}
}
