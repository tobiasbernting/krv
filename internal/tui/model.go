// Package tui is the interactive viewport over a rendered diff document.
package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/tobiasbernting/code-review-cli/internal/config"
	"github.com/tobiasbernting/code-review-cli/internal/diffparse"
	"github.com/tobiasbernting/code-review-cli/internal/ghsrc"
	"github.com/tobiasbernting/code-review-cli/internal/notes"
	"github.com/tobiasbernting/code-review-cli/internal/render"
)

type mode int

const (
	modeDiff mode = iota
	modeFiles
	modeHelp
	modeInput
	modeSubmit
	modeComment
	modeThreads
	modeThread
	modeReply
)

// Options are everything New needs that is not the diff itself.
type Options struct {
	Files     []*diffparse.FileDiff
	Theme     render.Theme
	Layout    render.Layout
	Config    config.Config
	Source    Source
	Review    *notes.Review
	Threads   []ghsrc.Thread
	SyncedAt  time.Time
	SyncError string
	Clipboard Clipboard
}

type Model struct {
	files  []*diffparse.FileDiff
	hl     *render.Highlighter
	doc    *render.Document
	rend   *render.Renderer
	theme  render.Theme
	layout render.Layout
	cfg    config.Config
	src    Source
	review *notes.Review
	clip   Clipboard

	// blobs maps a path to the hash of its new-side content, so changed drafts
	// can be detached for re-anchoring without re-reading the file.
	blobs map[string]string

	threads         []ghsrc.Thread
	expandedThreads map[string]bool
	newComments     map[int64]bool
	updatedComments map[int64]bool

	width, height int
	mode          mode

	cursor  int
	top     int
	hoffset int

	fileCursor int
	status     string
	err        string

	in input

	// rangeAnchor is the line a multi-line note starts from, 0 when not
	// selecting.
	rangeAnchor     int
	rangeAnchorPath string
	rangeAnchorHunk int

	// pending describes the note being composed.
	pending pendingNote

	follow      followupState
	changesView bool
	helpReturn  mode
	helpTop     int
	submit      submitState
	sync        syncState
	detail      commentDetail
	reanchor    reanchorState
	lastPress   lastPress
	drag        dragState

	// now is the clock double clicks are timed against.
	now func() time.Time
}

type pendingNote struct {
	path      string
	startLine int
	line      int
	editingID string
}

func New(opts Options) Model {
	m := Model{
		files:           opts.Files,
		theme:           opts.Theme,
		layout:          opts.Layout,
		cfg:             opts.Config,
		src:             opts.Source,
		review:          opts.Review,
		threads:         opts.Threads,
		width:           80,
		height:          24,
		hl:              render.NewHighlighter(opts.Theme.Syntax, opts.Config.Color),
		expandedThreads: map[string]bool{},
		newComments:     map[int64]bool{},
		updatedComments: map[int64]bool{},
		now:             time.Now,
		clip:            opts.Clipboard,
	}
	m.sync.syncedAt = opts.SyncedAt
	m.sync.err = opts.SyncError
	if opts.SyncError != "" {
		m.sync.failedAt = time.Now()
	}
	if opts.Source.FollowUp != nil {
		m.threads = opts.Source.FollowUp.Threads
	}
	m.blobs = Blobs(opts.Files)
	if m.review == nil {
		m.review = &notes.Review{Files: map[string]notes.FileMark{}}
	}
	m.rebuild()
	m.cursor = m.nextSelectable(0, 1)
	m.installFollowUp(opts.Source.FollowUp)
	return m
}

// rebuild regenerates the document after notes or marks change, keeping the
// cursor on the same line rather than on the same row index.
func (m *Model) rebuild() {
	m.rebuildAt(m.cursorAnchor())
}

type documentAnchor struct {
	path           string
	line           int
	oldLine        int
	rowKind        render.RowKind
	annotationKind render.AnnotationKind
	id             string
}

