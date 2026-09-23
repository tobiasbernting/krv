package ghsrc

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GitHub has two limits. The primary one is a budget per hour (5000 REST
// calls for a user); once spent, every call fails until its reset time.
// Secondary limits catch bursts: too many requests at once, or per minute.
// GitHub asks clients that hit one to wait at least a minute when no
// Retry-After is given, which gh does not show us, and then to slow down.
const (
	// secondaryWait is how long a burst limit is waited out.
	secondaryWait = time.Minute
	// longestWait is the most a load waits for the hourly budget to reset
	// before giving up and saying when to try again.
	longestWait = 90 * time.Second
	// rateLimitRetries is how many times one call waits and retries.
	rateLimitRetries = 2
)

// sleep and now are variables so tests can wait without waiting.
var (
	sleep = time.Sleep
	now   = time.Now
)

// throttle is shared by every copy of a client within one load, so a limit
// one command hits slows all of them: the calls still to start wait out the
// same pause, and from then on run one at a time.
type throttle struct {
	mu     sync.Mutex
	until  time.Time
	serial bool
	one    sync.Mutex // held around each call once serial
}

func newThrottle() *throttle { return &throttle{} }

// acquire waits until a call may start, and returns what to call when it
// ends. A nil throttle never waits.
func (t *throttle) acquire() func() {
	if t == nil {
		return func() {}
	}
	t.mu.Lock()
	wait, serial := t.until.Sub(now()), t.serial
	t.mu.Unlock()
	if wait > 0 {
		sleep(wait)
	}
	if !serial {
		return func() {}
	}
	t.one.Lock()
	return t.one.Unlock
}

// hold pauses every call until wait has passed, and makes the rest serial.
func (t *throttle) hold(wait time.Duration) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if until := now().Add(wait); until.After(t.until) {
		t.until = until
	}
	t.serial = true
}

// rateLimited sorts a gh failure: primary is the hourly budget, secondary a
// burst limit.
func rateLimited(err error) (limited, primary bool) {
	if err == nil {
		return false, false
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "secondary rate limit"), strings.Contains(msg, "abuse detection"),
		strings.Contains(msg, "submitted too quickly"), strings.Contains(msg, "http 429"):
		return true, false
	case strings.Contains(msg, "api rate limit exceeded"), strings.Contains(msg, "rate limit exceeded"):
		return true, true
	}
	return false, false
}

// rateLimitWait is how long to wait before retrying err, or an error saying
// why not to. Asking GitHub when the budget resets does not spend it.
func (c Client) rateLimitWait(err error, attempt int) (time.Duration, bool, error) {
	limited, primary := rateLimited(err)
	if !limited || attempt >= rateLimitRetries {
		return 0, false, nil
	}
	if !primary {
		return secondaryWait, true, nil
	}
	out, rerr := c.runOnce(nil, "api", "rate_limit", "--jq", ".resources.core.reset")
	if rerr != nil {
		return 0, false, nil
	}
	reset, perr := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if perr != nil {
		return 0, false, nil
	}
	at := time.Unix(reset, 0)
	wait := at.Sub(now()) + time.Second
	if wait > longestWait {
		return 0, false, fmt.Errorf("GitHub's hourly API limit is spent until %s — try again then: %w", at.Local().Format("15:04"), err)
	}
	return max(wait, time.Second), true, nil
}
