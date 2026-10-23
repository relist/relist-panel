// Package migrationtui is the terminal user interface of a server Migration
// (ADR-0019): Bubble Tea models for `relist-panel migration pull`, `serve`
// and `resume`. They only drive and show the Migration the panel performs,
// through a Controller, so quitting one never stops it.
package migrationtui

import (
	"fmt"
	"math"
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
// bottom into width×height, and returns the content with the offset clamped
// and how many middle lines the window shows. A zero height (no window size
// yet) shows everything.
//
// When middle overflows, a line above and below the window says how many
// lines it hides there. Elements holding newlines, or wider than width,
// count as the lines they render or wrap into. When top and bottom alone
// overflow, the last line (the keys) is kept and the lines above it are cut.
func layout(width, height int, top, middle, bottom []string, offset int) (string, int, int) {
	top, middle, bottom = wrap(width, top), wrap(width, middle), wrap(width, bottom)
	avail := len(middle)
	if height > 0 {
		avail = max(1, height-len(top)-len(bottom))
	}
	rows := pageRows(avail, len(middle))
	offset = max(0, min(offset, len(middle)-rows))
	end := min(len(middle), offset+rows)
	window := middle[offset:end]
	if rows < len(middle) && rows < avail {
		window = append(append([]string{hidden("↑", offset, "pgup")}, window...), hidden("↓", len(middle)-end, "pgdn"))
	}
	lines := append(append(append([]string{}, top...), window...), bottom...)
	if height > 1 && len(lines) > height {
		lines = append(lines[:height-1], lines[len(lines)-1])
	} else if height == 1 && len(lines) > 1 {
		lines = lines[len(lines)-1:]
	}
	return strings.Join(lines, "\n"), offset, rows
}

// pageRows is how many of n middle lines fit in avail rows: two fewer when
// they overflow, for the lines saying what is hidden, unless that leaves none.
func pageRows(avail, n int) int {
	if n <= avail || avail < 3 {
		return min(n, avail)
	}
	return avail - 2
}

// hidden says how many lines are scrolled away in one direction, or is
// blank when none are.
func hidden(arrow string, n int, key string) string {
	if n == 0 {
		return ""
	}
	return dimStyle.Render(fmt.Sprintf("  %s %d more (%s)", arrow, n, key))
}

// scrollKey moves offset for the keys every TUI pages with: pgup and pgdown
// by a window of rows, home and end to either end, where layout clamps it.
// It reports false for any other key.
func scrollKey(k string, offset, rows int) (int, bool) {
	page := max(1, rows-1)
	switch k {
	case "pgup":
		return offset - page, true
	case "pgdown":
		return offset + page, true
	case "home":
		return 0, true
	case "end":
		return math.MaxInt / 2, true
	}
	return offset, false
}

// split turns rendered blocks into lines.
func split(blocks ...string) []string {
	var out []string
	for _, b := range blocks {
		out = append(out, strings.Split(b, "\n")...)
	}
	return out
}

// wrap splits blocks into lines and wraps each to width, at spaces where it
// can. A zero width (no window size yet) leaves them as they are.
func wrap(width int, blocks []string) []string {
	lines := split(blocks...)
	if width <= 0 {
		return lines
	}
	var out []string
	for _, l := range lines {
		out = append(out, strings.Split(ansi.Wrap(l, width, ""), "\n")...)
	}
	return out
}

// view wraps content in the alternate screen the TUIs use.
func view(content string) tea.View {
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}
