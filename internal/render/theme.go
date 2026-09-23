package render

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/alecthomas/chroma/v2/styles"
)

// Theme holds every colour the diff renderer uses.
//
// Fields are grouped into semantic roles — surface, gutter, diff state, focus,
// annotation — rather than one flat palette, so a new preset only has to
// answer "what does a deleted line look like here?" instead of remembering
// which of a dozen greens meant what. The older field names (AddBg, Gutter,
// CursorBg, …) are kept as aliases because the TUI, the queue and the submit
// view already read them; presets fill both halves.
type Theme struct {
	// Name identifies the preset. Empty for a hand-built theme.
	Name string

	// Syntax is the chroma style name used for code highlighting.
	Syntax string

	// SyntaxMute blends syntax colours toward the surface, 0 (untouched) to 1
	// (invisible). Muting is what lets diff state win the page while code
	// keeps its shape. SyntaxMuteEmph is the weaker blend used for the tokens
	// worth keeping vivid: function, method, class and type names.
	SyntaxMute     float64
	SyntaxMuteEmph float64

	// Surface.
	Bg     string // the page behind unchanged code
	Fg     string // code with no syntax colour of its own
	Dim    string // secondary text: meta rows, ranges, hints
	Accent string // focus, hunk headers, keys

	// Gutter.
	GutterBg       string
	GutterSep      string // the rule between the old and new columns
	LineNumFg      string
	LineNumFocusFg string // the focused row's line number

	// Diff state. Edge is the left marker, Sign the +/− column, WordBg the
	// intra-line change shading, and BgFocus the tint a focused row keeps so
	// that focus never erases what kind of line it is.
	AddBg      string
	AddBgFocus string
	AddEdge    string
	AddSign    string
	AddWordBg  string

	DelBg      string
	DelBgFocus string
	DelEdge    string
	DelSign    string
	DelWordBg  string

	// FillBg is the empty half of a split row, where a change has no
	// counterpart on the other side. Neutral and quiet, so it reads as
	// absence rather than as a third kind of line.
	FillBg string

	// MarkUnderline underlines intra-line changes as well as shading them,
	// for themes that cannot rely on the shading being perceived.
	MarkUnderline bool

	// Focus.
	CursorBg  string // focused context row
	CursorBar string // the focus bar in the edge column

	// Headers.
	FileBg string
	FileFg string
	HunkBg string
	HunkFg string
	MetaFg string

	// Annotations.
	NoteBg     string
	NoteFg     string // your own unsent notes
	NoteBodyFg string // annotation body text, which has to stay readable
	CommentFg  string // existing review comments from GitHub
	StaleFg    string // notes and comments that no longer anchor

	// File state.
	ReviewedFg string
	ChangedFg  string

	// Gutter is the compatibility alias for LineNumFg.
	Gutter string
	// AddFg and DelFg are the compatibility aliases for AddSign and DelSign.
	AddFg string
	DelFg string
}

// DefaultTheme is the dark preset, which is what krv shows when nothing has
// been configured.
func DefaultTheme() Theme { return darkTheme() }

// ThemeGroup is one heading in the theme picker and the presets under it.
type ThemeGroup struct {
	Label string
	Names []string
}

// Preset groups, in the order the picker shows them: by the surface a preset
// sets, then the presets that do not lean on red and green at all.
const (
	groupDark        = "dark"
	groupLight       = "light"
	groupColourBlind = "colour-blind"
)

// ThemeGroups lists every preset once, under its group, alphabetical within
// each.
func ThemeGroups() []ThemeGroup {
	var out []ThemeGroup
	for _, label := range []string{groupDark, groupLight, groupColourBlind} {
		g := ThemeGroup{Label: label}
		for _, name := range ThemeNames() {
			if presets[name].group == label {
				g.Names = append(g.Names, name)
			}
		}
		out = append(out, g)
	}
	return out
}

