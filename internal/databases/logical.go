package databases

import (
	"context"
	"slices"
	"strconv"

	"relist-panel/internal/dbquery"
)

// ErrDatabaseExists is a Create database for a name that is taken.
type ErrDatabaseExists struct{ Name string }

func (e ErrDatabaseExists) Error() string { return "a database named " + e.Name + " already exists" }

// ErrUserExists is a Create user, or a Create database with an owning user,
// for a name that is taken.
type ErrUserExists struct{ Name string }

func (e ErrUserExists) Error() string { return "a user named " + e.Name + " already exists" }

// ErrProtectedDatabase is a Drop of a database that is never dropped: the
// default one, or one that does not exist.
type ErrProtectedDatabase struct{ Name string }

func (e ErrProtectedDatabase) Error() string { return e.Name + " cannot be dropped" }

// LogicalDatabase is one database inside a Database. Owner is Postgres's,
// Charset MySQL's and MariaDB's. Protected is never offered for Drop.
type LogicalDatabase struct {
	Name              string
	SizeBytes         int64
	Owner, Charset    string
	AnyUserCanConnect bool
	Protected         bool
}

// scrubAll removes every password from text that goes to the browser, the
// audit log or LastError.
func scrubAll(text string, passwords ...string) string {
	for _, p := range passwords {
		text = scrub(text, p)
	}
	return text
}

// adminError is what the server answered to an admin call, scrubbed. It is a
// bad gateway to the browser, not a fault of the panel.
type adminError struct{ msg string }

func (e adminError) Error() string { return e.msg }

// scrubErr is err with every password removed; nil stays nil. The error
// loses its type, which is fine: only the server's text is left.
func scrubErr(err error, passwords ...string) error {
	if err == nil {
		return nil
	}
	return adminError{scrubAll(err.Error(), passwords...)}
}

// adminRead checks a Database can take an admin read and says where it is:
// running, an SQL engine, no Migration lock.
func (s *Service) adminRead(ctx context.Context, id int64) (Database, dbquery.Target, accountSQL, error) {
	d, t, err := s.queryTarget(ctx, id, false)
	if err != nil {
		return Database{}, dbquery.Target{}, nil, err
	}
	a, err := accountsFor(d.Engine)
	if err != nil {
		return Database{}, dbquery.Target{}, nil, err
	}
	return d, t, a, nil
}

// adminTarget is adminRead for a write: ErrBusy while any job runs on the
// Database, so a write never interleaves with a Backup or a restore.
func (s *Service) adminTarget(ctx context.Context, id int64) (Database, dbquery.Target, accountSQL, error) {
	d, t, a, err := s.adminRead(ctx, id)
	if err != nil {
		return Database{}, dbquery.Target{}, nil, err
	}
	if s.Jobs(id) != "" {
		return Database{}, dbquery.Target{}, nil, ErrBusy
	}
	return d, t, a, nil
}

// logicalList reads the logical databases through the admin path.
func (s *Service) logicalList(ctx context.Context, d Database, t dbquery.Target, a accountSQL) ([]LogicalDatabase, error) {
	rows, err := s.d.Admin.Rows(ctx, t, d.Engine, "", a.DatabasesQuery())
	if err != nil {
		return nil, scrubErr(err, d.Password)
	}
	out := make([]LogicalDatabase, 0, len(rows))
	for _, r := range rows {
		if len(r) < 4 {
			continue
		}
		size, _ := strconv.ParseInt(r[1], 10, 64)
		l := LogicalDatabase{Name: r[0], SizeBytes: size}
		if d.Engine == "postgres" {
			l.Owner = r[2]
			l.AnyUserCanConnect = r[3] == "t"
			l.Protected = r[0] == "postgres"
		} else {
			l.Charset = r[2]
		}
		out = append(out, l)
	}
	return out, nil
}

