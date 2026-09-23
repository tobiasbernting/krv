package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/tobiasbernting/krv/v2/internal/config"
	"github.com/tobiasbernting/krv/v2/internal/diffparse"
	"github.com/tobiasbernting/krv/v2/internal/ghsrc"
	"github.com/tobiasbernting/krv/v2/internal/render"
)

// fakeSaver records what the picker saved instead of touching the user's
// configuration.
type fakeSaver struct {
	saved []string
	err   error
}

func (f *fakeSaver) save(name string) (string, error) {
	f.saved = append(f.saved, name)
	return "/home/me/.config/krv/config.toml", f.err
}

func newThemeModel(t *testing.T, cfg config.Config, saver *fakeSaver) Model {
	t.Helper()
	m := New(Options{
		Files:     diffparse.Parse(navDiff),
		Theme:     render.DefaultTheme(),
		Config:    cfg,
		Source:    Source{Kind: SourceLocal, Title: "test"},
		Review:    newTestReview(t),
		SaveTheme: saver.save,
	})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return next.(Model)
}

func TestThemePickerListsEveryPresetByGroup(t *testing.T) {
	m := press(t, newThemeModel(t, config.Defaults(), &fakeSaver{}), "T")
	view := m.plainView()
	for _, want := range []string{"dark", "light", "colour-blind", "nord", "solarized-light", "dark-cb"} {
		if !strings.Contains(view, want) {
			t.Errorf("picker does not show %q:\n%s", want, view)
		}
	}
	// It sits bottom-right, over the diff rather than instead of it.
	for _, line := range strings.Split(view, "\n") {
		if i := strings.Index(line, "solarized-light"); i >= 0 && ansi.StringWidth(line[:i]) < 50 {
			t.Errorf("the picker is not on the right of a 100-column screen:\n%s", view)
		}
	}
}

// Moving through the list restyles the review at once, so the diff behind the
// picker is the preview.
func TestThemePickerPreviewsAsYouMove(t *testing.T) {
	m := press(t, newThemeModel(t, config.Defaults(), &fakeSaver{}), "T", "j")
	if m.theme.Name != "dracula" {
		t.Errorf("after j the review shows %q, want the next preset, dracula", m.theme.Name)
	}
	if m.theme.Bg == render.DefaultTheme().Bg {
		t.Error("the preview did not change the surface")
	}
}

func TestThemePickerEscRevertsWithoutSaving(t *testing.T) {
	saver := &fakeSaver{}
	m := press(t, newThemeModel(t, config.Defaults(), saver), "T", "j", "j", "esc")
	if m.theme.Name != "dark" {
		t.Errorf("after esc the review shows %q, want dark back", m.theme.Name)
	}
	if len(saver.saved) != 0 {
		t.Errorf("esc saved %v", saver.saved)
	}
	if strings.Contains(m.plainView(), "solarized-light") {
		t.Error("the picker is still open after esc")
	}
	// The next key is the diff's again.
	if before := m.cursor; press(t, m, "j").cursor == before {
		t.Error("j did not move the diff cursor after the picker closed")
	}
}

func TestThemePickerEnterSavesAndSays(t *testing.T) {
	saver := &fakeSaver{}
	m := press(t, newThemeModel(t, config.Defaults(), saver), "T", "j", "enter")
	if len(saver.saved) != 1 || saver.saved[0] != "dracula" {
		t.Fatalf("saved %v, want [dracula]", saver.saved)
	}
	if m.theme.Name != "dracula" {
		t.Errorf("theme after saving = %q", m.theme.Name)
	}
	bar := m.plainView()
	if !strings.Contains(bar, "theme: dracula") || !strings.Contains(bar, "saved to") {
		t.Errorf("status does not report the save:\n%s", bar)
	}
}

// A theme set somewhere nearer than the user file still wins next time, and
// the picker says so rather than letting the save look like it did nothing.
func TestThemePickerNamesWhatOverridesTheSave(t *testing.T) {
	for _, tc := range []struct{ from, want string }{
		{"KRV_THEME", "KRV_THEME wins here"},
		{"--theme", "--theme wins here"},
		{"/src/app/.krv.toml", ".krv.toml wins here"},
	} {
		cfg := config.Defaults()
		cfg.ThemeFrom = tc.from
		m := press(t, newThemeModel(t, cfg, &fakeSaver{}), "T", "j", "enter")
		if view := m.plainView(); !strings.Contains(view, tc.want) {
			t.Errorf("theme from %s: status does not say %q:\n%s", tc.from, tc.want, view)
		}
		if m.theme.Name != "dracula" {
			t.Errorf("theme from %s: the session did not take the choice", tc.from)
		}
	}
}

