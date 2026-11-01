package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runSetupTest(args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	code = runSetup(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestSetupWritesFiles(t *testing.T) {
	dir := t.TempDir()
	code, stdout, stderr := runSetupTest("--dir", dir, "--domain", "panel.example.com", "--image", "ghcr.io/relist/relist-panel@sha256:abc", "--acme-email", "me@example.com")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, dir) {
		t.Errorf("stdout = %q", stdout)
	}
	compose, err := os.ReadFile(filepath.Join(dir, "docker-compose.yml"))
	if err != nil || !bytes.Equal(compose, installFile) {
		t.Fatalf("docker-compose.yml differs from the embedded Install file: %v", err)
	}
	env, _ := os.ReadFile(filepath.Join(dir, ".env"))
	want := "RELIST_DOMAIN=panel.example.com\nRELIST_ACME_EMAIL=me@example.com\nRELIST_PANEL_IMAGE=ghcr.io/relist/relist-panel@sha256:abc\nCOMPOSE_PROJECT_NAME=relist-panel\n"
	if string(env) != want {
		t.Fatalf(".env = %q", env)
	}
	for name, mode := range map[string]os.FileMode{"docker-compose.yml": 0o644, ".env": 0o600} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || info.Mode().Perm() != mode {
			t.Errorf("%s mode = %v, %v; want %v", name, info.Mode().Perm(), err, mode)
		}
	}
}

func TestSetupEmptyAcmeEmail(t *testing.T) {
	dir := t.TempDir()
	if code, _, stderr := runSetupTest("--dir", dir, "--domain", "panel.example.com", "--image", "img"); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	env, _ := os.ReadFile(filepath.Join(dir, ".env"))
	if !strings.Contains(string(env), "RELIST_ACME_EMAIL=\n") {
		t.Fatalf(".env = %q", env)
	}
}

func TestSetupRefusesExisting(t *testing.T) {
	for _, existing := range []string{".env", "docker-compose.yml"} {
		t.Run(existing, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, existing)
			if err := os.WriteFile(path, []byte("mine\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			code, _, stderr := runSetupTest("--dir", dir, "--domain", "panel.example.com", "--image", "img")
			if code != 1 || !strings.Contains(stderr, "already exists") {
				t.Fatalf("code = %d, stderr = %q", code, stderr)
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != 1 {
				t.Fatalf("wrote into a directory with %s: %v", existing, entries)
			}
			if got, _ := os.ReadFile(path); string(got) != "mine\n" {
				t.Fatalf("%s was overwritten: %q", existing, got)
			}
		})
	}
}

func TestSetupRefusesMissingDir(t *testing.T) {
	code, _, _ := runSetupTest("--dir", filepath.Join(t.TempDir(), "nope"), "--domain", "d.example.com", "--image", "img")
	if code != 1 {
		t.Fatalf("code = %d", code)
	}
}

func TestSetupUsage(t *testing.T) {
	dir := t.TempDir()
	cases := map[string][]string{
		"no arguments":    nil,
		"no domain":       {"--dir", dir, "--image", "img"},
		"no image":        {"--dir", dir, "--domain", "d.example.com"},
		"no dir":          {"--domain", "d.example.com", "--image", "img"},
		"relative dir":    {"--dir", "x", "--domain", "d.example.com", "--image", "img"},
		"newline domain":  {"--dir", dir, "--domain", "d.example.com\nEVIL=1", "--image", "img"},
		"newline image":   {"--dir", dir, "--domain", "d.example.com", "--image", "img\nEVIL=1"},
		"newline email":   {"--dir", dir, "--domain", "d.example.com", "--image", "img", "--acme-email", "a@b.c\nEVIL=1"},
		"dollar in value": {"--dir", dir, "--domain", "d.example.com", "--image", "img", "--acme-email", "a$b@c.d"},
		"extra argument":  {"--dir", dir, "--domain", "d.example.com", "--image", "img", "oops"},
		"unknown flag":    {"--dir", dir, "--domain", "d.example.com", "--image", "img", "--bogus"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			code, _, _ := runSetupTest(args...)
			if code != 2 {
				t.Fatalf("code = %d, want 2", code)
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Fatalf("wrote files: %v", entries)
			}
		})
	}
}
