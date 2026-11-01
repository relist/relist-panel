package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"relist-panel/internal/dockerexec"
	"relist-panel/internal/panelupdate"
)

// runPanelUpdate is `relist-panel panel-update apply --run <id>`, the command
// of the Updater Container (spec "Apply" step 4). Everything it needs is in
// <data dir>/.panel-update/<id>/plan.json; it reads the data directory from
// RELIST_DATA_DIR, which the panel passes in, and never falls back to the
// default one.
func runPanelUpdate(args []string) int {
	run, ok := parseApplyArgs(args)
	if !ok {
		fmt.Fprintln(os.Stderr, "Usage: relist-panel panel-update apply --run <id>")
		return 2
	}
	dataDir := os.Getenv("RELIST_DATA_DIR")
	if dataDir == "" || !filepath.IsAbs(dataDir) {
		fmt.Fprintln(os.Stderr, "panel-update: RELIST_DATA_DIR must be set to the panel's data directory")
		return 2
	}
	u := &panelupdate.Updater{
		DataDir: dataDir,
		Host:    dockerexec.NewPanelUpdaterHost(dockerexec.DefaultSocketPath),
		Logf:    log.Printf,
	}
	st, err := u.Apply(context.Background(), run)
	if err != nil {
		log.Printf("panel-update: %v", err)
		return 1
	}
	log.Printf("panel-update: run %s ended %s", run, st.State)
	if st.State != panelupdate.StateSucceeded {
		return 1
	}
	return 0
}

// parseApplyArgs accepts exactly `apply --run <id>` (or --run=<id>) with an id
// of the run-id form.
func parseApplyArgs(args []string) (run string, ok bool) {
	if len(args) < 1 || args[0] != "apply" {
		return "", false
	}
	switch rest := args[1:]; {
	case len(rest) == 2 && rest[0] == "--run":
		run = rest[1]
	case len(rest) == 1 && len(rest[0]) > len("--run=") && rest[0][:len("--run=")] == "--run=":
		run = rest[0][len("--run="):]
	default:
		return "", false
	}
	return run, panelupdate.ValidRunID(run)
}
