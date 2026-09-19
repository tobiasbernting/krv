package tui

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tobiasbernting/krv/v2/internal/browser"
	"github.com/tobiasbernting/krv/v2/internal/config"
	"github.com/tobiasbernting/krv/v2/internal/diffparse"
	"github.com/tobiasbernting/krv/v2/internal/gitsrc"
	"github.com/tobiasbernting/krv/v2/internal/render"
)

// editorKind is how an editor is told which line to open at, whether krv
// waits for it, and what the status calls it.
type editorKind struct {
	form string // "plus": +N file; "goto": --goto file:N; "colon": file:N
	wait bool
	name string
}

// editors are the editors krv knows, by executable name. A terminal editor
// takes over the terminal, so krv waits for it; a GUI one opens its own
// window and krv stays up.
var editors = map[string]editorKind{
	"vi": {"plus", true, ""}, "vim": {"plus", true, ""}, "nvim": {"plus", true, ""},
	"nano": {"plus", true, ""}, "hx": {"plus", true, ""}, "kak": {"plus", true, ""},
	"micro": {"plus", true, ""}, "emacs": {"plus", true, ""},
	"code":          {"goto", false, "VS Code"},
	"code-insiders": {"goto", false, "VS Code Insiders"},
	"cursor":        {"goto", false, "Cursor"},
	"windsurf":      {"goto", false, "Windsurf"},
	"zed":           {"colon", false, "Zed"},
	"subl":          {"colon", false, "Sublime Text"},
}

// headFileMsg carries the file o fetched to open, or why it could not.
type headFileMsg struct {
	file string
	line int
	err  error
}

// launchedMsg reports an editor or browser krv started, by name, or that a
// link was copied instead of opened.
type launchedMsg struct {
	name   string
	copied bool
	err    error
}

// startProcess starts a program krv does not wait for. Tests replace it.
var startProcess = func(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

// openInEditor opens the file under the cursor in the open editor, at its
// line. The working tree's files are opened where they are; any other
// revision's are copied out first, so the editor never sees the checkout
// pretending to be the pull request.
func (m Model) openInEditor() (tea.Model, tea.Cmd) {
	path, line, ok := m.openTarget()
	if !ok {
		m.err = "nothing to open here"
		return m, nil
	}
	src := m.src
	if src.Kind == SourceLocal && src.Rev == "" {
		if src.Root == "" {
			m.err = "nothing to open here"
			return m, nil
		}
		return m, m.launchEditor(filepath.Join(src.Root, filepath.FromSlash(path)), line)
	}
	m.status = "fetching " + path + "…"
	return m, func() tea.Msg {
		file, err := src.headFile(path)
		return headFileMsg{file: file, line: line, err: err}
	}
}

func (m Model) applyHeadFile(msg headFileMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.status = ""
		m.err = "could not open: " + msg.err.Error()
		return m, nil
	}
	return m, m.launchEditor(msg.file, msg.line)
}

// headFile is a read-only copy of path as the review's head has it.
func (s Source) headFile(path string) (string, error) {
	if s.Kind == SourcePR {
		return headCopy(os.TempDir(), s.Repo, s.HeadSHA, path, func() ([]byte, error) {
			return s.Client.FileAt(s.Repo, s.HeadSHA, path)
		})
	}
	// A local repository has no owner/name; its directory stands in.
	repo := "local/" + filepath.Base(s.Root)
	return headCopy(os.TempDir(), repo, s.Rev, path, func() ([]byte, error) {
		return (&gitsrc.Repo{Root: s.Root}).Show(s.Rev, path)
	})
}

// launchEditor starts the open editor on file at line: in the terminal,
// suspending krv until it exits, or beside it, as a GUI editor opens.
func (m Model) launchEditor(file string, line int) tea.Cmd {
	args, wait, name := editorCall(m.cfg, file, line)
	cmd := exec.Command(args[0], args[1:]...)
	if wait {
		return tea.ExecProcess(cmd, func(err error) tea.Msg { return launchedMsg{err: err} })
	}
	return func() tea.Msg { return launchedMsg{name: name, err: startProcess(cmd)} }
}

func (m Model) applyLaunched(msg launchedMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.err != nil:
		m.status = ""
		m.err = "could not open: " + msg.err.Error()
	case msg.copied:
		m.status = "copied link"
	case msg.name != "":
		m.status = "opened in " + msg.name
	}
	return m, nil
}

// openPullRequest opens the pull request's Files tab at the cursor's line.
func (m Model) openPullRequest() (tea.Model, tea.Cmd) {
	if m.src.Kind != SourcePR || m.src.URL == "" {
		m.err = "no pull request to open"
		return m, nil
	}
	path, line, ok := m.openTarget()
	if !ok {
		return m, browse(m.src.URL)
	}
	return m, browse(filesURL(m.src.URL, path, line))
}

