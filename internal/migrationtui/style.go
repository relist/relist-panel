// Package migrationtui is the terminal user interface of a server Migration
// (ADR-0019): Bubble Tea models for `relist-panel migration pull`, `serve`
// and `resume`. They only drive and show the Migration the panel performs,
// through a migrationctl.Controller, so quitting one never stops it.
package migrationtui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

var (
	titleStyle   = lipgloss.NewStyle().Bold(true)
	currentStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	failedStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
	dimStyle     = lipgloss.NewStyle().Faint(true)
	dangerStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	warnStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	okStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	boxStyle     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
)

// journey renders the stages with the current one in brackets, red when it
// failed or paused.
func journey(stages []string, current int, failed bool) string {
	parts := make([]string, len(stages))
	for i, s := range stages {
		switch {
		case i == current && failed:
			parts[i] = failedStyle.Render("[" + s + "]")
		case i == current:
			parts[i] = currentStyle.Render("[" + s + "]")
		case i < current:
			parts[i] = s
		default:
			parts[i] = dimStyle.Render(s)
		}
	}
	return strings.Join(parts, " · ")
}

// layout fits top, a scrolling window of middle starting at offset, and
// bottom into width×height, and returns the content with the offset clamped.
// A zero height (no window size yet) shows everything.
//
// Elements holding newlines count as the lines they render as. When top and
// bottom alone overflow, the last line (the keys) is kept and the lines
// above it are cut.
func layout(width, height int, top, middle, bottom []string, offset int) (string, int) {
	top, middle, bottom = split(top...), split(middle...), split(bottom...)
	avail := len(middle)
	if height > 0 {
		avail = max(1, height-len(top)-len(bottom))
	}
	offset = max(0, min(offset, len(middle)-avail))
	end := min(len(middle), offset+avail)
	lines := append(append(append([]string{}, top...), middle[offset:end]...), bottom...)
	if height > 1 && len(lines) > height {
		lines = append(lines[:height-1], lines[len(lines)-1])
	} else if height == 1 && len(lines) > 1 {
		lines = lines[len(lines)-1:]
	}
	if width > 0 {
		for i, l := range lines {
			lines[i] = ansi.Truncate(l, width, "…")
		}
	}
	return strings.Join(lines, "\n"), offset
}

// split turns rendered blocks into lines.
func split(blocks ...string) []string {
	var out []string
	for _, b := range blocks {
		out = append(out, strings.Split(b, "\n")...)
	}
	return out
}

// view wraps content in the alternate screen the TUIs use.
func view(content string) tea.View {
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}
