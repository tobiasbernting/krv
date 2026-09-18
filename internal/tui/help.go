package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

type helpEntry struct {
	keys, desc string
	// only hides the entry when it returns false; nil always shows it.
	only func(Model) bool
}

type helpSection struct {
	title   string
	entries []helpEntry
	only    func(Model) bool
}

func inFollowUp(m Model) bool { return m.follow.session != nil }
func withMouse(m Model) bool  { return m.cfg.Mouse }

// helpContent is every key crv's diff view answers to, grouped by what it is
// for, then a few recipes that string them together.
var helpContent = []helpSection{
	{title: "Move", entries: []helpEntry{
		{keys: "j / k, ↓ / ↑", desc: "line down / up"},
		{keys: "ctrl+d / ctrl+u", desc: "half a page down / up"},
		{keys: "pgdown / pgup", desc: "half a page down / up"},
		{keys: "g / G, home / end", desc: "top / bottom"},
		{keys: "n / p", desc: "next / previous hunk"},
		{keys: "tab / shift+tab", desc: "next / previous file"},
		{keys: "J / K, ] / [", desc: "next / previous file, as tab"},
		{keys: "h / l, ← / →", desc: "scroll sideways"},
		{keys: "0", desc: "back to the left margin"},
	}},
	{title: "View", entries: []helpEntry{
		{keys: "s", desc: "split or unified layout, for this session"},
		{keys: "f", desc: "file list"},
		{keys: "x", desc: "mark this file reviewed, and move on"},
		{keys: "enter", desc: "expand a thread, or open a comment in full"},
	}},
	{title: "Comment", entries: []helpEntry{
		{keys: "c", desc: "draft a comment on this line or the selection"},
		{keys: "v", desc: "start or clear a selection"},
		{keys: "e", desc: "edit the draft under the cursor"},
		{keys: "d", desc: "delete the draft under the cursor"},
		{keys: "m", desc: "re-anchor a draft whose line moved"},
		{keys: "ctrl+e", desc: "while typing: finish in $EDITOR"},
	}},
	{title: "Select & copy", entries: []helpEntry{
		{keys: "v", desc: "start or clear a selection, then move"},
		{keys: "drag", desc: "select lines with the mouse", only: withMouse},
		{keys: "y", desc: "copy the selection, line, hunk or path as code"},
		{keys: "Y", desc: "copy a reference: src/api.go:L12-L18"},
	}},
	{title: "Follow-up", only: inFollowUp, entries: []helpEntry{
		{keys: "t", desc: "your threads"},
		{keys: "a", desc: "changes since your latest review"},
		{keys: "D", desc: "the current pull request diff"},
		{keys: "N / P", desc: "next / previous thread with new activity"},
		{keys: "x / c / R", desc: "in a thread: verify / reply / resolve or reopen"},
	}},
	{title: "GitHub", entries: []helpEntry{
		{keys: "r", desc: "sync the diff and threads"},
		{keys: "S", desc: "submit the review"},
	}},
	{title: "Mouse", only: withMouse, entries: []helpEntry{
		{keys: "wheel", desc: "scroll; the cursor stays unless it leaves the screen"},
		{keys: "shift+wheel", desc: "scroll sideways"},
		{keys: "click", desc: "move the cursor there"},
		{keys: "double-click", desc: "open, as enter"},
		{keys: "drag", desc: "select lines; at the edge it scrolls"},
		{keys: "shift+drag", desc: "the terminal's own selection (option in iTerm2)"},
	}},
	{title: "General", entries: []helpEntry{
		{keys: "?", desc: "this help"},
		{keys: "esc", desc: "back to the diff"},
		{keys: "q, ctrl+c", desc: "quit"},
	}},
	{title: "Recipes", entries: []helpEntry{
		{keys: "comment on lines", desc: "v, move, then c"},
		{keys: "code into a chat", desc: "select, then y; Y for where it is"},
		{keys: "file by file", desc: "x as each file is done; f shows what is left"},
		{keys: "your threads", desc: "t, enter, then x verify, c reply, R resolve", only: inFollowUp},
		{keys: "finish", desc: "S, then comment, approve or request changes"},
		{keys: "copy as usual", desc: "hold shift while dragging (option in iTerm2)", only: withMouse},
	}},
}

// helpSections is the help that applies to this review.
func (m Model) helpSections() []helpSection {
	var out []helpSection
	for _, s := range helpContent {
		if s.only != nil && !s.only(m) {
			continue
		}
		kept := s
		kept.entries = nil
		for _, e := range s.entries {
			if e.only == nil || e.only(m) {
				kept.entries = append(kept.entries, e)
			}
		}
		out = append(out, kept)
	}
	return out
}

