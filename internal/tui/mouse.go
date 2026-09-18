package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tobiasbernting/code-review-cli/internal/render"
)

// doubleClick is the longest gap between two presses on the same row that
// still counts as one double click. Terminals report presses, not clicks, so
// crv times them itself.
const doubleClick = 400 * time.Millisecond

// lastPress is the previous left-button press, kept to recognise a double
// click. scope separates screens, so a press in one list and a press at the
// same height in another are never paired.
type lastPress struct {
	scope, row int
	at         time.Time
}

// double records a press and reports whether it completes a double click. A
// double click is consumed, so a third press starts over.
func (p *lastPress) double(scope, row int, now time.Time) bool {
	if !p.at.IsZero() && p.scope == scope && p.row == row && now.Sub(p.at) <= doubleClick {
		*p = lastPress{}
		return true
	}
	*p = lastPress{scope: scope, row: row, at: now}
	return false
}

// dragState follows the left button from a press in the diff to its release.
type dragState struct {
	held   bool
	origin int // the row the press landed on
}

// wheelStep is how many rows one wheel notch scrolls the diff.
const wheelStep = 3

// hStep is how many columns h, l and a sideways wheel notch scroll.
const hStep = 8

// handleMouse gives the mouse the same reach as the keyboard on screens that
// show a list or a document. Screens being typed into ignore it, and so does
// everything while a GitHub mutation is in flight.
func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.follow.busy {
		return m, nil
	}
	switch m.mode {
	case modeDiff:
		return m.handleDiffMouse(msg)
	case modeFiles:
		return m.handleListMouse(msg)
	case modeThreads:
		return m.handleListMouse(msg)
	case modeThread, modeComment, modeHelp:
		// Documents scroll with j and k, so the wheel is those keys.
		switch msg.Button {
		case tea.MouseButtonWheelDown:
			return m.handleKey(keyRune('j'))
		case tea.MouseButtonWheelUp:
			return m.handleKey(keyRune('k'))
		}
	}
	return m, nil
}

// handleListMouse is the file list and the thread list: the wheel steps the
// selection, a click selects, a double click opens as enter does.
func (m Model) handleListMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch msg.Button {
	case tea.MouseButtonWheelDown:
		return m.handleKey(keyRune('j'))
	case tea.MouseButtonWheelUp:
		return m.handleKey(keyRune('k'))
	case tea.MouseButtonLeft:
		if msg.Action != tea.MouseActionPress {
			return m, nil
		}
		idx, ok := m.listItemAt(msg.Y)
		if !ok {
			return m, nil
		}
		if m.mode == modeFiles {
			m.fileCursor = idx
		} else {
			m.follow.cursor = idx
		}
		if m.lastPress.double(int(m.mode), idx, m.now()) {
			return m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
		}
	}
	return m, nil
}

// listItemAt is the file or thread drawn on screen line y.
func (m Model) listItemAt(y int) (int, bool) {
	if m.mode == modeFiles {
		idx := m.filesTop() + y
		if y < 0 || y >= m.viewportHeight() || idx >= len(m.doc.Files) {
			return 0, false
		}
		return idx, true
	}
	// Two header lines, then any warnings, then two lines per thread.
	line := y - 2 + m.threadListTop() - m.threadListPrefix()
	if y < 2 || line < 0 || line/2 >= len(m.follow.threads) {
		return 0, false
	}
	return line / 2, true
}

func keyRune(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

func (m Model) handleDiffMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	button := msg.Button
	// Shift turns the vertical wheel sideways, for mice that have no
	// horizontal wheel of their own.
	if msg.Shift {
		switch button {
		case tea.MouseButtonWheelDown:
			button = tea.MouseButtonWheelRight
		case tea.MouseButtonWheelUp:
			button = tea.MouseButtonWheelLeft
		}
	}
	switch button {
	case tea.MouseButtonWheelDown:
		m.scrollBy(wheelStep)
	case tea.MouseButtonWheelUp:
		m.scrollBy(-wheelStep)
	case tea.MouseButtonWheelRight:
		m.hoffset += hStep
	case tea.MouseButtonWheelLeft:
		m.hoffset = max(0, m.hoffset-hStep)
	case tea.MouseButtonLeft:
		switch msg.Action {
		case tea.MouseActionPress:
			return m.clickDiff(msg.Y)
		case tea.MouseActionMotion:
			m.dragDiff(msg.Y)
		}
	case tea.MouseButtonNone:
		if msg.Action == tea.MouseActionRelease {
			m.drag.held = false
		}
	}
	return m, nil
}

