// Package faketest provides the test double for the terminal package's Exec
// seam that the spec's Testing Decisions call for: HTTP-seam tests install it
// in place of the real Docker Engine API adapter, script its answers, and
// assert exactly what would have run — all with no Docker in them. It lives in
// its own package so nothing but tests imports it.
package faketest

import (
	"context"
	"net"
	"sync"

	"relist-panel/internal/terminal"
)

// StartCall is one Start the fake served: the Container it was asked for, the
// options as they were passed, and the Session it handed back, whose Remote is
// the container's side of the stream.
type StartCall struct {
	ContainerID string
	Opts        terminal.ExecOptions
	Session     *Session
}

// ShellCall is one HasShell probe the fake served.
type ShellCall struct {
	ContainerID string
	Path        string
}

// KillCall is one Kill the fake served: the Container, the pid and the signal.
type KillCall struct {
	ContainerID string
	PID         int
	Signal      string
}

// Exec is a scripted terminal.Exec. Its zero value answers an empty listing,
// reports the feature available, and starts a fresh in-memory session.
type Exec struct {
	// Containers answers ServiceContainers, keyed "project/service". A
	// Service with no entry reads as no running Containers.
	Containers map[string][]terminal.Container
	// Shells answers HasShell, keyed "containerID:path". A missing entry
	// reads as the shell being absent.
	Shells map[string]bool
	// AvailableErr is returned by Available; the zero value — nil — answers
	// that the feature is available.
	AvailableErr error
	// StartErr, when set, is returned by Start instead of a session.
	StartErr error
	// ExitCode is what every started session reports from ExitCode.
	ExitCode int
	// BeforeResize, when set, runs at the start of every Session.Resize,
	// before the resize is recorded. A test can block on it to hold a resize
	// in flight.
	BeforeResize func()
	// KillExits, when it has an entry for a pid, makes a HUP end that pid:
	// the following liveness probe (signal "0") returns ErrNoProcess. Without
	// it the pid survives its HUP and the probe finds it.
	KillExits map[int]bool
	// Starts records every successful Start, in order. Reads race-free
	// through StartsSnapshot.
	Starts []StartCall

	mu         sync.Mutex
	shellCalls []ShellCall
	kills      []KillCall
	dead       map[int]bool
}

// StartsSnapshot copies the recorded Starts under the lock, for a race-free
// read while a handler may still be appending to it.
func (f *Exec) StartsSnapshot() []StartCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]StartCall(nil), f.Starts...)
}

// ShellCallsSnapshot copies the recorded HasShell probes under the lock.
func (f *Exec) ShellCallsSnapshot() []ShellCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ShellCall(nil), f.shellCalls...)
}

// KillsSnapshot copies the recorded Kills under the lock, for a race-free read
// while a handler may still be hanging a shell up.
func (f *Exec) KillsSnapshot() []KillCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]KillCall(nil), f.kills...)
}

// Available implements terminal.Exec.
func (f *Exec) Available(_ context.Context) error {
	return f.AvailableErr
}

// ServiceContainers implements terminal.Exec.
func (f *Exec) ServiceContainers(_ context.Context, project, service string) ([]terminal.Container, error) {
	return f.Containers[project+"/"+service], nil
}

// HasShell implements terminal.Exec.
func (f *Exec) HasShell(_ context.Context, containerID, path string) (bool, error) {
	f.mu.Lock()
	f.shellCalls = append(f.shellCalls, ShellCall{ContainerID: containerID, Path: path})
	f.mu.Unlock()
	return f.Shells[containerID+":"+path], nil
}

// Kill implements terminal.Exec. It records the call; a HUP for a pid in
// KillExits ends that pid, after which the "0" probe returns ErrNoProcess.
func (f *Exec) Kill(_ context.Context, containerID string, pid int, sig string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kills = append(f.kills, KillCall{ContainerID: containerID, PID: pid, Signal: sig})
	if sig == "HUP" && f.KillExits[pid] {
		if f.dead == nil {
			f.dead = make(map[int]bool)
		}
		f.dead[pid] = true
		return nil
	}
	if sig == "0" && f.dead[pid] {
		return terminal.ErrNoProcess
	}
	return nil
}

// Start implements terminal.Exec. It records the call and returns a Session
// over a net.Pipe whose Remote end is the container's side, which the test
// reads and writes.
func (f *Exec) Start(_ context.Context, containerID string, opts terminal.ExecOptions) (terminal.Session, error) {
	if f.StartErr != nil {
		return nil, f.StartErr
	}
	panel, remote := net.Pipe()
	session := &Session{conn: panel, Remote: remote, exitCode: f.ExitCode, beforeResize: f.BeforeResize}
	f.mu.Lock()
	f.Starts = append(f.Starts, StartCall{ContainerID: containerID, Opts: opts, Session: session})
	f.mu.Unlock()
	return session, nil
}

// Session is a scripted terminal.Session over one net.Pipe. Remote is the
// container's side: write to it to produce output the session reads, read from
// it to observe the bytes the session wrote. Appends to Resizes are guarded by
// a mutex; use ResizesSnapshot for a race-free read, or read the field once the
// handler has stopped.
type Session struct {
	// Remote is the container's side of the stream.
	Remote net.Conn
	// Resizes records every Resize, in order.
	Resizes [][2]uint16

	conn         net.Conn
	exitCode     int
	beforeResize func()
	mu           sync.Mutex
	closed       chan struct{}
	once         sync.Once
}

// Read implements io.Reader on the panel's side of the stream.
func (s *Session) Read(p []byte) (int, error) { return s.conn.Read(p) }

// Write implements io.Writer on the panel's side of the stream.
func (s *Session) Write(p []byte) (int, error) { return s.conn.Write(p) }

// Close closes the panel's side and signals Closed.
func (s *Session) Close() error {
	s.once.Do(func() { close(s.closedChannel()) })
	return s.conn.Close()
}

// Closed reports a channel that is closed once the session has been closed.
func (s *Session) Closed() <-chan struct{} { return s.closedChannel() }

// closedChannel lazily creates the closed channel, so a Session literal still
// signals Closed.
func (s *Session) closedChannel() chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed == nil {
		s.closed = make(chan struct{})
	}
	return s.closed
}

// Resize records a resize. The session forwards exactly what it was asked for;
// the handler clamps to 1–500.
func (s *Session) Resize(_ context.Context, rows, cols uint16) error {
	if s.beforeResize != nil {
		s.beforeResize()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Resizes = append(s.Resizes, [2]uint16{rows, cols})
	return nil
}

// ExitCode implements terminal.Session.
func (s *Session) ExitCode(_ context.Context) (int, error) {
	return s.exitCode, nil
}

// ResizesSnapshot copies the recorded resizes under the lock, for a race-free
// read while the handler may still be resizing.
func (s *Session) ResizesSnapshot() [][2]uint16 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][2]uint16(nil), s.Resizes...)
}

var _ terminal.Exec = (*Exec)(nil)
var _ terminal.Session = (*Session)(nil)
