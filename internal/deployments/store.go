// Package deployments stores the host-wide Deployment queue and history.
package deployments

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type Mode string

const (
	ModeDeploy    Mode = "deploy"
	ModeBuild     Mode = "build"
	ModeSkipBuild Mode = "skip-build"
)

type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCanceled  Status = "canceled"
)

type Deployment struct {
	ID, ComposeID            int64
	Number                   int
	Mode                     Mode
	NoCache, FreshVolumes    bool
	Status                   Status
	Step                     string
	Steps                    []string
	FailedStep, Reason       string
	SourceRef, SourceSubject string
	Warnings                 []string
	QueuedAt                 time.Time
	StartedAt, FinishedAt    *time.Time
}

// Active is a queued or running Deployment with its Project, Environment and Compose.
type Active struct {
	ComposeID       int64
	ComposeName     string
	Number          int
	Status          Status
	Step            string
	ProjectID       int64
	ProjectName     string
	EnvironmentID   int64
	EnvironmentName string
}

// ErrNotQueued is returned by Start when the Deployment was canceled or already
// started between NextQueued and Start.
var ErrNotQueued = errors.New("deployment is no longer queued")

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

const columns = `id, compose_id, number, mode, no_cache, fresh_volumes, status, step, steps,
	failed_step, reason, source_ref, source_subject, warnings, queued_at, started_at, finished_at`

type scanner interface{ Scan(dest ...any) error }

func scan(row scanner, extra ...any) (Deployment, error) {
	var (
		d                 Deployment
		mode, status      string
		noCache, fresh    int
		steps, warnings   string
		queued            int64
		started, finished sql.NullInt64
	)
	dest := []any{&d.ID, &d.ComposeID, &d.Number, &mode, &noCache, &fresh, &status, &d.Step, &steps,
		&d.FailedStep, &d.Reason, &d.SourceRef, &d.SourceSubject, &warnings, &queued, &started, &finished}
	if err := row.Scan(append(dest, extra...)...); err != nil {
		return Deployment{}, err
	}
	d.Mode, d.Status = Mode(mode), Status(status)
	d.NoCache, d.FreshVolumes = noCache != 0, fresh != 0
	if err := json.Unmarshal([]byte(steps), &d.Steps); err != nil {
		return Deployment{}, err
	}
	if err := json.Unmarshal([]byte(warnings), &d.Warnings); err != nil {
		return Deployment{}, err
	}
	d.QueuedAt = time.Unix(queued, 0)
	if started.Valid {
		t := time.Unix(started.Int64, 0)
		d.StartedAt = &t
	}
	if finished.Valid {
		t := time.Unix(finished.Int64, 0)
		d.FinishedAt = &t
	}
	return d, nil
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func queuedFor(ctx context.Context, q queryer, composeID int64) (Deployment, error) {
	return scan(q.QueryRowContext(ctx,
		`SELECT `+columns+` FROM deployments WHERE compose_id = ? AND status = 'queued'`, composeID))
}

// Enqueue queues a Deployment for the Compose. When one is already queued it is
// returned with created == false and its options are left as they were.
func (s *Store) Enqueue(ctx context.Context, composeID int64, mode Mode, noCache, fresh bool, now time.Time) (Deployment, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Deployment{}, false, err
	}
	defer tx.Rollback()

	existing, err := queuedFor(ctx, tx, composeID)
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Deployment{}, false, err
	}

	var number int
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(number), 0) + 1 FROM deployments WHERE compose_id = ?`, composeID).Scan(&number); err != nil {
		return Deployment{}, false, err
	}
	res, err := tx.ExecContext(ctx, `
INSERT INTO deployments (compose_id, number, mode, no_cache, fresh_volumes, status, queued_at)
VALUES (?, ?, ?, ?, ?, 'queued', ?)`,
		composeID, number, string(mode), boolInt(noCache), boolInt(fresh), now.Unix())
	if err != nil {
		if isUniqueConstraint(err) {
			// Defensive: the single connection serialises transactions, so this
			// should not happen. If it does, hand back the winner.
			tx.Rollback()
			winner, rerr := queuedFor(ctx, s.db, composeID)
			if rerr != nil {
				return Deployment{}, false, rerr
			}
			return winner, false, nil
		}
		return Deployment{}, false, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Deployment{}, false, err
	}
	d, err := scan(tx.QueryRowContext(ctx, `SELECT `+columns+` FROM deployments WHERE id = ?`, id))
	if err != nil {
		return Deployment{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Deployment{}, false, err
	}
	return d, true, nil
}

// NextQueued returns the oldest queued Deployment across all Composes.
func (s *Store) NextQueued(ctx context.Context) (Deployment, bool, error) {
	d, err := scan(s.db.QueryRowContext(ctx,
		`SELECT `+columns+` FROM deployments WHERE status = 'queued' ORDER BY queued_at, id LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return Deployment{}, false, nil
	}
	if err != nil {
		return Deployment{}, false, err
	}
	return d, true, nil
}

// Start marks a queued Deployment running and fixes its planned steps.
func (s *Store) Start(ctx context.Context, id int64, steps []string, now time.Time) error {
	if steps == nil {
		steps = []string{}
	}
	raw, err := json.Marshal(steps)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE deployments SET status = 'running', steps = ?, started_at = ? WHERE id = ? AND status = 'queued'`,
		string(raw), now.Unix(), id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotQueued
	}
	return nil
}

// SetSteps replaces the planned steps once the build Services are known.
func (s *Store) SetSteps(ctx context.Context, id int64, steps []string) error {
	if steps == nil {
		steps = []string{}
	}
	raw, err := json.Marshal(steps)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE deployments SET steps = ? WHERE id = ?`, string(raw), id)
	return err
}

