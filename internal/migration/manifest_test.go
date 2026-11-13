package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"relist-panel/internal/composes"
	"relist-panel/internal/database"
	"relist-panel/internal/datadirlock"
	"relist-panel/internal/volumes"
)

// projectCLI is a volumes.CLI answering per Compose project.
type projectCLI struct {
	config  map[string]string                  // project -> compose config JSON
	volumes map[string][]volumes.ProjectVolume // project -> labelled volumes
}

func (c *projectCLI) ComposeConfig(_ context.Context, ref volumes.ComposeRef) ([]byte, error) {
	out, ok := c.config[ref.ProjectName]
	if !ok {
		return nil, fmt.Errorf("couldn't find env file for %s", ref.ProjectName)
	}
	return []byte(out), nil
}
func (c *projectCLI) VolumeExists(context.Context, string) (bool, error) { return true, nil }
func (c *projectCLI) ListProjectVolumes(_ context.Context, project string) ([]volumes.ProjectVolume, error) {
	return c.volumes[project], nil
}
func (c *projectCLI) RemoveVolume(context.Context, string) error { return nil }

// fakeTargets answers Target for the Composes a fixture added.
type fakeTargets map[int64]composes.DeployTarget

func (f fakeTargets) Target(_ context.Context, id int64) (composes.DeployTarget, error) {
	t, ok := f[id]
	if !ok {
		return composes.DeployTarget{}, sql.ErrNoRows
	}
	return t, nil
}

type manifestFixture struct {
	t       *testing.T
	db      *sql.DB
	dataDir string
	targets fakeTargets
	cli     *projectCLI
	docker  *FakeDocker
	env     int64
}

func newManifestFixture(t *testing.T) *manifestFixture {
	t.Helper()
	db := openDB(t)
	f := &manifestFixture{t: t, db: db, dataDir: t.TempDir(), targets: fakeTargets{},
		cli: &projectCLI{config: map[string]string{}, volumes: map[string][]volumes.ProjectVolume{}}, docker: NewFakeDocker()}
	res, err := db.Exec(`INSERT INTO projects (name, created_at, updated_at) VALUES ('shop', 1, 1)`)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := res.LastInsertId()
	res, err = db.Exec(`INSERT INTO environments (project_id, name, created_at) VALUES (?, 'prod', 1)`, p)
	if err != nil {
		t.Fatal(err)
	}
	f.env, _ = res.LastInsertId()
	return f
}

// compose adds a Compose. deployed writes its resolved compose file, and
// config is its `docker compose config` JSON.
func (f *manifestFixture) compose(name, yaml, config string, deployed bool) int64 {
	f.t.Helper()
	res, err := f.db.Exec(`INSERT INTO composes (environment_id, name, yaml_content, created_at, updated_at) VALUES (?, ?, ?, 1, 1)`, f.env, name, yaml)
	if err != nil {
		f.t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	project := fmt.Sprintf("relist-c%d", id)
	dir := filepath.Join(f.dataDir, ".composes", fmt.Sprint(id))
	if deployed {
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(yaml), 0o644)
		f.cli.config[project] = config
	}
	f.targets[id] = composes.DeployTarget{
		Compose: composes.Compose{ID: id, Name: name, YAMLContent: yaml},
		Request: composes.RunRequest{ProjectName: project, ComposeFilePath: filepath.Join(dir, "docker-compose.yml"), EnvFilePath: filepath.Join(dir, ".env")},
	}
	return id
}

func (f *manifestFixture) builder() ManifestBuilder {
	return ManifestBuilder{DB: f.db, Composes: f.targets, Volumes: volumes.NewResolver(f.cli, f.dataDir), Docker: f.docker, DataDir: f.dataDir}
}

func localVolume(name, project, logical string) VolumeInfo {
	return VolumeInfo{Name: name, Driver: "local", Labels: map[string]string{
		volumes.ComposeProjectLabel: project, volumes.ComposeVolumeLabel: logical}}
}

func itemIDs(m Manifest) []string {
	var ids []string
	for _, it := range m.Items {
		ids = append(ids, it.ID)
	}
	return ids
}

func hasWarning(ws []string, sub string) bool {
	return slices.ContainsFunc(ws, func(w string) bool { return strings.Contains(w, sub) })
}

