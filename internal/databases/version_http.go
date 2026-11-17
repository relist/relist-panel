package databases

import (
	"net/http"
	"strconv"
	"time"

	"relist-panel/internal/audit"
)

// VersionChangeJSON is a Change version as the browser sees it.
// PreviousData is the volume a succeeded Postgres change left behind, until
// it is deleted; "" otherwise.
type VersionChangeJSON struct {
	ID           int64      `json:"id"`
	FromVersion  string     `json:"fromVersion"`
	ToVersion    string     `json:"toVersion"`
	Method       string     `json:"method"`
	Status       string     `json:"status"`
	Error        string     `json:"error"`
	Log          string     `json:"log"`
	PreviousData string     `json:"previousData"`
	StartedAt    time.Time  `json:"startedAt"`
	FinishedAt   *time.Time `json:"finishedAt"`
}

func versionChangeJSON(c VersionChange) VersionChangeJSON {
	out := VersionChangeJSON{ID: c.ID, FromVersion: c.FromVersion, ToVersion: c.ToVersion, Method: c.Method, Status: c.Status,
		Error: c.Error, Log: c.Log, StartedAt: c.StartedAt, FinishedAt: c.FinishedAt}
	if c.Status == JobSucceeded && c.FromVolumeRemovedAt == nil {
		out.PreviousData = c.FromVolume
	}
	return out
}

func (a *API) changeVersion(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	var in struct {
		Version     string `json:"version"`
		BackupFirst bool   `json:"backupFirst"`
	}
	if !decode(w, r, &in) {
		return
	}
	a.target(r, n)
	audit.Detail(r.Context(), "version", in.Version)
	c, err := a.s.ChangeVersion(r.Context(), n, in.Version, in.BackupFirst)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, versionChangeJSON(c))
}

func (a *API) listVersionChanges(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	list, err := a.s.ListVersionChanges(r.Context(), n)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]VersionChangeJSON, 0, len(list))
	for _, c := range list {
		out = append(out, versionChangeJSON(c))
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) deletePreviousData(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	cid, err := strconv.ParseInt(r.PathValue("changeID"), 10, 64)
	if err != nil || cid < 1 {
		writeError(w, ErrNoPreviousData)
		return
	}
	a.target(r, n)
	audit.Detail(r.Context(), "change", cid)
	if err := a.s.DeletePreviousData(r.Context(), n, cid); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
