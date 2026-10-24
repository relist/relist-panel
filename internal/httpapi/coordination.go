package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"relist-panel/internal/dockerhost"
)

// Project mutation gates cover lifecycle actions, parent teardown and child
// creation together. Different Projects remain independent. Alongside the gate
// each Project has a volume lock: Volume reads share it and anything that
// removes or rewrites volumes (deleting a Compose, Environment or Project, a
// Volume write, a fresh-volumes Deploy) holds it exclusively, so a delete
// cannot pull a mounted source out from under a long archive download. Reads
// never take the gate, which keeps a download from stalling the Deployment
// queue. Metadata reads and Compose previews need no gate.
//
// The lock order is always: host maintenance guard, mutation gate, volume
// lock. Nothing takes the gate while holding a volume lock.
type projectGate struct {
	token chan struct{}
	refs  int
}

// volumeLock is a read/write lock for one Project's volumes. A waiting
// exclusive holder blocks new shared holders, so a steady stream of readers
// cannot starve a delete. Its entry exists only while someone holds or waits:
// refs counts every caller (holder or waiter, shared or exclusive), and the
// entry goes when it reaches zero. readers, writer and waiting only decide
// admission.
type volumeLock struct {
	refs    int
	readers int
	writer  bool
	waiting int // exclusive holders waiting
	changed chan struct{}
}

type coordinator struct {
	host    *dockerhost.Guard
	mu      sync.Mutex
	gates   map[int64]*projectGate
	volumes map[int64]*volumeLock
}

func newCoordinator() *coordinator {
	return &coordinator{gates: make(map[int64]*projectGate), volumes: make(map[int64]*volumeLock)}
}
func (c *coordinator) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Image loading uses the same daemon as pruning, outside a Project.
		if c.host != nil && r.Method == http.MethodPut && r.URL.Path == "/api/images" {
			release, err := c.host.Acquire(r.Context())
			if err != nil {
				coordinationError(w, err)
				return
			}
			defer release()
		}
		projectID := coordinatedProject(r)
		if projectID == 0 {
			next.ServeHTTP(w, r)
			return
		}
		release, err := c.acquireFor(r.Context(), projectID, volumeClass(r))
		if err != nil {
			coordinationError(w, err)
			return
		}
		defer release()
		next.ServeHTTP(w, r)
	})
}

// volumeAccess is how a coordinated request uses the Project's volume lock.
type volumeAccess int

const (
	volumesUntouched volumeAccess = iota // mutation gate only
	volumesRead                          // shared volume lock only, no gate
	volumesWrite                         // mutation gate, then exclusive volume lock
)

// volumeClass sorts by method, not by route list, so a new volume route is
// safe by default: under .../volumes a read is GET or HEAD and everything else
// is a write. Deleting a Compose, Environment or Project removes volumes too.
func volumeClass(r *http.Request) volumeAccess {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	read := r.Method == http.MethodGet || r.Method == http.MethodHead
	if len(parts) >= 8 && parts[7] == "volumes" && parts[3] == "environments" && parts[5] == "composes" {
		if read {
			return volumesRead
		}
		return volumesWrite
	}
	if r.Method == http.MethodDelete && len(parts) >= 3 && parts[0] == "api" && parts[1] == "projects" {
		switch {
		case len(parts) == 3, // a Project
			len(parts) == 5 && parts[3] == "environments",
			len(parts) == 7 && parts[3] == "environments" && parts[5] == "composes":
			return volumesWrite
		}
	}
	return volumesUntouched
}

// acquireFor takes the host guard once, then what the class needs, in lock
// order. The release function frees them in reverse.
func (c *coordinator) acquireFor(ctx context.Context, id int64, access volumeAccess) (func(), error) {
	releaseHost, err := c.acquireHost(ctx)
	if err != nil {
		return nil, err
	}
	if access == volumesRead {
		releaseVolumes, err := c.lockVolumes(ctx, id, false)
		if err != nil {
			releaseHost()
			return nil, err
		}
		return func() { releaseVolumes(); releaseHost() }, nil
	}
	releaseGate, err := c.lockGate(ctx, id)
	if err != nil {
		releaseHost()
		return nil, err
	}
	if access == volumesUntouched {
		return func() { releaseGate(); releaseHost() }, nil
	}
	releaseVolumes, err := c.lockVolumes(ctx, id, true)
	if err != nil {
		releaseGate()
		releaseHost()
		return nil, err
	}
	return func() { releaseVolumes(); releaseGate(); releaseHost() }, nil
}

