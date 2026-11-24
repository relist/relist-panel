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
	m, ok := s.busy[id]
	if !ok {
		m = &sync.Mutex{}
		s.busy[id] = m
	}
	s.mu.Unlock()
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
	if _, err := s.db.ExecContext(ctx, `UPDATE uploads SET received = ?, updated_at = ? WHERE id = ?`,
		received, s.now().Unix(), id); err != nil {
		_ = f.Truncate(offset)
		return 0, fmt.Errorf("record chunk: %w", err)
	}
	return received, nil
}
