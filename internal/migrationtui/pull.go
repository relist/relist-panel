package migrationtui

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"relist-panel/internal/migration"
	"relist-panel/internal/migrationctl"
)

// ProgressMsg is one poll of the new host's progress.
type ProgressMsg struct {
	P   migration.Progress
	Err error
}

// ActionDoneMsg ends an action the operator asked for; P is set when the
// panel answered with the new progress.
type ActionDoneMsg struct {
	P   *migration.Progress
	Err error
}

type pollMsg struct{}

const (
	pollEvery = 500 * time.Millisecond
	// giveUpAfter is how long the panel may stay silent while it
	// re-executes before the TUI says it did not come back.
	giveUpAfter = 2 * time.Minute
	logTail     = 6
)

var pullStages = []string{"Connect", "Preview", "Lock old server", "Copy", "Finalize", "Done"}

type pullModel struct {
	c   Controller
	now func() time.Time

	p      migration.Progress
	loaded bool

	inputs  []textinput.Model
	focus   int
	formErr string

	selected    map[string]bool
	selectedFor string
	// includeRegistry is the Include registry data tick, off by default.
	includeRegistry bool
	cursor          int

	confirm string // "", "start" or "abort"
	pending string
	message string // the last action's refusal, kept until the next action
	pollErr string // the last poll's error, cleared by a good poll

	goneSince time.Time
	// readingSince is when the preview started waiting for the old panel's
	// sizes, Volumes, Images and host paths.
	readingSince time.Time
	restarting   bool
	gaveUp       bool

	width, height int
	// offset is the first middle line shown, rows how many show and
	// cursorLine where the preview's cursor is (-1 without one), as the last
	// View laid them out. follow keeps the cursor in view; it is set when the
	// cursor moves and cleared when the operator scrolls.
	offset, rows, cursorLine int
	follow                   bool
	showLog                  bool
	logOffset, logRows       int
	bar                      progress.Model
}

// NewPull is `relist-panel migration pull`: it connects, previews, starts
// and follows a Migration into this panel. defaultSelfURL prefills this
// panel's own URL.
func NewPull(c Controller, defaultSelfURL string) tea.Model {
	return newPull(c, defaultSelfURL, time.Now)
}

func newPull(c Controller, defaultSelfURL string, now func() time.Time) *pullModel {
	inputs := make([]textinput.Model, 3)
	for i := range inputs {
		inputs[i] = textinput.New()
		inputs[i].Prompt = ""
		inputs[i].CharLimit = 2048
		inputs[i].SetWidth(60)
	}
	inputs[1].EchoMode = textinput.EchoPassword
	inputs[2].SetValue(defaultSelfURL)
	inputs[0].Focus()
	return &pullModel{c: c, now: now, inputs: inputs,
		bar: progress.New(progress.WithWidth(16), progress.WithoutPercentage())}
}

func (m *pullModel) Init() tea.Cmd { return m.fetch }

func (m *pullModel) fetch() tea.Msg {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, err := m.c.Progress(ctx)
	return ProgressMsg{P: p, Err: err}
}

func poll() tea.Cmd {
	return tea.Tick(pollEvery, func(time.Time) tea.Msg { return pollMsg{} })
}

