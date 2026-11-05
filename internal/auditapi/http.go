// Package auditapi serves the Audit log (.scratch/multi-user/spec.md, Audit
// log → Reading): every event to admins, a Project's events to its Editors,
// the retention setting, and the daily prune. It is apart from package audit
// because the Project route reads authz's Access, and authz imports audit.
package auditapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"slices"
	"strconv"
	"time"

	"relist-panel/internal/audit"
	"relist-panel/internal/authz"
	"relist-panel/internal/grants"
)

// defaultDays is what the settings answer for days while events are kept
// forever: the period the operator returns to.
const defaultDays = 90

// Service serves the Audit log routes.
type Service struct {
	db  *sql.DB
	now func() time.Time
}

// New returns the Service over db.
func New(db *sql.DB) *Service {
	return &Service{db: db, now: time.Now}
}

// RegisterRoutes adds the Audit log routes. authz classifies them:
// /api/audit* is admin-only, the Project route is Viewer and checks Editor
// itself.
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/audit", s.list)
	mux.HandleFunc("GET /api/audit/settings", s.getSettings)
	mux.HandleFunc("PUT /api/audit/settings", s.putSettings)
	mux.HandleFunc("GET /api/projects/{id}/audit", s.listProject)
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	filter, ok := parseFilter(w, r)
	if !ok {
		return
	}
	s.respond(w, r, filter)
}

// listProject answers an admin every event of the Project, and a member who
// edits at least one of its Environments those Environments' events and the
// Project's own. authz has already answered 404 for a hidden Project.
func (s *Service) listProject(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || projectID <= 0 {
		writeError(w, http.StatusNotFound, "not found", "")
		return
	}
	filter, ok := parseFilter(w, r)
	if !ok {
		return
	}
	filter.ProjectID = projectID
	access, ok := authz.AccessFrom(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, "You don't have permission to do this", "")
		return
	}
	if !access.Admin {
		edited := []int64{}
		for _, id := range access.VisibleEnvironments(projectID) {
			if access.EnvironmentRole(id) >= grants.RoleEditor {
				edited = append(edited, id)
			}
		}
		if len(edited) == 0 {
			writeError(w, http.StatusForbidden, "You don't have permission to do this", "")
			return
		}
		filter.EnvironmentIDs, filter.ProjectOwn = edited, true
	}
	s.respond(w, r, filter)
}

func (s *Service) respond(w http.ResponseWriter, r *http.Request, filter audit.Filter) {
	rows, next, err := audit.List(r.Context(), s.db, filter)
	if err != nil {
		log.Printf("auditapi: list: %v", err)
		writeError(w, http.StatusInternalServerError, "could not read the audit log", "")
		return
	}
	if filter.EnvironmentIDs != nil {
		for i := range rows {
			rows[i].Detail = onlyEditedGrants(rows[i].Detail, filter.EnvironmentIDs)
		}
	}
	var nextCursor *int64
	if next != 0 {
		nextCursor = &next
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows, "nextCursor": nextCursor})
}

// onlyEditedGrants keeps, of a grant change's roles, the Project-wide ones
// and those on the Environments the reader edits: the rest are another
// Environment's members.
func onlyEditedGrants(detail map[string]any, edited []int64) map[string]any {
	list, ok := detail["grants"].([]any)
	if !ok {
		return detail
	}
	kept := []any{}
	for _, item := range list {
		grant, ok := item.(map[string]any)
		if !ok {
			continue
		}
		switch environment := grant["environmentId"].(type) {
		case nil:
			kept = append(kept, item)
		case float64:
			if slices.Contains(edited, int64(environment)) {
				kept = append(kept, item)
			}
		}
	}
	out := make(map[string]any, len(detail))
	for k, v := range detail {
		out[k] = v
	}
	out["grants"] = kept
	return out
}

// parseFilter reads cursor, actor, action, from and to; a malformed number
// answers 400 naming its field.
func parseFilter(w http.ResponseWriter, r *http.Request) (audit.Filter, bool) {
	query := r.URL.Query()
	f := audit.Filter{Actor: query.Get("actor"), Action: query.Get("action")}
	for _, field := range []struct {
		name string
		into *int64
	}{{"cursor", &f.Cursor}, {"from", &f.From}, {"to", &f.To}} {
		raw := query.Get(field.name)
		if raw == "" {
			continue
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, field.name+" must be a non-negative integer", field.name)
			return audit.Filter{}, false
		}
		*field.into = n
	}
	return f, true
}

func (s *Service) getSettings(w http.ResponseWriter, r *http.Request) {
	days, err := audit.Settings(r.Context(), s.db)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read the audit log settings", "")
		return
	}
	if days == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"retentionDays": defaultDays, "keepForever": true})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"retentionDays": days, "keepForever": false})
}

func (s *Service) putSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RetentionDays int  `json:"retentionDays"`
		KeepForever   bool `json:"keepForever"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "")
		return
	}
	days := body.RetentionDays
	if body.KeepForever {
		days = 0
	} else if days < 1 || days > audit.MaxRetentionDays {
		writeError(w, http.StatusBadRequest, "retention must be between 1 and 36500 days", "retentionDays")
		return
	}
	audit.Detail(r.Context(), "retentionDays", days)
	if err := audit.SetSettings(r.Context(), s.db, days); err != nil {
		if errors.Is(err, audit.ErrInvalidRetention) {
			writeError(w, http.StatusBadRequest, err.Error(), "retentionDays")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not save the audit log settings", "")
		return
	}
	s.getSettings(w, r)
}

// RunRetention prunes at once, then at every 00:00 UTC, until ctx is done.
func (s *Service) RunRetention(ctx context.Context) {
	for {
		if n, err := audit.Prune(ctx, s.db, s.now()); err != nil {
			log.Printf("auditapi: prune: %v", err)
		} else if n > 0 {
			log.Printf("auditapi: pruned %d audit events past retention", n)
		}
		now := s.now().UTC()
		midnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
		timer := time.NewTimer(midnight.Sub(now))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func writeError(w http.ResponseWriter, status int, message, field string) {
	body := map[string]string{"error": message}
	if field != "" {
		body["field"] = field
	}
	writeJSON(w, status, body)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
