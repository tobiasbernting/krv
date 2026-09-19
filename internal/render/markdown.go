package render

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

// Markdown rendering: pull request descriptions and review comments are
// Markdown, and krv draws them as styled terminal lines rather than raw text.
//
// goldmark parses (CommonMark plus the GFM tables, strikethrough, autolinks
// and task lists); everything after the parse is ours. Blocks lay out into
// lines of cells — one cell per terminal column's worth of text, each with its
// style and the link it belongs to — and only then are the lines painted. Cells
// are what let link positions survive wrapping, and what let Rendered.Focus
// repaint one link without laying the page out again.

// LinkKind says what a Link points at.
type LinkKind int

const (
	LinkURL   LinkKind = iota // a Markdown link, an autolink, an <a href>
	LinkImage                 // an image, drawn as [image: alt]; never fetched
)

func (k LinkKind) String() string {
	if k == LinkImage {
		return "image"
	}
	return "link"
}

// Link is one link in rendered Markdown. A link that wraps occupies more than
// one line, so it has one Span per line, in order.
type Link struct {
	URL   string
	Kind  LinkKind
	Text  string // what the link reads as: its text, or [image: alt]
	Spans []LinkSpan
}

// LinkSpan is where part of a link sits: a line of Rendered.Lines and the
// display columns [Start, End) on it.
type LinkSpan struct {
	Line       int
	Start, End int
}

// MarkdownOptions configures Markdown. The zero value renders uncoloured
// text with no highlighting.
type MarkdownOptions struct {
	Theme Theme

	// Highlighter colours fenced code blocks by their info string. Nil
	// leaves code uncoloured.
	Highlighter *Highlighter

	// Fg and Bg are the colours prose is drawn in and every line is padded
	// with; empty means the theme's Fg and Bg. An annotation panel passes its
	// own, so the Markdown sits on the panel rather than punching through it.
	Fg, Bg string

	// NoColor draws Markdown the way it has to read with colour off: heading
	// levels as #, ##, inline code in backticks, and no syntax colour. It is
	// implied when lipgloss has no colour to give (NO_COLOR, or not a
	// terminal), because bold and underline go with it.
	NoColor bool

	// Hyperlinks wraps every link in an OSC 8 hyperlink, so a terminal that
	// supports them can open it on click. Off for anything that is not a
	// live terminal: golden files, piped output.
	Hyperlinks bool

	// Suggestion draws a ```suggestion block. It gets the proposed lines and
	// the width it may use, and returns painted lines — no wider than that —
	// and true. Returning false, or a nil hook, draws the block as plain code
	// labelled "suggestion". The caller owns the hook because only it knows
	// which lines the suggestion replaces.
	Suggestion func(proposed []string, width int) ([]string, bool)
}

// Rendered is Markdown laid out to a width.
//
// Lines are ready to print: styled, padded to the width with the background,
// and — with Hyperlinks — carrying OSC 8 around each link. Links are in
// reading order, with their positions in Lines.
//
// A link cursor is the caller's: keep an index into Links and print
// Focus(index) instead of Lines. Text wraps one column short of the width so
// that the focus marker always has room; positions in Links are those of
// Lines, and on the focused link's first line everything from the link on
// sits one column further right.
type Rendered struct {
	Lines []string
	Links []Link

	width int
	lines []mdLine
	paint *mdPainter
}

// focusMark leads the focused link, so focus reads without reverse video.
const focusMark = "›"

// Focus returns Lines with link i focused: reverse video, led by a › marker.
// An index outside Links returns Lines unchanged.
func (r Rendered) Focus(i int) []string {
	if i < 0 || i >= len(r.Links) || r.paint == nil {
		return r.Lines
	}
	out := append([]string(nil), r.Lines...)
	for si, sp := range r.Links[i].Spans {
		line := r.lines[sp.Line]
		if si == 0 {
			line = withMarker(line, sp.Start, r.paint.marker())
		}
		out[sp.Line] = r.paint.line(line, r.Links, r.width, i)
	}
	return out
}

// LinkAt returns the index of the link at a display column of a line, for
// resolving a mouse click against Lines.
func (r Rendered) LinkAt(line, col int) (int, bool) {
	for i, l := range r.Links {
		for _, sp := range l.Spans {
			if sp.Line == line && col >= sp.Start && col < sp.End {
				return i, true
			}
		}
	}
	return -1, false
}

// withMarker inserts the focus marker in front of the cell at display column
// col, returning a copy.
func withMarker(l mdLine, col int, mark mdCell) mdLine {
	at, w := len(l.cells), 0
	for k, c := range l.cells {
		if w >= col {
			at = k
			break
		}
		w += c.w
	}
	cells := make([]mdCell, 0, len(l.cells)+1)
	cells = append(cells, l.cells[:at]...)
	cells = append(cells, mark)
	cells = append(cells, l.cells[at:]...)
	return mdLine{cells: cells, raw: l.raw}
}

