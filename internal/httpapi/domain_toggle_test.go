package httpapi_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// setEnabled turns a Domain of f on or off through the toggle endpoint.
func (f domainsFixture) setEnabled(t *testing.T, id int64, enabled bool) (*http.Response, []byte) {
	t.Helper()
	return f.send(t, http.MethodPut, "/domains/"+idString(id)+"/enabled", map[string]any{"enabled": enabled})
}

func decodeDomain(t *testing.T, raw []byte) domainResponse {
	t.Helper()
	var d domainResponse
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("body %s: %v", raw, err)
	}
	return d
}

func (f domainsFixture) listedDomain(t *testing.T, id int64) domainResponse {
	t.Helper()
	_, raw := f.send(t, http.MethodGet, "/domains", nil)
	var list domainsListResponse
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	for _, d := range list.Domains {
		if d.ID == id {
			return d
		}
	}
	t.Fatalf("domain %d not listed in %s", id, raw)
	return domainResponse{}
}

func TestDomainToggle(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	f := newDomainsCompose(t, server, "shop", "web")
	created := f.createDomain(t, "api.example.com", "/")

	const past = "1970-01-01T00:00:01Z"
	backdate := func() {
		t.Helper()
		if _, err := server.db.Exec("UPDATE composes SET domains_changed_at = 1 WHERE id = ?", f.composeID); err != nil {
			t.Fatal(err)
		}
	}
	stamped := func(step string) {
		t.Helper()
		if got := f.domainsChangedAt(t); got == nil || *got == past {
			t.Errorf("%s: domainsChangedAt = %v, want a new stamp", step, got)
		}
	}

	for _, step := range []struct {
		name    string
		enabled bool
	}{{"off", false}, {"off again", false}, {"on", true}} {
		backdate()
		response, raw := f.setEnabled(t, created.ID, step.enabled)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s: status = %d, body %s", step.name, response.StatusCode, raw)
		}
		if got := decodeDomain(t, raw); got.Enabled != step.enabled || got.Host != "api.example.com" {
			t.Fatalf("%s: response = %+v", step.name, got)
		}
		if got := f.listedDomain(t, created.ID); got.Enabled != step.enabled {
			t.Fatalf("%s: listed enabled = %v", step.name, got.Enabled)
		}
		stamped(step.name)
	}

	events := auditActions(auditRows(t, server), "domain.toggle")
	if len(events) != 3 {
		t.Fatalf("domain.toggle events = %+v, want 3", events)
	}
	if events[0].Detail["host"] != "api.example.com" || events[0].Detail["enabled"] != false || events[2].Detail["enabled"] != true {
		t.Errorf("details = %+v / %+v", events[0].Detail, events[2].Detail)
	}
}

func TestDomainToggleBadBody(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	f := newDomainsCompose(t, server, "shop", "web")
	created := f.createDomain(t, "api.example.com", "/")
	path := "/domains/" + idString(created.ID) + "/enabled"
	for _, body := range []any{map[string]any{}, map[string]any{"enabled": "yes"}, "not json", map[string]any{"enabled": true, "host": "x"}} {
		response, raw := f.send(t, http.MethodPut, path, body)
		if response.StatusCode != http.StatusBadRequest {
			t.Errorf("body %v: status = %d, body %s", body, response.StatusCode, raw)
		}
	}
	if got := f.listedDomain(t, created.ID); !got.Enabled {
		t.Error("a refused toggle changed the domain")
	}
}

func TestDomainToggleOtherCompose(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	f := newDomainsCompose(t, server, "shop", "web")
	other := f.sibling(t, "api")
	theirs := other.createDomain(t, "api.example.com", "/")
	if response, raw := f.setEnabled(t, theirs.ID, false); response.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, body %s", response.StatusCode, raw)
	}
	if got := other.listedDomain(t, theirs.ID); !got.Enabled {
		t.Error("another Compose's domain was turned off")
	}
}

