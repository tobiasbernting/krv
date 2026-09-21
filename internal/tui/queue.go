package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/tobiasbernting/krv/v2/internal/ghsrc"
	"github.com/tobiasbernting/krv/v2/internal/notes"
	"github.com/tobiasbernting/krv/v2/internal/render"
)

// Selection is the pull request a queue row names.
type Selection struct {
	Repo   string
	Number int
}

// QueueModel is the list of pull requests waiting on you. Choosing a row asks
// whoever runs it to open that pull request; App does, and keeps the queue
// underneath so the review can return to it.
type QueueModel struct {
	theme render.Theme
	limit int
	// fetch is where the list comes from: the client's cache in the program,
	// a stand-in in tests.
	fetch func(filter ghsrc.Filter, limit int, force bool) ([]ghsrc.QueueItem, time.Time, error)

	filter  ghsrc.Filter
	items   []ghsrc.QueueItem
	drafts  map[string]int // "repo#number" -> unsent notes
	fetched time.Time
	// requests are the list fetches running, one per filter.
	requests inflight
	err      string
	// notice is a failure that is not the list's, such as a pull request
	// that would not open. It lasts until the next key.
	notice string

	cursor        int
	width, height int

	lastPress lastPress
	now       func() time.Time
}

func NewQueue(client ghsrc.Client, theme render.Theme, limit int) QueueModel {
	return QueueModel{
		theme: theme, limit: limit,
		fetch:  client.CachedQueue,
		filter: ghsrc.FilterReviewRequested,
		drafts: map[string]int{},
		width:  80, height: 24,
		requests: inflight{},
		now:      time.Now,
	}
}

type queueLoadedMsg struct {
	filter  ghsrc.Filter
	items   []ghsrc.QueueItem
	fetched time.Time
	err     error
}

func (m QueueModel) Init() tea.Cmd { return m.load(false) }

// loading reports whether the list on screen is being fetched.
func (m QueueModel) loading() bool { return m.requests.has(reqQueue(m.filter)) }

// load fetches the list for the current filter, unless that fetch is already
// running. The request set is a map shared by every copy of the model, which
// is what lets Init, with no model to return, record the fetch it starts.
func (m QueueModel) load(force bool) tea.Cmd {
	if !m.requests.start(reqQueue(m.filter)) {
		return nil
	}
	fetch, filter, limit := m.fetch, m.filter, m.limit
	return func() tea.Msg {
		items, fetched, err := fetch(filter, limit, force)
		return queueLoadedMsg{filter: filter, items: items, fetched: fetched, err: err}
	}
}

func (m QueueModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case queueLoadedMsg:
		m.requests.done(reqQueue(msg.filter))
		// A late reply for a filter the user has already switched away from
		// would otherwise overwrite the list they are looking at.
		if msg.filter != m.filter {
			return m, nil
		}
		m.items, m.fetched = msg.items, msg.fetched
		m.err = ""
		if msg.err != nil {
			m.err = msg.err.Error()
			if len(msg.items) > 0 {
				m.err += " — showing the cached list"
			}
		}
		m.countDrafts()
		if m.cursor >= len(m.items) {
			m.cursor = maxInt(0, len(m.items)-1)
		}
		return m, nil

	case launchedMsg:
		if msg.err != nil {
			m.notice = "could not open the browser: " + msg.err.Error()
		} else if msg.copied {
			m.notice = "copied link"
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	case tea.MouseMsg:
		return m.handleMouse(msg)
	}
	return m, nil
}

// listBody is how many pull requests fit: the screen less the header and the
// footer, and less the line a notice or error takes over a list.
func (m QueueModel) listBody() int {
	body := m.height - 2
	if m.notice != "" || m.err != "" {
		body--
	}
	return body
}

// listTop is the first pull request shown: the list scrolls only as far as
// keeps the selection on screen, below the one-line header.
func (m QueueModel) listTop() int {
	return maxInt(0, m.cursor-m.listBody()+1)
}

