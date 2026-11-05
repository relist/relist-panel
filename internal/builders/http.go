package builders

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"math"
	"net/http"
	"strconv"

	"relist-panel/internal/audit"
	"relist-panel/internal/authz"
)

const maxRequestBody = 1 << 20

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	const base = "/api/builders"
	mux.HandleFunc("GET "+base, s.list)
	mux.HandleFunc("POST "+base, s.create)
	mux.HandleFunc("PUT "+base+"/default", s.putDefault)
	mux.HandleFunc("PUT "+base+"/settings", s.putSettings)
	mux.HandleFunc("PUT "+base+"/{id}", s.update)
	mux.HandleFunc("DELETE "+base+"/{id}", s.delete)
	mux.HandleFunc("POST "+base+"/{id}/start", s.start)
	mux.HandleFunc("GET "+base+"/{id}/disk-usage", s.diskUsage)
	mux.HandleFunc("POST "+base+"/{id}/prune", s.prune)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// builderJSON is a Builder on the wire. CPUs are cores; a zero max parallel
// steps (BuildKit's default) is null.
type builderJSON struct {
	ID             int64    `json:"id"`
	Name           string   `json:"name"`
	CPUs           float64  `json:"cpus"`
	MemoryMB       int      `json:"memoryMb"`
	MaxParallelism *int     `json:"maxParallelism"`
	State          string   `json:"state,omitempty"`
	LastError      string   `json:"lastError"`
	UsedBy         []string `json:"usedBy,omitempty"`
}

func toJSON(b Builder) builderJSON {
	j := builderJSON{ID: b.ID, Name: b.Name, CPUs: float64(b.CPUsMilli) / 1000, MemoryMB: b.MemoryMB, LastError: b.LastError}
	if b.MaxParallelism > 0 {
		n := b.MaxParallelism
		j.MaxParallelism = &n
	}
	return j
}

type listResponse struct {
	DefaultBuilderID     int64         `json:"defaultBuilderId"`
	BuildKitImage        string        `json:"buildkitImage"`
	DefaultBuildKitImage string        `json:"defaultBuildkitImage"`
	Capacity             capacityJSON  `json:"capacity"`
	Builders             []builderJSON `json:"builders"`
}

type capacityJSON struct {
	CPUs     int `json:"cpus"`
	MemoryMB int `json:"memoryMb"`
}

// listNames is a member's list: enough to choose a Builder on a Compose,
// and nothing about the host.
func (s *Service) listNames(w http.ResponseWriter, r *http.Request) {
	type named struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	resp := struct {
		Builders         []named `json:"builders"`
		DefaultBuilderID int64   `json:"defaultBuilderId"`
	}{Builders: []named{}}
	var err error
	if resp.DefaultBuilderID, err = s.d.Store.DefaultID(r.Context()); err != nil {
		s.internal(w, "load the default builder", err)
		return
	}
	list, err := s.d.Store.List(r.Context())
	if err != nil {
		s.internal(w, "list builders", err)
		return
	}
	for _, b := range list {
		resp.Builders = append(resp.Builders, named{ID: b.ID, Name: b.Name})
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, member := authz.Member(ctx); member {
		s.listNames(w, r)
		return
	}
	resp := listResponse{DefaultBuildKitImage: DefaultBuildKitImage, Builders: []builderJSON{}}
	var err error
	if resp.DefaultBuilderID, err = s.d.Store.DefaultID(ctx); err != nil {
		s.internal(w, "load the default builder", err)
		return
	}
	if resp.BuildKitImage, err = s.d.Store.BuildKitImage(ctx); err != nil {
		s.internal(w, "load the BuildKit image", err)
		return
	}
	cpus, memory, err := s.d.Engine.Capacity(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Could not read the host's CPUs and memory from Docker")
		return
	}
	resp.Capacity = capacityJSON{CPUs: cpus, MemoryMB: int(memory >> 20)}
	list, err := s.d.Store.List(ctx)
	if err != nil {
		s.internal(w, "list builders", err)
		return
	}
	for _, b := range list {
		j := toJSON(b)
		if j.UsedBy, err = s.d.Store.UsedBy(ctx, b.ID); err != nil {
			s.internal(w, "list builder users", err)
			return
		}
		j.State = "unknown"
		if insp, err := s.d.Engine.Inspect(ctx, HostName(b.ID)); err == nil {
			j.State = string(insp.State)
		}
		resp.Builders = append(resp.Builders, j)
	}
	writeJSON(w, http.StatusOK, resp)
}

type builderBody struct {
	Name           string  `json:"name"`
	CPUs           float64 `json:"cpus"`
	MemoryMB       int     `json:"memoryMb"`
	MaxParallelism *int    `json:"maxParallelism"`
}

// decodeBuilder reads a Builder body; it writes the error response itself.
func decodeBuilder(w http.ResponseWriter, r *http.Request) (Builder, bool) {
	var body builderBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return Builder{}, false
	}
	milli := math.Round(body.CPUs * 1000)
	if math.IsNaN(milli) || math.IsInf(milli, 0) || milli > math.MaxInt32 {
		milli = 0
	}
	b := Builder{Name: body.Name, CPUsMilli: int(milli), MemoryMB: body.MemoryMB}
	if body.MaxParallelism != nil {
		b.MaxParallelism = *body.MaxParallelism
	}
	return b, true
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	b, ok := decodeBuilder(w, r)
	if !ok {
		return
	}
	created, err := s.Create(r.Context(), b)
	if err != nil {
		s.fail(w, "create builder", err)
		return
	}
	audit.Target(r.Context(), created.Name)
	writeJSON(w, http.StatusCreated, toJSON(created))
}