// coordinatedProject is the Project a request is serialised on, or 0 when it
// needs no coordination. Volume GET/HEAD requests are coordinated too, on the
// shared volume lock only (see volumeClass).
func coordinatedProject(r *http.Request) int64 {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 || parts[0] != "api" || parts[1] != "projects" {
		return 0
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		if len(parts) < 8 || parts[7] != "volumes" {
			return 0
		}
	}
	// A Compose preview only reads the database, so it never waits behind a
	// deploy (compose-preview spec).
	if r.Method == http.MethodPost && len(parts) == 8 && parts[5] == "composes" && parts[7] == "preview" {
		return 0
	}
	// Canceling a Deployment must work while a Deploy holds the gate, and
	// testing or uploading a Source never touches what a Deploy uses (the
	// upload gates its own swap).
	if r.Method == http.MethodPost && len(parts) == 10 && parts[5] == "composes" && parts[7] == "deployments" && parts[9] == "cancel" {
		return 0
	}
	// Enqueue captures configuration under the same gate itself. Network and
	// filesystem capture must finish before opening its SQLite transaction.
	if r.Method == http.MethodPost && len(parts) == 8 && parts[5] == "composes" && parts[7] == "deployments" {
		return 0
	}
	if r.Method == http.MethodPost && len(parts) == 9 && parts[5] == "composes" && parts[7] == "source" && (parts[8] == "test" || parts[8] == "upload") {
		return 0
	}
	id, _ := strconv.ParseInt(parts[2], 10, 64)
	if id < 1 {
		return 0
	}
	return id
}

func (c *coordinator) acquireHost(ctx context.Context) (func(), error) {
	if c.host == nil {
		return func() {}, nil
	}
	return c.host.Acquire(ctx)
}

// Acquire takes the Project mutation gate, returning promptly when ctx is done.
func (c *coordinator) Acquire(ctx context.Context, id int64) (func(), error) {
	return c.acquireFor(ctx, id, volumesUntouched)
}

// AcquireVolumes takes the Project's volume lock exclusively, waiting for open
// Volume reads. A caller holding the mutation gate (Acquire) calls it second;
// it must never take the gate while holding a volume lock.
func (c *coordinator) AcquireVolumes(ctx context.Context, id int64) (func(), error) {
	releaseHost, err := c.acquireHost(ctx)
	if err != nil {
		return nil, err
	}
	release, err := c.lockVolumes(ctx, id, true)
	if err != nil {
		releaseHost()
		return nil, err
	}
	return func() { release(); releaseHost() }, nil
}

// acquireVolumesShared takes a read hold, which coexists with other read
// holds.
func (c *coordinator) acquireVolumesShared(ctx context.Context, id int64) (func(), error) {
	return c.acquireFor(ctx, id, volumesRead)
}

func (c *coordinator) lockGate(ctx context.Context, id int64) (func(), error) {
	c.mu.Lock()
	gate := c.gates[id]
	if gate == nil {
		gate = &projectGate{token: make(chan struct{}, 1)}
		gate.token <- struct{}{}
		c.gates[id] = gate
	}
	gate.refs++
	c.mu.Unlock()
	unref := func() {
		c.mu.Lock()
		gate.refs--
		if gate.refs == 0 {
			delete(c.gates, id)
		}
		c.mu.Unlock()
	}
	select {
	case <-ctx.Done():
		unref()
		return nil, ctx.Err()
	case <-gate.token:
		if err := ctx.Err(); err != nil {
			gate.token <- struct{}{}
			unref()
			return nil, err
		}
		return sync.OnceFunc(func() { gate.token <- struct{}{}; unref() }), nil
	}
}

// lockVolumes takes the volume lock without the host guard. Waiting follows
// dockerhost.Guard: re-check under the mutex, sleep on the lock's changed
// channel, and wake everyone whenever state moves.
func (c *coordinator) lockVolumes(ctx context.Context, id int64, exclusive bool) (func(), error) {
	c.mu.Lock()
	lock := c.volumes[id]
	if lock == nil {
		lock = &volumeLock{changed: make(chan struct{})}
		c.volumes[id] = lock
	}
	lock.refs++
	if exclusive {
		lock.waiting++
	}
	// leave is called with c.mu held when this caller stops waiting or
	// releases; it wakes the others and drops an entry nobody uses.
	leave := func() {
		close(lock.changed)
		lock.changed = make(chan struct{})
		lock.refs--
		if lock.refs == 0 && c.volumes[id] == lock {
			delete(c.volumes, id)
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			if exclusive {
				lock.waiting--
			}
			leave()
			c.mu.Unlock()
			return nil, err
		}
		if exclusive && !lock.writer && lock.readers == 0 {
			lock.waiting--
			lock.writer = true
			c.mu.Unlock()
			return sync.OnceFunc(func() {
				c.mu.Lock()
				lock.writer = false
				leave()
				c.mu.Unlock()
			}), nil
		}
		if !exclusive && !lock.writer && lock.waiting == 0 {
			lock.readers++
			c.mu.Unlock()
			return sync.OnceFunc(func() {
				c.mu.Lock()
				lock.readers--
				leave()
				c.mu.Unlock()
			}), nil
		}
		changed := lock.changed
		c.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
		}
		c.mu.Lock()
	}
}

func coordinationError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
