package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tobiasbernting/code-review-cli/internal/config"
	"github.com/tobiasbernting/code-review-cli/internal/diffparse"
	"github.com/tobiasbernting/code-review-cli/internal/render"
)

const yankDiff = `diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1,4 +1,4 @@ func main()
 one
-two
+TWO
 three
 four
@@ -10,2 +10,3 @@
 ten
+eleven
 twelve
`

type fakeClipboard struct {
	copied []string
	err    error
}

func (f *fakeClipboard) Copy(text string) error {
	f.copied = append(f.copied, text)
	return f.err
}

func newYankModel(t *testing.T, mode render.Mode) (Model, *fakeClipboard) {
	t.Helper()
	clip := &fakeClipboard{}
	m := New(Options{
		Files:     diffparse.Parse(yankDiff),
		Theme:     render.DefaultTheme(),
		Layout:    render.Layout{Mode: mode},
		Config:    config.Defaults(),
		Source:    Source{Kind: SourceLocal, Title: "test"},
		Review:    newTestReview(t),
		Clipboard: clip,
	})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 30})
	return next.(Model), clip
}

// yank presses key and runs whatever it asked for, as the program would.
func yank(t *testing.T, m Model, key string) Model {
	t.Helper()
	next, cmd := m.Update(keyMsg(key))
	m = next.(Model)
	if cmd != nil {
		next, _ = m.Update(cmd())
		m = next.(Model)
	}
	return m
}

func (m Model) cursorOnRow(t *testing.T, kind render.RowKind, nth int) Model {
	t.Helper()
	for i, row := range m.doc.Rows {
		if row.Kind == kind {
			if nth == 0 {
				m.cursor = i
				return m
			}
			nth--
		}
	}
	t.Fatalf("no such row")
	return m
}

func (f *fakeClipboard) last(t *testing.T) string {
	t.Helper()
	if len(f.copied) == 0 {
		t.Fatal("nothing was copied")
	}
	return f.copied[len(f.copied)-1]
}

func TestYankCopiesTheLineAsPlainCode(t *testing.T) {
	m, clip := newYankModel(t, render.ModeUnified)
	yank(t, m.cursorOnLine(t, 2, 0), "y")
	if got := clip.last(t); got != "TWO" {
		t.Errorf("copied %q, want the added line's code without a marker", got)
	}
}

func TestYankOnADeletedLineCopiesTheOldText(t *testing.T) {
	m, clip := newYankModel(t, render.ModeUnified)
	yank(t, m.cursorOnLine(t, 0, 2), "y")
	if got := clip.last(t); got != "two" {
		t.Errorf("copied %q, want the deleted line's text", got)
	}
}

func TestYankSelectionCopiesTheNewSideAndClearsIt(t *testing.T) {
	m, clip := newYankModel(t, render.ModeUnified)
	m = press(t, m.cursorOnLine(t, 1, 0), "v")
	m = yank(t, m.cursorOnLine(t, 3, 0), "y")
	if got := clip.last(t); got != "one\nTWO\nthree" {
		t.Errorf("copied %q, want lines 1-3 as they read after the change", got)
	}
	if m.rangeAnchor != 0 {
		t.Error("the selection survived the yank")
	}
	if !strings.Contains(m.statusBar(), "copied 3 lines") {
		t.Errorf("status bar does not report the copy:\n%s", m.statusBar())
	}
}

func TestYankOnAHunkHeaderCopiesTheHunk(t *testing.T) {
	m, clip := newYankModel(t, render.ModeUnified)
	yank(t, m.cursorOnRow(t, render.RowHunk, 1), "y")
	if got := clip.last(t); got != "ten\neleven\ntwelve" {
		t.Errorf("copied %q, want the second hunk's new side", got)
	}
}

func TestYankOnAFileHeaderCopiesThePath(t *testing.T) {
	m, clip := newYankModel(t, render.ModeUnified)
	yank(t, m.cursorOnRow(t, render.RowFile, 0), "y")
	if got := clip.last(t); got != "a.go" {
		t.Errorf("copied %q, want the path", got)
	}
}

func TestYankInSplitCopiesTheNewSide(t *testing.T) {
	m, clip := newYankModel(t, render.ModeSplit)
	yank(t, m.cursorOnLine(t, 2, 2), "y")
	if got := clip.last(t); got != "TWO" {
		t.Errorf("copied %q from a paired row, want the new side", got)
	}
}

func TestReferenceNamesTheLines(t *testing.T) {
	m, clip := newYankModel(t, render.ModeUnified)

	yank(t, m.cursorOnLine(t, 2, 0), "Y")
	if got := clip.last(t); got != "a.go:L2" {
		t.Errorf("reference to one line: %q", got)
	}

	sel := press(t, m.cursorOnLine(t, 3, 0), "v")
	yank(t, sel.cursorOnLine(t, 1, 0), "Y")
	if got := clip.last(t); got != "a.go:L1-L3" {
		t.Errorf("reference to a selection made upwards: %q", got)
	}

	yank(t, m.cursorOnRow(t, render.RowHunk, 1), "Y")
	if got := clip.last(t); got != "a.go:L10-L12" {
		t.Errorf("reference to a hunk: %q", got)
	}
}

func TestYankFailureIsReported(t *testing.T) {
	m, clip := newYankModel(t, render.ModeUnified)
	clip.err = errors.New("no terminal")
	m = yank(t, m.cursorOnLine(t, 2, 0), "y")
	if !strings.Contains(m.statusBar(), "no terminal") {
		t.Errorf("a failed copy was not reported:\n%s", m.statusBar())
	}
}
