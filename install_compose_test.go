package main

import (
	"bytes"
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
	traefik := doc.Services["traefik"]
	for _, flag := range []string{"--providers.file.directory=/letsencrypt/dynamic", "--providers.file.watch=true"} {
		if !slices.Contains(traefik.Command, flag) {
			t.Errorf("traefik command lacks %s", flag)
		}
	}
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

// Traefik's own log and the Access log (ADR-0027): the Access log goes to a
// file in the data directory that the panel rotates, and Docker's json-file
// logs of both containers are capped so stdout can never fill the disk.
func TestInstallComposeTraefikLogging(t *testing.T) {
	doc := readComposeDoc(t, "docker-compose.yml")
	traefik := doc.Services["traefik"]
	for _, flag := range []string{
		"--log.level=INFO",
		"--log.nocolor=true",
		"--accesslog=true",
		"--accesslog.filepath=/letsencrypt/access/access.log",
		"--accesslog.format=json",
		"--accesslog.fields.headers.defaultmode=drop",
		"--accesslog.fields.headers.names.User-Agent=keep",
		"--entrypoints.web.forwardedheaders.trustedips=${TRAEFIK_TRUSTED_IPS:-}",
		"--entrypoints.websecure.forwardedheaders.trustedips=${TRAEFIK_TRUSTED_IPS:-}",
	} {
		if !slices.Contains(traefik.Command, flag) {
			t.Errorf("traefik command lacks %s", flag)
		}
	}
	for _, name := range []string{"traefik", "panel"} {
		logging := doc.Services[name].Logging
		if logging.Driver != "json-file" || !maps.Equal(logging.Options, map[string]string{"max-size": "10m", "max-file": "5"}) {
			t.Errorf("%s logging = %+v, want json-file max-size 10m max-file 5", name, logging)
		}
	}
}
