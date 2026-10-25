package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/castrowithcee/qatlas-cli/internal/helptopics"
)

// A form is drawn in one of three ways. With the section list, the list stands in a panel at the left and
// the rows of the focused section in a panel beside it; without it, the rows of every section follow each
// other under a heading. Either way a row is a label and a framed field, and a form that does not fit the
// terminal that way falls back to one-line fields without a frame (formFlat), which keep the focus visible.

// formSectionTerms relates a form section to the term whose first sentence is its lead.
var formSectionTerms = map[string]string{
	secService: "term.service", secCredential: "term.credential", secTargets: "term.target",
	secTools: "term.profile", secAccess: "term.effect",
}

// sectionLead is the line under the title of a section: the first sentence of the term the section is about.
func (m *Model) sectionLead(name string) string {
	id := formSectionTerms[name]
	if name == secGeneral && m.section == sectionConnections && m.wizard == nil {
		id = "term.connection"
	}
	if id == "" {
		return ""
	}
	term, ok := helptopics.LookupTerm(id)
	if !ok {
		return ""
	}
	text := termText(term)
	if i := strings.Index(text, ". "); i > 0 {
		text = text[:i+1]
	}
	return text
}

// formTitle is the title line of a form: the title at the left and, where an entry is edited, its name and
// state at the right.
func (m *Model) formTitle(what string) string {
	title := styles.title.Render(what)
	if m.editing == "" || m.wizard != nil || m.section.isPage() {
		return title
	}
	right := m.editing
	if m.section == sectionConnections {
		state := m.fieldValue(stateLabel)
		kind := statusDim
		if state == stateActive {
			kind = statusOK
		}
		right += "  " + statusMark(kind, valueText(stateLabel, state))
	}
	room := m.usable(0) - lipgloss.Width(what) - 2
	if lipgloss.Width(right) > room {
		return title
	}
	return title + strings.Repeat(" ", room+2-lipgloss.Width(right)) + right
}

// selectable reports whether the row is a choice: its field ends in the dropdown arrow.
func selectable(f field) bool {
	return (f.kind == fieldChoice || f.kind == fieldProvider) && len(f.choices) > 0
}

// arrowed ends the first line of value in the dropdown arrow, at the right edge of a field of width cells.
func arrowed(value string, width int) string {
	text := truncateCells(strings.Split(value, "\n")[0], max(width-2, 1))
	return padLine(text, max(width-2, 0)) + " " + dropdownArrow
}

// rowRoom is the room a value has beside the label of a row, inside its frame where it has one.
func (m *Model) rowRoom() int {
	if m.formBox {
		return m.formValueW - 4
	}
	return m.usable(m.formIndent())
}

// hintIndent is where the hint and the warnings under a row start.
func (m *Model) hintIndent() string {
	if m.formBox {
		return strings.Repeat(" ", m.formValueCol)
	}
	return "    "
}

// boxedRow draws a row as its label and a framed field; the label stands beside the first line of the field.
func (m *Model) boxedRow(active bool, f field, value string) string {
	inner := m.formValueW - 4
	if selectable(f) {
		value = arrowed(value, inner)
	}
	lines := strings.Split(render(lipgloss.NewStyle(), inner, value), "\n")
	box := panel("", lines, inner, len(lines), panelOpts{focused: active})
	style := lipgloss.NewStyle()
	if active {
		style = styles.active
	}
	blank := strings.Repeat(" ", m.formValueCol)
	for i := range box {
		if i == 1 {
			box[i] = style.Render(m.formHead(active, f.label)) + box[i]
		} else {
			box[i] = blank + box[i]
		}
	}
	return strings.Join(box, "\n")
}

// flatRow draws a row as its label and a one-line field on the field surface. A value that does not fit
// continues under itself.
func (m *Model) flatRow(active bool, f field, value string) string {
	head := m.formHead(active, f.label)
	indent, width := m.fit(strings.Repeat(" ", lipgloss.Width(head)))
	if selectable(f) {
		value = arrowed(value, width)
	}
	lines := strings.Split(render(lipgloss.NewStyle(), width, value), "\n")
	style := lipgloss.NewStyle()
	if active {
		style = styles.active
	}
	for i, l := range lines {
		l = flatField(l, width, active)
		if i == 0 {
			lines[i] = style.Render(head) + l
		} else {
			lines[i] = indent + l
		}
	}
	return strings.Join(lines, "\n")
}

// flatField is a line of a one-line field: padded to width on the field surface, in the accent colour while
// the row has the focus.
func flatField(text string, width int, active bool) string {
	text = padLine(clipCells(text, width), width)
	return fieldStyle(active).Render(keepBackground(text, styles.palette.surface))
}

