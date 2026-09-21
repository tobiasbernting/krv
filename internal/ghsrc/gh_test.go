package ghsrc

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// gh --paginate concatenates one array per page; a decoder that assumes a
// single array silently drops every page after the first.
func TestParseCommentsAcrossPages(t *testing.T) {
	out := `[{"id":1,"path":"a.go","line":10,"position":3,"user":{"login":"ann"},"body":"first"}]
[{"id":2,"path":"b.go","line":4,"position":null,"user":{"login":"bo"},"body":"second"}]`

	got, err := parseComments(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d comments, want 2 — later pages were dropped", len(got))
	}
	if got[0].User.Login != "ann" || got[0].Path != "a.go" {
		t.Errorf("first comment = %+v", got[0])
	}
	if got[0].Outdated() {
		t.Error("comment with a position reported as outdated")
	}
	// A null position is GitHub saying the comment no longer anchors to the
	// diff, which happens after a force-push.
	if !got[1].Outdated() {
		t.Error("comment with a null position not reported as outdated")
	}
}

func TestParseCommentsEmpty(t *testing.T) {
	got, err := parseComments("[]")
	if err != nil || len(got) != 0 {
		t.Errorf("got %v, %v; want no comments and no error", got, err)
	}
}

func TestGroupCommentsBuildsThreads(t *testing.T) {
	live := 2
	comments := []Comment{
		{ID: 10, Path: "a.go", Line: 4, Position: &live, CreatedAt: "2026-01-01T10:00:00Z"},
		{ID: 12, InReplyTo: 10, Body: "later", CreatedAt: "2026-01-01T12:00:00Z"},
		{ID: 11, InReplyTo: 10, Body: "earlier", CreatedAt: "2026-01-01T11:00:00Z"},
		{ID: 20, Path: "gone.go", Line: 8, Position: nil},
	}

	got := groupComments(comments)
	if len(got) != 2 {
		t.Fatalf("got %d threads, want 2", len(got))
	}
	if got[0].RootID != 10 || got[0].Path != "a.go" || got[0].Outdated {
		t.Errorf("first thread = %+v", got[0])
	}
	if len(got[0].Comments) != 3 || got[0].Comments[1].ID != 11 {
		t.Errorf("replies not grouped in creation order: %+v", got[0].Comments)
	}
	if !got[1].Outdated {
		t.Error("REST fallback lost outdated state")
	}
}

func TestParseThreadStatesAcrossPages(t *testing.T) {
	out := `[{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[{"id":"T1","path":"a.go","line":4,"startLine":2,"diffSide":"RIGHT","isOutdated":false,"isResolved":true,"comments":{"nodes":[{"databaseId":10}]}}]}}}}},{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[{"id":"T2","path":"b.go","line":8,"diffSide":"LEFT","isOutdated":true,"isResolved":false,"comments":{"nodes":[{"databaseId":20}]}}]}}}}}]`

	got, err := parseThreadStates(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d states, want 2", len(got))
	}
	if got[0].RootID != 10 || !got[0].Resolved || got[0].Outdated || got[0].StartLine != 2 {
		t.Errorf("first state = %+v", got[0])
	}
	if !got[1].Outdated || got[1].Resolved || got[1].Side != "LEFT" {
		t.Errorf("second state = %+v", got[1])
	}
}

// A response GitHub declined to answer must not read as a pull request with no
// threads: Threads would then mark every discussion unresolved and still claim
// the resolution state was known. Ported from the GraphQL-paginated thread
// loader that --paginate replaced.
func TestParseThreadStatesRejectMalformedResponses(t *testing.T) {
	for name, out := range map[string]string{
		"graphql error":    `[{"errors":[{"message":"forbidden"}],"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}]`,
		"null data":        `[{"data":null}]`,
		"null repository":  `[{"data":{"repository":null}}]`,
		"null pullRequest": `[{"data":{"repository":{"pullRequest":null}}}]`,
		"not json":         `not json`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseThreadStates(out); err == nil {
				t.Fatalf("accepted %s as an answer", name)
			}
		})
	}
}

// An empty pull request is not malformed: it has to come back as no states and
// no error, or every clean PR would lose its resolution state.
func TestParseThreadStatesAcceptEmptyPullRequest(t *testing.T) {
	got, err := parseThreadStates(`[{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}]`)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v; want no states and no error", got, err)
	}
}

