package authgroups

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"relist-panel/internal/audit"
	"relist-panel/internal/authz"
	"relist-panel/internal/grants"
)

type Service struct {
	store   *Store
	dataDir string
	running RunningRefs
}

// RunningRefs lists the running Containers whose Traefik routers name
// middleware (e.g. "relist-auth-3@file").
type RunningRefs func(ctx context.Context, middleware string) ([]string, error)

// WithRunning makes a delete also refuse while a running Container still
// routes through the group: a Domain switched away from it but not redeployed
// keeps the old labels, and Traefik drops a router whose middleware is gone.
// Without it (tests, no Docker) only the Domains in the database count.
func (s *Service) WithRunning(running RunningRefs) *Service {
	s.running = running
	return s
}

func New(db *sql.DB, dataDir string) *Service {
	return &Service{store: NewStore(db), dataDir: dataDir}
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/auth-group-options", s.options)
	mux.HandleFunc("GET /api/auth-groups", s.list)
	mux.HandleFunc("POST /api/auth-groups", s.create)
	mux.HandleFunc("GET /api/auth-groups/{id}", s.detail)
	mux.HandleFunc("PATCH /api/auth-groups/{id}", s.rename)
	mux.HandleFunc("DELETE /api/auth-groups/{id}", s.delete)
	mux.HandleFunc("POST /api/auth-groups/{id}/users", s.addUser)
	mux.HandleFunc("PATCH /api/auth-groups/{id}/users/{uid}", s.updateUser)
	mux.HandleFunc("DELETE /api/auth-groups/{id}/users/{uid}", s.deleteUser)
	mux.HandleFunc("GET /api/auth-groups/{id}/users/{uid}/password", s.revealPassword)
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	groups, err := s.store.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list groups", "")
		return
	}
	writeJSON(w, http.StatusOK, groups)
}

// options lists the groups by name to a User who may edit a Domain
// somewhere: an admin, or a member who is an Editor in some Environment.
func (s *Service) options(w http.ResponseWriter, r *http.Request) {
	if access, member := authz.Member(r.Context()); member && !editsSomewhere(access) {
		writeJSON(w, http.StatusOK, []Option{})
		return
	}
	opts, err := s.store.Options(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list groups", "")
		return
	}
	writeJSON(w, http.StatusOK, opts)
}

func editsSomewhere(access grants.Access) bool {
	for _, id := range access.AllVisibleEnvironments() {
		if access.EnvironmentRole(id) >= grants.RoleEditor {
			return true
		}
	}
	return false
}

type groupBody struct {
	Name  string      `json:"name"`
	Users []UserInput `json:"users"`
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	var body groupBody
	if !readBody(w, r, &body) {
		return
	}
	name, ferr := CleanName(body.Name)
	if ferr != nil {
		writeFieldError(w, ferr)
		return
	}
	seen := map[string]bool{}
	for _, u := range body.Users {
		if ferr := CheckUsername(u.Username); ferr != nil {
			writeFieldError(w, ferr)
			return
		}
		if ferr := CheckPassword(u.Password); ferr != nil {
			writeFieldError(w, ferr)
			return
		}
		if seen[u.Username] {
			writeStoreError(w, usernameTaken(), "")
			return
		}
		seen[u.Username] = true
	}
	g, err := s.store.Create(r.Context(), name, body.Users)
	if err != nil {
		writeStoreError(w, err, "could not create group")
		return
	}
	audit.Target(r.Context(), g.Name)
	s.reconcileAndWrite(w, r, http.StatusCreated, g)
}

type groupDetail struct {
	Group
	Users   []User   `json:"users"`
	Domains []UsedBy `json:"domains"`
}

func (s *Service) detail(w http.ResponseWriter, r *http.Request) {
	id, ok := readID(w, r, "id")
	if !ok {
		return
	}
	g, err := s.store.Get(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "could not read group")
		return
	}
	users, err := s.store.Users(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read group", "")
		return
	}
	used, err := s.store.UsedBy(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read group", "")
		return
	}
	writeJSON(w, http.StatusOK, groupDetail{Group: g, Users: users, Domains: used})
}

func (s *Service) rename(w http.ResponseWriter, r *http.Request) {
	id, ok := readID(w, r, "id")
	if !ok {
		return
	}
	var body groupBody
	if !readBody(w, r, &body) {
		return
	}
	name, ferr := CleanName(body.Name)
	if ferr != nil {
		writeFieldError(w, ferr)
		return
	}
	g, err := s.store.Rename(r.Context(), id, name)
	if err != nil {
		writeStoreError(w, err, "could not rename group")
		return
	}
	s.reconcileAndWrite(w, r, http.StatusOK, g)
}

