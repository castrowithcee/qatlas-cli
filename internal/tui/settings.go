package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

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

// iconsTest explains the nerdfont value with the glyphs themselves, taken from areaIcons.
func iconsTest() string { return fmt.Sprintf(tr(tIconsTest), strings.Join(areaIcons[:], " ")) }

// iconsDetail is the long explanation of the icons preference.
func iconsDetail() string { return tr(tIconsDetail) + ". " + iconsTest() }

// Labels of the General form.
const (
	themeLabel = "theme"
	startLabel = "start view"
	iconsLabel = "icons"
	langLabel  = "language"
	mouseLabel = "mouse"
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
		return tr(tPageGeneral)
	case sectionTransfer:
		return tr(tPageTransfer)
	case sectionUpdate:
		return tr(tPageUpdate)
	case sectionSelftest:
		return tr(tPageSelftest)
	}
	return s.title()
}

// openPageForm builds the form of the active settings page fresh, so it shows what is true now.
func (m *Model) openPageForm() tea.Cmd {
	if m.section == sectionVault {
		return m.openVaultForm()
	}
	m.editing = ""
	m.keyCapture = false
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
		areas = append(areas, strings.ToLower(a.key()))
	}
	start := prefs.Start
	if _, ok := areaByName(start); !ok {
		start = uiprefs.DefaultStart
	}
	language := prefs.Language
	if language == "" {
		language = uiprefs.LanguageEnglish
	}
	current, _ := themeNamed(prefs.Theme)
	mouse := uiprefs.ChoiceMouseOn
	if !prefs.MouseEnabled() {
		mouse = uiprefs.ChoiceMouseOff
	}
	fields := []field{
		choiceField(themeLabel, themeNames(), current.name).
			withHint(tr(tThemeHint)).
			withDetail(tr(tThemeDetail)),
		choiceField(startLabel, areas, start).
			withHint(tr(tStartHint)),
		choiceField(iconsLabel, []string{uiprefs.IconsNone, uiprefs.IconsNerdFont}, prefs.Icons).
			withHint(tr(tIconsHint)).
			withDetail(iconsDetail()),
		choiceField(langLabel, []string{uiprefs.LanguageEnglish, uiprefs.LanguageGerman, uiprefs.LanguageSystem}, language).
			withHint(tr(tLanguageHint)).
			withDetail(tr(tLanguageDetail)),
		choiceField(mouseLabel, []string{uiprefs.ChoiceMouseOn, uiprefs.ChoiceMouseOff}, mouse).
			withHint(tr(tMouseHint)).
			withDetail(tr(tMouseDetail)),
	}
	return m.groupSections(append(fields, keyFields(prefs.Keys)...), -1)
}

// saveGeneral stores the preferences on this device. It needs neither an admin session nor the vault's
// passphrase: the file is not part of the configuration. leave says the save came from the leave question,
// which goes on to the tab line afterwards.
func (m *Model) saveGeneral(leave bool) tea.Cmd {
	next := uiprefs.Prefs{
		Theme: m.fieldValue(themeLabel), Start: m.fieldValue(startLabel), Icons: m.fieldValue(iconsLabel),
		Language: m.fieldValue(langLabel), MouseOff: m.fieldValue(mouseLabel) == uiprefs.ChoiceMouseOff, Keys: m.keysOfForm(),
	}
	if err := next.Keys.Validate(); err != nil {
		m.status, m.fail = "", err.Error()
		return nil
	}
	if m.savePrefs == nil {
		m.status, m.fail = "", tr(tSettingsCannotSaveHere)
		return nil
	}
	if err := m.savePrefs(next); err != nil {
		m.status, m.fail = "", tr(tSettingsSaveFailed, m.redactor.Apply(err.Error()))
		return nil
	}
	before, _ := themeNamed(m.prefs.Theme)
	m.prefs = next
	applyTheme(next.Theme)
	// Switching to the terminal theme asks the terminal for its colours again; until it answers the theme
	// draws without fills.
	var query tea.Cmd
	if before.name != next.Theme {
		query = terminalColourQuery(next.Theme)
	}
	applyKeys(next.Keys)
	// The language applies at once: the messages and the form below are drawn in it.
	setLanguage(next.Language)
	m.clearMessages()
	m.status = tr(tSettingsSaved)
	// The mouse mode of the terminal follows the preference with the next view (see mouseMode).
	if leave {
		return tea.Batch(query, m.showList())
	}
	return tea.Batch(query, m.openPageForm())
}

// pageAction is a row that runs one of the existing flows at once on enter.
func pageAction(action, label, hint, detail string) field {
	return vaultActionField(action, label, hint, detail)
}

func (m *Model) transferFields() []field {
	return []field{
		pageAction(pageActionExport, "export", tr(tExportHint), tr(tExportDetail)),
		pageAction(pageActionImport, "import", tr(tImportHint), tr(tImportDetail)),
	}
}

func (m *Model) updateFields() []field {
	status := tr(tUpdateNoneKnown)
	if reason := m.updateUnavailable(); reason != "" {
		status = reason
	}
	if m.updateOffered() {
		status = tr(tUpdateAvailableRow, m.release.Current, m.release.Latest)
	}
	return []field{
		textField("version", "qatlas "+m.runningVersion(), true),
		textField("release", status, true),
		pageAction(pageActionUpdate, "install update", tr(tUpdateInstallHint), tr(tUpdateInstallDetail)),
	}
}

func (m *Model) selftestFields() []field {
	var fields []field
	for _, sc := range m.selftest.list() {
		fields = append(fields, pageAction(pageActionSelftest+sc.Name, sc.Name, tr(tSelftestHint), tr(tSelftestDetail, sc.Summary)))
	}
	if len(fields) == 0 {
		fields = append(fields, textField("selftest", tr(tSelftestNone), true))
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
