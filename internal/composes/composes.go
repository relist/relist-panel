// Package composes owns Compose metadata and its HTTP API.
package composes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"relist-panel/internal/domains"
	"relist-panel/internal/slug"
)

const (
	maxRequestBody  = 1 << 20
	maxComposeName  = 255
	composeFileName = "docker-compose.yml"
	envFileName     = ".env"
	composeDirPerm  = 0o750
	composeFilePerm = 0o644
	envFilePerm     = 0o600
)

// RunRequest is what a ComposeRunner needs to act on one Compose: its
// deterministic project identity and the resolved files already written to
// disk for it.
type RunRequest struct {
	ProjectName     string
	ComposeFilePath string
	EnvFilePath     string
}

// ComposeStatus summarizes a Compose's live container state. Services are the
// Compose's Service names as `docker compose ps --all` reported them, in
// first-seen order; a Service whose Containers have all stopped is still
// named. They are not on the wire — the Compose page renders running/total —
// but they ride the status for the terminal listing, which needs the set of
// Services without owning a runner or a lookup of its own.
type ComposeStatus struct {
	Running  int      `json:"running"`
	Total    int      `json:"total"`
	Services []string `json:"-"`
}

// UpOptions tunes a deploy. Build passes --build, else --no-build. Out, when
// set, receives the command's stdout and stderr as they arrive.
type UpOptions struct {
	Build bool
	// Services, when set, limits up to them, with --no-deps (ADR-0023).
	Services []string
	Out      io.Writer
}

// ScaleOptions starts one more Container of Service next to the running ones,
// without recreating them: Replicas is how many there are afterwards.
type ScaleOptions struct {
	Service  string
	Replicas int
	Build    bool
	Out      io.Writer
}

// BuildOptions tunes building one Service. Out, when set, receives the
// command's stdout and stderr as they arrive.
type BuildOptions struct {
	Service string
	NoCache bool
	Out     io.Writer
}

// ComposeRunner is the one port in the system (ADR-0003): everything needed
// to drive a Compose's containers via the real `docker compose` CLI. Down
// preserves volumes for Stop, while DownWithVolumes performs the destructive
// teardown used by Delete. It is declared here, in the consumer, and
// implemented for real in the leaf package `dockerexec`; tests use a fake.
type ComposeRunner interface {
	Up(ctx context.Context, req RunRequest, opts UpOptions) error
	Build(ctx context.Context, req RunRequest, opts BuildOptions) error
	Down(ctx context.Context, req RunRequest) error
	DownWithVolumes(ctx context.Context, req RunRequest) error
	Restart(ctx context.Context, req RunRequest) error
	Status(ctx context.Context, req RunRequest) (ComposeStatus, error)
	Logs(ctx context.Context, req RunRequest) (string, error)
	// Config is `docker compose config --format json`: the file as Compose
	// resolves it, interpolated and with inactive profiles dropped.
	Config(ctx context.Context, req RunRequest) ([]byte, error)
	Scale(ctx context.Context, req RunRequest, opts ScaleOptions) error
}

type Service struct {
	repository *repository
	runner     ComposeRunner
	dataDir    string
	domains    *domains.Store
	canceler   DeploymentCanceler
	now        func() time.Time
}

type repository struct {
	db *sql.DB
}

// Compose is the API representation of a Compose.
type Compose struct {
	DeployIdentity  *DeployIdentity `json:"deployIdentity"`
	IdentityVersion int             `json:"-"`
	ID              int64           `json:"id"`
	EnvironmentID   int64           `json:"environmentId"`
	Name            string          `json:"name"`
	ProjectSlug     string          `json:"projectSlug"`
	EnvSlug         string          `json:"envSlug"`
	YAMLContent     string          `json:"yamlContent"`
	CreatedAt       time.Time       `json:"createdAt"`
	UpdatedAt       time.Time       `json:"updatedAt"`
	// LastDeployedAt is a pointer so never-deployed stays distinct from every
	// real timestamp on the wire: nil serializes as JSON null, while even a
	// deploy at the Unix epoch is a non-nil time. Only a successful deploy
	// ever sets it.
	LastDeployedAt *time.Time `json:"lastDeployedAt"`
	// DomainsChangedAt is the last time one of the Compose's domains was
	// created, updated or deleted; nil when none ever was.
	DomainsChangedAt *time.Time `json:"domainsChangedAt"`
	// VariablesChangedAt is the last time one of the Compose's variables was
	// created, updated or deleted; nil when none ever was.
	VariablesChangedAt *time.Time    `json:"variablesChangedAt"`
	Status             ComposeStatus `json:"status"`
}

// DeployIdentity is derived from the same RunRequest lifecycle actions use. It
// lets the UI display the configured directory and both identity versions
// without rebuilding paths from mutable names or default configuration.
type DeployIdentity struct {
	ProjectName string `json:"projectName"`
	Directory   string `json:"directory"`
}

