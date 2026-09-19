package render

import (
	"fmt"
	"strings"

	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/util"
)

// The glyphs Markdown is drawn with.
const (
	mdQuoteBar = "│"
	mdRule     = "─"
	mdSummary  = "▾" // an expanded <details>: its body is always shown
	mdColGap   = 2   // spaces between table columns
	mdCodePad  = 2   // indent of a code block
)

// mdBullets cycle with list depth, so a nested item reads as nested even
// where its indent is lost in a narrow terminal.
var mdBullets = []string{"•", "◦", "▪"}

// mdCtx is one Markdown render in progress: the source, the theme and the
// links met so far, in the order they were met.
type mdCtx struct {
	src  []byte
	opts MarkdownOptions
	t    Theme
	base mdStyle

	// markers draws what colour would otherwise say — # before a heading,
	// backticks around code. flat is MarkdownPlain, which wants neither
	// markers nor glyphs.
	markers bool
	flat    bool

	links []Link
	depth int // list nesting
	html  htmlState
}

func newMdCtx(src string, opts MarkdownOptions) *mdCtx {
	t := opts.Theme
	base := mdStyle{fg: opts.Fg, bg: opts.Bg}
	if base.fg == "" {
		base.fg = t.Fg
	}
	if base.bg == "" {
		base.bg = t.Bg
	}
	return &mdCtx{src: []byte(src), opts: opts, t: t, base: base, html: htmlState{link: -1}}
}

func (c *mdCtx) addLink(url string, kind LinkKind, text string) int {
	url = strings.TrimSpace(sanitize(url))
	c.links = append(c.links, Link{URL: url, Kind: kind, Text: strings.Join(strings.Fields(text), " ")})
	return len(c.links) - 1
}

// finishLinks records where every link landed and renumbers them in reading
// order — top to bottom, left to right — which is the order a link cursor
// visits them. A link that drew nothing is dropped.
func (c *mdCtx) finishLinks(lines []mdLine) []Link {
	remap := make([]int, len(c.links))
	for i := range remap {
		remap[i] = -1
	}
	var out []Link
	var drawn []string // what each link drew, for one with no text of its own (<a href>)
	for li, l := range lines {
		col := 0
		for k := range l.cells {
			cl := &l.cells[k]
			if cl.link >= 0 {
				id := remap[cl.link]
				if id < 0 {
					id = len(out)
					remap[cl.link] = id
					out = append(out, c.links[cl.link])
					drawn = append(drawn, "")
				}
				cl.link = id
				spans := out[id].Spans
				if n := len(spans); n > 0 && spans[n-1].Line == li && spans[n-1].End == col {
					spans[n-1].End += cl.w
				} else {
					if len(spans) > 0 {
						drawn[id] += " "
					}
					out[id].Spans = append(spans, LinkSpan{Line: li, Start: col, End: col + cl.w})
				}
				drawn[id] += cl.text
			}
			col += cl.w
		}
	}
	for i := range out {
		if out[i].Text == "" {
			out[i].Text = strings.Join(strings.Fields(drawn[i]), " ")
		}
	}
	return out
}

// blocks lays out the block children of parent, a blank line between each
// unless tight — a tight list's items and what they contain.
func (c *mdCtx) blocks(parent ast.Node, width int, st mdStyle, tight bool) []mdLine {
	var out []mdLine
	for n := parent.FirstChild(); n != nil; n = n.NextSibling() {
		lines := c.block(n, width, st)
		if len(lines) == 0 {
			continue
		}
		if len(out) > 0 && !tight {
			out = append(out, mdLine{})
		}
		out = append(out, lines...)
	}
	return out
}

