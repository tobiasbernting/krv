package render

import (
	"html"
	"regexp"
	"strings"

	"github.com/yuin/goldmark/ast"
)

// Raw HTML in Markdown is mostly PR-template furniture: comments telling the
// author what to write, <details> folds, <br>, badges as <img>. None of it is
// rendered as HTML. Tags are stripped and their text kept; comments go; a few
// tags keep their meaning — a line break, an image, a link, emphasis — and
// <details> is always shown open, its <summary> as a heading line.

var (
	htmlTagRe  = regexp.MustCompile(`^<(/?)([A-Za-z][A-Za-z0-9-]*)((?:[^>"']|"[^"]*"|'[^']*')*)>`)
	htmlAttrRe = regexp.MustCompile(`(?i)([a-z][a-z0-9-]*)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+))`)
)

// htmlBlockTags break the line they open or close when they appear in an HTML
// block. Inline, only <br> does: a <div> in the middle of a sentence is rare
// and a line break there would be the surprise.
var htmlBlockTags = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true,
	"center": true, "dd": true, "details": true, "div": true, "dl": true,
	"dt": true, "figcaption": true, "figure": true, "footer": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"header": true, "hr": true, "li": true, "ol": true, "p": true, "pre": true,
	"section": true, "summary": true, "table": true, "tr": true, "ul": true,
}

// htmlState is the inline styling open HTML tags have put in force. It lives
// on the render, not the node, because a tag opened in one piece of raw HTML
// is closed in another: `<b>` and `</b>` either side of Markdown text.
type htmlState struct {
	bold, italic, strike, code int
	link                       int // an open <a href>, or -1
}

// apply lays the open tags' styling over st.
func (h htmlState) apply(st mdStyle, link int, t Theme) (mdStyle, int) {
	if h.bold > 0 {
		st.bold = true
	}
	if h.italic > 0 {
		st.italic = true
	}
	if h.strike > 0 {
		st.strike = true
	}
	if h.code > 0 {
		st.fg, st.bg = t.FileFg, t.HunkBg
	}
	if link < 0 && h.link >= 0 {
		link = h.link
		st.fg, st.underline = t.Accent, true
	}
	return st, link
}

func dec(n *int) {
	if *n > 0 {
		*n--
	}
}

func htmlBlockText(n *ast.HTMLBlock, src []byte) string {
	var b strings.Builder
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		b.Write(seg.Value(src))
	}
	if n.HasClosure() {
		b.Write(n.ClosureLine.Value(src))
	}
	return b.String()
}

// htmlBlock draws an HTML block as the text it contains, one line per block
// tag. It has no blank lines, so a template's scaffolding leaves no holes; a
// block that was only a comment draws nothing.
func (c *mdCtx) htmlBlock(n *ast.HTMLBlock, width int, st mdStyle) []mdLine {
	lines := c.wrap(c.htmlCells(htmlBlockText(n, c.src), st, -1, true), width)
	out := lines[:0]
	for _, l := range lines {
		if len(l.cells) > 0 {
			out = append(out, l)
		}
	}
	return out
}

// htmlCells turns raw HTML into cells. block says whether it is an HTML block
// rather than a tag in the middle of a paragraph, which decides whether block
// tags break the line.
func (c *mdCtx) htmlCells(raw string, st mdStyle, link int, block bool) []mdCell {
	var out []mdCell
	text := func(s string) {
		s = html.UnescapeString(s)
		// Whitespace in HTML is a space, whatever it is written as; lines
		// break at tags.
		s = strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' || r == '\t' }), " ")
		s2, l2 := c.html.apply(st, link, c.t)
		out = append(out, runeCells(s, s2, l2)...)
	}
	for raw != "" {
		i := strings.IndexByte(raw, '<')
		if i < 0 {
			text(raw)
			break
		}
		if i > 0 {
			text(raw[:i])
			raw = raw[i:]
		}
		switch {
		case strings.HasPrefix(raw, "<!--"):
			end := strings.Index(raw[4:], "-->")
			if end < 0 {
				raw = ""
			} else {
				raw = raw[4+end+3:]
			}
			continue
		case strings.HasPrefix(raw, "<!"), strings.HasPrefix(raw, "<?"):
			end := strings.IndexByte(raw, '>')
			if end < 0 {
				raw = ""
			} else {
				raw = raw[end+1:]
			}
			continue
		}
		m := htmlTagRe.FindStringSubmatch(raw)
		if m == nil {
			text("<")
			raw = raw[1:]
			continue
		}
		raw = raw[len(m[0]):]
		out = append(out, c.htmlTag(strings.ToLower(m[2]), m[1] == "/", m[3], st, link, block)...)
	}
	return out
}

func (c *mdCtx) htmlTag(name string, closing bool, attrs string, st mdStyle, link int, block bool) []mdCell {
	var out []mdCell
	newline := func() { out = append(out, mdCell{text: "\n", st: st, link: -1}) }
	h := &c.html
	switch name {
	case "br":
		newline()
		return out
	case "img":
		a := htmlAttrs(attrs)
		st2, l2 := h.apply(st, link, c.t)
		return c.image(a["alt"], a["src"], st2, l2)
	case "a":
		if closing {
			h.link = -1
		} else if href := htmlAttrs(attrs)["href"]; href != "" && link < 0 {
			h.link = c.addLink(href, LinkURL, "")
		}
		return nil
	case "b", "strong":
		if closing {
			dec(&h.bold)
		} else {
			h.bold++
		}
	case "i", "em":
		if closing {
			dec(&h.italic)
		} else {
			h.italic++
		}
	case "s", "del", "strike":
		if closing {
			dec(&h.strike)
		} else {
			h.strike++
		}
	case "code", "kbd", "tt":
		if closing {
			dec(&h.code)
		} else {
			h.code++
		}
	case "summary", "h1", "h2", "h3", "h4", "h5", "h6":
		// Headings in all but name: bold, on a line of their own.
		if closing {
			dec(&h.bold)
		} else {
			h.bold++
		}
		if block {
			newline()
			if !closing && name == "summary" && !c.flat {
				s2, _ := h.apply(st, -1, c.t)
				out = append(out, runeCells(mdSummary+" ", s2, -1)...)
			}
		}
		return out
	case "td", "th":
		if closing {
			out = append(out, mdCell{text: " ", w: 1, st: st, link: -1})
		}
		return out
	}
	if block && htmlBlockTags[name] {
		newline()
	}
	return out
}

// htmlAttrs reads a tag's attributes, lower-cased names to unescaped values.
func htmlAttrs(s string) map[string]string {
	out := map[string]string{}
	for _, m := range htmlAttrRe.FindAllStringSubmatch(s, -1) {
		v := m[2] + m[3] + m[4]
		out[strings.ToLower(m[1])] = html.UnescapeString(v)
	}
	return out
}
