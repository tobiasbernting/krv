package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/tobiasbernting/krv/v2/internal/ghsrc"
	"github.com/tobiasbernting/krv/v2/internal/render"
)

// frameMsg advances the loading page's animation. It carries the generation
// of the load it belongs to, so a cancelled load's ticks die out instead of
// piling onto the next one.
type frameMsg struct{ gen int }

const frameEvery = 100 * time.Millisecond

func nextFrame(gen int) tea.Cmd {
	return tea.Tick(frameEvery, func(time.Time) tea.Msg { return frameMsg{gen: gen} })
}

var loadingLogo = []string{
	` _               `,
	`| | ___ ____   __`,
	`| |/ / '__\ \ / /`,
	`|   <| |   \ V / `,
	`|_|\_\_|    \_/  `,
}

// loadingHint is the loading page's keys, on every scene.
const loadingHint = "esc cancel · tab next"

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// loadingPage is what fills the screen while a pull request is fetched.
type loadingPage struct {
	item          ghsrc.QueueItem
	scene         int // index into scenes
	frame         int
	theme         render.Theme
	width, height int
	// trace is what the load has run so far, and now the time its running
	// entries are timed against, moved on by each frame.
	trace traceLog
	now   time.Time
}

func (p loadingPage) View() string {
	t := p.theme
	sc := scenes[p.scene%len(scenes)]
	bg := space
	if sc.themed {
		bg = t.Bg
	}
	height := p.height
	panel := p.height >= traceMinHeight
	if panel {
		height -= traceRows
	}
	c := newCanvas(p.width, height)
	var rows []string
	if sc.draw(c, p) {
		rows = c.rows(bg)
	} else {
		bg = t.Bg
		rows = p.oneLine(height)
	}
	if panel {
		tc := newCanvas(p.width, traceRows)
		drawTrace(tc, p)
		rows = append(rows, tc.rows(bg)...)
	}
	return strings.Join(rows, "\n")
}

// oneLine is the page for a screen too small for the scene: clipped or
// wrapped, a scene is noise. One line still says what is happening.
func (p loadingPage) oneLine(height int) []string {
	t := p.theme
	surface := lipgloss.NewStyle().Background(lipgloss.Color(t.Bg)).Foreground(lipgloss.Color(t.Fg))
	name := fmt.Sprintf("%s#%d", p.item.Repo, p.item.Number)
	// The title gives way first: which pull request, and how to get out,
	// are the parts that must survive.
	head, tail := "loading "+name+" ", " — "+loadingHint
	if step := p.trace.current(); step != "" {
		tail = " · " + step + tail
	}
	room := p.width - 4 - runewidth.StringWidth(head) - runewidth.StringWidth(tail)
	text := head + tail
	if room > 3 {
		text = head + runewidth.Truncate(p.item.Title, room, "…") + tail
	}
	spin := lipgloss.NewStyle().Background(lipgloss.Color(t.Bg)).Foreground(lipgloss.Color(t.Accent)).
		Render(spinner[p.frame%len(spinner)])
	line := spin + surface.Render(" ") + surface.Render(runewidth.Truncate(text, maxInt(1, p.width-4), "…"))

	top := maxInt(0, (height-1)/2)
	rows := make([]string, height)
	for i := range rows {
		if i == top {
			rows[i] = centered(surface, line, p.width)
			continue
		}
		rows[i] = surface.Render(strings.Repeat(" ", p.width))
	}
	return rows
}

// centered pads a styled line to the middle of a surface-coloured row.
func centered(surface lipgloss.Style, s string, width int) string {
	w := lipgloss.Width(s)
	left := maxInt(0, (width-w)/2)
	right := maxInt(0, width-w-left)
	return surface.Render(strings.Repeat(" ", left)) + s + surface.Render(strings.Repeat(" ", right))
}
