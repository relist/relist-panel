package migrationtui

import (
	"context"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"relist-panel/internal/migration"
)

// resumeInput asks to type RESUME before cancelling the Migration or
// resuming this server.
type resumeInput struct {
	prompt string
	input  textinput.Model
	active bool
	err    string
}

func newResumeInput(prompt string) resumeInput {
	in := textinput.New()
	in.Prompt = "> "
	in.CharLimit = 16
	return resumeInput{prompt: prompt, input: in}
}

func (r *resumeInput) open() tea.Cmd {
	r.active, r.err = true, ""
	r.input.SetValue("")
	return r.input.Focus()
}

// key handles a key press and reports whether RESUME was submitted.
func (r *resumeInput) key(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	switch msg.String() {
	case "esc":
		r.active = false
		return false, nil
	case "enter":
		if r.input.Value() != "RESUME" {
			r.err = "type RESUME to confirm"
			return false, nil
		}
		return true, nil
	}
	return false, r.update(msg)
}

func (r *resumeInput) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	r.input, cmd = r.input.Update(msg)
	return cmd
}

func (r *resumeInput) lines() []string {
	out := []string{warnStyle.Render(r.prompt), r.input.View()}
	if r.err != "" {
		out = append(out, dangerStyle.Render(r.err))
	}
	return append(out, dimStyle.Render("enter confirm · esc back"))
}

type resumeModel struct {
	c       Controller
	s       migration.SourceStatus
	loaded  bool
	in      resumeInput
	pending bool
	resumed bool
	warning string
	message string
}

// NewResume is `relist-panel migration resume`: Cancel migration before
// finish, Resume this server after it.
func NewResume(c Controller) tea.Model {
	return &resumeModel{c: c, in: newResumeInput("Type RESUME to resume this server:")}
}

func (m *resumeModel) Init() tea.Cmd { return fetchStatus(m.c) }

func (m *resumeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case statusMsg:
		if msg.Err != nil {
			m.message = msg.Err.Error()
			return m, nil
		}
		m.s, m.loaded = msg.S, true
		if (m.s.Step != migration.StepNone || m.s.ClaimedBy != "") && !m.resumed {
			return m, m.in.open()
		}
		return m, nil
	case resumeDoneMsg:
		m.pending = false
		if msg.err != nil {
			m.in.err = msg.err.Error()
			return m, nil
		}
		m.resumed, m.warning, m.in.active = true, msg.warning, false
		return m, nil
	case tea.KeyPressMsg:
		k := msg.String()
		if k == "ctrl+c" || ((!m.in.active || m.resumed) && (k == "q" || k == "esc" || k == "enter")) {
			return m, tea.Quit
		}
		if m.pending || !m.in.active {
			return m, nil
		}
		submit, cmd := m.in.key(msg)
		if !m.in.active {
			return m, tea.Quit
		}
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
	if m.in.active {
		return m, m.in.update(msg)
	}
	return m, nil
}

type resumeDoneMsg struct {
	warning string
	err     error
}

func (m *resumeModel) View() tea.View {
	out := []string{titleStyle.Render("Resume this server"), ""}
	switch {
	case m.message != "":
		out = append(out, dangerStyle.Render(m.message))
	case !m.loaded:
		out = append(out, "Reading the migration state…")
	case m.resumed:
		out = append(out, okStyle.Render("Resumed. This server's Composes are starting again."))
		if m.warning != "" {
			out = append(out, warnStyle.Render("! "+m.warning))
		}
	case m.s.Step == migration.StepNone && m.s.ClaimedBy != "":
		out = append(out, "Claimed by "+m.s.ClaimedBy+", not started yet.",
			"Resuming ends its session, so it can no longer lock this server.", "")
		out = append(out, m.in.lines()...)
		return view(joinLines(out))
	case m.s.Step == migration.StepNone:
		out = append(out, "This server is not locked for a migration.")
	default:
		if m.s.Step == migration.StepMigrated {
			line := "Migrated to " + m.s.TargetURL
			if m.s.MigratedAt != nil {
				line += " on " + m.s.MigratedAt.Local().Format("2006-01-02 15:04")
			}
			out = append(out, line+".")
		} else {
			out = append(out, "A migration is under way: "+string(m.s.Step)+".")
		}
		out = append(out, "Resuming removes the maintenance page and starts this server's Composes again.",
			"Data written on the new server since is not here.", "")
		out = append(out, m.in.lines()...)
		return view(joinLines(out))
	}
	out = append(out, "", dimStyle.Render("q quit"))
	return view(joinLines(out))
}

func joinLines(lines []string) string {
	content, _ := layout(0, 0, nil, lines, nil, 0)
	return content
}
