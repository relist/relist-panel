package panelupdate

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const embeddedFile = "services:\n  panel:\n    image: a\n  traefik:\n    image: b\n\n  other:\n    image: c\n"

// fakeDocker answers the Engine API calls of a Preflight.
type fakeDocker struct {
	pulled  []string
	pullErr error
	hashes  []ServiceHash
}

func (f *fakeDocker) Pull(_ context.Context, ref string) error {
	f.pulled = append(f.pulled, ref)
	return f.pullErr
}

func (f *fakeDocker) ConfigHashes(context.Context, string) ([]ServiceHash, error) {
	return f.hashes, nil
}

// composeHelper serves Install files like fakeHelper and answers `docker
// compose` by the number of -f files: configs[1] without an override.
type composeHelper struct {
	fakeHelper
	withOverride, without string // fixture names
	hashOutput            string
	composeErr            *HelperExitError
	composeRuns           [][]string
}

func (h *composeHelper) Run(ctx context.Context, run HelperRun, stdout io.Writer) error {
	if run.Argv[0] != "docker" {
		return h.fakeHelper.Run(ctx, run, stdout)
	}
	h.composeRuns = append(h.composeRuns, run.Argv)
	if h.composeErr != nil {
		return h.composeErr
	}
	if strings.Contains(strings.Join(run.Argv, " "), "--hash") {
		_, _ = io.WriteString(stdout, h.hashOutput)
		return nil
	}
	name := h.without
	if strings.Count(strings.Join(run.Argv, " "), " -f ") > 1 {
		name = h.withOverride
	}
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		return err
	}
	_, err = stdout.Write(data)
	return err
}

var allInvariants = []m{
	{"kind": "service-image", "service": "panel", "image": fixtureImage},
	{"kind": "service-mount", "service": "panel", "source": "/var/run/docker.sock", "target": "/var/run/docker.sock"},
	{"kind": "service-mount", "service": "panel", "source": "/proc", "target": "/host/proc", "read_only": true},
	{"kind": "data-dir-same-path", "service": "panel"},
	{"kind": "network-external", "network": "relist-network", "name": "relist-network"},
	{"kind": "service-network", "service": "panel", "network": "relist-network"},
	{"kind": "service-command-has", "service": "traefik", "arg": "--providers.file.directory=/letsencrypt/dynamic"},
	{"kind": "service-label", "service": "panel", "key": "traefik.http.routers.panel.priority", "value": "100000"},
}

type rig struct {
	svc    *Service
	rel    *release
	docker *fakeDocker
	helper *composeHelper
	data   string
	locked bool
}

type rigOpts struct {
	current    string            // the Install file on the host
	theirs     string            // the manifest's Install file
	files      map[string]string // other files by path
	invariants []m
}

func newRig(t *testing.T, o rigOpts) *rig {
	t.Helper()
	pub, priv := newKey(t)
	r := &rig{docker: &fakeDocker{}, data: t.TempDir()}
	r.rel = newRelease(t, priv)
	r.helper = &composeHelper{withOverride: "config_base.json", without: "config_base.json", hashOutput: "panel h1\ntraefik h2\n"}
	r.helper.files = map[string]string{
		"/srv/relist/docker-compose.yml": o.current,
		"/srv/relist/.env":               "RELIST_DOMAIN=panel.example.com\nRELIST_PANEL_IMAGE=old\n",
	}
	for k, v := range o.files {
		r.helper.files[k] = v
	}
	r.docker.hashes = []ServiceHash{{"panel", "h1"}, {"traefik", "h2"}}
	r.svc = New(Deps{
		Store: NewStore(newDB(t)), Version: "1.3.0", Key: pub, ManifestURL: r.rel.srv.URL + "/update-manifest.json",
		Self: fakeSelf{self: Self{ImageID: "sha256:img", Labels: map[string]string{
			labelProject: "relist-panel", labelWorkingDir: "/srv/relist", labelConfigFiles: "/srv/relist/docker-compose.yml",
		}}},
		Helper: r.helper, Docker: r.docker, DataDir: r.data, InstallFile: []byte(embeddedFile),
		MigrationLocked: func(context.Context) (bool, error) { return r.locked, nil },
	})
	invs := o.invariants
	if invs == nil {
		invs = allInvariants
	}
	body, _ := json.Marshal(m{
		"schema": 1, "version": "1.4.0", "released_at": "2026-11-20T09:00:00Z", "image": fixtureImage,
		"notes_url": "https://example.test/notes", "install_file": o.theirs, "invariants": invs,
		"manual_steps": []m{{"id": "open-443", "text": "Open port 443."}},
	})
	r.rel.publish(body)
	if err := r.svc.CheckNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *rig) preflight(t *testing.T) *Report {
	t.Helper()
	rep, err := r.svc.Preflight(context.Background())
	if err != nil || rep == nil {
		t.Fatalf("preflight = %v, %v", rep, err)
	}
	return rep
}

