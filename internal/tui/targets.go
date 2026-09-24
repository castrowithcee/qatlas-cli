package tui

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// The target list of a connection is edited in a screen of its own, opened from its row like a picker: a
// is add, enter edits the selected target, and x or d removes it after asking. ctrl+s keeps the list and
// saves the form in one step, from every state of the screen, the typed target included; in the guided
// setup it goes on to the next step instead, which saves only from its summary. esc closes an unchanged
// list at once and asks about a changed one, so no change is dropped silently. Each target is typed on its
// own, so none has to be quoted into a line with the others. A target is checked by the provider when it
// is taken; the list as a whole is checked by the core when the form is saved, like every other rule.

// targetMetadata is what the provider of the form says about its targets.
func (m *Model) targetMetadata() config.TargetMetadata {
	metadata, _ := m.cfg.ProviderMetadata(m.formProvider())
	return metadata.Target
}

// openTargets opens the entries of the focused target list on its first target.
func (m *Model) openTargets() {
	m.targetList.reset(append([]string(nil), m.fields[m.focus].entries...))
	m.targetEdit, m.targetRemove = -1, false
	m.targetInput.Blur()
	m.screen = screenTargets
	m.clearMessages()
}

// updateTargets handles the target list screen: the typed target first, then the remove question, then
// the list itself.
func (m *Model) updateTargets(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "ctrl+c":
		return m.quit()
	case "ctrl+s":
		// ctrl+s saves from every state of the list, the way it does from every row of the form. A typed
		// target is taken first; one the provider refuses stays typed with the reason, and nothing is saved.
		if m.targetEdit >= 0 {
			m.takeTarget()
			if m.targetEdit >= 0 {
				return nil
			}
		}
		// A remove question left unanswered keeps its target.
		m.targetRemove = false
		m.keepTargets()
		return m.updateForm(key)
	}
	if m.targetEdit >= 0 {
		switch key.String() {
		case "esc":
			m.targetEdit = -1
			m.targetInput.Blur()
			m.clearMessages()
		case "enter":
			m.takeTarget()
		default:
			var cmd tea.Cmd
			m.targetInput, cmd = m.targetInput.Update(key)
			return cmd
		}
		return nil
	}
	if m.targetRemove {
		switch key.String() {
		case "y":
			m.removeTarget()
		case "n", "esc":
			m.targetRemove = false
			m.status = "Target kept"
		}
		return nil
	}
	target, selected := m.targetList.selected()
	switch key.String() {
	case "esc":
		return m.leaveTargets()
	case "a":
		metadata := m.targetMetadata()
		if !metadata.Multiple && len(m.targetList.all) > 0 {
			m.status = ""
			m.fail = fmt.Sprintf("this provider takes one %s only; press enter to edit it or x to remove it",
				metadata.Label)
			return nil
		}
		m.editTarget(len(m.targetList.all), "")
	case "enter", "e":
		if !selected {
			// An empty list has nothing to edit, so enter adds its first target.
			m.editTarget(0, "")
			return nil
		}
		m.editTarget(m.targetList.cursor, target)
	case "x", "d":
		if selected {
			m.targetRemove = true
			m.clearMessages()
		}
	case "up", "k":
		m.targetList.move(-1)
	case "down", "j":
		m.targetList.move(1)
	case "pgup", "pgdown", "home", "end":
		start, end := m.targetWindow()
		m.targetList.page(key.String(), start, end)
	}
	return nil
}

// keepTargets hands the list over to its row and returns to the form. Nothing is written yet.
func (m *Model) keepTargets() {
	m.fields[m.focus].entries = append([]string(nil), m.targetList.all...)
	m.screen = screenForm
	m.clearMessages()
}

// leaveTargets closes the list without keeping it. An unchanged list closes at once; a changed one asks
// first, like a changed form does.
func (m *Model) leaveTargets() tea.Cmd {
	m.clearMessages()
	if slices.Equal(m.targetList.all, m.fields[m.focus].entries) {
		m.screen = screenForm
		return nil
	}
	m.leaveTo, m.leaveFrom = -1, screenTargets
	m.screen = screenLeave
	return nil
}

// answerTargetsLeave answers the question a changed list asks before it closes: k keeps the list in its
// row, d drops the changes, and esc returns to the list unchanged. No other key answers it.
func (m *Model) answerTargetsLeave(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "ctrl+c":
		return m.quit()
	case "esc":
		m.screen = screenTargets
		m.status = "Still editing the targets; nothing was kept or discarded"
	case "k":
		m.keepTargets()
		m.status = "Target list kept in the form; nothing was written yet"
	case "d":
		m.screen = screenForm
		m.clearMessages()
		m.status = "Target changes discarded; the list is as it was"
	}
	return nil
}

