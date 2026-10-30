package composes

import "context"

// EngineContainer is one Container of a Compose's project, as the Docker
// Engine lists it.
type EngineContainer struct {
	ID, Name, Service string
	Running           bool
}

// EngineMount is one volume mount of a Container. Anonymous is Docker's own
// com.docker.volume.anonymous label on the volume.
type EngineMount struct {
	Destination string
	Anonymous   bool
}

// EngineState is what a Rollout reads of one Container.
type EngineState struct {
	Running          bool
	Health           string // "", "starting", "healthy" or "unhealthy"
	LastHealthOutput string
	Labels           map[string]string
	Mounts           []EngineMount // volume mounts only
}

// ContainerEngine addresses single Containers by ID, which `docker compose`
// can't (ADR-0023). It is declared here, in the consumer, and implemented over
// the Docker socket in `dockerexec`; tests use a fake.
type ContainerEngine interface {
	// ProjectContainers lists the project's Containers in every state, one-off
	// ones (compose run) left out.
	ProjectContainers(ctx context.Context, project string) ([]EngineContainer, error)
	Inspect(ctx context.Context, id string) (EngineState, error)
	// Stop stops a Container within its own StopTimeout (stop_grace_period).
	Stop(ctx context.Context, id string) error
	// Remove removes a Container; one already gone is not an error.
	Remove(ctx context.Context, id string, force, volumes bool) error
}
