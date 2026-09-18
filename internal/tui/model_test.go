package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tobiasbernting/code-review-cli/internal/config"
	"github.com/tobiasbernting/code-review-cli/internal/diffparse"
	"github.com/tobiasbernting/code-review-cli/internal/notes"
	"github.com/tobiasbernting/code-review-cli/internal/render"
)

// newTestReview is an in-memory review; Save is a no-op because the path is
// inside the test's temporary directory.
func newTestReview(t *testing.T) *notes.Review {
	t.Helper()
	r, err := notes.LoadAt(filepath.Join(t.TempDir(), "review.json"), "test-scope")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// two files, the first with two hunks, so file jumps and hunk jumps are
// distinguishable.
const navDiff = `diff --git a/one.go b/one.go
--- a/one.go
+++ b/one.go
@@ -1,2 +1,2 @@
-a
+b
@@ -20,2 +20,2 @@
-c
+d
diff --git a/two.go b/two.go
--- a/two.go
+++ b/two.go
@@ -1,2 +1,2 @@
-e
+f
`

func newTestModel(t *testing.T) Model {
	t.Helper()
	m := New(Options{
		Files:  diffparse.Parse(navDiff),
		Theme:  render.DefaultTheme(),
		Config: config.Defaults(),
		Source: Source{Kind: SourceLocal, Title: "test"},
		Review: newTestReview(t),
	})
	// Deliberately shorter than the document, so scroll behaviour is exercised
	// rather than clamped away.
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 6})
	return next.(Model)
}

func press(t *testing.T, m Model, keys ...string) Model {
	t.Helper()
	for _, k := range keys {
		next, _ := m.Update(keyMsg(k))
		m = next.(Model)
	}
	return m
}

// keyMsg spells a key the way bubbletea delivers it, so tests exercise the
// same strings handleKey switches on.
func keyMsg(k string) tea.KeyMsg {
	switch k {
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
}

func (m Model) fileIdx() int { return m.doc.Rows[m.cursor].FileIdx }

func TestFileJumpKeysAreEquivalent(t *testing.T) {
	for _, key := range []string{"tab", "J", "]"} {
		t.Run(key, func(t *testing.T) {
			m := press(t, newTestModel(t), key)
			if got := m.fileIdx(); got != 1 {
				t.Errorf("%q landed on file %d, want 1", key, got)
			}
			if m.doc.Rows[m.cursor].Kind != render.RowFile {
				t.Errorf("%q did not land on a file header: %+v", key, m.doc.Rows[m.cursor])
			}
			// The target must be scrolled to the top, not left at the bottom edge.
			if m.top != m.cursor {
				t.Errorf("%q left the header at viewport row %d", key, m.cursor-m.top)
			}
		})
	}
}

// The bug that prompted this work: n steps hunk by hunk, so it takes three
// presses to leave a two-hunk file, while J leaves it in one.
func TestHunkJumpStepsThroughHunksNotFiles(t *testing.T) {
	m := newTestModel(t) // cursor starts on file 0's header

	m = press(t, m, "n")
	if got := m.doc.Rows[m.cursor]; got.FileIdx != 0 || got.HunkIdx != 0 {
		t.Fatalf("first n → file %d hunk %d, want file 0 hunk 0", got.FileIdx, got.HunkIdx)
	}
	m = press(t, m, "n")
	if got := m.doc.Rows[m.cursor]; got.FileIdx != 0 || got.HunkIdx != 1 {
		t.Fatalf("second n → file %d hunk %d, want file 0 hunk 1", got.FileIdx, got.HunkIdx)
	}
	m = press(t, m, "n")
	if got := m.fileIdx(); got != 1 {
		t.Errorf("third n → file %d, want 1", got)
	}

	// One tab does what those three did.
	if got := press(t, newTestModel(t), "tab").fileIdx(); got != 1 {
		t.Errorf("tab → file %d, want 1", got)
	}
}

func TestJumpAtEdgesReportsRatherThanWraps(t *testing.T) {
	m := press(t, newTestModel(t), "shift+tab")
	if m.fileIdx() != 0 {
		t.Errorf("shift+tab wrapped to file %d from the first file", m.fileIdx())
	}
	if m.status != "first file" {
		t.Errorf("status = %q, want %q", m.status, "first file")
	}

	m = press(t, newTestModel(t), "tab", "tab")
	if m.fileIdx() != 1 {
		t.Errorf("tab wrapped past the last file to %d", m.fileIdx())
	}
	if m.status != "last file" {
		t.Errorf("status = %q, want %q", m.status, "last file")
	}
}

func TestCursorNeverRestsOnSpacer(t *testing.T) {
	m := newTestModel(t)
	for i := 0; i < len(m.doc.Rows)+2; i++ {
		if m.doc.Rows[m.cursor].Kind == render.RowSpacer {
			t.Fatalf("cursor landed on a spacer at row %d", m.cursor)
		}
		m = press(t, m, "j")
	}
}

// The note and reviewed keys are the two a reviewer reaches for most, so they
// stay in the status bar on a pull request even though PR navigation adds
// hints, and narrow terminals drop the least useful ones first.
func TestStatusBarKeepsCoreHints(t *testing.T) {
	m := newTestModel(t)
	m.src = Source{Kind: SourcePR, Repo: "o/r", Title: "test"}

	for _, width := range []int{200, 120, 100, 80, 60, 40} {
		m.width = width
		bar := m.statusBar()
		if width >= 80 && !strings.Contains(bar, "c comment") {
			t.Errorf("width %d: status bar lost the comment hint: %q", width, bar)
		}
		if !strings.Contains(bar, "? help") {
			t.Errorf("width %d: status bar lost the help hint: %q", width, bar)
		}
	}
}

// No screen below the diff may be a dead end: whatever the terminal width, the
// hints have to keep saying how to get back.
func TestHintsAlwaysOfferAWayBack(t *testing.T) {
	m := newTestModel(t)
	m.src = Source{Kind: SourcePR, Repo: "o/r", Title: "test"}

	for _, mode := range []mode{modeThreads, modeThread, modeFiles, modeComment} {
		m.mode = mode
		for _, width := range []int{200, 100, 60, 30} {
			m.width = width
			hints := m.hintKeys()
			for _, h := range hints {
				if !strings.Contains(h, "esc back") {
					t.Errorf("mode %d: hint variant omits the way back: %q", mode, h)
				}
			}
			if got := fitHint(width, " left", hints); got == "" {
				t.Errorf("mode %d width %d: no hint variant fits", mode, width)
			}
		}
	}
}

// esc used to reassign the mode the thread list was already in, stranding the
// reviewer with no way back to the diff.
func TestEscLeavesTheThreadList(t *testing.T) {
	m := newTestModel(t)
	m.src = Source{Kind: SourcePR, Repo: "o/r", Title: "test"}
	m.mode = modeThreads

	m = press(t, m, "esc")
	if m.mode != modeDiff {
		t.Fatalf("esc in the thread list left mode %d, want the diff", m.mode)
	}
}
