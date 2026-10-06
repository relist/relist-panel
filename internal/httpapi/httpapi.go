// Package httpapi wires the panel's HTTP routes together.
package httpapi

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"

	"relist-panel/internal/auth"
	"relist-panel/internal/composes"
	"relist-panel/internal/environments"
	"relist-panel/internal/environmentvariables"
	"relist-panel/internal/images"
	"relist-panel/internal/metrics"
	"relist-panel/internal/projects"
	"relist-panel/internal/terminal"
	"relist-panel/internal/volumeapi"
	"relist-panel/internal/volumes"
	"relist-panel/internal/webassets"
)

// NewHandler builds the top-level HTTP handler for the panel. runner drives
// Compose deploy lifecycle actions (ADR-0003); dataDir is the root directory
// Compose files are resolved under (ADR-0002). history and services are the
// running sampler's read sides (metrics.HostHistory and ServiceFacts —
// *hostmetrics.Sampler and *hostmetrics.ServiceCollector in production,
// fakes at the HTTP seam); they may be nil, in which case the metrics
// endpoints stay unregistered (an unwired panel has nothing to serve).
// volumeRunner and volumeResolver are the Volume Explorer's operation seam
// and its read side (ADR-0010). terminalOptions, when non-nil, is the terminal
// seam and settings; nil leaves the terminal routes unregistered. imageStore
// is the Images page's Docker seam; nil leaves the image routes unregistered.
func NewHandler(db *sql.DB, runner composes.ComposeRunner, dataDir string, history metrics.HostHistory, services metrics.ServiceFacts, volumeRunner volumes.Runner, volumeResolver *volumes.Resolver, terminalOptions *terminal.Options, imageStore images.Images) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)
	authService := auth.New(db)
	authService.RegisterRoutes(mux)
	composesService := composes.New(db, runner, dataDir)
	projectsService := projects.New(db, composesService)
	projectsService.RegisterRoutes(mux)
	environmentsService := environments.New(db, composesService)
	environmentsService.RegisterRoutes(mux)
	environmentVariablesService := environmentvariables.New(db)
	environmentVariablesService.RegisterRoutes(mux)
	composesService.RegisterRoutes(mux)
	volumeapi.New(composesService, volumeResolver, volumeRunner).RegisterRoutes(mux)
	if terminalOptions != nil {
		terminal.New(composesService, *terminalOptions).RegisterRoutes(mux)
	}
	if imageStore != nil {
		images.New(imageStore).RegisterRoutes(mux)
	}
	if history != nil && services != nil {
		metricsService := metrics.New(metrics.NewStore(db), history, services)
		metricsService.RegisterRoutes(mux)
	}
	mux.Handle("/", webassets.Handler())
	return authService.Middleware(newCoordinator().middleware(mux))
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
		log.Printf("write health response: %v", err)
	}
}