type composeCreateInput struct {
	Name        string  `json:"name"`
	YAMLContent *string `json:"yamlContent"`
}

type composeUpdateInput struct {
	YAMLContent *string `json:"yamlContent"`
}

// New constructs the Compose service over the application's migrated SQLite
// database. runner drives deploy lifecycle actions; dataDir is the root
// directory Compose files are resolved under (ADR-0002).
func New(db *sql.DB, runner ComposeRunner, dataDir string) *Service {
	return &Service{repository: &repository{db: db}, runner: runner, dataDir: dataDir, domains: domains.NewStore(db), now: time.Now}
}

// RegisterRoutes adds the authenticated Compose endpoints to mux.
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/projects/{projectID}/environments/{environmentID}/composes", s.list)
	mux.HandleFunc("POST /api/projects/{projectID}/environments/{environmentID}/composes", s.create)
	mux.HandleFunc("GET /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}", s.get)
	mux.HandleFunc("PUT /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}", s.update)
	mux.HandleFunc("POST /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/preview", s.preview)
	mux.HandleFunc("POST /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/stop", s.stop)
	mux.HandleFunc("POST /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/restart", s.restart)
	mux.HandleFunc("DELETE /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}", s.delete)
	mux.HandleFunc("GET /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/removal-plan", s.removalPlan)
	mux.HandleFunc("GET /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/logs", s.logs)
	mux.HandleFunc("GET /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/services/{service}/effective-variables", s.effectiveVariables)
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	if !s.available() {
		writeError(w, http.StatusInternalServerError, "composes are temporarily unavailable")
		return
	}

	projectID, environmentID, err := routeIDs(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.requireEnvironment(r.Context(), projectID, environmentID); err != nil {
		writeParentError(w, err)
		return
	}

	composes, err := s.repository.list(r.Context(), environmentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load composes")
		return
	}
	writeJSON(w, http.StatusOK, s.withStatuses(r.Context(), composes))
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	if !s.available() {
		writeError(w, http.StatusInternalServerError, "composes are temporarily unavailable")
		return
	}

	projectID, environmentID, err := routeIDs(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.requireEnvironment(r.Context(), projectID, environmentID); err != nil {
		writeParentError(w, err)
		return
	}

	var input composeCreateInput
	if err := decodeInput(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name, err := validateComposeName(input.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	siblings, err := s.repository.list(r.Context(), environmentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create compose")
		return
	}
	if slug.Conflicts(name, siblings, func(c Compose) string { return c.Name }) {
		writeError(w, http.StatusConflict, "a compose with a conflicting name already exists")
		return
	}
	yamlContent := ""
	if input.YAMLContent != nil {
		yamlContent = *input.YAMLContent
	}

	// The deploy identity (ADR-0005) is frozen from the parents' names once,
	// here; later renames never reach it.
	projectName, environmentName, err := s.repository.parentNames(r.Context(), projectID, environmentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create compose")
		return
	}

	now := s.now().UTC().Truncate(time.Second)
	compose, err := s.repository.create(r.Context(), environmentID, slug.Slugify(projectName), slug.Slugify(environmentName), name, yamlContent, now)
	if err != nil {
		if isUniqueConstraint(err) {
			writeError(w, http.StatusConflict, "a compose with that name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not create compose")
		return
	}
	writeJSON(w, http.StatusCreated, s.withStatus(r.Context(), compose))
}

func (s *Service) get(w http.ResponseWriter, r *http.Request) {
	if !s.available() {
		writeError(w, http.StatusInternalServerError, "composes are temporarily unavailable")
		return
	}

	projectID, environmentID, err := routeIDs(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	composeID, err := composeID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "compose id must be a positive integer")
		return
	}
	if err := s.requireEnvironment(r.Context(), projectID, environmentID); err != nil {
		writeParentError(w, err)
		return
	}

	compose, err := s.repository.findByID(r.Context(), environmentID, composeID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "compose not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load compose")
		return
	}
	writeJSON(w, http.StatusOK, s.withStatus(r.Context(), compose))
}

func (s *Service) update(w http.ResponseWriter, r *http.Request) {
	if !s.available() {
		writeError(w, http.StatusInternalServerError, "composes are temporarily unavailable")
		return
	}

	projectID, environmentID, err := routeIDs(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	composeID, err := composeID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "compose id must be a positive integer")
		return
	}
	if err := s.requireEnvironment(r.Context(), projectID, environmentID); err != nil {
		writeParentError(w, err)
		return
	}

	var input composeUpdateInput
	if err := decodeInput(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.YAMLContent == nil {
		writeError(w, http.StatusBadRequest, "yamlContent is required")
		return
	}

	existing, err := s.repository.findByID(r.Context(), environmentID, composeID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "compose not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load compose")
		return
	}

	updatedAt := s.now().UTC().Truncate(time.Second)
	if !updatedAt.After(existing.UpdatedAt) {
		updatedAt = existing.UpdatedAt.Add(time.Second)
	}

	if err := s.repository.update(r.Context(), environmentID, composeID, *input.YAMLContent, updatedAt); err != nil {
		writeError(w, http.StatusInternalServerError, "could not update compose")
		return
	}

	compose, err := s.repository.findByID(r.Context(), environmentID, composeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load updated compose")
		return
	}
	writeJSON(w, http.StatusOK, s.withStatus(r.Context(), compose))
}

// previewResponse is the file a deploy would write and the warnings it
// would return.
type previewResponse struct {
	YAML     string   `json:"yaml"`
	Warnings []string `json:"warnings"`
}

// preview resolves a draft of the Compose's YAML exactly as a deploy would,
// without saving it, writing files or calling Docker (compose-preview spec).
// The deploy-identity check is skipped: it is about the project name, not
// the file.
func (s *Service) preview(w http.ResponseWriter, r *http.Request) {
	if !s.available() {
		writeError(w, http.StatusInternalServerError, "composes are temporarily unavailable")
		return
	}

	projectID, environmentID, err := routeIDs(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := composeID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "compose id must be a positive integer")
		return
	}
	if err := s.requireEnvironment(r.Context(), projectID, environmentID); err != nil {
		writeParentError(w, err)
		return
	}

	var input composeUpdateInput
	if err := decodeInput(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.YAMLContent == nil {
		writeError(w, http.StatusBadRequest, "yamlContent is required")
		return
	}

	compose, err := s.repository.findByID(r.Context(), environmentID, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "compose not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load compose")
		return
	}
	if strings.TrimSpace(*input.YAMLContent) == "" {
		writeError(w, http.StatusBadRequest, "compose has no YAML content to deploy")
		return
	}

	req := runRequestFor(s.dataDir, compose)
	vars, err := s.repository.loadDeployVariables(r.Context(), compose, false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load variables")
		return
	}
	resolvedYAML, _, warnings, err := s.resolveForDeploy(r.Context(), projectID, compose, *input.YAMLContent, req.EnvFilePath, vars)
	if err != nil {
		writeDeployFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, previewResponse{YAML: resolvedYAML, Warnings: warnings})
}

