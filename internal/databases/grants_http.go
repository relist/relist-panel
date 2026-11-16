package databases

import (
	"net/http"
	"strconv"
	"time"

	"relist-panel/internal/audit"
	"relist-panel/internal/auth"
	"relist-panel/internal/authz"
)

// caller is who a LevelDatabase request runs as: an Admin, or the grant that
// admitted a non-Admin. callerOf answers ok false for a non-Admin without a
// grant — a missing grant never means superuser.
type caller struct {
	Admin bool
	Grant authz.DatabaseGrant
}

func callerOf(r *http.Request) (caller, bool) {
	if g, ok := authz.DatabaseGrantFrom(r.Context()); ok {
		return caller{Grant: g}, true
	}
	if _, ok := authz.Member(r.Context()); ok {
		return caller{}, false
	}
	// An Admin, or a package test calling the handler without an Access.
	return caller{Admin: true}, true
}

// GrantJSON is one Database grant as the browser sees it.
type GrantJSON struct {
	UserID    int64     `json:"userId"`
	Email     string    `json:"email"`
	DBUser    string    `json:"dbUser,omitempty"`
	Level     string    `json:"level,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

func (a *API) listGrants(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	list, err := a.s.Grants(r.Context(), n)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]GrantJSON, 0, len(list))
	for _, g := range list {
		out = append(out, GrantJSON{UserID: g.UserID, Email: g.Email, DBUser: g.DBUser, Level: string(g.Level), CreatedAt: g.CreatedAt})
	}
	writeJSON(w, http.StatusOK, out)
}

// grantUserID reads the {userID} path value.
func grantUserID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	n, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil || n < 1 {
		writeJSONError(w, http.StatusNotFound, ErrUnknownPanelUser.Error())
		return 0, false
	}
	return n, true
}

// auditGrant names the Database and the panel user on the event.
func (a *API) auditGrant(r *http.Request, n, userID int64) {
	a.target(r, n)
	if email, _, err := a.s.d.Store.PanelUser(r.Context(), userID); err == nil {
		audit.Detail(r.Context(), "user", email)
	}
}

func (a *API) setGrant(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	userID, ok := grantUserID(w, r)
	if !ok {
		return
	}
	var in struct {
		DBUser string `json:"dbUser"`
		Level  string `json:"level"`
	}
	if !decode(w, r, &in) {
		return
	}
	a.auditGrant(r, n, userID)
	if in.DBUser != "" {
		audit.Detail(r.Context(), "dbUser", in.DBUser)
	}
	if in.Level != "" {
		audit.Detail(r.Context(), "level", in.Level)
	}
	if err := a.s.SetGrant(r.Context(), n, userID, in.DBUser, RedisLevel(in.Level)); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) deleteGrant(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	userID, ok := grantUserID(w, r)
	if !ok {
		return
	}
	a.auditGrant(r, n, userID)
	if err := a.s.DeleteGrant(r.Context(), n, userID); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// granteeJSON is a Database as its grantee sees it: no admin detail, the
// grant's own user, and the grant itself under access.
func granteeJSON(v View, g authz.DatabaseGrant) DatabaseJSON {
	out := toJSON(v)
	out.LastError = ""
	out.Warnings = []string{}
	out.SettingsOutdated = false
	out.MemoryMB, out.CPUs = 0, 0
	out.User = g.DBUser
	out.Access = &AccessJSON{DBUser: g.DBUser, Level: g.Level}
	return out
}

// granteeList is GET /api/databases for a non-Admin: only the Databases
// their grants name.
func (a *API) granteeList(w http.ResponseWriter, r *http.Request, user auth.User) {
	list, err := a.s.d.Store.GrantsForUser(r.Context(), user.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]DatabaseJSON, 0, len(list))
	for _, g := range list {
		v, err := a.s.Get(r.Context(), g.DatabaseID)
		if err != nil {
			continue
		}
		out = append(out, granteeJSON(v, authz.DatabaseGrant{DatabaseID: g.DatabaseID, DBUser: g.DBUser, Level: string(g.Level)}))
	}
	writeJSON(w, http.StatusOK, out)
}
