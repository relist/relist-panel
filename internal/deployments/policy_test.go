package deployments

import (
	"context"
	"strings"
	"sync"
	"testing"

	"relist-panel/internal/composes"
	"relist-panel/internal/policy"
)

// fakePolicy passes its first `passes` evaluations and violates after,
// recording each in rec.
type fakePolicy struct {
	mu     sync.Mutex
	rec    *recorder
	passes int
	calls  int
}

func (p *fakePolicy) EvaluatePolicy(context.Context, composes.DeployTarget) (composes.PolicyResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.rec.add("policy")
	if p.calls <= p.passes {
		return composes.PolicyResult{Hash: "h"}, nil
	}
	return composes.PolicyResult{Hash: "h", Violations: []policy.Violation{{Service: "web", Message: "privileged is not allowed"}}}, nil
}

func (p *fakePolicy) Approve(context.Context, int64, string, int64) error { return nil }

// An approval withdrawn between queueing and running stops the Deployment
// before up.
func TestWorkerRefusesAViolatingConfigBeforeUp(t *testing.T) {
	h := newHarness(t)
	h.policy = &fakePolicy{rec: h.rec, passes: 1}
	d := h.enqueue(h.a, ModeDeploy)
	h.start()
	got := h.waitSettled(h.a, d.Number)
	if got.Status != StatusFailed || !strings.Contains(got.Reason, "privileged is not allowed") {
		t.Fatalf("status = %s reason %q", got.Status, got.Reason)
	}
	if len(h.comp.deployed) != 0 {
		t.Fatalf("deploys = %+v, want none", h.comp.deployed)
	}
}

// A build can run privileged too: the worker checks before the first one.
func TestWorkerChecksBeforeTheFirstBuild(t *testing.T) {
	h := newHarness(t)
	h.gitSource(h.a)
	h.policy = &fakePolicy{rec: h.rec, passes: 1}
	d := h.enqueue(h.a, ModeBuild)
	h.start()
	got := h.waitSettled(h.a, d.Number)
	if got.Status != StatusFailed {
		t.Fatalf("status = %s reason %q", got.Status, got.Reason)
	}
	if len(h.build.opts) != 0 {
		t.Fatalf("builds = %+v, want none", h.build.opts)
	}
}

// A config that passes everywhere deploys, checked at enqueue, before the
// build and before up.
func TestPassingConfigIsCheckedThroughTheDeployment(t *testing.T) {
	h := newHarness(t)
	h.gitSource(h.a)
	h.policy = &fakePolicy{rec: h.rec, passes: 100}
	d := h.enqueue(h.a, ModeBuild)
	h.start()
	if got := h.waitSettled(h.a, d.Number); got.Status != StatusSucceeded {
		t.Fatalf("status = %s reason %q", got.Status, got.Reason)
	}
	if got := h.rec.count("policy"); got != 3 {
		t.Fatalf("policy checks = %d, want 3 (enqueue, before build, before up)", got)
	}
}
