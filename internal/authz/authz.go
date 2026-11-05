// Package authz decides what a signed-in User may reach. It runs inside
// auth.Middleware, which has already accepted the session, and looks the
// request's ServeMux pattern up in Routes. A pattern missing from Routes is
// admin-only: a new route is closed until someone classifies it
// (.scratch/multi-user/spec.md, ADR-0031).
//
// For a member it reads the Project, Environment and Compose from the path,
// checks in the database that they belong together, and compares the
// member's effective role there (package grants) with the route's Level:
// not visible answers 404, visible but too low 403.
package authz

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"relist-panel/internal/audit"
	"relist-panel/internal/auth"
	"relist-panel/internal/grants"
)

// Level is what a route requires of the signed-in User. The zero value is
// LevelAdmin, so a pattern missing from Routes is admin-only.
type Level int

const (
	// LevelAdmin is the default for every route not in Routes.
	LevelAdmin Level = iota
	// LevelPublic is a route auth lets through without a session; a
	// signed-in User reaches it too.
	LevelPublic
	// LevelSignedIn admits any User with a session.
	LevelSignedIn
	// LevelViewer, LevelOperator and LevelEditor need that role in the
	// path's Environment; on a Project-only path, LevelViewer needs the
	// Project to be visible.
	LevelViewer
	LevelOperator
	LevelEditor
	// LevelProjectEditor needs an editor Project Grant.
	LevelProjectEditor
)

// Rule is one route's requirement.
type Rule struct {
	Level Level
}

// Authorizer is the middleware's state: the database it reads Grants and
// parents from, and the session lookup a re-check repeats.
type Authorizer struct {
	db          *sql.DB
	currentUser func(*http.Request) (auth.User, bool, error)
	routes      map[string]Rule
	actions     map[string]string
	record      func(context.Context, audit.Event) error
	denied      *audit.Limiter
}

// New returns the Authorizer over Routes. currentUser is auth's session
// lookup (auth.Service.CurrentUser), used by StillAllowed.
func New(db *sql.DB, currentUser func(*http.Request) (auth.User, bool, error)) *Authorizer {
	return newWithRoutes(db, currentUser, Routes)
}

func newWithRoutes(db *sql.DB, currentUser func(*http.Request) (auth.User, bool, error), routes map[string]Rule) *Authorizer {
	a := &Authorizer{db: db, currentUser: currentUser, routes: routes, actions: Actions, denied: audit.NewLimiter(deniedPerMinute, time.Minute)}
	a.record = func(ctx context.Context, e audit.Event) error { return audit.Record(ctx, a.db, e) }
	return a
}

// WithRecorder replaces how events are stored (tests).
func (a *Authorizer) WithRecorder(record func(context.Context, audit.Event) error) *Authorizer {
	a.record = record
	return a
}

// Middleware authorizes every request that carries a User. A request
// without one passed auth as public. A request the mux would not route, or
// only to the "/" catch-all (webassets, which answers an unknown /api/ path
// 404), keeps its 404 or 405: refusing it would tell a member which paths
// exist.
func (a *Authorizer) Middleware(mux *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		_, pattern := mux.Handler(r)
		if pattern == "" || pattern == "/" {
			next.ServeHTTP(w, r)
			return
		}
		// The escaped path, split as the mux splits it: an encoded slash stays
		// inside its segment, so authz reads the same ids the handler will.
		path := r.URL.EscapedPath()
		action := a.actions[pattern]
		if r.Method == http.MethodHead {
			// ServeMux sends HEAD to the GET pattern; no body leaves, so a
			// HEAD is not a download.
			action = ""
		}
		pathScope, _ := scopeOf(pattern, path)
		event := audit.Event{
			ActorUserID: user.ID, Actor: user.Email, IP: peerIP(r),
			ProjectID: pathScope.ProjectID, EnvironmentID: pathScope.EnvironmentID, ComposeID: pathScope.ComposeID,
		}
		status, access, scope, err := a.decide(r.Context(), user, pattern, path)
		if err != nil {
			log.Printf("authz: %s %s: %v", r.Method, path, err)
			refuse(w, http.StatusInternalServerError, "authorization is temporarily unavailable")
			return
		}
		if status != 0 && action != "" && a.denied.Allow(fmt.Sprintf("%d %s", user.ID, action)) {
			denied := event
			denied.Action, denied.Outcome, denied.At = action, audit.OutcomeDenied, time.Now()
			if status == http.StatusNotFound {
				// The path's ids were never confirmed to belong together (or
				// name something hidden): naming them would show a hidden
				// Compose's name on a Project the member can read.
				denied.ProjectID, denied.EnvironmentID, denied.ComposeID = 0, 0, 0
				denied.Target = path
			} else {
				denied.Target = a.target(r.Context(), pattern, pathScope, path)
			}
			a.write(r.Context(), denied)
		}
		switch status {
		case http.StatusNotFound:
			refuse(w, status, "not found")
			return
		case http.StatusForbidden:
			refuse(w, status, "You don't have permission to do this")
			return
		}
		// The re-check reads the session from a copy of this request's
		// headers, after the request itself may be gone.
		base := r.Clone(context.Background())
		still := func(ctx context.Context) (bool, error) {
			current, ok, err := a.currentUser(base.WithContext(ctx))
			if err != nil || !ok {
				return false, err
			}
			status, _, _, err := a.decide(ctx, current, pattern, path)
			return err == nil && status == 0, err
		}
		ctx := WithAccess(r.Context(), access)
		ctx = context.WithValue(ctx, scopeKey{}, scope)
		ctx = context.WithValue(ctx, stillKey{}, still)
		resolve := func(ctx context.Context) string { return a.target(ctx, pattern, scope, path) }
		if action != "" {
			// Read the names now: after a delete they are gone.
			event.Target = resolve(ctx)
		}
		ctx, pending := audit.Begin(ctx, event, resolve, a.record)
		if action == "" {
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		if byteRange := r.Header.Get("Range"); byteRange != "" {
			// Still a download: only part of it.
			audit.Detail(ctx, "range", byteRange)
		}
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r.WithContext(ctx))
		if sw.status >= 200 && sw.status < 300 {
			e := pending.Event()
			e.Action, e.Outcome, e.At = action, audit.OutcomeOK, time.Now()
			if sw.failed {
				// The body broke off midway; what was sent still left.
				if e.Detail == nil {
					e.Detail = map[string]any{}
				}
				e.Detail["incomplete"] = true
			}
			a.write(ctx, e)
		}
	})
}

