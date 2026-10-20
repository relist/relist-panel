package migration_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"relist-panel/internal/composes"
	"relist-panel/internal/database"
	"relist-panel/internal/httpapi"
	"relist-panel/internal/migration"
	"relist-panel/internal/sources"
	"relist-panel/internal/volumes"
)

const composeConfig = `{"services":{
	"db":{"image":"postgres:16","volumes":[{"type":"volume","source":"pgdata","target":"/var/lib/postgresql/data"},{"type":"bind","source":"/srv/up","target":"/up"}]},
	"api":{"build":{"context":"./src"}}},
	"volumes":{"pgdata":{"name":"relist-c1_pgdata"}}}`

// statusRunner is a ComposeRunner whose Status reads the FakeDocker and
// whose lifecycle calls succeed without doing anything.
type statusRunner struct{ d *migration.FakeDocker }

func (r statusRunner) Up(context.Context, composes.RunRequest, composes.UpOptions) error { return nil }
func (r statusRunner) Build(context.Context, composes.RunRequest, composes.BuildOptions) error {
	return nil
}
func (r statusRunner) Down(context.Context, composes.RunRequest) error            { return nil }
func (r statusRunner) DownWithVolumes(context.Context, composes.RunRequest) error { return nil }
func (r statusRunner) Restart(context.Context, composes.RunRequest) error         { return nil }
func (r statusRunner) Logs(context.Context, composes.RunRequest) (string, error)  { return "", nil }
func (r statusRunner) Status(_ context.Context, req composes.RunRequest) (composes.ComposeStatus, error) {
	if r.d.Running[req.ProjectName] {
		return composes.ComposeStatus{Running: 1, Total: 1}, nil
	}
	return composes.ComposeStatus{Total: 1}, nil
}

// configCLI answers `docker compose config` for relist-c1 only.
type configCLI struct{}

func (configCLI) ComposeConfig(_ context.Context, ref volumes.ComposeRef) ([]byte, error) {
	if ref.ProjectName != "relist-c1" {
		return nil, errors.New("couldn't find env file")
	}
	return []byte(composeConfig), nil
}
func (configCLI) VolumeExists(context.Context, string) (bool, error) { return true, nil }
func (configCLI) ListProjectVolumes(context.Context, string) ([]volumes.ProjectVolume, error) {
	return nil, nil
}
func (configCLI) RemoveVolume(context.Context, string) error { return nil }

type panel struct {
	t       *testing.T
	dataDir string
	db      *sql.DB
	docker  *migration.FakeDocker
	srv     *httptest.Server
	handler http.Handler
	stop    func()
	client  *http.Client
	opts    migration.Options
	exited  chan struct{}
}

func newPanel(t *testing.T, docker *migration.FakeDocker, dataDir string, opts migration.Options) *panel {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	p := &panel{t: t, dataDir: dataDir, docker: docker, client: &http.Client{Jar: jar}, opts: opts, exited: make(chan struct{}, 4)}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.handler.ServeHTTP(w, r) }))
	t.Cleanup(p.srv.Close)
	p.boot()
	return p
}

