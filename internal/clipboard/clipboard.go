// Package clipboard puts text on the system clipboard from inside a terminal
// program, locally or over SSH.
//
// It always asks the terminal, with an OSC 52 escape sequence: that is the
// only route that reaches the user's own machine from a remote shell. Not
// every terminal honours it — macOS Terminal.app does not — so when crv runs
// locally it also hands the text to the platform's copy command. Writing
// twice is harmless; the second write carries the same text.
package clipboard

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/aymanbagabas/go-osc52/v2"
)

type Clipboard struct {
	// Out is the terminal. The escape sequence goes to it in one write, so it
	// cannot interleave with a frame the UI is drawing.
	Out io.Writer

	GOOS     string
	Getenv   func(string) string
	LookPath func(string) (string, error)
}

// New is the clipboard of the terminal crv is running in.
func New() Clipboard {
	return Clipboard{Out: os.Stdout, GOOS: runtime.GOOS, Getenv: os.Getenv, LookPath: exec.LookPath}
}

// Copy puts text on the clipboard. It fails only when neither route could
// be attempted; whether the terminal honoured OSC 52 cannot be known.
func (c Clipboard) Copy(text string) error {
	seq := osc52.New(text)
	switch {
	case c.Getenv("TMUX") != "":
		seq = seq.Tmux()
	case strings.HasPrefix(c.Getenv("TERM"), "screen"):
		seq = seq.Screen()
	}
	_, termErr := io.WriteString(c.Out, seq.String())

	argv := c.native()
	if argv == nil {
		return termErr
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil && termErr != nil {
		return fmt.Errorf("%s: %w", argv[0], err)
	}
	return nil
}

// native is the platform's copy command, or nil over SSH — there it would
// fill the remote machine's clipboard, not the user's — and when none is
// installed.
func (c Clipboard) native() []string {
	if c.Getenv("SSH_CONNECTION") != "" || c.Getenv("SSH_TTY") != "" {
		return nil
	}
	var candidates [][]string
	switch c.GOOS {
	case "darwin":
		candidates = [][]string{{"pbcopy"}}
	case "windows":
		candidates = [][]string{{"clip.exe"}}
	case "linux", "freebsd", "openbsd", "netbsd":
		if c.Getenv("WAYLAND_DISPLAY") != "" {
			candidates = append(candidates, []string{"wl-copy"})
		}
		if c.Getenv("DISPLAY") != "" {
			candidates = append(candidates,
				[]string{"xclip", "-selection", "clipboard"},
				[]string{"xsel", "--clipboard", "--input"})
		}
	}
	for _, argv := range candidates {
		if _, err := c.LookPath(argv[0]); err == nil {
			return argv
		}
	}
	return nil
}
