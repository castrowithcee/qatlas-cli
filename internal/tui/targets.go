package tui

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// The target list of a connection is edited in a screen of its own, opened from its row like a picker: a is
// add, which offers known targets and a builder where there are any, enter edits the selected target, and d
// removes it after asking. r labels the selected target for the person, in a line of its own; an empty
// label removes it. F2 keeps the list and saves the form in one step, from every state of the
// screen, the typed target included; in the guided setup it goes on to the next step instead, which saves
// only from its summary. esc is one level back: it closes an unchanged list, cancels an unchanged entry,
// and asks about a changed list, a typed entry or a half-built target, so no change is dropped silently. Each target is typed on its own, so none has to be quoted into a line with the others. A
// target is checked by the provider when it is taken; the list as a whole is checked by the core when the
// form is saved, like every other rule.

// targetMetadata is what the provider of the form says about its targets.
func (m *Model) targetMetadata() config.TargetMetadata {
	metadata, _ := m.cfg.ProviderMetadata(m.formProvider())
	return metadata.Target
}

// openTargets opens the entries of the focused target list on its first target.
func (m *Model) openTargets() {
	m.targetList.reset(append([]string(nil), m.fields[m.focus].entries...))
	m.targetLabels = maps.Clone(m.fields[m.focus].labels)
	m.closePick()
	m.targetEdit, m.targetRemove, m.targetAdd, m.labelEdit = -1, false, nil, false
	m.targetInput.Blur()
	m.labelInput.Blur()
	m.screen = screenTargets
	m.clearMessages()
}

// updateTargets handles the target list screen: the typed target first, then the remove question, then
// the list itself.
func (m *Model) updateTargets(key tea.KeyPressMsg) tea.Cmd {
	switch key.String() {
	case keyOf(actSave):
		// F2 saves from every state of the list, the way it does from every row of the form. A typed
		// target is taken first; one the provider refuses stays typed with the reason, and nothing is saved.
		// The add menu takes its typed line the same way, and the builder its target once it is complete.
		if m.targetPick != nil {
			// Marked entries are taken; with none marked the picker only closes, and a selected row is
			// never taken by F2.
			if len(m.targetPick.marks) > 0 && !m.pickTake(false) {
				return nil
			}
			m.closePick()
		}
		if m.targetAdd != nil && !m.saveAdd() {
			return nil
		}
		if m.targetEdit >= 0 {
			m.takeTarget()
			if m.targetEdit >= 0 {
				return nil
			}
		}
		if m.labelEdit {
			m.takeLabel()
		}
		// A remove question left unanswered keeps its target.
		m.targetRemove = false
		m.keepTargets()
		return m.updateForm(key)
	}
	if m.targetPick != nil {
		return m.updatePick(key)
	}
	if m.targetAdd != nil {
		return m.updateAdd(key)
	}
	if m.labelEdit {
		switch key.String() {
		case keyOf(actBack):
			m.stopLabel()
		case "enter":
			m.takeLabel()
		default:
			var cmd tea.Cmd
			m.labelInput, cmd = m.labelInput.Update(inputMsg(key))
			return cmd
		}
		return nil
	}
	if m.targetEdit >= 0 {
		switch key.String() {
		case keyOf(actBack):
			if m.targetTyped() {
				m.askLeave(false)
				return nil
			}
			m.targetEdit = -1
			m.targetInput.Blur()
			m.clearMessages()
		case "enter":
			m.takeTarget()
		default:
			var cmd tea.Cmd
			m.targetInput, cmd = m.targetInput.Update(inputMsg(key))
			return cmd
		}
		return nil
	}
	if m.targetRemove {
		switch key.String() {
		case "y":
			m.removeTarget()
		case "n", keyOf(actBack):
			m.targetRemove = false
			m.status = tr(tTargetTargetKept)
		}
		return nil
	}
	target, selected := m.targetList.selected()
	switch key.String() {
	case keyOf(actBack):
		return m.leaveTargets()
	case "a":
		metadata := m.targetMetadata()
		if !metadata.Multiple && len(m.targetList.all) > 0 {
			m.status = ""
			m.fail = tr(tTargetThisProviderTakesOneOnly,
				metadata.Label)
			return nil
		}
		m.startAdd()
	case "enter", "e":
		if !selected {
			// An empty list has nothing to edit, so enter adds its first target.
			m.startAdd()
			return nil
		}
		m.editTarget(m.targetList.cursor, target)
	case "r":
		// The guided setup saves no labels, so it offers none.
		if selected && m.wizard == nil {
			m.editLabel(target)
		}
	case keyOf(actDelete):
		if selected {
			m.targetRemove = true
			m.clearMessages()
		}
	case "up":
		m.targetList.move(-1)
	case "down":
		m.targetList.move(1)
	case "pgup", "pgdown", "home", "end":
		start, end := m.targetWindow()
		m.targetList.page(key.String(), start, end)
	}
	return nil
}

