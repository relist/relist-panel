package databases

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"relist-panel/internal/audit"
	"relist-panel/internal/auth"
	"relist-panel/internal/authz"
)

// API serves /api/databases. Every route is admin-only: authz leaves them out
// of Routes (ADR-0039).
type API struct {
	s *Service
}

func NewAPI(s *Service) *API { return &API{s: s} }

func (a *API) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/databases", a.list)
	mux.HandleFunc("GET /api/databases/engines", a.engines)
	mux.HandleFunc("POST /api/databases", a.create)
	mux.HandleFunc("GET /api/databases/{id}", a.get)
	mux.HandleFunc("PATCH /api/databases/{id}", a.rename)
	mux.HandleFunc("DELETE /api/databases/{id}", a.delete)
	mux.HandleFunc("GET /api/databases/{id}/credentials", a.credentials)
	mux.HandleFunc("GET /api/databases/{id}/logs", a.logs)
	mux.HandleFunc("POST /api/databases/{id}/start", a.action(a.s.Start))
	mux.HandleFunc("POST /api/databases/{id}/stop", a.action(a.s.Stop))
	mux.HandleFunc("POST /api/databases/{id}/restart", a.action(a.s.Restart))
	mux.HandleFunc("POST /api/databases/{id}/repair", a.action(a.s.Repair))
	mux.HandleFunc("POST /api/databases/{id}/update-image", a.updateImage)
	mux.HandleFunc("POST /api/databases/{id}/apply-settings", a.action(a.s.ApplySettings))
	mux.HandleFunc("GET /api/databases/{id}/logical-databases", a.logicalDatabases)
	mux.HandleFunc("GET /api/databases/{id}/logical-databases/details", a.logicalDetails)
	mux.HandleFunc("POST /api/databases/{id}/logical-databases", a.createLogical)
	mux.HandleFunc("POST /api/databases/{id}/logical-databases/{name}/restrict", a.restrictLogical)
	mux.HandleFunc("DELETE /api/databases/{id}/logical-databases/{name}", a.dropLogical)
	mux.HandleFunc("GET /api/databases/{id}/users", a.listUsers)
	mux.HandleFunc("POST /api/databases/{id}/users", a.createUser)
	mux.HandleFunc("PUT /api/databases/{id}/users/{name}/access", a.setUserAccess)
	mux.HandleFunc("POST /api/databases/{id}/users/{name}/reset-password", a.resetUserPassword)
	mux.HandleFunc("DELETE /api/databases/{id}/users/{name}", a.deleteUser)
	mux.HandleFunc("POST /api/databases/{id}/reset-password", a.resetSuperuser)
	mux.HandleFunc("GET /api/databases/{id}/grants", a.listGrants)
	mux.HandleFunc("PUT /api/databases/{id}/grants/{userID}", a.setGrant)
	mux.HandleFunc("DELETE /api/databases/{id}/grants/{userID}", a.deleteGrant)
	mux.HandleFunc("GET /api/databases/{id}/backups", a.listBackups)
	mux.HandleFunc("POST /api/databases/{id}/backups", a.startBackup)
	mux.HandleFunc("GET /api/databases/{id}/backups/{backupID}/download", a.downloadBackup)
	mux.HandleFunc("DELETE /api/databases/{id}/backups/{backupID}", a.deleteBackup)
	mux.HandleFunc("PUT /api/databases/{id}/backups/{backupID}/pin", a.pinBackup)
	mux.HandleFunc("POST /api/databases/{id}/backups/{backupID}/retry-upload", a.retryUpload)
	mux.HandleFunc("GET /api/databases/{id}/schedule", a.getSchedule)
	mux.HandleFunc("PUT /api/databases/{id}/schedule", a.putSchedule)
	mux.HandleFunc("GET /api/databases/{id}/restores", a.listRestores)
	mux.HandleFunc("POST /api/databases/{id}/restores", a.startRestore)
	mux.HandleFunc("POST /api/databases/{id}/restores/upload", a.uploadRestore)
	mux.HandleFunc("POST /api/databases/{id}/query", a.query)
	mux.HandleFunc("GET /api/databases/{id}/query/schema", a.querySchema)
	mux.HandleFunc("POST /api/databases/{id}/redis/command", a.redisCommand)
	mux.HandleFunc("GET /api/databases/{id}/redis/keys", a.redisKeys)
	mux.HandleFunc("GET /api/databases/{id}/redis/key", a.redisKey)
}