// writeFiles creates the Compose directory and writes the files, returning
// the user-facing description of the step that failed.
func writeFiles(req RunRequest, files deployFiles) (string, error) {
	if err := os.MkdirAll(filepath.Dir(req.ComposeFilePath), composeDirPerm); err != nil {
		return "could not prepare compose files", err
	}
	// Order keeps every path in either the old or the new YAML on disk at each
	// step: set files are written first and nothing is deleted until the new
	// YAML is in place.
	dir := setsDir(req.EnvFilePath)
	if len(files.setFiles) > 0 {
		if err := os.MkdirAll(dir, composeDirPerm); err != nil {
			return "could not prepare variable set files", err
		}
		for id, content := range files.setFiles {
			if err := os.WriteFile(setEnvPath(req.EnvFilePath, id), []byte(content), envFilePerm); err != nil {
				return "could not write variable set file", err
			}
		}
	}
	if err := os.WriteFile(req.EnvFilePath, []byte(files.envFile), envFilePerm); err != nil {
		return "could not write environment file", err
	}
	if err := os.WriteFile(req.ComposeFilePath, []byte(files.composeYAML), composeFilePerm); err != nil {
		return "could not write compose file", err
	}
	// The sets directory then matches the resolved YAML: sets that are no
	// longer bound are removed, since they can hold secret values.
	if err := removeStaleSetFiles(dir, req.EnvFilePath, files.setFiles); err != nil {
		return "could not clear variable set files", err
	}
	return "", nil
}

// removeStaleSetFiles deletes every file in the sets directory that keep does
// not name, and the directory itself once it is empty.
func removeStaleSetFiles(dir, envFilePath string, keep map[int64]string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	wanted := make(map[string]bool, len(keep))
	for id := range keep {
		wanted[setEnvPath(envFilePath, id)] = true
	}
	remaining := 0
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if wanted[path] {
			remaining++
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	if remaining == 0 {
		return os.Remove(dir)
	}
	return nil
}

// LifecycleContext detaches Docker work that must not stop halfway from the
// request that started it: a browser that closes mid-Stop or mid-Delete would
// otherwise kill the docker CLI and leave a half-removed stack. The timeout
// keeps a hung Docker from holding the Project gate forever.
func LifecycleContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), 10*time.Minute)
}