func (m Model) cursorAnchor() documentAnchor {
	var anchor documentAnchor
	if m.doc != nil && m.cursor >= 0 && m.cursor < len(m.doc.Rows) {
		row := m.doc.Rows[m.cursor]
		anchor.rowKind = row.Kind
		if row.Ann != nil {
			anchor.annotationKind = row.Ann.Kind
			anchor.id = row.Ann.ID
			anchor.line = row.Ann.Line
		}
		if row.FileIdx < len(m.files) {
			anchor.path = m.files[row.FileIdx].Path()
		}
		if row.Ann == nil {
			anchor.line = row.Line.NewNum
			if row.IsCode() {
				anchor.line, anchor.oldLine = row.NewNum(), row.Line.OldNum
			}
		}
	}
	return anchor
}

func (m *Model) rebuildAt(anchor documentAnchor) {
	m.doc = render.Build(m.files, m.hl, m.overlay(), m.layout.Fit(m.width))
	m.rend = render.NewRenderer(m.theme, m.doc)

	if len(m.doc.Rows) == 0 {
		m.cursor, m.top = 0, 0
		return
	}
	if anchor.id != "" {
		for i, row := range m.doc.Rows {
			if row.Ann != nil && row.Ann.Kind == anchor.annotationKind && row.Ann.ID == anchor.id {
				m.cursor = i
				m.clampScroll()
				return
			}
		}
	}
	if anchor.path == "" {
		if m.cursor >= len(m.doc.Rows) {
			m.cursor = m.nextSelectable(len(m.doc.Rows)-1, -1)
		}
		m.clampScroll()
		return
	}
	// A code row is a RowCode in one layout and a RowPair in the other, so it
	// is found by its line numbers rather than by its kind.
	if anchor.rowKind == render.RowCode || anchor.rowKind == render.RowPair {
		for fi, f := range m.files {
			if f.Path() != anchor.path {
				continue
			}
			if i, ok := m.doc.LineRow(fi, anchor.line, anchor.oldLine); ok {
				m.cursor = i
				m.clampScroll()
				return
			}
		}
	}
	for i, row := range m.doc.Rows {
		if row.Kind != anchor.rowKind || row.FileIdx >= len(m.files) {
			continue
		}
		if m.files[row.FileIdx].Path() == anchor.path && row.Line.NewNum == anchor.line {
			m.cursor = i
			m.clampScroll()
			return
		}
	}
	if m.cursor >= len(m.doc.Rows) {
		m.cursor = m.nextSelectable(len(m.doc.Rows)-1, -1)
	}
	m.clampScroll()
}

func (m *Model) overlay() render.Overlay {
	if m.changesView {
		return render.Overlay{}
	}
	return Overlay(m.review, m.threads, m.files, OverlayOptions{
		Expanded: m.expandedThreads, NewComments: m.newComments,
		UpdatedComments: m.updatedComments,
	})
}

