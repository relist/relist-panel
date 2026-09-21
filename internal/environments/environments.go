// Package environments owns Environment metadata and its HTTP API.
package environments

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

	"relist-panel/internal/slug"
)

const (
	maxRequestBody     = 1 << 20
	maxEnvironmentName = 255
)

type Service struct {
	repository *repository
	now        func() time.Time
}

type repository struct {
	db *sql.DB
}

// Environment is the API representation of an Environment.
type Environment struct {
	ID        int64     `json:"id"`
	ProjectID int64     `json:"projectId"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
}

type environmentInput struct {
	Name string `json:"name"`
}

// New constructs the Environment service over the application's migrated
// SQLite database.
func New(db *sql.DB) *Service {
	return &Service{repository: &repository{db: db}, now: time.Now}
}

// RegisterRoutes adds the authenticated Environment endpoints to mux.
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/projects/{projectID}/environments", s.list)
	mux.HandleFunc("POST /api/projects/{projectID}/environments", s.create)
	mux.HandleFunc("DELETE /api/projects/{projectID}/environments/{id}", s.delete)
}

// ProvisionDefault creates the Environment required for a newly-created
// Project. The caller owns the transaction so Project and Environment
// creation can commit or roll back together.
func ProvisionDefault(ctx context.Context, tx *sql.Tx, projectID int64, createdAt time.Time) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO environments (project_id, name, created_at)
		VALUES (?, 'production', ?)
	`, projectID, createdAt.Unix())
	return err
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	if !s.available() {
		writeError(w, http.StatusInternalServerError, "environments are temporarily unavailable")
		return
	}

	projectID, err := projectID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "project id must be a positive integer")
		return
	}
	if err := s.requireProject(r.Context(), projectID); err != nil {
		writeProjectError(w, err)
		return
	}

	environments, err := s.repository.list(r.Context(), projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load environments")
		return
	}

	writeJSON(w, http.StatusOK, environments)
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	if !s.available() {
		writeError(w, http.StatusInternalServerError, "environments are temporarily unavailable")
		return
	}

	projectID, err := projectID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "project id must be a positive integer")
		return
	}
	if err := s.requireProject(r.Context(), projectID); err != nil {
		writeProjectError(w, err)
		return
	}

	input, err := decodeEnvironmentInput(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateEnvironmentName(input.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	name := strings.TrimSpace(input.Name)
	siblings, err := s.repository.list(r.Context(), projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create environment")
		return
	}
	if slug.Conflicts(name, siblings, func(e Environment) string { return e.Name }) {
		writeError(w, http.StatusConflict, "an environment with a conflicting name already exists")
		return
	}

	environment, err := s.repository.create(r.Context(), projectID, name, s.now().UTC())
	if err != nil {
		if isUniqueConstraint(err) {
			writeError(w, http.StatusConflict, "an environment with that name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not create environment")
		return
	}

	writeJSON(w, http.StatusCreated, environment)
}

func (s *Service) delete(w http.ResponseWriter, r *http.Request) {
	if !s.available() {
		writeError(w, http.StatusInternalServerError, "environments are temporarily unavailable")
		return
	}

	projectID, err := projectID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "project id must be a positive integer")
		return
	}
	environmentID, err := environmentID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "environment id must be a positive integer")
		return
	}
	if err := s.requireProject(r.Context(), projectID); err != nil {
		writeProjectError(w, err)
		return
	}

	deleted, err := s.repository.delete(r.Context(), projectID, environmentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete environment")
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "environment not found")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) requireProject(ctx context.Context, id int64) error {
	exists, err := s.repository.projectExists(ctx, id)
	if err != nil {
		return err
	}
	if !exists {
		return errProjectNotFound
	}
	return nil
}

func (s *Service) available() bool {
	return s != nil && s.repository != nil && s.repository.db != nil
}

var errProjectNotFound = errors.New("project not found")

func writeProjectError(w http.ResponseWriter, err error) {
	if errors.Is(err, errProjectNotFound) {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	writeError(w, http.StatusInternalServerError, "could not load project")
}

func (r *repository) projectExists(ctx context.Context, id int64) (bool, error) {
	var one int
	err := r.db.QueryRowContext(ctx, "SELECT 1 FROM projects WHERE id = ?", id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (r *repository) list(ctx context.Context, projectID int64) ([]Environment, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, project_id, name, created_at
		FROM environments
		WHERE project_id = ?
		ORDER BY CASE WHEN name = 'production' THEN 0 ELSE 1 END, id ASC
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	environments := make([]Environment, 0)
	for rows.Next() {
		environment, err := scanEnvironmentValues(rows.Scan)
		if err != nil {
			return nil, err
		}
		environments = append(environments, environment)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return environments, nil
}

func (r *repository) create(ctx context.Context, projectID int64, name string, createdAt time.Time) (Environment, error) {
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO environments (project_id, name, created_at)
		VALUES (?, ?, ?)
	`, projectID, name, createdAt.Unix())
	if err != nil {
		return Environment{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Environment{}, err
	}
	return r.findByID(ctx, projectID, id)
}

func (r *repository) findByID(ctx context.Context, projectID, id int64) (Environment, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, project_id, name, created_at
		FROM environments
		WHERE project_id = ? AND id = ?
	`, projectID, id)
	return scanEnvironmentValues(row.Scan)
}

func (r *repository) delete(ctx context.Context, projectID, id int64) (bool, error) {
	result, err := r.db.ExecContext(ctx, "DELETE FROM environments WHERE project_id = ? AND id = ?", projectID, id)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

func scanEnvironmentValues(scan func(...any) error) (Environment, error) {
	var (
		environment Environment
		createdAt   int64
	)
	if err := scan(&environment.ID, &environment.ProjectID, &environment.Name, &createdAt); err != nil {
		return Environment{}, err
	}
	environment.CreatedAt = time.Unix(createdAt, 0).UTC()
	return environment, nil
}

func projectID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("projectID"), 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid project id")
	}
	return id, nil
}

func environmentID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid environment id")
	}
	return id, nil
}

func validateEnvironmentName(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return errors.New("environment name is required")
	}
	if utf8.RuneCountInString(trimmed) > maxEnvironmentName {
		return errors.New("environment name must be 255 characters or fewer")
	}
	return nil
}

func decodeEnvironmentInput(w http.ResponseWriter, r *http.Request) (environmentInput, error) {
	var input environmentInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return environmentInput{}, errors.New("request must contain a valid JSON object")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return environmentInput{}, errors.New("request must contain one JSON object")
	}
	return input, nil
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
