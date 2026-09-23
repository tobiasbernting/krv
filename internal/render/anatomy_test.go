package render

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/tobiasbernting/krv/v2/internal/diffparse"
)

const sampleDiff = "diff --git a/f.go b/f.go\n--- a/f.go\n+++ b/f.go\n" +
	"@@ -1,3 +1,3 @@ func Greet() {\n ctx := 1\n-old := 2\n+new := 2\n"

func sampleDoc(t *testing.T, layout Layout) (*Document, *Renderer) {
	t.Helper()
	doc := Build(diffparse.Parse(sampleDiff), NewHighlighter("", false), Overlay{}, layout)
	return doc, NewRenderer(DefaultTheme(), doc)
}

func rowOfKind(doc *Document, kind diffparse.LineKind) Row {
	for _, row := range doc.Rows {
		if row.Kind == RowCode && row.Line.Kind == kind {
			return row
		}
	}
	return Row{}
}

// Tests run without a TTY, so lipgloss emits plain text: what survives is
// exactly the part of the design that does not depend on colour.
func TestAddAndDeleteReadWithoutColour(t *testing.T) {
	doc, r := sampleDoc(t, Layout{})
	add := r.Render(rowOfKind(doc, diffparse.KindAdd), 60, 0, false)
	del := r.Render(rowOfKind(doc, diffparse.KindDel), 60, 0, false)
	ctx := r.Render(rowOfKind(doc, diffparse.KindContext), 60, 0, false)

	for _, tc := range []struct{ name, row, sign string }{
		{"added", add, signAdd},
		{"deleted", del, signDel},
	} {
		if !strings.HasPrefix(tc.row, edgeChange) {
			t.Errorf("%s row does not start with the change marker: %q", tc.name, tc.row)
		}
		if !strings.Contains(tc.row, tc.sign+" ") {
			t.Errorf("%s row is missing its %q sign: %q", tc.name, tc.sign, tc.row)
		}
	}
	if strings.HasPrefix(ctx, edgeChange) {
		t.Errorf("context row is marked as changed: %q", ctx)
	}
	if strings.Contains(ctx, signAdd) || strings.Contains(ctx, signDel) {
		t.Errorf("context row carries a diff sign: %q", ctx)
	}
}

// The whole grid depends on every row claiming the same number of columns
// before the code starts.
func TestGutterWidthMatchesWhatIsDrawn(t *testing.T) {
	doc, r := sampleDoc(t, Layout{})
	for _, kind := range []diffparse.LineKind{diffparse.KindAdd, diffparse.KindDel, diffparse.KindContext} {
		row := rowOfKind(doc, kind)
		out := r.Render(row, 60, 0, false)
		if got := lipgloss.Width(out); got != 60 {
			t.Errorf("row width = %d, want 60", got)
		}
		runes := []rune(out)
		prefix, code := string(runes[:doc.GutterWidth()]), string(runes[doc.GutterWidth():])
		if !strings.HasPrefix(code, row.Line.Text) {
			t.Errorf("code starts at the wrong column: gutter %q then %q, want %q",
				prefix, code, row.Line.Text)
		}
		if !strings.Contains(prefix, gutterRule) {
			t.Errorf("gutter %q has no rule between the old and new columns", prefix)
		}
	}
	// A header row sits on the same grid, so the two number columns are named
	// where the reader first meets them.
	for _, row := range doc.Rows {
		if row.Kind != RowHunk {
			continue
		}
		out := r.Render(row, 60, 0, false)
		if !strings.Contains(out, "old "+gutterRule+" new") {
			t.Errorf("hunk header does not label the gutter: %q", out)
		}
		if !strings.Contains(out, "-1,3 +1,3") {
			t.Errorf("hunk header lost its range: %q", out)
		}
	}
}

