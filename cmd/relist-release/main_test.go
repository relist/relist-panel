package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"relist-panel/internal/panelupdate"
)

const testDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

var fixedNow = func() time.Time { return time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC) }

type env struct {
	dir string
	pub string
	key string
}

func newEnv(t *testing.T) env {
	t.Helper()
	var out bytes.Buffer
	if err := run([]string{"keygen"}, &out, os.Getenv, fixedNow); err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`: (\S+)\n`)
	m := re.FindAllStringSubmatch(out.String(), -1)
	if len(m) != 2 {
		t.Fatalf("keygen output: %q", out.String())
	}
	dir := t.TempDir()
	write := func(name, body string) { os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644) }
	write("compose.yml", "services:\n  panel:\n    image: x\n")
	write("inv.json", `[{"kind":"service-image","service":"panel"},{"kind":"network-external","network":"n","name":"n"}]`)
	write("steps.json", `[{"id":"open-443","text":"Open port 443."}]`)
	return env{dir: dir, key: m[0][1], pub: m[1][1]}
}

func (e env) path(n string) string { return filepath.Join(e.dir, n) }

func (e env) manifest(t *testing.T) error {
	return run([]string{"manifest", "--version", "1.4.0", "--digest", testDigest,
		"--compose", e.path("compose.yml"), "--invariants", e.path("inv.json"),
		"--manual-steps", e.path("steps.json"), "--out", e.path("m.json")}, &bytes.Buffer{}, os.Getenv, fixedNow)
}

func (e env) sign() error {
	getenv := func(k string) string {
		if k == keyEnv {
			return e.key
		}
		return ""
	}
	return run([]string{"sign", "--manifest", e.path("m.json")}, &bytes.Buffer{}, getenv, fixedNow)
}

func (e env) verify() error {
	return run([]string{"verify", "--manifest", e.path("m.json"), "--key", e.pub}, &bytes.Buffer{}, os.Getenv, fixedNow)
}

func TestKeygenMakesAKeyPair(t *testing.T) {
	e := newEnv(t)
	priv, err := decodePrivate(e.key)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := panelupdate.DecodeKey(e.pub)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(priv.Public().(ed25519.PublicKey), pub) {
		t.Fatal("the public key is not the private key's half")
	}
}

func TestManifestSignVerifyRoundTrip(t *testing.T) {
	e := newEnv(t)
	if err := e.manifest(t); err != nil {
		t.Fatal(err)
	}
	if err := e.sign(); err != nil {
		t.Fatal(err)
	}
	if err := e.verify(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(e.path("m.json"))
	sig, _ := os.ReadFile(e.path("m.json.sig"))
	key, _ := panelupdate.DecodeKey(e.pub)
	m, err := panelupdate.ParseManifest(data, sig, key)
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "1.4.0" || m.Image != "ghcr.io/relist/relist-panel@"+testDigest ||
		!m.ReleasedAt.Equal(fixedNow()) || m.NotesURL != "https://github.com/relist/relist-panel/releases/tag/v1.4.0" ||
		m.InstallFile != "services:\n  panel:\n    image: x\n" ||
		len(m.Invariants) != 2 || m.Invariants[0].Kind != "service-image" ||
		len(m.ManualSteps) != 1 || m.ManualSteps[0].ID != "open-443" {
		t.Fatalf("manifest = %+v", m)
	}
}

func TestVerifyRejectsATamperedManifest(t *testing.T) {
	e := newEnv(t)
	if err := e.manifest(t); err != nil {
		t.Fatal(err)
	}
	if err := e.sign(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(e.path("m.json"))
	data[len(data)/2] ^= 1
	os.WriteFile(e.path("m.json"), data, 0o644)
	if err := e.verify(); err == nil {
		t.Fatal("a tampered manifest verified")
	}
}

func TestVerifyRejectsAnotherKey(t *testing.T) {
	e := newEnv(t)
	other := newEnv(t)
	if err := e.manifest(t); err != nil {
		t.Fatal(err)
	}
	if err := e.sign(); err != nil {
		t.Fatal(err)
	}
	e.pub = other.pub
	if err := e.verify(); err == nil {
		t.Fatal("a manifest verified under the wrong key")
	}
}

func TestManifestRejectsAnUnknownInvariantKind(t *testing.T) {
	e := newEnv(t)
	os.WriteFile(e.path("inv.json"), []byte(`[{"kind":"service-image","service":"panel"},{"kind":"made-up"}]`), 0o644)
	err := e.manifest(t)
	if err == nil || !strings.Contains(err.Error(), "made-up") {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(e.path("m.json")); statErr == nil {
		t.Fatal("a manifest was written")
	}
}

func TestSignNeedsTheKey(t *testing.T) {
	e := newEnv(t)
	if err := e.manifest(t); err != nil {
		t.Fatal(err)
	}
	e.key = base64.StdEncoding.EncodeToString([]byte("short"))
	if err := e.sign(); err == nil {
		t.Fatal("signed with a bad key")
	}
}

// The files the workflow feeds to manifest must pass it.
func TestRepoReleaseFilesAreValid(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "m.json")
	err := run([]string{"manifest", "--version", "1.0.0", "--digest", testDigest,
		"--compose", "../../docker-compose.yml", "--invariants", "../../release/invariants.json",
		"--manual-steps", "../../release/manual-steps.json", "--out", out}, &bytes.Buffer{}, os.Getenv, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
}

// Every action is pinned by a full commit SHA, and the file is valid YAML.
func TestReleaseWorkflowPinsActions(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		Jobs map[string]struct {
			Steps []struct {
				Uses string `yaml:"uses"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatal(err)
	}
	pin := regexp.MustCompile(`^[\w.-]+/[\w.-]+@[0-9a-f]{40}$`)
	uses := 0
	for _, job := range wf.Jobs {
		for _, s := range job.Steps {
			if s.Uses == "" {
				continue
			}
			uses++
			if !pin.MatchString(s.Uses) {
				t.Errorf("action %q is not pinned by commit SHA", s.Uses)
			}
		}
	}
	if uses == 0 {
		t.Fatal("no actions found")
	}
}
