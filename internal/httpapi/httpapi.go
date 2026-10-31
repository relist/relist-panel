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
	"relist-panel/internal/certificates"
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
	"relist-panel/internal/safely"
	"relist-panel/internal/sources"
	"relist-panel/internal/sshkeys"
	"relist-panel/internal/terminal"
	"relist-panel/internal/variablesets"
	"relist-panel/internal/volumeapi"
	"relist-panel/internal/volumes"
	"relist-panel/internal/vulnscan"
	"relist-panel/internal/webassets"
)

// Deps is what NewHandler wires together. DB, Runner and DataDir are
// required; every other field may be left zero, which leaves its routes
// unregistered.
type Deps struct {
	DB *sql.DB
	// Runner drives Compose deploy lifecycle actions (ADR-0003).
	Runner composes.ComposeRunner
	// DataDir is the root directory Compose files are resolved under
	// (ADR-0002).
	DataDir string
	// Engine addresses single Containers for zero-downtime Rollouts
	// (ADR-0023). Without one, a Rollout fails.
	Engine composes.ContainerEngine
	// History and Services are the running sampler's read sides
	// (*hostmetrics.Sampler and *hostmetrics.ServiceCollector in production,
	// fakes at the HTTP seam). The metrics endpoints need both.
	History  metrics.HostHistory
	Services metrics.ServiceFacts
	// VolumeRunner and VolumeResolver are the Volume Explorer's operation
	// seam and its read side (ADR-0010).
	VolumeRunner   volumes.Runner
	VolumeResolver *volumes.Resolver
	// Terminal is the terminal seam and its settings.
	Terminal *terminal.Options
	// Images is the Images page's Docker seam.
	Images images.Images
	// Migration adds a server Migration's routes and its lock middleware
	// (ADR-0018), and resumes an interrupted lock in the background.
	Migration *migration.Options
	// Docker adds host reports and confirmed cleanup.
	Docker dockerhost.Engine
	// VulnScanner and VulnInUse add vulnerability scanning (ADR-0021). Both
	// nil leaves the feature out.
	VulnScanner vulnscan.Scanner
	VulnInUse   vulnscan.InUse
	// PanelDomain is the install's RELIST_DOMAIN. A Domain cannot take this
	// host, or Traefik could route the panel's own traffic to an app. Empty
	// means unknown and refuses nothing.
	PanelDomain string
	// Version is the build version, shown by GET /api/auth/status.
	Version string
}

