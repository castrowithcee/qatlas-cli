// Package tui is a keyboard-driven editor for the local configuration. It is an interface on the shared
// configuration core: it holds no schema knowledge, no provider knowledge, and no validation of its own,
// and it saves only through the validating, atomic store.
//
// A secret is taken in here and handed straight to package secret, which owns the one resolution path. It
// is typed masked, it is never shown, never read back, and never written to the configuration. What the
// editor displays about a secret is where it resolves from, never what it is.
package tui

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Tester checks one configured connection and reports the stable outcome class. The editor holds no
// provider knowledge of its own; the caller supplies this function.
type Tester func(ctx context.Context, connection string) (provider.Class, error)

// testDoneMsg carries the outcome of a connection test back into the event loop.
type testDoneMsg struct {
	id    int
	class provider.Class
	err   string
}

type section int

const (
	sectionServices section = iota
	sectionCredentials
	sectionConnections
	sectionDefaults
	sectionCount
)

func (s section) title() string {
	return [...]string{"Services", "Credentials", "Connections", "Defaults"}[s]
}

type screen int

const (
	screenMenu screen = iota
	screenList
	screenForm
	screenConfirm
	screenPlaintextConfirm
	screenSecret
	// screenPicker is the searchable list of one choice row of the form, over the form it was opened from.
	screenPicker
)

type fieldKind int

const (
	fieldText fieldKind = iota
	fieldChoice
	fieldMultiChoice
	// fieldEnvName holds the NAME of an environment variable, for a credential of type env.
	fieldEnvName
	// fieldSecret is the row of one role of a keyring credential. It holds no value at all: it shows where
	// the role resolves from and carries the keys that store and remove the secret.
	fieldSecret
)

type field struct {
	label    string
	kind     fieldKind
	input    textinput.Model
	choices  []string
	index    int
	selected map[string]bool
	// hint says in one line what this field expects. It stays general: every rule belongs to the
	// configuration core, and the editor must not grow schema knowledge of its own.
	hint string
	// readOnly marks the name of an existing entry. Renaming would silently break every reference to it,
	// so an entry is changed in place or deleted and created again. A read-only field takes no editing
	// focus and says that it is locked instead of describing a choice that is no longer there.
	readOnly bool
	// roleLead marks the first of the secret role rows. What those rows hold is the same for all of them,
	// so it is said once, above them, rather than repeated under each one.
	roleLead bool
}

// Field hints. They name the shape of an entry, never a rule the core owns: the core states the exact
// character set when it refuses a value, and repeating it here would let the two drift apart.
const (
	nameHint    = "a key you choose, without spaces; connections and --connection refer to it"
	domainHint  = "a key you choose, without spaces; commands look up this default by it"
	baseURLHint = "the root url of the instance, with scheme http or https and without /api"
	// envHint is said once for all role rows and speaks of all of them: they are the same kind of row, so
	// repeating the sentence under the next one would read like a fault and add nothing.
	envHint = "every role row holds the NAME of an environment variable that holds the secret, " +
		"never the secret itself"
	typeHint = "env names one environment variable per role; keyring keeps the secrets in the credential store"
	// credentialProviderHint names the one thing this choice decides, because it is not obvious from the
	// field itself: the rows below it are the secret roles of the provider selected here.
	credentialProviderHint = "the system these secrets belong to; it decides which secret roles are asked for below"
	// connectionProviderHint says why this row exists at all: it is not stored, it decides what the rows
	// below may offer.
	connectionProviderHint = "the system this route leads to; it decides which services and credentials " +
		"can be chosen below"
	connectionServiceHint    = "the configured API endpoint this route uses; left/right chooses another instance"
	connectionCredentialHint = "the provider credential this route authenticates with; secret values stay " +
		"outside this file"
	// descriptionHint names the one consequence that sets this field apart from everything else in the
	// editor: what is typed here is published by discovery, so it is the one place where free text can
	// carry a secret out of this machine.
	descriptionHint = "optional; one line saying what this route is for. discovery publishes it, so it " +
		"must never carry a secret or personal data"
	secretHint = "s system keyring · p unencrypted file (asks first) · x remove; typing is masked"
	// lockedHint is what the name of an existing entry says about itself. It replaces the hint that
	// describes a free choice, which is the opposite of what this field does.
	lockedHint = "read-only; delete this entry and create it again to rename it"
)

// typeLabel is the field that decides what a credential is: which variables it names, or that its secrets
// live in a store. It is shown and chosen, never assumed.
const typeLabel = "type"

// providerLabel is the field that names the system an entry belongs to. A service always has one; a
// credential may, and then it decides which secret roles exist.
const providerLabel = "provider"

// providerColumn is the leading column both lists read down. A credential whose provider is neither named
// nor derivable says so rather than leaving a gap that shifts every column.
func providerColumn(provider string) string {
	if provider == "" {
		return "(none)"
	}
	return provider
}

// The defaults bridge the first frame until the terminal reports its real size. From then on both
// dimensions decide which dashboard can fit.
const (
	defaultWidth  = 80
	defaultHeight = 100
	minimumWidth  = 40
	minimumHeight = 12
	wideWidth     = 80
	wideHeight    = 18
)

func (f field) withHint(hint string) field {
	f.hint = hint
	return f
}

func (f field) value() string {
	if f.kind == fieldMultiChoice {
		if f.selected["default"] {
			return ""
		}
		var values []string
		for _, choice := range f.choices {
			if choice != "default" && f.selected[choice] {
				values = append(values, choice)
			}
		}
		if len(values) == 0 {
			return "none"
		}
		return strings.Join(values, ", ")
	}
	if f.kind == fieldChoice {
		if f.index < 0 || f.index >= len(f.choices) {
			return ""
		}
		return f.choices[f.index]
	}
	return strings.TrimSpace(f.input.Value())
}

// Model is the whole editor state.
type Model struct {
	store *config.Store
	cfg   *config.Config

	screen  screen
	section section
	// cursor is the section focused on the dashboard. The entries of the open section, with their filter,
	// selection and scroll position, are list.
	cursor int
	list   filterList

	editing string
	fields  []field
	focus   int
	// picker holds the values of the focused choice row while it is searched. The row itself changes only
	// when a value is taken.
	picker filterList
	// confirmRole names the role whose stored secret the confirmation removes. Empty means the
	// confirmation is about the selected entry of the list.
	confirmRole string

	status string
	fail   string
	// busy names the store write in flight and probing the question in flight, if any. The editor keeps
	// working while either runs.
	busy    string
	probing string
	// width is what the terminal reported. Messages are wrapped into it instead of being cut off.
	width  int
	height int
	// configExists distinguishes a loaded file from a new in-memory configuration. The default directory
	// is deliberately created only by the first successful save, and the editor must say so instead of
	// claiming that a file which does not exist was loaded.
	configExists bool

	// Credential store state. The editor never holds a secret: it holds where each role resolves from and
	// which stages the resolver checked, both of which name no value.
	secrets Secrets
	sources map[string]secret.Source
	checked map[string][]string
	// writes counts the store writes in flight. They are counted, not generation numbered: each one has
	// its own effect, so each outcome has to reach the user.
	writes int
	// probeID guards the display refresh, guardID the answer a waiting type change needs. They are apart
	// so an ordinary refresh cannot take that answer away.
	probeID     int
	guardID     int
	secretInput textinput.Model
	secretRole  string
	secretPlain bool

	// Connection test state. Raw responses never enter the model, only the class and a redacted message.
	tester     Tester
	redactor   *redact.Redactor
	testing    bool
	testName   string
	testClass  provider.Class
	testID     int
	cancelTest context.CancelFunc

	quitting bool
}

// New builds the editor over an existing store. A missing configuration file starts an empty one. The
// tester may be nil, in which case connection testing is unavailable; secrets may be nil, in which case the
// configuration stays editable and only the operations that would reach a store report why they cannot.
func New(store *config.Store, tester Tester, secrets Secrets, redactor *redact.Redactor) (*Model, error) {
	cfg, err := store.Load()
	configExists := true
	if err != nil {
		var notFound *config.NotFoundError
		if !asNotFound(err, &notFound) {
			return nil, err
		}
		cfg = store.New()
		configExists = false
	}
	if redactor == nil {
		redactor = &redact.Redactor{}
	}
	if secrets == nil {
		secrets = noSecrets{}
	}
	m := &Model{
		store: store, cfg: cfg, tester: tester, redactor: redactor,
		secrets: secrets,
		sources: map[string]secret.Source{},
		checked: map[string][]string{},
		width:   defaultWidth, height: defaultHeight, configExists: configExists,
	}
	m.list = newFilterList(m.describe)
	m.picker = newFilterList(choiceText)
	return m, nil
}

func asNotFound(err error, target **config.NotFoundError) bool {
	nf, ok := err.(*config.NotFoundError)
	if ok {
		*target = nf
	}
	return ok
}

// Init resolves credential locations for the dashboard. It asks only for source metadata; secret values
// never enter the model.
func (m *Model) Init() tea.Cmd { return m.refreshSources(m.keyringQueries()) }

