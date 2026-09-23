# Rendering

How `internal/render` turns a parsed diff into the rows you read, and how to
change what they look like. Written for someone about to edit the renderer or
add a theme; [the README](../README.md) covers using the tool.

The design goal behind all of it: **diff state wins the page, and never rests
on hue.** A reviewer scrolling fast has to see which lines changed without
reading them, and has to be able to tell an addition from a deletion with the
colour turned off, muted by a low-contrast terminal, or unperceived.

## The pipeline

```
git / gh ─▶ diffparse ─▶ render.Build ─▶ Document{Rows} ─▶ Renderer.Render ─▶ string
                             ▲                                   ▲
                           Overlay                             Theme
                   drafts, comments, marks              colours and muting
```

`Build` is structure: how many rows there are and what each one is. `Render`
is presentation: it paints one row at a given width. The split is what lets the
TUI, the piped output and the golden tests share a renderer — `render` imports
nothing from `tui`.

Width belongs to paint time, not to `Build`: `RenderLines` turns one annotation
row into as many terminal lines as its body needs, so a resize re-paints and
never rebuilds. Every other row kind is one line, and `Render` is the shorthand
for "the first line of this row".

## Row anatomy

Every row is drawn on the same grid, so the eye can lock onto one column and
scan down it:

```
▎  12 │  14 + func Greet(name string) string {
│   │     │  │ │
│   │     │  │ └─ code, syntax muted so diff state wins
│   │     │  └─── sign: + added, − deleted, blank unchanged
│   │     └────── new-side line number
│   └──────────── the rule separating old from new
└──────────────── edge: diff marker, or the focus bar on the cursor row
```

`Document.GutterWidth()` is `2 + gutterOld + 3 + gutterNew + 3`, and every row
kind honours it: code starts there, meta rows indent to the file title above
them, and an annotation's text begins one column right of it. `trackGutter`
widens the number columns as the rows are built, so a document with four-digit
line numbers still lines up.

| glyph | meaning |
| --- | --- |
| `▎` | an added or deleted line |
| `┃` | the cursor row |
| `▌` | a draft or a review comment |
| `+` / `−` | added / deleted |
| `›` | the line continues past the right edge |
| `✓` / `~` | reviewed / reviewed then changed underneath you |

Add and delete are stated three times over — edge marker, sign, row tint — so
two of the three can be lost and the diff still reads.
`TestAddAndDeleteReadWithoutColour` renders with lipgloss degraded to plain
text and asserts exactly that; if you replace a glyph with colour, that test is
the one that fails.

### Focus

`rowTones` decides everything focus and diff state settle between them. A
focused row lifts its tone by one step (`AddBgFocus` / `DelBgFocus` /
`CursorBg`), takes the edge column for the focus bar and bolds the new-side
line number — and keeps its sign, its tint and its syntax colouring. Focus that
replaced the tint would answer "where am I?" by erasing "what is this?".

### Row kinds

