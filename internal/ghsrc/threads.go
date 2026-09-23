package ghsrc

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Thread is one GitHub review discussion. Outdated describes its diff anchor;
// Resolved describes the discussion. They are deliberately independent.
type Thread struct {
	OriginalLine, OriginalStartLine      int
	ViewerCanResolve, ViewerCanUnresolve bool

	// ID is derived from the root comment, so it survives a GraphQL outage and
	// is safe to persist. GraphQLID is the node ID the resolution mutations
	// need, and is empty whenever the REST fallback was used.
	ID        string
	GraphQLID string

	RootID          int64
	Path            string
	Side            string
	Line            int
	StartLine       int
	Outdated        bool
	Resolved        bool
	ResolutionKnown bool
	Comments        []Comment
}

// ThreadFeed is the complete set of review threads visible to the viewer.
// ResolutionKnown is false when an older Enterprise host required the REST
// fallback, which has anchors and replies but no thread resolution state.
type ThreadFeed struct {
	Threads         []Thread
	ResolutionKnown bool
}

type threadState struct {
	ViewerCanResolve, ViewerCanUnresolve bool
	ID                                   string
	RootID                               int64
	Path                                 string
	Side                                 string
	Line                                 int
	StartLine                            int
	Outdated                             bool
	Resolved                             bool
}

const reviewThreadsQuery = `query($owner: String!, $name: String!, $number: Int!, $endCursor: String) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      reviewThreads(first: 100, after: $endCursor) {
        nodes {
          id path line startLine diffSide isOutdated isResolved viewerCanResolve viewerCanUnresolve
          comments(first: 1) { nodes { databaseId } }
        }
        pageInfo { hasNextPage endCursor }
      }
    }
  }
}`

// Threads loads every review comment through REST, then enriches the grouped
// discussions with GraphQL's resolved and outdated state. REST remains the
// fallback for Enterprise hosts whose schema predates reviewThreads.
func (c Client) Threads(repo string, number int) (ThreadFeed, error) {
	comments, err := c.Comments(repo, number)
	if err != nil {
		return ThreadFeed{}, err
	}

	threads := groupComments(comments)
	states, err := c.reviewThreadStates(repo, number)
	if err != nil {
		return ThreadFeed{Threads: threads}, nil
	}

	byRoot := make(map[int64]int, len(threads))
	for i := range threads {
		byRoot[threads[i].RootID] = i
	}
	for _, state := range states {
		i, ok := byRoot[state.RootID]
		if !ok {
			continue
		}
		thread := &threads[i]
		thread.ViewerCanResolve = state.ViewerCanResolve
		thread.ViewerCanUnresolve = state.ViewerCanUnresolve
		thread.GraphQLID = state.ID
		thread.Path = state.Path
		thread.Side = state.Side
		thread.Line = state.Line
		thread.StartLine = state.StartLine
		thread.Outdated = state.Outdated
		thread.Resolved = state.Resolved
		thread.ResolutionKnown = true
	}
	return ThreadFeed{Threads: threads, ResolutionKnown: true}, nil
}

func (c Client) reviewThreadStates(repo string, number int) ([]threadState, error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" {
		return nil, fmt.Errorf("invalid GitHub repository %q", repo)
	}
	out, err := c.run("api", "graphql", "--paginate", "--slurp",
		"-f", "query="+reviewThreadsQuery,
		"-F", "owner="+owner,
		"-F", "name="+name,
		"-F", "number="+strconv.Itoa(number))
	if err != nil {
		return nil, err
	}
	return parseThreadStates(out)
}

