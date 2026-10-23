package migrationcli

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"relist-panel/internal/migration"
	"relist-panel/internal/migrationtui"
)

const terminalLine = "needs a terminal: run it with docker compose exec, or docker exec -it\n"

// fakePanel answers the control routes from what the test sets.
type fakePanel struct {
	mu       sync.Mutex
	progress []migration.Progress // served in order, the last one repeats
	status   migration.OldHostStatus
	activity migration.Activity
	dropOnce bool // close the connection of the next progress request
	hits     int
}

func (f *fakePanel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case "/api/migration/pull/progress":
		if f.dropOnce && f.hits > 0 {
			f.dropOnce = false
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		p := migration.Progress{}
		if len(f.progress) > 0 {
			p = f.progress[min(f.hits, len(f.progress)-1)]
		}
		f.hits++
		json.NewEncoder(w).Encode(p)
	case "/api/migration/status":
		json.NewEncoder(w).Encode(f.status)
	case "/api/migration/serve/activity":
		json.NewEncoder(w).Encode(f.activity)
	case "/api/migration/pull/start":
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"error":"busy"}`))
	default:
		http.NotFound(w, r)
	}
}

func startPanel(t *testing.T, f *fakePanel) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "control.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(f)
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	return path
}

type result struct {
	code           int
	stdout, stderr string
}

func run(t *testing.T, env Env, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	env.Stdout, env.Stderr = &out, &errb
	if env.Stdin == nil {
		env.Stdin = strings.NewReader("")
	}
	if env.IsTerminal == nil {
		env.IsTerminal = func() bool { return false }
	}
	if env.Run == nil {
		env.Run = func(m tea.Model, _ io.Reader, _ io.Writer) (tea.Model, error) { return m, nil }
	}
	env.WatchEvery = time.Millisecond
	code := Run(args, env)
	return result{code, out.String(), errb.String()}
}

func TestNeedsATerminal(t *testing.T) {
	socket := startPanel(t, &fakePanel{})
	for _, cmd := range []string{"serve", "pull", "resume"} {
		r := run(t, Env{Socket: socket}, cmd)
		if r.code != 2 || r.stderr != terminalLine {
			t.Fatalf("%s without a terminal = %d %q", cmd, r.code, r.stderr)
		}
	}
}

func TestUnknownSubcommandPrintsUsage(t *testing.T) {
	r := run(t, Env{Socket: "/nope"}, "frobnicate")
	if r.code != 2 {
		t.Fatalf("exit = %d, want 2", r.code)
	}
	for _, want := range []string{"serve", "pull", "status", "resume"} {
		if !strings.Contains(r.stderr, want) {
			t.Fatalf("usage lacks %q:\n%s", want, r.stderr)
		}
	}
	if strings.Contains(r.stderr, "api") {
		t.Fatalf("usage lists the hidden api command:\n%s", r.stderr)
	}
}

func TestNoPanel(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "control.sock")
	r := run(t, Env{Socket: socket}, "status")
	if r.code != 1 || !strings.Contains(r.stderr, "the panel is not running in this container (no "+socket+")") {
		t.Fatalf("status without a panel = %d %q", r.code, r.stderr)
	}
}

func TestStatusNewHost(t *testing.T) {
	at := time.Date(2026, 10, 20, 9, 15, 30, 0, time.UTC)
	p := migration.Progress{Phase: migration.PhasePulling, SourceURL: "https://old.example.com", LockStep: migration.StepReady,
		Log: []migration.LogEntry{{At: at, Level: "info", Message: "Copied volume-a"}}}
	for i := range 5 {
		state := migration.ItemPending
		if i < 3 {
			state = migration.ItemDone
		}
		p.Items = append(p.Items, migration.ItemProgress{ID: "i", State: state})
	}
	got := FormatStatus(p, migration.OldHostStatus{}, migration.Activity{})
	for _, want := range []string{"Migration from https://old.example.com: pulling", "Items: 3/5 done",
		"Last: " + at.Local().Format("15:04:05") + " Copied volume-a"} {
		if !strings.Contains(got, want) {
			t.Fatalf("status lacks %q:\n%s", want, got)
		}
	}
}

func TestStatusOldHost(t *testing.T) {
	got := FormatStatus(migration.Progress{}, migration.OldHostStatus{Step: migration.StepReady, ClaimedBy: "203.0.113.9"},
		migration.Activity{Item: "volume-a", Bytes: 2048, Done: 3})
	for _, want := range []string{"This server is locked for a migration: ready", "Sending volume-a (2.0 KiB), 3 items sent"} {
		if !strings.Contains(got, want) {
			t.Fatalf("status lacks %q:\n%s", want, got)
		}
	}
}

func TestStatusNone(t *testing.T) {
	if got := FormatStatus(migration.Progress{}, migration.OldHostStatus{}, migration.Activity{}); got != "No migration on this host." {
		t.Fatalf("status = %q", got)
	}
}

func TestWatchWithNothingExitsOnce(t *testing.T) {
	socket := startPanel(t, &fakePanel{})
	r := run(t, Env{Socket: socket}, "status", "--watch")
	if r.code != 0 || r.stdout != "No migration on this host.\n" {
		t.Fatalf("watch with nothing = %d %q", r.code, r.stdout)
	}
}

func TestWatchStopsAtDone(t *testing.T) {
	src := "https://old.example.com"
	socket := startPanel(t, &fakePanel{progress: []migration.Progress{
		{Phase: migration.PhasePulling, SourceURL: src}, {Phase: migration.PhaseDone, SourceURL: src}}})
	r := run(t, Env{Socket: socket}, "status", "--watch")
	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	if r.code != 0 || !strings.Contains(lines[len(lines)-1], "done") {
		t.Fatalf("watch = %d:\n%s", r.code, r.stdout)
	}
}

func TestWatchSurvivesARestart(t *testing.T) {
	src := "https://old.example.com"
	socket := startPanel(t, &fakePanel{dropOnce: true, progress: []migration.Progress{
		{Phase: migration.PhaseFinalizing, SourceURL: src}, {Phase: migration.PhaseDone, SourceURL: src}}})
	r := run(t, Env{Socket: socket}, "status", "--watch")
	if r.code != 0 || !strings.Contains(r.stdout, "Restarting the panel…") || !strings.HasSuffix(strings.TrimSpace(r.stdout), ": done") {
		t.Fatalf("watch across a restart = %d:\n%s", r.code, r.stdout)
	}
}

func TestAPIPrintsTheBody(t *testing.T) {
	socket := startPanel(t, &fakePanel{progress: []migration.Progress{{Phase: migration.PhasePulling}}})
	r := run(t, Env{Socket: socket}, "api", "GET", "/api/migration/pull/progress")
	if r.code != 0 || !strings.Contains(r.stdout, `"phase":"pulling"`) {
		t.Fatalf("api GET = %d %q", r.code, r.stdout)
	}
	r = run(t, Env{Socket: socket}, "api", "POST", "/api/migration/pull/start", `{"hostPaths":[]}`)
	if r.code != 1 || !strings.Contains(r.stdout, "busy") {
		t.Fatalf("api POST refused = %d %q", r.code, r.stdout)
	}
}

func TestPullPassesTheDomainAsDefault(t *testing.T) {
	socket := startPanel(t, &fakePanel{})
	var got tea.Model
	r := run(t, Env{Socket: socket, Domain: "panel.example.com", IsTerminal: func() bool { return true },
		Run: func(m tea.Model, _ io.Reader, _ io.Writer) (tea.Model, error) { got = m; return m, nil }}, "pull")
	if r.code != 0 || got == nil {
		t.Fatalf("pull = %d %q", r.code, r.stderr)
	}
	got, _ = got.Update(migrationtui.ProgressMsg{})
	if !strings.Contains(ansi.Strip(got.View().Content), "https://panel.example.com") {
		t.Fatalf("pull view:\n%s", ansi.Strip(got.View().Content))
	}
}
