package httpapi_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// roleFixture: as the admin, Project alpha (production + staging, a Compose
// in each) and Project beta (production, one Compose); then signed in as
// member 2, who has no Grant until a test gives one.
type roleFixture struct {
	server                         *testServer
	alpha, beta                    projectResponse
	alphaProd, alphaStaging, betaE environmentResponse
	alphaWeb, alphaJob, betaWeb    composeResponse
}

func newRoleFixture(t *testing.T) *roleFixture {
	t.Helper()
	server := newTestServer(t)
	setupAndLogin(t, server)
	f := &roleFixture{server: server}
	f.alpha = createProject(t, server, "alpha", "")
	f.alphaProd = productionEnvironment(t, server, f.alpha)
	response := postJSON(t, server, fmt.Sprintf("/api/projects/%d/environments", f.alpha.ID), map[string]string{"name": "staging"})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create staging = %d", response.StatusCode)
	}
	decodeJSON(t, response, &f.alphaStaging)
	f.beta = createProject(t, server, "beta", "")
	f.betaE = productionEnvironment(t, server, f.beta)
	f.alphaWeb = createCompose(t, server, f.alpha.ID, f.alphaProd.ID, "web")
	f.alphaJob = createCompose(t, server, f.alpha.ID, f.alphaStaging.ID, "job")
	f.betaWeb = createCompose(t, server, f.beta.ID, f.betaE.ID, "web")
	signInAsUser(t, server, 2, "member@example.com", false)
	return f
}

// grant gives member 2 a role: on the Project when environmentID is 0.
func (f *roleFixture) grant(t *testing.T, projectID, environmentID int64, role string) {
	t.Helper()
	var env any
	if environmentID != 0 {
		env = environmentID
	}
	if _, err := f.server.db.Exec("INSERT INTO grants (user_id, project_id, environment_id, role) VALUES (2, ?, ?, ?)", projectID, env, role); err != nil {
		t.Fatal(err)
	}
}

func (f *roleFixture) get(t *testing.T, path string) (int, string) {
	t.Helper()
	response := f.server.request(t, http.MethodGet, path, nil)
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return response.StatusCode, string(body)
}

func TestStatusReportsAccess(t *testing.T) {
	f := newRoleFixture(t)
	f.grant(t, f.alpha.ID, 0, "editor")
	f.grant(t, f.alpha.ID, f.alphaProd.ID, "none")
	code, body := f.get(t, "/api/auth/status")
	want := fmt.Sprintf(`"access":{"projects":[{"id":%d,"role":"editor","environments":[{"id":%d,"role":"editor"}]}]}`, f.alpha.ID, f.alphaStaging.ID)
	if code != http.StatusOK || !strings.Contains(body, want) {
		t.Fatalf("member status = %d %s\nwant it to contain %s", code, body, want)
	}
	signInAsUser(t, f.server, 3, "admin2@example.com", true)
	if _, body := f.get(t, "/api/auth/status"); !strings.Contains(body, `"access":null`) {
		t.Fatalf("admin status = %s, want access null", body)
	}
}

func TestProjectListShowsOnlyVisible(t *testing.T) {
	f := newRoleFixture(t)
	f.grant(t, f.alpha.ID, 0, "viewer")
	f.grant(t, f.alpha.ID, f.alphaStaging.ID, "none")
	code, body := f.get(t, "/api/projects")
	var projects []projectResponse
	_ = json.Unmarshal([]byte(body), &projects)
	if code != http.StatusOK || len(projects) != 1 || projects[0].ID != f.alpha.ID || projects[0].EnvironmentCount != 1 {
		t.Fatalf("GET /api/projects = %d %s", code, body)
	}
	code, body = f.get(t, fmt.Sprintf("/api/projects/%d", f.alpha.ID))
	var project projectResponse
	_ = json.Unmarshal([]byte(body), &project)
	if code != http.StatusOK || project.EnvironmentCount != 1 {
		t.Fatalf("GET project = %d %s", code, body)
	}
	if code, _ := f.get(t, fmt.Sprintf("/api/projects/%d", f.beta.ID)); code != http.StatusNotFound {
		t.Fatalf("GET hidden project = %d, want 404", code)
	}
}

func TestEnvironmentListHidesNoAccess(t *testing.T) {
	f := newRoleFixture(t)
	f.grant(t, f.alpha.ID, 0, "viewer")
	f.grant(t, f.alpha.ID, f.alphaProd.ID, "none")
	code, body := f.get(t, fmt.Sprintf("/api/projects/%d/environments", f.alpha.ID))
	var environments []environmentResponse
	_ = json.Unmarshal([]byte(body), &environments)
	if code != http.StatusOK || len(environments) != 1 || environments[0].ID != f.alphaStaging.ID {
		t.Fatalf("environments = %d %s", code, body)
	}
}