func (s *Service) update(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	b, ok := decodeBuilder(w, r)
	if !ok {
		return
	}
	b.ID = id
	updated, err := s.Update(r.Context(), b)
	if err != nil {
		s.fail(w, "update builder", err)
		return
	}
	writeJSON(w, http.StatusOK, toJSON(updated))
}

func (s *Service) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.Delete(r.Context(), id); err != nil {
		s.fail(w, "delete builder", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) putDefault(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BuilderID int64 `json:"builderId"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.SetDefault(r.Context(), body.BuilderID); err != nil {
		s.fail(w, "set default builder", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) putSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BuildKitImage string `json:"buildkitImage"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.SetBuildKitImage(r.Context(), body.BuildKitImage); err != nil {
		s.fail(w, "set BuildKit image", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) start(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	b, err := s.Start(r.Context(), id)
	if err != nil {
		s.fail(w, "start builder", err)
		return
	}
	writeJSON(w, http.StatusOK, toJSON(b))
}

func (s *Service) diskUsage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	n, err := s.DiskUsage(r.Context(), id)
	if err != nil {
		s.fail(w, "read builder disk usage", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"bytes": n})
}

func (s *Service) prune(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.Prune(r.Context(), id); err != nil {
		s.fail(w, "prune builder cache", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, "builder not found")
		return 0, false
	}
	return id, true
}

// fail maps a Service error to its response.
func (s *Service) fail(w http.ResponseWriter, action string, err error) {
	var validation *ValidationError
	var inUse *InUseError
	var start *StartError
	var pull *PullError
	var engine *EngineError
	switch {
	case errors.As(err, &validation):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": validation.Message, "field": validation.Field})
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "builder not found")
	case errors.Is(err, ErrUnknownBuilder):
		writeError(w, http.StatusUnprocessableEntity, "builder not found")
	case errors.Is(err, ErrInvalidImage):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.As(err, &inUse), errors.Is(err, ErrBusy), errors.Is(err, ErrIsDefault), errors.Is(err, ErrQueued):
		writeError(w, http.StatusConflict, err.Error())
	case errors.As(err, &start), errors.As(err, &pull), errors.As(err, &engine):
		writeError(w, http.StatusBadGateway, err.Error())
	case errors.Is(err, context.Canceled):
		writeError(w, http.StatusServiceUnavailable, "request canceled")
	default:
		s.internal(w, action, err)
	}
}

func (s *Service) internal(w http.ResponseWriter, action string, err error) {
	log.Printf("builders: %s: %v", action, err)
	writeError(w, http.StatusInternalServerError, "could not "+action)
}
