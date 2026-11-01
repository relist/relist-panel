package panelupdate

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	const base = "/api/panel-update"
	mux.HandleFunc("GET "+base+"/status", s.getStatus)
	mux.HandleFunc("POST "+base+"/check", s.postCheck)
	mux.HandleFunc("PUT "+base+"/settings", s.putSettings)
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
	if m != nil {
		out.Available = &Available{Version: m.Version, ReleasedAt: m.ReleasedAt, NotesURL: m.NotesURL, ManualSteps: m.ManualSteps}
		if out.Available.ManualSteps == nil {
			out.Available.ManualSteps = []ManualStep{}
		}
	}
	return out, nil
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
