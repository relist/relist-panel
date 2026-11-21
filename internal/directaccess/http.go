package directaccess

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"relist-panel/internal/audit"
	"relist-panel/internal/auth"
)

// API serves /api/direct-accesses. Every route is admin-only: authz leaves
// them out of Routes, as it does /api/databases (ADR-0039, ADR-0041).
type API struct{ s *Service }

func NewAPI(s *Service) *API { return &API{s: s} }

func (a *API) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/direct-accesses", a.list)
	mux.HandleFunc("POST /api/direct-accesses", a.open)
	mux.HandleFunc("POST /api/direct-accesses/{id}/extend", a.extend)
	mux.HandleFunc("DELETE /api/direct-accesses/{id}", a.close)
}

// AccessJSON is a Direct access as the browser sees it. For a host_port
// channel bind and hostPort come from its channel_config.
type AccessJSON struct {
	ID         int64   `json:"id"`
	TargetKind string  `json:"targetKind"`
	TargetID   int64   `json:"targetId"`
	TargetPort int     `json:"targetPort"`
	Channel    string  `json:"channel"`
	Bind       string  `json:"bind"`
	HostPort   int     `json:"hostPort"`
	ExpiresAt  *string `json:"expiresAt"`
	OpenedAt   string  `json:"openedAt"`
}

func toJSON(a Access) AccessJSON {
	out := AccessJSON{
		ID: a.ID, TargetKind: a.Target.Kind, TargetID: a.Target.ID, TargetPort: a.Target.Port, Channel: a.Channel,
		Bind: a.HostPort.Bind, HostPort: a.HostPort.HostPort, OpenedAt: a.OpenedAt.UTC().Format(time.RFC3339),
	}
	if a.ExpiresAt != nil {
		s := a.ExpiresAt.UTC().Format(time.RFC3339)
		out.ExpiresAt = &s
	}
	return out
}

func (a *API) list(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("targetKind")
	id, err := strconv.ParseInt(r.URL.Query().Get("targetId"), 10, 64)
	if kind == "" || err != nil {
		writeJSONError(w, http.StatusBadRequest, "targetKind and targetId are required")
		return
	}
	all, err := a.s.ForTarget(r.Context(), kind, id)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]AccessJSON, 0, len(all))
	for _, x := range all {
		out = append(out, toJSON(x))
	}
	writeJSON(w, http.StatusOK, map[string]any{"accesses": out})
}

func (a *API) open(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TargetKind    string `json:"targetKind"`
		TargetID      int64  `json:"targetId"`
		Channel       string `json:"channel"`
		AllInterfaces bool   `json:"allInterfaces"`
		HostPort      int    `json:"hostPort"`
		Minutes       int    `json:"minutes"`
	}
	if !decode(w, r, &in) {
		return
	}
	audit.Target(r.Context(), a.s.TargetName(r.Context(), in.TargetKind, in.TargetID))
	var by int64
	if u, ok := auth.UserFrom(r.Context()); ok {
		by = u.ID
	}
	x, err := a.s.Open(r.Context(), OpenInput{
		TargetKind: in.TargetKind, TargetID: in.TargetID, Channel: in.Channel,
		AllInterfaces: in.AllInterfaces, HostPort: in.HostPort, Minutes: in.Minutes, OpenedBy: by,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	audit.Detail(r.Context(), "target", in.TargetKind+":"+strconv.FormatInt(in.TargetID, 10))
	audit.Detail(r.Context(), "channel", x.Channel)
	audit.Detail(r.Context(), "bind", x.HostPort.Bind)
	audit.Detail(r.Context(), "port", x.HostPort.HostPort)
	audit.Detail(r.Context(), "minutes", in.Minutes)
	writeJSON(w, http.StatusCreated, toJSON(x))
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	n, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || n < 1 {
		writeError(w, ErrNotFound)
		return 0, false
	}
	return n, true
}

// named audits the access's target by its display name, when it still exists.
func (a *API) named(r *http.Request, id int64) {
	if x, err := a.s.Get(r.Context(), id); err == nil {
		audit.Target(r.Context(), a.s.TargetName(r.Context(), x.Target.Kind, x.Target.ID))
	}
}

func (a *API) extend(w http.ResponseWriter, r *http.Request) {
	n, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Minutes int `json:"minutes"`
	}
	if !decode(w, r, &in) {
		return
	}
	a.named(r, n)
	x, err := a.s.Extend(r.Context(), n, in.Minutes)
	if err != nil {
		writeError(w, err)
		return
	}
	audit.Detail(r.Context(), "minutes", in.Minutes)
	writeJSON(w, http.StatusOK, toJSON(x))
}

func (a *API) close(w http.ResponseWriter, r *http.Request) {
	n, ok := pathID(w, r)
	if !ok {
		return
	}
	a.named(r, n)
	if err := a.s.Close(r.Context(), n); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decode(w http.ResponseWriter, r *http.Request, out any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(out); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

// writeError maps a service error to its status. Passwords never reach here:
// nothing in this package reads one.
func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrUnsupported):
		writeJSONError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrNotFound):
		writeJSONError(w, http.StatusNotFound, ErrNotFound.Error())
	case errors.Is(err, ErrTargetNotFound):
		writeJSONError(w, http.StatusNotFound, "Database not found")
	case errors.Is(err, ErrOpen), errors.Is(err, ErrTargetStopped), errors.Is(err, ErrLocked), errors.Is(err, ErrPortInUse):
		writeJSONError(w, http.StatusConflict, err.Error())
	case strings.HasPrefix(err.Error(), "docker"):
		writeJSONError(w, http.StatusBadGateway, err.Error())
	default:
		writeJSONError(w, http.StatusInternalServerError, err.Error())
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
