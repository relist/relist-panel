package backupdest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Store keeps Backup destinations in the panel database.
type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

const columns = `id, name, type, config, secret, last_test_at, last_test_ok, last_test_error`

func scan(row interface{ Scan(...any) error }) (Destination, error) {
	var d Destination
	var cfg string
	var at sql.NullInt64
	err := row.Scan(&d.ID, &d.Name, &d.Type, &cfg, &d.Secret, &at, &d.LastTestOK, &d.LastTestError)
	if errors.Is(err, sql.ErrNoRows) {
		return Destination{}, ErrNotFound
	}
	if err != nil {
		return Destination{}, err
	}
	if err := json.Unmarshal([]byte(cfg), &d.S3); err != nil {
		return Destination{}, fmt.Errorf("backup destination %d: %w", d.ID, err)
	}
	if at.Valid {
		t := time.Unix(at.Int64, 0)
		d.LastTestAt = &t
	}
	return d, nil
}

// List is every destination, ordered by name.
func (s *Store) List(ctx context.Context) ([]Destination, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+columns+` FROM backup_destinations ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Destination{}
	for rows.Next() {
		d, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) Get(ctx context.Context, id int64) (Destination, error) {
	return scan(s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM backup_destinations WHERE id = ?`, id))
}

func duplicate(err error) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrDuplicateName
	}
	return err
}

func (s *Store) Create(ctx context.Context, d Destination, now time.Time) (Destination, error) {
	cfg, _ := json.Marshal(d.S3)
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO backup_destinations (name, type, config, secret, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		d.Name, d.Type, string(cfg), d.Secret, now.Unix(), now.Unix())
	if err != nil {
		return Destination{}, duplicate(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Destination{}, err
	}
	return s.Get(ctx, id)
}

// Update changes name and settings and, when d.Secret is not empty, the
// secret. A changed destination forgets its last Test result.
func (s *Store) Update(ctx context.Context, d Destination, now time.Time) error {
	old, err := s.Get(ctx, d.ID)
	if err != nil {
		return err
	}
	cfg, _ := json.Marshal(d.S3)
	changed := old.S3 != d.S3 || d.Secret != ""
	secret := old.Secret
	if d.Secret != "" {
		secret = d.Secret
	}
	testAt, testOK, testErr := any(nil), false, ""
	if !changed {
		if old.LastTestAt != nil {
			testAt = old.LastTestAt.Unix()
		}
		testOK, testErr = old.LastTestOK, old.LastTestError
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE backup_destinations SET name = ?, config = ?, secret = ?, last_test_at = ?, last_test_ok = ?, last_test_error = ?, updated_at = ?
		WHERE id = ?`, d.Name, string(cfg), secret, testAt, testOK, testErr, now.Unix(), d.ID)
	return duplicate(err)
}

func (s *Store) SetTestResult(ctx context.Context, id int64, ok bool, msg string, at time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE backup_destinations SET last_test_at = ?, last_test_ok = ?, last_test_error = ? WHERE id = ?`, at.Unix(), ok, msg, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func usage(ctx context.Context, q queryer, id int64) (Usage, error) {
	u := Usage{Schedules: []string{}}
	rows, err := q.QueryContext(ctx, `
		SELECT d.name FROM database_backup_schedules s JOIN databases d ON d.id = s.database_id
		WHERE s.destination_id = ? ORDER BY d.name`, id)
	if err != nil {
		return u, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return u, err
		}
		u.Schedules = append(u.Schedules, name)
	}
	if err := rows.Err(); err != nil {
		return u, err
	}
	err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM database_backup_uploads WHERE destination_id = ? AND status = 'uploaded' AND deleted_at IS NULL`, id).Scan(&u.Copies)
	return u, err
}

// Usage reports what still uses destination id.
func (s *Store) Usage(ctx context.Context, id int64) (Usage, error) {
	return usage(ctx, s.db, id)
}

// Delete removes destination id and, with it, its upload rows. A schedule that
// copies to it refuses (ErrInUse). Copies still in the bucket need confirm to
// equal their number (ErrCopies). A Backup left with no file and no copy goes
// too, and a deleted Database left with no Backups goes with them; the IDs of
// those Databases are returned so their files can be removed.
func (s *Store) Delete(ctx context.Context, id int64, confirm int) (removedDatabases []int64, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := scan(tx.QueryRowContext(ctx, `SELECT `+columns+` FROM backup_destinations WHERE id = ?`, id)); err != nil {
		return nil, err
	}
	u, err := usage(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if len(u.Schedules) > 0 {
		return nil, ErrInUse{u.Schedules}
	}
	if u.Copies > 0 && confirm != u.Copies {
		return nil, ErrCopies{u.Copies}
	}
	// The Backups this destination held a copy of, before the copies go.
	rows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT b.id, b.database_id FROM database_backup_uploads u JOIN database_backups b ON b.id = u.backup_id
		WHERE u.destination_id = ? AND b.local_removed_at IS NOT NULL`, id)
	if err != nil {
		return nil, err
	}
	type held struct{ backup, database int64 }
	var maybe []held
	for rows.Next() {
		var h held
		if err := rows.Scan(&h.backup, &h.database); err != nil {
			rows.Close()
			return nil, err
		}
		maybe = append(maybe, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM backup_destinations WHERE id = ?`, id); err != nil {
		return nil, err
	}
	seen := map[int64]bool{}
	for _, h := range maybe {
		// Remote only, and no other live copy: nothing of it is left.
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM database_backups WHERE id = ? AND local_removed_at IS NOT NULL
			AND NOT EXISTS (SELECT 1 FROM database_backup_uploads WHERE backup_id = database_backups.id AND status = 'uploaded' AND deleted_at IS NULL)`, h.backup); err != nil {
			return nil, err
		}
		if seen[h.database] {
			continue
		}
		seen[h.database] = true
		res, err := tx.ExecContext(ctx, `
			DELETE FROM databases WHERE id = ? AND deleted_at IS NOT NULL
			AND NOT EXISTS (SELECT 1 FROM database_backups WHERE database_id = databases.id)`, h.database)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			removedDatabases = append(removedDatabases, h.database)
		}
	}
	return removedDatabases, tx.Commit()
}
