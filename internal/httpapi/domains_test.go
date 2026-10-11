package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
)

type domainResponse struct {
	ID           int64    `json:"id"`
	Host         string   `json:"host"`
	Path         string   `json:"path"`
	InternalPath string   `json:"internalPath"`
	Service      string   `json:"service"`
	Port         int      `json:"port"`
	HTTPS        bool     `json:"https"`
	CertProvider string   `json:"certProvider"`
	Middlewares  []string `json:"middlewares"`
}

type domainsListResponse struct {
	Domains  []domainResponse `json:"domains"`
	Services []string         `json:"services"`
}

const domainsYAML = "services: {web: {image: nginx:1.27}, db: {image: postgres:16}}"

// domainsFixture is a Compose whose saved YAML declares web and db.
type domainsFixture struct {
	server    *testServer
	project   projectResponse
	base      string
	composeID int64
}

func newDomainsCompose(t *testing.T, server *testServer, projectName, composeName string) domainsFixture {
	t.Helper()
	project := createProject(t, server, projectName, "")
	environment := productionEnvironment(t, server, project)
	compose := createCompose(t, server, project.ID, environment.ID, composeName)
	composePath := "/api/projects/" + idString(project.ID) + "/environments/" + idString(environment.ID) + "/composes/" + idString(compose.ID)
	response := server.request(t, http.MethodPut, composePath, map[string]any{"yamlContent": domainsYAML})
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("save compose yaml status = %d", response.StatusCode)
	}
	return domainsFixture{server: server, project: project, base: composePath, composeID: compose.ID}
}

func domainBody(host, path string) map[string]any {
	return map[string]any{
		"host": host, "path": path, "internalPath": "/", "stripPath": false, "service": "web", "port": 3000,
		"entrypoint": "", "https": true, "certProvider": "letsencrypt", "certResolver": "", "middlewares": []string{},
	}
}

func (f domainsFixture) send(t *testing.T, method, suffix string, body any) (*http.Response, []byte) {
	t.Helper()
	response := f.server.request(t, method, f.base+suffix, body)
	defer response.Body.Close()
	var raw json.RawMessage
	_ = json.NewDecoder(response.Body).Decode(&raw)
	return response, raw
}

func (f domainsFixture) createDomain(t *testing.T, host, path string) domainResponse {
	t.Helper()
	response, raw := f.send(t, http.MethodPost, "/domains", domainBody(host, path))
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create domain status = %d, body %s", response.StatusCode, raw)
	}
	var domain domainResponse
	if err := json.Unmarshal(raw, &domain); err != nil {
		t.Fatal(err)
	}
	return domain
}

func (f domainsFixture) domainsChangedAt(t *testing.T) *string {
	t.Helper()
	_, raw := f.send(t, http.MethodGet, "", nil)
	var compose struct {
		DomainsChangedAt *string `json:"domainsChangedAt"`
	}
	if err := json.Unmarshal(raw, &compose); err != nil {
		t.Fatal(err)
	}
	return compose.DomainsChangedAt
}

func TestDomainsCRUD(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	f := newDomainsCompose(t, server, "shop", "web")

	response, raw := f.send(t, http.MethodGet, "/domains", nil)
	if response.StatusCode != http.StatusOK || string(raw) != `{"domains":[],"services":["db","web"]}` {
		t.Fatalf("empty list = %d %s", response.StatusCode, raw)
	}

	created := f.createDomain(t, "API.example.com", "/")
	if created.Host != "api.example.com" || created.Port != 3000 || created.ID == 0 {
		t.Errorf("created = %+v", created)
	}

	update := domainBody("api.example.com", "/")
	update["port"] = 4000
	response, raw = f.send(t, http.MethodPut, "/domains/"+idString(created.ID), update)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d, body %s", response.StatusCode, raw)
	}
	var updated domainResponse
	_ = json.Unmarshal(raw, &updated)
	if updated.ID != created.ID || updated.Port != 4000 {
		t.Errorf("updated = %+v", updated)
	}

	response, raw = f.send(t, http.MethodPut, "/domains/"+idString(created.ID), update)
	if response.StatusCode != http.StatusOK {
		t.Errorf("unchanged update status = %d, body %s", response.StatusCode, raw)
	}

	_, raw = f.send(t, http.MethodGet, "/domains", nil)
	var list domainsListResponse
	_ = json.Unmarshal(raw, &list)
	if len(list.Domains) != 1 || list.Domains[0].Port != 4000 {
		t.Errorf("list = %s", raw)
	}

	if response, _ = f.send(t, http.MethodDelete, "/domains/"+idString(created.ID), nil); response.StatusCode != http.StatusNoContent {
		t.Errorf("delete status = %d", response.StatusCode)
	}
	if response, _ = f.send(t, http.MethodDelete, "/domains/"+idString(created.ID), nil); response.StatusCode != http.StatusNotFound {
		t.Errorf("second delete status = %d", response.StatusCode)
	}
	if response, _ = f.send(t, http.MethodPut, "/domains/"+idString(created.ID), update); response.StatusCode != http.StatusNotFound {
		t.Errorf("update of deleted status = %d", response.StatusCode)
	}
}

