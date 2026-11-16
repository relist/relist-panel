package databases

import (
	"context"
	"errors"
	"strings"

	"relist-panel/internal/dbquery"
)

// ErrGrantToAdmin refuses a grant to an Admin, who already reaches every
// Database.
type ErrGrantToAdmin struct{ Email string }

func (e ErrGrantToAdmin) Error() string {
	return e.Email + " is an Admin and already reaches every Database"
}

// ErrBadGrant is a grant whose shape does not fit the engine.
type ErrBadGrant struct{ Reason string }

func (e ErrBadGrant) Error() string { return e.Reason }

// ErrServerWideUser refuses a managed user whose powers go beyond its own
// databases (dbquery.ServerWideMessage).
type ErrServerWideUser struct{ User string }

func (e ErrServerWideUser) Error() string { return dbquery.ServerWideMessage(e.User) }

// ErrUserInUse is a Delete user that would take panel users' access with it;
// confirming deletes both.
type ErrUserInUse struct {
	User   string
	Emails []string
}

func (e ErrUserInUse) Error() string {
	return e.User + " is used by " + strings.Join(e.Emails, ", ") + "; confirm to delete it and their access"
}

// Grants lists a Database's grants.
func (s *Service) Grants(ctx context.Context, id int64) ([]Grant, error) {
	if _, err := s.liveDatabase(ctx, id); err != nil {
		return nil, err
	}
	return s.d.Store.Grants(ctx, id)
}

// DeleteGrant removes one panel user's grant.
func (s *Service) DeleteGrant(ctx context.Context, id, userID int64) error {
	if _, err := s.liveDatabase(ctx, id); err != nil {
		return err
	}
	return s.d.Store.DeleteGrant(ctx, id, userID)
}

// liveDatabase is the Database unless it is missing or a tombstone.
func (s *Service) liveDatabase(ctx context.Context, id int64) (Database, error) {
	d, err := s.d.Store.Get(ctx, id)
	if err != nil {
		return Database{}, err
	}
	if d.DeletedAt != nil {
		return Database{}, ErrNotFound
	}
	return d, nil
}

// SetGrant gives panel user userID access to Database id: as managed user
// dbUser on a SQL engine, or at level on Redis. A SQL grant needs the
// Database running, because the server-wide check connects to it; the same
// check runs again before every grantee run, since privileges change.
func (s *Service) SetGrant(ctx context.Context, id, userID int64, dbUser string, level RedisLevel) error {
	d, err := s.liveDatabase(ctx, id)
	if err != nil {
		return err
	}
	email, isAdmin, err := s.d.Store.PanelUser(ctx, userID)
	if err != nil {
		return err
	}
	if isAdmin {
		return ErrGrantToAdmin{Email: email}
	}
	if d.Engine == "redis" {
		if dbUser != "" {
			return ErrBadGrant{Reason: "a database user is only for SQL engines; pick Read or Read-write"}
		}
		if !level.Valid() {
			return ErrBadGrant{Reason: "pick Read or Read-write"}
		}
		return s.d.Store.SetGrant(ctx, Grant{DatabaseID: id, UserID: userID, Level: level})
	}
	if level != "" {
		return ErrBadGrant{Reason: "a level is only for Redis; pick a database user"}
	}
	if dbUser == "" {
		return ErrBadGrant{Reason: "pick a database user"}
	}
	if _, err := s.d.Store.GetUser(ctx, id, dbUser); err != nil {
		if errors.Is(err, ErrUserNotManaged) {
			return ErrBadGrant{Reason: dbUser + ": " + ErrUserNotManaged.Error()}
		}
		return err
	}
	_, t, a, err := s.adminRead(ctx, id)
	if err != nil {
		return err
	}
	rows, err := s.d.Admin.Rows(ctx, t, d.Engine, "", a.ServerWideQuery(dbUser))
	if err != nil {
		return scrubErr(err, d.Password)
	}
	if a.ServerWide(rows) {
		return ErrServerWideUser{User: dbUser}
	}
	return s.d.Store.SetGrant(ctx, Grant{DatabaseID: id, UserID: userID, DBUser: dbUser})
}

// GranteeDatabases lists the logical databases a grant's user can work in:
// the ones it owns, and the ones where its Access is not None.
func (s *Service) GranteeDatabases(ctx context.Context, id int64, dbUser string) ([]string, error) {
	d, t, a, err := s.adminRead(ctx, id)
	if err != nil {
		return nil, err
	}
	dbs, err := s.logicalList(ctx, d, t, a)
	if err != nil {
		return nil, err
	}
	acc, err := s.accessRows(ctx, d, t, a)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, l := range dbs {
		lvl := acc[dbUser][l.Name]
		if (d.Engine == "postgres" && l.Owner == dbUser) || (lvl != "" && lvl != AccessNone) {
			out = append(out, l.Name)
		}
	}
	return out, nil
}
