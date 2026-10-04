package httpapi_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"relist-panel/internal/volumes"
)

// orphanConfig declares one named volume, <project>_data, as the current YAML.
func orphanConfig(project string) []byte {
	return []byte(fmt.Sprintf(`{"services":{"web":{"volumes":[{"type":"volume","source":"data","target":"/data"}]}},"volumes":{"data":{"name":"%s_data"}}}`, project))
}

// listedVolume is a volume carrying the labels Compose stamps on one it
// created for project.
func listedVolume(project, logical string) volumes.ProjectVolume {
	return volumes.ProjectVolume{
		Name:      project + "_" + logical,
		CreatedAt: "2026-01-02T03:04:05Z",
		Labels: map[string]string{
			volumes.ComposeProjectLabel: project,
			volumes.ComposeVolumeLabel:  logical,
		},
	}
}

// orphanFixture is a Compose whose current YAML declares <project>_data.
type orphanFixture struct {
	server  *testServer
	project string
	list    string
	compose composeResponse
	base    string
}

func setupOrphanCompose(t *testing.T) orphanFixture {
	t.Helper()
	server := newTestServer(t)
	setupAndLogin(t, server)
	p := createProject(t, server, "Orphan Co", "")
	e := productionEnvironment(t, server, p)
	c := createCompose(t, server, p.ID, e.ID, "web")
	project := "relist-c" + lifecycleID(c.ID)
	server.volumeCLI.ComposeConfigOutput = orphanConfig(project)
	base := composeVolumesPath(p.ID, e.ID, c.ID) + "/orphans"
	return orphanFixture{server: server, project: project, list: base, compose: c, base: base}
}

