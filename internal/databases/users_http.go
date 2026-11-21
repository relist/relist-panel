package databases

import (
	"net/http"

	"relist-panel/internal/audit"
)

// LogicalDatabaseJSON is one database inside a Database.
type LogicalDatabaseJSON struct {
	Name              string `json:"name"`
	SizeBytes         int64  `json:"sizeBytes"`
	Owner             string `json:"owner"`
	Charset           string `json:"charset"`
	AnyUserCanConnect bool   `json:"anyUserCanConnect"`
	Protected         bool   `json:"protected"`
}

// DatabaseUserJSON is a login user. It never carries the password: only
// GET .../credentials does, and that read is audited.
type DatabaseUserJSON struct {
	Name    string            `json:"name"`
	Managed bool              `json:"managed"`
	Owns    []string          `json:"owns"`
	Access  map[string]Access `json:"access"`
}

func (a *API) logicalDetails(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	list, err := a.s.LogicalDatabases(r.Context(), n)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]LogicalDatabaseJSON, 0, len(list))
	for _, l := range list {
		out = append(out, LogicalDatabaseJSON{Name: l.Name, SizeBytes: l.SizeBytes, Owner: l.Owner, Charset: l.Charset,
			AnyUserCanConnect: l.AnyUserCanConnect, Protected: l.Protected})
	}
	writeJSON(w, http.StatusOK, map[string][]LogicalDatabaseJSON{"databases": out})
}

func (a *API) createLogical(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	var in struct {
		Name        string `json:"name"`
		CreateOwner bool   `json:"createOwner"`
	}
	if !decode(w, r, &in) {
		return
	}
	a.target(r, n)
	audit.Detail(r.Context(), "database", in.Name)
	if err := a.s.CreateLogicalDatabase(r.Context(), n, in.Name, in.CreateOwner); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"name": in.Name})
}

func (a *API) restrictLogical(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	a.target(r, n)
	audit.Detail(r.Context(), "database", name)
	if err := a.s.RestrictLogicalDatabase(r.Context(), n, name); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// dropLogical answers 202 for a drop with Back up first (a job) and 204 for
// one that already ran.
func (a *API) dropLogical(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	var in struct {
		Confirm     string `json:"confirm"`
		BackupFirst bool   `json:"backupFirst"`
	}
	if !decode(w, r, &in) {
		return
	}
	a.target(r, n)
	audit.Detail(r.Context(), "database", name)
	if err := a.s.DropLogicalDatabase(r.Context(), n, name, in.Confirm, in.BackupFirst); err != nil {
		writeError(w, err)
		return
	}
	if in.BackupFirst {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) listUsers(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	list, err := a.s.Users(r.Context(), n)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]DatabaseUserJSON, 0, len(list))
	for _, u := range list {
		out = append(out, DatabaseUserJSON{Name: u.Name, Managed: u.Managed, Owns: u.Owns, Access: u.Access})
	}
	writeJSON(w, http.StatusOK, map[string][]DatabaseUserJSON{"users": out})
}

func (a *API) createUser(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	a.target(r, n)
	audit.Detail(r.Context(), "user", in.Name)
	if err := a.s.CreateUser(r.Context(), n, in.Name); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"name": in.Name})
}

func (a *API) setUserAccess(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	var in struct {
		Access map[string]Access `json:"access"`
	}
	if !decode(w, r, &in) {
		return
	}
	a.target(r, n)
	audit.Detail(r.Context(), "user", name)
	if err := a.s.SetUserAccess(r.Context(), n, name, in.Access); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) resetUserPassword(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	a.target(r, n)
	audit.Detail(r.Context(), "user", name)
	if err := a.s.ResetUserPassword(r.Context(), n, name); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) deleteUser(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	a.target(r, n)
	audit.Detail(r.Context(), "user", name)
	if err := a.s.DeleteUser(r.Context(), n, name); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// resetSuperuser starts the reset job and answers 202 at once.
func (a *API) resetSuperuser(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	var in struct {
		Confirm string `json:"confirm"`
	}
	if !decode(w, r, &in) {
		return
	}
	a.target(r, n)
	if err := a.s.ResetSuperuserPassword(r.Context(), n, in.Confirm); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
