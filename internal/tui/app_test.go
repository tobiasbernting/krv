package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/tobiasbernting/krv/v2/internal/config"
	"github.com/tobiasbernting/krv/v2/internal/diffparse"
	"github.com/tobiasbernting/krv/v2/internal/ghsrc"
	"github.com/tobiasbernting/krv/v2/internal/render"
)

// openerFor returns an opener that serves a small review for any selection
// and records what it was asked for.
func openerFor(t *testing.T, asked *[]Selection) func(Selection, *ghsrc.Tracer) (Options, error) {
	return func(sel Selection, _ *ghsrc.Tracer) (Options, error) {
		if asked != nil {
			*asked = append(*asked, sel)
		}
		return Options{
			Files:  diffparse.Parse(navDiff),
			Theme:  render.DefaultTheme(),
			Config: config.Defaults(),
			Source: Source{Kind: SourcePR, Repo: sel.Repo, PRNumber: sel.Number, Title: "pr"},
			Review: newTestReview(t),
		}, nil
	}
}

func newApp(t *testing.T, open func(Selection, *ghsrc.Tracer) (Options, error)) App {
	t.Helper()
	q := newQueue(t)
	// The list never comes from gh in tests; returning to it reloads the
	// same rows.
	q.fetch = func(ghsrc.Filter, int, bool) ([]ghsrc.QueueItem, time.Time, error) {
		return queueItems(), time.Now(), nil
	}
	a := NewApp(q, open)
	next, _ := a.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return next.(App)
}

// settle feeds back every message the command produces within a short
// window, the way the program would. Ticks outlive the window and are
// dropped, so animation and sync timers never run in tests.
func settle(t *testing.T, a App, cmd tea.Cmd) (App, bool) {
	t.Helper()
	quit := false
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0 && steps < 50; steps++ {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		got := make(chan tea.Msg, 1)
		go func() { got <- c() }()
		var msg tea.Msg
		select {
		case msg = <-got:
		case <-time.After(50 * time.Millisecond):
			continue
		}
		switch msg := msg.(type) {
		case tea.BatchMsg:
			queue = append(queue, msg...)
			continue
		case tea.QuitMsg:
			quit = true
			continue
		}
		next, more := a.Update(msg)
		a = next.(App)
		queue = append(queue, more)
	}
	return a, quit
}

func pressA(t *testing.T, a App, keys ...string) (App, bool) {
	t.Helper()
	quit := false
	for _, k := range keys {
		msg := keyMsg(k)
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "ctrl+c":
			msg = tea.KeyMsg{Type: tea.KeyCtrlC}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "shift+tab":
			msg = tea.KeyMsg{Type: tea.KeyShiftTab}
		}
		next, cmd := a.Update(msg)
		var q bool
		a, q = settle(t, next.(App), cmd)
		quit = quit || q
	}
	return a, quit
}

func TestAppOpensChosenPullRequestWithoutQuitting(t *testing.T) {
	var asked []Selection
	a := newApp(t, openerFor(t, &asked))

	a, quit := pressA(t, a, "j", "enter")
	if quit {
		t.Fatal("choosing a pull request ended the program")
	}
	if len(asked) != 1 || asked[0].Repo != "acme/y" || asked[0].Number != 4 {
		t.Fatalf("opener asked for %+v", asked)
	}
	if a.screen != screenReview {
		t.Fatalf("screen = %v, want the review", a.screen)
	}
}

func TestAppLeavingReviewReturnsToQueue(t *testing.T) {
	a := newApp(t, openerFor(t, nil))
	a, _ = pressA(t, a, "j", "enter")

	a, quit := pressA(t, a, "q")
	if quit {
		t.Fatal("q in a review opened from the queue ended the program")
	}
	if a.screen != screenQueue {
		t.Fatalf("screen = %v, want the queue", a.screen)
	}
	if a.queue.cursor != 1 {
		t.Errorf("cursor = %d, want it where it was left", a.queue.cursor)
	}
}

func TestAppReturningRefreshesTheQueue(t *testing.T) {
	a := newApp(t, openerFor(t, nil))
	a, _ = pressA(t, a, "j", "enter")

	// Approving the second pull request took it off the list.
	var forced bool
	a.queue.fetch = func(_ ghsrc.Filter, _ int, force bool) ([]ghsrc.QueueItem, time.Time, error) {
		forced = force
		return queueItems()[:1], time.Now(), nil
	}
	a, _ = pressA(t, a, "q")

	if !forced {
		t.Error("the list was not refreshed from GitHub on return")
	}
	if len(a.queue.items) != 1 || a.queue.cursor != 0 {
		t.Errorf("items=%d cursor=%d, want the one remaining row selected", len(a.queue.items), a.queue.cursor)
	}
}

