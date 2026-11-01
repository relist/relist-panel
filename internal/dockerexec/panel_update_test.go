package dockerexec_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"relist-panel/internal/dockerexec"
	"relist-panel/internal/panelupdate"
)

func TestPanelSelfInspectsHostname(t *testing.T) {
	var path string
	socket := newFakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		path = req.URL.Path
		_, _ = w.Write([]byte(`{"Image":"sha256:abc","Config":{"Labels":{"com.docker.compose.project":"p"}}}`))
	}))
	got, err := dockerexec.NewPanelSelfNamed(socket, "deadbeef").InspectSelf(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := panelupdate.Self{ImageID: "sha256:abc", Labels: map[string]string{"com.docker.compose.project": "p"}}
	if path != "/containers/deadbeef/json" || !reflect.DeepEqual(got, want) {
		t.Fatalf("path %q, self %+v", path, got)
	}
}

func TestPanelSelfErrors(t *testing.T) {
	socket := newFakeDaemon(t, http.NotFoundHandler())
	if _, err := dockerexec.NewPanelSelfNamed(socket, "x").InspectSelf(context.Background()); err == nil {
		t.Fatal("want an error for an unknown container")
	}
	if _, err := dockerexec.NewPanelSelfNamed(socket, "").InspectSelf(context.Background()); err == nil {
		t.Fatal("want an error without a hostname")
	}
}

func TestHelperArgvMountsOnlyTheInstallReadOnly(t *testing.T) {
	got := dockerexec.HelperArgv(panelupdate.HelperRun{Image: "sha256:abc", Dir: "/srv/app", Argv: []string{"sh", "-c", "cat", "sh", "/srv/app/x"}})
	line := strings.Join(got, " ")
	for _, want := range []string{"run --rm", "--network none", "--read-only", "--cap-drop ALL", "-v /srv/app:/srv/app:ro", "--entrypoint sh sha256:abc -c cat sh /srv/app/x"} {
		if !strings.Contains(line, want) {
			t.Errorf("argv %q lacks %q", line, want)
		}
	}
	if strings.Count(line, " -v ") != 1 || strings.Contains(line, "docker.sock") {
		t.Errorf("argv %q mounts more than the Install dir", line)
	}
}

func TestPanelHelperRun(t *testing.T) {
	h := dockerexec.NewPanelHelperWith(func(_ context.Context, _ []string, _ io.Reader, stdout, stderr io.Writer) error {
		_, _ = stdout.Write([]byte("out"))
		_, _ = stderr.Write([]byte("boom\n"))
		return exec.Command("sh", "-c", "exit 44").Run()
	})
	var out strings.Builder
	err := h.Run(context.Background(), panelupdate.HelperRun{Image: "i", Dir: "/d", Argv: []string{"sh"}}, &out)
	var exit *panelupdate.HelperExitError
	if !errors.As(err, &exit) || exit.Code != 44 || exit.Stderr != "boom" || out.String() != "out" {
		t.Fatalf("err %v, out %q", err, out.String())
	}
}

// Real Docker: reads a file through a helper of an image the host has.
// Opt in with RELIST_DOCKER_TESTS=1; RELIST_TEST_IMAGE names the image
// (default alpine:3). Nothing persists: --rm, and the directory is temporary.
func TestPanelHelperRealDocker(t *testing.T) {
	if os.Getenv("RELIST_DOCKER_TESTS") != "1" {
		t.Skip("set RELIST_DOCKER_TESTS=1 to run against Docker")
	}
	image := os.Getenv("RELIST_TEST_IMAGE")
	if image == "" {
		image = "alpine:3"
	}
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/su-test.yml", []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	h := dockerexec.NewPanelHelper()
	script := []string{"sh", "-c", `[ -f "$1" ] || exit 44; exec cat -- "$1"`, "sh"}
	if err := h.Run(context.Background(), panelupdate.HelperRun{Image: image, Dir: dir, Argv: append(script, dir+"/su-test.yml")}, &out); err != nil || out.String() != "services: {}\n" {
		t.Fatalf("read: %v %q", err, out.String())
	}
	err := h.Run(context.Background(), panelupdate.HelperRun{Image: image, Dir: dir, Argv: append(script, dir+"/.env")}, io.Discard)
	var exit *panelupdate.HelperExitError
	if !errors.As(err, &exit) || exit.Code != 44 {
		t.Fatalf("missing file: %v", err)
	}
}
