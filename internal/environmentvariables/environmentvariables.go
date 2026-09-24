// Package environmentvariables owns Environment Variable metadata and its HTTP API.
package environmentvariables

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	maxRequestBody    = 1 << 20
	maxEnvironmentKey = 255
)

// The key shape the design and the tickets state as the server's rule.
var environmentKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type Service struct {
	repository *repository
}

type repository struct {
	db *sql.DB
}

// EnvironmentVariable is the API representation of an Environment Variable.
type EnvironmentVariable struct {
	ID            int64  `json:"id"`
	EnvironmentID int64  `json:"environmentId"`
	Key           string `json:"key"`
	Value         string `json:"value"`
}

type environmentVariableInput struct {
	Key   string  `json:"key"`
	Value *string `json:"value"`
}

// New constructs the Environment Variable service over the application's
// migrated SQLite database.
func New(db *sql.DB) *Service {
	return &Service{repository: &repository{db: db}}
}

// RegisterRoutes adds the authenticated Environment Variable endpoints to mux.
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/projects/{projectID}/environments/{environmentID}/variables", s.list)
	mux.HandleFunc("POST /api/projects/{projectID}/environments/{environmentID}/variables", s.create)
	mux.HandleFunc("PUT /api/projects/{projectID}/environments/{environmentID}/variables/{variableID}", s.update)
	mux.HandleFunc("DELETE /api/projects/{projectID}/environments/{environmentID}/variables/{variableID}", s.delete)
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	if !s.available() {
		writeError(w, http.StatusInternalServerError, "environment variables are temporarily unavailable")
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

	variables, err := s.repository.list(r.Context(), environmentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load environment variables")
		return
	}
	writeJSON(w, http.StatusOK, variables)
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	if !s.available() {
		writeError(w, http.StatusInternalServerError, "environment variables are temporarily unavailable")
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

	input, err := decodeInput(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	key, value, err := validateInput(input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	variable, err := s.repository.create(r.Context(), environmentID, key, value)
	if err != nil {
		if isUniqueConstraint(err) {
			writeError(w, http.StatusConflict, "an environment variable with that key already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not create environment variable")
		return
	}
	writeJSON(w, http.StatusCreated, variable)
}

func (s *Service) update(w http.ResponseWriter, r *http.Request) {
	if !s.available() {
		writeError(w, http.StatusInternalServerError, "environment variables are temporarily unavailable")
		return
	}

	projectID, environmentID, err := routeIDs(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	variableID, err := variableID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "environment variable id must be a positive integer")
		return
	}
	if err := s.requireEnvironment(r.Context(), projectID, environmentID); err != nil {
		writeParentError(w, err)
		return
	}

	input, err := decodeInput(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	key, value, err := validateInput(input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if _, err := s.repository.findByID(r.Context(), environmentID, variableID); errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "environment variable not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load environment variable")
		return
	}
	if err := s.repository.update(r.Context(), environmentID, variableID, key, value); err != nil {
		if isUniqueConstraint(err) {
			writeError(w, http.StatusConflict, "an environment variable with that key already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not update environment variable")
		return
	}

	variable, err := s.repository.findByID(r.Context(), environmentID, variableID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load updated environment variable")
		return
	}
	writeJSON(w, http.StatusOK, variable)
}

func (s *Service) delete(w http.ResponseWriter, r *http.Request) {
	if !s.available() {
		writeError(w, http.StatusInternalServerError, "environment variables are temporarily unavailable")
		return
	}

	projectID, environmentID, err := routeIDs(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	variableID, err := variableID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "environment variable id must be a positive integer")
		return
	}
	if err := s.requireEnvironment(r.Context(), projectID, environmentID); err != nil {
		writeParentError(w, err)
		return
	}

	deleted, err := s.repository.delete(r.Context(), environmentID, variableID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete environment variable")
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "environment variable not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

func (r *repository) list(ctx context.Context, environmentID int64) ([]EnvironmentVariable, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, environment_id, key, value
		FROM environment_variables
		WHERE environment_id = ?
		ORDER BY id ASC
	`, environmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	variables := make([]EnvironmentVariable, 0)
	for rows.Next() {
		var variable EnvironmentVariable
		if err := rows.Scan(&variable.ID, &variable.EnvironmentID, &variable.Key, &variable.Value); err != nil {
			return nil, err
		}
		variables = append(variables, variable)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return variables, nil
}

func (r *repository) create(ctx context.Context, environmentID int64, key, value string) (EnvironmentVariable, error) {
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO environment_variables (environment_id, key, value)
		VALUES (?, ?, ?)
	`, environmentID, key, value)
	if err != nil {
		return EnvironmentVariable{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return EnvironmentVariable{}, err
	}
	return r.findByID(ctx, environmentID, id)
}

func (r *repository) findByID(ctx context.Context, environmentID, id int64) (EnvironmentVariable, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, environment_id, key, value
		FROM environment_variables
		WHERE environment_id = ? AND id = ?
	`, environmentID, id)
	var variable EnvironmentVariable
	if err := row.Scan(&variable.ID, &variable.EnvironmentID, &variable.Key, &variable.Value); err != nil {
		return EnvironmentVariable{}, err
	}
	return variable, nil
}

func (r *repository) update(ctx context.Context, environmentID, id int64, key, value string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE environment_variables
		SET key = ?, value = ?
		WHERE environment_id = ? AND id = ?
	`, key, value, environmentID, id)
	return err
}

func (r *repository) delete(ctx context.Context, environmentID, id int64) (bool, error) {
	result, err := r.db.ExecContext(ctx, `
		DELETE FROM environment_variables
		WHERE environment_id = ? AND id = ?
	`, environmentID, id)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
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

func variableID(r *http.Request) (int64, error) {
	return parsePositiveID(r.PathValue("variableID"))
}

func parsePositiveID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("id must be a positive integer")
	}
	return id, nil
}

func decodeInput(w http.ResponseWriter, r *http.Request) (environmentVariableInput, error) {
	var input environmentVariableInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return environmentVariableInput{}, errors.New("request must contain a valid JSON object")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return environmentVariableInput{}, errors.New("request must contain one JSON object")
	}
	return input, nil
}

func validateInput(input environmentVariableInput) (string, string, error) {
	key := strings.TrimSpace(input.Key)
	if key == "" {
		return "", "", errors.New("environment variable key is required")
	}
	if utf8.RuneCountInString(key) > maxEnvironmentKey {
		return "", "", errors.New("environment variable key must be 255 characters or fewer")
	}
	// Keys are interpolated into Compose files as ${KEY}, so anything the
	// shell-parameter character set rejects would create a variable no
	// reference could reach.
	if !environmentKeyPattern.MatchString(key) {
		return "", "", errors.New("environment variable key must match [A-Za-z_][A-Za-z0-9_]*")
	}
	if input.Value == nil {
		return "", "", errors.New("environment variable value is required")
	}
	return key, *input.Value, nil
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
