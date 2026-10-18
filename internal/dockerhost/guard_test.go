package dockerhost

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPruneCannotOverlapWorkAndWorkWaitsForPrune(t *testing.T) {
	g := NewGuard()
	work, err := g.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := g.TryPrune(); ok {
		t.Fatal("prune started during work")
	}
	work()
	prune, ok := g.TryPrune()
	if !ok {
		t.Fatal("idle host refused prune")
	}
	if _, ok := g.TryPrune(); ok {
		t.Fatal("second prune overlapped")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := g.Acquire(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting work: %v", err)
	}
	prune()
	work, err = g.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	work()
}

func TestNestedWorkDoesNotDeadlockWhenPruneIsRefused(t *testing.T) {
	g := NewGuard()
	first, _ := g.Acquire(context.Background())
	defer first()
	if _, ok := g.TryPrune(); ok {
		t.Fatal("prune acquired during work")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	second, err := g.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second()
	if _, ok := g.TryPrune(); ok {
		t.Fatal("outer work was released by nested release")
	}
}

func TestUncertainPruneBlocksWorkUntilExplicitRecovery(t *testing.T) {
	g := NewGuard()
	release, ok := g.TryPrune()
	if !ok {
		t.Fatal("idle guard")
	}
	g.Block()
	if g.Recover() {
		t.Fatal("recovery overlapped active prune")
	}
	release()
	if _, err := g.Acquire(context.Background()); err == nil {
		t.Fatal("work started while prune completion is unknown")
	}
	if release, ok := g.TryPrune(); ok {
		release()
		t.Fatal("prune started before recovery")
	}
	if !g.Recover() {
		t.Fatal("explicit recovery failed")
	}
	work, err := g.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	work()
}