func (m Model) Init() tea.Cmd { return tickSyncAge() }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// Crossing SplitMinWidth changes what the document is shaped like,
		// not only how wide it is painted.
		if m.doc != nil && m.layout.Fit(m.width).Mode != m.doc.Layout.Mode {
			m.rebuild()
		}
		m.clampScroll()
		return m, nil
	case editorFinishedMsg:
		return m.applyEditorResult(msg)
	case submitResultMsg:
		return m.applySubmitResult(msg)
	case threadActionMsg:
		return m.applyThreadAction(msg)
	case threadContextMsg:
		return m.applyThreadContext(msg)
	case syncResultMsg:
		return m.applySyncResult(msg)
	case syncTickMsg:
		return m, tickSyncAge()
	case yankedMsg:
		return m.applyYanked(msg)
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tea.MouseMsg:
		return m.handleMouse(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	m.status, m.err = "", ""
	if m.follow.busy {
		// Quitting stays available: a GitHub mutation can hang, and the review
		// must not become impossible to leave while it does. Only ctrl+c does
		// it while a note is being typed, where q is just a letter.
		typing := m.mode == modeReply || m.mode == modeInput
		if key == "ctrl+c" || (key == "q" && !typing) {
			return m, tea.Quit
		}
		return m, nil
	}

	switch m.mode {
	case modeThreads, modeThread, modeReply:
		return m.handleThreadKey(msg)
	case modeInput:
		return m.handleInputKey(msg)
	case modeSubmit:
		return m.handleSubmitKey(msg)
	case modeComment:
		return m.handleCommentKey(msg)
	case modeHelp:
		return m.handleHelpKey(key), nil
	case modeFiles:
		return m.handleFilesKey(key)
	}
	if m.reanchor.id != "" {
		return m.handleReanchorKey(key)
	}

	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "t":
		if m.follow.session != nil {
			m.mode = modeThreads
		}
	case "a":
		return m.showChanges()
	case "D":
		m.showCurrentDiff("")
	case "?":
		m.openHelp()
	case "f":
		if len(m.doc.Rows) == 0 || len(m.doc.Files) == 0 {
			m.err = "no files"
			return m, nil
		}
		m.mode = modeFiles
		// Orphaned annotations carry a FileIdx one past the last file, so the
		// list would otherwise open with nothing selected.
		m.fileCursor = min(m.doc.Rows[m.cursor].FileIdx, len(m.doc.Files)-1)
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
		m.visitCurrentThread()
	case "G", "end":
		m.cursor = m.nextSelectable(len(m.doc.Rows)-1, -1)
		m.clampScroll()
		m.visitCurrentThread()
	case "n":
		m.jump(m.doc.HunkRows, 1, "hunk")
	case "p":
		m.jump(m.doc.HunkRows, -1, "hunk")
	case "tab", "J", "]":
		m.jump(m.doc.FileRows, 1, "file")
	case "shift+tab", "K", "[":
		m.jump(m.doc.FileRows, -1, "file")
	case "l", "right":
		m.hoffset += hStep
	case "h", "left":
		m.hoffset = max(0, m.hoffset-hStep)
	case "0":
		m.hoffset = 0
	case "s":
		if m.layout.Mode == render.ModeSplit {
			m.layout.Mode = render.ModeUnified
		} else {
			m.layout.Mode = render.ModeSplit
		}
		m.rebuild()
		m.status = m.layout.Mode.String()
	case "r":
		return m.startSync(nil)
	case "N":
		m.jumpActivity(1)
	case "P":
		m.jumpActivity(-1)
	case "enter":
		return m.openOrToggleComment()

	// review actions
	case "c":
		return m.startComment()
	case "v":
		return m.toggleRangeAnchor()
	case "y":
		return m.yank(false)
	case "Y":
		return m.yank(true)
	case "e":
		return m.editNoteUnderCursor()
	case "d":
		return m.deleteNoteUnderCursor()
	case "m":
		return m.startReanchor()
	case "x":
		return m.toggleReviewed()
	case "S":
		return m.openSubmit()
	}
	return m, nil
}

func (m Model) handleFilesKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc", "f":
		m.mode = modeDiff
	case "j", "down":
		if m.fileCursor < len(m.doc.Files)-1 {
			m.fileCursor++
		}
	case "k", "up":
		if m.fileCursor > 0 {
			m.fileCursor--
		}
	case "g", "home":
		m.fileCursor = 0
	case "G", "end":
		m.fileCursor = maxInt(0, len(m.doc.Files)-1)
	case "x":
		if m.fileCursor >= 0 && m.fileCursor < len(m.files) {
			path := m.files[m.fileCursor].Path()
			reviewed, _ := m.review.ReviewState(path, m.blobs[path])
			m.review.SetReviewed(path, m.blobs[path], !reviewed)
			m.save()
			m.rebuild()
		}
	case "enter", " ":
		if m.fileCursor >= 0 && len(m.doc.FileRows) > m.fileCursor {
			fileRow := m.doc.FileRows[m.fileCursor]
			if m.doc.Rows[fileRow].Collapsed {
				m.cursor = fileRow
			} else {
				m.cursor = m.nextSelectable(fileRow, 1)
			}
			m.top = m.doc.FileRows[m.fileCursor]
			m.clampScroll()
		}
		m.mode = modeDiff
	}
	return m, nil
}

