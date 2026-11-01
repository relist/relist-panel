package panelupdate

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
)

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	const base = "/api/panel-update"
	mux.HandleFunc("GET "+base+"/status", s.getStatus)
	mux.HandleFunc("POST "+base+"/check", s.postCheck)
	mux.HandleFunc("PUT "+base+"/settings", s.putSettings)
	mux.HandleFunc("POST "+base+"/preflight", s.postPreflight)
	mux.HandleFunc("POST "+base+"/apply", s.postApply)
	mux.HandleFunc("POST "+base+"/cancel", s.postCancel)
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
	// WaitingOn is what a quiescing run waits for; Note is a caveat about a
	// run that went on, such as a panel that did not answer.
	WaitingOn   string `json:"waitingOn"`
	Note        string `json:"note"`
	FromVersion string `json:"fromVersion"`
	ToVersion   string `json:"toVersion"`
	StartedAt   int64  `json:"startedAt"`
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
		WaitingOn: st.WaitingOn, Note: st.Note,
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

// previewResponse is POST /api/panel-update/preflight: the report of the
// update Container and the live gate the page shows next to it.
type previewResponse struct {
	Report *Report `json:"report"`
	Gate   Gate    `json:"gate"`
}

// liveGate is the state the update waits on, read now.
func (s *Service) liveGate(ctx context.Context, installAvailable bool) Gate {
	g := Gate{InstallAvailable: installAvailable, UpdateRunning: s.Locked()}
	if s.d.MigrationLocked != nil {
		locked, err := s.d.MigrationLocked(ctx)
		if err != nil {
			log.Printf("panelupdate: read the Migration state: %v", err)
		}
		g.MigrationLocked = locked
	}
	if s.d.DeploysBusy != nil {
		g.DeploysBusy = s.d.DeploysBusy()
	}
	return g
}

// installArgs are the flags that tell the update command which Install this is.
func installArgs(in Install) []string {
	args := []string{"--project", in.Project, "--dir", in.Dir}
	for _, f := range in.ConfigFiles {
		args = append(args, "--file", f)
	}
	return args
}

func (s *Service) updateSpec(in Install, name string, args []string) UpdateSpec {
	spec := UpdateSpec{Name: name, Image: in.Image, Args: args, InstallDir: in.Dir, DataDir: s.d.DataDir, Network: networkName}
	if s.d.ManifestURL != "" && s.d.ManifestURL != DefaultManifestURL {
		spec.Env = append(spec.Env, "RELIST_UPDATE_MANIFEST_URL="+s.d.ManifestURL)
	}
	return spec
}

// postPreflight runs `update --preflight --json` in a Container of the current
// image and returns its report. A step that fails is part of the report, not
// an HTTP error.
func (s *Service) postPreflight(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	switch {
	case s.Development():
		writeError(w, http.StatusConflict, developmentMessage)
		return
	case s.Locked():
		writeError(w, http.StatusConflict, "A Panel update is already running.")
		return
	}
	in, err := s.FindInstall(ctx)
	if err != nil {
		var unavailable *Unavailable
		if !errors.As(err, &unavailable) {
			writeError(w, http.StatusInternalServerError, "could not find the Install")
			return
		}
		report := &Report{
			FromVersion: s.d.Version, ReadmeURL: ReadmeURL, Invariants: []InvariantResult{}, Recreated: []string{},
			UnknownKinds: []string{}, ManualSteps: []ManualStep{},
			Failed: &Failure{Step: StepInstall, Message: "Panel update is unavailable: " + unavailable.Reason + "."},
		}
		writeJSON(w, http.StatusOK, previewResponse{Report: report, Gate: s.liveGate(ctx, false)})
		return
	}
	if s.d.Runner == nil {
		writeError(w, http.StatusInternalServerError, "this panel cannot start the update container")
		return
	}
	// One at a time: the preflight directory keeps only the latest report.
	s.pfRun.Lock()
	defer s.pfRun.Unlock()
	args := append([]string{"--preflight", "--json"}, installArgs(in)...)
	stdout, code, err := s.d.Runner.Preflight(ctx, s.updateSpec(in, "", args))
	if err != nil {
		log.Printf("panelupdate: preflight: %v", err)
		writeError(w, http.StatusInternalServerError, "could not run the preflight: "+err.Error())
		return
	}
	// Exit 1 (not ready) and 3 (up to date) are still reports.
	var report Report
	if code != 0 && code != 1 && code != 3 || json.Unmarshal(stdout, &report) != nil {
		log.Printf("panelupdate: preflight exited %d: %s", code, stdout)
		writeError(w, http.StatusInternalServerError, "the preflight did not produce a report")
		return
	}
	s.pfMu.Lock()
	s.latestID = report.ID
	s.pfMu.Unlock()
	writeJSON(w, http.StatusOK, previewResponse{Report: &report, Gate: s.liveGate(ctx, true)})
}

var stepIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// postApply starts the Updater detached and answers 202 with the run id at
// once; the page then follows the run through GET /status. A refusal is 409.
func (s *Service) postApply(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ReportID        string   `json:"report_id"`
		ManualStepsDone []string `json:"manual_steps_done"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ReportID == "" {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	for _, id := range body.ManualStepsDone {
		if !stepIDPattern.MatchString(id) {
			writeError(w, http.StatusBadRequest, "invalid manual step id")
			return
		}
	}
	ctx := r.Context()
	s.pfMu.Lock()
	latest := s.latestID
	s.pfMu.Unlock()
	if latest == "" || latest != body.ReportID {
		writeError(w, http.StatusConflict, "The preview is out of date. Run the preview again.")
		return
	}
	if st, ok, _ := NewestRun(s.d.DataDir); s.Locked() || ok && !IsTerminal(st.State) {
		writeError(w, http.StatusConflict, "A Panel update is already running.")
		return
	}
	// An Updater of any run that still runs refuses this one: two Applies
	// inside the re-check window would otherwise both start.
	if s.d.Updater != nil {
		found, err := s.d.Updater.ListUpdaters(ctx)
		if err != nil {
			log.Printf("panelupdate: list the Updater Containers: %v", err)
			writeError(w, http.StatusInternalServerError, "could not list the Updater Containers")
			return
		}
		for _, u := range found {
			if u.Running {
				writeError(w, http.StatusConflict, "The Updater "+u.Name+" is still running. Wait for it to finish: docker logs -f "+u.Name+".")
				return
			}
		}
	}
	in, err := s.FindInstall(ctx)
	if err != nil {
		writeError(w, http.StatusConflict, "Panel update is unavailable: "+err.Error()+".")
		return
	}
	if s.d.Runner == nil {
		writeError(w, http.StatusInternalServerError, "this panel cannot start the update container")
		return
	}
	run := NewRunID(s.d.Now())
	args := []string{"--report", body.ReportID, "--run", run}
	if len(body.ManualStepsDone) > 0 {
		args = append(args, "--steps-done", strings.Join(body.ManualStepsDone, ","))
	}
	args = append(args, installArgs(in)...)
	if err := s.d.Runner.StartUpdater(ctx, s.updateSpec(in, UpdaterName(run), args)); err != nil {
		log.Printf("panelupdate: start the Updater: %v", err)
		writeError(w, http.StatusInternalServerError, "could not start the update: "+err.Error())
		return
	}
	s.poke()
	writeJSON(w, http.StatusAccepted, map[string]string{"run": run})
}

// postCancel stops the Updater while its run is quiescing. Until the backup
// starts, SIGTERM ends the run as aborted and the panel releases its lock.
func (s *Service) postCancel(w http.ResponseWriter, r *http.Request) {
	st, ok, err := NewestRun(s.d.DataDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read the update status")
		return
	}
	if !ok || st.State != StateQuiescing {
		writeError(w, http.StatusConflict, "Only a waiting update can be cancelled")
		return
	}
	if s.d.Runner == nil {
		writeError(w, http.StatusInternalServerError, "this panel cannot stop the update container")
		return
	}
	if err := s.d.Runner.StopUpdater(r.Context(), UpdaterName(st.Run)); err != nil {
		log.Printf("panelupdate: stop the Updater: %v", err)
		writeError(w, http.StatusInternalServerError, "could not cancel the update: "+err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"run": st.Run})
}

// poke wakes the watcher, so a run it should follow is seen without waiting.
func (s *Service) poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
