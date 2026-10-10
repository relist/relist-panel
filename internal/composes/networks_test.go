package composes

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"relist-panel/internal/domains"
)

const testNetwork = "relist-network"

var testIdentity = networkIdentity{DeployName: "relist-c12", Compose: "test", Environment: "Production", Project: "Hello"}

func webDomain() domains.Domain {
	return domains.Domain{ID: 3, Host: "api.example.com", Path: "/", InternalPath: "/", Service: "web", Port: 80, CertProvider: "none"}
}

// resolveServices resolves yamlContent and returns its services and warnings.
func resolveServices(t *testing.T, yamlContent string, ds []domains.Domain, id networkIdentity) (map[string]map[string]any, []string) {
	t.Helper()
	resolved, _, warnings, err := resolveCompose(yamlContent, testNetwork, "/data/.env", 12, ds, id, deployVariables{})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Services map[string]map[string]any `yaml:"services"`
		Networks map[string]any            `yaml:"networks"`
	}
	if err := yaml.Unmarshal([]byte(resolved), &doc); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(doc.Networks[testNetwork], map[string]any{"external": true}) {
		t.Fatalf("top-level networks = %v, want relist-network external", doc.Networks)
	}
	return doc.Services, warnings
}

func sharedAliases(t *testing.T, service map[string]any) []string {
	t.Helper()
	networks, ok := service["networks"].(map[string]any)
	if !ok {
		t.Fatalf("networks = %#v, want a map", service["networks"])
	}
	entry, ok := networks[testNetwork].(map[string]any)
	if !ok {
		t.Fatalf("networks = %#v, want relist-network with aliases", networks)
	}
	var aliases []string
	for _, alias := range entry["aliases"].([]any) {
		aliases = append(aliases, alias.(string))
	}
	return aliases
}

func TestOnlyADomainServiceJoinsTheSharedNetwork(t *testing.T) {
	services, warnings := resolveServices(t, "services:\n  web: {image: nginx}\n  db: {image: postgres}\n", []domains.Domain{webDomain()}, testIdentity)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}

	web := services["web"]
	if _, ok := web["networks"].(map[string]any)["default"]; !ok {
		t.Fatalf("web networks = %v, want default kept beside relist-network", web["networks"])
	}
	want := []string{"web.relist-c12.internal", "web.test.production.hello.internal"}
	if got := sharedAliases(t, web); !reflect.DeepEqual(got, want) {
		t.Fatalf("aliases = %v, want %v", got, want)
	}

	db := services["db"]
	if _, ok := db["networks"]; ok {
		t.Fatalf("db networks = %v, want none: db stays on the compose's default network", db["networks"])
	}
	if !reflect.DeepEqual(db["env_file"], []any{"/data/.env"}) {
		t.Fatalf("db env_file = %v, want the generated file", db["env_file"])
	}
}

func TestHandWrittenTraefikLabelsJoinAndPinTraefikToTheNetwork(t *testing.T) {
	yamlContent := `services:
  list:
    image: a
    labels: [traefik.enable=true]
  pinned:
    image: b
    labels: {traefik.enable: "true", traefik.docker.network: other}
  hostmode:
    image: c
    network_mode: host
    labels: [traefik.enable=true]
`
	services, _ := resolveServices(t, yamlContent, nil, testIdentity)

	if got := services["list"]["labels"]; !reflect.DeepEqual(got, []any{"traefik.enable=true", "traefik.docker.network=relist-network"}) {
		t.Fatalf("list labels = %v, want traefik.docker.network added in list form", got)
	}
	sharedAliases(t, services["list"])

	pinned := services["pinned"]["labels"].(map[string]any)
	if pinned["traefik.docker.network"] != "other" {
		t.Fatalf("pinned labels = %v, want the hand-written network left alone", pinned)
	}
	sharedAliases(t, services["pinned"])

	if _, ok := services["hostmode"]["networks"]; ok {
		t.Fatalf("hostmode networks = %v, want none with network_mode", services["hostmode"]["networks"])
	}
}

