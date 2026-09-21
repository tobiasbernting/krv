package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/tobiasbernting/krv/v2/internal/diffparse"
	"github.com/tobiasbernting/krv/v2/internal/followup"
	"github.com/tobiasbernting/krv/v2/internal/ghsrc"
)

// overviewPR is a pull request with something to read in every section of
// the Overview.
func overviewPR() *ghsrc.PR {
	started := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	pr := &ghsrc.PR{
		Number: 42, Title: "Guard the nil case", State: "OPEN",
		BaseRef: "main", HeadRef: "guard", HeadSHA: "head",
		Body: "Fixes the panic in `Run`.\n\nSee [the issue](https://example.test/issue/7).",
		Checks: []ghsrc.Check{
			{Name: "test", Workflow: "ci", Status: ghsrc.CheckFail, Required: true,
				StartedAt: started, CompletedAt: started.Add(92 * time.Second),
				URL: "https://example.test/checks/test"},
			{Name: "lint", Status: ghsrc.CheckRunning, StartedAt: started,
				URL: "https://example.test/checks/lint"},
			{Name: "build", Workflow: "ci", Status: ghsrc.CheckPass,
				StartedAt: started, CompletedAt: started.Add(30 * time.Second)},
		},
		RequiredKnown: true,
	}
	pr.Author.Login = "teammate"
	return pr
}

// overviewModel is a pull request review with that pull request, synced just
// after its checks ran.
func overviewModel(t *testing.T, edit ...func(*ghsrc.PR)) Model {
	t.Helper()
	pr := overviewPR()
	for _, f := range edit {
		f(pr)
	}
	session := &followup.Session{PR: pr, Files: diffparse.Parse(noteDiff)}
	m := newReviewModel(t, func(o *Options) {
		o.Source = Source{Kind: SourcePR, Repo: "acme/x", PRNumber: 42, HeadSHA: "head", FollowUp: session}
		o.SyncedAt = time.Date(2026, 9, 19, 10, 2, 0, 0, time.UTC)
	})
	m.sync.syncedAt = time.Now().Add(-3 * time.Minute)
	return m
}

func overview(t *testing.T, m Model) (Model, string) {
	t.Helper()
	m = press(t, m, "i")
	if m.mode != modeOverview {
		t.Fatalf("i left mode %v, want the overview", m.mode)
	}
	return m, ansi.Strip(m.View())
}

func TestOverviewShowsHeaderChecksAndDescription(t *testing.T) {
	m, view := overview(t, overviewModel(t))
	for _, want := range []string{
		"Guard the nil case", "#42", "teammate", "main ← guard", "open", "synced 3m ago",
		"Checks", "1 failing, 1 running, 1 passed · 1 required failing",
		"test (ci)", "1m 32s", "required", "lint", "build (ci)", "30s",
		"Description", "Fixes the panic in", "the issue",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the overview does not say %q:\n%s", want, view)
		}
	}
	// Header, then Checks, then Description, in that order and as sections.
	page := m.overviewPage()
	if len(page.sections) != 3 {
		t.Fatalf("the overview has %d sections, want 3", len(page.sections))
	}
	text := strings.Join(page.lines, "\n")
	if ansi.Strip(text) == "" || strings.Index(text, "Checks") > strings.Index(text, "Description") {
		t.Errorf("sections are out of order:\n%s", ansi.Strip(text))
	}

	m = press(t, m, "esc")
	if m.mode != modeDiff {
		t.Errorf("esc left the overview open, in mode %v", m.mode)
	}
}

func TestOverviewGlyphsSayTheOutcomeWithoutColour(t *testing.T) {
	_, view := overview(t, overviewModel(t))
	for _, want := range []string{"✗ test", "● lint", "✓ build"} {
		if !strings.Contains(view, want) {
			t.Errorf("checks do not read without colour, missing %q:\n%s", want, view)
		}
	}
}

func TestOverviewSaysWhenRequiredStatusIsUnknown(t *testing.T) {
	_, view := overview(t, overviewModel(t, func(pr *ghsrc.PR) {
		pr.RequiredKnown = false
		pr.Checks[0].Required = false
	}))
	if !strings.Contains(view, "required status unavailable") {
		t.Errorf("the checks heading does not say required status is unknown:\n%s", view)
	}
	if strings.Contains(view, "  required") {
		t.Errorf("a check claims to be required although the host could not say:\n%s", view)
	}
}

func TestOverviewEmptyStates(t *testing.T) {
	_, view := overview(t, overviewModel(t, func(pr *ghsrc.PR) {
		pr.Body, pr.Checks = "", nil
	}))
	for _, want := range []string{"No checks.", "No description."} {
		if !strings.Contains(view, want) {
			t.Errorf("missing the empty state %q:\n%s", want, view)
		}
	}
}

func TestOverviewIsNotOfferedForALocalReview(t *testing.T) {
	m := press(t, newReviewModel(t), "i")
	if m.mode != modeDiff {
		t.Fatalf("i opened the overview for a local review, mode %v", m.mode)
	}
	if m.status != "no pull request" {
		t.Errorf("status = %q, want it to say there is no pull request", m.status)
	}
	if strings.Contains(m.plainView(), "i overview") {
		t.Error("a local review hints at the overview")
	}
	if hints := openHelp(t, newReviewModel(t), 100, 200).plainView(); strings.Contains(hints, "overview: description") {
		t.Error("a local review's help lists the overview")
	}
}