// helpTwoColumns is the width from which help sets its sections side by side.
const helpTwoColumns = 120

// helpLines lays the sections out for the terminal's width, and says which
// line each section's title landed on.
func (m Model) helpLines() (lines []string, titleAt map[string]int) {
	sections := m.helpSections()
	// One key column for the whole page, so every description starts at the
	// same place.
	keyWidth := 0
	for _, s := range sections {
		for _, e := range s.entries {
			keyWidth = max(keyWidth, runewidth.StringWidth(e.keys))
		}
	}
	blocks := make([][]string, len(sections))
	for i, s := range sections {
		blocks[i] = m.helpBlock(s, keyWidth)
	}
	titleAt = map[string]int{}
	if m.width < helpTwoColumns {
		for i, block := range blocks {
			titleAt[sections[i].title] = len(lines)
			lines = append(lines, block...)
		}
		return lines, titleAt
	}

	// Fill the left column until it holds half the lines, the right with the
	// rest, keeping each section whole.
	total := 0
	for _, block := range blocks {
		total += len(block)
	}
	var left, right []string
	for i, block := range blocks {
		if len(left) < total/2 {
			titleAt[sections[i].title] = len(left)
			left = append(left, block...)
		} else {
			titleAt[sections[i].title] = len(right)
			right = append(right, block...)
		}
	}
	col := m.width / 2
	surface := m.surface()
	for i := 0; i < max(len(left), len(right)); i++ {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		lines = append(lines, padStyled(surface, l, col)+r)
	}
	return lines, titleAt
}

// helpBlock is one section: its title, its entries, a blank line.
func (m Model) helpBlock(s helpSection, keyWidth int) []string {
	bg := lipgloss.Color(m.theme.Bg)
	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Fg)).Background(bg).Bold(true)
	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Accent)).Background(bg).Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Fg)).Background(bg)
	surface := m.surface()

	lines := []string{surface.Render("  ") + titleStyle.Render(s.title)}
	for _, e := range s.entries {
		lines = append(lines, surface.Render("    ")+keyStyle.Render(runewidth.FillRight(e.keys, keyWidth+2))+descStyle.Render(e.desc))
	}
	return append(lines, "")
}

// openHelp shows help, at the section for the screen it was opened from.
func (m *Model) openHelp() {
	m.helpReturn = m.mode
	m.mode = modeHelp
	m.helpTop = 0
	if m.helpReturn == modeThreads || m.helpReturn == modeThread {
		_, titleAt := m.helpLines()
		m.helpTop = m.clampHelpTop(titleAt["Follow-up"])
	}
}

func (m Model) helpHeight() int { return max(1, m.height-2) }

func (m Model) clampHelpTop(top int) int {
	lines, _ := m.helpLines()
	return max(0, min(top, len(lines)-m.helpHeight()))
}

func (m Model) handleHelpKey(key string) Model {
	switch key {
	case "q", "esc", "?":
		m.mode = m.helpReturn
	case "j", "down":
		m.helpTop = m.clampHelpTop(m.helpTop + 1)
	case "k", "up":
		m.helpTop = m.clampHelpTop(m.helpTop - 1)
	case "ctrl+d", "pgdown":
		m.helpTop = m.clampHelpTop(m.helpTop + m.helpHeight()/2)
	case "ctrl+u", "pgup":
		m.helpTop = m.clampHelpTop(m.helpTop - m.helpHeight()/2)
	case "g", "home":
		m.helpTop = 0
	case "G", "end":
		m.helpTop = m.clampHelpTop(1 << 30)
	}
	return m
}

func (m Model) helpView() string {
	lines, _ := m.helpLines()
	surface := m.surface()
	height := m.helpHeight()
	top := m.clampHelpTop(m.helpTop)

	var b strings.Builder
	b.WriteString(bar(m.theme, m.width, " crv — keys", "") + "\n")
	for i := 0; i < height; i++ {
		line := ""
		if idx := top + i; idx < len(lines) {
			line = lines[idx]
		}
		b.WriteString(padStyled(surface, line, m.width) + "\n")
	}
	left := " j/k scroll  q/esc/? back"
	right := ""
	if top+height < len(lines) {
		right = "↓ more below"
	}
	b.WriteString(bar(m.theme, m.width, left, right))
	return b.String()
}
