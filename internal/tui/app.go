package tui

import (
	"fmt"
	"math/rand/v2"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tobiasbernting/krv/v2/internal/ghsrc"
)

type screen int

const (
	screenQueue screen = iota
	screenLoading
	screenReview
)

// App is the queue and the reviews opened from it, as one program: choosing
// a pull request loads it in place, and leaving the review comes back to the
// list instead of to the shell.
type App struct {
	queue  QueueModel
	review Model
	open   func(Selection, *ghsrc.Tracer) (Options, error)

	screen screen
	// loading is the page shown while screen is screenLoading, naming the
	// pull request being fetched.
	loading loadingPage
	// sinceReview is whether the pull request loading opens on the changes
	// since your latest review.
	sinceReview bool
	// gen numbers each pull request opened. Messages from a load or a review
	// that has since been left carry an older number and are dropped, so a
	// late reply can never land in the review that replaced it.
	gen int

	width, height int
}

// NewApp starts on the queue. open loads what a row names; it runs off the
// UI goroutine, so it may block on the network. It reports the commands it
// runs to the tracer, which the loading page shows.
func NewApp(queue QueueModel, open func(Selection, *ghsrc.Tracer) (Options, error)) App {
	return App{queue: queue, open: open, width: queue.width, height: queue.height}
}

// openMsg is the queue asking for a pull request.
// A preview shows the loading page without loading anything; it stays until
// esc. sinceReview opens on the changes since your latest review, as a
// press of a would, instead of on your threads.
type openMsg struct {
	sel         Selection
	preview     bool
	sinceReview bool
}

// openedMsg is a finished load.
type openedMsg struct {
	gen  int
	opts Options
	err  error
}

func (a App) Init() tea.Cmd { return a.queue.Init() }

func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case reviewMsg:
		if !a.current(msg.gen, screenReview) {
			return a, nil
		}
		if _, ok := msg.msg.(backMsg); ok {
			return a.backToQueue()
		}
		return a.updateReview(msg.msg)

	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		a.loading.width, a.loading.height = msg.Width, msg.Height
		a, _ = a.updateQueue(msg)
		if a.screen == screenReview {
			return a.updateReview(msg)
		}
		return a, nil

	case queueLoadedMsg:
		return a.updateQueue(msg)

	case openMsg:
		a.gen++
		a.screen = screenLoading
		a.sinceReview = msg.sinceReview
		a.loading = loadingPage{
			item:  a.queue.item(msg.sel),
			scene: rand.IntN(len(scenes)),
			theme: a.queue.theme,
			width: a.width, height: a.height,
			now: time.Now(),
		}
		if msg.preview {
			a.loading.trace = sampleTrace(a.loading.now)
			return a, nextFrame(a.gen)
		}
		gen, open, sel := a.gen, a.open, msg.sel
		feed := newTraceFeed()
		load := func() tea.Msg {
			defer feed.close()
			opts, err := open(sel, ghsrc.NewTracer(feed.send))
			return openedMsg{gen: gen, opts: opts, err: err}
		}
		return a, tea.Batch(load, nextFrame(gen), readTrace(gen, feed.ch))

	case traceMsg:
		if !a.current(msg.gen, screenLoading) {
			return a, nil
		}
		a.loading.trace.add(msg.ev)
		a.loading.now = time.Now()
		return a, readTrace(msg.gen, msg.ch)

	case frameMsg:
		if !a.current(msg.gen, screenLoading) {
			return a, nil
		}
		a.loading.frame++
		a.loading.now = time.Now()
		return a, nextFrame(a.gen)

	case openedMsg:
		if !a.current(msg.gen, screenLoading) {
			return a, nil
		}
		if msg.err != nil {
			a.screen = screenQueue
			it := a.loading.item
			a.queue.notice = fmt.Sprintf("could not open %s#%d: %v", it.Repo, it.Number, msg.err)
			return a, nil
		}
		msg.opts.FromQueue = true
		// A theme picked in the queue, or in an earlier review, carries on.
		msg.opts.Theme = a.queue.theme
		if msg.opts.SaveTheme == nil {
			msg.opts.SaveTheme = a.queue.themes.save
		}
		a.review = New(msg.opts)
		a.screen = screenReview
		next, sized := a.review.Update(tea.WindowSizeMsg{Width: a.width, Height: a.height})
		a.review = next.(Model)
		if a.sinceReview {
			// Without a comparison this stays on the threads and says why.
			next, _ = a.review.showChanges()
			a.review = next.(Model)
		}
		return a, tea.Batch(a.tag(sized), a.tag(a.review.Init()))
	}

	switch a.screen {
	case screenLoading:
		if k, ok := msg.(tea.KeyMsg); ok {
			switch k.String() {
			case "ctrl+c":
				return a, tea.Quit
			case "esc", "q":
				// The load keeps running; its reply is dropped by gen.
				a.gen++
				a.screen = screenQueue
			case "tab", "shift+tab":
				step := 1
				if k.String() == "shift+tab" {
					step = len(scenes) - 1
				}
				a.loading.scene = (a.loading.scene + step) % len(scenes)
				a.loading.frame = 0 // each scene from its start
			}
		}
		return a, nil
	case screenReview:
		return a.updateReview(msg)
	case screenQueue:
		return a.updateQueue(msg)
	}
	return a, nil
}

// current reports whether a message stamped gen still belongs to what is on
// screen.
func (a App) current(gen int, s screen) bool { return gen == a.gen && a.screen == s }

func (a App) updateQueue(msg tea.Msg) (App, tea.Cmd) {
	next, cmd := a.queue.Update(msg)
	a.queue = next.(QueueModel)
	return a, cmd
}

func (a App) updateReview(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := a.review.Update(msg)
	a.review = next.(Model)
	return a, a.tag(cmd)
}

// backToQueue closes the review. The list is shown as it was left and
// refreshed underneath, since the review may have taken a row off it.
func (a App) backToQueue() (tea.Model, tea.Cmd) {
	a.gen++
	a.screen = screenQueue
	a.queue.theme = a.review.theme
	a.review = Model{}
	return a, a.queue.load(true)
}

// reviewMsg is a review's own message, stamped with the review it belongs to.
type reviewMsg struct {
	gen int
	msg tea.Msg
}

// tag stamps what a review command produces with the current generation.
// Only the review's own messages are wrapped: the rest, such as the one
// tea.ExecProcess returns to hand the terminal to an editor, are meant for
// the program itself and must reach it untouched.
func (a App) tag(cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	gen := a.gen
	return func() tea.Msg {
		msg := cmd()
		// A batch is run by the program, so each of its commands is
		// stamped on its own.
		if batch, ok := msg.(tea.BatchMsg); ok {
			tagged := make(tea.BatchMsg, len(batch))
			for i, c := range batch {
				tagged[i] = a.tag(c)
			}
			return tagged
		}
		if ownMsg(msg) {
			return reviewMsg{gen: gen, msg: msg}
		}
		return msg
	}
}

func (a App) View() string {
	switch a.screen {
	case screenReview:
		return a.review.View()
	case screenLoading:
		return a.loading.View()
	}
	return a.queue.View()
}