var _ = ed25519.PublicKeySize

const newerFile = "services:\n  panel:\n    image: a2\n  traefik:\n    image: b\n\n  other:\n    image: c\n"

func TestPreflightNotEdited(t *testing.T) {
	r := newRig(t, rigOpts{current: embeddedFile, theirs: newerFile})
	rep := r.preflight(t)
	if !rep.Ready || rep.Failed != nil || rep.Change != ChangeNotEdited {
		t.Fatalf("report = %+v", rep)
	}
	if rep.FromVersion != "1.3.0" || rep.ToVersion != "1.4.0" || rep.NotesURL == "" || len(rep.ManualSteps) != 1 {
		t.Fatalf("report = %+v", rep)
	}
	if len(rep.Invariants) != len(allInvariants) || !rep.Gate.InstallAvailable {
		t.Fatalf("invariants = %+v gate = %+v", rep.Invariants, rep.Gate)
	}
	if !strings.Contains(rep.Diff, "-    image: a\n") || !strings.Contains(rep.Diff, "+    image: a2\n") {
		t.Fatalf("diff = %q", rep.Diff)
	}
	if len(r.docker.pulled) != 1 || r.docker.pulled[0] != fixtureImage {
		t.Fatalf("pulled %v", r.docker.pulled)
	}
	if len(rep.Recreated) != 0 || rep.TraefikRecreated {
		t.Fatalf("recreated %v", rep.Recreated)
	}
	// The candidate files are in the scratch directory.
	dir := filepath.Join(r.data, ".panel-update", "preflight", rep.ID)
	if got, _ := os.ReadFile(filepath.Join(dir, "docker-compose.yml")); string(got) != newerFile {
		t.Fatalf("candidate = %q", got)
	}
	env, _ := os.ReadFile(filepath.Join(dir, ".env"))
	if string(env) != "RELIST_DOMAIN=panel.example.com\nRELIST_PANEL_IMAGE="+fixtureImage+"\n" {
		t.Fatalf("env = %q", env)
	}
	if r.svc.LatestReport() != rep {
		t.Fatal("the report was not kept")
	}
}

func TestPreflightComposeCommandLine(t *testing.T) {
	r := newRig(t, rigOpts{current: embeddedFile, theirs: newerFile})
	rep := r.preflight(t)
	dir := filepath.Join(r.data, ".panel-update", "preflight", rep.ID)
	want := "docker compose -p relist-panel --project-directory /srv/relist -f " + dir + "/docker-compose.yml --env-file " + dir + "/.env config --format json"
	if got := strings.Join(r.helper.composeRuns[0], " "); got != want {
		t.Fatalf("argv = %q\nwant   %q", got, want)
	}
	if got := strings.Join(r.helper.composeRuns[1], " "); !strings.HasSuffix(got, "config --hash *") {
		t.Fatalf("argv = %q", got)
	}
	for _, run := range r.helper.runs {
		if run.Argv[0] == "docker" && run.Scratch != dir {
			t.Fatalf("scratch = %q", run.Scratch)
		}
	}
}

