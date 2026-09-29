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

	"relist-panel/internal/dockernet"
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

// ComposeStatus summarizes a Compose's live container state.
type ComposeStatus struct {
	Running int `json:"running"`
	Total   int `json:"total"`
}

// ComposeRunner is the one port in the system (ADR-0003): everything needed
// to drive a Compose's containers via the real `docker compose` CLI. Down
// preserves volumes for Stop, while DownWithVolumes performs the destructive
// teardown used by Delete. It is declared here, in the consumer, and
// implemented for real in the leaf package `dockerexec`; tests use a fake.
type ComposeRunner interface {
	Up(ctx context.Context, req RunRequest) error
	Down(ctx context.Context, req RunRequest) error
	DownWithVolumes(ctx context.Context, req RunRequest) error
	Restart(ctx context.Context, req RunRequest) error
	Status(ctx context.Context, req RunRequest) (ComposeStatus, error)
	Logs(ctx context.Context, req RunRequest) (string, error)
}

type Service struct {
	repository *repository
	runner     ComposeRunner
	dataDir    string
	now        func() time.Time
}

type repository struct {
	db *sql.DB
}

// Compose is the API representation of a Compose.
type Compose struct {
	ID            int64     `json:"id"`
	EnvironmentID int64     `json:"environmentId"`
	Name          string    `json:"name"`
	ProjectSlug   string    `json:"projectSlug"`
	EnvSlug       string    `json:"envSlug"`
	YAMLContent   string    `json:"yamlContent"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
	// LastDeployedAt is a pointer so never-deployed stays distinct from every
	// real timestamp on the wire: nil serializes as JSON null, while even a
	// deploy at the Unix epoch is a non-nil time. Only a successful deploy
	// ever sets it.
	LastDeployedAt *time.Time    `json:"lastDeployedAt"`
	Status         ComposeStatus `json:"status"`
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
	return &Service{repository: &repository{db: db}, runner: runner, dataDir: dataDir, now: time.Now}
}

// RegisterRoutes adds the authenticated Compose endpoints to mux.
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/projects/{projectID}/environments/{environmentID}/composes", s.list)
	mux.HandleFunc("POST /api/projects/{projectID}/environments/{environmentID}/composes", s.create)
	mux.HandleFunc("GET /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}", s.get)
	mux.HandleFunc("PUT /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}", s.update)
	mux.HandleFunc("POST /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/deploy", s.deploy)
	mux.HandleFunc("POST /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/stop", s.stop)
	mux.HandleFunc("POST /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/restart", s.restart)
	mux.HandleFunc("DELETE /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}", s.delete)
	mux.HandleFunc("GET /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/logs", s.logs)
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

func (s *Service) deploy(w http.ResponseWriter, r *http.Request) {
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

	compose, err := s.repository.findByID(r.Context(), environmentID, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "compose not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load compose")
		return
	}
	if strings.TrimSpace(compose.YAMLContent) == "" {
		writeError(w, http.StatusBadRequest, "compose has no YAML content to deploy")
		return
	}

	req := runRequestFor(s.dataDir, compose)
	variables, err := s.repository.listEnvironmentVariables(r.Context(), environmentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load environment variables")
		return
	}

	if err := os.MkdirAll(filepath.Dir(req.ComposeFilePath), composeDirPerm); err != nil {
		writeError(w, http.StatusInternalServerError, "could not prepare compose files")
		return
	}
	resolvedYAML, err := resolveCompose(compose.YAMLContent, dockernet.NetworkName, req.EnvFilePath)
	if err != nil {
		writeError(w, http.StatusBadRequest, "compose yaml is invalid: "+err.Error())
		return
	}
	if err := os.WriteFile(req.ComposeFilePath, []byte(resolvedYAML), composeFilePerm); err != nil {
		writeError(w, http.StatusInternalServerError, "could not write compose file")
		return
	}
	if err := os.WriteFile(req.EnvFilePath, []byte(renderEnvFile(variables)), envFilePerm); err != nil {
		writeError(w, http.StatusInternalServerError, "could not write environment file")
		return
	}

	if err := s.runner.Up(r.Context(), req); err != nil {
		writeError(w, http.StatusBadGateway, "deploy failed: "+err.Error())
		return
	}

	// The deploy timestamp records success only (deploy-timestamps spec): it
	// lands after the runner returns without error, and no other action ever
	// writes it — "has deployed" and "is running" stay two different facts.
	deployedAt := s.now().UTC().Truncate(time.Second)
	if err := s.repository.markDeployed(r.Context(), environmentID, id, deployedAt); err != nil {
		writeError(w, http.StatusInternalServerError, "could not record deploy time")
		return
	}
	compose, err = s.repository.findByID(r.Context(), environmentID, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load compose")
		return
	}

	writeJSON(w, http.StatusOK, s.withRefreshedStatus(r.Context(), req, compose))
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

	if err := s.runner.Down(r.Context(), req); err != nil {
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

	if err := s.runner.Restart(r.Context(), req); err != nil {
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

	_, req, ok := s.loadComposeAndRunRequest(w, r, environmentID, id)
	if !ok {
		return
	}

	if _, err := os.Stat(req.ComposeFilePath); err == nil {
		if err := s.runner.DownWithVolumes(r.Context(), req); err != nil {
			writeError(w, http.StatusBadGateway, "delete failed: "+err.Error())
			return
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusInternalServerError, "could not inspect compose files")
		return
	}

	deleted, err := s.repository.delete(r.Context(), environmentID, id)
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
	return runRequestFor(s.dataDir, compose), nil
}

// withRefreshedStatus re-reads a Compose's live status immediately after a
// runner action (deploy/stop/restart) so the response reflects the outcome
// without a separate round-trip; best-effort like withStatus/withStatuses.
func (s *Service) withRefreshedStatus(ctx context.Context, req RunRequest, compose Compose) Compose {
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
	compose.Status = s.currentStatus(ctx, compose)
	return compose
}

func (s *Service) withStatuses(ctx context.Context, composes []Compose) []Compose {
	for i := range composes {
		composes[i].Status = s.currentStatus(ctx, composes[i])
	}
	return composes
}

// currentStatus only queries the runner once a Compose has actually been
// deployed (its resolved compose file exists on disk); an undeployed Compose
// always reads as a zero-value status without shelling out.
func (s *Service) currentStatus(ctx context.Context, compose Compose) ComposeStatus {
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

// parentNames resolves the Project and Environment names a Compose's deploy
// identity is frozen from at creation (ADR-0005).
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
	Key   string
	Value string
}

func (r *repository) listEnvironmentVariables(ctx context.Context, environmentID int64) ([]environmentVariable, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT key, value
		FROM environment_variables
		WHERE environment_id = ?
		ORDER BY id ASC
	`, environmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	variables := make([]environmentVariable, 0)
	for rows.Next() {
		var variable environmentVariable
		if err := rows.Scan(&variable.Key, &variable.Value); err != nil {
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
		SELECT id, environment_id, name, project_slug, env_slug, yaml_content, created_at, updated_at, deployed_at
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
		INSERT INTO composes (environment_id, name, project_slug, env_slug, yaml_content, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
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
		SELECT id, environment_id, name, project_slug, env_slug, yaml_content, created_at, updated_at, deployed_at
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
	)
	if err := scan(&compose.ID, &compose.EnvironmentID, &compose.Name, &compose.ProjectSlug, &compose.EnvSlug, &compose.YAMLContent, &createdAt, &updatedAt, &deployedAt); err != nil {
		return Compose{}, err
	}
	compose.CreatedAt = time.Unix(createdAt, 0).UTC()
	compose.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	if deployedAt.Valid {
		at := time.Unix(deployedAt.Int64, 0).UTC()
		compose.LastDeployedAt = &at
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

// DeployProjectName is the Compose's frozen `docker compose -p` project name
// (ADR-0005), formed from the slugs stored on its row — the identity metrics
// collection also uses to attribute containers to Composes.
func (c Compose) DeployProjectName() string {
	return c.ProjectSlug + "-" + c.EnvSlug + "-" + slug.Slugify(c.Name)
}

// runRequestFor computes a Compose's deploy identity from the slugs stored on
// its row (ADR-0005): the `docker compose -p` project name and the paths its
// resolved files live at under dataDir (ADR-0002). The compose name slugifies
// here because the row stores the display name only.
func runRequestFor(dataDir string, compose Compose) RunRequest {
	composeSlug := slug.Slugify(compose.Name)
	dir := filepath.Join(dataDir, compose.ProjectSlug, compose.EnvSlug, composeSlug)
	return RunRequest{
		ProjectName:     compose.DeployProjectName(),
		ComposeFilePath: filepath.Join(dir, composeFileName),
		EnvFilePath:     filepath.Join(dir, envFileName),
	}
}

// resolveCompose parses yamlContent and returns it with the shared Traefik
// network and the generated environment file injected into every service
// (ADR-0002) — the one place the panel modifies user-authored YAML.
func resolveCompose(yamlContent, networkName, envFilePath string) (string, error) {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(yamlContent), &doc); err != nil {
		return "", fmt.Errorf("parse compose yaml: %w", err)
	}
	if doc == nil {
		doc = map[string]any{}
	}

	if services, ok := doc["services"].(map[string]any); ok {
		for name, raw := range services {
			service, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			service["networks"] = addToNetworks(service["networks"], networkName)
			service["env_file"] = addToEnvFile(service["env_file"], envFilePath)
			services[name] = service
		}
	}

	networks, ok := doc["networks"].(map[string]any)
	if !ok {
		networks = map[string]any{}
	}
	networks[networkName] = map[string]any{"external": true}
	doc["networks"] = networks

	resolved, err := yaml.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("marshal resolved compose yaml: %w", err)
	}
	return string(resolved), nil
}

func addToNetworks(existing any, networkName string) any {
	switch value := existing.(type) {
	case []any:
		return appendUnique(value, networkName)
	case map[string]any:
		if _, ok := value[networkName]; !ok {
			value[networkName] = nil
		}
		return value
	default:
		return []any{networkName}
	}
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

func renderEnvFile(variables []environmentVariable) string {
	var b strings.Builder
	for _, variable := range variables {
		b.WriteString(variable.Key)
		b.WriteByte('=')
		b.WriteString(variable.Value)
		b.WriteByte('\n')
	}
	return b.String()
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
