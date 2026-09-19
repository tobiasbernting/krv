package ghsrc

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// isPRQuery tells PR's request apart from the other GraphQL requests a fake
// answers: it is the one sent on stdin asking for the check rollup.
func isPRQuery(stdin []byte) bool {
	return strings.Contains(string(stdin), "statusCheckRollup")
}

// prResponse is a PR answer with the given head and no Checks.
func prResponse(headSHA string) string {
	return fmt.Sprintf(`{"data":{"repository":{"pullRequest":{"number":1,"headRefOid":%q,"commits":{"nodes":[]}}}}}`, headSHA)
}

const checksFixture = `{"data":{"repository":{"pullRequest":{
  "number":7,"title":"Add checks","body":"Why.","state":"OPEN","url":"https://github.com/acme/x/pull/7",
  "baseRefName":"main","headRefName":"feat","headRefOid":"abc","isDraft":true,"author":{"login":"ann"},
  "commits":{"nodes":[{"commit":{"statusCheckRollup":{"contexts":{"nodes":[
    {"__typename":"CheckRun","name":"lint","status":"COMPLETED","conclusion":"SUCCESS",
     "startedAt":"2026-09-19T10:00:00Z","completedAt":"2026-09-19T10:01:30Z","detailsUrl":"https://ci/lint",
     "checkSuite":{"workflowRun":{"workflow":{"name":"CI"}}},"isRequired":false},
    {"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"FAILURE",
     "startedAt":"2026-09-19T10:00:00Z","completedAt":"2026-09-19T10:05:00Z","detailsUrl":"https://ci/test",
     "checkSuite":{"workflowRun":{"workflow":{"name":"CI"}}},"isRequired":true},
    {"__typename":"CheckRun","name":"build","status":"IN_PROGRESS","conclusion":null,
     "startedAt":"2026-09-19T10:00:00Z","completedAt":null,"detailsUrl":"https://ci/build",
     "checkSuite":{"workflowRun":null},"isRequired":false},
    {"__typename":"CheckRun","name":"deploy","status":"COMPLETED","conclusion":"SKIPPED",
     "startedAt":null,"completedAt":null,"detailsUrl":"https://ci/deploy","checkSuite":null,"isRequired":false},
    {"__typename":"CheckRun","name":"slow","status":"COMPLETED","conclusion":"TIMED_OUT",
     "startedAt":"2026-09-19T09:00:00Z","completedAt":"2026-09-19T10:00:00Z","detailsUrl":"https://ci/slow","isRequired":false},
    {"__typename":"StatusContext","context":"ci/legacy","state":"ERROR",
     "targetUrl":"https://legacy/1","createdAt":"2026-09-19T10:02:00Z","isRequired":true},
    {"__typename":"StatusContext","context":"coverage","state":"PENDING",
     "targetUrl":"https://cov/1","createdAt":"2026-09-19T10:03:00Z","isRequired":false}
  ]}}}}]}
}}}}`

func TestPRReadsHeaderAndChecks(t *testing.T) {
	var sent map[string]any
	client := Client{Repo: "acme/x", runOverride: func(stdin []byte, args ...string) (string, error) {
		if strings.Join(args, " ") != "api graphql --input -" {
			return "", fmt.Errorf("unexpected command: %v", args)
		}
		if err := json.Unmarshal(stdin, &sent); err != nil {
			t.Fatal(err)
		}
		return checksFixture, nil
	}}

	pr, err := client.PR(7)
	if err != nil {
		t.Fatal(err)
	}
	vars, _ := sent["variables"].(map[string]any)
	if vars["owner"] != "acme" || vars["name"] != "x" || vars["number"] != float64(7) {
		t.Errorf("variables = %v", vars)
	}
	if q, _ := sent["query"].(string); !strings.Contains(q, "isRequired(pullRequestNumber: $number)") {
		t.Error("query does not ask which checks are required")
	}
	if pr.Number != 7 || pr.Title != "Add checks" || pr.Body != "Why." || pr.State != "OPEN" ||
		pr.BaseRef != "main" || pr.HeadRef != "feat" || pr.HeadSHA != "abc" || pr.Author.Login != "ann" ||
		!pr.IsDraft || pr.URL != "https://github.com/acme/x/pull/7" {
		t.Errorf("header = %+v", pr)
	}
	if !pr.RequiredKnown {
		t.Error("required status reported unknown")
	}

	at := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	want := []Check{
		{Name: "ci/legacy", Status: CheckFail, StartedAt: at("2026-09-19T10:02:00Z"), URL: "https://legacy/1", Required: true},
		{Name: "test", Workflow: "CI", Status: CheckFail, StartedAt: at("2026-09-19T10:00:00Z"), CompletedAt: at("2026-09-19T10:05:00Z"), URL: "https://ci/test", Required: true},
		{Name: "build", Status: CheckRunning, StartedAt: at("2026-09-19T10:00:00Z"), URL: "https://ci/build"},
		{Name: "coverage", Status: CheckRunning, StartedAt: at("2026-09-19T10:03:00Z"), URL: "https://cov/1"},
		{Name: "deploy", Status: CheckSkipped, URL: "https://ci/deploy"},
		{Name: "lint", Workflow: "CI", Status: CheckPass, StartedAt: at("2026-09-19T10:00:00Z"), CompletedAt: at("2026-09-19T10:01:30Z"), URL: "https://ci/lint"},
		{Name: "slow", Status: CheckStopped, StartedAt: at("2026-09-19T09:00:00Z"), CompletedAt: at("2026-09-19T10:00:00Z"), URL: "https://ci/slow"},
	}
	if !reflect.DeepEqual(pr.Checks, want) {
		t.Errorf("checks =\n%+v\nwant\n%+v", pr.Checks, want)
	}
}

