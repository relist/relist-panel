// Package chunkupload owns uploads that arrive as a sequence of small
// requests (.scratch/chunked-upload/spec.md, ADR-0053): the session row in
// `uploads`, the staged file under <data>/.uploads/<id>, the offset each chunk
// is appended at, the background job that hands the finished file to its
// consumer, and the sweep that removes abandoned uploads. It knows nothing
// about what the file is for: a feature registers a Purpose, owns its routes,
// and passes the consume step to Complete.
package chunkupload

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

const (
	// ChunkSize is the chunk size the server tells a client to send.
	ChunkSize = 32 << 20
	// MaxChunk is the largest chunk body a request may carry: well under
	// Cloudflare's 100 MB request body limit.
	MaxChunk = 64 << 20
	// stagingMargin is the free space the staging disk keeps after every
	// open upload's promised bytes: it is the disk SQLite lives on.
	stagingMargin = 2 << 30
	// maxOpenPerUser caps the uploads one User holds open (uploading or
	// failed), so nobody reserves the staging disk with uploads that never
	// send a byte.
	maxOpenPerUser = 3
	stagingDirName = ".uploads"
)

// State is where an upload is in its life.
type State string

const (
	StateUploading State = "uploading"
	StateConsuming State = "consuming"
	StateDone      State = "done"
	StateFailed    State = "failed"
)

// ErrorKind classifies a failed consume, so the HTTP layer can word it.
type ErrorKind string

const (
	KindNone        ErrorKind = ""
	KindRefused     ErrorKind = "refused"
	KindExists      ErrorKind = "exists"
	KindFailed      ErrorKind = "failed"
	KindInterrupted ErrorKind = "interrupted"
)