func (m *pullModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case pollMsg:
		return m, m.fetch
	case ProgressMsg:
		m.onProgress(msg)
		return m, poll()
	case ActionDoneMsg:
		m.pending = ""
		m.message = ""
		if msg.Err != nil {
			m.message = msg.Err.Error()
		}
		if msg.P != nil {
			m.setProgress(*msg.P)
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.onKey(msg)
	}
	if m.loaded && m.p.Phase == migration.PhaseIdle {
		var cmd tea.Cmd
		m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *pullModel) onProgress(msg ProgressMsg) {
	if msg.Err != nil {
		if errors.Is(msg.Err, migrationctl.ErrNoPanel) {
			if m.goneSince.IsZero() {
				m.goneSince = m.now()
			}
			m.restarting = true
			m.gaveUp = m.now().Sub(m.goneSince) > giveUpAfter
			return
		}
		m.pollErr = msg.Err.Error()
		return
	}
	m.goneSince, m.restarting, m.gaveUp, m.pollErr = time.Time{}, false, false, ""
	m.setProgress(msg.P)
}

func (m *pullModel) setProgress(p migration.Progress) {
	m.p, m.loaded = p, true
	if p.Phase != migration.PhaseConnected || p.Info != nil {
		m.readingSince = time.Time{}
	} else if m.readingSince.IsZero() {
		m.readingSince = m.now()
	}
	switch {
	case p.Phase == migration.PhaseIdle:
		m.selected, m.selectedFor, m.includeRegistry = nil, "", false
	case p.Phase == migration.PhaseConnected && p.Info != nil && (m.selected == nil || m.selectedFor != p.SourceURL):
		// The panel picked the default (no system paths, nothing in its
		// data directory) or kept the operator's choice: take it as it is.
		m.selected, m.selectedFor, m.includeRegistry = map[string]bool{}, p.SourceURL, p.IncludeRegistry
		for _, path := range p.Selected {
			m.selected[path] = true
		}
	}
}

func (m *pullModel) selectedPaths() []string {
	var out []string
	for path, on := range m.selected {
		if on {
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}

// registryOffered reports whether the old server has Registry data to move:
// the Include registry data row follows the host paths only then.
func (m *pullModel) registryOffered() bool {
	return m.p.Info != nil && m.p.Info.Registry != nil
}

func (m *pullModel) blocked() bool {
	return m.p.Info == nil || slices.ContainsFunc(m.p.Checks, func(c migration.Check) bool { return c.Blocking })
}

// act runs one action against the panel in the background.
func (m *pullModel) act(what string) tea.Cmd {
	m.pending = what
	c := m.c
	switch what {
	case "start":
		paths, registry := m.selectedPaths(), m.includeRegistry && m.registryOffered()
		return func() tea.Msg { return ActionDoneMsg{Err: c.Start(context.Background(), paths, registry)} }
	case "abort":
		return func() tea.Msg { return ActionDoneMsg{Err: c.Abort(context.Background())} }
	case "retry":
		return func() tea.Msg { return ActionDoneMsg{Err: c.Retry(context.Background())} }
	case "recheck":
		paths, registry := m.selectedPaths(), m.includeRegistry && m.registryOffered()
		return func() tea.Msg {
			p, err := c.Recheck(context.Background(), paths, registry)
			if err != nil {
				return ActionDoneMsg{Err: err}
			}
			return ActionDoneMsg{P: &p}
		}
	case "connect":
		source, token, self := strings.TrimSpace(m.inputs[0].Value()), strings.TrimSpace(m.inputs[1].Value()), strings.TrimSpace(m.inputs[2].Value())
		if m.p.Phase == migration.PhaseConnected {
			// Preflight failed after the token was claimed: retry on the
			// same session, with the URL this panel was given then.
			source, token = m.p.SourceURL, ""
			if m.p.SelfURL != "" {
				self = m.p.SelfURL
			}
		}
		return func() tea.Msg {
			p, err := c.Connect(context.Background(), source, token, self)
			if err != nil {
				return ActionDoneMsg{Err: err}
			}
			return ActionDoneMsg{P: &p}
		}
	}
	m.pending = ""
	return nil
}

func (m *pullModel) onKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if k == "ctrl+c" {
		return m, tea.Quit
	}
	if m.confirm != "" {
		what := m.confirm
		m.confirm = ""
		if k == "y" {
			return m, m.act(what)
		}
		return m, nil
	}
	if m.showLog {
		switch k {
		case "l", "esc", "q":
			m.showLog = false
		case "j", "down":
			m.logOffset++
		case "k", "up":
			m.logOffset--
		default:
			m.logOffset, _ = scrollKey(k, m.logOffset, m.logRows)
		}
		return m, nil
	}
	if !m.loaded {
		if k == "q" || k == "esc" {
			return m, tea.Quit
		}
		return m, nil
	}
	switch m.p.Phase {
	case migration.PhaseIdle:
		return m.onFormKey(msg)
	case migration.PhaseConnected:
		return m.onPreviewKey(k)
	}
	switch k {
	case "q", "esc":
		return m, tea.Quit
	case "j", "down":
		m.offset++
	case "k", "up":
		m.offset--
	case "l":
		m.showLog, m.logOffset = true, len(m.p.Log)
	case "r":
		if m.p.CanRetry && m.pending == "" {
			return m, m.act("retry")
		}
	case "a":
		if m.p.CanAbort && m.pending == "" {
			m.confirm = "abort"
		}
	default:
		m.offset, _ = scrollKey(k, m.offset, m.rows)
	}
	return m, nil
}

// cursorShown reports whether the last View showed the preview's cursor.
// Before any View, or without a window size, it counts as shown.
func (m *pullModel) cursorShown() bool {
	return m.height == 0 || m.rows == 0 || (m.cursorLine >= m.offset && m.cursorLine < m.offset+m.rows)
}

func (m *pullModel) onFormKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return m, tea.Quit
	case "tab", "down":
		m.focus = (m.focus + 1) % len(m.inputs)
	case "shift+tab", "up":
		m.focus = (m.focus + len(m.inputs) - 1) % len(m.inputs)
	case "enter":
		if m.focus < len(m.inputs)-1 {
			m.focus++
			break
		}
		return m, m.submit()
	default:
		var cmd tea.Cmd
		m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
		return m, cmd
	}
	for i := range m.inputs {
		if i == m.focus {
			m.inputs[i].Focus()
		} else {
			m.inputs[i].Blur()
		}
	}
	return m, nil
}

func webURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func (m *pullModel) submit() tea.Cmd {
	switch {
	case m.pending != "":
		return nil
	case !webURL(strings.TrimSpace(m.inputs[0].Value())):
		m.formErr = "Old panel URL: enter an http:// or https:// URL"
	case strings.TrimSpace(m.inputs[1].Value()) == "":
		m.formErr = "Migration token: paste the token the old panel showed"
	case !webURL(strings.TrimSpace(m.inputs[2].Value())):
		m.formErr = "This panel's URL: enter an http:// or https:// URL"
	default:
		m.formErr = ""
		return m.act("connect")
	}
	return nil
}

func (m *pullModel) onPreviewKey(k string) (tea.Model, tea.Cmd) {
	var paths []migration.HostPathCandidate
	if m.p.Info != nil {
		paths = m.p.Info.HostPaths
	}
	rowCount := len(paths)
	if m.registryOffered() {
		rowCount++ // Include registry data
	}
	switch k {
	case "q", "esc":
		return m, tea.Quit
	// In view, ↑ and ↓ move the cursor, and past either end of the host
	// paths they scroll the preview on, so what is above and below the list
	// can be read. With the cursor scrolled out of view, space, or a key
	// toward it, first brings it back; a key away from it scrolls on.
	case "up", "k", "down", "j", "space":
		up := k == "up" || k == "k"
		above, below := m.cursorLine < m.offset, m.cursorLine >= m.offset+m.rows
		if rowCount > 0 && !m.cursorShown() && (k == "space" || (up && above) || (!up && below)) {
			m.follow = true
			break
		}
		switch {
		case k == "space":
			if m.cursor < len(paths) && m.pending == "" {
				path := paths[m.cursor].Path
				m.selected[path] = !m.selected[path]
				return m, m.act("recheck")
			}
			if m.cursor == len(paths) && m.registryOffered() && m.pending == "" {
				m.includeRegistry = !m.includeRegistry
				return m, m.act("recheck") // its size and a Registry already here are checks
			}
		case up && m.cursor > 0 && m.cursorShown():
			m.cursor, m.follow = m.cursor-1, true
		case up:
			m.offset, m.follow = m.offset-1, false
		case m.cursor < rowCount-1 && m.cursorShown():
			m.cursor, m.follow = m.cursor+1, true
		default:
			m.offset, m.follow = m.offset+1, false
		}
	case "r":
		if m.pending != "" {
			break
		}
		if m.p.Info == nil {
			return m, m.act("connect")
		}
		return m, m.act("recheck")
	case "enter":
		if m.pending != "" {
			break
		}
		if m.blocked() {
			m.message = "Fix the blocking checks first; r rechecks"
			break
		}
		m.confirm = "start"
	case "a":
		if m.pending == "" {
			m.confirm = "abort"
		}
	default:
		if off, ok := scrollKey(k, m.offset, m.rows); ok {
			m.offset, m.follow = off, false
		}
	}
	return m, nil
}

