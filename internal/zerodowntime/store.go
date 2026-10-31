// Package zerodowntime holds which Services deploy by Rollout (ADR-0023) and
// checks, from `docker compose config`, whether they can.
package zerodowntime

import (
	"context"
	"database/sql"
	"time"
)

// Store reads and writes the zero-downtime switch of each Service.
type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Enabled returns the Compose's Services with the switch on, sorted by name.
// A name may no longer be in the YAML or have a Domain; callers ignore those.
func (s *Store) Enabled(ctx context.Context, composeID int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT service FROM zero_downtime_services WHERE compose_id = ? ORDER BY service`, composeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	services := []string{}
	for rows.Next() {
		var service string
		if err := rows.Scan(&service); err != nil {
			return nil, err
		}
		services = append(services, service)
	}
	return services, rows.Err()
}

// Set turns a Service's switch on or off. Turning on twice is not an error.
func (s *Store) Set(ctx context.Context, composeID int64, service string, on bool, now time.Time) error {
	if on {
		_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO zero_downtime_services (compose_id, service, created_at) VALUES (?, ?, ?)`, composeID, service, now.Unix())
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM zero_downtime_services WHERE compose_id = ? AND service = ?`, composeID, service)
	return err
}
