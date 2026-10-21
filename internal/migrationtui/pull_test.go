package migrationtui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"relist-panel/internal/migration"
	"relist-panel/internal/migrationctl"
)

// fakeController records what the TUI asked the panel to do.
type fakeController struct {
	mu        sync.Mutex
	connected [][]string
	rechecked [][]string
	started   [][]string
	retried   int
	aborted   int
	startErr  error

	status      migration.SourceStatus
	activity    migration.Activity
	tokens      []string
	revoked     int
	resumed     int
	resumeWarn  string
	createdTok  migrationctl.Token
	progress    migration.Progress
	progressErr error
}

func (f *fakeController) Progress(context.Context) (migration.Progress, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.progress, f.progressErr
}

func (f *fakeController) Connect(_ context.Context, sourceURL, token, selfURL string) (migration.Progress, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connected = append(f.connected, []string{sourceURL, token, selfURL})
	return migration.Progress{Phase: migration.PhaseConnected, SourceURL: sourceURL}, nil
}

func (f *fakeController) Recheck(_ context.Context, paths []string) (migration.Progress, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rechecked = append(f.rechecked, slices.Clone(paths))
	return f.progress, nil
}

func (f *fakeController) Start(_ context.Context, paths []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, slices.Clone(paths))
	return f.startErr
}

func (f *fakeController) Retry(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.retried++
	return nil
}

func (f *fakeController) Abort(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.aborted++
	return nil
}

func (f *fakeController) CreateToken(_ context.Context, lifetime string) (migrationctl.Token, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens = append(f.tokens, lifetime)
	return f.createdTok, nil
}

func (f *fakeController) RevokeToken(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked++
	return nil
}

func (f *fakeController) Status(context.Context) (migration.SourceStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status, nil
}

func (f *fakeController) Resume(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resumed++
	return f.resumeWarn, nil
}

func (f *fakeController) Activity(context.Context) (migration.Activity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.activity, nil
}

// key builds a key press: "enter", "esc", "space", "tab", "up", "down",
// "ctrl+c", or a single printable character.
func key(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	case "ctrl+u":
		return tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

// send feeds msg to m, then runs the command it returned one level deep,
// feeding back what an action produced. Commands that do not answer within
// 100 ms (ticks) are dropped.
func send(t *testing.T, m tea.Model, msg tea.Msg) tea.Model {
	t.Helper()
	m, cmd := m.Update(msg)
	for _, out := range runCmd(cmd) {
		switch out.(type) {
		case ActionDoneMsg, ProgressMsg, tokenMsg, statusMsg, resumeDoneMsg:
			m, _ = m.Update(out)
		}
	}
	return m
}

func runCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		if batch, ok := msg.(tea.BatchMsg); ok {
			var out []tea.Msg
			for _, c := range batch {
				out = append(out, runCmd(c)...)
			}
			return out
		}
		return []tea.Msg{msg}
	case <-time.After(100 * time.Millisecond):
		return nil
	}
}

func typeText(t *testing.T, m tea.Model, s string) tea.Model {
	t.Helper()
	for _, r := range s {
		m = send(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return m
}

func screen(m tea.Model) string { return ansi.Strip(m.View().Content) }

func mustContain(t *testing.T, m tea.Model, want ...string) {
	t.Helper()
	got := screen(m)
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Fatalf("screen lacks %q:\n%s", w, got)
		}
	}
}

func connected() migration.Progress {
	return migration.Progress{
		Phase:     migration.PhaseConnected,
		SourceURL: "https://old.example.com",
		Selected:  []string{"/srv/up"},
		Info: &migration.SourceInfo{
			DataDir: "/var/lib/relist-panel",
			HostPaths: []migration.HostPathCandidate{
				{Path: "/srv/up", Size: 2048, Composes: []string{"store"}},
				{Path: "/etc/localtime", System: true, Composes: []string{"store"}},
			},
		},
	}
}

func pull(c *fakeController, p migration.Progress) tea.Model {
	m := NewPull(c, "https://new.example.com")
	m, _ = m.Update(ProgressMsg{P: p})
	return m
}

