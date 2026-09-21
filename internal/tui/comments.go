package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/tobiasbernting/krv/v2/internal/render"
)

// commentDetail is the comment or draft the reader is showing. The row it
// came from is kept so a suggestion in it can still find the lines it
// proposes to replace.
type commentDetail struct {
	annotation render.Annotation
	row        render.Row
}

func (m Model) openOrToggleComment() (tea.Model, tea.Cmd) {
	if m.cursor < 0 || m.cursor >= len(m.doc.Rows) {
		return m, nil
	}
	row := m.doc.Rows[m.cursor]
	if row.Kind != render.RowNote || row.Ann == nil {
		return m, nil
	}
	if row.Ann.Kind == render.AnnThread {
		m.expandedThreads[row.Ann.ThreadID] = row.Ann.Collapsed
		m.rebuild()
		return m, nil
	}
	m.detail = commentDetail{annotation: *row.Ann, row: row}
	m.mode = modeComment
	m.reader = readerState{focus: -1}
	return m, nil
}

// commentPage is the comment or draft in full, as Markdown, with its state
// in the title above it.
func (m Model) commentPage() page {
	a := m.detail.annotation
	title := a.Author
	if title == "" {
		title = "you"
	}
	var states []string
	if a.NeedsReanchor {
		states = append(states, "needs re-anchor")
	}
	if a.Outdated {
		states = append(states, "outdated")
	}
	if a.Kind == render.AnnComment {
		if !a.ResolutionKnown {
			states = append(states, "resolution unavailable")
		} else if a.Resolved {
			states = append(states, "resolved")
		}
	}
	if a.New {
		states = append(states, "new")
	}
	if a.Updated {
		states = append(states, "updated")
	}
	if len(states) > 0 {
		title += " [" + strings.Join(states, ", ") + "]"
	}

	st := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.theme.CommentFg)).
		Background(lipgloss.Color(m.theme.Bg)).Bold(true)

	p := page{title: st.Render(clipText(title, m.readerWidth()))}
	opts := m.markdownOptions()
	opts.Suggestion = m.suggestionMiniDiff(m.detail.row)
	p.markdown(render.Markdown(a.Body, m.readerWidth(), opts))
	return p
}

// markdownOptions is how krv draws Markdown in the live terminal: the
// review's theme, code through its highlighter, and every link an OSC 8
// hyperlink so the terminal can open it on a click of its own.
func (m Model) markdownOptions() render.MarkdownOptions {
	return render.MarkdownOptions{
		Theme:       m.theme,
		Highlighter: m.hl,
		NoColor:     !m.cfg.Color,
		Hyperlinks:  true,
	}
}

func (m *Model) jumpActivity(dir int) {
	type anchor struct {
		row      int
		threadID string
	}
	var anchors []anchor
	seen := map[string]bool{}
	for i, row := range m.doc.Rows {
		if row.Kind != render.RowNote || row.Ann == nil || row.Ann.Kind != render.AnnComment {
			continue
		}
		if (!row.Ann.New && !row.Ann.Updated) || seen[row.Ann.ThreadID] {
			continue
		}
		seen[row.Ann.ThreadID] = true
		anchors = append(anchors, anchor{row: i, threadID: row.Ann.ThreadID})
	}
	if len(anchors) == 0 {
		m.status = "no new or updated comments"
		return
	}
	chosen := -1
	if dir > 0 {
		for i := range anchors {
			if anchors[i].row > m.cursor {
				chosen = i
				break
			}
		}
	} else {
		for i := len(anchors) - 1; i >= 0; i-- {
			if anchors[i].row < m.cursor {
				chosen = i
				break
			}
		}
	}
	if chosen < 0 {
		m.status = map[bool]string{true: "last new thread", false: "first new thread"}[dir > 0]
		return
	}
	m.cursor = anchors[chosen].row
	m.clampScroll()
	m.visitCurrentThread()
}

func (m *Model) visitCurrentThread() {
	if m.cursor < 0 || m.cursor >= len(m.doc.Rows) {
		return
	}
	row := m.doc.Rows[m.cursor]
	if row.Kind != render.RowNote || row.Ann == nil || row.Ann.Kind != render.AnnComment {
		return
	}
	if !row.Ann.New && !row.Ann.Updated {
		return
	}
	threadID := row.Ann.ThreadID
	for _, thread := range m.threads {
		if thread.ID != threadID {
			continue
		}
		for _, comment := range thread.Comments {
			delete(m.newComments, comment.ID)
			delete(m.updatedComments, comment.ID)
		}
		break
	}
	m.rebuild()
}