func TestOrphanVolumesListsOnlyThisComposesOrphans(t *testing.T) {
	f := setupOrphanCompose(t)
	f.server.volumeCLI.ProjectVolumes = []volumes.ProjectVolume{
		listedVolume(f.project, "data"),
		listedVolume(f.project, "old"),
		listedVolume("other-project", "old"),
	}

	response := f.server.request(t, http.MethodGet, f.list, nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	var body struct {
		Orphans []struct {
			Name      string `json:"name"`
			CreatedAt string `json:"createdAt"`
		} `json:"orphans"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Orphans) != 1 || body.Orphans[0].Name != f.project+"_old" || body.Orphans[0].CreatedAt != "2026-01-02T03:04:05Z" {
		t.Fatalf("orphans = %+v, want only %s_old", body.Orphans, f.project)
	}
	if !reflect.DeepEqual(f.server.volumeCLI.ListedProjects, []string{f.project}) {
		t.Fatalf("listed projects = %v, want [%s]", f.server.volumeCLI.ListedProjects, f.project)
	}
}

func TestOrphanVolumesListIsAnEmptyArrayWhenThereAreNone(t *testing.T) {
	f := setupOrphanCompose(t)
	f.server.volumeCLI.ProjectVolumes = []volumes.ProjectVolume{listedVolume(f.project, "data")}

	response := f.server.request(t, http.MethodGet, f.list, nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(response.Body).Decode(&raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(raw["orphans"]) != "[]" {
		t.Fatalf("orphans = %s, want []", raw["orphans"])
	}
}

func TestOrphanVolumesListSurfacesTheNotDeployedError(t *testing.T) {
	f := setupOrphanCompose(t)
	f.server.volumeCLI.ComposeConfigErr = errors.New("couldn't find env file: /data/.env")

	response := f.server.request(t, http.MethodGet, f.list, nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", response.StatusCode)
	}
	assertErrorContains(t, response, "couldn't find env file")
}

func TestOrphanVolumesListFailureIs502(t *testing.T) {
	f := setupOrphanCompose(t)
	f.server.volumeCLI.ListProjectVolumesErr = errors.New("docker unavailable")

	response := f.server.request(t, http.MethodGet, f.list, nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", response.StatusCode)
	}
	assertErrorContains(t, response, "docker unavailable")
}

func TestOrphanVolumesUnknownIdsAre404(t *testing.T) {
	f := setupOrphanCompose(t)
	for _, path := range []string{
		"/api/projects/999/environments/1/composes/1/volumes/orphans",
		composeVolumesPath(1, 1, f.compose.ID+100) + "/orphans",
	} {
		response := f.server.request(t, http.MethodGet, path, nil)
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, response.StatusCode)
		}
	}
	if len(f.server.volumeCLI.ListedProjects) != 0 {
		t.Fatal("unknown compose reached the CLI")
	}
}

func TestDeleteOrphanVolumeRemovesIt(t *testing.T) {
	f := setupOrphanCompose(t)
	f.server.volumeCLI.ProjectVolumes = []volumes.ProjectVolume{listedVolume(f.project, "data"), listedVolume(f.project, "old")}

	response := f.server.request(t, http.MethodDelete, f.base+"/"+f.project+"_old", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", response.StatusCode)
	}
	if want := []string{f.project + "_old"}; !reflect.DeepEqual(f.server.volumeCLI.RemovedVolumes, want) {
		t.Fatalf("removed = %v, want %v", f.server.volumeCLI.RemovedVolumes, want)
	}
}

func TestDeleteOrphanVolumeRefusesCurrentOtherProjectAndPathLikeNames(t *testing.T) {
	f := setupOrphanCompose(t)
	f.server.volumeCLI.ProjectVolumes = []volumes.ProjectVolume{
		listedVolume(f.project, "data"),
		listedVolume(f.project, "old"),
		listedVolume("other-project", "old"),
	}
	for _, name := range []string{f.project + "_data", "other-project_old", "%2E%2E%2Fx", "missing"} {
		response := f.server.request(t, http.MethodDelete, f.base+"/"+name, nil)
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Errorf("DELETE %s = %d, want 404", name, response.StatusCode)
		}
	}
	if len(f.server.volumeCLI.RemovedVolumes) != 0 {
		t.Fatalf("refused deletes removed %v", f.server.volumeCLI.RemovedVolumes)
	}
}

func TestDeleteOrphanVolumeInUseIs409WithDockersMessage(t *testing.T) {
	f := setupOrphanCompose(t)
	f.server.volumeCLI.ProjectVolumes = []volumes.ProjectVolume{listedVolume(f.project, "old")}
	f.server.volumeCLI.RemoveVolumeErr = fmt.Errorf("%w: volume is in use - [abc123]", volumes.ErrVolumeInUse)

	response := f.server.request(t, http.MethodDelete, f.base+"/"+f.project+"_old", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", response.StatusCode)
	}
	assertErrorContains(t, response, "volume is in use - [abc123]")
}

func TestDeleteOrphanVolumeOtherFailuresAre502(t *testing.T) {
	f := setupOrphanCompose(t)
	f.server.volumeCLI.ProjectVolumes = []volumes.ProjectVolume{listedVolume(f.project, "old")}
	f.server.volumeCLI.RemoveVolumeErr = errors.New("docker unavailable")

	response := f.server.request(t, http.MethodDelete, f.base+"/"+f.project+"_old", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", response.StatusCode)
	}
}

func TestDeleteOrphanVolumeResolutionFailureIs502(t *testing.T) {
	f := setupOrphanCompose(t)
	f.server.volumeCLI.ComposeConfigErr = errors.New("couldn't find env file")

	response := f.server.request(t, http.MethodDelete, f.base+"/"+f.project+"_old", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", response.StatusCode)
	}
	if len(f.server.volumeCLI.RemovedVolumes) != 0 {
		t.Fatal("failed resolution removed a volume")
	}
}

func TestOrphanVolumeRoutesNeedAuthentication(t *testing.T) {
	server := newTestServer(t)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/projects/1/environments/1/composes/1/volumes/orphans"},
		{http.MethodDelete, "/api/projects/1/environments/1/composes/1/volumes/orphans/app_old"},
	} {
		response := server.request(t, tc.method, tc.path, nil)
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s = %d, want 401", tc.method, tc.path, response.StatusCode)
		}
	}
	if len(server.volumeCLI.ListedProjects) != 0 || len(server.volumeCLI.RemovedVolumes) != 0 {
		t.Fatal("unauthenticated request reached the CLI")
	}
}
