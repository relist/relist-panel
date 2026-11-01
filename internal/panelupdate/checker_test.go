package panelupdate

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeCompose answers `docker compose` by the number of -f files and the
// subcommand, from recorded JSON in testdata.
type fakeCompose struct {
	withOverride, without string
	hashOutput            string
	err                   error
	runs                  [][]string
}

func (f *fakeCompose) Run(_ context.Context, argv []string) ([]byte, error) {
	f.runs = append(f.runs, argv)
	if f.err != nil {
		return nil, f.err
	}
	line := strings.Join(argv, " ")
	if strings.Contains(line, "--hash") {
		return []byte(f.hashOutput), nil
	}
	name := f.without
	if strings.Count(line, " -f ") > 1 {
		name = f.withOverride
	}
	return os.ReadFile("testdata/" + name)
}

type crig struct {
	c       *Checker
	rel     *release
	docker  *fakeDocker
	compose *fakeCompose
	in      Install
	dir     string
	data    string
}

type crigOpts struct {
	current    string
	theirs     string
	override   string // content of docker-compose.override.yml, "" for none
	invariants []m
	version    string // running version, default 1.3.0
	manifestV  string // manifest version, default 1.4.0
}

func newCRig(t *testing.T, o crigOpts) *crig {
	t.Helper()
	pub, priv := newKey(t)
	r := &crig{docker: &fakeDocker{}, data: t.TempDir(), dir: t.TempDir()}
	r.docker.hashes = []ServiceHash{{"panel", "h1"}, {"traefik", "h2"}}
	r.rel = newRelease(t, priv)
	r.compose = &fakeCompose{withOverride: "config_base.json", without: "config_base.json", hashOutput: "panel h1\ntraefik h2\n"}
	files := []string{filepath.Join(r.dir, "docker-compose.yml")}
	os.WriteFile(files[0], []byte(o.current), 0o644)
	os.WriteFile(filepath.Join(r.dir, ".env"), []byte("RELIST_DOMAIN=panel.example.com\nRELIST_PANEL_IMAGE=old\n"), 0o600)
	if o.override != "" {
		ov := filepath.Join(r.dir, "docker-compose.override.yml")
		os.WriteFile(ov, []byte(o.override), 0o644)
		files = append(files, ov)
	}
	r.in = Install{Project: "relist-panel", Dir: r.dir, ConfigFiles: files, Image: "sha256:img"}
	invs := o.invariants
	if invs == nil {
		invs = allInvariants
	}
	mv := o.manifestV
	if mv == "" {
		mv = "1.4.0"
	}
	body, _ := json.Marshal(m{
		"schema": 1, "version": mv, "released_at": "2026-11-20T09:00:00Z", "image": fixtureImage,
		"notes_url": "https://example.test/notes", "install_file": o.theirs, "invariants": invs,
		"manual_steps": []m{{"id": "open-443", "text": "Open port 443."}},
	})
	r.rel.publish(body)
	v := o.version
	if v == "" {
		v = "1.3.0"
	}
	r.c = &Checker{
		DataDir: r.data, Version: v, InstallFile: []byte(embeddedFile), Key: pub,
		ManifestURL: r.rel.srv.URL + "/update-manifest.json", Docker: r.docker, Compose: r.compose,
	}
	return r
}

func (r *crig) check(t *testing.T) (*Report, *Candidate) {
	t.Helper()
	rep, cand, err := r.c.Check(context.Background(), r.in)
	if err != nil || rep == nil {
		t.Fatalf("check = %v, %v", rep, err)
	}
	return rep, cand
}

func TestCheckUpToDate(t *testing.T) {
	r := newCRig(t, crigOpts{current: embeddedFile, theirs: newerFile, version: "1.4.0"})
	rep, _ := r.check(t)
	if !rep.UpToDate || rep.Ready || rep.ExitCode() != 3 || rep.Failed != nil {
		t.Fatalf("report = %+v", rep)
	}
	if len(r.docker.pulled) != 0 || len(r.compose.runs) != 0 {
		t.Fatal("pulled or ran compose for an up to date install")
	}
}

func TestCheckUnknownKindStopsBeforePull(t *testing.T) {
	r := newCRig(t, crigOpts{current: embeddedFile, theirs: newerFile, invariants: []m{{"kind": "service-image", "service": "panel"}, {"kind": "from-the-future"}}})
	rep, _ := r.check(t)
	if !rep.NeedsManualUpdate || rep.Ready || rep.ExitCode() != 1 || rep.UnknownKinds[0] != "from-the-future" {
		t.Fatalf("report = %+v", rep)
	}
	if len(r.docker.pulled) != 0 {
		t.Fatal("pulled an image for a version that needs a manual update")
	}
}

