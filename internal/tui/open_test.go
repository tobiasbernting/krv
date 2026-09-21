package tui

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tobiasbernting/krv/v2/internal/config"
	"github.com/tobiasbernting/krv/v2/internal/diffparse"
	"github.com/tobiasbernting/krv/v2/internal/render"
)

func TestEditorCallByEditor(t *testing.T) {
	for _, tc := range []struct {
		editor string
		args   []string
		wait   bool
		name   string
	}{
		{"vi", []string{"vi", "+12", "a.go"}, true, "vi"},
		{"nvim", []string{"nvim", "+12", "a.go"}, true, "nvim"},
		{"/opt/homebrew/bin/hx", []string{"/opt/homebrew/bin/hx", "+12", "a.go"}, true, "hx"},
		{"emacs -nw", []string{"emacs", "-nw", "+12", "a.go"}, true, "emacs"},
		{"code", []string{"code", "--goto", "a.go:12"}, false, "VS Code"},
		{"code -r", []string{"code", "-r", "--goto", "a.go:12"}, false, "VS Code"},
		{"code-insiders", []string{"code-insiders", "--goto", "a.go:12"}, false, "VS Code Insiders"},
		{"cursor", []string{"cursor", "--goto", "a.go:12"}, false, "Cursor"},
		{"windsurf", []string{"windsurf", "--goto", "a.go:12"}, false, "Windsurf"},
		{"zed", []string{"zed", "a.go:12"}, false, "Zed"},
		{"subl", []string{"subl", "a.go:12"}, false, "Sublime Text"},
		{"ed", []string{"ed", "+12", "a.go"}, true, "ed"},
	} {
		t.Run(tc.editor, func(t *testing.T) {
			args, wait, name := editorCall(config.Config{OpenEditor: tc.editor}, "a.go", 12)
			if !reflect.DeepEqual(args, tc.args) || wait != tc.wait || name != tc.name {
				t.Errorf("got %q wait=%t %q, want %q wait=%t %q", args, wait, name, tc.args, tc.wait, tc.name)
			}
		})
	}
}

// fakeStart records the programs krv starts instead of starting them.
func fakeStart(t *testing.T) *[][]string {
	t.Helper()
	// Over SSH a link is copied, not opened; these tests are about opening.
	t.Setenv("SSH_CONNECTION", "")
	var started [][]string
	orig := startProcess
	startProcess = func(cmd *exec.Cmd) error {
		started = append(started, cmd.Args)
		return nil
	}
	t.Cleanup(func() { startProcess = orig })
	return &started
}

func TestOpenInVSCodeKeepsTheReviewUp(t *testing.T) {
	started := fakeStart(t)
	m, _ := newYankModel(t, render.ModeUnified)
	m.src.Root = "/repo"
	m.cfg.OpenEditor = "code"
	m = yank(t, m.cursorOnLine(t, 2, 0), "o")
	want := []string{"code", "--goto", filepath.Join("/repo", "a.go") + ":2"}
	if len(*started) != 1 || !reflect.DeepEqual((*started)[0], want) {
		t.Fatalf("started %q, want %q", *started, want)
	}
	if !strings.Contains(m.statusBar(), "opened in VS Code") {
		t.Errorf("no status:\n%s", m.statusBar())
	}
}

func TestOpenPullRequestAtTheLine(t *testing.T) {
	started := fakeStart(t)
	m, _ := newYankModel(t, render.ModeUnified)
	m.src = Source{Kind: SourcePR, Repo: "acme/x", PRNumber: 7, URL: "https://github.com/acme/x/pull/7"}
	yank(t, m.cursorOnLine(t, 2, 0), "O")
	want := filesURL("https://github.com/acme/x/pull/7", "a.go", 2)
	if len(*started) != 1 || (*started)[0][len((*started)[0])-1] != want {
		t.Errorf("started %q, want the Files tab at %s", *started, want)
	}
}

func TestCopiedLinkIsReported(t *testing.T) {
	m, _ := newYankModel(t, render.ModeUnified)
	next, _ := m.Update(launchedMsg{copied: true})
	if bar := next.(Model).statusBar(); !strings.Contains(bar, "copied link") {
		t.Errorf("no status:\n%s", bar)
	}
}

func TestOpenPullRequestInALocalReview(t *testing.T) {
	started := fakeStart(t)
	m, _ := newYankModel(t, render.ModeUnified)
	m = yank(t, m.cursorOnLine(t, 2, 0), "O")
	if len(*started) != 0 || !strings.Contains(m.statusBar(), "no pull request to open") {
		t.Errorf("started %q; status:\n%s", *started, m.statusBar())
	}
}

