// Package authz decides what a signed-in User may reach. It runs inside
// auth.Middleware, which has already accepted the session, and looks the
// request's ServeMux pattern up in Routes. A pattern missing from Routes is
// admin-only: a new route is closed until someone classifies it
// (.scratch/multi-user/spec.md). Phase 2 adds the scoped levels Viewer,
// Operator and Editor between LevelAdmin and LevelSignedIn.
package authz

import (
	"encoding/json"
	"net/http"

	"relist-panel/internal/auth"
)

// Level is what a route requires of the signed-in User.
type Level int

const (
	// LevelAdmin is the default for every route not in Routes.
	LevelAdmin Level = iota
	// LevelSignedIn admits any User with a session.
	LevelSignedIn
)

// Rule is one route's requirement.
type Rule struct {
	Level Level
}

// Middleware refuses a signed-in non-admin any route whose rule is not
// LevelSignedIn. A request without a User passed auth as public, and one the
// mux would not route keeps its 404 or 405.
func Middleware(mux *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok || user.IsAdmin {
			next.ServeHTTP(w, r)
			return
		}
		_, pattern := mux.Handler(r)
		if pattern == "" || Routes[pattern].Level == LevelSignedIn {
			next.ServeHTTP(w, r)
			return
		}
		forbid(w)
	})
}

func forbid(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "You don't have permission to do this"})
}