// Update handles one event. It is the whole editor logic and needs no terminal.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	defer m.keepScrollPosition()
	switch msg := msg.(type) {
	case testDoneMsg:
		m.finishTest(msg)
		return m, nil
	case sourcesMsg:
		return m, m.handleSources(msg)
	case placedMsg:
		return m, m.handlePlaced(msg)
	case writtenMsg:
		return m, m.handleWritten(msg)
	case tea.WindowSizeMsg:
		// A zero dimension is also how focused tests report only the dimension they exercise. Real size
		// messages carry both; keep the last known value until a non-zero replacement arrives.
		if msg.Width != 0 {
			m.width = max(msg.Width, 1)
		}
		if msg.Height != 0 {
			m.height = max(msg.Height, 1)
		}
		return m, nil
	case tea.KeyMsg:
		if m.activeScreenTooSmall() {
			switch msg.String() {
			case "q", "ctrl+c":
				return m, m.quit()
			case "esc":
				// Leaving is what makes this a notice instead of a trap: a screen too large for the
				// terminal must not be the only way out of the editor.
				return m, m.leaveScreen()
			}
			return m, nil
		}
		switch m.screen {
		case screenMenu:
			return m, m.updateMenu(msg)
		case screenList:
			return m, m.updateList(msg)
		case screenForm:
			return m, m.updateForm(msg)
		case screenConfirm:
			return m, m.updateConfirm(msg)
		case screenPlaintextConfirm:
			return m, m.updatePlaintextConfirm(msg)
		case screenSecret:
			return m, m.updateSecret(msg)
		case screenPicker:
			return m, m.updatePicker(msg)
		}
	}
	return m, nil
}

func (m *Model) updateMenu(key tea.KeyMsg) tea.Cmd {
	if s, ok := sectionShortcut(key.String()); ok {
		return m.openSection(s)
	}
	switch key.String() {
	case "q", "esc", "ctrl+c":
		return m.quit()
	case "shift+tab":
		m.cursor = wrap(m.cursor-1, int(sectionCount))
	case "tab":
		m.cursor = wrap(m.cursor+1, int(sectionCount))
	case "up", "k":
		m.moveDashboardFocus(0, -1)
	case "down", "j":
		m.moveDashboardFocus(0, 1)
	case "left", "h":
		m.moveDashboardFocus(-1, 0)
	case "right", "l":
		m.moveDashboardFocus(1, 0)
	case "enter":
		return m.openSection(section(m.cursor))
	case "n":
		cmd := m.openSection(section(m.cursor))
		if reason := m.newEntryBlocked(); reason != "" {
			m.fail = reason
			return cmd
		}
		return tea.Batch(cmd, m.openForm(""))
	}
	return nil
}

func (m *Model) moveDashboardFocus(horizontal, vertical int) {
	if m.dashboardLayout() != dashboardWide {
		m.cursor = wrap(m.cursor+horizontal+vertical, int(sectionCount))
		return
	}
	row, column := m.cursor/2, m.cursor%2
	row = wrap(row+vertical, 2)
	column = wrap(column+horizontal, 2)
	m.cursor = row*2 + column
}

func (m *Model) updateList(key tea.KeyMsg) tea.Cmd {
	if m.list.editing {
		return m.updateFilter(key)
	}
	if s, ok := sectionShortcut(key.String()); ok {
		return m.openSection(s)
	}
	switch key.String() {
	case "esc", "q":
		if m.testing {
			// Cancelling the test returns to the editor with the configuration untouched.
			m.stopTest()
			m.status = "Connection test cancelled"
			return nil
		}
		if m.list.query() != "" {
			// A filter is left before the list is: the first escape shows every entry again.
			m.list.clearFilter()
			return nil
		}
		m.screen, m.cursor = screenMenu, int(m.section)
		m.clearMessages()
	case "ctrl+c":
		return m.quit()
	case "/":
		m.list.startFilter()
	case "up", "k":
		m.list.move(-1)
	case "down", "j":
		m.list.move(1)
	case "pgup", "pgdown", "home", "end":
		m.jumpList(key.String())
	case "n":
		if reason := m.newEntryBlocked(); reason != "" {
			m.status = ""
			m.fail = reason
			return nil
		}
		return m.openForm("")
	case "enter":
		if name, ok := m.selected(); ok {
			return m.openForm(name)
		}
	case "d":
		if _, ok := m.selected(); ok {
			m.screen = screenConfirm
			m.clearMessages()
		}
	case "t":
		return m.startTest()
	}
	return nil
}

// updateFilter is the list while its filter is typed. Every printable key belongs to the filter, including
// the letters and digits that are list keys otherwise; the arrows still move through what it matches.
func (m *Model) updateFilter(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "ctrl+c":
		return m.quit()
	case "esc":
		m.list.clearFilter()
	case "enter":
		m.list.stopFilter()
	case "up":
		m.list.move(-1)
	case "down":
		m.list.move(1)
	case "pgup", "pgdown", "home", "end":
		m.jumpList(key.String())
	default:
		return m.list.updateFilter(key)
	}
	return nil
}

// jumpList moves by one screen of entries or to either end of the list.
func (m *Model) jumpList(key string) {
	start, end := m.listWindow()
	m.list.page(key, start, end)
}

// newEntryBlocked keeps a form with empty choice lists from turning an obvious missing prerequisite into
// a schema error. Existing entries remain editable even if a hand-written file is inconsistent; the core
// still owns validation in that case.
func (m *Model) newEntryBlocked() string {
	return m.newEntryBlockedFor(m.section)
}

func (m *Model) newEntryBlockedFor(s section) string {
	switch s {
	case sectionConnections:
		var missing []string
		if len(m.cfg.Services) == 0 {
			missing = append(missing, "a service")
		}
		if len(m.cfg.Credentials) == 0 {
			missing = append(missing, "a credential")
		}
		if len(missing) > 0 {
			return "Create " + strings.Join(missing, " and ") +
				" before adding a connection. Press esc to return to setup."
		}
	case sectionDefaults:
		if len(m.cfg.Connections) == 0 {
			return "Create a connection before choosing a default. Press esc to return to setup."
		}
	}
	return ""
}

// leaveScreen steps back one screen without saving anything. It is what the resize notice offers besides
// quitting, so a screen that does not fit is never the end of the session.
func (m *Model) leaveScreen() tea.Cmd {
	switch m.screen {
	case screenMenu:
		return nil
	case screenList:
		m.screen, m.cursor = screenMenu, int(m.section)
		m.clearMessages()
		return nil
	case screenPicker:
		// The picker steps back to its form, which keeps everything typed into it.
		m.screen = screenForm
		return nil
	default:
		cmd := m.returnToList("")
		m.status = "Cancelled"
		return cmd
	}
}

func (m *Model) updateForm(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "esc":
		// Cancelling discards the form; nothing was written.
		cmd := m.returnToList("")
		m.status = "Cancelled"
		return cmd
	case "ctrl+c":
		return m.quit()
	case "tab", "down":
		m.moveFocus(1)
		return nil
	case "shift+tab", "up":
		m.moveFocus(-1)
		return nil
	case "enter":
		return m.submit()
	}

	current := &m.fields[m.focus]
	switch current.kind {
	case fieldChoice:
		previous := current.value()
		switch key.String() {
		case "left", "h":
			current.index = wrap(current.index-1, len(current.choices))
		case "right", "l":
			current.index = wrap(current.index+1, len(current.choices))
		case "/":
			m.openPicker()
			return nil
		default:
			return nil
		}
		return m.choiceChanged(previous)
	case fieldMultiChoice:
		switch key.String() {
		case "left", "h":
			current.index = wrap(current.index-1, len(current.choices))
		case "right", "l":
			current.index = wrap(current.index+1, len(current.choices))
		case " ":
			current.toggleChoice()
		default:
			return nil
		}
		return nil
	case fieldSecret:
		// A secret row holds nothing to type into, so its keys are free for what a secret needs.
		return m.secretRowKey(current.label, key)
	}
	if current.readOnly {
		return nil
	}
	var cmd tea.Cmd
	current.input, cmd = current.input.Update(key)
	return cmd
}

// choiceChanged brings the rows that depend on the focused choice row in line with its new value, however
// that value was chosen.
func (m *Model) choiceChanged(previous string) tea.Cmd {
	switch m.fields[m.focus].label {
	case typeLabel:
		return m.credentialTypeChosen()
	case providerLabel:
		switch m.section {
		case sectionCredentials:
			return m.credentialProviderChosen()
		case sectionConnections:
			m.connectionProviderChosen()
		default:
			m.providerChosen(previous)
		}
	case "service":
		m.targetChosen()
	}
	return nil
}

// pickerThreshold is the number of values from which the form points out the picker. Fewer values are
// quicker to step through with left/right; the picker opens on any choice row all the same.
const pickerThreshold = 8

// openPicker opens the searchable list of the focused choice row, on its current value, with the filter
// ready for typing.
func (m *Model) openPicker() {
	f := m.fields[m.focus]
	if len(f.choices) == 0 {
		return
	}
	m.picker.reset(f.choices)
	m.picker.selectName(f.value())
	m.picker.startFilter()
	m.screen = screenPicker
	m.clearMessages()
}

// updatePicker handles the picker. Every printable key belongs to the filter; enter takes the selected
// value and esc leaves the row as it was.
func (m *Model) updatePicker(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "ctrl+c":
		return m.quit()
	case "esc":
		m.screen = screenForm
	case "enter":
		choice, ok := m.picker.selected()
		if !ok {
			// Nothing matches the filter, so there is nothing to take.
			return nil
		}
		m.screen = screenForm
		f := &m.fields[m.focus]
		previous := f.value()
		if choice == previous {
			return nil
		}
		for i, c := range f.choices {
			if c == choice {
				f.index = i
			}
		}
		return m.choiceChanged(previous)
	case "up":
		m.picker.move(-1)
	case "down":
		m.picker.move(1)
	case "pgup", "pgdown", "home", "end":
		start, end := m.pickerWindow()
		m.picker.page(key.String(), start, end)
	default:
		return m.picker.updateFilter(key)
	}
	return nil
}

// choiceText is how one value of a choice row reads. The empty value, which a credential offers while it
// names no provider, is a value too and has to be readable and findable.
func choiceText(choice string) string {
	if choice == "" {
		return "(none)"
	}
	return choice
}

func (m *Model) providerChosen(previous string) {
	base := m.field("base url")
	if base == nil {
		return
	}
	old, _ := m.cfg.ProviderMetadata(previous)
	metadata, _ := m.cfg.ProviderMetadata(m.fieldValue("provider"))
	if strings.TrimSpace(base.input.Value()) == "" || base.input.Value() == old.DefaultBaseURL {
		base.input.SetValue(metadata.DefaultBaseURL)
	}
}