// Older GitHub Enterprise has no isRequired. The pull request must still
// load, with no Check claiming to be required.
func TestPRFallsBackWithoutIsRequired(t *testing.T) {
	var queries []string
	client := Client{Repo: "acme/x", runOverride: func(stdin []byte, args ...string) (string, error) {
		var req struct{ Query string }
		if err := json.Unmarshal(stdin, &req); err != nil {
			t.Fatal(err)
		}
		queries = append(queries, req.Query)
		if strings.Contains(req.Query, "isRequired") {
			return "", errors.New("gh api graphql --input -: gh: Field 'isRequired' doesn't exist on type 'CheckRun'")
		}
		return checksFixture, nil
	}}

	pr, err := client.PR(7)
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 2 || strings.Contains(queries[1], "isRequired") {
		t.Fatalf("queries = %d, retry still asks isRequired", len(queries))
	}
	if pr.RequiredKnown {
		t.Error("required status claimed known")
	}
	for _, c := range pr.Checks {
		if c.Required {
			t.Errorf("%s marked required with required status unknown", c.Name)
		}
	}
	if len(pr.Checks) != 7 {
		t.Errorf("got %d checks, want 7", len(pr.Checks))
	}
}

// Any other failure is not a reason to ask again.
func TestPRDoesNotRetryOtherFailures(t *testing.T) {
	calls := 0
	client := Client{Repo: "acme/x", runOverride: func([]byte, ...string) (string, error) {
		calls++
		return "", errors.New("HTTP 502")
	}}
	if _, err := client.PR(7); err == nil || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestPRWithoutRollupHasNoChecks(t *testing.T) {
	for name, commits := range map[string]string{
		"null rollup": `{"nodes":[{"commit":{"statusCheckRollup":null}}]}`,
		"no commits":  `{"nodes":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			client := Client{Repo: "acme/x", runOverride: func([]byte, ...string) (string, error) {
				return `{"data":{"repository":{"pullRequest":{"number":3,"headRefOid":"abc","commits":` + commits + `}}}}`, nil
			}}
			pr, err := client.PR(3)
			if err != nil {
				t.Fatal(err)
			}
			if len(pr.Checks) != 0 || !pr.RequiredKnown || pr.HeadSHA != "abc" {
				t.Errorf("pr = %+v", pr)
			}
		})
	}
}

func TestPRRejectsMissingPullRequest(t *testing.T) {
	for name, out := range map[string]string{
		"null pullRequest": `{"data":{"repository":{"pullRequest":null}}}`,
		"null repository":  `{"data":{"repository":null}}`,
		"graphql error":    `{"errors":[{"message":"Could not resolve to a PullRequest"}],"data":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			client := Client{Repo: "acme/x", runOverride: func([]byte, ...string) (string, error) { return out, nil }}
			if _, err := client.PR(3); err == nil {
				t.Fatal("accepted a missing pull request")
			}
		})
	}
}

