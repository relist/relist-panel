package composes

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"path/filepath"
)

// RemovalPlanEntry is what deleting one Compose removes: its directory and the
// named volumes a destructive down takes with it, plus the volumes it keeps.
// Error is set when the volumes could not be worked out; the delete itself
// still runs with the server's own plan at that moment.
type RemovalPlanEntry struct {
	ComposeID     int64        `json:"composeId"`
	Name          string       `json:"name"`
	Directory     string       `json:"directory"`
	RemoveVolumes []string     `json:"removeVolumes"`
	KeepVolumes   []KeptVolume `json:"keepVolumes"`
	Error         string       `json:"error,omitempty"`
}

// RemovalPlan reports, without side effects, what deleting the Composes would
// remove: one Compose when composeID is not 0, else every Compose of the
// Environment (environmentID not 0) or of the Project. It is computed like the
// real teardown, with the Composes of one operation counted as deleted together.
func (s *Service) RemovalPlan(ctx context.Context, projectID, environmentID, composeID int64) ([]RemovalPlanEntry, error) {
	children, err := s.childComposes(ctx, projectID, environmentID)
	if err != nil {
		return nil, err
	}
	together := make([]int64, 0, len(children))
	for _, c := range children {
		together = append(together, c.ID)
	}
	entries := make([]RemovalPlanEntry, 0, len(children))
	for _, c := range children {
		if composeID != 0 && c.ID != composeID {
			continue
		}
		group := together
		if composeID != 0 {
			group = []int64{c.ID}
		}
		entries = append(entries, s.removalPlanEntry(ctx, c, group))
	}
	return entries, nil
}

func (s *Service) removalPlanEntry(ctx context.Context, c Compose, together []int64) RemovalPlanEntry {
	req := runRequestFor(s.dataDir, c)
	entry := RemovalPlanEntry{ComposeID: c.ID, Name: c.Name, Directory: filepath.Dir(req.ComposeFilePath), RemoveVolumes: []string{}, KeepVolumes: []KeptVolume{}}
	if err := s.requireUniqueIdentity(ctx, c); err != nil {
		entry.Error = err.Error()
		return entry
	}
	if _, err := os.Stat(req.ComposeFilePath); errors.Is(err, os.ErrNotExist) {
		return entry // never deployed: no volumes, and no Docker to ask
	} else if err != nil {
		entry.Error = "could not inspect compose files"
		return entry
	}
	plan, supported, err := s.volumePlan(ctx, c, req, together)
	if err != nil {
		entry.Error = err.Error()
	} else if !supported {
		entry.Error = "volume details unavailable"
	} else {
		entry.RemoveVolumes, entry.KeepVolumes = plan.Remove, plan.Keep
	}
	return entry
}

func (s *Service) removalPlan(w http.ResponseWriter, r *http.Request) {
	if !s.available() {
		writeError(w, http.StatusInternalServerError, "composes are temporarily unavailable")
		return
	}
	projectID, environmentID, err := routeIDs(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := composeID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "compose id must be a positive integer")
		return
	}
	if err := s.requireEnvironment(r.Context(), projectID, environmentID); err != nil {
		writeParentError(w, err)
		return
	}
	if _, err := s.repository.findByID(r.Context(), environmentID, id); errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "compose not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load compose")
		return
	}
	entries, err := s.RemovalPlan(r.Context(), projectID, environmentID, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not work out what a delete removes")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"composes": entries})
}
