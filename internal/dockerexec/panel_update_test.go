package dockerexec_test

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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

func TestUpdateSpecArgv(t *testing.T) {
	spec := panelupdate.UpdateSpec{
		Name: "relist-panel-updater-20261101T120000Z", Image: "sha256:abc", InstallDir: "/opt/relist-panel",
		DataDir: "/var/lib/relist-panel", Network: "relist-network",
		Args: []string{"--report", "id", "--run", "20261101T120000Z"},
		Env:  []string{"RELIST_UPDATE_MANIFEST_URL=http://registry.test/m.json"},
	}
	mounts := "--network relist-network -v /var/run/docker.sock:/var/run/docker.sock -v /opt/relist-panel:/opt/relist-panel -v /var/lib/relist-panel:/var/lib/relist-panel -e RELIST_DATA_DIR=/var/lib/relist-panel -e RELIST_UPDATE_MANIFEST_URL=http://registry.test/m.json sha256:abc update --report id --run 20261101T120000Z"
	if got, want := strings.Join(dockerexec.UpdateArgv(spec, false), " "), "docker run --rm "+mounts; got != want {
		t.Errorf("--rm argv\n got %q\nwant %q", got, want)
	}
	if got, want := strings.Join(dockerexec.UpdateArgv(spec, true), " "), "docker run -d --name relist-panel-updater-20261101T120000Z "+mounts; got != want {
		t.Errorf("detached argv\n got %q\nwant %q", got, want)
	}
	// No Compose labels: `up` must not take the Updater for an orphan.
	if strings.Contains(strings.Join(dockerexec.UpdateArgv(spec, true), " "), "com.docker.compose") || strings.Contains(strings.Join(dockerexec.UpdateArgv(spec, true), " "), "--restart") {
		t.Error("the Updater carries Compose labels or a restart policy")
	}
}

func TestPanelUpdateRunnerPreflightExitCodes(t *testing.T) {
	for _, code := range []int{0, 1, 3} {
		var argv []string
		r := dockerexec.NewPanelUpdateRunnerWith(func(_ context.Context, a []string, _ io.Reader, stdout, _ io.Writer) error {
			argv = a
			_, _ = stdout.Write([]byte(`{"id":"x"}`))
			return exec.Command("sh", "-c", fmt.Sprintf("exit %d", code)).Run()
		})
		out, got, err := r.Preflight(context.Background(), panelupdate.UpdateSpec{Image: "i", Args: []string{"--preflight"}})
		if err != nil || got != code || string(out) != `{"id":"x"}` || argv[2] != "--rm" {
			t.Errorf("exit %d: out %q code %d err %v argv %v", code, out, got, err, argv)
		}
	}
	r := dockerexec.NewPanelUpdateRunnerWith(func(context.Context, []string, io.Reader, io.Writer, io.Writer) error {
		return errors.New("no docker")
	})
	if _, _, err := r.Preflight(context.Background(), panelupdate.UpdateSpec{}); err == nil {
		t.Error("a docker that cannot run is an error, not an exit code")
	}
}

func TestPanelUpdateRunnerStartAndStop(t *testing.T) {
	var calls [][]string
	r := dockerexec.NewPanelUpdateRunnerWith(func(_ context.Context, a []string, _ io.Reader, _, _ io.Writer) error {
		calls = append(calls, a)
		return nil
	})
	if err := r.StartUpdater(context.Background(), panelupdate.UpdateSpec{Name: "n", Image: "i"}); err != nil {
		t.Fatal(err)
	}
	if err := r.StopUpdater(context.Background(), "relist-panel-updater-x"); err != nil {
		t.Fatal(err)
	}
	if calls[0][2] != "-d" || strings.Join(calls[1], " ") != "docker kill --signal TERM relist-panel-updater-x" {
		t.Fatalf("calls = %v", calls)
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
	plan := panelupdate.Install{Project: "relist-panel", Dir: "/srv/relist", ConfigFiles: []string{"/srv/relist/a.yml", "/srv/relist/b.yml"}}
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

func TestExecComposeDropsRelistDataDir(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\nprintf 'dir=%s other=%s' \"${RELIST_DATA_DIR-unset}\" \"$KEEP_ME\"\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("RELIST_DATA_DIR", "/var/lib/relist-panel")
	t.Setenv("KEEP_ME", "yes")
	out, err := dockerexec.ExecComposeForTest(context.Background(), []string{"up"})
	if err != nil || string(out) != "dir=unset other=yes" {
		t.Fatalf("out = %q, %v", out, err)
	}
}

func TestUpdaterHostContainersInDir(t *testing.T) {
	var filters string
	socket := newFakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		filters = req.URL.Query().Get("filters")
		_, _ = w.Write([]byte(`[{"ImageID":"sha256:p","Labels":{"com.docker.compose.project":"relist-panel","com.docker.compose.project.working_dir":"/opt/x"}}]`))
	}))
	got, err := dockerexec.NewPanelUpdaterHost(socket).ContainersInDir(context.Background(), "/opt/x")
	if err != nil || len(got) != 1 || got[0].ImageID != "sha256:p" || got[0].Labels["com.docker.compose.project"] != "relist-panel" {
		t.Fatalf("got %+v, %v", got, err)
	}
	if !strings.Contains(filters, "com.docker.compose.project.working_dir=/opt/x") {
		t.Fatalf("filters = %q", filters)
	}
}
