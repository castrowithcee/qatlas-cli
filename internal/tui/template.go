package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// Reusable entries can be duplicated with p on a list, and chosen as the template of a new entry with n,
// and the guided setup can start from a configured connection. Copying means filling the ordinary form of a
// new entry: nothing is saved before the form is, and saving stays the managing action every form is (see
// submit). Secret values are never part of an entry, so none is ever copied; a credential copied from a
// keyring or vault credential keeps only where its secrets are kept.

// Rows of the template question.
const (
	templateEmpty    = "empty"
	templateExisting = "from existing %s..."
)

// templateChoice is the state of screenTemplate: the question first, then the picker over the entries.
type templateChoice struct {
	// from is the screen to return to when the question is cancelled or an admin dialog is cancelled.
	from screen
	// setup is true when the question belongs to the guided setup instead of the active section.
	setup bool
	// picking is true once "from existing" was chosen and the entries are searched.
	picking bool
	// index is the row of the question: 0 for empty, 1 for from existing.
	index int
}

// templateSection reports whether a section holds reusable entries that can be copied.
func (m *Model) templateSection(s section) bool {
	switch s {
	case sectionServices, sectionCredentials, sectionConnections, sectionTokens:
		return true
	}
	return false
}

// templateNames are the entries the picker offers: those of the section, or the connections for the setup.
func (m *Model) templateNames(setup bool) []string {
	if setup {
		return m.entryNames(sectionConnections)
	}
	return m.entryNames(m.section)
}

// templateOffered reports whether n asks for a template first. Without an entry to copy there is nothing
// to ask.
func (m *Model) templateOffered() bool {
	return m.templateSection(m.section) && len(m.templateNames(false)) > 0
}

// askTemplate opens the template question of the active section, or of the guided setup.
func (m *Model) askTemplate(setup bool) {
	m.template = &templateChoice{from: m.screen, setup: setup}
	m.templateList.reset(m.templateNames(setup))
	m.screen = screenTemplate
	m.clearMessages()
}

// askSetupTemplate is c: the guided setup starts at once without a connection to copy, and otherwise asks
// whether to start empty or from an existing connection first.
func (m *Model) askSetupTemplate() {
	if len(m.cfg.Connections) == 0 {
		m.startSetup()
		return
	}
	m.clearMessages()
	if len(m.cfg.Providers()) == 0 {
		m.fail = "this build registers no provider"
		return
	}
	m.askTemplate(true)
}

// templateFromScreen is where the question was opened from, never a stale template screen.
func (m *Model) templateFromScreen() screen {
	if m.template != nil && m.template.from != screenTemplate {
		return m.template.from
	}
	return screenList
}

// duplicateSelected is p on a list: the form of a new entry filled from the marked one.
func (m *Model) duplicateSelected() tea.Cmd {
	if !m.templateSection(m.section) {
		return nil
	}
	name, ok := m.selected()
	if !ok {
		return nil
	}
	return m.openCopy(name)
}

// openCopy opens the form of a new entry filled from source. A new agent token is a managing action from
// the start, so it asks for the admin session first, like n does.
func (m *Model) openCopy(source string) tea.Cmd {
	if m.section == sectionTokens {
		return m.requireAdmin(func() tea.Cmd { return m.openEntry("", source) })
	}
	return m.openEntry("", source)
}

// copyName is the name suggested for a copy of source: source-copy, or source-copy-N with the lowest N from
// 2 on that is still free in the active section.
func (m *Model) copyName(source string) string {
	taken := map[string]bool{}
	for _, name := range m.entryNames(m.section) {
		taken[name] = true
	}
	return freeCopyName(source, taken)
}

func freeCopyName(source string, taken map[string]bool) string {
	name := source + "-copy"
	for n := 2; taken[name]; n++ {
		name = source + "-copy-" + strconv.Itoa(n)
	}
	return name
}

