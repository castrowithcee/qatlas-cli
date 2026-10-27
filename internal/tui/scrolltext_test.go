package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// longText is n numbered words, so a view shows which of them it reaches.
func longText(n int) string {
	words := make([]string, n)
	for i := range words {
		words[i] = fmt.Sprintf("word%03d", i)
	}
	return strings.Join(words, " ")
}

func hasPosition(view string) bool {
	return strings.Contains(view, "lines ") && strings.Contains(view, " of ")
}

// The detail screen of a log entry scrolls a result longer than the screen instead of cutting it.
func TestLogDetailScrollsALongResult(t *testing.T) {
	f := newLogFixture(t)
	f.append("cli", "", "op.short", "wiki", "short result")
	f.append("cli", "", "op.long", "wiki", longText(400))
	m := f.model(true, f.localToday())
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})

	open := func(op string) {
		m.screen = screenLogs
		m.logs.focus = logFocusRows
		for i, row := range m.logs.rows {
			if row.line.Entry.Operation == op {
				m.logs.detail = i
			}
		}
		m.logs.view.reset()
		m.screen = screenLogDetail
	}
	open("op.short")
	if view := screenOf(m); hasPosition(view) || strings.Contains(view, "scroll") {
		t.Fatalf("a short entry shows a position:\n%s", view)
	}
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 18})
	open("op.long")
	view := screenOf(m)
	assertViewFits(t, view, 80, 18)
	if !hasPosition(view) || strings.Contains(view, "response") || !strings.Contains(view, "up/down scroll") {
		t.Fatalf("a long entry does not offer to scroll:\n%s", view)
	}
	press(t, m, "end")
	view = screenOf(m)
	assertViewFits(t, view, 80, 18)
	if !strings.Contains(view, "response") {
		t.Fatalf("end does not reach the last line:\n%s", view)
	}
	press(t, m, "home")
	press(t, m, "down")
	if m.logs.view.offset() != 1 {
		t.Errorf("down moved to %d, want 1", m.logs.view.offset())
	}
	wheelAt(t, m, 20, 10, tea.MouseWheelDown)
	if m.logs.view.offset() != 4 {
		t.Errorf("a notch of the wheel moved to %d, want 4", m.logs.view.offset())
	}
}

// longRawModel is a table model whose connection has many targets, so its raw tab outgrows the pane.
func longRawModel(t *testing.T) *Model {
	t.Helper()
	m := newTableModel(t, 140)
	conn := m.cfg.Connections["tracker-issues"]
	conn.Targets = nil
	conn.TargetLabels = map[string]string{}
	for i := range 60 {
		conn.Targets = append(conn.Targets, fmt.Sprintf("octo/repo%02d", i))
	}
	conn.Description = longText(300)
	m.cfg.Connections["tracker-issues"] = conn
	openSectionByName(t, m, sectionConnections)
	m.list.selectName("tracker-issues")
	return m
}

// The raw tab and the details tab of the detail pane reach their last line with the page keys, show the
// position only where they do not fit, and scroll under the wheel.
func TestDetailPaneScrollsLongTabs(t *testing.T) {
	short := newTableModel(t, 140)
	openSectionByName(t, short, sectionServices)
	short.list.selectName("wiki-main")
	if view := showRaw(t, short); hasPosition(view) {
		t.Fatalf("a short raw tab shows a position:\n%s", view)
	}

	m := longRawModel(t)
	view := showRaw(t, m)
	if !hasPosition(view) || strings.Contains(view, "octo/repo59") {
		t.Fatalf("a long raw tab does not offer to scroll:\n%s", view)
	}
	press(t, m, "end")
	if view = screenOf(m); !strings.Contains(view, "octo/repo59") {
		t.Fatalf("end does not reach the last line of the raw tab:\n%s", view)
	}
	press(t, m, "home")
	if m.detailPane.view.offset() != 0 {
		t.Fatalf("home left offset %d", m.detailPane.view.offset())
	}
	before := m.list.cursor
	press(t, m, "pgdown")
	if m.detailPane.view.offset() == 0 || m.list.cursor != before {
		t.Errorf("pgdown: pane offset %d, row %d -> %d", m.detailPane.view.offset(), before, m.list.cursor)
	}
	press(t, m, "home")
	// The wheel over the pane scrolls it and leaves the rows alone.
	x, y := m.detailPane.x0+5, m.detailPane.y0+5
	wheelAt(t, m, x, y, tea.MouseWheelDown)
	if m.detailPane.view.offset() != wheelText || m.list.cursor != before {
		t.Errorf("the wheel moved the pane to %d and the row from %d to %d", m.detailPane.view.offset(), before, m.list.cursor)
	}
	// Another tab starts at its first line.
	press(t, m, "left", "left")
	if view = screenOf(m); m.detailPane.view.offset() != 0 {
		t.Errorf("the overview tab starts at offset %d", m.detailPane.view.offset())
	}
	press(t, m, "end")
	if view = screenOf(m); !strings.Contains(view, "word031") {
		t.Errorf("end does not reach the last line of the overview:\n%s", view)
	}
}

