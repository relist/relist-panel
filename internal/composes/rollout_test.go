package composes

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// rolloutConfig is compose config for web with a healthcheck (wait limit
// 0 + (1s + 1s) × 1 + 10s = 12s) and one Traefik label.
const rolloutConfig = `{"services":{"web":{"healthcheck":{"test":["CMD","true"],"interval":"1s","timeout":"1s","retries":1},"labels":{"traefik.enable":"true","other":"x"}}}}`

var oldLabels = map[string]string{"traefik.enable": "true", "com.docker.compose.service": "web"}

type rolloutFixture struct {
	svc    *Service
	runner *recordingRunner
	engine *fakeEngine
	target DeployTarget
	out    *strings.Builder
}

// newRollout has web running as Container a. Scaling adds the IDs in newIDs
// as running web Containers.
func newRollout(t *testing.T, newIDs ...string) *rolloutFixture {
	t.Helper()
	pollWas, overlapWas := rolloutPoll, rolloutOverlap
	rolloutPoll, rolloutOverlap = time.Millisecond, 0
	t.Cleanup(func() { rolloutPoll, rolloutOverlap = pollWas, overlapWas })

	svc, runner, target, _ := newDeployService(t, "services:\n  web: {image: nginx}\n")
	engine := &fakeEngine{
		containers: []EngineContainer{{ID: "a", Name: "p-web-1", Service: "web", Running: true}},
		states:     map[string][]EngineState{"a": {{Running: true, Health: "healthy", Labels: oldLabels}}},
	}
	svc.WithEngine(engine)
	runner.config = []byte(rolloutConfig)
	runner.onScale = func(ScaleOptions) {
		engine.mu.Lock()
		defer engine.mu.Unlock()
		for i, id := range newIDs {
			engine.containers = append(engine.containers, EngineContainer{ID: id, Name: "p-web-" + string(rune('2'+i)), Service: "web", Running: true})
		}
	}
	return &rolloutFixture{svc: svc, runner: runner, engine: engine, target: target, out: &strings.Builder{}}
}

func (f *rolloutFixture) health(id string, states ...EngineState) {
	f.engine.mu.Lock()
	defer f.engine.mu.Unlock()
	f.engine.states[id] = states
}

func (f *rolloutFixture) run(opts RolloutOptions) ([]string, error) {
	opts.Out = f.out
	return f.svc.RunRollout(context.Background(), f.target, "web", opts)
}

func TestRolloutHealthy(t *testing.T) {
	f := newRollout(t, "b")
	rolloutOverlap = 50 * time.Millisecond
	f.health("b", EngineState{Running: true, Health: "starting"}, EngineState{Running: true, Health: "healthy"})
	start := time.Now()
	warnings, err := f.run(RolloutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
		t.Fatalf("rollout took %s, want the 50ms overlap before stopping the old container", elapsed)
	}
	if !reflect.DeepEqual(f.runner.scales, []ScaleOptions{{Service: "web", Replicas: 2, NoPull: true, Out: f.out}}) {
		t.Fatalf("scales = %+v", f.runner.scales)
	}
	want := []string{"inspect a", "inspect b", "inspect b", "stop a", "remove a force=false v=false"}
	if got := f.engine.recorded(); !reflect.DeepEqual(got, want) {
		t.Fatalf("engine calls = %v, want %v", got, want)
	}
}

func TestRolloutDrift(t *testing.T) {
	f := newRollout(t, "b")
	rolloutOverlap = 2 * time.Second
	f.health("a", EngineState{Running: true, Health: "healthy", Labels: map[string]string{"traefik.enable": "false"}})
	f.health("b", EngineState{Running: true, Health: "healthy"})
	start := time.Now()
	warnings, err := f.run(RolloutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("drift mode waited out the overlap")
	}
	want := "web's Traefik labels changed (Domain edited or zero-downtime just enabled); this deploy has a short outage"
	if !reflect.DeepEqual(warnings, []string{want}) {
		t.Fatalf("warnings = %v, want [%s]", warnings, want)
	}
}

