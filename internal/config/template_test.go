package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tobiasbernting/krv/v2/internal/render"
)

// The shipped template must change nothing. A starter file that sets real
// values would pin today's defaults forever.
func TestTemplateOverridesNothing(t *testing.T) {
	if !TemplateIsInert() {
		t.Error("the template contains an active setting")
	}

	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	krv := filepath.Join(dir, "krv")
	if err := os.MkdirAll(krv, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(krv, UserFile), []byte(Template()), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("the template does not parse: %v", err)
	}
	d := Defaults()
	if got.Host != d.Host || got.Theme != d.Theme || got.Untracked != d.Untracked ||
		got.Color != d.Color || got.Width != d.Width {
		t.Errorf("the template changed the defaults:\ngot  %+v\nwant %+v", got, d)
	}
}

// Uncommenting a line has to produce something krv accepts, or the template
// is documentation for a format that does not exist.
func TestTemplateLinesWorkWhenUncommented(t *testing.T) {
	var uncommented []string
	for _, line := range strings.Split(Template(), "\n") {
		trimmed := strings.TrimSpace(line)
		// Setting lines look like "# key = value"; prose does not.
		if !strings.HasPrefix(trimmed, "# ") || !strings.Contains(trimmed, " = ") {
			continue
		}
		uncommented = append(uncommented, strings.TrimPrefix(trimmed, "# "))
	}
	if len(uncommented) < 6 {
		t.Fatalf("found only %d settings in the template: %v", len(uncommented), uncommented)
	}

	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(dir, RepoFile), []byte(strings.Join(uncommented, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err != nil {
		t.Fatalf("the template's own settings were rejected: %v\n%s", err, strings.Join(uncommented, "\n"))
	}
}

func TestTemplateDocumentsEverySetting(t *testing.T) {
	tmpl := Template()
	for _, key := range []string{"host", "theme", "density", "layout", "editor", "untracked", "color", "width"} {
		if !strings.Contains(tmpl, "# "+key+" = ") {
			t.Errorf("the template does not show %q", key)
		}
	}
}

func TestInitWritesAndRefusesToOverwrite(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	path, err := Init()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != Template() {
		t.Error("what was written is not the template")
	}

	// A second run must not clobber whatever has been edited in since.
	if err := os.WriteFile(path, []byte("theme = \"dracula\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(); err != ErrConfigExists {
		t.Errorf("second Init returned %v, want ErrConfigExists", err)
	}
	again, _ := os.ReadFile(path)
	if string(again) != "theme = \"dracula\"\n" {
		t.Error("Init overwrote an existing configuration")
	}
}

// The template lists the themes by name, so it has to keep up with the presets.
func TestTemplateNamesEveryTheme(t *testing.T) {
	for _, name := range render.ThemeNames() {
		if !strings.Contains(Template(), " "+name) {
			t.Errorf("the template does not mention the %s theme", name)
		}
	}
}