func (m *pullModel) stage() int {
	switch m.p.Phase {
	case migration.PhaseConnected:
		return 1
	case migration.PhaseLocking:
		return 2
	case migration.PhasePulling, migration.PhasePaused:
		return 3
	case migration.PhaseFinalizing:
		return 4
	case migration.PhaseDone:
		return 5
	}
	return 0
}

func (m *pullModel) View() tea.View {
	if m.showLog {
		lines := logLines(m.p.Log)
		content, off, rows := layout(m.width, m.height, []string{titleStyle.Render("Activity log"), ""}, lines, []string{"", dimStyle.Render("j/k scroll · l back")}, m.logOffset)
		m.logOffset, m.logRows = off, rows
		return view(content)
	}
	failed := m.p.Phase == migration.PhasePaused || (m.p.Error != "" && m.p.Phase != migration.PhaseConnected)
	top := []string{titleStyle.Render("Migrate from another panel"), journey(pullStages, m.stage(), failed), ""}
	var middle, bottom []string
	cursorLine := -1
	switch {
	case !m.loaded:
		middle = []string{"Reading the migration state…"}
	case m.p.Phase == migration.PhaseIdle:
		middle = m.formLines()
	case m.p.Phase == migration.PhaseConnected:
		middle, cursorLine = m.previewLines()
	case m.p.Phase == migration.PhaseDone:
		middle = m.doneLines()
	default:
		top = append(top, m.followHeader()...)
		middle = m.itemLines()
		if tail := logLines(m.p.Log); len(tail) > 0 {
			bottom = append(bottom, "")
			bottom = append(bottom, tail[max(0, len(tail)-logTail):]...)
		}
	}
	bottom = append(bottom, m.footer()...)
	top, bottom = wrap(m.width, top), wrap(m.width, bottom)
	if cursorLine >= 0 {
		// Lines above the cursor may wrap, which moves it down.
		cursorLine = len(wrap(m.width, middle[:cursorLine]))
	}

	if cursorLine >= 0 && m.height > 0 && m.follow {
		rows := pageRows(max(1, m.height-len(top)-len(bottom)), len(wrap(m.width, middle)))
		if cursorLine < m.offset {
			m.offset = cursorLine
		} else if cursorLine >= m.offset+rows {
			m.offset = cursorLine - rows + 1
		}
	}
	content, off, rows := layout(m.width, m.height, top, middle, bottom, m.offset)
	m.offset, m.rows, m.cursorLine = off, rows, cursorLine
	return view(content)
}

func (m *pullModel) formLines() []string {
	labels := []string{"Old panel URL", "Migration token", "This panel's URL (the old panel shows it as where it moved)"}
	var out []string
	for i, in := range m.inputs {
		label := "  " + labels[i]
		if i == m.focus {
			label = currentStyle.Render("> " + labels[i])
		}
		out = append(out, label, "    "+in.View(), "")
	}
	if m.formErr != "" {
		out = append(out, dangerStyle.Render(m.formErr))
	}
	if m.pending == "connect" {
		out = append(out, "Connecting…")
	}
	return out
}