func TestExplicitSharedNetworkJoinsAndKeepsTheOperatorsEntries(t *testing.T) {
	yamlContent := `services:
  listed:
    image: a
    networks: [custom, relist-network]
  mapped:
    image: b
    networks:
      backend: {ipv4_address: 10.0.0.5}
      relist-network:
        aliases: [mapped.relist-c12.internal, legacy-name]
networks:
  custom: {}
  backend: {}
`
	services, _ := resolveServices(t, yamlContent, nil, testIdentity)

	listed := services["listed"]["networks"].(map[string]any)
	if _, ok := listed["custom"]; !ok {
		t.Fatalf("listed networks = %v, want custom kept", listed)
	}
	if _, ok := listed["default"]; ok {
		t.Fatalf("listed networks = %v, want no default added to an explicit list", listed)
	}
	if got := sharedAliases(t, services["listed"]); len(got) != 2 {
		t.Fatalf("listed aliases = %v", got)
	}

	mapped := services["mapped"]["networks"].(map[string]any)
	if !reflect.DeepEqual(mapped["backend"], map[string]any{"ipv4_address": "10.0.0.5"}) {
		t.Fatalf("mapped backend = %v, want it untouched", mapped["backend"])
	}
	want := []string{"mapped.relist-c12.internal", "legacy-name", "mapped.test.production.hello.internal"}
	if got := sharedAliases(t, services["mapped"]); !reflect.DeepEqual(got, want) {
		t.Fatalf("mapped aliases = %v, want %v", got, want)
	}
}

func TestLegacyComposeStableAliasUsesItsFrozenName(t *testing.T) {
	id := networkIdentity{DeployName: "hello-production-test", Compose: "renamed", Environment: "production", Project: "hello"}
	services, _ := resolveServices(t, "services:\n  web: {image: nginx}\n", []domains.Domain{webDomain()}, id)
	want := []string{"web.hello-production-test.internal", "web.renamed.production.hello.internal"}
	if got := sharedAliases(t, services["web"]); !reflect.DeepEqual(got, want) {
		t.Fatalf("aliases = %v, want %v", got, want)
	}
}

func TestReadableAliasSkippedWhenANameHasNoDNSCharacters(t *testing.T) {
	id := testIdentity
	id.Project = "โปรเจค"
	services, warnings := resolveServices(t, "services:\n  web: {image: nginx}\n", []domains.Domain{webDomain()}, id)
	if got := sharedAliases(t, services["web"]); !reflect.DeepEqual(got, []string{"web.relist-c12.internal"}) {
		t.Fatalf("aliases = %v, want only the stable one", got)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "no readable relist-network aliases") || !strings.Contains(warnings[0], "project name") {
		t.Fatalf("warnings = %v", warnings)
	}
}

func TestReadableAliasNameProblemWarnsOncePerCompose(t *testing.T) {
	id := testIdentity
	id.Environment = "ทดสอบ"
	yamlContent := "services:\n  web: {image: a, labels: [traefik.enable=true]}\n  api: {image: b, networks: [relist-network]}\n  db: {image: c}\n"
	services, warnings := resolveServices(t, yamlContent, nil, id)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "environment name") {
		t.Fatalf("warnings = %v, want exactly one for the compose", warnings)
	}
	for _, name := range []string{"web", "api"} {
		if got := sharedAliases(t, services[name]); len(got) != 1 || !strings.HasSuffix(got[0], ".relist-c12.internal") {
			t.Fatalf("%s aliases = %v, want only the stable one", name, got)
		}
	}
}

func TestReadableAliasSkippedWhenALabelIsTooLong(t *testing.T) {
	id := testIdentity
	id.Compose = strings.Repeat("a", 64)
	services, warnings := resolveServices(t, "services:\n  web: {image: nginx}\n", []domains.Domain{webDomain()}, id)
	if got := sharedAliases(t, services["web"]); len(got) != 1 {
		t.Fatalf("aliases = %v, want only the stable one", got)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "longer than 63") {
		t.Fatalf("warnings = %v", warnings)
	}
}

func TestServicesThatSlugifyAlikeGetNoAliases(t *testing.T) {
	yamlContent := "services:\n  my_db: {image: a, networks: [relist-network]}\n  my-db: {image: b, networks: [relist-network]}\n"
	services, warnings := resolveServices(t, yamlContent, nil, testIdentity)
	for _, name := range []string{"my_db", "my-db"} {
		networks := services[name]["networks"].(map[string]any)
		if entry, ok := networks[testNetwork]; !ok || entry != nil {
			t.Fatalf("%s networks = %v, want relist-network with no aliases", name, networks)
		}
	}
	if len(warnings) != 2 || !strings.Contains(warnings[0], "no relist-network aliases") {
		t.Fatalf("warnings = %v", warnings)
	}
}
