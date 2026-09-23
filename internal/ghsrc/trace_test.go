package ghsrc

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
)

// recorder collects what a Tracer reports.
type recorder struct {
	mu     sync.Mutex
	events []TraceEvent
}

func (r *recorder) tracer() *Tracer {
	return NewTracer(func(ev TraceEvent) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.events = append(r.events, ev)
	})
}

// steps is the step of every start and marker, in order.
func (r *recorder) steps() []string {
	var out []string
	for _, ev := range r.events {
		if ev.Kind != TraceEnd {
			out = append(out, ev.Step)
		}
	}
	return out
}

func TestTraceNamesEachStepOfASnapshot(t *testing.T) {
	var rec recorder
	prCalls := 0
	client := Client{Trace: rec.tracer(), runOverride: func(_ []byte, args ...string) (string, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.HasPrefix(joined, "pr view"):
			// The head moves between the first pass's two looks.
			sha := []string{"a", "b", "c", "c"}[prCalls]
			prCalls++
			return fmt.Sprintf(`{"number":1,"headRefOid":%q}`, sha), nil
		case strings.HasPrefix(joined, "pr diff"):
			return "", nil
		case strings.Contains(joined, "/pulls/1/comments"):
			return "[]", nil
		case strings.Contains(joined, "api graphql"):
			return `[{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}]`, nil
		}
		return "", fmt.Errorf("unexpected command: %s", joined)
	}}

	if _, err := client.Snapshot("acme/x", 1); err != nil {
		t.Fatal(err)
	}
	// Diff and threads run side by side, so only their set is fixed.
	got := rec.steps()
	for _, span := range [][2]int{{1, 4}, {7, 10}} {
		sort.Strings(got[span[0]:span[1]])
	}
	pass := []string{"pull request", "diff", "threads", "threads", "recheck head"}
	want := append(append(append([]string{}, pass...), "head moved — retrying"), pass...)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("steps = %q\nwant    %q", got, want)
	}

	starts, ends := map[int]TraceEvent{}, 0
	for _, ev := range rec.events {
		switch ev.Kind {
		case TraceStart:
			starts[ev.ID] = ev
			if !strings.HasPrefix(ev.Command, "gh ") {
				t.Errorf("command %q does not say it is gh", ev.Command)
			}
		case TraceEnd:
			if _, ok := starts[ev.ID]; !ok {
				t.Errorf("end %d has no start before it", ev.ID)
			}
			ends++
		}
	}
	if ends != len(starts) {
		t.Errorf("%d commands started, %d ended", len(starts), ends)
	}
	// Threads' REST and GraphQL calls are one line; the two passes are not.
	groups := map[string]map[string]bool{}
	for _, ev := range starts {
		if groups[ev.Step] == nil {
			groups[ev.Step] = map[string]bool{}
		}
		groups[ev.Step][ev.Group] = true
	}
	if len(groups["threads"]) != 2 {
		t.Errorf("threads ran as %d lines over two passes, want 2", len(groups["threads"]))
	}
}

func TestTraceReportsAFailedCommand(t *testing.T) {
	var rec recorder
	client := Client{Trace: rec.tracer(), runOverride: func([]byte, ...string) (string, error) {
		return "", errors.New("HTTP 502")
	}}
	if _, err := client.traced("diff").Diff(1); err == nil {
		t.Fatal("want the error through")
	}
	last := rec.events[len(rec.events)-1]
	if last.Kind != TraceEnd || last.Err == nil || !strings.Contains(last.Err.Error(), "HTTP 502") {
		t.Errorf("last event = %+v, want an end carrying the error", last)
	}
}

func TestTraceCountsComparedFilesOnOneLine(t *testing.T) {
	revisionFixture(t, map[string]revisionFixtureFile{
		"a.go": {body: "a\n"},
		"b.go": {body: "b\n"},
	}, map[string]revisionFixtureFile{
		"a.go": {body: "a2\n"},
		"b.go": {body: "b2\n"},
	})
	var rec recorder
	if _, err := (Client{Trace: rec.tracer()}).CompareRevisions("acme/repo", revisionBase, revisionHead); err != nil {
		t.Fatal(err)
	}
	groups := map[string]string{} // step prefix → group
	var counts []string
	for _, ev := range rec.events {
		if ev.Kind != TraceStart {
			continue
		}
		prefix := ev.Step
		if strings.HasPrefix(ev.Step, "file versions ") {
			prefix = "file versions"
			counts = append(counts, strings.TrimPrefix(ev.Step, "file versions "))
		}
		if g, ok := groups[prefix]; ok && g != ev.Group {
			t.Errorf("%s split over lines %s and %s", prefix, g, ev.Group)
		}
		groups[prefix] = ev.Group
	}
	sort.Strings(counts) // fetched side by side
	if strings.Join(counts, ",") != "1/4,2/4,3/4,4/4" {
		t.Errorf("blob counts = %v, want 1/4 … 4/4", counts)
	}
	for _, step := range []string{"compare trees", "file versions", "build compare"} {
		if groups[step] == "" {
			t.Errorf("no %q line", step)
		}
	}
}

func TestNilTracerReportsNothing(t *testing.T) {
	var tr *Tracer
	tr.Mark("x")
	tr.end(tr.start("s", tr.group(), "gh", nil), nil)
	client := Client{runOverride: func([]byte, ...string) (string, error) { return "", nil }}
	if _, err := client.traced("diff").Diff(1); err != nil {
		t.Fatal(err)
	}
}
