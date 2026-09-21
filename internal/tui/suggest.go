package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/tobiasbernting/krv/v2/internal/diffparse"
	"github.com/tobiasbernting/krv/v2/internal/ghsrc"
	"github.com/tobiasbernting/krv/v2/internal/notes"
	"github.com/tobiasbernting/krv/v2/internal/render"
)

const suggestionFence = "```suggestion\n"

// startSuggestion opens the composer on a suggestion block holding the lines
// under the cursor, or the selection, as they read after the change: the code
// y copies. Only new-side lines can be replaced, so a deleted line is refused
// and a selection that spans one leaves it out.
func (m Model) startSuggestion() (tea.Model, tea.Cmd) {
	if m.cursor < len(m.doc.Rows) {
		if row := m.doc.Rows[m.cursor]; row.IsCode() && row.NewNum() == 0 {
			m.err = "suggestions replace new lines; this line was deleted"
			return m, nil
		}
	}
	path, start, line, hunk, err := m.draftAnchor()
	if err != "" {
		m.err = err
		return m, nil
	}
	file := m.files[m.doc.Rows[m.cursor].FileIdx]
	code := strings.Join(newSide(file.Hunks()[hunk].Lines, start, line), "\n")

	m.pending = pendingNote{path: path, startLine: start, line: line, suggestion: code, suggesting: true}
	m.in.start(draftPrompt("suggest", start, line), suggestionFence+code+"\n```")
	m.in.cursor = len([]rune(suggestionFence + code))
	m.mode = modeInput
	return m, nil
}

// unchangedSuggestion reports whether body is a suggestion that would leave
// its lines as they are, and has not been warned about yet. It warns once.
func (m *Model) unchangedSuggestion(body string) bool {
	if !m.pending.suggesting || m.pending.warned {
		return false
	}
	code, ok := suggestedCode(body)
	if !ok || code != m.pending.suggestion {
		return false
	}
	m.pending.warned = true
	m.err = "suggestion doesn't change anything — enter again to save anyway"
	return true
}

// suggestionMiniDiff draws a suggestion in a comment as the change it
// proposes: the lines it is anchored to as −, the proposed lines as +, like
// GitHub. It answers no — and the block is drawn as plain code labelled
// "suggestion" — when those lines cannot be trusted: an outdated Thread, a
// Draft that needs re-anchoring, or an anchor the current diff does not show.
func (m Model) suggestionMiniDiff(row render.Row) func([]string, int) ([]string, bool) {
	return suggestionMiniDiff(m.theme, m.files, m.threads, m.review, row)
}

func suggestionMiniDiff(t render.Theme, files []*diffparse.FileDiff, threads []ghsrc.Thread, review *notes.Review, row render.Row) func([]string, int) ([]string, bool) {
	return func(proposed []string, width int) ([]string, bool) {
		anchored, ok := anchoredLines(files, threads, review, row)
		if !ok {
			return nil, false
		}
		out := make([]string, 0, len(anchored)+len(proposed))
		for _, line := range anchored {
			out = append(out, suggestionLine(t, "−", line, t.DelSign, t.DelBg, width))
		}
		for _, line := range proposed {
			out = append(out, suggestionLine(t, "+", line, t.AddSign, t.AddBg, width))
		}
		return out, true
	}
}

// suggestionLine is one line of the mini-diff. The sign is drawn as well as
// the tint, so the direction of the change survives NO_COLOR.
func suggestionLine(t render.Theme, sign, code, fg, bg string, width int) string {
	text := plainText(strings.ReplaceAll(code, "\t", "    "))
	line := runewidth.Truncate(sign+" "+text, width, "…")
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(fg)).Background(lipgloss.Color(bg)).
		Render(runewidth.FillRight(line, width))
}

// anchoredLines are the new-side lines a comment is anchored to: what a
// suggestion in it replaces. ok is false when the anchor cannot be trusted,
// or when the current diff does not show every line of it.
func anchoredLines(files []*diffparse.FileDiff, threads []ghsrc.Thread, review *notes.Review, row render.Row) ([]string, bool) {
	a := row.Ann
	if a == nil || a.NeedsReanchor || a.Outdated || a.Line <= 0 || row.FileIdx >= len(files) {
		return nil, false
	}
	// A comment on the old side is anchored to lines a suggestion cannot
	// replace, and its line numbers are the old side's.
	if a.Kind == render.AnnNote {
		for _, n := range review.Notes {
			if n.ID == a.ID && n.Side == "LEFT" {
				return nil, false
			}
		}
	} else {
		for _, thread := range threads {
			if thread.ID == a.ThreadID && thread.Side == "LEFT" {
				return nil, false
			}
		}
	}
	start := a.StartLine
	if start <= 0 {
		start = a.Line
	}
	var out []string
	for _, hunk := range files[row.FileIdx].Hunks() {
		out = append(out, newSide(hunk.Lines, start, a.Line)...)
	}
	if len(out) != a.Line-start+1 {
		return nil, false
	}
	return out, true
}

// suggestedCode is the content of the first suggestion block in body.
func suggestedCode(body string) (string, bool) {
	_, rest, ok := strings.Cut(body, suggestionFence)
	if !ok {
		return "", false
	}
	code, _, ok := strings.Cut("\n"+rest, "\n```")
	if !ok {
		return "", false
	}
	return strings.TrimPrefix(code, "\n"), true
}