// keepTargets hands the list over to its row and returns to the form. Nothing is written yet.
func (m *Model) keepTargets() {
	m.closePick()
	m.fields[m.focus].entries = append([]string(nil), m.targetList.all...)
	m.fields[m.focus].labels = maps.Clone(m.targetLabels)
	m.screen = screenForm
	m.clearMessages()
}

// leaveTargets closes the list without keeping it. An unchanged list closes at once; a changed one asks
// first, like a changed form does.
func (m *Model) leaveTargets() tea.Cmd {
	m.clearMessages()
	m.closePick()
	if !m.targetsChanged() {
		m.screen = screenForm
		return nil
	}
	m.askLeave(false)
	return nil
}

// targetTyped reports whether the target being typed differs from the one it started from.
func (m *Model) targetTyped() bool {
	original := ""
	if m.targetEdit < len(m.targetList.all) {
		original = m.targetList.all[m.targetEdit]
	}
	return m.targetInput.Value() != original
}

// targetsPending reports whether closing the screen for good would lose something: a changed list, a typed
// target or a half-built one.
func (m *Model) targetsPending() bool {
	if m.targetPick != nil && len(m.targetPick.marks) > 0 {
		return true
	}
	if a := m.targetAdd; a != nil && (a.kind >= 0 || a.choices.query() != "") {
		return true
	}
	if m.targetEdit >= 0 && m.targetTyped() {
		return true
	}
	return m.targetsChanged()
}

// targetsChanged reports whether the list or its labels differ from what the row holds.
func (m *Model) targetsChanged() bool {
	row := m.fields[m.focus]
	return !slices.Equal(m.targetList.all, row.entries) || !maps.Equal(m.targetLabels, row.labels)
}

// editLabel starts typing the label of target.
func (m *Model) editLabel(target string) {
	m.labelEdit = true
	m.labelInput.SetValue(m.targetLabels[target])
	m.labelInput.CursorEnd()
	m.labelInput.Focus()
	m.clearMessages()
}

func (m *Model) stopLabel() {
	m.labelEdit = false
	m.labelInput.Blur()
	m.clearMessages()
}

// takeLabel sets the typed label of the selected target, or removes it when nothing is typed. Whether a
// label is acceptable is the core's to say when the form is saved.
func (m *Model) takeLabel() {
	target, ok := m.targetList.selected()
	if ok {
		if label := m.labelInput.Value(); label == "" {
			delete(m.targetLabels, target)
		} else {
			if m.targetLabels == nil {
				m.targetLabels = map[string]string{}
			}
			m.targetLabels[target] = label
		}
	}
	m.stopLabel()
}

// labelled is a target with its label, as far as it has one.
func (m *Model) labelled(target string, labels map[string]string) string {
	if label := labels[target]; label != "" {
		return target + "  " + m.logText(label)
	}
	return target
}

// editTarget starts typing the target at index, or a new one at the end of the list.
func (m *Model) editTarget(index int, value string) {
	m.targetEdit = index
	m.targetInput.SetValue(value)
	m.targetInput.CursorEnd()
	m.targetInput.Focus()
	m.clearMessages()
}

// targetProblem says why value cannot be put at index of the list: the provider refuses its form, or the
// list holds it already at another place. It is empty when the target is acceptable.
func (m *Model) targetProblem(value string, index int) string {
	metadata := m.targetMetadata()
	if value == "" {
		return tr(tTargetAMustNotBeEmpty, metadata.Label)
	}
	if metadata.Validate != nil {
		if err := metadata.Validate(value); err != nil {
			return m.redactor.Apply(err.Error())
		}
	}
	for i, entry := range m.targetList.all {
		if entry == value && i != index {
			return tr(tPickThisIsAlreadyInThe, metadata.Label)
		}
	}
	return ""
}