// The raw tab keeps its secrets masked while it scrolls.
func TestDetailRawScrollKeepsSecretsMasked(t *testing.T) {
	m := rawModel(t)
	conn := m.cfg.Connections["tracker-issues"]
	for i := range 60 {
		conn.Targets = append(conn.Targets, fmt.Sprintf("octo/more%02d", i))
	}
	m.cfg.Connections["tracker-issues"] = conn
	openSectionByName(t, m, sectionServices)
	m.list.selectName("wiki-main")
	for _, name := range []string{"wiki-main", "token-host"} {
		m.list.selectName(name)
		showRaw(t, m)
		for _, key := range []string{"end", "home", "pgdown"} {
			press(t, m, key)
			view := screenOf(m)
			for _, c := range []string{canaryURLPass, canaryQuery, canaryOption, canaryUser} {
				if strings.Contains(view, c) {
					t.Errorf("%s after %s shows %q", name, key, c)
				}
			}
		}
		m.leaveDetail()
	}
}

// The info overlay scrolls an explanation longer than itself, with the position and the keys named only then.
func TestInfoOverlayScrolls(t *testing.T) {
	m := connectionForm(t, 100)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m.infoOpen, m.infoShown = true, infoSubject{title: "Long", long: "short text"}
	m.info.reset()
	if view := m.render(); hasPosition(view) || strings.Contains(view, "scroll") {
		t.Fatalf("a short explanation shows a position:\n%s", view)
	}
	m.infoShown = infoSubject{title: "Long", long: longText(300)}
	view := m.render()
	if !hasPosition(view) || !strings.Contains(view, "up/down scroll") || strings.Contains(view, "word299") {
		t.Fatalf("a long explanation does not offer to scroll:\n%s", view)
	}
	press(t, m, "end")
	if view = m.render(); !strings.Contains(view, "word299") {
		t.Fatalf("end does not reach the last line:\n%s", view)
	}
	press(t, m, "home")
	wheelAt(t, m, 20, 10, tea.MouseWheelDown)
	if !m.infoOpen || m.info.offset() != wheelText {
		t.Errorf("the wheel: overlay %v offset %d, want open at %d", m.infoOpen, m.info.offset(), wheelText)
	}
	press(t, m, "esc")
	if m.infoOpen {
		t.Error("esc did not close the overlay")
	}
}

// The explanation pane beside a form scrolls under the wheel when its text is longer than the pane.
func TestInfoPaneScrollsUnderTheWheel(t *testing.T) {
	m := connectionForm(t, 140)
	m.Update(tea.WindowSizeMsg{Width: 140, Height: minimumHeight})
	focusField(t, m, "credential")
	m.render()
	m.cfg.Services = map[string]config.Service{}
	pane := &m.infoPane
	if !pane.more {
		t.Skip("the explanation fits the pane at this size")
	}
	wheelAt(t, m, pane.x0+3, pane.y0+3, tea.MouseWheelDown)
	if pane.view.offset() != wheelText {
		t.Errorf("the wheel moved the pane to %d, want %d", pane.view.offset(), wheelText)
	}
}
