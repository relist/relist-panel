// Package databases owns the panel's Databases: the Postgres, MySQL, MariaDB
// and Redis containers it runs, their records, and reconcile (ADR-0039).
package databases

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// What the Admin wants a Database to be doing.
const (
	DesiredRunning = "running"
	DesiredStopped = "stopped"
)

var (
	ErrNotFound      = errors.New("database not found")
	ErrDuplicateName = errors.New("a Database with this name already exists")
)

// Database is one panel-managed server. Its container and volume are named
// from ID, never from Name.
type Database struct {
	ID         int64
	Name, Slug string
	Engine     string // "postgres" | "mysql" | "mariadb" | "redis"
	Version    string // a tag from the engine's Versions
	Password   string
	MemoryMB   int64   // 0 = no limit
	CPUs       float64 // 0 = no limit
	Desired    string  // DesiredRunning | DesiredStopped
	// Initialized: the container has started once on this volume.
	Initialized bool
	LastError   string
	// AppliedSpec is the JSON ContainerSpec the container was last created
	// with; "" until the first reconcile fills it.
	AppliedSpec          string
	CreatedAt, UpdatedAt time.Time
}

type Store struct {
	db  *sql.DB
	now func() time.Time
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db, now: time.Now}
}

const columns = `id, name, slug, engine, version, password, memory_mb, cpus, desired, initialized, last_error, applied_spec, created_at, updated_at`

func scan(row interface{ Scan(...any) error }) (Database, error) {
	var d Database
	var created, updated int64
	err := row.Scan(&d.ID, &d.Name, &d.Slug, &d.Engine, &d.Version, &d.Password, &d.MemoryMB, &d.CPUs,
		&d.Desired, &d.Initialized, &d.LastError, &d.AppliedSpec, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Database{}, ErrNotFound
	}
	if err != nil {
		return Database{}, err
	}
	d.CreatedAt = time.UnixMilli(created)
	d.UpdatedAt = time.UnixMilli(updated)
	return d, nil
}

func isDuplicate(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// Insert stores d (ID and timestamps are set here) and returns the stored row.
func (s *Store) Insert(ctx context.Context, d Database) (Database, error) {
	if d.Desired == "" {
		d.Desired = DesiredRunning
	}
	now := s.now().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO databases (name, slug, engine, version, password, memory_mb, cpus, desired, initialized, last_error, applied_spec, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.Name, d.Slug, d.Engine, d.Version, d.Password, d.MemoryMB, d.CPUs, d.Desired, d.Initialized, d.LastError, d.AppliedSpec, now, now)
	if err != nil {
		if isDuplicate(err) {
			return Database{}, ErrDuplicateName
		}
		return Database{}, fmt.Errorf("database: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Database{}, err
	}
	return s.Get(ctx, id)
}

func (s *Store) Get(ctx context.Context, id int64) (Database, error) {
	return scan(s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM databases WHERE id = ?`, id))
}

// List returns every Database ordered by name.
func (s *Store) List(ctx context.Context) ([]Database, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+columns+` FROM databases ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Database{}
	for rows.Next() {
		d, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) update(ctx context.Context, id int64, set string, args ...any) error {
	args = append(append(args, s.now().UnixMilli()), id)
	res, err := s.db.ExecContext(ctx, `UPDATE databases SET `+set+`, updated_at = ? WHERE id = ?`, args...)
	if err != nil {
		if isDuplicate(err) {
			return ErrDuplicateName
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) Rename(ctx context.Context, id int64, name, slug string) error {
	return s.update(ctx, id, `name = ?, slug = ?`, name, slug)
}

func (s *Store) SetDesired(ctx context.Context, id int64, desired string) error {
	return s.update(ctx, id, `desired = ?`, desired)
}

func (s *Store) SetInitialized(ctx context.Context, id int64) error {
	return s.update(ctx, id, `initialized = 1`)
}

func (s *Store) SetLastError(ctx context.Context, id int64, msg string) error {
	return s.update(ctx, id, `last_error = ?`, msg)
}

func (s *Store) SetAppliedSpec(ctx context.Context, id int64, spec string) error {
	return s.update(ctx, id, `applied_spec = ?`, spec)
}

func (s *Store) Delete(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM databases WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
