package httpapi_test

import (
	"context"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"relist-panel/internal/composes"
)

// scriptedVolumeRunner adds the optional VolumeInspector capability to the
// fake runner: it reports the scripted candidates and records which volumes it
// was asked to remove.
type scriptedVolumeRunner struct {
	*fakeComposeRunner
	vmu        sync.Mutex
	candidates []composes.VolumeCandidate
	removeArgs [][]string
}

func (r *scriptedVolumeRunner) CandidateVolumes(context.Context, composes.RunRequest) ([]composes.VolumeCandidate, error) {
	r.vmu.Lock()
	defer r.vmu.Unlock()
	return slices.Clone(r.candidates), nil
}

func (r *scriptedVolumeRunner) DownAndRemoveVolumes(_ context.Context, req composes.RunRequest, names []string) ([]string, error) {
	r.vmu.Lock()
	r.removeArgs = append(r.removeArgs, slices.Clone(names))
	r.vmu.Unlock()
	r.recordCall(fakeComposeRun{method: "down-and-remove-volumes", request: req})
	return slices.Clone(names), nil
}

func (r *scriptedVolumeRunner) removals() [][]string {
	r.vmu.Lock()
	defer r.vmu.Unlock()
	return slices.Clone(r.removeArgs)
}

func methodsOf(r *fakeComposeRunner) []string {
	var methods []string
	for _, call := range r.snapshotCalls() {
		if call.method != "status" {
			methods = append(methods, call.method)
		}
	}
	return methods
}

// deployedWithInspector deploys a Compose named "Web" and swaps in an
// inspecting runner that reports the candidates candidatesFor returns for the
// Compose's project name.
func deployedWithInspector(t *testing.T, candidatesFor func(project string) []composes.VolumeCandidate) (freshDeployFixture, *scriptedVolumeRunner, composes.RunRequest) {
	t.Helper()
	f := newFreshDeployFixture(t)
	f.deploy(t)
	req := f.deployedRequest(t)
	runner := &scriptedVolumeRunner{fakeComposeRunner: f.server.runner, candidates: candidatesFor(req.ProjectName)}
	f.server.useRunner(t, runner)
	runner.resetCalls()
	return f, runner, req
}

func (f freshDeployFixture) deleteCompose(t *testing.T) {
	t.Helper()
	legacyStatus(t, f.server, http.MethodDelete, f.path(), http.StatusNoContent)
	legacyStatus(t, f.server, http.MethodGet, f.path(), http.StatusNotFound)
}

func TestDeleteKeepsVolumeOwnedByAnotherProject(t *testing.T) {
	f, runner, req := deployedWithInspector(t, func(project string) []composes.VolumeCandidate {
		return []composes.VolumeCandidate{
			{Name: "shared", Exists: true, Project: "relist-c99"},
			{Name: "mine", Exists: true, Project: project},
			{Name: "gone", Exists: false},
		}
	})
	f.deleteCompose(t)
	if got := runner.removals(); len(got) != 1 || !slices.Equal(got[0], []string{"mine"}) {
		t.Fatalf("DownAndRemoveVolumes args = %v, want [[mine]]", got)
	}
	if methods := methodsOf(runner.fakeComposeRunner); slices.Contains(methods, "down-with-volumes") {
		t.Fatalf("calls = %v, down -v must not run while a volume is kept", methods)
	}
	if _, err := os.Stat(req.ComposeFilePath); !os.IsNotExist(err) {
		t.Fatalf("compose file still on disk: %v", err)
	}
}

func TestDeleteKeepsVolumeDeclaredBySiblingCompose(t *testing.T) {
	f := newFreshDeployFixture(t)
	f.deploy(t)
	sibling := postJSON(t, f.server, composesPath(f.project.ID, f.env.ID), map[string]string{
		"name":        "Sibling",
		"yamlContent": "services:\n  web:\n    image: nginx\nvolumes:\n  db:\n    name: shared-db\n",
	})
	sibling.Body.Close()
	if sibling.StatusCode != http.StatusCreated {
		t.Fatalf("create sibling = %d", sibling.StatusCode)
	}
	req := f.deployedRequest(t)
	runner := &scriptedVolumeRunner{fakeComposeRunner: f.server.runner, candidates: []composes.VolumeCandidate{
		{Name: "shared-db", Exists: true, Project: req.ProjectName},
		{Name: "own", Exists: true, Project: req.ProjectName},
	}}
	f.server.useRunner(t, runner)
	runner.resetCalls()
	f.deleteCompose(t)
	if got := runner.removals(); len(got) != 1 || !slices.Equal(got[0], []string{"own"}) {
		t.Fatalf("DownAndRemoveVolumes args = %v, want [[own]]", got)
	}
}

