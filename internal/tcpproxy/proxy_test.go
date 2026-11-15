package tcpproxy

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func init() { pollEvery = 20 * time.Millisecond }

// echoServer accepts on loopback and echoes every byte back until closed.
func echoServer(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	return ln
}

type running struct {
	addr string
	done chan error
	stop context.CancelFunc
}

func start(t *testing.T, target net.Listener, deadline func() time.Time, maxConns int) *running {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	dial := func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", target.Addr().String())
	}
	r := &running{addr: ln.Addr().String(), done: make(chan error, 1), stop: cancel}
	go func() { r.done <- Serve(ctx, ln, dial, deadline, maxConns) }()
	return r
}

func farFuture() func() time.Time {
	t := time.Now().Add(time.Hour)
	return func() time.Time { return t }
}

func echoOnce(t *testing.T, c net.Conn, msg string) {
	t.Helper()
	c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Write([]byte(msg)); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf) != msg {
		t.Fatalf("got %q want %q", buf, msg)
	}
}

func TestForwardsBothWays(t *testing.T) {
	r := start(t, echoServer(t), farFuture(), 4)
	c, err := net.Dial("tcp", r.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	echoOnce(t, c, "ping")
}

func TestRefusesOverMaxConns(t *testing.T) {
	r := start(t, echoServer(t), farFuture(), 2)
	var conns []net.Conn
	for i := 0; i < 2; i++ {
		c, err := net.Dial("tcp", r.addr)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		echoOnce(t, c, "a") // ensures it is accepted and counted
		conns = append(conns, c)
	}
	third, err := net.Dial("tcp", r.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close()
	third.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := third.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("third conn read = %v, want EOF", err)
	}
	for _, c := range conns {
		echoOnce(t, c, "still")
	}
}

func TestDeadlineClosesOpenConnections(t *testing.T) {
	end := time.Now().Add(300 * time.Millisecond)
	r := start(t, echoServer(t), func() time.Time { return end }, 4)
	c, err := net.Dial("tcp", r.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	echoOnce(t, c, "hi")
	c.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatal("open conn read succeeded after the deadline")
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("open conn was not closed by the deadline (read timed out)")
	}
	select {
	case err := <-r.done:
		if err != nil {
			t.Fatalf("Serve = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not return")
	}
	if c2, err := net.DialTimeout("tcp", r.addr, 500*time.Millisecond); err == nil {
		c2.Close()
		t.Fatal("new dial succeeded after the deadline")
	}
}

func TestDeadlineMovesLater(t *testing.T) {
	var end atomic.Value
	end.Store(time.Now().Add(200 * time.Millisecond))
	r := start(t, echoServer(t), func() time.Time { return end.Load().(time.Time) }, 4)
	c, err := net.Dial("tcp", r.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	echoOnce(t, c, "a")
	end.Store(time.Now().Add(2 * time.Second))
	time.Sleep(600 * time.Millisecond)
	echoOnce(t, c, "b")
}

func TestFileDeadline(t *testing.T) {
	p := filepath.Join(t.TempDir(), "deadline")
	initial := time.Unix(1800000000, 0)
	f := FileDeadline(p, initial)
	if got := f(); !got.Equal(initial) {
		t.Fatalf("missing file: %v, want %v", got, initial)
	}
	next := time.Unix(1800005000, 0)
	if err := WriteDeadline(p, next); err != nil {
		t.Fatal(err)
	}
	if got := f(); !got.Equal(next) {
		t.Fatalf("after write: %v, want %v", got, next)
	}
	if err := os.WriteFile(p, []byte("not a number"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := f(); !got.Equal(next) {
		t.Fatalf("garbage: %v, want last good %v", got, next)
	}
}
