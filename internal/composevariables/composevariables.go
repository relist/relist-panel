// Package composevariables owns Compose Variable metadata and its HTTP API.
// A Compose Variable is a key/value pair scoped to one Compose; at deploy it
// overrides an Environment Variable of the same key (see internal/composes).
package composevariables

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

	"relist-panel/internal/audit"
	"relist-panel/internal/authz"
	"relist-panel/internal/environmentvariables"
)

const maxRequestBody = 1 << 20

// ComposeVariable is the API representation of a Compose Variable. It has the
// Environment Variable wire shape, keyed to a Compose instead of an
// Environment.
type ComposeVariable struct {
	ID        int64  `json:"id"`
	ComposeID int64  `json:"composeId"`
	Key       string `json:"key"`
	Value     string `json:"value"`
	Secret    bool   `json:"secret"`
}

type input struct {
	Key    string  `json:"key"`
	Value  *string `json:"value"`
	Secret bool    `json:"secret"`
}

// Service serves the Compose Variables endpoints.
type Service struct {
	db  *sql.DB
	now func() time.Time
}

// New constructs the service over the application's migrated SQLite database.
func New(db *sql.DB) *Service {
	return &Service{db: db, now: time.Now}
}

// RegisterRoutes adds the authenticated Compose Variable endpoints to mux.
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	const base = "/api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/variables"
	mux.HandleFunc("GET "+base, s.list)
	mux.HandleFunc("POST "+base, s.create)
	mux.HandleFunc("PUT "+base+"/{variableID}", s.update)
	mux.HandleFunc("DELETE "+base+"/{variableID}", s.delete)
}

var errNotFound = errors.New("not found")

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	composeID, ok := s.requireCompose(w, r)
	if !ok {
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, compose_id, key, value, secret
		FROM compose_variables
		WHERE compose_id = ?
		ORDER BY id ASC
	`, composeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load compose variables")
		return
	}
	defer rows.Close()
	variables := make([]ComposeVariable, 0)
	for rows.Next() {
		variable, err := scan(rows.Scan)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not load compose variables")
			return
		}
		variables = append(variables, variable)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not load compose variables")
		return
	}
	if !authz.SecretsVisible(r) {
		for i := range variables {
			if variables[i].Secret {
				variables[i].Value = ""
			}
		}
	} else {
		var keys []string
		for _, variable := range variables {
			if variable.Secret && variable.Value != "" {
				keys = append(keys, variable.Key)
			}
		}
		audit.SecretRead(r.Context(), keys)
	}
	writeJSON(w, http.StatusOK, variables)
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	composeID, ok := s.requireCompose(w, r)
	if !ok {
		return
	}
	in, key, value, ok := decodeAndValidate(w, r)
	if !ok {
		return
	}
	audit.Detail(r.Context(), "key", key)
	var id int64
	err := s.inTx(r.Context(), composeID, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(r.Context(), `
			INSERT INTO compose_variables (compose_id, key, value, secret)
			VALUES (?, ?, ?, ?)
		`, composeID, key, value, in.Secret)
		if err != nil {
			return err
		}
		id, err = result.LastInsertId()
		return err
	})
	if err != nil {
		s.writeWriteError(w, err, "could not create compose variable")
		return
	}
	s.respond(w, r, http.StatusCreated, composeID, id)
}

func (s *Service) update(w http.ResponseWriter, r *http.Request) {
	composeID, ok := s.requireCompose(w, r)
	if !ok {
		return
	}
	variableID, err := parsePositiveID(r.PathValue("variableID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "compose variable id must be a positive integer")
		return
	}
	in, key, value, ok := decodeAndValidate(w, r)
	if !ok {
		return
	}
	audit.Detail(r.Context(), "key", key)
	err = s.inTx(r.Context(), composeID, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(r.Context(), `
			UPDATE compose_variables
			SET key = ?, value = ?, secret = ?
			WHERE compose_id = ? AND id = ?
		`, key, value, in.Secret, composeID, variableID)
		if err != nil {
			return err
		}
		return requireAffected(result)
	})
	if err != nil {
		s.writeWriteError(w, err, "could not update compose variable")
		return
	}
	s.respond(w, r, http.StatusOK, composeID, variableID)
}

func (s *Service) delete(w http.ResponseWriter, r *http.Request) {
	composeID, ok := s.requireCompose(w, r)
	if !ok {
		return
	}
	variableID, err := parsePositiveID(r.PathValue("variableID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "compose variable id must be a positive integer")
		return
	}
	var key string
	if err := s.db.QueryRowContext(r.Context(), "SELECT key FROM compose_variables WHERE compose_id = ? AND id = ?", composeID, variableID).Scan(&key); err == nil {
		audit.Detail(r.Context(), "key", key)
	}
	err = s.inTx(r.Context(), composeID, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(r.Context(), `
			DELETE FROM compose_variables
			WHERE compose_id = ? AND id = ?
		`, composeID, variableID)
		if err != nil {
			return err
		}
		return requireAffected(result)
	})
	if err != nil {
		s.writeWriteError(w, err, "could not delete compose variable")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// respond reads the stored row back and writes it.
func (s *Service) respond(w http.ResponseWriter, r *http.Request, status int, composeID, id int64) {
	variable, err := scan(s.db.QueryRowContext(r.Context(), `
		SELECT id, compose_id, key, value, secret
		FROM compose_variables
		WHERE compose_id = ? AND id = ?
	`, composeID, id).Scan)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load compose variable")
		return
	}
	writeJSON(w, status, variable)
}

// inTx runs write and stamps the Compose's variables_changed_at in one
// transaction, so a change and its redeploy marker land together.
func (s *Service) inTx(ctx context.Context, composeID int64, write func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := write(tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE composes SET variables_changed_at = ? WHERE id = ?`, s.now().Unix(), composeID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) writeWriteError(w http.ResponseWriter, err error, message string) {
	switch {
	case errors.Is(err, errNotFound):
		writeError(w, http.StatusNotFound, "compose variable not found")
	case strings.Contains(strings.ToLower(err.Error()), "unique constraint"):
		writeError(w, http.StatusConflict, "a compose variable with that key already exists")
	default:
		writeError(w, http.StatusInternalServerError, message)
	}
}

