package httpapi_test

import (
	"encoding/json"
	"net/http"
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

	if got := f.domainsChangedAt(t); got != nil {
		t.Fatalf("initial domainsChangedAt = %v, want null", *got)
	}
	created := f.createDomain(t, "api.example.com", "/")
	afterCreate := f.domainsChangedAt(t)
	if afterCreate == nil {
		t.Fatal("domainsChangedAt is null after create")
	}
	f.send(t, http.MethodPut, "/domains/"+idString(created.ID), domainBody("api.example.com", "/x"))
	afterUpdate := f.domainsChangedAt(t)
	if afterUpdate == nil || *afterUpdate < *afterCreate {
		t.Errorf("after update = %v, want >= %v", afterUpdate, *afterCreate)
	}
	f.send(t, http.MethodDelete, "/domains/"+idString(created.ID), nil)
	afterDelete := f.domainsChangedAt(t)
	if afterDelete == nil || *afterDelete < *afterUpdate {
		t.Errorf("after delete = %v, want >= %v", afterDelete, *afterUpdate)
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
