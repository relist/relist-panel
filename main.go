package main

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"relist-panel/internal/auth"
	"relist-panel/internal/certificates"
	"relist-panel/internal/composes"
	"relist-panel/internal/config"
	"relist-panel/internal/database"
	"relist-panel/internal/dockerexec"
	"relist-panel/internal/dockernet"
	"relist-panel/internal/domains"
	"relist-panel/internal/hostmetrics"
	"relist-panel/internal/httpapi"
	"relist-panel/internal/metrics"
	"relist-panel/internal/migration"
	"relist-panel/internal/migrationcli"
	"relist-panel/internal/sources"
	"relist-panel/internal/terminal"
	"relist-panel/internal/volumes"
	"relist-panel/internal/vulnscan"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

// installFile is the Install file this build ships, embedded so Panel update
// can compare it with the copy on the host. It is handed to internal packages
// as a value, since go:embed cannot reach the repo root from them.
//
//go:embed docker-compose.yml
var installFile []byte

func main() {
	switch decide(os.Args[1:]) {
	case actionHelp:
		printUsage(os.Stdout)
		return
	case actionVersion:
		fmt.Println(version)
		return
	case actionUsageError:
		printUsage(os.Stderr)
		os.Exit(2)
	}
	if len(os.Args) > 1 && os.Args[1] == "reset-2fa" {
		os.Exit(runResetTwoFactor(os.Args[2:], os.Stdout, os.Stderr))
	}
	if len(os.Args) > 1 && os.Args[1] == "reset-password" {
		os.Exit(runResetPassword(os.Args[2:], os.Stdin, os.Stdout, os.Stderr, stdinConsole()))
	}
	if len(os.Args) > 1 && os.Args[1] == "create-admin" {
		os.Exit(runCreateAdmin(os.Args[2:], os.Stdin, os.Stdout, os.Stderr, stdinConsole()))
	}
	// Drive a server migration from a terminal in the panel container,
	// through the running panel's control socket (ADR-0019).
	if len(os.Args) > 1 && os.Args[1] == "migration" {
		cfg, err := config.Load()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		os.Exit(migrationcli.Run(os.Args[2:], migrationcli.DefaultEnv(cfg)))
	}
	// The maintenance container a server migration runs from this image
	// (ADR-0018): it only answers 503 with the maintenance page.
	if len(os.Args) > 1 && os.Args[1] == "maintenance-page" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := migration.ServeMaintenancePage(ctx, ":8080"); err != nil {
			log.Fatal(err)
		}
		return
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	// Cancelled on SIGINT/SIGTERM: the sampling loop stops first, and the
	// server shutdown below follows it.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Best-effort: the docker-compose.yml install path marks this network
	// external and requires it to exist before the panel container can even
	// start, so this mainly self-heals bare/standalone binary runs. It must
	// not block the panel's own HTTP server from coming up (e.g. when
	// running the binary without Docker at all during local development).
	if err := dockernet.EnsureNetwork(ctx, dockernet.NetworkName); err != nil {
		log.Printf("warning: could not ensure docker network %q exists: %v", dockernet.NetworkName, err)
	}

	// A server migration that finished pulling re-executed the panel to swap
	// in the migrated database, which must happen before it is opened
	// (ADR-0018, ADR-0019); the old host's lock and queue are then cleared
	// from it.
	migrationPending, err := migration.PrepareFinalize(cfg.DataDir)
	if err != nil {
		log.Fatal(err)
	}

	db, err := database.OpenAndMigrate(cfg.DatabasePath())
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// Report-only: the operator decides what to do with orphaned rows.
	if n, err := database.ForeignKeyViolations(ctx, db); err != nil {
		log.Printf("warning: could not check foreign keys: %v", err)
	} else if n > 0 {
		log.Printf("warning: %d rows reference deleted parents (PRAGMA foreign_key_check); they were left by an earlier connection without foreign keys", n)
	}

	if migrationPending {
		if err := migration.ClearCopiedState(ctx, db, time.Now()); err != nil {
			log.Fatal(err)
		}
	}

	// The first admin comes only from create-admin in the panel container;
	// until then every sign-in is refused like a wrong password.
	if exists, err := auth.AdminExists(ctx, db); err != nil {
		log.Fatal(err)
	} else if !exists {
		log.Println(`auth: no admin account exists; run "relist-panel create-admin" in the panel container`)
	}

	// Rows created before identity was stored (ADR-0005) must be frozen to
	// the identity their stacks run under before any action addresses them.
	if err := composes.BackfillLegacyIdentities(ctx, db); err != nil {
		log.Fatal(err)
	}

	// A crash mid-upload leaves staging files in a Compose directory, and one
	// between ReplaceSrc's renames leaves src/ missing; settle both before any
	// request can touch a Source. A failure is logged, never fatal.
	cleanSourceStaging(ctx, db, cfg.DataDir)

	// Traefik's certificate files follow panel.db (ADR-0022): rewrite them
	// after a rebuild, a restore or a Migration. A failure is logged, never
	// fatal.
	if err := certificates.Reconcile(ctx, certificates.NewStore(db), cfg.DataDir); err != nil {
		log.Printf("reconcile certificates: %v", err)
	}
	// The transport zero-downtime Domains name (ADR-0023) must always exist:
	// a Domain whose label names a missing one returns 404.
	if err := domains.WriteTransportFile(certificates.DynamicDir(cfg.DataDir)); err != nil {
		log.Printf("write traefik transport file: %v", err)
	}

	// The metrics loop (ADR-0006): one sampler for the panel's lifetime,
	// with the 30s container sweep and the 5-minute docker df refresh
	// riding its ticker off-loop, and the minute close persisting rollups
	// through the recorder.
	rollups := metrics.NewStore(db)
	containerSource := dockerexec.NewContainerSource(dockerexec.DefaultSocketPath)
	collector := hostmetrics.NewServiceCollector(containerSource, knownComposeProjects(db), time.Now)
	recorder := metrics.NewRecorder(rollups, collector, time.Now)
	var sampler *hostmetrics.Sampler
	sampler = hostmetrics.NewSampler(hostmetrics.Config{
		DataDir:         cfg.DataDir,
		SweepContainers: collector.Sweep,
		OnMinuteClose:   recorder.OnMinuteClose,
		RefreshDockerDisk: func(ctx context.Context) {
			usedBytes, err := containerSource.SystemDf(ctx)
			sampler.SetDockerDisk(float64(usedBytes), err)
		},
	})
	if err := sampler.Start(ctx); err != nil {
		log.Fatal(err)
	}

	volumeRunner := dockerexec.NewVolumeRunner()
	volumeResolver := volumes.NewResolver(dockerexec.NewVolumeResolver(), cfg.DataDir)
	terminalExec := dockerexec.NewTerminalExec(dockerexec.DefaultSocketPath, hostmetrics.DefaultProcRoot)

	// Finalize asks for a restart once the copied data is in place; main
	// shuts down and re-executes the panel (ADR-0019), while the finalize
	// goroutine waits to be replaced with the process.
	restartCh := make(chan struct{})
	var restartOnce sync.Once
	restart := func() {
		restartOnce.Do(func() { close(restartCh) })
		select {}
	}

	control := http.NewServeMux()
	handler, waitDeployments := httpapi.NewHandler(ctx, httpapi.Deps{
		DB:             db,
		Runner:         dockerexec.New(),
		Engine:         dockerexec.NewEngine(dockerexec.DefaultSocketPath),
		DataDir:        cfg.DataDir,
		Version:        version,
		History:        sampler,
		Services:       collector,
		VolumeRunner:   volumeRunner,
		VolumeResolver: volumeResolver,
		Terminal:       &terminal.Options{Exec: terminalExec},
		Images:         dockerexec.NewImages(dockerexec.DefaultSocketPath),
		Migration: &migration.Options{
			Docker:   dockerexec.NewMigrationDocker(),
			Install:  cfg.Install,
			Insecure: cfg.InsecureOldHost,
			Restart:  restart,
			Control:  control,
		},
		Docker:      dockerexec.NewDockerHost(dockerexec.DefaultSocketPath),
		VulnScanner: dockerexec.NewTrivyScanner(dockerexec.DefaultSocketPath, cfg.DataDir),
		VulnInUse:   dockerexec.NewInUse(dockerexec.DefaultSocketPath, composeIdentities(db), os.Hostname),
		PanelDomain: cfg.Install.Domain,
	})
	server := newServer(cfg.Address(), handler)
	controlServer, err := serveControl(cfg.ControlSocket, control)
	if err != nil {
		log.Printf("warning: no migration control socket, so this panel cannot migrate or resume a migration lock; set RELIST_CONTROL_SOCKET to a writable path and restart: %v", err)
	}

	log.Printf("relist-panel %s listening on %s", version, cfg.Address())
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.ListenAndServe()
	}()

	// The startup sweep is a backstop for a panel that crashed with a marked
	// shell still running. It must neither delay nor fail startup, so it runs
	// in the background under its own 30s budget, over the same frozen deploy
	// identities the metrics collector uses. A failed listing is logged and
	// the sweep skipped.
	go func() {
		sweepCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		projects, err := knownComposeProjects(db)(sweepCtx)
		if err != nil {
			log.Printf("warning: could not list composes for the terminal shell sweep: %v", err)
			return
		}
		names := make([]string, 0, len(projects))
		for name := range projects {
			names = append(names, name)
		}
		terminal.Sweep(sweepCtx, terminalExec, names, 2*time.Second, log.Printf)
	}()

	// shutdown stops accepting requests, then lets the sampler's loop finish
	// its tick and any in-flight sweep, and an in-progress Deploy step
	// finish, within ctx's deadline.
	shutdown := func(ctx context.Context) {
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("shutdown: %v", err)
		}
		if controlServer != nil {
			if err := controlServer.Shutdown(ctx); err != nil {
				log.Printf("shutdown the control socket: %v", err)
			}
		}
		if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("serve: %v", err)
		}
		stop()
		sampler.Wait()
		deploymentsDone := make(chan struct{})
		go func() {
			waitDeployments()
			close(deploymentsDone)
		}()
		select {
		case <-deploymentsDone:
		case <-ctx.Done():
			log.Printf("shutdown: a deployment was still running at the deadline")
		}
	}

	select {
	case err := <-serveErr:
		// The server exited on its own (a listen failure, say): cancel the
		// sampler's context and drain its loop before reporting.
		stop()
		sampler.Wait()
		waitDeployments()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shutdown(shutdownCtx)
	case <-restartCh:
		// The shutdown's own deadline is 5 s; the watchdog gives it 10.
		restartAfter(10*time.Second, func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			shutdown(shutdownCtx)
		}, db.Close, func() {
			exe, err := os.Executable()
			if err != nil {
				log.Printf("migration: find the panel binary: %v; exiting for the restart policy", err)
				os.Exit(1)
			}
			reexec(exe, os.Args, os.Environ(), syscall.Exec, os.Exit)
		})
	}
}