func (c *mdCtx) block(n ast.Node, width int, st mdStyle) []mdLine {
	width = maxi(width, 1)
	switch n := n.(type) {
	case *ast.Paragraph, *ast.TextBlock:
		return c.wrap(c.inlines(n, st, -1), width)
	case *ast.Heading:
		return c.heading(n, width, st)
	case *ast.ThematicBreak:
		rule := st
		rule.fg = c.t.Dim
		return []mdLine{{cells: runeCells(strings.Repeat(mdRule, width), rule, -1)}}
	case *ast.Blockquote:
		inner := st
		inner.fg = c.t.Dim
		lines := c.blocks(n, width-2, inner, false)
		bar := runeCells(mdQuoteBar+" ", inner, -1)
		return prefixLines(lines, bar, bar)
	case *ast.List:
		return c.list(n, width, st)
	case *ast.FencedCodeBlock:
		lang := strings.ToLower(string(n.Language(c.src)))
		if lang == "suggestion" {
			return c.suggestion(codeLines(n, c.src), width, st)
		}
		return c.code(lang, codeLines(n, c.src), width, st)
	case *ast.CodeBlock:
		return c.code("", codeLines(n, c.src), width, st)
	case *ast.HTMLBlock:
		return c.htmlBlock(n, width, st)
	case *east.Table:
		return c.table(n, width, st)
	}
	return c.blocks(n, width, st, false)
}

// wrap lays out inline cells, trimming blank lines from either end; a
// paragraph that was nothing but an HTML comment draws nothing at all.
func (c *mdCtx) wrap(cells []mdCell, width int) []mdLine {
	raw := wrapCells(cells, width)
	for len(raw) > 0 && len(raw[0]) == 0 {
		raw = raw[1:]
	}
	for len(raw) > 0 && len(raw[len(raw)-1]) == 0 {
		raw = raw[:len(raw)-1]
	}
	out := make([]mdLine, len(raw))
	for i, l := range raw {
		out[i] = mdLine{cells: l}
	}
	return out
}

func (c *mdCtx) heading(n *ast.Heading, width int, st mdStyle) []mdLine {
	s := st
	s.bold = true
	s.fg = c.t.FileFg
	if n.Level <= 2 {
		s.fg = c.t.Accent
	}
	cells := c.inlines(n, s, -1)
	if c.markers {
		cells = append(runeCells(strings.Repeat("#", n.Level)+" ", s, -1), cells...)
	}
	return c.wrap(cells, width)
}

// list lays out a list. A marker hangs its item: the item's own lines indent
// to where its text starts. A task item's checkbox takes the bullet's place.
func (c *mdCtx) list(l *ast.List, width int, st mdStyle) []mdLine {
	c.depth++
	defer func() { c.depth-- }()

	count := 0
	for it := l.FirstChild(); it != nil; it = it.NextSibling() {
		count++
	}
	numW := digits(l.Start + count - 1)

	var out []mdLine
	i := 0
	for it := l.FirstChild(); it != nil; it = it.NextSibling() {
		ms := st
		ms.fg = c.t.Dim
		var marker []mdCell
		if l.IsOrdered() {
			marker = runeCells(fmt.Sprintf("%*d%c ", numW, l.Start+i, l.Marker), ms, -1)
		} else {
			marker = runeCells(mdBullets[(c.depth-1)%len(mdBullets)]+" ", ms, -1)
		}
		if box, ok := taskBox(it); ok {
			bs := st
			bs.fg = c.t.Dim
			label := "[ ] "
			if box.IsChecked {
				label = "[x] "
				bs.fg = c.t.ReviewedFg
			}
			if l.IsOrdered() {
				marker = append(marker, runeCells(label, bs, -1)...)
			} else {
				marker = runeCells(label, bs, -1)
			}
		}
		mw := cellsWidth(marker)
		body := c.blocks(it, width-mw, st, l.IsTight)
		if len(body) == 0 {
			body = []mdLine{{}}
		}
		if i > 0 && !l.IsTight {
			out = append(out, mdLine{})
		}
		out = append(out, prefixLines(body, marker, runeCells(strings.Repeat(" ", mw), st, -1))...)
		i++
	}
	return out
}

// taskBox finds a task list item's checkbox, which GFM puts first in the
// item's first paragraph.
func taskBox(item ast.Node) (*east.TaskCheckBox, bool) {
	first := item.FirstChild()
	if first == nil || first.FirstChild() == nil {
		return nil, false
	}
	box, ok := first.FirstChild().(*east.TaskCheckBox)
	return box, ok
}

