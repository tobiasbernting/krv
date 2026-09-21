# krv

A terminal tool for reviewing a diff, local or a GitHub pull request, and
turning that review into comments.

## Language

### Review

**Draft**:
An unsent local review comment owned by krv, anchored to one line or a
Selection in the diff.
_Avoid_: Note (in user-facing text), pending comment

**Thread**:
A GitHub review discussion: one root comment and its replies.
_Avoid_: Conversation, remote comment

**Reply**:
A comment added to an existing Thread, posted to GitHub the moment it is sent.
A Reply is never a Draft: it is not saved locally or held for submission.
_Avoid_: Response, answer

**Selection**:
A contiguous run of lines within one hunk of one file, chosen with `v` or a
mouse drag; the target of a Draft or a Yank.
_Avoid_: Range, multi-line note, visual selection

### Pull request

**Overview**:
The panel showing a pull request's header, its Checks and its Description.
_Avoid_: Info, details, sidebar

**Description**:
The pull request's body text, written in Markdown.
_Avoid_: Body (in user-facing text), summary

**Check**:
One CI status or check run on the head commit.
_Avoid_: Status, job, CI

**Required check**:
A Check that branch protection requires. Unknown on a host that cannot say,
which the Overview states rather than guessing.
_Avoid_: Blocking check

### Clipboard

**Yank**:
Copying diff content to the system clipboard as plain code, without gutter,
line numbers or `+`/`-` markers. The key is `y`; user-facing text says "copy",
the plain word for it.
_Avoid_: Clipboard (for the action), paste

**Reference**:
A location written as `path:L12-L18`, naming a line or Selection for pasting
elsewhere.
_Avoid_: Link, permalink
