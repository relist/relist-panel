package certificates

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"
)

type Service struct {
	store   *Store
	dataDir string
	now     func() time.Time
}

func New(db *sql.DB, dataDir string) *Service {
	return &Service{store: NewStore(db), dataDir: dataDir, now: time.Now}
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/certificates", s.list)
	mux.HandleFunc("POST /api/certificates", s.create)
	mux.HandleFunc("PUT /api/certificates/{id}", s.replace)
	mux.HandleFunc("DELETE /api/certificates/{id}", s.delete)
}

// certificateResponse never carries the certificate or key PEM.
type certificateResponse struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	SANs      []string  `json:"sans"`
	Issuer    string    `json:"issuer"`
	NotAfter  time.Time `json:"notAfter"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func toResponse(c Certificate) certificateResponse {
	sans := c.SANs
	if sans == nil {
		sans = []string{}
	}
	return certificateResponse{ID: c.ID, Name: c.Name, SANs: sans, Issuer: c.Issuer, NotAfter: c.NotAfter, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
}

type requestBody struct {
	Name        string `json:"name"`
	Certificate string `json:"certificate"`
	PrivateKey  string `json:"privateKey"`
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	certs, err := s.store.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list certificates", "")
		return
	}
	out := make([]certificateResponse, 0, len(certs))
	for _, c := range certs {
		out = append(out, toResponse(c))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	name, ferr := CleanName(body.Name)
	if ferr != nil {
		writeError(w, http.StatusBadRequest, ferr.Message, ferr.Field)
		return
	}
	p, ferr := Parse(body.Certificate, body.PrivateKey, s.now())
	if ferr != nil {
		writeError(w, http.StatusBadRequest, ferr.Message, ferr.Field)
		return
	}
	c, err := s.store.Create(r.Context(), name, p)
	if err != nil {
		writeStoreError(w, err, "could not create certificate")
		return
	}
	s.reconcileAndWrite(w, r, http.StatusCreated, c)
}

// replace swaps the certificate and key; a name in the body is ignored.
func (s *Service) replace(w http.ResponseWriter, r *http.Request) {
	id, ok := readID(w, r)
	if !ok {
		return
	}
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	p, ferr := Parse(body.Certificate, body.PrivateKey, s.now())
	if ferr != nil {
		writeError(w, http.StatusBadRequest, ferr.Message, ferr.Field)
		return
	}
	c, err := s.store.Replace(r.Context(), id, p)
	if err != nil {
		writeStoreError(w, err, "could not replace certificate")
		return
	}
	s.reconcileAndWrite(w, r, http.StatusOK, c)
}

func (s *Service) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := readID(w, r)
	if !ok {
		return
	}
	if err := s.store.Delete(r.Context(), id); err != nil {
		writeStoreError(w, err, "could not delete certificate")
		return
	}
	if err := Reconcile(r.Context(), s.store, s.dataDir); err != nil {
		writeReconcileError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// reconcileAndWrite hands the change to Traefik, then answers with c. The
// database already holds c, so a failed write is repaired by the next
// Reconcile.
func (s *Service) reconcileAndWrite(w http.ResponseWriter, r *http.Request, status int, c Certificate) {
	if err := Reconcile(r.Context(), s.store, s.dataDir); err != nil {
		writeReconcileError(w, err)
		return
	}
	writeJSON(w, status, toResponse(c))
}

func writeReconcileError(w http.ResponseWriter, err error) {
	writeError(w, http.StatusInternalServerError, "the change was saved but Traefik's certificate files could not be written: "+err.Error(), "")
}

func readID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id", "")
		return 0, false
	}
	return id, true
}

func readBody(w http.ResponseWriter, r *http.Request) (requestBody, bool) {
	var body requestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "")
		return requestBody{}, false
	}
	return body, true
}

func writeStoreError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, ErrNameTaken):
		writeError(w, http.StatusConflict, err.Error(), "name")
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "not found", "")
	default:
		writeError(w, http.StatusInternalServerError, fallback, "")
	}
}

func writeError(w http.ResponseWriter, status int, message, field string) {
	body := map[string]string{"error": message}
	if field != "" {
		body["field"] = field
	}
	writeJSON(w, status, body)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
