package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The wheel moves every plain text area by three lines a notch, and by no more than one line sideways: none.
func TestTheWheelScrollsEveryTextAreaByThreeLines(t *testing.T) {
	longLines := func(n int) []string {
		lines := make([]string, n)
		for i := range lines {
			lines[i] = fmt.Sprintf("line %03d", i)
		}
		return lines
	}
	areas := []struct {
		name   string
		open   func(t *testing.T) (*Model, *textScroll)
		redraw func(m *Model)
	}{
		{"help", func(t *testing.T) (*Model, *textScroll) {
			m := sizedModel(groupModel(t), 100, 40)
			m.openHelp()
			return m, &m.help
		}, nil},
		{"approval detail", func(t *testing.T) (*Model, *textScroll) {
			dir := filepath.Join(t.TempDir(), "qatlas")
			store, _, _ := newApprovalFixture(t, dir, "correct horse battery staple")
			m := openApprovalModel(t, store, dir)
			m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
			m.approvalDetail, m.screen, m.approvalExpanded = "gh", screenConfirm, true
			return m, &m.approval
		}, func(m *Model) { m.approvalChangeView("Approve gh?", toolsChange()) }},
		{"export", func(t *testing.T) (*Model, *textScroll) {
			m := sizedModel(groupModel(t), 100, 30)
			m.export = &exportState{step: exportPreview, from: screenList, lines: longLines(100)}
			m.screen = screenTransfer
			return m, &m.export.view
		}, nil},
		{"import", func(t *testing.T) (*Model, *textScroll) {
			m := sizedModel(groupModel(t), 100, 30)
			m.imp = &importState{step: importResult, from: screenList, lines: longLines(100)}
			m.screen = screenTransfer
			return m, &m.imp.view
		}, nil},
		{"selftest", func(t *testing.T) (*Model, *textScroll) {
			m := sizedModel(groupModel(t), 100, 30)
			m.st = &selftestState{scenario: "alpha", done: true, failure: strings.Repeat("word ", 1500)}
			m.screen = screenSelftest
			return m, &m.st.view
		}, nil},
		{"rename", func(t *testing.T) (*Model, *textScroll) {
			m, _, _ := newModel(t)
			seedConnections(t, m, "docs")
			sizedModel(m, 100, 30)
			startRename(t, m, sectionServices, "wiki", "handbook")
			pump(t, m, "enter")
			for i := 0; i < 60; i++ {
				m.rename.plan.References = append(m.rename.plan.References, fmt.Sprintf("connections.c%02d.service", i))
			}
			return m, &m.rename.view
		}, nil},
	}
	for _, a := range areas {
		t.Run(a.name, func(t *testing.T) {
			m, view := a.open(t)
			m.render()
			if a.redraw != nil {
				a.redraw(m)
			}
			wheelAt(t, m, 20, 10, tea.MouseWheelDown)
			if view.offset() != 3 {
				t.Fatalf("one notch down moved to %d, want 3", view.offset())
			}
			wheelAt(t, m, 20, 10, tea.MouseWheelDown)
			if view.offset() != 6 {
				t.Fatalf("two notches down moved to %d, want 6", view.offset())
			}
			wheelAt(t, m, 20, 10, tea.MouseWheelUp)
			if view.offset() != 3 {
				t.Fatalf("one notch up moved to %d, want 3", view.offset())
			}
			// Shift and the wheel is the horizontal wheel elsewhere; here it is the same scroll, never sideways.
			if view.vp.XOffset() != 0 {
				t.Fatalf("the area scrolled sideways to %d", view.vp.XOffset())
			}
			wheelAt(t, m, 20, 10, tea.MouseWheelUp)
			wheelAt(t, m, 20, 10, tea.MouseWheelUp)
			if view.offset() != 0 {
				t.Fatalf("the wheel up past the top left %d, want 0", view.offset())
			}
		})
	}
}

// Shift and the wheel does not scroll a text area sideways: the rows stay as they are.
func TestShiftWheelDoesNotScrollATextAreaSideways(t *testing.T) {
	m := sizedModel(groupModel(t), 100, 30)
	m.export = &exportState{step: exportPreview, from: screenList,
		lines: []string{strings.Repeat("wide ", 60), "second"}}
	m.screen = screenTransfer
	before := m.render()
	for _, button := range []tea.MouseButton{tea.MouseWheelLeft, tea.MouseWheelRight} {
		event := tea.Mouse{X: 20, Y: 10, Button: button, Mod: tea.ModShift}
		_, cmd := m.Update(tea.MouseWheelMsg(event))
		deliver(m, cmd)
	}
	if m.export.view.vp.XOffset() != 0 || m.export.view.offset() != 0 || m.render() != before {
		t.Errorf("the sideways wheel moved the text:\n%s", m.render())
	}
}

// The running selftest follows its newest line and the finished report starts at its top.
func TestSelftestProgressFollowsTheEnd(t *testing.T) {
	m := sizedModel(groupModel(t), 100, 30)
	st := &selftestState{scenario: "alpha", profile: "smoke"}
	for i := 0; i < 100; i++ {
		st.lines = append(st.lines, fmt.Sprintf("step %03d", i))
	}
	m.st, m.screen = st, screenSelftest
	view := m.render()
	if !strings.Contains(view, "step 099") || strings.Contains(view, "step 000") {
		t.Fatalf("the progress does not show its end:\n%s", view)
	}
	st.lines = append(st.lines, "step 100")
	if view = m.render(); !strings.Contains(view, "step 100") {
		t.Fatalf("a new line is not followed:\n%s", view)
	}
}
