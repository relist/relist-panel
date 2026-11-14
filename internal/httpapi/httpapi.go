// Package httpapi wires the panel's HTTP routes together.
package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"relist-panel/internal/accesslog"
	"relist-panel/internal/auditapi"
	"relist-panel/internal/auth"
	"relist-panel/internal/authgroups"
	"relist-panel/internal/authz"
	"relist-panel/internal/builders"
	"relist-panel/internal/certificates"
	"relist-panel/internal/clientip"
	"relist-panel/internal/composes"
	"relist-panel/internal/composevariables"
	"relist-panel/internal/databases"
	"relist-panel/internal/deployments"
	"relist-panel/internal/dockerhost"
	"relist-panel/internal/domains"
	"relist-panel/internal/environments"
	"relist-panel/internal/environmentvariables"
	"relist-panel/internal/grants"
	"relist-panel/internal/images"
	"relist-panel/internal/metrics"
	"relist-panel/internal/migration"
	"relist-panel/internal/panelupdate"
	"relist-panel/internal/policy"
	"relist-panel/internal/projects"
	"relist-panel/internal/registry"
	"relist-panel/internal/registryauth"
	"relist-panel/internal/requestmetrics"
	"relist-panel/internal/safely"
	"relist-panel/internal/sources"
	"relist-panel/internal/sshkeys"
	"relist-panel/internal/terminal"
	"relist-panel/internal/traefikconfig"
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
	// AuthGroupRefs lets a Basic auth group delete refuse while a running
	// Container still routes through it (ADR-0036). Nil checks the database only.
	AuthGroupRefs authgroups.RunningRefs
	// VulnScanner and VulnInUse add vulnerability scanning (ADR-0021). Both
	// nil leaves the feature out.
	VulnScanner vulnscan.Scanner
	VulnInUse   vulnscan.InUse
	// Buildx adds Builders: resource-limited buildx builders each Compose can
	// build on. Nil builds everything on Docker default.
	Buildx builders.Engine
	// PanelSelf lets Panel update find the Install, and PanelRunner starts the
	// `update` Container.
	PanelSelf   panelupdate.SelfInspector
	PanelRunner panelupdate.UpdateRunner
	// PanelUpdater finds and removes the Updater Container.
	PanelUpdater panelupdate.UpdaterContainers
	// Traefik reaches the install's Traefik for the Traefik page and the
	// Access log rotation (ADR-0027). main always sets it; a run outside the
	// install stack finds no container, so the page reports no Traefik while
	// rotation still runs. Nil (tests) leaves rotation off.
	Traefik accesslog.Traefik
	// TraefikApply restarts and dry-runs the install's Traefik for a Traefik
	// apply (ADR-0030). Nil (tests) leaves the Configuration routes out.
	TraefikApply traefikconfig.Traefik
	// PanelDomain is the install's RELIST_DOMAIN. A Domain cannot take this
	// host, or Traefik could route the panel's own traffic to an app. Empty
	// means unknown and refuses nothing.
	PanelDomain string
	// PolicyApprover and PolicyRoots are the Compose policy's settings
	// (ADR-0032); the zero values are every admin and no roots.
	PolicyApprover policy.Approver
	PolicyRoots    []string
	// Version is the build version, shown by GET /api/auth/status.
	Version string
	// RegistryAuth is the Runner's source of registry logins; the handler
	// sets it once its stores exist. Nil (tests) leaves pulls without logins.
	RegistryAuth *registryauth.Switch
	// RegistryTester logs in for an External registry's Test button. Nil
	// answers every Test with "not available".
	RegistryTester registryauth.Tester
	// RegistryHost drives the Panel registry's container (ADR-0033). Nil
	// leaves the Registry routes out; its hostname still reserves Domains.
	RegistryHost registry.Host
	// DatabaseHost drives the panel's Databases (ADR-0039). Nil leaves the
	// Databases routes out.
	DatabaseHost databases.Host
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
	authService := auth.New(db).WithVersion(d.Version).WithAccess(func(ctx context.Context, u auth.User) (any, error) {
		access, err := grants.Load(ctx, db, u.ID, u.IsAdmin)
		if err != nil {
			return nil, err
		}
		return access.View(), nil
	})
	authService.RegisterRoutes(mux)
	externalRegistries := registryauth.NewStore(db)
	composesService := composes.New(db, runner, dataDir).WithEngine(d.Engine).WithRegistryLogins(func(ctx context.Context) ([]string, error) {
		list, err := externalRegistries.List(ctx)
		hosts := make([]string, 0, len(list))
		for _, e := range list {
			hosts = append(hosts, e.Host)
		}
		return hosts, err
	})
	registryService := newRegistryService(db, dataDir, d, composesService)
	composesService.WithRegistryHost(registryService.Hostname)
	// External registry logins and the Registry's own reach every pull before
	// any worker starts (ADR-0033, ADR-0034).
	if d.RegistryAuth != nil {
		d.RegistryAuth.Set(registryauth.MultiSource(externalRegistries, registryService))
	}
	composesService.SetPolicyRules(d.PolicyRoots, d.PanelDomain)
	composesService.SetApprover(d.PolicyApprover)
	authService.WithApprover(composesService.IsApprover)
	projectsService := projects.New(db, composesService)
	projectsService.RegisterRoutes(mux)
	environmentsService := environments.New(db, composesService)
	environmentsService.RegisterRoutes(mux)
	environmentVariablesService := environmentvariables.New(db)
	environmentVariablesService.RegisterRoutes(mux)
	grants.New(db).RegisterRoutes(mux)
	deploymentStore := deployments.NewStore(db)
	var builderPool deployments.BuilderPool
	if d.Buildx != nil {
		builderStore := builders.NewStore(db)
		builderService := builders.New(builders.Deps{Store: builderStore, Engine: d.Buildx, Deployments: deploymentStore})
		builderService.RegisterRoutes(mux)
		composesService.WithBuilders(builderStore)
		builderPool = builderService
	}
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
		Store:       deploymentStore,
		Sources:     sources.NewStore(db),
		Composes:    composesService,
		Policy:      composesService,
		Fetcher:     sources.Git{},
		Builder:     runner,
		Builders:    builderPool,
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
	deployments.NewAPI(queue, composesService).WithApprover(composesService.IsApprover).RegisterRoutes(mux)
	var worker sync.WaitGroup
	// vulnService is set before the queue starts, so OnSucceeded never reads
	// it while it is being written.
	if d.VulnScanner != nil && d.VulnInUse != nil {
		vulnService = vulnscan.New(vulnscan.Deps{Store: vulnscan.NewStore(db), Scanner: d.VulnScanner, InUse: d.VulnInUse, Gate: queue})
		vulnService.RegisterRoutes(mux)
	}
	// The update lock is checked before any worker starts: a Panel update that
	// is not over keeps this panel from running Deployments or taking writes.
	// registryCleaner is declared here so the update's WaitingOn can name a
	// running cleanup; it is set further down, once the Registry is wired.
	var registryCleaner *registry.Cleaner
	puDeps := panelupdate.Deps{
		Store: panelupdate.NewStore(db), Version: d.Version, ManifestURL: panelupdate.ManifestURLFromEnv(),
		Self: d.PanelSelf, Runner: d.PanelRunner, Updater: d.PanelUpdater,
		DataDir: dataDir, DeploysBusy: queue.Busy, Queue: queue,
		WaitingOn: func() string {
			var cleaning func() bool
			if registryCleaner != nil { // set below, before any update can run
				cleaning = registryCleaner.Running
			}
			return updateWaitingOn(ctx, db, queue, cleaning)
		},
		MigrationLocked: func(ctx context.Context) (bool, error) {
			if migrationOptions == nil {
				return false, nil
			}
			step, _, err := migration.NewStore(db, nil).Lock(ctx)
			return step != migration.StepNone, err
		},
	}
	if vulnService != nil {
		puDeps.Scans = vulnService
	}
	// The Traefik apply and the Panel update hold each other off: the
	// update's quiesce waits for an apply, and an apply is refused while the
	// update or the Migration lock is on.
	var panelUpdates *panelupdate.Service
	var traefikApply *traefikconfig.Runner
	if d.TraefikApply != nil {
		traefikApply = &traefikconfig.Runner{
			Paths: traefikconfig.PathsFor(dataDir), Traefik: d.TraefikApply, Domain: d.PanelDomain,
			Gate: func(ctx context.Context) error {
				if panelUpdates.Locked() {
					return fmt.Errorf("%w: a Panel update is running", traefikconfig.ErrLocked)
				}
				if locked, err := puDeps.MigrationLocked(ctx); err != nil {
					return err
				} else if locked {
					return fmt.Errorf("%w: a server Migration holds the lock", traefikconfig.ErrLocked)
				}
				return nil
			},
		}
		puDeps.TraefikApply = traefikApply
	}
	// Registry cleanup waits for Deployments and the locks, and a Panel
	// update's quiesce waits for it (ADR-0033).
	if d.RegistryHost != nil {
		deployed, running := registryRefs(d, composesService)
		registryCleaner = registry.NewCleaner(registry.CleanerDeps{
			Service: registryService,
			Gate: registryGate{busy: queue.Busy, locked: func() bool {
				if panelUpdates.Locked() {
					return true
				}
				locked, err := puDeps.MigrationLocked(ctx)
				return locked || err != nil
			}},
			InUse: inUseRefs(deployed, running),
		})
		puDeps.RegistryCleanup = registryCleaner
	}
	databaseService := newDatabaseService(db, d, puDeps.MigrationLocked)
	panelUpdates = panelupdate.New(puDeps)
	if err := panelUpdates.StartLocked(ctx); err != nil {
		log.Printf("panelupdate: start locked: %v", err)
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
	panelUpdates.RegisterRoutes(mux)
	worker.Add(2)
	go func() {
		defer worker.Done()
		panelUpdates.RunScheduler(ctx)
	}()
	go func() {
		defer worker.Done()
		panelUpdates.Watch(ctx)
	}()
	sources.New(db, composesService, sources.Git{}, queue, gate).RegisterRoutes(mux)
	sshkeys.New(db).RegisterRoutes(mux)
	registryauth.New(externalRegistries, d.RegistryTester, nil).RegisterRoutes(mux)
	certificates.New(db, dataDir).RegisterRoutes(mux)
	authgroups.New(db, dataDir).WithRunning(d.AuthGroupRefs).RegisterRoutes(mux)
	accessLogs := accesslog.New(db, dataDir, d.Traefik)
	accessLogs.RegisterRoutes(mux)
	requestStore := requestmetrics.NewStore(db)
	requestmetrics.NewService(requestStore, traefikconfig.PathsFor(dataDir), domains.NewStore(db), db).RegisterRoutes(mux)
	auditLog := auditapi.New(db)
	auditLog.RegisterRoutes(mux)
	worker.Add(1)
	go func() {
		defer worker.Done()
		auditLog.RunRetention(ctx)
	}()
	if traefikApply != nil {
		traefikconfig.NewService(traefikApply).RegisterRoutes(mux)
		if err := traefikApply.Resume(ctx); err != nil {
			log.Printf("traefikconfig: resume the last apply: %v", err)
		}
	}
	if d.Traefik != nil {
		worker.Add(1)
		go func() {
			defer worker.Done()
			accessLogs.Rotator().Run(ctx)
		}()
		// Request metrics are read from the same Access log (ADR-0035).
		domainHosts := requestmetrics.NewDomainHosts(domains.NewStore(db).ListAll, time.Now)
		ingester := &requestmetrics.Ingester{Dir: accesslog.Dir(dataDir), Store: requestStore, Hosts: domainHosts.Lookup}
		worker.Add(1)
		go func() {
			defer worker.Done()
			ingester.Run(ctx)
		}()
	}
	domains.New(db).WithPanelHost(d.PanelDomain).WithReservedHost(registryService.ReservedHost).RegisterRoutes(mux)
	if d.RegistryHost != nil {
		registryAPI(d, dataDir, registryService, composesService, registryCleaner).RegisterRoutes(mux)
		worker.Add(1)
		go func() {
			defer worker.Done()
			// A GC the panel died during goes back read-write first, then
			// reconcile, then the schedule.
			if err := registryCleaner.Startup(ctx); err != nil {
				log.Printf("registry: finish an interrupted cleanup: %v", err)
			}
			if _, err := registryService.Reconcile(ctx); err != nil {
				log.Printf("registry: reconcile at start: %v", err)
			}
			registryCleaner.RunScheduler(ctx)
		}()
	}
	if databaseService != nil {
		databases.NewAPI(databaseService).RegisterRoutes(mux)
		worker.Add(1)
		go func() {
			defer worker.Done()
			if err := databaseService.ReconcileAll(ctx); err != nil {
				log.Printf("databases: reconcile at start: %v", err)
			}
		}()
	}
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
			if err == nil && images != nil && volumes != nil && d.RegistryHost != nil {
				ownRegistry(images, volumes)
			}
			if err == nil && images != nil && volumes != nil && databaseService != nil {
				err = ownDatabases(ctx, databaseService, images, volumes)
			}
			// The previous panel image stays for a manual rollback.
			if err == nil && images != nil {
				var prev string
				if prev, err = panelUpdates.PreviousImage(ctx); err == nil && prev != "" {
					images[prev] = panelupdate.OwnerName
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
	var inner http.Handler = panelUpdates.Middleware(gate.middleware(mux))
	if migrationOptions != nil {
		domainStore := domains.NewStore(db)
		oldHostDeps := migration.OldHostDeps{
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
			UpdateLocked:    panelUpdates.Locked,
		}
		if registryCleaner != nil { // a nil *Cleaner in the interface would not read as nil
			oldHostDeps.RegistryCleanup = registryCleaner
		}
		oldHost := migration.NewOldHost(oldHostDeps)
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
				BeforeDeploy: registryBeforeDeploy(d, registryService),
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
	// authz sits inside auth, which puts the User in the context, and outside
	// every lock: a request without the right is refused before a lock sees it.
	h := http.NewCrossOriginProtection().Handler(authService.Middleware(authz.New(db, authService.CurrentUser).Middleware(mux, inner)))
	wait := func() {
		worker.Wait()
		if databaseService != nil {
			databaseService.Wait() // a Database being created finishes first
		}
	}
	return clientIPResolver(d.TraefikApply, dataDir).Middleware(h), wait
}

// clientIPResolver finds the client address the Audit log records: from
// X-Forwarded-For only when the peer is the install's Traefik (ADR-0037).
// Without a Traefik to ask, every request records its TCP peer.
func clientIPResolver(t traefikconfig.Traefik, dataDir string) *clientip.Resolver {
	proxyIP := func(ctx context.Context) (string, error) {
		if t == nil {
			return "", nil
		}
		st, err := t.State(ctx)
		return st.IP, err
	}
	effective := traefikconfig.PathsFor(dataDir).Effective()
	trusted := func() []string {
		raw, err := os.ReadFile(effective)
		if err != nil {
			return nil
		}
		return traefikconfig.TrustedIPs(raw)
	}
	return clientip.New(proxyIP, trusted)
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
		log.Printf("write health response: %v", err)
	}
}

// updateWaitingOn says what a quiescing Panel update waits for. It is asked
// only while the Deployment queue, the scan queue or a Registry cleanup is
// not idle, so with no Deployment and no cleanup running the wait is the scan
// worker's current task. cleaning is nil when there is no Registry.
func updateWaitingOn(ctx context.Context, db *sql.DB, queue *deployments.Queue, cleaning func() bool) string {
	composeID, number, ok := queue.Running()
	if !ok {
		if cleaning != nil && cleaning() {
			return "a Registry cleanup"
		}
		return "a vulnerability scan"
	}
	var name string
	if err := db.QueryRowContext(ctx, "SELECT name FROM composes WHERE id = ?", composeID).Scan(&name); err != nil {
		return fmt.Sprintf("Deployment #%d", number)
	}
	return fmt.Sprintf("Deployment %s #%d", name, number)
}

// registryBeforeDeploy brings the panel's own Registry up before a
// Migration's Deployments, which may pull from it. Reconcile is idempotent and
// shares the start-up one's lock. Nil without a Docker host for it.
func registryBeforeDeploy(d Deps, svc *registry.Service) func(context.Context) error {
	if d.RegistryHost == nil {
		return nil
	}
	return func(ctx context.Context) error {
		_, err := svc.Reconcile(ctx)
		return err
	}
}
