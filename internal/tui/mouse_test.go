package tui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tobiasbernting/code-review-cli/internal/config"
	"github.com/tobiasbernting/code-review-cli/internal/diffparse"
	"github.com/tobiasbernting/code-review-cli/internal/render"
)

// longDiff is one file with a single hunk of forty added lines, far taller
// than the test viewport, followed by a second small file.
func longDiff() string {
	var b strings.Builder
	b.WriteString("diff --git a/long.go b/long.go\n--- a/long.go\n+++ b/long.go\n@@ -0,0 +1,40 @@\n")
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&b, "+line %d\n", i)
	}
	b.WriteString("diff --git a/short.go b/short.go\n--- a/short.go\n+++ b/short.go\n@@ -1,2 +1,2 @@\n-old\n+new\n keep\n")
	return b.String()
}

func newMouseModel(t *testing.T) Model {
	t.Helper()
	m := New(Options{
		Files:  diffparse.Parse(longDiff()),
		Theme:  render.DefaultTheme(),
		Config: config.Defaults(),
		Source: Source{Kind: SourceLocal, Title: "test"},
		Review: newTestReview(t),
	})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 12})
	return next.(Model)
}

func mouse(t *testing.T, m Model, msgs ...tea.MouseMsg) Model {
	t.Helper()
	for _, msg := range msgs {
		next, _ := m.Update(msg)
		m = next.(Model)
	}
	return m
}

func wheel(button tea.MouseButton) tea.MouseMsg {
	return tea.MouseMsg{Action: tea.MouseActionPress, Button: button}
}

