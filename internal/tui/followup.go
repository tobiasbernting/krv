package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/tobiasbernting/code-review-cli/internal/diffparse"
	"github.com/tobiasbernting/code-review-cli/internal/followup"
	"github.com/tobiasbernting/code-review-cli/internal/ghsrc"

	"github.com/tobiasbernting/code-review-cli/internal/render"
)

type followupState struct {
	session                                         *followup.Session
	threads                                         []ghsrc.Thread
	cursor, top                                     int
	busy                                            bool
	context, contextErr, contextThread, contextHead string
	contextLoading                                  bool
}

func (m *Model) installFollowUp(s *followup.Session) {
	if s == nil {
		return
	}
	m.follow = followupState{session: s, threads: s.OwnThreads()}
	if s.Baseline != nil {
		m.mode = modeThreads
	}
	if len(s.Warnings) > 0 {
		m.err = strings.Join(s.Warnings, "; ")
	}
}

func (m Model) selectedThread() (ghsrc.Thread, bool) {
	if m.follow.cursor < 0 || m.follow.cursor >= len(m.follow.threads) {
		return ghsrc.Thread{}, false
	}
	return m.follow.threads[m.follow.cursor], true
}

func (m Model) handleThreadKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.sync.syncing {
		m.status = "wait for sync to finish"
		return m, nil
	}
	if m.mode == modeReply {
		done, cancelled := m.in.handle(msg)
		if cancelled {
			m.in.stop()
			m.mode = modeThread
			return m, nil
		}
		if done {
			body := strings.TrimSpace(m.in.value)
			if body == "" {
				m.err = "write a reply, or press esc to cancel"
				return m, nil
			}
			t, ok := m.selectedThread()
			if !ok || len(t.Comments) == 0 {
				return m, nil
			}
			m.follow.busy = true
			src := m.src
			return m, func() tea.Msg {
				comment, err := src.Client.Reply(src.Repo, src.PRNumber, t.Comments[0].ID, body)
				return threadActionMsg{id: t.ID, reply: &comment, err: err}
			}
		}
		return m, nil
	}
	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "?":
		m.openHelp()
	case "esc", "t":
		if m.mode == modeThreads {
			m.mode = modeDiff
		} else {
			m.mode = modeThreads
		}
		m.follow.top = 0
	case "r":
		return m.startSync(nil)
	case "a":
		return m.showChanges()
	case "D":
		path := ""
		if t, ok := m.selectedThread(); ok {
			path, _ = m.follow.session.Evidence(t)
		}
		m.showCurrentDiff(path)
	case "S":
		return m.openSubmit()
	case "j", "down", "k", "up", "ctrl+d", "ctrl+u", "g", "G":
		delta := 1
		if key == "k" || key == "up" || key == "ctrl+u" {
			delta = -1
		}
		if key == "ctrl+d" || key == "ctrl+u" {
			delta *= maxInt(1, (m.height-3)/2)
		}
		if m.mode == modeThreads {
			m.follow.cursor += delta
			if key == "g" {
				m.follow.cursor = 0
			}
			if key == "G" {
				m.follow.cursor = len(m.follow.threads) - 1
			}
			m.follow.cursor = maxInt(0, min(m.follow.cursor, len(m.follow.threads)-1))
		} else {
			m.follow.top += delta
			if key == "g" {
				m.follow.top = 0
			}
			if key == "G" {
				m.follow.top = len(m.threadLines())
			}
			m.follow.top = maxInt(0, min(m.follow.top, len(m.threadLines())-maxInt(1, m.height-3)))
		}
	case "enter", " ":
		if _, ok := m.selectedThread(); ok {
			m.mode = modeThread
			m.follow.top = 0
			return m, m.loadThreadContext()
		}
	case "n", "p", "tab", "shift+tab":
		if key == "n" || key == "tab" {
			m.follow.cursor++
		} else {
			m.follow.cursor--
		}
		m.follow.cursor = maxInt(0, min(m.follow.cursor, len(m.follow.threads)-1))
		m.follow.top = 0
		if m.mode == modeThread {
			return m, m.loadThreadContext()
		}
	case "x":
		return m.verifyThread()
	case "c":
		if _, ok := m.selectedThread(); ok {
			m.mode = modeReply
			m.in.start("reply to GitHub (enter sends) ›", "")
		}
	case "R":
		t, ok := m.selectedThread()
		if !ok {
			return m, nil
		}
		if t.GraphQLID == "" {
			m.err = "GitHub did not report this thread's resolution state; press r to retry"
			return m, nil
		}
		if (!t.Resolved && !t.ViewerCanResolve) || (t.Resolved && !t.ViewerCanUnresolve) {
			m.err = "GitHub does not allow you to change this thread's resolution"
			return m, nil
		}
		m.follow.busy = true
		client := m.src.Client
		return m, func() tea.Msg {
			err := client.SetThreadResolved(t.GraphQLID, !t.Resolved)
			return threadActionMsg{id: t.ID, resolved: !t.Resolved, err: err}
		}
	}
	return m, nil
}

