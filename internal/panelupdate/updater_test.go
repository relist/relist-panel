package panelupdate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeHost is the host the Updater drives. Containers answers by the number
// of `up` runs so far, so a test says what each `up` left behind.
type fakeHost struct {
	mu    sync.Mutex
	calls []string
	ups   int
	// upErr is the error of the n-th `up` (1-based).
	upErr map[int]error
	// panelImage is the image of the panel Container after the n-th `up`.
	panelImage map[int]string
	// healthy says whether the n-th `up` leaves a panel that answers.
	healthy map[int]bool
	// onUp runs inside the n-th `up`, as the new Containers would.
	onUp func(n int)
	// hangUp makes the n-th `up` wait for its context, like a hung pull.
	hangUp map[int]bool
	states []string // status.json state seen at each call
	dir    string
}

func (h *fakeHost) note(call string) {
	h.calls = append(h.calls, call)
	if st, err := ReadStatus(h.dir); err == nil {
		h.states = append(h.states, st.State)
	}
}

func (h *fakeHost) ComposeUp(ctx context.Context, _ Plan) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ups++
	h.note("up")
	if h.hangUp[h.ups] {
		h.mu.Unlock()
		<-ctx.Done()
		h.mu.Lock()
		return ctx.Err()
	}
	if h.onUp != nil {
		h.onUp(h.ups)
	}
	return h.upErr[h.ups]
}

func (h *fakeHost) ComposeStop(_ context.Context, _ Plan, service string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.note("stop " + service)
	return nil
}

func (h *fakeHost) Containers(_ context.Context, _, service string) ([]ContainerInfo, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if service == "traefik" {
		return []ContainerInfo{{ID: "t", Running: true, IPs: map[string]string{"relist-network": "172.20.0.2"}}}, nil
	}
	h.note("containers panel")
	// An old Container still being replaced is listed next to the new one.
	return []ContainerInfo{
		{ID: "gone", ImageID: "sha256:zzz", Running: false},
		{ID: "p", ImageID: h.panelImage[h.ups], Running: true},
	}, nil
}

func (h *fakeHost) Healthz(_ context.Context, ip, domain string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ip != "172.20.0.2" || domain != "panel.example.com" {
		return errors.New("wrong address: " + ip + " " + domain)
	}
	h.note("healthz")
	if !h.healthy[h.ups] {
		return errors.New("/healthz answered 502 Bad Gateway")
	}
	return nil
}

type updaterRig struct {
	u       *Updater
	host    *fakeHost
	data    string
	install string
	run     string
	dir     string
}

const (
	oldInstall = "services: {panel: {image: old}}\n"
	newInstall = "services: {panel: {image: new}}\n"
	oldEnv     = "RELIST_DOMAIN=panel.example.com\n"
	newEnv     = "RELIST_DOMAIN=panel.example.com\nRELIST_PANEL_IMAGE=new\n"
)

