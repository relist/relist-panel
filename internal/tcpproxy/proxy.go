// Package tcpproxy forwards TCP connections to one fixed target and stops at
// a deadline that can be moved later while it runs. It is the whole job of
// the relist-proxy container behind a Direct access (ADR-0041): the deadline
// is enforced here, in the container, so the access closes on time even when
// the panel is down.
package tcpproxy

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// pollEvery is how often the deadline is reread. Tests shorten it.
var pollEvery = 2 * time.Second

// Serve accepts on ln and forwards each connection to dial(), at most
// maxConns at once: one more is closed at accept. deadline is polled; when it
// passes, or ctx ends, Serve closes the listener and every open connection
// and returns nil. A deadline moved later while Serve runs is honoured.
func Serve(ctx context.Context, ln net.Listener, dial func(ctx context.Context) (net.Conn, error), deadline func() time.Time, maxConns int) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		mu     sync.Mutex
		conns  = map[net.Conn]struct{}{}
		closed bool
		wg     sync.WaitGroup
	)
	track := func(c net.Conn) bool {
		mu.Lock()
		defer mu.Unlock()
		if closed {
			return false
		}
		conns[c] = struct{}{}
		return true
	}
	untrack := func(c net.Conn) {
		mu.Lock()
		delete(conns, c)
		mu.Unlock()
		c.Close()
	}
	shutdown := func() {
		mu.Lock()
		closed = true
		for c := range conns {
			c.Close()
		}
		mu.Unlock()
		ln.Close()
	}

	// The watcher is the only thing that ends Serve on time.
	go func() {
		t := time.NewTicker(pollEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if !time.Now().Before(deadline()) {
					cancel()
					return
				}
			}
		}
	}()
	go func() {
		<-ctx.Done()
		shutdown()
	}()

	sem := make(chan struct{}, maxConns)
	var acceptErr error
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() == nil {
				acceptErr = err
				cancel()
			}
			break
		}
		select {
		case sem <- struct{}{}:
		default:
			c.Close()
			continue
		}
		if !track(c) {
			<-sem
			c.Close()
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			defer untrack(c)
			forward(ctx, c, dial, track, untrack)
		}()
	}
	<-ctx.Done()
	shutdown()
	wg.Wait()
	return acceptErr
}

func forward(ctx context.Context, client net.Conn, dial func(context.Context) (net.Conn, error), track func(net.Conn) bool, untrack func(net.Conn)) {
	up, err := dial(ctx)
	if err != nil {
		return
	}
	if !track(up) {
		up.Close()
		return
	}
	defer untrack(up)
	done := make(chan struct{}, 2)
	pipe := func(dst, src net.Conn) {
		io.Copy(dst, src)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		}
		done <- struct{}{}
	}
	go pipe(up, client)
	go pipe(client, up)
	// Both directions must finish (or the conns be closed by shutdown) before
	// the tracked conns are released.
	<-done
	<-done
}

// FileDeadline returns a func that reads a unix-seconds deadline from path.
// A missing or unparsable file keeps the last good value, starting at initial.
func FileDeadline(path string, initial time.Time) func() time.Time {
	var mu sync.Mutex
	last := initial
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		b, err := os.ReadFile(path)
		if err != nil {
			return last
		}
		n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
		if err != nil {
			return last
		}
		last = time.Unix(n, 0)
		return last
	}
}

// WriteDeadline writes t as unix seconds to path atomically: a temp file in
// the same directory, then a rename, so a reader never sees half a number.
func WriteDeadline(path string, t time.Time) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".deadline-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.WriteString(strconv.FormatInt(t.Unix(), 10))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
		return errors.New("write deadline: " + err.Error())
	}
	return nil
}
