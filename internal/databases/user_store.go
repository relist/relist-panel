package databases

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ErrUserNotManaged is a user the panel did not create or reset, so it has no
// password to show.
var ErrUserNotManaged = errors.New("this user's password is unknown: use Reset password")

// ManagedUser is a login user of a Database whose password the panel stores,
// because it created or reset the user.
type ManagedUser struct {
	DatabaseID     int64
	Name, Password string
	CreatedAt      time.Time
}

// UpsertUser stores the password of user name, replacing a stored one.
func (s *Store) UpsertUser(ctx context.Context, databaseID int64, name, password string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO database_users (database_id, name, password, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(database_id, name) DO UPDATE SET password = excluded.password`,
		databaseID, name, password, s.now().UnixMilli())
	return err
}

// GetUser returns the managed user, ErrUserNotManaged when there is none.
func (s *Store) GetUser(ctx context.Context, databaseID int64, name string) (ManagedUser, error) {
	var u ManagedUser
	var created int64
	err := s.db.QueryRowContext(ctx, `SELECT database_id, name, password, created_at FROM database_users WHERE database_id = ? AND name = ?`,
		databaseID, name).Scan(&u.DatabaseID, &u.Name, &u.Password, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return ManagedUser{}, ErrUserNotManaged
	}
	if err != nil {
		return ManagedUser{}, err
	}
	u.CreatedAt = time.UnixMilli(created)
	return u, nil
}

// ListUsers returns a Database's managed users by name.
func (s *Store) ListUsers(ctx context.Context, databaseID int64) ([]ManagedUser, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT database_id, name, password, created_at FROM database_users WHERE database_id = ? ORDER BY name`, databaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ManagedUser{}
	for rows.Next() {
		var u ManagedUser
		var created int64
		if err := rows.Scan(&u.DatabaseID, &u.Name, &u.Password, &created); err != nil {
			return nil, err
		}
		u.CreatedAt = time.UnixMilli(created)
		out = append(out, u)
	}
	return out, rows.Err()
}

// DeleteUser forgets a managed user; a missing row is fine.
func (s *Store) DeleteUser(ctx context.Context, databaseID int64, name string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM database_users WHERE database_id = ? AND name = ?`, databaseID, name)
	return err
}

// SetPendingPassword records a superuser password that is about to be set live.
func (s *Store) SetPendingPassword(ctx context.Context, id int64, pw string) error {
	return s.update(ctx, id, `pending_password = ?`, pw)
}

// PromotePending makes the pending password current and clears it. With
// nothing pending it changes nothing.
func (s *Store) PromotePending(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE databases SET password = pending_password, pending_password = '', updated_at = ? WHERE id = ? AND pending_password <> ''`,
		s.now().UnixMilli(), id)
	return err
}

// ClearPending drops the pending password.
func (s *Store) ClearPending(ctx context.Context, id int64) error {
	return s.update(ctx, id, `pending_password = ''`)
}
