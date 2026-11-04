package deployments

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"relist-panel/internal/composes"
)

// fakePool stands in for builders.Service: it records acquire:<id> and
// release on the harness's recorder.
type fakePool struct {
	rec    *recorder
	err    error
	hint   string
	ids    []int64
	hinted []error
}

func (p *fakePool) Acquire(_ context.Context, id int64, out io.Writer) (string, func(), error) {
	p.ids = append(p.ids, id)
	p.rec.add("acquire:%d", id)
	if p.err != nil {
		return "", nil, p.err
	}
	fmt.Fprintf(out, "Builder: pool-%d\n", id)
	name := ""
	if id != 0 {
		name = fmt.Sprintf("relist-builder-%d", id)
	}
	return name, func() { p.rec.add("release") }, nil
}

func (p *fakePool) FailureHint(_ context.Context, id int64, err error) string {
	p.hinted = append(p.hinted, err)
	return p.hint
}

func withPool(h *harness) *fakePool {
	h.pool = &fakePool{rec: h.rec}
	return h.pool
}

func readLog(t *testing.T, h *harness, id int64, n int) string {
	t.Helper()
	log, err := os.ReadFile(LogPath(h.comp.dirs[id], n))
	if err != nil {
		t.Fatal(err)
	}
	return string(log)
}

func TestBuildRunsOnSnapshotBuilder(t *testing.T) {
	h := newHarness(t)
	pool := withPool(h)
	h.gitSource(h.a)
	h.comp.builderID = 7
	d := h.enqueue(h.a, ModeBuild)
	h.start()
	if got := h.waitSettled(h.a, d.Number); got.Status != StatusSucceeded {
		t.Fatalf("status = %s %q", got.Status, got.Reason)
	}
	for _, r := range h.build.reqs {
		if r.Builder != "relist-builder-7" {
			t.Fatalf("build request Builder = %q", r.Builder)
		}
	}
	log := readLog(t, h, h.a, d.Number)
	if i, j := strings.Index(log, "Builder: pool-7\n"), strings.Index(log, "==> build:web"); i < 0 || j < i {
		t.Fatalf("log does not name the Builder before the build:\n%s", log)
	}
	events := h.rec.all()
	if h.rec.count("release") != 1 || slices.Index(events, "release") < slices.Index(events, fmt.Sprintf("deploy:%d", h.a)) {
		t.Fatalf("events = %v, want one release after the deploy", events)
	}
	if !slices.Equal(pool.ids, []int64{7}) {
		t.Fatalf("acquired = %v", pool.ids)
	}
}

func TestUpBuildRunsOnSnapshotBuilder(t *testing.T) {
	h := newHarness(t)
	withPool(h)
	h.comp.builderID = 7
	d := h.enqueue(h.a, ModeDeploy)
	h.start()
	if got := h.waitSettled(h.a, d.Number); got.Status != StatusSucceeded {
		t.Fatalf("status = %s %q", got.Status, got.Reason)
	}
	if len(h.comp.targets) != 1 || h.comp.targets[0].Request.Builder != "relist-builder-7" || !h.comp.deployed[0].Build {
		t.Fatalf("deploys = %+v / %+v", h.comp.targets, h.comp.deployed)
	}
}

func TestNoBuildServicesNeverAcquires(t *testing.T) {
	h := newHarness(t)
	pool := withPool(h)
	h.comp.services = nil
	h.comp.builderID = 7
	d := h.enqueue(h.a, ModeDeploy)
	h.start()
	if got := h.waitSettled(h.a, d.Number); got.Status != StatusSucceeded {
		t.Fatalf("status = %s %q", got.Status, got.Reason)
	}
	if len(pool.ids) != 0 || h.comp.targets[0].Request.Builder != "" {
		t.Fatalf("acquired = %v, Builder %q", pool.ids, h.comp.targets[0].Request.Builder)
	}
}

func TestOldSnapshotBuildsOnDockerDefault(t *testing.T) {
	h := newHarness(t)
	pool := withPool(h)
	h.gitSource(h.a)
	h.comp.builderID = 7
	d := h.enqueue(h.a, ModeBuild)
	// A Deployment queued before the feature has no BuilderID in its snapshot.
	res, err := h.db.Exec(`UPDATE deployment_snapshots SET snapshot = json_remove(snapshot, '$.Files.BuilderID')`)
	if n, _ := res.RowsAffected(); err != nil || n != 1 {
		t.Fatalf("strip BuilderID: %d rows, %v", n, err)
	}
	h.start()
	if got := h.waitSettled(h.a, d.Number); got.Status != StatusSucceeded {
		t.Fatalf("status = %s %q", got.Status, got.Reason)
	}
	if !slices.Equal(pool.ids, []int64{0}) || h.build.reqs[0].Builder != "" {
		t.Fatalf("acquired = %v, Builder %q", pool.ids, h.build.reqs[0].Builder)
	}
}

func TestAcquireFailureFailsDeployment(t *testing.T) {
	h := newHarness(t)
	pool := withPool(h)
	pool.err = errors.New("Could not start Builder large: boom")
	h.gitSource(h.a)
	h.comp.builderID = 7
	d := h.enqueue(h.a, ModeBuild)
	h.start()
	got := h.waitSettled(h.a, d.Number)
	if got.Status != StatusFailed || got.Reason != "Could not start Builder large: boom" {
		t.Fatalf("status = %s %q", got.Status, got.Reason)
	}
	if h.rec.count("build:") != 0 || h.rec.count("deploy:") != 0 {
		t.Fatalf("events = %v", h.rec.all())
	}
}

func TestBuildFailureAppendsHint(t *testing.T) {
	h := newHarness(t)
	pool := withPool(h)
	pool.hint = "The build ran out of memory on Builder large (4 GB)."
	h.gitSource(h.a)
	h.comp.builderID = 7
	h.build.onBuild = func(context.Context, composes.BuildOptions) error {
		return errors.New("exit code: 137")
	}
	d := h.enqueue(h.a, ModeBuild)
	h.start()
	if got := h.waitSettled(h.a, d.Number); got.Status != StatusFailed {
		t.Fatalf("status = %s", got.Status)
	}
	if log := readLog(t, h, h.a, d.Number); !strings.Contains(log, pool.hint+"\n") {
		t.Fatalf("log lacks the hint:\n%s", log)
	}
	if len(pool.hinted) != 1 || pool.hinted[0].Error() != "exit code: 137" {
		t.Fatalf("hinted = %v", pool.hinted)
	}
}

func TestUpBuildFailureAppendsHint(t *testing.T) {
	h := newHarness(t)
	pool := withPool(h)
	pool.hint = "hint for up"
	h.comp.builderID = 7
	h.comp.onDeploy = func(context.Context, int64, composes.DeployOptions) ([]string, error) {
		return nil, errors.New("deploy failed: cannot allocate memory")
	}
	d := h.enqueue(h.a, ModeDeploy)
	h.start()
	h.waitSettled(h.a, d.Number)
	if log := readLog(t, h, h.a, d.Number); !strings.Contains(log, "hint for up\n") {
		t.Fatalf("log lacks the hint:\n%s", log)
	}
}