func (s *Service) stop(w http.ResponseWriter, r *http.Request) {
	if !s.available() {
		writeError(w, http.StatusInternalServerError, "composes are temporarily unavailable")
		return
	}

	projectID, environmentID, err := routeIDs(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := composeID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "compose id must be a positive integer")
		return
	}
	if err := s.requireEnvironment(r.Context(), projectID, environmentID); err != nil {
		writeParentError(w, err)
		return
	}

	compose, req, ok := s.resolveDeployedCompose(w, r, environmentID, id)
	if !ok {
		return
	}

	ctx, cancel := LifecycleContext(r.Context())
	defer cancel()
	if err := s.runner.Down(ctx, req); err != nil {
		writeError(w, http.StatusBadGateway, "stop failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, s.withRefreshedStatus(r.Context(), req, compose))
}

func (s *Service) restart(w http.ResponseWriter, r *http.Request) {
	if !s.available() {
		writeError(w, http.StatusInternalServerError, "composes are temporarily unavailable")
		return
	}

	projectID, environmentID, err := routeIDs(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := composeID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "compose id must be a positive integer")
		return
	}
	if err := s.requireEnvironment(r.Context(), projectID, environmentID); err != nil {
		writeParentError(w, err)
		return
	}

	compose, req, ok := s.resolveDeployedCompose(w, r, environmentID, id)
	if !ok {
		return
	}

	ctx, cancel := LifecycleContext(r.Context())
	defer cancel()
	if err := s.runner.Restart(ctx, req); err != nil {
		writeError(w, http.StatusBadGateway, "restart failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, s.withRefreshedStatus(r.Context(), req, compose))
}

func (s *Service) delete(w http.ResponseWriter, r *http.Request) {
	if !s.available() {
		writeError(w, http.StatusInternalServerError, "composes are temporarily unavailable")
		return
	}

	projectID, environmentID, err := routeIDs(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := composeID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "compose id must be a positive integer")
		return
	}
	if err := s.requireEnvironment(r.Context(), projectID, environmentID); err != nil {
		writeParentError(w, err)
		return
	}

	compose, req, ok := s.loadComposeAndRunRequest(w, r, environmentID, id)
	if !ok {
		return
	}

	// From here the delete runs to the end even if the browser goes away.
	ctx, cancel := LifecycleContext(r.Context())
	defer cancel()
	s.cancelDeployments(ctx, id)
	if _, err := os.Stat(req.ComposeFilePath); err == nil {
		if _, _, err := s.destroyStack(ctx, compose, req, []int64{compose.ID}); err != nil {
			writeError(w, http.StatusBadGateway, "delete failed: "+err.Error())
			return
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusInternalServerError, "could not inspect compose files")
		return
	}

	deleted, err := s.repository.delete(ctx, environmentID, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete compose")
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "compose not found")
		return
	}

	// Best-effort: the row is already gone, so a leftover directory is a
	// disk leak, not an inconsistency — don't fail the request over it.
	if err := os.RemoveAll(filepath.Dir(req.ComposeFilePath)); err != nil {
		log.Printf("delete compose %d: remove files at %q: %v", id, filepath.Dir(req.ComposeFilePath), err)
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) logs(w http.ResponseWriter, r *http.Request) {
	if !s.available() {
		writeError(w, http.StatusInternalServerError, "composes are temporarily unavailable")
		return
	}

	projectID, environmentID, err := routeIDs(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := composeID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "compose id must be a positive integer")
		return
	}
	if err := s.requireEnvironment(r.Context(), projectID, environmentID); err != nil {
		writeParentError(w, err)
		return
	}

	_, req, ok := s.resolveDeployedCompose(w, r, environmentID, id)
	if !ok {
		return
	}

	logs, err := s.runner.Logs(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "logs failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"logs": logs})
}

// loadComposeAndRunRequest loads a Compose and its stored deploy identity
// (ADR-0005) — shared by every action that needs to locate a Compose's files,
// deployed or not. It writes any error response itself; the caller should
// return immediately when ok is false.
func (s *Service) loadComposeAndRunRequest(w http.ResponseWriter, r *http.Request, environmentID, id int64) (compose Compose, req RunRequest, ok bool) {
	compose, err := s.repository.findByID(r.Context(), environmentID, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "compose not found")
		return Compose{}, RunRequest{}, false
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load compose")
		return Compose{}, RunRequest{}, false
	}
	if err := s.requireUniqueIdentity(r.Context(), compose); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return Compose{}, RunRequest{}, false
	}
	return compose, runRequestFor(s.dataDir, compose), true
}