func (m Model) clickDiff(y int) (tea.Model, tea.Cmd) {
	row, ok := m.rowAt(y)
	if !ok || m.reanchor.id != "" {
		return m, nil
	}
	// A plain click starts over, as in any editor: the old selection goes.
	m.clearSelection()
	m.cursor = m.nextSelectable(row, 1)
	m.clampScroll()
	m.visitCurrentThread()
	m.drag = dragState{held: true, origin: m.cursor}
	if m.lastPress.double(int(m.mode), m.cursor, m.now()) {
		return m.openOrToggleComment()
	}
	return m, nil
}

// dragDiff follows a held button. The first move off the pressed row anchors
// a selection there, the same selection v makes. On or past the viewport's
// top or bottom line the view scrolls one row per move, so a selection can
// grow past one screen.
func (m *Model) dragDiff(y int) {
	if !m.drag.held || m.changesView || m.reanchor.id != "" || len(m.doc.Rows) == 0 {
		return
	}
	vh := m.viewportHeight()
	switch {
	case y <= 0 && m.top > 0:
		m.top--
	case y >= vh-1 && m.top < len(m.doc.Rows)-vh:
		m.top++
	}
	row, ok := m.rowAt(max(0, min(y, vh-1)))
	if !ok {
		row = len(m.doc.Rows) - 1
	}
	origin := m.drag.origin
	if row == origin && m.rangeAnchor == 0 {
		return
	}
	dir := 1
	if row < origin {
		dir = -1
	}
	if m.rangeAnchor == 0 {
		at := *m
		at.cursor = origin
		if path, line, hunk, ok := at.cursorLine(); ok {
			m.rangeAnchor, m.rangeAnchorPath, m.rangeAnchorHunk = line, path, hunk
		}
	}
	if m.rangeAnchor > 0 {
		row = m.clampToHunk(origin, row, dir)
	}
	m.cursor = m.nextSelectable(row, -dir)
	m.clampScroll()
	m.visitCurrentThread()
}

// clampToHunk stops a drag from origin at the last code row of origin's hunk
// in direction dir: GitHub rejects a comment whose ends are in two hunks.
func (m Model) clampToHunk(origin, row, dir int) int {
	o := m.doc.Rows[origin]
	last := origin
	for i := origin; i >= 0 && i < len(m.doc.Rows); i += dir {
		r := m.doc.Rows[i]
		if r.FileIdx != o.FileIdx || (r.IsCode() && r.HunkIdx != o.HunkIdx) || r.Kind == render.RowHunk || r.Kind == render.RowFile {
			break
		}
		if r.IsCode() {
			last = i
		}
		if i == row {
			return row
		}
	}
	return last
}

// rowAt is the document row drawn on screen line y of the diff view. It walks
// the viewport the way diffView draws it, because the row under the cursor
// can take several lines.
func (m Model) rowAt(y int) (int, bool) {
	if y < 0 || y >= m.viewportHeight() {
		return 0, false
	}
	line := 0
	for idx := m.top; idx < len(m.doc.Rows); idx++ {
		height := 1
		if idx == m.cursor {
			height = len(m.rend.RenderLines(m.doc.Rows[idx], m.width, m.hoffset, true, focusLines))
		}
		if y < line+height {
			return idx, true
		}
		line += height
	}
	return 0, false
}

// scrollBy moves the viewport without moving the cursor, unless the cursor
// would leave the screen, in which case it is carried along at the edge.
func (m *Model) scrollBy(delta int) {
	if len(m.doc.Rows) == 0 {
		return
	}
	vh := m.viewportHeight()
	m.top = max(0, min(m.top+delta, len(m.doc.Rows)-vh))
	cursor := m.cursor
	switch {
	case m.cursor < m.top:
		m.cursor = m.nextSelectable(m.top, 1)
	case m.cursor >= m.top+vh:
		m.cursor = m.nextSelectable(m.top+vh-1, -1)
	}
	m.clampScroll()
	if m.cursor != cursor {
		m.visitCurrentThread()
	}
}