// Focus has to be obvious without hiding what kind of line is focused: the
// sign, the tint and the syntax all have to survive it.
func TestFocusKeepsDiffState(t *testing.T) {
	doc, r := sampleDoc(t, Layout{})
	th := DefaultTheme()

	add := rowOfKind(doc, diffparse.KindAdd)
	focused := r.Render(add, 60, 0, true)
	if !strings.HasPrefix(focused, edgeFocus) {
		t.Errorf("focused row has no focus bar: %q", focused)
	}
	if !strings.Contains(focused, signAdd) {
		t.Errorf("focused row lost its + sign: %q", focused)
	}
	if !strings.Contains(focused, "new := 2") {
		t.Errorf("focused row lost its code: %q", focused)
	}

	tones := r.tones(diffparse.KindAdd, true)
	switch {
	case tones.bg == th.AddBg:
		t.Error("focus did not lift the added row's tone")
	case tones.bg == th.CursorBg:
		t.Error("focus replaced the add tint with the plain cursor tint")
	case tones.bg != th.AddBgFocus:
		t.Errorf("focused add background = %q, want AddBgFocus", tones.bg)
	}
	if tones.sign != signAdd || tones.signFg != th.AddSign {
		t.Error("focus changed the sign an added row shows")
	}
	if !tones.numBold || tones.numFg != th.LineNumFocusFg {
		t.Error("focus did not emphasise the line number")
	}
	if del := r.tones(diffparse.KindDel, true); del.bg == tones.bg {
		t.Error("a focused deletion is indistinguishable from a focused addition")
	}
}

// Syntax is muted so that diff state wins the page, but names — the tokens you
// actually scan for — keep most of their colour.
func TestSyntaxMutingKeepsNamesVivid(t *testing.T) {
	_, r := sampleDoc(t, Layout{})
	th := r.Theme
	const green = "#00ff00"

	plain := r.syntaxFg(green, th.Bg, false, false)
	name := r.syntaxFg(green, th.Bg, true, false)
	if plain == green {
		t.Error("ordinary syntax was not muted at all")
	}
	if contrast(name, th.Bg) <= contrast(plain, th.Bg) {
		t.Error("a name token is no more visible than ordinary syntax")
	}
	if got := r.syntaxFg("", th.Bg, false, false); got != th.Fg {
		t.Errorf("uncoloured code = %q, want the theme's own foreground %q", got, th.Fg)
	}

	// A theme that asks for no muting gets none.
	r.Theme.SyntaxMute, r.Theme.SyntaxMuteEmph = 0, 0
	r.muted = map[mutedKey]string{}
	if got := r.syntaxFg(green, th.Bg, false, false); got != green {
		t.Errorf("SyntaxMute 0 still changed %q to %q", green, got)
	}
}

// A line wider than the terminal has to say so, or it reads as a short line.
func TestLongLinesAreMarkedAsTruncated(t *testing.T) {
	long := "diff --git a/f.go b/f.go\n--- a/f.go\n+++ b/f.go\n@@ -1 +1 @@\n-x\n+" +
		strings.Repeat("abcdefghij", 12) + "\n"
	doc := Build(diffparse.Parse(long), NewHighlighter("", false), Overlay{}, Layout{})
	r := NewRenderer(DefaultTheme(), doc)
	row := rowOfKind(doc, diffparse.KindAdd)

	if out := r.Render(row, 40, 0, false); !strings.HasSuffix(out, overflowMark) {
		t.Errorf("truncated row is not marked: %q", out)
	}
	// Scrolled to the end, there is nothing left to promise.
	if out := r.Render(row, 40, 200, false); strings.Contains(out, overflowMark) {
		t.Errorf("row marked as overflowing when it is not: %q", out)
	}
}