const pgConfig = `{"services":{"db":{"image":"postgres:16","volumes":[{"type":"volume","source":"pgdata","target":"/var/lib/postgresql/data"}]},
"cache":{"image":"redis:7","profiles":["extra"],"volumes":[{"type":"volume","source":"cachedata","target":"/data"},{"type":"volume","source":"shared","target":"/shared"}]}},
"volumes":{"pgdata":{"name":"relist-c1_pgdata"},"cachedata":{"name":"relist-c1_cachedata"},"shared":{"name":"shared-ext"}}}`

func TestManifestVolumesIncludeProfilesExternalAndOrphans(t *testing.T) {
	f := newManifestFixture(t)
	id := f.compose("db", "services: {}", pgConfig, true)
	project := fmt.Sprintf("relist-c%d", id)
	for _, v := range []VolumeInfo{localVolume("relist-c1_pgdata", project, "pgdata"), localVolume("relist-c1_cachedata", project, "cachedata"),
		{Name: "shared-ext", Driver: "local"}, localVolume(project+"_old", project, "old")} {
		f.docker.Volumes[v.Name] = v
	}
	f.cli.volumes[project] = []volumes.ProjectVolume{{Name: project + "_old", Labels: map[string]string{
		volumes.ComposeProjectLabel: project, volumes.ComposeVolumeLabel: "old"}}}
	m, err := f.builder().Build(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"volume-relist-c1_pgdata", "volume-relist-c1_cachedata", "volume-shared-ext", "volume-" + project + "_old"} {
		if !slices.Contains(itemIDs(m), want) {
			t.Errorf("items %q lack %s", itemIDs(m), want)
		}
	}
	for _, it := range m.Items {
		if it.ID == "volume-relist-c1_pgdata" && it.Volume.Labels[volumes.ComposeVolumeLabel] != "pgdata" {
			t.Errorf("volume item lost its labels: %+v", it.Volume)
		}
	}
}

