package panelupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// localHelper runs a helper command on this machine, which stands in for the
// helper Container: same paths, same Docker Compose.
type localHelper struct{}

func (localHelper) Run(ctx context.Context, run HelperRun, stdout io.Writer) error {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, run.Argv[0], run.Argv[1:]...)
	cmd.Stdout, cmd.Stderr = stdout, &stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if ok := asExit(err, &exit); ok {
			return &HelperExitError{Code: exit.ExitCode(), Stderr: stderr.String()}
		}
		return err
	}
	return nil
}

func asExit(err error, target **exec.ExitError) bool {
	e, ok := err.(*exec.ExitError)
	*target = e
	return ok
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
func TestPreflightRelativeOverridePathsWithRealCompose(t *testing.T) {
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
	svc := New(Deps{
		Store: NewStore(newDB(t)), Version: "1.3.0", Key: pub, ManifestURL: rel.srv.URL + "/m.json",
		Self: fakeSelf{self: Self{ImageID: "sha256:img", Labels: map[string]string{
			labelProject: project, labelWorkingDir: dir, labelConfigFiles: installPath + "," + overridePath,
		}}},
		Helper: localHelper{}, Docker: docker, DataDir: t.TempDir(), InstallFile: []byte(realInstallFile),
	})
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
	if err := svc.CheckNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	rep, err := svc.Preflight(context.Background())
	if err != nil || rep == nil {
		t.Fatalf("preflight = %v, %v", rep, err)
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
	if rep, _ = svc.Preflight(context.Background()); strings.Join(rep.Recreated, ",") != "web" {
		t.Fatalf("recreated = %v", rep.Recreated)
	}
}
