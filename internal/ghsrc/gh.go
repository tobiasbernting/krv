// Package ghsrc talks to GitHub by shelling out to the gh CLI.
//
// gh already solves authentication, enterprise hosts and rate limiting, and
// it is the tool the user is already logged into. Reimplementing that against
// the REST API would mean owning three problems for no gain.
package ghsrc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// ErrNotInstalled is returned when gh is missing, so callers can tell the
// difference between "no GitHub" and "GitHub said no".
var ErrNotInstalled = errors.New("gh not found — install it: https://cli.github.com")

type Client struct {
	// Host overrides the GitHub hostname. Empty means gh's own configuration
	// decides, which is what makes a personal account and an enterprise host
	// both work without special-casing either.
	Host string
	Dir  string // repository directory, so gh resolves the right remote

	// Repo names a repository explicitly, as "owner/name". The queue lists
	// pull requests from every repository you are involved in, which are
	// mostly not the one you are standing in.
	Repo string

	// runOverride is an internal seam for exercising multi-command operations
	// without invoking gh. Production clients leave it nil.
	runOverride func(stdin []byte, args ...string) (string, error)

	// Trace, when set, hears about every command the client runs, labelled
	// with the step it belongs to; the loading page shows them.
	Trace *Tracer
	// step and group label the commands run, set by traced.
	step, group string
}

// Preflight checks that gh exists and is authenticated. It is called once per
// session: a clear message here beats an exec error surfacing from deep
// inside a diff fetch.
func (c Client) Preflight() error {
	if _, err := exec.LookPath("gh"); err != nil {
		return ErrNotInstalled
	}
	args := []string{"auth", "status"}
	if c.Host != "" {
		args = append(args, "--hostname", c.Host)
	}
	if _, err := c.run(args...); err != nil {
		host := c.Host
		if host == "" {
			host = "github.com"
		}
		return fmt.Errorf("gh is not authenticated for %s — run: gh auth login --hostname %s", host, host)
	}
	return nil
}

// CurrentRepo is the "owner/name" of the repository in the working directory.
func (c Client) CurrentRepo() (string, error) {
	if c.Repo != "" {
		return c.Repo, nil
	}
	out, err := c.run("repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner")
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(out)
	if name == "" {
		return "", errors.New("could not determine the repository — is there a GitHub remote?")
	}
	return name, nil
}

type PR struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	State   string `json:"state"`
	URL     string `json:"url"`
	BaseRef string `json:"baseRefName"`
	HeadRef string `json:"headRefName"`
	HeadSHA string `json:"headRefOid"`
	Author  struct {
		Login string `json:"login"`
	} `json:"author"`
	IsDraft bool `json:"isDraft"`
}

func (c Client) PR(number int) (*PR, error) {
	out, err := c.run(c.prArgs("view", strconv.Itoa(number), "--json",
		"number,title,body,state,url,baseRefName,headRefName,headRefOid,author,isDraft")...)
	if err != nil {
		return nil, err
	}
	var pr PR
	if err := json.Unmarshal([]byte(out), &pr); err != nil {
		return nil, fmt.Errorf("could not read pull request %d: %w", number, err)
	}
	return &pr, nil
}

// Diff returns the pull request's unified diff.
func (c Client) Diff(number int) (string, error) {
	return c.run(c.prArgs("diff", strconv.Itoa(number))...)
}

// prArgs builds a `gh pr` invocation, adding --repo when the client targets a
// repository other than the working directory's.
func (c Client) prArgs(args ...string) []string {
	out := append([]string{"pr"}, args...)
	if c.Repo != "" {
		out = append(out, "--repo", c.Repo)
	}
	return out
}

// Comment is one existing review comment written by a teammate.
type Comment struct {
	DiffHunk            string `json:"diff_hunk"`
	OriginalCommitID    string `json:"original_commit_id"`
	CommitID            string `json:"commit_id"`
	OriginalLine        int    `json:"original_line"`
	OriginalStartLine   int    `json:"original_start_line"`
	PullRequestReviewID int64  `json:"pull_request_review_id"`
	ID                  int64  `json:"id"`
	Path                string `json:"path"`
	Body                string `json:"body"`
	Side                string `json:"side"`
	Line                int    `json:"line"`
	StartLine           int    `json:"start_line"`
	CreatedAt           string `json:"created_at"`
	UpdatedAt           string `json:"updated_at"`
	URL                 string `json:"html_url"`
	InReplyTo           int64  `json:"in_reply_to_id"`
	User                struct {
		Login string `json:"login"`
	} `json:"user"`

	// Position is null once the comment's diff hunk no longer exists, which
	// is GitHub's way of saying the comment is outdated after a force-push.
	Position *int `json:"position"`
}

// Outdated reports that GitHub can no longer anchor this comment to the diff.
func (c Comment) Outdated() bool { return c.Position == nil }

// Comments lists the review comments on a pull request.
func (c Client) Comments(repo string, number int) ([]Comment, error) {
	out, err := c.run("api", "--paginate",
		fmt.Sprintf("repos/%s/pulls/%d/comments", repo, number))
	if err != nil {
		return nil, err
	}
	return parseComments(out)
}

// parseComments decodes gh's --paginate output, which concatenates one JSON
// array per page rather than emitting a single array.
func parseComments(out string) ([]Comment, error) {
	dec := json.NewDecoder(strings.NewReader(out))
	var all []Comment
	for {
		var page []Comment
		if err := dec.Decode(&page); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("could not read review comments: %w", err)
		}
		all = append(all, page...)
	}
	return all, nil
}