// Comfortable density buys separation where hierarchy changes, and nowhere
// else: it must not simply double the height of a diff.
func TestComfortableDensitySeparatesHunksNotLines(t *testing.T) {
	two := "diff --git a/f.go b/f.go\n--- a/f.go\n+++ b/f.go\n" +
		"@@ -1,2 +1,2 @@ one\n-a\n+b\n@@ -9,2 +9,2 @@ two\n-c\n+d\n"
	files := diffparse.Parse(two)

	roomy := Build(files, NewHighlighter("", false), Overlay{}, Layout{Density: DensityComfortable})
	tight := Build(files, NewHighlighter("", false), Overlay{}, Layout{Density: DensityCompact})

	if len(roomy.Rows) != len(tight.Rows)+1 {
		t.Errorf("comfortable added %d rows for one extra hunk, want 1",
			len(roomy.Rows)-len(tight.Rows))
	}
	if roomy.Rows[roomy.HunkRows[1]-1].Kind != RowSpacer {
		t.Error("no separation before the second hunk")
	}
	if roomy.Rows[roomy.HunkRows[0]-1].Kind == RowSpacer {
		t.Error("the first hunk was separated from the file header it belongs to")
	}
	if code := countKind(tight, RowCode); code != countKind(roomy, RowCode) {
		t.Errorf("density changed how many code rows there are: %d vs %d", code, countKind(roomy, RowCode))
	}
}