// boot opens the database and builds the handler the way main.go does.
func (p *panel) boot() {
	t := p.t
	t.Helper()
	pending, err := migration.PrepareFinalize(p.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	p.db, err = database.OpenAndMigrate(filepath.Join(p.dataDir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	if pending {
		if err := migration.ClearCopiedState(context.Background(), p.db, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	opts := p.opts
	opts.Docker = p.docker
	opts.Backoff = func(int) time.Duration { return 0 }
	opts.Restart = func() { p.exited <- struct{}{} }
	handler, wait := httpapi.NewHandler(ctx, p.db, statusRunner{p.docker}, p.dataDir, nil, nil, nil,
		volumes.NewResolver(configCLI{}, p.dataDir), nil, nil, &opts)
	var once sync.Once
	db := p.db
	p.stop = func() { once.Do(func() { cancel(); wait(); db.Close() }) }
	t.Cleanup(p.stop)
	p.handler = handler
}

// restart stops the handler and boots again over the same data directory.
func (p *panel) restart() {
	p.stop()
	p.boot()
}

func (p *panel) do(method, path string, body any) (int, []byte) {
	p.t.Helper()
	var r io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		r = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, p.srv.URL+path, r)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		p.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

func (p *panel) signIn(setup bool) {
	p.t.Helper()
	creds := map[string]string{"email": "admin@example.com", "password": "correct horse battery staple"}
	if setup {
		if code, body := p.do("POST", "/api/auth/setup", creds); code != http.StatusCreated {
			p.t.Fatalf("setup = %d %s", code, body)
		}
	}
	if code, body := p.do("POST", "/api/auth/login", creds); code != http.StatusOK {
		p.t.Fatalf("login = %d %s", code, body)
	}
}

func (p *panel) progress() migration.Progress {
	p.t.Helper()
	code, body := p.do("GET", "/api/migration/pull/progress", nil)
	if code != http.StatusOK {
		p.t.Fatalf("progress = %d %s", code, body)
	}
	var pr migration.Progress
	json.Unmarshal(body, &pr)
	return pr
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type pair struct {
	old, new *panel
	token    string
}

// newPair seeds the old panel with an admin, a Project, two Composes (one
// running, with a zip Source), a Domain, a Volume, a host path and an Image
// without RepoDigests, and creates a token on it.
func newPair(t *testing.T, newOpts migration.Options) *pair {
	t.Helper()
	oldDir, newDir := t.TempDir(), t.TempDir()
	oldDocker, newDocker := migration.NewFakeDocker(), migration.NewFakeDocker()
	old := newPanel(t, oldDocker, oldDir, migration.Options{ReportedDataDir: newDir})
	old.signIn(true)
	for _, stmt := range []string{
		`INSERT INTO projects (name, created_at, updated_at) VALUES ('shop', 1, 1)`,
		`INSERT INTO environments (project_id, name, created_at) VALUES (1, 'prod', 1)`,
		`INSERT INTO composes (environment_id, name, yaml_content, created_at, updated_at, project_slug, env_slug, identity_version) VALUES (1, 'store', 'services:
  db:
    image: postgres:16
  api:
    build: ./src
', 1, 1, 'shop', 'prod', 1)`,
		`INSERT INTO composes (environment_id, name, yaml_content, created_at, updated_at, project_slug, env_slug, identity_version) VALUES (1, 'draft', 'services: {}', 1, 1, 'shop', 'prod', 1)`,
		`INSERT INTO domains (compose_id, host, service, port, https, cert_provider, created_at, updated_at) VALUES (1, 'shop.example.com', 'db', 80, 1, 'letsencrypt', 0, 0)`,
	} {
		if _, err := old.db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if err := sources.NewStore(old.db).Save(context.Background(), sources.Source{ComposeID: 1, Kind: sources.KindZip, ZipName: "site.zip"}); err != nil {
		t.Fatal(err)
	}
	composeDir := filepath.Join(oldDir, ".composes", "1")
	os.MkdirAll(filepath.Join(composeDir, "src"), 0o750)
	os.WriteFile(filepath.Join(composeDir, "docker-compose.yml"), []byte("services: {}\n"), 0o644)
	os.WriteFile(filepath.Join(composeDir, ".env"), []byte(""), 0o600)
	os.MkdirAll(filepath.Join(oldDir, ".traefik"), 0o700)
	os.WriteFile(filepath.Join(oldDir, ".traefik", "acme.json"), []byte(`{"old":true}`), 0o600)

	oldDocker.Volumes["relist-c1_pgdata"] = migration.VolumeInfo{Name: "relist-c1_pgdata", Driver: "local",
		Labels: map[string]string{"com.docker.compose.project": "relist-c1", "com.docker.compose.volume": "pgdata"}}
	oldDocker.Blobs["relist-c1_pgdata|."] = []byte("pgdata owned by 999:999")
	oldDocker.Blobs["/srv|up"] = []byte("uploads")
	oldDocker.Sizes["/srv|up"] = 7
	built := migration.ImageInfo{ID: "sha256:builtbuiltbuilt", Tags: []string{"relist-c1-api:latest"}, RepoDigests: []string{}}
	oldDocker.AddImage(built)
	oldDocker.Images["relist-c1-api"] = built
	pg := migration.ImageInfo{ID: "sha256:pgpgpgpgpgpg", Tags: []string{"postgres:16"}, RepoDigests: []string{"postgres@sha256:pg"}}
	oldDocker.AddImage(pg)
	oldDocker.Running["relist-c1"] = true
	newDocker.Loadable["relist-c1-api:latest"] = built
	newDocker.Pullable["postgres@sha256:pg"] = pg

	code, body := old.do("POST", "/api/migration/tokens", map[string]string{"lifetime": "24h"})
	if code != http.StatusCreated {
		t.Fatalf("token = %d %s", code, body)
	}
	var tok struct{ Token string }
	json.Unmarshal(body, &tok)

	newOpts.Insecure = true
	return &pair{old: old, new: newPanel(t, newDocker, newDir, newOpts), token: tok.Token}
}

func (p *pair) connectAndStart(t *testing.T) {
	t.Helper()
	if code, body := p.new.do("POST", "/api/migration/pull/connect", map[string]string{"sourceUrl": p.old.srv.URL, "token": p.token}); code != http.StatusOK {
		t.Fatalf("connect = %d %s", code, body)
	}
	if code, body := p.new.do("POST", "/api/migration/pull/start", map[string]any{"hostPaths": []string{"/srv/up"}}); code != http.StatusAccepted {
		t.Fatalf("start = %d %s", code, body)
	}
}

func TestMigrationHappyPath(t *testing.T) {
	p := newPair(t, migration.Options{})
	p.connectAndStart(t)
	select {
	case <-p.new.exited:
	case <-time.After(20 * time.Second):
		t.Fatalf("the new panel never finalized: %+v", p.new.progress())
	}
	if p.old.docker.MaintenanceUp != true || p.old.docker.Running["relist-c1"] {
		t.Fatal("the old host is not locked down")
	}

	p.new.restart()
	p.new.signIn(false) // the admin came with the database
	eventually(t, "done", func() bool { return p.new.progress().Phase == migration.PhaseDone })

	var composesN, domainsN int
	p.new.db.QueryRow(`SELECT COUNT(*) FROM composes`).Scan(&composesN)
	p.new.db.QueryRow(`SELECT COUNT(*) FROM domains WHERE host = 'shop.example.com'`).Scan(&domainsN)
	if composesN != 2 || domainsN != 1 {
		t.Fatalf("new database: %d composes, %d domains", composesN, domainsN)
	}
	v := p.new.docker.Volumes["relist-c1_pgdata"]
	if v.Labels["com.docker.compose.volume"] != "pgdata" || string(p.new.docker.Unpacked["relist-c1_pgdata"]) != "pgdata owned by 999:999" {
		t.Fatalf("volume = %+v, data %q", v, p.new.docker.Unpacked["relist-c1_pgdata"])
	}
	if string(p.new.docker.Unpacked["/srv"]) != "uploads" {
		t.Fatalf("host path data = %q", p.new.docker.Unpacked["/srv"])
	}
	if p.new.docker.Images["relist-c1-api:latest"].ID != "sha256:builtbuiltbuilt" || p.new.docker.Images["postgres:16"].ID != "sha256:pgpgpgpgpgpg" {
		t.Fatal("images missing on the new host")
	}
	if b, _ := os.ReadFile(filepath.Join(p.new.dataDir, ".traefik", "acme.json")); string(b) != `{"old":true}` {
		t.Fatalf("acme.json = %q", b)
	}
	if !p.new.docker.TraefikUp {
		t.Fatal("Traefik left stopped")
	}
	var mode string
	if err := p.new.db.QueryRow(`SELECT mode FROM deployments WHERE compose_id = 1 ORDER BY id DESC LIMIT 1`).Scan(&mode); err != nil || mode != "skip-build" {
		t.Fatalf("deployment of the running Compose = %q, %v; summary %+v", mode, err, p.new.progress().Summary)
	}
	var others int
	p.new.db.QueryRow(`SELECT COUNT(*) FROM deployments WHERE compose_id = 2`).Scan(&others)
	if others != 0 {
		t.Fatal("a Compose that was not running was deployed")
	}

	code, body := p.old.do("GET", "/api/migration/status", nil)
	var st struct{ Step, TargetURL string }
	json.Unmarshal(body, &st)
	if code != 200 || st.Step != "migrated" || st.TargetURL != p.new.srv.URL {
		t.Fatalf("old status = %d %s", code, body)
	}
	if code, _ := p.old.do("GET", "/api/projects", nil); code != http.StatusServiceUnavailable {
		t.Fatalf("old projects = %d, want 503", code)
	}
}

// cutOnce cuts the first stream of one item halfway.
type cutOnce struct {
	mu   sync.Mutex
	path string
	done bool
}

func (c *cutOnce) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(req)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil && !c.done && strings.HasSuffix(req.URL.Path, c.path) {
		c.done = true
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		resp.Body = io.NopCloser(io.MultiReader(bytes.NewReader(raw[:len(raw)/2]), errReader{}))
	}
	return resp, err
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestMigrationConnectionCutMidItem(t *testing.T) {
	cut := &cutOnce{path: "/api/migration/items/volume-relist-c1_pgdata"}
	p := newPair(t, migration.Options{HTTP: &http.Client{Transport: cut}})
	p.connectAndStart(t)
	select {
	case <-p.new.exited:
	case <-time.After(20 * time.Second):
		t.Fatalf("the new panel never finalized: %+v", p.new.progress())
	}
	if !cut.done {
		t.Fatal("the stream was never cut")
	}
	if string(p.new.docker.Unpacked["relist-c1_pgdata"]) != "pgdata owned by 999:999" {
		t.Fatalf("volume data after the retry = %q", p.new.docker.Unpacked["relist-c1_pgdata"])
	}
}

func TestMigrationAbortRestoresOldHost(t *testing.T) {
	p := newPair(t, migration.Options{})
	p.new.docker.Fail["unpack"] = errors.New("disk on fire")
	p.connectAndStart(t)
	eventually(t, "paused", func() bool { return p.new.progress().Phase == migration.PhasePaused })
	if code, body := p.new.do("POST", "/api/migration/pull/abort", nil); code != http.StatusNoContent {
		t.Fatalf("abort = %d %s", code, body)
	}
	if !p.old.docker.Running["relist-c1"] || p.old.docker.MaintenanceUp {
		t.Fatal("the old host did not resume")
	}
	_, body := p.old.do("GET", "/api/migration/status", nil)
	var st struct{ Step string }
	json.Unmarshal(body, &st)
	if st.Step != "" {
		t.Fatalf("old step = %q", st.Step)
	}
	if _, err := os.Stat(filepath.Join(p.new.dataDir, ".migration")); !os.IsNotExist(err) {
		t.Fatal("the new host kept .migration")
	}
	if code, _ := p.old.do("GET", "/api/projects", nil); code != http.StatusOK {
		t.Fatalf("old projects after abort = %d", code)
	}
}

func TestResumeOnOldHostAfterMigrated(t *testing.T) {
	p := newPair(t, migration.Options{})
	p.connectAndStart(t)
	<-p.new.exited
	p.new.restart()
	p.new.signIn(false)
	eventually(t, "done", func() bool { return p.new.progress().Phase == migration.PhaseDone })
	if code, body := p.old.do("POST", "/api/migration/resume", map[string]string{"confirm": "RESUME"}); code != http.StatusNoContent {
		t.Fatalf("resume = %d %s", code, body)
	}
	if code, _ := p.old.do("GET", "/api/projects", nil); code != http.StatusOK {
		t.Fatalf("old projects after resume = %d", code)
	}
	if !p.old.docker.Running["relist-c1"] {
		t.Fatal("resume did not start the Compose")
	}
}