// codeLines is a code block's content, one entry per line.
func codeLines(n ast.Node, src []byte) []string {
	var out []string
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		out = append(out, strings.TrimRight(string(seg.Value(src)), "\r\n"))
	}
	return out
}

// code draws a code block as an indented panel, highlighted by its fence's
// language. Long lines wrap rather than clip: this is a reader, and there is
// no horizontal scroll to find the rest with.
func (c *mdCtx) code(lang string, lines []string, width int, st mdStyle) []mdLine {
	src := strings.ReplaceAll(strings.Join(lines, "\n"), "\t", strings.Repeat(" ", tabWidth))
	var segs [][]Segment
	if c.opts.Highlighter != nil && lang != "" {
		segs = c.opts.Highlighter.Lang(lang, src)
	} else {
		segs = splitPlain(src)
	}

	panel := st
	panel.fg, panel.bg = c.t.Fg, c.t.HunkBg
	indent := runeCells(strings.Repeat(" ", mdCodePad), st, -1)
	inner := maxi(width-mdCodePad-1, 1) // a column of panel either side
	var out []mdLine
	for _, line := range segs {
		var cells []mdCell
		for _, sg := range line {
			s := panel
			if sg.Fg != "" {
				amount := c.t.SyntaxMute
				if sg.Emph {
					amount = c.t.SyntaxMuteEmph
				}
				s.fg = mix(sg.Fg, c.t.Bg, amount)
			}
			cells = append(cells, runeCells(sg.Text, s, -1)...)
		}
		for _, chunk := range chunkCells(cells, inner) {
			row := append([]mdCell{}, indent...)
			row = append(row, mdCell{text: " ", w: 1, st: panel, link: -1})
			row = append(row, chunk...)
			if pad := inner - cellsWidth(chunk); pad > 0 {
				row = append(row, runeCells(strings.Repeat(" ", pad), panel, -1)...)
			}
			out = append(out, mdLine{cells: row})
		}
	}
	return out
}

// suggestion hands a ```suggestion block to the caller's hook, which can draw
// it as the change it proposes. Without one, it is code with a label saying
// what it is.
func (c *mdCtx) suggestion(lines []string, width int, st mdStyle) []mdLine {
	if c.opts.Suggestion != nil {
		if drawn, ok := c.opts.Suggestion(lines, width); ok {
			out := make([]mdLine, len(drawn))
			for i, l := range drawn {
				out[i] = mdLine{raw: l}
			}
			return out
		}
	}
	label := st
	label.fg = c.t.Dim
	out := []mdLine{{cells: runeCells(strings.Repeat(" ", mdCodePad)+"suggestion", label, -1)}}
	return append(out, c.code("", lines, width, st)...)
}

// table draws a GFM table as aligned columns under a rule. Cells do not wrap:
// when the table is too wide the widest column gives way first, and cut cells
// end in ….
func (c *mdCtx) table(tb *east.Table, width int, st mdStyle) []mdLine {
	var rows [][][]mdCell
	for r := tb.FirstChild(); r != nil; r = r.NextSibling() {
		s := st
		if r.Kind() == east.KindTableHeader {
			s.bold = true
		}
		var row [][]mdCell
		for cell := r.FirstChild(); cell != nil; cell = cell.NextSibling() {
			cs := c.inlines(cell, s, -1)
			for k := range cs {
				if cs[k].isNewline() {
					cs[k].text, cs[k].w = " ", 1
				}
			}
			row = append(row, cs)
		}
		rows = append(rows, row)
	}
	cols := len(tb.Alignments)
	for _, r := range rows {
		cols = maxi(cols, len(r))
	}
	if cols == 0 {
		return nil
	}
	widths := make([]int, cols)
	for _, r := range rows {
		for i, cell := range r {
			widths[i] = maxi(widths[i], cellsWidth(cell))
		}
	}
	total := func() int {
		t := mdColGap * (cols - 1)
		for _, w := range widths {
			t += w
		}
		return t
	}
	for total() > width {
		widest := 0
		for i, w := range widths {
			if w > widths[widest] {
				widest = i
			}
		}
		if widths[widest] <= 1 {
			break
		}
		widths[widest]--
	}

	gap := runeCells(strings.Repeat(" ", mdColGap), st, -1)
	var out []mdLine
	for ri, r := range rows {
		var line []mdCell
		for i := 0; i < cols; i++ {
			if i > 0 {
				line = append(line, gap...)
			}
			var cell []mdCell
			if i < len(r) {
				cell = truncCells(r[i], widths[i])
			}
			align := east.AlignNone
			if i < len(tb.Alignments) {
				align = tb.Alignments[i]
			}
			pad := widths[i] - cellsWidth(cell)
			left := 0
			switch align {
			case east.AlignRight:
				left = pad
			case east.AlignCenter:
				left = pad / 2
			}
			line = append(line, runeCells(strings.Repeat(" ", left), st, -1)...)
			line = append(line, cell...)
			if i < cols-1 {
				line = append(line, runeCells(strings.Repeat(" ", pad-left), st, -1)...)
			}
		}
		out = append(out, mdLine{cells: line})
		if ri == 0 {
			rule := st
			rule.fg = c.t.Dim
			var sep []mdCell
			for i, w := range widths {
				if i > 0 {
					sep = append(sep, gap...)
				}
				sep = append(sep, runeCells(strings.Repeat(mdRule, w), rule, -1)...)
			}
			out = append(out, mdLine{cells: sep})
		}
	}
	return out
}

