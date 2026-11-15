// Package directaccess owns Direct access: a timed way into one container's
// port that skips Traefik, through a small proxy the panel runs (ADR-0041).
// A Direct access is a target, a channel and an expiry; the table is the
// panel's truth.
package directaccess

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	KindDatabase    = "database"
	ChannelHostPort = "host_port"
)

var ErrNotFound = errors.New("this Direct access has ended")

// Target is what a Direct access reaches.
type Target struct {
	Kind string
	ID   int64
	Port int
}

// HostPort is the channel_config of the host_port channel.
type HostPort struct {
	Bind     string `json:"bind"`
	HostPort int    `json:"hostPort"`
}

type Access struct {
	ID      int64
	Target  Target
	Channel string
	// HostPort is channel_config when Channel is ChannelHostPort.
	HostPort  HostPort
	ExpiresAt *time.Time
	OpenedBy  int64
	OpenedAt  time.Time
}

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

const columns = `id, target_kind, target_id, target_port, channel, channel_config, expires_at, opened_by, opened_at`

func scan(row interface{ Scan(...any) error }) (Access, error) {
	var (
		a       Access
		cfg     string
		exp     sql.NullInt64
		by      sql.NullInt64
		openedA int64
	)
	if err := row.Scan(&a.ID, &a.Target.Kind, &a.Target.ID, &a.Target.Port, &a.Channel, &cfg, &exp, &by, &openedA); err != nil {
		return Access{}, err
	}
	if a.Channel == ChannelHostPort {
		if err := json.Unmarshal([]byte(cfg), &a.HostPort); err != nil {
			return Access{}, fmt.Errorf("direct access %d: bad channel_config: %w", a.ID, err)
		}
	}
	if exp.Valid {
		t := time.Unix(exp.Int64, 0)
		a.ExpiresAt = &t
	}
	a.OpenedBy = by.Int64
	a.OpenedAt = time.Unix(openedA, 0)
	return a, nil
}

func (s *Store) Insert(ctx context.Context, a Access) (int64, error) {
	cfg, err := json.Marshal(a.HostPort)
	if err != nil {
		return 0, err
	}
	var exp, by any
	if a.ExpiresAt != nil {
		exp = a.ExpiresAt.Unix()
	}
	if a.OpenedBy != 0 {
		by = a.OpenedBy
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO direct_accesses
		(target_kind, target_id, target_port, channel, channel_config, expires_at, opened_by, opened_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		a.Target.Kind, a.Target.ID, a.Target.Port, a.Channel, string(cfg), exp, by, a.OpenedAt.Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// SetHostPort records the port Docker published, keeping the bind.
func (s *Store) SetHostPort(ctx context.Context, id int64, port int) error {
	a, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	a.HostPort.HostPort = port
	cfg, err := json.Marshal(a.HostPort)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE direct_accesses SET channel_config = ? WHERE id = ?`, string(cfg), id)
	return err
}

func (s *Store) SetExpiry(ctx context.Context, id int64, t time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE direct_accesses SET expires_at = ? WHERE id = ?`, t.Unix(), id)
	return err
}

func (s *Store) Get(ctx context.Context, id int64) (Access, error) {
	a, err := scan(s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM direct_accesses WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Access{}, ErrNotFound
	}
	return a, err
}

func (s *Store) query(ctx context.Context, where string, args ...any) ([]Access, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+columns+` FROM direct_accesses `+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Access
	for rows.Next() {
		a, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) ForTarget(ctx context.Context, kind string, id int64) ([]Access, error) {
	return s.query(ctx, `WHERE target_kind = ? AND target_id = ?`, kind, id)
}

func (s *Store) List(ctx context.Context) ([]Access, error) { return s.query(ctx, ``) }

// Delete removes a row; a missing one is not an error.
func (s *Store) Delete(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM direct_accesses WHERE id = ?`, id)
	return err
}
