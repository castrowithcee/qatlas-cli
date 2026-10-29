package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// boundView cuts plain text to the workspace.
func (m *Model) boundView(view string) string { return m.boundTo(view, m.width, m.height) }

// boundTo cuts view to width by height. Lines that already fit keep their styling; only a line that does
// not fit is cut, which the callers keep to plain text.
func (m *Model) boundTo(view string, width, height int) string {
	width, height = max(width, 1), max(height, 1)
	lines := strings.Split(view, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for i := range lines {
		if lipgloss.Width(lines[i]) > width {
			lines[i] = clipCells(lines[i], width)
		}
	}
	return strings.Join(lines, "\n")
}

// boundKeepingKeys is boundTo for a question: when it does not fit, the first line and the last lines stay,
// so the key line, which ends the question, is never the part that is cut.
func (m *Model) boundKeepingKeys(view string, width, height int) string {
	height = max(height, 1)
	lines := strings.Split(view, "\n")
	if len(lines) > height {
		lines = append(lines[:1], lines[len(lines)-height+1:]...)
	}
	return m.boundTo(strings.Join(lines, "\n"), width, height)
}

// truncateLeft keeps the end of text within width and marks the cut at its start.
func truncateLeft(text string, width int) string {
	if lipgloss.Width(text) <= width {
		return text
	}
	if width <= 1 {
		return clipCells("…", max(width, 0))
	}
	runes := []rune(text)
	for i := range runes {
		if tail := string(runes[i:]); lipgloss.Width(tail) <= width-1 {
			return "…" + tail
		}
	}
	return "…"
}

// clipCells cuts text to width visible cells. Escape sequences count no cells and are never split; when
// the cut leaves a style open, a reset ends it. Text without escapes stays a true prefix of its input.
func clipCells(text string, width int) string {
	if width <= 0 {
		return ""
	}
	var b strings.Builder
	parser := ansi.NewParser()
	state := byte(ansi.NormalState)
	used, styled := 0, false
	for rest := text; rest != ""; {
		seq, w, n, next := ansi.DecodeSequence(rest, state, parser)
		state = next
		if w == 0 && ansi.HasCsiPrefix(seq) && strings.HasSuffix(seq, "m") {
			// An SGR sequence: keep it whole; a bare reset leaves nothing open.
			b.WriteString(seq)
			styled = seq != "\x1b[m" && seq != "\x1b[0m"
		} else if w == 0 && strings.HasPrefix(seq, "\x1b") {
			b.WriteString(seq)
		} else {
			if used+w > width {
				if styled {
					b.WriteString("\x1b[m")
				}
				return b.String()
			}
			b.WriteString(seq)
			used += w
		}
		rest = rest[n:]
	}
	return b.String()
}

func truncateCells(text string, width int) string {
	if lipgloss.Width(text) <= width {
		return text
	}
	if width <= 0 {
		return ""
	}
	if width == 1 {
		return "…"
	}
	return clipCells(text, width-1) + "…"
}

func wrapCells(text string, width int) string {
	if width <= 0 {
		return ""
	}
	var lines []string
	for text != "" {
		line := clipCells(text, width)
		if line == "" {
			break
		}
		lines = append(lines, line)
		text = strings.TrimPrefix(text, line)
	}
	return strings.Join(lines, "\n")
}

func padLine(text string, width int) string {
	return text + strings.Repeat(" ", max(width-lipgloss.Width(text), 0))
}

// wrapped fits a message into the terminal instead of leaving it to be cut off at the right edge.
//
// The messages of the cascade carry the way out at their end: the file to chmod, the command to run, the
// variable to export. A truncated line loses exactly the part the reader needs, and it is the longest
// messages, the ones that explain the most, that get truncated. Wrapping keeps all of it on screen and
// needs nothing but the width the terminal already reports.
func (m *Model) wrapped(style lipgloss.Style, text string) string {
	return render(style, m.usable(0), text)
}

// render wraps text into width and styles it line by line. A line that fits is left exactly as it is,
// including a trailing blank that may be the cursor of an input. A line that does not is wrapped, and its
// pieces lose their trailing blanks: Lip Gloss pads them and keeps the space at a wrap point, which can make
// a piece one cell wider than asked for and push the frame out of line.
func render(style lipgloss.Style, width int, text string) string {
	width = max(width, 1)
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if lipgloss.Width(line) <= width {
			out = append(out, style.Render(line))
			continue
		}
		for _, piece := range strings.Split(lipgloss.NewStyle().Width(width).Render(line), "\n") {
			out = append(out, style.Render(strings.TrimRight(piece, " ")))
		}
	}
	return strings.Join(out, "\n")
}