func TestSnapshotRetriesWhenHeadMoves(t *testing.T) {
	var prCalls, diffCalls int
	client := Client{runOverride: func(stdin []byte, args ...string) (string, error) {
		joined := strings.Join(args, " ")
		switch {
		case isPRQuery(stdin):
			shas := []string{"a", "b", "c", "c"}
			sha := shas[prCalls]
			prCalls++
			return prResponse(sha), nil
		case strings.HasPrefix(joined, "pr diff"):
			diffCalls++
			return "", nil
		case strings.Contains(joined, "/pulls/1/comments"):
			return "[]", nil
		case strings.Contains(joined, "api graphql"):
			return `[{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}]`, nil
		default:
			return "", fmt.Errorf("unexpected command: %s", joined)
		}
	}}

	snapshot, err := client.Snapshot("acme/x", 1)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.HeadSHA != "c" || prCalls != 4 || diffCalls != 2 {
		t.Errorf("snapshot=%+v prCalls=%d diffCalls=%d", snapshot, prCalls, diffCalls)
	}
}

func TestThreadsFallsBackToRESTWithoutResolution(t *testing.T) {
	client := Client{runOverride: func(_ []byte, args ...string) (string, error) {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "/pulls/1/comments") {
			return `[{"id":1,"path":"a.go","line":3,"position":2,"body":"visible"}]`, nil
		}
		if strings.Contains(joined, "api graphql") {
			return "", fmt.Errorf("field reviewThreads does not exist")
		}
		return "", fmt.Errorf("unexpected command: %s", joined)
	}}

	feed, err := client.Threads("acme/x", 1)
	if err != nil {
		t.Fatal(err)
	}
	if feed.ResolutionKnown || len(feed.Threads) != 1 || feed.Threads[0].ResolutionKnown {
		t.Errorf("fallback feed = %+v", feed)
	}
}

func TestReviewPayloadShape(t *testing.T) {
	payload, err := reviewPayload("", EventRequestChanges, "needs work", []ReviewComment{
		{Path: "a.go", Line: 12, Body: "nil check", Side: "RIGHT"},
		{Path: "b.go", Line: 30, StartLine: 24, Body: "extract", Side: "RIGHT", StartSide: "RIGHT"},
	})
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatal(err)
	}
	if got["event"] != "REQUEST_CHANGES" || got["body"] != "needs work" {
		t.Errorf("payload = %s", payload)
	}
	comments, _ := got["comments"].([]any)
	if len(comments) != 2 {
		t.Fatalf("got %d comments in payload, want 2", len(comments))
	}

	// A single-line comment must omit start_line entirely: sending
	// start_line == line is rejected by the API.
	first, _ := comments[0].(map[string]any)
	if _, ok := first["start_line"]; ok {
		t.Errorf("single-line comment carries start_line: %s", payload)
	}
	second, _ := comments[1].(map[string]any)
	if second["start_line"] != float64(24) {
		t.Errorf("multi-line comment lost start_line: %s", payload)
	}
}

func TestSubmitRejectsUnknownEvent(t *testing.T) {
	err := Client{}.SubmitReview("acme/x", 1, "MERGE", "", nil)
	if err == nil || !strings.Contains(err.Error(), "unknown review event") {
		t.Errorf("got %v, want an unknown-event error", err)
	}
}

func TestSubmitRejectsEmptyComment(t *testing.T) {
	if err := (Client{}).SubmitReview("acme/x", 1, EventComment, "", nil); err == nil {
		t.Error("expected an error when there is nothing to submit")
	}
	// Approving with no body and no comments is legitimate.
	if err := (Client{}).SubmitReview("acme/x", 1, EventApprove, "", nil); err != nil {
		if strings.Contains(err.Error(), "nothing to submit") {
			t.Error("a bare approval was rejected as empty")
		}
	}
}

// GitHub is inconsistent about the errors field: the reviews endpoint returns
// strings, validation failures elsewhere return objects. Both carry the only
// useful part of a 422.
func TestAPIErrorHandlesBothErrorShapes(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "string errors, as the reviews endpoint returns",
			body: `{"message":"Unprocessable Entity","errors":["Review Can not approve your own pull request"],"status":"422"}`,
			want: "Review Can not approve your own pull request",
		},
		{
			name: "object errors",
			body: `{"message":"Validation Failed","errors":[{"resource":"PullRequestReviewComment","field":"line","code":"invalid"}]}`,
			want: "Validation Failed; PullRequestReviewComment line (invalid)",
		},
		{
			name: "object errors carrying a message",
			body: `{"message":"Validation Failed","errors":[{"message":"line must be part of the diff"}]}`,
			want: "Validation Failed; line must be part of the diff",
		},
		{
			name: "message only",
			body: `{"message":"Not Found"}`,
			want: "Not Found",
		},
		{name: "not json", body: "<html>502</html>", want: ""},
		{name: "empty", body: "", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := apiError(tc.body); got != tc.want {
				t.Errorf("apiError = %q, want %q", got, tc.want)
			}
		})
	}
}
