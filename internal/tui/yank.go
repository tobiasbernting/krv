package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tobiasbernting/code-review-cli/internal/diffparse"
	"github.com/tobiasbernting/code-review-cli/internal/render"
)

// Clipboard is where a yank goes. The program's is the terminal's and the
// platform's (see package clipboard); tests pass a fake.
type Clipboard interface {
	Copy(text string) error
}

type yankedMsg struct{ err error }

// yank copies what the cursor is on as plain code: the selection, else the
// line, hunk or file path under the cursor. With reference set it copies
// where that is instead, as path:L12-L18.
func (m Model) yank(reference bool) (tea.Model, tea.Cmd) {
	text, ref, lines, err := m.yankTarget()
	if err != "" {
		m.err = err
		return m, nil
	}
	if m.clip == nil {
		m.err = "no clipboard"
		return m, nil
	}
	if reference {
		if ref == "" {
			m.err = "a deleted line has no line number to refer to"
			return m, nil
		}
		text = ref
		m.status = "copied " + ref
	} else {
		m.status = fmt.Sprintf("copied %d line%s", lines, plural(lines))
	}
	m.clearSelection()
	clip := m.clip
	return m, func() tea.Msg { return yankedMsg{err: clip.Copy(text)} }
}

func (m Model) applyYanked(msg yankedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.status = ""
		m.err = "copy failed: " + msg.err.Error()
	}
	return m, nil
}

// yankTarget is the code a yank copies, the reference that names it and how
// many lines it is, or a reason there is nothing to copy.
func (m Model) yankTarget() (text, ref string, lines int, err string) {
	if m.cursor >= len(m.doc.Rows) {
		return "", "", 0, "nothing to copy"
	}
	row := m.doc.Rows[m.cursor]
	if row.FileIdx >= len(m.files) {
		return "", "", 0, "nothing to copy here"
	}
	file := m.files[row.FileIdx]
	path := file.Path()

	if m.rangeAnchor > 0 {
		cpath, line, hunk, ok := m.cursorLine()
		if !ok || cpath != m.rangeAnchorPath || hunk != m.rangeAnchorHunk {
			return "", "", 0, "move the cursor into the selection's hunk to copy it"
		}
		start, end := min(m.rangeAnchor, line), max(m.rangeAnchor, line)
		code := newSide(file.Hunks()[hunk].Lines, start, end)
		return strings.Join(code, "\n"), lineRef(path, start, end), len(code), ""
	}

	switch row.Kind {
	case render.RowFile:
		return path, path, 1, ""
	case render.RowHunk:
		h := file.Hunks()[row.HunkIdx]
		end := h.NewStart + max(h.NewLines, 1) - 1
		code := newSide(h.Lines, h.NewStart, end)
		return strings.Join(code, "\n"), lineRef(path, h.NewStart, end), len(code), ""
	case render.RowCode, render.RowPair:
		ln := row.Line
		if row.Kind == render.RowPair && row.Right.NewNum > 0 {
			ln = row.Right
		}
		ref := ""
		if ln.NewNum > 0 {
			ref = lineRef(path, ln.NewNum, ln.NewNum)
		}
		return ln.Text, ref, 1, ""
	}
	return "", "", 0, "nothing to copy here"
}

// newSide is the text of lines start..end as they read after the change.
func newSide(lines []diffparse.Line, start, end int) []string {
	var out []string
	for _, ln := range lines {
		if ln.Kind != diffparse.KindDel && ln.NewNum >= start && ln.NewNum <= end {
			out = append(out, ln.Text)
		}
	}
	return out
}

func lineRef(path string, start, end int) string {
	if start == end {
		return fmt.Sprintf("%s:L%d", path, start)
	}
	return fmt.Sprintf("%s:L%d-L%d", path, start, end)
}
