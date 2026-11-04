package terminal

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// serveAccessLoop runs accessLoop on the server side of one WebSocket and
// returns the client side.
func serveAccessLoop(t *testing.T, still func(context.Context) (bool, error)) (*websocket.Conn, <-chan struct{}) {
	t.Helper()
	s := &Service{accessInterval: 10 * time.Millisecond}
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx, cancel := context.WithCancel(context.Background())
		go s.accessLoop(ctx, conn, cancel, still)
		<-ctx.Done()
		close(done)
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	client, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.CloseNow() })
	return client, done
}

func TestAccessLoopClosesOnRevoke(t *testing.T) {
	var calls atomic.Int32
	client, done := serveAccessLoop(t, func(context.Context) (bool, error) {
		return calls.Add(1) == 1, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, err := client.Read(ctx)
	var closeErr websocket.CloseError
	if !errors.As(err, &closeErr) || closeErr.Code != websocket.StatusPolicyViolation || closeErr.Reason != "Your access to this Terminal was removed" {
		t.Fatalf("read = %v, want close 1008 with the access reason", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the session was not cancelled")
	}
	if n := calls.Load(); n > 3 {
		t.Fatalf("still was called %d times before the close, want at most 3", n)
	}
}

func TestAccessLoopKeepsSessionOnError(t *testing.T) {
	var calls atomic.Int32
	client, done := serveAccessLoop(t, func(context.Context) (bool, error) {
		calls.Add(1)
		return false, errors.New("database is busy")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, _, err := client.Read(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("read = %v, want the session still open", err)
	}
	select {
	case <-done:
		t.Fatal("an error ended the session")
	default:
	}
	if calls.Load() < 2 {
		t.Fatalf("still was called %d times, want it retried", calls.Load())
	}
}