func TestAppDropsMessagesFromALeftReview(t *testing.T) {
	a := newApp(t, openerFor(t, nil))
	a, _ = pressA(t, a, "enter")
	first := a.gen
	a, _ = pressA(t, a, "q", "enter")

	// A sync the first review started finishes after the second has opened.
	late := reviewMsg{gen: first, msg: syncResultMsg{err: errors.New("from the old review")}}
	next, _ := a.Update(late)
	a = next.(App)
	if a.review.sync.err != "" {
		t.Errorf("the new review took the old one's sync result: %q", a.review.sync.err)
	}
}

func TestAppQWaitsForGitHubBeforeLeaving(t *testing.T) {
	a := newApp(t, openerFor(t, nil))
	a, _ = pressA(t, a, "enter")
	a.review.requests.start(reqResolve("t1"))

	a, quit := pressA(t, a, "q")
	if quit || a.screen != screenReview {
		t.Fatalf("q left a review with a GitHub change in flight (quit=%v, screen=%v)", quit, a.screen)
	}
	if !strings.Contains(a.View(), "ctrl+c to quit") {
		t.Error("the review does not say how to get out")
	}
}

func TestAppEscCancelsALoad(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	slow := func(sel Selection, _ *ghsrc.Tracer) (Options, error) {
		<-release
		return openerFor(t, nil)(sel, nil)
	}
	a := newApp(t, slow)
	a, _ = pressA(t, a, "enter")
	if a.screen != screenLoading {
		t.Fatalf("screen = %v, want the loading page", a.screen)
	}
	loading := a.gen

	a, quit := pressA(t, a, "esc")
	if quit || a.screen != screenQueue {
		t.Fatalf("esc did not return to the queue (quit=%v, screen=%v)", quit, a.screen)
	}

	opts, _ := openerFor(t, nil)(Selection{Repo: "acme/x", Number: 8}, nil)
	next, _ := a.Update(openedMsg{gen: loading, opts: opts})
	if next.(App).screen != screenQueue {
		t.Error("a cancelled load still opened its review")
	}
}

func stripANSI(s string) string { return ansiCodes.ReplaceAllString(s, "") }

// loadingApp is an App parked on the loading page for acme/x#8.
func loadingApp(t *testing.T, width, height int) App {
	t.Helper()
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	a := newApp(t, func(sel Selection, _ *ghsrc.Tracer) (Options, error) {
		<-release
		return Options{}, errors.New("released")
	})
	next, _ := a.Update(tea.WindowSizeMsg{Width: width, Height: height})
	a, _ = pressA(t, next.(App), "enter")
	return a
}

func TestAppLoadingPageNamesThePullRequest(t *testing.T) {
	a := loadingApp(t, 100, 30)
	for i, sc := range scenes {
		a.loading.scene = i
		// Through a whole loop of each animation, not just its first frame.
		for f := 0; f < 400; f += 7 {
			a.loading.frame = f
			view := stripANSI(a.View())
			for _, want := range []string{"acme/x#8", "feat: notes", "ann", "esc cancel", "tab next"} {
				if !strings.Contains(view, want) {
					t.Fatalf("%s, frame %d: loading page lacks %q:\n%s", sc.name, f, want, view)
				}
			}
			if lines := strings.Count(view, "\n") + 1; lines != 30 {
				t.Fatalf("%s, frame %d: loading page is %d lines, want the full 30", sc.name, f, lines)
			}
			for n, line := range strings.Split(view, "\n") {
				if w := lipgloss.Width(line); w != 100 {
					t.Fatalf("%s, frame %d: line %d is %d wide on a 100-column terminal", sc.name, f, n, w)
				}
			}
		}
	}
}

func TestAppLoadingPageTabCyclesScenes(t *testing.T) {
	a := loadingApp(t, 100, 30)
	a.loading.scene = 0
	a.loading.frame = 12
	seen := map[string]bool{}
	for range scenes {
		seen[a.View()] = true
		a, _ = pressA(t, a, "tab")
	}
	if len(seen) != len(scenes) {
		t.Errorf("tab showed %d different scenes, want all %d", len(seen), len(scenes))
	}
	if a.loading.scene != 0 {
		t.Errorf("tab round the scenes ended on %d, want back at 0", a.loading.scene)
	}
	if a.loading.frame != 0 {
		t.Error("a new scene did not start from its beginning")
	}
	a, _ = pressA(t, a, "shift+tab")
	if a.loading.scene != len(scenes)-1 {
		t.Errorf("shift+tab from the first scene went to %d, want the last", a.loading.scene)
	}
}

