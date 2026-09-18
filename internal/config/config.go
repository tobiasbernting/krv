// Package config resolves crv's settings.
//
// Precedence, highest first: command-line flags, CRV_* environment
// variables, a per-repository .crv.toml, the user's config.toml, built-in
// defaults. Host is deliberately empty by default so that gh's own configured
// host wins — switching between a personal account and an enterprise one is a
// repository-local file rather than global state you forget you set.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/BurntSushi/toml"
)

const (
	// RepoFile is looked for at the root of the repository being reviewed.
	RepoFile = ".crv.toml"
	// UserFile lives beside the stored reviews.
	UserFile = "config.toml"
)

type Config struct {
	// Host is the GitHub hostname, e.g. "github.com" or an enterprise host.
	// Empty means "whatever gh is configured to use".
	Host string `toml:"host"`

	// Theme names a crv colour theme: dark, light or high-contrast. A chroma
	// style name is also accepted, for configurations written before crv had
	// themes of its own.
	Theme string `toml:"theme"`

	// Syntax overrides the chroma style the theme would otherwise pick.
	Syntax string `toml:"syntax"`

	// Density is "comfortable" or "compact".
	Density string `toml:"density"`

	// Layout is "unified" or "split".
	Layout string `toml:"layout"`

	// Editor overrides $EDITOR for composing longer notes.
	Editor string `toml:"editor"`

	// Untracked includes untracked files in a working-tree review.
	Untracked bool `toml:"untracked"`

	// Width is the output width used when stdout is not a terminal.
	Width int `toml:"width"`

	// Color enables syntax highlighting and diff colours.
	Color bool `toml:"color"`

	// Mouse lets crv take clicks, drags and the wheel. The terminal's own
	// text selection then needs a modifier held (Shift, or Option in iTerm2).
	Mouse bool `toml:"mouse"`

	// sources records where the settings came from, for `crv --config`.
	sources []string
}

func Defaults() Config {
	return Config{
		Theme:     "dark",
		Density:   "comfortable",
		Layout:    "unified",
		Untracked: true,
		Width:     120,
		Color:     true,
		Mouse:     true,
	}
}

// Sources lists the files that contributed, nearest-wins last.
func (c Config) Sources() []string { return c.sources }

// userConfigDir is a variable so tests can point it at a temporary directory.
// os.UserConfigDir is platform-specific — XDG_CONFIG_HOME on Linux but
// ~/Library/Application Support on macOS — so environment variables alone
// cannot redirect it portably.
var userConfigDir = os.UserConfigDir

// UserPath is the path to the user-level config file.
func UserPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, UserFile), nil
}

// Load merges the user file, then the repository file, then the environment.
// Flags are applied by the caller, which is the only layer that knows which
// were actually set.
func Load(repoRoot string) (Config, error) {
	cfg := Defaults()

	if path, err := UserPath(); err == nil {
		if err := mergeFile(&cfg, path); err != nil {
			return cfg, err
		}
	}
	if repoRoot != "" {
		if err := mergeFile(&cfg, filepath.Join(repoRoot, RepoFile)); err != nil {
			return cfg, err
		}
	}
	if err := mergeEnv(&cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// mergeFile applies a TOML file if it exists. Only keys present in the file
// are applied, so a partial file overrides nothing it does not mention.
func mergeFile(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	var file Config
	md, err := toml.Decode(string(data), &file)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	for _, key := range md.Keys() {
		switch key.String() {
		case "host":
			cfg.Host = file.Host
		case "theme":
			cfg.Theme = file.Theme
		case "syntax":
			cfg.Syntax = file.Syntax
		case "density":
			cfg.Density = file.Density
		case "layout":
			cfg.Layout = file.Layout
		case "editor":
			cfg.Editor = file.Editor
		case "untracked":
			cfg.Untracked = file.Untracked
		case "width":
			cfg.Width = file.Width
		case "color":
			cfg.Color = file.Color
		case "mouse":
			cfg.Mouse = file.Mouse
		default:
			return fmt.Errorf("%s: unknown setting %q", path, key.String())
		}
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		return fmt.Errorf("%s: unknown setting %q", path, undecoded[0].String())
	}
	cfg.sources = append(cfg.sources, path)
	return nil
}

func mergeEnv(cfg *Config) error {
	if v, ok := os.LookupEnv("CRV_HOST"); ok {
		cfg.Host = v
	}
	if v, ok := os.LookupEnv("CRV_THEME"); ok {
		cfg.Theme = v
	}
	if v, ok := os.LookupEnv("CRV_SYNTAX"); ok {
		cfg.Syntax = v
	}
	if v, ok := os.LookupEnv("CRV_DENSITY"); ok {
		cfg.Density = v
	}
	if v, ok := os.LookupEnv("CRV_LAYOUT"); ok {
		cfg.Layout = v
	}
	if v, ok := os.LookupEnv("CRV_EDITOR"); ok {
		cfg.Editor = v
	}
	if v, ok := os.LookupEnv("CRV_WIDTH"); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("CRV_WIDTH: %w", err)
		}
		cfg.Width = n
	}
	if v, ok := os.LookupEnv("CRV_UNTRACKED"); ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("CRV_UNTRACKED: %w", err)
		}
		cfg.Untracked = b
	}
	// NO_COLOR is a cross-tool convention: its presence disables colour
	// whatever its value.
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		cfg.Color = false
	}
	if v, ok := os.LookupEnv("CRV_COLOR"); ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("CRV_COLOR: %w", err)
		}
		cfg.Color = b
	}
	if v, ok := os.LookupEnv("CRV_MOUSE"); ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("CRV_MOUSE: %w", err)
		}
		cfg.Mouse = b
	}
	return nil
}

// EditorCommand is the editor to use for longer notes.
func (c Config) EditorCommand() string {
	for _, v := range []string{c.Editor, os.Getenv("VISUAL"), os.Getenv("EDITOR")} {
		if v != "" {
			return v
		}
	}
	return "vi"
}
