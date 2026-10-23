package migrationtui

import (
	"context"
	"errors"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"relist-panel/internal/migration"
	"relist-panel/internal/migrationctl"
)

type statusMsg struct {
	S   migration.OldHostStatus
	A   migration.Activity
	Err error
}

type tokenMsg struct {
	T   migrationctl.Token
	Err error
}

var lifetimes = []struct{ label, value string }{{"1 hour", "1h"}, {"24 hours", "24h"}, {"7 days", "168h"}}

var serveStages = []string{"Token", "Connect", "Lock", "Send", "Migrated"}

// sourceSteps are the old host's lock steps in order, with what each did.
var sourceSteps = []struct {
	step  migration.Step
	label string
}{
	{migration.StepLocked, "Lock this server and cancel its queued Deployments"},
	{migration.StepStopping, "Stop its Composes"},
	{migration.StepMaintenance, "Start the maintenance page"},
	{migration.StepReady, "List the items to send"},
}

type serveModel struct {
	c      Controller
	s      migration.OldHostStatus
	a      migration.Activity
	loaded bool

	choice  int
	token   *migrationctl.Token
	confirm bool // asking whether to revoke the token on quit
	resume  resumeInput
	message string
	notice  string
	pending bool
	gone    bool

	width, height int
}

// NewServe is `relist-panel migration serve`: it creates a Migration token,
// then follows the claim, the lock and the items the new server pulls.
func NewServe(c Controller) tea.Model {
	return &serveModel{c: c, choice: 1, resume: newResumeInput("Type RESUME to cancel the migration and start this server's Composes again:")}
}

func fetchStatus(c Controller) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		s, err := c.Status(ctx)
		if err != nil {
			return statusMsg{Err: err}
		}
		a, _ := c.Activity(ctx)
		return statusMsg{S: s, A: a}
	}
}

func (m *serveModel) Init() tea.Cmd { return fetchStatus(m.c) }

func (m *serveModel) underWay() bool { return m.s.Step != migration.StepNone }

// following is when serve shows the Migration rather than the token form:
// under way, or claimed by a new server that has not started yet, whose
// session a new token would revoke.
func (m *serveModel) following() bool { return m.underWay() || m.s.ClaimedBy != "" }

// cancellable reports a Migration the old host can still call off: claimed
// or under way, and not yet Migrated. Before start, cancelling ends the
// claimed session, so the new server can no longer lock this one.
func (m *serveModel) cancellable() bool {
	return m.following() && m.s.Step != migration.StepMigrated
}

func (m *serveModel) unclaimed() bool {
	return m.token != nil && m.s.ClaimedBy == "" && !m.underWay()
}

func (m *serveModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case pollMsg:
		return m, fetchStatus(m.c)
	case statusMsg:
		if msg.Err != nil {
			m.gone = errors.Is(msg.Err, migrationctl.ErrNoPanel)
			if !m.gone {
				m.message = msg.Err.Error()
			}
			return m, poll()
		}
		m.s, m.a, m.loaded, m.gone = msg.S, msg.A, true, false
		return m, poll()
	case tokenMsg:
		m.pending = false
		if msg.Err != nil {
			m.message = msg.Err.Error()
			return m, nil
		}
		m.message, m.token = "", &msg.T
		return m, nil
	case ActionDoneMsg:
		m.pending = false
		if msg.Err != nil {
			m.message = msg.Err.Error()
			return m, nil
		}
		return m, tea.Quit
	case resumeDoneMsg:
		m.pending = false
		if msg.err != nil {
			m.resume.err = msg.err.Error()
			return m, nil
		}
		m.resume.active = false
		m.notice = "Resumed. This server's Composes are starting again."
		if msg.warning != "" {
			m.notice += " " + msg.warning
		}
		// The running poll picks the resumed state up.
		return m, nil
	case tea.KeyPressMsg:
		return m.onKey(msg)
	}
	if m.resume.active {
		return m, m.resume.update(msg)
	}
	return m, nil
}

func (m *serveModel) onKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if k == "ctrl+c" {
		// Ctrl-C asks about a live token like q does; a second one quits
		// without revoking it.
		if m.unclaimed() && !m.confirm && !m.pending {
			m.confirm = true
			return m, nil
		}
		return m, tea.Quit
	}
	if m.pending {
		return m, nil
	}
	if m.confirm {
		switch k {
		case "enter", "y", "Y":
			m.pending = true
			c := m.c
			return m, func() tea.Msg { return ActionDoneMsg{Err: c.RevokeToken(context.Background())} }
		case "n", "N":
			return m, tea.Quit
		case "esc":
			m.confirm = false
		}
		return m, nil
	}
	if m.resume.active {
		submit, cmd := m.resume.key(msg)
		if submit {
			m.pending = true
			c := m.c
			return m, func() tea.Msg {
				warning, err := c.Resume(context.Background())
				return resumeDoneMsg{warning: warning, err: err}
			}
		}
		return m, cmd
	}
	if !m.loaded {
		if k == "q" || k == "esc" {
			return m, tea.Quit
		}
		return m, nil
	}
	if m.token == nil && !m.following() {
		switch k {
		case "up", "k":
			m.choice = max(0, m.choice-1)
		case "down", "j":
			m.choice = min(len(lifetimes)-1, m.choice+1)
		case "enter":
			m.pending = true
			c, lifetime := m.c, lifetimes[m.choice].value
			return m, func() tea.Msg {
				t, err := c.CreateToken(context.Background(), lifetime)
				return tokenMsg{T: t, Err: err}
			}
		case "q", "esc":
			return m, tea.Quit
		}
		return m, nil
	}
	switch k {
	case "q", "esc":
		if m.unclaimed() {
			m.confirm = true
			return m, nil
		}
		return m, tea.Quit
	case "c":
		if m.cancellable() {
			return m, m.resume.open()
		}
	}
	return m, nil
}

