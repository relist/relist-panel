package chunkupload

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"
)

const (
	// expireAfter is how long an upload with no activity is kept.
	expireAfter = 24 * time.Hour
	// expireEmptyAfter is how long an upload that never received a byte is
	// kept: it only reserves staging space.
	expireEmptyAfter = time.Hour
	sweepEvery       = time.Hour
)

// Recover runs once at start, before any request: a job the last run left
// consuming can no longer finish, so it becomes a retryable failure, and an
// uploading file is cut back to the bytes its row counts, dropping whatever a
// crash left half-written past them.
func (s *Service) Recover(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE uploads SET state = 'failed', error_kind = ?, error = ?
		WHERE state = 'consuming'`, KindInterrupted, interruptedMessage); err != nil {
		return fmt.Errorf("recover interrupted uploads: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, received FROM uploads WHERE state = 'uploading'`)
	if err != nil {
		return fmt.Errorf("list uploads to recover: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var received int64
		if err := rows.Scan(&id, &received); err != nil {
			return fmt.Errorf("scan upload to recover: %w", err)
		}
		info, err := os.Stat(s.StagedPath(id))
		if err != nil || info.Size() <= received {
			continue // a missing file is the sweep's
		}
		if err := os.Truncate(s.StagedPath(id), received); err != nil {
			return fmt.Errorf("truncate staged %s: %w", id, err)
		}
	}
	return rows.Err()
}

// Sweep removes expired uploads with their files, uploads whose staged file
// is gone (a server migration does not carry them), and staged files no row
// owns. An upload being written is never touched.
func (s *Service) Sweep(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	rows, err := s.db.QueryContext(ctx, `SELECT `+columns+` FROM uploads`)
	if err != nil {
		return fmt.Errorf("list uploads to sweep: %w", err)
	}
	all, err := scanAll(rows)
	rows.Close()
	if err != nil {
		return err
	}

	owned := make(map[string]bool, len(all))
	for _, u := range all {
		owned[u.ID] = true
		if u.State == StateConsuming {
			continue
		}
		_, statErr := os.Stat(s.StagedPath(u.ID))
		fileGone := errors.Is(statErr, os.ErrNotExist)
		expired := now.Sub(u.UpdatedAt) > expireAfter ||
			(u.Received == 0 && now.Sub(u.UpdatedAt) > expireEmptyAfter)
		orphanRow := fileGone && (u.State == StateUploading || u.State == StateFailed)
		if !expired && !orphanRow {
			continue
		}
		if err := deleteRow(ctx, s.db, u.ID); err != nil {
			return err
		}
		delete(owned, u.ID)
		delete(s.busy, u.ID)
		if err := os.Remove(s.StagedPath(u.ID)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove staged %s: %w", u.ID, err)
		}
	}

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("list staged files: %w", err)
	}
	for _, e := range entries {
		if owned[e.Name()] {
			continue
		}
		if err := os.RemoveAll(s.StagedPath(e.Name())); err != nil {
			return fmt.Errorf("remove stray staged %s: %w", e.Name(), err)
		}
	}
	return nil
}

// RunSweeper sweeps every hour until ctx is done.
func (s *Service) RunSweeper(ctx context.Context) {
	ticker := time.NewTicker(sweepEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.Sweep(ctx); err != nil {
				log.Printf("chunkupload: sweep: %v", err)
			}
		}
	}
}