// resolveDeployedCompose loads a Compose and its run identity, gated on it
// already being deployed (its resolved compose file exists on disk) — shared
// by actions (stop/restart/logs) that are meaningless on an undeployed
// Compose. It writes any error response itself; the caller should return
// immediately when ok is false.
func (s *Service) resolveDeployedCompose(w http.ResponseWriter, r *http.Request, environmentID, id int64) (compose Compose, req RunRequest, ok bool) {
	compose, req, ok = s.loadComposeAndRunRequest(w, r, environmentID, id)
	if !ok {
		return Compose{}, RunRequest{}, false
	}
	if _, err := os.Stat(req.ComposeFilePath); err != nil {
		writeError(w, http.StatusBadRequest, "compose has not been deployed")
		return Compose{}, RunRequest{}, false
	}
	return compose, req, true
}

// ComposeRunRequest resolves one Compose's deploy identity for a sibling
// domain that must act on its files but owns neither the lookup nor the path
// derivation: the Project and Environment are checked exactly as the Compose
// routes check them, the row comes from repository.findByID and the identity
// from runRequestFor. A missing Project, Environment or Compose is reported
// as sql.ErrNoRows — including the lookup's own error, passed through
// unchanged — so the caller keeps a single 404 path.
func (s *Service) ComposeRunRequest(ctx context.Context, projectID, environmentID, composeID int64) (RunRequest, error) {
	if err := s.requireEnvironment(ctx, projectID, environmentID); err != nil {
		if errors.Is(err, errProjectNotFound) || errors.Is(err, errEnvironmentNotFound) {
			return RunRequest{}, sql.ErrNoRows
		}
		return RunRequest{}, err
	}
	compose, err := s.repository.findByID(ctx, environmentID, composeID)
	if err != nil {
		return RunRequest{}, err
	}
	if err := s.requireUniqueIdentity(ctx, compose); err != nil {
		return RunRequest{}, err
	}
	return runRequestFor(s.dataDir, compose), nil
}

// ServiceNames lists a Compose's Service names as its live status reports them
// — the same `docker compose ps --all` status the Compose page shows — for a
// sibling domain that needs the set but owns neither the runner nor the
// 404 path. It takes an already-resolved RunRequest so the caller shares one
// resolution with ComposeRunRequest.
//
// Like currentStatus, it only queries the runner once a Compose has actually
// been deployed (its resolved compose file exists on disk). An undeployed
// Compose has no Compose file for `docker compose ps` to read, so asking would
// fail; it lists no Services instead, which the UI renders as its empty state.
func (s *Service) ServiceNames(ctx context.Context, req RunRequest) ([]string, error) {
	if _, err := os.Stat(req.ComposeFilePath); err != nil {
		return nil, nil
	}
	status, err := s.runner.Status(ctx, req)
	if err != nil {
		return nil, err
	}
	return status.Services, nil
}

// withRefreshedStatus re-reads a Compose's live status immediately after a
// runner action (deploy/stop/restart) so the response reflects the outcome
// without a separate round-trip; best-effort like withStatus/withStatuses.
func (s *Service) withRefreshedStatus(ctx context.Context, req RunRequest, compose Compose) Compose {
	compose = s.withDeployIdentity(compose)
	status, err := s.runner.Status(ctx, req)
	if err != nil {
		status = ComposeStatus{}
	}
	compose.Status = status
	return compose
}

// withStatus and withStatuses attach each Compose's live container state.
// They are best-effort: a runner error (e.g. Docker is unreachable) must not
// fail an otherwise-successful read, so it degrades to a zero-value status.
func (s *Service) withStatus(ctx context.Context, compose Compose) Compose {
	compose = s.withDeployIdentity(compose)
	compose.Status = s.currentStatus(ctx, compose)
	return compose
}

func (s *Service) withStatuses(ctx context.Context, composes []Compose) []Compose {
	for i := range composes {
		composes[i] = s.withStatus(ctx, composes[i])
	}
	return composes
}

func (s *Service) withDeployIdentity(compose Compose) Compose {
	req := runRequestFor(s.dataDir, compose)
	compose.DeployIdentity = &DeployIdentity{ProjectName: req.ProjectName, Directory: filepath.Dir(req.ComposeFilePath)}
	return compose
}

// currentStatus only queries the runner once a Compose has actually been
// deployed (its resolved compose file exists on disk); an undeployed Compose
// always reads as a zero-value status without shelling out.
func (s *Service) currentStatus(ctx context.Context, compose Compose) ComposeStatus {
	if err := s.requireUniqueIdentity(ctx, compose); err != nil {
		return ComposeStatus{}
	}
	req := runRequestFor(s.dataDir, compose)
	if _, err := os.Stat(req.ComposeFilePath); err != nil {
		return ComposeStatus{}
	}
	status, err := s.runner.Status(ctx, req)
	if err != nil {
		return ComposeStatus{}
	}
	return status
}

