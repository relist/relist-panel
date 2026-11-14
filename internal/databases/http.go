package databases

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"relist-panel/internal/audit"
)

// API serves /api/databases. Every route is admin-only: authz leaves them out
// of Routes (ADR-0039).
type API struct {
	s *Service
}

func NewAPI(s *Service) *API { return &API{s: s} }

func (a *API) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/databases", a.list)
	mux.HandleFunc("GET /api/databases/engines", a.engines)
	mux.HandleFunc("POST /api/databases", a.create)
	mux.HandleFunc("GET /api/databases/{id}", a.get)
	mux.HandleFunc("PATCH /api/databases/{id}", a.rename)
	mux.HandleFunc("DELETE /api/databases/{id}", a.delete)
	mux.HandleFunc("GET /api/databases/{id}/credentials", a.credentials)
	mux.HandleFunc("GET /api/databases/{id}/logs", a.logs)
	mux.HandleFunc("POST /api/databases/{id}/start", a.action(a.s.Start))
	mux.HandleFunc("POST /api/databases/{id}/stop", a.action(a.s.Stop))
	mux.HandleFunc("POST /api/databases/{id}/restart", a.action(a.s.Restart))
	mux.HandleFunc("POST /api/databases/{id}/repair", a.action(a.s.Repair))
	mux.HandleFunc("POST /api/databases/{id}/update-image", a.action(a.s.UpdateImage))
}

// DatabaseJSON is a Database as the browser sees it. It never carries the
// password: only GET .../credentials does, and that read is audited.
type DatabaseJSON struct {
	ID              int64     `json:"id"`
	Name            string    `json:"name"`
	Slug            string    `json:"slug"`
	Engine          string    `json:"engine"`
	Version         string    `json:"version"`
	Image           string    `json:"image"`
	Desired         string    `json:"desired"`
	Status          string    `json:"status"`
	LastError       string    `json:"lastError"`
	Warnings        []string  `json:"warnings"`
	MemoryMB        int64     `json:"memoryMb"`
	CPUs            float64   `json:"cpus"`
	Host            string    `json:"host"`
	HostReadable    string    `json:"hostReadable"`
	Port            int       `json:"port"`
	User            string    `json:"user"`
	DefaultDatabase string    `json:"defaultDatabase"`
	CreatedAt       time.Time `json:"createdAt"`
}

func toJSON(v View) DatabaseJSON {
	out := DatabaseJSON{
		ID: v.ID, Name: v.Name, Slug: v.Slug, Engine: v.Engine, Version: v.Version,
		Desired: v.Desired, Status: v.Status, LastError: v.LastError, Warnings: v.Warnings,
		MemoryMB: v.MemoryMB, CPUs: v.CPUs, Host: ContainerName(v.ID), HostReadable: Alias(v.Slug),
		CreatedAt: v.CreatedAt,
	}
	if out.Warnings == nil {
		out.Warnings = []string{}
	}
	if e, ok := Engines[v.Engine]; ok {
		out.Image, out.Port, out.User, out.DefaultDatabase = e.Image(v.Version), e.Port(), e.User(), e.DefaultDatabase()
	}
	return out
}