func TestRolloutUnhealthy(t *testing.T) {
	f := newRollout(t, "b")
	f.health("b", EngineState{Running: true, Health: "unhealthy", LastHealthOutput: strings.Repeat("x", 3000)})
	_, err := f.run(RolloutOptions{})
	if err == nil || err.Error() != "web did not become healthy in 12s; kept the previous container" {
		t.Fatalf("err = %v", err)
	}
	calls := f.engine.recorded()
	if !reflect.DeepEqual(calls[len(calls)-1], "remove b force=true v=true") {
		t.Fatalf("engine calls = %v, want NEW removed last", calls)
	}
	for _, call := range calls {
		if call == "stop a" {
			t.Fatal("the old container was stopped")
		}
	}
	if !strings.Contains(f.out.String(), strings.Repeat("x", 2048)) || strings.Contains(f.out.String(), strings.Repeat("x", 2049)) {
		t.Fatal("the log must carry the health output cut to 2048 bytes")
	}
}

func TestRolloutNewExited(t *testing.T) {
	f := newRollout(t, "b")
	f.health("b", EngineState{Running: false, Health: "starting"})
	_, err := f.run(RolloutOptions{})
	if err == nil || !strings.Contains(err.Error(), "kept the previous container") {
		t.Fatalf("err = %v", err)
	}
	if calls := f.engine.recorded(); calls[len(calls)-1] != "remove b force=true v=true" {
		t.Fatalf("engine calls = %v", calls)
	}
}