func TestDeleteRemovesOwnedVolumesWithDownV(t *testing.T) {
	f, runner, _ := deployedWithInspector(t, func(project string) []composes.VolumeCandidate {
		return []composes.VolumeCandidate{
			{Name: "data", Exists: true, Project: project},
			{Name: "unlabelled", Exists: true},
			{Name: "gone", Exists: false, Project: "relist-c99"},
		}
	})
	f.deleteCompose(t)
	if got := methodsOf(runner.fakeComposeRunner); !slices.Equal(got, []string{"down-with-volumes"}) {
		t.Fatalf("calls = %v, want only [down-with-volumes]", got)
	}
	if got := runner.removals(); len(got) != 0 {
		t.Fatalf("DownAndRemoveVolumes called: %v", got)
	}
}

func TestDeleteOfUndeployedComposeCallsNoDocker(t *testing.T) {
	f := newFreshDeployFixture(t)
	runner := &scriptedVolumeRunner{fakeComposeRunner: f.server.runner, candidates: []composes.VolumeCandidate{{Name: "x", Exists: true, Project: "other"}}}
	f.server.useRunner(t, runner)
	runner.resetCalls()
	f.deleteCompose(t)
	if got := runner.snapshotCalls(); len(got) != 0 {
		t.Fatalf("runner calls = %+v, want none for a Compose that never deployed", got)
	}
}

func TestFreshDeployLogsKeptVolumes(t *testing.T) {
	f, runner, _ := deployedWithInspector(t, func(project string) []composes.VolumeCandidate {
		return []composes.VolumeCandidate{
			{Name: "shared", Exists: true, Project: "relist-c99"},
			{Name: "mine", Exists: true, Project: project},
		}
	})
	d := f.server.deployAndWait(t, f.path(), freshBody("Web"))
	if d.Status != "succeeded" {
		t.Fatalf("deployment = %s (%q), want succeeded", d.Status, d.Reason)
	}
	if got := runner.removals(); len(got) != 1 || !slices.Equal(got[0], []string{"mine"}) {
		t.Fatalf("DownAndRemoveVolumes args = %v, want [[mine]]", got)
	}
	fixture := deploymentsFixture{server: f.server, project: f.project, env: f.env, compose: f.compose}
	log := fixture.log(t, d.Number, 0).Text
	if want := "kept volume shared: owned by another Compose project (relist-c99)"; !strings.Contains(log, want) {
		t.Fatalf("deployment log = %q, want it to contain %q", log, want)
	}
	if strings.Contains(log, "kept volume mine") {
		t.Fatalf("deployment log reports an owned volume as kept: %q", log)
	}
}

// perProjectInspector reports, for every Compose it is asked about, two
// volumes owned by that Compose's own project.
type perProjectInspector struct{ *scriptedVolumeRunner }

func (r perProjectInspector) CandidateVolumes(_ context.Context, req composes.RunRequest) ([]composes.VolumeCandidate, error) {
	return []composes.VolumeCandidate{
		{Name: "shared-db", Exists: true, Project: req.ProjectName},
		{Name: "other-shared", Exists: true, Project: req.ProjectName},
	}, nil
}

func createDeployedCompose(t *testing.T, server *testServer, p projectResponse, e environmentResponse, name, yaml string) composeResponse {
	t.Helper()
	response := postJSON(t, server, composesPath(p.ID, e.ID), map[string]string{"name": name, "yamlContent": yaml})
	defer response.Body.Close()
	var c composeResponse
	decodeLifecycleResponse(t, response, &c)
	server.deployOK(t, composePath(p.ID, e.ID, c.ID), nil)
	return c
}

func TestProjectTeardownRemovesVolumeOnlyItsOwnComposesDeclare(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	p := createProject(t, server, "Together", "")
	e := productionEnvironment(t, server, p)
	const header = "services:\n  web:\n    image: nginx\nvolumes:\n"
	createDeployedCompose(t, server, p, e, "first", header+"  db:\n    name: shared-db\n  o:\n    name: other-shared\n")
	createDeployedCompose(t, server, p, e, "second", header+"  db:\n    name: shared-db\n")
	outside := createProject(t, server, "Outside", "")
	outsideEnv := productionEnvironment(t, server, outside)
	response := postJSON(t, server, composesPath(outside.ID, outsideEnv.ID), map[string]string{"name": "third", "yamlContent": header + "  o:\n    name: other-shared\n"})
	response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create outside compose = %d", response.StatusCode)
	}

	runner := perProjectInspector{&scriptedVolumeRunner{fakeComposeRunner: server.runner}}
	server.useRunner(t, runner)
	runner.resetCalls()
	legacyStatus(t, server, http.MethodDelete, "/api/projects/"+lifecycleID(p.ID), http.StatusNoContent)

	got := runner.removals()
	if len(got) != 2 || !slices.Equal(got[0], []string{"shared-db"}) || !slices.Equal(got[1], []string{"shared-db"}) {
		t.Fatalf("DownAndRemoveVolumes args = %v, want shared-db removed for both children and other-shared kept", got)
	}
}