// NewHandler builds the top-level HTTP handler for the panel.
//
// The Deployment worker runs until ctx is done. The returned wait blocks until
// it has returned, which is after a Deploy step in progress has finished.
func NewHandler(ctx context.Context, d Deps) (http.Handler, func()) {
	db, runner, dataDir := d.DB, d.Runner, d.DataDir
	migrationOptions := d.Migration
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)
	authService := auth.New(db).WithVersion(d.Version)
	authService.RegisterRoutes(mux)
	composesService := composes.New(db, runner, dataDir).WithEngine(d.Engine)
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
	// Without a Runner that can list container mounts, source snapshots are
	// never collected.
	mounts, _ := runner.(deployments.MountLister)
	var vulnService *vulnscan.Service
	queue := deployments.NewQueue(deployments.Deps{
		Maintenance: hostGuard,
		Store:       deployments.NewStore(db),
		Sources:     sources.NewStore(db),
		Composes:    composesService,
		Fetcher:     sources.Git{},
		Builder:     runner,
		Gate:        gate,
		Mounts:      mounts,
		Timeout:     60 * time.Minute,
		Keep:        20,
		OnSucceeded: func(composeID int64) {
			if vulnService != nil {
				if err := vulnService.ScanCompose(context.WithoutCancel(ctx), composeID); err != nil {
					log.Printf("vulnscan: queue the images of compose %d: %v", composeID, err)
				}
			}
		},
	})
	composesService.SetDeploymentCanceler(queue)
	deployments.NewAPI(queue, composesService).RegisterRoutes(mux)
	var worker sync.WaitGroup
	// vulnService is set before the queue starts, so OnSucceeded never reads
	// it while it is being written.
	if d.VulnScanner != nil && d.VulnInUse != nil {
		vulnService = vulnscan.New(vulnscan.Deps{Store: vulnscan.NewStore(db), Scanner: d.VulnScanner, InUse: d.VulnInUse, Gate: queue})
		vulnService.RegisterRoutes(mux)
	}
	worker.Add(1)
	go func() {
		defer worker.Done()
		queue.Run(ctx)
	}()
	if vulnService != nil {
		worker.Add(2)
		go func() {
			defer worker.Done()
			vulnService.Run(ctx)
		}()
		go func() {
			defer worker.Done()
			vulnService.RunScheduler(ctx)
		}()
	}
	sources.New(db, composesService, sources.Git{}, queue, gate).RegisterRoutes(mux)
	sshkeys.New(db).RegisterRoutes(mux)
	certificates.New(db, dataDir).RegisterRoutes(mux)
	domains.New(db).WithPanelHost(d.PanelDomain).RegisterRoutes(mux)
	composevariables.New(db).RegisterRoutes(mux)
	variablesets.New(db).RegisterRoutes(mux)
	volumeapi.New(composesService, d.VolumeResolver, d.VolumeRunner).RegisterRoutes(mux)
	var terminals migration.TerminalCloser
	if d.Terminal != nil {
		terminalService := terminal.New(composesService, *d.Terminal)
		terminalService.RegisterRoutes(mux)
		terminals = terminalService
	}
	if d.Images != nil {
		images.New(d.Images).RegisterRoutes(mux)
	}
	if d.Docker != nil {
		pending := func(ctx context.Context) (bool, error) {
			var count int
			err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM deployments WHERE status IN ('queued', 'running')").Scan(&count)
			return count > 0, err
		}
		owners := func(ctx context.Context) (dockerhost.Ownership, error) {
			projects, volumes, images, err := composesService.DockerOwnership(ctx)
			if err == nil && vulnService != nil && images != nil && volumes != nil {
				var ref string
				if ref, err = vulnService.ScannerImage(ctx); err == nil {
					for _, r := range vulnscan.ScannerRefs(ref) {
						images[r] = vulnscan.OwnerName
					}
					volumes[vulnscan.CacheVolume] = vulnscan.OwnerName
				}
			}
			return dockerhost.Ownership{Projects: projects, Volumes: volumes, Images: images}, err
		}
		dockerhost.NewAPI(d.Docker, hostGuard, pending, owners).RegisterRoutes(mux)
	}
	if d.History != nil && d.Services != nil {
		metricsService := metrics.New(metrics.NewStore(db), d.History, d.Services)
		metricsService.RegisterRoutes(mux)
	}
	var inner http.Handler = gate.middleware(mux)
	if migrationOptions != nil {
		domainStore := domains.NewStore(db)
		oldHost := migration.NewOldHost(migration.OldHostDeps{
			Store:           migration.NewStore(db, nil),
			Manifests:       migration.ManifestBuilder{DB: db, Composes: composesService, Volumes: d.VolumeResolver, Docker: migrationOptions.Docker, DataDir: dataDir},
			Queue:           queue,
			Docker:          migrationOptions.Docker,
			Status:          runner,
			Composes:        composesService,
			Domains:         domainStore,
			Terminals:       terminals,
			DB:              db,
			DataDir:         dataDir,
			ReportedDataDir: migrationOptions.ReportedDataDir,
			Install:         migrationOptions.Install,
		})
		oldHost.RegisterRoutes(mux)
		local := migration.LocalHost{DB: db, Docker: migrationOptions.Docker, DataDir: dataDir, Install: migrationOptions.Install}
		stateDir := filepath.Join(dataDir, ".migration")
		target := migration.NewTarget(migration.TargetDeps{
			Local:    local,
			HTTP:     migrationOptions.HTTP,
			Insecure: migrationOptions.Insecure,
			StateDir: stateDir,
			Restore:  migration.NewRestorer(local, filepath.Join(stateDir, "staging")),
			Finalizer: migration.NewFinalizer(migration.FinalizeDeps{
				Local: local, Restart: migrationOptions.Restart, Queue: queue, Sources: sources.NewStore(db), Domains: domainStore,
			}),
			Backoff: migrationOptions.Backoff,
			Context: ctx,
		})
		if migrationOptions.Control != nil {
			oldHost.RegisterControlRoutes(migrationOptions.Control)
			target.RegisterControlRoutes(migrationOptions.Control)
		}
		authService.AllowSelfAuthenticated(migration.SessionPath)
		inner = oldHost.Middleware(inner)
		worker.Add(1)
		go func() {
			defer worker.Done()
			if err := safely.Call("migration resume lock", func() error { return oldHost.ResumeLock(ctx) }); err != nil && ctx.Err() == nil {
				log.Printf("migration: resume the lock: %v", err)
			}
		}()
		if err := target.ResumeIfPending(ctx); err != nil {
			log.Printf("migration: resume the pull: %v", err)
		}
	}
	mux.Handle("/", webassets.Handler())
	// The session cookie is SameSite=Lax, and a sibling subdomain (an app this
	// panel deployed, say) is the same site, so its pages could write with the
	// admin's cookie. Writes from another origin are refused; requests with no
	// browser headers, like the migration pull, pass.
	return http.NewCrossOriginProtection().Handler(authService.Middleware(inner)), worker.Wait
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
		log.Printf("write health response: %v", err)
	}
}