func TestStatusLineHintsTheOverviewWhenThereIsADescription(t *testing.T) {
	m := overviewModel(t)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 20})
	if view := ansi.Strip(next.(Model).View()); !strings.Contains(view, "i overview") {
		t.Errorf("the status line does not hint at the overview:\n%s", view)
	}
	bare := overviewModel(t, func(pr *ghsrc.PR) { pr.Body = "" })
	next, _ = bare.Update(tea.WindowSizeMsg{Width: 160, Height: 20})
	if view := ansi.Strip(next.(Model).View()); strings.Contains(view, "i overview") {
		t.Errorf("a pull request with no description still hints at the overview:\n%s", view)
	}
}

func TestOverviewLinkCursorCyclesAndOpens(t *testing.T) {
	started := fakeStart(t)
	m, _ := overview(t, overviewModel(t))
	page := m.overviewPage()
	if len(page.links) != 3 {
		t.Fatalf("the overview has %d links, want a check, a check and the description's", len(page.links))
	}

	m = press(t, m, "tab")
	if m.reader.focus != 0 {
		t.Fatalf("tab focused link %d, want the first", m.reader.focus)
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "›✗ test") {
		t.Errorf("the focused check is not marked:\n%s", view)
	}
	m = press(t, m, "tab", "shift+tab")
	if m.reader.focus != 0 {
		t.Errorf("tab then shift+tab left focus on link %d", m.reader.focus)
	}

	m = yank(t, m, "o")
	if len(*started) != 1 || !strings.Contains(strings.Join((*started)[0], " "), "https://example.test/checks/test") {
		t.Fatalf("o did not open the focused check: %q", *started)
	}
	if m.mode != modeOverview {
		t.Errorf("opening a link left the overview, mode %v", m.mode)
	}

	// enter opens the focused link too; with none it closes the page.
	m = yank(t, m, "enter")
	if len(*started) != 2 {
		t.Errorf("enter on a focused link opened %d links", len(*started))
	}
	m.reader.focus = -1
	if m = press(t, m, "enter"); m.mode != modeDiff {
		t.Errorf("enter with no link focused left mode %v", m.mode)
	}
}

func TestOverviewLinkFocusFollowsTheVisibleLines(t *testing.T) {
	m, _ := overview(t, overviewModel(t))
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 8})
	m = next.(Model)
	m = press(t, m, "tab") // the first check
	m = press(t, m, "G")   // scrolls it off the screen
	m = press(t, m, "tab")
	page := m.overviewPage()
	line := page.links[m.reader.focus].line()
	if line < m.reader.offset || line >= m.reader.offset+m.readerHeight(page) {
		t.Errorf("tab after scrolling focused link %d on page line %d, off the screen at offset %d",
			m.reader.focus, line, m.reader.offset)
	}
}

func TestOverviewClickOpensALink(t *testing.T) {
	started := fakeStart(t)
	m, _ := overview(t, overviewModel(t))
	page := m.overviewPage()
	span := page.links[0].spans[0]
	next, cmd := m.Update(tea.MouseMsg{
		X: span.Start + readerMargin, Y: span.Line, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
	})
	m = next.(Model)
	if cmd != nil {
		next, _ = m.Update(cmd())
		m = next.(Model)
	}
	if len(*started) != 1 {
		t.Fatalf("a click on a link started %d programs", len(*started))
	}
	if m.reader.focus != 0 {
		t.Errorf("a click left focus on link %d", m.reader.focus)
	}
}

func TestOverviewSectionJumps(t *testing.T) {
	m, _ := overview(t, overviewModel(t))
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 8})
	m = press(t, next.(Model), "n")
	page := m.overviewPage()
	if m.reader.offset != page.sections[1] {
		t.Errorf("n scrolled to line %d, want the Checks section at %d", m.reader.offset, page.sections[1])
	}
	if m = press(t, m, "p"); m.reader.offset != page.sections[0] {
		t.Errorf("p scrolled to line %d, want back to the header", m.reader.offset)
	}
}

func TestSyncKeepsTheOverviewOpen(t *testing.T) {
	m, _ := overview(t, overviewModel(t))
	m = press(t, m, "tab")
	fresh := overviewPR()
	fresh.Title = "Guard the nil case, again"
	next, _ := m.Update(syncResultMsg{
		snapshot: ghsrc.Snapshot{PR: fresh, HeadSHA: "head", FetchedAt: time.Now()},
		files:    diffparse.Parse(noteDiff),
	})
	m = next.(Model)
	if m.mode != modeOverview {
		t.Fatalf("a sync from the overview left mode %v", m.mode)
	}
	if m.reader.focus != -1 {
		t.Errorf("the link cursor survived a sync, on link %d", m.reader.focus)
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "Guard the nil case, again") {
		t.Errorf("the overview still shows the old snapshot:\n%s", view)
	}
}