// inlines lays out the inline children of n as one run of cells.
func (c *mdCtx) inlines(n ast.Node, st mdStyle, link int) []mdCell {
	var out []mdCell
	for ch := n.FirstChild(); ch != nil; ch = ch.NextSibling() {
		out = append(out, c.inline(ch, st, link)...)
	}
	return out
}

func (c *mdCtx) inline(n ast.Node, st mdStyle, link int) []mdCell {
	switch n := n.(type) {
	case *ast.Text:
		st, link = c.html.apply(st, link, c.t)
		out := runeCells(c.textValue(n), st, link)
		switch {
		case n.HardLineBreak():
			out = append(out, mdCell{text: "\n", st: st, link: link})
		case n.SoftLineBreak():
			out = append(out, mdCell{text: " ", w: 1, st: st, link: link})
		}
		return out
	case *ast.String:
		st, link = c.html.apply(st, link, c.t)
		return runeCells(string(n.Value), st, link)
	case *ast.CodeSpan:
		return c.codeSpan(codeSpanText(n, c.src), st, link)
	case *ast.Emphasis:
		if n.Level >= 2 {
			st.bold = true
		} else {
			st.italic = true
		}
		return c.inlines(n, st, link)
	case *east.Strikethrough:
		st.strike = true
		return c.inlines(n, st, link)
	case *ast.Link:
		dest := unescape(n.Destination)
		if link < 0 {
			link = c.addLink(dest, LinkURL, plainInline(n, c.src))
		}
		out := c.inlines(n, c.linkStyle(st), link)
		if len(out) == 0 {
			out = runeCells(dest, c.linkStyle(st), link)
		}
		return out
	case *ast.AutoLink:
		label := string(n.Label(c.src))
		url := string(n.URL(c.src))
		switch {
		case n.AutoLinkType == ast.AutoLinkEmail && !strings.HasPrefix(strings.ToLower(url), "mailto:"):
			url = "mailto:" + url
		case n.AutoLinkType == ast.AutoLinkURL && !strings.Contains(url, "://"):
			url = "http://" + url // linkify's bare www.
		}
		if link < 0 {
			link = c.addLink(url, LinkURL, label)
		}
		return runeCells(label, c.linkStyle(st), link)
	case *ast.Image:
		return c.image(plainInline(n, c.src), unescape(n.Destination), st, link)
	case *east.TaskCheckBox:
		return nil // drawn as the list marker
	case *ast.RawHTML:
		var b strings.Builder
		for i := 0; i < n.Segments.Len(); i++ {
			seg := n.Segments.At(i)
			b.Write(seg.Value(c.src))
		}
		return c.htmlCells(b.String(), st, link, false)
	}
	return c.inlines(n, st, link)
}