func TestPullStartsOnConnectWhenIdle(t *testing.T) {
	c := &fakeController{}
	m := pull(c, migration.Progress{})
	mustContain(t, m, "Old panel URL", "Migration token", "This panel's URL", "https://new.example.com")
	m = typeText(t, m, "https://old.example.com")
	m = send(t, m, key("enter"))
	m = typeText(t, m, "tok")
	m = send(t, m, key("enter"))
	m = send(t, m, key("enter"))
	if len(c.connected) != 1 || !slices.Equal(c.connected[0], []string{"https://old.example.com", "tok", "https://new.example.com"}) {
		t.Fatalf("connect calls = %q", c.connected)
	}
	if strings.Contains(screen(m), "tok\n") {
		t.Fatal("the token is shown in clear")
	}
}

func TestPullRefusesABadURL(t *testing.T) {
	c := &fakeController{}
	m := pull(c, migration.Progress{})
	m = typeText(t, m, "ftp://x")
	m = send(t, m, key("enter"))
	m = typeText(t, m, "tok")
	m = send(t, m, key("enter"))
	m = send(t, m, key("enter"))
	mustContain(t, m, "enter an http:// or https:// URL")
	if len(c.connected) != 0 {
		t.Fatalf("connected with a bad URL: %q", c.connected)
	}
}

func TestPullPreviewDisablesStartWhileBlocked(t *testing.T) {
	c := &fakeController{}
	p := connected()
	p.Checks = []migration.Check{{Blocking: true, Message: "RELIST_DATA_DIR differs: set it to /srv/relist on this host"}}
	m := pull(c, p)
	mustContain(t, m, "RELIST_DATA_DIR differs: set it to /srv/relist on this host")
	m = send(t, m, key("enter"))
	m = send(t, m, key("y"))
	if len(c.started) != 0 {
		t.Fatal("started while a check blocks")
	}
}

func TestPullPreviewShowsSizesVolumesAndImages(t *testing.T) {
	c := &fakeController{}
	p := connected()
	p.Info.Volumes = []migration.Item{
		{ID: "volume-pgdata", Kind: migration.KindVolume, Size: 2 << 30, Volume: &migration.VolumeInfo{Name: "pgdata", Driver: "local"}},
		{ID: "volume-nfsdata", Kind: migration.KindVolume, Volume: &migration.VolumeInfo{Name: "nfsdata", Driver: "local", Options: map[string]string{"type": "nfs"}}},
	}
	p.Info.Images = []migration.Item{
		{ID: "image-a", Kind: migration.KindImage, Image: &migration.ImageInfo{ID: "a", RepoDigests: []string{"postgres@sha256:1"}}},
		{ID: "image-b", Kind: migration.KindImage, Image: &migration.ImageInfo{ID: "b"}},
	}
	m := pull(c, p)
	mustContain(t, m, "Volumes", "pgdata", "2.0 GiB", "nfsdata", "type=nfs", "1 pulled by digest", "1 copied")
	m = send(t, m, key("r"))
	if len(c.rechecked) != 1 || !slices.Equal(c.rechecked[0], []string{"/srv/up"}) {
		t.Fatalf("recheck calls = %q", c.rechecked)
	}
}

func TestPullPreviewLeavesSystemPathsUnticked(t *testing.T) {
	c := &fakeController{}
	m := pull(c, connected())
	mustContain(t, m, "[x] /srv/up", "[ ] /etc/localtime")
	m = send(t, m, key("down"))
	m = send(t, m, key("space"))
	if len(c.rechecked) != 1 || !slices.Equal(c.rechecked[0], []string{"/etc/localtime", "/srv/up"}) {
		t.Fatalf("recheck after ticking /etc/localtime = %q", c.rechecked)
	}
}

func TestPullStartNeedsY(t *testing.T) {
	c := &fakeController{}
	m := pull(c, connected())
	m = send(t, m, key("enter"))
	mustContain(t, m, "Start the migration? The old server stops every Compose and locks until you resume it. [y/N]")
	m = send(t, m, key("n"))
	if len(c.started) != 0 {
		t.Fatal("started on n")
	}
	if strings.Contains(screen(m), "[y/N]") {
		t.Fatal("the question stayed after n")
	}
	m = send(t, m, key("enter"))
	m = send(t, m, key("y"))
	if len(c.started) != 1 || !slices.Equal(c.started[0], []string{"/srv/up"}) {
		t.Fatalf("start calls = %q", c.started)
	}
}

func TestPullAbortNeedsY(t *testing.T) {
	c := &fakeController{}
	m := pull(c, connected())
	m = send(t, m, key("a"))
	mustContain(t, m, "Abort? The old server resumes and starts its Composes again. Everything copied here is deleted. [y/N]")
	m = send(t, m, key("esc"))
	if c.aborted != 0 {
		t.Fatal("aborted on esc")
	}
	m = send(t, m, key("a"))
	m = send(t, m, key("y"))
	if c.aborted != 1 {
		t.Fatalf("abort calls = %d", c.aborted)
	}
}

