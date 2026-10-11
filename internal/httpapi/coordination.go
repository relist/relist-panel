package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// Project mutation gates cover lifecycle actions, parent teardown and child
// creation together. Different Projects remain independent. Volume reads also
// hold the gate: deleting their parent must not remove a mounted source halfway
// through an operation. Metadata reads and Compose previews need no gate.
type projectGate struct {
	token chan struct{}
	refs  int
}
type coordinator struct {
	mu    sync.Mutex
	gates map[int64]*projectGate
}

func newCoordinator() *coordinator { return &coordinator{gates: make(map[int64]*projectGate)} }
func (c *coordinator) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		projectID := coordinatedProject(r)
		if projectID == 0 {
			next.ServeHTTP(w, r)
			return
		}
		release, err := c.Acquire(r.Context(), projectID)
		if err != nil {
			return
		}
		defer release()
		next.ServeHTTP(w, r)
	})
}
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
	if r.Method == http.MethodPost && len(parts) == 9 && parts[5] == "composes" && parts[7] == "source" && (parts[8] == "test" || parts[8] == "upload") {
		return 0
	}
	id, _ := strconv.ParseInt(parts[2], 10, 64)
	if id < 1 {
		return 0
	}
	return id
}

// Acquire takes the Project gate, returning promptly when ctx is done.
func (c *coordinator) Acquire(ctx context.Context, id int64) (func(), error) {
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
		return func() { gate.token <- struct{}{}; unref() }, nil
	}
}