// DatabaseJSON is a Database as the browser sees it. It never carries the
// password: only GET .../credentials does, and that read is audited.
type DatabaseJSON struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Slug    string `json:"slug"`
	Engine  string `json:"engine"`
	Version string `json:"version"`
	Image   string `json:"image"`
	Desired string `json:"desired"`
	Status  string `json:"status"`
	// SettingsOutdated: this panel would create the container with newer
	// settings; Apply settings or Update image applies them.
	SettingsOutdated bool      `json:"settingsOutdated"`
	LastError        string    `json:"lastError"`
	Warnings         []string  `json:"warnings"`
	MemoryMB         int64     `json:"memoryMb"`
	CPUs             float64   `json:"cpus"`
	Host             string    `json:"host"`
	HostReadable     string    `json:"hostReadable"`
	Port             int       `json:"port"`
	User             string    `json:"user"`
	DefaultDatabase  string    `json:"defaultDatabase"`
	CreatedAt        time.Time `json:"createdAt"`
	// Job is the job running on it ("backup", "restore", "update_image" or
	// ""); Deleted marks a tombstone that only keeps its Backups.
	Job     string `json:"job"`
	Deleted bool   `json:"deleted"`
	// Access is set for a grantee: how they reach this Database.
	Access *AccessJSON `json:"access,omitempty"`
}

// AccessJSON is a grantee's own grant.
type AccessJSON struct {
	DBUser string `json:"dbUser,omitempty"`
	Level  string `json:"level,omitempty"`
}

func toJSON(v View) DatabaseJSON {
	out := DatabaseJSON{
		ID: v.ID, Name: v.Name, Slug: v.Slug, Engine: v.Engine, Version: v.Version,
		Desired: v.Desired, Status: v.Status, SettingsOutdated: v.SettingsOutdated, LastError: v.LastError, Warnings: v.Warnings,
		MemoryMB: v.MemoryMB, CPUs: v.CPUs, Host: ContainerName(v.ID), HostReadable: Alias(v.Slug),
		CreatedAt: v.CreatedAt, Job: v.Job, Deleted: v.Deleted,
	}
	if out.Warnings == nil {
		out.Warnings = []string{}
	}
	if e, ok := Engines[v.Engine]; ok {
		out.Image, out.Port, out.User, out.DefaultDatabase = e.Image(v.Version), e.Port(), e.User(), e.DefaultDatabase()
	}
	return out
}