func click(y int) tea.MouseMsg {
	return tea.MouseMsg{Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
}

func drag(y int) tea.MouseMsg {
	return tea.MouseMsg{Y: y, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft}
}

func release(y int) tea.MouseMsg {
	return tea.MouseMsg{Y: y, Action: tea.MouseActionRelease, Button: tea.MouseButtonNone}
}

// newLine is the new-side line number under the cursor.
func (m Model) newLine() int { return m.doc.Rows[m.cursor].NewNum() }

func TestWheelScrollsTheViewAndLeavesTheCursor(t *testing.T) {
	m := newMouseModel(t)
	m = press(t, m, "j", "j", "j", "j") // well inside the viewport
	cursor := m.cursor

	m = mouse(t, m, wheel(tea.MouseButtonWheelDown))
	if m.top != 3 {
		t.Errorf("after one wheel notch top = %d, want 3", m.top)
	}
	if m.cursor != cursor {
		t.Errorf("the cursor moved from %d to %d though it was still visible", cursor, m.cursor)
	}

	m = mouse(t, m, wheel(tea.MouseButtonWheelUp))
	if m.top != 0 || m.cursor != cursor {
		t.Errorf("wheel up: top %d cursor %d, want 0 and %d", m.top, m.cursor, cursor)
	}
}

func TestWheelCarriesTheCursorOffTheTopEdge(t *testing.T) {
	m := newMouseModel(t) // cursor on the first row
	m = mouse(t, m, wheel(tea.MouseButtonWheelDown), wheel(tea.MouseButtonWheelDown))
	if m.cursor < m.top {
		t.Fatalf("cursor %d is above the viewport starting at %d", m.cursor, m.top)
	}
	if m.cursor != m.top {
		t.Errorf("cursor %d, want it carried to the top row %d", m.cursor, m.top)
	}
}

func TestWheelStopsAtTheEnd(t *testing.T) {
	m := newMouseModel(t)
	for range 50 {
		m = mouse(t, m, wheel(tea.MouseButtonWheelDown))
	}
	if want := len(m.doc.Rows) - m.viewportHeight(); m.top != want {
		t.Errorf("top = %d after scrolling far past the end, want %d", m.top, want)
	}
}

func TestSidewaysWheelScrollsHorizontally(t *testing.T) {
	m := newMouseModel(t)
	m = mouse(t, m, wheel(tea.MouseButtonWheelRight))
	if m.hoffset != 8 {
		t.Errorf("wheel right: hoffset %d, want 8", m.hoffset)
	}
	shifted := wheel(tea.MouseButtonWheelDown)
	shifted.Shift = true
	m = mouse(t, m, shifted)
	if m.hoffset != 16 || m.top != 0 {
		t.Errorf("shift+wheel down: hoffset %d top %d, want 16 and 0", m.hoffset, m.top)
	}
	m = mouse(t, m, wheel(tea.MouseButtonWheelLeft), wheel(tea.MouseButtonWheelLeft), wheel(tea.MouseButtonWheelLeft))
	if m.hoffset != 0 {
		t.Errorf("wheel left past the margin: hoffset %d, want 0", m.hoffset)
	}
}

// screenLine is what the terminal shows on line y of the diff view.
func (m Model) screenLine(t *testing.T, y int) string {
	t.Helper()
	lines := strings.Split(m.View(), "\n")
	if y >= len(lines) {
		t.Fatalf("the view has %d lines, no line %d", len(lines), y)
	}
	return lines[y]
}

var shownLineRe = regexp.MustCompile(`line (\d+)\s*$`)

// shownLine reads which line of long.go a screen line displays.
func shownLine(t *testing.T, screen string) int {
	t.Helper()
	match := shownLineRe.FindStringSubmatch(screen)
	if match == nil {
		t.Fatalf("screen line %q shows no code line", screen)
	}
	n, _ := strconv.Atoi(match[1])
	return n
}

func TestClickPutsTheCursorOnTheClickedLine(t *testing.T) {
	m := newMouseModel(t)
	m = mouse(t, m, wheel(tea.MouseButtonWheelDown)) // clicks must account for scroll
	shown := m.screenLine(t, 5)

	m = mouse(t, m, click(5), release(5))
	if got := shownLine(t, shown); m.newLine() != got {
		t.Errorf("clicked screen line %q, but the cursor is on line %d", strings.TrimSpace(shown), m.newLine())
	}
	if m.top != 3 {
		t.Errorf("a click scrolled the view: top %d, want 3", m.top)
	}
}

func TestClickOnTheStatusBarDoesNothing(t *testing.T) {
	m := newMouseModel(t)
	cursor := m.cursor
	m = mouse(t, m, click(m.viewportHeight()), release(m.viewportHeight()))
	if m.cursor != cursor {
		t.Errorf("clicking the status bar moved the cursor from %d to %d", cursor, m.cursor)
	}
}

// withDraft adds a draft on a line of long.go and returns the model with the
// screen line its row is drawn on.
func withDraft(t *testing.T, m Model, line int) (Model, int) {
	t.Helper()
	m.review.Add("long.go", line, line, m.blobs["long.go"], "a draft")
	m.rebuild()
	for i, row := range m.doc.Rows {
		if row.Kind == render.RowNote && row.Ann != nil && row.Ann.Kind == render.AnnNote {
			if i-m.top <= m.cursor-m.top {
				t.Fatal("the draft row is above the cursor; screen maths below assumes otherwise")
			}
			return m, i - m.top
		}
	}
	t.Fatal("the draft was not drawn")
	return m, 0
}

// ticking is a clock that advances by step every time it is read.
func ticking(step time.Duration) func() time.Time {
	now := time.Unix(0, 0)
	return func() time.Time {
		now = now.Add(step)
		return now
	}
}

func TestDoubleClickOpensLikeEnter(t *testing.T) {
	m, y := withDraft(t, newMouseModel(t), 3)
	m.now = ticking(100 * time.Millisecond)
	m = mouse(t, m, click(y), release(y), click(y), release(y))
	if m.mode != modeComment {
		t.Errorf("double click on a draft left mode %v, want the comment view", m.mode)
	}
}

func TestSlowClicksAreNotADoubleClick(t *testing.T) {
	m, y := withDraft(t, newMouseModel(t), 3)
	m.now = ticking(time.Second)
	m = mouse(t, m, click(y), release(y), click(y), release(y))
	if m.mode != modeDiff {
		t.Errorf("two slow clicks opened mode %v, want to stay in the diff", m.mode)
	}
}

func TestDragSelectsLinesForADraft(t *testing.T) {
	m := newMouseModel(t)
	from, to := shownLine(t, m.screenLine(t, 4)), shownLine(t, m.screenLine(t, 7))
	m = mouse(t, m, click(4), drag(5), drag(7), release(7))
	if m.rangeAnchor != from {
		t.Errorf("selection starts at L%d, want L%d where the drag began", m.rangeAnchor, from)
	}
	if m.newLine() != to {
		t.Errorf("cursor on L%d, want L%d where the drag ended", m.newLine(), to)
	}
	m = press(t, m, "c")
	if m.pending.startLine != from || m.pending.line != to {
		t.Errorf("c after a drag drafts L%d-%d, want L%d-%d", m.pending.startLine, m.pending.line, from, to)
	}
}

func TestPlainClickClearsTheSelection(t *testing.T) {
	m := newMouseModel(t)
	m = mouse(t, m, click(4), drag(7), release(7))
	m = mouse(t, m, click(5), release(5))
	if m.rangeAnchor != 0 {
		t.Errorf("a plain click kept the selection from L%d", m.rangeAnchor)
	}
}

func TestDragStopsAtTheEndOfTheHunk(t *testing.T) {
	m := newMouseModel(t)
	m = press(t, m, "G") // short.go, below long.go's only hunk
	bottom := m.cursor
	for m.top > 0 {
		m = mouse(t, m, wheel(tea.MouseButtonWheelUp))
	}
	for m.top+m.viewportHeight() <= bottom {
		m = mouse(t, m, wheel(tea.MouseButtonWheelDown))
	}
	// Find a long.go line on screen and drag from it to the last screen line,
	// which is in short.go.
	y := 0
	for ; y < m.viewportHeight(); y++ {
		if shownLineRe.MatchString(m.screenLine(t, y)) {
			break
		}
	}
	m = mouse(t, m, click(y), drag(m.viewportHeight()-1))
	if got := m.files[m.fileIdx()].Path(); got != "long.go" {
		t.Errorf("dragging into the next file moved the cursor into %s", got)
	}
	if m.newLine() != 40 {
		t.Errorf("cursor on L%d, want it held at L40, the hunk's last line", m.newLine())
	}
}

func TestDragPastTheBottomEdgeScrolls(t *testing.T) {
	m := newMouseModel(t)
	edge := m.viewportHeight() - 1
	m = mouse(t, m, click(4), drag(edge), drag(edge+1), drag(edge+1))
	if m.top != 3 {
		t.Errorf("three drag moves on or past the bottom edge: top %d, want 3", m.top)
	}
	if m.cursor < m.top || m.cursor >= m.top+m.viewportHeight() {
		t.Errorf("cursor %d fell outside the viewport starting at %d", m.cursor, m.top)
	}
}

func TestFileListClickSelectsAndDoubleClickOpens(t *testing.T) {
	m := press(t, newMouseModel(t), "f")
	m.now = ticking(100 * time.Millisecond)
	m = mouse(t, m, click(1), release(1))
	if m.fileCursor != 1 || m.mode != modeFiles {
		t.Fatalf("click on the second file: cursor %d mode %v, want 1 in the file list", m.fileCursor, m.mode)
	}
	m = mouse(t, m, click(1), release(1))
	if m.mode != modeDiff || m.files[m.fileIdx()].Path() != "short.go" {
		t.Errorf("double click on short.go: mode %v, file %s", m.mode, m.files[m.fileIdx()].Path())
	}
}

func TestWheelMovesListSelection(t *testing.T) {
	m := press(t, newMouseModel(t), "f")
	m = mouse(t, m, wheel(tea.MouseButtonWheelDown))
	if m.fileCursor != 1 {
		t.Errorf("wheel down in the file list: cursor %d, want 1", m.fileCursor)
	}
}

func TestThreadListDoubleClickOpensTheThread(t *testing.T) {
	m := followupModel(t)
	m.now = ticking(100 * time.Millisecond)
	m = mouse(t, m, click(2), release(2), click(2), release(2))
	if m.mode != modeThread {
		t.Errorf("double click on the first thread: mode %v, want the thread view", m.mode)
	}
}

func TestMouseIgnoredWhileGitHubIsBusy(t *testing.T) {
	m := newMouseModel(t)
	m.follow.busy = true
	m = mouse(t, m, wheel(tea.MouseButtonWheelDown), click(5))
	if m.top != 0 || m.cursor != m.nextSelectable(0, 1) {
		t.Errorf("mouse acted while busy: top %d cursor %d", m.top, m.cursor)
	}
}

func TestMouseIgnoredWhileTyping(t *testing.T) {
	m := press(t, newMouseModel(t), "j", "j", "c")
	cursor := m.cursor
	m = mouse(t, m, click(6), wheel(tea.MouseButtonWheelDown))
	if m.mode != modeInput || m.cursor != cursor || m.top != 0 {
		t.Errorf("mouse acted while a draft was being typed: mode %v cursor %d top %d", m.mode, m.cursor, m.top)
	}
}

func TestQueueClickAndDoubleClick(t *testing.T) {
	q := newQueue(t)
	q.now = ticking(100 * time.Millisecond)
	next, _ := q.Update(click(2))
	q = next.(QueueModel)
	if q.cursor != 1 {
		t.Fatalf("click on the second pull request: cursor %d, want 1", q.cursor)
	}
	next, cmd := q.Update(click(2))
	q = next.(QueueModel)
	if cmd == nil || q.Selected != (Selection{Repo: "acme/y", Number: 4, Chosen: true}) {
		t.Errorf("double click did not open acme/y#4: %+v", q.Selected)
	}
}

// SGR mouse reporting, which cell motion uses, names the button on release.
func TestReleaseEndsTheDragWhateverButtonItNames(t *testing.T) {
	m := newMouseModel(t)
	sgrRelease := tea.MouseMsg{Y: 4, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft}
	m = mouse(t, m, click(4), sgrRelease, drag(7))
	if m.rangeAnchor != 0 {
		t.Errorf("motion after the release still selected from L%d", m.rangeAnchor)
	}
}

func TestClickBelowTheThreadListSelectsNothing(t *testing.T) {
	m := followupModel(t)
	second := m.follow.threads[0]
	second.ID = "second"
	m.follow.threads = append(m.follow.threads, second)
	m.height = 5 // two list lines: just the first thread
	m = mouse(t, m, click(m.height-1))
	if m.follow.cursor != 0 {
		t.Errorf("a click on the status bar selected thread %d, which is off screen", m.follow.cursor)
	}
}
