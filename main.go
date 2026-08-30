package main

import (
	"context"
	"log"
	"net/http"

	"relist-panel/internal/config"
	"relist-panel/internal/database"
	"relist-panel/internal/dockerexec"
	"relist-panel/internal/dockernet"
	"relist-panel/internal/httpapi"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	// Best-effort: the docker-compose.yml install path marks this network
	// external and requires it to exist before the panel container can even
	// start, so this mainly self-heals bare/standalone binary runs. It must
	// not block the panel's own HTTP server from coming up (e.g. when
	// running the binary without Docker at all during local development).
	if err := dockernet.EnsureNetwork(context.Background(), dockernet.NetworkName); err != nil {
		log.Printf("warning: could not ensure docker network %q exists: %v", dockernet.NetworkName, err)
	}

	db, err := database.OpenAndMigrate(cfg.DatabasePath())
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	server := &http.Server{
		Addr:    cfg.Address(),
		Handler: httpapi.NewHandler(db, dockerexec.New(), cfg.DataDir),
	}

	log.Printf("relist-panel listening on %s", cfg.Address())
	log.Fatal(server.ListenAndServe())
}
