// Package volumeapi owns the Volume Explorer's HTTP surface: one Compose's
// resolved Volumes with their pre-flight status, the uid/gid an operation per
// Service would run as, whether the feature can run at all, and the
// operations themselves — the reads (list, stat, read and archive), the
// writes (write, over a request body, and delete), and the restore of a
// tar.gz streamed into extract — each one `docker run --rm` through the
// volumes.Runner seam (ADR-0010). It owns no SQL and derives no paths —
// composes.ComposeRunRequest carries the Compose's deploy identity and
// volumes.Resolver does the reading through the Compose CLI.
package volumeapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"strconv"

	"relist-panel/internal/composes"
	"relist-panel/internal/volumes"
)

// Service is the HTTP surface over one Compose's resolved Volumes.
type Service struct {
	composes *composes.Service
	resolver *volumes.Resolver
	runner   volumes.Runner
}

// New constructs the Volume Explorer's service. composeService loads a
// Compose's deploy identity; resolver is the read side (resolution and
// pre-flight); runner is the operation seam whose Available probe gates the
// feature.
func New(composeService *composes.Service, resolver *volumes.Resolver, runner volumes.Runner) *Service {
	return &Service{composes: composeService, resolver: resolver, runner: runner}
}

// RegisterRoutes adds the authenticated Volume Explorer endpoints to mux: the
// listing of a Compose's resolved Volumes, the per-operation reads (archive
// included), and the per-operation writes — content answers PUT for an
// upload or edit and DELETE for a file or a tree, directory and file answer
// PUT to create one entry (never replacing an existing one), and restore
// answers PUT for a tar.gz streamed into extract — never GET, so the
// SameSite=Lax cookie and the panel's cross-origin check keep them CSRF-safe.
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/volumes", s.list)
	mux.HandleFunc("GET /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/volumes/list", s.listFiles)
	mux.HandleFunc("GET /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/volumes/stat", s.statFile)
	mux.HandleFunc("GET /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/volumes/read", s.readFile)
	mux.HandleFunc("GET /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/volumes/archive", s.archive)
	mux.HandleFunc("PUT /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/volumes/content", s.writeFile)
	mux.HandleFunc("DELETE /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/volumes/content", s.deleteFile)
	mux.HandleFunc("PUT /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/volumes/directory", s.mkdir)
	mux.HandleFunc("PUT /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/volumes/file", s.createFile)
	mux.HandleFunc("PUT /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/volumes/restore", s.restore)
	mux.HandleFunc("GET /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/volumes/orphans", s.listOrphans)
	mux.HandleFunc("DELETE /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/volumes/orphans/{name}", s.deleteOrphan)
}

// list answers one Compose's resolved Volumes and every Service's operation
// identity. Availability comes first: without a containerized panel there is
// nothing the UI can do, so the reason is the answer rather than a failed
// resolution. Before the first deploy there is no env file for the Compose
// CLI to read, so it is not asked: the answer is `deployed: false`, a state
// rather than a failed request. The resolution itself is the Compose CLI's —
// the panel never parses YAML for paths — and a resolution failure surfaces
// the CLI's own message with a Bad Gateway status, like every other runner
// failure.
func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	req, ok := s.composeRequest(w, r)
	if !ok {
		return
	}

	if err := s.runner.Available(r.Context()); err != nil {
		writeJSON(w, http.StatusOK, response{
			Available: false,
			Reason:    err.Error(),
			Volumes:   []volumeResponse{},
			Services:  []serviceResponse{},
		})
		return
	}
	if !deployed(req.EnvFilePath) {
		writeJSON(w, http.StatusOK, response{
			Available: true,
			Volumes:   []volumeResponse{},
			Services:  []serviceResponse{},
		})
		return
	}

	resolution, err := s.resolver.Resolve(r.Context(), volumes.ComposeRef{
		ProjectName:     req.ProjectName,
		ComposeFilePath: req.ComposeFilePath,
		EnvFilePath:     req.EnvFilePath,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "volume resolution failed: "+err.Error())
		return
	}

	resolvedVolumes := make([]volumeResponse, 0, len(resolution.Volumes))
	for _, volume := range resolution.Volumes {
		preflight, err := s.resolver.Preflight(r.Context(), volume)
		if err != nil {
			writeError(w, http.StatusBadGateway, "volume pre-flight failed: "+err.Error())
			return
		}
		resolvedVolumes = append(resolvedVolumes, volumeResponseOf(volume, preflight))
	}
	services := make([]serviceResponse, 0, len(resolution.Users))
	for _, user := range resolution.Users {
		services = append(services, serviceResponse{
			Service: user.Service,
			User:    user.User,
			Source:  user.Source,
			Warning: user.Warning,
		})
	}

	writeJSON(w, http.StatusOK, response{
		Available: true,
		Deployed:  true,
		Volumes:   resolvedVolumes,
		Services:  services,
	})
}

