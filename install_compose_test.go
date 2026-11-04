package main

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Traefik's file provider reads its directory once at start and never
// retries, so the directory must exist before Traefik starts, not only once
// the panel's Reconcile creates it (ADR-0022). Docker creates a bind mount's
// host directory, so the install mounts it on its own.
func TestInstallComposeMountsTraefikDynamicDir(t *testing.T) {
	raw, err := os.ReadFile("docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Services map[string]struct {
			Command []string `yaml:"command"`
			Volumes []string `yaml:"volumes"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	// The file provider's flags live in the static config base now
	// (internal/traefikconfig/base.yml, ADR-0030).
	traefik := doc.Services["traefik"]
	if !slices.Contains(traefik.Volumes, "${RELIST_DATA_DIR:-/var/lib/relist-panel}/.traefik/dynamic:/letsencrypt/dynamic") {
		t.Errorf("traefik volumes = %v, want the dynamic directory mounted on its own", traefik.Volumes)
	}
}

// The binary embeds the Install file as the base for Panel update's change
// check. A build that forgot to re-embed it would compare against a stale copy.
func TestEmbeddedInstallFileMatchesRepoFile(t *testing.T) {
	raw, err := os.ReadFile("docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(installFile, raw) {
		t.Fatal("embedded Install file differs from docker-compose.yml; rebuild the binary")
	}
}

type composeDoc struct {
	Services map[string]struct {
		ContainerName string   `yaml:"container_name"`
		Image         string   `yaml:"image"`
		Build         any      `yaml:"build"`
		PullPolicy    string   `yaml:"pull_policy"`
		Command       []string `yaml:"command"`
		Logging       struct {
			Driver  string            `yaml:"driver"`
			Options map[string]string `yaml:"options"`
		} `yaml:"logging"`
	} `yaml:"services"`
}

func readComposeDoc(t *testing.T, path string) composeDoc {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc composeDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestInstallFileUsesPublishedImage(t *testing.T) {
	panel := readComposeDoc(t, "docker-compose.yml").Services["panel"]
	if want := "${RELIST_PANEL_IMAGE:-ghcr.io/relist/relist-panel:latest}"; panel.Image != want {
		t.Errorf("panel image = %q, want %q", panel.Image, want)
	}
	if panel.Build != nil {
		t.Errorf("panel has build: %v", panel.Build)
	}
	if panel.PullPolicy != "" {
		t.Errorf("panel has pull_policy %q", panel.PullPolicy)
	}
}

func TestInstallFileNamesContainers(t *testing.T) {
	doc := readComposeDoc(t, "docker-compose.yml")
	if got := doc.Services["panel"].ContainerName; got != "relist-panel" {
		t.Errorf("panel container_name = %q", got)
	}
	if got := doc.Services["traefik"].ContainerName; got != "relist-traefik" {
		t.Errorf("traefik container_name = %q", got)
	}
}

func TestOverrideExampleBuildsFromSource(t *testing.T) {
	panel := readComposeDoc(t, "docker-compose.override.example.yml").Services["panel"]
	if panel.Build != "." {
		t.Errorf("build = %v, want .", panel.Build)
	}
	if panel.Image != "relist-panel:local" {
		t.Errorf("image = %q", panel.Image)
	}
	if panel.PullPolicy != "build" {
		t.Errorf("pull_policy = %q", panel.PullPolicy)
	}
}

func TestGitignoreKeepsInstallLocalFiles(t *testing.T) {
	raw, err := os.ReadFile(".gitignore")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	for _, want := range []string{"docker-compose.override.yml", ".env"} {
		if !slices.Contains(lines, want) {
			t.Errorf(".gitignore lacks line %q", want)
		}
	}
}

// Traefik reads one generated file, which the one-shot traefik-config service
// writes before Traefik starts on every `up` (ADR-0030). Docker's json-file
// logs of the long-running containers are capped so stdout can never fill the
// disk (ADR-0027).
func TestInstallComposeTraefikStaticConfig(t *testing.T) {
	raw, err := os.ReadFile("docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Services map[string]struct {
			Image       string   `yaml:"image"`
			Command     []string `yaml:"command"`
			Restart     string   `yaml:"restart"`
			NetworkMode string   `yaml:"network_mode"`
			Volumes     []string `yaml:"volumes"`
			DependsOn   map[string]struct {
				Condition string `yaml:"condition"`
			} `yaml:"depends_on"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	traefik, cfg := doc.Services["traefik"], doc.Services["traefik-config"]
	if !slices.Equal(traefik.Command, []string{"--configfile=/etc/traefik/traefik.yml"}) {
		t.Errorf("traefik command = %q", traefik.Command)
	}
	if got := traefik.DependsOn["traefik-config"].Condition; got != "service_completed_successfully" {
		t.Errorf("traefik depends_on traefik-config condition = %q", got)
	}
	if !slices.Contains(traefik.Volumes, "${RELIST_DATA_DIR:-/var/lib/relist-panel}/.traefik/static:/etc/traefik:ro") {
		t.Errorf("traefik volumes = %v, want .traefik/static at /etc/traefik read-only", traefik.Volumes)
	}
	if !slices.Equal(cfg.Command, []string{"traefik-config"}) || cfg.Restart != "no" || cfg.NetworkMode != "none" {
		t.Errorf("traefik-config = command %q restart %q network_mode %q", cfg.Command, cfg.Restart, cfg.NetworkMode)
	}
	if cfg.Image != doc.Services["panel"].Image {
		t.Errorf("traefik-config image = %q, panel image = %q", cfg.Image, doc.Services["panel"].Image)
	}
	if !slices.Contains(cfg.Volumes, "/var/run/docker.sock:/var/run/docker.sock:ro") {
		t.Errorf("traefik-config volumes = %v, want the docker socket read-only", cfg.Volumes)
	}

	compose := readComposeDoc(t, "docker-compose.yml")
	for _, name := range []string{"traefik", "panel"} {
		logging := compose.Services[name].Logging
		if logging.Driver != "json-file" || !maps.Equal(logging.Options, map[string]string{"max-size": "10m", "max-file": "5"}) {
			t.Errorf("%s logging = %+v, want json-file max-size 10m max-file 5", name, logging)
		}
	}
}

// Every command and mount invariant a release states must hold for its own
// Install file, or a Panel update to it fails its own preflight.
func TestInvariantsHoldAgainstInstallFile(t *testing.T) {
	raw, err := os.ReadFile("release/invariants.json")
	if err != nil {
		t.Fatal(err)
	}
	var invariants []struct {
		Kind, Service, Arg, Source, Target string
		ReadOnly                           bool `json:"read_only"`
	}
	if err := json.Unmarshal(raw, &invariants); err != nil {
		t.Fatal(err)
	}
	compose, err := os.ReadFile("docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Services map[string]struct {
			Command []string `yaml:"command"`
			Volumes []string `yaml:"volumes"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(compose, &doc); err != nil {
		t.Fatal(err)
	}
	for _, inv := range invariants {
		svc := doc.Services[inv.Service]
		switch inv.Kind {
		case "service-command-has":
			if !slices.Contains(svc.Command, inv.Arg) {
				t.Errorf("%s command lacks %s", inv.Service, inv.Arg)
			}
		case "service-mount":
			want := inv.Source + ":" + inv.Target
			if inv.ReadOnly {
				want += ":ro"
			}
			if !slices.Contains(svc.Volumes, want) {
				t.Errorf("%s volumes lack %s", inv.Service, want)
			}
		}
	}
}