// sourceEntry is the entry whose values the form being applied carries: the copied one for a new entry
// filled from another, else the entry itself.
func (m *Model) sourceEntry(name string) string {
	if m.editing == "" && m.copyFrom != "" {
		return m.copyFrom
	}
	return name
}

// copyNote is the line under a copied form.
func (m *Model) copyNote() string {
	note := "everything from " + m.copyFrom + " except the name, ready to adjust before saving"
	switch m.section {
	case sectionCredentials:
		note = "where " + m.copyFrom + " keeps its secrets is copied, never a secret value: save this " +
			"credential, then store its secrets"
	case sectionTokens:
		note = "vorbilder and a future expiry from " + m.copyFrom + "; the new token gets its own value"
	}
	return note
}

// templateText is what the picker searches and shows of one entry.
func (m *Model) templateText(name string) string {
	if m.template != nil && m.template.setup {
		conn := m.cfg.Connections[name]
		return name + "  " + conn.Service
	}
	return m.describe(name)
}

// templateNoun is the entry the question talks about.
func (m *Model) templateNoun() string {
	if m.template != nil && m.template.setup {
		return "connection"
	}
	return m.section.entry()
}

func (m *Model) updateTemplate(key tea.KeyMsg) tea.Cmd {
	t := m.template
	if t == nil {
		m.screen = screenList
		return nil
	}
	if !t.picking {
		switch key.String() {
		case "ctrl+c":
			return m.quit()
		case "esc":
			m.template = nil
			m.screen = t.from
		case "up", "k", "down", "j":
			t.index = 1 - t.index
		case "enter":
			if t.index == 1 {
				t.picking = true
				m.templateList.reset(m.templateNames(t.setup))
				m.templateList.startFilter()
				return nil
			}
			m.template = nil
			m.screen = t.from
			if t.setup {
				m.startSetup()
				return nil
			}
			return m.newEmptyEntry()
		}
		return nil
	}
	switch key.String() {
	case "ctrl+c":
		return m.quit()
	case "esc":
		t.picking = false
		m.templateList.stopFilter()
	case "enter":
		source, ok := m.templateList.selected()
		if !ok {
			return nil
		}
		m.template = nil
		m.screen = t.from
		if t.setup {
			m.startSetupFrom(source)
			return nil
		}
		return m.openCopy(source)
	case "up":
		m.templateList.move(-1)
	case "down":
		m.templateList.move(1)
	case "pgup", "pgdown", "home", "end":
		start, end := m.templateWindow()
		m.templateList.page(key.String(), start, end)
	default:
		return m.templateList.updateFilter(key)
	}
	return nil
}

func (m *Model) templateView() string {
	if m.template == nil {
		return ""
	}
	if m.template.picking {
		header, footer := m.templateFrame()
		var b strings.Builder
		b.WriteString(header)
		start, end := m.templateWindow()
		for i := start; i < end; i++ {
			b.WriteString(m.templateRow(i) + "\n")
		}
		b.WriteString(footer)
		return b.String()
	}
	var b strings.Builder
	title := "New " + m.templateNoun()
	if m.template.setup {
		title = "Guided setup"
	}
	b.WriteString(m.wrapped(titleStyle, title) + "\n\n")
	rows := []string{templateEmpty, fmt.Sprintf(templateExisting, m.templateNoun())}
	for i, row := range rows {
		mark, style := "( ) ", hintStyle
		if i == m.template.index {
			mark, style = "(•) ", activeStyle
		}
		b.WriteString(m.indentedHanging(style, mark, row) + "\n")
	}
	b.WriteString(m.keyHint("up/down move · enter choose · esc cancel") + m.notes())
	return b.String()
}

