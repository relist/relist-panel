package chunkupload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"time"
)

// interruptedMessage is the failure a job gets when the panel stops under
// it, whether it is shut down mid-job or found consuming at the next start.
const interruptedMessage = "the panel restarted while writing this file; retry"

// ConsumeFunc reads the complete staged file. It runs in the background,
// after the HTTP request that started it has answered, under the server's
// lifetime context. It must be safe to call again after a failure or a panel
// restart.
type ConsumeFunc func(ctx context.Context, staged io.Reader) error

// KindError is how a ConsumeFunc classifies its failure; any other error is
// KindFailed.
type KindError struct {
	Kind ErrorKind
	Err  error
}

func (e *KindError) Error() string { return e.Err.Error() }
func (e *KindError) Unwrap() error { return e.Err }

// Complete starts writing a fully received upload through consume, in the
// background, then waits up to wait for it: a small file answers done or
// failed at once, a large one answers consuming and is polled with Get. A
// failed upload completes again the same way, from the same staged bytes.
func (s *Service) Complete(ctx context.Context, userID, composeID int64, id string, consume ConsumeFunc, wait time.Duration) (Session, error) {
	unlock, ok := s.lock(id)
	if !ok {
		return Session{}, ErrBusy
	}
	session, err := s.Get(ctx, userID, composeID, id)
	if err != nil {
		unlock()
		return Session{}, err
	}
	if session.State != StateUploading && session.State != StateFailed {
		unlock()
		return Session{}, &StateError{State: session.State}
	}
	if session.Received != session.Size {
		unlock()
		return Session{}, &OffsetError{Received: session.Received}
	}
	result, err := s.db.ExecContext(ctx, `UPDATE uploads SET state = 'consuming', error_kind = '', error = '',
		updated_at = ? WHERE id = ? AND state IN ('uploading', 'failed') AND received = size`, s.now().Unix(), id)
	if err != nil {
		unlock()
		return Session{}, fmt.Errorf("start upload job: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		unlock()
		return Session{}, ErrNotFound // deleted since the Get: a Compose delete's cascade
	}
	done := make(chan struct{})
	s.jobs.Add(1)
	go s.run(id, consume, done)
	unlock()

	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	case <-ctx.Done():
	}
	return s.Get(context.WithoutCancel(ctx), userID, composeID, id)
}

// run is one job: it feeds the staged file to consume and records the result.
// The result is written with a fresh context, because a shutdown cancels the
// job's own.
func (s *Service) run(id string, consume ConsumeFunc, done chan struct{}) {
	defer s.jobs.Done()
	defer close(done)

	err := s.consumeStaged(id, consume)
	record := context.Background()
	if err == nil {
		if _, dbErr := s.db.ExecContext(record, `UPDATE uploads SET state = 'done', updated_at = ? WHERE id = ?`,
			s.now().Unix(), id); dbErr != nil {
			log.Printf("chunkupload: record %s done: %v", id, dbErr)
			return
		}
		if rmErr := os.Remove(s.StagedPath(id)); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			log.Printf("chunkupload: remove staged %s: %v", id, rmErr)
		}
		return
	}

	kind, message := KindFailed, err.Error()
	var kindErr *KindError
	switch {
	case s.ctx.Err() != nil:
		kind, message = KindInterrupted, interruptedMessage
	case errors.As(err, &kindErr):
		kind = kindErr.Kind
	}
	if _, dbErr := s.db.ExecContext(record, `UPDATE uploads SET state = 'failed', error_kind = ?, error = ?,
		updated_at = ? WHERE id = ?`, kind, message, s.now().Unix(), id); dbErr != nil {
		log.Printf("chunkupload: record %s failed: %v", id, dbErr)
	}
}

func (s *Service) consumeStaged(id string, consume ConsumeFunc) error {
	f, err := os.Open(s.StagedPath(id))
	if err != nil {
		return fmt.Errorf("open staged file: %w", err)
	}
	defer f.Close()
	return consume(s.ctx, f)
}