// requireCompose parses the route and checks that the Compose lives under the
// route's Project and Environment. On false it has written the response.
func (s *Service) requireCompose(w http.ResponseWriter, r *http.Request) (int64, bool) {
	if s == nil || s.db == nil {
		writeError(w, http.StatusInternalServerError, "compose variables are temporarily unavailable")
		return 0, false
	}
	projectID, err := parsePositiveID(r.PathValue("projectID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "project id must be a positive integer")
		return 0, false
	}
	environmentID, err := parsePositiveID(r.PathValue("environmentID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "environment id must be a positive integer")
		return 0, false
	}
	composeID, err := parsePositiveID(r.PathValue("composeID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "compose id must be a positive integer")
		return 0, false
	}
	var one int
	err = s.db.QueryRowContext(r.Context(), `
		SELECT 1
		FROM composes c
		JOIN environments e ON e.id = c.environment_id
		WHERE c.id = ? AND e.id = ? AND e.project_id = ?
	`, composeID, environmentID, projectID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "compose not found")
		return 0, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load compose")
		return 0, false
	}
	return composeID, true
}

func decodeAndValidate(w http.ResponseWriter, r *http.Request) (input, string, string, bool) {
	var in input
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "request must contain a valid JSON object")
		return input{}, "", "", false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeError(w, http.StatusBadRequest, "request must contain one JSON object")
		return input{}, "", "", false
	}
	key, value, err := environmentvariables.ValidateKeyValue("compose variable", in.Key, in.Value)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return input{}, "", "", false
	}
	return in, key, value, true
}

func scan(scan func(dest ...any) error) (ComposeVariable, error) {
	var variable ComposeVariable
	err := scan(&variable.ID, &variable.ComposeID, &variable.Key, &variable.Value, &variable.Secret)
	return variable, err
}

func requireAffected(result sql.Result) error {
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errNotFound
	}
	return nil
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