// nextSelectable finds the nearest row the cursor is allowed to rest on,
// searching in direction dir. Spacers are skipped so the cursor never appears
// to vanish between files.
func (m Model) nextSelectable(from, dir int) int {
	for i := from; i >= 0 && i < len(m.doc.Rows); i += dir {
		if m.doc.Rows[i].Kind != render.RowSpacer {
			return i
		}
	}
	for i := from; i >= 0 && i < len(m.doc.Rows); i -= dir {
		if m.doc.Rows[i].Kind != render.RowSpacer {
			return i
		}
	}
	return 0
}

func (m *Model) moveCursor(delta int) {
	if len(m.doc.Rows) == 0 {
		return
	}
	target := m.cursor + delta
	if target < 0 {
		target = 0
	}
	if target >= len(m.doc.Rows) {
		target = len(m.doc.Rows) - 1
	}
	dir := 1
	if delta < 0 {
		dir = -1
	}
	m.cursor = m.nextSelectable(target, dir)
	m.clampScroll()
	m.visitCurrentThread()
}

// jump moves the cursor to the next or previous anchor row, scrolling that
// anchor to the top of the viewport so the file or hunk header is the first
// thing read rather than appearing at the bottom edge.
func (m *Model) jump(anchors []int, dir int, what string) {
	if len(anchors) == 0 {
		return
	}
	if dir > 0 {
		for _, a := range anchors {
			if a > m.cursor {
				m.seek(a)
				return
			}
		}
		m.status = "last " + what
		return
	}
	for i := len(anchors) - 1; i >= 0; i-- {
		if anchors[i] < m.cursor {
			m.seek(anchors[i])
			return
		}
	}
	m.status = "first " + what
}

// seek puts row at the top of the viewport with the cursor on it.
func (m *Model) seek(row int) {
	m.cursor = row
	m.top = row
	m.clampScroll()
	m.visitCurrentThread()
}

func (m *Model) viewportHeight() int {
	h := m.height - 1 // status bar
	if m.mode == modeInput {
		h--
	}
	if h < 1 {
		h = 1
	}
	return h
}

func (m *Model) clampScroll() {
	vh := m.viewportHeight()
	rowHeight := 1
	if m.cursor >= 0 && m.cursor < len(m.doc.Rows) && m.doc.Rows[m.cursor].Kind == render.RowNote {
		rowHeight = len(m.rend.RenderLines(m.doc.Rows[m.cursor], m.width, m.hoffset, true, focusLines))
	}
	if m.cursor < m.top {
		m.top = m.cursor
	}
	if m.cursor+rowHeight > m.top+vh {
		m.top = m.cursor + rowHeight - vh
	}
	if maxTop := len(m.doc.Rows) + rowHeight - 1 - vh; m.top > maxTop {
		m.top = maxTop
	}
	if m.top < 0 {
		m.top = 0
	}
}

func (m Model) View() string {
	switch m.mode {
	case modeHelp:
		return m.helpView()
	case modeFiles:
		return m.filesView()
	case modeSubmit:
		return m.submitView()
	case modeThreads, modeThread, modeReply:
		return m.followupView()
	case modeComment:
		return m.commentView()
	}
	return m.diffView()
}

// focusLines is how far the row under the cursor may expand: a comment shows
// its line breaks there, up to this many lines.
const focusLines = 8

