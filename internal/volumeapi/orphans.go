package volumeapi

import (
	"database/sql"
	"errors"
	"net/http"

	"relist-panel/internal/composes"
	"relist-panel/internal/volumes"
)

// orphansResponse is the wire shape of a Compose's orphan volumes; the array
// is never null.
type orphansResponse struct {
	Orphans []orphanResponse `json:"orphans"`
}

// orphanResponse is one orphan volume on the wire. v1 carries no size or
// in-use state: Docker reports "in use" at delete time.
type orphanResponse struct {
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
}

// listOrphans answers the volumes Compose created for this Compose's project
// that its current YAML no longer declares. The derivation is the Resolver's,
// so a resolution or listing failure — `couldn't find env file` before the
// first deploy included — surfaces the CLI's own message with a Bad Gateway
// status. It does not depend on the helper-container availability probe.
func (s *Service) listOrphans(w http.ResponseWriter, r *http.Request) {
	ref, ok := s.orphanRef(w, r)
	if !ok {
		return
	}

	orphans, err := s.resolver.ListOrphans(r.Context(), ref)
	if err != nil {
		writeError(w, http.StatusBadGateway, "orphan volume listing failed: "+err.Error())
		return
	}

	body := orphansResponse{Orphans: make([]orphanResponse, 0, len(orphans))}
	for _, orphan := range orphans {
		body.Orphans = append(body.Orphans, orphanResponse{Name: orphan.Name, CreatedAt: orphan.CreatedAt})
	}
	writeJSON(w, http.StatusOK, body)
}

// deleteOrphan removes one orphan volume. The handler decides nothing about
// what may go: the Resolver re-derives the orphans and removes the name only
// if it is one, so a current, foreign or path-like name is a 404 and runs no
// removal. A volume a container still uses is Docker's refusal, returned as
// 409 with Docker's message; there is no force.
func (s *Service) deleteOrphan(w http.ResponseWriter, r *http.Request) {
	ref, ok := s.orphanRef(w, r)
	if !ok {
		return
	}

	err := s.resolver.RemoveOrphan(r.Context(), ref, r.PathValue("name"))
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, volumes.ErrNotOrphan):
		writeError(w, http.StatusNotFound, "volume is not an orphan of this compose")
	case errors.Is(err, volumes.ErrVolumeInUse):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusBadGateway, "orphan volume removal failed: "+err.Error())
	}
}

// orphanRef parses the route ids and loads the Compose's deploy identity like
// composeRequest, but answers a colliding legacy identity with 409 instead of
// a 500: these routes delete data, so the refusal must be a clear one.
func (s *Service) orphanRef(w http.ResponseWriter, r *http.Request) (volumes.ComposeRef, bool) {
	projectID, environmentID, err := routeIDs(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return volumes.ComposeRef{}, false
	}
	composeID, err := parsePositiveID(r.PathValue("composeID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "compose id must be a positive integer")
		return volumes.ComposeRef{}, false
	}

	req, err := s.composes.ComposeRunRequest(r.Context(), projectID, environmentID, composeID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "compose not found")
		return volumes.ComposeRef{}, false
	case errors.Is(err, composes.ErrAmbiguousIdentity):
		writeError(w, http.StatusConflict, err.Error())
		return volumes.ComposeRef{}, false
	case err != nil:
		writeError(w, http.StatusInternalServerError, "could not load compose")
		return volumes.ComposeRef{}, false
	}
	return volumes.ComposeRef{
		ProjectName:     req.ProjectName,
		ComposeFilePath: req.ComposeFilePath,
		EnvFilePath:     req.EnvFilePath,
	}, true
}