func TestAppLoadingPageAnimates(t *testing.T) {
	a := loadingApp(t, 100, 30)
	before := a.View()
	next, cmd := a.Update(frameMsg{gen: a.gen})
	a = next.(App)
	if a.View() == before {
		t.Error("a frame did not change the page")
	}
	if cmd == nil {
		t.Error("the animation stopped while still loading")
	}

	a, _ = pressA(t, a, "esc")
	if _, cmd := a.Update(frameMsg{gen: a.gen - 1}); cmd != nil {
		t.Error("the animation kept ticking after the load was cancelled")
	}
}

func TestAppLoadingPageFallsBackOnSmallTerminals(t *testing.T) {
	a := loadingApp(t, 40, 6)
	for i, sc := range scenes {
		a.loading.scene = i
		view := stripANSI(a.View())
		if !strings.Contains(view, "acme/x#8") {
			t.Errorf("%s: small loading page lacks the pull request:\n%s", sc.name, view)
		}
		for n, line := range strings.Split(view, "\n") {
			if w := lipgloss.Width(line); w > 40 {
				t.Errorf("%s: line %d is %d wide on a 40-column terminal", sc.name, n, w)
			}
		}
	}
}

func TestAppLoadFailureStaysOnQueueWithError(t *testing.T) {
	failing := func(Selection, *ghsrc.Tracer) (Options, error) { return Options{}, errors.New("HTTP 502") }
	a := newApp(t, failing)

	a, quit := pressA(t, a, "enter")
	if quit || a.screen != screenQueue {
		t.Fatalf("a failed load left the queue (quit=%v, screen=%v)", quit, a.screen)
	}
	view := a.View()
	if !strings.Contains(view, "HTTP 502") || !strings.Contains(view, "acme/x#8") {
		t.Errorf("the queue does not say what failed:\n%s", view)
	}
	if !strings.Contains(view, "feat: notes") {
		t.Error("the error replaced the list")
	}
}

func TestAppReviewSaysQGoesBack(t *testing.T) {
	a := newApp(t, openerFor(t, nil))
	a, _ = pressA(t, a, "enter")
	if view := stripANSI(a.View()); !strings.Contains(view, "q queue") || strings.Contains(view, "q quit") {
		t.Errorf("status bar does not say q goes back:\n%s", view)
	}
	a, _ = pressA(t, a, "?", "G") // General is last; help scrolls
	if view := stripANSI(a.View()); !strings.Contains(view, "back to the queue") {
		t.Errorf("help does not say q goes back:\n%s", view)
	}
}

func TestAppCtrlCInANoteCancelsTheNote(t *testing.T) {
	a := newApp(t, openerFor(t, nil))
	a, _ = pressA(t, a, "enter")
	for i, row := range a.review.doc.Rows {
		if _, _, _, ok := (Model{doc: a.review.doc, files: a.review.files, cursor: i}).cursorLine(); ok && row.IsCode() {
			a.review.cursor = i
			break
		}
	}
	a, _ = pressA(t, a, "c")
	if a.review.mode != modeInput {
		t.Fatalf("mode = %v, want a note being typed (err %q, status %q)", a.review.mode, a.review.err, a.review.status)
	}

	a, quit := pressA(t, a, "ctrl+c")
	if quit || a.screen != screenReview {
		t.Fatalf("ctrl+c in a note ended the review (quit=%v, screen=%v)", quit, a.screen)
	}
	if a.review.mode == modeInput {
		t.Error("ctrl+c did not cancel the note")
	}
}

func TestAppCtrlCQuitsWhileSubmitting(t *testing.T) {
	a := newApp(t, openerFor(t, nil))
	a, _ = pressA(t, a, "enter")
	a.review.mode = modeSubmit
	a.review.requests.start(reqSubmit)

	if _, quit := pressA(t, a, "ctrl+c"); !quit {
		t.Error("a hung submit made the program impossible to leave")
	}
}

func TestAppLoadFailureDoesNotOfferRetryOfTheList(t *testing.T) {
	failing := func(Selection, *ghsrc.Tracer) (Options, error) { return Options{}, errors.New("HTTP 502") }
	a := newApp(t, failing)
	a, _ = pressA(t, a, "enter")
	if strings.Contains(stripANSI(a.View()), "r retry") {
		t.Error("r is offered as a retry, but it refreshes the list")
	}

	a, _ = pressA(t, a, "j")
	if strings.Contains(stripANSI(a.View()), "HTTP 502") {
		t.Error("the failure stays after moving on")
	}
}

func TestAppCtrlCQuitsFromLoading(t *testing.T) {
	a := loadingApp(t, 100, 30)
	if _, quit := pressA(t, a, "ctrl+c"); !quit {
		t.Error("ctrl+c did not end the program while loading")
	}
}

func TestAppCtrlCQuitsFromReview(t *testing.T) {
	a := newApp(t, openerFor(t, nil))
	a, _ = pressA(t, a, "enter")

	if _, quit := pressA(t, a, "ctrl+c"); !quit {
		t.Error("ctrl+c did not end the program")
	}
}