func TestPullPausedOffersRetryAndAbort(t *testing.T) {
	c := &fakeController{}
	p := connected()
	p.Phase = migration.PhasePaused
	p.Error = "volume-b failed 3 times: extract failed"
	m := pull(c, p)
	mustContain(t, m, "volume-b failed 3 times: extract failed", "r retry", "a abort")
	m = send(t, m, key("r"))
	if c.retried != 1 {
		t.Fatalf("retry calls = %d", c.retried)
	}
}

func TestPullShowsBusyAsALine(t *testing.T) {
	c := &fakeController{startErr: &migrationctl.APIError{Status: 409, Message: "another action is in progress"}}
	m := pull(c, connected())
	m = send(t, m, key("enter"))
	m, cmd := m.Update(key("y"))
	var quit bool
	for _, out := range runCmd(cmd) {
		if _, ok := out.(tea.QuitMsg); ok {
			quit = true
		}
		m, _ = m.Update(out)
	}
	if quit {
		t.Fatal("the TUI quit on a refused action")
	}
	mustContain(t, m, "another action is in progress")
}

func TestPullRestartingWhenThePanelIsGone(t *testing.T) {
	c := &fakeController{}
	p := connected()
	p.Phase = migration.PhaseFinalizing
	p.FinalizeStep = migration.FinalizeRestart
	m := pull(c, p)
	m, cmd := m.Update(ProgressMsg{Err: fmt.Errorf("%w: EOF", migrationctl.ErrNoPanel)})
	mustContain(t, m, "Restarting the panel…")
	if cmd == nil {
		t.Fatal("polling stopped when the panel went away")
	}
	for _, out := range runCmd(cmd) {
		if _, ok := out.(tea.QuitMsg); ok {
			t.Fatal("the TUI quit when the panel went away")
		}
	}
	p.FinalizeStep = migration.FinalizeStartTraefik
	m, _ = m.Update(ProgressMsg{P: p})
	if strings.Contains(screen(m), "Restarting the panel…") {
		t.Fatal("still restarting after the panel answered")
	}
}

func TestPullGivesUpAfterTwoMinutes(t *testing.T) {
	c := &fakeController{}
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	m := newPull(c, "https://new.example.com", func() time.Time { return now })
	p := connected()
	p.Phase = migration.PhaseFinalizing
	var model tea.Model = m
	model, _ = model.Update(ProgressMsg{P: p})
	gone := ProgressMsg{Err: migrationctl.ErrNoPanel}
	model, _ = model.Update(gone)
	now = now.Add(119 * time.Second)
	model, _ = model.Update(gone)
	if strings.Contains(screen(model), "did not come back") {
		t.Fatal("gave up before two minutes")
	}
	now = now.Add(2 * time.Second)
	model, _ = model.Update(gone)
	mustContain(t, model, "the panel did not come back: run docker compose up -d in the install directory")
}

func TestPullDoneListsDNSAndDeployments(t *testing.T) {
	c := &fakeController{}
	p := connected()
	p.Phase = migration.PhaseDone
	p.Summary = &migration.Summary{DNS: []string{"shop.example.com"}, Deployments: []migration.SummaryDeployment{{ComposeID: 3, Number: 7}}}
	m := pull(c, p)
	mustContain(t, m, "shop.example.com", "Compose 3", "Deployment #7", "Sign in with the admin account of the old server.")
}

func TestPullFitsASmallWindow(t *testing.T) {
	c := &fakeController{}
	p := connected()
	p.Phase = migration.PhasePulling
	for i := range 40 {
		p.Items = append(p.Items, migration.ItemProgress{ID: fmt.Sprintf("volume-%02d", i), Kind: migration.KindVolume, Size: 100, State: "pending"})
	}
	for i := range 10 {
		p.Log = append(p.Log, migration.LogEntry{At: time.Now(), Level: "info", Message: fmt.Sprintf("log line %d", i)})
	}
	m := NewPull(c, "https://new.example.com")
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(ProgressMsg{P: p})
	lines := strings.Split(strings.TrimRight(screen(m), "\n"), "\n")
	if len(lines) > 24 {
		t.Fatalf("%d lines in a 24-line window:\n%s", len(lines), screen(m))
	}
	mustContain(t, m, "Connect · Preview", "q quit", "volume-00")
	if strings.Contains(screen(m), "volume-39") {
		t.Fatal("all 40 items shown in 24 lines")
	}
	for range 40 {
		m = send(t, m, key("j"))
	}
	mustContain(t, m, "volume-39")
	if strings.Contains(screen(m), "volume-00") {
		t.Fatal("the list did not scroll")
	}
	for _, l := range strings.Split(screen(m), "\n") {
		if ansi.StringWidth(l) > 80 {
			t.Fatalf("line wider than the window: %q", l)
		}
	}
}