// connectionProviders are the providers a connection can actually be built for: those with at least one
// service. Offering a provider without one would lead to a form whose service row is empty.
func (m *Model) connectionProviders() []string {
	seen := map[string]bool{}
	var providers []string
	for _, name := range m.entryNames(sectionServices) {
		if provider := m.cfg.Services[name].Provider; provider != "" && !seen[provider] {
			seen[provider] = true
			providers = append(providers, provider)
		}
	}
	sort.Strings(providers)
	return providers
}

func (m *Model) firstProviderWithService() string {
	if providers := m.connectionProviders(); len(providers) > 0 {
		return providers[0]
	}
	return ""
}

// providerServices are the services of one provider, in list order.
func (m *Model) providerServices(provider string) []string {
	var names []string
	for _, name := range m.entryNames(sectionServices) {
		if m.cfg.Services[name].Provider == provider {
			names = append(names, name)
		}
	}
	return names
}

// providerCredentials are the credentials that can serve one provider: those that belong to it, and those
// whose provider is still open. A credential just created has no connection yet, so nothing places it
// anywhere; keeping it selectable is what lets this form be the place that settles it.
func (m *Model) providerCredentials(provider string) []string {
	var names []string
	for _, name := range m.entryNames(sectionCredentials) {
		switch belongs := m.credentialProvider(name, m.cfg.Credentials[name]); belongs {
		case provider, "":
			names = append(names, name)
		}
	}
	return names
}

// targetHint says what the target of one service means, in that provider's own words.
func (m *Model) targetHint(service string) string {
	metadata, _ := m.cfg.ProviderMetadata(m.cfg.Services[service].Provider)
	hint := metadata.Target.Description
	if metadata.Target.Required {
		hint += "; required for " + metadata.Name
	}
	return hint
}

// connectionProviderChosen narrows the service and credential rows to the provider now selected. A route
// that binds a BookStack service to a Telegram credential cannot work, so it is not offered here.
func (m *Model) connectionProviderChosen() {
	provider := m.fieldValue(providerLabel)
	m.replaceChoices("service", m.providerServices(provider))
	m.replaceChoices("credential", m.providerCredentials(provider))
	if target := m.field("target"); target != nil {
		target.hint = m.targetHint(m.fieldValue("service"))
	}
	m.replacePermissionChoices(provider)
}

// replaceChoices puts a new set of options on one choice row, keeping the current value when it is still
// among them and falling back to the first otherwise.
func (m *Model) replaceChoices(label string, choices []string) {
	f := m.field(label)
	if f == nil {
		return
	}
	current := f.value()
	f.choices = choices
	f.index = 0
	for i, choice := range choices {
		if choice == current {
			f.index = i
		}
	}
}

func (m *Model) targetChosen() {
	if target := m.field("target"); target != nil {
		target.hint = m.targetHint(m.fieldValue("service"))
	}
}

func (m *Model) permissionHint(provider string) string {
	metadata, _ := m.cfg.ProviderMetadata(provider)
	defaults := make([]string, len(metadata.DefaultPermissions))
	for i, permission := range metadata.DefaultPermissions {
		defaults[i] = string(permission)
	}
	if len(defaults) == 0 {
		defaults = []string{"read"}
	}
	return "space toggles the local agent permissions offered by this provider; default currently means " +
		strings.Join(defaults, ", ") + "; selecting none denies every operation"
}

func (m *Model) permissionChoices(provider string) []string {
	metadata, _ := m.cfg.ProviderMetadata(provider)
	choices := []string{"default"}
	for _, permission := range metadata.SupportedPermissions {
		choices = append(choices, string(permission))
	}
	return choices
}

func (m *Model) permissionChoicesFor(provider string, current []config.Permission) []string {
	choices := m.permissionChoices(provider)
	for _, permission := range current {
		name := string(permission)
		found := false
		for _, choice := range choices {
			if choice == name {
				found = true
				break
			}
		}
		if !found {
			choices = append(choices, name)
		}
	}
	return choices
}

func (m *Model) replacePermissionChoices(provider string) {
	f := m.field("permissions")
	if f == nil || f.kind != fieldMultiChoice {
		return
	}
	selected, wasDefault := f.selected, f.selected["default"]
	f.choices = m.permissionChoices(provider)
	f.selected = map[string]bool{}
	if wasDefault {
		f.selected["default"] = true
	} else {
		for _, choice := range f.choices {
			if selected[choice] {
				f.selected[choice] = true
			}
		}
	}
	f.index = 0
	f.hint = m.permissionHint(provider)
}

func (m *Model) updateConfirm(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "y":
		if role := m.confirmRole; role != "" {
			m.confirmRole = ""
			m.screen = screenForm
			return m.removeSecret(m.editing, role)
		}
		return m.delete()
	case "n", "esc":
		if m.confirmRole != "" {
			m.confirmRole = ""
			m.screen = screenForm
		} else {
			m.screen = screenList
		}
		m.status = "Cancelled"
	case "ctrl+c":
		return m.quit()
	}
	return nil
}

// startTest runs the connection test for the selected connection. The event loop keeps handling keys
// while it runs, so the editor never blocks.
func (m *Model) startTest() tea.Cmd {
	if m.section != sectionConnections {
		return nil
	}
	name, ok := m.selected()
	if !ok {
		return nil
	}
	if m.tester == nil {
		m.fail = "connection testing is unavailable"
		return nil
	}

	m.stopTest()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelTest = cancel
	m.testing = true
	m.testName = name
	m.testClass = ""
	m.clearMessages()

	id := m.testID
	tester := m.tester
	return func() tea.Msg {
		class, err := tester(ctx, name)
		msg := testDoneMsg{id: id, class: class}
		if err != nil {
			msg.err = connectionTestError(err)
		}
		return msg
	}
}

// connectionTestError turns a missing credential role into an editor action. Other errors stay intact for
// redaction, but receive context when they are displayed by finishTest.
func connectionTestError(err error) string {
	var missing *secret.MissingSecretError
	if !errors.As(err, &missing) {
		return err.Error()
	}
	if missing.Type == config.CredentialTypeKeyring {
		return fmt.Sprintf("credential %q is missing %s; open Credentials, edit it, select %s, and press s "+
			"(or p when the system credential store is unavailable)",
			missing.Credential, missing.Role, missing.Role)
	}
	return fmt.Sprintf("credential %q is missing %s; open Credentials and set the environment variable "+
		"named for that role", missing.Credential, missing.Role)
}

// finishTest accepts a result only while it is still the current one, so a cancelled test cannot report
// back later.
func (m *Model) finishTest(done testDoneMsg) {
	if !m.testing || done.id != m.testID {
		return
	}
	m.testing = false
	m.testID++
	if m.cancelTest != nil {
		m.cancelTest()
		m.cancelTest = nil
	}
	if done.err != "" {
		// Redaction happens before anything reaches the model, including an unexpected provider error.
		m.fail = "Connection test could not run: " + m.redactor.Apply(done.err)
		return
	}
	m.testClass = done.class
}

// quit ends the editor. A running test is cancelled first, so its context never outlives the editor.
func (m *Model) quit() tea.Cmd {
	m.stopTest()
	m.quitting = true
	return tea.Quit
}

// stopTest abandons a running test. Its result is ignored when it arrives.
func (m *Model) stopTest() {
	if m.cancelTest != nil {
		m.cancelTest()
		m.cancelTest = nil
	}
	if m.testing {
		m.testID++
	}
	m.testing = false
	m.testClass = ""
}

// openSection shows a section from its first entry, without a filter.
func (m *Model) openSection(s section) tea.Cmd {
	m.section = s
	m.list.reset(m.entryNames(s))
	return m.showList()
}

// returnToList comes back to the list a form or confirmation was opened from. The filter stays, and so
// does the selection: on the named entry when it is shown, on the entry that was selected otherwise, or,
// when that one is gone, on the one now in its place.
func (m *Model) returnToList(name string) tea.Cmd {
	m.list.setItems(m.entryNames(m.section))
	if name != "" {
		m.list.selectName(name)
	}
	return m.showList()
}

func (m *Model) showList() tea.Cmd {
	m.stopTest()
	m.testName = ""
	m.screen = screenList
	m.editing = ""
	m.confirmRole = ""
	m.clearMessages()
	if m.section != sectionCredentials {
		return nil
	}
	// The list says where each secret resolves from, and that answer comes from the resolver rather than
	// from anything this editor remembers.
	return m.refreshSources(m.keyringQueries())
}

func (m *Model) selected() (string, bool) { return m.list.selected() }

func (m *Model) entryNames(s section) []string {
	var names []string
	switch s {
	case sectionServices:
		for name := range m.cfg.Services {
			names = append(names, name)
		}
	case sectionCredentials:
		for name := range m.cfg.Credentials {
			names = append(names, name)
		}
	case sectionConnections:
		for name := range m.cfg.Connections {
			names = append(names, name)
		}
	case sectionDefaults:
		for domain := range m.cfg.Defaults.Connections {
			names = append(names, domain)
		}
	}
	sort.Strings(names)
	return names
}

// openForm builds the form for a new entry, or for the named existing one.
func (m *Model) openForm(name string) tea.Cmd {
	m.editing = name
	m.screen = screenForm
	m.clearMessages()
	m.fields = m.buildFields(name)
	m.focus = m.firstEditable()
	m.applyFocus()
	if m.section == sectionCredentials && m.credentialType() == config.CredentialTypeKeyring {
		return m.refreshSources(m.editedQuery())
	}
	return nil
}

