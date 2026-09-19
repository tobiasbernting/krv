package ghsrc

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// CheckStatus is a Check's outcome, normalized across GitHub's two kinds of
// Check: check runs (status plus conclusion) and commit statuses (one state).
type CheckStatus int

const (
	CheckPass    CheckStatus = iota
	CheckFail                // failure, startup failure; a status's error too
	CheckRunning             // queued, in progress, waiting, pending, expected
	CheckSkipped             // skipped or neutral
	CheckStopped             // cancelled, timed out, action required, stale
)

// Check is one CI status or check run on the head commit.
type Check struct {
	Name     string
	Workflow string // the Actions workflow, "" for a commit status or when unknown
	Status   CheckStatus

	// StartedAt is when a check run started, or when a commit status was
	// posted. CompletedAt is zero until a check run completes, and always for
	// a commit status, which reports no end.
	StartedAt   time.Time
	CompletedAt time.Time
	URL         string

	// Required reports branch protection requires this Check. It is always
	// false when the PR's RequiredKnown is false.
	Required bool
}

// Duration is how long the Check took: completed minus started, or for one
// still running the time since it started as of now. Zero when unknown.
func (c Check) Duration(now time.Time) time.Duration {
	switch {
	case c.StartedAt.IsZero():
		return 0
	case !c.CompletedAt.IsZero():
		return max(0, c.CompletedAt.Sub(c.StartedAt))
	case c.Status == CheckRunning:
		return max(0, now.Sub(c.StartedAt))
	}
	return 0
}

// checkRunStatus maps a check run's status and conclusion. Anything not
// completed is running; an unknown conclusion is treated as stopped, not as
// a pass.
func checkRunStatus(status, conclusion string) CheckStatus {
	if status != "COMPLETED" {
		return CheckRunning
	}
	switch conclusion {
	case "SUCCESS":
		return CheckPass
	case "FAILURE", "STARTUP_FAILURE":
		return CheckFail
	case "NEUTRAL", "SKIPPED":
		return CheckSkipped
	}
	return CheckStopped
}

// statusContextStatus maps a commit status's state.
func statusContextStatus(state string) CheckStatus {
	switch state {
	case "SUCCESS":
		return CheckPass
	case "FAILURE", "ERROR":
		return CheckFail
	case "PENDING", "EXPECTED":
		return CheckRunning
	}
	return CheckStopped
}

// SortChecks orders Checks for reading: failing, then running, then the
// rest; within each group required before optional, then by name.
func SortChecks(checks []Check) {
	group := func(s CheckStatus) int {
		switch s {
		case CheckFail:
			return 0
		case CheckRunning:
			return 1
		}
		return 2
	}
	sort.SliceStable(checks, func(i, j int) bool {
		a, b := checks[i], checks[j]
		if ga, gb := group(a.Status), group(b.Status); ga != gb {
			return ga < gb
		}
		if a.Required != b.Required {
			return a.Required
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Workflow < b.Workflow
	})
}

// CheckSummary counts Checks by outcome.
type CheckSummary struct {
	Failing, Running, Passed, Skipped, Other int
	RequiredFailing                          int
}

func SummarizeChecks(checks []Check) CheckSummary {
	var s CheckSummary
	for _, c := range checks {
		switch c.Status {
		case CheckFail:
			s.Failing++
			if c.Required {
				s.RequiredFailing++
			}
		case CheckRunning:
			s.Running++
		case CheckPass:
			s.Passed++
		case CheckSkipped:
			s.Skipped++
		default:
			s.Other++
		}
	}
	return s
}

// String is the summary line, "3 failing, 1 running, 12 passed · 2 required
// failing", leaving out every zero count. It is "" when there are no Checks.
func (s CheckSummary) String() string {
	var parts []string
	for _, p := range []struct {
		n     int
		label string
	}{
		{s.Failing, "failing"}, {s.Running, "running"}, {s.Passed, "passed"},
		{s.Skipped, "skipped"}, {s.Other, "other"},
	} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", p.n, p.label))
		}
	}
	line := strings.Join(parts, ", ")
	if s.RequiredFailing > 0 {
		line += fmt.Sprintf(" · %d required failing", s.RequiredFailing)
	}
	return line
}
