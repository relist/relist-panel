package sshkeys

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"
)

type Service struct {
	store *Store
}

func New(db *sql.DB) *Service {
	return &Service{store: NewStore(db)}
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ssh-keys", s.list)
	mux.HandleFunc("POST /api/ssh-keys", s.create)
	mux.HandleFunc("PATCH /api/ssh-keys/{id}", s.rename)
	mux.HandleFunc("DELETE /api/ssh-keys/{id}", s.delete)
}

type useResponse struct {
	ProjectID     int64  `json:"projectId"`
	ProjectName   string `json:"projectName"`
	EnvironmentID int64  `json:"environmentId"`
	ComposeID     int64  `json:"composeId"`
	ComposeName   string `json:"composeName"`
}

// keyResponse never carries the private key.
type keyResponse struct {
	ID        int64         `json:"id"`
	Name      string        `json:"name"`
	PublicKey string        `json:"publicKey"`
	CreatedAt time.Time     `json:"createdAt"`
	UsedBy    []useResponse `json:"usedBy"`
}

func toResponse(k Key) keyResponse {
	used := make([]useResponse, 0, len(k.UsedBy))
	for _, u := range k.UsedBy {
		used = append(used, useResponse(u))
	}
	return keyResponse{ID: k.ID, Name: k.Name, PublicKey: k.PublicKey, CreatedAt: k.CreatedAt, UsedBy: used}
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	keys, err := s.store.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list SSH keys")
		return
	}
	out := make([]keyResponse, 0, len(keys))
	for _, k := range keys {
		out = append(out, toResponse(k))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	name, ok := readName(w, r)
	if !ok {
		return
	}
	k, err := s.store.Create(r.Context(), name)
	if err != nil {
		writeStoreError(w, err, "could not create SSH key")
		return
	}
	writeJSON(w, http.StatusCreated, toResponse(k))
}

func (s *Service) rename(w http.ResponseWriter, r *http.Request) {
	id, ok := readID(w, r)
	if !ok {
		return
	}
	name, ok := readName(w, r)
	if !ok {
		return
	}
	k, err := s.store.Rename(r.Context(), id, name)
	if err != nil {
		writeStoreError(w, err, "could not rename SSH key")
		return
	}
	writeJSON(w, http.StatusOK, toResponse(k))
}

func (s *Service) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := readID(w, r)
	if !ok {
		return
	}
	if err := s.store.Delete(r.Context(), id); err != nil {
		writeStoreError(w, err, "could not delete SSH key")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func readID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return 0, false
	}
	return id, true
}

func readName(w http.ResponseWriter, r *http.Request) (string, bool) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return "", false
	}
	return body.Name, true
}

func writeStoreError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, ErrNameRequired), errors.Is(err, ErrNameInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrNameTaken):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "not found")
	default:
		writeError(w, http.StatusInternalServerError, fallback)
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
