// Package httpapi wires the panel's HTTP routes together.
package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"relist-panel/internal/auth"
	"relist-panel/internal/composes"
	"relist-panel/internal/composevariables"
	"relist-panel/internal/deployments"
	"relist-panel/internal/dockerhost"
	"relist-panel/internal/domains"
	"relist-panel/internal/environments"
	"relist-panel/internal/environmentvariables"
	"relist-panel/internal/images"
	"relist-panel/internal/metrics"
	"relist-panel/internal/projects"
	"relist-panel/internal/sources"
	"relist-panel/internal/terminal"
	"relist-panel/internal/variablesets"
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
// The optional dockerStores argument adds host reports and confirmed cleanup.
// The first store is used; omission keeps those routes unregistered.
//
// The Deployment worker runs until ctx is done. The returned wait blocks until
// it has returned, which is after a Deploy step in progress has finished.
func NewHandler(ctx context.Context, db *sql.DB, runner composes.ComposeRunner, dataDir string, history metrics.HostHistory, services metrics.ServiceFacts, volumeRunner volumes.Runner, volumeResolver *volumes.Resolver, terminalOptions *terminal.Options, imageStore images.Images, dockerStores ...dockerhost.Engine) (http.Handler, func()) {
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
	gate := newCoordinator()
	hostGuard := dockerhost.NewGuard()
	gate.host = hostGuard
	queue := deployments.NewQueue(deployments.Deps{
		Maintenance: hostGuard,
		Store:       deployments.NewStore(db),
		Sources:     sources.NewStore(db),
		Composes:    composesService,
		Fetcher:     sources.Git{},
		Builder:     runner,
		Gate:        gate,
		Timeout:     60 * time.Minute,
		Keep:        20,
	})
	composesService.SetDeploymentCanceler(queue)
	deployments.NewAPI(queue, composesService).RegisterRoutes(mux)
	var worker sync.WaitGroup
	worker.Add(1)
	go func() {
		defer worker.Done()
		queue.Run(ctx)
	}()
	sources.New(db, composesService, sources.Git{}, queue, gate).RegisterRoutes(mux)
	domains.New(db).RegisterRoutes(mux)
	composevariables.New(db).RegisterRoutes(mux)
	variablesets.New(db).RegisterRoutes(mux)
	volumeapi.New(composesService, volumeResolver, volumeRunner).RegisterRoutes(mux)
	if terminalOptions != nil {
		terminal.New(composesService, *terminalOptions).RegisterRoutes(mux)
	}
	if imageStore != nil {
		images.New(imageStore).RegisterRoutes(mux)
	}
	if len(dockerStores) > 0 && dockerStores[0] != nil {
		pending := func(ctx context.Context) (bool, error) {
			var count int
			err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM deployments WHERE status IN ('queued', 'running')").Scan(&count)
			return count > 0, err
		}
		dockerhost.NewAPI(dockerStores[0], hostGuard, pending).RegisterRoutes(mux)
	}
	if history != nil && services != nil {
		metricsService := metrics.New(metrics.NewStore(db), history, services)
		metricsService.RegisterRoutes(mux)
	}
	mux.Handle("/", webassets.Handler())
	return authService.Middleware(gate.middleware(mux)), worker.Wait
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
		log.Printf("write health response: %v", err)
	}
}