func parseThreadStates(out string) ([]threadState, error) {
	var pages []struct {
		Errors []struct{ Message string } `json:"errors"`
		Data   *struct {
			Repository *struct {
				PullRequest *struct {
					ReviewThreads struct {
						Nodes []struct {
							ViewerCanResolve   bool   `json:"viewerCanResolve"`
							ViewerCanUnresolve bool   `json:"viewerCanUnresolve"`
							ID                 string `json:"id"`
							Path               string `json:"path"`
							Line               int    `json:"line"`
							StartLine          int    `json:"startLine"`
							DiffSide           string `json:"diffSide"`
							IsOutdated         bool   `json:"isOutdated"`
							IsResolved         bool   `json:"isResolved"`
							Comments           struct {
								Nodes []struct {
									DatabaseID int64 `json:"databaseId"`
								} `json:"nodes"`
							} `json:"comments"`
						} `json:"nodes"`
					} `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &pages); err != nil {
		return nil, fmt.Errorf("could not read review threads: %w", err)
	}

	var states []threadState
	for _, page := range pages {
		if len(page.Errors) > 0 {
			return nil, fmt.Errorf("GitHub thread state unavailable: %s", page.Errors[0].Message)
		}
		// A null data, repository or pullRequest is GitHub declining to answer,
		// not a pull request without threads. Silently reading it as "no
		// threads" would let Threads report every discussion as unresolved
		// while still claiming the resolution state is known.
		if page.Data == nil || page.Data.Repository == nil || page.Data.Repository.PullRequest == nil {
			return nil, errors.New("GitHub returned no review thread data")
		}
		for _, node := range page.Data.Repository.PullRequest.ReviewThreads.Nodes {
			if len(node.Comments.Nodes) == 0 || node.Comments.Nodes[0].DatabaseID == 0 {
				continue
			}
			states = append(states, threadState{
				ViewerCanResolve: node.ViewerCanResolve, ViewerCanUnresolve: node.ViewerCanUnresolve,
				ID: node.ID, RootID: node.Comments.Nodes[0].DatabaseID,
				Path: node.Path, Side: node.DiffSide, Line: node.Line,
				StartLine: node.StartLine, Outdated: node.IsOutdated,
				Resolved: node.IsResolved,
			})
		}
	}
	return states, nil
}

func groupComments(comments []Comment) []Thread {
	byRoot := make(map[int64]*Thread)
	var order []int64
	for _, comment := range comments {
		rootID := comment.ID
		if comment.InReplyTo != 0 {
			rootID = comment.InReplyTo
		}
		thread, ok := byRoot[rootID]
		if !ok {
			thread = &Thread{ID: "rest:" + strconv.FormatInt(rootID, 10), RootID: rootID}
			byRoot[rootID] = thread
			order = append(order, rootID)
		}
		thread.Comments = append(thread.Comments, comment)
		if comment.ID == rootID {
			thread.OriginalLine, thread.OriginalStartLine = comment.OriginalLine, comment.OriginalStartLine
			thread.Path = comment.Path
			thread.Side = comment.Side
			thread.Line = comment.Line
			thread.StartLine = comment.StartLine
			thread.Outdated = comment.Outdated()
		}
	}

	threads := make([]Thread, 0, len(order))
	for _, rootID := range order {
		thread := byRoot[rootID]
		sort.SliceStable(thread.Comments, func(i, j int) bool {
			if thread.Comments[i].ID == rootID {
				return true
			}
			if thread.Comments[j].ID == rootID {
				return false
			}
			return thread.Comments[i].CreatedAt < thread.Comments[j].CreatedAt
		})
		threads = append(threads, *thread)
	}
	return threads
}

// Snapshot is one coherent refresh of a pull request's diff and discussions.
type Snapshot struct {
	PR              *PR
	Viewer          string
	Baseline        *SubmittedReview
	Comparison      *RevisionComparison
	ComparisonError string
	Warnings        []string
	RawDiff         string
	Threads         ThreadFeed
	HeadSHA         string
	FetchedAt       time.Time
}

// Snapshot loads the diff and comments against one head commit. If the pull
// request moves during the fetch, it retries once instead of returning a mixed
// view whose comments may point at the wrong code.
func (c Client) Snapshot(repo string, number int) (Snapshot, error) {
	return c.snapshot(repo, number, false)
}

// ReviewSnapshot extends the coherent queue snapshot with review history.
func (c Client) ReviewSnapshot(repo string, number int) (Snapshot, error) {
	return c.snapshot(repo, number, true)
}

func (c Client) snapshot(repo string, number int, history bool) (Snapshot, error) {
	target := c
	target.Repo = repo
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			target.Trace.Mark("head moved — retrying")
		}
		before, err := target.traced("pull request").PR(number)
		if err != nil {
			return Snapshot{}, err
		}
		raw, err := target.traced("diff").Diff(number)
		if err != nil {
			return Snapshot{}, err
		}
		threads, err := target.traced("threads").Threads(repo, number)
		if err != nil {
			return Snapshot{}, err
		}
		meta := Snapshot{}
		if history {
			meta.Viewer, err = target.traced("viewer").Viewer()
			if err != nil {
				meta.Warnings = append(meta.Warnings, "Review identity unavailable: "+err.Error())
			} else {
				reviews, historyErr := target.traced("reviews").Reviews(repo, number)
				if historyErr != nil {
					meta.Warnings = append(meta.Warnings, "Review history unavailable: "+historyErr.Error())
				} else {
					meta.Baseline = LatestReview(reviews, meta.Viewer)
				}
			}
			if meta.Baseline != nil {
				meta.Comparison, err = target.CompareRevisions(repo, meta.Baseline.CommitID, before.HeadSHA)
				if err != nil {
					meta.ComparisonError = "Historical comparison unavailable: " + err.Error()
				}
			}
		}
		after, err := target.traced("recheck head").PR(number)
		if err != nil {
			return Snapshot{}, err
		}
		if before.HeadSHA == after.HeadSHA {
			meta.PR, meta.RawDiff, meta.Threads, meta.HeadSHA, meta.FetchedAt = after, raw, threads, after.HeadSHA, time.Now()
			return meta, nil
		}
	}
	return Snapshot{}, fmt.Errorf("pull request changed while syncing; press r to retry")
}
