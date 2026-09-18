package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tobiasbernting/code-review-cli/internal/notes"
	"github.com/tobiasbernting/code-review-cli/internal/render"
)

type reanchorState struct {
	id string
}

func (m Model) startReanchor() (tea.Model, tea.Cmd) {
	if m.changesView {
		m.err = "press D for the current PR diff before editing draft anchors"
		return m, nil
	}
	if m.cursor < 0 || m.cursor >= len(m.doc.Rows) {
		return m, nil
	}
	row := m.doc.Rows[m.cursor]
	if row.Kind != render.RowNote || row.Ann == nil || row.Ann.Kind != render.AnnNote {
		m.err = "put the cursor on a local draft to re-anchor it"
		return m, nil
	}
	if !row.Ann.NeedsReanchor {
		m.err = "this draft is still anchored"
		return m, nil
	}
	m.reanchor.id = row.Ann.ID
	m.rangeAnchor, m.rangeAnchorPath, m.rangeAnchorHunk = 0, "", -1
	m.status = "re-anchoring draft; move to a line and press enter"
	return m, nil
}

func (m Model) handleReanchorKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		m.reanchor = reanchorState{}
		m.rangeAnchor, m.rangeAnchorPath, m.rangeAnchorHunk = 0, "", -1
		m.status = "re-anchor cancelled"
	case "j", "down":
		m.moveCursor(1)
	case "k", "up":
		m.moveCursor(-1)
	case "ctrl+d", "pgdown":
		m.moveCursor(m.viewportHeight() / 2)
	case "ctrl+u", "pgup":
		m.moveCursor(-m.viewportHeight() / 2)
	case "g", "home":
		m.cursor = m.nextSelectable(0, 1)
		m.clampScroll()
	case "G", "end":
		m.cursor = m.nextSelectable(len(m.doc.Rows)-1, -1)
		m.clampScroll()
	case "n":
		m.jump(m.doc.HunkRows, 1, "hunk")
	case "p":
		m.jump(m.doc.HunkRows, -1, "hunk")
	case "tab", "J", "]":
		m.jump(m.doc.FileRows, 1, "file")
	case "shift+tab", "K", "[":
		m.jump(m.doc.FileRows, -1, "file")
	case "f":
		if len(m.doc.Files) > 0 {
			m.mode = modeFiles
			if m.cursor < len(m.doc.Rows) && m.doc.Rows[m.cursor].FileIdx < len(m.doc.Files) {
				m.fileCursor = m.doc.Rows[m.cursor].FileIdx
			}
		}
	case "v":
		return m.toggleReanchorRange()
	case "enter":
		return m.finishReanchor()
	default:
		m.status = "re-anchoring draft; enter confirm, v range, esc cancel"
	}
	return m, nil
}

func (m Model) toggleReanchorRange() (tea.Model, tea.Cmd) {
	if m.rangeAnchor > 0 {
		m.rangeAnchor, m.rangeAnchorPath, m.rangeAnchorHunk = 0, "", -1
		m.status = "re-anchor range cleared"
		return m, nil
	}
	path, line, hunk, ok := m.cursorCodeLine()
	if !ok {
		m.err = "put the cursor on an added or unchanged line"
		return m, nil
	}
	m.rangeAnchor, m.rangeAnchorPath, m.rangeAnchorHunk = line, path, hunk
	m.status = fmt.Sprintf("re-anchor range from L%d; move and press enter", line)
	return m, nil
}

func (m Model) finishReanchor() (tea.Model, tea.Cmd) {
	path, line, hunk, ok := m.cursorCodeLine()
	if !ok {
		m.err = "put the cursor on an added or unchanged line"
		return m, nil
	}
	start := line
	if m.rangeAnchor > 0 {
		if m.rangeAnchorPath != path {
			m.err = "the re-anchor range started in another file"
			return m, nil
		}
		if m.rangeAnchorHunk != hunk {
			m.err = "a selection cannot span two hunks"
			return m, nil
		}
		start = m.rangeAnchor
		if start > line {
			start, line = line, start
		}
	}
	if !m.review.Reanchor(m.reanchor.id, path, start, line, notes.SideRight, m.blobs[path]) {
		m.err = "draft no longer exists"
		m.reanchor = reanchorState{}
		return m, nil
	}
	m.reanchor = reanchorState{}
	m.rangeAnchor, m.rangeAnchorPath, m.rangeAnchorHunk = 0, "", -1
	m.save()
	m.rebuild()
	m.status = "draft re-anchored"
	return m, nil
}

func (m Model) cursorCodeLine() (path string, line, hunk int, ok bool) {
	if m.cursor < 0 || m.cursor >= len(m.doc.Rows) {
		return "", 0, -1, false
	}
	row := m.doc.Rows[m.cursor]
	if !row.IsCode() || row.NewNum() == 0 || row.FileIdx >= len(m.files) {
		return "", 0, row.HunkIdx, false
	}
	return m.files[row.FileIdx].Path(), row.NewNum(), row.HunkIdx, true
}
