package ghsrc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Fake gh records requests and serves ordered responses, so even mutation tests
// never contact GitHub or depend on the developer's authenticated account.
func fakeThreadsGH(t *testing.T, responses ...string) string {
	t.Helper()
	dir := t.TempDir()
	for i, response := range responses {
		if err := os.WriteFile(filepath.Join(dir, "response"+strconv.Itoa(i)), []byte(response), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	script := `#!/bin/sh
set -eu
dir="$KRV_THREADS_GH_TEST"
n=0
if test -f "$dir/count"; then n=$(cat "$dir/count"); fi
printf '%s' "$((n + 1))" > "$dir/count"
printf '%s\n' "$@" > "$dir/args$n"
printf '%s' "${GH_HOST-}" > "$dir/host$n"
pwd > "$dir/pwd$n"
cat > "$dir/input$n"
cat "$dir/response$n"
`
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KRV_THREADS_GH_TEST", dir)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func readThreadRequest(t *testing.T, dir string, index int) struct {
	Query     string
	Variables map[string]any
} {
	t.Helper()
	var request struct {
		Query     string
		Variables map[string]any
	}
	data, err := os.ReadFile(filepath.Join(dir, "input"+strconv.Itoa(index)))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &request); err != nil {
		t.Fatal(err)
	}
	return request
}

func TestReviewsAcrossPagesAndLatestSubmitted(t *testing.T) {
	dir := fakeThreadsGH(t, `[
 {"id":3,"commit_id":"older","submitted_at":"2026-09-06T10:00:00Z","state":"COMMENTED","user":{"login":"ann"}},
 {"id":5,"commit_id":"dismissed","submitted_at":"2026-09-07T10:00:00Z","state":"DISMISSED","user":{"login":"Ann"}}
]
[
 {"id":7,"commit_id":"pending","state":"PENDING","user":{"login":"ann"}},
 {"id":9,"commit_id":"someone-else","submitted_at":"2026-09-07T12:00:00Z","state":"APPROVED","user":{"login":"bo"}}
]`)
	reviews, err := (Client{}).Reviews("acme/elsewhere", 42)
	if err != nil {
		t.Fatal(err)
	}
	if len(reviews) != 4 {
		t.Fatalf("got %d reviews", len(reviews))
	}
	latest := LatestReview(reviews, "ann")
	if latest == nil || latest.ID != 5 || latest.CommitID != "dismissed" {
		t.Fatalf("latest = %+v", latest)
	}
	if LatestReview(reviews, "nobody") != nil || LatestReview(reviews, "") != nil {
		t.Fatal("invented a reviewer baseline")
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args0"))
	if !strings.Contains(string(args), "--paginate\nrepos/acme/elsewhere/pulls/42/reviews") {
		t.Fatalf("args = %s", args)
	}
}

func TestLatestReviewUnorderedAndPending(t *testing.T) {
	now := time.Now()
	reviews := []SubmittedReview{
		{ID: 2, Author: "ann", State: "APPROVED", SubmittedAt: now},
		{ID: 4, Author: "ann", State: "PENDING", SubmittedAt: now.Add(time.Hour)},
		{ID: 3, Author: "ann", State: "COMMENTED", SubmittedAt: now},
		{ID: 1, Author: "ann", State: "COMMENTED", SubmittedAt: now.Add(-time.Hour)},
	}
	if got := LatestReview(reviews, "ann"); got == nil || got.ID != 3 {
		t.Fatalf("latest = %+v", got)
	}
}

func TestReviewsRejectMalformedLaterPage(t *testing.T) {
	fakeThreadsGH(t, `[] {broken`)
	if _, err := (Client{}).Reviews("a/b", 1); err == nil {
		t.Fatal("accepted truncated history")
	}
}

func TestThreadReplyPreservesBodyAndTarget(t *testing.T) {
	dir := fakeThreadsGH(t, `{"id":123,"body":"posted","in_reply_to_id":99,"original_commit_id":"old"}`)
	body := "fixed?\n\n`literal` $(literal) \"quoted\""
	got, err := (Client{}).Reply("acme/remote", 42, 99, body)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 123 || got.InReplyTo != 99 || got.OriginalCommitID != "old" {
		t.Fatalf("reply = %+v", got)
	}
	input, _ := os.ReadFile(filepath.Join(dir, "input0"))
	var payload map[string]string
	if err := json.Unmarshal(input, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["body"] != body {
		t.Fatalf("body = %q", payload["body"])
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args0"))
	if !strings.Contains(string(args), "POST\nrepos/acme/remote/pulls/42/comments/99/replies") {
		t.Fatalf("args = %s", args)
	}
}

func TestThreadResolutionExplicitAndConfirmed(t *testing.T) {
	for _, resolved := range []bool{true, false} {
		t.Run(strconv.FormatBool(resolved), func(t *testing.T) {
			dir := fakeThreadsGH(t, `{"data":{"result":{"thread":{"id":"T1","isResolved":`+strconv.FormatBool(resolved)+`}}}}`)
			if err := (Client{}).SetThreadResolved("T1", resolved); err != nil {
				t.Fatal(err)
			}
			request := readThreadRequest(t, dir, 0)
			mutation := "resolveReviewThread"
			if !resolved {
				mutation = "unresolveReviewThread"
			}
			if !strings.Contains(request.Query, "result: "+mutation+"(") || request.Variables["id"] != "T1" {
				t.Fatalf("request = %+v", request)
			}
		})
	}
}

func TestThreadResolutionRejectsPartialOrMismatchedResult(t *testing.T) {
	for _, response := range []string{
		`{"data":{"result":null},"errors":[{"message":"permission denied"}]}`,
		`{"data":{"result":null}}`,
		`{"data":{"result":{"thread":{"id":"other","isResolved":true}}}}`,
		`{"data":{"result":{"thread":{"id":"T1","isResolved":false}}}}`,
	} {
		t.Run(response, func(t *testing.T) {
			fakeThreadsGH(t, response)
			if err := (Client{}).SetThreadResolved("T1", true); err == nil {
				t.Fatal("accepted unconfirmed resolution")
			}
		})
	}
}

func TestReviewSnapshotUsesBrowserBaselineAndRetriesWholeLoad(t *testing.T) {
	head := strings.Repeat("a", 40)
	moved := strings.Repeat("b", 40)
	for _, unavailable := range []bool{false, true} {
		t.Run(fmt.Sprint(unavailable), func(t *testing.T) {
			prCalls, histories := 0, 0
			client := Client{runOverride: func(stdin []byte, args ...string) (string, error) {
				command := strings.Join(args, " ")
				switch {
				case isPRQuery(stdin):
					prCalls++
					sha := head
					if prCalls > 1 {
						sha = moved
					}
					return prResponse(sha), nil
				case strings.HasPrefix(command, "pr diff"):
					return "", nil
				case strings.Contains(command, "/comments"):
					return `[{"id":1,"path":"deleted.go","body":"keep guard","user":{"login":"reviewer"},"diff_hunk":"original context"}]`, nil
				case strings.Contains(command, "graphql"):
					return `[{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[{"id":"thread","path":"deleted.go","isResolved":true,"isOutdated":true,"viewerCanUnresolve":true,"comments":{"nodes":[{"databaseId":1}]}}]}}}}}]`, nil
				case strings.Contains(command, "/reviews"):
					histories++
					return fmt.Sprintf(`[{"id":5,"commit_id":%q,"submitted_at":"2026-09-07T10:00:00Z","state":"COMMENTED","user":{"login":"reviewer"}}]`, head), nil
				case strings.Contains(command, "/git/trees/"):
					if unavailable {
						return "", fmt.Errorf("historical object missing")
					}
					return fmt.Sprintf(`{"sha":%q,"truncated":false,"tree":[]}`, head), nil
				case strings.Contains(command, "api user"):
					return "reviewer", nil
				default:
					return "", fmt.Errorf("unexpected command %s", command)
				}
			}}
			snapshot, err := client.ReviewSnapshot("a/b", 1)
			if err != nil {
				t.Fatal(err)
			}
			if prCalls != 4 || histories != 2 || snapshot.HeadSHA != moved || snapshot.Baseline == nil || snapshot.Baseline.ID != 5 {
				t.Fatalf("mixed snapshot: %+v calls=%d histories=%d", snapshot, prCalls, histories)
			}
			thread := snapshot.Threads.Threads[0]
			if !thread.Resolved || !thread.Outdated || !thread.ViewerCanUnresolve || thread.Comments[0].DiffHunk != "original context" {
				t.Fatalf("thread context lost: %+v", thread)
			}
			if unavailable && (snapshot.Comparison != nil || !strings.Contains(snapshot.ComparisonError, "unavailable")) {
				t.Fatal("invented comparison")
			}
			if !unavailable && (snapshot.Comparison == nil || snapshot.Comparison.HeadSHA != moved) {
				t.Fatal("missing exact comparison")
			}
		})
	}
}

func TestPinnedSubmissionRejectsMovedHeadAndGitHubFailure(t *testing.T) {
	for _, moved := range []bool{false, true} {
		posted := false
		client := Client{runOverride: func(input []byte, args ...string) (string, error) {
			if isPRQuery(input) {
				head := "reviewed"
				if moved {
					head = "new"
				}
				return prResponse(head), nil
			}
			posted = true
			var request reviewRequest
			if err := json.Unmarshal(input, &request); err != nil {
				t.Fatal(err)
			}
			if request.CommitID != "reviewed" {
				t.Fatal("missing reviewed commit")
			}
			return "", fmt.Errorf("GitHub rejected review")
		}}
		if err := client.SubmitReviewAt("a/b", 1, "reviewed", EventApprove, "", nil); err == nil {
			t.Fatal("failure hidden")
		}
		if posted == moved {
			t.Fatal("incorrect post after head check")
		}
	}
}

func TestThreadStateErrorsDoNotClaimKnownResolution(t *testing.T) {
	_, err := parseThreadStates(`[{"errors":[{"message":"permission denied"}],"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}]`)
	if err == nil {
		t.Fatal("partial GraphQL response claimed known resolution")
	}
}