// credentialProvider is the provider a credential belongs to. It is the one the credential names, or,
// while it names none, the one every connection using it agrees on: a connection binds this credential to
// a service, and that service has a provider, so the answer is already in the configuration. A credential
// no connection uses, or one two providers disagree about, has none.
func (m *Model) credentialProvider(name string, cred config.Credential) string {
	if cred.Provider != "" {
		return cred.Provider
	}
	derived := ""
	for _, connection := range m.cfg.Connections {
		if connection.Credential != name {
			continue
		}
		provider := m.cfg.Services[connection.Service].Provider
		if provider == "" {
			continue
		}
		if derived != "" && derived != provider {
			return ""
		}
		derived = provider
	}
	return derived
}

// credentialRoles are the secret roles a credential actually uses. A credential that names its provider
// uses exactly that provider's roles; one written before this field, or by hand without it, still has to
// be editable, so it keeps offering every compiled role.
func (m *Model) credentialRoles(provider string) []string {
	if provider == "" {
		return m.cfg.SecretRoles()
	}
	return m.cfg.SecretRolesOf(provider)
}

// credentialProviders offers the compiled providers, plus the empty choice while this credential names
// none. Once it names one the empty choice is gone: a credential that knows its system never goes back to
// not knowing it, and the roles below would have nothing to narrow.
func (m *Model) credentialProviders(current string) []string {
	providers := m.cfg.Providers()
	if current == "" {
		return append([]string{""}, providers...)
	}
	return providers
}

// roleFields draws one row per secret role of the credential being edited.
func (m *Model) roleFields(cred config.Credential, provider, credType string) []field {
	roles := m.credentialRoles(provider)
	fields := make([]field, 0, len(roles))
	for i, role := range roles {
		fields = append(fields, m.roleField(role, cred.Values[role], credType, i == 0))
	}
	return fields
}

// credentialProviderChosen replaces the role rows with those of the provider now selected. The rows are
// what the choice is about: BookStack needs a token pair, Telegram a bot token, and a row of the other
// provider would only ever resolve to nothing.
func (m *Model) credentialProviderChosen() tea.Cmd {
	cred := m.cfg.Credentials[m.editing]
	// What was typed into a role that both providers define is kept; the credential on file only knows
	// what was last saved.
	values := map[string]string{}
	for _, f := range m.fields {
		if f.kind == fieldEnvName {
			values[f.label] = f.value()
		}
	}
	cred.Values = values
	credType := m.credentialType()
	// Only the role rows are replaced. Slicing to a fixed length would have dropped whatever field
	// happens to sit after them, so the rows to keep are the ones that are not roles.
	kept := make([]field, 0, len(m.fields))
	for _, f := range m.fields {
		if f.kind != fieldEnvName && f.kind != fieldSecret {
			kept = append(kept, f)
		}
	}
	m.fields = append(kept, m.roleFields(cred, m.fieldValue(providerLabel), credType)...)
	if m.focus >= len(m.fields) {
		m.focus = len(m.fields) - 1
	}
	m.applyFocus()
	if credType == config.CredentialTypeKeyring {
		return m.refreshSources(m.editedQuery())
	}
	return nil
}

// credentialType is the type the credential form currently shows.
func (m *Model) credentialType() string { return m.fieldValue(typeLabel) }

// credentialTypeChosen switches the role rows to the type that is now selected.
//
// The same row means a different thing under each type: under env it holds the NAME of a variable, under
// keyring it stands for an entry in the credential store. Drawing them alike is what let a keyring
// credential look like an env credential and turn into one on the next save. A variable name that was
// already typed survives a switch back, so trying out both types costs nothing.
func (m *Model) credentialTypeChosen() tea.Cmd {
	keyring := m.credentialType() == config.CredentialTypeKeyring
	for i := range m.fields {
		f := &m.fields[i]
		if f.kind != fieldEnvName && f.kind != fieldSecret {
			continue
		}
		if keyring {
			// A secret row builds its hint from what the resolver reported, so it carries none itself.
			f.kind, f.hint = fieldSecret, ""
		} else {
			f.kind, f.hint = fieldEnvName, m.roleHint(f.label, f.roleLead)
		}
	}
	m.applyFocus()
	if keyring {
		return m.refreshSources(m.editedQuery())
	}
	return nil
}

func (m *Model) buildFields(name string) []field {
	key := textField("name", name, name != "").withHint(nameHint)
	if m.section == sectionDefaults {
		key.label, key.hint = "domain", domainHint
	}
	if key.readOnly {
		// A locked field must not go on describing a free choice: that hint would claim the opposite of
		// what the field does. It says that it is locked, and how to get the rename it refuses.
		key.hint = lockedHint
	}
	fields := []field{key}

	switch m.section {
	case sectionServices:
		s := m.cfg.Services[name]
		fields = append(fields,
			choiceField("provider", m.cfg.Providers(), s.Provider),
			textField("base url", s.BaseURL, false).withHint(baseURLHint),
		)
		if name == "" {
			metadata, _ := m.cfg.ProviderMetadata(fields[1].value())
			fields[2].input.SetValue(metadata.DefaultBaseURL)
		}
	case sectionCredentials:
		cred := m.cfg.Credentials[name]
		credType := cred.Type
		if credType == "" {
			// A new credential starts as keyring: that is the type this editor can complete on its own,
			// while env needs a variable exported in a shell the editor cannot reach.
			credType = config.CredentialTypeKeyring
		}
		provider := m.credentialProvider(name, cred)
		fields = append(fields,
			choiceField(providerLabel, m.credentialProviders(provider), provider).
				withHint(credentialProviderHint),
			choiceField(typeLabel, config.CredentialTypes(), credType).withHint(typeHint),
		)
		fields = append(fields, m.roleFields(cred, fields[1].value(), credType)...)
	case sectionConnections:
		conn := m.cfg.Connections[name]
		// The provider is not stored on a connection: its service already names one. It stands here
		// because it decides what the two rows below may offer, and a route that mixes two systems is a
		// route that cannot work.
		provider := m.cfg.Services[conn.Service].Provider
		if provider == "" {
			provider = m.firstProviderWithService()
		}
		metadata, _ := m.cfg.ProviderMetadata(provider)
		targetValue := conn.Target
		if metadata.Target.Multiple {
			targetValue = formatTargets(conn)
		}
		permissions := permissionField(m.permissionChoicesFor(provider, conn.Permissions), conn.Permissions)
		permissions.hint = m.permissionHint(provider)
		fields = append(fields,
			choiceField(providerLabel, m.connectionProviders(), provider).withHint(connectionProviderHint),
			choiceField("service", m.providerServices(provider), conn.Service).withHint(connectionServiceHint),
			choiceField("credential", m.providerCredentials(provider), conn.Credential).
				withHint(connectionCredentialHint),
			textField("target", targetValue, false),
			// Description and permissions explain the route the fields above define. Only the description
			// is published during discovery.
			textField("description", conn.Description, false).withHint(descriptionHint),
			permissions,
		)
		fields[4].hint = m.targetHint(fields[2].value())
	case sectionDefaults:
		fields = append(fields,
			choiceField("connection", m.entryNames(sectionConnections), m.cfg.Defaults.Connections[name]),
		)
	}
	return fields
}

// submit saves the form, unless the change first has to be checked against the places that keep secrets.
func (m *Model) submit() tea.Cmd {
	m.trimFields()
	if cmd := m.guardTypeChange(); cmd != nil {
		return cmd
	}
	// The core owns every rule, including that a name must not be empty.
	return m.save(m.fields[0].value())
}

// save applies the form to a copy of the configuration and saves it. The editor keeps the change only when
// the store accepted it.
func (m *Model) save(name string) tea.Cmd {
	wasNewKeyring := m.section == sectionCredentials && m.editing == "" &&
		m.credentialType() == config.CredentialTypeKeyring
	candidate := m.cfg.Clone()
	if err := m.apply(candidate, name); err != nil {
		m.fail = m.redactor.Apply(err.Error())
		return nil
	}
	if err := m.store.Save(candidate); err != nil {
		m.fail = m.redactor.Apply(err.Error())
		return nil
	}
	m.cfg = candidate
	m.configExists = true
	if wasNewKeyring {
		cmd := m.openForm(name)
		for i := range m.fields {
			if m.fields[i].kind == fieldSecret {
				m.focus = i
				break
			}
		}
		m.applyFocus()
		m.status = "Credential saved. Add the required provider secrets below: press s on each " +
			"role, or p if the system credential store is unavailable."
		return cmd
	}
	cmd := m.returnToList(name)
	m.status = "Saved " + name
	return cmd
}

func (m *Model) apply(cfg *config.Config, name string) error {
	switch m.section {
	case sectionServices:
		return cfg.SetService(name, config.Service{
			Provider: m.fieldValue("provider"),
			BaseURL:  m.fieldValue("base url"),
			Options:  cfg.Services[name].Options,
		})
	case sectionCredentials:
		cred := config.Credential{Provider: m.fieldValue(providerLabel), Type: m.credentialType()}
		// Only an env credential names anything here. A keyring credential carries no values at all, so
		// this file cannot hold a secret even by accident; the core refuses one that does.
		if cred.Type == config.CredentialTypeEnv {
			cred.Values = map[string]string{}
			for _, role := range m.credentialRoles(cred.Provider) {
				if v := m.fieldValue(role); v != "" {
					cred.Values[role] = v
				}
			}
		}
		return cfg.SetCredential(name, cred)
	case sectionConnections:
		permissions, err := config.ParsePermissions(m.fieldValue("permissions"))
		if err != nil {
			return err
		}
		provider := m.fieldValue(providerLabel)
		metadata, _ := cfg.ProviderMetadata(provider)
		target, targets := m.fieldValue("target"), []string(nil)
		if metadata.Target.Multiple {
			target, targets, err = parseTargets(target)
			if err != nil {
				return err
			}
		}
		return cfg.SetConnection(name, config.Connection{
			Service:     m.fieldValue("service"),
			Credential:  m.fieldValue("credential"),
			Target:      target,
			Targets:     targets,
			Description: m.fieldValue("description"),
			Permissions: permissions,
		})
	case sectionDefaults:
		return cfg.SetDefault(name, m.fieldValue("connection"))
	}
	return nil
}

