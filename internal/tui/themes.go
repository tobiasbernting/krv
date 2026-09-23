package tui

import (
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/tobiasbernting/krv/v2/internal/config"
	"github.com/tobiasbernting/krv/v2/internal/render"
)

// ThemeSaver writes a chosen theme to the user's configuration and returns
// where. config.SaveTheme is the real one; tests pass a stand-in.
type ThemeSaver func(name string) (path string, err error)

// themeSettings is what picking a theme needs beyond the theme on screen.
type themeSettings struct {
	color bool
	// syntax is the configured chroma style, which overrides every theme's
	// own, previewed ones included.
	syntax string
	// from is where the configured theme came from; a save to the user file
	// does not show on the next run when something nearer sets it.
	from string
	save ThemeSaver
}

func newThemeSettings(cfg config.Config, save ThemeSaver) themeSettings {
	if save == nil {
		save = config.SaveTheme
	}
	return themeSettings{color: cfg.Color, syntax: cfg.Syntax, from: cfg.ThemeFrom, save: save}
}

// theme is the preset by name, with the configured syntax style over it.
func (s themeSettings) theme(name string) render.Theme {
	th, _ := render.ThemeByName(name)
	if s.syntax != "" {
		th.Syntax = s.syntax
	}
	return th
}

// keep saves the picker's choice and returns the status line saying so: where
// it went, and what will still win over it on the next run. The theme the
// picker opened on is left alone rather than saved, so a configuration that
// names a chroma style keeps it.
func (s themeSettings) keep(p themePicker) (status, failure string) {
	name := p.name()
	if name == p.original.Name {
		return "theme: " + name + ", unchanged", ""
	}
	path, err := s.save(name)
	if err != nil {
		return "", "theme: " + name + " · could not save: " + err.Error()
	}
	if s.from == "" || s.from == path {
		return "theme: " + name + " · saved to " + tildePath(path), ""
	}
	// What overrides the save is the news; where it went can wait.
	winner := s.from
	if strings.ContainsRune(winner, os.PathSeparator) {
		winner = filepath.Base(winner)
	}
	return "theme: " + name + " · saved, but " + winner + " wins here", ""
}

func tildePath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if rest, ok := strings.CutPrefix(path, home+string(os.PathSeparator)); ok {
		return "~/" + rest
	}
	return path
}

// themePicker is the list T opens over whatever is on screen. Moving through
// it restyles the screen behind it, which is the preview; enter keeps the
// theme and esc puts back the one it opened on.
type themePicker struct {
	open     bool
	cursor   int // index into the names of pickerRows
	original render.Theme
}

type pickerRow struct {
	heading string
	name    string
}

// pickerRows is the list as drawn: each group's heading, then its presets.
func pickerRows() []pickerRow {
	var rows []pickerRow
	for _, g := range render.ThemeGroups() {
		rows = append(rows, pickerRow{heading: g.Label})
		for _, n := range g.Names {
			rows = append(rows, pickerRow{name: n})
		}
	}
	return rows
}

func pickerNames() []string {
	var names []string
	for _, r := range pickerRows() {
		if r.name != "" {
			names = append(names, r.name)
		}
	}
	return names
}

// pickResult is what a key did to the picker.
type pickResult int

const (
	pickNone pickResult = iota
	pickMove
	pickAccept
	pickCancel
)

// start opens the picker on the theme in use.
func (p *themePicker) start(current render.Theme) {
	p.open, p.original, p.cursor = true, current, 0
	for i, n := range pickerNames() {
		if n == current.Name {
			p.cursor = i
		}
	}
}

func (p *themePicker) key(key string) pickResult {
	names := pickerNames()
	switch key {
	case "j", "down":
		if p.cursor < len(names)-1 {
			p.cursor++
			return pickMove
		}
	case "k", "up":
		if p.cursor > 0 {
			p.cursor--
			return pickMove
		}
	case "g", "home":
		p.cursor = 0
		return pickMove
	case "G", "end":
		p.cursor = len(names) - 1
		return pickMove
	case "enter":
		p.open = false
		return pickAccept
	case "esc", "q", "T":
		p.open = false
		return pickCancel
	}
	return pickNone
}

func (p themePicker) name() string { return pickerNames()[p.cursor] }

const pickerHint = "j/k preview  enter save  esc cancel"