// indentedHanging draws a row behind a two-space margin and its marker, wrapping under the text.
func (m *Model) indentedHanging(style lipgloss.Style, marker, text string) string {
	head, width := m.fit("  " + marker)
	lines := strings.Split(render(style, width, text), "\n")
	for i, l := range lines {
		if i == 0 {
			lines[i] = head + l
			continue
		}
		lines[i] = strings.Repeat(" ", len(head)) + l
	}
	return strings.Join(lines, "\n")
}

func (m *Model) templateFrame() (string, string) {
	l := &m.templateList
	shown, total := len(l.matches), len(l.all)
	var head strings.Builder
	head.WriteString(m.wrapped(titleStyle, fmt.Sprintf("New %s, from existing  %d/%d (%d total)",
		m.templateNoun(), min(l.cursor+1, shown), shown, total)) + "\n")
	head.WriteString(m.searchLine(l, "search: ") + "\n")
	if shown == 0 {
		head.WriteString(m.wrapped(hintStyle,
			fmt.Sprintf("No entry matches %q. esc goes back.", l.query())) + "\n")
	}
	return head.String(), m.keyHint("type to search · up/down move · enter choose · esc back") + m.notes()
}

func (m *Model) templateRow(i int) string {
	_, width := m.fit("  ")
	return m.listRow(i == m.templateList.cursor, i%2 == 1,
		truncateCells(m.templateList.text(m.templateList.matches[i]), width))
}

func (m *Model) templateWindow() (int, int) {
	header, footer := m.templateFrame()
	return m.windowIn(&m.templateList, header, footer, m.templateRow)
}

// startSetupFrom starts the guided setup past its provider step, on the provider of the named connection,
// with the rows of the later steps filled from it. The setup still writes nothing before its last step, and
// the last step is still saved through requireAdmin.
func (m *Model) startSetupFrom(source string) {
	m.clearMessages()
	conn, ok := m.cfg.Connections[source]
	provider := m.cfg.Services[conn.Service].Provider
	if !ok || provider == "" {
		m.fail = fmt.Sprintf("connection %s names no provider to start from", source)
		return
	}
	m.stopTest()
	m.testName = ""
	m.wizard = &setup{provider: provider, template: source}
	m.wizard.pages = [][]field{m.setupPage(stepProvider)}
	m.setupShow(stepService)
}

// setupPrefill fills the rows of a step the setup has just built from the connection it started from. The
// credential row only names an existing credential: the setup copies no secret.
func (m *Model) setupPrefill(step int, page []field) {
	w := m.wizard
	conn, ok := m.cfg.Connections[w.template]
	if !ok {
		return
	}
	switch step {
	case stepService:
		if slices.Contains(page[0].choices, conn.Service) {
			page[0] = choiceField("service", page[0].choices, conn.Service).withHint(page[0].hint)
		}
	case stepCredential:
		if slices.Contains(page[0].choices, conn.Credential) {
			page[0] = choiceField("credential", page[0].choices, conn.Credential).withHint(page[0].hint)
		}
	case stepScope:
		taken := map[string]bool{}
		for name := range m.cfg.Connections {
			taken[name] = true
		}
		page[0].input.SetValue(freeCopyName(w.template, taken))
		page[1].entries = slices.Clone(conn.TargetValues())
		page[2].input.SetValue(conn.Description)
	case stepPermissions:
		provider := w.provider
		permissions := permissionField(m.permissionChoicesFor(provider, conn.Permissions), conn.Permissions)
		permissions.hint = m.permissionHint(provider)
		page[1] = permissions
		copy(page[2:], m.toolFields(provider, slices.Clone(conn.Tools)))
	}
}

// setupTemplateExtras are what the setup has no row for and copies from its template unchanged: the paths
// and local file lists of the connection.
func (m *Model) setupTemplateExtras() ([]string, config.Files) {
	source, ok := m.cfg.Connections[m.wizard.template]
	if !ok {
		return nil, config.Files{}
	}
	return slices.Clone(source.Paths), config.Files{Read: slices.Clone(source.Files.Read), Write: slices.Clone(source.Files.Write)}
}
