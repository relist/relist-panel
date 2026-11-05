package audit

import (
	"context"
	"testing"
)

func TestContextHooks(t *testing.T) {
	var recorded []Event
	rec := func(_ context.Context, e Event) error {
		recorded = append(recorded, e)
		return nil
	}
	base := Event{Actor: "a@example.com", ActorUserID: 2, ProjectID: 1, EnvironmentID: 11, IP: "192.0.2.1"}
	resolved := 0
	resolve := func(context.Context) string {
		resolved++
		return "alpha / production"
	}
	ctx, pending := Begin(context.Background(), base, resolve, rec)
	Detail(ctx, "key", "TOKEN")
	Target(ctx, "bob@example.com")
	got := pending.Event()
	if got.Detail["key"] != "TOKEN" || got.Target != "bob@example.com" || got.Actor != "a@example.com" {
		t.Fatalf("pending = %+v", got)
	}

	ctx2, _ := Begin(context.Background(), base, resolve, rec)
	Emit(ctx2, "secret.read", map[string]any{"keys": []string{"TOKEN"}})
	if len(recorded) != 1 {
		t.Fatalf("recorded = %d", len(recorded))
	}
	e := recorded[0]
	if e.Action != "secret.read" || e.Outcome != OutcomeOK || e.Target != "alpha / production" || e.ProjectID != 1 ||
		e.ActorUserID != 2 || e.At.IsZero() || resolved != 1 {
		t.Fatalf("emitted = %+v (resolved %d)", e, resolved)
	}

	// Without Begin they do nothing.
	Detail(context.Background(), "k", "v")
	Target(context.Background(), "x")
	Emit(context.Background(), "x.y", nil)
	if len(recorded) != 1 {
		t.Fatalf("no-op recorded %d", len(recorded))
	}
}

// A Terminal's close is emitted after its request context is gone, and
// possibly after its Compose was deleted: it keeps the name open found.
func TestEmitKeepsTheNameAfterTheRequestEnds(t *testing.T) {
	var recorded []Event
	rec := func(_ context.Context, e Event) error {
		recorded = append(recorded, e)
		return nil
	}
	name := "alpha / production / api"
	resolve := func(ctx context.Context) string {
		if ctx.Err() != nil {
			return "/api/path"
		}
		return name
	}
	ctx, cancel := context.WithCancel(context.Background())
	ctx, _ = Begin(ctx, Event{ProjectID: 1}, resolve, rec)
	Emit(ctx, "terminal.open", nil)
	cancel()
	name = "" // deleted
	Emit(ctx, "terminal.close", nil)
	if len(recorded) != 2 || recorded[0].Target != "alpha / production / api" || recorded[1].Target != "alpha / production / api" {
		t.Fatalf("recorded = %+v", recorded)
	}

	// The first Emit after cancel still resolves, without the cancel.
	recorded = nil
	ctx2, cancel2 := context.WithCancel(context.Background())
	ctx2, _ = Begin(ctx2, Event{ProjectID: 1}, func(ctx context.Context) string {
		if ctx.Err() != nil {
			return "/api/path"
		}
		return "alpha"
	}, rec)
	cancel2()
	Emit(ctx2, "terminal.close", nil)
	if len(recorded) != 1 || recorded[0].Target != "alpha" {
		t.Fatalf("recorded = %+v", recorded)
	}
}