func (m QueueModel) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch msg.Button {
	case tea.MouseButtonWheelDown:
		return m.handleKey(keyRune('j'))
	case tea.MouseButtonWheelUp:
		return m.handleKey(keyRune('k'))
	case tea.MouseButtonLeft:
		idx := m.listTop() + msg.Y - 1
		if msg.Action != tea.MouseActionPress || msg.Y < 1 || msg.Y > m.listBody() || idx >= len(m.items) {
			return m, nil
		}
		m.cursor = idx
		if m.lastPress.double(0, idx, m.now()) {
			return m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
		}
	}
	return m, nil
}

func (m QueueModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.notice = ""
	switch msg.String() {
	case "q", "ctrl+c", "esc":
		return m, tea.Quit
	case "j", "down":
		if m.cursor < len(m.items)-1 {
			m.cursor++
		}
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
	case "g", "home":
		m.cursor = 0
	case "G", "end":
		m.cursor = maxInt(0, len(m.items)-1)
	case "r":
		return m, m.load(true)
	case "t", "tab":
		if m.filter == ghsrc.FilterReviewRequested {
			m.filter = ghsrc.FilterAuthored
		} else {
			m.filter = ghsrc.FilterReviewRequested
		}
		m.cursor = 0
		return m, m.load(false)
	case "L":
		// The loading page with nothing loading, to look at the animations.
		sel := Selection{Repo: "krv", Number: 42}
		if m.cursor < len(m.items) {
			sel = Selection{Repo: m.items[m.cursor].Repo, Number: m.items[m.cursor].Number}
		}
		return m, func() tea.Msg { return openMsg{sel: sel, preview: true} }
	case "O":
		if m.cursor < len(m.items) && m.items[m.cursor].URL != "" {
			return m, browse(m.items[m.cursor].URL, nil)
		}
	case "enter", " ":
		if m.cursor < len(m.items) {
			it := m.items[m.cursor]
			sel := Selection{Repo: it.Repo, Number: it.Number}
			// A marked row is one you have reviewed before, and what is new
			// since then is what you came back for.
			since := m.columns().marker && it.NewSinceReview()
			return m, func() tea.Msg { return openMsg{sel: sel, sinceReview: since} }
		}
	}
	return m, nil
}

// item is the listed row a selection names, or just its name when the list
// has since changed underneath it.
func (m QueueModel) item(sel Selection) ghsrc.QueueItem {
	for _, it := range m.items {
		if it.Repo == sel.Repo && it.Number == sel.Number {
			return it
		}
	}
	return ghsrc.QueueItem{Repo: sel.Repo, Number: sel.Number}
}

// countDrafts reports how many unsent notes each listed pull request has, so
// a half-finished review is visible from the queue rather than forgotten.
func (m *QueueModel) countDrafts() {
	m.drafts = map[string]int{}
	for _, it := range m.items {
		review, err := notes.Load(notes.PRScope(it.Repo, it.Number))
		if err != nil || len(review.Notes) == 0 {
			continue
		}
		m.drafts[fmt.Sprintf("%s#%d", it.Repo, it.Number)] = len(review.Notes)
	}
}

func (m QueueModel) View() string {
	t := m.theme
	var b strings.Builder

	header := lipgloss.NewStyle().Background(lipgloss.Color(t.FileBg)).
		Foreground(lipgloss.Color(t.FileFg)).Bold(true)
	// The queue paints its own surface rather than borrowing the terminal's:
	// a theme that only covered the diff would leave this screen unreadable
	// on any background it was not designed for.
	surface := lipgloss.NewStyle().Background(lipgloss.Color(t.Bg)).
		Foreground(lipgloss.Color(t.Fg))
	title := fmt.Sprintf(" review queue — %s", m.filter.Label())
	if m.loading() {
		title += "  ·  loading…"
	} else if !m.fetched.IsZero() {
		title += fmt.Sprintf("  ·  updated %s ago", shortAge(time.Since(m.fetched)))
	}
	b.WriteString(header.Render(pad(title, m.width)) + "\n")

	body := m.height - 2
	switch {
	case m.err != "" && len(m.items) == 0:
		b.WriteString(surface.Render(pad("", m.width)) + "\n")
		b.WriteString(m.message(t.DelSign, m.err) + "\n")
	case m.loading() && len(m.items) == 0:
		b.WriteString(surface.Render(pad("", m.width)) + "\n")
		b.WriteString(m.message(t.Dim, "loading…") + "\n")
	case len(m.items) == 0:
		b.WriteString(surface.Render(pad("", m.width)) + "\n")
		b.WriteString(m.message(t.Dim, "nothing waiting on you — press t for your own pull requests") + "\n")
	default:
		// An error over a list that is still worth showing takes the last
		// line of it, rather than replacing it.
		notice := m.notice
		if notice == "" {
			notice = m.err
		}
		body = m.listBody()
		top := m.listTop()
		for i := 0; i < body; i++ {
			idx := top + i
			if idx >= len(m.items) {
				b.WriteString(surface.Render(pad("", m.width)) + "\n")
				continue
			}
			b.WriteString(m.row(m.items[idx], idx == m.cursor) + "\n")
		}
		if notice != "" {
			b.WriteString(m.message(t.DelSign, notice) + "\n")
		}
	}

	hint := "enter open  O browser  t switch  r refresh  L loading  q quit"
	if m.err != "" && len(m.items) > 0 {
		hint = "r retry  " + hint
	}
	b.WriteString(header.Render(pad(" "+fmt.Sprintf("%d pull request%s", len(m.items), plural(len(m.items))), m.width-len(hint)-1) + hint + " "))
	return b.String()
}

