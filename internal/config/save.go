package config

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

var (
	activeTheme    = regexp.MustCompile(`(?m)^([ \t]*theme[ \t]*=[ \t]*)("[^"\n]*"|'[^'\n]*')`)
	commentedTheme = regexp.MustCompile(`(?m)^[ \t]*#[ \t]*theme[ \t]*=.*$`)
)

// SetTheme returns src with its theme set to name, touching nothing else: an
// active theme line has its value replaced and keeps any comment after it,
// otherwise the template's commented-out theme line is taken over, otherwise a
// line is appended. The template's comments are the point of the file, so the
// TOML is edited as text rather than decoded and re-encoded without them.
func SetTheme(src []byte, name string) []byte {
	value := strconv.Quote(name)
	if loc := activeTheme.FindSubmatchIndex(src); loc != nil {
		return splice(src, loc[4], loc[5], value)
	}
	if loc := commentedTheme.FindIndex(src); loc != nil {
		return splice(src, loc[0], loc[1], "theme = "+value)
	}
	out := append([]byte{}, src...)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	return append(out, "theme = "+value+"\n"...)
}

func splice(src []byte, start, end int, with string) []byte {
	out := append([]byte{}, src[:start]...)
	out = append(out, with...)
	return append(out, src[end:]...)
}

// SaveTheme sets the theme in the user's config file, creating the file with
// just that line if there is none yet, and returns the file's path.
func SaveTheme(name string) (string, error) {
	path, err := UserPath()
	if err != nil {
		return "", err
	}
	src, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return path, err
	}
	// A config symlinked from a dotfiles repository is saved through the
	// link, not replaced by a file of its own.
	target := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		target = resolved
	}
	perm := os.FileMode(0o600)
	if info, err := os.Stat(target); err == nil {
		perm = info.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return path, err
	}
	// Written beside the file and renamed over it, so a failed write never
	// leaves someone's configuration truncated.
	tmp, err := os.CreateTemp(filepath.Dir(target), UserFile+".*")
	if err != nil {
		return path, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(SetTheme(src, name)); err != nil {
		tmp.Close()
		return path, err
	}
	if err := tmp.Close(); err != nil {
		return path, err
	}
	if err := os.Chmod(tmp.Name(), perm); err != nil {
		return path, err
	}
	return path, os.Rename(tmp.Name(), target)
}
