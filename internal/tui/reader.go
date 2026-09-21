package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tobiasbernting/krv/v2/internal/render"
)

// The Markdown reader is one full-screen page that scrolls, with a link
// cursor over the links on it. The Overview and the comment reader are both
// one, so a description and a comment are read the same way.
//
// What a page says is built fresh for the terminal's width on every frame;
// only where it is scrolled to and which link has focus are kept between
// them.

// readerMargin is the gutter the reader keeps either side of its page, so
// text does not start against the terminal's edge.
const readerMargin = 2

type readerState struct {
	offset int // the page line drawn at the top
	focus  int // the focused link, -1 for none
}

// page is what a reader shows: lines ready to print, the links on them and
// the lines n and p jump between.
type page struct {
	// title sits above the page and does not scroll. Empty for a page that
	// carries its own heading, as the Overview does.
	title string

	lines    []string
	links    []pageLink
	sections []int

	// blocks are the runs of lines the page was assembled from, each able to
	// repaint itself with one of its own links focused.
	blocks []pageBlock
}

// pageLink is one link on a page, with where it landed in lines.
type pageLink struct {
	url   string
	spans []render.LinkSpan
}

type pageBlock struct {
	start, firstLink, links int
	focus                   func(i int) []string
}

// section marks the line about to be added as the start of a section.
func (p *page) section() { p.sections = append(p.sections, len(p.lines)) }

// text adds lines with no links on them.
func (p *page) text(lines ...string) { p.lines = append(p.lines, lines...) }

// markdown adds rendered Markdown, links and all.
func (p *page) markdown(r render.Rendered) {
	links := make([]pageLink, len(r.Links))
	for i, l := range r.Links {
		links[i] = pageLink{url: l.URL, spans: l.Spans}
	}
	p.block(r.Lines, links, r.Focus)
}

// block adds lines whose links are positioned within the block itself; focus
// repaints the block with one of them focused.
func (p *page) block(lines []string, links []pageLink, focus func(i int) []string) {
	start := len(p.lines)
	p.blocks = append(p.blocks, pageBlock{start: start, firstLink: len(p.links), links: len(links), focus: focus})
	for _, l := range links {
		spans := make([]render.LinkSpan, len(l.spans))
		for i, sp := range l.spans {
			sp.Line += start
			spans[i] = sp
		}
		p.links = append(p.links, pageLink{url: l.url, spans: spans})
	}
	p.lines = append(p.lines, lines...)
}

// focused is the page's lines with link i focused: only the block that owns
// the link is repainted, so the rest of the page is laid out once.
func (p page) focused(i int) []string {
	if i < 0 || i >= len(p.links) {
		return p.lines
	}
	out := append([]string(nil), p.lines...)
	for _, b := range p.blocks {
		if i < b.firstLink || i >= b.firstLink+b.links || b.focus == nil {
			continue
		}
		copy(out[b.start:], b.focus(i-b.firstLink))
	}
	return out
}

// linkAt is the link drawn at a column of a page line, for a mouse click.
func (p page) linkAt(line, col int) (int, bool) {
	for i, l := range p.links {
		for _, sp := range l.spans {
			if sp.Line == line && col >= sp.Start && col < sp.End {
				return i, true
			}
		}
	}
	return -1, false
}

// line is where a link starts, which is what the link cursor scrolls to.
func (l pageLink) line() int {
	if len(l.spans) == 0 {
		return 0
	}
	return l.spans[0].Line
}

func (l pageLink) lastLine() int {
	if len(l.spans) == 0 {
		return 0
	}
	return l.spans[len(l.spans)-1].Line
}

// readerPage is the page the reader is showing.
func (m Model) readerPage() page {
	if m.mode == modeOverview {
		return m.overviewPage()
	}
	return m.commentPage()
}

// readerWidth is how wide a page is laid out: the terminal less its gutters.
func (m Model) readerWidth() int { return max(1, m.width-2*readerMargin) }

// readerHeight is how many page lines are on screen at once.
func (m Model) readerHeight(p page) int {
	h := m.height - 1 // status bar
	if p.title != "" {
		h -= 2 // the title and the blank line under it
	}
	return max(1, h)
}

func (m Model) handleReaderKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.readerPage()
	visible := m.readerHeight(p)
	maxOffset := max(0, len(p.lines)-visible)
	switch key := msg.String(); key {
	case "ctrl+c":
		return m.leave(key)
	case "esc", "q":
		m.mode = modeDiff
	case "enter":
		// With a link focused enter opens it; otherwise it closes the page,
		// as it opened it.
		if m.reader.focus >= 0 && m.reader.focus < len(p.links) {
			return m.openLink(p.links[m.reader.focus].url)
		}
		m.mode = modeDiff
	case "o":
		if m.reader.focus < 0 || m.reader.focus >= len(p.links) {
			m.err = "no link focused — tab to one"
			return m, nil
		}
		return m.openLink(p.links[m.reader.focus].url)
	case "tab":
		m.focusLink(p, 1)
	case "shift+tab":
		m.focusLink(p, -1)
	case "n":
		m.jumpSection(p, 1)
	case "p":
		m.jumpSection(p, -1)
	case "r":
		if m.mode == modeOverview {
			return m.startSync(nil)
		}
	case "?":
		m.openHelp()
	case "j", "down":
		m.reader.offset = min(m.reader.offset+1, maxOffset)
	case "k", "up":
		m.reader.offset = max(m.reader.offset-1, 0)
	case "ctrl+d", "pgdown":
		m.reader.offset = min(m.reader.offset+visible/2, maxOffset)
	case "ctrl+u", "pgup":
		m.reader.offset = max(m.reader.offset-visible/2, 0)
	case "g", "home":
		m.reader.offset = 0
	case "G", "end":
		m.reader.offset = maxOffset
	}
	return m, nil
}

