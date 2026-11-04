package audit

import (
	"context"
	"log"
	"sync"
	"time"
)

type pendingKey struct{}

// Pending is the event a request is building: its actor, scope and IP from
// the middleware, plus what the handler adds with Detail and Target.
type Pending struct {
	mu            sync.Mutex
	base          Event
	resolveTarget func(context.Context) string
	record        func(context.Context, Event) error
}

// Begin puts a pending event in ctx. resolveTarget names the request's
// scope for Emit when base has no Target; record stores an event.
func Begin(ctx context.Context, base Event, resolveTarget func(context.Context) string, record func(context.Context, Event) error) (context.Context, *Pending) {
	p := &Pending{base: base, resolveTarget: resolveTarget, record: record}
	return context.WithValue(ctx, pendingKey{}, p), p
}

func pendingFrom(ctx context.Context) *Pending {
	p, _ := ctx.Value(pendingKey{}).(*Pending)
	return p
}

// Event answers the pending event as it stands.
func (p *Pending) Event() Event {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.base
	if p.base.Detail != nil {
		e.Detail = make(map[string]any, len(p.base.Detail))
		for k, v := range p.base.Detail {
			e.Detail[k] = v
		}
	}
	return e
}

// Detail adds one detail to the request's event. Never a variable's value.
func Detail(ctx context.Context, key string, value any) {
	p := pendingFrom(ctx)
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.base.Detail == nil {
		p.base.Detail = map[string]any{}
	}
	p.base.Detail[key] = value
}

// Target replaces the request's event target with a readable name.
func Target(ctx context.Context, target string) {
	p := pendingFrom(ctx)
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.base.Target = target
}

// Emit records an extra event now, with the request's actor and scope: a
// read the middleware cannot see (a secret value, a Terminal). A failure
// is logged.
func Emit(ctx context.Context, action string, detail map[string]any) {
	p := pendingFrom(ctx)
	if p == nil {
		return
	}
	e := p.Event()
	if e.Target == "" && p.resolveTarget != nil {
		e.Target = p.resolveTarget(ctx)
	}
	e.Action, e.Outcome, e.Detail, e.At = action, OutcomeOK, detail, time.Now()
	if err := p.record(context.WithoutCancel(ctx), e); err != nil {
		log.Printf("audit: %s: %v", action, err)
	}
}