var mdParser = goldmark.New(goldmark.WithExtensions(
	extension.Table,
	extension.Strikethrough,
	extension.Linkify,
	extension.TaskList,
)).Parser()

// Markdown renders src at width columns. See Rendered for what comes back
// and how to draw a focused link.
func Markdown(src string, width int, opts MarkdownOptions) Rendered {
	if width <= 0 {
		return Rendered{}
	}
	c := newMdCtx(src, opts)
	c.markers = opts.NoColor || lipgloss.ColorProfile() == termenv.Ascii
	if opts.NoColor {
		c.opts.Highlighter = nil
	}

	doc := mdParser.Parse(text.NewReader(c.src))
	lines := c.blocks(doc, maxi(width-1, 1), c.base, false)
	for i := range lines {
		// Only a width too narrow for a list marker or a quote bar gets
		// here; the text itself was already laid out to fit.
		lines[i].cells = clipCells(lines[i].cells, width)
	}
	links := c.finishLinks(lines)

	p := &mdPainter{base: c.base, accent: c.t.Accent, hyperlinks: opts.Hyperlinks, styles: map[mdStyle]lipgloss.Style{}}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = p.line(l, links, width, -1)
	}
	return Rendered{Lines: out, Links: links, width: width, lines: lines, paint: p}
}

// MarkdownPlain flattens Markdown into one plain line, for a collapsed row:
// emphasis dropped, links reduced to their text, images to [image: alt], code
// blocks to their content, HTML comments gone and whitespace collapsed.
func MarkdownPlain(src string) string {
	c := newMdCtx(src, MarkdownOptions{})
	c.flat = true
	var b strings.Builder
	c.flatten(mdParser.Parse(text.NewReader(c.src)), &b)
	return strings.Join(strings.Fields(b.String()), " ")
}

// mdStyle is how one cell is drawn. It is a map key, so it stays comparable.
type mdStyle struct {
	fg, bg                                   string
	bold, italic, strike, underline, reverse bool
}

// mdCell is one character's worth of a line: a rune and any zero-width runes
// that combine with it, its display width, its style, and the index of the
// link it belongs to (-1 for none).
type mdCell struct {
	text string
	w    int
	st   mdStyle
	link int
}

func (c mdCell) isSpace() bool   { return c.text == " " }
func (c mdCell) isNewline() bool { return c.text == "\n" }

// mdLine is one laid-out line. raw, when set, is a line the Suggestion hook
// painted; cells are then only the prefix (list indent, quote bar) before it.
type mdLine struct {
	cells []mdCell
	raw   string
}

func cellsWidth(cells []mdCell) int {
	w := 0
	for _, c := range cells {
		w += c.w
	}
	return w
}

func cellsText(cells []mdCell) string {
	var b strings.Builder
	for _, c := range cells {
		if c.isNewline() {
			b.WriteByte(' ')
			continue
		}
		b.WriteString(c.text)
	}
	return b.String()
}

// wrapCells lays a run of inline cells out at width. Explicit newlines (hard
// breaks, <br>) end a line; otherwise lines break at spaces, and between any
// two wide characters, since CJK text has no spaces to break at. A word longer
// than the line is broken wherever it has to be.
func wrapCells(cells []mdCell, width int) [][]mdCell {
	var lines [][]mdCell
	var cur []mdCell
	curW := 0
	var space []mdCell // the pending separator, collapsed to one cell
	for i := 0; i < len(cells); {
		cl := cells[i]
		switch {
		case cl.isNewline():
			lines = append(lines, cur)
			cur, curW, space = nil, 0, nil
			i++
			continue
		case cl.isSpace():
			if space == nil {
				space = cells[i : i+1]
			}
			i++
			continue
		}
		j := i + 1
		if cl.w < 2 {
			for j < len(cells) && !cells[j].isSpace() && !cells[j].isNewline() && cells[j].w < 2 {
				j++
			}
		}
		word := cells[i:j]
		sepW := len(space)
		if curW > 0 && curW+sepW+cellsWidth(word) > width {
			lines = append(lines, cur)
			cur, curW = nil, 0
		}
		if curW > 0 {
			cur = append(cur, space...)
			curW += sepW
		}
		space = nil
		for _, wc := range word {
			if curW > 0 && curW+wc.w > width {
				lines = append(lines, cur)
				cur, curW = nil, 0
			}
			cur = append(cur, wc)
			curW += wc.w
		}
		i = j
	}
	return append(lines, cur)
}

// chunkCells breaks a line of code at width without looking for spaces: code
// keeps its exact text, and a wrapped line of it still reads as one line.
func chunkCells(cells []mdCell, width int) [][]mdCell {
	var out [][]mdCell
	var cur []mdCell
	w := 0
	for _, c := range cells {
		if w > 0 && w+c.w > width {
			out = append(out, cur)
			cur, w = nil, 0
		}
		cur = append(cur, c)
		w += c.w
	}
	return append(out, cur)
}

