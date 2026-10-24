package tui

import (
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The sections of a form. A form with two or more of them shows a list of the sections at its left, or,
// where the terminal is too narrow for that, a heading above the rows of each. The order here is the order
// the rows stand in: rows are grouped by section when a form is built, so tab walks the sections in the
// order the list shows them.
const (
	secGeneral    = "General"
	secService    = "Service"
	secCredential = "Credential"
	secTargets    = "Targets"
	secTools      = "Tools"
	secAccess     = "Access"
	secAdvanced   = "Advanced"
	secKeys       = "Keys"
)

var sectionRank = map[string]int{
	secGeneral: 0, secService: 1, secCredential: 2, secTargets: 3, secTools: 4, secAccess: 5, secAdvanced: 6, secKeys: 7,
}

// connectionSections places the rows of a connection by their label. Whatever is technical and rarely
// changed lives under Advanced.
var connectionSections = map[string]string{
	"name": secGeneral, "description": secGeneral, stateLabel: secGeneral,
	providerLabel: secGeneral, "service": secService,
	"credential": secCredential,
	targetsLabel: secTargets, pathsLabel: secTargets,
	profileLabel: secTools, toolListLabel: secTools,
	accessLabel: secAccess, effectsLabel: secAccess,
	filesReadLabel: secAdvanced, filesWriteLabel: secAdvanced, activeForLabel: secAdvanced, forwardLabel: secAdvanced,
}

const (
	// sectionListMinWidth is the working width from which the sections stand as a list at the left of the
	// rows. Below it each section is a heading above its rows, so the rows keep the room their hints need.
	sectionListMinWidth = 100
)

// sectionOfRow is the section of a row of the form being built: step is the step of the guided setup, or -1
// outside it. A form without sections gives "".
func (m *Model) sectionOfRow(f field, step int) string {
	if step >= 0 {
		switch step {
		case stepProvider, stepService:
			return secService
		case stepCredential:
			return secCredential
		}
		return connectionSection(f.label)
	}
	switch m.section {
	case sectionConnections:
		return connectionSection(f.label)
	case sectionCredentials:
		if f.label == "name" || f.kind == fieldProvider {
			return secGeneral
		}
		return secCredential
	case sectionGeneral:
		if f.kind == fieldKey {
			return secKeys
		}
		return secGeneral
	}
	return ""
}

// entryKey is the row that names the entry of the form (name, or domain): the first row of every form but a
// connection form, which leads with its provider.
func (m *Model) entryKey() field {
	for _, f := range m.fields {
		if f.label == "name" || f.label == "domain" {
			return f
		}
	}
	return m.fields[0]
}

func connectionSection(label string) string {
	if s, ok := connectionSections[label]; ok {
		return s
	}
	return secGeneral
}

// groupSections gives every row of a newly built form its section and gathers the rows of a section, keeping
// their order within it.
func (m *Model) groupSections(fields []field, step int) []field {
	for i := range fields {
		fields[i].section = m.sectionOfRow(fields[i], step)
	}
	// A connection form leads with its provider: it limits the services and credentials to one system.
	lead := func(f field) int {
		if step < 0 && m.section == sectionConnections && f.label == providerLabel {
			return 0
		}
		return 1
	}
	sort.SliceStable(fields, func(a, b int) bool {
		if ra, rb := sectionRank[fields[a].section], sectionRank[fields[b].section]; ra != rb {
			return ra < rb
		}
		return lead(fields[a]) < lead(fields[b])
	})
	return fields
}

// inSection returns f placed in the given section; a row built anew in place of an old one keeps the
// section of the old one this way.
func (f field) inSection(section string) field {
	f.section = section
	return f
}

// hasValue reports whether the row holds something other than what a fresh row starts with. The first choice
// of a choice row is its starting value, except the one that stands for the limit a saved connection keeps.
func (f field) hasValue() bool {
	switch f.kind {
	case fieldChoice, fieldProvider:
		return f.keep != "" || f.index > 0
	case fieldToolList:
		return len(f.marked()) > 0
	case fieldTargets, fieldPaths:
		return len(f.entries) > 0
	case fieldMultiChoice:
		return f.value() != ""
	}
	return f.value() != ""
}

// sectionClosed reports whether the rows of a section are folded away. Only Advanced folds, and only while
// no row in it holds a value or a problem, the focus is not in it and the user has not jumped into it.
func (m *Model) sectionClosed(name string) bool {
	if name != secAdvanced || m.sectionPinned[name] {
		return false
	}
	for i, f := range m.fields {
		if f.section != name || f.hidden {
			continue
		}
		if i == m.focus || f.hasValue() || len(m.fieldProblems(f)) > 0 {
			return false
		}
	}
	return true
}

// skipped reports whether focus steps over row i: hidden, or inside a closed section.
func (m *Model) skipped(i int) bool {
	f := m.fields[i]
	return f.hidden || (f.section != "" && m.sectionClosed(f.section))
}

// sectionEntry is one entry of the section list.
type sectionEntry struct {
	name   string
	closed bool
}

// formSections are the sections that have a shown row (a section whose rows are all hidden is not listed), in
// the order of the form.
func (m *Model) formSections() []sectionEntry {
	var out []sectionEntry
	seen := map[string]bool{}
	for _, f := range m.fields {
		if f.hidden || f.section == "" || seen[f.section] {
			continue
		}
		seen[f.section] = true
		out = append(out, sectionEntry{f.section, m.sectionClosed(f.section)})
	}
	return out
}

func (e sectionEntry) text() string {
	if e.closed {
		return sectionText(e.name) + tr(tSectionClosedMark)
	}
	return sectionText(e.name)
}

// currentSection is the section of the focused row.
func (m *Model) currentSection() string {
	if m.focus < 0 || m.focus >= len(m.fields) {
		return ""
	}
	return m.fields[m.focus].section
}

// sectionList draws the list of the sections and returns it with its width. The section of the focused row
// leads with ">" like a focused row does.
func (m *Model) sectionList(entries []sectionEntry) (string, int) {
	current := m.currentSection()
	width := 0
	for _, e := range entries {
		width = max(width, 2+lipgloss.Width(e.text()))
	}
	lines := make([]string, len(entries))
	for i, e := range entries {
		line := "  " + e.text()
		style := lipgloss.NewStyle()
		switch {
		case e.name == current:
			line, style = "> "+e.text(), styles.active
		case e.closed:
			style = styles.hint
		}
		lines[i] = style.Render(line + strings.Repeat(" ", width-lipgloss.Width(line)))
	}
	return strings.Join(lines, "\n"), width
}

// sectionListFits reports whether the sections stand as a list beside the rows: there are two or more of them
// and the workspace has the width and the height for the list.
func (m *Model) sectionListFits(entries []sectionEntry) bool {
	return len(entries) >= 2 && m.width >= sectionListMinWidth && m.height >= len(entries)+6
}

// sectionHeading is the heading above the rows of a section where the list does not fit.
func (m *Model) sectionHeading(e sectionEntry) string {
	if e.closed {
		return styles.title.Render(sectionText(e.name)) + styles.hint.Render(tr(tSectionClosedMark))
	}
	return styles.title.Render(sectionText(e.name))
}

// jumpSection moves the focus to the first row of the previous or next section, opening it if it is closed.
// It is the way to a section of the list; tab and shift+tab still go row by row.
func (m *Model) jumpSection(by int) {
	entries := m.formSections()
	if len(entries) < 2 {
		return
	}
	at := 0
	for i, e := range entries {
		if e.name == m.currentSection() {
			at = i
		}
	}
	next := min(max(at+by, 0), len(entries)-1)
	if next == at {
		return
	}
	m.enterSection(entries[next].name)
}

// enterSection moves the focus to the first row of the named section that takes input, opening the section if
// it is closed.
func (m *Model) enterSection(name string) {
	m.trimFields()
	if m.sectionPinned == nil {
		m.sectionPinned = map[string]bool{}
	}
	m.sectionPinned[name] = true
	target := -1
	for i, f := range m.fields {
		if f.section != name || f.hidden {
			continue
		}
		if target < 0 {
			target = i
		}
		if !f.readOnly {
			target = i
			break
		}
	}
	if target >= 0 {
		m.focus = target
		m.applyFocus()
	}
}

// fieldLabels are the labels of the rows a form shows, which set its label column. A row of a closed section
// counts too, so opening the section moves no value.
func (m *Model) fieldLabels() []string {
	var labels []string
	for _, f := range m.fields {
		if !f.hidden {
			labels = append(labels, f.label)
		}
	}
	return labels
}
