package render

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// mdText renders src the way the tests read it: escapes stripped, trailing
// padding trimmed. Tests run without a TTY, so lipgloss has no colour and the
// renderer draws its no-colour markers.
func mdText(src string, width int, opts MarkdownOptions) []string {
	return plainLines(Markdown(src, width, opts).Lines)
}

func plainLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = strings.TrimRight(ansi.Strip(l), " ")
	}
	return out
}

func TestMarkdownElements(t *testing.T) {
	for _, tc := range []struct {
		name  string
		src   string
		width int
		want  []string
	}{
		{"headings mark their level without colour", "# One\n\n## Two\n\n###### Six",
			40, []string{"# One", "", "## Two", "", "###### Six"}},
		{"emphasis, strong and strikethrough keep only their text", "a *b* **c** ~~d~~ ***e***",
			40, []string{"a b c d e"}},
		{"inline code is backticked without colour", "run `go test` now",
			40, []string{"run `go test` now"}},
		{"links read as their text", "see [the docs](https://example.com) first",
			40, []string{"see the docs first"}},
		{"a link with no text reads as its URL", "see [](https://example.com)",
			40, []string{"see https://example.com"}},
		{"autolinks and bare URLs read as themselves", "<https://a.example> and https://b.example/x",
			60, []string{"https://a.example and https://b.example/x"}},
		{"bullets nest with their own glyph", "- one\n- two\n  - three\n    - four",
			40, []string{"• one", "• two", "  ◦ three", "    ▪ four"}},
		{"numbers keep their start and align", "9. nine\n10. ten",
			40, []string{" 9. nine", "10. ten"}},
		{"a loose list keeps its air", "- one\n\n- two",
			40, []string{"• one", "", "• two"}},
		{"list items hang their wrapped text", "- alpha beta gamma delta",
			14, []string{"• alpha beta", "  gamma delta"}},
		{"task list boxes replace the bullet", "- [ ] todo\n- [x] done",
			40, []string{"[ ] todo", "[x] done"}},
		{"numbered tasks keep the number", "1. [x] done",
			40, []string{"1. [x] done"}},
		{"block quotes carry a bar on every line", "> quoted words that wrap\n>\n> again",
			16, []string{"│ quoted words", "│ that wrap", "│", "│ again"}},
		{"nested quotes stack bars", "> outer\n>> inner",
			40, []string{"│ outer", "│", "│ │ inner"}},
		{"fenced code keeps its text and indentation", "```go\nif x {\n\treturn\n}\n```",
			40, []string{"   if x {", "       return", "   }"}},
		{"indented code is code too", "    a  b",
			40, []string{"   a  b"}},
		{"long code lines wrap rather than clip", "```\nabcdefghij\n```",
			10, []string{"   abcdef", "   ghij"}},
		{"tables align their columns under a rule", "| name | n |\n|:-----|--:|\n| a | 100 |\n| bb | 2 |",
			40, []string{"name    n", "────  ───", "a     100", "bb      2"}},
		{"centred columns centre", "| x |\n|:-:|\n| abcde |",
			40, []string{"  x", "─────", "abcde"}},
		{"too-wide tables give way at the widest column", "| a | b |\n|---|---|\n| short | a much longer cell |",
			20, []string{"a      b", "─────  ────────────", "short  a much long…"}},
		{"thematic break is a rule one short of the width", "a\n\n---\n\nb",
			10, []string{"a", "", "─────────", "", "b"}},
		{"soft breaks are spaces, hard breaks are lines", "one\ntwo  \nthree\\\nfour",
			40, []string{"one two", "three", "four"}},
		{"escapes and entities resolve", `a &amp; b \*c\* &#65;`,
			40, []string{"a & b *c* A"}},
		{"control characters never reach the terminal", "red \x1b[31mtext\x07",
			40, []string{"red [31mtext"}},
		{"images are placeholders", "![a cat](cat.png) ![](x.png)",
			40, []string{"[image: a cat] [image]"}},
		{"html comments are dropped", "<!-- say what changed -->\n\ntext <!-- inline --> here",
			40, []string{"text here"}},
		{"a comment spanning blank lines is one block", "<!--\nfirst\n\nsecond\n-->\nafter",
			40, []string{"after"}},
		{"details shows its summary then its body", "<details>\n<summary>Logs</summary>\n\nbody text\n\n</details>",
			40, []string{"▾ Logs", "", "body text"}},
		{"br breaks the line", "one<br>two<br/>three",
			40, []string{"one", "two", "three"}},
		{"img tags are images", `<img alt="shot" src="https://x/y.png" width="10">`,
			40, []string{"[image: shot]"}},
		{"tags are stripped and their text kept", `<p align="center">Hello <b>there</b> &amp; <kbd>q</kbd></p>`,
			40, []string{"Hello there & q"}},
		{"html blocks break at block tags", "<div>\n<p>one</p><p>two</p>\n</div>",
			40, []string{"one", "two"}},
		{"a lone < survives", "a <3 b",
			40, []string{"a <3 b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := mdText(tc.src, tc.width, MarkdownOptions{Theme: DefaultTheme()})
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
		})
	}
}

