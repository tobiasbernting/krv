package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/tobiasbernting/krv/v2/internal/ghsrc"
	"github.com/tobiasbernting/krv/v2/internal/render"
)

// onComment puts the cursor on the first remote comment of a review holding
// one thread with this body.
func onComment(t *testing.T, body string, edit ...func(*ghsrc.Thread)) Model {
	t.Helper()
	thread := testThread("T1", 1, body)
	for _, f := range edit {
		f(&thread)
	}
	m := newReviewModel(t, func(o *Options) { o.Threads = []ghsrc.Thread{thread} })
	for i, row := range m.doc.Rows {
		if row.Ann != nil && row.Ann.Kind == render.AnnComment {
			m.cursor = i
			return m
		}
	}
	t.Fatal("the thread's comment is not in the document")
	return m
}

// expansion is the comment under the cursor as the diff draws it.
func expansion(m Model, maxLines int) string {
	return ansi.Strip(strings.Join(m.rend.RenderLines(m.doc.Rows[m.cursor], 80, 0, true, maxLines), "\n"))
}

func TestFocusedLongCommentExpandsAndOpens(t *testing.T) {
	thread := testThread("T1", 1, strings.Repeat("long comment content ", 80))
	m := newReviewModel(t, func(o *Options) { o.Threads = []ghsrc.Thread{thread} })
	for i, row := range m.doc.Rows {
		if row.Ann != nil && row.Ann.Kind == render.AnnComment {
			m.cursor = i
			break
		}
	}
	lines := m.rend.RenderLines(m.doc.Rows[m.cursor], 60, 0, true, 8)
	if len(lines) != 8 {
		t.Fatalf("focused comment rendered %d lines, want 8", len(lines))
	}
	if !strings.Contains(lines[7], "…") {
		t.Error("truncated expansion has no overflow marker")
	}
	m = press(t, m, "enter")
	if m.mode != modeComment {
		t.Fatalf("enter left mode %v, want comment detail", m.mode)
	}
}

func TestCommentReaderRendersMarkdownWithALinkCursor(t *testing.T) {
	started := fakeStart(t)
	m := onComment(t, "**Guard** the nil case.\n\n- see [the docs](https://example.test/docs)\n")
	m = press(t, m, "enter")
	if m.mode != modeComment {
		t.Fatalf("enter left mode %v, want the comment reader", m.mode)
	}
	view := ansi.Strip(m.View())
	if strings.Contains(view, "**") || !strings.Contains(view, "Guard the nil case.") {
		t.Errorf("the reader shows raw Markdown:\n%s", view)
	}
	if !strings.Contains(view, "the docs") {
		t.Errorf("the reader dropped a link's text:\n%s", view)
	}

	m = press(t, m, "tab")
	if m.reader.focus != 0 {
		t.Fatalf("tab focused link %d, want the comment's only link", m.reader.focus)
	}
	m = yank(t, m, "o")
	if len(*started) != 1 || !strings.Contains(strings.Join((*started)[0], " "), "https://example.test/docs") {
		t.Fatalf("o did not open the focused link: %q", *started)
	}
	if m = press(t, m, "q"); m.mode != modeDiff {
		t.Errorf("q left the reader open, in mode %v", m.mode)
	}
}

func TestCommentExpansionIsMarkdownCappedAtEightLines(t *testing.T) {
	m := onComment(t, "**Guard** it.\n\n"+strings.Repeat("A paragraph of prose. ", 40))
	lines := m.rend.RenderLines(m.doc.Rows[m.cursor], 80, 0, true, 8)
	if len(lines) != 8 {
		t.Fatalf("the expansion is %d lines, want the eight-line cap", len(lines))
	}
	if !strings.Contains(lines[7], "…") {
		t.Error("the capped expansion has no overflow marker")
	}
	if text := expansion(m, 8); strings.Contains(text, "**") || !strings.Contains(text, "Guard it.") {
		t.Errorf("the expansion shows raw Markdown:\n%s", text)
	}
}

func TestCollapsedCommentRowIsOneStrippedLine(t *testing.T) {
	m := onComment(t, "**Guard** the nil case:\n\n```go\nif x == nil {\n```\n")
	line := expansion(m, 1)
	if strings.Count(line, "\n") != 0 {
		t.Fatalf("the collapsed row is more than one line:\n%s", line)
	}
	if strings.Contains(line, "**") || strings.Contains(line, "```") {
		t.Errorf("the collapsed row shows Markdown punctuation: %q", line)
	}
	if !strings.Contains(line, "Guard the nil case:") {
		t.Errorf("the collapsed row lost the comment's words: %q", line)
	}
}

func TestSuggestionRendersAsAMiniDiff(t *testing.T) {
	body := "Guard it:\n\n```suggestion\nfunc Run() (err error) {\n```\n"
	m := press(t, onComment(t, body), "enter")
	view := ansi.Strip(m.View())
	// svc.go line 3 is `func Run() error {`, which the suggestion replaces.
	if !strings.Contains(view, "− func Run() error {") || !strings.Contains(view, "+ func Run() (err error) {") {
		t.Errorf("the suggestion is not drawn as the change it proposes:\n%s", view)
	}
	if strings.Contains(view, "suggestion") {
		t.Errorf("a mini-diff still carries the fence's label:\n%s", view)
	}
}

func TestSuggestionFallsBackWhenTheAnchorIsUnknown(t *testing.T) {
	body := "Guard it:\n\n```suggestion\nfunc Run() (err error) {\n```\n"
	m := onComment(t, body, func(thread *ghsrc.Thread) { thread.Outdated = true })
	m = press(t, m, "enter")
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "suggestion") {
		t.Errorf("an outdated thread's suggestion is not labelled as one:\n%s", view)
	}
	if strings.Contains(view, "− func Run() error {") {
		t.Errorf("an outdated thread's suggestion claims to know what it replaces:\n%s", view)
	}
	if !strings.Contains(view, "func Run() (err error) {") {
		t.Errorf("the fallback dropped the proposed code:\n%s", view)
	}
}
