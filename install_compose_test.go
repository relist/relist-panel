package main

import (
	"bytes"
	"os"
	"slices"
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
