package chunkupload

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// fullUpload creates a 5-byte upload and stores all of it.
func (h *harness) fullUpload() Session {
	h.t.Helper()
	s := h.mustCreate(1, 1, 5)
	if _, err := h.append(s.ID, 0, "hello"); err != nil {
		h.t.Fatal(err)
	}
	return s
}

// recorder is a ConsumeFunc that keeps what it read.
type recorder struct {
	mu    sync.Mutex
	calls int
	read  string
	err   error
}

func (r *recorder) consume(_ context.Context, staged io.Reader) error {
	b, err := io.ReadAll(staged)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.read = string(b)
	return r.err
}

func (h *harness) complete(id string, consume ConsumeFunc, wait time.Duration) (Session, error) {
	return h.svc.Complete(context.Background(), 1, 1, id, consume, wait)
}

func TestCompleteBeforeLastByte(t *testing.T) {
	h := newHarness(t)
	s := h.mustCreate(1, 1, 5)
	if _, err := h.append(s.ID, 0, "hel"); err != nil {
		t.Fatal(err)
	}
	var offsetErr *OffsetError
	if _, err := h.complete(s.ID, (&recorder{}).consume, time.Second); !errors.As(err, &offsetErr) || offsetErr.Received != 3 {
		t.Fatalf("incomplete: %v", err)
	}
}

func TestCompleteFastJobAnswersDone(t *testing.T) {
	h := newHarness(t)
	s := h.fullUpload()
	rec := &recorder{}
	got, err := h.complete(s.ID, rec.consume, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateDone {
		t.Fatalf("state %s, want done", got.State)
	}
	if rec.read != "hello" {
		t.Fatalf("consume read %q", rec.read)
	}
	if _, err := os.Stat(h.svc.StagedPath(s.ID)); !os.IsNotExist(err) {
		t.Fatalf("staged file kept after done: %v", err)
	}
}

func TestCompleteSlowJobAnswersConsuming(t *testing.T) {
	h := newHarness(t)
	s := h.fullUpload()
	release := make(chan struct{})
	slow := func(_ context.Context, staged io.Reader) error {
		<-release
		_, err := io.Copy(io.Discard, staged)
		return err
	}
	got, err := h.complete(s.ID, slow, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateConsuming {
		t.Fatalf("state %s, want consuming", got.State)
	}
	var stateErr *StateError
	if _, err := h.complete(s.ID, slow, 0); !errors.As(err, &stateErr) || stateErr.State != StateConsuming {
		t.Fatalf("second complete: %v", err)
	}
	close(release)
	h.svc.Wait()
	got, _ = h.svc.Get(context.Background(), 1, 1, s.ID)
	if got.State != StateDone {
		t.Fatalf("after job: %s", got.State)
	}
}

func TestJobFailureKinds(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		kind ErrorKind
		msg  string
	}{
		"refused": {&KindError{Kind: KindRefused, Err: errors.New("need 5, 1 free")}, KindRefused, "need 5"},
		"plain":   {errors.New("docker exploded"), KindFailed, "docker exploded"},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			s := h.fullUpload()
			got, err := h.complete(s.ID, (&recorder{err: tc.err}).consume, 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if got.State != StateFailed || got.ErrorKind != tc.kind || !strings.Contains(got.Error, tc.msg) {
				t.Fatalf("session = %+v", got)
			}
			if h.staged(s.ID) != "hello" {
				t.Fatal("staged file not kept after a failure")
			}
		})
	}
}

func TestCompleteRetriesFailed(t *testing.T) {
	h := newHarness(t)
	s := h.fullUpload()
	rec := &recorder{err: errors.New("first try fails")}
	if got, _ := h.complete(s.ID, rec.consume, 5*time.Second); got.State != StateFailed {
		t.Fatalf("first: %s", got.State)
	}
	rec.err = nil
	got, err := h.complete(s.ID, rec.consume, 5*time.Second)
	if err != nil || got.State != StateDone || got.ErrorKind != KindNone || got.Error != "" {
		t.Fatalf("retry = %+v, %v", got, err)
	}
	if rec.calls != 2 || rec.read != "hello" {
		t.Fatalf("calls %d read %q", rec.calls, rec.read)
	}
}

func TestJobCancelledByShutdownIsInterrupted(t *testing.T) {
	h := newHarness(t)
	s := h.fullUpload()
	started := make(chan struct{})
	blocked := func(ctx context.Context, _ io.Reader) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	if _, err := h.complete(s.ID, blocked, 0); err != nil {
		t.Fatal(err)
	}
	<-started
	h.cancel()
	h.svc.Wait()
	got, _ := h.svc.Get(context.Background(), 1, 1, s.ID)
	if got.State != StateFailed || got.ErrorKind != KindInterrupted {
		t.Fatalf("session = %+v", got)
	}
	if h.staged(s.ID) != "hello" {
		t.Fatal("staged file not kept")
	}
}

func TestCompleteOfADeletedUploadDoesNotRun(t *testing.T) {
	h := newHarness(t)
	s := h.fullUpload()
	rec := &recorder{}
	h.exec(`DELETE FROM uploads WHERE id = ?`, s.ID)
	if _, err := h.complete(s.ID, rec.consume, time.Second); !errors.Is(err, ErrNotFound) {
		t.Fatalf("complete of a deleted upload: %v", err)
	}
	h.svc.Wait()
	if rec.calls != 0 {
		t.Fatal("consume ran for a deleted upload")
	}
}
