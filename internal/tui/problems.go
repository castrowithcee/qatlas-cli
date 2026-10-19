package tui

import (
	"fmt"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// problemState holds the validation problems of the last refused save. A problem that belongs to a form row
// stands under that row until the row changes; every other one stands in a line of its own, named by the kind
// and name of the entry it concerns. Nothing here quotes a file path or a configuration key.
type problemState struct {
	// rows maps a form row label to the lines under that row, and at to the value the row held when they
	// were reported: a row whose value differs has been changed, which retires its lines.
	rows map[string][]string
	at   map[string]string
	// other are the lines of problems no open row can show.
	other []string
}

// problemKinds names the entry a problem concerns by the section it is reported under.
var problemKinds = map[string]string{
	config.SectionServices:      "service",
	config.SectionCredentials:   "credential",
	config.SectionConnections:   "connection",
	config.SectionDefaults:      "default",
	config.SectionVault:         "vault settings",
	config.SectionLogs:          "log settings",
	config.SectionProviderNotes: "provider note",
	config.SectionVersion:       "configuration",
}

// problemSections maps the section of an open form to the configuration section it edits.
var problemSections = map[section]string{
	sectionServices:    config.SectionServices,
	sectionCredentials: config.SectionCredentials,
	sectionConnections: config.SectionConnections,
	sectionDefaults:    config.SectionDefaults,
	sectionVault:       config.SectionVault,
}

// problemRows maps the configuration field of a problem to the label of the form row that edits it. This
// is the only place that knows the pairing; the form rows of every section are listed here, a field without
// a row is shown above the form instead. A key with an empty field stands for the entry as a whole.
var problemRows = map[string]map[string]string{
	config.SectionServices: {
		"": "name", "provider": providerLabel, "base_url": "base url",
	},
	config.SectionProviderNotes: {"": providerNoteLabel},
	config.SectionCredentials: {
		"": "name", "provider": providerLabel, "type": storageLabel, "description": "description",
		"fields": payloadNewLabel,
	},
	config.SectionConnections: {
		"": "name", "service": "service", "credential": "credential", "forward_secrets": forwardLabel,
		"description": "description", "permissions": "permissions", "target": targetsLabel,
		"targets": targetsLabel, "paths": pathsLabel, "tools": toolListLabel,
		"files.read": filesReadLabel, "files.write": filesWriteLabel,
	},
	config.SectionDefaults: {"": "domain"},
	config.SectionVault:    {"idle_timeout": "idle timeout", "admin_timeout": "admin timeout"},
}

// problemCodeRows places the problems of a connection that name no field, by the rule they report.
var problemCodeRows = map[string]string{
	config.CodeProviderMismatch:  "credential",
	config.CodeSecretRoleMissing: "credential",
	config.CodeTargetAndTargets:  targetsLabel,
}

// problemRow returns the label of the open form's row that shows p, or "" when none does.
func (m *Model) problemRow(p config.Problem, name string) string {
	if m.fields == nil || problemSections[m.section] != p.Section && p.Section != config.SectionProviderNotes {
		return ""
	}
	field := p.Field
	switch p.Section {
	case config.SectionProviderNotes:
		if m.section != sectionServices || p.Name != m.fieldValue(providerLabel) {
			return ""
		}
	case config.SectionDefaults:
		// The domain is the key below connections, not the entry name of the problem.
		if domain, ok := strings.CutPrefix(p.Field, "connections."); !ok || domain != name {
			return ""
		}
		return m.shownRow("connection")
	case config.SectionVault:
	default:
		if p.Name != name {
			return ""
		}
	}
	label := ""
	switch {
	case p.Section == config.SectionCredentials && strings.HasPrefix(field, "values"):
		label = strings.TrimPrefix(strings.TrimPrefix(field, "values"), ".")
		if label == "" {
			label = m.firstRoleRow()
		}
	case field == "":
		label = problemCodeRows[p.Code]
		if label == "" {
			label = problemRows[p.Section][""]
		}
		if label == "name" || label == "domain" {
			label = m.fields[0].label
		}
	default:
		label = problemRows[p.Section][field]
	}
	if label == toolListLabel && m.shownRow(label) == "" {
		label = toolsLabel
	}
	return m.shownRow(label)
}

// shownRow returns label when the open form has a visible row of that name.
func (m *Model) shownRow(label string) string {
	if f := m.field(label); f != nil && !f.hidden {
		return label
	}
	return ""
}

// firstRoleRow is the first secret role row of a credential form, "" when there is none.
func (m *Model) firstRoleRow() string {
	for _, f := range m.fields {
		if f.kind == fieldEnvName {
			return f.label
		}
	}
	return ""
}

// problemLine is the cause of a problem and, after a dash, the way to resolve it, both from the core.
func (m *Model) problemLine(p config.Problem) string {
	line := p.Detail()
	if hint := p.Hint(); hint != "" {
		line += " - " + hint
	}
	return m.redactor.Apply(line)
}

// problemOtherLine names the entry a problem concerns, for a problem without a row to stand under.
func (m *Model) problemOtherLine(p config.Problem) string {
	who := problemKinds[p.Section]
	name := p.Name
	if domain, ok := strings.CutPrefix(p.Field, "connections."); ok && p.Section == config.SectionDefaults {
		name = domain
	}
	if name != "" {
		who += " " + name
	}
	return who + ": " + m.problemLine(p)
}

// reportSaveError shows why a save or deletion was refused. Validation problems stand under the form rows
// they concern, or in a line of their own, and the status line only counts them; any other error keeps its
// text. form says whether the form of the entry is open, so that rows can be used.
func (m *Model) reportSaveError(err error, name string, form bool) {
	problems := config.Problems(err)
	if len(problems) == 0 {
		m.fail = m.redactor.Apply(err.Error())
		return
	}
	state := problemState{rows: map[string][]string{}, at: map[string]string{}}
	first := -1
	for _, p := range problems {
		label := ""
		if form {
			label = m.problemRow(p, name)
		}
		if label == "" {
			state.other = append(state.other, m.problemOtherLine(p))
			continue
		}
		state.rows[label] = append(state.rows[label], m.problemLine(p))
		state.at[label] = m.field(label).value()
		if first < 0 {
			for i := range m.fields {
				if m.fields[i].label == label {
					first = i
					break
				}
			}
		}
	}
	m.problems = state
	if first >= 0 {
		// The row may lie outside the visible part of a long form.
		m.focus = first
		m.applyFocus()
	}
	noun := "problems"
	if len(problems) == 1 {
		noun = "problem"
	}
	m.status, m.fail = "", fmt.Sprintf("%d %s; nothing was saved", len(problems), noun)
}

// fieldProblems are the lines of the refused save that still stand under a row: none once the row changed.
func (m *Model) fieldProblems(f field) []string {
	lines := m.problems.rows[f.label]
	if len(lines) == 0 || m.problems.at[f.label] != f.value() {
		return nil
	}
	return lines
}

// otherProblems renders the problems that stand in no row.
func (m *Model) otherProblems() string {
	var b strings.Builder
	for _, line := range m.problems.other {
		b.WriteString(m.wrapped(failStyle, "[!] "+line) + "\n")
	}
	return b.String()
}