func (m Model) diffView() string {
	vh := m.viewportHeight()
	var b strings.Builder
	written := 0
	for idx := m.top; idx < len(m.doc.Rows) && written < vh; idx++ {
		maxLines := 1
		if idx == m.cursor {
			maxLines = focusLines
		}
		lines := m.rend.RenderLines(m.doc.Rows[idx], m.width, m.hoffset, idx == m.cursor, maxLines)
		for _, line := range lines {
			if written >= vh {
				break
			}
			b.WriteString(line)
			b.WriteString("\n")
			written++
		}
	}
	for written < vh {
		b.WriteString("\n")
		written++
	}
	if m.mode == modeInput {
		b.WriteString(m.in.render(m.width, m.theme.NoteFg, m.theme.NoteBg))
		b.WriteString("\n")
	}
	b.WriteString(m.statusBar())
	return b.String()
}

func (m Model) statusBar() string {
	if len(m.doc.Rows) == 0 {
		left := " no changes" + m.syncStatus()
		if m.changesView {
			left = " no changes since your latest review" + m.syncStatus()
		}
		return bar(m.theme, m.width, left, fitHint(m.width, left, []string{
			"r sync  ? help  q quit",
			"r sync  ? help",
			"? help",
		}))
	}
	row := m.doc.Rows[m.cursor]
	name := ""
	if row.FileIdx < len(m.doc.Files) {
		name = m.doc.Files[row.FileIdx].Path()
	}

	left := " review"
	if row.FileIdx < len(m.doc.Files) {
		left = fmt.Sprintf(" [%d/%d] %s", row.FileIdx+1, len(m.doc.Files), name)
	}
	if m.changesView {
		left = " since review " + shortSHA(m.src.HeadSHA) + " ·" + left
	}
	if n := len(m.review.Notes); n > 0 {
		left += fmt.Sprintf("  ·  %d draft%s", n, plural(n))
	}
	if m.rangeAnchor > 0 {
		left += fmt.Sprintf("  ·  selection from L%d", m.rangeAnchor)
	}
	if m.layout.Mode != m.doc.Layout.Mode {
		left += "  ·  split → unified (narrow)"
	}
	switch {
	case m.err != "":
		left += "  ·  " + m.err
	case m.status != "":
		left += "  ·  " + m.status
	}
	left += m.syncStatus()

	return bar(m.theme, m.width, left, fitHint(m.width, left, m.hintKeys()))
}

// hintKeys is every screen's key hints in one place, most detailed first so
// fitHint can drop the least useful ones on a narrow terminal. The order is
// fixed per screen rather than per state, so the bar does not reshuffle as the
// review progresses, and every screen below the diff ends with the way back,
// so no view is a dead end.
func (m Model) hintKeys() []string {
	switch m.mode {
	case modeThreads:
		return []string{
			"enter open  x verify  a changes  D PR diff  r sync  esc back  ? help",
			"enter open  x verify  a changes  esc back  ? help",
			"enter open  x verify  esc back  ? help",
			"esc back  ? help",
		}
	case modeThread:
		return []string{
			"x verify  c reply  R resolve/reopen  n/p thread  esc back  ? help",
			"x verify  c reply  R resolve/reopen  esc back  ? help",
			"x verify  c reply  esc back  ? help",
			"esc back  ? help",
		}
	case modeFiles:
		return []string{
			"enter open  x reviewed  esc back  ? help",
			"enter open  x reviewed  esc back",
			"esc back",
		}
	case modeComment:
		return []string{
			"j/k scroll  esc back",
			"esc back",
		}
	}
	if m.src.CanSubmit() {
		return []string{
			"c comment  y copy  x reviewed  t threads  a changes  D PR diff  r sync  S submit  ? help  q quit",
			"c comment  x reviewed  t threads  r sync  S submit  ? help  q quit",
			"c comment  x reviewed  S submit  ? help",
			"c comment  S submit  ? help",
			"? help",
		}
	}
	return []string{
		"c comment  y copy  x reviewed  ? help  q quit",
		"c comment  x reviewed  ? help",
		"c comment  ? help",
		"? help",
	}
}

