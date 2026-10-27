package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/castrowithcee/qatlas-cli/internal/helptopics"
)

// firstWords is the start of a topic's text, short enough to stand on one line of any workspace.
func firstWords(topic helptopics.Topic) string {
	return strings.Join(strings.Fields(topic.Text)[:3], " ")
}

// ? opens the help from the tab line and from the list, every topic is one key away, and esc returns to where
// it was opened.
func TestHelpShowsEveryTopicAndClosesWithEscape(t *testing.T) {
	m, _, _ := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
	if keys := strings.Join(strings.Fields(screenOf(m)), " "); !strings.Contains(keys, ": cmds") || !strings.Contains(keys, "? help") {
		t.Errorf("the tab line keys do not offer ? help:\n%s", screenOf(m))
	}

	press(t, m, "?")
	if m.screen != screenHelp {
		t.Fatalf("? on the tab line opened screen %v", m.screen)
	}
	topics := helptopics.All()
	for i, key := range []string{"right", "right", "right"} {
		topic := topics[i%len(topics)]
		view := m.render()
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
		t.Fatalf("esc returned to screen %v, want the tab line", m.screen)
	}

	press(t, m, "enter")
	if keys := strings.Join(strings.Fields(screenOf(m)), " "); !strings.Contains(keys, ": cmds") || !strings.Contains(keys, "? help") {
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
	for _, size := range []struct{ width, height int }{{100, 28}, {60, 24}, {40, minimumHeight}} {
		m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		m.screen = screenNav
		press(t, m, "?", "right")
		view := m.render()
		assertViewFits(t, view, size.width, size.height)
		if !strings.Contains(view, "lines 1-") {
			t.Fatalf("%dx%d: the agents topic does not say it scrolls:\n%s", size.width, size.height, view)
		}
		press(t, m, "down", "down")
		if m.help.offset() != 2 {
			t.Errorf("%dx%d: down twice scrolled to %d", size.width, size.height, m.help.offset())
		}
		press(t, m, "end")
		view = m.render()
		assertViewFits(t, view, size.width, size.height)
		lines := m.helpLines()
		if !strings.Contains(view, strings.TrimSpace(lines[len(lines)-1])) {
			t.Errorf("%dx%d: end does not show the last line:\n%s", size.width, size.height, view)
		}
		end := m.help.offset()
		press(t, m, "down")
		if m.help.offset() != end {
			t.Errorf("%dx%d: down scrolled past the end", size.width, size.height)
		}
		press(t, m, "home")
		if m.help.offset() != 0 {
			t.Errorf("%dx%d: home scrolled to %d", size.width, size.height, m.help.offset())
		}
		press(t, m, "pgdown")
		if m.help.offset() == 0 {
			t.Errorf("%dx%d: pgdown did not scroll", size.width, size.height)
		}
		press(t, m, "right")
		if m.help.offset() != 0 {
			t.Errorf("%dx%d: a new topic does not start at its top", size.width, size.height)
		}
		press(t, m, "esc")
	}
}

// Where keys are text, ? is text: in a form and in the filter of a list.
func TestQuestionMarkIsTextWhereTextIsTyped(t *testing.T) {
	m, _, _ := newModel(t)
	press(t, m, "5", "n")
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

// The help stands as a centred panel over the screen it was opened from where the terminal has the room for a
// modal, and fills the workspace below that; the topics are tabs, a heading is bold and a key is in the accent
// colour, and without colour everything is still there.
func TestHelpIsAPanelOverTheScreenWhereThereIsRoom(t *testing.T) {
	for _, noColor := range []bool{false, true} {
		restoreRenderer(t)
		t.Setenv("NO_COLOR", "")
		applyColorScheme("dark", "")
		if noColor {
			withNoColor(t)
		}
		m, _, _ := newModel(t)
		for _, size := range []struct{ width, height int }{{100, 28}, {modalMinWidth, modalMinHeight}, {modalMinWidth - 1, modalMinHeight}, {40, minimumHeight}} {
			m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
			m.screen = screenNav
			press(t, m, "?")
			view := m.render()
			assertViewFits(t, view, size.width, size.height)
			plain := ansi.Strip(view)
			framed := strings.Contains(plain, "╭─ Help ") || strings.Contains(plain, "╔═ Help ")
			if framed != (size.width >= modalMinWidth && size.height >= modalMinHeight) {
				t.Errorf("%dx%d: panel over the screen = %v:\n%s", size.width, size.height, framed, plain)
			}
			if !strings.Contains(plain, "[start]") || !strings.Contains(plain, "close") {
				t.Errorf("%dx%d: the help lacks its topics or its keys:\n%s", size.width, size.height, plain)
			}
			if noColor && plain != view {
				t.Errorf("%dx%d: NO_COLOR still emits escape sequences", size.width, size.height)
			}
			if framed && !noColor {
				if !strings.Contains(view, colourOf(styles.footKey)+"left/right") {
					t.Errorf("%dx%d: the keys are not in the accent colour:\n%q", size.width, size.height, view)
				}
				// The list of keys comes with its heading in bold.
				for i, line := range m.helpLines() {
					if line == keysHeading {
						m.clampHelp()
						m.help.vp.SetYOffset(i)
					}
				}
				if !strings.Contains(m.render(), colourOf(styles.title)+keysHeading) {
					t.Errorf("%dx%d: the heading of the keys is not bold:\n%q", size.width, size.height, m.render())
				}
			}
			press(t, m, "esc")
		}
	}
}
