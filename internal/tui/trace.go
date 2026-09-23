package tui

import (
	"fmt"
	"strconv"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
	"github.com/tobiasbernting/krv/v2/internal/ghsrc"
)

// The trace panel sits under the loading page's scene and says what the
// load is doing: each step, the command behind it, and how long it took. A
// slow load then explains itself instead of just spinning.
const (
	traceShown = 5              // entries on screen; older ones scroll off
	traceRows  = traceShown + 1 // and a blank row between scene and panel
	// traceMinHeight is the smallest terminal that gets the panel. Below it
	// the scene needs every row, and the one-liner names the step instead.
	traceMinHeight = 16
	// traceBuffer is how many events can wait for the UI before the load
	// starts dropping them rather than waiting.
	traceBuffer = 256
)

// traceMsg is one event from a load, stamped with its generation so that a
// cancelled load's events are dropped like its frames.
type traceMsg struct {
	gen int
	ev  ghsrc.TraceEvent
	ch  <-chan ghsrc.TraceEvent
}

// readTrace waits for the load's next event. Each one read re-arms it; a
// closed channel means the load is over and ends the reading.
func readTrace(gen int, ch <-chan ghsrc.TraceEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil
		}
		return traceMsg{gen: gen, ev: ev, ch: ch}
	}
}

// traceFeed carries a load's events to the UI. send never blocks the load:
// when the UI falls that far behind, the event is dropped and the panel
// keeps what it has. Once closed, sends are ignored, so a client that
// outlives its load cannot panic on the closed channel.
type traceFeed struct {
	mu     sync.Mutex
	ch     chan ghsrc.TraceEvent
	closed bool
}

func newTraceFeed() *traceFeed { return &traceFeed{ch: make(chan ghsrc.TraceEvent, traceBuffer)} }

func (f *traceFeed) send(ev ghsrc.TraceEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	select {
	case f.ch <- ev:
	default:
	}
}

func (f *traceFeed) close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		close(f.ch)
	}
}

// traceEntry is one line of the panel. Commands in the same group share a
// line, which follows the latest of them; running counts the ones still
// going, so parallel commands keep the line spinning until the last ends.
type traceEntry struct {
	key        string
	step       string
	command    string
	start, end time.Time
	running    int
	failed     bool
	marker     bool
}

// traceLog is the load's entries in the order they started.
type traceLog struct {
	entries []traceEntry
	keys    map[int]string // command id → the entry it ended in
}

func (l *traceLog) add(ev ghsrc.TraceEvent) {
	if l.keys == nil {
		l.keys = map[int]string{}
	}
	switch ev.Kind {
	case ghsrc.TraceMarker:
		l.entries = append(l.entries, traceEntry{key: "m" + strconv.Itoa(ev.ID), step: ev.Step, start: ev.At, marker: true})
	case ghsrc.TraceStart:
		key := ev.Group
		if key == "" {
			key = "c" + strconv.Itoa(ev.ID)
		}
		l.keys[ev.ID] = key
		e := l.find(key)
		if e == nil {
			l.entries = append(l.entries, traceEntry{key: key, start: ev.At})
			e = &l.entries[len(l.entries)-1]
		}
		e.step, e.command = ev.Step, ev.Command
		e.running++
	case ghsrc.TraceEnd:
		key, ok := l.keys[ev.ID]
		if !ok {
			return
		}
		delete(l.keys, ev.ID)
		if e := l.find(key); e != nil {
			e.running--
			e.end = ev.At
			e.failed = e.failed || ev.Err != nil
		}
	}
}

func (l *traceLog) find(key string) *traceEntry {
	for i := len(l.entries) - 1; i >= 0; i-- {
		if l.entries[i].key == key {
			return &l.entries[i]
		}
	}
	return nil
}

// current is the step most recently started and still running.
func (l traceLog) current() string {
	for i := len(l.entries) - 1; i >= 0; i-- {
		if l.entries[i].running > 0 {
			return l.entries[i].step
		}
	}
	return ""
}

// drawTrace fills c, the rows under the scene, with the log's latest
// entries. The first row stays blank.
func drawTrace(c *canvas, p loadingPage) {
	t := p.theme
	entries := p.trace.entries
	if len(entries) > traceShown {
		entries = entries[len(entries)-traceShown:]
	}
	width := minInt(c.w-4, maxInt(48, c.w*6/10))
	labelW := 0
	for _, e := range entries {
		labelW = maxInt(labelW, runewidth.StringWidth(e.step))
	}
	labelW = minInt(labelW, 22)
	const durW = 6
	for i, e := range entries {
		y := 1 + i
		mark, fg := "✓", t.Dim
		switch {
		case e.running > 0:
			mark, fg = spinner[p.frame%len(spinner)], t.Accent
		case e.failed:
			mark, fg = "✗", t.DelSign
		case e.marker:
			mark = "↻"
		}
		x := c.text(2, y, mark+" ", fg, false)
		label := runewidth.FillRight(runewidth.Truncate(e.step, labelW, "…"), labelW)
		x = c.text(x, y, label+"  ", fg, e.running > 0)
		if e.marker {
			continue
		}
		room := width - (x - 2) - durW - 1
		if room > 3 {
			c.text(x, y, runewidth.Truncate(e.command, room, "…"), t.Dim, false)
		}
		dur := runewidth.FillLeft(elapsed(e, p.now), durW)
		c.text(2+width-durW, y, dur, fg, false)
	}
}

// elapsed is how long an entry ran, or has run so far.
func elapsed(e traceEntry, now time.Time) string {
	end := e.end
	if e.running > 0 {
		end = now
	}
	d := end.Sub(e.start)
	switch {
	case d < 0:
		d = 0
	case d >= time.Minute:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	case d >= 10*time.Second:
		return fmt.Sprintf("%.0fs", d.Seconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// sampleTrace is what the loading preview shows, since nothing loads.
func sampleTrace(now time.Time) traceLog {
	var l traceLog
	at := now.Add(-5 * time.Second)
	step := func(id int, step, group, cmd string, took time.Duration) {
		l.add(ghsrc.TraceEvent{ID: id, Kind: ghsrc.TraceStart, Step: step, Group: group, Command: cmd, At: at})
		if took > 0 {
			at = at.Add(took)
			l.add(ghsrc.TraceEvent{ID: id, Kind: ghsrc.TraceEnd, At: at})
		}
	}
	step(1, "pull request", "g1", "gh pr view 42 --json number,title,body,state,url --repo acme/app", 600*time.Millisecond)
	step(2, "diff", "g2", "gh pr diff 42 --repo acme/app", 1100*time.Millisecond)
	step(3, "threads", "g3", "gh api graphql -f query=…", 900*time.Millisecond)
	step(4, "compare trees", "g4", "gh api repos/acme/app/git/trees/3f9a2c1?recursive=1", 400*time.Millisecond)
	step(5, "file versions 7/23", "g5", "gh api repos/acme/app/git/blobs/9fe1b07", 0)
	return l
}
