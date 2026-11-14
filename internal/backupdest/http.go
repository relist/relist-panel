package backupdest

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

// Service serves the Backup destinations page and opens destinations for the
// Databases' uploads. Every route is admin-only (authz.Routes leaves them out).
type Service struct {
	store *Store
	// connect builds the client of a destination; NewS3 unless a test swaps it.
	connect func(Destination) (Remote, error)
	now     func() time.Time
	// RemoveFiles removes a deleted Database's files when deleting a
	// destination took its last Backup; nil = nothing to remove.
	removeFiles func(databaseID int64) error
}

func New(store *Store, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, connect: NewS3, now: now}
}

// WithFileRemover sets what removes a deleted Database's files.
func (s *Service) WithFileRemover(fn func(databaseID int64) error) *Service {
	s.removeFiles = fn
	return s
}

// Open implements Opener.
func (s *Service) Open(ctx context.Context, id int64) (Destination, Remote, error) {
	d, err := s.store.Get(ctx, id)
	if err != nil {
		return Destination{}, nil, err
	}
	r, err := s.connect(d)
	if err != nil {
		return Destination{}, nil, err
	}
	return d, r, nil
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/backup-destinations", s.list)
	mux.HandleFunc("POST /api/backup-destinations", s.create)
	mux.HandleFunc("PUT /api/backup-destinations/{id}", s.update)
	mux.HandleFunc("DELETE /api/backup-destinations/{id}", s.delete)
	mux.HandleFunc("POST /api/backup-destinations/{id}/test", s.test)
}

// S3JSON is the settings of an s3 destination as the browser sees them: no secret.
type S3JSON struct {
	Endpoint    string `json:"endpoint"`
	Region      string `json:"region"`
	Bucket      string `json:"bucket"`
	Prefix      string `json:"prefix"`
	AccessKeyID string `json:"accessKeyId"`
	PathStyle   bool   `json:"pathStyle"`
}

// DestinationJSON is a destination as the browser sees it. It never carries the secret.
type DestinationJSON struct {
	ID            int64      `json:"id"`
	Name          string     `json:"name"`
	Type          string     `json:"type"`
	S3            S3JSON     `json:"s3"`
	LastTestAt    *time.Time `json:"lastTestAt"`
	LastTestOK    bool       `json:"lastTestOk"`
	LastTestError string     `json:"lastTestError"`
}

func toJSON(d Destination) DestinationJSON {
	return DestinationJSON{ID: d.ID, Name: d.Name, Type: d.Type, S3: S3JSON(d.S3),
		LastTestAt: d.LastTestAt, LastTestOK: d.LastTestOK, LastTestError: d.LastTestError}
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list Backup destinations")
		return
	}
	out := make([]DestinationJSON, 0, len(list))
	for _, d := range list {
		out = append(out, toJSON(d))
	}
	writeJSON(w, http.StatusOK, out)
}

type input struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	S3     S3JSON `json:"s3"`
	Secret string `json:"secret"`
}

// readInput decodes and checks a form; requireSecret is false on edit, where
// an empty secret keeps the stored one.
func readInput(w http.ResponseWriter, r *http.Request, requireSecret bool) (Destination, bool) {
	var in input
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return Destination{}, false
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		writeFieldError(w, http.StatusUnprocessableEntity, "name", "name is required")
		return Destination{}, false
	}
	if in.Type != TypeS3 {
		writeFieldError(w, http.StatusUnprocessableEntity, "type", `type must be "s3"`)
		return Destination{}, false
	}
	cfg, err := ValidateS3(S3Config(in.S3))
	if err != nil {
		var fe *FieldError
		if errors.As(err, &fe) {
			writeFieldError(w, http.StatusUnprocessableEntity, fe.Field, fe.Msg)
		} else {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
		}
		return Destination{}, false
	}
	secret := strings.Trim(in.Secret, "\r\n")
	if requireSecret && secret == "" {
		writeFieldError(w, http.StatusUnprocessableEntity, "secret", "secret access key is required")
		return Destination{}, false
	}
	return Destination{Name: name, Type: TypeS3, S3: cfg, Secret: secret}, true
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	in, ok := readInput(w, r, true)
	if !ok {
		return
	}
	d, err := s.store.Create(r.Context(), in, s.now())
	if err != nil {
		writeStoreError(w, err, "could not save the Backup destination")
		return
	}
	audit.Target(r.Context(), d.Name)
	writeJSON(w, http.StatusCreated, toJSON(d))
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
	old, err := s.store.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "could not read the Backup destination")
		return
	}
	// A kept secret stays with the endpoint it was given for: it is never
	// sent to another host.
	if in.Secret == "" && old.S3.Endpoint != in.S3.Endpoint {
		writeFieldError(w, http.StatusUnprocessableEntity, "secret", "enter the secret access key again when changing the endpoint")
		return
	}
	if err := s.store.Update(r.Context(), in, s.now()); err != nil {
		writeStoreError(w, err, "could not save the Backup destination")
		return
	}
	d, err := s.store.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "could not read the Backup destination")
		return
	}
	audit.Target(r.Context(), d.Name)
	writeJSON(w, http.StatusOK, toJSON(d))
}

func (s *Service) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := readID(w, r)
	if !ok {
		return
	}
	d, err := s.store.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "could not read the Backup destination")
		return
	}
	audit.Target(r.Context(), d.Name)
	confirm := -1
	if raw := r.URL.Query().Get("confirm"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			confirm = n
		}
	}
	removed, err := s.store.Delete(r.Context(), id, confirm)
	var inUse ErrInUse
	var copies ErrCopies
	switch {
	case errors.As(err, &inUse):
		writeJSON(w, http.StatusConflict, map[string]any{"error": inUse.Error(), "schedules": inUse.Schedules})
		return
	case errors.As(err, &copies):
		writeJSON(w, http.StatusConflict, map[string]any{"error": copies.Error(), "copies": copies.Copies})
		return
	case err != nil:
		writeStoreError(w, err, "could not delete the Backup destination")
		return
	}
	if s.removeFiles != nil {
		for _, dbID := range removed {
			_ = s.removeFiles(dbID)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// test writes, reads and deletes a small object, and records the result. A
// failed test is an answer, not an error: 200 with the failed result stored.
func (s *Service) test(w http.ResponseWriter, r *http.Request) {
	id, ok := readID(w, r)
	if !ok {
		return
	}
	d, err := s.store.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "could not read the Backup destination")
		return
	}
	audit.Target(r.Context(), d.Name)
	msg := ""
	if remote, err := s.connect(d); err != nil {
		msg = scrub(err.Error(), d.Secret)
	} else if err := remote.Test(r.Context()); err != nil {
		msg = scrub(err.Error(), d.Secret)
	}
	if err := s.store.SetTestResult(r.Context(), id, msg == "", msg, s.now()); err != nil {
		writeStoreError(w, err, "could not record the test")
		return
	}
	d, err = s.store.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "could not read the Backup destination")
		return
	}
	writeJSON(w, http.StatusOK, toJSON(d))
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
	case errors.Is(err, ErrDuplicateName):
		writeFieldError(w, http.StatusConflict, "name", err.Error())
	case errors.Is(err, ErrNotFound), errors.Is(err, sql.ErrNoRows):
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

func writeFieldError(w http.ResponseWriter, status int, field, message string) {
	writeJSON(w, status, map[string]string{"error": message, "field": field})
}
