package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tobiasbernting/krv/v2/internal/diffparse"
	"github.com/tobiasbernting/krv/v2/internal/followup"
	"github.com/tobiasbernting/krv/v2/internal/ghsrc"
	"github.com/tobiasbernting/krv/v2/internal/notes"
)

type syncState struct {
	syncedAt time.Time
	failedAt time.Time
	err      string
}

type syncResultMsg struct {
	snapshot  ghsrc.Snapshot
	files     []*diffparse.FileDiff
	err       error
	submitted []notes.Note
}

type syncTickMsg time.Time

func tickSyncAge() tea.Cmd {
	return tea.Tick(time.Minute, func(now time.Time) tea.Msg { return syncTickMsg(now) })
}

func (m Model) startSync(submitted []notes.Note) (tea.Model, tea.Cmd) {
	if !m.src.CanSubmit() {
		m.err = "sync is available only for pull requests"
		return m, nil
	}
	if !m.requests.start(reqSync) {
		m.status = "sync already in progress"
		return m, nil
	}
	m.sync.err = ""
	src := m.src
	return m, func() tea.Msg {
		var snapshot ghsrc.Snapshot
		var err error
		if src.FollowUp != nil {
			snapshot, err = src.Client.ReviewSnapshot(src.Repo, src.PRNumber)
		} else {
			snapshot, err = src.Client.Snapshot(src.Repo, src.PRNumber)
		}
		if err != nil {
			return syncResultMsg{err: err, submitted: submitted}
		}
		files := diffparse.Parse(snapshot.RawDiff)
		diffparse.FillStats(files)
		return syncResultMsg{snapshot: snapshot, files: files, submitted: submitted}
	}
}

type syncChanges struct {
	newComments     int
	updatedComments int
	deletedComments int
	resolved        int
	reopened        int
	outdated        int
	current         int
	files           int
}

func (m Model) applySyncResult(msg syncResultMsg) (tea.Model, tea.Cmd) {
	m.requests.done(reqSync)
	if msg.err != nil {
		m.sync.failedAt = time.Now()
		m.sync.err = msg.err.Error()
		if len(msg.submitted) > 0 {
			m.status = fmt.Sprintf("submitted %d comment%s; comments could not refresh",
				len(msg.submitted), plural(len(msg.submitted)))
		}
		return m, nil
	}

	newSet, updatedSet, changes := compareThreads(m.threads, msg.snapshot.Threads.Threads)
	suppressSubmitted(newSet, msg.snapshot.Threads.Threads, msg.submitted, m.src.Viewer)
	changes.newComments = len(newSet)
	oldFiles := m.files
	if m.follow.session != nil {
		oldFiles = m.follow.session.Files
	}
	changes.files = changedFileCount(oldFiles, msg.files)

	current := map[int64]bool{}
	for _, thread := range msg.snapshot.Threads.Threads {
		for _, comment := range thread.Comments {
			current[comment.ID] = true
		}
	}
	for id := range m.newComments {
		if !current[id] {
			delete(m.newComments, id)
		}
	}
	for id := range m.updatedComments {
		if !current[id] {
			delete(m.updatedComments, id)
		}
	}
	for id := range newSet {
		m.newComments[id] = true
	}
	for id := range updatedSet {
		m.updatedComments[id] = true
	}
	for _, thread := range msg.snapshot.Threads.Threads {
		for _, comment := range thread.Comments {
			if newSet[comment.ID] || updatedSet[comment.ID] {
				m.expandedThreads[thread.ID] = true
			}
		}
	}

	selected, hadSelected := m.selectedThread()
	previousMode, changesView := m.mode, m.changesView
	anchor := m.cursorAnchor()
	m.files = msg.files
	m.blobs = Blobs(msg.files)
	m.threads = msg.snapshot.Threads.Threads
	m.src.HeadSHA = msg.snapshot.HeadSHA
	m.fileCursor = 0
	if msg.snapshot.PR != nil {
		m.pr = msg.snapshot.PR
	}
	if msg.snapshot.PR != nil && m.src.FollowUp != nil {
		s := followup.FromSnapshot(msg.snapshot)
		m.src.FollowUp, m.src.Viewer, m.src.Author = s, s.Viewer, s.PR.Author.Login
		m.installFollowUp(s)
		m.mode = previousMode
		if hadSelected {
			for i, t := range m.follow.threads {
				if t.ID == selected.ID {
					m.follow.cursor = i
					break
				}
			}
		}
	}
	m.changesView = false
	m.rangeAnchor, m.rangeAnchorPath = 0, ""
	m.reanchor = reanchorState{}
	m.resetGaps()
	m.sync.syncedAt = msg.snapshot.FetchedAt
	m.sync.failedAt = time.Time{}
	m.sync.err = ""
	m.rebuildAt(anchor)

	summary := changes.String()
	if len(msg.submitted) > 0 {
		m.status = fmt.Sprintf("submitted %d comment%s; %s",
			len(msg.submitted), plural(len(msg.submitted)), summary)
	} else {
		m.status = summary
	}
	if changesView && m.follow.session != nil {
		if m.follow.session.Comparison != nil {
			next, cmd := m.showChanges()
			m = next.(Model)
			m.keepOverview(previousMode)
			return m, cmd
		}
		m.mode = modeDiff
		m.err = m.follow.session.ComparisonError
	}
	m.keepOverview(previousMode)
	if m.mode == modeThread {
		return m, m.loadThreadContext()
	}
	return m, nil
}

