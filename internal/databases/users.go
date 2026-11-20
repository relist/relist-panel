package databases

import (
	"context"
	"errors"
	"slices"
	"strings"

	"relist-panel/internal/dbquery"
)

var ErrBadAccess = errors.New("access is none, read or full")

// ErrUnknownUser is a user the Database does not have, or one the Users tab
// never lists (a system user).
type ErrUnknownUser struct{ Name string }

func (e ErrUnknownUser) Error() string { return "no such user: " + e.Name }

// ErrOwnerAccess is an attempt to lower the access of the user that owns a
// database.
type ErrOwnerAccess struct{ User, Database string }

func (e ErrOwnerAccess) Error() string {
	return e.User + " owns " + e.Database + "; an owner always has Full access"
}

// ErrFullOnSuperuserDB is Full on a Postgres database a superuser owns: Full
// is membership in the owner role, so the user would become that superuser's
// equal in every database it owns.
type ErrFullOnSuperuserDB struct{ Database, User string }

func (e ErrFullOnSuperuserDB) Error() string {
	fix := ""
	if e.User != "" && e.Database != "postgres" {
		// A database restored from a Backup without its state is owned by
		// postgres though its tables are the user's.
		fix = ", make the user its owner (" + ownerFix(e.Database, e.User) + " in the Query tab)"
	}
	return e.Database + " is owned by a superuser, so Full access would make the user one; give it Read-only" + fix + ", or create the database with an owning user"
}

// ownerFix is the statement that makes user the owner of database.
func ownerFix(database, user string) string {
	return "ALTER DATABASE " + pgIdent(database) + " OWNER TO " + pgIdent(user)
}

// ErrUserOwnsIn is changing the access of a non-owner on a Postgres database
// where it still owns tables or other objects: Read-only and None would revoke
// them, and Full's SET role would hide them.
type ErrUserOwnsIn struct {
	User, Database, Owner string
	// SuperOwner: Owner is a superuser, and the user's objects must not go
	// to it; the user becomes the owner instead.
	SuperOwner bool
}

func (e ErrUserOwnsIn) Error() string {
	if e.SuperOwner {
		return e.User + " owns tables or other objects in " + e.Database + ", which the superuser " + e.Owner + " owns; make " + e.User + " its owner first: " + ownerFix(e.Database, e.User) + " in the Query tab"
	}
	return e.User + " owns tables or other objects in " + e.Database + "; give them to " + e.Owner + " first, then change its access"
}

// ErrUserOwns is a Delete user of a Postgres role that still owns databases
// or objects.
type ErrUserOwns struct {
	User                 string
	Databases, ObjectsIn []string
}

func (e ErrUserOwns) Error() string {
	var parts []string
	if len(e.Databases) > 0 {
		parts = append(parts, "owns databases: "+strings.Join(e.Databases, ", "))
	}
	if len(e.ObjectsIn) > 0 {
		parts = append(parts, "owns objects in: "+strings.Join(e.ObjectsIn, ", "))
	}
	return e.User + " " + strings.Join(parts, "; ") + ". Give them another owner or drop them first"
}

// DBUser is a login user of a Database. Owns lists the databases it owns
// (Postgres); its access to them is fixed at Full. Access has one level for
// every logical database.
type DBUser struct {
	Name    string
	Managed bool
	Owns    []string
	Access  map[string]Access
}

// defaultDB is where server-level statements run.
func defaultDB(engine string) string {
	if engine == "postgres" {
		return "postgres"
	}
	return ""
}

// liveUsers reads the user names the Users tab lists.
func (s *Service) liveUsers(ctx context.Context, d Database, t dbquery.Target, a accountSQL) ([]string, error) {
	rows, err := s.d.Admin.Rows(ctx, t, d.Engine, "", a.UsersQuery())
	if err != nil {
		return nil, scrubErr(err, d.Password)
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if len(r) > 0 {
			out = append(out, r[0])
		}
	}
	return out, nil
}

// accessRows reads every user's access: user -> database -> level.
func (s *Service) accessRows(ctx context.Context, d Database, t dbquery.Target, a accountSQL) (map[string]map[string]Access, error) {
	rows, err := s.d.Admin.Rows(ctx, t, d.Engine, "", a.AccessQuery())
	if err != nil {
		return nil, scrubErr(err, d.Password)
	}
	out := map[string]map[string]Access{}
	for _, r := range rows {
		if len(r) < 3 {
			continue
		}
		if out[r[0]] == nil {
			out[r[0]] = map[string]Access{}
		}
		out[r[0]][r[1]] = Access(r[2])
	}
	return out, nil
}