// editTarget starts typing the target at index, or a new one at the end of the list.
func (m *Model) editTarget(index int, value string) {
	m.targetEdit = index
	m.targetInput.SetValue(value)
	m.targetInput.CursorEnd()
	m.targetInput.Focus()
	m.clearMessages()
}

// takeTarget puts the typed target into the list once the provider accepts its form and the list does not
// hold it yet. A refused target stays typed with the reason.
func (m *Model) takeTarget() {
	value := strings.TrimSpace(m.targetInput.Value())
	metadata := m.targetMetadata()
	m.status = ""
	if value == "" {
		m.fail = "a " + metadata.Label + " must not be empty; esc cancels the entry"
		return
	}
	if metadata.Validate != nil {
		if err := metadata.Validate(value); err != nil {
			m.fail = m.redactor.Apply(err.Error())
			return
		}
	}
	entries := append([]string(nil), m.targetList.all...)
	for i, entry := range entries {
		if entry == value && i != m.targetEdit {
			m.fail = "this " + metadata.Label + " is already in the list"
			return
		}
	}
	if m.targetEdit >= len(entries) {
		entries = append(entries, value)
	} else {
		entries[m.targetEdit] = value
	}
	m.targetList.setItems(entries)
	m.targetList.selectName(value)
	m.targetEdit = -1
	m.targetInput.Blur()
	m.clearMessages()
}

// removeTarget takes the selected target out of the list.
func (m *Model) removeTarget() {
	m.targetRemove = false
	target, ok := m.targetList.selected()
	if !ok {
		return
	}
	i := m.targetList.cursor
	entries := append(append([]string(nil), m.targetList.all[:i]...), m.targetList.all[i+1:]...)
	m.targetList.setItems(entries)
	m.status = "Removed " + target
}

// targetsView draws the target list screen: its frame, and between them as many targets as fit.
func (m *Model) targetsView() string {
	header, footer := m.targetFrame()
	var b strings.Builder
	b.WriteString(header)
	start, end := m.windowIn(&m.targetList, header, footer, m.targetRow)
	for i := start; i < end; i++ {
		b.WriteString(m.targetRow(i) + "\n")
	}
	b.WriteString(footer)
	return b.String()
}

// targetFrame is everything of the target list screen except the targets: above them what one target is
// and what the provider allows of the list, below them the target being typed or the remove question, the
// keys and the notes.
func (m *Model) targetFrame() (string, string) {
	metadata := m.targetMetadata()
	total := len(m.targetList.all)
	title := "Targets · " + metadata.Label
	if total > 0 {
		title += fmt.Sprintf("  %d/%d", m.targetList.cursor+1, total)
	}
	var head strings.Builder
	head.WriteString(m.wrapped(titleStyle, title) + "\n")
	head.WriteString(m.wrapped(hintStyle, m.providerTargetHint(m.formProvider())) + "\n\n")
	if total == 0 {
		head.WriteString(m.wrapped(hintStyle, "("+emptyTargets(metadata)+")") + "\n")
	}

	var foot strings.Builder
	save := "ctrl+s save"
	if m.wizard != nil {
		// The guided setup saves only from its summary.
		save = "ctrl+s next step"
	}
	keys := "a add · enter edit · x remove · up/down move · " + save + " · esc close"
	switch {
	case m.targetEdit >= 0:
		label := "new: "
		if m.targetEdit < total {
			label = "edit: "
		}
		prefix, width := m.fit(label)
		in := m.targetInput
		in.Width = max(width-1, 1)
		foot.WriteString("\n" + prefix + in.View() + "\n")
		keys = "enter take · " + save + " · esc cancel entry"
	case m.targetRemove:
		target, _ := m.targetList.selected()
		foot.WriteString("\n" + m.wrapped(warningStyle, fmt.Sprintf("Remove %q from the list?", target)) + "\n")
		keys = "y remove · n keep"
	}
	foot.WriteString(m.hint(keys))
	return head.String(), foot.String() + m.notes()
}

// targetRow draws the target at index i of the list.
func (m *Model) targetRow(i int) string {
	return m.row(i == m.targetList.cursor, m.targetList.matches[i])
}

func (m *Model) targetWindow() (int, int) {
	header, footer := m.targetFrame()
	return m.windowIn(&m.targetList, header, footer, m.targetRow)
}