// ReviewComment is one comment being submitted.
type ReviewComment struct {
	Path      string `json:"path"`
	Body      string `json:"body"`
	Line      int    `json:"line"`
	StartLine int    `json:"start_line,omitempty"`
	Side      string `json:"side,omitempty"`
	StartSide string `json:"start_side,omitempty"`
}

// Review events accepted by the GitHub API.
const (
	EventComment        = "COMMENT"
	EventApprove        = "APPROVE"
	EventRequestChanges = "REQUEST_CHANGES"
)

// reviewPayload builds the request body for the reviews endpoint. An empty
// commitID leaves the review unpinned, which is what a plain SubmitReview does.
func reviewPayload(commitID, event, body string, comments []ReviewComment) ([]byte, error) {
	return json.Marshal(reviewRequest{CommitID: commitID, Body: body, Event: event, Comments: comments})
}

type reviewRequest struct {
	CommitID string          `json:"commit_id,omitempty"`
	Body     string          `json:"body,omitempty"`
	Event    string          `json:"event"`
	Comments []ReviewComment `json:"comments,omitempty"`
}

// SubmitReview posts every comment as one review. GitHub reviews are atomic,
// which is why notes are held locally until this is called: a half-written
// review never reaches the author, and one review sends one notification
// rather than one per comment.
func (c Client) SubmitReview(repo string, number int, event, body string, comments []ReviewComment) error {
	return c.submitReview(repo, number, "", event, body, comments)
}

// SubmitReviewAt pins a review to the revision that was displayed. The head
// check refuses a review that is already out of date; commit_id also keeps a
// push racing with this request from silently approving unseen code.
func (c Client) SubmitReviewAt(repo string, number int, headSHA, event, body string, comments []ReviewComment) error {
	if strings.TrimSpace(headSHA) == "" {
		return errors.New("the reviewed commit is unknown — reopen the pull request before submitting")
	}
	return c.submitReview(repo, number, headSHA, event, body, comments)
}

func (c Client) submitReview(repo string, number int, headSHA, event, body string, comments []ReviewComment) error {
	if event != EventComment && event != EventApprove && event != EventRequestChanges {
		return fmt.Errorf("unknown review event %q", event)
	}
	if event == EventComment && strings.TrimSpace(body) == "" && len(comments) == 0 {
		return errors.New("nothing to submit")
	}
	if event == EventRequestChanges && strings.TrimSpace(body) == "" {
		return errors.New("requesting changes requires a review body")
	}
	if headSHA != "" {
		remote := c
		remote.Repo = repo
		pr, err := remote.PR(number)
		if err != nil {
			return fmt.Errorf("could not check the pull request head before submitting: %w", err)
		}
		if pr.HeadSHA != headSHA {
			return errors.New("the pull request changed since you opened it — reopen and review the new changes before submitting; your notes are kept")
		}
	}
	payload, err := reviewPayload(headSHA, event, body, comments)
	if err != nil {
		return err
	}
	_, err = c.runInput(payload, "api", "--method", "POST",
		fmt.Sprintf("repos/%s/pulls/%d/reviews", repo, number), "--input", "-")
	return err
}

func (c Client) run(args ...string) (string, error) { return c.runInput(nil, args...) }

func (c Client) runInput(stdin []byte, args ...string) (_ string, err error) {
	id := c.Trace.start(c.step, c.group, "gh", args)
	defer func() { c.Trace.end(id, err) }()
	if c.runOverride != nil {
		return c.runOverride(stdin, args...)
	}
	cmd := exec.Command("gh", args...)
	cmd.Dir = c.Dir
	cmd.Env = os.Environ()
	if c.Host != "" {
		cmd.Env = append(cmd.Env, "GH_HOST="+c.Host)
	}
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errOut.String())
		if msg == "" {
			msg = err.Error()
		}
		// gh reports "Unprocessable Entity (HTTP 422)" and leaves the useful
		// part — GitHub's own validation errors — in the response body on
		// stdout. Without this a rejected review says nothing about why.
		if detail := apiError(out.String()); detail != "" {
			msg += ": " + detail
		}
		return "", fmt.Errorf("gh %s: %s", strings.Join(args, " "), msg)
	}
	return out.String(), nil
}

// apiError extracts GitHub's own explanation from an error response.
//
// The errors field is not one shape: the reviews endpoint returns an array of
// strings ("Review Can not approve your own pull request"), while validation
// failures elsewhere return objects with resource/field/code. Both are
// handled, because the string form is exactly the case worth explaining.
func apiError(body string) string {
	body = strings.TrimSpace(body)
	if body == "" || !strings.HasPrefix(body, "{") {
		return ""
	}
	var resp struct {
		Message string            `json:"message"`
		Errors  []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		return ""
	}

	var parts []string
	if resp.Message != "" && resp.Message != "Unprocessable Entity" {
		parts = append(parts, resp.Message)
	}
	for _, raw := range resp.Errors {
		if s := decodeError(raw); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "; ")
}

func decodeError(raw json.RawMessage) string {
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString
	}
	var obj struct {
		Resource string `json:"resource"`
		Field    string `json:"field"`
		Code     string `json:"code"`
		Message  string `json:"message"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	if obj.Message != "" {
		return obj.Message
	}
	if obj.Field != "" {
		return fmt.Sprintf("%s %s (%s)", obj.Resource, obj.Field, obj.Code)
	}
	return ""
}

// Viewer is the login of the authenticated user.
func (c Client) Viewer() (string, error) {
	out, err := c.run("api", "user", "--jq", ".login")
	return strings.TrimSpace(out), err
}
