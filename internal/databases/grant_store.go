package databases

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ErrUnknownPanelUser is a user id with no row in users.
var ErrUnknownPanelUser = errors.New("no such panel user")

// RedisLevel is what a Redis grant allows: "read" or "read_write".
type RedisLevel string

const (
	LevelRead      RedisLevel = "read"
	LevelReadWrite RedisLevel = "read_write"
)

// Valid reports whether l is a level a grant can hold.
func (l RedisLevel) Valid() bool { return l == LevelRead || l == LevelReadWrite }

// Grant gives one panel user access to one Database: as managed user DBUser
// (SQL engines), or at Level (Redis). Exactly one of the two is set. Email is
// the panel user's, read through a join.
type Grant struct {
	DatabaseID, UserID int64
	Email              string
	DBUser             string
	Level              RedisLevel
	CreatedAt          time.Time
}

// nullable turns "" into NULL.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// SetGrant stores g, replacing the user's existing grant on that Database
// and keeping its created_at.
func (s *Store) SetGrant(ctx context.Context, g Grant) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO database_grants (database_id, user_id, db_user, level, created_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(database_id, user_id) DO UPDATE SET db_user = excluded.db_user, level = excluded.level`,
		g.DatabaseID, g.UserID, nullable(g.DBUser), nullable(string(g.Level)), s.now().UnixMilli())
	return err
}

const grantColumns = `SELECT g.database_id, g.user_id, u.email, COALESCE(g.db_user, ''), COALESCE(g.level, ''), g.created_at
	FROM database_grants g JOIN users u ON u.id = g.user_id`

func (s *Store) grantRows(ctx context.Context, where string, args ...any) ([]Grant, error) {
	rows, err := s.db.QueryContext(ctx, grantColumns+" "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Grant{}
	for rows.Next() {
		var g Grant
		var created int64
		if err := rows.Scan(&g.DatabaseID, &g.UserID, &g.Email, &g.DBUser, &g.Level, &created); err != nil {
			return nil, err
		}
		g.CreatedAt = time.UnixMilli(created)
		out = append(out, g)
	}
	return out, rows.Err()
}

// Grants lists a Database's grants by email.
func (s *Store) Grants(ctx context.Context, databaseID int64) ([]Grant, error) {
	return s.grantRows(ctx, "WHERE g.database_id = ? ORDER BY u.email", databaseID)
}

// GrantsForUser lists a panel user's grants, skipping tombstoned Databases.
func (s *Store) GrantsForUser(ctx context.Context, userID int64) ([]Grant, error) {
	return s.grantRows(ctx, `JOIN databases d ON d.id = g.database_id
		WHERE g.user_id = ? AND d.deleted_at IS NULL ORDER BY g.database_id`, userID)
}

// GrantsForDBUser lists the grants that use managed user dbUser.
func (s *Store) GrantsForDBUser(ctx context.Context, databaseID int64, dbUser string) ([]Grant, error) {
	return s.grantRows(ctx, "WHERE g.database_id = ? AND g.db_user = ? ORDER BY u.email", databaseID, dbUser)
}

// DeleteGrant removes one grant; ErrNotFound without one.
func (s *Store) DeleteGrant(ctx context.Context, databaseID, userID int64) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM database_grants WHERE database_id = ? AND user_id = ?", databaseID, userID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}

// PanelUser reads a panel user's email and whether they are an Admin.
func (s *Store) PanelUser(ctx context.Context, userID int64) (email string, isAdmin bool, err error) {
	err = s.db.QueryRowContext(ctx, "SELECT email, is_admin FROM users WHERE id = ?", userID).Scan(&email, &isAdmin)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, ErrUnknownPanelUser
	}
	return email, isAdmin, err
}
