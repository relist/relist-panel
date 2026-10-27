package vulnscan

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type TaskKind string

const (
	KindDBUpdate TaskKind = "db_update"
	KindImage    TaskKind = "image"
)

type Reason string

const (
	ReasonDeploy Reason = "deploy"
	ReasonDaily  Reason = "daily"
	ReasonManual Reason = "manual"
	ReasonSetup  Reason = "setup"
)

// Task is one row of the scan queue: a DB update, or one image to scan.
type Task struct {
	ID       int64
	Kind     TaskKind
	ImageID  string
	Reason   Reason
	QueuedAt time.Time
	Started  bool
}

// Enqueue adds a task unless the same one is already queued, and reports
// whether it added a row. Asking again manually upgrades a queued automatic
// task, so it is not skipped for an unchanged DB.
func (s *Store) Enqueue(ctx context.Context, kind TaskKind, imageID string, reason Reason, now time.Time) (bool, error) {
	var image any
	if kind == KindImage {
		image = imageID
	}
	res, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO scan_queue (kind, image_id, reason, queued_at) VALUES (?, ?, ?, ?)`,
		string(kind), image, string(reason), now.Unix())
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return true, nil
	}
	if reason == ReasonManual {
		_, err = s.db.ExecContext(ctx, `UPDATE scan_queue SET reason = ? WHERE kind = ? AND (image_id IS ? OR kind = 'db_update')`,
			string(reason), string(kind), image)
	}
	return false, err
}

const taskColumns = `id, kind, COALESCE(image_id, ''), reason, queued_at, started_at IS NOT NULL`

func scanTask(row interface{ Scan(...any) error }) (Task, error) {
	var t Task
	var queued int64
	err := row.Scan(&t.ID, &t.Kind, &t.ImageID, &t.Reason, &queued, &t.Started)
	t.QueuedAt = time.Unix(queued, 0)
	return t, err
}

// Next is the task to run: a DB update first, then images in queue order.
func (s *Store) Next(ctx context.Context) (Task, bool, error) {
	t, err := scanTask(s.db.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM scan_queue
		ORDER BY kind = 'image', queued_at, id LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, false, nil
	}
	return t, err == nil, err
}

func (s *Store) Start(ctx context.Context, id int64, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE scan_queue SET started_at = ? WHERE id = ?`, now.Unix(), id)
	return err
}

// Done removes a finished task.
func (s *Store) Done(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM scan_queue WHERE id = ?`, id)
	return err
}

func (s *Store) ClearQueue(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM scan_queue`)
	return err
}

// Unstart returns the tasks a restart interrupted to the queue.
func (s *Store) Unstart(ctx context.Context) (int, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE scan_queue SET started_at = NULL WHERE started_at IS NOT NULL`)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// QueueState reports the running task, if any, and how many wait behind it.
func (s *Store) QueueState(ctx context.Context) (*Task, int, error) {
	var queued int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scan_queue WHERE started_at IS NULL`).Scan(&queued); err != nil {
		return nil, 0, err
	}
	t, err := scanTask(s.db.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM scan_queue WHERE started_at IS NOT NULL LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, queued, nil
	}
	if err != nil {
		return nil, 0, err
	}
	return &t, queued, nil
}
