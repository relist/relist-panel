// Package terminal owns the Compose terminal's HTTP surface: the listing of a
// Compose's Services and their running Containers that the Terminal tab fills
// its selectors from, and the WebSocket session that runs an interactive shell
// in one of those Containers. It declares its port — the Exec seam — in the
// consumer, per ADR-0003, implemented for real in the leaf package dockerexec
// and faked in tests by terminal/faketest. It owns no SQL and derives no paths:
// composes.ComposeRunRequest carries the Compose's deploy identity and the
// Compose's status carries the Service names, exactly as the Compose page reads
// them.
package terminal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"relist-panel/internal/composes"
)

// Container is one running Container of a Service on the wire: the id and name
// the session addresses it by, and its ordinal within the Service.
type Container struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Number int    `json:"number"`
}

// ExecOptions is one interactive exec's parameters, in the seam's own
// vocabulary: the resolved shell path, the resolved user ("" for the image's
// own user, "0" for root) and the initial terminal size.
type ExecOptions struct {
	Shell      string // "/bin/bash" or "/bin/sh"
	User       string // "" or "0"
	Rows, Cols uint16
}

// Session is one live interactive exec: the raw TTY stream both ways, plus the
// window resize and the exit code the Engine API offers out of band.
type Session interface {
	io.ReadWriteCloser
	Resize(ctx context.Context, rows, cols uint16) error
	ExitCode(ctx context.Context) (int, error)
}

// Exec is the seam that turns the terminal's requests into Docker Engine API
// calls. It is declared here, in the consumer, and implemented for real in
// dockerexec; tests use terminal/faketest. Service names come from the
// Compose's status, so this seam only has to answer for one named Service.
type Exec interface {
	// Available reports whether the Engine API can be reached at all.
	Available(ctx context.Context) error
	// ServiceContainers lists one Service's running Containers, ordered by
	// their container number.
	ServiceContainers(ctx context.Context, project, service string) ([]Container, error)
	// HasShell reports whether path runs as a shell in the Container.
	HasShell(ctx context.Context, containerID, path string) (bool, error)
	// Start opens a TTY exec running shell as user.
	Start(ctx context.Context, containerID string, opts ExecOptions) (Session, error)
}

// Options configures the terminal service. The zero value of each limit or
// duration takes its documented default; Exec is required.
type Options struct {
	Exec         Exec
	MaxSessions  int           // 0 → 10
	PingInterval time.Duration // 0 → 30s
	PingTimeout  time.Duration // 0 → 10s
	KillGrace    time.Duration // 0 → 2s
}

const (
	defaultMaxSessions  = 10
	defaultPingInterval = 30 * time.Second
	defaultPingTimeout  = 10 * time.Second
	defaultKillGrace    = 2 * time.Second
)

// Service is the terminal's HTTP surface over one Compose.
type Service struct {
	composes     *composes.Service
	exec         Exec
	maxSessions  int
	pingInterval time.Duration
	pingTimeout  time.Duration
	killGrace    time.Duration
}

// New constructs the terminal service over the Compose service. opts.Exec is
// the Engine API seam; the zero value of every other field takes its default.
func New(composeService *composes.Service, opts Options) *Service {
	if opts.MaxSessions == 0 {
		opts.MaxSessions = defaultMaxSessions
	}
	if opts.PingInterval == 0 {
		opts.PingInterval = defaultPingInterval
	}
	if opts.PingTimeout == 0 {
		opts.PingTimeout = defaultPingTimeout
	}
	if opts.KillGrace == 0 {
		opts.KillGrace = defaultKillGrace
	}
	return &Service{
		composes:     composeService,
		exec:         opts.Exec,
		maxSessions:  opts.MaxSessions,
		pingInterval: opts.PingInterval,
		pingTimeout:  opts.PingTimeout,
		killGrace:    opts.KillGrace,
	}
}

// RegisterRoutes adds the terminal endpoints to mux. The listing is a GET, so
// the project coordinator never gates it: a session may outlive a deploy, and
// a redeploy that replaces the Container ends it through the stream instead.
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/terminal/services", s.listServices)
}

