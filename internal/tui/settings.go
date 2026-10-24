package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
)

// areaIcons are the Nerd Font glyphs (Font Awesome range) shown before an area's name when the icons
// preference is nerdfont: home, link, history, key, book, cog. The name always stays beside the icon.
var areaIcons = [areaCount]string{
	areaOverview:    "",
	areaConnections: "",
	areaActivity:    "",
	areaAccess:      "",
	areaLibrary:     "",
	areaSettings:    "",
}

// Labels of the General form.
const (
	themeLabel = "color scheme"
	startLabel = "start view"
	iconsLabel = "icons"
)

// Actions of the Transfer, Update and Self-test pages, run by a fieldVaultAction row.
const (
	pageActionExport = "export"
	pageActionImport = "import"
	pageActionUpdate = "update"
	// pageActionSelftest is followed by the scenario name.
	pageActionSelftest = "selftest:"
)

// isPage reports whether s is a settings page: a form without a list, opened directly.
func (s section) isPage() bool {
	switch s {
	case sectionVault, sectionGeneral, sectionTransfer, sectionUpdate, sectionSelftest:
		return true
	}
	return false
}

// pageTitle is the heading of the form of a settings page.
func (s section) pageTitle() string {
	switch s {
	case sectionGeneral:
		return "General settings"
	case sectionTransfer:
		return "Export and import"
	case sectionUpdate:
		return "Update"
	case sectionSelftest:
		return "Self-test"
	}
	return s.title()
}

// openPageForm builds the form of the active settings page fresh, so it shows what is true now.
func (m *Model) openPageForm() tea.Cmd {
	if m.section == sectionVault {
		return m.openVaultForm()
	}
	m.editing = ""
	m.screen = screenForm
	switch m.section {
	case sectionGeneral:
		m.fields = m.generalFields()
	case sectionTransfer:
		m.fields = m.transferFields()
	case sectionUpdate:
		m.fields = m.updateFields()
	default:
		m.fields = m.selftestFields()
	}
	m.focus = max(m.firstEditable(), 0)
	m.applyFocus()
	m.pristine = m.formState()
	return nil
}

// generalFields are the rows of the General form: finite choices only, so nothing is typed.
func (m *Model) generalFields() []field {
	prefs := m.prefs
	if prefs == (uiprefs.Prefs{}) {
		prefs = uiprefs.Default()
	}
	var areas []string
	for a := area(0); a < areaCount; a++ {
		areas = append(areas, strings.ToLower(a.title()))
	}
	start := prefs.Start
	if _, ok := areaByName(start); !ok {
		start = uiprefs.DefaultStart
	}
	return []field{
		choiceField(themeLabel, []string{uiprefs.ThemeAuto, uiprefs.ThemeLight, uiprefs.ThemeDark}, prefs.Theme).
			withHint("auto follows the terminal's background; NO_COLOR turns every colour off whatever is chosen here"),
		choiceField(startLabel, areas, start).
			withHint("the area the editor opens on the next time it starts"),
		choiceField(iconsLabel, []string{uiprefs.IconsNone, uiprefs.IconsNerdFont}, prefs.Icons).
			withHint("nerdfont puts an icon before each area's name and needs a Nerd Font in the terminal; the name always stays"),
	}
}

// saveGeneral stores the preferences on this device. It needs neither an admin session nor the vault's
// passphrase: the file is not part of the configuration. leave says the save came from the leave question,
// which goes on to the tab line afterwards.
func (m *Model) saveGeneral(leave bool) tea.Cmd {
	next := uiprefs.Prefs{
		Theme: m.fieldValue(themeLabel), Start: m.fieldValue(startLabel), Icons: m.fieldValue(iconsLabel),
	}
	if m.savePrefs == nil {
		m.status, m.fail = "", "the settings cannot be saved here"
		return nil
	}
	if err := m.savePrefs(next); err != nil {
		m.status, m.fail = "", "cannot save the settings: "+m.redactor.Apply(err.Error())
		return nil
	}
	m.prefs = next
	applyColorScheme(next.Theme)
	m.clearMessages()
	m.status = "Saved; the start view applies the next time the editor starts"
	if leave {
		return m.showList()
	}
	return m.openPageForm()
}

// pageAction is a row that runs one of the existing flows at once on enter.
func pageAction(action, label, hint string) field {
	return vaultActionField(action, label, hint)
}

func (m *Model) transferFields() []field {
	return []field{
		pageAction(pageActionExport, "export",
			"enter chooses what to write into an export file, optionally with secrets; needs the admin session"),
		pageAction(pageActionImport, "import",
			"enter reads an exported file and shows what it would change before anything is written; needs the admin session"),
	}
}

func (m *Model) updateFields() []field {
	status := "no newer release is known"
	if reason := m.updateUnavailable(); reason != "" {
		status = reason
	}
	if m.updateOffered() {
		status = "update available " + m.release.Current + " → " + m.release.Latest
	}
	return []field{
		textField("release", status, true),
		pageAction(pageActionUpdate, "install update", "enter asks before the newer release is installed"),
	}
}

func (m *Model) selftestFields() []field {
	var fields []field
	for _, sc := range m.selftest.list() {
		fields = append(fields, pageAction(pageActionSelftest+sc.Name, sc.Name, "enter measures: "+sc.Summary))
	}
	if len(fields) == 0 {
		fields = append(fields, textField("selftest", "this build cannot start the selftest", true))
	}
	return fields
}

// runPageAction starts the flow of a Transfer, Update or Self-test row. It reports false for an action that
// is not one of theirs. The flows keep their own rules, among them the admin session and updateBlocker.
func (m *Model) runPageAction(action string) (tea.Cmd, bool) {
	switch {
	case action == pageActionExport:
		return m.askExport(), true
	case action == pageActionImport:
		return m.askImport(), true
	case action == pageActionUpdate:
		if reason := m.updateUnavailable(); reason != "" {
			m.clearMessages()
			m.status = reason
			return nil, true
		}
		m.askUpdate()
		return nil, true
	case strings.HasPrefix(action, pageActionSelftest):
		return m.startSelftest(strings.TrimPrefix(action, pageActionSelftest)), true
	}
	return nil, false
}
