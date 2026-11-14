// Package authgroups keeps host-wide Basic auth groups (ADR-0036) and hands
// them to Traefik's file provider.
package authgroups

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// FieldError names the invalid field of a request, by its JSON name.
type FieldError struct{ Field, Message string }

func (e *FieldError) Error() string { return e.Field + ": " + e.Message }

// ErrNotFound means the group or user does not exist.
var ErrNotFound = errors.New("not found")

// ConflictError means a group name or a username in a group is taken.
type ConflictError struct{ Field, Message string }

func (e *ConflictError) Error() string { return e.Message }

// Group is one Basic auth group with its counts.
type Group struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	UserCount   int       `json:"userCount"`
	DomainCount int       `json:"domainCount"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// User is one username and password of a group. Password and Hash never
// leave the package through JSON.
type User struct {
	ID        int64     `json:"id"`
	GroupID   int64     `json:"-"`
	Username  string    `json:"username"`
	Password  string    `json:"-"`
	Hash      string    `json:"-"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// UsedBy is one Domain that uses a group.
type UsedBy struct {
	ID              int64  `json:"id"`
	Host            string `json:"host"`
	Path            string `json:"path"`
	ProjectID       int64  `json:"projectId"`
	EnvironmentID   int64  `json:"environmentId"`
	ComposeID       int64  `json:"composeId"`
	ProjectName     string `json:"projectName"`
	EnvironmentName string `json:"environmentName"`
	ComposeName     string `json:"composeName"`
}

// UserInput is a username and the password to set for it.
type UserInput struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Option is a group as the Domain form lists it.
type Option struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// CleanName trims name and checks it.
func CleanName(name string) (string, *FieldError) {
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n < 1 || n > 64 {
		return "", &FieldError{"name", "name must be 1-64 characters"}
	}
	return name, nil
}

// CheckUsername allows 1-64 printable ASCII characters except ':'.
func CheckUsername(u string) *FieldError {
	if len(u) < 1 || len(u) > 64 {
		return &FieldError{"username", "username must be 1-64 characters"}
	}
	for i := 0; i < len(u); i++ {
		if c := u[i]; c < 0x21 || c > 0x7e || c == ':' {
			return &FieldError{"username", "username must be printable ASCII without spaces or ':'"}
		}
	}
	return nil
}

// CheckPassword allows 8-72 bytes, bcrypt's limit.
func CheckPassword(p string) *FieldError {
	if len(p) < 8 || len(p) > 72 {
		return &FieldError{"password", "password must be 8-72 bytes"}
	}
	return nil
}

type Store struct {
	db  *sql.DB
	now func() time.Time
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db, now: time.Now}
}

type scanner interface{ Scan(dest ...any) error }

const selectGroups = `
	SELECT g.id, g.name, g.created_at, g.updated_at,
	       (SELECT COUNT(*) FROM auth_group_users u WHERE u.group_id = g.id),
	       (SELECT COUNT(*) FROM domains d WHERE d.auth_group_id = g.id)
	FROM auth_groups g`

func scanGroup(row scanner) (Group, error) {
	var g Group
	var created, updated int64
	if err := row.Scan(&g.ID, &g.Name, &created, &updated, &g.UserCount, &g.DomainCount); err != nil {
		return Group{}, err
	}
	g.CreatedAt = time.Unix(created, 0)
	g.UpdatedAt = time.Unix(updated, 0)
	return g, nil
}

// List returns every group ordered by name.
func (s *Store) List(ctx context.Context) ([]Group, error) {
	rows, err := s.db.QueryContext(ctx, selectGroups+` ORDER BY g.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := []Group{}
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

// Get returns the group or ErrNotFound.
func (s *Store) Get(ctx context.Context, id int64) (Group, error) {
	return getGroup(ctx, s.db, id)
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func getGroup(ctx context.Context, q queryer, id int64) (Group, error) {
	g, err := scanGroup(q.QueryRowContext(ctx, selectGroups+` WHERE g.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Group{}, ErrNotFound
	}
	return g, err
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}

func nameTaken() *ConflictError {
	return &ConflictError{"name", "A group with this name already exists"}
}

func usernameTaken() *ConflictError {
	return &ConflictError{"username", "This group already has a user with this name"}
}

func hashPassword(p string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
	return string(h), err
}