func TestPreflightEditedMerged(t *testing.T) {
	// The operator added a line at the end; the new version changes the top.
	current := embeddedFile + "  mine:\n    image: d\n"
	r := newRig(t, rigOpts{current: current, theirs: newerFile})
	rep := r.preflight(t)
	if rep.Change != ChangeEditedMerged || rep.Failed != nil || !rep.Ready {
		t.Fatalf("report = %+v", rep)
	}
	dir := filepath.Join(r.data, ".panel-update", "preflight", rep.ID)
	got, _ := os.ReadFile(filepath.Join(dir, "docker-compose.yml"))
	if string(got) != newerFile+"  mine:\n    image: d\n" {
		t.Fatalf("candidate = %q", got)
	}
	if !strings.Contains(rep.Diff, "+    image: a2") || strings.Contains(rep.Diff, "mine") {
		t.Fatalf("diff = %q", rep.Diff)
	}
}

func TestPreflightEditedConflict(t *testing.T) {
	current := strings.Replace(embeddedFile, "image: a\n", "image: mine\n", 1)
	r := newRig(t, rigOpts{current: current, theirs: newerFile})
	rep := r.preflight(t)
	if rep.Change != ChangeEditedConflict || rep.Ready || rep.Failed == nil || rep.Failed.Step != StepChange {
		t.Fatalf("report = %+v", rep)
	}
	if !strings.Contains(rep.Failed.Message, "docker-compose.override.yml") {
		t.Fatalf("message = %q", rep.Failed.Message)
	}
	c := rep.Conflict
	if c == nil || c.Hunks != 1 || c.Ours != current || c.Theirs != newerFile || !strings.Contains(c.Merged, "<<<<<<<") {
		t.Fatalf("conflict = %+v", c)
	}
	if len(r.helper.composeRuns) != 0 {
		t.Fatal("ran compose after a conflict")
	}
}

func TestPreflightOverrideReplacingTraefikCommand(t *testing.T) {
	r := newRig(t, rigOpts{
		current: embeddedFile, theirs: newerFile,
		files: map[string]string{"/srv/relist/docker-compose.override.yml": "services:\n  traefik:\n    command: [--api.insecure=true]\n"},
	})
	r.svc.d.Self = fakeSelf{self: Self{ImageID: "sha256:img", Labels: map[string]string{
		labelProject: "relist-panel", labelWorkingDir: "/srv/relist",
		labelConfigFiles: "/srv/relist/docker-compose.yml,/srv/relist/docker-compose.override.yml",
	}}}
	r.helper.withOverride = "config_override_command.json"
	rep := r.preflight(t)
	if rep.Ready || rep.Failed != nil {
		t.Fatalf("report = %+v", rep)
	}
	var failing []InvariantResult
	for _, i := range rep.Invariants {
		if !i.OK {
			failing = append(failing, i)
		}
	}
	if len(failing) != 1 || failing[0].Kind != "service-command-has" || failing[0].Source != "the Install override" {
		t.Fatalf("failing = %+v", failing)
	}
	// The override file is a -f argument after the Install file, in scratch.
	argv := strings.Join(r.helper.composeRuns[0], " ")
	if !strings.Contains(argv, "/docker-compose.yml -f ") || !strings.Contains(argv, "01-docker-compose.override.yml") {
		t.Fatalf("argv = %q", argv)
	}
}

