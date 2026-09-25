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
	"github.com/castrowithcee/qatlas-cli/internal/selfupdate"
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

// entry is what one entry of the section is called.
func (s section) entry() string {
	return [...]string{"service", "credential", "connection", "default"}[s]
}

type screen int

const (
	// screenNav is the sidebar in focus: the arrows choose the active section, whose list the workspace
	// shows beside it.
	screenNav screen = iota
	// screenList is the list of the active section in focus.
	screenList
	screenForm
	screenConfirm
	screenPlaintextConfirm
	screenSecret
	// screenPicker is the searchable list of one choice row of the form, over the form it was opened from.
	screenPicker
	// screenSummary is the last step of the guided setup: what will be saved, and after saving, the test.
	screenSummary
	// screenProviders is the table of a provider row, over the form it was opened from.
	screenProviders
	// screenLeave asks what happens to the unsaved input of a form before it is left: save, discard, or stay.
	screenLeave
	// screenHelp shows the help topics over the sidebar or the list it was opened from.
	screenHelp
	// screenUpdate asks before a newer release of qatlas is installed.
	screenUpdate
	// screenTargets edits the target list of a connection entry by entry.
	screenTargets
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
	// fieldToolList is a set of registered tools, ticked in a searchable picker because a provider may
	// register more tools than a form row can show.
	fieldToolList
	// fieldMasked takes a secret value in the guided setup. It is typed masked and drawn without its value;
	// the value leaves the editor only towards the credential store, when the setup is saved.
	fieldMasked
	// fieldProvider holds one provider like a choice row, but is chosen in the provider table only, so every
	// provider row looks and works the same however many providers there are.
	fieldProvider
	// fieldTargets is the target list of a connection. Its entries are added, edited, and removed one by one
	// in a screen of their own, so a target never has to be quoted into one line with the others. The row
	// shows a short form of them, or every entry while it is expanded.
	fieldTargets
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
	// hidden takes a row out of the form without dropping what was typed into it: the guided setup shows
	// the rows of a new entry only while a new entry is chosen, and brings them back unchanged.
	hidden bool
	// entries and expanded belong to a target list: its targets in the order they are saved, and whether
	// the row shows every one of them.
	entries  []string
	expanded bool
}

// providerNoteLabel is the row of a service form that edits the note of the service's provider.
const providerNoteLabel = "provider note"

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
	descriptionHint = "optional; what an agent uses this route for, e.g. 'issues in the test repository, " +
		"read and create'. discovery publishes it, so it must never carry a secret or personal data"
	// providerNoteHint says the same for the note of a provider, and that one note serves every service
	// of that provider rather than this service alone.
	providerNoteHint = "optional; what this provider stands for here, e.g. 'wiki' or 'CRM', shared by every " +
		"service of the provider. discovery publishes and searches it, so it must never carry a secret or " +
		"personal data"
	// undescribedMarker ends the row of a connection that needsDescription, and undescribedHint says once
	// under the list what it means. Neither blocks anything: the description stays optional.
	undescribedMarker = "(no description)"
	undescribedHint   = undescribedMarker + ": this connection shares its provider with another one, and an " +
		"agent that has to choose between them sees only names and descriptions; add one line on what each " +
		"route is for"
	// lockedHint is what the name of an existing entry says about itself. It replaces the hint that
	// describes a free choice, which is the opposite of what this field does.
	lockedHint = "read-only; delete this entry and create it again to rename it"
	// toolsHint says what each tools mode means, including the two consequences that are easy to miss: a
	// tool added by a later version joins only the first mode, and an empty selection closes the route.
	toolsHint = "all allowed by permissions also offers tools a later version adds, but never a tool marked " +
		"listed only; only selected tools offers exactly the tools ticked below, and none ticked offers no tool at all"
	toolListHint = "enter opens this provider's tools to tick; a tool is offered only when the permissions " +
		"above allow its effect as well"
	toolListOffHint = "not used while every tool the permissions allow is offered; choose only selected " +
		"tools above to pick them"
)

// The profile row of a connection. A profile is a provider's named starting selection: choosing one ticks
// the permissions and tools it expands to, and nothing else. The row shows the profile the ticks match, or
// custom, so it holds no state of its own and nothing of it is saved.
const (
	profileLabel  = "profile"
	profileCustom = "custom"
	profileHint   = "a starting selection, not a role: only the ticks below are saved, and they do not narrow " +
		"what the credential itself may do at the provider"
	profileCustomHint = "custom: the ticks below match no profile; choosing a profile replaces them after asking"
)

// The tools modes of a connection. The first one is a connection without a tools list, the second one a
// list, which may also be empty.
const (
	toolsLabel    = "tools"
	toolListLabel = "tool list"
	toolsAll      = "all allowed by permissions"
	toolsSelected = "only selected tools"
)

// providerLabel is the field that names the system an entry belongs to. A service always has one; a
// credential may, and then it decides which secret roles exist.
const providerLabel = "provider"

// providerColumn is the provider column of the credential list. A credential whose provider is neither
// named nor derivable says so rather than leaving an empty cell.
func providerColumn(provider string) string {
	if provider == "" {
		return "(none)"
	}
	return provider
}

// The defaults bridge the first frame until the terminal reports its real size, which it does before a
// person can press a key; they are generous so that frame is not squeezed. From then on the width decides
// whether the sections stand in a sidebar beside the workspace or in one line above it.
const (
	defaultWidth  = 120
	defaultHeight = 100
	minimumWidth  = 40
	minimumHeight = 12
	// sidebarMinWidth is the narrowest terminal that keeps the sidebar; below it the sections stack above
	// the workspace in one navigation line.
	sidebarMinWidth = 80
	// sidebarWidth is the outer width of the sidebar, frame included.
	sidebarWidth = 22
	// frameCells is what the frame of the workspace takes from each dimension: a border line on both
	// sides, and a blank column inside each vertical border.
	frameCells = 4
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
	if f.kind == fieldChoice || f.kind == fieldProvider {
		if f.index < 0 || f.index >= len(f.choices) {
			return ""
		}
		return f.choices[f.index]
	}
	if f.kind == fieldToolList {
		return strings.Join(f.marked(), ", ")
	}
	if f.kind == fieldTargets {
		// One target per line, so the value tells every list apart, a target with a comma included.
		return strings.Join(f.entries, "\n")
	}
	if f.kind == fieldMasked {
		// A secret is taken as typed, like in the masked prompt of a credential.
		return f.input.Value()
	}
	return strings.TrimSpace(f.input.Value())
}

// marked returns the ticked values of a tool list in the order it offers them. It is never nil, so a list
// with nothing ticked is stored as an explicit empty list rather than as a missing one.
func (f field) marked() []string {
	values := []string{}
	for _, choice := range f.choices {
		if f.selected[choice] {
			values = append(values, choice)
		}
	}
	return values
}

// Model is the whole editor state.
type Model struct {
	store *config.Store
	cfg   *config.Config

	screen screen
	// section is the active section: the one the sidebar marks and whose entries, with their filter,
	// selection and scroll position, list holds.
	section section
	list    filterList

	editing string
	fields  []field
	focus   int
	// picker holds the values of the focused choice row while it is searched. The row itself changes only
	// when a value is taken.
	picker filterList
	// pickerMarks holds the ticks of a tool list or the permissions while their picker is open, and is nil
	// for a single choice. The row takes them over only when they are kept.
	pickerMarks map[string]bool
	// providers is the table of the focused provider row while it is open.
	providers providerTable
	// targetList holds the entries of the focused target list while its screen is open; the row takes them
	// over only when they are kept. targetEdit is the entry typed into targetInput: its index, the length of
	// the list for a new one, or -1 while nothing is typed. targetRemove asks before the selected entry goes.
	targetList   filterList
	targetInput  textinput.Model
	targetEdit   int
	targetRemove bool
	// targetAdd is the menu that adds a target, with its builder, while it is open.
	targetAdd *targetAdd
	// confirmRole names the role whose stored secret the confirmation removes. Empty means the
	// confirmation is about the selected entry of the list.
	confirmRole string
	// pendingProfile names the profile the confirmation would apply to the permission and tool ticks of the
	// form. Empty means the confirmation is about something else.
	pendingProfile string
	// pristine is what the open form held when it was opened, so leaving it can tell whether anything
	// would be lost. leaveTo is the section the leave question is about, or -1 for the list of the form's
	// own section; leaveFrom is the screen it returns to when the user stays.
	pristine  string
	leaveTo   int
	leaveFrom screen

	status string
	fail   string
	// busy names the store write in flight and probing the question in flight, if any. The editor keeps
	// working while either runs.
	busy    string
	probing string
	// termWidth and termHeight are what the terminal reported. width and height are the room of the
	// workspace inside the frame; every screen lays itself out in them, and messages are wrapped into
	// width instead of being cut off.
	termWidth  int
	termHeight int
	width      int
	height     int
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

	// wizard is the guided setup while it runs, and nil otherwise.
	wizard *setup

	// helpTopic is the topic the help screen shows, scrolled to helpOffset; helpFrom is the screen it
	// returns to.
	helpTopic  int
	helpOffset int
	helpFrom   screen

	// Self-update state. release is a newer stable release the check at start found; updated names the
	// version installed since, which takes effect only after a restart. updateFrom is the screen the update
	// question returns to.
	updater    Updater
	release    selfupdate.Result
	updating   bool
	updated    string
	updateFrom screen

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
		secrets:   secrets,
		sources:   map[string]secret.Source{},
		checked:   map[string][]string{},
		termWidth: defaultWidth, termHeight: defaultHeight, configExists: configExists,
	}
	m.layoutWorkspace()
	m.list = newFilterList(m.describe)
	m.picker = newFilterList(choiceText)
	m.targetList = newFilterList(func(target string) string { return target })
	m.targetInput = textField("", "", false).input
	// The editor opens on the sidebar, with the first section already shown beside it.
	m.list.reset(m.entryNames(m.section))
	return m, nil
}

