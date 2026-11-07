package registryauth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"relist-panel/internal/audit"
)

// Tester logs in to a registry with one login, to check it before a deploy
// needs it.
type Tester interface {
	Login(ctx context.Context, a Auth) error
}

// Service serves the External registries tab. Every route is admin-only
// (authz.Routes leaves them out).
type Service struct {
	store  *Store
	tester Tester
	now    func() time.Time
}

func New(store *Store, tester Tester, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, tester: tester, now: now}
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/registry/external", s.list)
	mux.HandleFunc("POST /api/registry/external", s.create)
	mux.HandleFunc("PUT /api/registry/external/{id}", s.update)
	mux.HandleFunc("DELETE /api/registry/external/{id}", s.delete)
	mux.HandleFunc("POST /api/registry/external/{id}/test", s.test)
}

// externalResponse never carries the secret.
type externalResponse struct {
	ID            int64      `json:"id"`
	Host          string     `json:"host"`
	Username      string     `json:"username"`
	LastTestAt    *time.Time `json:"lastTestAt"`
	LastTestOK    bool       `json:"lastTestOk"`
	LastTestError string     `json:"lastTestError"`
}

func toResponse(e External) externalResponse {
	return externalResponse{ID: e.ID, Host: e.Host, Username: e.Username, LastTestAt: e.LastTestAt, LastTestOK: e.LastTestOK, LastTestError: e.LastTestError}
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list External registries")
		return
	}
	out := make([]externalResponse, 0, len(list))
	for _, e := range list {
		out = append(out, toResponse(e))
	}
	writeJSON(w, http.StatusOK, out)
}

type input struct {
	Host     string `json:"host"`
	Username string `json:"username"`
	Secret   string `json:"secret"`
}

// readInput decodes and checks a form; requireSecret is false on edit, where
// an empty secret keeps the stored one.
func readInput(w http.ResponseWriter, r *http.Request, requireSecret bool) (External, bool) {
	var in input
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return External{}, false
	}
	host, err := NormalizeHost(in.Host)
	if err != nil {
		writeFieldError(w, http.StatusUnprocessableEntity, "host", err.Error())
		return External{}, false
	}
	username := strings.TrimSpace(in.Username)
	if username == "" {
		writeFieldError(w, http.StatusUnprocessableEntity, "username", "username is required")
		return External{}, false
	}
	// Docker's config joins them as user:secret, so a colon would split there.
	if strings.Contains(username, ":") {
		writeFieldError(w, http.StatusUnprocessableEntity, "username", `username cannot contain ":"`)
		return External{}, false
	}
	// A pasted token often brings a line ending; spaces may belong to a
	// password, so they stay (docker login --password-stdin drops only the
	// line ending too).
	secret := strings.Trim(in.Secret, "\r\n")
	if requireSecret && secret == "" {
		writeFieldError(w, http.StatusUnprocessableEntity, "secret", "token or password is required")
		return External{}, false
	}
	return External{Host: host, Username: username, Secret: secret}, true
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	in, ok := readInput(w, r, true)
	if !ok {
		return
	}
	e, err := s.store.Create(r.Context(), in, s.now())
	if err != nil {
		writeStoreError(w, err, "could not save the External registry")
		return
	}
	audit.Target(r.Context(), e.Host)
	writeJSON(w, http.StatusCreated, toResponse(e))
}

func (s *Service) update(w http.ResponseWriter, r *http.Request) {
	id, ok := readID(w, r)
	if !ok {
		return
	}
	in, ok := readInput(w, r, false)
	if !ok {
		return
	}
	in.ID = id
	// A kept secret stays with the host it was given for.
	if in.Secret == "" {
		old, err := s.store.Get(r.Context(), id)
		if err != nil {
			writeStoreError(w, err, "could not read the External registry")
			return
		}
		if old.Host != in.Host {
			writeFieldError(w, http.StatusUnprocessableEntity, "secret", "enter the token or password again when changing the host")
			return
		}
	}
	if err := s.store.Update(r.Context(), in, s.now()); err != nil {
		writeStoreError(w, err, "could not save the External registry")
		return
	}
	e, err := s.store.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "could not read the External registry")
		return
	}
	audit.Target(r.Context(), e.Host)
	writeJSON(w, http.StatusOK, toResponse(e))
}

func (s *Service) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := readID(w, r)
	if !ok {
		return
	}
	e, err := s.store.Get(r.Context(), id)
	if err == nil {
		err = s.store.Delete(r.Context(), id)
	}
	if err != nil {
		writeStoreError(w, err, "could not delete the External registry")
		return
	}
	audit.Target(r.Context(), e.Host)
	w.WriteHeader(http.StatusNoContent)
}

// test logs in with the stored login and records the result. A failed login
// is an answer, not an error: 200 with ok false.
func (s *Service) test(w http.ResponseWriter, r *http.Request) {
	id, ok := readID(w, r)
	if !ok {
		return
	}
	e, err := s.store.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "could not read the External registry")
		return
	}
	audit.Target(r.Context(), e.Host)
	if s.tester == nil {
		writeError(w, http.StatusServiceUnavailable, "testing a login is not available")
		return
	}
	msg := ""
	if err := s.tester.Login(r.Context(), Auth{Host: e.Host, Username: e.Username, Secret: e.Secret}); err != nil {
		msg = err.Error()
	}
	if err := s.store.SetTestResult(r.Context(), id, msg == "", msg, s.now()); err != nil {
		writeStoreError(w, err, "could not record the test")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": msg == "", "error": msg})
}

func readID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return 0, false
	}
	return id, true
}

func writeStoreError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, ErrDuplicateHost):
		writeFieldError(w, http.StatusConflict, "host", err.Error())
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

// writeFieldError names the form field at fault, so the page puts the
// message under it.
func writeFieldError(w http.ResponseWriter, status int, field, message string) {
	writeJSON(w, status, map[string]string{"error": message, "field": field})
}