func (m *serveModel) stage() int {
	switch {
	case m.s.Step == migration.StepMigrated:
		return 4
	case m.s.Step == migration.StepReady:
		return 3
	case m.underWay():
		return 2
	case m.token != nil, m.s.ClaimedBy != "":
		return 1
	}
	return 0
}

func (m *serveModel) View() tea.View {
	top := []string{titleStyle.Render("Migrate this server to another panel"), journey(serveStages, m.stage(), false), ""}
	var middle []string
	switch {
	case !m.loaded:
		middle = []string{"Reading the migration state…"}
	case m.token == nil && !m.following():
		middle = append(middle, "How long may the new server take to connect?")
		for i, l := range lifetimes {
			if i == m.choice {
				middle = append(middle, currentStyle.Render("> "+l.label))
			} else {
				middle = append(middle, "  "+l.label)
			}
		}
		if m.s.TokenExpiresAt != nil {
			middle = append(middle, "", warnStyle.Render("Creating a token revokes the one that is live."))
		}
	default:
		if m.token != nil && !m.underWay() {
			middle = append(middle, split(boxStyle.Render(fmt.Sprintf("Migration token\n\n%s\n\nExpires %s. It is shown once: copy it now.",
				m.token.Token, m.token.ExpiresAt.Local().Format("2006-01-02 15:04"))))...)
			middle = append(middle, "", "On the new server, run:", "  "+migration.TerminalCommand("pull"),
				"and enter this panel's URL and the token.", "")
		}
		middle = append(middle, oldHostLines(m.s, m.a)...)
	}
	bottom := []string{""}
	if m.gone {
		bottom = append(bottom, warnStyle.Render("The panel is not answering; retrying…"))
	}
	if m.message != "" {
		bottom = append(bottom, dangerStyle.Render(m.message))
	}
	if m.notice != "" {
		bottom = append(bottom, okStyle.Render(m.notice))
	}
	switch {
	case m.confirm:
		bottom = append(bottom, warnStyle.Render("Revoke the token? [Y/n]"))
	case m.resume.active:
		bottom = append(bottom, m.resume.lines()...)
	case m.loaded && m.token == nil && !m.following():
		bottom = append(bottom, dimStyle.Render("↑/↓ choose · enter create the token · q quit"))
	case m.cancellable():
		bottom = append(bottom, dimStyle.Render("c cancel the migration · q quit"))
	default:
		bottom = append(bottom, dimStyle.Render("q quit"))
	}
	content, _ := layout(m.width, m.height, top, middle, bottom, 0)
	return view(content)
}

// oldHostLines is the old host's Migration as lines: the claim, the lock
// steps, what is being sent, and Migrated.
func oldHostLines(s migration.OldHostStatus, a migration.Activity) []string {
	var out []string
	if s.ClaimedBy == "" && s.Step == migration.StepNone {
		return append(out, "Waiting for the new server to connect…")
	}
	if s.ClaimedBy != "" {
		out = append(out, "Claimed by "+s.ClaimedBy)
	}
	out = append(out, "")
	reached := -1
	for i, st := range sourceSteps {
		if st.step == s.Step {
			reached = i
		}
	}
	if s.Step == migration.StepMigrated {
		reached = len(sourceSteps)
	}
	for i, st := range sourceSteps {
		done := i <= reached && s.Step != migration.StepStopping || i < reached
		switch {
		case done:
			out = append(out, okStyle.Render("  ✓ ")+st.label)
		case i == reached || (i == reached+1 && s.Step != migration.StepStopping):
			out = append(out, currentStyle.Render("  ▸ "+st.label))
		default:
			out = append(out, dimStyle.Render("    "+st.label))
		}
	}
	out = append(out, "")
	if line := ActivityLine(a); line != "" && (s.Step == migration.StepReady || s.Step == migration.StepMigrated) {
		out = append(out, line)
	}
	if s.Step == migration.StepMigrated {
		out = append(out, okStyle.Render(MigratedTo(s)))
	}
	return out
}

// MigratedTo says where a Migrated server went, and when.
func MigratedTo(s migration.OldHostStatus) string {
	line := "Migrated to " + s.TargetURL
	if s.MigratedAt != nil {
		line += " on " + s.MigratedAt.Local().Format("2006-01-02 15:04")
	}
	return line
}

// ActivityLine says what the old host is sending.
func ActivityLine(a migration.Activity) string {
	sent := fmt.Sprintf("%d items sent", a.Done)
	if a.Done == 1 {
		sent = "1 item sent"
	}
	switch {
	case a.Item != "":
		return fmt.Sprintf("Sending %s (%s), %s", a.Item, migration.HumanBytes(a.Bytes), sent)
	case a.Done > 0:
		return sent
	}
	return ""
}