// knownComposeProjects reports every Compose's frozen deploy identity
// (ADR-0005), the set of container labels the collector is allowed to
// attribute: anything else on the daemon is ignored, not invented.
func knownComposeProjects(db *sql.DB) hostmetrics.KnownProjects {
	return func(ctx context.Context) (map[string]bool, error) {
		rows, err := db.QueryContext(ctx, `
			SELECT id, project_slug, env_slug, name, identity_version
			FROM composes
		`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		known := make(map[string]bool)
		for rows.Next() {
			var compose composes.Compose
			if err := rows.Scan(&compose.ID, &compose.ProjectSlug, &compose.EnvSlug, &compose.Name, &compose.IdentityVersion); err != nil {
				return nil, err
			}
			known[compose.DeployProjectName()] = true
		}
		return known, rows.Err()
	}
}

// composeIdentities maps every Compose's deploy identity to its IDs, so the
// vulnerability scanner can say which Service runs an image.
func composeIdentities(db *sql.DB) dockerexec.ComposeIdentities {
	return func(ctx context.Context) (map[string]vulnscan.ImageUser, error) {
		rows, err := db.QueryContext(ctx, `
			SELECT c.id, c.project_slug, c.env_slug, c.name, c.identity_version, e.id, e.project_id
			FROM composes c JOIN environments e ON e.id = c.environment_id
		`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		known := make(map[string]vulnscan.ImageUser)
		for rows.Next() {
			var compose composes.Compose
			var user vulnscan.ImageUser
			if err := rows.Scan(&compose.ID, &compose.ProjectSlug, &compose.EnvSlug, &compose.Name, &compose.IdentityVersion,
				&user.EnvironmentID, &user.ProjectID); err != nil {
				return nil, err
			}
			user.Kind, user.ComposeID = vulnscan.UserService, compose.ID
			known[compose.DeployProjectName()] = user
		}
		return known, rows.Err()
	}
}

// cleanSourceStaging runs sources.CleanStaging over every Compose directory,
// and composes.CleanStaging for the zero-downtime check and build copies a
// stopped panel left there, which hold env values.
func cleanSourceStaging(ctx context.Context, db *sql.DB, dataDir string) {
	dirs, err := composes.ComposeDirs(ctx, db, dataDir)
	if err != nil {
		log.Printf("warning: could not list Compose directories to clear upload leftovers: %v", err)
		return
	}
	for _, dir := range dirs {
		if err := sources.CleanStaging(dir); err != nil {
			log.Printf("warning: clearing upload leftovers in %s: %v", dir, err)
		}
		if err := composes.CleanStaging(dir); err != nil {
			log.Printf("warning: clearing staged compose files in %s: %v", dir, err)
		}
	}
}