// ThemeNames lists the built-in presets in a stable order.
func ThemeNames() []string {
	names := make([]string, 0, len(presets))
	for name := range presets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ThemeByName returns a built-in preset. The second result is false for a
// name krv does not know, which is how the config layer tells a theme name
// from a chroma style name.
func ThemeByName(name string) (Theme, bool) {
	build, ok := presets[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return Theme{}, false
	}
	return build.build(), true
}

type preset struct {
	group string
	build func() Theme
}

// presets named after a chroma style highlight with that style, so a theme
// setting written when it could only name a style now gets the whole theme.
var presets = map[string]preset{
	"dark":            {groupDark, darkTheme},
	"dracula":         {groupDark, draculaTheme},
	"gruvbox-dark":    {groupDark, gruvboxDarkTheme},
	"high-contrast":   {groupDark, highContrastTheme},
	"nord":            {groupDark, nordTheme},
	"tokyonight":      {groupDark, tokyonightTheme},
	"light":           {groupLight, lightTheme},
	"solarized-light": {groupLight, solarizedLightTheme},
	"dark-cb":         {groupColourBlind, darkCBTheme},
	"light-cb":        {groupColourBlind, lightCBTheme},
}

// resolve fills the compatibility aliases from the semantic roles, so a preset
// states each colour once and nothing downstream reads an empty field.
func (t Theme) resolve() Theme {
	t.Gutter = t.LineNumFg
	t.AddFg = t.AddSign
	t.DelFg = t.DelSign
	return t
}

func darkTheme() Theme {
	return Theme{
		Name:           "dark",
		Syntax:         "catppuccin-mocha",
		SyntaxMute:     0.45,
		SyntaxMuteEmph: 0.15,

		Bg:     "#1c1f26",
		Fg:     "#c8ccd4",
		Dim:    "#7b8394",
		Accent: "#7aa2f7",

		GutterBg:       "#181b21",
		GutterSep:      "#3a4050",
		LineNumFg:      "#5c6370",
		LineNumFocusFg: "#e6e9ef",

		AddBg:      "#16261d",
		AddBgFocus: "#1e3728",
		AddEdge:    "#4f9d69",
		AddSign:    "#7fd88f",
		AddWordBg:  "#25603e",

		DelBg:      "#291a1e",
		DelBgFocus: "#3b242a",
		DelEdge:    "#b3596a",
		DelSign:    "#f07178",
		DelWordBg:  "#6e2733",

		FillBg: "#16181e",

		CursorBg:  "#262b36",
		CursorBar: "#7aa2f7",

		FileBg: "#262b36",
		FileFg: "#e6e9ef",
		HunkBg: "#20242c",
		HunkFg: "#7aa2f7",
		MetaFg: "#7b8394",

		NoteBg:     "#22262f",
		NoteFg:     "#e5c07b",
		NoteBodyFg: "#c8ccd4",
		CommentFg:  "#56b6c2",
		StaleFg:    "#6b7280",

		ReviewedFg: "#7fd88f",
		ChangedFg:  "#e5c07b",
	}.resolve()
}

func lightTheme() Theme {
	return Theme{
		Name:           "light",
		Syntax:         "catppuccin-latte",
		SyntaxMute:     0.40,
		SyntaxMuteEmph: 0.10,

		Bg:     "#fbfbfa",
		Fg:     "#2b2f36",
		Dim:    "#6b7280",
		Accent: "#2f6fd0",

		GutterBg:       "#f1f1ef",
		GutterSep:      "#c9ccd2",
		LineNumFg:      "#9aa0aa",
		LineNumFocusFg: "#1f2329",

		AddBg:      "#e8f5ec",
		AddBgFocus: "#d3ebdc",
		AddEdge:    "#2f8a52",
		AddSign:    "#1f7a43",
		AddWordBg:  "#b3e0c3",

		DelBg:      "#fdecec",
		DelBgFocus: "#f7d9d9",
		DelEdge:    "#c04a4a",
		DelSign:    "#b02b2b",
		DelWordBg:  "#f4bebe",

		FillBg: "#eeeeeb",

		CursorBg:  "#e9ebf0",
		CursorBar: "#2f6fd0",

		FileBg: "#e5e7ec",
		FileFg: "#1f2329",
		HunkBg: "#f2f3f6",
		HunkFg: "#2f6fd0",
		MetaFg: "#6b7280",

		NoteBg:     "#f5f2e8",
		NoteFg:     "#8a6d1f",
		NoteBodyFg: "#3a3f46",
		CommentFg:  "#136f7a",
		StaleFg:    "#8b9099",

		ReviewedFg: "#1f7a43",
		ChangedFg:  "#8a6d1f",
	}.resolve()
}

func highContrastTheme() Theme {
	return Theme{
		Name:           "high-contrast",
		Syntax:         "github-dark",
		SyntaxMute:     0.12,
		SyntaxMuteEmph: 0,
		MarkUnderline:  true,

		Bg:     "#000000",
		Fg:     "#ffffff",
		Dim:    "#c0c0c0",
		Accent: "#7fd4ff",

		GutterBg:       "#0b0b0b",
		GutterSep:      "#767676",
		LineNumFg:      "#a8a8a8",
		LineNumFocusFg: "#ffffff",

		AddBg:      "#042b13",
		AddBgFocus: "#0a4620",
		AddEdge:    "#00d76a",
		AddSign:    "#3dff92",
		AddWordBg:  "#0a6b34",

		DelBg:      "#2b040b",
		DelBgFocus: "#460a15",
		DelEdge:    "#ff4d6d",
		DelSign:    "#ff8b99",
		DelWordBg:  "#7a0f22",

		FillBg: "#121212",

		CursorBg:  "#1c1c1c",
		CursorBar: "#ffd400",

		FileBg: "#1c1c1c",
		FileFg: "#ffffff",
		HunkBg: "#101010",
		HunkFg: "#7fd4ff",
		MetaFg: "#c0c0c0",

		NoteBg:     "#141414",
		NoteFg:     "#ffd400",
		NoteBodyFg: "#f2f2f2",
		CommentFg:  "#7fd4ff",
		StaleFg:    "#a8a8a8",

		ReviewedFg: "#3dff92",
		ChangedFg:  "#ffd400",
	}.resolve()
}

// darkCBTheme is dark with changes in blue and orange instead of green and
// red, the pair that survives red-green colour blindness. Intra-line changes
// are underlined too, so they never rest on hue alone.
func darkCBTheme() Theme {
	t := darkTheme()
	t.Name = "dark-cb"
	t.MarkUnderline = true
	t.Accent, t.CursorBar = "#c3a6ff", "#c3a6ff"

	t.AddBg, t.AddBgFocus = "#172234", "#1f2e45"
	t.AddEdge, t.AddSign, t.AddWordBg = "#4d8fe0", "#79b8ff", "#24497a"
	t.DelBg, t.DelBgFocus = "#2c2118", "#3d2c1e"
	t.DelEdge, t.DelSign, t.DelWordBg = "#d97a2b", "#ffa657", "#6e4119"

	t.ReviewedFg = "#79b8ff"
	return t.resolve()
}

// lightCBTheme is light with changes in blue and orange.
func lightCBTheme() Theme {
	t := lightTheme()
	t.Name = "light-cb"
	t.MarkUnderline = true
	t.Accent, t.CursorBar, t.HunkFg = "#8250df", "#8250df", "#8250df"

	t.AddBg, t.AddBgFocus = "#e6effb", "#d3e3f7"
	t.AddEdge, t.AddSign, t.AddWordBg = "#2f6fd0", "#1f5fbf", "#b5d0f2"
	t.DelBg, t.DelBgFocus = "#fdf0e3", "#f9e1c9"
	t.DelEdge, t.DelSign, t.DelWordBg = "#c8661a", "#a8520f", "#f5c99a"

	t.ReviewedFg = "#1f5fbf"
	return t.resolve()
}

func draculaTheme() Theme {
	return Theme{
		Name:           "dracula",
		Syntax:         "dracula",
		SyntaxMute:     0.40,
		SyntaxMuteEmph: 0.12,

		Bg:     "#282a36",
		Fg:     "#f8f8f2",
		Dim:    "#8b93b8",
		Accent: "#bd93f9",

		GutterBg:       "#21222c",
		GutterSep:      "#44475a",
		LineNumFg:      "#6272a4",
		LineNumFocusFg: "#f8f8f2",

		AddBg:      "#23392d",
		AddBgFocus: "#2c4a39",
		AddEdge:    "#50fa7b",
		AddSign:    "#50fa7b",
		AddWordBg:  "#2f6b45",

		DelBg:      "#3d2630",
		DelBgFocus: "#4f2e3a",
		DelEdge:    "#ff5555",
		DelSign:    "#ff6e6e",
		DelWordBg:  "#7a3040",

		FillBg: "#21222c",

		CursorBg:  "#343746",
		CursorBar: "#bd93f9",

		FileBg: "#343746",
		FileFg: "#f8f8f2",
		HunkBg: "#2d2f3d",
		HunkFg: "#bd93f9",
		MetaFg: "#8b93b8",

		NoteBg:     "#303241",
		NoteFg:     "#f1fa8c",
		NoteBodyFg: "#f8f8f2",
		CommentFg:  "#8be9fd",
		StaleFg:    "#6272a4",

		ReviewedFg: "#50fa7b",
		ChangedFg:  "#f1fa8c",
	}.resolve()
}

func gruvboxDarkTheme() Theme {
	return Theme{
		Name:           "gruvbox-dark",
		Syntax:         "gruvbox",
		SyntaxMute:     0.40,
		SyntaxMuteEmph: 0.12,

		Bg:     "#282828",
		Fg:     "#ebdbb2",
		Dim:    "#928374",
		Accent: "#83a598",

		GutterBg:       "#232323",
		GutterSep:      "#504945",
		LineNumFg:      "#7c6f64",
		LineNumFocusFg: "#fbf1c7",

		AddBg:      "#32361f",
		AddBgFocus: "#3d4224",
		AddEdge:    "#98971a",
		AddSign:    "#b8bb26",
		AddWordBg:  "#5a6124",

		DelBg:      "#3c2522",
		DelBgFocus: "#4a2b27",
		DelEdge:    "#cc241d",
		DelSign:    "#fb4934",
		DelWordBg:  "#7a3029",

		FillBg: "#1d2021",

		CursorBg:  "#3c3836",
		CursorBar: "#fabd2f",

		FileBg: "#3c3836",
		FileFg: "#fbf1c7",
		HunkBg: "#32302f",
		HunkFg: "#83a598",
		MetaFg: "#928374",

		NoteBg:     "#32302f",
		NoteFg:     "#fabd2f",
		NoteBodyFg: "#ebdbb2",
		CommentFg:  "#8ec07c",
		StaleFg:    "#7c6f64",

		ReviewedFg: "#b8bb26",
		ChangedFg:  "#fabd2f",
	}.resolve()
}

func nordTheme() Theme {
	return Theme{
		Name:           "nord",
		Syntax:         "nord",
		SyntaxMute:     0.35,
		SyntaxMuteEmph: 0.10,

		Bg:     "#2e3440",
		Fg:     "#d8dee9",
		Dim:    "#7b88a1",
		Accent: "#88c0d0",

		GutterBg:       "#2a2f3a",
		GutterSep:      "#4c566a",
		LineNumFg:      "#616e88",
		LineNumFocusFg: "#eceff4",

		AddBg:      "#33403b",
		AddBgFocus: "#3b4a42",
		AddEdge:    "#a3be8c",
		AddSign:    "#a3be8c",
		AddWordBg:  "#4d6450",

		DelBg:      "#413539",
		DelBgFocus: "#4c3b40",
		DelEdge:    "#bf616a",
		DelSign:    "#d57780",
		DelWordBg:  "#6e4550",

		FillBg: "#292e39",

		CursorBg:  "#3b4252",
		CursorBar: "#88c0d0",

		FileBg: "#3b4252",
		FileFg: "#eceff4",
		HunkBg: "#353b49",
		HunkFg: "#88c0d0",
		MetaFg: "#7b88a1",

		NoteBg:     "#353b49",
		NoteFg:     "#ebcb8b",
		NoteBodyFg: "#d8dee9",
		CommentFg:  "#8fbcbb",
		StaleFg:    "#616e88",

		ReviewedFg: "#a3be8c",
		ChangedFg:  "#ebcb8b",
	}.resolve()
}

func tokyonightTheme() Theme {
	return Theme{
		Name:           "tokyonight",
		Syntax:         "tokyonight-night",
		SyntaxMute:     0.40,
		SyntaxMuteEmph: 0.12,

		Bg:     "#1a1b26",
		Fg:     "#c0caf5",
		Dim:    "#737aa2",
		Accent: "#7aa2f7",

		GutterBg:       "#16161e",
		GutterSep:      "#3b4261",
		LineNumFg:      "#545c7e",
		LineNumFocusFg: "#c0caf5",

		AddBg:      "#1e2b26",
		AddBgFocus: "#263a30",
		AddEdge:    "#9ece6a",
		AddSign:    "#9ece6a",
		AddWordBg:  "#2f5a3a",

		DelBg:      "#2d1f2a",
		DelBgFocus: "#3b2533",
		DelEdge:    "#db4b4b",
		DelSign:    "#f7768e",
		DelWordBg:  "#6b2e40",

		FillBg: "#16161e",

		CursorBg:  "#292e42",
		CursorBar: "#7aa2f7",

		FileBg: "#292e42",
		FileFg: "#c0caf5",
		HunkBg: "#1f2335",
		HunkFg: "#7aa2f7",
		MetaFg: "#737aa2",

		NoteBg:     "#1f2335",
		NoteFg:     "#e0af68",
		NoteBodyFg: "#c0caf5",
		CommentFg:  "#7dcfff",
		StaleFg:    "#545c7e",

		ReviewedFg: "#9ece6a",
		ChangedFg:  "#e0af68",
	}.resolve()
}

func solarizedLightTheme() Theme {
	return Theme{
		Name:           "solarized-light",
		Syntax:         "solarized-light",
		SyntaxMute:     0.30,
		SyntaxMuteEmph: 0.08,

		Bg:     "#fdf6e3",
		Fg:     "#586e75",
		Dim:    "#839496",
		Accent: "#268bd2",

		GutterBg:       "#eee8d5",
		GutterSep:      "#d3cbb7",
		LineNumFg:      "#93a1a1",
		LineNumFocusFg: "#073642",

		AddBg:      "#f0f1d2",
		AddBgFocus: "#e4e8bf",
		AddEdge:    "#859900",
		AddSign:    "#6c7c00",
		AddWordBg:  "#d4dc9a",

		DelBg:      "#fbe6d8",
		DelBgFocus: "#f6d6c9",
		DelEdge:    "#dc322f",
		DelSign:    "#c0281f",
		DelWordBg:  "#f2b9a8",

		FillBg: "#eee8d5",

		CursorBg:  "#eee8d5",
		CursorBar: "#268bd2",

		FileBg: "#eee8d5",
		FileFg: "#073642",
		HunkBg: "#f5efdc",
		HunkFg: "#268bd2",
		MetaFg: "#839496",

		NoteBg:     "#f5efdc",
		NoteFg:     "#b58900",
		NoteBodyFg: "#586e75",
		CommentFg:  "#2aa198",
		StaleFg:    "#93a1a1",

		ReviewedFg: "#859900",
		ChangedFg:  "#b58900",
	}.resolve()
}

// mix blends fg toward bg by amount, 0 returning fg unchanged and 1 returning
// bg. Anything it cannot parse — a named colour, an ANSI index — is returned
// untouched rather than guessed at.
func mix(fg, bg string, amount float64) string {
	if amount <= 0 {
		return fg
	}
	if amount > 1 {
		amount = 1
	}
	fr, fg2, fb, ok := parseHex(fg)
	if !ok {
		return fg
	}
	br, bgc, bb, ok := parseHex(bg)
	if !ok {
		return fg
	}
	blend := func(a, b int) int {
		v := float64(a) + (float64(b)-float64(a))*amount
		return int(v + 0.5)
	}
	return fmt.Sprintf("#%02x%02x%02x", blend(fr, br), blend(fg2, bgc), blend(fb, bb))
}

func parseHex(s string) (r, g, b int, ok bool) {
	if !strings.HasPrefix(s, "#") {
		return 0, 0, 0, false
	}
	h := s[1:]
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	if len(h) != 6 {
		return 0, 0, 0, false
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return int(v >> 16 & 0xff), int(v >> 8 & 0xff), int(v & 0xff), true
}

// minCodeContrast is the least luminance difference code keeps against the
// cell behind it, however hard its syntax colour was muted. An intra-line
// change is held to minMarkContrast, because it is the text the reviewer is
// being pointed at.
const (
	minCodeContrast = 0.2
	minMarkContrast = 0.3
)

// readable lifts fg toward text, just far enough to stand floor clear of bg.
// Text that is itself too close to bg gives way to black or white. Colours
// already clear of bg, and anything unparseable, are returned unchanged.
func readable(fg, bg, text string, floor float64) string {
	if _, _, _, ok := parseHex(fg); !ok {
		return fg
	}
	if _, _, _, ok := parseHex(bg); !ok {
		return fg
	}
	if contrast(fg, bg) >= floor {
		return fg
	}
	lb := luminance(bg)
	target := text
	if _, _, _, ok := parseHex(target); !ok || contrast(target, bg) < floor {
		target = "#ffffff"
		if lb > 0.5 {
			target = "#000000"
		}
	}
	lf, lt := luminance(fg), luminance(target)
	goal := lb + floor
	if lt < lb {
		goal = lb - floor
	}
	// Luminance is linear in the blend, so the amount has a closed form. The
	// margin absorbs rounding to whole channel values.
	amount := (goal-lf)/(lt-lf) + 0.01
	return mix(target, fg, 1-min(amount, 1))
}

// luminance is a colour's relative brightness, 0 for black to 1 for white.
func luminance(hex string) float64 {
	r, g, b, ok := parseHex(hex)
	if !ok {
		return 0
	}
	return (0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(b)) / 255
}

// contrast is the luminance difference between two colours.
func contrast(a, b string) float64 {
	d := luminance(a) - luminance(b)
	if d < 0 {
		return -d
	}
	return d
}

// KnownSyntax reports whether chroma has a style by this name. It is how the
// config layer tells a mistyped theme name from a deliberate chroma style.
func KnownSyntax(name string) bool {
	return styles.Get(name) != nil && styles.Get(name).Name == name
}
