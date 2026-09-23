package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/castrowithcee/qatlas-cli/internal/helptopics"
)

// firstWords is the start of a topic's text, short enough to stand on one line of any workspace.
func firstWords(topic helptopics.Topic) string {
	return strings.Join(strings.Fields(topic.Text)[:3], " ")
}

// ? opens the help from the sidebar and from the list, every topic is one key away, and esc returns to where
// it was opened.
func TestHelpShowsEveryTopicAndClosesWithEscape(t *testing.T) {
	m, _, _ := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
	if keys := strings.Join(strings.Fields(screenOf(m)), " "); !strings.Contains(keys, "c setup · ? help") {
		t.Errorf("the sidebar keys do not offer ? help:\n%s", screenOf(m))
	}

	press(t, m, "?")
	if m.screen != screenHelp {
		t.Fatalf("? on the sidebar opened screen %v", m.screen)
	}
	topics := helptopics.All()
	for i, key := range []string{"right", "right", "right"} {
		topic := topics[i%len(topics)]
		view := m.View()
		if !strings.Contains(view, "["+topic.Name+"]") || !strings.Contains(view, firstWords(topic)) {
			t.Errorf("topic %s is not shown:\n%s", topic.Name, view)
		}
		if !strings.Contains(view, " Help ") {
			t.Errorf("the workspace is not titled Help:\n%s", view)
		}
		press(t, m, key)
	}
	if m.helpTopic != 0 {
		t.Errorf("right after the last topic shows topic %d, want the first", m.helpTopic)
	}
	press(t, m, "left")
	if last := topics[len(topics)-1]; !strings.Contains(screenOf(m), "["+last.Name+"]") {
		t.Errorf("left from the first topic does not show the last one:\n%s", screenOf(m))
	}
	press(t, m, "tab")
	if m.helpTopic != 0 {
		t.Errorf("tab does not switch the topic: %d", m.helpTopic)
	}
	press(t, m, "esc")
	if m.screen != screenNav {
		t.Fatalf("esc returned to screen %v, want the sidebar", m.screen)
	}

	press(t, m, "enter")
	if keys := strings.Join(strings.Fields(screenOf(m)), " "); !strings.Contains(keys, "sections · ? help · q quit") {
		t.Errorf("the list keys do not offer ? help:\n%s", screenOf(m))
	}
	press(t, m, "?", "right")
	if m.screen != screenHelp || !strings.Contains(screenOf(m), "[agents]") {
		t.Fatalf("? in the list does not open the help:\n%s", screenOf(m))
	}
	press(t, m, "esc")
	if m.screen != screenList {
		t.Errorf("esc returned to screen %v, want the list", m.screen)
	}
}

// A topic longer than the workspace scrolls, and the screen keeps its size while it does.
func TestHelpScrollsWithinTheWorkspace(t *testing.T) {
	m, _, _ := newModel(t)
	for _, size := range []struct{ width, height int }{{100, 28}, {60, 24}, {40, 12}} {
		m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		m.screen = screenNav
		press(t, m, "?", "right")
		view := m.View()
		assertViewFits(t, view, size.width, size.height)
		if !strings.Contains(view, "lines 1-") {
			t.Fatalf("%dx%d: the agents topic does not say it scrolls:\n%s", size.width, size.height, view)
		}
		press(t, m, "down", "down")
		if m.helpOffset != 2 {
			t.Errorf("%dx%d: down twice scrolled to %d", size.width, size.height, m.helpOffset)
		}
		press(t, m, "end")
		view = m.View()
		assertViewFits(t, view, size.width, size.height)
		lines := m.helpLines()
		if !strings.Contains(view, strings.TrimSpace(lines[len(lines)-1])) {
			t.Errorf("%dx%d: end does not show the last line:\n%s", size.width, size.height, view)
		}
		end := m.helpOffset
		press(t, m, "down")
		if m.helpOffset != end {
			t.Errorf("%dx%d: down scrolled past the end", size.width, size.height)
		}
		press(t, m, "home")
		if m.helpOffset != 0 {
			t.Errorf("%dx%d: home scrolled to %d", size.width, size.height, m.helpOffset)
		}
		press(t, m, "pgdown")
		if m.helpOffset == 0 {
			t.Errorf("%dx%d: pgdown did not scroll", size.width, size.height)
		}
		press(t, m, "right")
		if m.helpOffset != 0 {
			t.Errorf("%dx%d: a new topic does not start at its top", size.width, size.height)
		}
		press(t, m, "esc")
	}
}

// Where keys are text, ? is text: in a form and in the filter of a list.
func TestQuestionMarkIsTextWhereTextIsTyped(t *testing.T) {
	m, _, _ := newModel(t)
	press(t, m, "1", "n")
	typeText(t, m, "a?b")
	if m.screen != screenForm || m.fieldValue("name") != "a?b" {
		t.Errorf("? in a form left it or was not typed: screen %v name %q", m.screen, m.fieldValue("name"))
	}

	list, _, _ := newModel(t)
	press(t, list, "enter", "/", "?")
	if list.screen != screenList || list.list.query() != "?" {
		t.Errorf("? in a filter was not typed: screen %v query %q", list.screen, list.list.query())
	}
}
