package hostmetrics

import (
	"context"
	"testing"
	"time"
)

// A4: a panic in an off-loop sampler hook is not recovered. Run this test
// alone: the expected outcome is the test binary dying with the panic.
func TestReviewA4HookPanicKillsProcess(t *testing.T) {
	s := NewSampler(Config{
		Interval:        10 * time.Second,
		ProcRoot:        t.TempDir(),
		SweepContainers: func(context.Context) { panic("unexpected Docker stats payload") },
	})
	for i := 0; i < 3; i++ {
		s.tick(context.Background())
	}
	s.waitIdle()
	t.Log("A4 survived (a recover exists)")
}