func asNotFound(err error, target **config.NotFoundError) bool {
	nf, ok := err.(*config.NotFoundError)
	if ok {
		*target = nf
	}
	return ok
}

// Init resolves credential locations for the credential list. It asks only for source metadata; secret values
// never enter the model. It also starts the check for a newer release, which never blocks the editor.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.refreshSources(m.keyringQueries()), m.checkUpdate())
}

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
	case setupSavedMsg:
		return m, m.setupSaved(msg)
	case updateCheckedMsg:
		m.updateChecked(msg)
		return m, nil
	case updateDoneMsg:
		m.updateDone(msg)
		return m, nil
	case tea.WindowSizeMsg:
		// A zero dimension is also how focused tests report only the dimension they exercise. Real size
		// messages carry both; keep the last known value until a non-zero replacement arrives.
		if msg.Width != 0 {
			m.termWidth = max(msg.Width, 1)
		}
		if msg.Height != 0 {
			m.termHeight = max(msg.Height, 1)
		}
		m.layoutWorkspace()
		return m, nil
	case tea.KeyMsg:
		if m.activeScreenTooSmall() {
			switch msg.String() {
			case "ctrl+c":
				return m, m.quit()
			case "q":
				// q is a letter in a form, so it quits only where nothing typed can be lost.
				if m.screen == screenNav || m.screen == screenList {
					return m, m.quit()
				}
			case "esc":
				// Leaving is what makes this a notice instead of a trap: a screen too large for the
				// terminal must not be the only way out of the editor.
				return m, m.leaveScreen()
			}
			return m, nil
		}
		if s, ok := altSectionShortcut(msg.String()); ok && m.canSwitchSection() {
			return m, m.switchSection(s)
		}
		var cmd tea.Cmd
		switch m.screen {
		case screenNav:
			cmd = m.updateNav(msg)
		case screenList:
			cmd = m.updateList(msg)
		case screenForm:
			cmd = m.updateForm(msg)
		case screenConfirm:
			cmd = m.updateConfirm(msg)
		case screenPlaintextConfirm:
			cmd = m.updatePlaintextConfirm(msg)
		case screenSecret:
			cmd = m.updateSecret(msg)
		case screenPicker:
			cmd = m.updatePicker(msg)
		case screenProviders:
			cmd = m.updateProviderTable(msg)
		case screenSummary:
			cmd = m.updateSummary(msg)
		case screenLeave:
			cmd = m.updateLeave(msg)
		case screenHelp:
			cmd = m.updateHelp(msg)
		case screenUpdate:
			cmd = m.updateUpdateConfirm(msg)
		case screenTargets:
			cmd = m.updateTargets(msg)
		}
		// Whatever a key changed, the profile row shows what the ticks now are.
		if m.screen == screenForm {
			m.syncProfile()
		}
		return m, cmd
	}
	return m, nil
}

// updateNav handles the sidebar in focus. Moving through it changes the active section at once, so the
// workspace beside it always shows the section the marker stands on.
func (m *Model) updateNav(key tea.KeyMsg) tea.Cmd {
	if s, ok := sectionShortcut(key.String()); ok {
		return m.openSection(s)
	}
	switch key.String() {
	case "q", "ctrl+c":
		return m.quit()
	case "up", "k", "shift+tab":
		return m.previewSection(section(wrap(int(m.section)-1, int(sectionCount))))
	case "down", "j":
		return m.previewSection(section(wrap(int(m.section)+1, int(sectionCount))))
	case "enter", "right", "l", "tab":
		m.screen = screenList
		m.clearMessages()
	case "c":
		m.startSetup()
	case "?":
		m.openHelp()
	case "u":
		m.askUpdate()
	case "n":
		if reason := m.newEntryBlocked(); reason != "" {
			m.status = ""
			m.fail = reason
			return nil
		}
		return m.openForm("")
	}
	return nil
}

// previewSection makes s the active section while the sidebar keeps the focus.
func (m *Model) previewSection(s section) tea.Cmd {
	cmd := m.openSection(s)
	m.screen = screenNav
	return cmd
}

func (m *Model) updateList(key tea.KeyMsg) tea.Cmd {
	if m.list.editing {
		return m.updateFilter(key)
	}
	if s, ok := sectionShortcut(key.String()); ok {
		return m.openSection(s)
	}
	switch key.String() {
	case "esc":
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
		m.focusNav()
	case "left", "h", "tab", "shift+tab":
		m.focusNav()
	case "c":
		m.startSetup()
	case "?":
		m.openHelp()
	case "u":
		m.askUpdate()
	case "q", "ctrl+c":
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
				" before adding a connection. Press 1 or 2 to open that section, or c for the guided setup."
		}
	case sectionDefaults:
		if len(m.cfg.Connections) == 0 {
			return "Create a connection before choosing a default. Press 3 to open Connections, or c for " +
				"the guided setup."
		}
	}
	return ""
}

// leaveScreen steps back one level without saving anything. It is what the resize notice offers besides
// quitting, so a screen that does not fit is never the end of the session; a form with unsaved input asks
// first, like every other way out of it.
func (m *Model) leaveScreen() tea.Cmd {
	switch m.screen {
	case screenNav:
		return nil
	case screenList:
		m.focusNav()
		return nil
	case screenLeave:
		// Staying is the answer that loses nothing.
		m.screen = m.leaveFrom
		return nil
	case screenHelp:
		m.screen = m.helpFrom
		return nil
	case screenUpdate:
		m.screen = m.updateFrom
		return nil
	case screenForm, screenSummary:
		return m.requestLeave(-1)
	case screenTargets:
		// A typed target or a remove question is dropped as esc drops it; a changed list still asks.
		m.targetEdit, m.targetRemove, m.targetAdd = -1, false, nil
		m.targetInput.Blur()
		return m.leaveTargets()
	case screenProviders:
		if m.wizard != nil {
			return m.leaveSetup()
		}
	case screenConfirm:
		if m.confirmRole == "" && m.pendingProfile == "" {
			// The delete question stands over the list.
			m.screen = screenList
			m.status = "Cancelled"
			return nil
		}
	}
	// A picker, table, prompt or question over a form steps back to the form, which keeps everything typed
	// into it; nothing it was about is written.
	m.secretInput.Reset()
	m.confirmRole, m.pendingProfile = "", ""
	m.screen = screenForm
	return nil
}

// focusNav hands the focus to the sidebar. The workspace keeps showing the list of the active section.
func (m *Model) focusNav() {
	m.screen = screenNav
	m.clearMessages()
}

// canSwitchSection reports whether a direct section key applies now. It does on the sidebar, the list, a
// form and the summary of the guided setup; a picker, table, prompt or confirmation is closed first, so a
// key meant for it never jumps away from what it is about.
func (m *Model) canSwitchSection() bool {
	switch m.screen {
	case screenNav, screenList, screenForm:
		return true
	case screenSummary:
		return m.wizard != nil && !m.wizard.saving
	}
	return false
}

// switchSection opens s from wherever a section key applies. Input that was not saved is never dropped
// or saved on the way: the leave question comes first.
func (m *Model) switchSection(s section) tea.Cmd {
	if m.screen == screenForm || m.screen == screenSummary {
		return m.requestLeave(int(s))
	}
	return m.openSection(s)
}

// requestLeave leaves the open form, or the guided setup, for the section to, or for the list of its own
// section when to is -1. Without unsaved input it leaves at once; otherwise it asks.
func (m *Model) requestLeave(to int) tea.Cmd {
	if m.wizard != nil && m.wizard.saving {
		// The save in flight decides; leaving now would leave its outcome without a place to show.
		return nil
	}
	if m.wizard != nil && m.wizard.saved != "" {
		cmd := m.finishSetup()
		if to >= 0 {
			return m.openSection(section(to))
		}
		return cmd
	}
	if !m.dirty() {
		return m.abandon(to)
	}
	m.leaveTo, m.leaveFrom = to, m.screen
	m.screen = screenLeave
	m.clearMessages()
	return nil
}

// abandon goes where a leave question pointed, dropping the form or the guided setup with everything
// typed into it. Nothing of it was written.
func (m *Model) abandon(to int) tea.Cmd {
	setup := m.wizard != nil
	m.wizard, m.fields = nil, nil
	var cmd tea.Cmd
	if to >= 0 {
		cmd = m.openSection(section(to))
	} else {
		cmd = m.returnToList("")
	}
	if setup {
		m.status = "Setup cancelled; nothing was written"
	}
	return cmd
}