func (m Model) verifyThread() (tea.Model, tea.Cmd) {
	t, ok := m.selectedThread()
	if !ok {
		return m, nil
	}
	_, fingerprint := m.follow.session.Evidence(t)
	if fingerprint == "" {
		m.err = "verification needs the historical comparison; refresh to retry"
		return m, nil
	}
	verified, _ := m.review.ThreadState(t.ID, fingerprint)
	m.review.SetThreadVerified(t.ID, fingerprint, m.src.HeadSHA, !verified)
	m.save()
	if verified {
		m.status = "verification cleared; GitHub resolution unchanged"
	} else {
		m.status = "verified locally at " + shortSHA(m.src.HeadSHA) + "; GitHub resolution unchanged"
	}
	return m, nil
}

type threadActionMsg struct {
	id       string
	reply    *ghsrc.Comment
	resolved bool
	err      error
}

func (m Model) applyThreadAction(msg threadActionMsg) (tea.Model, tea.Cmd) {
	m.follow.busy = false
	if msg.err != nil {
		m.err = msg.err.Error()
		return m, nil
	}
	for i := range m.follow.threads {
		if m.follow.threads[i].ID != msg.id {
			continue
		}
		if msg.reply != nil {
			m.follow.threads[i].Comments = append(m.follow.threads[i].Comments, *msg.reply)
			m.in.stop()
			m.mode = modeThread
			m.status = "reply posted to GitHub"
		} else {
			m.follow.threads[i].Resolved = msg.resolved
			// A successful mutation grants the inverse operation for this local
			// snapshot; a later server permission change still fails explicitly.
			m.follow.threads[i].ViewerCanResolve = true
			m.follow.threads[i].ViewerCanUnresolve = true
			m.status = "thread reopened on GitHub"
			if msg.resolved {
				m.status = "thread resolved on GitHub; still visible for verification"
			}
		}
		for j := range m.follow.session.Threads {
			if m.follow.session.Threads[j].ID == msg.id {
				m.follow.session.Threads[j] = m.follow.threads[i]
			}
		}
	}
	m.threads = m.follow.session.Threads
	m.rebuild()
	return m, nil
}

func (m Model) showChanges() (tea.Model, tea.Cmd) {
	s := m.follow.session
	if s == nil {
		return m, nil
	}
	if s.Comparison == nil {
		m.err = s.ComparisonError
		if m.err == "" {
			m.err = "no submitted review baseline is available; press D for the current PR diff"
		}
		return m, nil
	}
	m.files = diffparse.Parse(s.Comparison.Diff)
	diffparse.FillStats(m.files)
	m.mode, m.changesView = modeDiff, true
	m.doc = nil
	m.cursor, m.top, m.hoffset, m.fileCursor = 0, 0, 0, 0
	m.rangeAnchor, m.rangeAnchorPath = 0, ""
	m.rebuild()
	return m, nil
}

func (m *Model) showCurrentDiff(path string) {
	if m.follow.session == nil {
		return
	}
	if path == "" && m.doc != nil && len(m.files) > 0 && m.cursor >= 0 && m.cursor < len(m.doc.Rows) {
		if i := m.doc.Rows[m.cursor].FileIdx; i >= 0 && i < len(m.files) {
			path = m.files[i].Path()
		}
	}
	m.files = m.follow.session.Files
	m.mode, m.changesView = modeDiff, false
	m.doc = nil
	m.cursor, m.top, m.fileCursor = 0, 0, 0
	m.rangeAnchor, m.rangeAnchorPath = 0, ""
	m.rebuild()
	for i, f := range m.files {
		if f.Path() == path {
			m.seek(m.doc.FileRows[i])
			break
		}
	}
}

type threadContextMsg struct {
	thread, head, text string
	err                error
}