// Session is one upload's row.
type Session struct {
	ID           string
	UserID       int64
	ComposeID    int64 // 0 for a purpose that belongs to no Compose
	Purpose      string
	Target       string // purpose-owned
	Name         string
	Size         int64
	LastModified int64
	Received     int64
	State        State
	ErrorKind    ErrorKind
	Error        string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Purpose is what a feature plugs into chunkupload.
type Purpose struct {
	Name string
	Cap  int64 // largest declared size accepted
}

// Options configures a Service. The function fields default to the real
// clock and statfs; tests inject them.
type Options struct {
	Purposes  []Purpose
	FreeBytes func(dir string) (uint64, error)
	Now       func() time.Time
}

// NewSession is what Create needs to start an upload.
type NewSession struct {
	Purpose      string
	UserID       int64
	ComposeID    int64
	Target       string
	Name         string
	Size         int64
	LastModified int64
}

var (
	ErrNotFound    = errors.New("upload not found")
	ErrTooLarge    = errors.New("upload exceeds the cap")
	ErrTooManyOpen = errors.New("finish or discard an open upload first")
	ErrConsuming   = errors.New("upload is being written")
)

// NoSpaceError refuses an upload the staging disk cannot hold.
type NoSpaceError struct {
	Need, Free uint64
}

func (e *NoSpaceError) Error() string {
	return fmt.Sprintf("need %d bytes, %d free", e.Need, e.Free)
}

// Service is the chunked upload store and job runner.
type Service struct {
	ctx      context.Context
	db       *sql.DB
	dir      string
	purposes map[string]Purpose
	free     func(dir string) (uint64, error)
	now      func() time.Time

	mu    sync.Mutex             // serialises Create's checks with its insert, and guards busy
	busy  map[string]*sync.Mutex // one append or complete at a time per upload
	jobs  sync.WaitGroup
	doneC map[string]chan struct{} // the running job of each upload
}

// New opens the staging directory under dataDir. ctx is the server's
// lifetime: background jobs run under it and are cancelled with it.
func New(ctx context.Context, db *sql.DB, dataDir string, opts Options) (*Service, error) {
	dir := filepath.Join(dataDir, stagingDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create upload staging dir: %w", err)
	}
	s := &Service{
		ctx:      ctx,
		db:       db,
		dir:      dir,
		purposes: make(map[string]Purpose),
		free:     opts.FreeBytes,
		now:      opts.Now,
		busy:     make(map[string]*sync.Mutex),
		doneC:    make(map[string]chan struct{}),
	}
	for _, p := range opts.Purposes {
		s.purposes[p.Name] = p
	}
	if s.free == nil {
		s.free = statfsFree
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s, nil
}

func statfsFree(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, fmt.Errorf("statfs %s: %w", dir, err)
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}

// StagedPath is where an upload's bytes are kept.
func (s *Service) StagedPath(id string) string {
	return filepath.Join(s.dir, id)
}

// Create starts an upload: it checks the purpose's cap, the User's open
// uploads and the staging disk's space, then records the row and creates the
// empty staged file. The checks and the insert run under one lock, so two
// concurrent starts cannot both pass them.
func (s *Service) Create(ctx context.Context, n NewSession) (Session, error) {
	purpose, ok := s.purposes[n.Purpose]
	if !ok {
		return Session{}, fmt.Errorf("unknown upload purpose %q", n.Purpose)
	}
	if n.Size < 1 {
		return Session{}, errors.New("upload size must be at least 1 byte")
	}
	if n.Size > purpose.Cap {
		return Session{}, ErrTooLarge
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	open, err := countOpen(ctx, s.db, n.UserID)
	if err != nil {
		return Session{}, err
	}
	if open >= maxOpenPerUser {
		return Session{}, ErrTooManyOpen
	}
	promised, err := promisedBytes(ctx, s.db)
	if err != nil {
		return Session{}, err
	}
	free, err := s.free(s.dir)
	if err != nil {
		return Session{}, err
	}
	need := uint64(promised) + uint64(n.Size) + stagingMargin
	if free < need {
		return Session{}, &NoSpaceError{Need: need, Free: free}
	}

	id, err := newID()
	if err != nil {
		return Session{}, err
	}
	now := s.now()
	session := Session{
		ID: id, UserID: n.UserID, ComposeID: n.ComposeID, Purpose: n.Purpose, Target: n.Target,
		Name: n.Name, Size: n.Size, LastModified: n.LastModified, State: StateUploading,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := insert(ctx, s.db, session); err != nil {
		return Session{}, err
	}
	f, err := os.OpenFile(s.StagedPath(id), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		_ = deleteRow(ctx, s.db, id)
		return Session{}, fmt.Errorf("create staged file: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = deleteRow(ctx, s.db, id)
		_ = os.Remove(s.StagedPath(id))
		return Session{}, fmt.Errorf("create staged file: %w", err)
	}
	return session, nil
}

// Get answers one upload, only to the User who started it and only under the
// Compose it targets.
func (s *Service) Get(ctx context.Context, userID, composeID int64, id string) (Session, error) {
	return get(ctx, s.db, userID, composeID, id)
}

// ListOpen answers a User's uploads for one Compose and purpose in every state
// but done, most recently active first.
func (s *Service) ListOpen(ctx context.Context, userID, composeID int64, purpose string) ([]Session, error) {
	return listOpen(ctx, s.db, userID, composeID, purpose)
}

// Cancel discards an upload and its staged bytes. One being written is not
// cancelled: its job holds the file.
func (s *Service) Cancel(ctx context.Context, userID, composeID int64, id string) error {
	session, err := s.Get(ctx, userID, composeID, id)
	if err != nil {
		return err
	}
	if session.State == StateConsuming {
		return ErrConsuming
	}
	if err := deleteRow(ctx, s.db, id); err != nil {
		return err
	}
	if err := os.Remove(s.StagedPath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove staged file: %w", err)
	}
	return nil
}

// Wait blocks until every background job has finished.
func (s *Service) Wait() {
	s.jobs.Wait()
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("upload id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