func (m *Model) remove(cfg *config.Config, name string) error {
	switch m.section {
	case sectionServices:
		return cfg.DeleteService(name)
	case sectionCredentials:
		return cfg.DeleteCredential(name)
	case sectionConnections:
		return cfg.DeleteConnection(name)
	case sectionDefaults:
		return cfg.DeleteDefault(name)
	}
	return nil
}

func (m *Model) delete() tea.Cmd {
	name, ok := m.selected()
	if !ok {
		m.screen = screenList
		return nil
	}

	candidate := m.cfg.Clone()
	if err := m.remove(candidate, name); err != nil {
		m.fail = m.redactor.Apply(err.Error())
		m.screen = screenList
		return nil
	}
	if err := m.store.Save(candidate); err != nil {
		m.fail = m.redactor.Apply(err.Error())
		m.screen = screenList
		return nil
	}
	m.cfg = candidate
	m.configExists = true
	cmd := m.returnToList("")
	m.status = "Deleted " + name
	return cmd
}

func (m *Model) fieldValue(label string) string {
	for _, f := range m.fields {
		if f.label == label {
			return f.value()
		}
	}
	return ""
}

func (m *Model) field(label string) *field {
	for i := range m.fields {
		if m.fields[i].label == label {
			return &m.fields[i]
		}
	}
	return nil
}

// moveFocus steps to the next field that can be edited. A read-only field is stepped over rather than
// stopped on: stopping there would show a cursor on a field that swallows every key, which is the
// contradiction this is here to remove. The field stays drawn and readable, it just cannot be entered.
//
// The search runs at most once around the form, so a form made only of read-only fields keeps the focus
// where it is instead of spinning; nothing traps the user, because esc and enter are handled before this.
func (m *Model) moveFocus(by int) {
	m.trimFields()
	next := m.focus
	for i := 0; i < len(m.fields); i++ {
		next = wrap(next+by, len(m.fields))
		if !m.fields[next].readOnly {
			break
		}
	}
	m.focus = next
	m.applyFocus()
}

// firstEditable is where a form opens. A form whose first field is locked opens on the first field that
// takes input, so the user starts where typing has an effect.
func (m *Model) firstEditable() int {
	for i := range m.fields {
		if !m.fields[i].readOnly {
			return i
		}
	}
	return 0
}

// trimFields makes the shown text the text that will be stored. value() ignores spaces at both edges, so a
// pasted " https://wiki " is saved without them; without this the form would keep showing spaces that never
// reach the file. Trimming happens when the user leaves a field or saves, never while typing.
func (m *Model) trimFields() {
	for i := range m.fields {
		f := &m.fields[i]
		if f.kind == fieldChoice || f.kind == fieldMultiChoice {
			continue
		}
		if trimmed := strings.TrimSpace(f.input.Value()); trimmed != f.input.Value() {
			f.input.SetValue(trimmed)
		}
	}
}

func (m *Model) applyFocus() {
	for i := range m.fields {
		if i == m.focus && m.fields[i].kind != fieldChoice &&
			m.fields[i].kind != fieldMultiChoice && !m.fields[i].readOnly {
			m.fields[i].input.Focus()
			continue
		}
		m.fields[i].input.Blur()
	}
}

func (m *Model) clearMessages() { m.status, m.fail = "", "" }

func textField(label, value string, readOnly bool) field {
	in := textinput.New()
	in.SetValue(value)
	in.Prompt = ""
	// A static cursor is drawn on every frame the editor already paints. Blinking would need the blink
	// command threaded through Init, through every focus change and through the timer messages this event
	// loop drops, and a missed tick would leave the cursor invisible again.
	in.Cursor.SetMode(cursor.CursorStatic)
	return field{label: label, kind: fieldText, input: in, readOnly: readOnly}
}

// roleField is the row of one secret role. Under a credential of type env it holds the NAME of an
// environment variable, which the editor reads and writes; under keyring it stands for an entry in the
// credential store, which the editor can fill and remove but never read. The value is never read into the
// editor in either case.
//
// lead marks the first role row, the one that carries what all of them have in common.
func (m *Model) roleField(role, envName, credType string, lead bool) field {
	f := textField(role, envName, false)
	f.roleLead = lead
	f.kind, f.hint = fieldEnvName, m.roleHint(role, lead)
	if credType == config.CredentialTypeKeyring {
		// A secret row builds its hint from what the resolver reported, so it carries none itself.
		f.kind, f.hint = fieldSecret, ""
	}
	return f
}

// roleHint says what the provider-defined role means. Only the first row adds the explanation shared by
// every env row; repeating that part under the next role would read like a fault.
func (m *Model) roleHint(role string, lead bool) string {
	var parts []string
	if description := m.cfg.SecretRoleDescription(role); description != "" {
		parts = append(parts, description)
	}
	if lead {
		parts = append(parts, envHint)
	}
	return strings.Join(parts, "; ")
}

func choiceField(label string, choices []string, value string) field {
	f := field{label: label, kind: fieldChoice, choices: choices}
	for i, c := range choices {
		if c == value {
			f.index = i
		}
	}
	return f
}

func permissionField(choices []string, permissions []config.Permission) field {
	f := field{label: "permissions", kind: fieldMultiChoice, choices: choices, selected: map[string]bool{}}
	if permissions == nil {
		f.selected["default"] = true
		return f
	}
	for _, permission := range permissions {
		f.selected[string(permission)] = true
	}
	return f
}

func (f *field) toggleChoice() {
	if f.index < 0 || f.index >= len(f.choices) {
		return
	}
	choice := f.choices[f.index]
	if choice == "default" {
		if f.selected["default"] {
			f.selected = map[string]bool{}
		} else {
			f.selected = map[string]bool{"default": true}
		}
		return
	}
	delete(f.selected, "default")
	f.selected[choice] = !f.selected[choice]
	if !f.selected[choice] {
		delete(f.selected, choice)
	}
}

func formatTargets(connection config.Connection) string {
	values := connection.TargetValues()
	if len(values) == 0 {
		return ""
	}
	var b strings.Builder
	w := csv.NewWriter(&b)
	_ = w.Write(values)
	w.Flush()
	return strings.TrimSuffix(b.String(), "\n")
}

func parseTargets(raw string) (string, []string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil, nil
	}
	if strings.ContainsAny(raw, "\r\n") {
		return "", nil, errors.New("the table allow-list must stay on one line")
	}
	r := csv.NewReader(strings.NewReader(raw))
	r.TrimLeadingSpace = true
	values, err := r.Read()
	if err != nil {
		return "", nil, errors.New("the table allow-list must be one CSV row; quote a table name that contains a comma")
	}
	for i := range values {
		values[i] = strings.TrimSpace(values[i])
	}
	if len(values) == 1 {
		return values[0], nil, nil
	}
	return "", values, nil
}

func wrap(i, n int) int {
	if n <= 0 {
		return 0
	}
	return ((i % n) + n) % n
}

func sectionShortcut(key string) (section, bool) {
	if len(key) != 1 || key[0] < '1' || key[0] > '4' {
		return 0, false
	}
	return section(key[0] - '1'), true
}

var (
	titleStyle   = lipgloss.NewStyle().Bold(true)
	activeStyle  = lipgloss.NewStyle().Bold(true)
	hintStyle    = lipgloss.NewStyle().Faint(true)
	failStyle    = lipgloss.NewStyle().Bold(true)
	warningStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3"))
)

// View renders the current screen.
func (m *Model) View() string {
	if m.quitting {
		return ""
	}
	if m.activeScreenTooSmall() {
		return m.resizeView()
	}
	if m.screen == screenMenu {
		return m.dashboardView()
	}
	view := m.editorView()
	if !m.viewFits(view) {
		return m.resizeView()
	}
	return view
}

// editorView renders the current screen at the density its terminal allows. A form whose hints do not fit
// keeps the hint of the focused field and drops the rest: the field a person is on is the one they need
// explained, and a screen that says less is worth more than one that cannot be shown at all.
func (m *Model) editorView() string {
	view := m.buildEditorView(false)
	if m.screen == screenForm && !m.viewFits(view) {
		if dense := m.buildEditorView(true); m.viewFits(dense) {
			return dense
		}
	}
	return view
}