func (m *pullModel) previewLines() ([]string, int) {
	p := m.p
	out := []string{fmt.Sprintf("Connected to %s. Nothing there is locked until you start.", p.SourceURL), ""}
	if p.Info == nil {
		// Connect claims the token before it reads the old panel, so a
		// poll can land between the two.
		if p.Error == "" {
			out = append(out, fmt.Sprintf("Reading the old panel: sizes, Volumes, Images and host paths… %ds",
				int(m.now().Sub(m.readingSince).Seconds())))
			if m.pending == "" {
				// Nothing here is reading it: a TUI started after the connect.
				out = append(out, "", dimStyle.Render("r reads it again if this stays"))
			}
			return out, -1
		}
		return append(out, dangerStyle.Render(p.Error), "", "r retries on the same session"), -1
	}
	info := p.Info
	var volumes, images, paths, registryBytes int64
	if info.Registry != nil && m.includeRegistry {
		registryBytes = info.Registry.Size
	}
	for _, v := range info.Volumes {
		volumes += v.Size
	}
	digests := 0
	for _, i := range info.Images {
		images += i.Size
		if i.Image != nil && len(i.Image.RepoDigests) > 0 {
			digests++
		}
	}
	for _, hp := range info.HostPaths {
		if m.selected[hp.Path] {
			paths += hp.Size
		}
	}
	if len(p.Checks) == 0 {
		out = append(out, okStyle.Render("✓ Every check passed"))
	}
	for _, c := range p.Checks {
		if c.Blocking {
			out = append(out, split(dangerStyle.Render("✗ "+c.Message))...)
		} else {
			out = append(out, split(warnStyle.Render("! "+c.Message))...)
		}
	}
	out = append(out, "", titleStyle.Render("Volumes"))
	if len(info.Volumes) == 0 {
		out = append(out, dimStyle.Render("  none"))
	}
	for _, it := range info.Volumes {
		name, driver, options := it.ID, "local", ""
		if v := it.Volume; v != nil {
			name, options = v.Name, formatOptions(v.Options)
			if v.Driver != "" {
				driver = v.Driver
			}
			if migration.UnrestorableVolume(*v) != "" {
				driver, options = dangerStyle.Render(driver), dangerStyle.Render(options)
			}
		}
		out = append(out, fmt.Sprintf("  %-24s %-8s %-20s %s", name, driver, options, migration.HumanBytes(it.Size)))
	}
	out = append(out, "", fmt.Sprintf("%s  %d pulled by digest, %d copied", titleStyle.Render("Images"), digests, len(info.Images)-digests))
	for _, it := range info.Images {
		name, how := it.ID, "copied"
		if img := it.Image; img != nil {
			name = migration.ShortID(img.ID)
			if len(img.Tags) > 0 {
				name = img.Tags[0]
			}
			if len(img.RepoDigests) > 0 {
				how = "pulled by digest"
			}
		}
		out = append(out, fmt.Sprintf("  %-32s %-17s %s", name, how, migration.HumanBytes(it.Size)))
	}
	out = append(out, "", titleStyle.Render("Host paths")+dimStyle.Render("  space ticks, r rechecks"))
	cursorLine := -1
	for i, hp := range info.HostPaths {
		mark, box := "  ", "[ ]"
		if i == m.cursor {
			mark, cursorLine = "> ", len(out)
		}
		if m.selected[hp.Path] {
			box = "[x]"
		}
		line := fmt.Sprintf("%s%s %s  %s", mark, box, hp.Path, migration.HumanBytes(hp.Size))
		if hp.System {
			line += dimStyle.Render("  system")
		}
		if len(hp.Composes) > 0 {
			line += dimStyle.Render("  used by " + strings.Join(hp.Composes, ", "))
		}
		out = append(out, line)
	}
	if len(info.HostPaths) == 0 {
		out = append(out, dimStyle.Render("  none outside the data directory"))
	}
	if info.Registry != nil {
		mark, box := "  ", "[ ]"
		if m.cursor == len(info.HostPaths) {
			mark, cursorLine = "> ", len(out)
		}
		if m.includeRegistry {
			box = "[x]"
		}
		out = append(out, fmt.Sprintf("%s%s Include registry data (%s)", mark, box, migration.HumanBytes(info.Registry.Size))+
			dimStyle.Render("  CI can push again instead"))
	}
	out = append(out, "", titleStyle.Render("Sizes"))
	for _, s := range []struct {
		label string
		size  int64
	}{
		{"data directory", info.DataDirSize},
		{"database", info.DatabaseSize},
		{"Volumes", volumes},
		{"host paths", paths},
		{"Registry data", registryBytes},
		{"Images", images},
	} {
		out = append(out, fmt.Sprintf("  %-16s %s", s.label, migration.HumanBytes(s.size)))
	}
	return out, cursorLine
}

func formatOptions(o map[string]string) string {
	keys := slices.Sorted(maps.Keys(o))
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + o[k]
	}
	return strings.Join(parts, ", ")
}

