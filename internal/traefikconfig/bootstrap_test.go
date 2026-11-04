package traefikconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var errNoTraefikTest = errors.New("no traefik")

type fakeArgs struct {
	args []string
	err  error
}

func (f fakeArgs) Args(context.Context) ([]string, error) { return f.args, f.err }

func isNoTraefik(err error) bool { return errors.Is(err, errNoTraefikTest) }

func noEnv(string) string { return "" }

func bootstrap(t *testing.T, p Paths, a ArgsReader, getenv func(string) string) error {
	t.Helper()
	var log bytes.Buffer
	return Bootstrap(context.Background(), p, a, isNoTraefik, getenv, &log)
}

func bootstrapWarning(t *testing.T, p Paths) string {
	t.Helper()
	b, err := os.ReadFile(p.ApplyFile())
	if err != nil {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	s, _ := m["bootstrapWarning"].(string)
	return s
}

func TestBootstrapFromArgs(t *testing.T) {
	p := PathsFor(t.TempDir())
	if err := bootstrap(t, p, fakeArgs{args: args002(t)}, noEnv); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.Override()); err != nil {
		t.Fatalf("override: %v", err)
	}
	eff, err := os.ReadFile(p.Effective())
	if err != nil {
		t.Fatal(err)
	}
	if !AccessLogOn(eff) {
		t.Errorf("accessLog not carried over")
	}
}

func TestBootstrapRecordsUnconverted(t *testing.T) {
	p := PathsFor(t.TempDir())
	if err := bootstrap(t, p, fakeArgs{args: append(args002(t), "--weird")}, noEnv); err != nil {
		t.Fatal(err)
	}
	if w := bootstrapWarning(t, p); !strings.Contains(w, "--weird") {
		t.Fatalf("bootstrapWarning = %q", w)
	}
}

func TestBootstrapFresh(t *testing.T) {
	p := PathsFor(t.TempDir())
	env := func(k string) string {
		if k == "RELIST_ACME_EMAIL" {
			return "a@b.c"
		}
		return ""
	}
	if err := bootstrap(t, p, fakeArgs{err: errNoTraefikTest}, env); err != nil {
		t.Fatal(err)
	}
	ov, _ := os.ReadFile(p.Override())
	if !strings.Contains(string(ov), "a@b.c") {
		t.Errorf("override = %s", ov)
	}
	eff, _ := os.ReadFile(p.Effective())
	if AccessLogOn(eff) {
		t.Errorf("fresh install has the Access log on")
	}
}

// Compose recreates Traefik before it runs traefik-config (probe 2026-11-04),
// so an update from 0.0.2 sees only the new Traefik's --configfile. Its old
// flags came from .env, and its Access log was always on: files in access/
// say so.
func TestBootstrapUpdateKeepsAccessLogFromFiles(t *testing.T) {
	dataDir := t.TempDir()
	p := PathsFor(dataDir)
	os.MkdirAll(filepath.Join(dataDir, ".traefik", "access"), 0o755)
	os.WriteFile(filepath.Join(dataDir, ".traefik", "access", "access.log"), []byte("{}\n"), 0o644)
	env := func(k string) string {
		switch k {
		case "RELIST_ACME_EMAIL":
			return "ops@example.com"
		case "TRAEFIK_TRUSTED_IPS":
			return "10.0.0.0/8"
		}
		return ""
	}
	if err := bootstrap(t, p, fakeArgs{args: []string{"--configfile=/etc/traefik/traefik.yml"}}, env); err != nil {
		t.Fatal(err)
	}
	eff, _ := os.ReadFile(p.Effective())
	if !AccessLogOn(eff) {
		t.Errorf("Access log off although access/ has files")
	}
	if got := TrustedIPs(eff); len(got) != 1 || got[0] != "10.0.0.0/8" {
		t.Errorf("trustedIPs = %v", got)
	}
}

func TestBootstrapKeepsExistingOverride(t *testing.T) {
	p := PathsFor(t.TempDir())
	os.MkdirAll(p.Dir, 0o755)
	want := []byte("# mine\nlog: {level: WARN}\n")
	os.WriteFile(p.Override(), want, 0o644)
	if err := bootstrap(t, p, fakeArgs{args: args002(t)}, noEnv); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(p.Override()); !bytes.Equal(got, want) {
		t.Errorf("override changed to %q", got)
	}
	eff, _ := os.ReadFile(p.Effective())
	m, _ := parseMap(eff)
	if _, v := lookup(m, "log", "level"); v == nil || v.Value != "WARN" {
		t.Errorf("level = %v", v)
	}
}

