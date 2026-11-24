package chunkupload

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const columns = `id, user_id, COALESCE(compose_id, 0), purpose, target, name, size, last_modified,
	received, state, error_kind, error, created_at, updated_at`

type scanner interface {
	Scan(dest ...any) error
}

func scanSession(row scanner) (Session, error) {
	var s Session
	var created, updated int64
	err := row.Scan(&s.ID, &s.UserID, &s.ComposeID, &s.Purpose, &s.Target, &s.Name, &s.Size,
		&s.LastModified, &s.Received, &s.State, &s.ErrorKind, &s.Error, &created, &updated)
	if err != nil {
		return Session{}, err
	}
	s.CreatedAt, s.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
	return s, nil
}

func nullableCompose(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

func insert(ctx context.Context, db *sql.DB, s Session) error {
	_, err := db.ExecContext(ctx, `INSERT INTO uploads (id, user_id, purpose, compose_id, target, name, size,
		last_modified, received, state, error_kind, error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, ?, '', '', ?, ?)`,
		s.ID, s.UserID, s.Purpose, nullableCompose(s.ComposeID), s.Target, s.Name, s.Size,
		s.LastModified, s.State, s.CreatedAt.Unix(), s.UpdatedAt.Unix())
	if err != nil {
		return fmt.Errorf("insert upload: %w", err)
	}
	return nil
}

func get(ctx context.Context, db *sql.DB, userID, composeID int64, id string) (Session, error) {
	s, err := scanSession(db.QueryRowContext(ctx, `SELECT `+columns+` FROM uploads
		WHERE id = ? AND user_id = ? AND COALESCE(compose_id, 0) = ?`, id, userID, composeID))
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("load upload: %w", err)
	}
	return s, nil
}

// getByID loads a row with no owner check, for the job and the sweep.
func getByID(ctx context.Context, db *sql.DB, id string) (Session, error) {
	s, err := scanSession(db.QueryRowContext(ctx, `SELECT `+columns+` FROM uploads WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("load upload: %w", err)
	}
	return s, nil
}

func listOpen(ctx context.Context, db *sql.DB, userID, composeID int64, purpose string) ([]Session, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+columns+` FROM uploads
		WHERE user_id = ? AND COALESCE(compose_id, 0) = ? AND purpose = ? AND state != 'done'
		ORDER BY updated_at DESC, id`, userID, composeID, purpose)
	if err != nil {
		return nil, fmt.Errorf("list uploads: %w", err)
	}
	defer rows.Close()
	return scanAll(rows)
}

func scanAll(rows *sql.Rows) ([]Session, error) {
	out := []Session{}
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, fmt.Errorf("scan upload: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func countOpen(ctx context.Context, db *sql.DB, userID int64) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM uploads
		WHERE user_id = ? AND state IN ('uploading', 'failed')`, userID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count open uploads: %w", err)
	}
	return n, nil
}

// promisedBytes is what open uploads may still write: their declared sizes
// less what is already on disk (which statfs already counts as used).
func promisedBytes(ctx context.Context, db *sql.DB) (int64, error) {
	var n int64
	err := db.QueryRowContext(ctx, `SELECT COALESCE(SUM(size - received), 0) FROM uploads
		WHERE state = 'uploading'`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("sum promised upload bytes: %w", err)
	}
	return n, nil
}

func deleteRow(ctx context.Context, db *sql.DB, id string) error {
	if _, err := db.ExecContext(ctx, `DELETE FROM uploads WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete upload: %w", err)
	}
	return nil
}
