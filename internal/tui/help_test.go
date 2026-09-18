package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func openHelp(t *testing.T, m Model, width, height int) Model {
	t.Helper()
	next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return press(t, next.(Model), "?")
}

func (m Model) plainView() string { return ansi.Strip(m.View()) }

func TestHelpIsGroupedIntoSections(t *testing.T) {
	m := openHelp(t, newMouseModel(t), 100, 200)
	view := m.plainView()
	for _, title := range []string{"Move", "View", "Comment", "Select & copy", "GitHub", "Mouse", "General", "Recipes"} {
		if !strings.Contains(view, title) {
			t.Errorf("help has no %q section", title)
		}
	}
	if strings.Contains(strings.ToLower(view), "note") {
		t.Errorf("help says note; the word is draft:\n%s", view)
	}
}

func TestHelpShowsOnlyWhatApplies(t *testing.T) {
	local := openHelp(t, newMouseModel(t), 100, 200).plainView()
	if strings.Contains(local, "Follow-up") {
		t.Error("a local review's help lists follow-up keys")
	}
	followup := openHelp(t, followupModel(t), 100, 200).plainView()
	if !strings.Contains(followup, "Follow-up") {
		t.Error("a follow-up review's help lacks its follow-up keys")
	}

	m := newMouseModel(t)
	m.cfg.Mouse = false
	noMouse := openHelp(t, m, 100, 200).plainView()
	if strings.Contains(noMouse, "Mouse") || strings.Contains(noMouse, "drag") {
		t.Error("with the mouse off, help still describes it")
	}
}

func TestWideHelpFitsWithoutScrolling(t *testing.T) {
	wide := openHelp(t, followupModel(t), 160, 45)
	if strings.Contains(wide.plainView(), "more below") {
		t.Errorf("at 160 columns help should fit in two columns:\n%s", wide.plainView())
	}
	narrow := openHelp(t, newMouseModel(t), 90, 45)
	if !strings.Contains(narrow.plainView(), "more below") {
		t.Error("a single column taller than the screen gives no sign there is more")
	}
}

func TestHelpScrolls(t *testing.T) {
	m := openHelp(t, newMouseModel(t), 90, 20)
	first := m.plainView()
	m = press(t, m, "j", "j")
	if m.plainView() == first {
		t.Error("j did not scroll help")
	}
	m = mouse(t, m, wheel(tea.MouseButtonWheelUp), wheel(tea.MouseButtonWheelUp))
	if m.plainView() != first {
		t.Error("wheel up did not scroll help back to the top")
	}
	if m = press(t, m, "q"); m.mode != modeDiff {
		t.Errorf("q left help in mode %v", m.mode)
	}
}

func TestHelpFromThreadsOpensAtFollowUp(t *testing.T) {
	m := openHelp(t, followupModel(t), 90, 20) // the follow-up model starts in threads
	lines := strings.Split(m.plainView(), "\n")
	if !strings.Contains(strings.Join(lines[:3], "\n"), "Follow-up") {
		t.Errorf("help from the thread list does not open at Follow-up:\n%s", m.plainView())
	}
}

// diffKeys reads the keys handleKey's switch on key handles straight from the
// source, so a key added there without a help entry fails here.
func diffKeys(t *testing.T) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "model.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "handleKey" {
			return true
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			sw, ok := n.(*ast.SwitchStmt)
			if !ok {
				return true
			}
			if id, ok := sw.Tag.(*ast.Ident); !ok || id.Name != "key" {
				return true
			}
			for _, stmt := range sw.Body.List {
				for _, expr := range stmt.(*ast.CaseClause).List {
					if lit, ok := expr.(*ast.BasicLit); ok {
						k, _ := strconv.Unquote(lit.Value)
						keys = append(keys, k)
					}
				}
			}
			return false
		})
		return false
	})
	if len(keys) < 20 {
		t.Fatalf("found only %d keys in handleKey; has the switch moved?", len(keys))
	}
	return keys
}

func TestEveryDiffKeyIsInHelp(t *testing.T) {
	m := followupModel(t) // every section applies
	shown := map[string]bool{}
	for _, s := range m.helpSections() {
		for _, e := range s.entries {
			for _, k := range strings.FieldsFunc(e.keys, func(r rune) bool { return r == ' ' || r == '/' || r == ',' }) {
				shown[k] = true
			}
		}
	}
	arrows := map[string]string{"down": "↓", "up": "↑", "left": "←", "right": "→"}
	for _, k := range diffKeys(t) {
		if !shown[k] && !shown[arrows[k]] {
			t.Errorf("handleKey handles %q but help does not list it", k)
		}
	}
}

func TestTwoColumnHelpNeverOverflows(t *testing.T) {
	for width := 110; width <= 170; width += 5 {
		m := openHelp(t, followupModel(t), width, 60)
		for i, line := range strings.Split(m.View(), "\n") {
			if w := ansi.StringWidth(line); w > width {
				t.Errorf("width %d: help line %d is %d columns and would wrap: %q", width, i, w, ansi.Strip(line))
				break
			}
		}
	}
}
