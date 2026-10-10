package variablesets

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
)

const maxRequestBody = 1 << 20

var errNotFound = errors.New("not found")

// invalidInput is a request the store refused after reading the database.
type invalidInput string

func (e invalidInput) Error() string { return string(e) }

// requireEnvironment parses the route and checks the Environment lives under
// the route's Project. On false it has written the response.
func (s *Service) requireEnvironment(w http.ResponseWriter, r *http.Request) (int64, bool) {
	if s == nil || s.db == nil {
		writeError(w, http.StatusInternalServerError, "variable sets are temporarily unavailable")
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
	var one int
	err = s.db.QueryRowContext(r.Context(), `SELECT 1 FROM environments WHERE id = ? AND project_id = ?`, environmentID, projectID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "environment not found")
		return 0, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load environment")
		return 0, false
	}
	return environmentID, true
}

// requireCompose is requireEnvironment plus a check that the route's Compose
// lives in that Environment.
func (s *Service) requireCompose(w http.ResponseWriter, r *http.Request) (composeID int64, ok bool) {
	environmentID, ok := s.requireEnvironment(w, r)
	if !ok {
		return 0, false
	}
	composeID, err := parsePositiveID(r.PathValue("composeID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "compose id must be a positive integer")
		return 0, false
	}
	var one int
	err = s.db.QueryRowContext(r.Context(), `SELECT 1 FROM composes WHERE id = ? AND environment_id = ?`, composeID, environmentID).Scan(&one)
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

// requireSet is requireCompose plus a check that the route's set belongs to
// that Compose.
func (s *Service) requireSet(w http.ResponseWriter, r *http.Request) (composeID, setID int64, ok bool) {
	composeID, ok = s.requireCompose(w, r)
	if !ok {
		return 0, 0, false
	}
	setID, err := parsePositiveID(r.PathValue("setID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "set id must be a positive integer")
		return 0, 0, false
	}
	var one int
	err = s.db.QueryRowContext(r.Context(), `SELECT 1 FROM variable_sets WHERE id = ? AND compose_id = ?`, setID, composeID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "variable set not found")
		return 0, 0, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load variable set")
		return 0, 0, false
	}
	return composeID, setID, true
}

func decodeBody(w http.ResponseWriter, r *http.Request, into any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		writeError(w, http.StatusBadRequest, "request must contain a valid JSON object")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeError(w, http.StatusBadRequest, "request must contain one JSON object")
		return false
	}
	return true
}

// decodeName reads and validates a set name body.
func decodeName(w http.ResponseWriter, r *http.Request) (string, bool) {
	var in setInput
	if !decodeBody(w, r, &in) {
		return "", false
	}
	if err := validateName(in.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return "", false
	}
	return in.Name, true
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
