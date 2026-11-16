package databases

import (
	"net/http"
	"os"
	"strconv"
	"time"

	"relist-panel/internal/audit"
)

// BackupJSON is a Backup as the browser sees it.
type BackupJSON struct {
	ID         int64      `json:"id"`
	Trigger    string     `json:"trigger"`
	Database   string     `json:"database"`
	Status     string     `json:"status"`
	Error      string     `json:"error"`
	Log        string     `json:"log"`
	Size       int64      `json:"size"`
	SHA256     string     `json:"sha256"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt"`
	Pinned     bool       `json:"pinned"`
	// LocalRemoved: Rotation removed the file; it cannot be downloaded or restored.
	LocalRemoved bool `json:"localRemoved"`
	// Upload is the latest copy to a Backup destination; null when none was tried.
	Upload *UploadJSON `json:"upload"`
}

// UploadJSON is a Backup's copy in a Backup destination.
type UploadJSON struct {
	Destination string     `json:"destination"`
	Status      string     `json:"status"`
	Error       string     `json:"error"`
	UploadedAt  *time.Time `json:"uploadedAt"`
}

func backupJSON(b Backup) BackupJSON {
	var up *UploadJSON
	// A copy Rotation already deleted from its destination is not shown.
	if b.Upload != nil && b.Upload.DeletedAt == nil {
		up = &UploadJSON{Destination: b.Upload.DestinationName, Status: b.Upload.Status, Error: b.Upload.Error, UploadedAt: b.Upload.UploadedAt}
	}
	return BackupJSON{Upload: up, ID: b.ID, Trigger: b.Trigger, Database: b.LogicalName, Status: b.Status, Error: b.Error, Log: b.Log,
		Size: b.Size, SHA256: b.SHA256, StartedAt: b.StartedAt, FinishedAt: b.FinishedAt, Pinned: b.Pinned, LocalRemoved: b.LocalRemovedAt != nil}
}

// RestoreJSON is a restore as the browser sees it.
type RestoreJSON struct {
	ID          int64      `json:"id"`
	Source      string     `json:"source"`
	BackupID    *int64     `json:"backupId"`
	PreBackupID *int64     `json:"preBackupId"`
	UploadName  string     `json:"uploadName"`
	Target      string     `json:"target"`
	Status      string     `json:"status"`
	Error       string     `json:"error"`
	Log         string     `json:"log"`
	StartedAt   time.Time  `json:"startedAt"`
	FinishedAt  *time.Time `json:"finishedAt"`
}

func restoreJSON(r Restore) RestoreJSON {
	return RestoreJSON{ID: r.ID, Source: r.Source, BackupID: r.BackupID, PreBackupID: r.PreBackupID, UploadName: r.UploadName, Target: r.Target,
		Status: r.Status, Error: r.Error, Log: r.Log, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt}
}

func (a *API) logicalDatabases(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	c, ok := callerOf(r)
	if !ok {
		writeError(w, ErrNotFound)
		return
	}
	if !c.Admin {
		if c.Grant.DBUser == "" {
			writeError(w, ErrNoQuery)
			return
		}
		names, err := a.s.GranteeDatabases(r.Context(), n, c.Grant.DBUser)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string][]string{"databases": names})
		return
	}
	names, err := a.s.ListLogicalDatabases(r.Context(), n)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string][]string{"databases": names})
}

func (a *API) listBackups(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	list, err := a.s.ListBackups(r.Context(), n)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]BackupJSON, 0, len(list))
	for _, b := range list {
		out = append(out, backupJSON(b))
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) startBackup(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	var in BackupRequest
	if !decode(w, r, &in) {
		return
	}
	a.target(r, n)
	list, err := a.s.StartBackup(r.Context(), n, in)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]BackupJSON, 0, len(list))
	for _, b := range list {
		out = append(out, backupJSON(b))
	}
	writeJSON(w, http.StatusAccepted, out)
}

// backupID reads the {backupID} path value.
func backupID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	n, err := strconv.ParseInt(r.PathValue("backupID"), 10, 64)
	if err != nil || n < 1 {
		writeJSONError(w, http.StatusNotFound, "Backup not found")
		return 0, false
	}
	return n, true
}

// downloadBackup streams a succeeded Backup's file. The download is audited.
func (a *API) downloadBackup(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	bid, ok := backupID(w, r)
	if !ok {
		return
	}
	a.target(r, n)
	path, b, err := a.s.BackupPath(r.Context(), n, bid)
	if err != nil {
		writeError(w, err)
		return
	}
	d, err := a.s.d.Store.Get(r.Context(), n)
	if err != nil {
		writeError(w, err)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		writeError(w, ErrNotFound)
		return
	}
	defer f.Close()
	audit.Detail(r.Context(), "backup", bid)
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+d.Slug+"-"+b.StartedAt.UTC().Format("20060102T150405Z")+`.sql.gz"`)
	http.ServeContent(w, r, "", b.StartedAt, f)
}

func (a *API) deleteBackup(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	bid, ok := backupID(w, r)
	if !ok {
		return
	}
	a.target(r, n)
	if err := a.s.DeleteBackup(r.Context(), n, bid); err != nil {
		writeError(w, err)
		return
	}
	audit.Detail(r.Context(), "backup", bid)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) listRestores(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	list, err := a.s.ListRestores(r.Context(), n)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]RestoreJSON, 0, len(list))
	for _, x := range list {
		out = append(out, restoreJSON(x))
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) startRestore(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	var in struct {
		BackupID int64  `json:"backupId"`
		Target   string `json:"target"`
		Confirm  string `json:"confirm"`
	}
	if !decode(w, r, &in) {
		return
	}
	a.target(r, n)
	audit.Detail(r.Context(), "backup", in.BackupID)
	rs, err := a.s.RestoreFromBackup(r.Context(), n, in.BackupID, in.Target, in.Confirm)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, restoreJSON(rs))
}

// uploadRestore restores the raw request body, which is a .sql or .sql.gz
// file; target, confirm and name come in the query.
func (a *API) uploadRestore(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	a.target(r, n)
	audit.Detail(r.Context(), "file", q.Get("name"))
	rs, err := a.s.RestoreFromUpload(r.Context(), n, q.Get("target"), q.Get("confirm"), q.Get("name"), r.Body)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, restoreJSON(rs))
}