// LogicalDatabases lists the databases inside Database id: size, owner or
// character set, and whether any user may connect.
func (s *Service) LogicalDatabases(ctx context.Context, id int64) ([]LogicalDatabase, error) {
	d, t, a, err := s.adminRead(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.logicalList(ctx, d, t, a)
}

// CreateLogicalDatabase creates database name. With withOwner it first makes
// a user of the same name with a generated password and makes it the owner
// (Full access). A failure after the user exists removes it again.
func (s *Service) CreateLogicalDatabase(ctx context.Context, id int64, name string, withOwner bool) error {
	d, t, a, err := s.adminTarget(ctx, id)
	if err != nil {
		return err
	}
	have, err := s.logicalList(ctx, d, t, a)
	if err != nil {
		return err
	}
	names := make([]string, len(have))
	for i, h := range have {
		names[i] = h.Name
	}
	exists, err := checkTarget(name, names)
	if err != nil {
		return err
	}
	if exists {
		return ErrDatabaseExists{Name: name}
	}
	if !withOwner {
		return scrubErr(s.d.Admin.Exec(ctx, t, d.Engine, "", a.CreateDatabase(name, "")), d.Password)
	}
	if err := checkUserName(d.Engine, name); err != nil {
		return err
	}
	users, err := s.d.Admin.Rows(ctx, t, d.Engine, "", a.UsersQuery())
	if err != nil {
		return scrubErr(err, d.Password)
	}
	for _, u := range users {
		if len(u) > 0 && u[0] == name {
			return ErrUserExists{Name: name}
		}
	}
	pw, err := newPassword()
	if err != nil {
		return err
	}
	if err := s.d.Admin.Exec(ctx, t, d.Engine, "", a.CreateUser(name, pw)); err != nil {
		return scrubErr(err, d.Password, pw)
	}
	undoUser := func() {
		cctx := context.WithoutCancel(ctx)
		_ = s.d.Admin.Exec(cctx, t, d.Engine, "", a.DropUser(name))
		_ = s.d.Store.DeleteUser(cctx, d.ID, name)
	}
	if err := s.d.Store.UpsertUser(ctx, d.ID, name, pw); err != nil {
		undoUser()
		return err
	}
	// The first statement creates the database; if a later one fails the
	// database is ours and goes with the user, or the user could not be dropped.
	stmts := a.CreateDatabase(name, name)
	if err := s.d.Admin.Exec(ctx, t, d.Engine, "", stmts[:1]); err != nil {
		undoUser()
		return scrubErr(err, d.Password, pw)
	}
	if err := s.d.Admin.Exec(ctx, t, d.Engine, "", stmts[1:]); err != nil {
		_ = s.d.Admin.Exec(context.WithoutCancel(ctx), t, d.Engine, "", a.DropDatabase(name))
		undoUser()
		return scrubErr(err, d.Password, pw)
	}
	return nil
}

// RestrictLogicalDatabase stops every user but its owner and the users given
// access from connecting. Postgres only.
func (s *Service) RestrictLogicalDatabase(ctx context.Context, id int64, name string) error {
	d, t, a, err := s.adminTarget(ctx, id)
	if err != nil {
		return err
	}
	if d.Engine != "postgres" {
		return ErrNoQuery
	}
	have, err := s.logicalList(ctx, d, t, a)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(have, func(l LogicalDatabase) bool { return l.Name == name }) {
		return ErrUnknownLogical{Names: []string{name}}
	}
	return scrubErr(s.d.Admin.Exec(ctx, t, d.Engine, "", a.Restrict(name)), d.Password)
}

// DropLogicalDatabase drops database name; confirm must equal name. With
// backupFirst it is a job like Update image: it checks the Database is
// running, answers at once, takes a pre_drop Backup in the background and
// drops only when that succeeded.
func (s *Service) DropLogicalDatabase(ctx context.Context, id int64, name, confirm string, backupFirst bool) error {
	if confirm != name {
		return ErrConfirm
	}
	d, t, a, err := s.adminTarget(ctx, id)
	if err != nil {
		return err
	}
	have, err := s.logicalList(ctx, d, t, a)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(have, func(l LogicalDatabase) bool { return l.Name == name })
	if i < 0 || have[i].Protected {
		return ErrProtectedDatabase{Name: name}
	}
	if !backupFirst {
		return s.dropNow(ctx, d, t, a, name)
	}
	d, dp, err := s.acquire(ctx, id, JobKindDropDatabase)
	if err != nil {
		return err
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.endJob(id)
		ctx := context.WithoutCancel(ctx)
		s.contain(ctx, id, "drop database", func() {
			if b := s.backupNow(ctx, d, dp, name, TriggerPreDrop, nil); b.Status != JobSucceeded {
				_ = s.d.Store.SetLastError(ctx, id, "the pre-drop backup failed, so "+name+" was not dropped: "+b.Error)
				return
			}
			// The slot is this job's own, so the busy check of adminTarget is not used.
			fresh, ft, fa, err := s.adminRead(ctx, id)
			if err == nil {
				err = s.dropNow(ctx, fresh, ft, fa, name)
			}
			if err != nil {
				_ = s.d.Store.SetLastError(ctx, id, err.Error())
			}
		})
	}()
	return nil
}

// dropNow drops the database. MySQL and MariaDB schema grants outlive the
// schema and would apply to a new database of the same name, so they go
// first. The name then leaves the Backup schedule's list.
func (s *Service) dropNow(ctx context.Context, d Database, t dbquery.Target, a accountSQL, name string) error {
	var stmts []string
	if d.Engine != "postgres" {
		rows, err := s.d.Admin.Rows(ctx, t, d.Engine, "", a.AccessQuery())
		if err != nil {
			return scrubErr(err, d.Password)
		}
		for _, r := range rows {
			if len(r) >= 2 && r[1] == name {
				stmts = append(stmts, a.ServerAccess(r[0], name, "", AccessRead, AccessNone)...)
			}
		}
	}
	stmts = append(stmts, a.DropDatabase(name)...)
	if err := s.d.Admin.Exec(ctx, t, d.Engine, "", stmts); err != nil {
		return scrubErr(err, d.Password)
	}
	return s.d.Store.RemoveScheduledDatabase(ctx, d.ID, name)
}
