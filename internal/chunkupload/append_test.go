package chunkupload

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func (h *harness) append(id string, offset int64, body string) (int64, error) {
	return h.svc.Append(context.Background(), 1, 1, id, offset, strings.NewReader(body), int64(len(body)))
}

func (h *harness) staged(id string) string {
	h.t.Helper()
	b, err := os.ReadFile(h.svc.StagedPath(id))
	if err != nil {
		h.t.Fatal(err)
	}
	return string(b)
}

func TestAppendWritesInOrder(t *testing.T) {
	h := newHarness(t)
	s := h.mustCreate(1, 1, 5)
	h.now = h.now.Add(time.Minute)
	if n, err := h.append(s.ID, 0, "hel"); err != nil || n != 3 {
		t.Fatalf("first append = %d, %v", n, err)
	}
	if n, err := h.append(s.ID, 3, "lo"); err != nil || n != 5 {
		t.Fatalf("second append = %d, %v", n, err)
	}
	if got := h.staged(s.ID); got != "hello" {
		t.Fatalf("staged %q", got)
	}
	got, err := h.svc.Get(context.Background(), 1, 1, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Received != 5 || !got.UpdatedAt.After(s.UpdatedAt) {
		t.Fatalf("session = %+v", got)
	}
}

func TestAppendStaleOffsetAnswersStored(t *testing.T) {
	h := newHarness(t)
	s := h.mustCreate(1, 1, 5)
	if _, err := h.append(s.ID, 0, "hel"); err != nil {
		t.Fatal(err)
	}
	var offsetErr *OffsetError
	if _, err := h.append(s.ID, 0, "hel"); !errors.As(err, &offsetErr) || offsetErr.Received != 3 {
		t.Fatalf("stale append: %v, want *OffsetError{3}", err)
	}
	if got := h.staged(s.ID); got != "hel" {
		t.Fatalf("staged %q", got)
	}
}

func TestAppendPastEnd(t *testing.T) {
	h := newHarness(t)
	s := h.mustCreate(1, 1, 4)
	if _, err := h.append(s.ID, 0, "hello"); !errors.Is(err, ErrPastEnd) {
		t.Fatalf("past end: %v", err)
	}
}

func TestAppendShortBodyTruncatesBack(t *testing.T) {
	h := newHarness(t)
	s := h.mustCreate(1, 1, 5)
	_, err := h.svc.Append(context.Background(), 1, 1, s.ID, 0, strings.NewReader("he"), 4)
	if !errors.Is(err, ErrShortBody) {
		t.Fatalf("short body: %v", err)
	}
	if got := h.staged(s.ID); got != "" {
		t.Fatalf("staged %q, want empty", got)
	}
	got, _ := h.svc.Get(context.Background(), 1, 1, s.ID)
	if got.Received != 0 {
		t.Fatalf("received %d", got.Received)
	}
}

func TestAppendTruncatesLeftoverBytes(t *testing.T) {
	h := newHarness(t)
	s := h.mustCreate(1, 1, 5)
	if err := os.WriteFile(h.svc.StagedPath(s.ID), []byte("junkjunk"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.append(s.ID, 0, "ab"); err != nil {
		t.Fatal(err)
	}
	if got := h.staged(s.ID); got != "ab" {
		t.Fatalf("staged %q, want ab", got)
	}
}

// blockingReader yields its bytes only once release is closed.
type blockingReader struct {
	started chan struct{}
	release chan struct{}
	r       io.Reader
}

func (b *blockingReader) Read(p []byte) (int, error) {
	select {
	case <-b.started:
	default:
		close(b.started)
	}
	<-b.release
	return b.r.Read(p)
}

func TestAppendConcurrentIsBusy(t *testing.T) {
	h := newHarness(t)
	s := h.mustCreate(1, 1, 5)
	body := &blockingReader{started: make(chan struct{}), release: make(chan struct{}), r: strings.NewReader("hel")}
	done := make(chan error, 1)
	go func() {
		_, err := h.svc.Append(context.Background(), 1, 1, s.ID, 0, body, 3)
		done <- err
	}()
	<-body.started
	if _, err := h.append(s.ID, 0, "hel"); !errors.Is(err, ErrBusy) {
		t.Fatalf("concurrent append: %v, want ErrBusy", err)
	}
	close(body.release)
	if err := <-done; err != nil {
		t.Fatalf("first append: %v", err)
	}
	if got := h.staged(s.ID); got != "hel" {
		t.Fatalf("staged %q", got)
	}
}

func TestAppendOnlyWhileUploading(t *testing.T) {
	h := newHarness(t)
	s := h.mustCreate(1, 1, 5)
	h.exec(`UPDATE uploads SET state = 'failed' WHERE id = ?`, s.ID)
	var stateErr *StateError
	if _, err := h.append(s.ID, 0, "hel"); !errors.As(err, &stateErr) || stateErr.State != StateFailed {
		t.Fatalf("append to failed: %v", err)
	}
}

func TestAppendHidesOtherUsers(t *testing.T) {
	h := newHarness(t)
	s := h.mustCreate(1, 1, 5)
	if _, err := h.svc.Append(context.Background(), 2, 1, s.ID, 0, strings.NewReader("h"), 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user: %v", err)
	}
}
