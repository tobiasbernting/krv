package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetTheme(t *testing.T) {
	for _, tc := range []struct {
		name, src, want string
	}{
		{
			name: "empty file",
			src:  "",
			want: "theme = \"nord\"\n",
		},
		{
			name: "active line keeps its comment",
			src:  "host = \"x\"\ntheme = \"dark\"       # dark, light or high-contrast\nwidth = 90\n",
			want: "host = \"x\"\ntheme = \"nord\"       # dark, light or high-contrast\nwidth = 90\n",
		},
		{
			name: "commented template line is taken over",
			src:  "# Colour theme.\n# theme = \"dark\"\n\n# density = \"compact\"\n",
			want: "# Colour theme.\ntheme = \"nord\"\n\n# density = \"compact\"\n",
		},
		{
			name: "an active line wins over a commented one",
			src:  "# theme = \"dark\"\ntheme = 'light'\n",
			want: "# theme = \"dark\"\ntheme = \"nord\"\n",
		},
		{
			name: "no theme anywhere is appended",
			src:  "density = \"compact\"",
			want: "density = \"compact\"\ntheme = \"nord\"\n",
		},
		{
			name: "a syntax line is not a theme line",
			src:  "# syntax = \"monokai\"\n",
			want: "# syntax = \"monokai\"\ntheme = \"nord\"\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(SetTheme([]byte(tc.src), "nord")); got != tc.want {
				t.Errorf("SetTheme:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}

// The generated template is what most people's file is. Saving a theme into
// it changes exactly one line and leaves a file krv still loads.
func TestSetThemeOnTheTemplate(t *testing.T) {
	src := Template()
	got := string(SetTheme([]byte(src), "gruvbox-dark"))
	a, b := strings.Split(src, "\n"), strings.Split(got, "\n")
	if len(a) != len(b) {
		t.Fatalf("line count changed: %d → %d", len(a), len(b))
	}
	changed := 0
	for i := range a {
		if a[i] != b[i] {
			changed++
			if b[i] != `theme = "gruvbox-dark"` {
				t.Errorf("changed line %d to %q", i+1, b[i])
			}
		}
	}
	if changed != 1 {
		t.Errorf("%d lines changed, want 1", changed)
	}
}

func TestSaveTheme(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "krv", UserFile)

	saved, err := SaveTheme("nord")
	if err != nil {
		t.Fatal(err)
	}
	if saved != path {
		t.Errorf("saved to %s, want %s", saved, path)
	}
	if _, err := SaveTheme("dracula"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "theme = \"dracula\"\n" {
		t.Errorf("file = %q", data)
	}
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Theme != "dracula" {
		t.Errorf("loaded theme %q, want dracula", cfg.Theme)
	}
}

func TestSaveThemeKeepsASymlinkedConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	dotfiles := filepath.Join(dir, "dotfiles.toml")
	if err := os.WriteFile(dotfiles, []byte("density = \"compact\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "krv", UserFile)
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dotfiles, link); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveTheme("nord"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was replaced by a file")
	}
	data, _ := os.ReadFile(dotfiles)
	if string(data) != "density = \"compact\"\ntheme = \"nord\"\n" {
		t.Errorf("dotfiles copy = %q", data)
	}
}