// deployed reports whether a deploy has written the Compose's env file. The
// Compose CLI refuses to resolve without it (`couldn't find env file`), so
// before the first deploy there is nothing to ask. Any error other than "not
// there" counts as deployed, so the CLI's own message reports it.
func deployed(envFilePath string) bool {
	_, err := os.Stat(envFilePath)
	return !errors.Is(err, fs.ErrNotExist)
}

// composeRequest parses the three route ids and loads the Compose's deploy
// identity, answering the same 400/404/500 the Compose routes use. Every
// Volume Explorer route starts here, so an unknown Project, Environment or
// Compose — or a non-numeric id — is refused identically and no CLI or runner
// call happens.
func (s *Service) composeRequest(w http.ResponseWriter, r *http.Request) (composes.RunRequest, bool) {
	projectID, environmentID, err := routeIDs(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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

// response is the wire shape: the feature flag, whether a deploy has written
// the env file the resolution needs, and when both hold the Compose's
// resolved Volumes and Services. Reason is present only when the feature is
// unavailable.
type response struct {
	Available bool              `json:"available"`
	Deployed  bool              `json:"deployed"`
	Reason    string            `json:"reason,omitempty"`
	Volumes   []volumeResponse  `json:"volumes"`
	Services  []serviceResponse `json:"services"`
}

// volumeResponse is one Volume on the wire, camelCased like the rest of the
// API.
type volumeResponse struct {
	Kind        volumes.VolumeKind `json:"kind"`
	LogicalName string             `json:"logicalName"`
	Source      string             `json:"source"`
	Uses        []useResponse      `json:"uses"`
	Preflight   preflightResponse  `json:"preflight"`
}

// useResponse is one Service's mount of a Volume.
type useResponse struct {
	Service  string `json:"service"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"readOnly"`
}

// preflightResponse is the mount-safety outcome for a Volume.
type preflightResponse struct {
	Status  volumes.PreflightStatus `json:"status"`
	Warning string                  `json:"warning"`
}

// serviceResponse is one Service's operation identity.
type serviceResponse struct {
	Service string             `json:"service"`
	User    string             `json:"user"`
	Source  volumes.UserSource `json:"source"`
	Warning string             `json:"warning"`
}

func volumeResponseOf(volume volumes.Volume, preflight volumes.Preflight) volumeResponse {
	uses := make([]useResponse, 0, len(volume.Uses))
	for _, use := range volume.Uses {
		uses = append(uses, useResponse{
			Service:  use.Service,
			Target:   use.Target,
			ReadOnly: use.ReadOnly,
		})
	}
	return volumeResponse{
		Kind:        volume.Kind,
		LogicalName: volume.LogicalName,
		Source:      volume.Source,
		Uses:        uses,
		Preflight: preflightResponse{
			Status:  preflight.Status,
			Warning: preflight.Warning,
		},
	}
}

// routeIDs parses the two parent ids the Compose routes share, with the same
// messages they use.
func routeIDs(r *http.Request) (int64, int64, error) {
	projectID, err := parsePositiveID(r.PathValue("projectID"))
	if err != nil {
		return 0, 0, errors.New("project id must be a positive integer")
	}
	environmentID, err := parsePositiveID(r.PathValue("environmentID"))
	if err != nil {
		return 0, 0, errors.New("environment id must be a positive integer")
	}
	return projectID, environmentID, nil
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
