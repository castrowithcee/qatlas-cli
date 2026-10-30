package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// A modal dialog shows the answers of its key line as buttons too. The buttons are drawn from the key line
// itself, so a button always presses the key it names and no dialog states its answers twice.

// dialogButton is one answer of a dialog.
type dialogButton struct {
	key     string
	label   string
	kind    buttonKind
	focused bool
}

// warnMark leads the heading of a question in a modal.
const warnMark = "⚠"

// buttonGap separates two buttons of a row.
const buttonGap = "  "

// dialogButtons reads the answers out of a key line: every group that names a key a button can press,
// except the groups of the editor around the dialog (help, commands, navigation). With danger the groups
// that answer with y or d are drawn as the dangerous kind. The group of enter is the focused one, so the
// default answer shows without colour too; a question that enter does not answer has none.
func (m *Model) dialogButtons(keys string, danger bool, labels map[string]string) []dialogButton {
	text := keyPlaceholders(strings.ReplaceAll(footerText(keys), "{info}", m.infoLabel()))
	var buttons []dialogButton
	seen := map[string]bool{}
	for i, group := range strings.Split(text, " · ") {
		key := footerKey(group)
		rank, _ := footerGroupRank(group, i)
		answers := rank == footerBack || rank == footerPrimary || rank == footerSave ||
			rank >= footerOthers && rank < footerInfo
		if key == "" || !answers || seen[key] {
			continue
		}
		seen[key] = true
		label := labels[key]
		if label == "" {
			label = buttonLabel(group)
		}
		if label == "" {
			label = key
		}
		kind := buttonPrimary
		switch {
		case rank == footerBack:
			kind = buttonNormal
		case danger && (key == keyOf(actYes) || key == keyOf(actDiscard)):
			kind = buttonDanger
		}
		first, _, _ := strings.Cut(group, " ")
		focused := first == keyOf(actEnter) || strings.HasPrefix(group, first+" or "+keyLabel(actEnter)+" ")
		buttons = append(buttons, dialogButton{key: key, label: label, kind: kind, focused: focused})
	}
	return buttons
}

// buttonLabel is the action of a key group as a button label: "n/esc cancel, nothing saved" gives "Cancel".
func buttonLabel(group string) string {
	_, action, _ := strings.Cut(group, " ")
	if rest, ok := strings.CutPrefix(action, "or "); ok {
		_, action, _ = strings.Cut(rest, " ")
	}
	action, _, _ = strings.Cut(action, ",")
	action = strings.TrimSpace(truncateCells(action, 24))
	if action == "" {
		return ""
	}
	r := []rune(action)
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

// buttonRows draws the buttons in rows that fit the dialog and makes each of them clickable: the rows are
// logged like footer lines, with the key of every button as its span.
func (m *Model) buttonRows(buttons []dialogButton) string {
	width := m.usable(0)
	var rows []string
	var line string
	var spans []footerSpan
	flush := func() {
		if line == "" {
			return
		}
		rows = append(rows, line)
		if m.footerLog != nil {
			*m.footerLog = append(*m.footerLog, footerLine{plain: ansi.Strip(line), spans: spans})
		}
		line, spans = "", nil
	}
	x := 0
	for _, b := range buttons {
		drawn := button(b.kind, b.label, b.focused)
		w := lipgloss.Width(drawn)
		if x > 0 && x+len(buttonGap)+w > width {
			flush()
			x = 0
		}
		if x > 0 {
			line += buttonGap
			x += len(buttonGap)
		}
		spans = append(spans, footerSpan{x, x + w, b.key})
		line += drawn
		x += w
	}
	flush()
	return strings.Join(rows, "\n")
}

// withButtons is the text of a modal dialog up to its key line, then its buttons and the key line.
func (m *Model) withButtons(text, keys string, danger bool, labels map[string]string) string {
	rows := m.buttonRows(m.dialogButtons(keys, danger, labels))
	if rows != "" {
		text += "\n" + rows + "\n"
	}
	return text + m.keyHint(keys)
}