func TestPreflightBlamesTheInstallFileWhenTheOverrideIsInnocent(t *testing.T) {
	r := newRig(t, rigOpts{
		current: embeddedFile, theirs: newerFile,
		files:      map[string]string{"/srv/relist/docker-compose.override.yml": "services: {}\n"},
		invariants: []m{{"kind": "service-label", "service": "panel", "key": "nope", "value": "x"}},
	})
	r.svc.d.Self = fakeSelf{self: Self{Labels: map[string]string{
		labelProject: "p", labelWorkingDir: "/srv/relist",
		labelConfigFiles: "/srv/relist/docker-compose.yml,/srv/relist/docker-compose.override.yml",
	}}}
	rep := r.preflight(t)
	if len(rep.Invariants) != 1 || rep.Invariants[0].OK || rep.Invariants[0].Source != "the Install file" {
		t.Fatalf("invariants = %+v", rep.Invariants)
	}
}

func TestPreflightPanelWithBuildFailsServiceImage(t *testing.T) {
	r := newRig(t, rigOpts{current: embeddedFile, theirs: newerFile, invariants: allInvariants[:1]})
	r.helper.without, r.helper.withOverride = "config_panel_build.json", "config_panel_build.json"
	rep := r.preflight(t)
	if rep.Ready || len(rep.Invariants) != 1 || rep.Invariants[0].OK || !strings.Contains(rep.Invariants[0].Detail, "builds") || rep.Invariants[0].Source != ".env" {
		t.Fatalf("report = %+v", rep)
	}
}

func TestPreflightRecreatedServices(t *testing.T) {
	r := newRig(t, rigOpts{current: embeddedFile, theirs: newerFile})
	r.docker.hashes = []ServiceHash{{"panel", "old"}, {"traefik", "old"}, {"traefik", "h2"}, {"gone", "x"}}
	rep := r.preflight(t)
	if strings.Join(rep.Recreated, ",") != "panel,traefik" || !rep.TraefikRecreated {
		t.Fatalf("recreated = %v", rep.Recreated)
	}
	r.docker.hashes = []ServiceHash{{"panel", "old"}}
	if rep := r.preflight(t); strings.Join(rep.Recreated, ",") != "panel" || rep.TraefikRecreated {
		t.Fatalf("recreated = %v", rep.Recreated)
	}
}

func TestPreflightUnknownKindNeedsManualUpdate(t *testing.T) {
	r := newRig(t, rigOpts{current: embeddedFile, theirs: newerFile, invariants: []m{{"kind": "service-image", "service": "panel"}, {"kind": "from-the-future"}}})
	rep := r.preflight(t)
	if !rep.NeedsManualUpdate || rep.Ready || rep.ReadmeURL == "" || len(rep.UnknownKinds) != 1 || rep.UnknownKinds[0] != "from-the-future" {
		t.Fatalf("report = %+v", rep)
	}
	if len(r.docker.pulled) != 0 {
		t.Fatal("pulled an image for a version that needs a manual update")
	}
}

func TestPreflightStopsAtTheFirstFailingStep(t *testing.T) {
	t.Run("migration lock", func(t *testing.T) {
		r := newRig(t, rigOpts{current: embeddedFile, theirs: newerFile})
		r.locked = true
		rep := r.preflight(t)
		if rep.Failed == nil || rep.Failed.Step != StepGate || !rep.Gate.MigrationLocked || len(r.docker.pulled) != 0 {
			t.Fatalf("report = %+v", rep)
		}
	})
	t.Run("install unavailable", func(t *testing.T) {
		r := newRig(t, rigOpts{current: embeddedFile, theirs: newerFile})
		r.svc.d.Self = fakeSelf{self: Self{}}
		rep := r.preflight(t)
		if rep.Failed == nil || rep.Failed.Step != StepGate || rep.Gate.InstallAvailable || !strings.Contains(rep.Failed.Message, reasonNotCompose) {
			t.Fatalf("report = %+v", rep)
		}
	})
	t.Run("pull", func(t *testing.T) {
		r := newRig(t, rigOpts{current: embeddedFile, theirs: newerFile})
		r.docker.pullErr = errors.New("manifest unknown")
		rep := r.preflight(t)
		if rep.Failed == nil || rep.Failed.Step != StepPull || !strings.Contains(rep.Failed.Message, "manifest unknown") || len(r.helper.runs) != 0 {
			t.Fatalf("report = %+v", rep)
		}
	})
	t.Run("config", func(t *testing.T) {
		r := newRig(t, rigOpts{current: embeddedFile, theirs: newerFile})
		r.helper.composeErr = &HelperExitError{Code: 1, Stderr: "services.panel.image must be a string\n"}
		rep := r.preflight(t)
		if rep.Failed == nil || rep.Failed.Step != StepConfig || !strings.Contains(rep.Failed.Message, "services.panel.image must be a string") || len(rep.Invariants) != 0 {
			t.Fatalf("report = %+v", rep)
		}
	})
	t.Run("missing install file", func(t *testing.T) {
		r := newRig(t, rigOpts{current: embeddedFile, theirs: newerFile})
		delete(r.helper.files, "/srv/relist/docker-compose.yml")
		if rep := r.preflight(t); rep.Failed == nil || rep.Failed.Step != StepChange {
			t.Fatalf("report = %+v", rep)
		}
	})
}

