package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
	"relist-panel/internal/composes"
	"relist-panel/internal/database"
)

// Direct insertion is an old database fixture; all behavioral assertions use
// the HTTP seam and the Docker port.
func legacyCompose(t *testing.T, server *testServer, environmentID int64, projectSlug, envSlug, name string) int64 {
	t.Helper()
	result, err := server.db.Exec(`INSERT INTO composes (environment_id,name,project_slug,env_slug,yaml_content,created_at,updated_at) VALUES (?,?,?,?,?,1,1)`, environmentID, name, projectSlug, envSlug, "services:\n  web:\n    image: nginx\n")
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func legacyStatus(t *testing.T, server *testServer, method, path string, want int) {
	t.Helper()
	r := server.request(t, method, path, nil)
	defer r.Body.Close()
	if r.StatusCode != want {
		t.Fatalf("%s %s = %d, want %d", method, path, r.StatusCode, want)
	}
}

func TestLegacyUpgradeKeepsDeploymentAndReusedParentNameGetsIndependentIdentity(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	p := createProject(t, server, "alpha", "")
	e := productionEnvironment(t, server, p)
	// Build a pre-upgrade row with no identity_version column, then run the
	// same migration and backfill that startup runs against existing data.
	if err := goose.DownTo(server.db, ".", 10); err != nil {
		t.Fatal(err)
	}
	id := legacyCompose(t, server, e.ID, "", "", "web")
	oldPath := filepath.Join(server.dataDir, "alpha", "production", "web", "docker-compose.yml")
	if err := os.MkdirAll(filepath.Dir(oldPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldPath, []byte("services:\n  web:\n    image: legacy-image\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(server.db); err != nil {
		t.Fatal(err)
	}
	if err := composes.BackfillLegacyIdentities(context.Background(), server.db); err != nil {
		t.Fatal(err)
	}
	if oldContent, err := os.ReadFile(oldPath); err != nil || string(oldContent) != "services:\n  web:\n    image: legacy-image\n" {
		t.Fatalf("upgrade altered legacy deployed files: %q, %v", oldContent, err)
	}
	legacyStatus(t, server, http.MethodPost, composeStopPath(p.ID, e.ID, id), http.StatusOK)
	if req := server.runner.snapshotCalls()[0].request; req.ProjectName != "alpha-production-web" || req.ComposeFilePath != oldPath {
		t.Fatalf("upgraded stack no longer addressed its old deployment: %+v", req)
	}
	server.runner.resetCalls()
	server.deployOK(t, composePath(p.ID, e.ID, id), nil)
	old := server.runner.snapshotCalls()[0].request
	if old.ProjectName != "alpha-production-web" || old.ComposeFilePath != oldPath {
		t.Fatalf("upgraded legacy deployment moved: %+v", old)
	}
	rename := server.request(t, http.MethodPut, "/api/projects/"+lifecycleID(p.ID), map[string]string{"name": "beta"})
	rename.Body.Close()
	if rename.StatusCode != http.StatusOK {
		t.Fatalf("rename: %d", rename.StatusCode)
	}
	if err := composes.BackfillLegacyIdentities(context.Background(), server.db); err != nil {
		t.Fatal(err)
	}
	server.runner.resetCalls()
	legacyStatus(t, server, http.MethodPost, composeStopPath(p.ID, e.ID, id), http.StatusOK)
	if stopped := server.runner.snapshotCalls()[0].request; stopped != old {
		t.Fatalf("legacy identity changed after rename/backfill: %+v, want %+v", stopped, old)
	}
	freshParent := createProject(t, server, "alpha", "")
	freshEnv := productionEnvironment(t, server, freshParent)
	server.runner.resetCalls()
	fresh := deployFixture(t, server, freshParent.ID, freshEnv.ID, "web")
	newReq := server.runner.snapshotCalls()[0].request
	if newReq.ProjectName != "relist-c"+lifecycleID(fresh.ID) || newReq.ComposeFilePath != filepath.Join(server.dataDir, ".composes", lifecycleID(fresh.ID), "docker-compose.yml") {
		t.Fatalf("fresh deployment did not get independent ID identity: %+v", newReq)
	}
	if _, err := os.Stat(old.ComposeFilePath); err != nil {
		t.Fatalf("creating replacement removed legacy files: %v", err)
	}
}

func TestAmbiguousLegacyIdentitiesRefuseLifecycleAndParentDeletionBeforeDocker(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	p := createProject(t, server, "Legacy", "")
	e := productionEnvironment(t, server, p)
	one := legacyCompose(t, server, e.ID, "a-b", "c", "web")
	two := legacyCompose(t, server, e.ID, "a", "b-c", "other")
	// Separate parents historically allowed matching names; make the second
	// old row match the first Docker project while retaining a distinct path.
	if _, err := server.db.Exec(`UPDATE composes SET name='c-web', env_slug='b' WHERE id=?`, two); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{one, two} {
		deploy := server.request(t, http.MethodPost, composeDeploymentsPath(p.ID, e.ID, id), map[string]any{"mode": "deploy"})
		deploy.Body.Close()
		if deploy.StatusCode != http.StatusConflict {
			t.Fatalf("deploy of ambiguous legacy compose %d = %d, want %d", id, deploy.StatusCode, http.StatusConflict)
		}
		legacyStatus(t, server, http.MethodPost, composeStopPath(p.ID, e.ID, id), http.StatusConflict)
		legacyStatus(t, server, http.MethodDelete, composePath(p.ID, e.ID, id), http.StatusConflict)
	}
	legacyStatus(t, server, http.MethodDelete, environmentPath(p.ID, e.ID), http.StatusBadGateway)
	legacyStatus(t, server, http.MethodDelete, "/api/projects/"+lifecycleID(p.ID), http.StatusBadGateway)
	if len(server.runner.snapshotCalls()) != 0 {
		t.Fatalf("ambiguous legacy identity reached Docker: %+v", server.runner.snapshotCalls())
	}
	legacyStatus(t, server, http.MethodGet, "/api/projects/"+lifecycleID(p.ID), http.StatusOK)
	legacyStatus(t, server, http.MethodGet, projectEnvironmentsPath(p.ID), http.StatusOK)
	legacyStatus(t, server, http.MethodGet, composePath(p.ID, e.ID, one), http.StatusOK)
	legacyStatus(t, server, http.MethodGet, composePath(p.ID, e.ID, two), http.StatusOK)
}

func TestParentTeardownFailurePreservesMetadataForRetry(t *testing.T) {
	for _, kind := range []string{"project", "environment"} {
		t.Run(kind, func(t *testing.T) {
			server := newTestServer(t)
			setupAndLogin(t, server)
			p := createProject(t, server, "Retry", "")
			e := productionEnvironment(t, server, p)
			c := deployFixture(t, server, p.ID, e.ID, "web")
			parentPath := environmentPath(p.ID, e.ID)
			if kind == "project" {
				parentPath = "/api/projects/" + lifecycleID(p.ID)
			}
			server.runner.setDownWithVolumesErr(errors.New("Docker unavailable"))
			legacyStatus(t, server, http.MethodDelete, parentPath, http.StatusBadGateway)
			legacyStatus(t, server, http.MethodGet, "/api/projects/"+lifecycleID(p.ID), http.StatusOK)
			legacyStatus(t, server, http.MethodGet, composePath(p.ID, e.ID, c.ID), http.StatusOK)
			server.runner.setDownWithVolumesErr(nil)
			legacyStatus(t, server, http.MethodDelete, parentPath, http.StatusNoContent)
			legacyStatus(t, server, http.MethodGet, composePath(p.ID, e.ID, c.ID), http.StatusNotFound)
		})
	}
}

type partialTeardownRunner struct {
	*fakeComposeRunner
	failedProject string
	fail          bool
}

func (r *partialTeardownRunner) DownWithVolumes(ctx context.Context, req composes.RunRequest) error {
	if r.fail && req.ProjectName == r.failedProject {
		r.recordCall(fakeComposeRun{method: "down-with-volumes", request: req})
		return errors.New("second stack unavailable")
	}
	return r.fakeComposeRunner.DownWithVolumes(ctx, req)
}

func TestPartialParentTeardownCanRetryWithoutRepeatingCompletedChild(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	p := createProject(t, server, "Partial", "")
	e := productionEnvironment(t, server, p)
	one := deployFixture(t, server, p.ID, e.ID, "first")
	two := deployFixture(t, server, p.ID, e.ID, "second")
	runner := &partialTeardownRunner{fakeComposeRunner: server.runner, failedProject: "relist-c" + lifecycleID(two.ID), fail: true}
	server.useRunner(t, runner)
	runner.resetCalls()
	parentPath := "/api/projects/" + lifecycleID(p.ID)
	legacyStatus(t, server, http.MethodDelete, parentPath, http.StatusBadGateway)
	for _, id := range []int64{one.ID, two.ID} {
		legacyStatus(t, server, http.MethodGet, composePath(p.ID, e.ID, id), http.StatusOK)
	}
	runner.resetCalls()
	runner.fail = false
	legacyStatus(t, server, http.MethodDelete, parentPath, http.StatusNoContent)
	if len(runner.snapshotCalls()) != 1 || runner.snapshotCalls()[0].method != "down-with-volumes" || runner.snapshotCalls()[0].request.ProjectName != runner.failedProject {
		t.Fatalf("retry reprocessed completed child or skipped failed child: %+v", runner.snapshotCalls())
	}
	legacyStatus(t, server, http.MethodGet, parentPath, http.StatusNotFound)
}

func TestComposeDeployFreshRefusesAmbiguousLegacyIdentity(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	p := createProject(t, server, "Legacy", "")
	e := productionEnvironment(t, server, p)
	one := legacyCompose(t, server, e.ID, "a-b", "c", "web")
	two := legacyCompose(t, server, e.ID, "a", "b-c", "other")
	if _, err := server.db.Exec(`UPDATE composes SET name='c-web', env_slug='b' WHERE id=?`, two); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{one, two} {
		name := "web"
		if id == two {
			name = "c-web"
		}
		r := server.request(t, http.MethodPost, composeDeploymentsPath(p.ID, e.ID, id), freshBody(name))
		r.Body.Close()
		if r.StatusCode != http.StatusConflict {
			t.Fatalf("deploy-fresh of ambiguous legacy compose %d = %d, want %d", id, r.StatusCode, http.StatusConflict)
		}
	}
	if len(server.runner.snapshotCalls()) != 0 {
		t.Fatalf("ambiguous legacy identity reached Docker: %+v", server.runner.snapshotCalls())
	}
}

func TestOrphanVolumesRefuseAmbiguousLegacyIdentity(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	p := createProject(t, server, "Legacy", "")
	e := productionEnvironment(t, server, p)
	one := legacyCompose(t, server, e.ID, "a-b", "c", "web")
	two := legacyCompose(t, server, e.ID, "a", "b-c", "other")
	if _, err := server.db.Exec(`UPDATE composes SET name='c-web', env_slug='b' WHERE id=?`, two); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{one, two} {
		orphans := composeVolumesPath(p.ID, e.ID, id) + "/orphans"
		legacyStatus(t, server, http.MethodGet, orphans, http.StatusConflict)
		legacyStatus(t, server, http.MethodDelete, orphans+"/a-b-c-web_old", http.StatusConflict)
	}
	cli := server.volumeCLI
	if len(cli.ComposeConfigRefs) != 0 || len(cli.ListedProjects) != 0 || len(cli.RemovedVolumes) != 0 {
		t.Fatalf("ambiguous legacy identity reached the Docker CLI: %+v", cli)
	}
}
