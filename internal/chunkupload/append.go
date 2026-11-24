package chunkupload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

var (
	// ErrBusy refuses a second append or complete while one is running for
	// the same upload: two tabs resuming one upload, say.
	ErrBusy = errors.New("upload is busy with another request")
	// ErrPastEnd refuses a chunk that would run past the declared size.
	ErrPastEnd = errors.New("chunk runs past the declared size")
	// ErrShortBody reports a chunk that ended before its declared length;
	// nothing of it is kept.
	ErrShortBody = errors.New("chunk ended before its declared length")
)

// OffsetError refuses a chunk sent at the wrong place, naming where the
// upload really is so the client can continue from there.
type OffsetError struct {
	Received int64
}

func (e *OffsetError) Error() string {
	return fmt.Sprintf("upload is at offset %d", e.Received)
}

// StateError refuses an operation the upload's state does not allow.
type StateError struct {
	State State
}

func (e *StateError) Error() string {
	return fmt.Sprintf("upload is %s", e.State)
}

// lock takes an upload's per-upload lock without waiting: a second request
// for the same upload is refused, not queued.
func (s *Service) lock(id string) (func(), bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lockHeld(id)
}

// forget drops an upload's lock entry: for an ID that does not exist, so
// requests for made-up IDs leave nothing behind. A holder keeps its pointer
// and unlocks it as usual.
func (s *Service) forget(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.busy, id)
}

// lockHeld is lock for a caller that already holds s.mu (the sweep).
func (s *Service) lockHeld(id string) (func(), bool) {
	m, ok := s.busy[id]
	if !ok {
		m = &sync.Mutex{}
		s.busy[id] = m
	}
	if !m.TryLock() {
		return nil, false
	}
	return m.Unlock, true
}

// Append stores one chunk at offset and answers the new offset. The chunk is
// written after the stored offset (dropping any bytes a crash left past it),
// fsynced, and only then counted in the row, so the row never claims bytes
// that are not on disk.
func (s *Service) Append(ctx context.Context, userID, composeID int64, id string, offset int64, body io.Reader, length int64) (int64, error) {
	unlock, ok := s.lock(id)
	if !ok {
		return 0, ErrBusy
	}
	defer unlock()

	session, err := s.Get(ctx, userID, composeID, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			s.forget(id)
		}
		return 0, err
	}
	if session.State != StateUploading {
		return 0, &StateError{State: session.State}
	}
	if offset != session.Received {
		return 0, &OffsetError{Received: session.Received}
	}
	if length < 0 || offset+length > session.Size {
		return 0, ErrPastEnd
	}

	f, err := os.OpenFile(s.StagedPath(id), os.O_WRONLY, 0)
	if err != nil {
		return 0, fmt.Errorf("open staged file: %w", err)
	}
	defer f.Close()
	if err := f.Truncate(offset); err != nil {
		return 0, fmt.Errorf("truncate staged file: %w", err)
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return 0, fmt.Errorf("seek staged file: %w", err)
	}
	written, copyErr := io.CopyN(f, body, length)
	if copyErr != nil || written != length {
		_ = f.Truncate(offset)
		if copyErr != nil && !errors.Is(copyErr, io.EOF) && !errors.Is(copyErr, io.ErrUnexpectedEOF) {
			return 0, errors.Join(ErrShortBody, copyErr)
		}
		return 0, ErrShortBody
	}
	if err := f.Sync(); err != nil {
		_ = f.Truncate(offset)
		return 0, fmt.Errorf("sync staged file: %w", err)
	}
	received := offset + length
	result, err := s.db.ExecContext(ctx, `UPDATE uploads SET received = ?, updated_at = ? WHERE id = ? AND state = 'uploading'`,
		received, s.now().Unix(), id)
	if err != nil {
		_ = f.Truncate(offset)
		return 0, fmt.Errorf("record chunk: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		// The row went while the chunk was written: a Compose delete's
		// cascade. The bytes went to a file nothing owns any more.
		return 0, ErrNotFound
	}
	return received, nil
}
