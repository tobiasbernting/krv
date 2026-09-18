package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// useConfigDir points the configuration directory at a temporary location
// and returns the crv subdirectory inside it.
func useConfigDir(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	crv, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(crv, 0o700); err != nil {
		t.Fatal(err)
	}
	return crv
}

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDefaultsWhenNothingConfigured(t *testing.T) {
	useConfigDir(t)
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d := Defaults()
	if cfg.Theme != d.Theme || cfg.Untracked != d.Untracked || cfg.Width != d.Width || cfg.Color != d.Color {
		t.Errorf("got %+v, want defaults %+v", cfg, d)
	}
	// Host stays empty so gh's own configured host is used.
	if cfg.Host != "" {
		t.Errorf("default host = %q, want empty", cfg.Host)
	}
}

func TestRepoFileOverridesUserFile(t *testing.T) {
	write(t, useConfigDir(t), UserFile, "host = \"github.com\"\ntheme = \"dracula\"\n")

	repo := t.TempDir()
	write(t, repo, RepoFile, "host = \"github.acme.internal\"\n")

	cfg, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "github.acme.internal" {
		t.Errorf("host = %q, want the repository's", cfg.Host)
	}
	// A key the repository file does not mention keeps the user's value.
	if cfg.Theme != "dracula" {
		t.Errorf("theme = %q, want dracula from the user file", cfg.Theme)
	}
}

// A false value in a file must override a true default; naive merging that
// skips zero values gets this wrong.
func TestFileCanSetFalse(t *testing.T) {
	useConfigDir(t)
	repo := t.TempDir()
	write(t, repo, RepoFile, "untracked = false\n")

	cfg, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Untracked {
		t.Error("untracked = true, want the file's false to win over the default")
	}
}

func TestEnvOverridesFiles(t *testing.T) {
	useConfigDir(t)
	repo := t.TempDir()
	write(t, repo, RepoFile, "host = \"from-file\"\nwidth = 100\n")
	t.Setenv("CRV_HOST", "from-env")

	cfg, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "from-env" {
		t.Errorf("host = %q, want from-env", cfg.Host)
	}
	if cfg.Width != 100 {
		t.Errorf("width = %d, want the file's 100", cfg.Width)
	}
}

func TestNoColorConvention(t *testing.T) {
	useConfigDir(t)
	t.Setenv("NO_COLOR", "")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Color {
		t.Error("NO_COLOR present but colour still enabled")
	}
}

func TestUnknownSettingIsAnError(t *testing.T) {
	useConfigDir(t)
	repo := t.TempDir()
	write(t, repo, RepoFile, "colour = \"blue\"\n")

	_, err := Load(repo)
	if err == nil {
		t.Fatal("expected an error for an unknown setting")
	}
	if !strings.Contains(err.Error(), "colour") {
		t.Errorf("error %q does not name the offending key", err)
	}
}

func TestBadEnvValueIsAnError(t *testing.T) {
	useConfigDir(t)
	t.Setenv("CRV_WIDTH", "wide")
	if _, err := Load(""); err == nil {
		t.Error("expected an error for a non-numeric CRV_WIDTH")
	}
}

// layout goes through every layer like any other setting: the user file,
// then the repository file, then CRV_LAYOUT. (Flags are applied by cmd/crv.)
func TestLayoutPrecedence(t *testing.T) {
	user := useConfigDir(t)
	repo := t.TempDir()

	cfg, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Layout != "unified" {
		t.Errorf("default layout = %q, want unified", cfg.Layout)
	}

	write(t, user, UserFile, "layout = \"split\"\n")
	if cfg, err = Load(repo); err != nil {
		t.Fatal(err)
	}
	if cfg.Layout != "split" {
		t.Errorf("layout = %q, want split from the user file", cfg.Layout)
	}

	write(t, repo, RepoFile, "layout = \"unified\"\n")
	if cfg, err = Load(repo); err != nil {
		t.Fatal(err)
	}
	if cfg.Layout != "unified" {
		t.Errorf("layout = %q, want unified from the repository file", cfg.Layout)
	}

	t.Setenv("CRV_LAYOUT", "split")
	if cfg, err = Load(repo); err != nil {
		t.Fatal(err)
	}
	if cfg.Layout != "split" {
		t.Errorf("layout = %q, want split from CRV_LAYOUT", cfg.Layout)
	}
}

func TestEditorPrecedence(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "nano")
	if got := (Config{}).EditorCommand(); got != "nano" {
		t.Errorf("EditorCommand = %q, want nano", got)
	}
	if got := (Config{Editor: "hx"}).EditorCommand(); got != "hx" {
		t.Errorf("configured editor ignored, got %q", got)
	}
	t.Setenv("EDITOR", "")
	if got := (Config{}).EditorCommand(); got != "vi" {
		t.Errorf("fallback = %q, want vi", got)
	}
}

func TestMouseIsOnByDefaultAndCanBeTurnedOff(t *testing.T) {
	useConfigDir(t)
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Mouse {
		t.Error("mouse is off by default, want on")
	}

	repo := t.TempDir()
	write(t, repo, RepoFile, "mouse = false\n")
	cfg, err = Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mouse {
		t.Error("mouse = false in the file left it on")
	}

	t.Setenv("CRV_MOUSE", "true")
	cfg, err = Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Mouse {
		t.Error("CRV_MOUSE=true should win over the file's false")
	}
}
