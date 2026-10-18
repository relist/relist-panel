package dockerhost

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

type API struct {
	engine    Engine
	pending   func(context.Context) (bool, error)
	guard     *Guard
	cacheLock chan struct{}
	cached    *Report
	cachedAt  time.Time
}

func NewAPI(engine Engine, guard *Guard, pending func(context.Context) (bool, error)) *API {
	return &API{engine: engine, guard: guard, pending: pending, cacheLock: make(chan struct{}, 1)}
}
func (a *API) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/docker", a.report)
	mux.HandleFunc("POST /api/docker/prune", a.prune)
	mux.HandleFunc("POST /api/docker/recover", a.recover)
}
func reply(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func failure(w http.ResponseWriter, status int, err string) {
	reply(w, status, map[string]string{"error": err})
}
func (a *API) lock(ctx context.Context) error {
	select {
	case a.cacheLock <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (a *API) report(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	started := time.Now()
	if err := a.lock(ctx); err != nil {
		failure(w, 504, "Docker report timed out")
		return
	}
	defer func() { <-a.cacheLock }()
	if a.cached != nil && (a.cachedAt.After(started) || (r.URL.Query().Get("refresh") != "1" && time.Since(a.cachedAt) < time.Minute)) {
		a.replyReport(w, *a.cached)
		return
	}
	report, err := a.engine.Report(ctx)
	if err != nil {
		a.cached = nil // A retry must read Docker, not resurrect stale successful data.
		failure(w, 502, err.Error())
		return
	}
	a.cachedAt = time.Now()
	report.CheckedAt = a.cachedAt.UTC().Format(time.RFC3339)
	a.cached = &report
	a.replyReport(w, report)
}
func (a *API) prune(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Kind    Kind   `json:"kind"`
		Confirm string `json:"confirm"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		failure(w, 400, "Provide a prune kind and its confirmation")
		return
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		failure(w, 400, "Provide one JSON object")
		return
	}
	if expected := input.Kind.Confirmation(); expected == "" || input.Confirm != expected {
		failure(w, 400, "Prune confirmation does not match the selected operation")
		return
	}
	release, ok := a.guard.TryPrune()
	if !ok {
		if a.guard.Blocked() {
			failure(w, 409, ErrMaintenanceBlocked.Error())
			return
		}
		failure(w, 409, "Docker is busy with a panel operation or another prune. Wait for it to finish and try again.")
		return
	}
	defer release()
	if a.pending != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		busy, err := a.pending(ctx)
		cancel()
		if err != nil {
			failure(w, 503, "Could not check the Deployment queue; prune did not start")
			return
		}
		if busy {
			failure(w, 409, "The Deployment queue has pending or running work. Wait for it to finish or cancel queued Deployments before pruning.")
			return
		}
	}
	// A lost browser connection must not release the exclusion while Docker
	// might still be deleting. Keep the operation bounded, and never retry it.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Minute)
	defer cancel()
	if err := a.lock(ctx); err != nil {
		failure(w, 504, "Docker prune timed out before it started")
		return
	}
	defer func() { <-a.cacheLock }()
	result, err := a.engine.Prune(ctx, input.Kind)
	a.cached = nil // Errors can follow partial deletion too.
	if err != nil {
		if errors.Is(err, ErrPruneUncertain) {
			a.guard.Block()
		}
		failure(w, 502, err.Error()+". Docker may have removed some resources; refresh the report before trying again.")
		return
	}
	reply(w, 200, result)
}

func (a *API) replyReport(w http.ResponseWriter, report Report) {
	report.MaintenanceBlocked = a.guard.Blocked()
	reply(w, 200, report)
}
func (a *API) recover(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Confirm string `json:"confirm"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.Confirm != "RESUME DOCKER" {
		failure(w, 400, "Type RESUME DOCKER after verifying that Docker has finished deleting")
		return
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		failure(w, 400, "Provide one JSON object")
		return
	}
	if !a.guard.Recover() {
		failure(w, 409, "Docker operations are not paused, or the previous prune request is still active")
		return
	}
	reply(w, 200, map[string]bool{"resumed": true})
}