// usable is the room left for text after a prefix of n cells.
//
// A terminal that has not reported its width yet is assumed to be the usual one; a terminal that reported a
// width narrower than the prefix still gets one cell to wrap into, rather than being drawn at the assumed
// width and spilling. Falling back to the assumed width because the real one is small would be the very
// truncation this is here to avoid.
func (m *Model) usable(prefix int) int {
	width := m.width
	if width <= 0 {
		width = defaultWidth
	}
	if width-prefix < 1 {
		return 1
	}
	return width - prefix
}

// fit returns the prefix to draw and the room left for the text beside it, so that the two together never
// exceed the terminal. A terminal narrower than the prefix keeps one cell for the text and loses part of
// the prefix: an indent that pushes text past the right edge is worse than a missing indent.
func (m *Model) fit(prefix string) (string, int) { return m.fitIn(m.usable(0), prefix) }

// fitIn is fit within the first room cells of the workspace.
func (m *Model) fitIn(room int, prefix string) (string, int) {
	if m.width > 0 {
		if room := room - 1; room < len(prefix) {
			prefix = prefix[:max(room, 0)]
		}
	}
	return prefix, max(room-len(prefix), 1)
}

// indented draws a hint under the row it belongs to and keeps its continuation lines there too, so a hint
// that needs two lines still reads as one hint rather than as a stray sentence at the left margin.
func (m *Model) indented(text string) string {
	return m.indentedWith(styles.hint, text)
}

func (m *Model) indentedWith(style lipgloss.Style, text string) string {
	indent, width := m.fit(m.hintIndent())
	lines := strings.Split(render(style, width, text), "\n")
	for i, l := range lines {
		lines[i] = indent + l
	}
	return strings.Join(lines, "\n")
}