// listServices answers one Compose's Services and their running Containers,
// with which of bash/sh each Container carries. Availability comes first: when
// the Engine API is unreachable there is nothing the UI can do, so the reason
// is the answer rather than a failed listing. The Service names come from the
// Compose's own status — the same `docker compose ps` status the Compose page
// shows — so a Service whose Container is stopped still appears, with no
// running Container, which is what the selector needs to render it disabled.
func (s *Service) listServices(w http.ResponseWriter, r *http.Request) {
	req, ok := s.composeRequest(w, r)
	if !ok {
		return
	}

	if err := s.exec.Available(r.Context()); err != nil {
		writeJSON(w, http.StatusOK, servicesResponse{
			Available: false,
			Reason:    err.Error(),
			Services:  []serviceResponse{},
		})
		return
	}

	names, err := s.composes.ServiceNames(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "compose status failed: "+err.Error())
		return
	}

	services := make([]serviceResponse, 0, len(names))
	for _, name := range names {
		containers, err := s.exec.ServiceContainers(r.Context(), req.ProjectName, name)
		if err != nil {
			writeError(w, http.StatusBadGateway, "could not list containers: "+err.Error())
			return
		}
		entries := make([]containerResponse, 0, len(containers))
		for _, container := range containers {
			bash, err := s.exec.HasShell(r.Context(), container.ID, "/bin/bash")
			if err != nil {
				writeError(w, http.StatusBadGateway, "could not probe the container's shells: "+err.Error())
				return
			}
			sh, err := s.exec.HasShell(r.Context(), container.ID, "/bin/sh")
			if err != nil {
				writeError(w, http.StatusBadGateway, "could not probe the container's shells: "+err.Error())
				return
			}
			entries = append(entries, containerResponse{
				ID:     container.ID,
				Name:   container.Name,
				Number: container.Number,
				Shells: shellsResponse{Bash: bash, Sh: sh},
			})
		}
		services = append(services, serviceResponse{Name: name, Containers: entries})
	}

	writeJSON(w, http.StatusOK, servicesResponse{Available: true, Services: services})
}

// composeRequest parses the three route ids and resolves the Compose's deploy
// identity, answering the same 400/404/500 the Compose routes use. An unknown
// Project, Environment or Compose is refused before any Engine API call.
func (s *Service) composeRequest(w http.ResponseWriter, r *http.Request) (composes.RunRequest, bool) {
	projectID, err := parsePositiveID(r.PathValue("projectID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "project id must be a positive integer")
		return composes.RunRequest{}, false
	}
	environmentID, err := parsePositiveID(r.PathValue("environmentID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "environment id must be a positive integer")
		return composes.RunRequest{}, false
	}
	composeID, err := parsePositiveID(r.PathValue("composeID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "compose id must be a positive integer")
		return composes.RunRequest{}, false
	}

	req, err := s.composes.ComposeRunRequest(r.Context(), projectID, environmentID, composeID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "compose not found")
		return composes.RunRequest{}, false
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load compose")
		return composes.RunRequest{}, false
	}
	return req, true
}

// servicesResponse is the wire shape: the feature flag, and when available the
// Compose's Services. Reason is always present — empty when available — so the
// UI reads one shape.
type servicesResponse struct {
	Available bool              `json:"available"`
	Reason    string            `json:"reason"`
	Services  []serviceResponse `json:"services"`
}

// serviceResponse is one Service and its running Containers. A Service with no
// running Container carries an empty list, never null.
type serviceResponse struct {
	Name       string              `json:"name"`
	Containers []containerResponse `json:"containers"`
}

// containerResponse is one running Container and the shells it was probed for.
type containerResponse struct {
	ID     string         `json:"id"`
	Name   string         `json:"name"`
	Number int            `json:"number"`
	Shells shellsResponse `json:"shells"`
}

// shellsResponse reports which of the two shells the Container carries.
type shellsResponse struct {
	Bash bool `json:"bash"`
	Sh   bool `json:"sh"`
}

func parsePositiveID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("id must be a positive integer")
	}
	return id, nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