func TestPreflightRefusesATagReference(t *testing.T) {
	r := newRig(t, rigOpts{current: embeddedFile, theirs: newerFile})
	if err := r.svc.pull(context.Background(), &Manifest{Image: "ghcr.io/x/y:latest"}); err == nil {
		t.Fatal("pulled a tag")
	}
}

func TestPreflightKeepsOnlyTheLatestScratch(t *testing.T) {
	r := newRig(t, rigOpts{current: embeddedFile, theirs: newerFile})
	first := r.preflight(t)
	second := r.preflight(t)
	if first.ID == second.ID || r.svc.LatestReport() != second {
		t.Fatal("ids repeat or the latest is stale")
	}
	entries, _ := os.ReadDir(filepath.Join(r.data, ".panel-update", "preflight"))
	if len(entries) != 1 || entries[0].Name() != second.ID {
		t.Fatalf("scratch = %v", entries)
	}
}

func TestPreflightWithoutAnUpdate(t *testing.T) {
	s, _ := newService(t, "1.3.0")
	rep, err := s.Preflight(context.Background())
	if rep != nil || err != nil {
		t.Fatalf("preflight = %v, %v", rep, err)
	}
	if rec, _ := serve(t, s, "POST", "/api/panel-update/preflight", ""); rec.Code != 409 {
		t.Fatalf("code = %d", rec.Code)
	}
}

func TestPreflightRoute(t *testing.T) {
	r := newRig(t, rigOpts{current: embeddedFile, theirs: newerFile})
	rec, _ := serve(t, r.svc, "POST", "/api/panel-update/preflight", "")
	var got struct {
		ID         string
		Ready      bool
		Change     string
		Invariants []struct{ Kind string }
		Recreated  []string
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 {
		t.Fatalf("code %d body %s", rec.Code, rec.Body)
	}
	if got.ID == "" || !got.Ready || got.Change != ChangeNotEdited || len(got.Invariants) != len(allInvariants) || got.Recreated == nil {
		t.Fatalf("report = %+v\n%s", got, rec.Body)
	}
	if got.ID != r.svc.LatestReport().ID {
		t.Fatal("the route's report is not the latest")
	}
}

func TestMergeFileExitCodes(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	merged, n, err := mergeFile([]byte("a\nb\nc\n"), []byte("a\nb\nc\n"), []byte("a\nB\nc\n"))
	if err != nil || n != 0 || string(merged) != "a\nB\nc\n" {
		t.Fatalf("clean merge = %q, %d, %v", merged, n, err)
	}
	_, n, err = mergeFile([]byte("a\nX\nc\nd\ne\nf\ng\nY\n"), []byte("a\nb\nc\nd\ne\nf\ng\nh\n"), []byte("a\nB\nc\nd\ne\nf\ng\nH\n"))
	if err != nil || n != 2 {
		t.Fatalf("conflicts = %d, %v", n, err)
	}
}