// dirty reports whether leaving now would lose input. A guided setup with a chosen provider always holds
// some; a form does when any row differs from what it opened with.
func (m *Model) dirty() bool {
	if m.wizard != nil {
		return m.wizard.provider != ""
	}
	return m.screen == screenForm && m.formState() != m.pristine
}

// formState is what the rows of the form hold, in a form that compares whole. It holds a typed secret only
// in the guided setup, and only for the comparison; it is never drawn or stored.
func (m *Model) formState() string {
	var b strings.Builder
	for _, f := range m.fields {
		b.WriteString(f.label + "\x00" + f.value() + "\x00")
	}
	return b.String()
}

// updateLeave answers the leave question. s saves through the same path as enter and then goes on, d
// drops the input and goes on, and esc returns to the form unchanged. Nothing else answers it, not even
// y or n, whose meaning would be a guess, so a stray key neither saves nor discards.
func (m *Model) updateLeave(key tea.KeyMsg) tea.Cmd {
	if m.leaveFrom == screenTargets {
		return m.answerTargetsLeave(key)
	}
	switch key.String() {
	case "ctrl+c":
		return m.quit()
	case "esc":
		m.screen = m.leaveFrom
		m.status = "Still editing; nothing was saved or discarded"
	case "d":
		setup := m.wizard != nil
		cmd := m.abandon(m.leaveTo)
		if !setup {
			m.status = "Changes discarded; nothing was written"
		}
		return cmd
	case "s":
		if m.wizard != nil {
			// The guided setup saves only from its summary, after every step has been checked.
			return nil
		}
		return m.saveAndLeave()
	}
	return nil
}

// saveAndLeave saves the form like enter does and leaves it only when the save went through. A refused
// save keeps the form open with every input and the reason.
func (m *Model) saveAndLeave() tea.Cmd {
	m.screen = m.leaveFrom
	m.trimFields()
	if cmd := m.guardTypeChange(); cmd != nil {
		// This save has to ask the credential stores first and completes, or explains itself, when they
		// answered; the form stays open until then.
		return cmd
	}
	name := m.fields[0].value()
	cmd := m.save(name)
	if m.fail != "" {
		return cmd
	}
	if m.leaveTo >= 0 {
		cmd = m.openSection(section(m.leaveTo))
	} else {
		cmd = m.returnToList(name)
	}
	m.status = "Saved " + name
	return cmd
}

