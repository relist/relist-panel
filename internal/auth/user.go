package auth

import "context"

// OwnerID is the User who is always an admin: the web can never disable,
// delete or demote it (.scratch/multi-user/spec.md).
const OwnerID int64 = 1

// User is the signed-in User a request carries once Middleware accepts its
// session.
type User struct {
	ID      int64
	Email   string
	IsAdmin bool
}

// IsOwner reports whether u is the Owner.
func (u User) IsOwner() bool { return u.ID == OwnerID }

type userKey struct{}

// WithUser returns ctx carrying u, as Middleware does for a signed-in
// request.
func WithUser(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, userKey{}, u)
}

// UserFrom returns the signed-in User of a request's context.
func UserFrom(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(userKey{}).(User)
	return u, ok
}