// message draws one line of surface with a coloured sentence on it.
func (m QueueModel) message(fg, text string) string {
	t := m.theme
	surface := lipgloss.NewStyle().Background(lipgloss.Color(t.Bg)).Foreground(lipgloss.Color(t.Fg))
	line := surface.Render("  ") +
		lipgloss.NewStyle().Foreground(lipgloss.Color(fg)).Background(lipgloss.Color(t.Bg)).
			Render(runewidth.Truncate(text, maxInt(1, m.width-2), "…"))
	return padStyled(surface, line, m.width)
}

func (m QueueModel) row(it ghsrc.QueueItem, selected bool) string {
	t := m.theme

	// Every piece of the row states its own background: a nested style that
	// set only a foreground would end the selected row's band where it
	// started, which is what makes a highlight look broken.
	bg, textFg, edge, edgeFg := t.Bg, t.Fg, " ", t.Bg
	if selected {
		bg, textFg, edge, edgeFg = t.CursorBg, t.LineNumFocusFg, render.FocusBar, t.CursorBar
	}
	st := func(fg string) lipgloss.Style {
		s := lipgloss.NewStyle().Background(lipgloss.Color(bg))
		if fg != "" {
			s = s.Foreground(lipgloss.Color(fg))
		}
		if selected {
			s = s.Bold(true)
		}
		return s
	}

	cols := m.columns()
	check, checkFg := m.checkMark(it.Checks)
	name := fmt.Sprintf("%s#%d", it.Repo, it.Number)
	meta := m.meta(it)

	line := st(edgeFg).Render(edge) + st(checkFg).Render(check) + st("").Render(" ")
	if cols.marker {
		mark := " "
		if it.NewSinceReview() {
			mark = newCommitsMark
		}
		line += st(t.ChangedFg).Render(mark) + st("").Render(" ")
	}
	line += st(t.Accent).Render(name) + st("").Render("  ")

	// The title gets whatever is left, so the identifying columns survive a
	// narrow terminal.
	titleWidth := maxInt(10, m.width-cols.left(name)-runewidth.StringWidth(meta)-cols.right())
	title := it.Title
	if it.IsDraft {
		title = "[draft] " + title
	}
	line += st(textFg).Render(runewidth.Truncate(title, titleWidth, "…"))
	line += st(t.Dim).Render(meta)

	// The review state keeps to the right edge, where its columns line up
	// whatever the title and author took.
	var right string
	if cols.decision {
		glyph, word, fg := m.decisionMark(it.Decision)
		cell := glyph
		if cols.words {
			cell = runewidth.FillRight(glyph+" "+word, decisionWidth)
		}
		right += st("").Render("  ") + st(fg).Render(cell)
	}
	if cols.size {
		add, del := "", ""
		if it.Additions != 0 || it.Deletions != 0 {
			add, del = fmt.Sprintf("+%d", it.Additions), fmt.Sprintf("−%d", it.Deletions)
		}
		gap := cols.sizeWidth - runewidth.StringWidth(add) - runewidth.StringWidth(del)
		if add != "" {
			gap--
		}
		right += st("").Render("  "+strings.Repeat(" ", maxInt(0, gap))) + st(t.AddSign).Render(add)
		if add != "" {
			right += st("").Render(" ") + st(t.DelSign).Render(del)
		}
	}
	if right != "" {
		right += st("").Render(" ")
	}

	if w := lipgloss.Width(line) + lipgloss.Width(right); w < m.width {
		line += st("").Render(strings.Repeat(" ", m.width-w))
	}
	return line + right
}