func (a *API) list(w http.ResponseWriter, r *http.Request) {
	views, err := a.s.List(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]DatabaseJSON, 0, len(views))
	for _, v := range views {
		out = append(out, toJSON(v))
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) engines(w http.ResponseWriter, _ *http.Request) {
	type version struct {
		Tag     string `json:"tag"`
		Default bool   `json:"default"`
	}
	type engine struct {
		Name     string    `json:"name"`
		Label    string    `json:"label"`
		Port     int       `json:"port"`
		Versions []version `json:"versions"`
	}
	out := []engine{}
	for _, e := range Engines {
		item := engine{Name: e.Name(), Label: e.Label(), Port: e.Port(), Versions: []version{}}
		for _, v := range e.Versions() {
			item.Versions = append(item.Versions, version{Tag: v.Tag, Default: v.Default})
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	writeJSON(w, http.StatusOK, out)
}

func (a *API) create(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name     string  `json:"name"`
		Engine   string  `json:"engine"`
		Version  string  `json:"version"`
		MemoryMB int64   `json:"memoryMb"`
		CPUs     float64 `json:"cpus"`
	}
	if !decode(w, r, &in) {
		return
	}
	audit.Target(r.Context(), strings.TrimSpace(in.Name))
	d, err := a.s.Create(r.Context(), CreateInput{Name: in.Name, Engine: in.Engine, Version: in.Version, MemoryMB: in.MemoryMB, CPUs: in.CPUs})
	if err != nil {
		writeError(w, err)
		return
	}
	v, err := a.s.Get(r.Context(), d.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toJSON(v))
}

// id reads the {id} path value; a bad one is a missing Database.
func id(w http.ResponseWriter, r *http.Request) (int64, bool) {
	n, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || n < 1 {
		writeError(w, ErrNotFound)
		return 0, false
	}
	return n, true
}

// target names the Database in the audit event.
func (a *API) target(r *http.Request, n int64) {
	if d, err := a.s.d.Store.Get(r.Context(), n); err == nil {
		audit.Target(r.Context(), d.Name)
	}
}

func (a *API) get(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	v, err := a.s.Get(r.Context(), n)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toJSON(v))
}

func (a *API) rename(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	a.target(r, n)
	if err := a.s.Rename(r.Context(), n, in.Name); err != nil {
		writeError(w, err)
		return
	}
	audit.Detail(r.Context(), "name", strings.TrimSpace(in.Name))
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) action(run func(ctx context.Context, id int64) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n, ok := id(w, r)
		if !ok {
			return
		}
		a.target(r, n)
		if err := run(r.Context(), n); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (a *API) delete(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	var in struct {
		Confirm string `json:"confirm"`
	}
	if !decode(w, r, &in) {
		return
	}
	a.target(r, n)
	if err := a.s.Delete(r.Context(), n, in.Confirm); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) credentials(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	d, err := a.s.d.Store.Get(r.Context(), n)
	if err != nil {
		writeError(w, err)
		return
	}
	eng, err := Lookup(d.Engine, d.Version)
	if err != nil {
		writeError(w, err)
		return
	}
	audit.Target(r.Context(), d.Name)
	audit.SecretRead(r.Context(), []string{"password"})
	url := eng.URL(ContainerName(d.ID), d.Password, eng.DefaultDatabase())
	readable := ""
	if alias := Alias(d.Slug); alias != "" {
		readable = eng.URL(alias, d.Password, eng.DefaultDatabase())
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"password": d.Password, "url": url, "urlReadable": readable, "env": "DATABASE_URL=" + url,
	})
}

func (a *API) logs(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	tail := 200
	if raw := r.URL.Query().Get("tail"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > 2000 {
			writeJSONError(w, http.StatusBadRequest, "tail must be between 1 and 2000")
			return
		}
		tail = v
	}
	logs, err := a.s.Logs(r.Context(), n, tail)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"logs": logs})
}

func decode(w http.ResponseWriter, r *http.Request, out any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(out); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

// writeError maps a service error to its status.
func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidName), errors.Is(err, ErrUnknownEngine), errors.Is(err, ErrUnknownVersion), errors.Is(err, ErrConfirm):
		writeJSONError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrNotFound):
		writeJSONError(w, http.StatusNotFound, "Database not found")
	case errors.Is(err, ErrDuplicateName), errors.Is(err, ErrLocked):
		writeJSONError(w, http.StatusConflict, err.Error())
	case strings.HasPrefix(err.Error(), "docker"):
		writeJSONError(w, http.StatusBadGateway, err.Error())
	default:
		writeJSONError(w, http.StatusInternalServerError, err.Error())
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