func TestCheckBadSignature(t *testing.T) {
	r := newCRig(t, crigOpts{current: embeddedFile, theirs: newerFile})
	pub, _ := newKey(t) // a key that did not sign the manifest
	r.c.Key = pub
	rep, _ := r.check(t)
	if rep.Failed == nil || rep.Failed.Step != StepManifest || rep.Ready || rep.ExitCode() != 1 {
		t.Fatalf("report = %+v", rep)
	}
	if len(r.docker.pulled) != 0 {
		t.Fatal("pulled after a bad signature")
	}
}

func TestCheckNotEditedReady(t *testing.T) {
	r := newCRig(t, crigOpts{current: embeddedFile, theirs: newerFile})
	rep, cand := r.check(t)
	if !rep.Ready || rep.Failed != nil || rep.Change != ChangeNotEdited || rep.ExitCode() != 0 {
		t.Fatalf("report = %+v", rep)
	}
	if rep.FromVersion != "1.3.0" || rep.ToVersion != "1.4.0" || rep.Image != fixtureImage || len(rep.ManualSteps) != 1 || rep.Fingerprint == "" {
		t.Fatalf("report = %+v", rep)
	}
	if !strings.Contains(rep.Diff, "+    image: a2\n") {
		t.Fatalf("diff = %q", rep.Diff)
	}
	if len(r.docker.pulled) != 1 || r.docker.pulled[0] != fixtureImage || cand.ImageID != "sha256:target" {
		t.Fatalf("pulled %v image %q", r.docker.pulled, cand.ImageID)
	}
	dir := filepath.Join(r.data, ".panel-update", "preflight", rep.ID)
	if got, _ := os.ReadFile(filepath.Join(dir, "docker-compose.yml")); string(got) != newerFile {
		t.Fatalf("candidate = %q", got)
	}
	if string(cand.Env) != "RELIST_DOMAIN=panel.example.com\nRELIST_PANEL_IMAGE="+fixtureImage+"\n" {
		t.Fatalf("env = %q", cand.Env)
	}
	want := "-p relist-panel --project-directory " + r.dir + " -f " + dir + "/docker-compose.yml --env-file " + dir + "/.env config --format json"
	if got := strings.Join(r.compose.runs[0], " "); got != want {
		t.Fatalf("argv = %q\nwant   %q", got, want)
	}
}

func TestCheckEditedMerged(t *testing.T) {
	r := newCRig(t, crigOpts{current: embeddedFile + "  mine:\n    image: d\n", theirs: newerFile})
	rep, cand := r.check(t)
	if rep.Change != ChangeEditedMerged || rep.Failed != nil || !rep.Ready {
		t.Fatalf("report = %+v", rep)
	}
	if string(cand.Install) != newerFile+"  mine:\n    image: d\n" {
		t.Fatalf("candidate = %q", cand.Install)
	}
}

func TestCheckConflict(t *testing.T) {
	current := strings.Replace(embeddedFile, "image: a\n", "image: mine\n", 1)
	r := newCRig(t, crigOpts{current: current, theirs: newerFile})
	rep, _ := r.check(t)
	if rep.Change != ChangeEditedConflict || rep.Ready || rep.Failed == nil || rep.Failed.Step != StepChange || rep.Conflict == nil || rep.Conflict.Hunks != 1 {
		t.Fatalf("report = %+v", rep)
	}
	if len(r.compose.runs) != 0 {
		t.Fatal("ran compose after a conflict")
	}
}

func TestCheckFingerprintChangesWithEnv(t *testing.T) {
	r := newCRig(t, crigOpts{current: embeddedFile, theirs: newerFile})
	a, _ := r.check(t)
	b, _ := r.check(t)
	if a.ID == b.ID {
		t.Fatal("report ids repeat")
	}
	if a.Fingerprint != b.Fingerprint {
		t.Fatalf("same inputs, different fingerprints: %s %s", a.Fingerprint, b.Fingerprint)
	}
	os.WriteFile(filepath.Join(r.dir, ".env"), []byte("RELIST_DOMAIN=panel.example.com\nRELIST_PANEL_IMAGE=olc\n"), 0o600)
	c, _ := r.check(t)
	if c.Fingerprint == a.Fingerprint {
		t.Fatal("one changed byte of .env left the fingerprint alone")
	}
}

func TestCheckFingerprintChangesWithImageID(t *testing.T) {
	r := newCRig(t, crigOpts{current: embeddedFile, theirs: newerFile})
	a, _ := r.check(t)
	r.docker.imageID = "sha256:other"
	b, _ := r.check(t)
	if a.Fingerprint == b.Fingerprint {
		t.Fatal("a different pulled image left the fingerprint alone")
	}
}