func (m *Model) buildEditorView(dense bool) string {
	switch m.screen {
	case screenList:
		return m.listView()
	case screenPicker:
		return m.pickerView()
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("Qatlas setup") + "\n")
	path := "Config: " + m.store.Path()
	if !m.configExists {
		path += " (created on first save)"
	}
	b.WriteString(m.wrapped(hintStyle, path) + "\n\n")

	switch m.screen {
	case screenForm:
		what := "New " + strings.ToLower(m.section.title())
		if m.editing != "" {
			what = "Edit " + m.editing
		}
		b.WriteString(titleStyle.Render(what) + "\n\n")
		for i, f := range m.fields {
			// A field and its hint are one block, and the blank line stands between the blocks. Without it
			// the hint is as close to the next field as to its own, and an indented line under a row of
			// rows reads as the introduction to what follows rather than as the note of what precedes.
			// A dense form has no blocks to separate: it carries one hint, under the field it belongs to.
			if i > 0 && !dense {
				b.WriteString("\n")
			}
			b.WriteString(line(i == m.focus, m.renderField(f, i == m.focus)) + "\n")
			if dense && i != m.focus {
				continue
			}
			if hint := m.fieldHint(f); hint != "" {
				b.WriteString(m.indented(hint) + "\n")
			}
			if warning := m.fieldWarning(f); warning != "" {
				b.WriteString(m.indentedWith(warningStyle, "warning: "+warning) + "\n")
			}
		}
		keys := "tab move · left/right choose · enter save · esc cancel"
		if f := m.fields[m.focus]; f.kind == fieldChoice && len(f.choices) >= pickerThreshold {
			keys = "/ search · " + keys
		}
		if m.fields[m.focus].kind == fieldMultiChoice {
			keys = "left/right choose · space toggle · " + keys
		}
		if m.fields[m.focus].kind == fieldSecret {
			if m.editing == "" {
				keys = "enter save credential first · tab move · esc cancel"
			} else {
				keys = "s system keyring · p unencrypted file (asks first) · x remove · " + keys
			}
		}
		b.WriteString(m.hint(keys))
	case screenPlaintextConfirm:
		b.WriteString(titleStyle.Render("Store this secret unencrypted?") + "\n\n")
		b.WriteString(m.wrapped(failStyle,
			fmt.Sprintf("This does not use the system keyring. It writes %s.%s as readable text for your "+
				"user account into %s.", m.editing, m.secretRole, m.plaintextPath())) + "\n")
		b.WriteString(m.indented(
			"Choose no if you want the keyring. Unlock or configure the system keyring, return here, and press s.") +
			"\n")
		b.WriteString(m.hint("y continue to masked input · n/esc cancel without writing"))
	case screenSecret:
		where := "the credential store of this machine"
		if m.secretPlain {
			where = "the plaintext file " + m.plaintextPath()
		}
		b.WriteString(titleStyle.Render(fmt.Sprintf("Secret for %s.%s", m.editing, m.secretRole)) + "\n\n")
		b.WriteString("  " + m.secretInput.View() + "\n")
		b.WriteString(m.indented(
			"the value is masked while you type, is never shown back, and goes into "+where) + "\n")
		b.WriteString(m.hint("enter store · esc cancel"))
	case screenConfirm:
		if m.confirmRole != "" {
			b.WriteString(fmt.Sprintf("Remove the stored secret for %s.%s?\n", m.editing, m.confirmRole))
			b.WriteString(m.indented(
				"it is removed from the credential store and from the plaintext file; an environment "+
					"variable is not touched, because it belongs to your shell") + "\n")
		} else {
			name, _ := m.selected()
			b.WriteString(fmt.Sprintf("Delete %q?\n", name))
			if m.section == sectionCredentials &&
				m.cfg.Credentials[name].Type == config.CredentialTypeKeyring {
				b.WriteString(m.indented(
					"its stored secrets are not removed with it; remove them first with x on the role, "+
						"or later with 'qatlas credential delete'") + "\n")
			}
		}
		b.WriteString(m.hint("y remove · n keep"))
	}

	b.WriteString(m.notes())
	return b.String()
}

// notes are the lines under every editor screen: what is still running, and what the last action did.
func (m *Model) notes() string {
	var b strings.Builder
	for _, note := range []string{m.busy, m.probing} {
		if note != "" {
			b.WriteString("\n\n" + m.wrapped(hintStyle, note+" ... (the editor stays usable)"))
		}
	}
	if m.fail != "" {
		b.WriteString("\n\n" + m.wrapped(failStyle, "error: "+m.fail))
	} else if m.status != "" {
		b.WriteString("\n\n" + m.wrapped(hintStyle, m.status))
	}
	return b.String()
}

// listView draws the list screen: its frame, and between them as many rows as fit.
func (m *Model) listView() string {
	header, footer := m.listFrame()
	var b strings.Builder
	b.WriteString(header)
	start, end := m.listWindowIn(header, footer)
	for i := start; i < end; i++ {
		b.WriteString(m.listRow(i) + "\n")
	}
	b.WriteString(footer)
	return b.String()
}

// listFrame is everything of the list screen except its rows: above them the heading, the position of the
// selection and the filter; below them the test result, the keys and the notes. The rows get the lines
// that are left, so a list of any length fits the terminal and only the rows scroll.
func (m *Model) listFrame() (string, string) {
	var head strings.Builder
	head.WriteString(titleStyle.Render("Qatlas setup") + "\n")
	path := "Config: " + m.store.Path()
	if !m.configExists {
		path += " (created on first save)"
	}
	head.WriteString(m.wrapped(hintStyle, path) + "\n\n")

	title := titleStyle.Render(m.section.title())
	total, shown := len(m.list.all), len(m.list.matches)
	filtered := m.list.editing || m.list.query() != ""
	switch {
	case filtered:
		title += fmt.Sprintf("  %d/%d (%d total)", min(m.list.cursor+1, shown), shown, total)
	case total > 0:
		title += fmt.Sprintf("  %d/%d", m.list.cursor+1, total)
	}
	head.WriteString(title + "\n")
	if filtered {
		head.WriteString(m.filterLine(&m.list) + "\n")
	} else {
		head.WriteString("\n")
	}
	switch {
	case total == 0:
		head.WriteString(m.wrapped(hintStyle, m.emptyHelp()) + "\n")
	case shown == 0:
		head.WriteString(m.wrapped(hintStyle,
			fmt.Sprintf("No entry matches %q. Press esc to clear the filter.", m.list.query())) + "\n")
	}

	var keys string
	switch {
	case m.list.editing:
		keys = "type to filter · up/down move · enter keep filter · esc clear filter"
	case m.section == sectionConnections:
		keys = "/ filter · n new · enter edit · d delete · t test · esc back"
	default:
		keys = "/ filter · n new · enter edit · d delete · esc back"
	}
	if !m.list.editing && m.list.query() != "" {
		keys = strings.TrimSuffix(keys, "esc back") + "esc clear filter"
	}
	foot := m.hint(keys)
	if m.section == sectionConnections {
		foot = m.testLine() + foot
	}
	return head.String(), foot + m.notes()
}

// filterLine shows the filter of l: while it is typed with its cursor, afterwards as the text it holds.
func (m *Model) filterLine(l *filterList) string {
	prefix, width := m.fit("filter: ")
	if !l.editing {
		return prefix + truncateCells(l.query(), width)
	}
	in := l.input
	in.Width = max(width-1, 1)
	return prefix + in.View()
}

// listRow draws the shown entry at index i of the list.
func (m *Model) listRow(i int) string {
	return m.row(i == m.list.cursor, m.describe(m.list.matches[i]))
}

// listWindow is the range of shown entries whose rows fit between the frame of the list screen.
func (m *Model) listWindow() (int, int) { return m.listWindowIn(m.listFrame()) }

func (m *Model) listWindowIn(header, footer string) (int, int) {
	return m.windowIn(&m.list, header, footer, m.listRow)
}

// windowIn is the range of shown entries of l whose rows, drawn by row, fit between header and footer.
func (m *Model) windowIn(l *filterList, header, footer string, row func(i int) string) (int, int) {
	room := m.height - strings.Count(header, "\n") - strings.Count(footer, "\n") - 1
	heights := map[int]int{}
	return l.window(room, func(i int) int {
		if _, ok := heights[i]; !ok {
			heights[i] = strings.Count(row(i), "\n") + 1
		}
		return heights[i]
	})
}

// pickerView draws the picker of the focused choice row like a list: its frame, and between them as many
// values as fit.
func (m *Model) pickerView() string {
	header, footer := m.pickerFrame()
	var b strings.Builder
	b.WriteString(header)
	start, end := m.windowIn(&m.picker, header, footer, m.pickerRow)
	for i := start; i < end; i++ {
		b.WriteString(m.pickerRow(i) + "\n")
	}
	b.WriteString(footer)
	return b.String()
}

// pickerFrame is everything of the picker except its values: above them the row being chosen for, the
// position among the matches, the filter and the current value; below them the keys and the notes. It
// leaves out the heading of the other screens, so the values keep room in a small terminal.
func (m *Model) pickerFrame() (string, string) {
	f := m.fields[m.focus]
	shown, total := len(m.picker.matches), len(m.picker.all)
	var head strings.Builder
	head.WriteString(m.wrapped(titleStyle, fmt.Sprintf("Choose %s  %d/%d (%d total)",
		f.label, min(m.picker.cursor+1, shown), shown, total)) + "\n")
	head.WriteString(m.filterLine(&m.picker) + "\n")
	head.WriteString(m.wrapped(hintStyle, "current: "+choiceText(f.value())) + "\n")
	if shown == 0 {
		head.WriteString(m.wrapped(hintStyle,
			fmt.Sprintf("No value matches %q. esc keeps the current one.", m.picker.query())) + "\n")
	}
	return head.String(), m.hint("type to filter · up/down move · enter choose · esc cancel") + m.notes()
}

// pickerRow draws the shown value at index i of the picker and marks the value the row holds now.
func (m *Model) pickerRow(i int) string {
	choice := m.picker.matches[i]
	text := choiceText(choice)
	if choice == m.fields[m.focus].value() {
		text += "  (current)"
	}
	return m.row(i == m.picker.cursor, text)
}

func (m *Model) pickerWindow() (int, int) {
	header, footer := m.pickerFrame()
	return m.windowIn(&m.picker, header, footer, m.pickerRow)
}

// keepScrollPosition remembers where the list or the picker is scrolled to, so the next frame keeps it as
// long as the selection stays on screen instead of jumping to put the selection at an edge.
func (m *Model) keepScrollPosition() {
	if m.terminalTooSmall() {
		return
	}
	switch m.screen {
	case screenList:
		m.list.offset, _ = m.listWindow()
	case screenPicker:
		m.picker.offset, _ = m.pickerWindow()
	}
}

type dashboardLayout int

const (
	dashboardCompact dashboardLayout = iota
	dashboardStacked
	dashboardWide
)

func (m *Model) terminalTooSmall() bool {
	return m.width > 0 && m.height > 0 && (m.width < minimumWidth || m.height < minimumHeight)
}

func (m *Model) activeScreenTooSmall() bool {
	if m.terminalTooSmall() {
		return true
	}
	return m.screen != screenMenu && !m.viewFits(m.editorView())
}

