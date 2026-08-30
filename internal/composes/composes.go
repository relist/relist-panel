// Package composes owns Compose metadata and its HTTP API.
package composes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxRequestBody = 1 << 20
	maxComposeName = 255
)

type Service struct {
	repository *repository
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
	YAMLContent   string    `json:"yamlContent"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type composeCreateInput struct {
	Name        string  `json:"name"`
	YAMLContent *string `json:"yamlContent"`
}

type composeUpdateInput struct {
	YAMLContent *string `json:"yamlContent"`
}

// New constructs the Compose service over the application's migrated SQLite
// database.
func New(db *sql.DB) *Service {
	return &Service{repository: &repository{db: db}, now: time.Now}
}

// RegisterRoutes adds the authenticated Compose endpoints to mux.
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/projects/{projectID}/environments/{environmentID}/composes", s.list)
	mux.HandleFunc("POST /api/projects/{projectID}/environments/{environmentID}/composes", s.create)
	mux.HandleFunc("GET /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}", s.get)
	mux.HandleFunc("PUT /api/projects/{projectID}/environments/{environmentID}/composes/{composeID}", s.update)
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
	writeJSON(w, http.StatusOK, composes)
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
	yamlContent := ""
	if input.YAMLContent != nil {
		yamlContent = *input.YAMLContent
	}

	now := s.now().UTC().Truncate(time.Second)
	compose, err := s.repository.create(r.Context(), environmentID, name, yamlContent, now)
	if err != nil {
		if isUniqueConstraint(err) {
			writeError(w, http.StatusConflict, "a compose with that name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not create compose")
		return
	}
	writeJSON(w, http.StatusCreated, compose)
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
	writeJSON(w, http.StatusOK, compose)
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
	writeJSON(w, http.StatusOK, compose)
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
	return s != nil && s.repository != nil && s.repository.db != nil
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

func (r *repository) list(ctx context.Context, environmentID int64) ([]Compose, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, environment_id, name, yaml_content, created_at, updated_at
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

func (r *repository) create(ctx context.Context, environmentID int64, name, yamlContent string, createdAt time.Time) (Compose, error) {
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO composes (environment_id, name, yaml_content, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
	`, environmentID, name, yamlContent, createdAt.Unix(), createdAt.Unix())
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
		SELECT id, environment_id, name, yaml_content, created_at, updated_at
		FROM composes
		WHERE environment_id = ? AND id = ?
	`, environmentID, id)
	return scanComposeValues(row.Scan)
}

func (r *repository) update(ctx context.Context, environmentID, id int64, yamlContent string, updatedAt time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE composes
		SET yaml_content = ?, updated_at = ?
		WHERE environment_id = ? AND id = ?
	`, yamlContent, updatedAt.Unix(), environmentID, id)
	return err
}

func scanComposeValues(scan func(...any) error) (Compose, error) {
	var (
		compose   Compose
		createdAt int64
		updatedAt int64
	)
	if err := scan(&compose.ID, &compose.EnvironmentID, &compose.Name, &compose.YAMLContent, &createdAt, &updatedAt); err != nil {
		return Compose{}, err
	}
	compose.CreatedAt = time.Unix(createdAt, 0).UTC()
	compose.UpdatedAt = time.Unix(updatedAt, 0).UTC()
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