// The deadline passes while Compose is still scaling: the Container it
// created must be found and removed even though ctx is done by then.
func TestRolloutDeadlineStillRemovesNew(t *testing.T) {
	f := newRollout(t, "b")
	f.health("b", EngineState{Running: true, Health: "starting"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scale := f.runner.onScale
	f.runner.onScale = func(o ScaleOptions) { scale(o); cancel() }
	_, err := f.svc.RunRollout(ctx, f.target, "web", RolloutOptions{Out: f.out})
	if err == nil {
		t.Fatal("want a failure")
	}
	if calls := f.engine.recorded(); calls[len(calls)-1] != "remove b force=true v=true" {
		t.Fatalf("engine calls = %v, want NEW removed after the deadline", calls)
	}
}

// A new Container that crash-loops is "restarting", not running, when listed.
func TestRolloutRemovesNewThatIsNotRunning(t *testing.T) {
	f := newRollout(t)
	f.runner.onScale = func(ScaleOptions) {
		f.engine.mu.Lock()
		defer f.engine.mu.Unlock()
		f.engine.containers = append(f.engine.containers, EngineContainer{ID: "b", Name: "p-web-2", Service: "web"})
	}
	f.health("b", EngineState{Running: false})
	if _, err := f.run(RolloutOptions{}); err == nil {
		t.Fatal("want a failure")
	}
	if calls := f.engine.recorded(); calls[len(calls)-1] != "remove b force=true v=true" {
		t.Fatalf("engine calls = %v, want the non-running NEW removed", calls)
	}
}

// A stopped Container of the Service left by an earlier run would be reused
// by --no-recreate instead of a new one from the current file.
func TestRolloutRemovesStaleContainersBeforeScaling(t *testing.T) {
	f := newRollout(t, "b")
	f.engine.containers = append(f.engine.containers, EngineContainer{ID: "z", Name: "p-web-9", Service: "web"})
	f.health("b", EngineState{Running: true, Health: "healthy"})
	if _, err := f.run(RolloutOptions{}); err != nil {
		t.Fatal(err)
	}
	if calls := f.engine.recorded(); !reflect.DeepEqual(calls[:3], []string{"inspect a", "remove z force=true v=false", "inspect b"}) {
		t.Fatalf("engine calls = %v, want the stale container removed before the scale", calls)
	}
	if got := f.runner.scales[0].Replicas; got != 2 {
		t.Fatalf("scaled to %d, want 2", got)
	}
}

// A Service whose profile is off is not in compose config; naming it in
// `up` would start it.
func TestRolloutSkipsServiceOutsideActiveProfiles(t *testing.T) {
	f := newRollout(t, "b")
	f.engine.containers = nil
	f.runner.config = []byte(`{"services":{"db":{}}}`)
	if _, err := f.run(RolloutOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(f.runner.ups) != 0 || len(f.runner.scales) != 0 {
		t.Fatalf("ups = %+v scales = %+v, want nothing started", f.runner.ups, f.runner.scales)
	}
	if !strings.Contains(f.out.String(), "web is not in the active profiles; skipped") {
		t.Fatalf("log = %q", f.out.String())
	}
}

// compose config escapes $ as $$; labels with a literal $ are not drift.
func TestRolloutDollarLabelIsNotDrift(t *testing.T) {
	f := newRollout(t, "b")
	f.runner.config = []byte(`{"services":{"web":{"healthcheck":{"test":["CMD","true"]},"labels":{"traefik.http.middlewares.a.basicauth.users":"u:$$apr1$$x"}}}}`)
	f.health("a", EngineState{Running: true, Health: "healthy", Labels: map[string]string{"traefik.http.middlewares.a.basicauth.users": "u:$apr1$x"}})
	f.health("b", EngineState{Running: true, Health: "healthy"})
	warnings, err := f.run(RolloutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want no drift", warnings)
	}
}

func TestRolloutNoOldContainer(t *testing.T) {
	f := newRollout(t)
	f.engine.containers = nil
	warnings, err := f.run(RolloutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(warnings, []string{"web had no running container; deployed without rollout"}) {
		t.Fatalf("warnings = %v", warnings)
	}
	if len(f.runner.scales) != 0 || len(f.runner.ups) != 1 || !reflect.DeepEqual(f.runner.ups[0].Services, []string{"web"}) || f.runner.ups[0].Build || !f.runner.ups[0].NoPull {
		t.Fatalf("scales = %+v ups = %+v", f.runner.scales, f.runner.ups)
	}
}

func TestRolloutAnonymousVolume(t *testing.T) {
	f := newRollout(t, "b")
	f.health("a", EngineState{Running: true, Health: "healthy", Labels: oldLabels, Mounts: []EngineMount{
		{Destination: "/data", Anonymous: false},
		{Destination: "/var/lib/app", Anonymous: true},
	}})
	_, err := f.run(RolloutOptions{})
	want := "web has an anonymous volume at /var/lib/app (from the image's VOLUME); mount a named volume there or turn off zero-downtime deploy"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %s", err, want)
	}
	if len(f.runner.scales) != 0 {
		t.Fatal("a new container was created")
	}
}

func TestRolloutTwoOldContainers(t *testing.T) {
	f := newRollout(t, "b")
	f.engine.containers = append(f.engine.containers, EngineContainer{ID: "c", Name: "p-web-9", Service: "web", Running: true})
	f.health("b", EngineState{Running: true, Health: "healthy"})
	if _, err := f.run(RolloutOptions{}); err != nil {
		t.Fatal(err)
	}
	if f.runner.scales[0].Replicas != 3 {
		t.Fatalf("replicas = %d, want 3", f.runner.scales[0].Replicas)
	}
	calls := strings.Join(f.engine.recorded(), ",")
	for _, want := range []string{"stop a", "remove a force=false v=false", "stop c", "remove c force=false v=false"} {
		if !strings.Contains(calls, want) {
			t.Fatalf("engine calls = %s, missing %s", calls, want)
		}
	}
}

func TestRolloutCannotIdentifyNew(t *testing.T) {
	f := newRollout(t, "b", "d")
	_, err := f.run(RolloutOptions{})
	if err == nil || err.Error() != "could not identify the new container" {
		t.Fatalf("err = %v", err)
	}
	calls := strings.Join(f.engine.recorded(), ",")
	if !strings.Contains(calls, "remove b force=true v=true") || !strings.Contains(calls, "remove d force=true v=true") || strings.Contains(calls, "remove a") {
		t.Fatalf("engine calls = %s, want b and d removed, a kept", calls)
	}
}

func TestRolloutRemoveNewFails(t *testing.T) {
	f := newRollout(t, "b")
	f.health("b", EngineState{Running: true, Health: "unhealthy"})
	f.engine.removeErr = errors.New("device busy")
	_, err := f.run(RolloutOptions{})
	if err == nil || strings.Contains(err.Error(), "kept the previous container") || !strings.Contains(err.Error(), "p-web-2") {
		t.Fatalf("err = %v, want it to name the leftover container", err)
	}
}

// A drift warning raised before the health wait still reaches the Deployment
// when the new Container then fails.
func TestRolloutFailureReturnsDriftWarning(t *testing.T) {
	f := newRollout(t, "b")
	f.health("a", EngineState{Running: true, Health: "healthy", Labels: map[string]string{"traefik.enable": "false"}})
	f.health("b", EngineState{Running: true, Health: "unhealthy"})
	warnings, err := f.run(RolloutOptions{})
	if err == nil {
		t.Fatal("want a failure")
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "Traefik labels changed") {
		t.Fatalf("warnings = %v, want the drift warning", warnings)
	}
}