func TestDomainsRejectInvalidFieldWith400(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	f := newDomainsCompose(t, server, "shop", "web")

	response, raw := f.send(t, http.MethodPost, "/domains", domainBody("bad host", "/"))
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.StatusCode)
	}
	var body struct{ Error, Field string }
	_ = json.Unmarshal(raw, &body)
	if body.Field != "host" || body.Error == "" {
		t.Errorf("body = %s", raw)
	}
}

func TestDomainsConflictAcrossComposes(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	first := newDomainsCompose(t, server, "shop", "web")
	second := newDomainsCompose(t, server, "blog", "site")
	first.createDomain(t, "api.example.com", "/")

	response, raw := second.send(t, http.MethodPost, "/domains", domainBody("api.example.com", "/"))
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", response.StatusCode)
	}
	var body struct{ Error string }
	_ = json.Unmarshal(raw, &body)
	if want := "api.example.com/ is already used by shop / production / web"; body.Error != want {
		t.Errorf("error = %q, want %q", body.Error, want)
	}
	second.createDomain(t, "api.example.com", "/api")

	// Moving an existing domain onto a taken address conflicts as well.
	other := second.createDomain(t, "other.example.com", "/")
	response, _ = second.send(t, http.MethodPut, "/domains/"+idString(other.ID), domainBody("api.example.com", "/"))
	if response.StatusCode != http.StatusConflict {
		t.Errorf("update conflict status = %d, want 409", response.StatusCode)
	}
}

func TestDomainsChangedAtMovesOnEveryChange(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	f := newDomainsCompose(t, server, "shop", "web")
	other := newDomainsCompose(t, server, "blog", "site")

	// backdate sets the stamp to a known past second, so any later write that
	// stamps the Compose is visibly different.
	const past = "1970-01-01T00:00:01Z"
	backdate := func() {
		t.Helper()
		if _, err := server.db.Exec("UPDATE composes SET domains_changed_at = 1 WHERE id = ?", f.composeID); err != nil {
			t.Fatal(err)
		}
		if got := f.domainsChangedAt(t); got == nil || *got != past {
			t.Fatalf("backdated domainsChangedAt = %v, want %s", got, past)
		}
	}

	if got := f.domainsChangedAt(t); got != nil {
		t.Fatalf("initial domainsChangedAt = %v, want null", *got)
	}

	// A rejected create (409) must not stamp: the stamp shares the domain write's transaction.
	other.createDomain(t, "taken.example.com", "/")
	if response, _ := f.send(t, http.MethodPost, "/domains", domainBody("taken.example.com", "/")); response.StatusCode != http.StatusConflict {
		t.Fatalf("conflicting create status = %d, want 409", response.StatusCode)
	}
	if got := f.domainsChangedAt(t); got != nil {
		t.Errorf("domainsChangedAt after rejected create = %v, want null", *got)
	}

	created := f.createDomain(t, "api.example.com", "/")
	if got := f.domainsChangedAt(t); got == nil {
		t.Fatal("domainsChangedAt is null after create")
	}

	backdate()
	if response, raw := f.send(t, http.MethodPut, "/domains/"+idString(created.ID), domainBody("api.example.com", "/x")); response.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d, body %s", response.StatusCode, raw)
	}
	if got := f.domainsChangedAt(t); got == nil || *got == past {
		t.Errorf("domainsChangedAt after update = %v, want a fresh stamp", got)
	}

	backdate()
	if response, _ := f.send(t, http.MethodPut, "/domains/"+idString(created.ID), domainBody("taken.example.com", "/")); response.StatusCode != http.StatusConflict {
		t.Fatalf("conflicting update status = %d, want 409", response.StatusCode)
	}
	if got := f.domainsChangedAt(t); got == nil || *got != past {
		t.Errorf("domainsChangedAt after rejected update = %v, want it left at %s", got, past)
	}

	if response, _ := f.send(t, http.MethodDelete, "/domains/"+idString(created.ID), nil); response.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d", response.StatusCode)
	}
	if got := f.domainsChangedAt(t); got == nil || *got == past {
		t.Errorf("domainsChangedAt after delete = %v, want a fresh stamp", got)
	}
}