func (s *Store) SetStep(ctx context.Context, id int64, step string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE deployments SET step = ? WHERE id = ?`, step, id)
	return err
}

func (s *Store) SetSource(ctx context.Context, id int64, ref, subject string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE deployments SET source_ref = ?, source_subject = ? WHERE id = ?`, ref, subject, id)
	return err
}

// Finish settles a running Deployment. A row already settled by FailRunning or
// CancelQueued, or removed by a Compose delete, is left alone and returns nil.
func (s *Store) Finish(ctx context.Context, id int64, status Status, failedStep, reason string, warnings []string, now time.Time) error {
	if warnings == nil {
		warnings = []string{}
	}
	raw, err := json.Marshal(warnings)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
UPDATE deployments SET status = ?, failed_step = ?, reason = ?, warnings = ?, finished_at = ? WHERE id = ? AND status = 'running'`,
		string(status), failedStep, reason, string(raw), now.Unix(), id)
	return err
}

// FailRunning marks every running Deployment failed, for a panel restart.
func (s *Store) FailRunning(ctx context.Context, reason string, now time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx, `
UPDATE deployments SET status = 'failed', failed_step = step, reason = ?, finished_at = ?
WHERE status = 'running'`, reason, now.Unix())
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// CancelQueued cancels the Compose's queued Deployment, if it has one.
func (s *Store) CancelQueued(ctx context.Context, composeID int64, now time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
UPDATE deployments SET status = 'canceled', finished_at = ? WHERE compose_id = ? AND status = 'queued'`,
		now.Unix(), composeID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// CancelQueuedNumber cancels the Compose's queued Deployment only when it has
// that number, so a Deployment queued after the caller looked is left alone.
func (s *Store) CancelQueuedNumber(ctx context.Context, composeID int64, number int, now time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
UPDATE deployments SET status = 'canceled', finished_at = ? WHERE compose_id = ? AND number = ? AND status = 'queued'`,
		now.Unix(), composeID, number)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// Get returns one Deployment by its per-Compose number, or sql.ErrNoRows.
func (s *Store) Get(ctx context.Context, composeID int64, number int) (Deployment, error) {
	return scan(s.db.QueryRowContext(ctx,
		`SELECT `+columns+` FROM deployments WHERE compose_id = ? AND number = ?`, composeID, number))
}

// List returns the Compose's Deployments, newest first.
func (s *Store) List(ctx context.Context, composeID int64, limit int) ([]Deployment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+columns+` FROM deployments WHERE compose_id = ? ORDER BY number DESC LIMIT ?`, composeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Deployment
	for rows.Next() {
		d, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Ahead counts the queued Deployments before d in the queue, plus one when a
// Deployment is running.
func (s *Store) Ahead(ctx context.Context, d Deployment) (int, error) {
	return ahead(ctx, s.db, d)
}

func ahead(ctx context.Context, q queryer, d Deployment) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `
SELECT (SELECT COUNT(*) FROM deployments WHERE status = 'queued'
        AND (queued_at < ? OR (queued_at = ? AND id < ?)))
     + (SELECT COUNT(*) FROM (SELECT 1 FROM deployments WHERE status = 'running' LIMIT 1))`,
		d.QueuedAt.Unix(), d.QueuedAt.Unix(), d.ID).Scan(&n)
	return n, err
}

// Prune deletes the Compose's finished Deployments beyond the newest keep and
// returns the removed numbers, ascending. Queued and running ones stay.
func (s *Store) Prune(ctx context.Context, composeID int64, keep int) ([]int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
SELECT number FROM deployments WHERE compose_id = ?
ORDER BY number DESC LIMIT -1 OFFSET ?`, composeID, keep)
	if err != nil {
		return nil, err
	}
	var removed []int
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return nil, err
		}
		removed = append(removed, n)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	var out []int
	for i := len(removed) - 1; i >= 0; i-- {
		res, err := tx.ExecContext(ctx, `
DELETE FROM deployments WHERE compose_id = ? AND number = ? AND status NOT IN ('queued', 'running')`,
			composeID, removed[i])
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			out = append(out, removed[i])
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

// Active lists the queued and running Deployments, running first, then queue order.
func (s *Store) Active(ctx context.Context) ([]Active, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT d.compose_id, c.name, d.number, d.status, d.step, p.id, p.name, e.id, e.name
FROM deployments d JOIN composes c ON c.id = d.compose_id
JOIN environments e ON e.id = c.environment_id
JOIN projects p ON p.id = e.project_id
WHERE d.status IN ('queued', 'running')
ORDER BY CASE d.status WHEN 'running' THEN 0 ELSE 1 END, d.queued_at, d.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Active
	for rows.Next() {
		var a Active
		var status string
		if err := rows.Scan(&a.ComposeID, &a.ComposeName, &a.Number, &status, &a.Step,
			&a.ProjectID, &a.ProjectName, &a.EnvironmentID, &a.EnvironmentName); err != nil {
			return nil, err
		}
		a.Status = Status(status)
		out = append(out, a)
	}
	return out, rows.Err()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func isUniqueConstraint(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}