// newCommitsMark flags a pull request that has moved on since your latest
// review. It is a shape, not a colour, so it reads on any terminal.
const newCommitsMark = "●"

// decisionWidth is the decision column with its word: a glyph, a space and
// the longest word.
const decisionWidth = 10

// queueColumns is how much of the review state the rows have room for. It is
// decided once for the whole list, so every row keeps the same columns.
type queueColumns struct {
	// marker is the new-commits column, which only the list to review has:
	// your own pull requests are not yours to review.
	marker                bool
	decision, words, size bool
	sizeWidth             int
}

// left is the width of a row before its title.
func (c queueColumns) left(name string) int {
	w := 1 + 1 + 1 + runewidth.StringWidth(name) + 2
	if c.marker {
		w += 2
	}
	return w
}

// right is the width of the review state at the end of a row.
func (c queueColumns) right() int {
	w := 0
	if c.decision {
		w += 2 + 1
		if c.words {
			w += decisionWidth - 1
		}
	}
	if c.size {
		w += 2 + c.sizeWidth
	}
	if w > 0 {
		w++
	}
	return w
}

// columns gives up the review state a piece at a time while the widest row
// would squeeze its title below the ten columns it always had: first the
// size, then the decision's word. The glyph stays.
func (m QueueModel) columns() queueColumns {
	c := queueColumns{marker: m.filter == ghsrc.FilterReviewRequested, decision: true, words: true, size: true}
	widest := 0
	for _, it := range m.items {
		if it.Additions != 0 || it.Deletions != 0 {
			c.sizeWidth = maxInt(c.sizeWidth, runewidth.StringWidth(fmt.Sprintf("+%d −%d", it.Additions, it.Deletions)))
		}
		w := c.left(fmt.Sprintf("%s#%d", it.Repo, it.Number)) + runewidth.StringWidth(m.meta(it))
		widest = maxInt(widest, w)
	}
	c.size = c.sizeWidth > 0
	fits := func() bool { return widest+c.right()+10 <= m.width }
	if !fits() {
		c.size = false
	}
	if !fits() {
		c.words = false
	}
	return c
}

// meta is what a row says after its title: drafts, author and age.
func (m QueueModel) meta(it ghsrc.QueueItem) string {
	meta := fmt.Sprintf("  %s  %s", it.Author, it.Age())
	if n := m.drafts[fmt.Sprintf("%s#%d", it.Repo, it.Number)]; n > 0 {
		meta = fmt.Sprintf("  %d draft%s%s", n, plural(n), meta)
	}
	return meta
}

// decisionMark renders GitHub's review decision as a glyph and a word. No
// decision, as in a repository that requires no reviews, is left blank
// rather than shown as one it is not.
func (m QueueModel) decisionMark(decision string) (glyph, word, fg string) {
	t := m.theme
	switch decision {
	case "APPROVED":
		return "✓", "approved", t.ReviewedFg
	case "CHANGES_REQUESTED":
		return "✗", "changes", t.DelSign
	case "REVIEW_REQUIRED":
		return "○", "required", t.Dim
	default:
		return " ", "", t.Dim
	}
}

// checkMark renders the CI rollup, in the theme's own colours. An empty state
// means the pull request has no checks at all, which is different from checks
// that have not finished.
func (m QueueModel) checkMark(state string) (string, string) {
	t := m.theme
	switch state {
	case "SUCCESS":
		return "✓", t.ReviewedFg
	case "FAILURE", "ERROR":
		return "✗", t.DelSign
	case "PENDING":
		return "•", t.ChangedFg
	default:
		return " ", t.Dim
	}
}

func shortAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "moments"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