func TestDomainsCascadeWithCompose(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	f := newDomainsCompose(t, server, "shop", "web")
	f.createDomain(t, "api.example.com", "/")

	response, _ := f.send(t, http.MethodDelete, "", nil)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("delete compose status = %d", response.StatusCode)
	}
	var count int
	if err := server.db.QueryRow("SELECT COUNT(*) FROM domains").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("domains left = %d, want 0", count)
	}
}

func TestDomainsRequireKnownCompose(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	f := newDomainsCompose(t, server, "shop", "web")
	other := createProject(t, server, "blog", "")
	otherEnv := productionEnvironment(t, server, other)

	paths := []string{
		"/api/projects/" + idString(f.project.ID) + "/environments/1/composes/9999/domains",
		"/api/projects/" + idString(other.ID) + "/environments/" + idString(otherEnv.ID) + "/composes/" + idString(f.composeID) + "/domains",
	}
	for _, path := range paths {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			var body any
			if method == http.MethodPost {
				body = domainBody("api.example.com", "/")
			}
			response := server.request(t, method, path, body)
			response.Body.Close()
			if response.StatusCode != http.StatusNotFound {
				t.Errorf("%s %s status = %d, want 404", method, path, response.StatusCode)
			}
		}
	}
}

func (f domainsFixture) saveYAML(t *testing.T, yaml string) {
	t.Helper()
	response, raw := f.send(t, http.MethodPut, "", map[string]any{"yamlContent": yaml})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("save yaml status = %d, body %s", response.StatusCode, raw)
	}
}

// deploy queues a Deployment with body (nil is a plain deploy) and returns it settled.
func (f domainsFixture) deploy(t *testing.T, body any) deploymentResponse {
	t.Helper()
	return f.server.deployAndWait(t, f.base, body)
}

// deployOK is deploy for a Deployment that must succeed.
func (f domainsFixture) deployOK(t *testing.T, body any) deploymentResponse {
	t.Helper()
	return f.server.deployOK(t, f.base, body)
}

func (f domainsFixture) upCalls() int {
	n := 0
	for _, call := range f.server.runner.snapshotCalls() {
		if call.method == "up" {
			n++
		}
	}
	return n
}

func (f domainsFixture) resolvedFile(t *testing.T) string {
	t.Helper()
	for _, call := range f.server.runner.snapshotCalls() {
		if call.method == "up" {
			raw, err := os.ReadFile(call.request.ComposeFilePath)
			if err != nil {
				t.Fatal(err)
			}
			return string(raw)
		}
	}
	t.Fatal("no up call recorded")
	return ""
}

func newDeployDomainsFixture(t *testing.T) domainsFixture {
	t.Helper()
	server := newTestServer(t)
	setupAndLogin(t, server)
	return newDomainsCompose(t, server, "Deploy Domains", "web")
}

func TestDeployInjectsDomainLabels(t *testing.T) {
	f := newDeployDomainsFixture(t)
	domain := f.createDomain(t, "api.example.com", "/")

	finished := f.deployOK(t, nil)
	if finished.Warnings == nil || len(finished.Warnings) != 0 {
		t.Fatalf("warnings = %v, want []", finished.Warnings)
	}
	if _, raw := f.send(t, http.MethodGet, "/deployments/"+strconv.Itoa(finished.Number), nil); !strings.Contains(string(raw), `"warnings":[]`) {
		t.Fatalf("body = %s, want warnings:[]", raw)
	}

	resolved := f.resolvedFile(t)
	router := "relist-c" + strconv.FormatInt(f.composeID, 10) + "-d" + strconv.FormatInt(domain.ID, 10)
	for _, want := range []string{"traefik.http.routers." + router + ".rule=Host(`api.example.com`)", "relist-network"} {
		if !strings.Contains(resolved, want) {
			t.Fatalf("resolved compose = %s, want it to contain %q", resolved, want)
		}
	}
}

