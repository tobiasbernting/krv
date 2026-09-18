package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/tobiasbernting/code-review-cli/internal/ghsrc"
	"github.com/tobiasbernting/code-review-cli/internal/notes"
)

// submitState drives the review submission screen. Notes are held locally
// until this point because GitHub reviews are atomic: one review, one
// notification, and a half-written review never reaches the author.
type submitState struct {
	returnMode mode
	event      string
	body       string
	editing    bool // the overall review body is being typed
	sending    bool
}

func (m Model) openSubmit() (tea.Model, tea.Cmd) {
	if m.sync.syncing {
		m.err = "wait for sync to finish before submitting"
		return m, nil
	}
	if !m.src.CanSubmit() {
		m.err = "not reviewing a pull request — drafts stay local"
		return m, nil
	}
	if m.needsReanchor() > 0 {
		m.err = fmt.Sprintf("%d draft(s) need re-anchoring before submission", m.needsReanchor())
		return m, nil
	}
	m.submit = submitState{event: ghsrc.EventComment, returnMode: m.mode}
	m.mode = modeSubmit
	return m, nil
}

func (m Model) needsReanchor() int {
	n := 0
	for _, note := range m.review.Notes {
		current, exists := m.blobs[note.Path]
		if !exists || noteMoved(note.Blob, current) {
			n++
		}
	}
	return n
}

func noteMoved(noteBlob, current string) bool {
	return noteBlob != "" && current != "" && noteBlob != current
}

type submitResultMsg struct {
	err   error
	event string
}

func (m Model) handleSubmitKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.submit.sending {
		return m, nil
	}
	if m.submit.editing {
		done, cancelled := m.in.handle(msg)
		if cancelled {
			m.in.stop()
			m.submit.editing = false
		}
		if done {
			m.submit.body = strings.TrimSpace(m.in.value)
			m.in.stop()
			m.submit.editing = false
		}
		return m, nil
	}

	switch msg.String() {
	case "esc", "q":
		m.mode = m.submit.returnMode
	case "c":
		m.submit.event = ghsrc.EventComment
	case "a":
		if m.src.OwnPR() {
			m.err = "GitHub does not let you approve your own pull request"
			return m, nil
		}
		m.submit.event = ghsrc.EventApprove
	case "r":
		if m.src.OwnPR() {
			m.err = "GitHub does not let you request changes on your own pull request"
			return m, nil
		}
		m.submit.event = ghsrc.EventRequestChanges
	case "b":
		m.submit.editing = true
		m.in.start("review body ›", m.submit.body)
	case "enter":
		if m.submit.event == ghsrc.EventComment && strings.TrimSpace(m.submit.body) == "" && len(m.review.Notes) == 0 {
			m.err = "add a review body or a comment, or choose approve"
			return m, nil
		}
		if m.submit.event == ghsrc.EventRequestChanges && strings.TrimSpace(m.submit.body) == "" {
			m.err = "requesting changes requires a review body — press b to add one"
			return m, nil
		}
		if m.src.OwnPR() && m.submit.event != ghsrc.EventComment {
			m.err = "GitHub does not let you approve or request changes on your own pull request"
			return m, nil
		}
		m.submit.sending = true
		return m, m.sendReview()
	}
	return m, nil
}

// sendReview posts every note as one GitHub review.
func (m Model) sendReview() tea.Cmd {
	comments := make([]ghsrc.ReviewComment, 0, len(m.review.Notes))
	for _, n := range m.review.Notes {
		rc := ghsrc.ReviewComment{
			Path: n.Path,
			Body: n.Body,
			Line: n.Line,
			Side: n.Side,
		}
		if n.StartLine > 0 && n.StartLine != n.Line {
			rc.StartLine = n.StartLine
			rc.StartSide = n.Side
		}
		comments = append(comments, rc)
	}

	src, event, body := m.src, m.submit.event, m.submit.body
	return func() tea.Msg {
		err := src.Client.SubmitReviewAt(src.Repo, src.PRNumber, src.HeadSHA, event, body, comments)
		return submitResultMsg{err: err, event: event}
	}
}

func (m Model) applySubmitResult(msg submitResultMsg) (tea.Model, tea.Cmd) {
	m.submit.sending = false
	if msg.err != nil {
		m.err = msg.err.Error()
		m.mode = modeSubmit
		return m, nil
	}

	// GitHub owns these comments now. Dropping the local copies is what keeps
	// there from being two versions of the same review.
	sent := len(m.review.Notes)
	submitted := append([]notes.Note(nil), m.review.Notes...)
	m.review.Notes = nil
	m.save()
	m.rebuild()
	m.mode = m.submit.returnMode
	m.status = fmt.Sprintf("submitted %d comment%s as %s; syncing",
		sent, plural(sent), strings.ToLower(strings.ReplaceAll(msg.event, "_", " ")))
	return m.startSync(submitted)
}

func (m Model) submitView() string {
	t := m.theme
	title := lipgloss.NewStyle().Bold(true)
	key := lipgloss.NewStyle().Foreground(lipgloss.Color(t.HunkFg)).Bold(true)
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color(t.MetaFg))
	sel := lipgloss.NewStyle().Foreground(lipgloss.Color(t.ReviewedFg)).Bold(true)

	var b strings.Builder
	fmt.Fprintf(&b, "\n  %s\n\n", title.Render(fmt.Sprintf("Submit review to %s#%d", m.src.Repo, m.src.PRNumber)))

	for _, opt := range []struct{ key, event, label string }{
		{"c", ghsrc.EventComment, "Comment"},
		{"a", ghsrc.EventApprove, "Approve"},
		{"r", ghsrc.EventRequestChanges, "Request changes"},
	} {
		marker := "  "
		style := dim
		if m.submit.event == opt.event {
			marker, style = "▸ ", sel
		}
		label := opt.label
		if m.src.OwnPR() && opt.event != ghsrc.EventComment {
			label = dim.Render(opt.label + "  (not allowed on your own pull request)")
		} else {
			label = style.Render(label)
		}
		fmt.Fprintf(&b, "  %s%s  %s\n", marker, key.Render(opt.key), label)
	}

	body := m.submit.body
	if body == "" {
		body = dim.Render("(none — press b to add one)")
	}
	fmt.Fprintf(&b, "\n  %s %s\n", key.Render("b"), "body: "+body)

	fmt.Fprintf(&b, "\n  %d comment%s will be posted as one review:\n\n",
		len(m.review.Notes), plural(len(m.review.Notes)))
	for i, n := range m.review.Notes {
		if i >= 8 {
			fmt.Fprintf(&b, "    %s\n", dim.Render(fmt.Sprintf("… and %d more", len(m.review.Notes)-8)))
			break
		}
		loc := fmt.Sprintf("L%d", n.Line)
		if n.StartLine > 0 {
			loc = fmt.Sprintf("L%d-%d", n.StartLine, n.Line)
		}
		fmt.Fprintf(&b, "    %s %s  %s\n", dim.Render(n.Path), dim.Render(loc), firstLine(n.Body))
	}

	if m.submit.sending {
		fmt.Fprintf(&b, "\n  %s\n", sel.Render("submitting…"))
	} else if m.err != "" {
		fmt.Fprintf(&b, "\n  %s\n", lipgloss.NewStyle().Foreground(lipgloss.Color(t.DelFg)).Render(m.err))
	}

	fmt.Fprintf(&b, "\n  %s submit   %s cancel\n", key.Render("enter"), key.Render("esc"))
	if m.submit.editing {
		b.WriteString("\n" + m.in.render(m.width, t.NoteFg, t.NoteBg) + "\n")
	}
	return b.String()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}