var lockSteps = map[migration.Step]string{
	migration.StepLocked:      "locked, its queued Deployments canceled",
	migration.StepStopping:    "waiting for a running Deployment, then stopping its Composes",
	migration.StepMaintenance: "maintenance page up",
	migration.StepReady:       "stopped and ready: sending items",
	migration.StepMigrated:    "migrated",
}

var finalizeSteps = []struct {
	step  migration.FinalizeStep
	label string
}{
	{migration.FinalizeStopTraefik, "Stop Traefik"},
	{migration.FinalizeMoveData, "Move the copied data into place"},
	{migration.FinalizeRestart, "Restart the panel with the migrated database"},
	{migration.FinalizeStartTraefik, "Start Traefik with the copied certificates"},
	{migration.FinalizeDeploy, "Deploy the Composes that were running"},
	{migration.FinalizeFinish, "Tell the old server the migration finished"},
}

func (m *pullModel) followHeader() []string {
	p := m.p
	var out []string
	if p.Phase == migration.PhaseFinalizing {
		reached := slices.IndexFunc(finalizeSteps, func(s struct {
			step  migration.FinalizeStep
			label string
		}) bool {
			return s.step == p.FinalizeStep
		})
		for i, s := range finalizeSteps {
			switch {
			case i < reached:
				out = append(out, okStyle.Render("  ✓ ")+s.label)
			case i == reached:
				out = append(out, currentStyle.Render("  ▸ "+s.label))
			default:
				out = append(out, dimStyle.Render("    "+s.label))
			}
		}
		out = append(out, "")
	} else if p.LockStep == migration.StepStopping && p.Info != nil && p.Info.ActiveDeployment != "" {
		out = append(out, "Old server: waiting for Deployment "+p.Info.ActiveDeployment+" to finish, then stopping its Composes", "")
	} else if step, ok := lockSteps[p.LockStep]; ok {
		out = append(out, "Old server: "+step, "")
	} else if p.Phase == migration.PhaseLocking {
		out = append(out, "Old server: locking…", "")
	}
	if p.Error != "" {
		out = append(out, dangerStyle.Render(p.Error), "")
	}
	return out
}

func (m *pullModel) itemLines() []string {
	var out []string
	done := 0
	for _, it := range m.p.Items {
		if it.State == migration.ItemDone {
			done++
		}
	}
	if len(m.p.Items) > 0 {
		out = append(out, fmt.Sprintf("Items: %d/%d done", done, len(m.p.Items)))
	}
	for _, it := range m.p.Items {
		frac := 0.0
		if it.Size > 0 {
			frac = min(1, float64(it.Done)/float64(it.Size))
		}
		if it.State == migration.ItemDone {
			frac = 1
		}
		state := string(it.State)
		switch {
		case it.State == migration.ItemRunning && it.Attempts > 1:
			state = fmt.Sprintf("retrying %d/3", it.Attempts)
		case it.State == migration.ItemRunning:
			state = "copying"
		case it.State == migration.ItemPending:
			state = "waiting"
		}
		// The ID comes last, so a narrow window cuts the ID, never the state.
		line := fmt.Sprintf("  %-12s %-9s %s %9s / %-9s %s", state, it.Kind, m.bar.ViewAs(frac),
			migration.HumanBytes(it.Done), migration.HumanBytes(it.Size), it.ID)
		switch it.State {
		case migration.ItemFailed:
			line = dangerStyle.Render(line)
		case migration.ItemDone:
			line = okStyle.Render(line)
		}
		out = append(out, line)
		if it.Error != "" && it.State != migration.ItemDone {
			out = append(out, dangerStyle.Render("      "+it.Error))
		}
	}
	return out
}