func (m *Model) updateForm(key tea.KeyMsg) tea.Cmd {
	if key.String() == "ctrl+s" {
		// ctrl+s saves, or goes on to the next setup step, from any row, since enter on a choice row opens it.
		if m.wizard != nil {
			m.setupNext()
			return nil
		}
		return m.submit()
	}
	switch m.fields[m.focus].kind {
	case fieldProvider, fieldChoice, fieldMultiChoice, fieldToolList:
		// Every choice row opens its values on enter, space, and /, the way a provider row opens its table,
		// rather than saving past it or going on to the next setup step.
		switch key.String() {
		case "enter", " ", "/":
			if m.fields[m.focus].kind == fieldProvider {
				m.openProviderTable()
			} else {
				m.openPicker()
			}
			return nil
		}
	case fieldTargets:
		// A target list opens like a choice row, and left/right fold it in the form like a tree.
		switch key.String() {
		case "enter", " ", "/":
			m.openTargets()
			return nil
		case "right", "l":
			m.fields[m.focus].expanded = true
			return nil
		case "left", "h":
			m.fields[m.focus].expanded = false
			return nil
		}
	}
	if m.wizard != nil {
		switch key.String() {
		case "esc":
			return m.requestLeave(-1)
		case "enter":
			m.setupNext()
			return nil
		case "ctrl+b":
			m.setupBack()
			return nil
		}
	}
	switch key.String() {
	case "esc":
		// Leaving a form never drops input silently: an unchanged form closes, a changed one asks first.
		return m.requestLeave(-1)
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
		// left/right step through the values in place, a quick way on a row of few values.
		previous := current.value()
		switch key.String() {
		case "left", "h":
			current.index = wrap(current.index-1, len(current.choices))
		case "right", "l":
			current.index = wrap(current.index+1, len(current.choices))
		default:
			return nil
		}
		return m.choiceChanged(previous)
	case fieldProvider, fieldMultiChoice, fieldToolList, fieldTargets:
		// Their values opened above; no other key changes them.
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
	if m.fields[m.focus].label == profileLabel {
		m.profileChosen()
		return nil
	}
	if m.wizard != nil {
		m.setupRefresh()
		return nil
	}
	switch m.fields[m.focus].label {
	case storageLabel:
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
	case toolsLabel:
		m.toolsModeChosen()
	}
	return nil
}

// openPicker opens the searchable list of the focused choice row, on its current value, with the filter
// ready for typing.
func (m *Model) openPicker() {
	f := m.fields[m.focus]
	if len(f.choices) == 0 {
		return
	}
	m.picker.text, m.pickerMarks = choiceText, nil
	if f.kind == fieldToolList || f.kind == fieldMultiChoice {
		m.pickerMarks = map[string]bool{}
		for choice, marked := range f.selected {
			m.pickerMarks[choice] = marked
		}
	}
	if f.kind == fieldToolList {
		m.picker.text = m.toolText
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
	if m.pickerMarks != nil {
		// A tool list or the permissions take any number of values: space ticks the selected one instead of
		// typing a blank, which no tool ID or permission contains, and enter keeps every tick at once.
		switch key.String() {
		case " ":
			if choice, ok := m.picker.selected(); ok {
				toggleMark(m.pickerMarks, choice, m.fields[m.focus].kind == fieldMultiChoice)
			}
			return nil
		case "enter":
			f := &m.fields[m.focus]
			f.selected = map[string]bool{}
			for choice, marked := range m.pickerMarks {
				if marked {
					f.selected[choice] = true
				}
			}
			m.screen = screenForm
			return nil
		}
	}
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
		return m.choose(choice)
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

// choose puts one of its values on the focused choice or provider row and brings the rows that depend on it
// in line. Taking the value the row already holds changes nothing.
func (m *Model) choose(choice string) tea.Cmd {
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
	// The note row shows the note of the chosen provider, unless it holds text typed for the previous one.
	if note := m.field(providerNoteLabel); note != nil && note.input.Value() == m.cfg.ProviderNotes[previous] {
		note.input.SetValue(m.cfg.ProviderNotes[m.fieldValue("provider")])
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
	return m.providerTargetHint(m.cfg.Services[service].Provider)
}

// providerTargetHint says what one target of the provider is and what the provider allows of the list:
// whether it may stay empty, and whether it holds more than one entry.
func (m *Model) providerTargetHint(provider string) string {
	metadata, _ := m.cfg.ProviderMetadata(provider)
	hint := metadata.Target.Description
	if metadata.Target.Required {
		hint += "; required for " + metadata.Name + ", so an empty list cannot be saved"
	}
	if !metadata.Target.Multiple {
		hint += "; one " + metadata.Target.Label + " at most"
	}
	return hint
}

// emptyTargets says what a connection without targets does for the provider.
func emptyTargets(metadata config.TargetMetadata) string {
	if metadata.Required {
		return "none yet: required, enter adds one"
	}
	return "none: the connection reaches whatever its credential reaches"
}

// connectionProviderChosen narrows the service and credential rows to the provider now selected. A route
// that binds a BookStack service to a Telegram credential cannot work, so it is not offered here.
func (m *Model) connectionProviderChosen() {
	provider := m.fieldValue(providerLabel)
	m.replaceChoices("service", m.providerServices(provider))
	m.replaceChoices("credential", m.providerCredentials(provider))
	if target := m.field(targetsLabel); target != nil {
		target.hint = m.targetHint(m.fieldValue("service"))
	}
	m.replacePermissionChoices(provider)
	if list := m.field(toolListLabel); list != nil {
		// Tool IDs carry their provider, so no tick survives a change of provider.
		list.choices, list.selected = m.toolChoices(provider, nil), map[string]bool{}
	}
	if m.editing == "" {
		m.applyRecommendedProfile()
	}
}

// toolsModeChosen opens the tool list for ticking only while only selected tools are offered; otherwise the
// row is stepped over, and keeps its ticks for when that mode is chosen again.
func (m *Model) toolsModeChosen() {
	list := m.field(toolListLabel)
	if list == nil {
		return
	}
	list.readOnly = m.fieldValue(toolsLabel) != toolsSelected
	list.hint = toolListHint
	if list.readOnly {
		list.hint = toolListOffHint
	}
}

// toolChoices are the tools a connection lists now, in its own order, followed by the other tools the
// provider registered, in ID order. A listed tool that is not registered is shown rather than silently
// dropped, and a saved list is written back in the order it was saved. The editor keeps no list of its own.
func (m *Model) toolChoices(provider string, current []string) []string {
	metadata, _ := m.cfg.ProviderMetadata(provider)
	var choices []string
	known := map[string]bool{}
	for _, tool := range current {
		if !known[tool] {
			choices = append(choices, tool)
			known[tool] = true
		}
	}
	for _, tool := range metadata.Tools {
		if !known[tool.ID] {
			choices = append(choices, tool.ID)
			known[tool.ID] = true
		}
	}
	return choices
}

// toolText is how one tool reads in the picker: its ID and effect, whether it is offered only when ticked
// here, and whether the permissions of the form allow that effect at all. The same text is what the filter
// searches.
func (m *Model) toolText(id string) string {
	metadata, _ := m.cfg.ProviderMetadata(m.formProvider())
	for _, tool := range metadata.Tools {
		if tool.ID != id {
			continue
		}
		text := id + "  " + string(tool.Effect)
		if tool.RequiresToolAllowList {
			text += "  (listed only)"
		}
		if !m.permittedEffects(metadata)[tool.Effect] {
			text += "  (not permitted)"
		}
		return text
	}
	return id + "  (not registered)"
}

// formProvider is the provider the form belongs to: the provider row of a connection form, or the provider
// the guided setup was started for, whose later steps carry no provider row of their own.
func (m *Model) formProvider() string {
	if m.wizard != nil {
		return m.wizard.provider
	}
	return m.fieldValue(providerLabel)
}

// toolFields are the tools mode row and the tool list row of a connection, over the tools it lists now.
func (m *Model) toolFields(provider string, current []string) []field {
	// A connection without a tools list offers every tool its permissions allow, and a new one starts
	// that way too, so the editor writes a list only when one is chosen here.
	mode := toolsAll
	if current != nil {
		mode = toolsSelected
	}
	tools := field{label: toolListLabel, kind: fieldToolList,
		choices: m.toolChoices(provider, current), selected: map[string]bool{}}
	for _, tool := range current {
		tools.selected[tool] = true
	}
	tools.readOnly, tools.hint = current == nil, toolListOffHint
	if current != nil {
		tools.hint = toolListHint
	}
	return []field{choiceField(toolsLabel, []string{toolsAll, toolsSelected}, mode).withHint(toolsHint), tools}
}

// profileChosen applies the profile now shown on the profile row. Ticks changed by hand, and the ticks of a
// saved connection, are replaced only after asking; custom itself changes nothing.
func (m *Model) profileChosen() {
	chosen := m.fieldValue(profileLabel)
	if chosen == profileCustom {
		return
	}
	if m.matchingProfile() == "" || (m.wizard == nil && m.editing != "") {
		m.pendingProfile = chosen
		m.screen = screenConfirm
		m.clearMessages()
		return
	}
	m.applyProfile(chosen)
}

// applyRecommendedProfile ticks the recommended profile of the form's provider, which is how a new
// connection starts. A provider without profiles leaves the rows as they are.
func (m *Model) applyRecommendedProfile() {
	metadata, _ := m.cfg.ProviderMetadata(m.formProvider())
	if profile, ok := metadata.RecommendedProfile(); ok {
		m.applyProfile(profile.ID)
	}
	m.syncProfile()
}

// applyProfile expands one profile into the rows below it: the permissions its tools need, only selected
// tools, and exactly its tools ticked. Every tick stays free to change before saving.
func (m *Model) applyProfile(id string) {
	metadata, _ := m.cfg.ProviderMetadata(m.formProvider())
	profile, ok := findProfile(metadata, id)
	if !ok {
		return
	}
	if permissions := m.field("permissions"); permissions != nil {
		permissions.selected = map[string]bool{}
		for _, permission := range metadata.ProfilePermissions(profile) {
			permissions.selected[string(permission)] = true
		}
	}
	if mode := m.field(toolsLabel); mode != nil {
		*mode = choiceField(toolsLabel, mode.choices, toolsSelected).withHint(mode.hint)
	}
	if list := m.field(toolListLabel); list != nil {
		list.selected = map[string]bool{}
		for _, tool := range profile.Tools {
			list.selected[tool] = true
		}
	}
	m.toolsModeChosen()
	m.syncProfile()
}

func findProfile(metadata config.ProviderMetadata, id string) (config.ToolProfile, bool) {
	for _, profile := range metadata.Profiles {
		if profile.ID == id {
			return profile, true
		}
	}
	return config.ToolProfile{}, false
}

// matchingProfile is the profile whose expansion the permission and tool ticks equal, or empty when they
// match none.
func (m *Model) matchingProfile() string {
	list := m.field(toolListLabel)
	if list == nil || m.fieldValue(toolsLabel) != toolsSelected {
		return ""
	}
	metadata, _ := m.cfg.ProviderMetadata(m.formProvider())
	ticked := list.marked()
	sort.Strings(ticked)
	for _, profile := range metadata.Profiles {
		tools := append([]string(nil), profile.Tools...)
		sort.Strings(tools)
		if m.fieldValue("permissions") == config.FormatPermissions(metadata.ProfilePermissions(profile)) &&
			strings.Join(tools, ",") == strings.Join(ticked, ",") {
			return profile.ID
		}
	}
	return ""
}

// syncProfile shows on the profile row what the ticks are now: a profile they match, or custom. The row is
// hidden for a provider that declares no profile.
func (m *Model) syncProfile() {
	row := m.field(profileLabel)
	if row == nil {
		return
	}
	metadata, _ := m.cfg.ProviderMetadata(m.formProvider())
	row.hidden = len(metadata.Profiles) == 0
	row.choices = nil
	for _, profile := range metadata.Profiles {
		row.choices = append(row.choices, profile.ID)
	}
	match := m.matchingProfile()
	if match == "" {
		row.choices = append(row.choices, profileCustom)
		row.index, row.hint = len(row.choices)-1, profileCustomHint+"; "+profileHint
		return
	}
	profile, _ := findProfile(metadata, match)
	*row = choiceField(profileLabel, row.choices, match)
	row.hint = profileText(metadata, profile) + "; " + profileHint
}

// profileText says what one profile is for and exactly what it ticks.
func profileText(metadata config.ProviderMetadata, profile config.ToolProfile) string {
	text := profile.Title + ": " + profile.Description
	if profile.MutationReason != "" {
		text += "; it preselects a change because " + profile.MutationReason
	}
	return text + "; ticks permissions " + config.FormatPermissions(metadata.ProfilePermissions(profile)) +
		" and tools " + strings.Join(profile.Tools, ", ")
}

// permittedEffects are the effects the permissions row of the form allows right now.
func (m *Model) permittedEffects(metadata config.ProviderMetadata) map[config.Permission]bool {
	permissions, err := config.ParsePermissions(m.fieldValue("permissions"))
	if err != nil {
		return nil
	}
	if permissions == nil {
		permissions = defaultPermissions(metadata)
	}
	permitted := map[config.Permission]bool{}
	for _, permission := range permissions {
		permitted[permission] = true
	}
	return permitted
}

// defaultPermissions are what a connection without a permissions list allows.
func defaultPermissions(metadata config.ProviderMetadata) []config.Permission {
	if len(metadata.DefaultPermissions) > 0 {
		return metadata.DefaultPermissions
	}
	return []config.Permission{config.PermissionRead}
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
	if target := m.field(targetsLabel); target != nil {
		target.hint = m.targetHint(m.fieldValue("service"))
	}
}

func (m *Model) permissionHint(provider string) string {
	metadata, _ := m.cfg.ProviderMetadata(provider)
	permissions := defaultPermissions(metadata)
	defaults := make([]string, len(permissions))
	for i, permission := range permissions {
		defaults[i] = string(permission)
	}
	return "enter opens the local agent permissions offered by this provider to tick; default currently means " +
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
	f.hint = m.permissionHint(provider)
}

func (m *Model) updateConfirm(key tea.KeyMsg) tea.Cmd {
	if profile := m.pendingProfile; profile != "" {
		switch key.String() {
		case "y":
			m.pendingProfile, m.screen = "", screenForm
			m.applyProfile(profile)
			m.status = "Profile " + profile + " ticked; review the permissions and tools before saving"
		case "n", "esc":
			m.pendingProfile, m.screen = "", screenForm
			m.status = "Profile not applied; the ticks are unchanged"
		case "ctrl+c":
			return m.quit()
		}
		return nil
	}
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

// startTest runs the connection test for the selected connection.
func (m *Model) startTest() tea.Cmd {
	if m.section != sectionConnections {
		return nil
	}
	name, ok := m.selected()
	if !ok {
		return nil
	}
	return m.testConnection(name)
}

// testConnection runs the connection test for one saved connection. The event loop keeps handling keys
// while it runs, so the editor never blocks.
func (m *Model) testConnection(name string) tea.Cmd {
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
			"to store it in the system keyring; the row says what to do if the keyring is locked or "+
			"unreachable", missing.Credential, missing.Role, missing.Role)
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
	if m.section == sectionConnections {
		// A new connection starts on the recommended profile, ticked and visible; a saved one opens as it
		// is saved, with the profile row only saying which profile its ticks match.
		if name == "" {
			m.applyRecommendedProfile()
		}
		m.syncProfile()
	}
	m.focus = m.firstEditable()
	m.applyFocus()
	m.pristine = m.formState()
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

// credentialType is the type the storage row of the credential form currently stands for.
func (m *Model) credentialType() string { return storageType(m.fieldValue(storageLabel)) }

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
			providerField(m.cfg.Providers(), s.Provider),
			textField("base url", s.BaseURL, false).withHint(baseURLHint),
		)
		if name == "" {
			metadata, _ := m.cfg.ProviderMetadata(fields[1].value())
			fields[2].input.SetValue(metadata.DefaultBaseURL)
		}
		// The note belongs to the provider, not to this service; it stands here because a service is
		// where a provider is set up.
		fields = append(fields, textField(providerNoteLabel, m.cfg.ProviderNotes[fields[1].value()], false).
			withHint(providerNoteHint))
	case sectionCredentials:
		cred := m.cfg.Credentials[name]
		// A new credential starts in the system keyring: that is the place this editor can complete on its
		// own, while environment variables need a shell the editor cannot reach.
		choice := storageKeyring
		if cred.Type != "" {
			choice = storageChoice(m.storagePlace(name, cred))
		}
		provider := m.credentialProvider(name, cred)
		fields = append(fields,
			providerField(m.credentialProviders(provider), provider).withHint(credentialProviderHint),
			storageField(choice),
		)
		fields = append(fields, m.roleFields(cred, fields[1].value(), storageType(choice))...)
	case sectionConnections:
		conn := m.cfg.Connections[name]
		// The provider is not stored on a connection: its service already names one. It stands here
		// because it decides what the two rows below may offer, and a route that mixes two systems is a
		// route that cannot work.
		provider := m.cfg.Services[conn.Service].Provider
		if provider == "" {
			provider = m.firstProviderWithService()
		}
		permissions := permissionField(m.permissionChoicesFor(provider, conn.Permissions), conn.Permissions)
		permissions.hint = m.permissionHint(provider)
		fields = append(fields,
			providerField(m.connectionProviders(), provider).withHint(connectionProviderHint),
			choiceField("service", m.providerServices(provider), conn.Service).withHint(connectionServiceHint),
			choiceField("credential", m.providerCredentials(provider), conn.Credential).
				withHint(connectionCredentialHint),
			targetsField(conn.TargetValues()),
			// Description and permissions explain the route the fields above define. Only the description
			// is published during discovery.
			textField("description", conn.Description, false).withHint(descriptionHint),
			field{label: profileLabel, kind: fieldChoice, hidden: true},
			permissions,
		)
		fields = append(fields, m.toolFields(provider, conn.Tools)...)
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
	choice := m.fieldValue(storageLabel)
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
		// Nothing is stored yet, so the reopened form cannot tell the unencrypted file from the keyring;
		// it keeps the place that was chosen.
		*m.field(storageLabel) = storageField(choice)
		m.pristine = m.formState()
		for i := range m.fields {
			if m.fields[i].kind == fieldSecret {
				m.focus = i
				break
			}
		}
		m.applyFocus()
		where := secret.StoreLabel(platform) + " (recommended)"
		if choice == storagePlaintext {
			where = "the " + storagePlaintext
		}
		m.status = "Credential saved. Add the required provider secrets below: press s on each role to " +
			"store it in " + where + "."
		return cmd
	}
	cmd := m.returnToList(name)
	m.status = "Saved " + name
	return cmd
}

func (m *Model) apply(cfg *config.Config, name string) error {
	switch m.section {
	case sectionServices:
		provider := m.fieldValue("provider")
		if err := cfg.SetService(name, config.Service{
			Provider: provider,
			BaseURL:  m.fieldValue("base url"),
			Options:  cfg.Services[name].Options,
		}); err != nil {
			return err
		}
		if provider == "" {
			return nil
		}
		return cfg.SetProviderNote(provider, m.fieldValue(providerNoteLabel))
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
		target, targets := splitTargets(targetEntries(m.fields))
		var tools []string
		if list := m.field(toolListLabel); list != nil && m.fieldValue(toolsLabel) == toolsSelected {
			tools = list.marked()
		}
		return cfg.SetConnection(name, config.Connection{
			Service:     m.fieldValue("service"),
			Credential:  m.fieldValue("credential"),
			Target:      target,
			Targets:     targets,
			Description: m.fieldValue("description"),
			Permissions: permissions,
			Tools:       tools,
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
		if !m.fields[next].readOnly && !m.fields[next].hidden {
			break
		}
	}
	m.focus = next
	m.applyFocus()
}

// firstEditable is where a form opens. A form whose first field is locked opens on the first field that
// takes input, so the user starts where typing has an effect. A provider row is passed over while another
// row takes input: it decides what every other row offers, and a form opened to change something else
// should not start on it.
func (m *Model) firstEditable() int {
	first := -1
	for i := range m.fields {
		if m.fields[i].readOnly || m.fields[i].hidden {
			continue
		}
		if m.fields[i].kind != fieldProvider {
			return i
		}
		if first < 0 {
			first = i
		}
	}
	return max(first, 0)
}

// trimFields makes the shown text the text that will be stored. value() ignores spaces at both edges, so a
// pasted " https://wiki " is saved without them; without this the form would keep showing spaces that never
// reach the file. Trimming happens when the user leaves a field or saves, never while typing.
func (m *Model) trimFields() {
	for i := range m.fields {
		f := &m.fields[i]
		if f.kind == fieldChoice || f.kind == fieldProvider || f.kind == fieldMultiChoice || f.kind == fieldToolList ||
			f.kind == fieldMasked || f.kind == fieldTargets {
			continue
		}
		if trimmed := strings.TrimSpace(f.input.Value()); trimmed != f.input.Value() {
			f.input.SetValue(trimmed)
		}
	}
}

func (m *Model) applyFocus() {
	for i := range m.fields {
		if i == m.focus && m.fields[i].kind != fieldChoice && m.fields[i].kind != fieldProvider &&
			m.fields[i].kind != fieldMultiChoice && m.fields[i].kind != fieldToolList &&
			m.fields[i].kind != fieldTargets && !m.fields[i].readOnly {
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

// providerField is the provider row of a form, chosen in the provider table.
func providerField(choices []string, value string) field {
	f := choiceField(providerLabel, choices, value)
	f.kind = fieldProvider
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

// toggleMark ticks or unticks one value of a set of ticks. In the permissions, default stands for the
// provider's own set, so ticking it drops every explicit permission and ticking one of those drops default.
func toggleMark(marks map[string]bool, choice string, permissions bool) {
	if permissions && choice == "default" {
		wasDefault := marks["default"]
		clear(marks)
		if !wasDefault {
			marks["default"] = true
		}
		return
	}
	if permissions {
		delete(marks, "default")
	}
	if marks[choice] {
		delete(marks, choice)
	} else {
		marks[choice] = true
	}
}

// targetsLabel is the row of a connection's target list.
const targetsLabel = "targets"

// targetsField is the target list row over the targets a connection holds now.
func targetsField(values []string) field {
	return field{label: targetsLabel, kind: fieldTargets, entries: values}
}

// targetEntries are the entries of the target list among fields, or none when there is no such row.
func targetEntries(fields []field) []string {
	for _, f := range fields {
		if f.kind == fieldTargets {
			return f.entries
		}
	}
	return nil
}

// splitTargets is how a target list is written: one entry as target, several as targets, and none as
// neither, so a file that names one target reads as it always did.
func splitTargets(values []string) (string, []string) {
	switch len(values) {
	case 0:
		return "", nil
	case 1:
		return values[0], nil
	}
	return "", append([]string(nil), values...)
}

// targetSummary is the short form of a target list: a single target as it is, and otherwise how many there
// are with the first two of them.
func targetSummary(values []string) string {
	switch len(values) {
	case 0:
		return ""
	case 1:
		return values[0]
	}
	text := fmt.Sprintf("%d targets: %s", len(values), strings.Join(values[:2], ", "))
	if len(values) > 2 {
		text += fmt.Sprintf(", +%d", len(values)-2)
	}
	return text
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

// altSectionShortcut is the section key that works in a form too, where the digits alone are text.
func altSectionShortcut(key string) (section, bool) {
	if !strings.HasPrefix(key, "alt+") {
		return 0, false
	}
	return sectionShortcut(strings.TrimPrefix(key, "alt+"))
}

// The colours are few and taken from the terminal's own palette, so they follow its theme. None of them
// carries meaning on its own: focus has its marker and its border, a result its "[ok]", "[failed]",
// "warning:" or "error:" prefix, and a terminal without colour, or with NO_COLOR set, loses nothing.
var (
	accent       = lipgloss.Color("6")
	titleStyle   = lipgloss.NewStyle().Bold(true)
	activeStyle  = lipgloss.NewStyle().Bold(true).Foreground(accent)
	hintStyle    = lipgloss.NewStyle().Faint(true)
	okStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("2"))
	failStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1"))
	warningStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3"))
	focusBorder  = lipgloss.NewStyle().Foreground(accent)
	quietBorder  = lipgloss.NewStyle().Faint(true)
)

// View renders the frame: the configuration path, the sections, and the workspace with the current screen.
func (m *Model) View() string {
	if m.quitting {
		return ""
	}
	if m.terminalTooSmall() {
		if m.screen == screenLeave {
			return m.boundTo(m.leaveView(), m.termWidth, m.termHeight)
		}
		return m.resizeView()
	}
	return m.frame(m.workspaceView())
}

// workspaceView is the current screen, or, when it cannot fit the workspace, what it needs and the way out
// of it. The sections and the path stay on screen either way.
func (m *Model) workspaceView() string {
	view := m.editorView()
	if m.screen == screenLeave || m.viewFits(view) {
		return view
	}
	width, height := lipgloss.Width(view), lipgloss.Height(view)
	way := "esc leave, asking first if anything changed"
	if m.screen == screenNav || m.screen == screenList {
		way = "q quit"
	}
	return m.boundView(wrapCells(fmt.Sprintf("Resize terminal. Need %dx%d. %s.",
		width+m.termWidth-m.width, height+m.termHeight-m.height, way), m.width))
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
	case screenNav, screenList:
		return m.listView()
	case screenPicker:
		return m.pickerView()
	case screenProviders:
		return m.providerTableView()
	case screenHelp:
		return m.helpView()
	case screenUpdate:
		return m.updateView() + m.notes()
	case screenTargets:
		return m.targetsView()
	}
	var b strings.Builder
	switch m.screen {
	case screenForm:
		what := "New " + m.section.entry()
		if m.editing != "" {
			what = "Edit " + m.editing
		}
		if m.wizard != nil {
			b.WriteString(m.setupHeading(dense))
		} else {
			b.WriteString(titleStyle.Render(what) + "\n\n")
		}
		for i, f := range m.fields {
			if f.hidden {
				continue
			}
			// A field and its hint are one block, and the blank line stands between the blocks. Without it
			// the hint is as close to the next field as to its own, and an indented line under a row of
			// rows reads as the introduction to what follows rather than as the note of what precedes.
			// A dense form has no blocks to separate: it carries one hint, under the field it belongs to.
			if i > 0 && !dense {
				b.WriteString("\n")
			}
			b.WriteString(m.formRow(i == m.focus, f.label, m.renderField(f, i == m.focus)) + "\n")
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
		keys := "tab move · " + formKeys
		switch m.fields[m.focus].kind {
		case fieldChoice:
			keys = "enter choose · left/right switch · tab move · " + choiceFormKeys
		case fieldMultiChoice, fieldToolList:
			keys = "enter tick · tab move · " + choiceFormKeys
		case fieldProvider:
			keys = "enter choose provider in the table · tab move · " + choiceFormKeys
		case fieldTargets:
			keys = "enter edit list · right/left expand/collapse · tab move · " + choiceFormKeys
		}
		if m.fields[m.focus].kind == fieldSecret {
			if m.editing == "" {
				keys = "enter save credential first · tab move · " + leaveKeys
			} else {
				keys = m.secretKeys() + " · " + keys
			}
		}
		if m.wizard != nil {
			keys = strings.Replace(keys, formKeys, setupKeys(m.wizard.step, "enter"), 1)
			keys = strings.Replace(keys, choiceFormKeys, setupKeys(m.wizard.step, "ctrl+s"), 1)
		}
		b.WriteString(m.hint(keys))
	case screenPlaintextConfirm:
		if m.wizard != nil {
			b.WriteString(m.setupPlaintextView())
			break
		}
		b.WriteString(titleStyle.Render("Store this secret unencrypted?") + "\n\n")
		b.WriteString(m.wrapped(failStyle,
			fmt.Sprintf("This does not use the system keyring. It writes %s.%s as readable text for your "+
				"user account into %s.", m.editing, m.secretRole, m.plaintextPath())) + "\n")
		b.WriteString(m.indented("Choose no if you want the "+placeKeyring+": choose "+storageKeyring+
			" in the "+storageLabel+" row, unlock or configure "+secret.StoreLabel(platform)+
			" if needed, and press s.") + "\n")
		b.WriteString(m.hint("y continue to masked input · n/esc cancel without writing"))
	case screenSecret:
		where := secret.StoreLabel(platform) + " of this machine"
		if m.secretPlain {
			where = "the " + placePlaintext + " " + m.plaintextPath()
		}
		b.WriteString(titleStyle.Render(fmt.Sprintf("Secret for %s.%s", m.editing, m.secretRole)) + "\n\n")
		b.WriteString("  " + m.secretInput.View() + "\n")
		b.WriteString(m.indented(
			"the value is masked while you type, is never shown back, and goes into "+where) + "\n")
		b.WriteString(m.hint("enter store · esc cancel"))
	case screenConfirm:
		if m.pendingProfile != "" {
			b.WriteString(m.profileConfirmView())
			break
		}
		if m.confirmRole != "" {
			b.WriteString(fmt.Sprintf("Remove the stored secret for %s.%s?\n", m.editing, m.confirmRole))
			b.WriteString(m.indented(
				"it is removed from the system keyring and from the unencrypted file; an environment "+
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
	case screenSummary:
		b.WriteString(m.summaryView())
	case screenLeave:
		b.WriteString(m.leaveView())
	}

	b.WriteString(m.notes())
	return b.String()
}

// formKeys ends the key line of every form: how it is saved and, in leaveKeys, the two ways out of it, both
// of which ask before unsaved input is lost. A section key in a form carries alt, because the digits are
// text there. On a choice row enter opens the values, so choiceFormKeys names ctrl+s, which saves from
// every row.
const (
	leaveKeys      = "esc leave · alt+1-4 section"
	formKeys       = "enter save · " + leaveKeys
	choiceFormKeys = "ctrl+s save · " + leaveKeys
)

// leaveView is the leave question. It names what would be lost and where the user was going, and offers
// exactly the answers that apply; they come right under the warning, so even a tiny terminal shows them.
func (m *Model) leaveView() string {
	what := "New " + m.section.entry()
	if m.editing != "" {
		what = m.editing
	}
	where := "the " + m.section.title() + " list"
	if m.leaveTo >= 0 {
		where = section(m.leaveTo).title()
	}
	warning, keys := "warning: unsaved changes in "+what, "s save and go on · d discard · esc keep editing"
	why := "Leaving for " + where + " would lose them."
	if m.wizard != nil {
		warning, keys = "warning: the guided setup is not saved", "d discard setup · esc keep editing"
		why = "Leaving for " + where + " drops every step, typed secrets included. Nothing was written yet."
	}
	if m.leaveFrom == screenTargets {
		warning = "warning: the target list changed"
		keys = "k keep the list · d discard changes · esc keep editing"
		why = "Closing the list without keeping it would lose the changes. Nothing was written yet."
	}
	return m.wrapped(warningStyle, warning) + "\n" + m.wrapped(hintStyle, keys) + "\n\n" +
		m.wrapped(lipgloss.NewStyle(), why)
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
	row := m.listRows()
	var b strings.Builder
	b.WriteString(header)
	start, end := m.windowIn(&m.list, header, footer, row)
	for i := start; i < end; i++ {
		b.WriteString(row(i) + "\n")
	}
	b.WriteString(footer)
	return b.String()
}

// listFrame is everything of the list screen except its rows: above them the heading, the position of the
// selection, the filter and the column names; below them the test result, the keys and the notes. The rows
// get the lines that are left, so a list of any length fits the terminal and only the rows scroll.
func (m *Model) listFrame() (string, string) {
	var head strings.Builder
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
	if total > 0 {
		t := m.listTable()
		_, width := m.fit("  ")
		head.WriteString(hintStyle.Render(clipCells("  "+t.header(t.layout(width)), m.usable(0))) + "\n")
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
	case m.screen == screenNav:
		keys = "up/down section · enter open list · 1-4 open · n new · c setup · ? help · q quit"
	case m.list.editing:
		keys = "type to filter · up/down move · enter keep filter · esc clear filter"
	case m.section == sectionConnections:
		keys = "/ filter · n new · enter edit · d delete · t test · c guided setup · 1-4 or left sections · " +
			"? help · q quit"
	default:
		keys = "/ filter · n new · enter edit · d delete · c guided setup · 1-4 or left sections · ? help · q quit"
	}
	if m.screen == screenList && !m.list.editing && m.list.query() != "" {
		keys += " · esc clear filter"
	}
	foot := m.hint(keys)
	if m.section == sectionConnections {
		for _, name := range m.list.all {
			if m.needsDescription(name) {
				foot = m.hint(undescribedHint) + foot
				break
			}
		}
		// A connection that offers no tool is valid, so it is named rather than refused, with the same
		// words 'qatlas config validate' uses.
		var idle strings.Builder
		for _, name := range m.list.all {
			if warning := m.cfg.IdleWarning(name); warning != "" {
				idle.WriteString("\n" + m.wrapped(warningStyle, "warning: "+warning))
			}
		}
		foot = m.testLine() + idle.String() + foot
	}
	return head.String(), foot + m.notes()
}

// filterLine shows the filter of l: while it is typed with its cursor, afterwards as the text it holds.
func (m *Model) filterLine(l *filterList) string { return m.searchLine(l, "filter: ") }

// searchLine shows the filter of l behind the given label.
func (m *Model) searchLine(l *filterList, label string) string {
	prefix, width := m.fit(label)
	if !l.editing {
		return prefix + truncateCells(l.query(), width)
	}
	in := l.input
	in.Width = max(width-1, 1)
	return prefix + in.View()
}

// listRows draws the shown entry at index i of the list as a row of its table, with the columns laid out
// once for all rows. The selection carries the focus marker only while the list has the focus, so the screen
// never shows two.
func (m *Model) listRows() func(i int) string {
	t := m.listTable()
	_, width := m.fit("  ")
	layout := t.layout(width)
	return func(i int) string {
		name := m.list.matches[i]
		return m.row(i == m.list.cursor && m.screen != screenNav, t.line(t.rows[name], layout))
	}
}

// listWindow is the range of shown entries whose rows fit between the frame of the list screen.
func (m *Model) listWindow() (int, int) {
	header, footer := m.listFrame()
	return m.windowIn(&m.list, header, footer, m.listRows())
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
	keys := "type to filter · up/down move · enter choose · esc cancel"
	if m.pickerMarks != nil {
		ticked := 0
		for _, choice := range m.picker.all {
			if m.pickerMarks[choice] {
				ticked++
			}
		}
		head.WriteString(m.wrapped(hintStyle, fmt.Sprintf("ticked: %d of %d", ticked, total)) + "\n")
		keys = "type to filter · up/down move · space tick · enter keep · esc cancel"
	} else {
		head.WriteString(m.wrapped(hintStyle, "current: "+choiceText(f.value())) + "\n")
	}
	if shown == 0 {
		head.WriteString(m.wrapped(hintStyle,
			fmt.Sprintf("No value matches %q. esc keeps the current one.", m.picker.query())) + "\n")
	}
	return head.String(), m.hint(keys) + m.notes()
}

// pickerRow draws the shown value at index i of the picker and marks the value the row holds now.
func (m *Model) pickerRow(i int) string {
	choice := m.picker.matches[i]
	if m.pickerMarks != nil {
		mark := "[ ] "
		if m.pickerMarks[choice] {
			mark = "[x] "
		}
		return m.row(i == m.picker.cursor, mark+m.picker.text(choice))
	}
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
	case screenNav, screenList:
		m.list.offset, _ = m.listWindow()
	case screenPicker:
		m.picker.offset, _ = m.pickerWindow()
	case screenProviders:
		m.providers.list.offset, _ = m.providerTableWindow()
	case screenTargets:
		if m.targetAdd != nil {
			m.targetAdd.choices.offset, _ = m.addWindow()
			return
		}
		m.targetList.offset, _ = m.targetWindow()
	}
}

func (m *Model) terminalTooSmall() bool {
	return m.termWidth < minimumWidth || m.termHeight < minimumHeight
}

// activeScreenTooSmall reports whether the current screen cannot be used at this size. The leave question
// is always answerable: it is the one screen whose keys protect unsaved input.
func (m *Model) activeScreenTooSmall() bool {
	if m.screen == screenLeave {
		return false
	}
	return m.terminalTooSmall() || !m.viewFits(m.editorView())
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

// resizeView is deliberately useful even at one cell by one row: the way out remains visible, while
// additional room reveals the requested size. Rendering it does not change the screen, focus, form, or
// test state, so resizing back resumes exactly where the user was.
func (m *Model) resizeView() string {
	way := "q quit"
	if m.screen != screenNav && m.screen != screenList {
		way = "esc leave"
	}
	lines := []string{way, "Resize terminal", fmt.Sprintf("Need %dx%d", minimumWidth, minimumHeight)}
	return m.boundTo(strings.Join(lines, "\n"), m.termWidth, m.termHeight)
}

// sidebarLayout reports whether the sections stand in a sidebar beside the workspace.
func (m *Model) sidebarLayout() bool { return m.termWidth >= sidebarMinWidth }

// layoutWorkspace derives the room of the workspace from the terminal. Above it stands one line with the
// configuration path; beside it the sidebar with its frame, or above it one navigation line and a rule.
func (m *Model) layoutWorkspace() {
	if m.sidebarLayout() {
		m.width = max(m.termWidth-sidebarWidth-1-frameCells, 1)
		m.height = max(m.termHeight-1-2, 1)
		return
	}
	m.width = max(m.termWidth, 1)
	m.height = max(m.termHeight-3, 1)
}

// frame puts the workspace into the editor's frame.
func (m *Model) frame(workspace string) string {
	lines := []string{m.pathLine()}
	content := strings.Split(workspace, "\n")
	if !m.sidebarLayout() {
		lines = append(lines, m.navLine(), quietBorder.Render(strings.Repeat("─", m.termWidth)))
		lines = append(lines, content...)
		return m.boundTo(strings.Join(lines, "\n"), m.termWidth, m.termHeight)
	}
	title := m.section.title()
	switch {
	case m.screen == screenHelp:
		title = "Help"
	case m.screen == screenUpdate:
		title = "Update"
	case m.wizard != nil:
		title = "Guided setup"
	}
	left := box("Sections", m.sidebarLines(), sidebarWidth-frameCells, m.height, m.screen == screenNav)
	right := box(title, content, m.width, m.height, m.screen != screenNav)
	for i := range left {
		lines = append(lines, left[i]+" "+right[i])
	}
	return strings.Join(lines, "\n")
}

// pathLine names the configuration file the editor works on. A path too long for the line keeps its end,
// the part that tells files apart. A newer release stands between the title and the path, in the longest
// form that still leaves the path some room.
func (m *Model) pathLine() string {
	suffix := ""
	if !m.configExists {
		suffix = " (created on first save)"
	}
	title := "Qatlas setup  "
	if m.termWidth < 60 {
		title = ""
	}
	banner := m.updateBanner(m.termWidth - lipgloss.Width(title) - 2 - pathRoom)
	if banner != "" {
		banner += "  "
	}
	rest := m.termWidth - lipgloss.Width(title+banner)
	label := "Config: "
	if banner != "" && lipgloss.Width(label+suffix)+pathRoom > rest {
		// Beside the banner a narrow line keeps the end of the path and drops its label.
		label, suffix = "", ""
	}
	path := truncateLeft(m.store.Path(), rest-lipgloss.Width(label+suffix))
	return titleStyle.Render(title) + okStyle.Render(banner) + hintStyle.Render(label+path+suffix)
}

// pathRoom is what the banner of a newer release leaves the path at least.
const pathRoom = 12

// sidebarLines are the rows of the sidebar: the sections with their entry counts, the guided setup, the help,
// quitting, and the next step while there is room. The active section is marked "> " while the sidebar has
// the focus and "* " while the workspace has it; the marker, not the colour, is what says so.
func (m *Model) sidebarLines() []string {
	inner := sidebarWidth - frameCells
	var lines []string
	for s := section(0); s < sectionCount; s++ {
		text := fmt.Sprintf("%d %-11s%3d", int(s)+1, s.title(), len(m.entryNames(s)))
		switch {
		case s != m.section || m.wizard != nil:
			lines = append(lines, "  "+text)
		case m.screen == screenNav:
			lines = append(lines, activeStyle.Render("> "+text))
		default:
			lines = append(lines, titleStyle.Render("* "+text))
		}
	}
	setup := "  c guided setup"
	if m.wizard != nil {
		setup = activeStyle.Render("> c guided setup")
	}
	lines = append(lines, "", setup, "  ? help", "  q quit")
	if next := render(hintStyle, inner, "next: "+m.nextStep()); len(lines)+1+strings.Count(next, "\n")+1 <= m.height {
		lines = append(lines, "")
		lines = append(lines, strings.Split(next, "\n")...)
	}
	return lines
}

// navLine is the navigation of a narrow terminal: every section in one line, the active one in brackets,
// and "> " in front while it has the focus. A terminal too narrow for the names keeps the active name and
// the numbers of the others.
func (m *Model) navLine() string {
	focus := "  "
	if m.screen == screenNav {
		focus = "> "
	}
	full, short := []string{}, []string{}
	for s := section(0); s < sectionCount; s++ {
		name := fmt.Sprintf("%d %s", int(s)+1, s.title())
		if s == m.section && m.wizard == nil {
			full, short = append(full, "["+name+"]"), append(short, "["+name+"]")
			continue
		}
		full, short = append(full, " "+name+" "), append(short, fmt.Sprint(int(s)+1))
	}
	if m.wizard != nil {
		full, short = append(full, "[c setup]"), append(short, "[c setup]")
	}
	line := focus + strings.Join(full, " ")
	if lipgloss.Width(line) > m.termWidth {
		line = focus + strings.Join(short, " ")
	}
	return activeStyle.Render(clipCells(line, m.termWidth))
}

// box draws a frame of inner width by rows around lines, with its title in the top border. The frame with
// the focus is drawn double, the other one single, so focus reads without colour as well.
func box(title string, lines []string, inner, rows int, focused bool) []string {
	style, h, v, corners := quietBorder, "─", "│", [4]string{"╭", "╮", "╰", "╯"}
	if focused {
		style, h, v, corners = focusBorder, "═", "║", [4]string{"╔", "╗", "╚", "╝"}
	}
	label := truncateCells(" "+title+" ", inner+1)
	out := []string{style.Render(corners[0]+h) + titleStyle.Render(label) +
		style.Render(strings.Repeat(h, max(inner+1-lipgloss.Width(label), 0))+corners[1])}
	for i := 0; i < rows; i++ {
		text := ""
		if i < len(lines) {
			text = lines[i]
		}
		out = append(out, style.Render(v)+" "+padLine(text, inner)+" "+style.Render(v))
	}
	return append(out, style.Render(corners[2]+strings.Repeat(h, inner+2)+corners[3]))
}

func (m *Model) needsDescription(name string) bool {
	connection := m.cfg.Connections[name]
	provider := m.cfg.Services[connection.Service].Provider
	if connection.Description != "" || provider == "" {
		return false
	}
	for other, candidate := range m.cfg.Connections {
		if other != name && m.cfg.Services[candidate.Service].Provider == provider {
			return true
		}
	}
	return false
}

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
	case len(m.cfg.Connections) == 0:
		// The guided setup creates whatever of service and credential is still missing, so it is the
		// next step however far the expert sections got.
		return "press c to set up a connection step by step"
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
	lines := strings.Split(render(style, width, text), "\n")
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
	if f.kind == fieldProvider {
		return providerHint(f)
	}
	if f.kind == fieldToolList && !f.readOnly && len(f.marked()) > 0 {
		// The ticks are what is saved, so they stand in full under the row that holds them.
		return f.hint + "; ticked: " + strings.Join(f.marked(), ", ")
	}
	return f.hint
}

func (m *Model) fieldWarning(f field) string {
	if f.kind != fieldTargets {
		return ""
	}
	provider := m.cfg.Services[m.fieldValue("service")].Provider
	if m.wizard != nil {
		provider = m.wizard.provider
	}
	metadata, ok := m.cfg.ProviderMetadata(provider)
	if !ok || metadata.Target.Wildcard == "" || metadata.Target.WildcardWarning == "" {
		return ""
	}
	for _, value := range f.entries {
		if value == metadata.Target.Wildcard {
			return metadata.Target.WildcardWarning
		}
	}
	return ""
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
		return "\n" + m.wrapped(okStyle,
			fmt.Sprintf("[ok] %s: ok - %s accepted the connection", m.testName, providerName))
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
			provider.ClassNotFound: providerName + " did not find the target; check the target and whether the " +
				"credential may see it",
		}[m.testClass]
		if providerName == "BookStack" && m.testClass == provider.ClassAuth {
			explanation = "BookStack rejected the token or its user lacks permission"
		}
		return "\n" + m.wrapped(failStyle,
			fmt.Sprintf("[failed] %s: %s - %s", m.testName, m.testClass, explanation))
	}
	return ""
}

// sectionColumns are the columns of the list of each section. The name leads, the free text of a section
// is the first to give way, and a column that only adds detail to the others may be left out of a narrow
// terminal.
var sectionColumns = map[section][]column{
	sectionServices: {{title: "NAME"}, {title: "PROVIDER", optional: true}, {title: "BASE URL", flex: true}},
	sectionCredentials: {{title: "NAME"}, {title: "PROVIDER", optional: true},
		{title: "STORAGE", optional: true, short: map[string]string{
			placeKeyring: "keyring", placeEnv: "env", placePlaintext: "file"}},
		{title: "SECRETS", flex: true}},
	sectionConnections: {{title: "NAME"}, {title: "SERVICE"}, {title: "EFFECTS", optional: true},
		{title: "TOOLS", optional: true}, {title: "TARGETS", optional: true}, {title: "DESCRIPTION", flex: true}},
	sectionDefaults: {{title: "PROVIDER OR TOOL"}, {title: "CONNECTION"}},
}

// listTable is the table of every entry of the section, filtered out or not.
func (m *Model) listTable() table {
	t := table{columns: sectionColumns[m.section], rows: map[string][]string{}}
	for _, name := range m.list.all {
		t.rows[name] = m.cells(name)
	}
	return t
}

// describe is what the filter of the list looks at for one entry: every cell of its row, so what can be
// read can be found. It never holds a secret value.
func (m *Model) describe(name string) string { return strings.Join(m.cells(name), columnGap) }

// cells are the values of one entry in the columns of its section. They never show a secret value.
func (m *Model) cells(name string) []string {
	switch m.section {
	case sectionServices:
		s := m.cfg.Services[name]
		return []string{name, s.Provider, s.BaseURL}
	case sectionCredentials:
		// Where the secrets are kept decides what the credential is, so it is a column rather than
		// something the reader has to open the form to find out.
		cred := m.cfg.Credentials[name]
		provider := m.credentialProvider(name, cred)
		credentialRoles := m.credentialRoles(provider)
		roles := make([]string, 0, len(credentialRoles))
		for _, role := range credentialRoles {
			source := m.envSource(cred.Values[role])
			if cred.Type == config.CredentialTypeKeyring {
				source = m.storedSource(name, role)
			}
			roles = append(roles, role+" "+secretState(source))
		}
		return []string{name, providerColumn(provider), m.storagePlace(name, cred), strings.Join(roles, ", ")}
	case sectionConnections:
		conn := m.cfg.Connections[name]
		effects := make([]string, 0, len(config.Permissions()))
		for _, permission := range m.cfg.ConnectionPermissions(name) {
			effects = append(effects, string(permission))
		}
		tools := "all"
		if conn.Tools != nil {
			tools = fmt.Sprintf("listed %d", len(conn.Tools))
			if len(conn.Tools) == 0 {
				tools = "none"
			}
		}
		// A single target stands as it is, several by their number: the form lists them.
		targets, values := "-", conn.TargetValues()
		switch {
		case len(values) == 1:
			targets = values[0]
		case len(values) > 1:
			targets = fmt.Sprintf("%d targets", len(values))
		}
		description := conn.Description
		if description == "" && m.needsDescription(name) {
			description = undescribedMarker
		}
		return []string{name, conn.Service, strings.Join(effects, ","), tools, targets, description}
	case sectionDefaults:
		return []string{name, m.cfg.Defaults.Connections[name]}
	}
	return []string{name}
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
	case f.kind == fieldMasked && focused:
		// The input draws one mask character per typed character and never the characters themselves.
		value = f.input.View()
	case f.kind == fieldMasked && f.input.Value() != "":
		value = "(entered, masked)"
	case f.kind == fieldMasked:
		value = hintStyle.Render("(empty)")
	case f.kind == fieldProvider:
		value = m.providerValue(f)
	case f.kind == fieldChoice && len(f.choices) == 0:
		value = "(nothing to choose)"
	case f.kind == fieldChoice:
		value = "< " + value + " >"
	case f.kind == fieldToolList && f.readOnly:
		value = hintStyle.Render("(every tool the permissions allow)")
	case f.kind == fieldToolList && len(f.marked()) == 0:
		value = fmt.Sprintf("none of %d ticked: no tool is offered", len(f.choices))
	case f.kind == fieldToolList:
		value = fmt.Sprintf("%d of %d ticked", len(f.marked()), len(f.choices))
	case f.kind == fieldTargets && len(f.entries) == 0:
		metadata, _ := m.cfg.ProviderMetadata(m.formProvider())
		value = hintStyle.Render("(" + emptyTargets(metadata.Target) + ")")
	case f.kind == fieldTargets && f.expanded:
		// Every target stands on a line of its own, under how many there are.
		value = fmt.Sprintf("%d targets\n%s", len(f.entries), strings.Join(f.entries, "\n"))
		if len(f.entries) == 1 {
			value = "1 target\n" + f.entries[0]
		}
	case f.kind == fieldTargets:
		value = targetSummary(f.entries)
	case f.kind == fieldMultiChoice:
		parts := make([]string, len(f.choices))
		for i, choice := range f.choices {
			mark := " "
			if f.selected[choice] {
				mark = "x"
			}
			parts[i] = "[" + mark + "] " + choice
		}
		// A box is never split from its value: the row breaks between the values only.
		var lines []string
		for _, part := range parts {
			if last := len(lines) - 1; last >= 0 && lipgloss.Width(lines[last]+"  "+part) <= m.usable(formIndent) {
				lines[last] += "  " + part
				continue
			}
			lines = append(lines, part)
		}
		value = strings.Join(lines, "\n")
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
	return value
}

// formIndent is where the value of a form row starts: the focus marker and the label column.
const formIndent = 15

// formRow draws one form row: the focus marker, the label, and the value, which continues under itself
// when it does not fit instead of pushing past the right edge.
func (m *Model) formRow(active bool, label, value string) string {
	marker, style := "  ", lipgloss.NewStyle()
	if active {
		marker, style = "> ", activeStyle
	}
	head := fmt.Sprintf("%s%-12s ", marker, label)
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

// hint is the key line of a screen. Like every other prose line it is wrapped into the terminal instead of
// being cut off at its right edge.
func (m *Model) hint(text string) string { return "\n" + m.wrapped(hintStyle, text) }

// Run starts the editor on the given terminal streams. The updater may be nil, in which case the editor does
// not look for a newer release.
func Run(store *config.Store, tester Tester, secrets Secrets, redactor *redact.Redactor, updater Updater,
	in, out *os.File) error {
	model, err := New(store, tester, secrets, redactor)
	if err != nil {
		return err
	}
	model.updater = updater
	_, err = tea.NewProgram(model, tea.WithInput(in), tea.WithOutput(out)).Run()
	return err
}

// profileConfirmView asks before a profile replaces ticks that were changed by hand or that belong to a
// saved connection. It names exactly what the profile ticks.
func (m *Model) profileConfirmView() string {
	metadata, _ := m.cfg.ProviderMetadata(m.formProvider())
	profile, _ := findProfile(metadata, m.pendingProfile)
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Replace the permission and tool ticks with profile %s?\n", profile.ID))
	b.WriteString(m.indented(profileText(metadata, profile)) + "\n")
	b.WriteString(m.indented("every tick stays changeable afterwards; nothing is saved before you save the form") + "\n")
	b.WriteString(m.hint("y replace the ticks · n keep them"))
	return b.String()
}