// Users lists the live login users, merged with the ones whose password the
// panel stores. A stored user that no longer exists is left alone, not listed.
func (s *Service) Users(ctx context.Context, id int64) ([]DBUser, error) {
	d, t, a, err := s.adminRead(ctx, id)
	if err != nil {
		return nil, err
	}
	names, err := s.liveUsers(ctx, d, t, a)
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
	managed, err := s.d.Store.ListUsers(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]DBUser, 0, len(names))
	for _, n := range names {
		u := DBUser{Name: n, Owns: []string{}, Access: map[string]Access{}}
		u.Managed = slices.ContainsFunc(managed, func(m ManagedUser) bool { return m.Name == n })
		for _, l := range dbs {
			u.Access[l.Name] = AccessNone
			if lvl, ok := acc[n][l.Name]; ok {
				u.Access[l.Name] = lvl
			}
			if d.Engine == "postgres" && l.Owner == n {
				u.Owns = append(u.Owns, l.Name)
				u.Access[l.Name] = AccessFull
			}
		}
		out = append(out, u)
	}
	return out, nil
}

// requireUser is ErrUnknownUser unless name is a listed user.
func (s *Service) requireUser(ctx context.Context, d Database, t dbquery.Target, a accountSQL, name string) error {
	names, err := s.liveUsers(ctx, d, t, a)
	if err != nil {
		return err
	}
	if !slices.Contains(names, name) {
		return ErrUnknownUser{Name: name}
	}
	return nil
}

// CreateUser makes a login user with a generated password the panel stores.
func (s *Service) CreateUser(ctx context.Context, id int64, name string) error {
	d, t, a, err := s.adminTarget(ctx, id)
	if err != nil {
		return err
	}
	if err := checkUserName(d.Engine, name); err != nil {
		return err
	}
	names, err := s.liveUsers(ctx, d, t, a)
	if err != nil {
		return err
	}
	if slices.Contains(names, name) {
		return ErrUserExists{Name: name}
	}
	pw, err := newPassword()
	if err != nil {
		return err
	}
	if err := s.d.Admin.Exec(ctx, t, d.Engine, defaultDB(d.Engine), a.CreateUser(name, pw)); err != nil {
		return scrubErr(err, d.Password, pw)
	}
	if err := s.d.Store.UpsertUser(ctx, id, name, pw); err != nil {
		_ = s.d.Admin.Exec(context.WithoutCancel(ctx), t, d.Engine, defaultDB(d.Engine), a.DropUser(name))
		return err
	}
	return nil
}

// ResetUserPassword gives a user a new generated password; the panel stores it
// from then on. The row is written first, so a crash never leaves the server
// with a password the panel does not have; a failure puts the old row back.
func (s *Service) ResetUserPassword(ctx context.Context, id int64, name string) error {
	d, t, a, err := s.adminTarget(ctx, id)
	if err != nil {
		return err
	}
	if err := s.requireUser(ctx, d, t, a, name); err != nil {
		return err
	}
	pw, err := newPassword()
	if err != nil {
		return err
	}
	old, oldErr := s.d.Store.GetUser(ctx, id, name)
	if err := s.d.Store.UpsertUser(ctx, id, name, pw); err != nil {
		return err
	}
	if err := s.d.Admin.Exec(ctx, t, d.Engine, defaultDB(d.Engine), a.SetPassword(name, pw)); err != nil {
		cctx := context.WithoutCancel(ctx)
		if oldErr == nil {
			_ = s.d.Store.UpsertUser(cctx, id, name, old.Password)
		} else {
			_ = s.d.Store.DeleteUser(cctx, id, name)
		}
		return scrubErr(err, d.Password, pw)
	}
	return nil
}

