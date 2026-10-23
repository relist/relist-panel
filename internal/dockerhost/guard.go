package dockerhost

import (
	"context"
	"sync"
)

// Guard excludes host-wide pruning from panel operations which use Docker.
// Prune never waits for active work: callers get a conflict and decide when to retry.
// Ordinary work can nest (a Deployment also acquires its Project gate).
type Guard struct {
	mu      sync.Mutex
	users   int
	pruning bool
	blocked bool
	changed chan struct{}
}

func NewGuard() *Guard { return &Guard{changed: make(chan struct{})} }

func (g *Guard) Acquire(ctx context.Context) (func(), error) {
	for {
		g.mu.Lock()
		if err := ctx.Err(); err != nil {
			g.mu.Unlock()
			return nil, err
		}
		if g.blocked {
			g.mu.Unlock()
			return nil, ErrMaintenanceBlocked
		}
		if !g.pruning {
			g.users++
			g.mu.Unlock()
			return sync.OnceFunc(func() { g.mu.Lock(); g.users--; g.mu.Unlock() }), nil
		}
		changed := g.changed
		g.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (g *Guard) TryPrune() (func(), bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.blocked || g.pruning || g.users > 0 {
		return nil, false
	}
	g.pruning = true
	return sync.OnceFunc(func() {
		g.mu.Lock()
		g.pruning = false
		close(g.changed)
		g.changed = make(chan struct{})
		g.mu.Unlock()
	}), true
}

// Block is fail-closed: a lost mutation result is not evidence that Docker stopped.
func (g *Guard) Block()        { g.mu.Lock(); g.blocked = true; g.mu.Unlock() }
func (g *Guard) Blocked() bool { g.mu.Lock(); defer g.mu.Unlock(); return g.blocked }

// Recover is an operator acknowledgement, not a daemon completion probe.
func (g *Guard) Recover() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.blocked || g.pruning || g.users > 0 {
		return false
	}
	g.blocked = false
	return true
}
