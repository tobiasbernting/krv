package render

import (
	"reflect"
	"strings"
	"testing"
)

// Every preset has to answer every semantic role. A theme with an empty field
// paints that element in the terminal's default colour, which is exactly the
// unreadable combination — dark text on a dark tint — the presets exist to
// avoid.
func TestPresetsFillEveryRole(t *testing.T) {
	for _, name := range ThemeNames() {
		th, ok := ThemeByName(name)
		if !ok {
			t.Fatalf("ThemeNames listed %q but ThemeByName does not know it", name)
		}
		v := reflect.ValueOf(th)
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			if f.Type.Kind() != reflect.String || f.Name == "Name" {
				continue
			}
			if v.Field(i).String() == "" {
				t.Errorf("theme %s: %s is empty", name, f.Name)
			}
		}
		if th.Name != name {
			t.Errorf("theme %s: Name = %q", name, th.Name)
		}
	}
}

// The old field names are what the queue, the submit view and the status bar
// still read. They have to keep tracking the roles they were renamed from.
func TestPresetsKeepCompatibilityAliases(t *testing.T) {
	for _, name := range ThemeNames() {
		th, _ := ThemeByName(name)
		for _, c := range []struct{ alias, role, got, want string }{
			{"Gutter", "LineNumFg", th.Gutter, th.LineNumFg},
			{"AddFg", "AddSign", th.AddFg, th.AddSign},
			{"DelFg", "DelSign", th.DelFg, th.DelSign},
		} {
			if c.got != c.want {
				t.Errorf("theme %s: %s = %q, want %s (%q)", name, c.alias, c.got, c.role, c.want)
			}
		}
	}
}

func TestThemeByNameIsCaseAndSpaceTolerant(t *testing.T) {
	if _, ok := ThemeByName("  High-Contrast "); !ok {
		t.Error("high-contrast not found with surrounding space and capitals")
	}
	if _, ok := ThemeByName("solarized"); ok {
		t.Error("an unknown name must not resolve, or a typo silently picks a theme")
	}
}

// The presets exist to be selected explicitly, so the default has to be one of
// them rather than a fourth palette nobody can name.
func TestDefaultThemeIsAPreset(t *testing.T) {
	if DefaultTheme().Name != "dark" {
		t.Errorf("DefaultTheme = %q, want dark", DefaultTheme().Name)
	}
}

func TestKnownSyntax(t *testing.T) {
	if !KnownSyntax("catppuccin-mocha") {
		t.Error("catppuccin-mocha should be a known chroma style")
	}
	if KnownSyntax("not-a-style") {
		t.Error("an unknown chroma style must not be reported as known")
	}
}

func TestMix(t *testing.T) {
	for _, tc := range []struct {
		fg, bg string
		amount float64
		want   string
	}{
		{"#000000", "#ffffff", 0.5, "#808080"},
		{"#000000", "#ffffff", 1, "#ffffff"},
		{"#123456", "#ffffff", 0, "#123456"},
		{"#fff", "#000000", 0.5, "#808080"},
		// Anything unparseable is left alone rather than guessed at.
		{"red", "#000000", 0.5, "red"},
		{"#123456", "", 0.5, "#123456"},
	} {
		if got := mix(tc.fg, tc.bg, tc.amount); got != tc.want {
			t.Errorf("mix(%q, %q, %v) = %q, want %q", tc.fg, tc.bg, tc.amount, got, tc.want)
		}
	}
}

// Light and dark have to disagree about which end of the scale text sits on;
// a light theme that kept dark-theme foregrounds would be unreadable on the
// background it asks for.
func TestLightAndDarkAreOpposites(t *testing.T) {
	light, _ := ThemeByName("light")
	dark, _ := ThemeByName("dark")
	if luminance(light.Bg) <= luminance(light.Fg) {
		t.Error("light theme: background is not lighter than its text")
	}
	if luminance(dark.Bg) >= luminance(dark.Fg) {
		t.Error("dark theme: background is not darker than its text")
	}
}