func TestManifestSkipsNonLocalDriversWithWarning(t *testing.T) {
	f := newManifestFixture(t)
	f.compose("db", "services: {}", `{"services":{"db":{"image":"postgres:16","volumes":[{"type":"volume","source":"nfs","target":"/d"},{"type":"volume","source":"opts","target":"/e"}]}},
		"volumes":{"nfs":{"name":"nfsvol"},"opts":{"name":"optvol"}}}`, true)
	f.docker.Volumes["nfsvol"] = VolumeInfo{Name: "nfsvol", Driver: "rexray"}
	f.docker.Volumes["optvol"] = VolumeInfo{Name: "optvol", Driver: "local", Options: map[string]string{"type": "nfs"}}
	m, err := f.builder().Build(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(itemIDs(m), "volume-nfsvol") || slices.Contains(itemIDs(m), "volume-optvol") {
		t.Fatalf("items %q include a non-local volume", itemIDs(m))
	}
	if !hasWarning(m.Warnings, "Volume nfsvol uses driver rexray and is not moved") || !hasWarning(m.Warnings, "Volume optvol uses driver local") {
		t.Fatalf("warnings = %q", m.Warnings)
	}
}

func TestHostPathCandidatesMarkSystemPaths(t *testing.T) {
	f := newManifestFixture(t)
	f.compose("web", "services: {}", `{"services":{"web":{"image":"nginx","volumes":[
		{"type":"bind","source":"/etc/localtime","target":"/etc/localtime"},
		{"type":"bind","source":"/var/run/docker.sock","target":"/s"},
		{"type":"bind","source":"/srv/x.sock","target":"/x"},
		{"type":"bind","source":"/srv/uploads","target":"/u"},
		{"type":"bind","source":"`+f.dataDir+`/.composes/1/data","target":"/data"}]}}}`, true)
	got, _, err := f.builder().HostPathCandidates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	system := map[string]bool{}
	for _, c := range got {
		system[c.Path] = c.System
	}
	want := map[string]bool{"/etc/localtime": true, "/var/run/docker.sock": true, "/srv/x.sock": true, "/srv/uploads": false}
	if len(system) != len(want) {
		t.Fatalf("candidates = %+v, want %v (the data dir bind excluded)", got, want)
	}
	for p, sys := range want {
		if s, ok := system[p]; !ok || s != sys {
			t.Errorf("%s system = %v (present %v), want %v", p, s, ok, sys)
		}
	}
}

func TestManifestIncludesBuiltImagesOfStoppedComposes(t *testing.T) {
	f := newManifestFixture(t)
	id := f.compose("api", "services: {}", `{"services":{"api":{"build":{"context":"./src"}}}}`, true)
	tag := fmt.Sprintf("relist-c%d-api:latest", id)
	f.docker.AddImage(ImageInfo{ID: "sha256:built", Tags: []string{tag}, RepoDigests: []string{}, Size: 10})
	f.docker.Images[fmt.Sprintf("relist-c%d-api", id)] = f.docker.Images[tag]
	m, err := f.builder().Build(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var found *Item
	for i := range m.Items {
		if m.Items[i].Kind == KindImage {
			found = &m.Items[i]
		}
	}
	if found == nil || found.Image.ID != "sha256:built" || !slices.Contains(found.Image.Tags, tag) || len(found.Image.RepoDigests) != 0 {
		t.Fatalf("image item = %+v, want the built image with its tag", found)
	}
}

func TestManifestDedupesImagesByID(t *testing.T) {
	f := newManifestFixture(t)
	a := f.compose("a", "services: {}", `{"services":{"web":{"image":"nginx:1"}}}`, true)
	f.compose("b", "services: {}", `{"services":{"web":{"image":"nginx:latest"}}}`, true)
	img := ImageInfo{ID: "sha256:ng", Tags: []string{"nginx:1", "nginx:latest"}, RepoDigests: []string{"nginx@sha256:d"}}
	f.docker.AddImage(img)
	f.docker.ContainerImages[fmt.Sprintf("relist-c%d", a)] = []string{"sha256:ng"}
	m, err := f.builder().Build(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, it := range m.Items {
		if it.Kind == KindImage {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("image items = %d, want 1: %q", n, itemIDs(m))
	}
}

func TestManifestItemOrder(t *testing.T) {
	f := newManifestFixture(t)
	f.compose("web", "services: {}", `{"services":{"web":{"image":"nginx:1","volumes":[
		{"type":"volume","source":"v","target":"/v"},{"type":"bind","source":"/srv/up","target":"/u"}]}},"volumes":{"v":{"name":"vol"}}}`, true)
	f.docker.Volumes["vol"] = VolumeInfo{Name: "vol", Driver: "local"}
	f.docker.AddImage(ImageInfo{ID: "sha256:abcdef0123456789", Tags: []string{"nginx:1"}})
	m, err := f.builder().Build(context.Background(), []int64{1}, []string{"/srv/up"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"database", "data-dir", "volume-vol", "host-path-" + hostPathID("/srv/up"), "image-abcdef012345"}
	if !slices.Equal(itemIDs(m), want) {
		t.Fatalf("items = %q, want %q", itemIDs(m), want)
	}
	known, _ := database.KnownVersion()
	if m.Protocol != ProtocolVersion || m.SchemaVersion != known || m.DataDir != f.dataDir || !slices.Equal(m.RunningComposeIDs, []int64{1}) {
		t.Fatalf("manifest header = %+v", m)
	}
}

func TestManifestRefusesUnknownHostPath(t *testing.T) {
	f := newManifestFixture(t)
	f.compose("web", "services: {}", `{"services":{"web":{"image":"nginx"}}}`, true)
	if _, err := f.builder().Build(context.Background(), nil, []string{"/etc/shadow"}); err == nil {
		t.Fatal("Build() accepted a host path no Compose mounts")
	}
}

func TestManifestSkipsNeverDeployedCompose(t *testing.T) {
	f := newManifestFixture(t)
	f.compose("draft", "services: {}", "", false)
	m, err := f.builder().Build(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if !hasWarning(m.Warnings, "draft was never deployed; only its files and database rows move") {
		t.Fatalf("warnings = %q", m.Warnings)
	}
}

func TestManifestWarnsAboutHandWrittenRouters(t *testing.T) {
	f := newManifestFixture(t)
	f.compose("legacy", "services:\n  web:\n    image: nginx\n    labels:\n      - traefik.http.routers.x.rule=Host(`x.example.com`)\n",
		`{"services":{"web":{"image":"nginx"}}}`, true)
	m, err := f.builder().Build(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !hasWarning(m.Warnings, "legacy: hand-written Traefik routers get no maintenance page") {
		t.Fatalf("warnings = %q", m.Warnings)
	}
}

func TestHostPathCandidatesTreatRootAndAncestorsAsSystem(t *testing.T) {
	f := newManifestFixture(t)
	parent := filepath.Dir(f.dataDir)
	f.compose("exporter", "services: {}", `{"services":{"x":{"image":"node-exporter","volumes":[
		{"type":"bind","source":"/","target":"/host"},
		{"type":"bind","source":"/var/lib","target":"/varlib"},
		{"type":"bind","source":"`+parent+`","target":"/parent"},
		{"type":"bind","source":"/srv/gone","target":"/gone"}]}}}`, true)
	f.docker.Fail["size"] = errors.New("stat /data/gone: no such file or directory")
	got, warnings, err := f.builder().HostPathCandidates(context.Background())
	if err != nil {
		t.Fatalf("HostPathCandidates() error = %v, want a warning instead", err)
	}
	system := map[string]bool{}
	for _, c := range got {
		system[c.Path] = c.System
	}
	for _, p := range []string{"/", "/var/lib", parent} {
		if !system[p] {
			t.Errorf("%s is not a system path", p)
		}
	}
	if !hasWarning(warnings, "Could not size /srv/gone") {
		t.Fatalf("warnings = %q", warnings)
	}
}

// A socket, device or FIFO bind source starts unticked whatever its name
// (spec: "and any socket"), and is not sized: there is nothing to copy.
func TestHostPathCandidatesMarkSpecialFilesAsSystem(t *testing.T) {
	f := newManifestFixture(t)
	f.compose("web", "services: {}", `{"services":{"web":{"image":"nginx","volumes":[
		{"type":"bind","source":"/srv/app.ipc","target":"/ipc"}]}}}`, true)
	f.docker.Paths["/srv|app.ipc"] = PathSpecial
	got, _, err := f.builder().HostPathCandidates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].System || got[0].Size != 0 {
		t.Fatalf("candidates = %+v, want /srv/app.ipc as an unsized system path", got)
	}
}

// A system path the operator may tick is sized like any other, so the
// preview and the free-space checks count it (spec: "the item sizes plus
// 10%"). Virtual file systems and what holds the data directory are not.
func TestHostPathCandidatesSizeSystemPathsThatCanMove(t *testing.T) {
	f := newManifestFixture(t)
	f.compose("web", "services: {}", `{"services":{"web":{"image":"nginx","volumes":[
		{"type":"bind","source":"/etc/ssl","target":"/ssl"},
		{"type":"bind","source":"/proc/cpuinfo","target":"/c"}]}}}`, true)
	f.docker.Sizes["/etc|ssl"] = 4096
	f.docker.Sizes["/proc|cpuinfo"] = 99
	got, _, err := f.builder().HostPathCandidates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	sizes := map[string]int64{}
	for _, c := range got {
		sizes[c.Path] = c.Size
	}
	if sizes["/etc/ssl"] != 4096 || sizes["/proc/cpuinfo"] != 0 {
		t.Fatalf("sizes = %v, want /etc/ssl sized and /proc/cpuinfo not", sizes)
	}
}

func TestSkipInDataDirScanWork(t *testing.T) {
	if !skipInDataDir(".scan-work") {
		t.Fatal(".scan-work would be copied by a Migration")
	}
	if !skipInDataDir(datadirlock.FileName) {
		t.Fatal("the lock file would be copied, and finalize would replace it under the running panel")
	}
	if skipInDataDir("projects") {
		t.Fatal("projects skipped")
	}
}

func TestManifestIncludesRegistryWhenAsked(t *testing.T) {
	f := newManifestFixture(t)
	f.docker.Volumes[RegistryVolume] = VolumeInfo{Name: RegistryVolume, Driver: "local"}
	m, err := f.builder().BuildWith(context.Background(), nil, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(itemIDs(m), "volume-"+RegistryVolume) {
		t.Fatalf("items %q lack the Registry volume", itemIDs(m))
	}
}

func TestManifestOmitsRegistryByDefault(t *testing.T) {
	f := newManifestFixture(t)
	f.docker.Volumes[RegistryVolume] = VolumeInfo{Name: RegistryVolume, Driver: "local"}
	m, err := f.builder().Build(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(itemIDs(m), "volume-"+RegistryVolume) {
		t.Fatalf("items %q carry the Registry volume unasked", itemIDs(m))
	}
	// Asked, but there is no Registry volume: nothing, and no warning.
	delete(f.docker.Volumes, RegistryVolume)
	m, err = f.builder().BuildWith(context.Background(), nil, nil, true)
	if err != nil || slices.Contains(itemIDs(m), "volume-"+RegistryVolume) || hasWarning(m.Warnings, RegistryVolume) {
		t.Fatalf("no volume: items %q, warnings %q, err %v", itemIDs(m), m.Warnings, err)
	}
}