func TestDeployReturnsWarnings(t *testing.T) {
	for _, fresh := range []bool{false, true} {
		t.Run(fmt.Sprintf("freshVolumes=%v", fresh), func(t *testing.T) {
			f := newDeployDomainsFixture(t)
			f.saveYAML(t, "services:\n  web:\n    image: nginx\n    labels:\n      - traefik.enable=false\n")
			f.createDomain(t, "api.example.com", "/")

			body := map[string]any{"mode": "deploy"}
			if fresh {
				body = freshBody("web")
			}
			got := f.deployOK(t, body)
			if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "traefik.enable=false was overridden") {
				t.Fatalf("warnings = %v, want the enable warning", got.Warnings)
			}
		})
	}
}

func TestDeployRefusedWhenDomainServiceRemoved(t *testing.T) {
	f := newDeployDomainsFixture(t)
	f.createDomain(t, "api.example.com", "/")
	f.deployOK(t, nil)
	first := f.resolvedFile(t)

	f.saveYAML(t, "services: {db: {image: postgres:16}}")
	refused := f.deploy(t, nil)
	want := `domain api.example.com/ points at service "web", which is not in the compose file`
	if refused.Status != "failed" || refused.Reason != want {
		t.Fatalf("deployment = %s with reason %q, want failed with exactly the refusal copy %q", refused.Status, refused.Reason, want)
	}
	if n := f.upCalls(); n != 1 {
		t.Fatalf("up calls = %d, want 1", n)
	}
	if after := f.resolvedFile(t); after != first {
		t.Fatalf("resolved file changed after refused deploy:\n%s\nwas\n%s", after, first)
	}
}

func TestDeployFreshRefusedWhenDomainServiceRemoved(t *testing.T) {
	f := newDeployDomainsFixture(t)
	f.createDomain(t, "api.example.com", "/")
	f.deployOK(t, nil)
	first := f.resolvedFile(t)

	f.saveYAML(t, "services: {db: {image: postgres:16}}")
	refused := f.deploy(t, freshBody("web"))
	want := `domain api.example.com/ points at service "web", which is not in the compose file`
	if refused.Status != "failed" || refused.Reason != want {
		t.Fatalf("deployment = %s with reason %q, want failed with exactly the refusal copy %q", refused.Status, refused.Reason, want)
	}
	for _, call := range f.server.runner.snapshotCalls() {
		if call.method == "down-with-volumes" {
			t.Fatal("deploy-fresh removed volumes although the deploy was refused")
		}
	}
	if n := f.upCalls(); n != 1 {
		t.Fatalf("up calls = %d, want 1", n)
	}
	if after := f.resolvedFile(t); after != first {
		t.Fatalf("resolved file changed after refused deploy:\n%s\nwas\n%s", after, first)
	}
}

func TestDeployNamesLegacyComposeRoutersFromDatabaseIDs(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	project := createProject(t, server, "Legacy Domains", "")
	environment := productionEnvironment(t, server, project)
	// Slugs that would be tempting (and wrong) to derive router names from.
	composeID := legacyCompose(t, server, environment.ID, "legacy-domains", "production", "web")
	f := domainsFixture{
		server:    server,
		project:   project,
		base:      "/api/projects/" + idString(project.ID) + "/environments/" + idString(environment.ID) + "/composes/" + idString(composeID),
		composeID: composeID,
	}
	domain := f.createDomain(t, "legacy.example.com", "/")

	f.deployOK(t, nil)

	if len(server.runner.snapshotCalls()) == 0 {
		t.Fatal("deploy reached no runner call")
	}
	ups := 0
	for _, call := range server.runner.snapshotCalls() {
		if call.method != "up" {
			continue
		}
		ups++
		if call.request.ProjectName != "legacy-domains-production-web" {
			t.Fatalf("deploy project name = %q, want the legacy one", call.request.ProjectName)
		}
	}
	if ups == 0 {
		t.Fatal("deploy recorded no up call")
	}
	resolved := f.resolvedFile(t)
	router := "relist-c" + strconv.FormatInt(composeID, 10) + "-d" + strconv.FormatInt(domain.ID, 10)
	for _, want := range []string{
		"traefik.http.routers." + router + ".rule=Host(`legacy.example.com`)",
		"traefik.http.services." + router + ".loadbalancer.server.port=3000",
		"traefik.http.routers." + router + "-http.entrypoints=web",
	} {
		if !strings.Contains(resolved, want) {
			t.Fatalf("resolved compose = %s, want it to contain %q", resolved, want)
		}
	}
	for _, line := range strings.Split(resolved, "\n") {
		if strings.Contains(line, "traefik.") && strings.Contains(line, "legacy-domains") {
			t.Fatalf("label %q derives a Traefik name from slugs", line)
		}
	}
}
