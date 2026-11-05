package grants

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"relist-panel/internal/audit"
	"strconv"
)

const maxBody = 1 << 20

// Service serves the Grants routes. They are admin-only: authz leaves them
// out of its table.
type Service struct{ db *sql.DB }

func New(db *sql.DB) *Service { return &Service{db: db} }

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/users/{id}/grants", s.listUser)
	mux.HandleFunc("PUT /api/users/{id}/grants", s.replaceUser)
	mux.HandleFunc("GET /api/projects/{id}/grants", s.listProject)
	mux.HandleFunc("PUT /api/projects/{id}/grants", s.replaceProject)
}

func (s *Service) listUser(w http.ResponseWriter, r *http.Request) {
	s.list(w, r, "users", "grants.user_id = ?", "user not found")
}

func (s *Service) listProject(w http.ResponseWriter, r *http.Request) {
	s.list(w, r, "projects", "grants.project_id = ?", "project not found")
}

func (s *Service) list(w http.ResponseWriter, r *http.Request, table, where, missing string) {
	id, ok := s.pathID(w, r, table, missing)
	if !ok {
		return
	}
	out, err := listGrants(r.Context(), s.db, where, id)
	if err != nil {
		s.internal(w, "list grants", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Service) replaceUser(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r, "users", "user not found")
	if !ok {
		return
	}
	inputs, ok := decode(w, r)
	if !ok {
		return
	}
	for i := range inputs {
		inputs[i].UserID = id
	}
	var email string
	if err := s.db.QueryRowContext(r.Context(), "SELECT email FROM users WHERE id = ?", id).Scan(&email); err == nil {
		audit.Target(r.Context(), email)
	}
	s.replace(w, r, "user_id", id, inputs, "grants.user_id = ?")
}

func (s *Service) replaceProject(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r, "projects", "project not found")
	if !ok {
		return
	}
	inputs, ok := decode(w, r)
	if !ok {
		return
	}
	for i := range inputs {
		inputs[i].ProjectID = id
	}
	var name string
	if err := s.db.QueryRowContext(r.Context(), "SELECT name FROM projects WHERE id = ?", id).Scan(&name); err == nil {
		audit.Target(r.Context(), name)
	}
	s.replace(w, r, "project_id", id, inputs, "grants.project_id = ?")
}

func (s *Service) replace(w http.ResponseWriter, r *http.Request, scope string, id int64, inputs []grantInput, where string) {
	audit.Detail(r.Context(), "grants", inputs)
	err := replace(r.Context(), s.db, scope, id, inputs)
	var invalid validationError
	if errors.As(err, &invalid) {
		writeError(w, http.StatusBadRequest, invalid.message)
		return
	}
	if err != nil {
		s.internal(w, "replace grants", err)
		return
	}
	out, err := listGrants(r.Context(), s.db, where, id)
	if err != nil {
		s.internal(w, "list grants", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Service) pathID(w http.ResponseWriter, r *http.Request, table, missing string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, missing)
		return 0, false
	}
	ok, err := exists(r.Context(), s.db, table, id)
	if err != nil {
		s.internal(w, "find "+table, err)
		return 0, false
	}
	if !ok {
		writeError(w, http.StatusNotFound, missing)
		return 0, false
	}
	return id, true
}

func decode(w http.ResponseWriter, r *http.Request) ([]grantInput, bool) {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	decoder.DisallowUnknownFields()
	var inputs []grantInput
	if err := decoder.Decode(&inputs); err != nil || inputs == nil {
		writeError(w, http.StatusBadRequest, "request must be a JSON array of grants")
		return nil, false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeError(w, http.StatusBadRequest, "request must contain one JSON array")
		return nil, false
	}
	return inputs, true
}

func (s *Service) internal(w http.ResponseWriter, what string, err error) {
	log.Printf("grants: %s: %v", what, err)
	writeError(w, http.StatusInternalServerError, "could not "+what)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
