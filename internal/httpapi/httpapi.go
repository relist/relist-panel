// Package httpapi wires the panel's HTTP routes together.
package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"path/filepath"
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
	"relist-panel/internal/migration"
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
// migrationOptions, when non-nil, adds a server Migration's routes and its
// lock middleware (ADR-0018) and resumes an interrupted lock in the
// background.
// The optional dockerStores argument adds host reports and confirmed cleanup.
// The first store is used; omission keeps those routes unregistered.
//
// The Deployment worker runs until ctx is done. The returned wait blocks until
// it has returned, which is after a Deploy step in progress has finished.
func NewHandler(ctx context.Context, db *sql.DB, runner composes.ComposeRunner, dataDir string, history metrics.HostHistory, services metrics.ServiceFacts, volumeRunner volumes.Runner, volumeResolver *volumes.Resolver, terminalOptions *terminal.Options, imageStore images.Images, migrationOptions *migration.Options, dockerStores ...dockerhost.Engine) (http.Handler, func()) {
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
	var inner http.Handler = gate.middleware(mux)
	if migrationOptions != nil {
		source := migration.NewSource(migration.SourceDeps{
			Store:           migration.NewStore(db, nil),
			Builder:         migration.Builder{DB: db, Composes: composesService, Volumes: volumeResolver, Docker: migrationOptions.Docker, DataDir: dataDir},
			Queue:           queue,
			Docker:          migrationOptions.Docker,
			Status:          runner,
			Composes:        composesService,
			Domains:         domains.NewStore(db),
			DB:              db,
			DataDir:         dataDir,
			ReportedDataDir: migrationOptions.ReportedDataDir,
			Install:         migrationOptions.Install,
		})
		source.RegisterRoutes(mux)
		local := migration.LocalHost{DB: db, Docker: migrationOptions.Docker, DataDir: dataDir, Install: migrationOptions.Install}
		stateDir := filepath.Join(dataDir, ".migration")
		target := migration.NewTarget(migration.TargetDeps{
			Local:    local,
			HTTP:     migrationOptions.HTTP,
			Insecure: migrationOptions.Insecure,
			StateDir: stateDir,
			Restore:  migration.NewRestorer(local, filepath.Join(stateDir, "staging")),
			Finalizer: migration.NewFinalizer(migration.FinalizeDeps{
				Local: local, Exit: migrationOptions.Exit, Queue: queue, Sources: sources.NewStore(db), Domains: domains.NewStore(db),
			}),
			Backoff: migrationOptions.Backoff,
			Context: ctx,
		})
		target.RegisterRoutes(mux, func(r *http.Request) (bool, error) {
			exists, err := authService.AdminExists(r)
			if err != nil || !exists {
				return !exists, err
			}
			return authService.Authenticated(r)
		})
		authService.SetSetupBlocker(target.InProgress)
		authService.AllowSelfAuthenticated(migration.SessionPath)
		inner = source.Middleware(inner)
		worker.Add(1)
		go func() {
			defer worker.Done()
			if err := source.ResumeLock(ctx); err != nil && ctx.Err() == nil {
				log.Printf("migration: resume the lock: %v", err)
			}
		}()
		if err := target.ResumeIfPending(ctx); err != nil {
			log.Printf("migration: resume the pull: %v", err)
		}
	}
	mux.Handle("/", webassets.Handler())
	return authService.Middleware(inner), worker.Wait
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
		log.Printf("write health response: %v", err)
	}
}