func TestThemePickerReportsAFailedSave(t *testing.T) {
	saver := &fakeSaver{err: errors.New("read-only file system")}
	m := press(t, newThemeModel(t, config.Defaults(), saver), "T", "j", "enter")
	if view := m.plainView(); !strings.Contains(view, "read-only file system") {
		t.Errorf("the failure is not shown:\n%s", view)
	}
}

func TestThemePickerNeedsColour(t *testing.T) {
	cfg := config.Defaults()
	cfg.Color = false
	m := press(t, newThemeModel(t, cfg, &fakeSaver{}), "T")
	view := m.plainView()
	if !strings.Contains(view, "colour is off") {
		t.Errorf("T with colour off does not say so:\n%s", view)
	}
	if strings.Contains(view, "solarized-light") {
		t.Error("the picker opened with colour off")
	}
}

// The syntax setting overrides every theme's own style, so it has to survive
// previewing one.
func TestThemePickerKeepsTheSyntaxOverride(t *testing.T) {
	cfg := config.Defaults()
	cfg.Syntax = "monokai"
	m := press(t, newThemeModel(t, cfg, &fakeSaver{}), "T", "j")
	if m.theme.Syntax != "monokai" {
		t.Errorf("previewed theme highlights with %q, want the configured monokai", m.theme.Syntax)
	}
}

func TestThemePickerOpensFromTheFileList(t *testing.T) {
	m := press(t, newThemeModel(t, config.Defaults(), &fakeSaver{}), "f", "T", "j")
	if m.theme.Name != "dracula" {
		t.Errorf("T in the file list: theme = %q, want dracula", m.theme.Name)
	}
	if m.mode != modeFiles {
		t.Error("the picker took the file list away")
	}
}

// The queue picks a theme the same way, and a review opened afterwards uses
// it — as does the queue again once the review changed it.
func TestThemePickerInTheQueue(t *testing.T) {
	saver := &fakeSaver{}
	a := newApp(t, openerFor(t, nil))
	a.queue = a.queue.WithThemes(config.Defaults(), saver.save)
	a, _ = settle(t, a, a.Init())

	a, _ = pressA(t, a, "T", "j", "enter")
	if a.queue.theme.Name != "dracula" || len(saver.saved) != 1 {
		t.Fatalf("queue theme = %q, saved %v", a.queue.theme.Name, saver.saved)
	}
	a, _ = pressA(t, a, "enter")
	if a.screen != screenReview {
		t.Fatal("the pull request did not open")
	}
	if a.review.theme.Name != "dracula" {
		t.Errorf("review opened in %q, want the queue's dracula", a.review.theme.Name)
	}

	a, _ = pressA(t, a, "T", "j", "enter", "q")
	if a.queue.theme.Name != "gruvbox-dark" {
		t.Errorf("back in the queue: theme = %q, want the review's gruvbox-dark", a.queue.theme.Name)
	}
}

// Keeping the theme you opened on is not a choice worth writing down, and for
// a configuration that names a chroma style it would replace that style with
// the plain dark theme.
func TestThemePickerKeepingTheSameThemeSavesNothing(t *testing.T) {
	saver := &fakeSaver{}
	m := press(t, newThemeModel(t, config.Defaults(), saver), "T", "enter")
	if len(saver.saved) != 0 {
		t.Errorf("enter on the theme already in use saved %v", saver.saved)
	}
	if view := m.plainView(); !strings.Contains(view, "theme: dark, unchanged") {
		t.Errorf("status does not say nothing changed:\n%s", view)
	}
}

// An empty queue draws only a few lines, but the picker still gets the
// screen, and what it says after a save is not lost with the list.
func TestThemePickerInAnEmptyQueue(t *testing.T) {
	q := NewQueue(ghsrc.Client{}, render.DefaultTheme(), 30).WithThemes(config.Defaults(), (&fakeSaver{}).save)
	next, _ := q.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	q = next.(QueueModel)
	next, _ = q.Update(queueLoadedMsg{filter: q.filter})
	q = pressQ(t, next.(QueueModel), "T")
	if view := ansi.Strip(q.View()); !strings.Contains(view, "light-cb") {
		t.Errorf("the picker is cut short on an empty queue:\n%s", view)
	}
	q = pressQ(t, q, "j", "enter")
	if view := ansi.Strip(q.View()); !strings.Contains(view, "saved to") {
		t.Errorf("the save is not reported on an empty queue:\n%s", view)
	}
}
