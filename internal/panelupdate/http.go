package panelupdate

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"
)

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	const base = "/api/panel-update"
	mux.HandleFunc("GET "+base+"/status", s.getStatus)
	mux.HandleFunc("POST "+base+"/check", s.postCheck)
	mux.HandleFunc("PUT "+base+"/settings", s.putSettings)
	mux.HandleFunc("POST "+base+"/preflight", s.postPreflight)
	mux.HandleFunc("POST "+base+"/apply", s.postApply)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// Status is the GET /api/panel-update/status response. Later steps add fields.
type Status struct {
	Version string `json:"version"`
	// Development is true for a build that was not stamped; Message says why
	// no update is offered.
	Development  bool          `json:"development"`
	Message      string        `json:"message,omitempty"`
	Available    *Available    `json:"available"`
	CheckEnabled bool          `json:"checkEnabled"`
	LastCheckAt  *time.Time    `json:"lastCheckAt"`
	LastError    string        `json:"lastError"`
	Install      InstallStatus `json:"install"`
	// Locked is true while the update lock is on.
	Locked bool `json:"locked"`
	// Run is the newest update run, null when there has been none.
	Run *RunInfo `json:"run"`
}

// RunInfo is the newest run's status.json, for the update page.
type RunInfo struct {
	ID       string `json:"id"`
	State    string `json:"state"`
	Terminal bool   `json:"terminal"`
	Error    string `json:"error"`
	// RollbackError is why the rollback failed, set with rollback-failed.
	RollbackError string `json:"rollbackError"`
	FromVersion   string `json:"fromVersion"`
	ToVersion     string `json:"toVersion"`
	StartedAt     int64  `json:"startedAt"`
	// BackupPath is the run directory that holds the backup, empty before the
	// backup is complete.
	BackupPath string `json:"backupPath"`
	// BackupFiles names each backed-up Install file (and .env) with where it
	// belongs and its copy in BackupPath, as backup.json has them: the manual
	// restore copies each back.
	BackupFiles []BackupFile `json:"backupFiles"`
}

// InstallStatus says whether the Install was found and, if not, why not.
type InstallStatus struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
}

// Available describes the version on offer.
type Available struct {
	Version     string       `json:"version"`
	ReleasedAt  time.Time    `json:"releasedAt"`
	NotesURL    string       `json:"notesUrl"`
	ManualSteps []ManualStep `json:"manualSteps"`
}

const developmentMessage = "Development build: updates are not offered"

func (s *Service) status(r *http.Request) (Status, error) {
	ctx := r.Context()
	st, err := s.d.Store.Settings(ctx)
	if err != nil {
		return Status{}, err
	}
	m, err := s.Available(ctx)
	if err != nil {
		return Status{}, err
	}
	out := Status{
		Version: s.d.Version, Development: s.Development(),
		CheckEnabled: st.CheckEnabled, LastCheckAt: st.LastCheckAt, LastError: st.LastError,
	}
	if out.Development {
		out.Message = developmentMessage
	} else if s.NoKey() {
		out.Message = ErrNoKey.Error()
	}
	out.Install = s.installStatus(ctx)
	out.Locked = s.Locked()
	out.Run = s.runInfo()
	if m != nil {
		out.Available = &Available{Version: m.Version, ReleasedAt: m.ReleasedAt, NotesURL: m.NotesURL, ManualSteps: m.ManualSteps}
		if out.Available.ManualSteps == nil {
			out.Available.ManualSteps = []ManualStep{}
		}
	}
	return out, nil
}

func (s *Service) runInfo() *RunInfo {
	if s.d.DataDir == "" {
		return nil
	}
	st, ok, err := NewestRun(s.d.DataDir)
	if err != nil {
		log.Printf("panelupdate: read the newest run: %v", err)
		return nil
	}
	if !ok {
		return nil
	}
	info := &RunInfo{
		ID: st.Run, State: st.State, Terminal: IsTerminal(st.State), Error: st.Error, RollbackError: st.RollbackError,
		FromVersion: st.FromVersion, ToVersion: st.ToVersion, StartedAt: st.StartedAt,
	}
	info.BackupFiles = []BackupFile{}
	if b, err := ReadBackup(RunDir(s.d.DataDir, st.Run)); err == nil {
		info.BackupPath = RunDir(s.d.DataDir, st.Run)
		info.BackupFiles = append(info.BackupFiles, b.Config...)
		if b.Env != nil {
			info.BackupFiles = append(info.BackupFiles, *b.Env)
		}
	}
	return info
}

func (s *Service) installStatus(ctx context.Context) InstallStatus {
	_, err := s.FindInstall(ctx)
	var unavailable *Unavailable
	if errors.As(err, &unavailable) {
		return InstallStatus{Reason: unavailable.Reason}
	}
	return InstallStatus{Available: err == nil}
}

func (s *Service) respondStatus(w http.ResponseWriter, r *http.Request) {
	out, err := s.status(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read the update status")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Service) getStatus(w http.ResponseWriter, r *http.Request) {
	s.respondStatus(w, r)
}

// postCheck runs a check even with the daily check off. A failed check is not
// an HTTP error: the status carries lastError next to the last good result.
func (s *Service) postCheck(w http.ResponseWriter, r *http.Request) {
	if err := s.CheckNow(r.Context()); errors.Is(err, ErrBusy) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.respondStatus(w, r)
}

func (s *Service) putSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CheckEnabled *bool `json:"checkEnabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.CheckEnabled == nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.d.Store.SetCheckEnabled(r.Context(), *body.CheckEnabled); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save the update settings")
		return
	}
	s.respondStatus(w, r)
}

// postPreflight runs the Preflight and returns its report. A step that fails
// is part of the report, not an HTTP error.
func (s *Service) postPreflight(w http.ResponseWriter, r *http.Request) {
	report, err := s.Preflight(r.Context())
	switch {
	case err != nil:
		writeError(w, http.StatusInternalServerError, "could not run the preflight")
	case report == nil:
		writeError(w, http.StatusConflict, "No update is available")
	default:
		writeJSON(w, http.StatusOK, report)
	}
}

// postApply starts the update. It answers 202 with the run id; the page then
// follows the run through GET /status. A refusal is 409.
func (s *Service) postApply(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ReportID        string   `json:"report_id"`
		ManualStepsDone []string `json:"manual_steps_done"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ReportID == "" {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	id, err := s.Apply(r.Context(), body.ReportID, body.ManualStepsDone)
	var refusal *Refusal
	switch {
	case errors.As(err, &refusal):
		writeError(w, http.StatusConflict, refusal.Message)
	case err != nil:
		log.Printf("panelupdate: apply: %v", err)
		writeError(w, http.StatusInternalServerError, "could not start the update: "+err.Error())
	default:
		writeJSON(w, http.StatusAccepted, map[string]string{"id": id})
	}
}