func (c *mdCtx) linkStyle(st mdStyle) mdStyle {
	st.fg = c.t.Accent
	st.underline = true
	return st
}

func (c *mdCtx) codeSpan(text string, st mdStyle, link int) []mdCell {
	if link < 0 {
		st.fg, st.bg = c.t.FileFg, c.t.HunkBg
	}
	if c.markers {
		text = "`" + text + "`"
	}
	return runeCells(text, st, link)
}

// image draws an image as [image: alt] — nothing is fetched — and makes it a
// link to its URL. An image inside a link (a badge) belongs to that link
// instead: the badge's own picture is not what anyone wants to open.
func (c *mdCtx) image(alt, url string, st mdStyle, link int) []mdCell {
	label := imageLabel(alt)
	if link < 0 {
		link = c.addLink(url, LinkImage, label)
	}
	st.fg = c.t.Dim
	st.underline = !c.flat
	return runeCells(label, st, link)
}

func imageLabel(alt string) string {
	if alt = strings.Join(strings.Fields(alt), " "); alt != "" {
		return "[image: " + alt + "]"
	}
	return "[image]"
}

// textValue is a text node's text with backslash escapes and entities
// resolved, which the parser leaves to the renderer.
func (c *mdCtx) textValue(n *ast.Text) string {
	v := n.Segment.Value(c.src)
	if n.IsRaw() {
		return string(v)
	}
	return unescape(v)
}

func unescape(v []byte) string {
	v = util.UnescapePunctuations(v)
	v = util.ResolveNumericReferences(v)
	v = util.ResolveEntityNames(v)
	return string(v)
}

func codeSpanText(n *ast.CodeSpan, src []byte) string {
	var b strings.Builder
	for ch := n.FirstChild(); ch != nil; ch = ch.NextSibling() {
		if t, ok := ch.(*ast.Text); ok {
			b.WriteString(strings.ReplaceAll(string(t.Segment.Value(src)), "\n", " "))
		}
	}
	return b.String()
}

// plainInline is the text of an inline subtree with all markup gone: a link's
// text, an image's alt.
func plainInline(n ast.Node, src []byte) string {
	var b strings.Builder
	var walk func(ast.Node)
	walk = func(n ast.Node) {
		for ch := n.FirstChild(); ch != nil; ch = ch.NextSibling() {
			switch ch := ch.(type) {
			case *ast.Text:
				b.WriteString(unescape(ch.Segment.Value(src)))
				if ch.SoftLineBreak() || ch.HardLineBreak() {
					b.WriteByte(' ')
				}
			case *ast.String:
				b.Write(ch.Value)
			case *ast.CodeSpan:
				b.WriteString(codeSpanText(ch, src))
			case *ast.AutoLink:
				b.Write(ch.Label(src))
			case *ast.Image:
				b.WriteString(imageLabel(plainInline(ch, src)))
			case *ast.RawHTML:
			default:
				walk(ch)
			}
		}
	}
	walk(n)
	return sanitize(b.String())
}

// flatten writes the text of every block under n, for MarkdownPlain.
func (c *mdCtx) flatten(n ast.Node, b *strings.Builder) {
	for ch := n.FirstChild(); ch != nil; ch = ch.NextSibling() {
		switch ch := ch.(type) {
		case *ast.Paragraph, *ast.TextBlock, *ast.Heading, *east.TableCell:
			b.WriteString(cellsText(c.inlines(ch, mdStyle{}, -1)))
		case *ast.FencedCodeBlock, *ast.CodeBlock:
			b.WriteString(strings.Join(codeLines(ch, c.src), " "))
		case *ast.HTMLBlock:
			b.WriteString(cellsText(c.htmlCells(htmlBlockText(ch, c.src), mdStyle{}, -1, true)))
		case *ast.ListItem:
			if box, ok := taskBox(ch); ok {
				if box.IsChecked {
					b.WriteString("[x] ")
				} else {
					b.WriteString("[ ] ")
				}
			}
			c.flatten(ch, b)
		default:
			c.flatten(ch, b)
		}
		b.WriteByte(' ')
	}
}
