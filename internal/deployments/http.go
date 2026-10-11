package deployments

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"time"

	"relist-panel/internal/composes"
	"relist-panel/internal/sources"
)

const (
	historyLimit = 20
	logReadMax   = 1 << 20
	maxBodyBytes = 1 << 16
)

// APIComposes is what the HTTP layer needs from the Compose service beyond
// what the Queue itself uses.
type APIComposes interface {
	Composes
	ComposeRunRequest(ctx context.Context, projectID, environmentID, composeID int64) (composes.RunRequest, error)
}

// API serves the Deployment routes.
type API struct {
	q        *Queue
	composes APIComposes
}

func NewAPI(q *Queue, c APIComposes) *API { return &API{q: q, composes: c} }

const composeBase = "/api/projects/{projectID}/environments/{environmentID}/composes/{composeID}/deployments"

func (a *API) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST "+composeBase, a.create)
	mux.HandleFunc("GET "+composeBase, a.list)
	mux.HandleFunc("GET "+composeBase+"/{number}", a.get)
	mux.HandleFunc("GET "+composeBase+"/{number}/log", a.log)
	mux.HandleFunc("POST "+composeBase+"/{number}/cancel", a.cancel)
	mux.HandleFunc("GET /api/deployments/queue", a.queue)
}

type deploymentJSON struct {
	ID            int64      `json:"id"`
	ComposeID     int64      `json:"composeId"`
	Number        int        `json:"number"`
	Mode          Mode       `json:"mode"`
	NoCache       bool       `json:"noCache"`
	FreshVolumes  bool       `json:"freshVolumes"`
	Status        Status     `json:"status"`
	Step          string     `json:"step"`
	Steps         []string   `json:"steps"`
	FailedStep    string     `json:"failedStep"`
	Reason        string     `json:"reason"`
	SourceRef     string     `json:"sourceRef"`
	SourceSubject string     `json:"sourceSubject"`
	Warnings      []string   `json:"warnings"`
	QueuedAt      time.Time  `json:"queuedAt"`
	StartedAt     *time.Time `json:"startedAt"`
	FinishedAt    *time.Time `json:"finishedAt"`
	Ahead         *int       `json:"ahead,omitempty"`
}

func toJSON(d Deployment) deploymentJSON {
	j := deploymentJSON{
		ID: d.ID, ComposeID: d.ComposeID, Number: d.Number, Mode: d.Mode,
		NoCache: d.NoCache, FreshVolumes: d.FreshVolumes, Status: d.Status, Step: d.Step,
		Steps: d.Steps, FailedStep: d.FailedStep, Reason: d.Reason,
		SourceRef: d.SourceRef, SourceSubject: d.SourceSubject, Warnings: d.Warnings,
		QueuedAt: d.QueuedAt, StartedAt: d.StartedAt, FinishedAt: d.FinishedAt,
	}
	if j.Steps == nil {
		j.Steps = []string{}
	}
	if j.Warnings == nil {
		j.Warnings = []string{}
	}
	return j
}

type createInput struct {
	Mode         Mode   `json:"mode"`
	NoCache      bool   `json:"noCache"`
	FreshVolumes bool   `json:"freshVolumes"`
	ConfirmName  string `json:"confirmName"`
}

func (a *API) create(w http.ResponseWriter, r *http.Request) {
	composeID, ok := a.resolve(w, r)
	if !ok {
		return
	}
	var in createInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "request body must be JSON")
		return
	}
	switch in.Mode {
	case ModeDeploy, ModeBuild, ModeSkipBuild:
	default:
		writeError(w, http.StatusBadRequest, "mode must be deploy, build or skip-build")
		return
	}
	target, err := a.composes.Target(r.Context(), composeID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "compose not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load compose")
		return
	}
	if in.FreshVolumes && in.ConfirmName != target.Compose.Name {
		writeError(w, http.StatusBadRequest, "confirmName must match the compose name")
		return
	}
	if err := a.composes.CheckDeployable(r.Context(), target); errors.Is(err, composes.ErrBlankYAML) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	} else if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if in.Mode != ModeDeploy {
		src, err := a.q.d.Sources.Get(r.Context(), composeID)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			writeError(w, http.StatusBadRequest, "compose has no source")
			return
		case err != nil:
			writeError(w, http.StatusInternalServerError, "could not load source")
			return
		case in.Mode == ModeBuild && src.Kind == sources.KindGit && src.Branch == "":
			writeError(w, http.StatusBadRequest, reasonNoBranch)
			return
		}
	}
	d, created, err := a.q.Enqueue(r.Context(), composeID, Request{Mode: in.Mode, NoCache: in.NoCache, FreshVolumes: in.FreshVolumes})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not queue the deployment")
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusAccepted
	}
	writeJSON(w, status, map[string]int{"number": d.Number})
}