// renderField draws one form row. The focused text field is drawn by the text input itself, so the cursor
// stands where it really stands, including in the middle of a url being corrected. Every other field is
// drawn plainly from its value.
func (m *Model) renderField(f field, focused bool) string {
	value := f.value()
	switch {
	case f.kind == fieldVaultState:
		// Read live, never cached: the very act of running an action rebuilds the form (see vaultsettings.go),
		// but reading it fresh here as well costs nothing and can never go stale.
		value = m.vaultStateMark()
	case f.kind == fieldVaultAction:
		value = tr(tValueRunsThis)
	case f.kind == fieldKey && focused && m.keyCapture:
		value = tr(tKeyPress)
	case f.kind == fieldKey:
		value = keyRowValue(f)
	case f.kind == fieldSecret:
		// A secret row shows where the role resolves from and nothing else: there is no value to draw,
		// and the resolver would not hand one out.
		value = "(" + m.roleState(m.editing, f.label, m.credentialType()) + ")"
	case f.kind == fieldMasked && focused:
		// The input draws one mask character per typed character and never the characters themselves.
		value = inputView(fitInput(f.input, m.valueRoom(f.label)))
	case f.kind == fieldMasked && f.input.Value() != "":
		value = tr(tRenderEnteredMasked)
	case f.kind == fieldMasked && f.stored:
		value = styles.hint.Render(tr(tRenderStoredUnchanged))
	case f.kind == fieldMasked:
		value = emptyValue(wordEmpty)
	case f.kind == fieldProvider:
		value = m.providerValue(f)
	case f.kind == fieldChoice && len(f.choices) == 0:
		value = emptyValue(wordNone)
	case f.kind == fieldChoice && value == "":
		value = emptyValue(wordNone)
	case f.kind == fieldChoice:
		value = valueText(f.label, value)
	case f.kind == fieldText && f.readOnly && f.hint != "":
		// A read-only text field never takes focus, so its hint would never be the one shown; it explains
		// itself on its own row instead, the way a read-only tool list already does below.
		value += " (" + f.hint + ")"
	case f.kind == fieldToolList && f.label == forwardLabel && len(f.marked()) == 0:
		value = emptyValue(wordNone)
	case f.kind == fieldToolList && f.label == forwardLabel:
		value = strings.Join(f.marked(), ", ")
	case f.kind == fieldToolList && f.label == vorbilderLabel && len(f.marked()) == 0:
		value = emptyValue(wordNone)
	case f.kind == fieldToolList && f.label == vorbilderLabel:
		value = strings.Join(f.marked(), ", ")
	case f.kind == fieldToolList && len(f.marked()) == 0:
		value = emptyValue(wordNone) + tr(tRenderNoToolIsOffered)
	case f.kind == fieldToolList && f.label == toolListLabel:
		value = tr(tRenderOf, len(f.marked()), len(f.choices))
		metadata, _ := m.cfg.ProviderMetadata(m.formProvider())
		if effects := config.EffectsOfTools(metadata, f.marked()); len(effects) > 0 {
			names := make([]string, len(effects))
			for i, effect := range effects {
				names[i] = string(effect)
			}
			value += " · " + strings.Join(names, ", ")
		}
	case f.kind == fieldToolList:
		value = tr(tRenderOfTicked, len(f.marked()), len(f.choices))
	case f.kind == fieldTargets && len(f.entries) == 0:
		metadata, _ := m.cfg.ProviderMetadata(m.formProvider())
		value = emptyValue(wordAny)
		if metadata.Target.Required {
			value = emptyValue(wordRequired)
		}
	case f.kind == fieldTargets && f.expanded:
		lines := make([]string, len(f.entries))
		for i, target := range f.entries {
			lines[i] = m.targetLine(target, f.labels, f.excluded)
		}
		value = listLines(lines, "target", "targets")
	case f.kind == fieldTargets:
		value = targetsSummary(f.entries, f.excluded)
	case f.kind == fieldPaths && isFilesLabel(f.label) && len(f.entries) == 0:
		value = emptyValue(wordNone)
	case f.kind == fieldPaths && isFilesLabel(f.label) && f.expanded:
		value = listLines(f.entries, "directory", "directories")
	case f.kind == fieldPaths && isFilesLabel(f.label):
		value = listSummary(f.entries, "directories")
	case f.kind == fieldPaths && len(f.entries) == 0:
		value = emptyValue(wordAny)
	case f.kind == fieldPaths && f.expanded:
		value = listLines(f.entries, "path", "paths")
	case f.kind == fieldPaths:
		value = listSummary(f.entries, "paths")
	case f.kind == fieldMultiChoice:
		parts := make([]string, len(f.choices))
		for i, choice := range f.choices {
			mark := " "
			if f.selected[choice] {
				mark = "x"
			}
			parts[i] = "[" + mark + "] " + valueText(f.label, choice)
		}
		// A box is never split from its value: the row breaks between the values only.
		var lines []string
		for _, part := range parts {
			if last := len(lines) - 1; last >= 0 && lipgloss.Width(lines[last]+"  "+part) <= m.rowRoom() {
				lines[last] += "  " + part
				continue
			}
			lines = append(lines, part)
		}
		value = strings.Join(lines, "\n")
	case focused && !f.readOnly:
		source := ""
		if f.kind == fieldEnvName && f.value() != "" {
			source = " (" + m.envSource(f.value()) + ")"
		}
		value = inputView(fitInput(f.input, m.valueRoom(f.label)-lipgloss.Width(source))) + source
	case f.kind == fieldEnvName && value != "":
		value += " (" + m.envSource(value) + ")"
	case value == "":
		value = emptyValue(wordEmpty)
	}
	return value
}

// The words an empty row shows. The word itself carries the meaning, so the dimming is never the only
// signal (NO_COLOR keeps the word): empty is a text with nothing in it, none a list or choice with nothing
// in it, any a list whose being empty puts no limit on the connection, required a list that must not stay
// empty.
const (
	wordEmpty    = "empty"
	wordNone     = "none"
	wordAny      = "any"
	wordRequired = "required"
)

