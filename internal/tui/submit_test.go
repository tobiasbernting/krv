package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tobiasbernting/krv/v2/internal/ghsrc"
	"github.com/tobiasbernting/krv/v2/internal/notes"
)

func submitTestModel(t *testing.T, remoteHead string) (Model, string) {
	t.Helper()
	dir := t.TempDir()
	payloadPath := filepath.Join(dir, "payload.json")
	t.Setenv("TUI_SUBMIT_TEST_HEAD", remoteHead)
	t.Setenv("TUI_SUBMIT_TEST_PAYLOAD", payloadPath)
	script := `#!/bin/sh
case "$1 $2" in
"api graphql") printf '{"data":{"repository":{"pullRequest":{"headRefOid":"%s"}}}}\n' "$TUI_SUBMIT_TEST_HEAD" ;;
api*) cat > "$TUI_SUBMIT_TEST_PAYLOAD"; printf '{}\n' ;;
*) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	review, err := notes.LoadAt(filepath.Join(dir, "review.json"), "scope")
	if err != nil {
		t.Fatal(err)
	}
	m := newReviewModel(t, func(o *Options) {
		o.Review = review
		o.Source = Source{Kind: SourcePR, Repo: "acme/x", PRNumber: 1, HeadSHA: "reviewed-head", Author: "ann", Viewer: "bo"}
	})
	return m, payloadPath
}

func TestCleanApprovalSubmitsPinnedReview(t *testing.T) {
	m, payloadPath := submitTestModel(t, "reviewed-head")
	m = press(t, m, "S")
	m = press(t, m, "a")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if cmd == nil || !m.requests.has(reqSubmit) {
		t.Fatalf("clean approval did not start: %s", m.err)
	}
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.err != "" || !strings.Contains(m.status, "submitted") {
		t.Fatalf("clean approval failed: err=%q status=%q", m.err, m.status)
	}
	data, err := os.ReadFile(payloadPath)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		CommitID string                `json:"commit_id"`
		Event    string                `json:"event"`
		Comments []ghsrc.ReviewComment `json:"comments"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.CommitID != "reviewed-head" || payload.Event != ghsrc.EventApprove || len(payload.Comments) != 0 {
		t.Fatalf("unexpected payload: %s", data)
	}
}

func TestEmptyCommentAndRequestChangesAreRejectedLocally(t *testing.T) {
	for _, eventKey := range []string{"c", "r"} {
		t.Run(eventKey, func(t *testing.T) {
			m, payloadPath := submitTestModel(t, "reviewed-head")
			m = press(t, m, "S")
			m = press(t, m, eventKey)
			next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			m = next.(Model)
			if cmd != nil || m.requests.has(reqSubmit) || m.err == "" {
				t.Fatalf("empty review was not refused locally: %+v", m.submit)
			}
			if _, err := os.Stat(payloadPath); !os.IsNotExist(err) {
				t.Error("empty review reached gh")
			}
		})
	}
}

func TestMovedHeadPreservesDraftNotesOnDisk(t *testing.T) {
	m, payloadPath := submitTestModel(t, "new-head")
	note := m.review.Add("svc.go", 0, 3, "bbbbbbb", "check this")
	if err := m.review.Save(); err != nil {
		t.Fatal(err)
	}
	m = press(t, m, "S")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if cmd == nil {
		t.Fatalf("submission did not start: %s", m.err)
	}
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.mode != modeSubmit || !strings.Contains(m.err, "changed since") {
		t.Fatalf("head movement was not surfaced: mode=%v err=%q", m.mode, m.err)
	}
	if len(m.review.Notes) != 1 || m.review.Notes[0].ID != note.ID {
		t.Fatal("failed submission discarded local notes")
	}
	loaded, err := notes.LoadAt(filepath.Join(filepath.Dir(payloadPath), "review.json"), "scope")
	if err != nil || len(loaded.Notes) != 1 || loaded.Notes[0].ID != note.ID {
		t.Fatalf("original note not retained for reload: review=%+v err=%v", loaded, err)
	}
	if _, err := os.Stat(payloadPath); !os.IsNotExist(err) {
		t.Fatal("moved head review was posted")
	}
}

func TestSubmitReturnsToOriginatingView(t *testing.T) {
	for _, complete := range []bool{false, true} {
		m, _ := submitTestModel(t, "reviewed-head")
		m.mode = modeFiles
		next, _ := m.openSubmit()
		m = next.(Model)
		if complete {
			next, _ = m.applySubmitResult(submitResultMsg{event: ghsrc.EventApprove})
		} else {
			next, _ = m.handleSubmitKey(tea.KeyMsg{Type: tea.KeyEsc})
		}
		if got := next.(Model).mode; got != modeFiles {
			t.Errorf("complete=%v: returned to mode %v, want files", complete, got)
		}
	}
}