// Every line is exactly the width asked for, whatever it holds, so a caller
// can stack them into a panel without measuring.
func TestMarkdownLinesFillTheWidth(t *testing.T) {
	src := "# Head\n\n- a list item that wraps a few times over\n\n> quote\n\n```\ncode that is long enough to wrap\n```\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n中文字符和English混合的一段文字，没有空格。\n\n![img](x) [link](y)"
	for _, width := range []int{1, 2, 5, 13, 40} {
		r := Markdown(src, width, MarkdownOptions{Theme: DefaultTheme()})
		for i, l := range r.Lines {
			if w := ansi.StringWidth(l); w != width {
				t.Errorf("width %d: line %d is %d wide: %q", width, i, w, ansi.Strip(l))
			}
		}
	}
}

// CJK text has no spaces to break at. It has to wrap between characters, and
// a double-width character must never be split across the edge.
func TestMarkdownWrapsEastAsianText(t *testing.T) {
	src := "日本語のテキストはスペースがありません。English words mixed 中文。"
	got := mdText(src, 13, MarkdownOptions{})
	want := []string{
		"日本語のテキ",
		"ストはスペー",
		"スがありませ",
		"ん。English",
		"words mixed",
		"中文。",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, l := range got {
		if w := ansi.StringWidth(l); w > 12 {
			t.Errorf("line %q is %d wide, past the 12 columns text may use", l, w)
		}
	}
}

func TestMarkdownLinks(t *testing.T) {
	src := "Read [the guide](https://g.example) or <https://a.example>.\n\n" +
		"![diagram](https://img.example/d.png)\n\n" +
		"[![build](https://badge.example/b.svg)](https://ci.example)\n\n" +
		"<a href=\"https://html.example\">html link</a>"
	r := Markdown(src, 60, MarkdownOptions{Theme: DefaultTheme()})
	want := []Link{
		{URL: "https://g.example", Kind: LinkURL, Text: "the guide", Spans: []LinkSpan{{Line: 0, Start: 5, End: 14}}},
		{URL: "https://a.example", Kind: LinkURL, Text: "https://a.example", Spans: []LinkSpan{{Line: 0, Start: 18, End: 35}}},
		{URL: "https://img.example/d.png", Kind: LinkImage, Text: "[image: diagram]", Spans: []LinkSpan{{Line: 2, Start: 0, End: 16}}},
		// A badge belongs to the link around it.
		{URL: "https://ci.example", Kind: LinkURL, Text: "[image: build]", Spans: []LinkSpan{{Line: 4, Start: 0, End: 14}}},
		{URL: "https://html.example", Kind: LinkURL, Text: "html link", Spans: []LinkSpan{{Line: 6, Start: 0, End: 9}}},
	}
	if !reflect.DeepEqual(r.Links, want) {
		t.Errorf("links\n got %+v\nwant %+v", r.Links, want)
	}
	lines := plainLines(r.Lines)
	for _, l := range r.Links {
		for _, sp := range l.Spans {
			if got := ansi.Cut(lines[sp.Line], sp.Start, sp.End); got != l.Text {
				t.Errorf("span of %s covers %q, want %q", l.URL, got, l.Text)
			}
		}
	}
}

// A link that wraps is one link with a span per line, so the cursor visits it
// once and a click on either half opens it.
func TestMarkdownLinkPositionsSurviveWrapping(t *testing.T) {
	src := "a very long line with [a link that wraps](https://w.example) and [後の日本語リンク](https://j.example)"
	r := Markdown(src, 20, MarkdownOptions{})
	lines := plainLines(r.Lines)
	if len(r.Links) != 2 {
		t.Fatalf("got %d links, want 2: %+v", len(r.Links), r.Links)
	}
	wrapped := r.Links[0]
	if len(wrapped.Spans) < 2 {
		t.Fatalf("wrapped link has spans %+v, want one per line", wrapped.Spans)
	}
	var parts []string
	for _, sp := range wrapped.Spans {
		parts = append(parts, ansi.Cut(lines[sp.Line], sp.Start, sp.End))
	}
	if got := strings.Join(parts, " "); got != "a link that wraps" {
		t.Errorf("wrapped spans cover %q", got)
	}
	cjk := r.Links[1]
	var cjkParts []string
	for _, sp := range cjk.Spans {
		cjkParts = append(cjkParts, ansi.Cut(lines[sp.Line], sp.Start, sp.End))
	}
	if got := strings.Join(cjkParts, ""); got != "後の日本語リンク" {
		t.Errorf("CJK link spans cover %q (lines %q)", got, lines)
	}

	for i, l := range r.Links {
		for _, sp := range l.Spans {
			for col := sp.Start; col < sp.End; col++ {
				if got, ok := r.LinkAt(sp.Line, col); !ok || got != i {
					t.Errorf("LinkAt(%d, %d) = %d, %v; want %d", sp.Line, col, got, ok, i)
				}
			}
		}
	}
	if _, ok := r.LinkAt(0, 0); ok {
		t.Error("LinkAt found a link on plain text")
	}
}

// Focus marks one link with › and reverse video, and changes nothing else.
func TestMarkdownFocus(t *testing.T) {
	src := "first [one](https://1.example) and [two](https://2.example)\n\nplain"
	r := Markdown(src, 40, MarkdownOptions{Theme: DefaultTheme()})
	focused := r.Focus(1)
	if got, want := strings.TrimRight(ansi.Strip(focused[0]), " "), "first one and ›two"; got != want {
		t.Errorf("focused line = %q, want %q", got, want)
	}
	if w := ansi.StringWidth(focused[0]); w != 40 {
		t.Errorf("focused line is %d wide, want 40", w)
	}
	for i := 1; i < len(r.Lines); i++ {
		if focused[i] != r.Lines[i] {
			t.Errorf("line %d changed under focus", i)
		}
	}
	if r.Lines[0] == focused[0] {
		t.Error("Lines changed along with Focus")
	}
	for _, i := range []int{-1, 2} {
		if got := r.Focus(i); !reflect.DeepEqual(got, r.Lines) {
			t.Errorf("Focus(%d) changed the lines", i)
		}
	}
}

// A link at the very end of a full line still fits once focused: text wraps a
// column short so the marker always has room.
func TestMarkdownFocusNeverOverflows(t *testing.T) {
	src := "abcdefgh [ij](https://x.example)"
	r := Markdown(src, 12, MarkdownOptions{})
	for i := range r.Links {
		for _, l := range r.Focus(i) {
			if w := ansi.StringWidth(l); w != 12 {
				t.Errorf("focused line %q is %d wide, want 12", ansi.Strip(l), w)
			}
		}
	}
}

func TestMarkdownFocusIsVisibleInColour(t *testing.T) {
	defer lipgloss.SetColorProfile(termenv.Ascii)
	lipgloss.SetColorProfile(termenv.TrueColor)

	r := Markdown("[x](https://x.example)", 20, MarkdownOptions{Theme: DefaultTheme()})
	if strings.Contains(r.Lines[0], "\x1b[7") || strings.Contains(r.Lines[0], ";7m") {
		t.Errorf("unfocused link is reversed: %q", r.Lines[0])
	}
	if f := r.Focus(0)[0]; !strings.Contains(f, "7m") && !strings.Contains(f, ";7;") {
		t.Errorf("focused link is not reversed: %q", f)
	}
}

func TestMarkdownHyperlinks(t *testing.T) {
	src := "[docs](https://d.example/a?b=1) and ![pic](https://p.example/i.png)"
	off := Markdown(src, 60, MarkdownOptions{})
	if strings.Contains(strings.Join(off.Lines, ""), "\x1b]8;") {
		t.Error("OSC 8 emitted without Hyperlinks")
	}
	on := Markdown(src, 60, MarkdownOptions{Hyperlinks: true})
	line := on.Lines[0]
	for _, url := range []string{"https://d.example/a?b=1", "https://p.example/i.png"} {
		if !strings.Contains(line, ansi.SetHyperlink(url)) {
			t.Errorf("no OSC 8 for %s in %q", url, line)
		}
	}
	if ansi.StringWidth(line) != 60 {
		t.Errorf("hyperlinks changed the width: %d", ansi.StringWidth(line))
	}
	if got := strings.TrimRight(ansi.Strip(line), " "); got != "docs and [image: pic]" {
		t.Errorf("text = %q", got)
	}
	// Focus keeps the hyperlink around the focused link.
	if f := on.Focus(0)[0]; !strings.Contains(f, ansi.SetHyperlink("https://d.example/a?b=1")) {
		t.Errorf("focus lost the hyperlink: %q", f)
	}
}

// A URL is someone else's text too: it must not be able to end the OSC 8
// sequence early and write its own.
func TestMarkdownHyperlinkURLsAreSanitised(t *testing.T) {
	r := Markdown("<a href=\"https://x.example/\x1b]8;;evil\x07\">x</a>", 30, MarkdownOptions{Hyperlinks: true})
	if len(r.Links) != 1 {
		t.Fatalf("links = %+v", r.Links)
	}
	if strings.ContainsAny(r.Links[0].URL, "\x1b\x07") {
		t.Errorf("URL kept control characters: %q", r.Links[0].URL)
	}
}

func TestMarkdownColour(t *testing.T) {
	defer lipgloss.SetColorProfile(termenv.Ascii)
	lipgloss.SetColorProfile(termenv.TrueColor)

	src := "## Title\n\nuse `x`"
	coloured := Markdown(src, 30, MarkdownOptions{Theme: DefaultTheme()})
	if got := plainLines(coloured.Lines); !reflect.DeepEqual(got, []string{"Title", "", "use x"}) {
		t.Errorf("colour output still carries markers: %q", got)
	}
	if !strings.Contains(coloured.Lines[0], "\x1b[") {
		t.Errorf("heading is unstyled in colour: %q", coloured.Lines[0])
	}

	// NoColor draws the markers even where colour is available, the way
	// --no-color asks.
	nc := Markdown(src, 30, MarkdownOptions{Theme: DefaultTheme(), NoColor: true})
	if got := plainLines(nc.Lines); !reflect.DeepEqual(got, []string{"## Title", "", "use `x`"}) {
		t.Errorf("NoColor output = %q", got)
	}
}

func TestMarkdownHighlightsFencedCode(t *testing.T) {
	defer lipgloss.SetColorProfile(termenv.Ascii)
	lipgloss.SetColorProfile(termenv.TrueColor)

	th := DefaultTheme()
	src := "```go\nfunc main() {}\n```"
	fg := func(opts MarkdownOptions) int {
		return strings.Count(Markdown(src, 40, opts).Lines[0], "38;2;")
	}
	plain := fg(MarkdownOptions{Theme: th})
	lit := fg(MarkdownOptions{Theme: th, Highlighter: NewHighlighter(th.Syntax, true)})
	if lit <= plain {
		t.Errorf("highlighted code has %d colours, unhighlighted %d", lit, plain)
	}
	if off := fg(MarkdownOptions{Theme: th, Highlighter: NewHighlighter(th.Syntax, true), NoColor: true}); off != plain {
		t.Errorf("NoColor still highlights: %d colours, want %d", off, plain)
	}
}

func TestMarkdownSuggestion(t *testing.T) {
	src := "Try this:\n\n```suggestion\nreturn nil\n```"
	var gotLines []string
	var gotWidth int
	hook := func(proposed []string, width int) ([]string, bool) {
		gotLines, gotWidth = proposed, width
		return []string{"- return err", "+ return nil"}, true
	}
	r := Markdown(src, 30, MarkdownOptions{Suggestion: hook})
	if want := []string{"Try this:", "", "- return err", "+ return nil"}; !reflect.DeepEqual(plainLines(r.Lines), want) {
		t.Errorf("hooked suggestion = %q, want %q", plainLines(r.Lines), want)
	}
	if !reflect.DeepEqual(gotLines, []string{"return nil"}) || gotWidth != 29 {
		t.Errorf("hook got %q at width %d", gotLines, gotWidth)
	}

	declined := func([]string, int) ([]string, bool) { return nil, false }
	fallback := []string{"Try this:", "", "  suggestion", "   return nil"}
	for name, opts := range map[string]MarkdownOptions{
		"declined": {Suggestion: declined},
		"no hook":  {},
	} {
		if got := mdText(src, 30, opts); !reflect.DeepEqual(got, fallback) {
			t.Errorf("%s: got %q, want %q", name, got, fallback)
		}
	}
}

// A suggestion inside a list keeps the list's indent in front of it.
func TestMarkdownSuggestionKeepsItsIndent(t *testing.T) {
	src := "- change:\n\n  ```suggestion\n  x\n  ```"
	hook := func(p []string, w int) ([]string, bool) { return []string{"+ " + p[0]}, true }
	got := mdText(src, 30, MarkdownOptions{Suggestion: hook})
	want := []string{"• change:", "", "  + x"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMarkdownEmpty(t *testing.T) {
	for _, src := range []string{"", "   \n\n", "<!-- only a comment -->"} {
		if r := Markdown(src, 40, MarkdownOptions{}); len(r.Lines) != 0 || len(r.Links) != 0 {
			t.Errorf("%q rendered %q", src, r.Lines)
		}
	}
	if r := Markdown("text", 0, MarkdownOptions{}); len(r.Lines) != 0 {
		t.Errorf("width 0 rendered %q", r.Lines)
	}
}

func TestMarkdownPlain(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"**bold** and *em* and ~~gone~~", "bold and em and gone"},
		{"see [the docs](https://x) now", "see the docs now"},
		{"![a cat](c.png) ![](d.png)", "[image: a cat] [image]"},
		{"<!-- template -->\nreal text", "real text"},
		{"# Title\n\nbody\ntext", "Title body text"},
		{"use `go vet`", "use go vet"},
		{"before\n\n```go\nx := 1\ny := 2\n```\n\nafter", "before x := 1 y := 2 after"},
		{"- one\n- [x] two", "one [x] two"},
		{"<details><summary>More</summary>\n\nhidden\n\n</details>", "More hidden"},
		{"a<br>b", "a b"},
		{"| a | b |\n|---|---|\n| 1 | 2 |", "a b 1 2"},
		{"  lots   of \n\n\n  space  ", "lots of space"},
		{"", ""},
	} {
		if got := MarkdownPlain(tc.src); got != tc.want {
			t.Errorf("MarkdownPlain(%q) = %q, want %q", tc.src, got, tc.want)
		}
	}
}

// The golden is a pull request description as templates produce them —
// comments for the author, a checklist, a fold, a table, code — so a change
// to how any of it reads shows up as a diff of plain text.
func TestMarkdownGolden(t *testing.T) {
	src := `<!-- Thanks for contributing! Describe your change below. -->
## Summary

Replaces the **retry loop** in ` + "`fetch`" + ` with exponential backoff, as
discussed in [#123](https://github.com/o/r/issues/123). See also
https://example.com/design-doc.

<!--
Checklist: tick what applies.
-->
### Checklist

- [x] Tests added
- [ ] Docs updated
- [x] Changelog entry
  - under *Unreleased*
  - with a link

## How to test

1. Run the server.
2. Kill the network:
   ` + "```sh" + `
   sudo ifconfig en0 down
   ` + "```" + `
3. Watch it back off.

> **Note**
> The old behaviour hammered the API — see the graph below.

![latency graph](https://example.com/graph.png)

<details>
<summary>Benchmark results</summary>

| case | before | after |
|:-----|-------:|------:|
| cold | 120ms | 80ms |
| warm | 40ms | 38ms |

</details>

---

Closes #123. 日本語のメモ：再試行の間隔は指数的に伸びます。<br>
Thanks!
`
	r := Markdown(src, 60, MarkdownOptions{Theme: DefaultTheme()})
	var b strings.Builder
	for _, l := range plainLines(r.Lines) {
		b.WriteString(l + "\n")
	}
	b.WriteString("\nlinks:\n")
	for i, l := range r.Links {
		var spans []string
		for _, sp := range l.Spans {
			spans = append(spans, fmt.Sprintf("%d:%d-%d", sp.Line, sp.Start, sp.End))
		}
		fmt.Fprintf(&b, "%d %s %q %s %s\n", i, l.Kind, l.Text, l.URL, strings.Join(spans, " "))
	}
	checkGolden(t, "markdown-pr.golden", b.String())
}
