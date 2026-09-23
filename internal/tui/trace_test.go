package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/tobiasbernting/krv/v2/internal/ghsrc"
)

var traceT0 = time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)

func start(id int, step, group, cmd string, at time.Duration) ghsrc.TraceEvent {
	return ghsrc.TraceEvent{ID: id, Kind: ghsrc.TraceStart, Step: step, Group: group, Command: cmd, At: traceT0.Add(at)}
}

func end(id int, at time.Duration, err error) ghsrc.TraceEvent {
	return ghsrc.TraceEvent{ID: id, Kind: ghsrc.TraceEnd, Err: err, At: traceT0.Add(at)}
}

// tracedPage is the loading page at width × height with the events added.
func tracedPage(t *testing.T, width, height int, now time.Duration, events ...ghsrc.TraceEvent) App {
	t.Helper()
	a := loadingApp(t, width, height)
	a.loading.trace = traceLog{}
	for _, ev := range events {
		a.loading.trace.add(ev)
	}
	a.loading.now = traceT0.Add(now)
	return a
}

// panelLines is the view's last rows, where the panel is.
func panelLines(a App) []string {
	lines := strings.Split(stripANSI(a.View()), "\n")
	return lines[len(lines)-traceShown:]
}

func TestTracePanelShowsTheLatestFiveSteps(t *testing.T) {
	var events []ghsrc.TraceEvent
	for i := 1; i <= 7; i++ {
		g := fmt.Sprintf("g%d", i)
		events = append(events,
			start(i*2, fmt.Sprintf("step %d", i), g, "gh cmd", time.Duration(i)*time.Second),
			end(i*2, time.Duration(i)*time.Second+300*time.Millisecond, nil))
	}
	a := tracedPage(t, 100, 30, 10*time.Second, events...)
	panel := strings.Join(panelLines(a), "\n")
	for i := 3; i <= 7; i++ {
		if !strings.Contains(panel, fmt.Sprintf("step %d", i)) {
			t.Errorf("panel lacks step %d:\n%s", i, panel)
		}
	}
	if strings.Contains(panel, "step 1 ") || strings.Contains(panel, "step 2 ") {
		t.Errorf("panel still shows steps scrolled off:\n%s", panel)
	}
	if !strings.Contains(panel, "✓ step 7") || !strings.Contains(panel, "0.3s") {
		t.Errorf("a finished step does not show its tick and duration:\n%s", panel)
	}
}

func TestTracePanelTimesRunningStepsAndKeepsLabels(t *testing.T) {
	long := "gh api repos/acme/app/git/blobs/" + strings.Repeat("9f", 40)
	a := tracedPage(t, 100, 30, 4200*time.Millisecond,
		start(1, "file versions 7/23", "g1", long, 0),
		start(2, "build compare", "g2", "git hash-object -w --stdin", 0))
	panel := panelLines(a)
	for _, want := range []string{"file versions 7/23", "build compare", "4.2s", "…"} {
		if !strings.Contains(strings.Join(panel, "\n"), want) {
			t.Errorf("panel lacks %q:\n%s", want, strings.Join(panel, "\n"))
		}
	}
	if strings.Contains(strings.Join(panel, "\n"), "✓") {
		t.Error("a step still running shows as done")
	}
	for n, line := range strings.Split(a.View(), "\n") {
		if w := lipgloss.Width(line); w != 100 {
			t.Fatalf("line %d is %d wide on a 100-column terminal", n, w)
		}
	}
}

func TestTraceGroupSpinsUntilItsLastCommandEnds(t *testing.T) {
	var l traceLog
	l.add(start(1, "file versions 1/2", "g", "gh a", 0))
	l.add(start(2, "file versions 2/2", "g", "gh b", 0))
	l.add(end(1, time.Second, nil))
	if len(l.entries) != 1 || l.entries[0].running != 1 || l.current() != "file versions 2/2" {
		t.Fatalf("after one of two ends: %+v", l.entries)
	}
	l.add(end(2, 2*time.Second, fmt.Errorf("HTTP 502")))
	if e := l.entries[0]; e.running != 0 || !e.failed || elapsed(e, traceT0) != "2.0s" {
		t.Errorf("after both end: %+v", e)
	}
	if l.current() != "" {
		t.Errorf("current = %q with nothing running", l.current())
	}
}

func TestTraceOneLinerNamesTheStepOnSmallTerminals(t *testing.T) {
	a := tracedPage(t, 60, 6, time.Second, start(1, "diff", "g1", "gh pr diff 8", 0))
	for i, sc := range scenes {
		a.loading.scene = i
		view := stripANSI(a.View())
		if lines := strings.Count(view, "\n") + 1; lines != 6 {
			t.Fatalf("%s: small page is %d lines, want 6", sc.name, lines)
		}
		if strings.Contains(view, "gh pr diff") {
			t.Errorf("%s: panel drawn on a 6-row terminal:\n%s", sc.name, view)
		}
		if !strings.Contains(view, "· diff") {
			t.Errorf("%s: one-liner does not name the step:\n%s", sc.name, view)
		}
	}
}

func TestTraceEventsReachThePageAndStaleOnesDrop(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	a := newApp(t, func(_ Selection, trace *ghsrc.Tracer) (Options, error) {
		trace.Mark("head moved — retrying")
		<-release
		return Options{}, fmt.Errorf("released")
	})
	a, _ = pressA(t, a, "enter")
	if !strings.Contains(stripANSI(a.View()), "head moved — retrying") {
		t.Fatalf("the load's event never reached the page:\n%s", stripANSI(a.View()))
	}

	before := len(a.loading.trace.entries)
	next, cmd := a.Update(traceMsg{gen: a.gen - 1, ev: start(99, "stale", "", "gh x", 0)})
	if got := next.(App); len(got.loading.trace.entries) != before || cmd != nil {
		t.Error("an event from an older load landed on the page")
	}
}

func TestTraceFeedIgnoresSendsAfterClose(t *testing.T) {
	f := newTraceFeed()
	f.close()
	f.send(ghsrc.TraceEvent{}) // must not panic
	f.close()
}

func TestLoadingPreviewShowsASampleTrace(t *testing.T) {
	a := newApp(t, openerFor(t, nil))
	next, _ := a.Update(openMsg{sel: Selection{Repo: "acme/x", Number: 8}, preview: true})
	a = next.(App)
	view := stripANSI(a.View())
	for _, want := range []string{"pull request", "file versions 7/23", "gh pr diff"} {
		if !strings.Contains(view, want) {
			t.Errorf("preview lacks %q:\n%s", want, view)
		}
	}
}