func TestQueueOpensThePullRequest(t *testing.T) {
	started := fakeStart(t)
	q := newQueue(t)
	q.items[1].URL = "https://github.com/acme/y/pull/4"
	q = pressQ(t, q, "j", "o")
	if len(*started) != 0 {
		t.Errorf("o in the queue started %q", *started)
	}
	_, cmd := q.Update(keyMsg("O"))
	if cmd == nil {
		t.Fatal("O in the queue did nothing")
	}
	cmd()
	if len(*started) != 1 || (*started)[0][len((*started)[0])-1] != "https://github.com/acme/y/pull/4" {
		t.Errorf("started %q", *started)
	}
}

func TestOpenTargetIsTheNewSideLine(t *testing.T) {
	m, _ := newYankModel(t, render.ModeUnified)
	for _, tc := range []struct {
		what string
		m    Model
		line int
	}{
		{"an added line", m.cursorOnLine(t, 2, 0), 2},
		{"a deleted line", m.cursorOnLine(t, 0, 2), 2},
		{"a hunk header", m.cursorOnRow(t, render.RowHunk, 1), 10},
		{"a file header", m.cursorOnRow(t, render.RowFile, 0), 1},
	} {
		path, line, ok := tc.m.openTarget()
		if !ok || path != "a.go" || line != tc.line {
			t.Errorf("on %s: %s:%d ok=%t, want a.go:%d", tc.what, path, line, ok, tc.line)
		}
	}

	split, _ := newYankModel(t, render.ModeSplit)
	if _, line, _ := split.cursorOnLine(t, 0, 2).openTarget(); line != 2 {
		t.Errorf("on a deleted line in split: line %d, want 2", line)
	}
}

func TestOpenTargetAfterADeletionAtTheEnd(t *testing.T) {
	m := New(Options{
		Files: diffparse.Parse(deletionDiff), Theme: render.DefaultTheme(),
		Config: config.Defaults(), Source: Source{Kind: SourceLocal, Title: "test"},
		Review: newTestReview(t),
	})
	_, line, ok := m.cursorOnLine(t, 0, 3).openTarget()
	if !ok || line != 2 {
		t.Errorf("a deletion with no new lines left opens at %d, want 2 (the hunk's new start)", line)
	}
}

func TestFilesURLAnchorsThePathAndLine(t *testing.T) {
	got := filesURL("https://github.com/acme/x/pull/7", "internal/a.go", 12)
	sum := sha256.Sum256([]byte("internal/a.go"))
	want := "https://github.com/acme/x/pull/7/files#diff-" + hex.EncodeToString(sum[:]) + "R12"
	if got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
}

func TestHeadCopyIsReadOnlyAndKeyedBySHA(t *testing.T) {
	tmp := t.TempDir()
	reads := 0
	read := func() ([]byte, error) { reads++; return []byte("package a\n"), nil }

	path, err := headCopy(tmp, "acme/x", "abc123", "internal/a.go", read)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(tmp, "krv", "acme", "x", "abc123", "internal", "a.go"); path != want {
		t.Errorf("copied to %s, want %s", path, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o444 {
		t.Errorf("mode %v, want read-only", info.Mode().Perm())
	}
	if data, _ := os.ReadFile(path); string(data) != "package a\n" {
		t.Errorf("copy holds %q", data)
	}

	if again, err := headCopy(tmp, "acme/x", "abc123", "internal/a.go", read); err != nil || again != path || reads != 1 {
		t.Errorf("a second open fetched again (%d reads) or moved: %s %v", reads, again, err)
	}
	if _, err := headCopy(tmp, "acme/x", "abc123", "../escape.go", read); err == nil {
		t.Error("a path out of the copy's directory was written")
	}
}

func TestEditorCallTemplate(t *testing.T) {
	cfg := config.Config{OpenEditor: "code", OpenEditorCmd: "myedit --line {line} {file}"}
	args, wait, name := editorCall(cfg, "a.go", 12)
	if want := []string{"myedit", "--line", "12", "a.go"}; !reflect.DeepEqual(args, want) {
		t.Errorf("template gave %q, want %q", args, want)
	}
	if !wait || name != "myedit" {
		t.Errorf("an unknown template command should be waited for: wait=%t %q", wait, name)
	}

	no := false
	cfg.OpenEditorWait = &no
	if _, wait, _ := editorCall(cfg, "a.go", 12); wait {
		t.Error("open_editor_wait = false was ignored")
	}
	cfg = config.Config{OpenEditor: "vim", OpenEditorWait: &no}
	if _, wait, _ := editorCall(cfg, "a.go", 12); wait {
		t.Error("open_editor_wait = false did not override the table")
	}
}