func TestPullJourneyHighlightsTheStage(t *testing.T) {
	cases := map[migration.Phase]string{
		migration.PhaseIdle:       "[Connect]",
		migration.PhaseConnected:  "[Preview]",
		migration.PhaseLocking:    "[Lock old server]",
		migration.PhasePulling:    "[Copy]",
		migration.PhasePaused:     "[Copy]",
		migration.PhaseFinalizing: "[Finalize]",
		migration.PhaseDone:       "[Done]",
	}
	for phase, want := range cases {
		p := connected()
		p.Phase = phase
		if phase == migration.PhaseIdle {
			p = migration.Progress{}
		}
		m := pull(&fakeController{}, p)
		first := strings.SplitN(screen(m), "\n", 3)
		if len(first) < 2 || !strings.Contains(first[1], want) {
			t.Errorf("phase %q: journey line = %q, want %q", phase, first, want)
		}
	}
}

func TestQuitNote(t *testing.T) {
	p := connected()
	p.Phase = migration.PhasePulling
	if note := QuitNote(pull(&fakeController{}, p)); !strings.Contains(note, "relist-panel migration pull") {
		t.Fatalf("note while pulling = %q", note)
	}
	p.Phase = migration.PhaseDone
	if note := QuitNote(pull(&fakeController{}, p)); note != "" {
		t.Fatalf("note when done = %q", note)
	}
	if note := QuitNote(pull(&fakeController{}, migration.Progress{})); note != "" {
		t.Fatalf("note with nothing started = %q", note)
	}
}

func TestPullFitsMultiLineErrorsInASmallWindow(t *testing.T) {
	p := connected()
	p.Phase = migration.PhaseFinalizing
	p.Error = "start traefik: exit status 1\nError response from daemon: driver failed\nprogramming external connectivity"
	m := NewPull(&fakeController{}, "https://new.example.com")
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m, _ = m.Update(ProgressMsg{P: p})
	lines := strings.Split(screen(m), "\n")
	if len(lines) > 12 {
		t.Fatalf("%d lines in a 12-line window:\n%s", len(lines), screen(m))
	}
	if !strings.Contains(lines[len(lines)-1], "q quit") {
		t.Fatalf("the keys line fell off the bottom:\n%s", screen(m))
	}
	if !strings.Contains(lines[1], "[Finalize]") {
		t.Fatalf("the journey line is gone:\n%s", screen(m))
	}
}

func TestPullShowsItemStateAt80Columns(t *testing.T) {
	p := connected()
	p.Phase = migration.PhasePulling
	p.Items = []migration.ItemProgress{{ID: "host-path-srv-a-rather-long-directory-name", Kind: migration.KindHostPath, Size: 100, Done: 40, State: "running", Attempts: 2}}
	m := NewPull(&fakeController{}, "https://new.example.com")
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(ProgressMsg{P: p})
	mustContain(t, m, "retrying 2/3")
}

func TestPullPreviewKeepsTheCursorVisibleUnderMultiLineChecks(t *testing.T) {
	p := connected()
	p.Checks = []migration.Check{{Message: "line one\nline two\nline three\nline four\nline five\nline six"}}
	for i := range 20 {
		p.Info.HostPaths = append(p.Info.HostPaths, migration.HostPathCandidate{Path: fmt.Sprintf("/srv/p%02d", i)})
	}
	m := NewPull(&fakeController{}, "https://new.example.com")
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	m, _ = m.Update(ProgressMsg{P: p})
	for range 21 {
		m = send(t, m, key("down"))
	}
	mustContain(t, m, "> [ ] /srv/p19")
}