// emptyValue draws the empty value of a row of any kind the same way.
func emptyValue(word string) string { return styles.hint.Render(wordText(word)) }

// shownLabel is the text a row label is drawn as, in the language of the editor. Every label of a form
// passes through here, for its width as well as for its text, so the label column fits the drawn label.
func (m *Model) shownLabel(label string) string { return labelText(label) }

// labelColumn is the width of the label column of a form with the given labels: the longest label, so that
// no value is pushed aside by a label that does not fit. A column is never wider than a third of the
// workspace; a label beyond that pushes its own value, as it always did.
func (m *Model) labelColumn(labels ...string) int {
	longest := 0
	for _, label := range labels {
		longest = max(longest, lipgloss.Width(m.shownLabel(label)))
	}
	return min(longest, max(minLabelColumn, m.usable(0)/3))
}

// minLabelColumn is the narrowest cap the label column is cut to in a narrow terminal.
const minLabelColumn = 12

// formIndent is where the value of a form row starts: the focus marker and the label column.
func (m *Model) formIndent() int { return 2 + m.formCol + 1 }

// formHead is the focus marker and the label of a form row, padded to the label column of the form. A
// label longer than its column pushes the value further right.
func (m *Model) formHead(active bool, label string) string {
	marker := "  "
	if active {
		marker = "> "
	}
	label = m.shownLabel(label)
	return marker + label + strings.Repeat(" ", max(m.formCol-lipgloss.Width(label), 0)) + " "
}

// valueRoom is the room a form row with the given label leaves for its value.
func (m *Model) valueRoom(label string) int {
	if m.formBox {
		return m.formValueW - 4
	}
	_, width := m.fit(strings.Repeat(" ", lipgloss.Width(m.formHead(false, label))))
	return width
}

// formRow draws one form row: the focus marker, the label, and the value, which continues under itself
// when it does not fit instead of pushing past the right edge.
func (m *Model) formRow(active bool, label, value string) string {
	style := lipgloss.NewStyle()
	if active {
		style = styles.active
	}
	head := m.formHead(active, label)
	indent, width := m.fit(strings.Repeat(" ", lipgloss.Width(head)))
	lines := strings.Split(render(style, width, value), "\n")
	lines[0] = style.Render(head) + lines[0]
	for i := 1; i < len(lines); i++ {
		lines[i] = indent + lines[i]
	}
	return strings.Join(lines, "\n")
}

// row draws one line of a list. A line that does not fit is continued under its own entry instead of being
// cut off at the right edge: the list is where the source of every secret role stands, and that is the part
// that falls off first.
func (m *Model) row(active bool, text string) string { return m.rowIn(m.usable(0), active, text) }

