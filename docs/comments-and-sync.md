# Comments and sync

This document defines how `krv` distinguishes local drafts, remote discussion
state, and the age of the displayed pull request snapshot.

## Terms

- A **draft** is an unsent local review comment owned by `krv`.
- **Needs re-anchor** means a draft's file blob or path no longer matches the
  current diff. Its saved line coordinates cannot be trusted.
- A **thread** is a GitHub review discussion: one root comment and its replies.
- **Outdated** means GitHub can no longer anchor a remote thread to the current
  diff. It says nothing about whether the discussion was addressed.
- **Resolved** means the GitHub discussion was closed. It says nothing about
  whether its original code anchor still exists.
- **Synced** describes the age of the diff and thread snapshot currently shown.

`needs re-anchor`, `outdated`, and `resolved` are separate states. Only a local
draft that needs re-anchoring blocks submission.

## Thread presentation

| Thread state | Presentation |
| --- | --- |
| Current and unresolved | Expanded inline at its diff line |
| Current and resolved | Collapsed inline |
| Outdated and unresolved | Expanded under `Outdated, unresolved` |
| Outdated and resolved | Collapsed under `Outdated, resolved` |

When resolution state is unavailable on an older GitHub Enterprise host, the
thread is labeled `resolution unavailable`. Review comments remain visible.

Threads whose path is absent from the current diff are grouped by their
original path at the end of the document. They are never silently dropped.
New or edited activity expands a normally collapsed thread for the rest of the
session.

Each comment is one line when unfocused. Under the cursor it preserves line
breaks and indentation, wraps to the terminal, and expands to at most eight
lines. An overflow marker indicates more content. `enter` opens a full-height
scrollable view; `j` and `k` or the mouse wheel scroll it, and `enter`,
`esc`, or `q` closes it.

## Replies

`c` on any row of a thread, its summary or any comment, composes a reply;
on a code line it still drafts a comment. A reply always goes to the thread's
root comment, which is what GitHub requires. Any thread can be answered:
resolved, outdated, or on a path absent from the diff. Replying to a collapsed
thread expands it for the rest of the session.

A reply is posted the moment `enter` is pressed. It is never a draft, is not
saved locally, and is not part of a submitted review. `ctrl+e` finishes it in
`$EDITOR`; saving there sends it, and an empty reply is discarded. `esc`
discards it.

While the reply is posted the review holds still, and a second `enter` sends
nothing. On success the comment is added to the displayed thread without a
sync, so the diff does not move; the next sync already knows it, so it is not
marked new. On failure the composer keeps the text with GitHub's error, and
`enter` retries. A reply cannot start while a sync is running.

## Requests in flight

A GitHub request is identified by what it does and to what: a sync, a
submission, a reply or resolution of one thread, the queue for one filter.
Pressing a key again while its request runs does nothing, rather than sending
it twice. While a request that changes GitHub runs, a submission, reply or
resolution, the review ignores other keys except those that quit, so the
result is never lost to a keypress.

## Sync

`r` manually syncs a pull request. Opening a pull request performs the initial
load; there is no background refresh that can move the diff while it is being
read.

A sync fetches the latest diff and all visible review threads. It verifies that
the pull request head did not move during the fetch and retries once if it did.
The same GraphQL request that reads the head also reads its Checks, so they
are part of the snapshot; a host without `isRequired` leaves required status
unknown.
The new diff and threads replace the current snapshot together. If any required
fetch fails, neither is applied, so old line coordinates are never combined
with a new diff.

The cursor is restored by comment identity, or by file and line when possible.
Sync never jumps to new feedback automatically. The result reports changed
files, new, edited, or deleted comments, and outdated, re-anchored, resolved,
or reopened threads.

The status bar shows the snapshot age as `synced 4m ago`. A failure remains
visible as `sync failed 1m ago` until the next successful sync. If comments fail during the initial load, the load fails so no incomplete
snapshot is presented. Review-history or comparison failures instead appear
as explicit warnings alongside the coherent diff and threads.

The paginated REST feed supplies every comment and reply; GitHub GraphQL adds
thread resolution and authoritative outdated state. If those GraphQL fields
are unavailable, `krv` keeps the REST grouping and marks resolution as
unavailable.

## New activity

Comment IDs identify remote activity. A comment absent from the previous
snapshot is `new`; a known comment whose body or update timestamp changed is
`updated`. Deleted comments disappear. Resolution-only changes appear in the
sync summary but are not treated as new comments.

`N` and `P` move between threads containing unvisited new or updated comments,
focusing the first changed comment in each thread. Visiting a changed comment
clears the markers for that thread. This read state lasts only for the current
process and is not persisted across sessions.

After a successful submission, local drafts are deleted and sync runs
automatically so the posted review returns as GitHub-owned threads. Comments
just submitted by this process are not marked new; unrelated activity fetched
at the same time still is.

## Re-anchoring drafts

A changed or missing path moves its drafts into `Needs re-anchor`. To repair a
draft:

1. Put the cursor on the detached draft and press `m`.
2. Navigate anywhere in the current diff, including another file.
3. Press `enter` on an added or unchanged line to attach it there.

For a multiline target, press `v` on the first line, move within the same diff
hunk, then press `enter`. `esc` cancels without changing the draft. Confirmation
updates only the path, side, line range, and blob; the draft ID and body remain
unchanged.

## Non-interactive output

When stdout is not a terminal, every thread and reply is printed. Interactive
collapse state is ignored, and state labels such as `resolved`, `outdated`,
`new`, `updated`, and `needs re-anchor` remain explicit.

## Follow-up review

Opening a PR selects your latest submitted GitHub review, including browser
reviews and dismissed reviews. Pending reviews are excluded. When a baseline
exists, the first screen lists threads you started, including resolved,
outdated, and removed-file threads. `t` returns to that list.

`enter` opens the original comment and historical hunk, changes in its file
since the review, replies, and current file context. Deleted-side and outdated
anchors never supply guessed current line numbers.

- `x` toggles local verification. This does not resolve the GitHub thread.
- `c` composes a reply; `enter` posts it to GitHub and `esc` cancels.
- `R` resolves or reopens the thread when GitHub permits it.
- `a` shows all changes since your latest submitted review.
- `D` shows the full current PR diff, with the existing draft re-anchor workflow.
- `S` submits a review, including an approval with no inline comments.

Comparison uses immutable commit trees and changed blobs, so force-pushed or
unrelated histories remain comparable. Unavailable historical objects produce
an explicit warning; the current PR diff is never presented as changes since
review. The comparison view does not accept inline drafts because its diff
coordinates differ from the current PR diff.

Verification persists per thread and file content/mode fingerprint. Changes to
that file invalidate the mark; unrelated files do not. Unmapped missing paths
bind verification to the whole head revision. Unknown comparison evidence
cannot be verified. GitHub resolution and outdated state remain independent.

Refresh uses the same snapshot and activity tracking as the full diff. It also
reloads browser review history, comparison evidence, and thread permissions.
Failed refresh preserves the loaded snapshot and drafts. Submission checks the
remote head and sends the displayed `commit_id`; rejection or head movement
retains local drafts. A successful submission refreshes the baseline.

Copilot issue highlighting and automated “did this fix address my comment?”
analysis remain TODOs.