func (m *Model) loadThreadContext() tea.Cmd {
	t, ok := m.selectedThread()
	if !ok {
		return nil
	}
	s := m.follow.session
	if m.follow.contextThread == t.ID && m.follow.contextHead == m.src.HeadSHA {
		return nil
	}
	m.follow.context, m.follow.contextErr = "", ""
	m.follow.contextLoading = false
	m.follow.contextThread, m.follow.contextHead = t.ID, m.src.HeadSHA
	path, _ := s.Evidence(t)
	if s.Comparison == nil {
		return nil
	}
	file, exists := s.Comparison.HeadFiles[path]
	if !exists {
		m.follow.contextErr = "This path is absent from the current revision. Inspect all changes for a move or replacement; verification will expire on the next revision."
		return nil
	}
	fileMode, sha, found := strings.Cut(file, ":")
	if !found || fileMode == "160000" {
		m.follow.contextErr = "Text context unavailable for this file type."
		return nil
	}
	m.follow.contextLoading = true
	src := m.src
	return func() tea.Msg {
		text, err := src.Client.BlobText(src.Repo, sha)
		return threadContextMsg{thread: t.ID, head: src.HeadSHA, text: text, err: err}
	}
}

func (m Model) applyThreadContext(msg threadContextMsg) (tea.Model, tea.Cmd) {
	if msg.thread != m.follow.contextThread || msg.head != m.follow.contextHead {
		return m, nil
	}
	m.follow.contextLoading = false
	if msg.err != nil {
		m.follow.contextErr = "Current context unavailable: " + msg.err.Error()
	} else {
		m.follow.context = msg.text
	}
	return m, nil
}