// Create stores a group and its first users in one transaction. name and
// the users must already be valid.
func (s *Store) Create(ctx context.Context, name string, users []UserInput) (Group, error) {
	hashes := make([]string, len(users))
	for i, u := range users {
		h, err := hashPassword(u.Password)
		if err != nil {
			return Group{}, err
		}
		hashes[i] = h
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Group{}, err
	}
	defer tx.Rollback()
	now := s.now().Unix()
	res, err := tx.ExecContext(ctx, `INSERT INTO auth_groups (name, created_at, updated_at) VALUES (?, ?, ?)`, name, now, now)
	if err != nil {
		if isUnique(err) {
			return Group{}, nameTaken()
		}
		return Group{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Group{}, err
	}
	for i, u := range users {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO auth_group_users (group_id, username, password, hash, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)`, id, u.Username, u.Password, hashes[i], now, now); err != nil {
			if isUnique(err) {
				return Group{}, usernameTaken()
			}
			return Group{}, err
		}
	}
	g, err := getGroup(ctx, tx, id)
	if err != nil {
		return Group{}, err
	}
	return g, tx.Commit()
}

// Rename changes the name of id.
func (s *Store) Rename(ctx context.Context, id int64, name string) (Group, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE auth_groups SET name = ?, updated_at = ? WHERE id = ?`, name, s.now().Unix(), id)
	if err != nil {
		if isUnique(err) {
			return Group{}, nameTaken()
		}
		return Group{}, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return Group{}, err
	} else if n == 0 {
		return Group{}, ErrNotFound
	}
	return s.Get(ctx, id)
}

// Delete removes the group and its users. While a Domain uses it nothing is
// deleted and those Domains come back instead.
func (s *Store) Delete(ctx context.Context, id int64) ([]UsedBy, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := getGroup(ctx, tx, id); err != nil {
		return nil, err
	}
	used, err := usedBy(ctx, tx, id)
	if err != nil || len(used) > 0 {
		return used, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM auth_groups WHERE id = ?`, id); err != nil {
		return nil, err
	}
	return nil, tx.Commit()
}

const userColumns = `id, group_id, username, password, hash, created_at, updated_at`

func scanUser(row scanner) (User, error) {
	var u User
	var created, updated int64
	if err := row.Scan(&u.ID, &u.GroupID, &u.Username, &u.Password, &u.Hash, &created, &updated); err != nil {
		return User{}, err
	}
	u.CreatedAt = time.Unix(created, 0)
	u.UpdatedAt = time.Unix(updated, 0)
	return u, nil
}

// Users returns the users of a group ordered by username, with Password and
// Hash filled.
func (s *Store) Users(ctx context.Context, groupID int64) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userColumns+` FROM auth_group_users WHERE group_id = ? ORDER BY username`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *Store) getUser(ctx context.Context, groupID, userID int64) (User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM auth_group_users WHERE id = ? AND group_id = ?`, userID, groupID))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

// GetUser returns one user of a group or ErrNotFound.
func (s *Store) GetUser(ctx context.Context, groupID, userID int64) (User, error) {
	return s.getUser(ctx, groupID, userID)
}

// touch bumps the group's updated_at.
func touch(ctx context.Context, tx *sql.Tx, groupID, now int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE auth_groups SET updated_at = ? WHERE id = ?`, now, groupID)
	return err
}

// AddUser adds a user to the group, whose existence is checked here.
func (s *Store) AddUser(ctx context.Context, groupID int64, in UserInput) (User, error) {
	hash, err := hashPassword(in.Password)
	if err != nil {
		return User{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	if _, err := getGroup(ctx, tx, groupID); err != nil {
		return User{}, err
	}
	now := s.now().Unix()
	res, err := tx.ExecContext(ctx, `
		INSERT INTO auth_group_users (group_id, username, password, hash, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`, groupID, in.Username, in.Password, hash, now, now)
	if err != nil {
		if isUnique(err) {
			return User{}, usernameTaken()
		}
		return User{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return User{}, err
	}
	if err := touch(ctx, tx, groupID, now); err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return s.getUser(ctx, groupID, id)
}

// UpdateUser changes the username and/or password of a user. A nil password
// keeps the stored one and its hash.
func (s *Store) UpdateUser(ctx context.Context, groupID, userID int64, username *string, password *string) (User, error) {
	var hash string
	if password != nil {
		h, err := hashPassword(*password)
		if err != nil {
			return User{}, err
		}
		hash = h
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	now := s.now().Unix()
	res, err := tx.ExecContext(ctx, `
		UPDATE auth_group_users
		SET username = COALESCE(?, username),
		    password = COALESCE(?, password),
		    hash = COALESCE(?, hash),
		    updated_at = ?
		WHERE id = ? AND group_id = ?`,
		username, password, nullable(password, hash), now, userID, groupID)
	if err != nil {
		if isUnique(err) {
			return User{}, usernameTaken()
		}
		return User{}, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return User{}, err
	} else if n == 0 {
		return User{}, ErrNotFound
	}
	if err := touch(ctx, tx, groupID, now); err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return s.getUser(ctx, groupID, userID)
}

func nullable(set *string, hash string) any {
	if set == nil {
		return nil
	}
	return hash
}

// DeleteUser removes one user of a group.
func (s *Store) DeleteUser(ctx context.Context, groupID, userID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `DELETE FROM auth_group_users WHERE id = ? AND group_id = ?`, userID, groupID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound
	}
	if err := touch(ctx, tx, groupID, s.now().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

type rowQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func usedBy(ctx context.Context, q rowQueryer, groupID int64) ([]UsedBy, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT d.id, d.host, d.path, p.id, e.id, c.id, p.name, e.name, c.name
		FROM domains d
		JOIN composes c ON c.id = d.compose_id
		JOIN environments e ON e.id = c.environment_id
		JOIN projects p ON p.id = e.project_id
		WHERE d.auth_group_id = ?
		ORDER BY d.host, d.path`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	used := []UsedBy{}
	for rows.Next() {
		var u UsedBy
		if err := rows.Scan(&u.ID, &u.Host, &u.Path, &u.ProjectID, &u.EnvironmentID, &u.ComposeID, &u.ProjectName, &u.EnvironmentName, &u.ComposeName); err != nil {
			return nil, err
		}
		used = append(used, u)
	}
	return used, rows.Err()
}

// UsedBy lists the Domains that use the group, by host and path.
func (s *Store) UsedBy(ctx context.Context, groupID int64) ([]UsedBy, error) {
	return usedBy(ctx, s.db, groupID)
}

// Exists reports whether a group with id exists.
func (s *Store) Exists(ctx context.Context, id int64) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM auth_groups WHERE id = ?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// Options lists every group by name for the Domain form.
func (s *Store) Options(ctx context.Context) ([]Option, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name FROM auth_groups ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Option{}
	for rows.Next() {
		var o Option
		if err := rows.Scan(&o.ID, &o.Name); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