// Every preset's plain text has to read on its surface and on every tint it
// paints behind a line.
func TestPresetsKeepTextReadableOnTints(t *testing.T) {
	for _, name := range ThemeNames() {
		th, _ := ThemeByName(name)
		if contrast(th.Fg, th.Bg) < 0.35 {
			t.Errorf("theme %s: code text has too little contrast against the surface", th.Name)
		}
		for _, tint := range []string{th.AddBg, th.DelBg, th.AddBgFocus, th.DelBgFocus, th.CursorBg} {
			if contrast(th.Fg, tint) < 0.3 {
				t.Errorf("theme %s: text on tint %s is too close in tone", th.Name, tint)
			}
		}
	}
}

// The picker lists every preset exactly once, grouped by the surface it sets
// and then by whether it leans on red and green, alphabetical within a group.
func TestThemeGroups(t *testing.T) {
	want := []ThemeGroup{
		{"dark", []string{"dark", "dracula", "gruvbox-dark", "high-contrast", "nord", "tokyonight"}},
		{"light", []string{"light", "solarized-light"}},
		{"colour-blind", []string{"dark-cb", "light-cb"}},
	}
	if got := ThemeGroups(); !reflect.DeepEqual(got, want) {
		t.Errorf("ThemeGroups() = %v\nwant %v", got, want)
	}
	seen := map[string]bool{}
	for _, g := range ThemeGroups() {
		for _, n := range g.Names {
			seen[n] = true
		}
	}
	for _, n := range ThemeNames() {
		if !seen[n] {
			t.Errorf("preset %s is in no group, so the picker cannot offer it", n)
		}
	}
}

// Presets named after a chroma style take over that name: they highlight with
// the style they are named for, so a configuration that meant the style gets
// the full theme around it.
func TestPresetsNamedForAStyleUseIt(t *testing.T) {
	for _, name := range ThemeNames() {
		th, _ := ThemeByName(name)
		if KnownSyntax(name) && th.Syntax != name {
			t.Errorf("theme %s shadows the chroma style of that name but highlights with %s", name, th.Syntax)
		}
	}
}

// The colour-blind presets mark changes in blue and orange, the pair that
// survives red-green colour blindness.
func TestColourBlindPresetsAvoidRedAndGreen(t *testing.T) {
	for _, name := range []string{"dark-cb", "light-cb"} {
		th, _ := ThemeByName(name)
		for _, c := range []string{th.AddSign, th.AddEdge, th.AddWordBg} {
			r, g, b, _ := parseHex(c)
			if b <= r || b <= g {
				t.Errorf("theme %s: add colour %s is not blue", name, c)
			}
		}
		for _, c := range []string{th.DelSign, th.DelEdge, th.DelWordBg} {
			r, g, b, _ := parseHex(c)
			if !(r > g && g > b) {
				t.Errorf("theme %s: delete colour %s is not orange", name, c)
			}
		}
	}
}

// The high-contrast theme has to out-contrast the others, and cannot lean on
// colour alone for intra-line changes.
func TestHighContrastIsStronger(t *testing.T) {
	hc, _ := ThemeByName("high-contrast")
	dark, _ := ThemeByName("dark")
	if contrast(hc.Fg, hc.Bg) <= contrast(dark.Fg, dark.Bg) {
		t.Error("high-contrast is no stronger than dark")
	}
	if hc.SyntaxMute >= dark.SyntaxMute {
		t.Error("high-contrast should mute syntax less, not more")
	}
	if !hc.MarkUnderline {
		t.Error("high-contrast should underline intra-line changes as well as shade them")
	}
}

// Added and deleted rows must differ in glyph, not only in hue.
func TestPresetsSeparateAddAndDeleteWithoutColour(t *testing.T) {
	if signAdd == signDel {
		t.Fatal("the add and delete signs are identical")
	}
	if strings.TrimSpace(edgeChange) == "" {
		t.Fatal("the edge marker is blank, leaving tint as the only cue")
	}
}