func (a *API) list(w http.ResponseWriter, r *http.Request) {
	if _, ok := authz.Member(r.Context()); ok {
		// A non-Admin sees only the Databases their grants name.
		if user, ok := auth.UserFrom(r.Context()); ok {
			a.granteeList(w, r, user)
			return
		}
	}
	views, err := a.s.List(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]DatabaseJSON, 0, len(views))
	for _, v := range views {
		out = append(out, toJSON(v))
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) engines(w http.ResponseWriter, _ *http.Request) {
	type version struct {
		Tag     string `json:"tag"`
		Default bool   `json:"default"`
	}
	type engine struct {
		Name     string    `json:"name"`
		Label    string    `json:"label"`
		Port     int       `json:"port"`
		Versions []version `json:"versions"`
	}
	out := []engine{}
	for _, e := range Engines {
		item := engine{Name: e.Name(), Label: e.Label(), Port: e.Port(), Versions: []version{}}
		for _, v := range e.Versions() {
			item.Versions = append(item.Versions, version{Tag: v.Tag, Default: v.Default})
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	writeJSON(w, http.StatusOK, out)
}

func (a *API) create(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name     string  `json:"name"`
		Engine   string  `json:"engine"`
		Version  string  `json:"version"`
		MemoryMB int64   `json:"memoryMb"`
		CPUs     float64 `json:"cpus"`
	}
	if !decode(w, r, &in) {
		return
	}
	audit.Target(r.Context(), strings.TrimSpace(in.Name))
	d, err := a.s.Create(r.Context(), CreateInput{Name: in.Name, Engine: in.Engine, Version: in.Version, MemoryMB: in.MemoryMB, CPUs: in.CPUs})
	if err != nil {
		writeError(w, err)
		return
	}
	v, err := a.s.Get(r.Context(), d.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toJSON(v))
}

// id reads the {id} path value; a bad one is a missing Database.
func id(w http.ResponseWriter, r *http.Request) (int64, bool) {
	n, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || n < 1 {
		writeError(w, ErrNotFound)
		return 0, false
	}
	return n, true
}

// target names the Database in the audit event.
func (a *API) target(r *http.Request, n int64) {
	if d, err := a.s.d.Store.Get(r.Context(), n); err == nil {
		audit.Target(r.Context(), d.Name)
	}
}

func (a *API) get(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	c, ok := callerOf(r)
	if !ok {
		writeError(w, ErrNotFound)
		return
	}
	v, err := a.s.Get(r.Context(), n)
	if err != nil {
		writeError(w, err)
		return
	}
	if !c.Admin {
		writeJSON(w, http.StatusOK, granteeJSON(v, c.Grant))
		return
	}
	writeJSON(w, http.StatusOK, toJSON(v))
}

func (a *API) rename(w http.ResponseWriter, r *http.Request) {
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
	if err := a.s.Rename(r.Context(), n, in.Name); err != nil {
		writeError(w, err)
		return
	}
	audit.Detail(r.Context(), "name", strings.TrimSpace(in.Name))
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) action(run func(ctx context.Context, id int64) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n, ok := id(w, r)
		if !ok {
			return
		}
		a.target(r, n)
		if err := run(r.Context(), n); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// updateImage takes an optional {backupFirst}. With it true the update is a
// job: 202 at once, and 409 when the Database is not running. Otherwise 204.
func (a *API) updateImage(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	var in struct {
		BackupFirst bool `json:"backupFirst"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil && !errors.Is(err, io.EOF) {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}
	a.target(r, n)
	if err := a.s.UpdateImage(r.Context(), n, in.BackupFirst); err != nil {
		writeError(w, err)
		return
	}
	if in.BackupFirst {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) delete(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	var in struct {
		Confirm       string `json:"confirm"`
		DeleteBackups bool   `json:"deleteBackups"`
	}
	if !decode(w, r, &in) {
		return
	}
	a.target(r, n)
	if err := a.s.Delete(r.Context(), n, in.Confirm, in.DeleteBackups); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// credentials answers with the superuser's password, or with a managed user's
// when ?user= names one; ?database= picks the database in the URL (the
// engine's default otherwise). Either read is audited as a secret read.
func (a *API) credentials(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	d, err := a.s.d.Store.Get(r.Context(), n)
	if err == nil && d.DeletedAt != nil {
		err = ErrNotFound
	}
	if err != nil {
		writeError(w, err)
		return
	}
	eng, err := Lookup(d.Engine, d.Version)
	if err != nil {
		writeError(w, err)
		return
	}
	c, okc := callerOf(r)
	if !okc {
		writeError(w, ErrNotFound)
		return
	}
	user, password, database := eng.User(), d.Password, eng.DefaultDatabase()
	want := r.URL.Query().Get("user")
	if !c.Admin {
		// A grantee only ever gets its own user; ?user is ignored. A Redis
		// grant has only the superuser's password, which a non-Admin never
		// sees.
		if c.Grant.DBUser == "" {
			writeJSONError(w, http.StatusForbidden, "a Redis grant has no credentials to show")
			return
		}
		want = c.Grant.DBUser
		// The password works outside the panel's restricted sessions, so a
		// user that has grown server-wide powers is not revealed either.
		if err := a.s.refuseServerWide(r.Context(), n, want); err != nil {
			writeError(w, err)
			return
		}
		if r.URL.Query().Get("database") == "" {
			if own, err := a.s.GranteeDatabases(r.Context(), n, want); err == nil && len(own) > 0 {
				database = own[0]
			}
		}
	}
	if want != "" && want != eng.User() {
		if db := r.URL.Query().Get("database"); db != "" {
			if !targetPattern.MatchString(db) {
				writeJSONError(w, http.StatusUnprocessableEntity, ErrBadTarget.Error())
				return
			}
			database = db
		}
		pw, err := a.s.UserCredentials(r.Context(), n, want)
		if err != nil {
			writeError(w, err)
			return
		}
		user, password = want, pw
	}
	audit.Target(r.Context(), d.Name)
	audit.Detail(r.Context(), "user", user)
	audit.Emit(r.Context(), "secret.read", map[string]any{"keys": []string{"password"}, "user": user})
	url := eng.UserURL(ContainerName(d.ID), user, password, database)
	readable := ""
	if alias := Alias(d.Slug); alias != "" {
		readable = eng.UserURL(alias, user, password, database)
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"user": user, "password": password, "url": url, "urlReadable": readable, "env": "DATABASE_URL=" + url,
	})
}

func (a *API) logs(w http.ResponseWriter, r *http.Request) {
	n, ok := id(w, r)
	if !ok {
		return
	}
	tail := 200
	if raw := r.URL.Query().Get("tail"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > 2000 {
			writeJSONError(w, http.StatusBadRequest, "tail must be between 1 and 2000")
			return
		}
		tail = v
	}
	logs, err := a.s.Logs(r.Context(), n, tail)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"logs": logs})
}

func decode(w http.ResponseWriter, r *http.Request, out any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(out); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

// writeError maps a service error to its status.
func writeError(w http.ResponseWriter, err error) {
	var unknownLogical ErrUnknownLogical
	var forbidden ErrForbiddenStatement
	var invalid ErrInvalidSchedule
	var fullOnSuper ErrFullOnSuperuserDB
	var reserved ErrReservedUser
	var ownerAccess ErrOwnerAccess
	var protected ErrProtectedDatabase
	var userOwns ErrUserOwns
	var userOwnsIn ErrUserOwnsIn
	var userExists ErrUserExists
	var dbExists ErrDatabaseExists
	var unknownUser ErrUnknownUser
	var admin adminError
	var grantToAdmin ErrGrantToAdmin
	var badGrant ErrBadGrant
	var serverWide ErrServerWideUser
	var userInUse ErrUserInUse
	switch {
	case errors.As(err, &grantToAdmin), errors.As(err, &badGrant), errors.As(err, &serverWide):
		writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.As(err, &userInUse):
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "users": userInUse.Emails})
	case errors.Is(err, ErrUnknownPanelUser):
		writeJSONError(w, http.StatusNotFound, err.Error())
	case errors.As(err, &invalid), errors.As(err, &fullOnSuper), errors.As(err, &reserved), errors.As(err, &ownerAccess),
		errors.As(err, &protected), errors.Is(err, ErrBadUserName):
		writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.As(err, &userOwns), errors.As(err, &userOwnsIn), errors.As(err, &userExists), errors.As(err, &dbExists), errors.Is(err, ErrPendingPassword), errors.Is(err, ErrUserNotManaged):
		writeJSONError(w, http.StatusConflict, err.Error())
	case errors.As(err, &unknownUser):
		writeJSONError(w, http.StatusNotFound, err.Error())
	case errors.As(err, &admin):
		writeJSONError(w, http.StatusBadGateway, err.Error())
	case errors.Is(err, ErrBadAccess):
		writeJSONError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrInvalidName), errors.Is(err, ErrUnknownEngine), errors.Is(err, ErrUnknownVersion), errors.Is(err, ErrConfirm), errors.Is(err, ErrInvalidLimits),
		errors.Is(err, ErrNoBackups), errors.Is(err, ErrNoScope), errors.As(err, &unknownLogical),
		errors.Is(err, ErrUnsafeName), errors.Is(err, ErrBadTarget), errors.Is(err, ErrNothingToBackUp), errors.Is(err, ErrNotRestorable),
		errors.Is(err, ErrBadUpload), errors.Is(err, ErrNoQuery), errors.Is(err, ErrNoConsole), errors.As(err, &forbidden):
		writeJSONError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrNotFound):
		writeJSONError(w, http.StatusNotFound, "Database not found")
	case errors.Is(err, ErrDuplicateName), errors.Is(err, ErrLocked), errors.Is(err, ErrBusy), errors.Is(err, ErrNotRunning), errors.Is(err, ErrChangedFile), errors.Is(err, ErrRemoteOnly), errors.Is(err, ErrNotScheduled), errors.Is(err, ErrNoFailedUpload):
		writeJSONError(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrRemoteDelete):
		writeJSONError(w, http.StatusBadGateway, err.Error())
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
