package dockerexec_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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

func TestHelperArgvMountsTheScratchDirReadOnly(t *testing.T) {
	got := dockerexec.HelperArgv(panelupdate.HelperRun{Image: "sha256:abc", Dir: "/srv/app", Scratch: "/data/.panel-update/preflight/x", Argv: []string{"docker", "compose", "config"}})
	line := strings.Join(got, " ")
	for _, want := range []string{"-v /srv/app:/srv/app:ro", "-v /data/.panel-update/preflight/x:/data/.panel-update/preflight/x:ro", "--network none", "--read-only", "--cap-drop ALL", "--entrypoint docker sha256:abc compose config"} {
		if !strings.Contains(line, want) {
			t.Errorf("argv %q lacks %q", line, want)
		}
	}
	if strings.Count(line, " -v ") != 2 || strings.Contains(line, "docker.sock") || strings.Contains(line, ":rw") {
		t.Errorf("argv %q mounts more than two read-only directories", line)
	}
}

func TestPanelDockerPullsByDigest(t *testing.T) {
	var method, query string
	socket := newFakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		method, query = req.Method+" "+req.URL.Path, req.URL.RawQuery
		_, _ = w.Write([]byte(`{"status":"Pulling from relist/relist-panel"}` + "\n" + `{"status":"Digest: sha256:abc"}` + "\n"))
	}))
	err := dockerexec.NewPanelDockerAt(socket).Pull(context.Background(), "ghcr.io/relist/relist-panel@sha256:abc")
	if err != nil {
		t.Fatal(err)
	}
	if method != "POST /images/create" || query != "fromImage=ghcr.io%2Frelist%2Frelist-panel&tag=sha256%3Aabc" {
		t.Fatalf("request = %s ?%s", method, query)
	}
}

func TestPanelDockerPullReportsDockersWords(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
		want   string
	}{
		"error in the stream": {200, `{"status":"Pulling"}` + "\n" + `{"error":"manifest unknown","errorDetail":{"message":"manifest unknown"}}` + "\n", "manifest unknown"},
		"http error":          {500, `{"message":"unauthorized: authentication required"}`, "unauthorized: authentication required"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			socket := newFakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			}))
			err := dockerexec.NewPanelDockerAt(socket).Pull(context.Background(), "r/i@sha256:abc")
			if err == nil || err.Error() != c.want {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
}

func TestPanelDockerConfigHashes(t *testing.T) {
	var query string
	socket := newFakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		query = req.URL.Query().Get("filters")
		_, _ = w.Write([]byte(`[{"Labels":{"com.docker.compose.service":"panel","com.docker.compose.config-hash":"h1"}},{"Labels":{"com.docker.compose.service":"traefik"}}]`))
	}))
	got, err := dockerexec.NewPanelDockerAt(socket).ConfigHashes(context.Background(), "relist-panel")
	if err != nil {
		t.Fatal(err)
	}
	want := []panelupdate.ServiceHash{{Service: "panel", Hash: "h1"}, {Service: "traefik"}}
	if !reflect.DeepEqual(got, want) || !strings.Contains(query, "com.docker.compose.project=relist-panel") {
		t.Fatalf("hashes = %+v filters %s", got, query)
	}
}

func TestPanelDockerUpdaterState(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
		want   panelupdate.UpdaterState
	}{
		"running": {200, `{"State":{"Status":"running"}}`, panelupdate.UpdaterState{Exists: true, Running: true}},
		"created": {200, `{"State":{"Status":"created"}}`, panelupdate.UpdaterState{Exists: true, Running: true}},
		"exited":  {200, `{"State":{"Status":"exited"}}`, panelupdate.UpdaterState{Exists: true}},
		"gone":    {404, `{"message":"No such container"}`, panelupdate.UpdaterState{}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			var path string
			socket := newFakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				path = req.URL.Path
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			}))
			got, err := dockerexec.NewPanelDockerAt(socket).UpdaterState(context.Background(), "relist-panel-updater-20261101T120000Z")
			if err != nil || got != c.want || path != "/containers/relist-panel-updater-20261101T120000Z/json" {
				t.Fatalf("got %+v, %v at %s", got, err, path)
			}
		})
	}
	socket := newFakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }))
	if _, err := dockerexec.NewPanelDockerAt(socket).UpdaterState(context.Background(), "x"); err == nil {
		t.Fatal("a daemon error must not read as a missing Container")
	}
}

func TestPanelDockerStartUpdaterBody(t *testing.T) {
	var created map[string]any
	var name, started string
	socket := newFakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case strings.HasSuffix(req.URL.Path, "/containers/create"):
			name = req.URL.Query().Get("name")
			_ = json.NewDecoder(req.Body).Decode(&created)
			_, _ = w.Write([]byte(`{"Id":"c1"}`))
		case strings.HasSuffix(req.URL.Path, "/start"):
			started = req.URL.Path
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	err := dockerexec.NewPanelDocker(socket).StartUpdater(context.Background(), panelupdate.UpdaterSpec{
		Name: "relist-panel-updater-20261101T120000Z", Image: "sha256:abc", Run: "20261101T120000Z",
		InstallDir: "/srv/relist", DataDir: "/var/lib/relist-panel", Network: "relist-network",
	})
	if err != nil {
		t.Fatal(err)
	}
	host := created["HostConfig"].(map[string]any)
	if name != "relist-panel-updater-20261101T120000Z" || started != "/containers/c1/start" || created["Image"] != "sha256:abc" ||
		!reflect.DeepEqual(created["Cmd"], []any{"panel-update", "apply", "--run", "20261101T120000Z"}) ||
		!reflect.DeepEqual(created["Env"], []any{"RELIST_DATA_DIR=/var/lib/relist-panel"}) ||
		host["NetworkMode"] != "relist-network" ||
		!reflect.DeepEqual(host["Binds"], []any{"/var/run/docker.sock:/var/run/docker.sock", "/srv/relist:/srv/relist", "/var/lib/relist-panel:/var/lib/relist-panel"}) {
		t.Fatalf("name %q created %+v", name, created)
	}
	// No Compose labels, so `up` never sees an orphan; nothing restarts it or
	// removes it on exit.
	if _, ok := created["Labels"]; ok || host["RestartPolicy"] != nil || host["AutoRemove"] != nil {
		t.Fatalf("created %+v", created)
	}
}