func TestPullRetriesPreflightWithTheStoredSelfURL(t *testing.T) {
	c := &fakeController{}
	p := connected()
	p.Info, p.Error, p.SelfURL = nil, "source busy", "http://10.0.0.2:8888"
	m := NewPull(c, "") // a re-run without RELIST_DOMAIN
	m, _ = m.Update(ProgressMsg{P: p})
	mustContain(t, m, "source busy")
	m = send(t, m, key("r"))
	if len(c.connected) != 1 || !slices.Equal(c.connected[0], []string{"https://old.example.com", "", "http://10.0.0.2:8888"}) {
		t.Fatalf("retry connect = %q, want the stored selfUrl", c.connected)
	}
}

func TestPullOffersRetryWhenALockOrFinalizeStepFails(t *testing.T) {
	for _, phase := range []migration.Phase{migration.PhaseLocking, migration.PhasePulling, migration.PhaseFinalizing} {
		c := &fakeController{}
		p := connected()
		p.Phase, p.Error = phase, "start traefik: no traefik service"
		m := pull(c, p)
		mustContain(t, m, "r retry")
		m = send(t, m, key("r"))
		if c.retried != 1 {
			t.Fatalf("%s with an error: retry calls = %d", phase, c.retried)
		}
	}
	c := &fakeController{}
	p := connected()
	p.Phase = migration.PhasePulling
	m := pull(c, p)
	if strings.Contains(screen(m), "r retry") {
		t.Fatal("retry offered while pulling without an error")
	}
}

func TestPullTakesTheServersSelection(t *testing.T) {
	c := &fakeController{}
	p := connected()
	p.Selected = []string{} // the operator unticked everything on the web
	m := pull(c, p)
	mustContain(t, m, "[ ] /srv/up", "[ ] /etc/localtime")
}

func TestPullClearsAPollErrorWhenPollsRecover(t *testing.T) {
	p := connected()
	p.Phase = migration.PhasePulling
	m := pull(&fakeController{}, p)
	m, _ = m.Update(ProgressMsg{Err: errors.New("the panel answered 500")})
	mustContain(t, m, "the panel answered 500")
	m, _ = m.Update(ProgressMsg{P: p})
	if strings.Contains(screen(m), "the panel answered 500") {
		t.Fatal("the poll error stayed after a good poll")
	}
}

func TestPullKeepsAnActionErrorAcrossPolls(t *testing.T) {
	c := &fakeController{startErr: &migrationctl.APIError{Status: 409, Message: "another action is in progress"}}
	m := pull(c, connected())
	m = send(t, m, key("enter"))
	m = send(t, m, key("y"))
	m, _ = m.Update(ProgressMsg{P: connected()})
	mustContain(t, m, "another action is in progress")
}

func TestPullSaysWhichDeploymentTheOldServerWaitsFor(t *testing.T) {
	p := connected()
	p.Phase, p.LockStep = migration.PhaseLocking, migration.StepStopping
	p.Info.ActiveDeployment = "api #12"
	m := pull(&fakeController{}, p)
	mustContain(t, m, "Old server: waiting for Deployment api #12 to finish, then stopping its Composes")
}

func TestPullPreviewSaysItIsReadingBeforeTheOldPanelAnswers(t *testing.T) {
	p := connected()
	p.Info = nil
	m := pull(&fakeController{}, p)
	mustContain(t, m, "Reading the old panel")
	if strings.Contains(screen(m), "Could not read") {
		t.Fatalf("a preflight still under way reads as a failure:\n%s", screen(m))
	}
}

func TestPullPreviewShowsAPreflightErrorWithRetry(t *testing.T) {
	p := connected()
	p.Info, p.Error = nil, "dial tcp: connection refused"
	m := pull(&fakeController{}, p)
	mustContain(t, m, "dial tcp: connection refused", "r retries on the same session")
}

func TestPullPreviewListsSizesOnePerLineBelowTheHostPaths(t *testing.T) {
	p := connected()
	p.Info.DataDirSize, p.Info.DatabaseSize = 1024, 4096
	got := screen(pull(&fakeController{}, p))
	sizes, hostPaths := strings.Index(got, "Sizes"), strings.Index(got, "Host paths")
	if sizes < 0 || hostPaths < 0 || sizes < hostPaths {
		t.Fatalf("Sizes should come after Host paths:\n%s", got)
	}
	for _, want := range []string{"data directory", "database", "Volumes", "host paths", "Images"} {
		found := false
		for _, line := range strings.Split(got[sizes:], "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), want) && !strings.Contains(line, "·") {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("no line of its own for %q under Sizes:\n%s", want, got[sizes:])
		}
	}
}