// overlay draws the picker over the bottom-right of a rendered screen, above
// its last line, which is the status bar on every screen that offers it. A
// screen shorter than the terminal is stretched to it first, so the picker is
// never cut short by how little there is to show.
func (p themePicker) overlay(view string, t render.Theme, width, height int) string {
	lines := strings.Split(strings.TrimSuffix(view, "\n"), "\n")
	if short := height - len(lines); short > 0 {
		last := len(lines) - 1
		lines = append(lines[:last], append(make([]string, short), lines[last])...)
	}
	box := p.box(t, len(lines)-1)
	if len(box) == 0 || lipgloss.Width(box[0]) > width {
		return view
	}
	boxW := lipgloss.Width(box[0])
	top := len(lines) - 1 - len(box)
	for i, b := range box {
		// Lines past the end of a document are empty, so the part left of
		// the box is padded out to where the box starts.
		left := ansi.Truncate(lines[top+i], width-boxW, "")
		left += strings.Repeat(" ", width-boxW-ansi.StringWidth(left))
		lines[top+i] = left + b
	}
	out := strings.Join(lines, "\n")
	if strings.HasSuffix(view, "\n") {
		out += "\n"
	}
	return out
}

// box renders the list in at most maxHeight lines, scrolled to keep the
// selection in view.
func (p themePicker) box(t render.Theme, maxHeight int) []string {
	rows := pickerRows()
	inner := 0
	for _, r := range rows {
		inner = max(inner, lipgloss.Width(r.heading), lipgloss.Width(r.name)+2)
	}
	inner += 2

	// The border takes two lines.
	visible := min(len(rows), maxHeight-2)
	if visible < 1 {
		return nil
	}
	selected, n := 0, 0
	for i, r := range rows {
		if r.name != "" {
			if n == p.cursor {
				selected = i
			}
			n++
		}
	}
	first := max(0, min(selected-visible/2, len(rows)-visible))

	surface := lipgloss.NewStyle().Background(lipgloss.Color(t.NoteBg)).Foreground(lipgloss.Color(t.Fg))
	heading := surface.Foreground(lipgloss.Color(t.Dim))
	focus := lipgloss.NewStyle().Background(lipgloss.Color(t.CursorBg)).
		Foreground(lipgloss.Color(t.LineNumFocusFg)).Bold(true)
	bar := lipgloss.NewStyle().Background(lipgloss.Color(t.CursorBg)).Foreground(lipgloss.Color(t.CursorBar))

	var body []string
	for i := first; i < first+visible; i++ {
		r := rows[i]
		switch {
		case r.heading != "":
			body = append(body, heading.Render(pad(" "+r.heading, inner)))
		case i == selected:
			body = append(body, bar.Render(render.FocusBar)+focus.Render(pad("  "+r.name, inner-1)))
		default:
			body = append(body, surface.Render(pad("   "+r.name, inner)))
		}
	}
	framed := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(t.Accent)).
		BorderBackground(lipgloss.Color(t.NoteBg)).
		Render(strings.Join(body, "\n"))
	return strings.Split(framed, "\n")
}

// openPicker opens the theme picker over the review, when there is colour to
// pick.
func (m *Model) openPicker() {
	if !m.themes.color {
		m.status = "colour is off"
		return
	}
	m.picker.start(m.theme)
}

func (m Model) handlePickerKey(key string) (tea.Model, tea.Cmd) {
	if key == "ctrl+c" {
		return m.leave(key)
	}
	switch m.picker.key(key) {
	case pickMove:
		m.setTheme(m.themes.theme(m.picker.name()))
	case pickCancel:
		m.setTheme(m.picker.original)
	case pickAccept:
		m.status, m.err = m.themes.keep(m.picker)
	}
	return m, nil
}

// setTheme restyles the whole review, rebuilding the document when the
// syntax style changes with it.
func (m *Model) setTheme(th render.Theme) {
	m.theme = th
	hl, ok := m.hls[th.Syntax]
	if !ok {
		hl = render.NewHighlighter(th.Syntax, m.themes.color)
		m.hls[th.Syntax] = hl
	}
	m.hl = hl
	m.rebuild()
}

func (m QueueModel) handlePickerKey(key string) (tea.Model, tea.Cmd) {
	if key == "ctrl+c" {
		return m, tea.Quit
	}
	switch m.picker.key(key) {
	case pickMove:
		m.theme = m.themes.theme(m.picker.name())
	case pickCancel:
		m.theme = m.picker.original
	case pickAccept:
		m.info, m.notice = m.themes.keep(m.picker)
	}
	return m, nil
}
