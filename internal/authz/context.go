package authz

import (
	"context"
	"net/http"
	"strconv"

	"relist-panel/internal/grants"
)

type (
	accessKey struct{}
	scopeKey  struct{}
	stillKey  struct{}
)

// WithAccess returns ctx carrying access, as Middleware does for every
// signed-in request. Tests that call handlers without the middleware use it.
func WithAccess(ctx context.Context, access grants.Access) context.Context {
	return context.WithValue(ctx, accessKey{}, access)
}

// AccessFrom is the signed-in User's Access.
func AccessFrom(ctx context.Context) (grants.Access, bool) {
	access, ok := ctx.Value(accessKey{}).(grants.Access)
	return access, ok
}

// ScopeFrom is the Project, Environment and Compose the request's path named,
// checked against the database.
func ScopeFrom(ctx context.Context) (Scope, bool) {
	scope, ok := ctx.Value(scopeKey{}).(Scope)
	return scope, ok
}

// CanReadSecrets reports whether the request may receive secret variable
// values of the Environment: an Editor there, or an admin. Without an Access
// in the context it is false.
func CanReadSecrets(ctx context.Context, environmentID int64) bool {
	return RoleIn(ctx, environmentID) >= grants.RoleEditor
}

// StillAllowed returns a check that repeats this request's authorization
// with a fresh read of the session and the Grants, for a connection that
// outlives the request (the Terminal). It reports false once the session
// ended or the User may no longer reach the route. Nil when the request did
// not pass through Middleware.
func StillAllowed(ctx context.Context) func(context.Context) (bool, error) {
	still, _ := ctx.Value(stillKey{}).(func(context.Context) (bool, error))
	return still
}

// Member returns the Access of a signed-in User who is not an admin, for a
// list to filter by. ok is false for an admin and for a request without an
// Access (a package test calling the handler directly), which see the full
// list as before.
func Member(ctx context.Context) (grants.Access, bool) {
	access, ok := AccessFrom(ctx)
	if !ok || access.Admin {
		return grants.Access{}, false
	}
	return access, true
}

// RoleIn is the signed-in User's effective role in an Environment; an admin
// is an Editor. Without an Access in the context it is RoleNone.
func RoleIn(ctx context.Context, environmentID int64) grants.Role {
	access, ok := AccessFrom(ctx)
	if !ok {
		return grants.RoleNone
	}
	return access.EnvironmentRole(environmentID)
}

// SecretsVisible reports whether r may receive secret variable values of the
// Environment its path names ({environmentID}): CanReadSecrets for it. A path
// without one reads nothing secret.
func SecretsVisible(r *http.Request) bool {
	id, err := strconv.ParseInt(r.PathValue("environmentID"), 10, 64)
	return err == nil && id > 0 && CanReadSecrets(r.Context(), id)
}