// Every `up` runs traefik-config. When the file comes out the same, its mtime
// must not move: with no healthy apply recorded, pending restart compares the
// mtime with Traefik's start, and a touch alone would ask for a restart.
func TestBootstrapLeavesSameFileAlone(t *testing.T) {
	p := PathsFor(t.TempDir())
	os.MkdirAll(p.Dir, 0o755)
	os.WriteFile(p.Override(), []byte("log: {level: WARN}\n"), 0o644)
	if err := bootstrap(t, p, fakeArgs{err: errNoTraefikTest}, noEnv); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	os.Chtimes(p.Effective(), old, old)
	if err := bootstrap(t, p, fakeArgs{err: errNoTraefikTest}, noEnv); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(p.Effective()); !st.ModTime().Equal(old) {
		t.Errorf("traefik.yml rewritten: mtime %v, want %v", st.ModTime(), old)
	}
}

func TestBootstrapBadOverrideKeepsGenerated(t *testing.T) {
	p := PathsFor(t.TempDir())
	os.MkdirAll(p.Dir, 0o755)
	os.WriteFile(p.Override(), []byte("a: [oops\n"), 0o644)
	os.WriteFile(p.Effective(), []byte("X"), 0o644)
	if err := bootstrap(t, p, fakeArgs{err: errNoTraefikTest}, noEnv); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(p.Effective()); string(got) != "X" {
		t.Errorf("traefik.yml = %q", got)
	}
	if w := bootstrapWarning(t, p); !strings.Contains(w, "line 1") {
		t.Errorf("bootstrapWarning = %q", w)
	}
}

func TestBootstrapBadOverrideNoGenerated(t *testing.T) {
	p := PathsFor(t.TempDir())
	os.MkdirAll(p.Dir, 0o755)
	os.WriteFile(p.Override(), []byte("a: [oops\n"), 0o644)
	if err := bootstrap(t, p, fakeArgs{err: errNoTraefikTest}, noEnv); err == nil {
		t.Fatal("no error with no usable traefik.yml")
	}
}

func TestBootstrapArgsDockerDown(t *testing.T) {
	p := PathsFor(t.TempDir())
	if err := bootstrap(t, p, fakeArgs{err: errors.New("docker down")}, noEnv); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.Effective()); err != nil {
		t.Fatal(err)
	}
	if w := bootstrapWarning(t, p); !strings.Contains(w, "docker down") {
		t.Errorf("bootstrapWarning = %q", w)
	}
}

func TestBootstrapTraefikAlreadyOnConfigFile(t *testing.T) {
	// The override was deleted by hand while Traefik already reads the file:
	// its args say nothing, so start from .env instead.
	p := PathsFor(t.TempDir())
	env := func(k string) string {
		if k == "RELIST_ACME_EMAIL" {
			return "a@b.c"
		}
		return ""
	}
	if err := bootstrap(t, p, fakeArgs{args: []string{"--configfile=/etc/traefik/traefik.yml"}}, env); err != nil {
		t.Fatal(err)
	}
	if ov, _ := os.ReadFile(p.Override()); !strings.Contains(string(ov), "a@b.c") {
		t.Errorf("override = %s", ov)
	}
}

func TestBootstrapClearsOldWarning(t *testing.T) {
	p := PathsFor(t.TempDir())
	os.MkdirAll(p.Dir, 0o755)
	os.WriteFile(p.ApplyFile(), []byte(`{"bootstrapWarning":"old","state":"ok"}`), 0o644)
	os.WriteFile(p.Override(), []byte("log: {level: WARN}\n"), 0o644)
	if err := bootstrap(t, p, fakeArgs{err: errNoTraefikTest}, noEnv); err != nil {
		t.Fatal(err)
	}
	if w := bootstrapWarning(t, p); w != "" {
		t.Errorf("bootstrapWarning = %q", w)
	}
	b, _ := os.ReadFile(p.ApplyFile())
	if !strings.Contains(string(b), `"state":"ok"`) {
		t.Errorf("apply.json lost other fields: %s", b)
	}
}
