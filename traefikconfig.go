package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"relist-panel/internal/accesslog"
	"relist-panel/internal/dockerexec"
	"relist-panel/internal/traefikconfig"
)

type traefikConfigOps struct {
	getenv    func(string) string
	bootstrap func(ctx context.Context, dataDir string) error
}

func realTraefikConfigOps() traefikConfigOps {
	return traefikConfigOps{
		getenv: os.Getenv,
		bootstrap: func(ctx context.Context, dataDir string) error {
			noTraefik := func(err error) bool { return errors.Is(err, accesslog.ErrNoTraefik) }
			return traefikconfig.Bootstrap(ctx, traefikconfig.PathsFor(dataDir), dockerexec.NewTraefikDocker(),
				noTraefik, os.Getenv, os.Stderr)
		},
	}
}

// runTraefikConfig is `relist-panel traefik-config`, the Install's one-shot
// traefik-config service: it writes Traefik's static config before Traefik
// starts (ADR-0030).
func runTraefikConfig(args []string) int {
	return runTraefikConfigWith(args, realTraefikConfigOps(), os.Stderr)
}

func runTraefikConfigWith(args []string, ops traefikConfigOps, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintln(stderr, "usage: relist-panel traefik-config (takes no arguments; reads RELIST_DATA_DIR)")
		return 2
	}
	dataDir := ops.getenv("RELIST_DATA_DIR")
	if dataDir == "" {
		fmt.Fprintln(stderr, "traefik-config: RELIST_DATA_DIR is not set")
		return 1
	}
	if err := ops.bootstrap(context.Background(), dataDir); err != nil {
		fmt.Fprintf(stderr, "traefik-config: %v\n", err)
		return 1
	}
	return 0
}