func TestCheckStepFailures(t *testing.T) {
	t.Run("pull", func(t *testing.T) {
		r := newCRig(t, crigOpts{current: embeddedFile, theirs: newerFile})
		r.docker.pullErr = errors.New("manifest unknown")
		rep, _ := r.check(t)
		if rep.Failed == nil || rep.Failed.Step != StepPull || !strings.Contains(rep.Failed.Message, "manifest unknown") || len(r.compose.runs) != 0 {
			t.Fatalf("report = %+v", rep)
		}
	})
	t.Run("config", func(t *testing.T) {
		r := newCRig(t, crigOpts{current: embeddedFile, theirs: newerFile})
		r.compose.err = &HelperExitError{Code: 1, Stderr: "services.panel.image must be a string\n"}
		rep, _ := r.check(t)
		if rep.Failed == nil || rep.Failed.Step != StepConfig || !strings.Contains(rep.Failed.Message, "services.panel.image must be a string") || len(rep.Invariants) != 0 {
			t.Fatalf("report = %+v", rep)
		}
	})
	t.Run("missing install file", func(t *testing.T) {
		r := newCRig(t, crigOpts{current: embeddedFile, theirs: newerFile})
		os.Remove(r.in.ConfigFiles[0])
		if rep, _ := r.check(t); rep.Failed == nil || rep.Failed.Step != StepChange {
			t.Fatalf("report = %+v", rep)
		}
	})
	t.Run("tag reference", func(t *testing.T) {
		r := newCRig(t, crigOpts{current: embeddedFile, theirs: newerFile})
		if err := r.c.pull(context.Background(), &Manifest{Image: "ghcr.io/x/y:latest"}); err == nil {
			t.Fatal("pulled a tag")
		}
	})
}

func TestCheckOverrideBlame(t *testing.T) {
	r := newCRig(t, crigOpts{
		current: embeddedFile, theirs: newerFile,
		override: "services:\n  traefik:\n    command: [--api.insecure=true]\n",
	})
	r.compose.withOverride = "config_override_command.json"
	rep, _ := r.check(t)
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
	argv := strings.Join(r.compose.runs[0], " ")
	if !strings.Contains(argv, "/docker-compose.yml -f ") || !strings.Contains(argv, "01-docker-compose.override.yml") {
		t.Fatalf("argv = %q", argv)
	}
}

func TestCheckBuildFailsServiceImage(t *testing.T) {
	r := newCRig(t, crigOpts{current: embeddedFile, theirs: newerFile, invariants: allInvariants[:1]})
	r.compose.without, r.compose.withOverride = "config_panel_build.json", "config_panel_build.json"
	rep, _ := r.check(t)
	if rep.Ready || len(rep.Invariants) != 1 || rep.Invariants[0].OK || rep.Invariants[0].Source != ".env" {
		t.Fatalf("report = %+v", rep)
	}
}

func TestCheckRecreatedServices(t *testing.T) {
	r := newCRig(t, crigOpts{current: embeddedFile, theirs: newerFile})
	r.docker.hashes = []ServiceHash{{"panel", "old"}, {"traefik", "old"}}
	rep, _ := r.check(t)
	if strings.Join(rep.Recreated, ",") != "panel,traefik" || !rep.TraefikRecreated {
		t.Fatalf("recreated = %v", rep.Recreated)
	}
}

func TestWriteReportKeepsLatest(t *testing.T) {
	data := t.TempDir()
	a := &Report{ID: strings.Repeat("a", 32), ToVersion: "1.4.0"}
	b := &Report{ID: strings.Repeat("b", 32), ToVersion: "1.5.0", Invariants: []InvariantResult{}}
	if err := WriteReport(data, a); err != nil {
		t.Fatal(err)
	}
	if err := WriteReport(data, b); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(data, ".panel-update", "preflight"))
	if len(entries) != 1 || entries[0].Name() != b.ID {
		t.Fatalf("dirs = %v", entries)
	}
	got, err := ReadReport(data, b.ID)
	if err != nil || got.ToVersion != "1.5.0" {
		t.Fatalf("read = %+v, %v", got, err)
	}
	if _, err := ReadReport(data, a.ID); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("old report: err = %v", err)
	}
	if _, err := ReadReport(data, "../etc"); err == nil {
		t.Fatal("read a report by a path")
	}
	info, _ := os.Stat(filepath.Join(data, ".panel-update", "preflight", b.ID, "report.json"))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
}

func TestCheckScratchIsPrivate(t *testing.T) {
	r := newCRig(t, crigOpts{current: embeddedFile, theirs: newerFile})
	rep, _ := r.check(t)
	dir := filepath.Join(r.data, ".panel-update", "preflight", rep.ID)
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("scratch dir = %v, %v", info, err)
	}
}
