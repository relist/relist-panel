package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"relist-panel/internal/composes"
	"relist-panel/internal/config"
	"relist-panel/internal/database"
	"relist-panel/internal/dockerexec"
	"relist-panel/internal/dockernet"
	"relist-panel/internal/hostmetrics"
	"relist-panel/internal/httpapi"
	"relist-panel/internal/metrics"
	"relist-panel/internal/terminal"
	"relist-panel/internal/volumes"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "reset-2fa" {
		os.Exit(runResetTwoFactor(os.Args[2:], os.Stdout, os.Stderr))
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

	db, err := database.OpenAndMigrate(cfg.DatabasePath())
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// Rows created before identity was stored (ADR-0005) must be frozen to
	// the identity their stacks run under before any action addresses them.
	if err := composes.BackfillLegacyIdentities(ctx, db); err != nil {
		log.Fatal(err)
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

	server := &http.Server{
		Addr: cfg.Address(),
		Handler: httpapi.NewHandler(db, dockerexec.New(), cfg.DataDir, sampler, collector, volumeRunner, volumeResolver, &terminal.Options{
			Exec: terminalExec,
		}, dockerexec.NewImages(dockerexec.DefaultSocketPath)),
	}

	log.Printf("relist-panel listening on %s", cfg.Address())
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

	select {
	case err := <-serveErr:
		// The server exited on its own (a listen failure, say): cancel the
		// sampler's context and drain its loop before reporting.
		stop()
		sampler.Wait()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	case <-ctx.Done():
		// A signal: stop accepting requests, then let the sampler's loop
		// finish its tick and any in-flight sweep before exiting.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown: %v", err)
		}
		if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("serve: %v", err)
		}
		stop()
		sampler.Wait()
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