func TestPanelDockerStartUpdaterRemovesWhatDidNotStart(t *testing.T) {
	var deleted string
	socket := newFakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodDelete:
			deleted = req.URL.Path
		case strings.HasSuffix(req.URL.Path, "/containers/create"):
			_, _ = w.Write([]byte(`{"Id":"c1"}`))
		default:
			http.Error(w, `{"message":"port is already allocated"}`, http.StatusInternalServerError)
		}
	}))
	err := dockerexec.NewPanelDocker(socket).StartUpdater(context.Background(), panelupdate.UpdaterSpec{Name: "n", Image: "i", Run: "r"})
	if err == nil || deleted != "/containers/c1" {
		t.Fatalf("err %v deleted %q", err, deleted)
	}
}

func TestPanelDockerListUpdatersKeepsOnlyThePrefix(t *testing.T) {
	socket := newFakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte(`[
			{"Names":["/relist-panel-updater-20261101T120000Z"],"State":"exited"},
			{"Names":["/relist-panel-updater-20261102T120000Z"],"State":"running"},
			{"Names":["/not-relist-panel-updater-x"],"State":"running"}]`))
	}))
	got, err := dockerexec.NewPanelDocker(socket).ListUpdaters(context.Background())
	want := []panelupdate.UpdaterListing{
		{Name: "relist-panel-updater-20261101T120000Z"},
		{Name: "relist-panel-updater-20261102T120000Z", Running: true},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestPanelDockerImageID(t *testing.T) {
	var path string
	socket := newFakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		path = req.URL.Path
		_, _ = w.Write([]byte(`{"Id":"sha256:cfg"}`))
	}))
	id, err := dockerexec.NewPanelDocker(socket).ImageID(context.Background(), "ghcr.io/x/y@sha256:abc")
	if err != nil || id != "sha256:cfg" || path != "/images/ghcr.io/x/y@sha256:abc/json" {
		t.Fatalf("id %q err %v path %q", id, err, path)
	}
}

func TestUpdaterHostContainersByComposeLabels(t *testing.T) {
	var filters string
	socket := newFakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		filters = req.URL.Query().Get("filters")
		_, _ = w.Write([]byte(`[{"Id":"t1","ImageID":"sha256:tr","State":"running","NetworkSettings":{"Networks":{"relist-network":{"IPAddress":"172.20.0.2"}}}}]`))
	}))
	got, err := dockerexec.NewPanelUpdaterHost(socket).Containers(context.Background(), "relist-panel", "traefik")
	want := []panelupdate.ContainerInfo{{ID: "t1", ImageID: "sha256:tr", Running: true, IPs: map[string]string{"relist-network": "172.20.0.2"}}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, %v", got, err)
	}
	for _, label := range []string{"com.docker.compose.project=relist-panel", "com.docker.compose.service=traefik"} {
		if !strings.Contains(filters, label) {
			t.Fatalf("filters %q lack %q", filters, label)
		}
	}
}

func TestUpdaterHostComposeArgs(t *testing.T) {
	var got [][]string
	h := dockerexec.NewPanelUpdaterHostWith(func(_ context.Context, args []string) ([]byte, error) {
		got = append(got, args)
		return []byte("boom"), nil
	})
	plan := panelupdate.Plan{Project: "relist-panel", InstallDir: "/srv/relist", ConfigFiles: []string{"/srv/relist/a.yml", "/srv/relist/b.yml"}}
	if err := h.ComposeUp(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if err := h.ComposeStop(context.Background(), plan, "panel"); err != nil {
		t.Fatal(err)
	}
	prefix := "-p relist-panel --project-directory /srv/relist -f /srv/relist/a.yml -f /srv/relist/b.yml "
	if strings.Join(got[0], " ") != prefix+"up -d" || strings.Join(got[1], " ") != prefix+"stop panel" {
		t.Fatalf("args %q", got)
	}
}

func TestUpdaterHostHealthzSendsHostAndSNI(t *testing.T) {
	var host, sni string
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		host = req.Host
		if req.URL.Path != "/healthz" {
			t.Errorf("path %q", req.URL.Path)
		}
		_, _ = w.Write([]byte("ok"))
	}))
	srv.TLS = &tls.Config{GetCertificate: func(h *tls.ClientHelloInfo) (*tls.Certificate, error) {
		sni = h.ServerName
		return nil, nil
	}}
	srv.StartTLS()
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "https://")
	if err := dockerexec.NewPanelUpdaterHost("").Healthz(context.Background(), addr, "panel.example.com"); err != nil {
		t.Fatal(err)
	}
	if host != "panel.example.com" || sni != "panel.example.com" {
		t.Fatalf("host %q sni %q", host, sni)
	}
}
