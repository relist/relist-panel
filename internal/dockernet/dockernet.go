// Package dockernet ensures the Docker network shared between the panel,
// Traefik, and every deployed Compose exists on the host.
package dockernet

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// NetworkName is the Docker network shared by the panel, Traefik, and the
// deployed Services that need it, per ADR-0002 as narrowed by ADR-0014. It is a fixed name, not configurable,
// because docker-compose.yml (the install bootstrap) references it by this
// exact literal, marked external there — see the ADR for why.
const NetworkName = "relist-network"

// EnsureNetwork makes sure a Docker network with the given name exists,
// creating it if it does not. It is idempotent: calling it when the network
// already exists is a no-op, including when another process wins a race to
// create it first.
func EnsureNetwork(ctx context.Context, name string) error {
	if exec.CommandContext(ctx, "docker", "network", "inspect", name).Run() == nil {
		return nil
	}

	output, err := exec.CommandContext(ctx, "docker", "network", "create", name).CombinedOutput()
	if err != nil && !strings.Contains(string(output), "already exists") {
		return fmt.Errorf("create docker network %q: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return nil
}