func TestDomainToggleOnNeedsService(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	f := newDomainsCompose(t, server, "shop", "web")
	created := f.createDomain(t, "api.example.com", "/")
	if response, raw := f.setEnabled(t, created.ID, false); response.StatusCode != http.StatusOK {
		t.Fatalf("off: %d %s", response.StatusCode, raw)
	}
	const want = `service "web" is not a service of this compose that can be routed to; edit the domain first`
	for _, yaml := range []string{
		"services: {db: {image: postgres:16}}",
		"services: {web: {image: nginx, network_mode: host}, db: {image: postgres:16}}",
	} {
		f.saveYAML(t, yaml)
		if response, raw := f.setEnabled(t, created.ID, false); response.StatusCode != http.StatusOK {
			t.Errorf("off with %q: %d %s", yaml, response.StatusCode, raw)
		}
		response, raw := f.setEnabled(t, created.ID, true)
		var body struct{ Error string }
		_ = json.Unmarshal(raw, &body)
		if response.StatusCode != http.StatusUnprocessableEntity || body.Error != want {
			t.Errorf("on with %q: %d %s", yaml, response.StatusCode, raw)
		}
	}
	if got := f.listedDomain(t, created.ID); got.Enabled {
		t.Error("a refused turn-on left the domain on")
	}
}

func TestDomainEditKeepsItOff(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	f := newDomainsCompose(t, server, "shop", "web")
	created := f.createDomain(t, "api.example.com", "/")
	f.setEnabled(t, created.ID, false)
	body := domainBody("api.example.com", "/")
	body["port"] = 8080
	response, raw := f.send(t, http.MethodPut, "/domains/"+idString(created.ID), body)
	if got := decodeDomain(t, raw); response.StatusCode != http.StatusOK || got.Enabled || got.Port != 8080 {
		t.Fatalf("edit = %d %s", response.StatusCode, raw)
	}
}

func TestDomainOffStillHoldsItsHost(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	shop := newDomainsCompose(t, server, "shop", "web")
	blog := newDomainsCompose(t, server, "blog", "site")
	created := shop.createDomain(t, "api.example.com", "/")
	shop.setEnabled(t, created.ID, false)
	if response, raw := blog.send(t, http.MethodPost, "/domains", domainBody("api.example.com", "/")); response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, body %s", response.StatusCode, raw)
	}
}

func TestAuthGroupInUseByOffDomain(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	f := newDomainsCompose(t, server, "shop", "web")
	response := server.request(t, http.MethodPost, "/api/auth-groups", map[string]any{"name": "Staging"})
	var group struct {
		ID int64 `json:"id"`
	}
	decodeJSON(t, response, &group)
	body := domainBody("api.example.com", "/")
	body["authGroupId"] = group.ID
	response, raw := f.send(t, http.MethodPost, "/domains", body)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d %s", response.StatusCode, raw)
	}
	f.setEnabled(t, decodeDomain(t, raw).ID, false)
	response = server.request(t, http.MethodDelete, "/api/auth-groups/"+idString(group.ID), nil)
	response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("delete group used by an off domain = %d, want 409", response.StatusCode)
	}
}

func TestDeploySkipsOffDomain(t *testing.T) {
	f := newDeployDomainsFixture(t)
	domain := f.createDomain(t, "api.example.com", "/")
	router := "relist-c" + idString(f.composeID) + "-d" + idString(domain.ID)
	if response, raw := f.setEnabled(t, domain.ID, false); response.StatusCode != http.StatusOK {
		t.Fatalf("off: %d %s", response.StatusCode, raw)
	}
	f.deployOK(t, nil)
	resolved := f.resolvedFile(t)
	if strings.Contains(resolved, router) {
		t.Fatalf("resolved compose routes an off domain:\n%s", resolved)
	}
	if !strings.Contains(resolved, "relist-network") {
		t.Fatalf("resolved compose lost relist-network:\n%s", resolved)
	}

	f.setEnabled(t, domain.ID, true)
	f.deployOK(t, nil)
	if resolved := f.resolvedFile(t); !strings.Contains(resolved, router) {
		t.Fatalf("resolved compose lacks the domain turned back on:\n%s", resolved)
	}
}