// Without an explicit repository, PR asks gh which one the directory is.
func TestPRResolvesWorkingDirectoryRepo(t *testing.T) {
	client := Client{runOverride: func(stdin []byte, args ...string) (string, error) {
		if strings.HasPrefix(strings.Join(args, " "), "repo view") {
			return "acme/here\n", nil
		}
		var req struct{ Variables map[string]any }
		if err := json.Unmarshal(stdin, &req); err != nil {
			t.Fatal(err)
		}
		if req.Variables["owner"] != "acme" || req.Variables["name"] != "here" {
			t.Errorf("variables = %v", req.Variables)
		}
		return prResponse("abc"), nil
	}}
	if _, err := client.PR(1); err != nil {
		t.Fatal(err)
	}
}

func TestCheckStatusMapping(t *testing.T) {
	for _, tc := range []struct{ status, conclusion string }{
		{"QUEUED", ""}, {"IN_PROGRESS", ""}, {"WAITING", ""}, {"PENDING", ""}, {"REQUESTED", ""},
	} {
		if got := checkRunStatus(tc.status, tc.conclusion); got != CheckRunning {
			t.Errorf("%s = %v, want running", tc.status, got)
		}
	}
	for conclusion, want := range map[string]CheckStatus{
		"SUCCESS": CheckPass, "FAILURE": CheckFail, "STARTUP_FAILURE": CheckFail,
		"NEUTRAL": CheckSkipped, "SKIPPED": CheckSkipped,
		"CANCELLED": CheckStopped, "TIMED_OUT": CheckStopped, "ACTION_REQUIRED": CheckStopped,
		"STALE": CheckStopped, "": CheckStopped,
	} {
		if got := checkRunStatus("COMPLETED", conclusion); got != want {
			t.Errorf("completed %q = %v, want %v", conclusion, got, want)
		}
	}
	for state, want := range map[string]CheckStatus{
		"SUCCESS": CheckPass, "FAILURE": CheckFail, "ERROR": CheckFail,
		"PENDING": CheckRunning, "EXPECTED": CheckRunning,
	} {
		if got := statusContextStatus(state); got != want {
			t.Errorf("status %q = %v, want %v", state, got, want)
		}
	}
}

func TestSortChecks(t *testing.T) {
	checks := []Check{
		{Name: "a-pass", Status: CheckPass},
		{Name: "z-fail", Status: CheckFail},
		{Name: "b-skip", Status: CheckSkipped, Required: true},
		{Name: "a-fail", Status: CheckFail},
		{Name: "m-fail", Status: CheckFail, Required: true},
		{Name: "run", Status: CheckRunning},
		{Name: "c-stop", Status: CheckStopped},
	}
	SortChecks(checks)
	var got []string
	for _, c := range checks {
		got = append(got, c.Name)
	}
	want := []string{"m-fail", "a-fail", "z-fail", "run", "b-skip", "a-pass", "c-stop"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestSummarizeChecks(t *testing.T) {
	var checks []Check
	add := func(n int, c Check) {
		for range n {
			checks = append(checks, c)
		}
	}
	add(2, Check{Status: CheckFail, Required: true})
	add(1, Check{Status: CheckFail})
	add(1, Check{Status: CheckRunning})
	add(12, Check{Status: CheckPass})
	got := SummarizeChecks(checks)
	if want := (CheckSummary{Failing: 3, Running: 1, Passed: 12, RequiredFailing: 2}); got != want {
		t.Fatalf("summary = %+v, want %+v", got, want)
	}
	if s := got.String(); s != "3 failing, 1 running, 12 passed · 2 required failing" {
		t.Errorf("line = %q", s)
	}

	for _, tc := range []struct {
		s    CheckSummary
		want string
	}{
		{CheckSummary{}, ""},
		{CheckSummary{Passed: 4}, "4 passed"},
		{CheckSummary{Passed: 1, Skipped: 2, Other: 1}, "1 passed, 2 skipped, 1 other"},
	} {
		if got := tc.s.String(); got != tc.want {
			t.Errorf("%+v = %q, want %q", tc.s, got, tc.want)
		}
	}
}

func TestCheckDuration(t *testing.T) {
	start := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	now := start.Add(7 * time.Minute)
	for name, tc := range map[string]struct {
		check Check
		want  time.Duration
	}{
		"completed":   {Check{Status: CheckPass, StartedAt: start, CompletedAt: start.Add(90 * time.Second)}, 90 * time.Second},
		"running":     {Check{Status: CheckRunning, StartedAt: start}, 7 * time.Minute},
		"not started": {Check{Status: CheckRunning}, 0},
		"status":      {Check{Status: CheckFail, StartedAt: start}, 0},
	} {
		if got := tc.check.Duration(now); got != tc.want {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
}