func (s *Service) requireEnvironment(ctx context.Context, projectID, environmentID int64) error {
	exists, err := s.repository.projectExists(ctx, projectID)
	if err != nil {
		return err
	}
	if !exists {
		return errProjectNotFound
	}

	exists, err = s.repository.environmentExists(ctx, projectID, environmentID)
	if err != nil {
		return err
	}
	if !exists {
		return errEnvironmentNotFound
	}
	return nil
}

func (s *Service) available() bool {
	return s != nil && s.repository != nil && s.repository.db != nil && s.runner != nil
}

var (
	errProjectNotFound     = errors.New("project not found")
	errEnvironmentNotFound = errors.New("environment not found")
)

func writeParentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errProjectNotFound):
		writeError(w, http.StatusNotFound, "project not found")
	case errors.Is(err, errEnvironmentNotFound):
		writeError(w, http.StatusNotFound, "environment not found")
	default:
		writeError(w, http.StatusInternalServerError, "could not load environment")
	}
}

func (r *repository) projectExists(ctx context.Context, id int64) (bool, error) {
	var one int
	err := r.db.QueryRowContext(ctx, "SELECT 1 FROM projects WHERE id = ?", id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (r *repository) environmentExists(ctx context.Context, projectID, environmentID int64) (bool, error) {
	var one int
	err := r.db.QueryRowContext(ctx, `
		SELECT 1
		FROM environments
		WHERE project_id = ? AND id = ?
	`, projectID, environmentID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// parentNames resolves the current Project and Environment names: a new
// Compose's deploy identity is frozen from them at creation (ADR-0005), and
// every deploy builds the readable relist-network alias from them (ADR-0014).
func (r *repository) parentNames(ctx context.Context, projectID, environmentID int64) (projectName, environmentName string, err error) {
	err = r.db.QueryRowContext(ctx, `
		SELECT p.name, e.name
		FROM environments e
		JOIN projects p ON p.id = e.project_id
		WHERE e.id = ? AND e.project_id = ?
	`, environmentID, projectID).Scan(&projectName, &environmentName)
	return projectName, environmentName, err
}

type environmentVariable struct {
	Key    string
	Value  string
	Secret bool
}

func (r *repository) listEnvironmentVariables(ctx context.Context, environmentID int64, withValues bool) ([]environmentVariable, error) {
	return r.listVariables(ctx, "environment_variables", "environment_id", environmentID, withValues)
}

func (r *repository) listComposeVariables(ctx context.Context, composeID int64, withValues bool) ([]environmentVariable, error) {
	return r.listVariables(ctx, "compose_variables", "compose_id", composeID, withValues)
}

// listVariables reads one scope's variables in ID order. Without withValues
// the value column is never selected, so a caller that only needs the keys
// (preview) cannot expose a value.
func (r *repository) listVariables(ctx context.Context, table, scopeColumn string, scopeID int64, withValues bool) ([]environmentVariable, error) {
	valueColumn := "''"
	if withValues {
		valueColumn = "value"
	}
	// table, scopeColumn and valueColumn are constants from this file.
	rows, err := r.db.QueryContext(ctx, "SELECT key, "+valueColumn+", secret FROM "+table+" WHERE "+scopeColumn+" = ? ORDER BY id ASC", scopeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	variables := make([]environmentVariable, 0)
	for rows.Next() {
		var variable environmentVariable
		if err := rows.Scan(&variable.Key, &variable.Value, &variable.Secret); err != nil {
			return nil, err
		}
		variables = append(variables, variable)
	}
	return variables, rows.Err()
}

// BackfillLegacyIdentities freezes the deploy identity of composes created
// before identity was stored on the row (ADR-0005): it fills any empty slugs
// from the parents' current names, which is what those stacks were deployed
// under. It only ever fills empty slugs, so it is idempotent and never
// follows later renames. It runs once at startup, after the migrations.
func BackfillLegacyIdentities(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `
		SELECT c.id, p.name, e.name
		FROM composes c
		JOIN environments e ON e.id = c.environment_id
		JOIN projects p ON p.id = e.project_id
		WHERE c.project_slug = '' OR c.env_slug = ''
	`)
	if err != nil {
		return fmt.Errorf("load composes without a stored identity: %w", err)
	}

	type legacyRow struct {
		id          int64
		projectSlug string
		envSlug     string
	}
	var legacyRows []legacyRow
	for rows.Next() {
		var row legacyRow
		var projectName, environmentName string
		if err := rows.Scan(&row.id, &projectName, &environmentName); err != nil {
			rows.Close()
			return fmt.Errorf("scan composes without a stored identity: %w", err)
		}
		row.projectSlug = slug.Slugify(projectName)
		row.envSlug = slug.Slugify(environmentName)
		legacyRows = append(legacyRows, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate composes without a stored identity: %w", err)
	}
	rows.Close()

	// The queries must finish before any write: SQLite runs on a single
	// connection, so an UPDATE issued while rows are open would deadlock.
	for _, row := range legacyRows {
		if _, err := db.ExecContext(ctx, `
			UPDATE composes
			SET project_slug = ?, env_slug = ?
			WHERE id = ?
		`, row.projectSlug, row.envSlug, row.id); err != nil {
			return fmt.Errorf("store legacy identity for compose %d: %w", row.id, err)
		}
	}
	return nil
}

func (r *repository) list(ctx context.Context, environmentID int64) ([]Compose, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, environment_id, name, project_slug, env_slug, yaml_content, created_at, updated_at, deployed_at, identity_version, domains_changed_at, variables_changed_at
		FROM composes
		WHERE environment_id = ?
		ORDER BY id ASC
	`, environmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	composes := make([]Compose, 0)
	for rows.Next() {
		compose, err := scanComposeValues(rows.Scan)
		if err != nil {
			return nil, err
		}
		composes = append(composes, compose)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return composes, nil
}

func (r *repository) create(ctx context.Context, environmentID int64, projectSlug, envSlug, name, yamlContent string, createdAt time.Time) (Compose, error) {
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO composes (environment_id, name, project_slug, env_slug, yaml_content, created_at, updated_at, identity_version)
		VALUES (?, ?, ?, ?, ?, ?, ?, 1)
	`, environmentID, name, projectSlug, envSlug, yamlContent, createdAt.Unix(), createdAt.Unix())
	if err != nil {
		return Compose{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Compose{}, err
	}
	return r.findByID(ctx, environmentID, id)
}

func (r *repository) findByID(ctx context.Context, environmentID, id int64) (Compose, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, environment_id, name, project_slug, env_slug, yaml_content, created_at, updated_at, deployed_at, identity_version, domains_changed_at, variables_changed_at
		FROM composes
		WHERE environment_id = ? AND id = ?
	`, environmentID, id)
	return scanComposeValues(row.Scan)
}

// markDeployed records a successful deploy's time on the Compose row. It is
// deployed_at's only writer: called after the runner reports success, never
// by stop, restart, save or delete — so a stopped Compose keeps the timestamp
// that says it has been deployed before.
func (r *repository) markDeployed(ctx context.Context, environmentID, id int64, deployedAt time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE composes
		SET deployed_at = ?
		WHERE environment_id = ? AND id = ?
	`, deployedAt.Unix(), environmentID, id)
	return err
}

func (r *repository) update(ctx context.Context, environmentID, id int64, yamlContent string, updatedAt time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE composes
		SET yaml_content = ?, updated_at = ?
		WHERE environment_id = ? AND id = ?
	`, yamlContent, updatedAt.Unix(), environmentID, id)
	return err
}

func (r *repository) delete(ctx context.Context, environmentID, id int64) (bool, error) {
	result, err := r.db.ExecContext(ctx, `
		DELETE FROM composes
		WHERE environment_id = ? AND id = ?
	`, environmentID, id)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

func scanComposeValues(scan func(...any) error) (Compose, error) {
	var (
		compose    Compose
		createdAt  int64
		updatedAt  int64
		deployedAt sql.NullInt64
		changedAt  sql.NullInt64
		varsAt     sql.NullInt64
	)
	if err := scan(&compose.ID, &compose.EnvironmentID, &compose.Name, &compose.ProjectSlug, &compose.EnvSlug, &compose.YAMLContent, &createdAt, &updatedAt, &deployedAt, &compose.IdentityVersion, &changedAt, &varsAt); err != nil {
		return Compose{}, err
	}
	compose.CreatedAt = time.Unix(createdAt, 0).UTC()
	compose.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	if deployedAt.Valid {
		at := time.Unix(deployedAt.Int64, 0).UTC()
		compose.LastDeployedAt = &at
	}
	if changedAt.Valid {
		at := time.Unix(changedAt.Int64, 0).UTC()
		compose.DomainsChangedAt = &at
	}
	if varsAt.Valid {
		at := time.Unix(varsAt.Int64, 0).UTC()
		compose.VariablesChangedAt = &at
	}
	return compose, nil
}

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

func composeID(r *http.Request) (int64, error) {
	return parsePositiveID(r.PathValue("composeID"))
}

func parsePositiveID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("id must be a positive integer")
	}
	return id, nil
}

func decodeInput(w http.ResponseWriter, r *http.Request, input any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(input); err != nil {
		return errors.New("request must contain a valid JSON object")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("request must contain one JSON object")
	}
	return nil
}

func validateComposeName(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", errors.New("compose name is required")
	}
	if utf8.RuneCountInString(trimmed) > maxComposeName {
		return "", errors.New("compose name must be 255 characters or fewer")
	}
	return trimmed, nil
}

// DeployProjectName is the Compose's immutable Docker identity (ADR-0011):
// new rows use their database ID; legacy rows retain their frozen slugs. Metrics
// collection uses this same identity to attribute containers to Composes.
func (c Compose) DeployProjectName() string {
	if c.IdentityVersion == 1 {
		return "relist-c" + strconv.FormatInt(c.ID, 10)
	}
	return c.ProjectSlug + "-" + c.EnvSlug + "-" + slug.Slugify(c.Name)
}

// runRequestFor resolves the immutable name and paths for either identity
// version (ADR-0011). Legacy files stay at their original slug paths; new
// Composes use the separate .composes/<ID> namespace.
func runRequestFor(dataDir string, compose Compose) RunRequest {
	composeSlug := slug.Slugify(compose.Name)
	dir := filepath.Join(dataDir, compose.ProjectSlug, compose.EnvSlug, composeSlug)
	if compose.IdentityVersion == 1 {
		dir = filepath.Join(dataDir, ".composes", strconv.FormatInt(compose.ID, 10))
	}
	return RunRequest{
		ProjectName:     compose.DeployProjectName(),
		ComposeFilePath: filepath.Join(dir, composeFileName),
		EnvFilePath:     filepath.Join(dir, envFileName),
	}
}

// resolveCompose parses yamlContent and returns it with the generated
// environment file injected into every service (ADR-0002) — the one place the
// panel modifies user-authored YAML. The Compose's domains become Traefik
// labels, and only the Services that need the shared network join it, with
// their aliases (ADR-0014). Each Service's bound Variable Sets follow the
// panel's env file, in binding order; the IDs of the sets used are returned.
// The warnings say what that overrode or skipped. A *domains.TargetError is
// returned as is.
func resolveCompose(yamlContent, networkName, envFilePath string, composeID int64, ds []domains.Domain, id networkIdentity, vars deployVariables) (string, []int64, []string, error) {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(yamlContent), &doc); err != nil {
		return "", nil, nil, fmt.Errorf("parse compose yaml: %w", err)
	}
	if doc == nil {
		doc = map[string]any{}
	}

	var usedSets []int64
	services, ok := doc["services"].(map[string]any)
	if ok {
		for name, raw := range services {
			service, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			var setPaths []string
			for _, set := range vars.setsFor(name) {
				setPaths = append(setPaths, setEnvPath(envFilePath, set.ID))
				usedSets = append(usedSets, set.ID)
			}
			service["env_file"] = addSetFiles(addToEnvFile(service["env_file"], envFilePath), setPaths)
			services[name] = service
		}
	} else {
		services = map[string]any{}
	}
	// Who joins is read from the YAML as written, before the domains add
	// their own Traefik labels.
	joins := sharedNetworkJoins(services, networkName, ds)
	// With no services at all, a domain still has to be refused.
	warnings, err := domains.Inject(services, composeID, ds, networkName, nil)
	if err != nil {
		return "", nil, nil, err
	}
	warnings = append(warnings, joinSharedNetwork(services, joins, networkName, id)...)
	warnings = append(warnings, variableWarnings(yamlContent, services, vars)...)

	networks, ok := doc["networks"].(map[string]any)
	if !ok {
		networks = map[string]any{}
	}
	networks[networkName] = map[string]any{"external": true}
	doc["networks"] = networks

	resolved, err := yaml.Marshal(doc)
	if err != nil {
		return "", nil, nil, fmt.Errorf("marshal resolved compose yaml: %w", err)
	}
	return string(resolved), usedSets, warnings, nil
}

func addToEnvFile(existing any, envFilePath string) any {
	switch value := existing.(type) {
	case []any:
		return appendUnique(value, envFilePath)
	case string:
		if value == envFilePath {
			return value
		}
		return []any{value, envFilePath}
	default:
		return []any{envFilePath}
	}
}

// appendUnique appends value to a YAML flow sequence unless it is already
// present as a string entry.
func appendUnique(sequence []any, value string) []any {
	for _, item := range sequence {
		if entry, ok := item.(string); ok && entry == value {
			return sequence
		}
	}
	return append(sequence, value)
}

func isUniqueConstraint(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// ComposeDirs lists the directory of every Compose row, legacy identities
// included, resolved the same way a deploy resolves it.
func ComposeDirs(ctx context.Context, db *sql.DB, dataDir string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, project_slug, env_slug, name, identity_version
		FROM composes
	`)
	if err != nil {
		return nil, fmt.Errorf("list compose directories: %w", err)
	}
	defer rows.Close()

	var dirs []string
	for rows.Next() {
		var compose Compose
		if err := rows.Scan(&compose.ID, &compose.ProjectSlug, &compose.EnvSlug, &compose.Name, &compose.IdentityVersion); err != nil {
			return nil, fmt.Errorf("scan compose directory: %w", err)
		}
		dirs = append(dirs, ComposeDir(runRequestFor(dataDir, compose)))
	}
	return dirs, rows.Err()
}