func (m *Model) viewFits(view string) bool {
	lines := strings.Split(view, "\n")
	if m.height > 0 && len(lines) > m.height {
		return false
	}
	if m.width > 0 {
		for _, line := range lines {
			if lipgloss.Width(line) > m.width {
				return false
			}
		}
	}
	return true
}

func (m *Model) dashboardLayout() dashboardLayout {
	switch {
	case m.width >= wideWidth && m.height >= wideHeight:
		return dashboardWide
	case m.width >= 60 && m.height >= 22:
		return dashboardStacked
	default:
		return dashboardCompact
	}
}

// resizeView is deliberately useful even at one cell by one row: q remains visible, while additional
// room reveals the requested size. Rendering it does not change the screen, focus, form, or test state, so
// resizing back resumes exactly where the user was.
func (m *Model) resizeView() string {
	width, height := minimumWidth, minimumHeight
	if m.screen != screenMenu && !m.terminalTooSmall() {
		view := m.editorView()
		lines := strings.Split(view, "\n")
		height = max(height, len(lines))
		for _, line := range lines {
			width = max(width, lipgloss.Width(line))
		}
	}
	lines := []string{"q", "Resize terminal", fmt.Sprintf("Need %dx%d", width, height)}
	if m.width >= 6 {
		lines[0] = "q quit"
	}
	return m.boundView(strings.Join(lines, "\n"))
}

func (m *Model) dashboardView() string {
	var b strings.Builder
	b.WriteString(m.clipLine("Qatlas setup"))
	b.WriteByte('\n')

	layout := m.dashboardLayout()
	if layout == dashboardCompact {
		b.WriteString(wrapCells(m.dashboardPath(), m.width))
		b.WriteByte('\n')
		for s := section(0); s < sectionCount; s++ {
			marker := "  "
			if int(s) == m.cursor {
				marker = "> "
			}
			b.WriteString(m.clipLine(fmt.Sprintf("%s%d. %-13s %d", marker, int(s)+1, s.title(),
				len(m.entryNames(s)))))
			b.WriteByte('\n')
		}
		b.WriteString(wrapCells("Next: "+m.nextStep(), m.width))
		b.WriteByte('\n')
		b.WriteString(m.clipLine("arrows/hjkl/tab · enter · n · 1-4 · q"))
	} else {
		path := m.dashboardPath()
		b.WriteString(wrapCells(path, m.width))
		b.WriteByte('\n')
		b.WriteString(wrapCells("Next: "+m.nextStep(), m.width))
		b.WriteByte('\n')
		rows := m.dashboardRows(layout, path)

		if layout == dashboardWide {
			left := (m.width - 1) / 2
			right := m.width - left - 1
			for row := 0; row < 2; row++ {
				if row > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(joinCards(m.dashboardCard(section(row*2), left, rows),
					m.dashboardCard(section(row*2+1), right, rows)))
				b.WriteByte('\n')
			}
		} else {
			for s := section(0); s < sectionCount; s++ {
				b.WriteString(m.dashboardCard(s, m.width, rows))
				b.WriteByte('\n')
			}
		}
		help := "arrows/hjkl/tab · enter · n · 1-4 · q"
		if layout == dashboardWide {
			help = "arrows/hjkl/tab move · enter open · n new · 1-4 open · q quit"
		}
		b.WriteString(m.clipLine(help))
	}

	for _, note := range []string{m.busy, m.probing} {
		if note != "" {
			b.WriteByte('\n')
			b.WriteString(wrapCells(note+" ...", m.width))
		}
	}
	if m.fail != "" {
		b.WriteByte('\n')
		b.WriteString(wrapCells("error: "+m.fail, m.width))
	} else if m.status != "" {
		b.WriteByte('\n')
		b.WriteString(wrapCells(m.status, m.width))
	}
	return m.boundView(strings.TrimSuffix(b.String(), "\n"))
}

func (m *Model) dashboardPath() string {
	path := "Config: " + m.store.Path()
	if !m.configExists {
		path += " (created on first save)"
	}
	return path
}

func (m *Model) dashboardRows(layout dashboardLayout, path string) int {
	headerRows := 1 + len(strings.Split(wrapCells(path, m.width), "\n")) +
		len(strings.Split(wrapCells("Next: "+m.nextStep(), m.width), "\n")) + 1
	if layout == dashboardWide {
		headerRows++ // the blank line between the two card rows
	}
	for _, note := range []string{m.busy, m.probing} {
		if note != "" {
			headerRows += len(strings.Split(wrapCells(note+" ...", m.width), "\n"))
		}
	}
	if m.fail != "" {
		headerRows += len(strings.Split(wrapCells("error: "+m.fail, m.width), "\n"))
	} else if m.status != "" {
		headerRows += len(strings.Split(wrapCells(m.status, m.width), "\n"))
	}

	groups := 4
	if layout == dashboardWide {
		groups = 2
	}
	available := max((m.height-headerRows)/groups-2, 1) // top and bottom border use two rows
	needed := 1
	for s := section(0); s < sectionCount; s++ {
		needed = max(needed, len(m.dashboardDetails(s)))
	}
	return min(available, max(needed, 2))
}

// dashboardCard is a small table: its top border identifies the section and count; following rows are
// actual configured entries or the concrete prerequisite for creating one. Its fixed dimensions make two
// cards safe to join without relying on terminal-side wrapping.
func (m *Model) dashboardCard(s section, width, rows int) string {
	width = max(width, 4)
	inner := width - 4
	bottom := "╰" + strings.Repeat("─", width-2) + "╯"
	marker := "  "
	if int(s) == m.cursor {
		marker = "> "
	}
	label := truncateCells(fmt.Sprintf("%s%d. %s · %d · %s ", marker, int(s)+1, s.title(),
		len(m.entryNames(s)), dashboardPurpose(s)), width-2)
	top := "╭" + label + strings.Repeat("─", max(width-2-lipgloss.Width(label), 0)) + "╮"
	lines := make([]string, 0, rows)
	details := m.dashboardDetails(s)
	if len(details) > rows {
		lines = append(lines, details[:rows-1]...)
		lines = append(lines, fmt.Sprintf("+%d more", len(details)-rows+1))
	} else {
		lines = append(lines, details...)
	}
	for len(lines) < rows {
		lines = append(lines, "")
	}

	rendered := make([]string, 0, len(lines)+2)
	rendered = append(rendered, top)
	for _, line := range lines {
		rendered = append(rendered, "│ "+padLine(truncateCells(line, inner), inner)+" │")
	}
	rendered = append(rendered, bottom)
	return strings.Join(rendered, "\n")
}

func dashboardPurpose(s section) string {
	return [...]string{"server URLs", "secret sources", "service + credential", "optional choices"}[s]
}

func (m *Model) dashboardDetails(s section) []string {
	names := m.entryNames(s)
	if len(names) == 0 {
		if reason := m.newEntryBlockedFor(s); reason != "" {
			return []string{strings.TrimSuffix(strings.Split(reason, ".")[0], ".")}
		}
		if s == sectionDefaults {
			return []string{"Optional; commands can use --connection"}
		}
		return []string{"No entries; press n to create one"}
	}
	details := make([]string, 0, len(names))
	for _, name := range names {
		details = append(details, m.dashboardEntry(s, name))
	}
	return details
}

// dashboardEntry contains configuration and resolver status only. In particular it never requests or
// renders a secret value.
func (m *Model) dashboardEntry(s section, name string) string {
	switch s {
	case sectionServices:
		service := m.cfg.Services[name]
		return fmt.Sprintf("%s · %s · %s", service.Provider, name, service.BaseURL)
	case sectionCredentials:
		cred := m.cfg.Credentials[name]
		provider := m.credentialProvider(name, cred)
		credentialRoles := m.credentialRoles(provider)
		roles := make([]string, 0, len(credentialRoles))
		for _, role := range credentialRoles {
			source := sourceUnnamed
			if cred.Type == config.CredentialTypeKeyring {
				source = m.storedSource(name, role)
			} else if envName := cred.Values[role]; envName != "" {
				source = m.envSource(envName)
			}
			roles = append(roles, role+"="+source)
		}
		return fmt.Sprintf("%s · %s · %s · %s", providerColumn(provider), name, cred.Type,
			strings.Join(roles, ", "))
	case sectionConnections:
		connection := m.cfg.Connections[name]
		detail := fmt.Sprintf("%s · %s + %s", name, connection.Service, connection.Credential)
		if targets := formatTargets(connection); targets != "" {
			detail += " → " + targets
		}
		return detail
	case sectionDefaults:
		return fmt.Sprintf("%s → %s", name, m.cfg.Defaults.Connections[name])
	default:
		return name
	}
}

func joinCards(left, right string) string {
	leftLines, rightLines := strings.Split(left, "\n"), strings.Split(right, "\n")
	lines := make([]string, min(len(leftLines), len(rightLines)))
	for i := range lines {
		lines[i] = leftLines[i] + " " + rightLines[i]
	}
	return strings.Join(lines, "\n")
}

func (m *Model) clipLine(line string) string { return clipCells(line, max(m.width, 1)) }

func (m *Model) boundView(view string) string {
	lines := strings.Split(view, "\n")
	if m.height > 0 && len(lines) > m.height {
		lines = lines[:m.height]
	}
	for i := range lines {
		lines[i] = m.clipLine(lines[i])
	}
	return strings.Join(lines, "\n")
}