| kind | what it draws |
| --- | --- |
| `RowFile` | full-width band: review marker, path, `+n −m` pushed right |
| `RowMeta` | rename, mode change, `binary file — not shown`; indented to the title |
| `RowHunk` | labels the two gutter columns (`old │ new`), the section, the `@@` range right |
| `RowCode` | the grid above |
| `RowNote` | an annotation — one line away from the cursor, expanded under it |
| `RowSection` | heads a group of annotations that no longer anchor to a line |
| `RowSpacer` | blank; navigation skips it |
| `RowPair` | split layout only: an old line and a new line side by side (below) |
| `RowGap` | the unchanged lines a diff leaves out: `⋯ 42 unchanged lines` ([Gaps](#gaps)) |

## Split layout

`Layout.Mode == ModeSplit` puts the old file on the left and the new one on
the right. `Build` does not know the terminal's width, so callers pass a layout
already run through `Layout.Fit(width)`, which falls back to unified below
`SplitMinWidth` (140). Unified output is untouched by any of this.

```
▎  13 − if err := s.authorize(ctx, r)… │▎  13 + if err := s.authorise(ctx, r, s.policy)…
│   │  │ │                              ││
│   │  │ └─ code                        │└─ the right pane's own edge marker
│   │  └─── sign                        └── the rule between the panes
│   └────── old-side line number
└────────── edge: the left pane's marker, or the focus bar on the cursor row
```

Every hunk line becomes a `RowPair`: `Line`/`Segs`/`Marks` are the left side,
`Right`/`RightSegs`/`RightMarks` the right. Pairing is GitHub's: a context line
sits on both sides of one row, and in each change block (deletions, then the
additions straight after) the i-th deleted line shares a row with the i-th
added line. The shorter side is filler — a side whose own line number
(`Line.OldNum`, `Right.NewNum`) is zero — painted blank in `FillBg`.
`changeBlocks` is the one pairing both this and `markHunk` use, so word marks
always sit on the row showing the line they were compared against.

The two panes are the same width and the same anatomy, each a unified row with
one number column (`paneNumWidth`, shared so the code starts at the same offset
in both). Each pane states add/delete with its own marker, sign and tint, so
the no-colour rule holds per pane (`TestSplitAddAndDeleteReadWithoutColour`).
Focus lifts both panes and takes the far-left edge; the right pane keeps its
marker. One `hoffset` scrolls both panes, and each marks its own overflow.

File, meta, section and spacer rows stay full width. A hunk header puts the old
range over the left pane — followed by the section — and the new range over
the right. Annotations stay full width under the pair row, anchored by the
right side's new line number, so a row whose right side is filler has none —
exactly as a deleted line has none in unified. `GutterWidth()` is the left
pane's gutter in split, which is where annotations indent to.

`Document.LineRow(fileIdx, newNum, oldNum)` finds the row showing a line in
either mode — new-side number first, old-side as the fallback — which is how a
view keeps its cursor on the same line across a mode switch.

## Gaps

A **Gap** is a run of unchanged lines the diff does not show: before the first
hunk, between two, or after the last. `Gaps(file, newLines)` finds them from
the hunk headers alone, numbered on both sides; the one after the last hunk
needs the file's length, so it is left out until the file has been read.
`MayContinue` says whether a file could have one at all — a last line followed
by `\ No newline at end of file` settles that it cannot.

Every Gap gets a row, in the gutter's colours so it reads as margin rather
than as a line of the file:

```
    3 │   4   three
      │     ⋯ 7 unchanged lines
  old │ new   (the next hunk's header)
```

The `⋯` sits in the sign column and the count where code starts. A Gap between
two hunks takes the place of the spacer comfortable density would put there:
it separates them already, and a blank line among its lines would read as one
the file does not have.

Opening a Gap is `Overlay.Expansion`: the file's new-side text and, per Gap,
how many lines are shown from its top (after the hunk above) and from its
bottom (before the hunk below). `Build` draws the top lines, a `RowGap` for
what is still hidden, then the bottom lines; a Gap shown whole has no row.

An expanded line is a `RowCode` (or a `RowPair`, the same line on both panes)
with `Expanded` set: an unchanged line, both sides numbered, the old number
following from the hunk offsets. It is painted in `Dim` instead of its syntax,
so what the diff changed still wins the page, and its `HunkIdx` is `-1`:
nothing anchors to it, and a Selection stops at it.

Gap rows only appear when the overlay supplies `Expansion`. Piped output has
no way to open one, so it prints the diff as git does.

## Themes

A `Theme` is grouped by semantic role — surface, gutter, diff state, focus,
headers, annotations — not as a flat palette, so a new preset answers "what
does a deleted line look like here?" rather than remembering which of a dozen
greens meant what. `resolve()` fills the older flat field names (`Gutter`,
`AddFg`, `DelFg`) from the roles; the queue, the submit view and the status bar
still read those.

| role | field | job |
| --- | --- | --- |
| surface | `Bg` `Fg` `Dim` `Accent` | the page behind unchanged code, and text with no colour of its own |
| gutter | `GutterBg` `GutterSep` `LineNumFg` `LineNumFocusFg` | the number columns and the rule between them |
| diff | `AddBg` `AddBgFocus` `AddEdge` `AddSign` `AddWordBg` (and `Del*`), `FillBg` | the tint, the marker, the sign, intra-line changes; the empty side of a split row |
| focus | `CursorBg` `CursorBar` | a focused context row and the bar itself |
| headers | `FileBg` `FileFg` `HunkBg` `HunkFg` `MetaFg` | file and hunk bands |
| annotations | `NoteBg` `NoteFg` `NoteBodyFg` `CommentFg` `StaleFg` | panel, label, body, and who is speaking |

A theme covers every screen, not only the diff: the queue, the file list and
the help all paint `Bg`/`Fg` themselves instead of leaving gaps to the
terminal's own colours, and each marks its cursor row with `render.FocusBar` in
the same column. A view that borrowed the terminal's background could only ever
look right on the one background it was written against.

Themes are **chosen, not detected**. Each preset states its own background, so
krv never has to guess what your terminal is — and never guesses wrong. The
built-ins are listed by `render.ThemeGroups`, in the order the `T` picker shows
them: dark surfaces, light surfaces, then the colour-blind presets that mark
changes in blue and orange instead of green and red. `render.ThemeByName`
reports whether a name is one of them, which is how the config layer tells a
theme name from a chroma style name. A preset named after a chroma style
(`dracula`, `nord`, `solarized-light`) highlights with that style, so a
configuration that named the style gets the whole theme.

The picker (`internal/tui/themes.go`) restyles the live screen as the cursor
moves — the diff behind it is the preview — and saves with
`config.SaveTheme`, which edits the user file as text so its comments survive.

### Syntax muting

Chroma colours the code; the renderer then blends each colour toward the
background of the cell it is painted on — the surface, a row tint or an
intra-line highlight — by `SyntaxMute`, and by the gentler `SyntaxMuteEmph`
for tokens that *name* something — functions, methods, classes, types, tags.
Those are what the eye scans for in an unfamiliar diff, so they keep most of
their colour while the punctuation, keywords and literals recede far enough
that the add/delete tint wins. `Segment.Emph` is set in `highlight.go` from the chroma token type;
`Renderer.syntaxFg` does the blending and caches the result.

Muting has a floor. A token that ends up closer in luminance to its cell than
`minCodeContrast` is lifted toward `Fg` until it clears it, and an intra-line
change is held to the stricter `minMarkContrast`, since it is the text the
reviewer is being pointed at. Muting toward `Bg` alone used to leave a grey
comment on a mid-green highlight all but invisible;
`TestSyntaxStaysReadableOnEveryBackground` now checks every colour of every
preset's chroma style against every background that preset paints.

`SyntaxMute: 0` disables muting entirely, which is what a theme that wants raw
chroma colours should set. The floor above still applies: a raw colour too
close to its background is lifted all the same.

### Adding a theme

1. Add a `func myTheme() Theme` in `theme.go`, ending in `.resolve()`.
2. Register it in the `presets` map, under its group.
3. Run `go test ./internal/render` — `TestPresetsFillEveryRole` fails on any
   empty field, `TestPresetsKeepCompatibilityAliases` on a missed `resolve()`,
   and the contrast tests on text that will not read against its own tint.
4. Add its name to the theme list in `internal/config/template.go`
   (`TestTemplateNamesEveryTheme`), then run
   `go test ./internal/config -update-example`.
5. Regenerate the screenshots (below) if it is meant to ship.

A user does not have to edit Go to change colours: `--syntax` overrides the
chroma style a theme comes with, and `theme = "monokai"` — a chroma style name
where a theme name is expected — still means what it did before krv had themes
of its own.

## Density

`Layout.Density` is the structural half of presentation. `comfortable` (the
default) inserts a spacer before every hunk but the first in a file — unless a
drawn [Gap](#gaps) sits between them — and after a group of annotations, so
hierarchy gets air and nothing else does; `compact`
gives every line of the terminal to the diff. It changes only how many
`RowSpacer` rows exist — `TestComfortableDensitySeparatesHunksNotLines` pins
that it never changes how many code rows there are.

## Annotations

Drafts, review comments and thread summaries are all `RowNote`, and all of them
wrap at paint time:

```
▌             robin [outdated]: Spelling of authorise is inconsistent with the
▌               rest of the package, and the exported helper still spells it the
▌               other way.
```

`annotationText` builds the line — who is speaking, the state badges
(`[outdated]`, `[resolved]`, `[needs re-anchor]`, `[new]`), the line range, then
the body. `RenderLines` wraps it with `WrapText`, which keeps the explicit
newlines a GitHub comment was written with rather than flattening the body into
one paragraph. `splitLabel` then divides the first line at the first `": "` so
the label keeps the annotation's own colour and weight while the body takes
`NoteBodyFg` — a comment you have to squint at is a comment you skip.

Continuation lines hang `noteHang` columns in from the label, and the wrap
width is reduced by the same amount so the hang never pushes text off the edge.

`maxLines` decides how much is shown: the TUI passes `1` for an annotation
away from the cursor — one line, ending in `…` if there is more — and `8` for
the one under it, which is what makes the cursor the reading position. Plain
output passes `0`, meaning no limit, because a file has no cursor.

## Tests

```sh
go test ./...
go test ./internal/render -update                              # rewrite goldens
go test ./internal/render -run TestWritePreviewSVG -preview docs/img   # screenshots
```

Golden files run without a TTY, so lipgloss degrades to plain text and the
goldens stay readable diffs of *layout* rather than walls of escape codes:

| file | what it pins |
| --- | --- |
| `testdata/basic.golden` | the grid, headers, meta rows, comfortable density |
| `testdata/basic-compact.golden` | the same diff with density off |
| `testdata/dense.golden` | rename, mode change, word diffs, over-wide lines, a conversation with state badges, a focused row |
| `testdata/dense-compact.golden` | the same, compact |
| `testdata/basic-split.golden` / `dense-split.golden` | the same diffs in split at width 160: pairing, filler, per-pane markers and overflow |

Colour is tested where colour lives: `theme_test.go` checks role completeness,
the aliases, that light and dark disagree about which end of the scale text
sits on, that text keeps contrast against every tint a theme paints behind it,
that the colour-blind presets stay off red and green, and that high-contrast
out-contrasts dark and underlines intra-line changes
rather than relying on shading.

## Screenshots

`TestWritePreviewSVG` renders `testdata/dense.diff` through the real renderer
at 24-bit colour — annotations expanded, as the plain-text path prints them —
and writes one SVG per theme to `docs/img/`, plus `layout-split.svg` for the
split layout at 160 columns. It is a test
because that is where the renderer, the fixture and the overlay already live,
and it is skipped unless `-preview` is passed. SVG rather than a terminal
capture: diffable in review, no font needed on the reader's machine, and the
true colours a 24-bit terminal would show.

Regenerate them whenever the row anatomy or a palette changes — a screenshot
that no longer matches the renderer is worse than none.
