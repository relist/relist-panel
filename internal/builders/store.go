// Package builders owns Builders: named, resource-limited buildx builders the
// panel builds Images on, the panel default, each Compose's choice and the
// BuildKit image setting (.scratch/build-resource-limits/spec.md).
package builders

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// DockerDefault is the built-in Builder: dockerd's own, with no limits.
const DockerDefault int64 = 0

// The modes of a Compose's Builder choice.
const (
	ModeDefault = "default" // the panel default
	ModeDocker  = "docker"  // Docker default
	ModeBuilder = "builder" // the Builder BuilderID
)

// ErrUnknownBuilder is a choice naming no Builder, or an unknown mode.
var ErrUnknownBuilder = errors.New("unknown builder")

type Builder struct {
	ID             int64
	Name           string
	CPUsMilli      int
	MemoryMB       int
	MaxParallelism int // 0 = BuildKit's default
	LastError      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Choice is a Compose's Builder setting. BuilderID is set only for ModeBuilder.
type Choice struct {
	Mode      string
	BuilderID int64
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

const builderColumns = `id, name, cpus_milli, memory_mb, max_parallelism, last_error, created_at, updated_at`

func scanBuilder(row interface{ Scan(...any) error }) (Builder, error) {
	var b Builder
	var created, updated int64
	if err := row.Scan(&b.ID, &b.Name, &b.CPUsMilli, &b.MemoryMB, &b.MaxParallelism, &b.LastError, &created, &updated); err != nil {
		return Builder{}, err
	}
	b.CreatedAt, b.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
	return b, nil
}

// List returns every Builder, by name.
func (s *Store) List(ctx context.Context) ([]Builder, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+builderColumns+` FROM builders ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Builder{}
	for rows.Next() {
		b, err := scanBuilder(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, b)
	}
	return list, rows.Err()
}

// Get returns sql.ErrNoRows for a missing Builder.
func (s *Store) Get(ctx context.Context, id int64) (Builder, error) {
	return scanBuilder(s.db.QueryRowContext(ctx, `SELECT `+builderColumns+` FROM builders WHERE id = ?`, id))
}

func (s *Store) Create(ctx context.Context, b Builder, now time.Time) (Builder, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO builders (name, cpus_milli, memory_mb, max_parallelism, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		b.Name, b.CPUsMilli, b.MemoryMB, b.MaxParallelism, now.Unix(), now.Unix())
	if err != nil {
		return Builder{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Builder{}, err
	}
	return s.Get(ctx, id)
}

// Update saves the name and limits; last_error is SetLastError's.
func (s *Store) Update(ctx context.Context, b Builder, now time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE builders SET name = ?, cpus_milli = ?, memory_mb = ?, max_parallelism = ?, updated_at = ? WHERE id = ?`,
		b.Name, b.CPUsMilli, b.MemoryMB, b.MaxParallelism, now.Unix(), b.ID)
	return err
}

// Delete fails while the Builder is the panel default or a Compose's choice.
func (s *Store) Delete(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM builders WHERE id = ?`, id)
	return err
}

func (s *Store) SetLastError(ctx context.Context, id int64, msg string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE builders SET last_error = ? WHERE id = ?`, msg, id)
	return err
}

// DefaultID is the panel default Builder, DockerDefault when none is set.
func (s *Store) DefaultID(ctx context.Context) (int64, error) {
	var id sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT default_builder_id FROM builder_settings WHERE id = 1`).Scan(&id)
	return id.Int64, err
}

func (s *Store) SetDefault(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE builder_settings SET default_builder_id = ? WHERE id = 1`, nullID(id))
	return err
}

// BuildKitImage is the BuildKit image setting, '' for the release's default.
func (s *Store) BuildKitImage(ctx context.Context) (string, error) {
	var ref string
	err := s.db.QueryRowContext(ctx, `SELECT buildkit_image FROM builder_settings WHERE id = 1`).Scan(&ref)
	return ref, err
}

func (s *Store) SetBuildKitImage(ctx context.Context, ref string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE builder_settings SET buildkit_image = ? WHERE id = 1`, ref)
	return err
}

func (s *Store) ComposeChoice(ctx context.Context, composeID int64) (Choice, error) {
	var id sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT builder_id FROM compose_builders WHERE compose_id = ?`, composeID).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Choice{Mode: ModeDefault}, nil
	case err != nil:
		return Choice{}, err
	case !id.Valid:
		return Choice{Mode: ModeDocker}, nil
	}
	return Choice{Mode: ModeBuilder, BuilderID: id.Int64}, nil
}

// SetComposeChoice returns ErrUnknownBuilder for a missing Builder or mode.
func (s *Store) SetComposeChoice(ctx context.Context, composeID int64, c Choice) error {
	switch c.Mode {
	case ModeDefault:
		_, err := s.db.ExecContext(ctx, `DELETE FROM compose_builders WHERE compose_id = ?`, composeID)
		return err
	case ModeDocker:
		c.BuilderID = DockerDefault
	case ModeBuilder:
		if _, err := s.Get(ctx, c.BuilderID); errors.Is(err, sql.ErrNoRows) {
			return ErrUnknownBuilder
		} else if err != nil {
			return err
		}
	default:
		return ErrUnknownBuilder
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO compose_builders (compose_id, builder_id) VALUES (?, ?)
		 ON CONFLICT (compose_id) DO UPDATE SET builder_id = excluded.builder_id`,
		composeID, nullID(c.BuilderID))
	return err
}

// Resolve is the Builder a build of the Compose runs on now.
func (s *Store) Resolve(ctx context.Context, composeID int64) (int64, error) {
	c, err := s.ComposeChoice(ctx, composeID)
	if err != nil {
		return 0, err
	}
	switch c.Mode {
	case ModeDocker:
		return DockerDefault, nil
	case ModeBuilder:
		return c.BuilderID, nil
	}
	return s.DefaultID(ctx)
}

// UsedBy names the Composes that chose the Builder explicitly, sorted.
func (s *Store) UsedBy(ctx context.Context, id int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT c.name FROM compose_builders cb JOIN composes c ON c.id = cb.compose_id WHERE cb.builder_id = ? ORDER BY c.name`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func nullID(id int64) sql.NullInt64 {
	return sql.NullInt64{Int64: id, Valid: id != DockerDefault}
}
