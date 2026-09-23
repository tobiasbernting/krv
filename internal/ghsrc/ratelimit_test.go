package ghsrc

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeClock stands in for sleep and now: sleeping moves the clock instead of
// waiting, and records how long was asked for.
func fakeClock(t *testing.T) *[]time.Duration {
	t.Helper()
	var mu sync.Mutex
	var slept []time.Duration
	clock := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	oldSleep, oldNow := sleep, now
	sleep = func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		slept = append(slept, d)
		clock = clock.Add(d)
	}
	now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return clock
	}
	t.Cleanup(func() { sleep, now = oldSleep, oldNow })
	return &slept
}

func TestSecondaryRateLimitWaitsAMinuteAndRetries(t *testing.T) {
	slept := fakeClock(t)
	var rec recorder
	calls := 0
	client := Client{Trace: rec.tracer(), throttle: newThrottle(), runOverride: func(_ []byte, args ...string) (string, error) {
		calls++
		if calls == 1 {
			return "", errors.New("gh pr diff 1: HTTP 403: You have exceeded a secondary rate limit")
		}
		return "diff", nil
	}}
	out, err := client.traced("diff").Diff(1)
	if err != nil || out != "diff" {
		t.Fatalf("got %q, %v; want the retry's diff", out, err)
	}
	if len(*slept) != 1 || (*slept)[0] != time.Minute {
		t.Errorf("slept %v, want one minute", *slept)
	}
	if !client.throttle.serial {
		t.Error("calls after a burst limit still run side by side")
	}
	if !strings.Contains(strings.Join(rec.steps(), ","), "rate limited") {
		t.Errorf("the wait is not on the panel: %q", rec.steps())
	}
}

func TestPrimaryRateLimitWaitsForAResetThatIsClose(t *testing.T) {
	fakeClock(t)
	reset := now().Add(30 * time.Second).Unix()
	calls := 0
	client := Client{runOverride: func(_ []byte, args ...string) (string, error) {
		if strings.Join(args, " ") == "api rate_limit --jq .resources.core.reset" {
			return fmt.Sprint(reset), nil
		}
		calls++
		if calls == 1 {
			return "", errors.New("HTTP 403: API rate limit exceeded for user ID 1.")
		}
		return "ok", nil
	}}
	if out, err := client.run("api", "user"); err != nil || out != "ok" {
		t.Fatalf("got %q, %v; want a retry after the reset", out, err)
	}
}

func TestPrimaryRateLimitFarFromResetSaysWhen(t *testing.T) {
	slept := fakeClock(t)
	reset := now().Add(40 * time.Minute)
	client := Client{runOverride: func(_ []byte, args ...string) (string, error) {
		if args[0] == "api" && args[1] == "rate_limit" {
			return fmt.Sprint(reset.Unix()), nil
		}
		return "", errors.New("HTTP 403: API rate limit exceeded for user ID 1.")
	}}
	_, err := client.run("api", "user")
	if err == nil || !strings.Contains(err.Error(), "hourly API limit") || !strings.Contains(err.Error(), reset.Local().Format("15:04")) {
		t.Fatalf("err = %v, want it to say the limit resets at %s", err, reset.Local().Format("15:04"))
	}
	if len(*slept) != 0 {
		t.Errorf("waited %v for a reset 40 minutes off", *slept)
	}
}

func TestRateLimitGivesUpAfterTwoWaits(t *testing.T) {
	slept := fakeClock(t)
	client := Client{runOverride: func([]byte, ...string) (string, error) {
		return "", errors.New("HTTP 429: too many requests")
	}}
	if _, err := client.run("api", "user"); err == nil {
		t.Fatal("a limit that never lifts did not fail")
	}
	if len(*slept) != rateLimitRetries {
		t.Errorf("waited %d times, want %d", len(*slept), rateLimitRetries)
	}
}

func TestOtherErrorsAreNotRetried(t *testing.T) {
	slept := fakeClock(t)
	calls := 0
	client := Client{runOverride: func([]byte, ...string) (string, error) {
		calls++
		return "", errors.New("HTTP 404: Not Found")
	}}
	if _, err := client.run("api", "user"); err == nil || calls != 1 || len(*slept) != 0 {
		t.Errorf("err=%v calls=%d slept=%v; want one failed call", err, calls, *slept)
	}
}

func TestSnapshotFetchesDiffAndThreadsSideBySide(t *testing.T) {
	// The diff answers only once threads has started: run in sequence, the
	// load would wait here until the timeout.
	threadsStarted := make(chan struct{})
	var once sync.Once
	client := Client{runOverride: func(_ []byte, args ...string) (string, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.HasPrefix(joined, "pr view"):
			return `{"number":1,"headRefOid":"a"}`, nil
		case strings.HasPrefix(joined, "pr diff"):
			select {
			case <-threadsStarted:
				return "", nil
			case <-time.After(2 * time.Second):
				return "", errors.New("threads never started while the diff was fetched")
			}
		case strings.Contains(joined, "/pulls/1/comments"):
			once.Do(func() { close(threadsStarted) })
			return "[]", nil
		case strings.Contains(joined, "api graphql"):
			return `[{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}]`, nil
		}
		return "", fmt.Errorf("unexpected command: %s", joined)
	}}
	if _, err := client.Snapshot("acme/x", 1); err != nil {
		t.Fatal(err)
	}
}
