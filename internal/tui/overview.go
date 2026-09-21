package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
	"github.com/tobiasbernting/krv/v2/internal/ghsrc"
	"github.com/tobiasbernting/krv/v2/internal/render"
)

// The Overview is why the pull request exists and what CI says about it: its
// header, its Checks and its Description, as one scrolling page in the
// Markdown reader. It follows the snapshot — what is shown is what the last
// sync fetched — and never opens by itself.

// checkGlyphs say a Check's outcome without colour, so it survives NO_COLOR.
var checkGlyphs = map[ghsrc.CheckStatus]string{
	ghsrc.CheckPass:    "✓",
	ghsrc.CheckFail:    "✗",
	ghsrc.CheckRunning: "●",
	ghsrc.CheckSkipped: "−",
	ghsrc.CheckStopped: "!",
}

// openOverview shows the pull request's Overview.
func (m Model) openOverview() (tea.Model, tea.Cmd) {
	if m.src.Kind != SourcePR {
		m.status = "no pull request"
		return m, nil
	}
	if m.pr == nil {
		m.err = "pull request details unavailable; press r to sync"
		return m, nil
	}
	m.mode = modeOverview
	m.reader = readerState{focus: -1}
	return m, nil
}

// hasDescription reports whether there is a Description to read, which is
// when the status line bothers hinting at the Overview.
func (m Model) hasDescription() bool {
	return m.pr != nil && strings.TrimSpace(m.pr.Body) != ""
}

func (m Model) overviewPage() page {
	pr := m.pr
	if pr == nil {
		return page{}
	}
	w := m.readerWidth()
	t := m.theme
	plain := func(fg string) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(fg)).Background(lipgloss.Color(t.Bg))
	}
	heading := func(title, detail string) string {
		line := plain(t.Accent).Bold(true).Render(clipText(title, w))
		if detail != "" {
			line += plain(t.Dim).Render(clipText("  "+detail, w-runewidth.StringWidth(title)))
		}
		return line
	}

	var p page

	// Header: what this pull request is, and how old what we know of it is.
	p.section()
	for _, line := range render.WrapText(plainText(pr.Title), w) {
		p.text(plain(t.Fg).Bold(true).Render(line))
	}
	meta := []string{fmt.Sprintf("#%d", pr.Number)}
	if pr.Author.Login != "" {
		meta = append(meta, pr.Author.Login)
	}
	if pr.BaseRef != "" && pr.HeadRef != "" {
		meta = append(meta, pr.BaseRef+" ← "+pr.HeadRef)
	}
	meta = append(meta, prState(*pr))
	if synced := age(m.sync.syncedAt); synced != "" {
		meta = append(meta, "synced "+synced)
	}
	for _, line := range render.WrapText(strings.Join(meta, " · "), w) {
		p.text(plain(t.Dim).Render(line))
	}
	p.text("")

	// Checks.
	p.section()
	detail := ""
	if !pr.RequiredKnown && len(pr.Checks) > 0 {
		// Mirrors a thread's [resolution unavailable]: the state is missing,
		// not false.
		detail = "[required status unavailable]"
	}
	p.text(heading("Checks", detail))
	if len(pr.Checks) == 0 {
		p.text(plain(t.Dim).Italic(true).Render("No checks."))
	} else {
		if summary := ghsrc.SummarizeChecks(pr.Checks).String(); summary != "" {
			p.text(plain(t.Dim).Render(clipText(summary, w)))
		}
		m.addChecks(&p, pr, w)
	}
	p.text("")

	// Description.
	p.section()
	p.text(heading("Description", ""))
	if strings.TrimSpace(pr.Body) == "" {
		p.text(plain(t.Dim).Italic(true).Render("No description."))
	} else {
		p.markdown(render.Markdown(pr.Body, w, m.markdownOptions()))
	}
	return p
}

