package browser

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// fake is an Opener that records what it started and copied.
type fake struct {
	started  [][]string
	copied   []string
	startErr error
	copyErr  error
}

func (f *fake) opener(goos string, env map[string]string) Opener {
	return Opener{
		GOOS:   goos,
		Getenv: func(k string) string { return env[k] },
		Start: func(name string, args ...string) error {
			f.started = append(f.started, append([]string{name}, args...))
			return f.startErr
		},
		Copy: func(text string) error {
			f.copied = append(f.copied, text)
			return f.copyErr
		},
	}
}

const link = "https://github.com/acme/x/pull/7"

func TestOpenStartsThePlatformOpener(t *testing.T) {
	for goos, want := range map[string][]string{
		"darwin":  {"open", link},
		"linux":   {"xdg-open", link},
		"freebsd": {"xdg-open", link},
		"windows": {"rundll32", "url.dll,FileProtocolHandler", link},
	} {
		t.Run(goos, func(t *testing.T) {
			f := &fake{}
			copied, err := f.opener(goos, nil).Open(link)
			if err != nil || copied {
				t.Fatalf("copied=%t err=%v", copied, err)
			}
			if len(f.started) != 1 || !reflect.DeepEqual(f.started[0], want) || len(f.copied) != 0 {
				t.Errorf("started %q copied %q, want %q", f.started, f.copied, want)
			}
		})
	}
}

// Over SSH the browser would open on the remote machine, out of sight.
func TestOpenOverSSHCopies(t *testing.T) {
	f := &fake{}
	copied, err := f.opener("linux", map[string]string{"SSH_CONNECTION": "1.2.3.4 5 6.7.8.9 22"}).Open(link)
	if err != nil || !copied {
		t.Fatalf("copied=%t err=%v", copied, err)
	}
	if len(f.started) != 0 || !reflect.DeepEqual(f.copied, []string{link}) {
		t.Errorf("started %q copied %q", f.started, f.copied)
	}
}

func TestOpenCopiesWhatIsNotAWebLink(t *testing.T) {
	for _, l := range []string{
		"file:///etc/passwd", "javascript:alert(1)", "vscode://open?x", "mailto:a@b.c",
		"-a Calculator", "relative/path", "https://", "",
	} {
		t.Run(l, func(t *testing.T) {
			f := &fake{}
			copied, err := f.opener("darwin", nil).Open(l)
			if err != nil || !copied || len(f.started) != 0 {
				t.Errorf("copied=%t err=%v started %q", copied, err, f.started)
			}
		})
	}
	for _, l := range []string{"http://example.com", "HTTPS://example.com/a?b#c"} {
		if !Openable(l) {
			t.Errorf("%q not openable", l)
		}
	}
}

func TestOpenCopiesWhenTheBrowserDoesNotStart(t *testing.T) {
	f := &fake{startErr: errors.New("xdg-open: not found")}
	copied, err := f.opener("linux", nil).Open(link)
	if err != nil || !copied || !reflect.DeepEqual(f.copied, []string{link}) {
		t.Fatalf("copied=%t err=%v copied %q", copied, err, f.copied)
	}
}

func TestOpenReportsWhenNeitherWorks(t *testing.T) {
	f := &fake{startErr: errors.New("xdg-open: not found"), copyErr: errors.New("no terminal")}
	copied, err := f.opener("linux", nil).Open(link)
	if copied || err == nil || !strings.Contains(err.Error(), "xdg-open") || !strings.Contains(err.Error(), "no terminal") {
		t.Fatalf("copied=%t err=%v", copied, err)
	}

	f = &fake{copyErr: errors.New("no terminal")}
	if copied, err := f.opener("linux", nil).Open("file:///x"); copied || err == nil {
		t.Fatalf("copied=%t err=%v", copied, err)
	}
}