// write stores an event; a failure is logged and never changes the response.
func (a *Authorizer) write(ctx context.Context, e audit.Event) {
	if err := a.record(context.WithoutCancel(ctx), e); err != nil {
		log.Printf("audit: %s: %v", e.Action, err)
	}
}

// decide answers 0 to let the request through, or the status to refuse it
// with. An error is a database failure.
func (a *Authorizer) decide(ctx context.Context, user auth.User, pattern, path string) (int, grants.Access, Scope, error) {
	scope, ok := scopeOf(pattern, path)
	if user.IsAdmin {
		return 0, grants.Access{Admin: true}, scope, nil
	}
	access, err := grants.Load(ctx, a.db, user.ID, false)
	if err != nil {
		return 0, grants.Access{}, Scope{}, err
	}
	level := a.routes[pattern].Level
	if level == LevelPublic || level == LevelSignedIn {
		return 0, access, scope, nil
	}
	if !ok {
		return http.StatusNotFound, access, Scope{}, nil
	}
	if scope.ProjectID == 0 {
		// Admin-only, or a scoped level on a pattern without a Project:
		// refuse rather than guess.
		return http.StatusForbidden, access, scope, nil
	}
	if found, err := a.parentsMatch(ctx, scope); err != nil {
		return 0, grants.Access{}, Scope{}, err
	} else if !found {
		return http.StatusNotFound, access, scope, nil
	}
	role := grants.RoleNone
	if scope.EnvironmentID != 0 {
		role = access.EnvironmentRole(scope.EnvironmentID)
		if role == grants.RoleNone {
			return http.StatusNotFound, access, scope, nil
		}
	} else if !access.ProjectVisible(scope.ProjectID) {
		return http.StatusNotFound, access, scope, nil
	}
	allowed := false
	switch level {
	case LevelViewer:
		allowed = scope.EnvironmentID == 0 || role >= grants.RoleViewer
	case LevelOperator:
		allowed = scope.EnvironmentID != 0 && role >= grants.RoleOperator
	case LevelEditor:
		allowed = scope.EnvironmentID != 0 && role >= grants.RoleEditor
	case LevelProjectEditor:
		allowed = access.ProjectGrant(scope.ProjectID) == grants.RoleEditor
	}
	if !allowed {
		return http.StatusForbidden, access, scope, nil
	}
	return 0, access, scope, nil
}

// parentsMatch checks that the path's Compose lives in its Environment and
// the Environment in its Project, so a forged Project id cannot raise a role.
func (a *Authorizer) parentsMatch(ctx context.Context, scope Scope) (bool, error) {
	var err error
	switch {
	case scope.ComposeID != 0:
		var projectID, environmentID int64
		err = a.db.QueryRowContext(ctx,
			"SELECT e.project_id, c.environment_id FROM composes c JOIN environments e ON e.id = c.environment_id WHERE c.id = ?",
			scope.ComposeID).Scan(&projectID, &environmentID)
		if err == nil {
			return projectID == scope.ProjectID && environmentID == scope.EnvironmentID, nil
		}
	case scope.EnvironmentID != 0:
		var projectID int64
		err = a.db.QueryRowContext(ctx, "SELECT project_id FROM environments WHERE id = ?", scope.EnvironmentID).Scan(&projectID)
		if err == nil {
			return projectID == scope.ProjectID, nil
		}
	default:
		var one int
		err = a.db.QueryRowContext(ctx, "SELECT 1 FROM projects WHERE id = ?", scope.ProjectID).Scan(&one)
		if err == nil {
			return true, nil
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, err
}

func refuse(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
