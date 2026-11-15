package directaccess

import (
	"context"
	"errors"
	"strconv"
	"time"
)

// Container naming (ADR-0041). The expires label is informational only: Extend
// cannot change a container's labels, so the table is the truth.
const (
	IDLabel      = "relist.access.id"
	TargetLabel  = "relist.access.target"
	ExpiresLabel = "relist.access.expires"
)

// ContainerName is the proxy container of one Direct access.
func ContainerName(id int64) string { return "relist-access-" + strconv.FormatInt(id, 10) }

var (
	// ErrPortInUse is wrapped with the port, so the message names it.
	ErrPortInUse = errors.New("that port is already in use on the host")
	// ErrGone means the proxy container is missing or has stopped.
	ErrGone = errors.New("the proxy is no longer running")
)

// ProxySpec is everything one proxy container needs.
type ProxySpec struct {
	AccessID    int64
	TargetLabel string // "database:7"
	Network     string
	Upstream    string // "relist-db-7:5432"
	ListenPort  int    // the port inside the proxy container
	BindIP      string
	HostPort    int // 0 = Docker picks
	Deadline    time.Time
}

type ProxyContainer struct {
	AccessID int64
	Running  bool
}

// Host runs the proxy containers on the Docker host.
type Host interface {
	// StartProxy creates and starts the proxy and returns the host port
	// Docker published, read back after start. A port already taken is an
	// error wrapping ErrPortInUse, and nothing is left behind.
	StartProxy(ctx context.Context, spec ProxySpec) (hostPort int, err error)
	ListProxies(ctx context.Context) ([]ProxyContainer, error)
	// RemoveProxy force-removes the container; a missing one is not an error.
	RemoveProxy(ctx context.Context, accessID int64) error
	// Extend moves the proxy's deadline without dropping connections. A
	// missing or stopped container gives ErrGone.
	Extend(ctx context.Context, accessID int64, deadline time.Time) error
}
