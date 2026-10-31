package composes

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"relist-panel/internal/certificates"
	"relist-panel/internal/domains"
	"relist-panel/internal/zerodowntime"
)

// fakeEngine is a ContainerEngine over an in-memory list of Containers. calls
// records every call in order, e.g. "stop a" or "remove b force=true v=true".
type fakeEngine struct {
	mu         sync.Mutex
	containers []EngineContainer
	states     map[string][]EngineState // per ID; the last one repeats
	calls      []string
	removeErr  error
	stopBudget time.Duration // time left on the last Stop's ctx
	onScale    func()        // runs when the runner scales; adds NEW
}

// Like the real Engine, every call fails once its ctx is done.
func (e *fakeEngine) ProjectContainers(ctx context.Context, _ string) ([]EngineContainer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]EngineContainer(nil), e.containers...), nil
}

func (e *fakeEngine) Inspect(ctx context.Context, id string) (EngineState, error) {
	if err := ctx.Err(); err != nil {
		return EngineState{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, "inspect "+id)
	states := e.states[id]
	if len(states) == 0 {
		return EngineState{Running: true}, nil
	}
	state := states[0]
	if len(states) > 1 {
		e.states[id] = states[1:]
	}
	return state, nil
}

func (e *fakeEngine) Stop(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, "stop "+id)
	if deadline, ok := ctx.Deadline(); ok {
		e.stopBudget = time.Until(deadline)
	}
	return nil
}

func (e *fakeEngine) Remove(ctx context.Context, id string, force, volumes bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, "remove "+id+" force="+boolString(force)+" v="+boolString(volumes))
	return e.removeErr
}

func (e *fakeEngine) recorded() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.calls...)
}

func boolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func seedDomain(t *testing.T, db *sql.DB, service string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO domains (compose_id, host, service, port, created_at, updated_at) VALUES (1, ?, ?, 80, 0, 0)`, service+".example.com", service); err != nil {
		t.Fatal(err)
	}
}

func switchOn(t *testing.T, db *sql.DB, services ...string) {
	t.Helper()
	store := zerodowntime.NewStore(db)
	for _, svc := range services {
		if err := store.Set(context.Background(), 1, svc, true, time.Unix(0, 0)); err != nil {
			t.Fatal(err)
		}
	}
}

const zdYAML = "services:\n  worker: {image: w}\n  web: {image: nginx}\n  db: {image: postgres}\n"

func TestCaptureRecordsZeroDowntime(t *testing.T) {
	svc, _, target, db := newDeployService(t, zdYAML)
	seedDomain(t, db, "web")
	switchOn(t, db, "web", "worker")
	captured, err := svc.Capture(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if got := captured.Snapshot.ZeroDowntime; !reflect.DeepEqual(got, []string{"web"}) {
		t.Fatalf("ZeroDowntime = %v, want [web] (worker has no Domain)", got)
	}
	if !strings.Contains(captured.Snapshot.YAML, "retry.attempts") || !strings.Contains(captured.Snapshot.YAML, "relist-fastdial@file") {
		t.Fatalf("snapshot YAML lacks the zero-downtime labels:\n%s", captured.Snapshot.YAML)
	}
}

func TestCaptureIgnoresRowWithoutDomain(t *testing.T) {
	svc, _, target, db := newDeployService(t, zdYAML)
	seedDomain(t, db, "web")
	switchOn(t, db, "old")
	captured, err := svc.Capture(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if got := captured.Snapshot.ZeroDowntime; len(got) != 0 {
		t.Fatalf("ZeroDowntime = %v, want none", got)
	}
	if strings.Contains(captured.Snapshot.YAML, "retry.attempts") {
		t.Fatalf("labels injected for a switch with no Domain:\n%s", captured.Snapshot.YAML)
	}
}

func TestCaptureKeepsYAMLOrder(t *testing.T) {
	svc, _, target, db := newDeployService(t, zdYAML)
	seedDomain(t, db, "web")
	seedDomain(t, db, "worker")
	switchOn(t, db, "web", "worker")
	captured, err := svc.Capture(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if got := captured.Snapshot.ZeroDowntime; !reflect.DeepEqual(got, []string{"worker", "web"}) {
		t.Fatalf("ZeroDowntime = %v, want YAML order [worker web]", got)
	}
}

// countingSwitches counts reads of the switch set.
type countingSwitches struct {
	*zerodowntime.Store
	reads int
}

func (c *countingSwitches) Enabled(ctx context.Context, composeID int64) ([]string, error) {
	c.reads++
	return c.Store.Enabled(ctx, composeID)
}

func TestCaptureReadsSwitchesOnce(t *testing.T) {
	svc, _, target, db := newDeployService(t, zdYAML)
	seedDomain(t, db, "web")
	switchOn(t, db, "web")
	counting := &countingSwitches{Store: zerodowntime.NewStore(db)}
	svc.switches = counting
	if _, err := svc.Capture(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	if counting.reads != 1 {
		t.Fatalf("switches read %d times per Capture, want 1", counting.reads)
	}
}

func TestZeroDowntimeProblemsUsesConfig(t *testing.T) {
	svc, runner, target, db := newDeployService(t, zdYAML)
	seedDomain(t, db, "web")
	switchOn(t, db, "web")
	captured, err := svc.Capture(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	runner.config = []byte(`{"services":{"web":{"image":"nginx"}}}`)
	var staged []string
	runner.onConfig = func(req RunRequest) {
		for _, path := range []string{req.ComposeFilePath, req.EnvFilePath} {
			if _, err := os.Stat(path); err != nil {
				t.Errorf("staged file missing during config: %v", err)
			}
			if filepath.Dir(path) != ComposeDir(target.Request) || !strings.HasPrefix(filepath.Base(path), ".zd-check-") {
				t.Errorf("staged at %s, want a .zd-check-* file in the Compose dir", path)
			}
			staged = append(staged, path)
		}
	}
	problems, err := svc.ZeroDowntimeProblems(context.Background(), captured)
	if err != nil {
		t.Fatal(err)
	}
	if want := []zerodowntime.Problem{{Service: "web", Reasons: []string{"has no healthcheck"}}}; !reflect.DeepEqual(problems, want) {
		t.Fatalf("problems = %+v, want %+v", problems, want)
	}
	for _, path := range staged {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s left behind: %v", path, err)
		}
	}
}

func TestStageCheckLeavesBuildFilesAlone(t *testing.T) {
	svc, _, target, db := newDeployService(t, zdYAML)
	seedDomain(t, db, "web")
	switchOn(t, db, "web")
	captured, err := svc.Capture(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	dir := ComposeDir(target.Request)
	if err := os.MkdirAll(filepath.Join(dir, ".build-sets"), 0o700); err != nil {
		t.Fatal(err)
	}
	build := map[string]string{
		filepath.Join(dir, ".build-compose.yml"):   "build yaml",
		filepath.Join(dir, ".build.env"):           "build env",
		filepath.Join(dir, ".build-sets", "x.env"): "build set",
	}
	for path, content := range build {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.ZeroDowntimeProblems(context.Background(), captured); err != nil {
		t.Fatal(err)
	}
	for path, want := range build {
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q untouched", path, got, err, want)
		}
	}
}

func TestRunDeployExcludeRunsSubset(t *testing.T) {
	svc, runner, target, db := newDeployService(t, zdYAML+"  tool:\n    image: t\n    profiles: [tools]\n")
	runner.config = []byte(`{"services":{"worker":{},"web":{},"db":{}}}`)
	if _, err := svc.RunDeploy(context.Background(), target, DeployOptions{Exclude: []string{"web"}}); err != nil {
		t.Fatal(err)
	}
	if len(runner.ups) != 1 || !reflect.DeepEqual(runner.ups[0].Services, []string{"db", "worker"}) {
		t.Fatalf("ups = %+v, want one up of [db worker] (no profiled tool, no web)", runner.ups)
	}
	var deployedAt sql.NullInt64
	if err := db.QueryRow(`SELECT deployed_at FROM composes WHERE id = 1`).Scan(&deployedAt); err != nil || deployedAt.Valid {
		t.Fatalf("deployed_at = %v, %v; want unset until the rollouts finish", deployedAt, err)
	}
}

func TestRunDeployExcludeAllRemovesOrphansViaEngine(t *testing.T) {
	svc, runner, target, _ := newDeployService(t, "services:\n  web: {image: nginx}\n")
	runner.config = []byte(`{"services":{"web":{}}}`)
	engine := &fakeEngine{containers: []EngineContainer{
		{ID: "w", Name: "p-web-1", Service: "web", Running: true},
		{ID: "o", Name: "p-old-1", Service: "old", Running: true},
	}}
	svc.WithEngine(engine)
	if _, err := svc.RunDeploy(context.Background(), target, DeployOptions{Exclude: []string{"web"}}); err != nil {
		t.Fatal(err)
	}
	if len(runner.ups) != 0 {
		t.Fatalf("ups = %+v, want none", runner.ups)
	}
	if want := []string{"stop o", "remove o force=false v=false"}; !reflect.DeepEqual(engine.recorded(), want) {
		t.Fatalf("engine calls = %v, want %v", engine.recorded(), want)
	}
	// An orphan's stop_grace_period may run past 30s; it gets the same
	// budget as an old Container in a rollout.
	if engine.stopBudget < stopTimeout-time.Minute {
		t.Fatalf("orphan stop had %s, want about %s", engine.stopBudget, stopTimeout)
	}
}

func TestRunDeployWritesTransportFile(t *testing.T) {
	svc, runner, target, db := newDeployService(t, zdYAML)
	seedDomain(t, db, "web")
	switchOn(t, db, "web")
	captured, err := svc.Capture(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RunDeploy(context.Background(), captured, DeployOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(certificates.DynamicDir(svc.dataDir), domains.TransportFileName)); err != nil {
		t.Fatalf("transport file: %v", err)
	}

	// A dynamic dir that can't be written fails the deploy before Docker.
	dynamic := certificates.DynamicDir(svc.dataDir)
	if err := os.RemoveAll(dynamic); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dynamic, []byte("not a dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner.calls = nil
	_, err = svc.RunDeploy(context.Background(), captured, DeployOptions{})
	var failure *DeployFailure
	if !errors.As(err, &failure) || failure.Error() != "could not write Traefik transport file" {
		t.Fatalf("err = %v, want the transport failure", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner calls = %v, want none", runner.calls)
	}
}

// TestStageCheckResolvesRelativeEnvFile runs the real `docker compose config`
// on staged check files: a relative env_file must resolve against the Compose
// dir, as it does on deploy.
func TestStageCheckResolvesRelativeEnvFile(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not installed")
	}
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skip("docker compose not available")
	}
	svc, _, target, _ := newDeployService(t, "services:\n  web:\n    image: nginx\n    env_file: ./app.env\n")
	captured, err := svc.Capture(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	dir := ComposeDir(target.Request)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.env"), []byte("A=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	req, cleanup, err := stageCheck(captured)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	out, err := exec.Command("docker", "compose", "-p", req.ProjectName, "-f", req.ComposeFilePath, "--env-file", req.EnvFilePath, "config", "--format", "json").CombinedOutput()
	if err != nil {
		t.Fatalf("compose config on staged files: %v\n%s", err, out)
	}
}

func TestZeroDowntimeProblemsConfigFailureIsInvalid(t *testing.T) {
	svc, runner, target, db := newDeployService(t, zdYAML)
	seedDomain(t, db, "web")
	switchOn(t, db, "web")
	captured, err := svc.Capture(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	runner.configErr = errors.New("docker compose config: exit status 1: required variable X is missing a value\nmore")
	_, err = svc.ZeroDowntimeProblems(context.Background(), captured)
	var failure *DeployFailure
	if !errors.As(err, &failure) || failure.Kind != FailureInvalid || failure.Message != "compose config failed: required variable X is missing a value" {
		t.Fatalf("err = %#v", err)
	}
}

// dockerexec wraps a failed command as "docker <argv>: exit status N: <stderr>",
// and Compose prints its warnings before the error. The operator needs the error.
func TestFirstErrorLineSkipsArgvAndWarnings(t *testing.T) {
	err := errors.New("docker compose -p relist-c1 -f /data/p/e/c/.zd-check-0a1b.yml --env-file /data/p/e/c/.zd-check-0a1b.env config --format json: exit status 1: " +
		"time=\"2026-10-31T10:00:00Z\" level=warning msg=\"The \\\"Y\\\" variable is not set. Defaulting to a blank string.\"\n" +
		"required variable X is missing a value: X is required")
	if got, want := firstErrorLine(err), "required variable X is missing a value: X is required"; got != want {
		t.Fatalf("firstErrorLine = %q, want %q", got, want)
	}
	if got := firstErrorLine(errors.New("plain failure\nmore")); got != "plain failure" {
		t.Fatalf("firstErrorLine(plain) = %q", got)
	}
}

// A Compose that was never deployed has no directory; checking it must not
// leave one behind.
func TestStageCheckRemovesTheDirectoryItCreated(t *testing.T) {
	svc, _, target, db := newDeployService(t, zdYAML)
	seedDomain(t, db, "web")
	switchOn(t, db, "web")
	captured, err := svc.Capture(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	dir := ComposeDir(target.Request)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("compose dir exists before the check: %v", err)
	}
	if _, err := svc.ZeroDowntimeProblems(context.Background(), captured); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("compose dir left after the check: %v", err)
	}
}

// Staged copies hold env values; a panel that died mid-check or mid-build
// leaves them, so startup removes them and nothing else.
func TestCleanStaging(t *testing.T) {
	dir := t.TempDir()
	for _, path := range []string{".zd-check-ab.yml", ".zd-check-ab.env", ".zd-check-ab-sets/1.env", ".build-compose.yml", ".build.env", ".build-sets/x.env", "docker-compose.yml", ".env", "sets/1.env"} {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := CleanStaging(dir); err != nil {
		t.Fatal(err)
	}
	var left []string
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dir, path)
			left = append(left, rel)
		}
		return nil
	})
	if want := []string{".env", "docker-compose.yml", "sets/1.env"}; !reflect.DeepEqual(left, want) {
		t.Fatalf("left = %v, want %v", left, want)
	}
	if err := CleanStaging(filepath.Join(dir, "missing")); err != nil {
		t.Fatalf("CleanStaging(missing dir) = %v", err)
	}
}