// browse opens url in the system's browser, or copies it (see package
// browser).
func browse(url string) tea.Cmd {
	o := browser.New()
	o.Start = func(name string, args ...string) error { return startProcess(exec.Command(name, args...)) }
	return func() tea.Msg {
		copied, err := o.Open(url)
		if copied {
			return launchedMsg{copied: true}
		}
		return launchedMsg{name: "browser", err: err}
	}
}

// openTarget is the file and head-side line o and O open: the cursor's line,
// or for a deleted line the nearest one that still exists.
func (m Model) openTarget() (path string, line int, ok bool) {
	if m.cursor >= len(m.doc.Rows) {
		return "", 0, false
	}
	row := m.doc.Rows[m.cursor]
	if row.FileIdx >= len(m.files) {
		return "", 0, false
	}
	file := m.files[row.FileIdx]
	path = file.Path()
	switch {
	case row.IsCode() && row.NewNum() > 0:
		return path, row.NewNum(), true
	case row.IsCode():
		return path, nearestNewLine(file.Hunks()[row.HunkIdx], row.Line.OldNum), true
	case row.Kind == render.RowNote && row.Ann != nil && row.Ann.Line > 0:
		return path, row.Ann.Line, true
	case row.Kind == render.RowHunk:
		return path, max(1, file.Hunks()[row.HunkIdx].NewStart), true
	}
	return path, 1, true
}

// nearestNewLine is the new-side line closest to the deleted line old,
// preferring the one after it: that is where the deleted text used to be.
func nearestNewLine(h diffparse.Hunk, old int) int {
	at := -1
	for i, ln := range h.Lines {
		if ln.Kind == diffparse.KindDel && ln.OldNum == old {
			at = i
		}
	}
	for d := 1; at >= 0 && d < len(h.Lines); d++ {
		for _, j := range []int{at + d, at - d} {
			if j >= 0 && j < len(h.Lines) && h.Lines[j].Kind != diffparse.KindDel {
				return h.Lines[j].NewNum
			}
		}
	}
	return max(1, h.NewStart)
}

// filesURL is a pull request's Files tab at a line of a file, anchored the
// way GitHub anchors it: by the SHA-256 of the path.
func filesURL(prURL, path string, line int) string {
	sum := sha256.Sum256([]byte(path))
	return fmt.Sprintf("%s/files#diff-%sR%d", strings.TrimSuffix(prURL, "/"), hex.EncodeToString(sum[:]), line)
}

// headCopy writes the head version of path, read on demand, to
// <tmp>/krv/<repo>/<sha>/<path> and returns where. It keeps the real path so
// an editor detects the language, and is read-only, since editing it would
// change nothing. A copy already there is reused: the SHA pins its content.
func headCopy(tmp, repo, sha, path string, read func() ([]byte, error)) (string, error) {
	if !filepath.IsLocal(filepath.FromSlash(path)) || !filepath.IsLocal(filepath.FromSlash(repo)) || !filepath.IsLocal(sha) {
		return "", fmt.Errorf("cannot open %s: not a path inside the repository", path)
	}
	dest := filepath.Join(tmp, "krv", filepath.FromSlash(repo), sha, filepath.FromSlash(path))
	if _, err := os.Stat(dest); err == nil {
		return dest, nil
	}
	data, err := read()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	// Written aside and renamed into place, so a half-written copy is never
	// taken for a finished one.
	f, err := os.CreateTemp(filepath.Dir(dest), ".krv-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(f.Name(), 0o444)
	}
	if err == nil {
		err = os.Rename(f.Name(), dest)
	}
	if err != nil {
		return "", err
	}
	return dest, nil
}

// editorCall is the command that opens file at line in the open editor,
// whether to wait for it, and the editor's name.
func editorCall(cfg config.Config, file string, line int) (args []string, wait bool, name string) {
	n := strconv.Itoa(line)
	if tmpl := strings.Fields(cfg.OpenEditorCmd); len(tmpl) > 0 {
		fill := strings.NewReplacer("{file}", file, "{line}", n)
		for _, f := range tmpl {
			args = append(args, fill.Replace(f))
		}
		wait, name = true, filepath.Base(args[0])
	} else {
		args = strings.Fields(cfg.OpenEditorCommand())
		exe := filepath.Base(args[0])
		kind, known := editors[exe]
		if !known {
			kind = editorKind{form: "plus", wait: true}
		}
		switch kind.form {
		case "goto":
			args = append(args, "--goto", file+":"+n)
		case "colon":
			args = append(args, file+":"+n)
		default:
			args = append(args, "+"+n, file)
		}
		wait, name = kind.wait, kind.name
		if name == "" {
			name = exe
		}
	}
	if cfg.OpenEditorWait != nil {
		wait = *cfg.OpenEditorWait
	}
	return args, wait, name
}