// takeTarget puts the typed target into the list once the provider accepts its form and the list does not
// hold it yet. A refused target stays typed with the reason.
func (m *Model) takeTarget() {
	value := strings.TrimSpace(m.targetInput.Value())
	m.status = ""
	if problem := m.targetProblem(value, m.targetEdit); problem != "" {
		m.fail = problem
		return
	}
	entries := append([]string(nil), m.targetList.all...)
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
	m.status = tr(tPathRemoved, target)
}

// targetsView draws the target list screen: its frame, and between them as many targets as fit.
func (m *Model) targetsView() string {
	if m.targetPick != nil {
		return m.pickView()
	}
	if m.targetAdd != nil {
		return m.addView()
	}
	header, footer := m.targetFrame()
	var b strings.Builder
	b.WriteString(header)
	start, end := m.windowIn(&m.targetList, header, footer, m.targetRow)
	for i := start; i < end; i++ {
		b.WriteString(m.targetRow(i) + "\n")
	}
	m.hitSelect(&m.targetList, screenTargets, header, start, end, m.targetRow)
	b.WriteString(footer)
	return b.String()
}

// targetFrame is everything of the target list screen except the targets: above them what one target is
// and what the provider allows of the list, below them the target being typed or the remove question, the
// keys and the notes.
func (m *Model) targetFrame() (string, string) {
	p := m.dialogPanelOf()
	metadata := m.targetMetadata()
	total := len(m.targetList.all)
	position := ""
	if total > 0 {
		position = fmt.Sprintf("%d/%d", m.targetList.cursor+1, total)
	}
	var head strings.Builder
	head.WriteString(p.top(tr(tTargetTargets, metadata.Label), position))
	head.WriteString(p.text(styles.hint, m.providerTargetHint(m.formProvider())))
	head.WriteString(p.blank())
	if total == 0 {
		head.WriteString(p.text(styles.hint, "("+emptyTargets(metadata)+")"))
	}

	var foot strings.Builder
	save := "{save} save"
	if m.wizard != nil {
		// The guided setup saves only from its summary.
		save = "{save} next step"
	}
	keys := "a add · enter edit · r label · {delete} remove · up/down move · " + save + " · {back} close · {command} cmds · {help} help"
	if m.wizard != nil {
		keys = "a add · enter edit · {delete} remove · up/down move · " + save + " · {back} close · {command} cmds · {help} help"
	}
	switch {
	case m.labelEdit:
		target, _ := m.targetList.selected()
		foot.WriteString(p.blank() + p.text(styles.hint, tr(tTargetLabelForLeaveItEmpty, target)))
		foot.WriteString(p.styled(m.inputLineIn(p.room, tr(tTargetLabel), m.labelInput)))
		keys = "enter take · " + save + " · {back} cancel entry"
	case m.targetEdit >= 0:
		label := tr(tTargetNew2)
		if m.targetEdit < total {
			label = tr(tTargetEdit)
		}
		foot.WriteString(p.blank() + p.styled(m.inputLineIn(p.room, label, m.targetInput)))
		keys = "enter take · " + save + " · {back} cancel entry"
	case m.targetRemove:
		target, _ := m.targetList.selected()
		foot.WriteString(p.blank() + p.text(styles.warning, warnMark+" "+tr(tTargetRemoveFromTheList, target)))
		keys = "y remove · n/{back} keep · {command} cmds · {help} help"
	}
	foot.WriteString(p.bottom())
	foot.WriteString(m.keyHint(keys))
	return head.String(), foot.String() + m.notes()
}

// targetRow draws the target at index i of the list.
func (m *Model) targetRow(i int) string {
	return m.dialogPanelOf().row(i == m.targetList.cursor, m.labelled(m.targetList.matches[i], m.targetLabels))
}

func (m *Model) targetWindow() (int, int) {
	header, footer := m.targetFrame()
	return m.windowIn(&m.targetList, header, footer, m.targetRow)
}