// SetUserAccess applies the given levels. Every check runs before any
// statement; each listed database is applied again, unchanged levels
// included, which picks up schemas added since.
func (s *Service) SetUserAccess(ctx context.Context, id int64, name string, access map[string]Access) error {
	d, t, a, err := s.adminTarget(ctx, id)
	if err != nil {
		return err
	}
	if err := s.requireUser(ctx, d, t, a, name); err != nil {
		return err
	}
	dbs, err := s.logicalList(ctx, d, t, a)
	if err != nil {
		return err
	}
	byName := map[string]LogicalDatabase{}
	for _, l := range dbs {
		byName[l.Name] = l
	}
	names := make([]string, 0, len(access))
	var missing []string
	for n, lvl := range access {
		if lvl != AccessNone && lvl != AccessRead && lvl != AccessFull {
			return ErrBadAccess
		}
		if _, ok := byName[n]; !ok {
			missing = append(missing, n)
		}
		names = append(names, n)
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return ErrUnknownLogical{Names: missing}
	}
	slices.Sort(names)
	acc, err := s.accessRows(ctx, d, t, a)
	if err != nil {
		return err
	}
	var supers []string
	if q := a.SuperuserRolesQuery(); q != "" {
		rows, err := s.d.Admin.Rows(ctx, t, d.Engine, "", q)
		if err != nil {
			return scrubErr(err, d.Password)
		}
		for _, r := range rows {
			if len(r) > 0 {
				supers = append(supers, r[0])
			}
		}
	}
	for _, n := range names {
		owner := byName[n].Owner
		if d.Engine == "postgres" && owner == name && access[n] != AccessFull {
			return ErrOwnerAccess{User: name, Database: n}
		}
		if d.Engine == "postgres" && access[n] == AccessFull && owner != name && slices.Contains(supers, owner) {
			return ErrFullOnSuperuserDB{Database: n, User: name}
		}
	}
	fromOf := func(n string) Access {
		if l := acc[name][n]; l != "" {
			return l
		}
		return AccessNone
	}
	// A non-owner that owns objects in a database keeps its level there: None
	// and Read-only would revoke its own tables, and Full's SET role would hide
	// them. A change is refused; an unchanged level is left alone, so a Save
	// that changes other databases still works.
	keep := map[string]bool{}
	if q := a.OwnsInQuery(name); q != "" {
		rows, err := s.d.Admin.Rows(ctx, t, d.Engine, "", q)
		if err != nil {
			return scrubErr(err, d.Password)
		}
		ownsIn := map[string]bool{}
		for _, r := range rows {
			if len(r) > 0 {
				ownsIn[r[0]] = true
			}
		}
		for _, n := range names {
			if byName[n].Owner == name || !ownsIn[n] {
				continue
			}
			if access[n] != fromOf(n) {
				owner := byName[n].Owner
				return ErrUserOwnsIn{User: name, Database: n, Owner: owner, SuperOwner: slices.Contains(supers, owner)}
			}
			keep[n] = true
		}
	}
	for _, n := range names {
		if keep[n] {
			continue
		}
		owner := byName[n].Owner
		from := fromOf(n)
		if stmts := a.ServerAccess(name, n, owner, from, access[n]); len(stmts) > 0 {
			if err := s.d.Admin.Exec(ctx, t, d.Engine, defaultDB(d.Engine), stmts); err != nil {
				return scrubErr(err, d.Password)
			}
		}
		if d.Engine != "postgres" || owner == name {
			continue
		}
		rows, err := s.d.Admin.Rows(ctx, t, d.Engine, n, a.SchemasQuery())
		if err != nil {
			return scrubErr(err, d.Password)
		}
		var schemas []string
		for _, r := range rows {
			if len(r) > 0 {
				schemas = append(schemas, r[0])
			}
		}
		if stmts := a.InDatabaseAccess(name, owner, schemas, access[n]); len(stmts) > 0 {
			if err := s.d.Admin.Exec(ctx, t, d.Engine, n, stmts); err != nil {
				return scrubErr(err, d.Password)
			}
		}
	}
	return nil
}

// DeleteUser drops a user. Postgres refuses while the role owns a database or
// objects, naming them; otherwise what it was granted is dropped first.
func (s *Service) DeleteUser(ctx context.Context, id int64, name string) error {
	d, t, a, err := s.adminTarget(ctx, id)
	if err != nil {
		return err
	}
	if err := s.requireUser(ctx, d, t, a, name); err != nil {
		return err
	}
	if q := a.OwnedQuery(name); q != "" {
		rows, err := s.d.Admin.Rows(ctx, t, d.Engine, "", q)
		if err != nil {
			return scrubErr(err, d.Password)
		}
		owns := ErrUserOwns{User: name}
		for _, r := range rows {
			if len(r) < 2 {
				continue
			}
			switch r[0] {
			case "database":
				owns.Databases = append(owns.Databases, r[1])
			case "objects":
				owns.ObjectsIn = append(owns.ObjectsIn, r[1])
			}
		}
		if len(owns.Databases) > 0 || len(owns.ObjectsIn) > 0 {
			return owns
		}
	}
	if q := a.PrivilegedDatabasesQuery(name); q != "" {
		rows, err := s.d.Admin.Rows(ctx, t, d.Engine, "", q)
		if err != nil {
			return scrubErr(err, d.Password)
		}
		for _, r := range rows {
			if len(r) == 0 {
				continue
			}
			if err := s.d.Admin.Exec(ctx, t, d.Engine, r[0], a.DropOwned(name)); err != nil {
				return scrubErr(err, d.Password)
			}
		}
	}
	if err := s.d.Admin.Exec(ctx, t, d.Engine, defaultDB(d.Engine), a.DropUser(name)); err != nil {
		return scrubErr(err, d.Password)
	}
	return s.d.Store.DeleteUser(ctx, id, name)
}

// UserCredentials is a managed user's password, or ErrUserNotManaged.
func (s *Service) UserCredentials(ctx context.Context, id int64, name string) (string, error) {
	d, err := s.d.Store.Get(ctx, id)
	if err != nil {
		return "", err
	}
	if d.DeletedAt != nil {
		return "", ErrNotFound
	}
	u, err := s.d.Store.GetUser(ctx, id, name)
	if err != nil {
		return "", err
	}
	return u.Password, nil
}