func TestParseDensity(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Density
		ok   bool
	}{
		{"", DensityComfortable, true},
		{"comfortable", DensityComfortable, true},
		{" Compact ", DensityCompact, true},
		{"roomy", DensityComfortable, false},
	} {
		got, ok := ParseDensity(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("ParseDensity(%q) = %v, %v; want %v, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// An annotation is only worth drawing if it can be read: the body takes the
// theme's body colour, not the quiet marker colour of its label.
func TestAnnotationBodyIsReadable(t *testing.T) {
	doc, r := sampleDoc(t, Layout{})
	row := Row{Kind: RowNote, Ann: &Annotation{
		Kind: AnnComment, Author: "robin", Body: "needs a test", ResolutionKnown: true,
	}}
	out := r.Render(row, 60, 0, false)

	if !strings.HasPrefix(out, edgeNote) {
		t.Errorf("annotation has no left marker: %q", out)
	}
	if !strings.Contains(out, "robin: needs a test") {
		t.Errorf("annotation lost its label or body: %q", out)
	}
	if lipgloss.Width(out) != 60 {
		t.Errorf("annotation row width = %d, want 60", lipgloss.Width(out))
	}
	if body := strings.Index(out, "robin"); body < doc.GutterWidth()-1 {
		t.Errorf("annotation text starts at column %d, before the code column", body)
	}
	if r.Theme.NoteBodyFg == r.Theme.NoteBg {
		t.Error("annotation body is the same colour as its panel")
	}
	// A comment carrying state says so, rather than looking like a plain note.
	stateful := Row{Kind: RowNote, Ann: &Annotation{
		Kind: AnnComment, Author: "robin", Body: "needs a test",
		Outdated: true, Resolved: true, ResolutionKnown: true,
	}}
	if out := r.Render(stateful, 80, 0, false); !strings.Contains(out, "[outdated]") ||
		!strings.Contains(out, "[resolved]") {
		t.Errorf("comment state is not shown: %q", out)
	}
}

func countKind(d *Document, k RowKind) int {
	n := 0
	for _, row := range d.Rows {
		if row.Kind == k {
			n++
		}
	}
	return n
}

// A note longer than the terminal is wrapped where it is read: the label keeps
// the first line, and the rest hangs under it so the block reads as one
// paragraph rather than as several notes.
func TestAnnotationWrapsUnderItsLabel(t *testing.T) {
	doc, r := sampleDoc(t, Layout{})
	body := "GitHub rejects the whole review with a bare 422 when both ends of a " +
		"multi-line comment do not sit in the same hunk, so this wants a guard."
	row := Row{Kind: RowNote, Ann: &Annotation{
		Kind: AnnComment, Author: "robin", Body: body, ResolutionKnown: true,
	}}

	lines := r.RenderLines(row, 60, 0, false, 0)
	if len(lines) < 2 {
		t.Fatalf("a %d-column body rendered %d lines at width 60", len(body), len(lines))
	}
	for i, line := range lines {
		if got := lipgloss.Width(line); got != 60 {
			t.Errorf("line %d width = %d, want 60", i, got)
		}
		if !strings.HasPrefix(line, edgeNote) {
			t.Errorf("line %d has no left marker: %q", i, line)
		}
	}
	if strings.Contains(strings.Join(lines, ""), "…") {
		t.Error("the body was truncated even though nothing limited the lines")
	}
	if indent(lines[1]) <= indent(lines[0]) {
		t.Errorf("continuation does not hang under the label: %q then %q", lines[0], lines[1])
	}
	if indent(lines[0]) < doc.GutterWidth()-1 {
		t.Errorf("annotation text starts before the code column: %q", lines[0])
	}

	// Away from the cursor the note is one line, and says it was cut.
	compact := r.RenderLines(row, 60, 0, false, 1)
	if len(compact) != 1 {
		t.Fatalf("compact form rendered %d lines, want 1", len(compact))
	}
	if !strings.Contains(compact[0], "…") {
		t.Errorf("compact form does not show that it was cut: %q", compact[0])
	}
}

func TestSplitLabel(t *testing.T) {
	for _, tc := range []struct{ in, label, body string }{
		{"robin: needs a test", "robin: ", "needs a test"},
		{"you [needs re-anchor]: moved", "you [needs re-anchor]: ", "moved"},
		{"− thread · 2 comments [unresolved]", "", "− thread · 2 comments [unresolved]"},
	} {
		label, body := splitLabel(tc.in)
		if label != tc.label || body != tc.body {
			t.Errorf("splitLabel(%q) = %q, %q; want %q, %q", tc.in, label, body, tc.label, tc.body)
		}
	}
}

// indent is the column an annotation's text starts at, past its edge marker.
func indent(line string) int {
	body := string([]rune(line)[1:])
	return 1 + strings.IndexFunc(body, func(r rune) bool { return r != ' ' })
}

// The cursor can rest on a header — n and tab land on them — so a header has
// to show focus too, in the same column as everything else. A cursor that
// vanishes when it leaves the code is a cursor you have to hunt for.
func TestEveryRowTheCursorCanRestOnShowsFocus(t *testing.T) {
	ov := Overlay{
		At: func(path string, line int) []Annotation {
			if line == 2 {
				return []Annotation{{Author: "robin", Body: "here", ResolutionKnown: true, Line: 2}}
			}
			return nil
		},
		Detached: func(path string) []AnnotationGroup {
			return []AnnotationGroup{{
				Title: "no longer anchored",
				Items: []Annotation{{Author: "sam", Body: "moved", ResolutionKnown: true}},
			}}
		},
	}
	// A renamed file, so meta rows are in the document too.
	renamed := sampleDiff + "diff --git a/old.go b/new.go\nrename from old.go\nrename to new.go\n" +
		"--- a/old.go\n+++ b/new.go\n@@ -1 +1 @@\n-a\n+b\n"
	doc := Build(diffparse.Parse(renamed), NewHighlighter("", false), ov, Layout{})
	r := NewRenderer(DefaultTheme(), doc)

	seen := map[RowKind]bool{}
	for _, row := range doc.Rows {
		if row.Kind == RowSpacer {
			continue
		}
		seen[row.Kind] = true
		focused := r.Render(row, 60, 0, true)
		if focused == r.Render(row, 60, 0, false) {
			t.Errorf("row kind %d looks the same focused as unfocused: %q", row.Kind, focused)
			continue
		}
		if !strings.HasPrefix(focused, edgeFocus) && !strings.HasPrefix(focused, edgeNote) {
			t.Errorf("row kind %d does not mark focus in the edge column: %q", row.Kind, focused)
		}
	}
	for _, kind := range []RowKind{RowFile, RowMeta, RowHunk, RowCode, RowNote, RowSection} {
		if !seen[kind] {
			t.Errorf("row kind %d never appeared, so focus on it is untested", kind)
		}
	}
}
