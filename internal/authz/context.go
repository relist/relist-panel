package authz

import (
	"context"

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
	access, ok := AccessFrom(ctx)
	return ok && access.EnvironmentRole(environmentID) >= grants.RoleEditor
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
