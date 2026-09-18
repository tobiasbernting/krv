package clipboard

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func env(pairs ...string) func(string) string {
	m := map[string]string{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i]] = pairs[i+1]
	}
	return func(k string) string { return m[k] }
}

func has(names ...string) func(string) (string, error) {
	return func(name string) (string, error) {
		for _, n := range names {
			if n == name {
				return "/usr/bin/" + name, nil
			}
		}
		return "", errors.New("not found")
	}
}

func TestSequenceCarriesTheTextForTheTerminal(t *testing.T) {
	var out bytes.Buffer
	c := Clipboard{Out: &out, Getenv: env(), LookPath: has()}
	if err := c.Copy("fmt.Println(1)\n"); err != nil {
		t.Fatal(err)
	}
	want := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("fmt.Println(1)\n")) + "\x07"
	if out.String() != want {
		t.Errorf("wrote %q, want %q", out.String(), want)
	}
}

func TestInsideTmuxTheSequenceIsPassedThrough(t *testing.T) {
	var out bytes.Buffer
	c := Clipboard{Out: &out, Getenv: env("TMUX", "/tmp/tmux-1/default,1,0"), LookPath: has()}
	if err := c.Copy("x"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "\x1bPtmux;") {
		t.Errorf("inside tmux wrote %q, want a tmux passthrough", out.String())
	}
}

func TestNativeCommandIsLocalOnly(t *testing.T) {
	cases := []struct {
		name string
		goos string
		env  func(string) string
		look func(string) (string, error)
		want string
	}{
		{"macOS", "darwin", env(), has("pbcopy"), "pbcopy"},
		{"Wayland", "linux", env("WAYLAND_DISPLAY", "wayland-0"), has("wl-copy", "xclip"), "wl-copy"},
		{"X11", "linux", env("DISPLAY", ":0"), has("xclip"), "xclip"},
		{"X11 without xclip", "linux", env("DISPLAY", ":0"), has(), ""},
		{"no display", "linux", env(), has("xclip"), ""},
		{"Windows", "windows", env(), has("clip.exe"), "clip.exe"},
		{"over SSH", "darwin", env("SSH_CONNECTION", "1.2.3.4 1 5.6.7.8 22"), has("pbcopy"), ""},
		{"over SSH tty", "linux", env("SSH_TTY", "/dev/pts/1", "DISPLAY", ":0"), has("xclip"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Clipboard{GOOS: tc.goos, Getenv: tc.env, LookPath: tc.look}
			got := ""
			if argv := c.native(); argv != nil {
				got = argv[0]
			}
			if got != tc.want {
				t.Errorf("native command %q, want %q", got, tc.want)
			}
		})
	}
}