func shortSHA(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

func (m Model) baselineLabel() string {
	s := m.follow.session
	if s == nil || s.Baseline == nil {
		return "No submitted review baseline · current PR " + shortSHA(m.src.HeadSHA)
	}
	return fmt.Sprintf("Since your review %s · %s → %s", s.Baseline.SubmittedAt.Format("Jan 2 15:04"), shortSHA(s.Baseline.CommitID), shortSHA(m.src.HeadSHA))
}

func (m Model) threadStatus(t ghsrc.Thread) string {
	resolution := "open"
	if !t.ResolutionKnown {
		resolution = "resolution unavailable"
	}
	if t.Resolved {
		resolution = "resolved on GitHub"
	}
	_, fingerprint := m.follow.session.Evidence(t)
	verified, changed := m.review.ThreadState(t.ID, fingerprint)
	verification := "needs verification"
	if verified {
		verification = "✓ verified by you"
	} else if changed {
		verification = "~ changed; verify again"
	}
	if t.Outdated {
		resolution += " · outdated"
	}
	return resolution + " · " + verification
}

func (m Model) followupView() string {
	var lines []string
	if m.mode == modeThreads {
		lines = m.threadListLines()
	} else {
		lines = m.threadLines()
	}
	height := maxInt(1, m.height-3)
	if m.mode == modeReply {
		height = maxInt(1, height-1)
	}
	top := m.follow.top
	if m.mode == modeThreads {
		top = m.threadListTop()
	} else {
		top = maxInt(0, min(top, len(lines)-height))
	}
	var b strings.Builder
	b.WriteString(bar(m.theme, m.width, " "+m.src.Title, ""))
	b.WriteByte('\n')
	b.WriteString(ansi.Truncate(" "+m.baselineLabel(), maxInt(1, m.width), "…"))
	b.WriteByte('\n')
	for i := 0; i < height; i++ {
		if idx := top + i; idx < len(lines) {
			b.WriteString(ansi.Truncate(lines[idx], maxInt(1, m.width), "…"))
		}
		b.WriteByte('\n')
	}
	if m.mode == modeReply {
		b.WriteString(m.in.render(m.width, m.theme.NoteFg, m.theme.NoteBg))
		b.WriteByte('\n')
	}
	verified := 0
	for _, t := range m.follow.threads {
		_, fingerprint := m.follow.session.Evidence(t)
		if ok, _ := m.review.ThreadState(t.ID, fingerprint); ok {
			verified++
		}
	}
	left := fmt.Sprintf(" %d/%d threads verified", verified, len(m.follow.threads))
	if m.sync.syncing {
		left = " refreshing…"
	} else if m.follow.busy {
		left = " updating GitHub…"
	} else if m.sync.err != "" {
		left = " sync failed: " + m.sync.err
	} else if m.err != "" {
		left = " " + m.err
	} else if m.status != "" {
		left = " " + m.status
	}
	right := fitHint(m.width, left, m.hintKeys())
	if m.mode == modeReply {
		right = "enter sends to GitHub  esc cancels"
	}
	b.WriteString(bar(m.theme, m.width, left, right))
	return b.String()
}

// threadListPrefix is how many warning lines precede the threads.
func (m Model) threadListPrefix() int {
	prefix := len(m.follow.session.Warnings)
	if m.follow.session.ComparisonError != "" {
		prefix++
	}
	return prefix
}

// threadListTop is the first line the thread list shows. Each thread is two
// lines, and the list scrolls only as far as keeps the selection on screen.
func (m Model) threadListTop() int {
	target := m.threadListPrefix() + 2*m.follow.cursor
	return maxInt(0, target-maxInt(1, m.height-3)+2)
}

func (m Model) threadListLines() []string {
	s := m.follow.session
	var lines []string
	for _, warning := range s.Warnings {
		lines = append(lines, " ! "+warning)
	}
	if s.ComparisonError != "" {
		lines = append(lines, " ! "+s.ComparisonError)
	}
	if len(m.follow.threads) == 0 {
		return append(lines, "", " No threads started by you are available.", " Press a for changes since your review, or D for the current PR diff.")
	}
	for i, t := range m.follow.threads {
		marker := "  "
		style := lipgloss.NewStyle()
		if i == m.follow.cursor {
			marker = "▸ "
			style = style.Background(lipgloss.Color(m.theme.CursorBg)).Bold(true)
		}
		line := t.OriginalLine
		if line == 0 {
			line = t.Line
		}
		lines = append(lines, style.Render(fmt.Sprintf("%s%s:%d · %s", marker, t.Path, line, m.threadStatus(t))))
		body := ""
		if len(t.Comments) > 0 {
			body = firstLine(t.Comments[0].Body)
		}
		lines = append(lines, "    "+body)
	}
	return lines
}

func (m Model) threadLines() []string {
	t, ok := m.selectedThread()
	if !ok {
		return []string{" No thread selected. Press t to return."}
	}
	s := m.follow.session
	lines := []string{fmt.Sprintf(" %s · %s", t.Path, m.threadStatus(t)), ""}
	appendText := func(text string) {
		wrapped := lipgloss.NewStyle().Width(maxInt(1, m.width-4)).Render(text)
		for _, line := range strings.Split(wrapped, "\n") {
			lines = append(lines, "  "+line)
		}
	}
	if len(t.Comments) > 0 {
		c := t.Comments[0]
		appendText("Your original comment · " + shortSHA(c.OriginalCommitID))
		appendText(c.Body)
		if c.DiffHunk != "" {
			lines = append(lines, "", " Original code context (when this comment was written):")
			path := strconv.Quote("a/"+t.Path) + " " + strconv.Quote("b/"+t.Path)
			files := diffparse.Parse("diff --git " + path + "\n" + c.DiffHunk + "\n")
			lines = append(lines, m.renderThreadDiff(files)...)
		} else {
			appendText("Original code context is unavailable.")
		}
	}
	lines = append(lines, "", " Changes in this file since your latest review:")
	switch {
	case s.ComparisonError != "":
		appendText(s.ComparisonError)
	case s.Comparison == nil:
		appendText("No submitted review baseline is available. Press D for the current PR diff.")
	default:
		files := s.ChangesFor(t)
		if len(files) == 0 {
			appendText("No changes to this file since that review. This does not establish that the concern is fixed.")
		} else {
			lines = append(lines, m.renderThreadDiff(files)...)
		}
	}
	lines = append(lines, "", " Replies:")
	if len(t.Comments) < 2 {
		appendText("No replies yet.")
	} else {
		for _, c := range t.Comments[1:] {
			appendText(c.User.Login + ": " + c.Body)
		}
	}
	lines = append(lines, "", " Current file context · "+shortSHA(m.src.HeadSHA)+":")
	switch {
	case m.follow.contextThread != t.ID:
		appendText("Press enter to load current context.")
	case m.follow.contextLoading:
		appendText("Loading current context…")
	case m.follow.contextErr != "":
		appendText(m.follow.contextErr)
	case s.Comparison == nil:
		appendText("Current file context unavailable without revision metadata; press D for the current PR diff.")
	default:
		content := strings.Split(strings.TrimSuffix(m.follow.context, "\n"), "\n")
		if t.Outdated || t.Line == 0 || t.Side == "LEFT" {
			appendText("The original line has no reliable current-side anchor. Showing the current file; no replacement line was guessed.")
			for i, line := range content {
				lines = append(lines, fmt.Sprintf(" %5d  %s", i+1, line))
			}
		} else {
			start := t.Line - 6
			if t.StartLine > 0 {
				start = t.StartLine - 6
			}
			for i := maxInt(0, start); i < min(len(content), t.Line+5); i++ {
				lines = append(lines, fmt.Sprintf(" %5d  %s", i+1, content[i]))
			}
		}
	}
	return lines
}

func (m Model) renderThreadDiff(files []*diffparse.FileDiff) []string {
	doc := render.Build(files, m.hl, render.Overlay{}, m.layout.Fit(m.width))
	r := render.NewRenderer(m.theme, doc)
	lines := make([]string, 0, len(doc.Rows))
	for _, row := range doc.Rows {
		lines = append(lines, r.Render(row, maxInt(1, m.width), 0, false))
	}
	return lines
}