// rowIn is row within the first room cells of the workspace.
func (m *Model) rowIn(room int, active bool, text string) string {
	blank, width := m.fitIn(room, "  ")
	marker := blank
	if active && len(blank) == 2 {
		marker = "> "
	}
	style := lipgloss.NewStyle()
	if active {
		style = styles.active
	}
	lines := strings.Split(render(style, width, text), "\n")
	for i, l := range lines {
		prefix := blank
		if i == 0 {
			prefix = marker
		}
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

// listRow draws one row of a table screen: a section list, the picker, or the provider table. text is
// already cut to fit beside the marker: long cells are shortened before they ever reach here, never
// wrapped. The marked row is highlighted the full width of the row, behind a narrow accent mark, which is
// '>' where colour cannot show the bar, so NO_COLOR or an ASCII terminal still shows which one it is; every
// other row is plain.
func (m *Model) listRow(active bool, text string) string {
	return m.listRowIn(m.usable(0), active, text)
}

// listRowIn is listRow within the first room cells of the workspace.
func (m *Model) listRowIn(room int, active bool, text string) string {
	blank, width := m.fitIn(room, "  ")
	if !active {
		return blank + padLine(text, width)
	}
	body := padLine(text, width)
	if len(blank) != 2 {
		// No room for the mark: the bar alone shows the row.
		return styles.activeRow.Render(keepStyle(blank+body, styles.activeRow))
	}
	// The cells of the text may reset their own styles, so the bar is set again after each reset.
	return styles.selMark.Render(selectionMark()) + styles.activeRow.Render(keepStyle(" "+body, styles.activeRow))
}

// selectionMark leads the marked row: a narrow accent bar where colour shows it, and '>' where it does not,
// so the selection never depends on colour alone.
func selectionMark() string {
	// The plain terminal palette has colour but no selection fill for the bar to stand on.
	if noColour() || styles.palette.selection == "" {
		return ">"
	}
	return "▌"
}

// hint is the key line of a screen. Like every other prose line it is wrapped into the terminal instead of
// being cut off at its right edge.
func (m *Model) hint(text string) string { return "\n" + m.wrapped(styles.hint, text) }

// keyHint is the key line of a screen: groups of keys separated by " · ". It hands the line to the footer of
// the frame (see footerLine) and returns nothing for the workspace; only while a modal dialog is drawn does
// it return the line itself (see dialogKeyHint).
func (m *Model) keyHint(text string) string {
	if strings.Contains(text, "{command} cmds") && !strings.Contains(text, "{info}") && m.infoWorks() {
		text = strings.Replace(text, "{command} cmds", "{info} info · {command} cmds", 1)
	}
	if m.footerLog == nil {
		// A screen's keys stand in the footer of the frame, not in the workspace. A dialog under a backdrop
		// leaves the footer as it was.
		if !m.backdrop {
			if m.copyKeyShown() {
				text = withCopyKey(text)
			}
			m.screenKeys = text
		}
		return ""
	}
	return m.dialogKeyHint(text)
}

// dialogKeyHint is the key line of a modal dialog: groups never split across a wrap, only between groups,
// and within a group the key (its first word) is bold while the action after it keeps the plain hint style.
// A single group too wide for the terminal is cut with "...". It stands in the dialog itself, where its
// keys are buttons.
func (m *Model) dialogKeyHint(text string) string {
	if strings.Contains(text, "{info} info") {
		// The info key is named only where that costs no extra footer line, so no screen needs more rows
		// for it than it needed before.
		without := strings.Replace(strings.Replace(text, " · {info} info", "", 1), "{info} info · ", "", 1)
		if len(m.keyHintLines(text)) > len(m.keyHintLines(without)) {
			text = without
		}
	}
	return "\n" + strings.Join(m.keyHintLines(text), "\n")
}

// keyHintLines is the footer text broken into lines of whole groups.
func (m *Model) keyHintLines(text string) []string { return m.keyHintLinesWith(text, keyHintGroup) }

// keyHintLinesWith is keyHintLines with each group drawn by style.
func (m *Model) keyHintLinesWith(text string, style func(group string) string) []string {
	text = keyPlaceholders(strings.ReplaceAll(footerText(text), "{info}", m.infoLabel()))
	width := m.usable(0)
	var lines []string
	var line string
	lineWidth := 0
	var current []footerSpan
	var spanned [][]footerSpan
	for _, group := range strings.Split(text, " · ") {
		group = truncateCells(group, width)
		gw := lipgloss.Width(group)
		if lineWidth > 0 && lineWidth+lipgloss.Width(" · ")+gw > width {
			lines, spanned = append(lines, line), append(spanned, current)
			line, lineWidth, current = "", 0, nil
		}
		if lineWidth > 0 {
			line += styles.hint.Render(" · ")
			lineWidth += lipgloss.Width(" · ")
		}
		current = append(current, footerSpan{lineWidth, lineWidth + gw, footerKey(group)})
		line += style(group)
		lineWidth += gw
	}
	if lineWidth > 0 || len(lines) == 0 {
		lines, spanned = append(lines, line), append(spanned, current)
	}
	if m.footerLog != nil {
		for i, l := range lines {
			*m.footerLog = append(*m.footerLog, footerLine{plain: ansi.Strip(l), spans: spanned[i]})
		}
	}
	return lines
}

// keyHintGroup styles one "key action" hint: the key (its first word) bold, the action after it faint.
func keyHintGroup(group string) string {
	key, action, ok := strings.Cut(group, " ")
	if !ok {
		return styles.key.Render(key)
	}
	return styles.key.Render(key) + styles.hint.Render(" "+action)
}
