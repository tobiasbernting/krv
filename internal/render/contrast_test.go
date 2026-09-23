package render

import (
	"testing"

	"github.com/alecthomas/chroma/v2/styles"
)

// codeFloor is the least luminance difference any code token may have against
// the cell it is painted on. Muting is meant to make syntax recede, not to
// make it vanish. markFloor is stricter: an intra-line change is the text a
// reviewer is being pointed at.
const (
	codeFloor = 0.2
	markFloor = 0.3
)

type surface struct {
	bg    string
	mark  bool
	floor float64
}

// backgrounds is every surface a theme paints code on.
func backgrounds(th Theme) map[string]surface {
	return map[string]surface{
		"Bg": {th.Bg, false, codeFloor}, "CursorBg": {th.CursorBg, false, codeFloor},
		"AddBg": {th.AddBg, false, codeFloor}, "AddBgFocus": {th.AddBgFocus, false, codeFloor},
		"DelBg": {th.DelBg, false, codeFloor}, "DelBgFocus": {th.DelBgFocus, false, codeFloor},
		"AddWordBg": {th.AddWordBg, true, markFloor}, "DelWordBg": {th.DelWordBg, true, markFloor},
	}
}

// Every colour a preset's syntax style can produce has to stay readable on
// every background the preset paints code on — the intra-line highlight
// above all, where a muted green string on a green tint used to all but
// disappear.
func TestSyntaxStaysReadableOnEveryBackground(t *testing.T) {
	for _, name := range ThemeNames() {
		th, _ := ThemeByName(name)
		r := NewRenderer(th, nil)
		st := styles.Get(th.Syntax)
		colours := map[string]bool{"": true}
		for _, tt := range st.Types() {
			if c := st.Get(tt).Colour; c.IsSet() {
				colours[c.String()] = true
			}
		}
		for role, sf := range backgrounds(th) {
			for fg := range colours {
				for _, emph := range []bool{false, true} {
					got := r.syntaxFg(fg, sf.bg, emph, sf.mark)
					if c := contrast(got, sf.bg); c < sf.floor {
						t.Errorf("theme %s: %s (emph %v) painted %s on %s %s: contrast %.2f",
							name, fg, emph, got, role, sf.bg, c)
					}
				}
			}
		}
	}
}

// A worked example from the dark theme: catppuccin-mocha's comment grey on the
// intra-line highlight. Muted toward the page surface it came out at #484c5b,
// near enough the highlight's own tone to read as a dark smudge.
func TestDarkCommentOnWordHighlightIsLifted(t *testing.T) {
	th, _ := ThemeByName("dark")
	r := NewRenderer(th, nil)
	got := r.syntaxFg("#6c7086", th.AddWordBg, false, true)
	if luminance(got) <= luminance("#6c7086") {
		t.Errorf("comment on the word highlight painted %s, want lighter than its own #6c7086", got)
	}
}

// Muting still has to mute: on the page surface, where the source colour
// already has room, a token keeps receding toward it.
func TestSyntaxStillMutesOnTheSurface(t *testing.T) {
	th, _ := ThemeByName("dark")
	r := NewRenderer(th, nil)
	if got := r.syntaxFg("#cba6f7", th.Bg, false, false); got == "#cba6f7" {
		t.Error("a keyword on the surface was not muted at all")
	}
}
