package composes

import (
	"context"
	"sort"
	"sync"
	"time"
)

// snapshotTTL is how long one Engine listing of every Compose container is
// reused by list requests.
const snapshotTTL = 2 * time.Second

// snapshotTimeout bounds the shared Engine call, which runs on no caller's ctx.
const snapshotTimeout = 10 * time.Second

// statusOf is the ComposeStatus of one project's containers: Total counts
// every container, Running those in state running, and Services is the
// distinct Service names, sorted.
func statusOf(containers []EngineContainer) ComposeStatus {
	var status ComposeStatus
	seen := map[string]bool{}
	for _, c := range containers {
		status.Total++
		if c.Running {
			status.Running++
		}
		if c.Service != "" && !seen[c.Service] {
			seen[c.Service] = true
			status.Services = append(status.Services, c.Service)
		}
	}
	sort.Strings(status.Services)
	return status
}

// snapshot shares one ComposeContainers call between list requests: callers
// that arrive while it runs wait for it, and its result is reused for
// snapshotTTL. Errors are never kept. There is no background loop.
type snapshot struct {
	mu        sync.Mutex
	now       func() time.Time
	at        time.Time
	byProject map[string][]EngineContainer // nil when there is nothing to reuse
	inflight  *snapshotCall
	gen       uint64 // bumped by drop, so a call that started earlier is not kept
}

type snapshotCall struct {
	done      chan struct{}
	byProject map[string][]EngineContainer
	err       error
}

func newSnapshot() *snapshot { return &snapshot{now: time.Now} }

func (n *snapshot) get(ctx context.Context, engine ContainerEngine) (map[string][]EngineContainer, error) {
	n.mu.Lock()
	if n.byProject != nil && n.now().Sub(n.at) < snapshotTTL {
		byProject := n.byProject
		n.mu.Unlock()
		return byProject, nil
	}
	call := n.inflight
	if call == nil {
		call = &snapshotCall{done: make(chan struct{})}
		n.inflight = call
		gen := n.gen
		// The shared call must not die with the caller that happened to start it.
		go n.fetch(context.WithoutCancel(ctx), engine, call, gen)
	}
	n.mu.Unlock()

	select {
	case <-call.done:
		return call.byProject, call.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (n *snapshot) fetch(ctx context.Context, engine ContainerEngine, call *snapshotCall, gen uint64) {
	ctx, cancel := context.WithTimeout(ctx, snapshotTimeout)
	defer cancel()
	containers, err := engine.ComposeContainers(ctx)
	if err == nil {
		call.byProject = map[string][]EngineContainer{}
		for _, c := range containers {
			call.byProject[c.Project] = append(call.byProject[c.Project], c)
		}
	}
	call.err = err

	n.mu.Lock()
	if n.inflight == call {
		n.inflight = nil
	}
	if err == nil && gen == n.gen {
		n.byProject, n.at = call.byProject, n.now()
	}
	n.mu.Unlock()
	close(call.done)
}

// drop forgets the snapshot and any call in flight, so the next get reads
// fresh. A runner action calls it: the list then shows the outcome at once.
func (n *snapshot) drop() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.byProject, n.inflight = nil, nil
	n.gen++
}
