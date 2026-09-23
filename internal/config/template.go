package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Template is a starter configuration file.
//
// Every setting is commented out on purpose. A file that lists real values
// pins them: change a default in a later version and anyone holding such a
// file never sees it, because their file mentions the key. Commented lines
// document what exists and what it currently defaults to, while leaving krv
// free to change its mind.
func Template() string {
	d := Defaults()
	return fmt.Sprintf(`# krv configuration
#
# Every setting is optional and shown here with its current default.
# Uncomment a line to override it — while a line stays commented, krv keeps
# using its own default, including if that default changes in a later version.
#
# Precedence, highest first:
#   1. command-line flags
#   2. environment: KRV_HOST, KRV_THEME, KRV_SYNTAX, KRV_DENSITY,
#      KRV_LAYOUT, KRV_EDITOR, KRV_WIDTH, KRV_UNTRACKED, KRV_COLOR,
#      KRV_MOUSE, NO_COLOR
#   3. %s in the repository being reviewed
#   4. this file
#
# Run "krv --config" to see what is actually in effect.

# GitHub hostname. Left unset, krv uses whatever gh is configured with, which
# is usually what you want. Set it in a repository's %s to review
# on an enterprise host without changing anything globally.
# host = "github.example.com"

# Colour theme. Pick the one that matches your terminal — krv does not guess,
# because guessing wrong is worse than being told once. Press T in krv to try
# them all on the diff in front of you; enter saves the choice here.
#   dark:         dark, dracula, gruvbox-dark, high-contrast, nord, tokyonight
#   light:        light, solarized-light
#   colour-blind: dark-cb, light-cb (blue and orange instead of green and red)
# theme = "%s"

# Syntax highlighting style: any chroma style name, overriding the one the
# theme comes with. See https://xyproto.github.io/splash/docs/ for the list.
# syntax = "catppuccin-mocha"

# Row density: comfortable separates hunks and annotation groups; compact
# gives every line of the terminal to the diff.
# density = "%s"

# Diff layout: unified interleaves old and new in one column; split puts them
# side by side. Split needs a wide terminal — below 140 columns krv draws
# unified until there is room. Toggle for the session with s.
# layout = "%s"

# Editor for composing longer notes with ctrl+e.
# Unset, krv uses $VISUAL, then $EDITOR, then vi.
# editor = "hx"

# Editor o opens the file under the cursor in, at that line. Unset, krv uses
# the editor above. Terminal editors (vi, vim, nvim, nano, hx, kak, micro,
# emacs) take over the terminal until you quit them; VS Code (code), Cursor,
# Windsurf, Zed and Sublime Text (subl) open beside krv.
# open_editor = "code"

# How to call an editor krv does not know, with {file} and {line}, and
# whether krv waits for it to exit before carrying on.
# open_editor_cmd = "myedit --line {line} {file}"
# open_editor_wait = true

# Include untracked files when reviewing uncommitted work with "krv .".
# On, because new files are usually the substance of generated-code review.
# untracked = %t

# Syntax highlighting and diff colours. NO_COLOR disables them whatever this
# says.
# color = %t

# Clicks, drags and the wheel. Click moves the cursor, a double click opens,
# a drag selects lines. While krv has the mouse, the terminal's own text
# selection needs a modifier: hold Shift (Option in iTerm2, fn in Terminal.app).
# mouse = %t

# Output width used when stdout is not a terminal.
# width = %d
`, RepoFile, RepoFile, d.Theme, d.Density, d.Layout, d.Untracked, d.Color, d.Mouse, d.Width)
}

// ErrConfigExists is returned rather than overwriting someone's settings.
var ErrConfigExists = errors.New("configuration file already exists")

// Init writes the template to the user-level configuration path. It refuses
// to overwrite an existing file: this is a convenience, not something worth
// losing settings to.
func Init() (path string, err error) {
	path, err = UserPath()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err == nil {
		return path, ErrConfigExists
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return path, err
	}
	return path, os.WriteFile(path, []byte(Template()), 0o600)
}

// TemplateIsInert reports whether the template, as written, changes nothing.
// It exists so a test can prove the shipped file overrides no defaults.
func TemplateIsInert() bool {
	for _, line := range strings.Split(Template(), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			return false
		}
	}
	return true
}