// addChecks adds one row per Check, each linking to where it reports, so the
// link cursor reaches them.
func (m Model) addChecks(p *page, pr *ghsrc.PR, width int) {
	// One column for the names, so the durations line up and a long name is
	// what gives way.
	required := false
	nameWidth := 0
	for _, c := range pr.Checks {
		nameWidth = max(nameWidth, runewidth.StringWidth(checkName(c)))
		required = required || (c.Required && pr.RequiredKnown)
	}
	tail := 2 + 8 // two spaces and the duration column
	if required {
		tail += 2 + len("required")
	}
	nameWidth = max(1, min(nameWidth, width-4-tail))

	rows := make([]string, len(pr.Checks))
	var links []pageLink
	owner := map[int]int{} // link index to its row
	for i, c := range pr.Checks {
		rows[i] = m.checkRow(c, pr.RequiredKnown, nameWidth, false)
		if c.URL == "" {
			continue
		}
		owner[len(links)] = i
		links = append(links, pageLink{url: c.URL, spans: []render.LinkSpan{{
			Line: i, Start: checkNameCol, End: checkNameCol + runewidth.StringWidth(clipText(checkName(c), nameWidth)),
		}}})
	}
	p.block(rows, links, func(link int) []string {
		focused := append([]string(nil), rows...)
		if row, ok := owner[link]; ok {
			focused[row] = m.checkRow(pr.Checks[row], pr.RequiredKnown, nameWidth, true)
		}
		return focused
	})
}

// checkNameCol is the column a Check's name starts in: the focus marker, its
// glyph and a space come before it.
const checkNameCol = 3

// checkRow draws one Check: its glyph, its name and workflow, how long it
// took and whether branch protection requires it. Focused, it is led by the
// reader's marker and its name is in reverse video.
func (m Model) checkRow(c ghsrc.Check, requiredKnown bool, nameWidth int, focus bool) string {
	t := m.theme
	st := func(fg string) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(fg)).Background(lipgloss.Color(t.Bg))
	}
	var b strings.Builder
	if focus {
		b.WriteString(st(t.Accent).Bold(true).Render(render.FocusMark))
	} else {
		b.WriteString(st(t.Fg).Render(" "))
	}
	b.WriteString(st(m.checkFg(c.Status)).Render(checkGlyphs[c.Status]))
	b.WriteString(st(t.Fg).Render(" "))

	name := runewidth.FillRight(clipText(checkName(c), nameWidth), nameWidth)
	nameStyle := st(t.Fg)
	if focus {
		nameStyle = nameStyle.Reverse(true)
	}
	link := nameStyle.Render(name)
	if c.URL != "" {
		link = ansi.SetHyperlink(c.URL) + link + ansi.ResetHyperlink()
	}
	b.WriteString(link)

	b.WriteString(st(t.Dim).Render(fmt.Sprintf("  %-8s", checkDuration(c, m.snapshotTime()))))
	if requiredKnown && c.Required {
		b.WriteString(st(t.Dim).Render("  required"))
	}
	return b.String()
}

// checkFg is the colour of a Check's glyph; the glyph itself already says
// which outcome it is.
func (m Model) checkFg(s ghsrc.CheckStatus) string {
	switch s {
	case ghsrc.CheckPass:
		return m.theme.AddSign
	case ghsrc.CheckFail:
		return m.theme.DelSign
	case ghsrc.CheckRunning:
		return m.theme.Accent
	case ghsrc.CheckStopped:
		return m.theme.ChangedFg
	}
	return m.theme.Dim
}

// checkName is a Check with the workflow it belongs to, when that is known.
func checkName(c ghsrc.Check) string {
	if c.Workflow == "" {
		return plainText(c.Name)
	}
	return plainText(c.Name) + " (" + plainText(c.Workflow) + ")"
}

// checkDuration is how long a Check took, or has been running as of the
// snapshot, in the coarsest units that still say something.
func checkDuration(c ghsrc.Check, now time.Time) string {
	d := c.Duration(now).Round(time.Second)
	switch {
	case d <= 0:
		return ""
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
}

// snapshotTime is the clock a running Check's elapsed time is measured
// against: when the snapshot was fetched, so the page does not claim to know
// more than the last sync did.
func (m Model) snapshotTime() time.Time {
	if m.sync.syncedAt.IsZero() {
		return m.now()
	}
	return m.sync.syncedAt
}

// prState is the pull request's state as the header says it: open, closed or
// merged, and whether it is still a draft.
func prState(pr ghsrc.PR) string {
	state := strings.ToLower(pr.State)
	if state == "" {
		state = "open"
	}
	if pr.IsDraft {
		state += " · draft"
	}
	return state
}

// clipText cuts plain text to width, ending in … when anything was cut.
func clipText(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return runewidth.Truncate(s, width, "…")
}

// plainText drops the control characters from someone else's text: a title,
// a check name or a line of code must not carry an escape sequence into the
// terminal.
func plainText(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t':
			return ' '
		case r < 0x20, r == 0x7f:
			return -1
		}
		return r
	}, s)
}