func clipCells(text string, width int) string {
	if width <= 0 {
		return ""
	}
	var b strings.Builder
	used := 0
	for _, r := range text {
		cells := lipgloss.Width(string(r))
		if used+cells > width {
			break
		}
		b.WriteRune(r)
		used += cells
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

func (m *Model) nextStep() string {
	switch {
	case len(m.cfg.Services) == 0:
		return "add a Service and choose its provider"
	case len(m.cfg.Credentials) == 0:
		return "add Credentials and store the required provider secrets"
	case len(m.cfg.Connections) == 0:
		return "add a Connection that combines the service and credentials"
	case len(m.cfg.Defaults.Connections) == 0:
		return "open Connections and press t to test; Defaults are optional"
	default:
		return "open Connections and press t to test the selected provider"
	}
}

func (m *Model) emptyHelp() string {
	switch m.section {
	case sectionServices:
		return "No services yet. Press n to choose a provider and add its API root URL."
	case sectionCredentials:
		return "No credentials yet. Press n to add the roles required by a provider."
	case sectionConnections:
		if reason := m.newEntryBlocked(); reason != "" {
			return reason
		}
		return "No connections yet. Press n to combine a service and credential."
	case sectionDefaults:
		if reason := m.newEntryBlocked(); reason != "" {
			return reason + " Defaults are optional."
		}
		return "No default yet. Press n to choose one, or leave this empty and select connections explicitly."
	}
	return "Nothing configured yet."
}

// wrapped fits a message into the terminal instead of leaving it to be cut off at the right edge.
//
// The messages of the cascade carry the way out at their end: the file to chmod, the command to run, the
// variable to export. A truncated line loses exactly the part the reader needs, and it is the longest
// messages, the ones that explain the most, that get truncated. Wrapping keeps all of it on screen and
// needs nothing but the width the terminal already reports.
func (m *Model) wrapped(style lipgloss.Style, text string) string {
	return style.Width(m.usable(0)).Render(text)
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
func (m *Model) fit(prefix string) (string, int) {
	if m.width > 0 {
		if room := m.width - 1; room < len(prefix) {
			prefix = prefix[:max(room, 0)]
		}
	}
	return prefix, m.usable(len(prefix))
}

// indented draws a hint under the row it belongs to and keeps its continuation lines there too, so a hint
// that needs two lines still reads as one hint rather than as a stray sentence at the left margin.
func (m *Model) indented(text string) string {
	return m.indentedWith(hintStyle, text)
}

func (m *Model) indentedWith(style lipgloss.Style, text string) string {
	indent, width := m.fit("    ")
	lines := strings.Split(style.Width(width).Render(text), "\n")
	for i, l := range lines {
		lines[i] = indent + l
	}
	return strings.Join(lines, "\n")
}

// fieldHint is what one form row says about itself. A secret row adds the stages the resolver checked,
// which is the actionable half of a missing secret and names no value.
func (m *Model) fieldHint(f field) string {
	if f.kind == fieldSecret {
		if m.editing == "" {
			var parts []string
			if description := m.cfg.SecretRoleDescription(f.label); description != "" {
				parts = append(parts, description)
			}
			if f.roleLead {
				parts = append(parts,
					"save this credential with enter first; you will stay here to add both secrets")
			}
			return strings.Join(parts, "; ")
		}
		return m.secretRowHint(m.editing, f.label, f.roleLead)
	}
	return f.hint
}

func (m *Model) fieldWarning(f field) string {
	if f.label != "target" {
		return ""
	}
	service := m.fieldValue("service")
	metadata, ok := m.cfg.ProviderMetadata(m.cfg.Services[service].Provider)
	if !ok || metadata.Target.Wildcard == "" || metadata.Target.WildcardWarning == "" {
		return ""
	}
	for _, value := range parseTargetValuesForWarning(f.value()) {
		if value == metadata.Target.Wildcard {
			return metadata.Target.WildcardWarning
		}
	}
	return ""
}

func parseTargetValuesForWarning(raw string) []string {
	_, values, err := parseTargets(raw)
	if err != nil {
		return nil
	}
	if values == nil && strings.TrimSpace(raw) != "" {
		return []string{strings.TrimSpace(raw)}
	}
	return values
}

// testLine keeps the stable class visible and adds the next useful interpretation for a human.
func (m *Model) testLine() string {
	providerName := "the provider"
	if connection, ok := m.cfg.Connections[m.testName]; ok {
		if metadata, found := m.cfg.ProviderMetadata(m.cfg.Services[connection.Service].Provider); found {
			providerName = metadata.Name
		}
	}
	switch {
	case m.testing:
		return "\n" + m.wrapped(hintStyle, fmt.Sprintf("testing %s ... (esc cancels)", m.testName))
	case m.testClass == provider.ClassOK:
		return "\n" + m.wrapped(activeStyle,
			fmt.Sprintf("%s: ok - %s accepted the connection", m.testName, providerName))
	case m.testClass != "":
		explanation := map[provider.Class]string{
			provider.ClassUnreachable:     "the server did not answer; check the base URL and network",
			provider.ClassTLS:             "the secure connection failed; check the server certificate and URL",
			provider.ClassAuth:            providerName + " rejected the credential or it lacks permission",
			provider.ClassPermission:      providerName + " accepted the credential but refused the operation",
			provider.ClassTimeout:         providerName + " did not answer before the request deadline",
			provider.ClassRateLimited:     providerName + " is rate-limiting requests; wait and try again",
			provider.ClassInvalidResponse: providerName + " returned an invalid response",
			provider.ClassProviderError:   providerName + " returned an unusable response; check the root URL and API access",
		}[m.testClass]
		if providerName == "BookStack" && m.testClass == provider.ClassAuth {
			explanation = "BookStack rejected the token or its user lacks permission"
		}
		return "\n" + m.wrapped(failStyle,
			fmt.Sprintf("%s: %s - %s", m.testName, m.testClass, explanation))
	}
	return ""
}

// describe summarises one entry for the list. It never shows a secret value.
func (m *Model) describe(name string) string {
	switch m.section {
	case sectionServices:
		// The provider leads the line: what a service is comes before what it is called, and reading down
		// the column shows at a glance how many services each provider has.
		s := m.cfg.Services[name]
		return fmt.Sprintf("%s  %s  %s", s.Provider, name, s.BaseURL)
	case sectionCredentials:
		// The type decides what the credential is, so it is part of the line rather than something the
		// reader has to open the form to find out.
		cred := m.cfg.Credentials[name]
		provider := m.credentialProvider(name, cred)
		credentialRoles := m.credentialRoles(provider)
		parts := make([]string, 0, len(credentialRoles))
		for _, role := range credentialRoles {
			switch {
			case cred.Type == config.CredentialTypeKeyring:
				parts = append(parts, fmt.Sprintf("%s (%s)", role, m.storedSource(name, role)))
			case cred.Values[role] != "":
				parts = append(parts, fmt.Sprintf("%s=%s (%s)", role, cred.Values[role],
					m.envSource(cred.Values[role])))
			}
		}
		return strings.TrimSpace(fmt.Sprintf("%s  %s  %s  %s", providerColumn(provider), name, cred.Type,
			strings.Join(parts, "  ")))
	case sectionConnections:
		conn := m.cfg.Connections[name]
		detail := fmt.Sprintf("%s  %s / %s", name, conn.Service, conn.Credential)
		if targets := formatTargets(conn); targets != "" {
			detail += " / " + targets
		}
		return detail
	case sectionDefaults:
		return fmt.Sprintf("%s  %s", name, m.cfg.Defaults.Connections[name])
	}
	return name
}

// renderField draws one form row. The focused text field is drawn by the text input itself, so the cursor
// stands where it really stands, including in the middle of a url being corrected. Every other field is
// drawn plainly from its value.
func (m *Model) renderField(f field, focused bool) string {
	value := f.value()
	switch {
	case f.kind == fieldSecret:
		// A secret row shows where the role resolves from and nothing else: there is no value to draw,
		// and the resolver would not hand one out.
		value = "(" + m.storedSource(m.editing, f.label) + ")"
	case f.kind == fieldChoice && len(f.choices) == 0:
		value = "(nothing to choose)"
	case f.kind == fieldChoice:
		value = "< " + value + " >"
	case f.kind == fieldMultiChoice:
		parts := make([]string, len(f.choices))
		for i, choice := range f.choices {
			mark := " "
			if f.selected[choice] {
				mark = "x"
			}
			parts[i] = "[" + mark + "] " + choice
			if focused && i == f.index {
				parts[i] = "<" + parts[i] + ">"
			}
		}
		value = strings.Join(parts, "  ")
	case focused && !f.readOnly:
		value = f.input.View()
		if f.kind == fieldEnvName && f.value() != "" {
			value += " (" + m.envSource(f.value()) + ")"
		}
	case f.kind == fieldEnvName && value != "":
		value += " (" + m.envSource(value) + ")"
	case value == "":
		value = hintStyle.Render("(empty)")
	}
	return fmt.Sprintf("%-12s %s", f.label, value)
}

func line(active bool, text string) string {
	if active {
		return activeStyle.Render("> " + text)
	}
	return "  " + text
}

// row draws one line of a list. A line that does not fit is continued under its own entry instead of being
// cut off at the right edge: the list is where the source of every secret role stands, and that is the part
// that falls off first.
func (m *Model) row(active bool, text string) string {
	blank, width := m.fit("  ")
	marker := blank
	if active && len(blank) == 2 {
		marker = "> "
	}
	style := lipgloss.NewStyle()
	if active {
		style = activeStyle
	}
	lines := strings.Split(style.Width(width).Render(text), "\n")
	for i, l := range lines {
		prefix := blank
		if i == 0 {
			prefix = marker
		}
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

// hint is the key line of a screen. Like every other prose line it is wrapped into the terminal instead of
// being cut off at its right edge.
func (m *Model) hint(text string) string { return "\n" + m.wrapped(hintStyle, text) }

// Run starts the editor on the given terminal streams.
func Run(store *config.Store, tester Tester, secrets Secrets, redactor *redact.Redactor, in, out *os.File) error {
	model, err := New(store, tester, secrets, redactor)
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(model, tea.WithInput(in), tea.WithOutput(out)).Run()
	return err
}