func (f *roleFixture) addDeployment(t *testing.T, composeID int64, status string) {
	t.Helper()
	if _, err := f.server.db.Exec(
		"INSERT INTO deployments (compose_id, number, mode, status, queued_at) VALUES (?, 1, 'deploy', ?, 1)", composeID, status,
	); err != nil {
		t.Fatal(err)
	}
}

func TestDeploymentHistoryShowsOnlyVisible(t *testing.T) {
	f := newRoleFixture(t)
	f.grant(t, f.alpha.ID, f.alphaProd.ID, "viewer")
	f.addDeployment(t, f.alphaWeb.ID, "succeeded")
	f.addDeployment(t, f.betaWeb.ID, "succeeded")
	code, body := f.get(t, "/api/deployments")
	var page struct {
		Items []struct {
			ComposeID int64 `json:"composeId"`
		} `json:"items"`
		Total int `json:"total"`
	}
	_ = json.Unmarshal([]byte(body), &page)
	if code != http.StatusOK || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ComposeID != f.alphaWeb.ID {
		t.Fatalf("history = %d %s", code, body)
	}
	// A member with no Grant at all sees none.
	if _, err := f.server.db.Exec("DELETE FROM grants"); err != nil {
		t.Fatal(err)
	}
	if _, body := f.get(t, "/api/deployments"); !strings.Contains(body, `"total":0`) {
		t.Fatalf("history without grants = %s", body)
	}
}

func TestQueueRedactsHiddenRows(t *testing.T) {
	f := newRoleFixture(t)
	f.grant(t, f.alpha.ID, f.alphaProd.ID, "viewer")
	f.addDeployment(t, f.betaWeb.ID, "running")
	code, body := f.get(t, "/api/deployments/queue")
	var rows []map[string]any
	_ = json.Unmarshal([]byte(body), &rows)
	if code != http.StatusOK || len(rows) != 1 {
		t.Fatalf("queue = %d %s", code, body)
	}
	row := rows[0]
	if row["composeId"] != float64(0) || row["projectName"] != "" || row["composeName"] != "" || row["environmentName"] != "" ||
		row["projectId"] != float64(0) || row["environmentId"] != float64(0) || row["status"] != "running" {
		t.Fatalf("hidden queue row = %v", row)
	}
	signInAsUser(t, f.server, 3, "admin2@example.com", true)
	if _, body := f.get(t, "/api/deployments/queue"); !strings.Contains(body, `"projectName":"beta"`) {
		t.Fatalf("admin queue = %s", body)
	}
}

func TestOperatorCannotDeployWithFreshVolumes(t *testing.T) {
	f := newRoleFixture(t)
	f.grant(t, f.alpha.ID, f.alphaProd.ID, "operator")
	path := fmt.Sprintf("/api/projects/%d/environments/%d/composes/%d/deployments", f.alpha.ID, f.alphaProd.ID, f.alphaWeb.ID)
	response := f.server.request(t, http.MethodPost, path, map[string]any{"mode": "deploy", "freshVolumes": true, "confirmName": "web"})
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("fresh volumes as operator = %d, want 403", response.StatusCode)
	}
	response = f.server.request(t, http.MethodPost, path, map[string]any{"mode": "deploy"})
	response.Body.Close()
	if response.StatusCode >= 300 {
		t.Fatalf("plain deploy as operator = %d", response.StatusCode)
	}
}

func TestSSHKeysAreReducedForMembers(t *testing.T) {
	f := newRoleFixture(t)
	signInAsUser(t, f.server, 3, "admin2@example.com", true)
	response := postJSON(t, f.server, "/api/ssh-keys", map[string]string{"name": "deploy"})
	response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create key = %d", response.StatusCode)
	}
	if _, body := f.get(t, "/api/ssh-keys"); !strings.Contains(body, `"publicKey"`) {
		t.Fatalf("admin keys = %s", body)
	}
	signInAsUser(t, f.server, 4, "member4@example.com", false)
	code, body := f.get(t, "/api/ssh-keys")
	if code != http.StatusOK || strings.Contains(body, "publicKey") || strings.Contains(body, "usedBy") || !strings.Contains(body, `"name":"deploy"`) {
		t.Fatalf("member keys = %d %s", code, body)
	}
}