// clipCells cuts cells to width, silently.
func clipCells(cells []mdCell, width int) []mdCell {
	w := 0
	for i, c := range cells {
		if w+c.w > width {
			return cells[:i]
		}
		w += c.w
	}
	return cells
}

// truncCells cuts cells to width, ending in … when anything was cut.
func truncCells(cells []mdCell, width int) []mdCell {
	if cellsWidth(cells) <= width {
		return cells
	}
	if width <= 0 {
		return nil
	}
	var out []mdCell
	w := 0
	for _, c := range cells {
		if w+c.w > width-1 {
			break
		}
		out = append(out, c)
		w += c.w
	}
	st := mdStyle{}
	if len(out) > 0 {
		st = out[len(out)-1].st
	} else if len(cells) > 0 {
		st = cells[0].st
	}
	return append(out, mdCell{text: "…", w: 1, st: st, link: -1})
}

// prefixLines puts first in front of the first line and rest in front of the
// others: a list marker and its hanging indent, or a quote bar on every line.
func prefixLines(lines []mdLine, first, rest []mdCell) []mdLine {
	out := make([]mdLine, len(lines))
	for i, l := range lines {
		p := rest
		if i == 0 {
			p = first
		}
		cells := make([]mdCell, 0, len(p)+len(l.cells))
		cells = append(cells, p...)
		cells = append(cells, l.cells...)
		out[i] = mdLine{cells: cells, raw: l.raw}
	}
	return out
}

// mdPainter turns laid-out lines into strings. It outlives Markdown inside
// Rendered, so Focus can repaint with the same cached styles.
type mdPainter struct {
	base       mdStyle
	accent     string
	hyperlinks bool
	styles     map[mdStyle]lipgloss.Style
}

func (p *mdPainter) style(s mdStyle) lipgloss.Style {
	if st, ok := p.styles[s]; ok {
		return st
	}
	st := lipgloss.NewStyle()
	if s.fg != "" {
		st = st.Foreground(lipgloss.Color(s.fg))
	}
	if s.bg != "" {
		st = st.Background(lipgloss.Color(s.bg))
	}
	st = st.Bold(s.bold).Italic(s.italic).Strikethrough(s.strike).
		Underline(s.underline).Reverse(s.reverse)
	p.styles[s] = st
	return st
}

func (p *mdPainter) marker() mdCell {
	st := p.base
	st.fg, st.bold = p.accent, true
	return mdCell{text: focusMark, w: 1, st: st, link: -1}
}

// line paints one line: runs of cells sharing a style and a link become one
// styled string, a link run is wrapped in OSC 8 when hyperlinks are on, and
// the line is padded to width in the base background. Cells of link focus are
// drawn in reverse video.
func (p *mdPainter) line(l mdLine, links []Link, width, focus int) string {
	var b strings.Builder
	w := 0
	cells := clipCells(l.cells, width)
	for i := 0; i < len(cells); {
		j := i + 1
		for j < len(cells) && cells[j].st == cells[i].st && cells[j].link == cells[i].link {
			j++
		}
		var t strings.Builder
		for _, c := range cells[i:j] {
			t.WriteString(c.text)
			w += c.w
		}
		st := cells[i].st
		link := cells[i].link
		if link >= 0 && link == focus {
			st.reverse = true
		}
		s := p.style(st).Render(t.String())
		if p.hyperlinks && link >= 0 && link < len(links) {
			s = ansi.SetHyperlink(links[link].URL) + s + ansi.ResetHyperlink()
		}
		b.WriteString(s)
		i = j
	}
	if l.raw != "" && w < width {
		raw := ansi.Truncate(l.raw, width-w, "")
		b.WriteString(raw)
		w += ansi.StringWidth(raw)
	}
	if w < width {
		pad := p.base
		pad.bold, pad.italic, pad.strike, pad.underline = false, false, false, false
		b.WriteString(p.style(pad).Render(strings.Repeat(" ", width-w)))
	}
	return b.String()
}

// sanitize drops control characters. Markdown here is someone else's text,
// and an escape sequence in a PR description must not reach the terminal.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n':
			return r
		case r < 0x20, r >= 0x7f && r < 0xa0:
			return -1
		}
		return r
	}, s)
}

// runeCells splits s into cells, attaching zero-width runes to the cell before
// them. Newlines become newline cells and tabs a space; callers that need tabs
// expanded do so first.
func runeCells(s string, st mdStyle, link int) []mdCell {
	var out []mdCell
	for _, r := range sanitize(s) {
		switch r {
		case '\n':
			out = append(out, mdCell{text: "\n", st: st, link: link})
			continue
		case '\t':
			r = ' '
		}
		w := runewidth.RuneWidth(r)
		if w == 0 {
			if n := len(out); n > 0 && !out[n-1].isNewline() {
				out[n-1].text += string(r)
			}
			continue
		}
		out = append(out, mdCell{text: string(r), w: w, st: st, link: link})
	}
	return out
}