// focusLink moves the link cursor. From a focused link on screen it steps to
// the next one, wrapping; from none, or from one scrolling has taken off
// screen, it starts again at the first link in view. The link it lands on is
// scrolled into view.
func (m *Model) focusLink(p page, dir int) {
	if len(p.links) == 0 {
		m.status = "no links here"
		return
	}
	visible := m.readerHeight(p)
	onScreen := func(i int) bool {
		line := p.links[i].line()
		return line >= m.reader.offset && line < m.reader.offset+visible
	}
	focus := -1
	switch f := m.reader.focus; {
	case f >= 0 && f < len(p.links) && onScreen(f):
		focus = (f + dir + len(p.links)) % len(p.links)
	default:
		for i := range p.links {
			if dir < 0 {
				i = len(p.links) - 1 - i
			}
			if onScreen(i) {
				focus = i
				break
			}
		}
	}
	if focus < 0 {
		// Nothing in view: the nearest link in that direction, wrapping.
		focus = map[bool]int{true: 0, false: len(p.links) - 1}[dir > 0]
		for i := range p.links {
			if dir < 0 {
				i = len(p.links) - 1 - i
			}
			if (dir > 0 && p.links[i].line() >= m.reader.offset) ||
				(dir < 0 && p.links[i].line() < m.reader.offset) {
				focus = i
				break
			}
		}
	}
	m.reader.focus = focus
	m.scrollTo(p, p.links[focus].line(), p.links[focus].lastLine())
}

// scrollTo brings the lines from first to last into view, moving as little
// as it takes.
func (m *Model) scrollTo(p page, first, last int) {
	visible := m.readerHeight(p)
	if first < m.reader.offset {
		m.reader.offset = first
	}
	if last >= m.reader.offset+visible {
		m.reader.offset = last - visible + 1
	}
	m.reader.offset = max(0, min(m.reader.offset, max(0, len(p.lines)-visible)))
}

// jumpSection puts the next or previous section at the top of the page, so
// its heading is the first thing read rather than the last.
func (m *Model) jumpSection(p page, dir int) {
	if len(p.sections) == 0 {
		return
	}
	seek := func(at int) {
		m.reader.offset = max(0, min(at, max(0, len(p.lines)-m.readerHeight(p))))
	}
	if dir > 0 {
		for _, at := range p.sections {
			if at > m.reader.offset {
				seek(at)
				return
			}
		}
		m.status = "last section"
		return
	}
	for i := len(p.sections) - 1; i >= 0; i-- {
		if p.sections[i] < m.reader.offset {
			seek(p.sections[i])
			return
		}
	}
	m.status = "first section"
}

// openLink opens the focused link in the browser, or copies it when that
// cannot work (see package browser).
func (m Model) openLink(url string) (tea.Model, tea.Cmd) {
	if url == "" {
		m.err = "this link has no address"
		return m, nil
	}
	m.status = "opening " + url
	return m, browse(url, m.clip)
}

// handleReaderMouse gives the reader the wheel and click-to-open.
func (m Model) handleReaderMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch msg.Button {
	case tea.MouseButtonWheelDown:
		return m.handleReaderKey(keyRune('j'))
	case tea.MouseButtonWheelUp:
		return m.handleReaderKey(keyRune('k'))
	case tea.MouseButtonLeft:
		if msg.Action != tea.MouseActionPress {
			return m, nil
		}
		p := m.readerPage()
		line := m.reader.offset + msg.Y
		if p.title != "" {
			line -= 2
		}
		i, ok := p.linkAt(line, msg.X-readerMargin)
		if !ok {
			return m, nil
		}
		m.reader.focus = i
		return m.openLink(p.links[i].url)
	}
	return m, nil
}

func (m Model) readerView() string {
	p := m.readerPage()
	lines := p.focused(m.reader.focus)
	visible := m.readerHeight(p)
	offset := max(0, min(m.reader.offset, max(0, len(lines)-visible)))
	surface := m.surface()
	margin := surface.Render(strings.Repeat(" ", readerMargin))

	var b strings.Builder
	if p.title != "" {
		b.WriteString(padStyled(surface, margin+p.title, m.width) + "\n")
		b.WriteString(surface.Render(pad("", m.width)) + "\n")
	}
	for i := 0; i < visible; i++ {
		line := ""
		if idx := offset + i; idx < len(lines) {
			line = lines[idx]
		}
		b.WriteString(padStyled(surface, margin+line, m.width) + "\n")
	}

	left := fmt.Sprintf(" %d/%d", min(offset+1, max(1, len(lines))), max(1, len(lines)))
	switch {
	case m.err != "":
		left += "  ·  " + m.err
	case m.status != "":
		left += "  ·  " + m.status
	case m.reader.focus >= 0 && m.reader.focus < len(p.links):
		left += "  ·  " + p.links[m.reader.focus].url
	}
	b.WriteString(bar(m.theme, m.width, left, fitHint(m.width, left, m.hintKeys())))
	return b.String()
}
