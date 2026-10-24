package httpapi_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"relist-panel/internal/composes"
)

type removalPlanResponse struct {
	Composes []struct {
		ComposeID     int64    `json:"composeId"`
		Name          string   `json:"name"`
		Directory     string   `json:"directory"`
		RemoveVolumes []string `json:"removeVolumes"`
		KeepVolumes   []struct {
			Name   string `json:"name"`
			Reason string `json:"reason"`
		} `json:"keepVolumes"`
		Error string `json:"error"`
	} `json:"composes"`
}

// getRemovalPlan fetches path, requires 200 and returns the decoded plan with
// the raw body, so a test can also check arrays are never null.
func getRemovalPlan(t *testing.T, server *testServer, path string) (removalPlanResponse, string) {
	t.Helper()
	r := server.request(t, http.MethodGet, path, nil)
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", path, r.StatusCode)
	}
	var raw json.RawMessage
	decodeLifecycleResponse(t, r, &raw)
	var plan removalPlanResponse
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	return plan, string(raw)
}

func TestRemovalPlanListsVolumesAndDirectory(t *testing.T) {
	f, runner, req := deployedWithInspector(t, func(project string) []composes.VolumeCandidate {
		return []composes.VolumeCandidate{
			{Name: "mine", Exists: true, Project: project},
			{Name: "shared", Exists: true, Project: "relist-c99"},
			{Name: "gone", Exists: false},
		}
	})
	plan, _ := getRemovalPlan(t, f.server, f.path()+"/removal-plan")
	if len(plan.Composes) != 1 {
		t.Fatalf("entries = %+v, want 1", plan.Composes)
	}
	entry := plan.Composes[0]
	if entry.ComposeID != f.compose.ID || entry.Name != "Web" {
		t.Fatalf("entry = %+v", entry)
	}
	if entry.Directory != filepath.Dir(req.ComposeFilePath) {
		t.Fatalf("directory = %q, want %q", entry.Directory, filepath.Dir(req.ComposeFilePath))
	}
	if len(entry.RemoveVolumes) != 1 || entry.RemoveVolumes[0] != "mine" {
		t.Fatalf("removeVolumes = %v, want [mine]", entry.RemoveVolumes)
	}
	if len(entry.KeepVolumes) != 1 || entry.KeepVolumes[0].Name != "shared" || !strings.Contains(entry.KeepVolumes[0].Reason, "relist-c99") {
		t.Fatalf("keepVolumes = %+v, want shared with its reason", entry.KeepVolumes)
	}
	if entry.Error != "" {
		t.Fatalf("error = %q", entry.Error)
	}
	if got := methodsOf(runner.fakeComposeRunner); len(got) != 0 {
		t.Fatalf("calls = %v, reading the plan must change nothing", got)
	}
	// Reading the plan deletes nothing.
	legacyStatus(t, f.server, http.MethodGet, f.path(), http.StatusOK)
}

func TestRemovalPlanForUndeployedCompose(t *testing.T) {
	f := newFreshDeployFixture(t)
	runner := &scriptedVolumeRunner{fakeComposeRunner: f.server.runner, candidates: []composes.VolumeCandidate{{Name: "x", Exists: true, Project: "other"}}}
	f.server.useRunner(t, runner)
	runner.resetCalls()
	plan, raw := getRemovalPlan(t, f.server, f.path()+"/removal-plan")
	if len(plan.Composes) != 1 {
		t.Fatalf("entries = %+v, want 1", plan.Composes)
	}
	entry := plan.Composes[0]
	if len(entry.RemoveVolumes) != 0 || len(entry.KeepVolumes) != 0 || entry.Error != "" {
		t.Fatalf("entry = %+v, want empty plan without error", entry)
	}
	if strings.Contains(raw, "null") {
		t.Fatalf("body = %s, arrays must not be null", raw)
	}
	if got := runner.snapshotCalls(); len(got) != 0 {
		t.Fatalf("runner calls = %+v, want none", got)
	}
}

