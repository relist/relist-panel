package composes

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"relist-panel/internal/dockernet"
	"relist-panel/internal/domains"
)

func traefikLabelKeys(service map[string]any) []string {
	var keys []string
	switch labels := service["labels"].(type) {
	case map[string]any:
		for key := range labels {
			if strings.HasPrefix(key, "traefik.") {
				keys = append(keys, key)
			}
		}
	case []any:
		for _, entry := range labels {
			if s, _ := entry.(string); strings.HasPrefix(s, "traefik.") {
				keys = append(keys, s)
			}
		}
	}
	return keys
}

func TestOffDomainWritesNoTraefikLabelsButKeepsAliases(t *testing.T) {
	off := webDomain()
	off.Enabled = false
	services, _ := resolveServices(t, "services:\n  web: {image: nginx}\n", []domains.Domain{off}, testIdentity)
	if keys := traefikLabelKeys(services["web"]); len(keys) != 0 {
		t.Fatalf("traefik labels for an off domain: %v", keys)
	}
	want := []string{"web.relist-c12.internal", "web.test.production.hello.internal"}
	if got := sharedAliases(t, services["web"]); !reflect.DeepEqual(got, want) {
		t.Fatalf("aliases = %v, want %v", got, want)
	}
}

func TestOffDomainOnMissingServiceDoesNotFail(t *testing.T) {
	off := webDomain()
	off.Enabled, off.Service = false, "gone"
	if _, _, _, err := resolveCompose("services:\n  web: {image: nginx}\n", testNetwork, "/data/.env", 12, []domains.Domain{off}, testIdentity, deployVariables{}, nil); err != nil {
		t.Fatalf("resolveCompose = %v, want no error for an off domain", err)
	}
}

func TestOnAndOffDomainsOnOneService(t *testing.T) {
	on := webDomain()
	off := webDomain()
	off.ID, off.Host, off.Enabled = 4, "old.example.com", false
	services, _ := resolveServices(t, "services:\n  web: {image: nginx}\n", []domains.Domain{on, off}, testIdentity)
	joined := strings.Join(traefikLabelKeys(services["web"]), "\n")
	if !strings.Contains(joined, "relist-c12-d3") {
		t.Errorf("labels lack the on domain's router:\n%s", joined)
	}
	if strings.Contains(joined, "relist-c12-d4") {
		t.Errorf("labels carry the off domain's router:\n%s", joined)
	}
}

func seedDomainOff(t *testing.T, db *sql.DB, service string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO domains (compose_id, host, service, port, created_at, updated_at, enabled) VALUES (1, ?, ?, 80, 0, 0, 0)`, "off-"+service+".example.com", service); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureSkipsServiceWhoseDomainsAreOff(t *testing.T) {
	svc, _, target, db := newDeployService(t, zdYAML)
	seedDomainOff(t, db, "web")
	switchOn(t, db, "web")
	captured, err := svc.Capture(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if got := captured.Snapshot.ZeroDowntime; len(got) != 0 {
		t.Fatalf("ZeroDowntime = %v, want none: web's only domain is off", got)
	}
	if strings.Contains(captured.Snapshot.YAML, "retry.attempts") {
		t.Fatalf("zero-downtime labels for a service whose domains are off:\n%s", captured.Snapshot.YAML)
	}

	seedDomain(t, db, "web")
	captured, err = svc.Capture(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if got := captured.Snapshot.ZeroDowntime; !reflect.DeepEqual(got, []string{"web"}) {
		t.Fatalf("ZeroDowntime = %v, want [web] once one of its domains is on", got)
	}
}

func TestZeroDowntimeHTTPListSkipsOffDomain(t *testing.T) {
	svc, runner, _, db := newDeployService(t, zdYAML)
	seedDomainOff(t, db, "web")
	seedDomain(t, db, "db")
	runner.config = []byte(`{"services":{"web":{},"db":{"healthcheck":{"test":["CMD","true"]}},"worker":{}}}`)
	rec := serve(t, svc, http.MethodGet, zeroDowntimePath, "")
	var got []ZeroDowntimeStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("GET = %d %s", rec.Code, rec.Body)
	}
	if len(got) != 1 || got[0].Service != "db" {
		t.Fatalf("GET = %+v, want only db", got)
	}
}

// Labels for a Domain that is off are not the panel's to write, so the
// policy refuses them like any hand-written Traefik label.
func TestPolicyRefusesLabelsOfAnOffDomain(t *testing.T) {
	svc, runner, target, db := newDeployService(t, "services: {web: {image: nginx}}\n")
	if _, err := db.Exec(`INSERT INTO domains (compose_id, host, path, service, port, created_at, updated_at, enabled) VALUES (?, 'shop.example.com', '/', 'web', 80, 0, 0, 0)`, target.Compose.ID); err != nil {
		t.Fatal(err)
	}
	ds, err := svc.domains.ListForCompose(context.Background(), target.Compose.ID)
	if err != nil || len(ds) != 1 {
		t.Fatalf("domains = %v, %v", ds, err)
	}
	ds[0].Enabled = true // the labels it would get if it were on
	labels := map[string]any{}
	for key, value := range domains.Generated(target.Compose.ID, ds, dockernet.NetworkName, nil)["web"] {
		labels[key] = value
	}
	runner.config, _ = json.Marshal(map[string]any{"name": "relist-c1", "services": map[string]any{"web": map[string]any{"image": "nginx", "labels": labels}}})
	result, err := svc.EvaluatePolicy(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Violations) == 0 {
		t.Fatal("policy allowed the Traefik labels of a domain that is off")
	}
}

// A Service with hand-written Traefik routers keeps Traefik pinned to
// relist-network when its panel Domain is off: Inject no longer runs for it,
// so the join has to pin it, as it does for a Service with no Domain.
func TestOffDomainKeepsHandWrittenRoutersOnTheSharedNetwork(t *testing.T) {
	yamlContent := "services:\n  web:\n    image: nginx\n    labels: {traefik.enable: \"true\", traefik.http.routers.mine.rule: \"Host(`mine.example.com`)\"}\n"
	off := webDomain()
	off.Enabled = false
	services, _ := resolveServices(t, yamlContent, []domains.Domain{off}, testIdentity)
	labels := services["web"]["labels"].(map[string]any)
	if labels["traefik.docker.network"] != testNetwork {
		t.Fatalf("labels = %v, want traefik.docker.network=%s", labels, testNetwork)
	}
}

// A Service whose Domains are all off is told so, not that it has none.
func TestZeroDowntimeHTTPSetWhenDomainsAreOff(t *testing.T) {
	svc, _, _, db := newDeployService(t, zdYAML)
	seedDomainOff(t, db, "web")
	rec := serve(t, svc, http.MethodPut, zeroDowntimePath+"/web", `{"enabled":true}`)
	if rec.Code != http.StatusUnprocessableEntity || strings.TrimSpace(rec.Body.String()) != `{"error":"web has no domain that is on"}` {
		t.Fatalf("PUT = %d %s", rec.Code, rec.Body)
	}
}
