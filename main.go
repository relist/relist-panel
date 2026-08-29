package main

import (
	"log"
	"net/http"

	"relist-panel/internal/config"
	"relist-panel/internal/database"
	"relist-panel/internal/httpapi"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	db, err := database.OpenAndMigrate(cfg.DatabasePath())
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	server := &http.Server{
		Addr:    cfg.Address(),
		Handler: httpapi.NewHandler(),
	}

	log.Printf("relist-panel listening on %s", cfg.Address())
	log.Fatal(server.ListenAndServe())
}
