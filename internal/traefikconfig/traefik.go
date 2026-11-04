package traefikconfig

import (
	"context"
	"time"
)

// Traefik is what a Traefik apply needs from Docker: the install's Traefik
// Container, and throwaway dry-run Containers of its image.
type Traefik interface {
	State(ctx context.Context) (State, error)
	Restart(ctx context.Context) error
	// LogsSince is Traefik's own log from since, at most tail lines.
	LogsSince(ctx context.Context, since time.Time, tail int) (string, error)
	// DryRun starts relist-traefik-dryrun-<id> from image with dir mounted at
	// /etc/traefik, waits wait, reads whether it still runs and its log, and
	// always removes it.
	DryRun(ctx context.Context, id, dir, image string, wait time.Duration) (DryRunResult, error)
	// RemoveDryRuns removes every relist-traefik-dryrun-* Container.
	RemoveDryRuns(ctx context.Context) error
	// Healthz asks Traefik at ip for the panel's /healthz with domain as Host
	// and SNI.
	Healthz(ctx context.Context, ip, domain string) error
}

// State is the install's Traefik Container as Docker reports it.
type State struct {
	Running      bool
	RestartCount int
	StartedAt    time.Time
	Image        string // image ID
	IP           string // on relist-network
}

type DryRunResult struct {
	Running bool   `json:"running"`
	Log     string `json:"log"`
}