func (s *Service) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := readID(w, r, "id")
	if !ok {
		return
	}
	if s.running != nil {
		if _, err := s.store.Get(r.Context(), id); err != nil {
			writeStoreError(w, err, "could not delete group")
			return
		}
		containers, err := s.running(r.Context(), MiddlewareName(id)+"@file")
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "could not check which running containers use this group: "+err.Error(), "")
			return
		}
		if len(containers) > 0 {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":   "Running containers still use this group: " + strings.Join(containers, ", ") + ". Redeploy their Composes first.",
				"domains": []UsedBy{},
			})
			return
		}
	}
	used, err := s.store.Delete(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "could not delete group")
		return
	}
	if len(used) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":   "This group is used by a domain. Pick another group or None on those domains first.",
			"domains": used,
		})
		return
	}
	if err := Reconcile(r.Context(), s.store, s.dataDir); err != nil {
		writeReconcileError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) addUser(w http.ResponseWriter, r *http.Request) {
	id, ok := readID(w, r, "id")
	if !ok {
		return
	}
	var in UserInput
	if !readBody(w, r, &in) {
		return
	}
	if ferr := CheckUsername(in.Username); ferr != nil {
		writeFieldError(w, ferr)
		return
	}
	if ferr := CheckPassword(in.Password); ferr != nil {
		writeFieldError(w, ferr)
		return
	}
	u, err := s.store.AddUser(r.Context(), id, in)
	if err != nil {
		writeStoreError(w, err, "could not add user")
		return
	}
	audit.Detail(r.Context(), "username", u.Username)
	s.reconcileAndWrite(w, r, http.StatusCreated, u)
}

type userPatch struct {
	Username *string `json:"username"`
	Password *string `json:"password"`
}

func (s *Service) updateUser(w http.ResponseWriter, r *http.Request) {
	gid, uid, ok := readIDs(w, r)
	if !ok {
		return
	}
	var body userPatch
	if !readBody(w, r, &body) {
		return
	}
	if body.Username != nil {
		if ferr := CheckUsername(*body.Username); ferr != nil {
			writeFieldError(w, ferr)
			return
		}
	}
	if body.Password != nil {
		if ferr := CheckPassword(*body.Password); ferr != nil {
			writeFieldError(w, ferr)
			return
		}
	}
	u, err := s.store.UpdateUser(r.Context(), gid, uid, body.Username, body.Password)
	if err != nil {
		writeStoreError(w, err, "could not update user")
		return
	}
	audit.Detail(r.Context(), "username", u.Username)
	s.reconcileAndWrite(w, r, http.StatusOK, u)
}

func (s *Service) deleteUser(w http.ResponseWriter, r *http.Request) {
	gid, uid, ok := readIDs(w, r)
	if !ok {
		return
	}
	if u, err := s.store.GetUser(r.Context(), gid, uid); err == nil {
		audit.Detail(r.Context(), "username", u.Username)
	}
	if err := s.store.DeleteUser(r.Context(), gid, uid); err != nil {
		writeStoreError(w, err, "could not remove user")
		return
	}
	if err := Reconcile(r.Context(), s.store, s.dataDir); err != nil {
		writeReconcileError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) revealPassword(w http.ResponseWriter, r *http.Request) {
	gid, uid, ok := readIDs(w, r)
	if !ok {
		return
	}
	u, err := s.store.GetUser(r.Context(), gid, uid)
	if err != nil {
		writeStoreError(w, err, "could not read user")
		return
	}
	audit.Detail(r.Context(), "username", u.Username)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"password": u.Password})
}

// reconcileAndWrite hands the change to Traefik, then answers with v. The
// database already holds the change, so a failed write is repaired by the
// next Reconcile.
func (s *Service) reconcileAndWrite(w http.ResponseWriter, r *http.Request, status int, v any) {
	if err := Reconcile(r.Context(), s.store, s.dataDir); err != nil {
		writeReconcileError(w, err)
		return
	}
	writeJSON(w, status, v)
}

func writeReconcileError(w http.ResponseWriter, err error) {
	writeError(w, http.StatusInternalServerError, "the change was saved but Traefik's basic auth files could not be written: "+err.Error(), "")
}

func readID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id", "")
		return 0, false
	}
	return id, true
}

func readIDs(w http.ResponseWriter, r *http.Request) (int64, int64, bool) {
	gid, ok := readID(w, r, "id")
	if !ok {
		return 0, 0, false
	}
	uid, ok := readID(w, r, "uid")
	return gid, uid, ok
}

func readBody(w http.ResponseWriter, r *http.Request, into any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(into); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "")
		return false
	}
	return true
}

func writeStoreError(w http.ResponseWriter, err error, fallback string) {
	var conflict *ConflictError
	switch {
	case errors.As(err, &conflict):
		writeError(w, http.StatusConflict, conflict.Message, conflict.Field)
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not found", "")
	default:
		writeError(w, http.StatusInternalServerError, fallback, "")
	}
}

func writeFieldError(w http.ResponseWriter, ferr *FieldError) {
	writeError(w, http.StatusBadRequest, ferr.Message, ferr.Field)
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
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