// newUpdaterRig lays out what the panel leaves for the Updater: a run
// directory in starting-updater with plan.json and a backup, and an Install
// directory holding the old files.
func newUpdaterRig(t *testing.T, withEnv bool) *updaterRig {
	t.Helper()
	r := &updaterRig{data: t.TempDir(), install: t.TempDir(), run: "20261101T120000Z"}
	r.dir = RunDir(r.data, r.run)
	if err := os.MkdirAll(r.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	compose := filepath.Join(r.install, "docker-compose.yml")
	override := filepath.Join(r.install, "docker-compose.override.yml")
	write := func(path, data string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(compose, oldInstall)
	write(override, "services: {}\n")
	write(filepath.Join(r.dir, "config-0.yml"), oldInstall)
	write(filepath.Join(r.dir, "config-1.yml"), "services: {}\n")
	backup := `{"config":[{"path":"` + compose + `","copy":"config-0.yml"},{"path":"` + override + `","copy":"config-1.yml"}],"env":null}`
	if withEnv {
		write(filepath.Join(r.install, ".env"), oldEnv)
		write(filepath.Join(r.dir, "env"), oldEnv)
		backup = `{"config":[{"path":"` + compose + `","copy":"config-0.yml"},{"path":"` + override + `","copy":"config-1.yml"}],"env":{"path":"` +
			filepath.Join(r.install, ".env") + `","copy":"env"}}`
	}
	write(filepath.Join(r.dir, backupMeta), backup)
	write(filepath.Join(r.dir, backupDB), "database before the update")
	write(filepath.Join(r.data, "panel.db"), "database now")
	plan := Plan{
		Run: r.run, Project: "relist-panel", InstallDir: r.install, ConfigFiles: []string{compose, override},
		Domain: "panel.example.com", TargetRef: "repo@sha256:abc", TargetImageID: "sha256:new", FromImageID: "sha256:old",
	}
	if err := writePlan(r.dir, plan, []byte(newInstall), []byte(newEnv)); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	if err := WriteStatus(r.dir, RunStatus{Run: r.run, State: StateStartingUpdater, FromVersion: "1.3.0", ToVersion: "1.4.0", FromImage: "sha256:old"}, now); err != nil {
		t.Fatal(err)
	}
	r.host = &fakeHost{
		dir: r.dir, upErr: map[int]error{},
		panelImage: map[int]string{1: "sha256:new", 2: "sha256:old"},
		healthy:    map[int]bool{1: true, 2: true},
	}
	r.u = &Updater{
		DataDir: r.data, Host: r.host, Now: func() time.Time { return now },
		HealthTimeout: 300 * time.Millisecond, HealthEvery: 5 * time.Millisecond,
	}
	return r
}

func (r *updaterRig) read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (r *updaterRig) compose() string { return filepath.Join(r.install, "docker-compose.yml") }
func (r *updaterRig) env() string     { return filepath.Join(r.install, ".env") }

func TestUpdaterSucceeds(t *testing.T) {
	r := newUpdaterRig(t, true)
	st, err := r.u.Apply(context.Background(), r.run)
	if err != nil || st.State != StateSucceeded || st.Error != "" {
		t.Fatalf("status = %+v, %v", st, err)
	}
	if got := r.read(t, r.compose()); got != newInstall {
		t.Fatalf("Install file = %q", got)
	}
	if got := r.read(t, r.env()); got != newEnv {
		t.Fatalf(".env = %q", got)
	}
	if got := r.read(t, filepath.Join(r.data, "panel.db")); got != "database now" {
		t.Fatalf("panel.db = %q: a successful update must not restore it", got)
	}
	if on, _ := ReadStatus(r.dir); on.State != StateSucceeded {
		t.Fatalf("status.json = %+v", on)
	}
	// compose-up while `up` runs, waiting-health while the health is polled.
	if r.host.states[0] != StateComposeUp || r.host.states[1] != StateWaitingHealth {
		t.Fatalf("states = %v", r.host.states)
	}
	if strings.Join(r.host.calls, ",") != "up,containers panel,healthz" {
		t.Fatalf("calls = %v", r.host.calls)
	}
}

func TestUpdaterHealthTimeoutRollsBack(t *testing.T) {
	r := newUpdaterRig(t, true)
	r.host.healthy[1] = false
	// The new panel opened the database before it failed health.
	r.host.onUp = func(n int) {
		if n == 1 {
			_ = os.WriteFile(filepath.Join(r.data, "panel.db"), []byte("database after migrating"), 0o600)
			_ = os.WriteFile(filepath.Join(r.data, "panel.db-wal"), []byte("wal"), 0o600)
			_ = os.WriteFile(filepath.Join(r.data, "panel.db-shm"), []byte("shm"), 0o600)
		}
	}
	st, err := r.u.Apply(context.Background(), r.run)
	if err != nil || st.State != StateRolledBack || !strings.Contains(st.Error, "not healthy through Traefik within") ||
		!strings.Contains(st.Error, "502") {
		t.Fatalf("status = %+v, %v", st, err)
	}
	if got := r.read(t, r.compose()); got != oldInstall {
		t.Fatalf("Install file = %q", got)
	}
	if got := r.read(t, r.env()); got != oldEnv {
		t.Fatalf(".env = %q", got)
	}
	if got := r.read(t, filepath.Join(r.data, "panel.db")); got != "database before the update" {
		t.Fatalf("panel.db = %q", got)
	}
	for _, sibling := range []string{"panel.db-wal", "panel.db-shm"} {
		if _, err := os.Stat(filepath.Join(r.data, sibling)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s survived the restore: %v", sibling, err)
		}
	}
	// Stop the panel, restore the database, then bring the old one up.
	calls := strings.Join(r.host.calls, ",")
	if i, j := strings.Index(calls, "stop panel"), strings.LastIndex(calls, "up"); i < 0 || j < i {
		t.Fatalf("calls = %s", calls)
	}
	if on, _ := ReadStatus(r.dir); on.State != StateRolledBack {
		t.Fatalf("status.json = %+v", on)
	}
}

func TestUpdaterRollbackRemovesEnvItCreated(t *testing.T) {
	r := newUpdaterRig(t, false)
	r.host.healthy[1] = false
	st, _ := r.u.Apply(context.Background(), r.run)
	if st.State != StateRolledBack {
		t.Fatalf("status = %+v", st)
	}
	if _, err := os.Stat(r.env()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(".env the update created is still there: %v", err)
	}
}

func TestUpdaterComposeUpFailureRollsBack(t *testing.T) {
	r := newUpdaterRig(t, true)
	r.host.upErr[1] = errors.New("pull access denied")
	st, _ := r.u.Apply(context.Background(), r.run)
	if st.State != StateRolledBack || !strings.Contains(st.Error, "pull access denied") {
		t.Fatalf("status = %+v", st)
	}
}

func TestUpdaterRestoreFailureIsRollbackFailed(t *testing.T) {
	r := newUpdaterRig(t, true)
	r.host.healthy[1] = false
	if err := os.Remove(filepath.Join(r.dir, backupDB)); err != nil {
		t.Fatal(err)
	}
	st, err := r.u.Apply(context.Background(), r.run)
	if err != nil || st.State != StateRollbackFailed {
		t.Fatalf("status = %+v, %v", st, err)
	}
	if !strings.Contains(st.Error, "within") || !strings.Contains(st.RollbackError, "restore panel.db") {
		t.Fatalf("both errors expected: %+v", st)
	}
	if on, _ := ReadStatus(r.dir); on.State != StateRollbackFailed || on.RollbackError == "" {
		t.Fatalf("status.json = %+v", on)
	}
	// The new panel is stopped and no second `up` ran on a half-restored host.
	if r.host.ups != 1 {
		t.Fatalf("ups = %d", r.host.ups)
	}
}

func TestUpdaterRollbackHealthFailureIsRollbackFailed(t *testing.T) {
	r := newUpdaterRig(t, true)
	r.host.healthy[1] = false
	r.host.healthy[2] = false
	st, _ := r.u.Apply(context.Background(), r.run)
	if st.State != StateRollbackFailed || !strings.Contains(st.RollbackError, "the old version") {
		t.Fatalf("status = %+v", st)
	}
}

func TestUpdaterRollbackWaitsForTheOldImage(t *testing.T) {
	r := newUpdaterRig(t, true)
	r.host.healthy[1] = false
	r.host.panelImage[2] = "sha256:new" // the old panel never came back
	st, _ := r.u.Apply(context.Background(), r.run)
	if st.State != StateRollbackFailed || !strings.Contains(st.RollbackError, "sha256:old") {
		t.Fatalf("status = %+v", st)
	}
}

func TestUpdaterHealthNeedsTheTargetImage(t *testing.T) {
	r := newUpdaterRig(t, true)
	// Traefik answers, but through the old panel Container.
	r.host.panelImage[1] = "sha256:old"
	st, _ := r.u.Apply(context.Background(), r.run)
	if st.State != StateRolledBack || !strings.Contains(st.Error, "sha256:new") {
		t.Fatalf("status = %+v", st)
	}
}

func TestUpdaterDoesNothingUnlessStarting(t *testing.T) {
	for _, state := range []string{StateBackingUp, StateWritingFiles, StateSucceeded, StateInterrupted} {
		r := newUpdaterRig(t, true)
		if err := WriteStatus(r.dir, RunStatus{Run: r.run, State: state}, time.Now()); err != nil {
			t.Fatal(err)
		}
		_, err := r.u.Apply(context.Background(), r.run)
		if !errors.Is(err, ErrNotStarting) {
			t.Fatalf("%s: err = %v", state, err)
		}
		if got := r.read(t, r.compose()); got != oldInstall || len(r.host.calls) != 0 {
			t.Fatalf("%s: the run changed the host: %q %v", state, got, r.host.calls)
		}
	}
}

func TestUpdaterKeepsFileModes(t *testing.T) {
	r := newUpdaterRig(t, true)
	if err := os.Chmod(r.env(), 0o640); err != nil {
		t.Fatal(err)
	}
	if st, _ := r.u.Apply(context.Background(), r.run); st.State != StateSucceeded {
		t.Fatalf("status = %+v", st)
	}
	info, err := os.Stat(r.env())
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf(".env mode = %v, %v", info.Mode(), err)
	}
}

func TestRestoreDatabaseLeavesPanelDBWhenAWalCannotGo(t *testing.T) {
	r := newUpdaterRig(t, true)
	db := filepath.Join(r.data, "panel.db")
	// A non-empty directory named panel.db-wal cannot be removed as a file.
	wal := db + "-wal"
	if err := os.MkdirAll(filepath.Join(wal, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := restoreDatabase(r.data, r.dir); err == nil {
		t.Fatal("want an error")
	}
	if got := r.read(t, db); got != "database now" {
		t.Fatalf("panel.db = %q: it must be untouched", got)
	}
}

func TestUpdaterRefusesABadPlan(t *testing.T) {
	good := func(r *updaterRig) Plan {
		p, err := ReadPlan(r.dir)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := map[string]func(p *Plan, r *updaterRig){
		"other run":        func(p *Plan, _ *updaterRig) { p.Run = "20200101T000000Z" },
		"empty files":      func(p *Plan, _ *updaterRig) { p.ConfigFiles = nil },
		"relative file":    func(p *Plan, _ *updaterRig) { p.ConfigFiles = []string{"docker-compose.yml"} },
		"unclean file":     func(p *Plan, r *updaterRig) { p.ConfigFiles = []string{r.install + "/../x/docker-compose.yml"} },
		"file outside dir": func(p *Plan, _ *updaterRig) { p.ConfigFiles = []string{"/etc/docker-compose.yml"} },
		"relative dir":     func(p *Plan, _ *updaterRig) { p.InstallDir = "install" },
		"unclean dir":      func(p *Plan, r *updaterRig) { p.InstallDir = r.install + "/" },
		"no project":       func(p *Plan, _ *updaterRig) { p.Project = "" },
		"no target image":  func(p *Plan, _ *updaterRig) { p.TargetImageID = "" },
		"no old image":     func(p *Plan, _ *updaterRig) { p.FromImageID = "" },
		"no domain":        func(p *Plan, _ *updaterRig) { p.Domain = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := newUpdaterRig(t, true)
			p := good(r)
			mutate(&p, r)
			if err := writePlan(r.dir, p, []byte(newInstall), []byte(newEnv)); err != nil {
				t.Fatal(err)
			}
			st, err := r.u.Apply(context.Background(), r.run)
			if err != nil || st.State != StateInterrupted || !strings.Contains(st.Error, "plan was refused") {
				t.Fatalf("status = %+v, %v", st, err)
			}
			if len(r.host.calls) != 0 || r.read(t, r.compose()) != oldInstall {
				t.Fatalf("the host was touched: %v", r.host.calls)
			}
		})
	}
	if err := good(newUpdaterRig(t, true)).Validate("20261101T120000Z", "relative"); err == nil {
		t.Fatal("a relative data dir must be rejected")
	}
}

func TestUpdaterHungComposeUpRollsBack(t *testing.T) {
	r := newUpdaterRig(t, true)
	r.host.hangUp = map[int]bool{1: true}
	r.u.UpTimeout = 30 * time.Millisecond
	st, _ := r.u.Apply(context.Background(), r.run)
	if st.State != StateRolledBack || !strings.Contains(st.Error, "did not finish within") {
		t.Fatalf("status = %+v", st)
	}
	if r.read(t, r.compose()) != oldInstall || r.read(t, r.env()) != oldEnv {
		t.Fatal("the files were not restored")
	}
}

func TestUpdaterRefusesSymlinkedFiles(t *testing.T) {
	for name, link := range map[string]func(r *updaterRig) string{
		"Install file": func(r *updaterRig) string { return r.compose() },
		"env":          func(r *updaterRig) string { return r.env() },
	} {
		t.Run(name, func(t *testing.T) {
			r := newUpdaterRig(t, true)
			path := link(r)
			target := filepath.Join(t.TempDir(), "elsewhere")
			if err := os.WriteFile(target, []byte("outside"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
			st, err := r.u.Apply(context.Background(), r.run)
			if err != nil || st.State != StateInterrupted || !strings.Contains(st.Error, "is a symlink") {
				t.Fatalf("status = %+v, %v", st, err)
			}
			if len(r.host.calls) != 0 {
				t.Fatalf("the host was touched: %v", r.host.calls)
			}
			if got, _ := os.Readlink(path); got != target || r.read(t, target) != "outside" {
				t.Fatal("the symlink or its target changed")
			}
		})
	}
}
