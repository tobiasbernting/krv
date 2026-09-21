// Package browser opens a link in the user's web browser, or copies it when
// that cannot work.
//
// Over SSH the browser that would start is the remote machine's, which the
// user cannot see, so the link goes to their clipboard instead (see package
// clipboard: OSC 52 reaches the local terminal). The same happens when the
// browser does not start, and for anything that is not an http or https URL:
// a link from a pull request's Description is untrusted, and handing a
// file: or custom-scheme URL to the system opener could launch anything.
package browser

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/tobiasbernting/krv/v2/internal/clipboard"
)

type Opener struct {
	GOOS   string
	Getenv func(string) string

	// Start runs a program without waiting for it.
	Start func(name string, args ...string) error

	// Copy puts text on the clipboard: where a link goes when it is not
	// opened.
	Copy func(text string) error
}

// New is the opener of the machine krv runs on.
func New() Opener {
	return Opener{
		GOOS:   runtime.GOOS,
		Getenv: os.Getenv,
		Start: func(name string, args ...string) error {
			cmd := exec.Command(name, args...)
			if err := cmd.Start(); err != nil {
				return err
			}
			go cmd.Wait()
			return nil
		},
		Copy: clipboard.New().Copy,
	}
}

// Open opens link in the browser. copied reports it went to the clipboard
// instead, which the UI says as "copied link"; err is set only when neither
// worked.
func (o Opener) Open(link string) (copied bool, err error) {
	var startErr error
	if Openable(link) && o.Getenv("SSH_CONNECTION") == "" {
		argv := Command(o.GOOS, link)
		if startErr = o.Start(argv[0], argv[1:]...); startErr == nil {
			return false, nil
		}
	}
	if o.Copy == nil {
		return false, errors.Join(startErr, errors.New("no clipboard to copy the link to"))
	}
	if err := o.Copy(link); err != nil {
		if startErr != nil {
			return false, fmt.Errorf("%w; copying the link failed too: %w", startErr, err)
		}
		return false, fmt.Errorf("could not copy the link: %w", err)
	}
	return true, nil
}

// Openable reports link is an http or https URL, the only kind handed to the
// system's opener.
func Openable(link string) bool {
	u, err := url.Parse(link)
	if err != nil || u.Host == "" {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	return scheme == "http" || scheme == "https"
}

// Command is the program that opens link in the browser on goos.
func Command(goos, link string) []string {
	switch goos {
	case "darwin":
		return []string{"open", link}
	case "windows":
		return []string{"rundll32", "url.dll,FileProtocolHandler", link}
	}
	return []string{"xdg-open", link}
}