// keepOverview leaves the Overview open across a sync it was started from,
// whatever else the fresh snapshot did to the screen. Its scroll offset
// stands, clamped to the new page when that is shorter; the link cursor
// resets, because the links it counted are not these.
func (m *Model) keepOverview(previous mode) {
	if previous != modeOverview {
		return
	}
	m.mode = modeOverview
	m.reader.focus = -1
}

func compareThreads(old, fresh []ghsrc.Thread) (map[int64]bool, map[int64]bool, syncChanges) {
	oldComments := map[int64]ghsrc.Comment{}
	oldThreads := map[int64]ghsrc.Thread{}
	for _, thread := range old {
		oldThreads[thread.RootID] = thread
		for _, comment := range thread.Comments {
			oldComments[comment.ID] = comment
		}
	}

	newSet := map[int64]bool{}
	updatedSet := map[int64]bool{}
	seen := map[int64]bool{}
	changes := syncChanges{}
	for _, thread := range fresh {
		if previous, ok := oldThreads[thread.RootID]; ok && previous.ResolutionKnown && thread.ResolutionKnown {
			switch {
			case !previous.Resolved && thread.Resolved:
				changes.resolved++
			case previous.Resolved && !thread.Resolved:
				changes.reopened++
			}
		}
		if previous, ok := oldThreads[thread.RootID]; ok {
			switch {
			case !previous.Outdated && thread.Outdated:
				changes.outdated++
			case previous.Outdated && !thread.Outdated:
				changes.current++
			}
		}
		for _, comment := range thread.Comments {
			seen[comment.ID] = true
			previous, ok := oldComments[comment.ID]
			if !ok {
				newSet[comment.ID] = true
				continue
			}
			if previous.Body != comment.Body || previous.UpdatedAt != comment.UpdatedAt {
				updatedSet[comment.ID] = true
			}
		}
	}
	for id := range oldComments {
		if !seen[id] {
			changes.deletedComments++
		}
	}
	changes.newComments = len(newSet)
	changes.updatedComments = len(updatedSet)
	return newSet, updatedSet, changes
}

func suppressSubmitted(newSet map[int64]bool, threads []ghsrc.Thread, submitted []notes.Note, viewer string) {
	if len(submitted) == 0 {
		return
	}
	want := map[string]int{}
	for _, note := range submitted {
		want[noteSignature(note.Path, note.StartLine, note.Line, note.Body)]++
	}
	for _, thread := range threads {
		for _, comment := range thread.Comments {
			if !newSet[comment.ID] || (viewer != "" && comment.User.Login != viewer) {
				continue
			}
			key := noteSignature(thread.Path, thread.StartLine, thread.Line, comment.Body)
			if want[key] > 0 {
				delete(newSet, comment.ID)
				want[key]--
			}
		}
	}
}

func noteSignature(path string, start, line int, body string) string {
	return fmt.Sprintf("%s\x00%d\x00%d\x00%s", path, start, line, strings.TrimSpace(body))
}

func changedFileCount(old, fresh []*diffparse.FileDiff) int {
	before := Blobs(old)
	after := Blobs(fresh)
	paths := map[string]bool{}
	for path := range before {
		paths[path] = true
	}
	for path := range after {
		paths[path] = true
	}
	n := 0
	for path := range paths {
		if before[path] != after[path] {
			n++
		}
	}
	return n
}

func (c syncChanges) String() string {
	var parts []string
	add := func(n int, one, many string) {
		if n == 1 {
			parts = append(parts, "1 "+one)
		} else if n > 1 {
			parts = append(parts, fmt.Sprintf("%d %s", n, many))
		}
	}
	add(c.newComments, "new comment", "new comments")
	add(c.updatedComments, "updated comment", "updated comments")
	add(c.deletedComments, "deleted comment", "deleted comments")
	add(c.resolved, "thread resolved", "threads resolved")
	add(c.reopened, "thread reopened", "threads reopened")
	add(c.outdated, "thread became outdated", "threads became outdated")
	add(c.current, "thread re-anchored", "threads re-anchored")
	add(c.files, "file changed", "files changed")
	if len(parts) == 0 {
		return "synced; no changes"
	}
	sort.Strings(parts)
	return "synced; " + strings.Join(parts, ", ")
}

func age(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	if d < time.Minute {
		return "now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh ago", int(d.Hours()))
}
