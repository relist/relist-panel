package panelupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// localCompose runs `docker compose` on this machine, which stands in for the
// update Container: same paths, same Docker Compose.
type localCompose struct{}

func (localCompose) Run(ctx context.Context, argv []string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "docker", append([]string{"compose"}, argv...)...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return nil, &HelperExitError{Code: exit.ExitCode(), Stderr: stderr.String()}
		}
		return nil, err
	}
	return stdout.Bytes(), nil
}

const realInstallFile = `services:
  panel:
    image: ${RELIST_PANEL_IMAGE:-ghcr.io/relist/relist-panel:latest}
    environment:
      RELIST_DATA_DIR: /data
    volumes:
      - /data:/data
  web:
    image: nginx:alpine
`

const realOverride = `services:
  web:
    env_file: ./x.env
    volumes:
      - ./foo:/foo
`

// An override that uses paths relative to the Install directory passes the
// Preflight with real Docker Compose, and its Service is not recreated when
// nothing else changed.
func TestCheckRelativeOverridePathsWithRealCompose(t *testing.T) {
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skip("docker compose is not available")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	installPath := write("docker-compose.yml", realInstallFile)
	overridePath := write("docker-compose.override.yml", realOverride)
	write("x.env", "A=1\n")
	write(".env", "RELIST_PANEL_IMAGE="+fixtureImage+"\n")
	project := "su-test-preflight"

	// What "is running": the hashes Compose computes for the Install as it is.
	hashes := func(t *testing.T) []ServiceHash {
		out, err := exec.Command("docker", "compose", "-p", project, "--project-directory", dir,
			"-f", installPath, "-f", overridePath, "--env-file", filepath.Join(dir, ".env"), "config", "--hash", "*").Output()
		if err != nil {
			t.Fatal(err)
		}
		var running []ServiceHash
		for svc, hash := range parseHashes(out) {
			running = append(running, ServiceHash{svc, hash})
		}
		return running
	}

	pub, priv := newKey(t)
	rel := newRelease(t, priv)
	docker := &fakeDocker{hashes: hashes(t)}
	in := Install{Project: project, Dir: dir, ConfigFiles: []string{installPath, overridePath}}
	checker := &Checker{
		Version: "1.3.0", Key: pub, ManifestURL: rel.srv.URL + "/m.json", Docker: docker, Compose: localCompose{},
		DataDir: t.TempDir(), InstallFile: []byte(realInstallFile),
	}
	body, _ := json.Marshal(m{
		"schema": 1, "version": "1.4.0", "released_at": "2026-11-20T09:00:00Z", "image": fixtureImage,
		"notes_url": "https://example.test/n", "install_file": realInstallFile,
		"invariants": []m{
			{"kind": "service-image", "service": "panel", "image": fixtureImage},
			{"kind": "data-dir-same-path", "service": "panel"},
			{"kind": "service-mount", "service": "web", "source": filepath.Join(dir, "foo"), "target": "/foo"},
		},
	})
	rel.publish(body)
	rep, _, err := checker.Check(context.Background(), in)
	if err != nil || rep == nil {
		t.Fatalf("check = %v, %v", rep, err)
	}
	if rep.Failed != nil || !rep.Ready {
		t.Fatalf("report = %+v failure = %+v", rep, rep.Failed)
	}
	if len(rep.Recreated) != 0 {
		t.Fatalf("recreated = %v", rep.Recreated)
	}

	// A Service whose Container differs is listed.
	stale := hashes(t)
	for i := range stale {
		if stale[i].Service == "web" {
			stale[i].Hash = "stale"
		}
	}
	docker.hashes = stale
	if rep, _, _ = checker.Check(context.Background(), in); strings.Join(rep.Recreated, ",") != "web" {
		t.Fatalf("recreated = %v", rep.Recreated)
	}
}