func TestRemovalPlanInspectorErrorIsPerCompose(t *testing.T) {
	f, runner, _ := deployedWithInspector(t, func(string) []composes.VolumeCandidate { return nil })
	runner.candidatesErr = errors.New("docker unreachable")
	plan, raw := getRemovalPlan(t, f.server, f.path()+"/removal-plan")
	if len(plan.Composes) != 1 || !strings.Contains(plan.Composes[0].Error, "docker unreachable") {
		t.Fatalf("plan = %+v, want the inspector error on the entry", plan.Composes)
	}
	if strings.Contains(raw, "null") {
		t.Fatalf("body = %s, arrays must not be null", raw)
	}
}

func TestRemovalPlanWithoutVolumeInspector(t *testing.T) {
	f := newFreshDeployFixture(t)
	f.deploy(t)
	plan, raw := getRemovalPlan(t, f.server, f.path()+"/removal-plan")
	if len(plan.Composes) != 1 || plan.Composes[0].Error != "volume details unavailable" || strings.Contains(raw, "null") {
		t.Fatalf("plan = %s, want volume details unavailable", raw)
	}
}

func TestProjectRemovalPlanCoversEveryCompose(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	p := createProject(t, server, "Together", "")
	e := productionEnvironment(t, server, p)
	er := postJSON(t, server, projectEnvironmentsPath(p.ID), map[string]string{"name": "staging"})
	defer er.Body.Close()
	var staging environmentResponse
	decodeLifecycleResponse(t, er, &staging)
	const header = "services:\n  web:\n    image: nginx\nvolumes:\n"
	createDeployedCompose(t, server, p, e, "first", header+"  db:\n    name: shared-db\n")
	createDeployedCompose(t, server, p, e, "second", header+"  db:\n    name: shared-db\n")
	createDeployedCompose(t, server, p, staging, "third", header+"  db:\n    name: shared-db\n")
	runner := perProjectInspector{&scriptedVolumeRunner{fakeComposeRunner: server.runner}}
	server.useRunner(t, runner)
	runner.resetCalls()

	plan, _ := getRemovalPlan(t, server, "/api/projects/"+lifecycleID(p.ID)+"/removal-plan")
	if len(plan.Composes) != 3 {
		t.Fatalf("project entries = %+v, want 3", plan.Composes)
	}
	for _, entry := range plan.Composes {
		// shared-db is declared only by Composes deleted together, so it goes.
		if len(entry.KeepVolumes) != 0 || len(entry.RemoveVolumes) != 2 {
			t.Fatalf("entry %q = %+v, want both volumes removed", entry.Name, entry)
		}
	}
	envPlan, _ := getRemovalPlan(t, server, environmentPath(p.ID, e.ID)+"/removal-plan")
	if len(envPlan.Composes) != 2 {
		t.Fatalf("environment entries = %+v, want 2", envPlan.Composes)
	}
	for _, entry := range envPlan.Composes {
		// "third" in the other Environment survives this delete and still declares shared-db.
		if len(entry.KeepVolumes) != 1 || entry.KeepVolumes[0].Name != "shared-db" || !strings.Contains(entry.KeepVolumes[0].Reason, "third") {
			t.Fatalf("entry %q keepVolumes = %+v, want shared-db kept for third", entry.Name, entry.KeepVolumes)
		}
		if len(entry.RemoveVolumes) != 1 || entry.RemoveVolumes[0] != "other-shared" {
			t.Fatalf("entry %q removeVolumes = %v, want [other-shared]", entry.Name, entry.RemoveVolumes)
		}
	}
	if got := runner.removals(); len(got) != 0 {
		t.Fatalf("removals = %v, reading the plan must not remove", got)
	}
}

func TestRemovalPlanMissingParentIs404(t *testing.T) {
	f := newFreshDeployFixture(t)
	legacyStatus(t, f.server, http.MethodGet, "/api/projects/9999/removal-plan", http.StatusNotFound)
	legacyStatus(t, f.server, http.MethodGet, environmentPath(f.project.ID, 9999)+"/removal-plan", http.StatusNotFound)
	legacyStatus(t, f.server, http.MethodGet, composePath(f.project.ID, f.env.ID, 9999)+"/removal-plan", http.StatusNotFound)
}