func (m *pullModel) doneLines() []string {
	out := []string{okStyle.Render("Migration complete."), "Sign in with the admin account of the old server.", ""}
	s := m.p.Summary
	if s == nil {
		return out
	}
	if len(s.DNS) > 0 {
		out = append(out, titleStyle.Render("DNS to change")+dimStyle.Render("  point these at this server, then turn the old one off"))
		for _, host := range s.DNS {
			out = append(out, "  "+host)
		}
		out = append(out, "")
	}
	if len(s.Deployments) > 0 {
		out = append(out, titleStyle.Render("Deployments started"))
		for _, d := range s.Deployments {
			if d.Error != "" {
				out = append(out, dangerStyle.Render(fmt.Sprintf("  Compose %d: %s", d.ComposeID, d.Error)))
			} else {
				out = append(out, fmt.Sprintf("  Compose %d · Deployment #%d", d.ComposeID, d.Number))
			}
		}
		out = append(out, "")
	}
	for _, w := range s.Warnings {
		out = append(out, warnStyle.Render("! "+w))
	}
	return out
}

func logLines(log []migration.LogEntry) []string {
	out := make([]string, 0, len(log))
	for _, e := range log {
		line := e.At.Local().Format("15:04:05") + "  " + e.Message
		switch e.Level {
		case "warn":
			line = warnStyle.Render(line)
		case "error":
			line = dangerStyle.Render(line)
		default:
			line = dimStyle.Render(line)
		}
		out = append(out, line)
	}
	return out
}

func (m *pullModel) footer() []string {
	out := []string{""}
	if m.restarting {
		switch {
		case m.gaveUp:
			out = append(out, dangerStyle.Render("the panel did not come back: run docker compose up -d in the install directory"))
		case m.p.Phase == migration.PhaseFinalizing:
			out = append(out, warnStyle.Render("Restarting the panel…"))
		default:
			out = append(out, warnStyle.Render("The panel is not answering; retrying…"))
		}
	}
	if m.pollErr != "" {
		out = append(out, dangerStyle.Render(m.pollErr))
	}
	if m.message != "" {
		out = append(out, dangerStyle.Render(m.message))
	}
	if m.pending != "" && m.pending != "connect" {
		out = append(out, "Working…")
	}
	switch m.confirm {
	case "start":
		out = append(out, warnStyle.Render("Start the migration? The old server stops every Compose and locks until you resume it. [y/N]"))
		return out
	case "abort":
		out = append(out, warnStyle.Render("Abort? The old server resumes and starts its Composes again. Everything copied here is deleted. [y/N]"))
		return out
	}
	var help string
	switch {
	case !m.loaded:
		help = "q quit"
	case m.p.Phase == migration.PhaseIdle:
		help = "enter next · tab move · esc quit"
	case m.p.Phase == migration.PhaseConnected && m.blocked():
		help = "↑/↓ move · space tick · r recheck · start blocked · a abort · q quit"
	case m.p.Phase == migration.PhaseConnected:
		help = "↑/↓ move · space tick · r recheck · enter start · a abort · q quit"
	case m.p.Phase == migration.PhaseDone:
		help = "l log · q quit"
	default:
		parts := []string{"j/k scroll", "l log"}
		if m.p.CanRetry {
			parts = append(parts, "r retry")
		}
		if m.p.CanAbort {
			parts = append(parts, "a abort")
		}
		help = strings.Join(append(parts, "q quit"), " · ")
	}
	return append(out, dimStyle.Render(help))
}

// QuitNote is the line printed after a TUI ends, saying what goes on without
// it; "" when there is nothing to say.
func QuitNote(m tea.Model) string {
	if s, ok := m.(*serveModel); ok {
		switch {
		case s.unclaimed():
			return fmt.Sprintf("The migration token stays live until %s. Run %s to follow it.",
				s.token.ExpiresAt.Local().Format("2006-01-02 15:04"), migration.TerminalCommand("serve"))
		case s.following() && s.s.Step != migration.StepMigrated:
			return "The migration goes on. Run " + migration.TerminalCommand("serve") + " to follow it again."
		}
		return ""
	}
	if p, ok := m.(*pullModel); ok {
		switch p.p.Phase {
		case migration.PhaseLocking, migration.PhasePulling, migration.PhasePaused, migration.PhaseFinalizing:
			return "The migration goes on. Run " + migration.TerminalCommand("pull") + " to follow it again."
		case migration.PhaseConnected:
			return fmt.Sprintf("Connected to %s; nothing is locked yet. Run %s to start, or to abort.", p.p.SourceURL, migration.TerminalCommand("pull"))
		}
	}
	return ""
}