func (a *API) list(w http.ResponseWriter, r *http.Request) {
	composeID, ok := a.resolve(w, r)
	if !ok {
		return
	}
	ds, err := a.q.d.Store.List(r.Context(), composeID, historyLimit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list deployments")
		return
	}
	out := make([]deploymentJSON, 0, len(ds))
	for _, d := range ds {
		out = append(out, toJSON(d))
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) get(w http.ResponseWriter, r *http.Request) {
	d, ok := a.deployment(w, r)
	if !ok {
		return
	}
	j := toJSON(d)
	if d.Status == StatusQueued {
		ahead, err := a.q.d.Store.Ahead(r.Context(), d)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not load deployment")
			return
		}
		j.Ahead = &ahead
	}
	writeJSON(w, http.StatusOK, j)
}

type logResponse struct {
	Text   string `json:"text"`
	Offset int64  `json:"offset"`
	Done   bool   `json:"done"`
}

func (a *API) log(w http.ResponseWriter, r *http.Request) {
	// The status is read before the log, so a settled Deployment's log is
	// already complete when it is read.
	d, ok := a.deployment(w, r)
	if !ok {
		return
	}
	var offset int64
	if v := r.URL.Query().Get("offset"); v != "" {
		var err error
		if offset, err = strconv.ParseInt(v, 10, 64); err != nil || offset < 0 {
			writeError(w, http.StatusBadRequest, "offset must be a non-negative integer")
			return
		}
	}
	target, err := a.composes.Target(r.Context(), d.ComposeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load compose")
		return
	}
	terminal := d.Status != StatusQueued && d.Status != StatusRunning
	path := LogPath(composes.ComposeDir(target.Request), d.Number)
	text, next, err := ReadLog(path, offset, logReadMax)
	if errors.Is(err, os.ErrNotExist) {
		// Not opened yet (queued), or never opened (failed before it started).
		writeJSON(w, http.StatusOK, logResponse{Offset: offset, Done: terminal})
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read the log")
		return
	}
	done := false
	if terminal {
		if info, err := os.Stat(path); err == nil {
			done = next >= info.Size()
		}
	}
	writeJSON(w, http.StatusOK, logResponse{Text: text, Offset: next, Done: done})
}

func (a *API) cancel(w http.ResponseWriter, r *http.Request) {
	composeID, ok := a.resolve(w, r)
	if !ok {
		return
	}
	number, ok := parseNumber(w, r)
	if !ok {
		return
	}
	err := a.q.Cancel(r.Context(), composeID, number)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "deployment not found")
	case errors.Is(err, ErrNotCancelable):
		writeError(w, http.StatusConflict, err.Error())
	case err != nil:
		writeError(w, http.StatusInternalServerError, "could not cancel the deployment")
	default:
		w.WriteHeader(http.StatusAccepted)
	}
}

type activeJSON struct {
	ComposeID   int64  `json:"composeId"`
	ComposeName string `json:"composeName"`
	Number      int    `json:"number"`
	Status      Status `json:"status"`
	Step        string `json:"step"`
}

func (a *API) queue(w http.ResponseWriter, r *http.Request) {
	active, err := a.q.d.Store.Active(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list the queue")
		return
	}
	out := make([]activeJSON, 0, len(active))
	for _, x := range active {
		out = append(out, activeJSON(x))
	}
	writeJSON(w, http.StatusOK, out)
}

// resolve checks the route's Project and Environment own the Compose and
// returns its id. An ambiguous legacy identity does not block it: it only
// refuses new Deployments, which create checks itself, and the history of
// an ambiguous Compose stays readable.
func (a *API) resolve(w http.ResponseWriter, r *http.Request) (int64, bool) {
	ids := [3]int64{}
	for i, name := range []string{"projectID", "environmentID", "composeID"} {
		id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "ids must be positive integers")
			return 0, false
		}
		ids[i] = id
	}
	_, err := a.composes.ComposeRunRequest(r.Context(), ids[0], ids[1], ids[2])
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "compose not found")
		return 0, false
	case err != nil && !errors.Is(err, composes.ErrAmbiguousIdentity):
		writeError(w, http.StatusInternalServerError, "could not load compose")
		return 0, false
	}
	return ids[2], true
}

func parseNumber(w http.ResponseWriter, r *http.Request) (int, bool) {
	n, err := strconv.Atoi(r.PathValue("number"))
	if err != nil || n <= 0 {
		writeError(w, http.StatusNotFound, "deployment not found")
		return 0, false
	}
	return n, true
}

// deployment resolves the route's Compose and Deployment number.
func (a *API) deployment(w http.ResponseWriter, r *http.Request) (Deployment, bool) {
	composeID, ok := a.resolve(w, r)
	if !ok {
		return Deployment{}, false
	}
	number, ok := parseNumber(w, r)
	if !ok {
		return Deployment{}, false
	}
	d, err := a.q.d.Store.Get(r.Context(), composeID, number)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "deployment not found")
		return Deployment{}, false
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load deployment")
		return Deployment{}, false
	}
	return d, true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
