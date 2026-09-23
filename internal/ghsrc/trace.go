package ghsrc

import (
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// TraceKind says what a TraceEvent reports.
type TraceKind int

const (
	TraceStart  TraceKind = iota // a command began
	TraceEnd                     // a command finished, Err says how
	TraceMarker                  // something happened that is not a command
)

// TraceEvent is one command a Client ran, or a marker between them, as the
// loading page shows it.
//
// A step can take several commands: threads is a REST call and a GraphQL
// one, and a comparison fetches one blob per changed file. Those share a
// Group, so they read as one line whose Step and Command follow the latest.
type TraceEvent struct {
	ID      int
	Kind    TraceKind
	Step    string
	Group   string
	Command string
	Err     error
	At      time.Time
}

// Tracer reports what a Client runs. A nil Tracer reports nothing, so
// clients outside the loading page carry none and pay nothing.
//
// The sink runs on whichever goroutine ran the command, and must not block:
// it sits on the load's own path.
type Tracer struct {
	sink func(TraceEvent)
	ids  atomic.Int64
}

func NewTracer(sink func(TraceEvent)) *Tracer { return &Tracer{sink: sink} }

func (t *Tracer) next() int { return int(t.ids.Add(1)) }

// group names a new line on the loading page.
func (t *Tracer) group() string {
	if t == nil {
		return ""
	}
	return "g" + strconv.Itoa(t.next())
}

// Mark reports a step with no command, such as a retry.
func (t *Tracer) Mark(step string) {
	if t == nil {
		return
	}
	t.sink(TraceEvent{ID: t.next(), Kind: TraceMarker, Step: step, At: time.Now()})
}

// start reports a command beginning and returns the id its end reports.
func (t *Tracer) start(step, group, name string, args []string) int {
	if t == nil {
		return 0
	}
	id := t.next()
	// A GraphQL query is many lines; the panel has one.
	command := strings.Join(strings.Fields(name+" "+strings.Join(args, " ")), " ")
	t.sink(TraceEvent{ID: id, Kind: TraceStart, Step: step, Group: group, Command: command, At: time.Now()})
	return id
}

func (t *Tracer) end(id int, err error) {
	if t == nil {
		return
	}
	t.sink(TraceEvent{ID: id, Kind: TraceEnd, Err: err, At: time.Now()})
}

// traced is c with the commands it runs reported under a line of their own.
func (c Client) traced(step string) Client {
	c.step, c.group = step, c.Trace.group()
	return c
}

// relabel keeps c's line and renames it, for a step that counts as it goes.
func (c Client) relabel(step string) Client {
	c.step = step
	return c
}