// fitHint picks the most detailed key hints that still leave a gap beside left.
func fitHint(width int, left string, hints []string) string {
	for _, h := range hints {
		if width-lipgloss.Width(left)-lipgloss.Width(h)-1 >= 2 {
			return h
		}
	}
	return ""
}

func (m Model) syncStatus() string {
	switch {
	case m.sync.syncing:
		return "  ·  syncing…"
	case m.sync.err != "":
		return "  ·  sync failed " + age(m.sync.failedAt) + ": " + m.sync.err
	case !m.sync.syncedAt.IsZero():
		return "  ·  synced " + age(m.sync.syncedAt)
	default:
		return ""
	}
}

// surface is the theme's own page. Every view paints it rather than leaving
// gaps to the terminal's colours, which would only match one theme.
func (m Model) surface() lipgloss.Style {
	return lipgloss.NewStyle().
		Background(lipgloss.Color(m.theme.Bg)).
		Foreground(lipgloss.Color(m.theme.Fg))
}

func (m Model) filesView() string {
	var b strings.Builder
	surface := m.surface()
	vh := m.viewportHeight()
	top := m.filesTop()
	for i := 0; i < vh; i++ {
		idx := top + i
		if idx >= len(m.doc.Files) {
			b.WriteString(surface.Render(pad("", m.width)) + "\n")
			continue
		}
		f := m.doc.Files[idx]
		reviewed, changed := m.review.ReviewState(f.Path(), m.blobs[f.Path()])
		mark := " "
		switch {
		case reviewed && changed:
			mark = "~"
		case reviewed:
			mark = "✓"
		}
		edge, st := " ", surface
		if idx == m.fileCursor {
			edge = render.FocusBar
			st = lipgloss.NewStyle().
				Background(lipgloss.Color(m.theme.CursorBg)).
				Foreground(lipgloss.Color(m.theme.LineNumFocusFg)).Bold(true)
		}
		line := fmt.Sprintf("%s%s %-8s %s  +%d −%d", edge, mark, statusLabel(f), f.Path(), f.Additions, f.Deletions)
		if n := m.notesFor(f.Path()); n > 0 {
			line += fmt.Sprintf("  %d draft%s", n, plural(n))
		}
		b.WriteString(st.Render(pad(line, m.width)))
		b.WriteString("\n")
	}
	left := fmt.Sprintf(" %d files", len(m.doc.Files))
	b.WriteString(bar(m.theme, m.width, left, fitHint(m.width, left, m.hintKeys())))
	return b.String()
}

// filesTop is the first file the file list shows: the list scrolls only as
// far as it takes to keep the selection on screen.
func (m Model) filesTop() int {
	return max(0, m.fileCursor-m.viewportHeight()+1)
}

func (m Model) notesFor(path string) int {
	n := 0
	for _, note := range m.review.Notes {
		if note.Path == path {
			n++
		}
	}
	return n
}

func statusLabel(f *diffparse.FileDiff) string {
	if f.IsBinary {
		return "binary"
	}
	return f.Status.String()
}

func bar(t render.Theme, width int, left, right string) string {
	st := lipgloss.NewStyle().Background(lipgloss.Color(t.FileBg)).Foreground(lipgloss.Color(t.FileFg))
	gap := width - lipgloss.Width(left) - lipgloss.Width(right) - 1
	if gap < 1 {
		return st.Render(pad(left, width))
	}
	return st.Render(left + strings.Repeat(" ", gap) + right + " ")
}

// padStyled fills a line that already carries styling out to width. The
// padding gets its own style rather than wrapping the line in one: an outer
// style would end at the line's first reset and leave the tail bare.
func padStyled(st lipgloss.Style, s string, width int) string {
	if w := lipgloss.Width(s); w < width {
		return s + st.Render(strings.Repeat(" ", width-w))
	}
	return s
}

func pad(s string, width int) string {
	if w := lipgloss.Width(s); w > width {
		return runewidth.Truncate(s, width, "…")
	} else if w < width {
		return s + strings.Repeat(" ", width-w)
	}
	return s
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