// formBody draws the rows of the open form. With two or more sections it puts the list of the sections at
// the left where the terminal has room for it, and a heading above each section where it has not.
func (m *Model) formBody() string {
	entries := m.formSections()
	m.formLeft, m.sectionListWidth = 0, 0
	sections := m.sectionListFits(entries)
	steps := m.stepListFits(0)
	if steps && sections && !m.stepListFits(len(entries)+4) {
		// Steps and sections do not fit under each other: the sections go back to headings above their rows.
		sections = false
	}
	if !sections && !steps {
		return m.formRows(len(entries) >= 2, entries, "")
	}
	var list string
	width, stepsHeight := 0, 0
	if steps {
		list, width = m.stepList()
		stepsHeight = setupSteps + 2
	}
	if sections {
		sectionsList, sectionsWidth := m.sectionList(entries)
		for i, e := range entries {
			name := e.name
			m.addHit(0, m.formTop+1+stepsHeight+i, sectionsWidth, m.formTop+2+stepsHeight+i,
				func() tea.Cmd { return m.clickSection(name) })
		}
		if steps {
			list = lipgloss.JoinVertical(lipgloss.Left, list, sectionsList)
		} else {
			list = sectionsList
		}
		width = max(width, sectionsWidth)
	}
	full, top := m.width, m.formTop
	inner := full - width - 1 - 4
	var head []string
	current := ""
	if sections {
		current = m.currentSection()
		head = []string{styles.title.Render(sectionText(current))}
		if lead := m.sectionLead(current); lead != "" {
			head = append(head, styles.hint.Render(truncateDots(lead, inner)))
		}
		if !m.formFlat {
			head = append(head, "")
		}
	}
	m.width, m.formTop = inner, top+1+len(head)
	m.formLeft, m.sectionListWidth = width+1+2, width
	rows := m.formRows(!sections && len(entries) >= 2, entries, current)
	m.width, m.formTop = full, top
	lines := append(head, strings.Split(strings.TrimSuffix(rows, "\n"), "\n")...)
	panelLines := panel("", lines, inner, len(lines), panelOpts{})
	return lipgloss.JoinHorizontal(lipgloss.Top, list, " ", strings.Join(panelLines, "\n")) + "\n"
}

// formRows draws the rows of the open form in one label column, each with the hint, warning and problems that
// belong under it. headings puts the name of a section above its rows; only limits the rows to one section;
// the rows of a closed section are left out.
func (m *Model) formRows(headings bool, entries []sectionEntry, only string) string {
	var b strings.Builder
	m.formCol = m.labelColumn(m.fieldLabels()...)
	m.formBox = !m.formFlat
	if m.formBox {
		m.formValueCol = m.formIndent()
		_, m.formValueW = m.fitIn(m.usable(0), strings.Repeat(" ", m.formValueCol))
		m.formBox = m.formValueW >= 12
	}
	defer func() { m.formCol, m.formBox = 0, false }()
	closed := map[string]bool{}
	for _, e := range entries {
		closed[e.name] = e.closed
	}
	last := ""
	for i, f := range m.fields {
		if f.hidden || (only != "" && f.section != only) {
			continue
		}
		outside := m.window != nil && (i < m.window[0] || i > m.window[1])
		// Headings are the first thing given up when the form is cut to the rows around the focus.
		if headings && m.window == nil && f.section != "" && f.section != last && !outside {
			last = f.section
			name := f.section
			y := m.formTop + strings.Count(b.String(), "\n")
			m.addHit(m.formLeft, y, m.formLeft+m.width, y+1, func() tea.Cmd { return m.clickSection(name) })
			b.WriteString(m.sectionHeading(sectionEntry{name: f.section, closed: closed[f.section]}) + "\n")
		}
		if f.section != "" && closed[f.section] {
			continue
		}
		if outside {
			if i == m.window[0]-1 || i == m.window[1]+1 {
				b.WriteString(styles.hint.Render("  …") + "\n")
			}
			continue
		}
		var row string
		if m.formBox {
			row = m.boxedRow(i == m.focus, f, m.renderField(f, i == m.focus))
		} else {
			row = m.flatRow(i == m.focus, f, m.renderField(f, i == m.focus))
		}
		if !f.readOnly {
			y, index := m.formTop+strings.Count(b.String(), "\n"), i
			m.addHit(m.formLeft, y, m.formLeft+m.width, y+strings.Count(row, "\n")+1,
				func() tea.Cmd { return m.clickField(index) })
		}
		b.WriteString(row + "\n")
		// A read-only field can never be the one focused, so its hint would never show; it explains
		// itself in its own value instead (see renderField), the way a read-only tool list already does.
		if i == m.focus && !f.readOnly {
			if hint := m.fieldHint(f); hint != "" {
				line := m.hintLine(hint, m.moreBehind(f, hint))
				m.hitInfoMark(line, m.formTop+strings.Count(b.String(), "\n"), m.formLeft, func() tea.Cmd {
					m.openInfo()
					return nil
				})
				b.WriteString(line + "\n")
			}
		}
		// A warning is not the hint of the field, and its cause (a validation problem, a wildcard target,
		// an idle connection) matters whether or not the field is the one focused, so it always stands.
		if warning := m.fieldWarning(f); warning != "" {
			b.WriteString(m.indentedWith(styles.warning, warningLine(warning)) + "\n")
		}
		// A refused save names its cause and its resolution under the row it concerns. The symbol, not
		// only the colour, marks the line as an error.
		for _, line := range m.fieldProblems(f) {
			b.WriteString(m.indentedWith(styles.fail, "[!] "+line) + "\n")
		}
	}
	return b.String()
}