func TestAppDoubleClickInTheQueueOpensThePullRequest(t *testing.T) {
	var asked []Selection
	a := newApp(t, openerFor(t, &asked))
	a.queue.now = ticking(100 * time.Millisecond)
	for range 2 {
		next, cmd := a.Update(click(2))
		a, _ = settle(t, next.(App), cmd)
	}
	if a.screen != screenReview || len(asked) != 1 || asked[0].Number != 4 {
		t.Errorf("double click on the second row: screen %v, opened %+v", a.screen, asked)
	}
}

// followupApp is a queue of pull requests in every review state, each of
// which opens as one you reviewed before its latest commits.
func followupApp(t *testing.T) App {
	t.Helper()
	open := func(sel Selection, _ *ghsrc.Tracer) (Options, error) {
		opts, err := openerFor(t, nil)(sel, nil)
		opts.Files = diffparse.Parse(noteDiff)
		opts.Source.FollowUp = followupSession()
		return opts, err
	}
	a := newApp(t, open)
	a.queue.items = reviewStateItems()
	return a
}

func TestAppOpensNewCommitsOnChangesSinceReview(t *testing.T) {
	a, _ := pressA(t, followupApp(t), "enter")
	if a.screen != screenReview {
		t.Fatalf("screen = %v, want the review", a.screen)
	}
	if !a.review.changesView || a.review.mode != modeDiff {
		t.Fatalf("opened on mode %v, changes %v; want the changes since your review", a.review.mode, a.review.changesView)
	}
	if !strings.Contains(a.View(), "since review") {
		t.Errorf("the view does not say it shows changes since your review:\n%s", a.View())
	}
	a, _ = pressA(t, a, "D")
	if a.review.changesView {
		t.Error("D did not reach the full diff")
	}
}

func TestAppOpensAnUnmarkedPullRequestAsBefore(t *testing.T) {
	a, _ := pressA(t, followupApp(t), "j", "enter")
	if a.screen != screenReview {
		t.Fatalf("screen = %v, want the review", a.screen)
	}
	if a.review.changesView || a.review.mode != modeThreads {
		t.Errorf("opened on mode %v, changes %v; want your threads as before", a.review.mode, a.review.changesView)
	}
}

func TestAppDoubleClickOnNewCommitsOpensChangesSinceReview(t *testing.T) {
	a := followupApp(t)
	a.queue.now = ticking(100 * time.Millisecond)
	for range 2 {
		next, cmd := a.Update(click(1))
		a, _ = settle(t, next.(App), cmd)
	}
	if a.screen != screenReview || !a.review.changesView {
		t.Errorf("double click on a marked row: screen %v, changes %v", a.screen, a.review.changesView)
	}
}

func TestAppDropsAYankResultFromALeftReview(t *testing.T) {
	clip := &fakeClipboard{err: errors.New("from the old review")}
	open := func(sel Selection, _ *ghsrc.Tracer) (Options, error) {
		o, err := openerFor(t, nil)(sel, nil)
		o.Clipboard = clip
		return o, err
	}
	a := newApp(t, open)
	a, _ = pressA(t, a, "enter")
	next, yank := a.Update(keyMsg("y")) // the cursor starts on a file header
	a = next.(App)
	if yank == nil {
		t.Fatal("y asked for nothing")
	}
	a, _ = pressA(t, a, "q", "enter")

	next, _ = a.Update(yank())
	if a = next.(App); a.review.err != "" {
		t.Errorf("the new review reported the old one's copy: %q", a.review.err)
	}
}

func TestRoman(t *testing.T) {
	for n, want := range map[int]string{1: "I", 4: "IV", 8: "VIII", 31: "XXXI", 1994: "MCMXCIV", 4000: "4000"} {
		if got := roman(n); got != want {
			t.Errorf("roman(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestAppPreviewShowsLoadingPageWithoutLoading(t *testing.T) {
	loads := 0
	a := newApp(t, func(Selection, *ghsrc.Tracer) (Options, error) {
		loads++
		return Options{}, nil
	})
	next, _ := a.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	a, _ = pressA(t, next.(App), "L")
	if a.screen != screenLoading {
		t.Fatalf("screen = %v, want the loading page", a.screen)
	}
	if view := stripANSI(a.View()); !strings.Contains(view, "acme/x#8") {
		t.Errorf("preview does not name the highlighted pull request:\n%s", view)
	}
	next, cmd := a.Update(frameMsg{gen: a.gen})
	a = next.(App)
	if cmd == nil {
		t.Error("the preview does not animate")
	}
	a, _ = pressA(t, a, "esc")
	if a.screen != screenQueue {
		t.Errorf("esc left the preview on %v, want the queue", a.screen)
	}
	if loads != 0 {
		t.Errorf("the preview loaded %d pull requests, want none", loads)
	}
}
