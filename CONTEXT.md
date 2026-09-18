# crv

A terminal tool for reviewing a diff, local or a GitHub pull request, and
turning that review into comments.

## Language

### Review

**Draft**:
An unsent local review comment owned by crv, anchored to one line or a
Selection in the diff.
_Avoid_: Note (in user-facing text), pending comment

**Thread**:
A GitHub review discussion: one root comment and its replies.
_Avoid_: Conversation, remote comment

**Selection**:
A contiguous run of lines within one hunk of one file, chosen with `v` or a
mouse drag; the target of a Draft or a Yank.
_Avoid_: Range, multi-line note, visual selection

### Clipboard

**Yank**:
Copying diff content to the system clipboard as plain code, without gutter,
line numbers or `+`/`-` markers.
_Avoid_: Copy (ambiguous with the terminal's own selection)

**Reference**:
A location written as `path:L12-L18`, naming a line or Selection for pasting
elsewhere.
_Avoid_: Link, permalink
