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
	"maps"
	"os"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/manage"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/selfupdate"
	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
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
	// sectionVault is a settings form, not a list of named entries: it shows the vault's state and the
	// actions that apply to it, and edits vault.idle_timeout and vault.admin_timeout. See vaultsettings.go.
	sectionVault
	// sectionApprovals lists the connections an encrypted vault has not approved as they are configured
	// now: a new connection reading a vault credential, or one whose scope changed since it was last
	// approved. See approvals.go.
	sectionApprovals
	// sectionTokens lists the agent tokens of an encrypted vault, with their vorbild connections and expiry,
	// and creates, shows, and revokes them in an admin session. See tokens.go.
	sectionTokens
	// sectionLogs is the invocation log, read only: its own screen with a day, range or all days, filters,
	// search, and the status of every entry. See logs.go.
	sectionLogs
	// sectionOverview is the start of the editor. It holds no entries; see overview.go.
	sectionOverview
	// sectionGeneral holds the device-local preferences (theme, start view, icons), a settings form
	// that needs no admin session. sectionTransfer, sectionUpdate and sectionSelftest are pages of action
	// rows that start the export and import, the update and the selftests. See settings.go.
	sectionGeneral
	sectionTransfer
	sectionUpdate
	sectionSelftest
	sectionCount
)

// key is the English name of the section: the name the commands are named after, whatever language the
// editor draws in.
func (s section) key() string {
	return [...]string{"Services", "Credentials", "Connections", "Defaults", "Vault", "Approvals", "Tokens", "Logs",
		"Overview", "General", "Transfer", "Update", "Self-test"}[s]
}

// title is the name of the section as drawn.
func (s section) title() string {
	return tr([...]textID{tSecServices, tSecCredentials, tSecConnections, tSecDefaults, tSecVault, tSecApprovals,
		tSecTokens, tSecLogs, tSecOverview, tSecGeneral, tSecTransfer, tSecUpdate, tSecSelftest}[s])
}

// entry is what one entry of the section is called. Vault has no entries of its own; the name here is
// never shown, because the vault form and its leave question are worded on their own (see editorView and
// leaveView). Approvals never opens a "New ..." form either: its entries only ever come from Pending. Logs
// and Overview hold no list of named entries either.
func (s section) entry() string {
	name := [...]string{"service", "credential", "connection", "default", "vault setting", "approval", "agent token",
		"log entry", "overview", "preference", "transfer", "update", "selftest"}[s]
	return entryWord(name)
}

type screen int

const (
	// screenNav is the tab line, or the option bar of the area below it, in focus: the arrows choose the
	// active area or sub-tab, whose list the workspace shows beneath them.
	screenNav screen = iota
	// screenList is the list of the active section in focus.
	screenList
	screenForm
	screenConfirm
	screenSecret
	// screenVaultOffer is the passphrase offer that follows a vault's very first secret, over the form or
	// the guided setup summary that led to it.
	screenVaultOffer
	// screenAdminAuth is the masked admin passphrase dialog requireAdmin opens over whatever screen a
	// managing action was attempted from, gating it behind this window's admin session; see admin.go.
	screenAdminAuth
	// screenPicker is the searchable list of one choice row of the form, over the form it was opened from.
	screenPicker
	// screenSummary is the last step of the guided setup: what will be saved, and after saving, the test.
	screenSummary
	// screenProviders is the table of a provider row, over the form it was opened from.
	screenProviders
	// screenLeave asks what happens to the unsaved input of a form before it is left: save, discard, or stay.
	screenLeave
	// screenHelp shows the help topics over the tab line or the list it was opened from.
	screenHelp
	// screenUpdate asks before a newer release of qatlas is installed.
	screenUpdate
	// screenRestart asks before the editor restarts into an installed release and drops unsaved input.
	screenRestart
	// screenTargets edits the target list of a connection entry by entry.
	screenTargets
	// screenPaths edits the path list of a connection entry by entry, with tab completion for directories.
	screenPaths
	// screenVaultUnlock is the masked passphrase dialog 'ctrl+l' opens for a locked vault with no other
	// action pending: unlike requireAdmin's own "Unlock vault to continue", answering it does nothing but
	// the unlock itself, the vault process handoff on Linux, macOS, and Windows, and starting this window's admin session. See
	// vaultheader.go. The matching "Lock the vault now?" question reuses screenConfirm (vaultLockConfirm).
	screenVaultUnlock
	// screenLogs is the Logs section with the focus in its workspace, including its date and filter dialogs.
	// See logs.go.
	screenLogs
	// screenLogDetail shows every field of one log entry.
	screenLogDetail
	// screenTemplate asks whether a new entry or the guided setup starts empty or from an existing entry, and
	// lists the entries to start from in a searchable picker; see template.go.
	screenTemplate
	// screenRename is the new-name prompt and the dry run of renaming a list entry; see rename.go.
	screenRename
	// screenFindings lists the renames found in the vault and offers the action for each; see renamefind.go.
	screenFindings
	// screenTransfer is the guided export or import of the configuration; see transfer.go.
	screenTransfer
	// screenCommand is the command line over the screen it was opened from; see commandline.go.
	screenCommand
	// screenSelftest shows a selftest from its start to its result; see selftest.go.
	screenSelftest
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
	// fieldPaths is the path list of a connection, a list like fieldTargets: its directories are added,
	// edited, and removed one by one in a screen of their own, with tab completion for directories.
	fieldPaths
	// fieldVaultState is the read-only row of the vault form that names the vault's current state. It holds
	// no value of its own: renderField computes what it shows live, from the vault, every time it is drawn.
	fieldVaultState
	// fieldVaultAction is a row of the vault form that runs a vault action (turning encryption on or off, or
	// changing the passphrase) at once on enter, instead of saving with F2 like every other row. See
	// vaultsettings.go.
	fieldVaultAction
	// fieldPayloadNew is the row of a payload secret that takes the name of a field to add; enter turns the
	// name into a masked value row (see payload.go). It is typed like a text row.
	fieldPayloadNew
	// fieldKey holds the key of one global action in the General form; see keybinding.go.
	fieldKey
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
	// detail is the full text of a hint whose hint field holds a shortened form; empty when the hint is
	// already complete. The form always shows the full text.
	detail string
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
	// keep is the active_until text an active-for row's first choice stands for, so saving without a change
	// keeps that instant.
	keep string
	// entries and expanded belong to a target list: its targets in the order they are saved, and whether
	// the row shows every one of them.
	entries  []string
	expanded bool
	// labels belong to a target list: the label of a target by its value. They are part of what the row
	// holds, so a changed label counts as an unsaved change and retires the problems shown under the row.
	labels map[string]string
	// excluded belongs to a target list too: the entries that are exclusions, not targets. An entry not in it
	// is reachable. It is part of what the row holds like the labels are.
	excluded map[string]bool
	// action identifies which vault action a fieldVaultAction row runs on enter (see vaultsettings.go).
	action string
	// stored marks the masked row of a payload field that already has a value: left empty, it keeps it.
	stored bool
	// bound is the key a fieldKey row holds.
	bound string
	// section is the form section the row belongs to ("" in a form without sections); see sections.go.
	section string
}

// providerNoteLabel is the row of a service form that edits the note of the service's provider.
const providerNoteLabel = "provider note"

// Field hints. A hint is one short line under the focused row; the explanation behind it is the detail of
// the same name (see fieldDetail), which the pane and the info key show. Both name the shape of an entry,
// never a rule the core owns: the core states the exact character set when it refuses a value, and repeating
// it here would let the two drift apart.
//
// The env hint is said once for all role rows and speaks of all of them: they are the same kind of row, so
// repeating the sentence under the next one would read like a fault and add nothing. The provider hint of a
// credential names the one thing this choice decides, because it is not obvious from the field itself: the
// rows below it are the secret roles of the provider selected here. The provider hint of a connection says why
// this row exists at all: it is not stored, it decides what the rows below may offer. The description hints
// name the one consequence that sets these fields apart from everything else in the editor: what is typed
// there is published by discovery, so it is the one place where free text can carry a secret out of this
// machine; the note of a provider says the same, and that one note serves every service of that provider. The
// undescribed hint says once under the list what the marker at the end of an undescribed connection means;
// neither blocks anything, the description stays optional. The locked hint is what the name of an existing
// entry says about itself; it replaces the hint that describes a free choice, which is the opposite of what
// that field does. The access hint says what each access mode means, including the two consequences that are
// easy to miss: a tool added by a later version joins only the first mode, and an empty selection closes the
// route.

// The profile row of a connection. A profile is a provider's named starting selection: choosing one ticks
// the tools it expands to as selected tools, and nothing else. The row shows the profile the ticks match, or
// custom, so it holds no state of its own and nothing of it is saved.
const (
	profileLabel  = "profile"
	profileCustom = "custom"
)

// The state row of a connection form: a disabled connection keeps its selection but serves nothing.
const (
	stateLabel    = "state"
	stateActive   = "active"
	stateDisabled = "disabled"
)

// The access modes of a connection. The first one is a connection that offers by effect, the second one a
// connection that offers its tools list, which may also be empty. Only the row of the mode in use is shown,
// and only its field is saved.
const (
	accessLabel   = "access"
	effectsLabel  = "effects"
	toolListLabel = "tools"
	accessEffects = "by effect"
	accessTools   = "selected tools"
)

// providerLabel is the field that names the system an entry belongs to. A service always has one; a
// credential may, and then it decides which secret roles exist.
const providerLabel = "provider"

// The defaults bridge the first frame until the terminal reports its real size, which it does before a
// person can press a key; they are generous so that frame is not squeezed.
const (
	defaultWidth  = 120
	defaultHeight = 100
	minimumWidth  = 40
	minimumHeight = 14
	// narrowWidth is the width below which the header and long headings take a second line.
	narrowWidth = 80
	// frameCells is what the frame of the workspace takes from each dimension: a border line on both
	// sides, and a blank column inside each vertical border.
	frameCells = 4
)

func (f field) withHint(hint string) field {
	f.hint = hint
	return f
}

// withDetail sets the explanation behind the one-line hint: the text the pane and the info key show.
func (f field) withDetail(detail string) field {
	f.detail = detail
	return f
}

func (f field) value() string {
	if f.kind == fieldKey {
		return f.bound
	}
	if f.kind == fieldVaultState || f.kind == fieldVaultAction {
		// Neither kind holds anything to save: the state is read live from the vault when it is drawn, and
		// an action runs at once on enter rather than being saved with the rest of the form.
		return ""
	}
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
	if f.kind == fieldTargets || f.kind == fieldPaths {
		// One entry per line, so the value tells every list apart, entries with commas included.
		value := strings.Join(f.entries, "\n")
		for _, key := range slices.Sorted(maps.Keys(f.labels)) {
			value += "\x00" + key + "\x00" + f.labels[key]
		}
		for _, key := range slices.Sorted(maps.Keys(f.excluded)) {
			value += "\x01" + key
		}
		return value
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
	// svc performs every configuration read and save, secret commit, vault process handover and connection
	// log entry of the editor (see internal/manage).
	svc *manage.Service
	// store is the editor's own handle on the configuration file, meant only for the vault form's migrate
	// action and for approving connections when the vault gets encrypted; both reach the store beyond what
	// the management service offers. Nothing else reads or writes through it.
	store *config.Store
	cfg   *config.Config
	// problems are the validation problems of the last refused save or deletion (see problems.go).
	problems problemState

	screen screen
	// section is the active section: the one the tab line and option bar mark and whose entries, with their
	// filter, selection and scroll position, list holds. Its area is derived, never stored.
	section section
	// overviewCursor is the selected line of the Overview, counting its tiles first and then its next steps.
	overviewCursor int
	// navSub is set while screenNav stands on the option bar of the area instead of on the tab line.
	navSub bool
	list   filterList
	// groups is the provider grouping of list while its section is grouped, nil otherwise; folds keeps
	// the fold state of each grouped section (see listgroups.go).
	groups *listGroups
	folds  map[section]map[string]bool
	// listOpts holds the filter bar of each grouped section; barFocus is the bar field that has the focus
	// (1-based), 0 while the rows do (see listbar.go).
	listOpts map[section]*listOptions
	barFocus int
	// noticeFocus is set while the hint line about the renames found has the focus, the stop below the last
	// row; noticeAt and noticeRows are where the last frame drew that line in the foot (see renamefind.go).
	noticeFocus          bool
	noticeAt, noticeRows int
	// detailFocus is set while the detail pane of a list has the focus, detailTab is the tab it shows then.
	detailFocus bool
	detailTab   detailTab
	// detailPane scrolls the text of the detail pane that is longer than the pane, infoPane the one of the
	// explanation pane beside a form; info scrolls the explanation overlay.
	detailPane paneScroll
	infoPane   paneScroll
	info       textScroll
	// dd is the open list of a dropdown field of a bar (see dropdown.go).
	dd *dropdown

	editing string
	// copyFrom names the entry a new form was filled from. The form is still a new entry (editing stays
	// empty); this only says where its values came from. See template.go.
	copyFrom string
	// template is the question or picker of screenTemplate while it is open.
	template *templateChoice
	// cmdline is the command line of screenCommand while it is open.
	cmdline *commandLine
	// selftest is how selftests run; st is the one on screen, selftestID numbers the runs. See selftest.go.
	selftest   SelfTest
	st         *selftestState
	selftestID int
	// findings is the list of screenFindings while it is open.
	findings *findingsState
	// rename is the prompt or dry run of screenRename while it is open.
	rename *renameState
	// export and imp are the guided export or import of screenTransfer while one is open; never both.
	export *exportState
	imp    *importState
	fields []field
	focus  int
	// picker holds the values of the focused choice row while it is searched. The row itself changes only
	// when a value is taken.
	picker filterList
	// templateList holds the entries of screenTemplate's picker.
	templateList filterList
	// pickerMarks holds the ticks of a tool list or the effects while their picker is open, and is nil
	// for a single choice. The row takes them over only when they are kept.
	pickerMarks map[string]bool
	// pickerGroups is set while the picker of a tool list of a grouped provider is open; nil otherwise.
	pickerGroups *toolGroups
	// pickerEffects is set while the picker of the tool list is open; it holds the effect rows above the rest.
	pickerEffects *toolEffects
	// providers is the table of the focused provider row while it is open.
	providers providerTable
	// targetList holds the entries of the focused target list while its screen is open; the row takes them
	// over only when they are kept. targetEdit is the entry typed into targetInput: its index, the length of
	// the list for a new one, or -1 while nothing is typed. targetRemove asks before the selected entry goes.
	targetList  filterList
	targetInput textinput.Model
	// targetLabels are the labels of the open target screen, labelInput the label being typed for the
	// selected target while labelEdit is set. They reach the row only when the list is kept.
	targetLabels map[string]string
	// targetExcluded are the entries of the open target screen that are exclusions. targetAsExclusion says that
	// the entries added next are taken as exclusions.
	targetExcluded    map[string]bool
	targetAsExclusion bool
	labelInput        textinput.Model
	labelEdit         bool
	targetEdit        int
	targetRemove      bool
	// targetAdd is the menu that adds a target, with its builder, while it is open.
	targetAdd *targetAdd
	// pathList, pathInput, pathEdit and pathRemove do for the path list of a connection what the target
	// fields above do for its targets (see paths.go).
	pathList   filterList
	pathInput  pathField
	pathEdit   int
	pathRemove bool
	// now is the clock the active-for row and the connection list read; nil means time.Now.
	now func() time.Time
	// wheelSeen is the wheel event last acted on; see wheelMerge.
	wheelSeen wheelSeen
	// startDir is the working directory the TUI was started in, the first suggestion for a new path.
	startDir string
	// confirmRole names the role whose stored secret the confirmation removes. Empty means the
	// confirmation is about the selected entry of the list.
	confirmRole string
	// pendingProfile names the profile the confirmation would apply to the access and tool ticks of the
	// form. Empty means the confirmation is about something else.
	pendingProfile string
	// approvalDetail names the connection the Approvals detail screen shows, "" otherwise. approveAllConfirm
	// is the Approvals list's own bulk confirmation, asked before 'a' approves every connection currently
	// open. Both stand over the Approvals list, not a form; see approvals.go.
	approvalDetail    string
	approveAllConfirm bool
	// approvalExpanded spells out the unchanged entries of a list change on the detail screen, which
	// approval scrolls; both start over each time the screen opens.
	approvalExpanded bool
	approval         textScroll
	approvalPage     int
	// approvalOrigin is where the change of the connection approvalOriginFor came from, once the log was
	// read; approvalOriginReady is false until then (see approvalOriginMsg).
	approvalOrigin      approval.Origin
	approvalOriginFor   string
	approvalOriginReady bool
	// tokenDetail names the agent token the Tokens detail screen shows, "" otherwise; tokenReveal says that
	// s asked to show its value there (see tokenValueShown). tokenRevoke names the token the revoke question
	// is about, asked from the list or over the detail screen. All stand over the Tokens list; see tokens.go.
	tokenDetail string
	tokenReveal bool
	tokenRevoke string
	// pristine is what the open form held when it was opened, so leaving it can tell whether anything
	// would be lost. leaveFrom is the screen the leave question returns to when the user stays; leaveQuit
	// says that the question was raised by ctrl+c, so discarding ends the program instead of leaving.
	pristine  string
	leaveFrom screen
	leaveQuit bool

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
	// rev is the revision of the configuration file cfg was loaded from or last written as (see
	// revision.go). Every write is committed against it, so a change another writer made meanwhile is
	// reported as a conflict and never overwritten.
	rev config.Revision

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
	// vaultOffer holds the passphrase offer that follows a vault's very first secret while it is open, and
	// nil otherwise. vaultBusy is true while a vault write, or the offer that may lead to one, is in flight,
	// so a second one cannot start before it is done.
	vaultOffer *vaultOffer
	vaultBusy  bool
	// adminAuth holds the masked admin passphrase dialog while requireAdmin has one open, and nil otherwise.
	// admin is this window's own admin session: inactive until started, and, with vault.admin_timeout: 0,
	// never kept at all. See admin.go.
	adminAuth *adminAuth
	admin     manage.AdminSession
	// decryptConfirm is the explicit y/n question before the vault's encryption is switched off, holding the
	// already-verified current passphrase to run the switch with once it is answered y. It is cleared, and
	// the passphrase dropped, the moment the question is answered either way.
	decryptConfirm    bool
	decryptPassphrase string
	// handoverShown is the update behaviour the vault form's choice row opened with, "" while that row is
	// not editable. Only a row moved off it is saved (see saveVault).
	handoverShown string
	// migrate holds a 'migrate credentials.yaml' action's decided plan while any of its several screens are
	// open, and nil otherwise; see internal/tui/migrate.go. migratePlanConfirm and migrateDeleteConfirm are
	// its two y/n questions, asked apart: the first before anything is written, the second, once
	// migrateResult names the outcome, about deleting the file.
	migrate              *migratePlan
	migratePlanConfirm   bool
	migrateDeleteConfirm bool
	migrateResult        string

	// Connection test state. Raw responses never enter the model, only the class and a redacted message.
	tester     Tester
	redactor   *redact.Redactor
	testing    bool
	testName   string
	testClass  provider.Class
	testID     int
	cancelTest context.CancelFunc

	// targetPick is the picker of the targets a provider lists while it is open over the add menu; pickSeq
	// numbers its reads. See targetpick.go.
	targetPick *targetPick
	pickSeq    int

	// wizard is the guided setup while it runs, and nil otherwise.
	wizard *setup

	// window, while a form is being fitted to the terminal, is the first and last row index drawn; nil
	// draws every row (see fittedEditorView).
	window *[2]int
	// formCol is the width of the label column of the form being drawn (see labelColumn); 0 outside a draw.
	formCol int
	// formFlat draws the rows of a form as one-line fields without a frame, for a form that is too tall to
	// stand in frames. formBox says the rows being drawn stand in frames; formValueCol and formValueW are where
	// their value column starts and how wide it is.
	formFlat, formBox        bool
	formValueCol, formValueW int
	// sectionPinned holds the sections the user jumped into, which stay open while the form does even when
	// nothing in them has a value (see sectionClosed).
	sectionPinned map[string]bool
	// infoOpen is set while the explanation of infoShown stands as an overlay over the screen.
	infoOpen bool
	// inPane is set while the left part of a screen with a pane is drawn, whose width is already reduced.
	inPane    bool
	infoShown infoSubject
	// helpTopic is the topic the help screen shows, scrolled by help; helpFrom is the screen it
	// returns to.
	helpTopic int
	help      textScroll
	helpFrom  screen

	// Self-update state. release is a newer stable release the check at start found; updated names the
	// version installed since, which takes effect only after a restart. updateFrom is the screen the update
	// question returns to.
	updater    Updater
	version    string
	release    selfupdate.Result
	updating   bool
	updated    string
	updateFrom screen
	// updatePercent is how much of the release archive has arrived during an installation, -1 until known.
	updatePercent int
	// updateUnlocked is whether the vault was unlocked when the installation started; the restarted editor
	// is told. restart replaces the process after an installation, nil where this platform cannot, and
	// execute runs the command that does so with the terminal released. restartPath, restartFrom and
	// restartTo are what a pending restart runs and tells, restartBack the screen the question returns to,
	// and restartNote what a restarted editor says in the unlock dialog it opens.
	updateUnlocked bool
	restart        *Restart
	execute        func(tea.ExecCommand, tea.ExecCallback) tea.Cmd
	restartPath    string
	restartFrom    string
	restartTo      string
	restartBack    screen
	restartNote    string
	// backdrop is set while a screen is drawn as the background of a dialog: the dialog shows what the
	// screen would show for its own input.
	backdrop bool
	// The click targets of the frame drawn last; see mouse.go.
	hits      []hit
	frameHits []hit
	// cursor is where the focused text field of the frame drawn last puts the terminal cursor; nil hides it.
	cursor *tea.Cursor
	// drawn is set when the targets match the state: render sets it, any other message clears it. During a
	// mouse event, verified says that frame also fits the screen, so the first key of the event needs no check.
	drawn, verified bool
	// tooSmall is set while the workspace is replaced by the size notice, which has nothing to click.
	tooSmall bool
	// footerLog, when set, collects the footer lines a dialog draws, to make its keys clickable.
	footerLog *[]footerLine
	// inModal is set while a dialog is drawn as a modal: its text then carries its buttons.
	inModal bool
	// inPanel is set while a question is drawn for the inside of a frame (see framedText).
	inPanel bool
	// screenKeys is the key text the screen drawn last hands to the footer of the frame.
	screenKeys string
	// screenPos is the position in the list of the screen drawn last, for the right end of the footer.
	screenPos string
	// footerSpans are the cells of the key groups in the footer drawn last.
	footerSpans []footerSpan
	// formTop and formLeft are where the rows of the form being drawn start; sectionListWidth is the width of
	// the list of its sections, 0 where there is none.
	formTop, formLeft, sectionListWidth int
	// overviewHits are the cells of the Overview that select each of its items, in the order of the items.
	overviewHits []overviewHit
	// logBarSpecs are the fields of the bar of the Logs screen as the last frame drew them.
	logBarSpecs []barSpec
	// updateText is what the update question says an installation does, set when it opens.
	updateText string

	// Vault header and 'ctrl+l' state (see vaultheader.go). vaultProcessUnlocked caches whether a vault
	// process holds a locked vault open, refreshed by a tick and right after this window's own lock and
	// unlock actions; it is read only while the vault is locked in this process, since an unlocked one
	// already answers the header synchronously from vault.State() alone. vaultUnlock holds the masked
	// 'ctrl+l' unlock dialog while it is open, back is the screen it returns to either way once it is done.
	// vaultLockConfirm and vaultLockBack are the same for the short 'Lock the vault now?' question.
	vaultProcessUnlocked bool
	vaultUnlock          *vaultUnlockPrompt
	vaultLockConfirm     bool
	vaultLockBack        screen

	// logs is the state of the Logs section; see logs.go.
	logs *logView
	// overviewAct is the latest activity the Overview shows, read in the background.
	overviewAct overviewActivity

	// payloadSaving is true while the commit of a payload secret is in flight; its form takes no input then.
	payloadSaving bool
	// payloadRemoveAsk is true while the question before removing the focused payload field is open.
	payloadRemoveAsk bool

	quitting bool

	// prefs are the device-local preferences and savePrefs stores them; the latter is nil when they cannot be
	// saved.
	prefs uiprefs.Prefs
	// keyCapture is set while a key row of the General form waits for the key to bind; see keybinding.go.
	keyCapture bool
	savePrefs  func(uiprefs.Prefs) error
	// start is the area the editor opens on; the Overview is where it already is.
	start area
}

// New builds the editor over the management service svc. store is only the handle the vault form's migrate
// and encrypt actions need (see Model.store). A missing configuration file starts an empty one. The
// tester may be nil, in which case connection testing is unavailable; secrets may be nil, in which case the
// configuration stays editable and only the operations that would reach a store report why they cannot.
func New(svc *manage.Service, store *config.Store, tester Tester, secrets Secrets, redactor *redact.Redactor) (*Model, error) {
	cfg, rev, err := svc.Load()
	configExists := true
	if err != nil {
		var notFound *config.NotFoundError
		if !asNotFound(err, &notFound) {
			return nil, err
		}
		cfg = svc.NewConfig()
		rev = config.RevisionAbsent
		configExists = false
	}
	if redactor == nil {
		redactor = &redact.Redactor{}
	}
	if secrets == nil {
		secrets = noSecrets{}
		svc = svc.WithSecrets(secrets)
	}
	// Without a working directory a new path simply starts without a suggestion.
	startDir, _ := os.Getwd()
	m := &Model{
		svc: svc, store: store, cfg: cfg, rev: rev, tester: tester, redactor: redactor,
		secrets:   secrets,
		sources:   map[string]secret.Source{},
		checked:   map[string][]string{},
		termWidth: defaultWidth, termHeight: defaultHeight, configExists: configExists,
		startDir: startDir, execute: tea.Exec,
	}
	m.layoutWorkspace()
	m.list = newFilterList(m.describe)
	m.picker = newFilterList(m.choiceShown)
	m.templateList = newFilterList(m.templateText)
	m.templateList.input.Placeholder = "Type to search"
	m.picker.input.Placeholder = "Type to search"
	m.targetList = newFilterList(func(target string) string { return target + " " + m.targetLabels[target] })
	m.targetInput = textField("", "", false).input
	m.labelInput = textField("", "", false).input
	m.pathList = newFilterList(func(path string) string { return path })
	m.pathInput = newPathField(pathDirectory)
	m.logs = newLogView(time.Now())
	// The editor opens on the tab line, with the Overview already shown below it.
	m.section = sectionOverview
	m.list.reset(m.listItems(m.section))
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
// never enter the model. It also starts the check for a newer release, which never blocks the editor, and
// asks the terminal for its colours where the theme mixes from them.
func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.refreshSources(m.keyringQueries()), m.checkUpdate(), m.checkVaultProcess(), vaultTick(),
		m.loadOverviewActivity(), terminalColourQuery(m.prefs.Theme)}
	if m.start != areaOverview {
		// The chosen area opens like a tab pick: on its first part, with the focus on the tab line.
		notice := m.status
		cmds = append(cmds, m.previewArea(m.start))
		m.status = notice
		m.start = areaOverview
	}
	return tea.Batch(cmds...)
}

// Update handles one event. It is the whole editor logic and needs no terminal.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	fits := m.drawn && m.verified
	if _, mouse := msg.(tea.MouseMsg); !mouse {
		m.drawn = false
	}
	defer m.keepScrollPosition()
	// Deferred last, so it runs first: the vault or admin state, and with it the header, may have changed.
	defer m.layoutWorkspace()
	switch msg := msg.(type) {
	case keysMsg:
		var cmds []tea.Cmd
		for _, key := range msg {
			_, cmd := m.Update(key)
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)
	case testDoneMsg:
		m.finishTest(msg)
		return m, nil
	case pickLoadedMsg:
		return m, m.pickLoaded(msg)
	case pickSearchMsg:
		return m, m.pickSearch(msg)
	case sourcesMsg:
		return m, m.handleSources(msg)
	case placedMsg:
		return m, m.handlePlaced(msg)
	case writtenMsg:
		return m, m.handleWritten(msg)
	case vaultActionMsg:
		return m, m.handleVaultAction(msg)
	case vaultPassphraseVerifiedMsg:
		return m, m.handleVaultPassphraseVerified(msg)
	case adminVerifiedMsg:
		return m, m.handleAdminVerified(msg)
	case exportDoneMsg:
		return m, m.handleExportDone(msg)
	case importPlannedMsg:
		return m, m.handleImportPlanned(msg)
	case importDoneMsg:
		return m, m.handleImportDone(msg)
	case importApprovedMsg:
		return m, m.handleImportApproved(msg)
	case planVaultMigrateMsg:
		return m, m.handlePlannedVaultMigrate(msg)
	case vaultMigrateWrittenMsg:
		return m, m.handleVaultMigrateWritten(msg)
	case setupSavedMsg:
		return m, m.setupSaved(msg)
	case payloadSavedMsg:
		return m, m.handlePayloadSaved(msg)
	case approvalSweepMsg:
		return m, m.handleApprovalSweep(msg)
	case approvalActionMsg:
		return m, m.handleApprovalAction(msg)
	case approvalOriginMsg:
		m.handleApprovalOrigin(msg)
		return m, nil
	case tokenActionMsg:
		return m, m.handleTokenAction(msg)
	case vaultUnlockedMsg:
		return m, tea.Batch(m.handleVaultUnlocked(msg), m.reloadShownLogs(), m.loadOverviewActivity())
	case vaultProcessStartedMsg:
		return m, m.handleVaultProcessStarted(msg)
	case vaultLockedMsg:
		return m, tea.Batch(m.handleVaultLocked(msg), m.reloadShownLogs(), m.loadOverviewActivity())
	case logsLoadedMsg:
		return m, m.handleLogsLoaded(msg)
	case overviewActivityMsg:
		m.handleOverviewActivity(msg)
		return m, nil
	case vaultProcessCheckMsg:
		m.vaultProcessUnlocked = msg.unlocked
		return m, nil
	case vaultTickMsg:
		return m, tea.Batch(m.checkVaultProcess(), vaultTick())
	case updateCheckedMsg:
		m.updateChecked(msg)
		return m, nil
	case updateProgressMsg:
		m.updatePercent = msg.percent
		return m, msg.next
	case updateDoneMsg:
		return m, m.updateDone(msg)
	case selftestMsg:
		return m, m.handleSelftest(msg)
	case restartFailedMsg:
		m.restartFailed(msg)
		return m, nil
	case tea.ForegroundColorMsg, tea.BackgroundColorMsg:
		m.takeTerminalColour(msg)
		return m, nil
	case tea.PasteMsg:
		_, cmd := m.Update(pasteKey(msg.Content))
		return m, cmd
	case tea.MouseMsg:
		return m, m.updateMouse(msg)
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
	case tea.KeyPressMsg:
		if m.infoOpen {
			if keyIs(msg, actForceQuit) {
				return m, m.interrupt()
			}
			if keyIs(msg, actCopy) {
				return m, m.copyCurrent()
			}
			m.updateInfo(msg)
			return m, nil
		}
		if !fits && m.activeScreenTooSmall() {
			switch {
			case keyIs(msg, actForceQuit):
				return m, m.interrupt()
			case keyIs(msg, actQuit):
				// q is a letter in a form, so it quits only where nothing typed can be lost.
				if m.screen == screenNav || m.screen == screenList || m.screen == screenLogs {
					return m, m.quit()
				}
			case keyIs(msg, actBack):
				// Leaving is what makes this a notice instead of a trap: a screen too large for the
				// terminal must not be the only way out of the editor.
				return m, m.leaveScreen()
			}
			return m, nil
		}
		// Every key is activity for this window's admin session, whichever screen it lands on; the dialog
		// that starts one runs its own check on "enter" instead, never through this.
		m.touchAdminSessionIfActive()
		// 'ctrl+l' is global, ahead of every screen's own keys (see vaultheader.go): it never reaches a
		// screen that already asks its own question, and is a no-op while vaultBusy, so it can never step on
		// a passphrase dialog, a confirmation, or vault work already running.
		if m.keyCapture {
			if !keyIs(msg, actForceQuit) {
				return m, m.captureKey(msg)
			}
			// ctrl+c ends the editor everywhere, so it ends the capture first and then acts as usual.
			m.keyCapture = false
		}
		if keyIs(msg, actLock) && m.vaultLockKeyAllowed() {
			return m, m.handleVaultLockKey()
		}
		if keyIs(msg, actForceQuit) {
			return m, m.interrupt()
		}
		if m.dd != nil {
			return m, m.updateDropdown(msg)
		}
		if m.testResultShown() {
			m.testResultKey(msg)
			return m, nil
		}
		if keyIs(msg, actHelp) && m.helpReachable() {
			m.openHelp()
			return m, nil
		}
		if keyIs(msg, actCommand) && m.helpReachable() {
			m.openCommandLine()
			return m, nil
		}
		if m.infoPressed(msg) && m.openInfo() {
			return m, nil
		}
		// Like the other letters, c is text wherever text is typed.
		if keyIs(msg, actCopy) && m.helpReachable() {
			if cmd := m.copyCurrent(); cmd != nil {
				return m, cmd
			}
		}
		msg = m.moveKey(msg)
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
		case screenSecret:
			cmd = m.updateSecret(msg)
		case screenVaultOffer:
			cmd = m.updateVaultOffer(msg)
		case screenAdminAuth:
			cmd = m.updateAdminAuth(msg)
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
		case screenRestart:
			cmd = m.updateRestartConfirm(msg)
		case screenTargets:
			cmd = m.updateTargets(msg)
		case screenPaths:
			cmd = m.updatePaths(msg)
		case screenVaultUnlock:
			cmd = m.updateVaultUnlockPrompt(msg)
		case screenLogs:
			cmd = m.updateLogs(msg)
		case screenLogDetail:
			cmd = m.updateLogDetail(msg)
		case screenTemplate:
			cmd = m.updateTemplate(msg)
		case screenRename:
			cmd = m.updateRename(msg)
		case screenFindings:
			cmd = m.updateFindings(msg)
		case screenCommand:
			cmd = m.updateCommandLine(msg)
		case screenSelftest:
			cmd = m.updateSelftest(msg)
		case screenTransfer:
			cmd = m.updateTransfer(msg)
		}
		// Whatever a key changed, the profile row shows what the ticks now are.
		if m.screen == screenForm {
			m.syncProfile()
		}
		return m, cmd
	}
	return m, nil
}

// updateNav handles the tab line and the option bar in focus: left/right step through the areas, or on the
// option bar through the sub-tabs, with wraparound. Down from the tab line moves to the option bar when the
// area has one; down from the option bar, enter, or tab move the focus into the workspace, and up on the
// option bar returns to the tab line. home/end and pgup/pgdown jump to the first or last area. Moving
// changes the active section at once, so the workspace below always shows what the marker stands on.
func (m *Model) updateNav(key tea.KeyPressMsg) tea.Cmd {
	if s, ok := sectionShortcut(key.String()); ok {
		return m.openSection(s)
	}
	if m.setupUpdateKey(key) {
		return nil
	}
	switch {
	case keyIs(key, actQuit):
		return m.quit()
	case keyIs(key, actPageUp) || keyIs(key, actHome):
		return m.previewArea(0)
	case keyIs(key, actPageDown) || keyIs(key, actEnd):
		return m.previewArea(areaCount - 1)
	case keyIs(key, actLeft) || keyIs(key, actRight):
		step := 1
		if keyIs(key, actLeft) {
			step = -1
		}
		if m.navSub && m.hasSubTabs() {
			tabs := areaSections[m.section.area()]
			return m.previewSection(tabs[wrap(m.section.index()+step, len(tabs))])
		}
		return m.previewArea(area(wrap(int(m.section.area())+step, int(areaCount))))
	case keyIs(key, actUp):
		m.navSub = false
	case keyIs(key, actDown):
		if !m.navSub && m.hasSubTabs() {
			m.navSub = true
			return nil
		}
		return m.focusList()
	case keyIs(key, actEnter) || keyIs(key, actNext) || keyIs(key, actPrev):
		// The bars and the workspace are the stops, so next and previous lead to the same one.
		return m.focusList()
	case keyIs(key, actNew):
		if m.section != sectionOverview {
			return m.newEntry()
		}
	}
	return nil
}

// newEntry is n from the tab line or a list: the form of a new entry of the active section, unless a missing
// prerequisite blocks it. In Credentials it first asks for a credential or a payload secret. A new agent
// token is a managing action from the start (see newToken). In Connections n is the guided setup, the same
// flow as g, because it creates a missing service or credential on the way instead of sending the person to
// the Library.
func (m *Model) newEntry() tea.Cmd {
	if m.section == sectionConnections {
		m.askSetupTemplate()
		return nil
	}
	if reason := m.newEntryBlocked(); reason != "" {
		m.status = ""
		m.fail = reason
		return nil
	}
	if m.section == sectionCredentials {
		m.askKind()
		return nil
	}
	if m.templateOffered() {
		m.askTemplate(false)
		return nil
	}
	return m.newEmptyEntry()
}

// newEmptyEntry opens the form of a new entry that starts empty.
func (m *Model) newEmptyEntry() tea.Cmd {
	if m.section == sectionTokens {
		return m.newToken()
	}
	return m.openForm("")
}

// focusList hands the focus to the active section's list, or, for Vault, which holds no entries of its
// own, opens its form the same way enter does from the list, or, for Logs, to its own screen. The Overview
// focuses its lines.
func (m *Model) focusList() tea.Cmd {
	if m.section == sectionOverview {
		m.screen = screenList
		m.clearMessages()
		return nil
	}
	if m.section.isPage() {
		return m.enterPage()
	}
	if m.section == sectionLogs {
		m.screen = screenLogs
		m.clearMessages()
		if !m.logs.loaded && !m.logs.loading {
			return m.loadLogs()
		}
		return nil
	}
	m.screen = screenList
	m.barFocus = 0
	m.clearMessages()
	return nil
}

// previewSection makes s the active section while the tab line keeps the focus.
func (m *Model) previewSection(s section) tea.Cmd {
	cmd := m.openSection(s)
	m.screen = screenNav
	return cmd
}

func (m *Model) updateList(key tea.KeyPressMsg) tea.Cmd {
	if m.section == sectionOverview {
		return m.updateOverview(key)
	}
	if m.list.editing {
		return m.updateFilter(key)
	}
	if m.barFocus > 0 {
		if cmd, done := m.updateBar(key); done {
			return cmd
		}
	}
	if m.detailFocused() {
		if cmd, done := m.updateDetail(key); done {
			return cmd
		}
	}
	if s, ok := sectionShortcut(key.String()); ok {
		return m.openSection(s)
	}
	if m.setupUpdateKey(key) {
		return nil
	}
	if m.noticeFocused() {
		if cmd, done := m.updateNotice(key); done {
			return cmd
		}
	}
	switch {
	case keyIs(key, actBack):
		if m.testing {
			// Cancelling the test returns to the editor with the configuration untouched.
			m.stopTest()
			m.status = tr(tSetupConnectionTestCancelled)
			return nil
		}
		if m.list.query() != "" {
			// A filter is left before the list is: the first escape shows every entry again.
			m.list.clearFilter()
			return nil
		}
		m.focusNav()
	case keyIs(key, actNext):
		// The stops run rows, the detail pane, the fields of the filter bar, the tab line.
		if m.paneWidth() > 0 {
			m.detailFocus, m.detailTab = true, tabOverview
		} else if m.listBarShown() {
			m.barFocus = 1
		} else {
			m.focusNav()
		}
	case keyIs(key, actPrev):
		m.focusNav()
	case keyIs(key, actQuit):
		return m.quit()
	case keyIs(key, actSearch):
		m.list.startFilter()
	case keyIs(key, actUp):
		// No wraparound: the first entry is where the list ends and the filter bar or, without one, the tab
		// line begins, in an empty list just as much as a full one.
		if m.list.cursor <= 0 {
			if m.listBarShown() {
				m.barFocus = 1
			} else {
				m.focusNav()
			}
		} else {
			m.list.jump(-1)
		}
	case keyIs(key, actDown):
		if m.list.cursor >= len(m.list.matches)-1 && len(m.sectionFindings()) > 0 {
			// The stop below the last row is the hint line about the renames found.
			m.noticeFocus = true
		} else {
			m.list.jump(1)
		}
	case keyIs(key, actPageUp) || keyIs(key, actPageDown) || keyIs(key, actHome) || keyIs(key, actEnd):
		m.jumpList(key)
	case keyIs(key, actLeft) || keyIs(key, actRight):
		m.foldKey(key)
	case keyIs(key, actNew):
		return m.newEntry()
	case keyIs(key, actDuplicate):
		return m.duplicateSelected()
	case keyIs(key, actRename):
		return m.askRename()
	case keyIs(key, actEdit):
		return m.editSelected()
	case keyIs(key, actEnter):
		if (m.section == sectionApprovals || m.section == sectionTokens) && m.vaultLocked() {
			// Nothing is listed while the vault is locked (see approvalsReport); enter is a managing action
			// like any other here, gated behind requireAdmin, whose "Unlock vault to continue" dialog
			// unlocks it and starts the admin session together. The action itself writes nothing: it only
			// rebuilds the list, now that Pending can read it.
			return m.requireAdmin(func() tea.Cmd { return m.returnToList("") })
		}
		if m.foldKey(key) {
			return nil
		}
		name, ok := m.selected()
		if !ok {
			return nil
		}
		if m.section == sectionApprovals {
			m.approvalDetail, m.approvalExpanded = name, false
			m.approval.reset()
			m.screen = screenConfirm
			m.clearMessages()
			return m.loadApprovalOrigin(name)
		}
		if m.section == sectionTokens {
			// The detail screen shows the value masked; v there reveals it, in an admin session only.
			m.tokenDetail, m.tokenReveal = name, false
			m.screen = screenConfirm
			m.clearMessages()
			return nil
		}
		return m.openForm(name)
	case keyIs(key, actDelete):
		if m.section == sectionApprovals {
			return nil
		}
		if name, ok := m.selected(); ok {
			if m.section == sectionTokens {
				// A token is revoked, never deleted like a configuration entry.
				m.tokenRevoke = name
			}
			m.screen = screenConfirm
			m.clearMessages()
		}
	case keyIs(key, actTest):
		return m.startTest()
	case keyIs(key, actApproveAll):
		if m.section != sectionApprovals {
			return nil
		}
		// A bulk approval is offered whenever it would do something: approve what is open, remove a stale
		// approval left over from a deleted or renamed connection, or both, the same way 'qatlas vault
		// approve' with no --connection does.
		if report, unavailable := m.approvalsReport(); unavailable == "" &&
			(len(report.Open) > 0 || len(report.RevocableStale()) > 0) {
			m.approveAllConfirm = true
			m.screen = screenConfirm
			m.clearMessages()
		}
	}
	return nil
}

// editSelected is e on a list: the form of the selected entry, as enter opens it. A group row, an approval,
// and an agent token have no form, so e leaves them alone (enter folds, reviews, or shows them).
func (m *Model) editSelected() tea.Cmd {
	if m.section == sectionApprovals || m.section == sectionTokens {
		return nil
	}
	name, ok := m.selected()
	if !ok || isGroupRow(name) {
		return nil
	}
	return m.openForm(name)
}

// updateFilter is the list while its filter is typed. Every printable key belongs to the filter, including
// the letters and digits that are list keys otherwise; the arrows still move through what it matches.
func (m *Model) updateFilter(key tea.KeyPressMsg) tea.Cmd {
	switch {
	case keyIs(key, actBack):
		m.list.clearFilter()
	case keyIs(key, actEnter):
		m.list.stopFilter()
	case keyIs(key, actUp):
		m.list.move(-1)
	case keyIs(key, actDown):
		m.list.move(1)
	case keyIs(key, actPageUp) || keyIs(key, actPageDown) || keyIs(key, actHome) || keyIs(key, actEnd):
		m.jumpList(key)
	default:
		return m.list.updateFilter(key)
	}
	return nil
}

// jumpList moves by one screen of entries or to either end of the list.
func (m *Model) jumpList(key tea.KeyPressMsg) {
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
	case sectionDefaults:
		if len(m.cfg.Connections) == 0 {
			return tr(tDefaultsNeedConnection)
		}
	case sectionVault, sectionGeneral, sectionTransfer, sectionUpdate, sectionSelftest:
		return tr(tSettingsNotList, m.section.title())
	case sectionApprovals:
		return tr(tApprovalsNothingToAdd)
	case sectionTokens:
		return m.tokensBlocked()
	case sectionLogs:
		return "Logs shows what was invoked; there is nothing to add by hand."
	}
	return ""
}

// leaveScreen steps back one level without saving anything. It is what the resize notice offers besides
// quitting, so a screen that does not fit is never the end of the session; a form with unsaved input asks
// first, like every other way out of it.
func (m *Model) leaveScreen() tea.Cmd {
	// A capture belongs to the form it started in; the resize notice can leave that form mid-capture.
	m.keyCapture = false
	switch m.screen {
	case screenNav:
		return nil
	case screenList:
		m.focusNav()
		return nil
	case screenLogs:
		// A dialog or a typed search is left before the screen is, the way esc leaves them.
		switch lv := m.logs; {
		case lv.dialog != nil:
			lv.dialog = nil
		case lv.pick != nil:
			lv.pick = nil
		case lv.list.editing:
			lv.list.clearFilter()
		default:
			m.focusNav()
		}
		return nil
	case screenLogDetail:
		m.screen = screenLogs
		return nil
	case screenTemplate:
		m.template = nil
		m.screen = m.templateFromScreen()
		return nil
	case screenRename:
		m.cancelRename()
		return nil
	case screenFindings:
		m.closeFindings()
		return nil
	case screenTransfer:
		if m.imp != nil {
			if m.imp.working == "" {
				return m.importBack()
			}
			return nil
		}
		if m.export != nil && !m.export.writing {
			return m.exportBack()
		}
		return nil
	case screenLeave:
		// Staying is the answer that loses nothing.
		m.screen, m.leaveQuit = m.leaveFrom, false
		return nil
	case screenVaultUnlock:
		m.screen = m.vaultUnlock.back
		m.vaultUnlock = nil
		m.status = tr(tCancelled)
		return nil
	case screenHelp:
		m.screen = m.helpFrom
		return nil
	case screenCommand:
		m.closeCommandLine()
		return nil
	case screenSelftest:
		return m.updateSelftest(tea.KeyPressMsg{Code: tea.KeyEscape})
	case screenUpdate:
		m.screen = m.updateFrom
		return nil
	case screenRestart:
		m.screen = m.restartBack
		m.status = m.restartHint()
		return nil
	case screenForm, screenSummary:
		return m.requestLeave()
	case screenTargets:
		return m.updateTargets(tea.KeyPressMsg{Code: tea.KeyEscape})
	case screenPaths:
		return m.updatePaths(tea.KeyPressMsg{Code: tea.KeyEscape})
	case screenProviders:
		if m.wizard != nil {
			return m.setupEsc()
		}
	case screenConfirm:
		if m.approvalDetail != "" || m.approveAllConfirm {
			// Both stand over the Approvals list, never a form; see approvals.go.
			m.approvalDetail, m.approveAllConfirm = "", false
			m.screen = screenList
			m.status = tr(tCancelled)
			return nil
		}
		if m.tokenDetail != "" || m.tokenRevoke != "" {
			// Both stand over the Tokens list, never a form; see tokens.go.
			m.tokenDetail, m.tokenRevoke, m.tokenReveal = "", "", false
			m.screen = screenList
			m.status = tr(tCancelled)
			return nil
		}
		if m.confirmRole == "" && m.pendingProfile == "" {
			// The delete question stands over the list.
			m.screen = screenList
			m.status = tr(tCancelled)
			return nil
		}
	}
	// A picker, table, prompt or question over a form steps back to the form, which keeps everything typed
	// into it; nothing it was about is written.
	m.secretInput.Reset()
	m.confirmRole, m.pendingProfile = "", ""
	if m.adminAuth != nil {
		// The admin dialog returns to whatever screen requireAdmin was called from, not always the form; see
		// openAdminPrompt.
		back := m.adminAuth.back
		m.adminAuth = nil
		m.status = tr(tCancelled)
		m.screen = back
		return nil
	}
	if m.vaultOffer != nil {
		// The offer's own cancel decides where this returns to: the form for a single role, or the setup
		// summary that was about to save several at once.
		cancel := m.vaultOffer.cancel
		m.vaultOffer = nil
		return cancel()
	}
	m.screen = screenForm
	return nil
}

// focusNav hands the focus to the bar above the workspace: the option bar when the area has one, else the
// tab line. The workspace keeps showing the list of the active section.
func (m *Model) focusNav() {
	m.barFocus, m.noticeFocus = 0, false
	m.leaveDetail()
	m.screen = screenNav
	m.navSub = m.hasSubTabs()
	m.clearMessages()
}

// requestLeave leaves the open form, or the guided setup, for the list of its own section. Without unsaved
// input it leaves at once; otherwise it asks.
func (m *Model) requestLeave() tea.Cmd {
	if m.wizard != nil && m.wizard.saving {
		// The save in flight decides; leaving now would leave its outcome without a place to show.
		return nil
	}
	if m.wizard != nil && m.wizard.saved != "" {
		return m.finishSetup()
	}
	if !m.dirty() {
		return m.abandon()
	}
	m.askLeave(false)
	return nil
}

// askLeave raises the one question every unsaved change meets, from the screen that is shown now. quit says
// that ctrl+c raised it, so discarding ends the program.
func (m *Model) askLeave(quit bool) {
	m.leaveFrom, m.leaveQuit = m.screen, quit
	m.screen = screenLeave
	m.clearMessages()
}

// interrupt is ctrl+c on every screen: it ends the program at once, unless that would lose unsaved
// changes. A prompt over the form is closed first, so the question is asked, and answered, on the form.
func (m *Model) interrupt() tea.Cmd {
	if m.screen == screenLeave {
		m.leaveQuit = true
		return nil
	}
	if m.st != nil && !m.st.done {
		// The scratch directory is removed only when selftest.Run has returned; quit then.
		m.endSelftest()
		m.st.quitWhenDone = true
		return nil
	}
	if m.export != nil && m.export.writing {
		// The file is being written; quitting now could leave it half done.
		m.status = "Wait until the export is written"
		return nil
	}
	if m.imp != nil && m.imp.working != "" {
		// The configuration and the secrets are being written; quitting now could leave them half done.
		m.status = "Wait until the import is " + map[string]string{"reading": "read", "writing": "written"}[m.imp.working]
		return nil
	}
	if m.payloadSaving || (m.wizard != nil && (m.wizard.saving || m.wizard.saved != "")) || !m.unsaved() {
		return m.quit()
	}
	var cmd tea.Cmd
	switch m.screen {
	case screenTransfer:
	case screenForm, screenSummary, screenProviders, screenPicker, screenTargets, screenPaths:
	default:
		cmd = m.leaveScreen()
	}
	m.askLeave(true)
	return cmd
}

// unsaved reports whether the screen shown, or the form under it, holds changes that no key has kept yet.
func (m *Model) unsaved() bool {
	screen := m.screen
	switch screen {
	case screenVaultUnlock:
		screen = m.vaultUnlock.back
	case screenAdminAuth:
		screen = m.adminAuth.back
	case screenConfirm:
		if m.confirmRole == "" && m.pendingProfile == "" {
			return false
		}
		screen = screenForm
	}
	switch screen {
	case screenTargets:
		if m.targetsPending() {
			return true
		}
	case screenPaths:
		if m.pathsPending() {
			return true
		}
	case screenPicker:
		if m.pickerChanged() {
			return true
		}
	case screenTransfer:
		if m.imp != nil {
			return m.imp.dirty()
		}
		return m.export.dirty()
	case screenForm, screenSummary, screenProviders, screenSecret, screenVaultOffer:
	default:
		return false
	}
	return m.formDirty()
}

// abandon returns to the list of the form's own section, dropping the form or the guided setup with
// everything typed into it. Nothing of it was written.
func (m *Model) abandon() tea.Cmd {
	setup := m.wizard != nil
	m.wizard, m.fields = nil, nil
	cmd := m.returnToList("")
	if setup {
		m.status = tr(tFormSetupCancelledNothingWasWritten)
	}
	return cmd
}

// dirty reports whether leaving the form now would lose input.
func (m *Model) dirty() bool {
	return (m.wizard != nil || m.screen == screenForm) && m.formDirty()
}

// formDirty reports whether the open form holds input. A guided setup with a chosen provider always does;
// a form does when any row differs from what it opened with.
func (m *Model) formDirty() bool {
	if m.wizard != nil {
		return m.wizard.provider != "" && m.wizard.saved == ""
	}
	return m.fields != nil && m.formState() != m.pristine
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

// updateLeave answers the leave question. F2 saves, or goes on in the guided setup, d drops the input and
// goes on, or ends the program after ctrl+c, and esc returns to where it came from unchanged. Nothing else
// answers it, not even y or n, whose meaning would be a guess, so a stray key neither saves nor discards.
func (m *Model) updateLeave(key tea.KeyPressMsg) tea.Cmd {
	from := m.leaveFrom
	switch {
	case keyIs(key, actBack):
		m.screen, m.leaveQuit = from, false
		m.status = tr(tFormStillEditingNothingWasSaved)
	case keyIs(key, actDiscard):
		if m.leaveQuit {
			return m.quit()
		}
		return m.discard()
	case keyIs(key, actSave):
		m.leaveQuit = false
		switch from {
		case screenTargets:
			m.screen = from
			return m.updateTargets(key)
		case screenPaths:
			m.screen = from
			return m.updatePaths(key)
		case screenSummary:
			m.screen = from
			return m.startSetupSave()
		case screenTransfer:
			m.screen = from
			return m.updateTransfer(key)
		case screenPicker:
			m.keepPicker()
		}
		m.screen = screenForm
		if m.wizard != nil {
			m.setupNext()
			return nil
		}
		m.leaveFrom = screenForm
		return m.saveAndLeave()
	}
	return nil
}

// discard is d in the leave question: it drops what the screen it came from holds, down to the one level
// that esc would have left.
func (m *Model) discard() tea.Cmd {
	m.screen = m.leaveFrom
	switch m.leaveFrom {
	case screenTargets:
		switch {
		case m.targetPick != nil:
			m.closePick()
		case m.targetAdd != nil:
			m.targetAdd = nil
		case m.targetEdit >= 0:
			m.targetEdit = -1
			m.targetInput.Blur()
		default:
			m.screen = screenForm
		}
		m.clearMessages()
		m.status = tr(tFormTargetChangesDiscarded)
		return nil
	case screenPaths:
		if m.pathEdit >= 0 {
			m.pathEdit = -1
			m.pathInput.Blur()
		} else {
			m.screen = screenForm
		}
		m.clearMessages()
		m.status = tr(tFormPathChangesDiscarded)
		return nil
	case screenPicker:
		m.screen = screenForm
		m.status = tr(tFormSelectionDiscardedTheRowIs)
		return nil
	case screenTransfer:
		if m.imp != nil {
			m.cancelImport()
			return nil
		}
		m.cancelExport()
		return nil
	}
	setup := m.wizard != nil
	cmd := m.abandon()
	if !setup {
		m.status = tr(tFormChangesDiscardedNothingWasWritten)
	}
	return cmd
}

// saveAndLeave saves the form like F2 does and leaves it only when the save went through. A refused
// save keeps the form open with every input and the reason.
func (m *Model) saveAndLeave() tea.Cmd {
	m.screen = m.leaveFrom
	m.trimFields()
	if m.section == sectionGeneral {
		return m.saveGeneral(true)
	}
	if m.section == sectionVault {
		return m.requireAdmin(m.saveVault)
	}
	if m.section == sectionTokens {
		// A created token opens its own detail screen (see handleTokenAction); a refused one keeps the form.
		return m.requireAdmin(m.createToken)
	}
	if m.isPayloadForm() {
		// The payload secret returns to the list itself once its commit succeeded.
		return m.requireAdmin(m.savePayload)
	}
	// requireAdmin gates the whole save, guards included: a locked vault must be unlocked before
	// guardVaultTypeChange can even check what it already holds (see requireAdmin's own comment).
	return m.requireAdmin(func() tea.Cmd {
		if reason := m.guardVaultTypeChange(); reason != "" {
			m.status, m.fail = "", reason
			return nil
		}
		if cmd := m.guardTypeChange(); cmd != nil {
			// This save has to ask the credential stores first and completes, or explains itself, when they
			// answered; the form stays open until then.
			return cmd
		}
		name := m.entryKey().value()
		cmd := m.save(name)
		if m.fail != "" {
			return cmd
		}
		cmd = m.returnToList(name)
		m.status = tr(tSetupSaved, name)
		return cmd
	})
}

func (m *Model) updateForm(key tea.KeyPressMsg) tea.Cmd {
	if m.payloadSaving {
		// The values are on their way to the store; nothing typed now could still take part.
		return nil
	}
	if m.payloadRemoveAsk {
		switch {
		case keyIs(key, actYes):
			m.payloadRemoveAsk = false
			m.removePayloadField()
		case keyIs(key, actNo) || keyIs(key, actBack):
			m.payloadRemoveAsk = false
			m.status = tr(tFormFieldKept)
		}
		return nil
	}
	if key.String() == keyOf(actSave) {
		// F2 saves, or goes on to the next setup step, from any row; enter never saves.
		if m.wizard != nil {
			m.setupNext()
			return nil
		}
		return m.submit()
	}
	switch m.fields[m.focus].kind {
	case fieldProvider, fieldChoice, fieldMultiChoice, fieldToolList:
		// Every choice row opens its values on enter and /, the way a provider row opens its table.
		switch {
		case keyIs(key, actEnter) || keyIs(key, actSearch):
			if m.fields[m.focus].kind == fieldProvider {
				m.openProviderTable()
			} else {
				m.openPicker()
			}
			return nil
		}
	case fieldTargets, fieldPaths:
		// A target or path list opens like a choice row, and left/right fold it in the form like a tree.
		switch {
		case keyIs(key, actEnter) || keyIs(key, actSearch):
			if m.fields[m.focus].kind == fieldPaths {
				m.openPaths()
			} else {
				m.openTargets()
			}
			return nil
		case keyIs(key, actRight):
			m.fields[m.focus].expanded = true
			return nil
		case keyIs(key, actLeft):
			m.fields[m.focus].expanded = false
			return nil
		}
	case fieldKey:
		if cmd, handled := m.keyRowKey(key); handled {
			return cmd
		}
	case fieldVaultAction:
		// An action row runs at once on enter, never deferred to F2 like the rest of the form.
		switch {
		case keyIs(key, actEnter):
			return m.runVaultActionField(m.fields[m.focus].action)
		}
	case fieldSecret:
		// enter opens the masked prompt of a role row.
		if keyIs(key, actEnter) {
			return m.secretRowKey(m.fields[m.focus].label, key)
		}
	}
	if m.isPayloadForm() && m.payloadKey(key) {
		return nil
	}
	switch {
	case keyIs(key, actBack):
		// Leaving a form never drops input silently: an unchanged form closes, a changed one asks first.
		if m.wizard != nil {
			return m.setupEsc()
		}
		return m.requestLeave()
	case keyIs(key, actNext) || keyIs(key, actDown):
		m.moveFocus(1)
		return nil
	case keyIs(key, actPrev) || keyIs(key, actUp):
		m.moveFocus(-1)
		return nil
	case keyIs(key, actPageDown):
		// Page keys are unused on a form row, text rows included: they pick the next or previous section.
		m.jumpSection(1)
		return nil
	case keyIs(key, actPageUp):
		m.jumpSection(-1)
		return nil
	case keyIs(key, actEnter):
		// A text row goes on to the next row; on the last one enter does nothing.
		if m.hasNextField() {
			m.moveFocus(1)
		}
		return nil
	}

	current := &m.fields[m.focus]
	switch current.kind {
	case fieldChoice:
		// left/right step through the values in place, a quick way on a row of few values.
		previous := current.value()
		switch {
		case keyIs(key, actLeft):
			current.index = wrap(current.index-1, len(current.choices))
		case keyIs(key, actRight):
			current.index = wrap(current.index+1, len(current.choices))
		default:
			return nil
		}
		return m.choiceChanged(previous)
	case fieldProvider, fieldMultiChoice, fieldToolList, fieldTargets, fieldPaths:
		// Their values opened above; no other key changes them.
		return nil
	case fieldSecret:
		// A secret row holds nothing to type into, so its keys are free for what a secret needs.
		return m.secretRowKey(current.label, key)
	case fieldVaultState, fieldVaultAction, fieldKey:
		// None of these kinds holds anything to type into: the state is never focused (it is read-only), and an
		// action already ran, if it was going to, above.
		return nil
	}
	if current.readOnly {
		return nil
	}
	var cmd tea.Cmd
	current.input, cmd = current.input.Update(inputMsg(key))
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
	case accessLabel:
		m.accessModeChosen()
	case stateLabel:
		m.syncActiveFor()
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
	m.picker.text, m.pickerMarks, m.picker.match, m.pickerGroups, m.pickerEffects = m.choiceShown, nil, nil, nil, nil
	if f.kind == fieldToolList || f.kind == fieldMultiChoice {
		m.pickerMarks = map[string]bool{}
		for choice, marked := range f.selected {
			m.pickerMarks[choice] = marked
		}
	}
	if f.kind == fieldToolList && f.label == toolListLabel {
		m.picker.text = m.toolText
	}
	entries := f.choices
	if f.kind == fieldToolList && f.label == toolListLabel {
		metadata, _ := m.cfg.ProviderMetadata(m.formProvider())
		if g := newToolGroups(metadata, f.choices); g != nil {
			m.pickerGroups, entries = g, g.entries()
			m.picker.match = g.matches(m.picker.text)
		}
		if e := newToolEffects(metadata, f.choices); e != nil {
			m.pickerEffects = e
			base := m.picker.match
			text := m.picker.text
			if base == nil {
				base = func(name, query string) bool {
					return query == "" || strings.Contains(strings.ToLower(text(name)), query)
				}
			}
			m.picker.match = e.matches(base)
			entries = append(e.rows(), entries...)
		}
	}
	m.picker.reset(entries)
	m.picker.selectName(f.value())
	m.picker.startFilter()
	m.screen = screenPicker
	m.clearMessages()
}

// updatePicker handles the picker. Every printable key belongs to the filter; enter takes the selected
// value and esc leaves the row as it was.
func (m *Model) updatePicker(key tea.KeyPressMsg) tea.Cmd {
	if m.pickerMarks != nil {
		// A tool list or the effects take any number of values: space ticks the selected one instead of
		// typing a blank, which no tool ID or effect contains, and enter keeps every tick at once.
		switch {
		case keyIs(key, actSpace):
			if choice, ok := m.picker.selected(); ok && m.pickerEffects != nil && isEffectRow(choice) {
				m.pickerEffects.toggle(choice, m.pickerMarks, m.searchVisible())
			} else if ok && m.pickerGroups != nil && m.pickerGroups.isRow(choice) {
				m.pickerGroups.toggle(choice, m.pickerMarks, m.searchVisible())
			} else if ok {
				toggleMark(m.pickerMarks, choice, m.fields[m.focus].kind == fieldMultiChoice)
			}
			return nil
		case keyIs(key, actEnter):
			m.keepPicker()
			m.screen = screenForm
			return nil
		case keyIs(key, actBack):
			if m.pickerChanged() {
				m.askLeave(false)
			} else {
				m.screen = screenForm
			}
			return nil
		}
	}
	switch {
	case keyIs(key, actBack):
		m.screen = screenForm
	case keyIs(key, actEnter):
		choice, ok := m.picker.selected()
		if !ok {
			// Nothing matches the filter, so there is nothing to take.
			return nil
		}
		m.screen = screenForm
		return m.choose(choice)
	case keyIs(key, actLeft) || keyIs(key, actRight):
		// In the grouped tool list these fold the group under the cursor; while a search is typed they stay
		// with the search text, whose matches show every group unfolded anyway.
		if m.pickerGroups == nil || m.picker.query() != "" {
			return m.picker.updateFilter(key)
		}
		if choice, ok := m.picker.selected(); ok && !isEffectRow(choice) {
			if row := m.pickerGroups.fold(choice, keyIs(key, actLeft)); row != "" {
				m.picker.refilter()
				m.picker.selectName(row)
			}
		}
	case keyIs(key, actUp):
		m.picker.move(-1)
	case keyIs(key, actDown):
		m.picker.move(1)
	case keyIs(key, actPageUp) || keyIs(key, actPageDown) || keyIs(key, actHome) || keyIs(key, actEnd):
		start, end := m.pickerWindow()
		m.picker.page(key, start, end)
	default:
		return m.picker.updateFilter(key)
	}
	return nil
}

// keepPicker hands the ticks of the picker to the row it was opened from.
func (m *Model) keepPicker() {
	if m.pickerMarks == nil {
		return
	}
	f := &m.fields[m.focus]
	f.selected = map[string]bool{}
	for choice, marked := range m.pickerMarks {
		if marked {
			f.selected[choice] = true
		}
	}
}

// pickerChanged reports whether the ticks of an open picker differ from the row they came from.
func (m *Model) pickerChanged() bool {
	if m.pickerMarks == nil {
		return false
	}
	kept := m.fields[m.focus].selected
	for choice, marked := range m.pickerMarks {
		if marked != kept[choice] {
			return true
		}
	}
	for choice, marked := range kept {
		if marked && !m.pickerMarks[choice] {
			return true
		}
	}
	return false
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
		return tr(tApprovalNone)
	}
	return choice
}

// choiceShown is how a value of the focused choice row reads in the picker.
func (m *Model) choiceShown(choice string) string {
	label := ""
	if m.focus >= 0 && m.focus < len(m.fields) {
		label = m.fields[m.focus].label
	}
	return choiceText(valueText(label, choice))
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
	return manage.ProviderServices(m.cfg, provider)
}

// providerCredentials are the credentials that can serve one provider; see manage.ProviderCredentials.
func (m *Model) providerCredentials(provider string) []string {
	return manage.ProviderCredentials(m.cfg, provider)
}

// targetHint says what the target of one service means, in that provider's own words.
func (m *Model) targetHint(service string) string {
	return m.providerTargetHint(m.cfg.Services[service].Provider)
}

// providerTargetHint is the short line of the target row: what one target of the provider is, and whether
// the list may stay empty and holds more than one entry. The provider's own description is behind the info
// key (providerTargetDetail).
func (m *Model) providerTargetHint(provider string) string {
	metadata, _ := m.cfg.ProviderMetadata(provider)
	need, count := tr(tFormOptional), tr(tFormSeveral)
	if metadata.Target.Required {
		need = tr(tFormRequired)
	}
	if !metadata.Target.Multiple {
		count = tr(tFormOne)
	}
	return metadata.Target.Label + ": " + need + ", " + count
}

// targetDetail is the explanation behind targetHint.
func (m *Model) targetDetail(service string) string {
	return m.providerTargetDetail(m.cfg.Services[service].Provider)
}

// providerTargetDetail says what one target of the provider is and what the provider allows of the list.
func (m *Model) providerTargetDetail(provider string) string {
	metadata, _ := m.cfg.ProviderMetadata(provider)
	hint := metadata.Target.Description
	if metadata.Target.Required {
		hint += tr(tFormRequiredForSoAnEmpty, metadata.Name)
	}
	if !metadata.Target.Multiple {
		hint += tr(tFormOneAtMost, metadata.Target.Label)
	}
	if metadata.Target.Excludes() {
		hint += tr(tFormExcludable)
	}
	return hint
}

// emptyTargets says what a connection without targets does for the provider.
func emptyTargets(metadata config.TargetMetadata) string {
	if metadata.Required {
		return tr(tFormNoneYetRequired)
	}
	return tr(tFormUnrestricted)
}

// connectionProviderChosen narrows the service and credential rows to the provider now selected. A route
// that binds a BookStack service to a Telegram credential cannot work, so it is not offered here.
func (m *Model) connectionProviderChosen() {
	provider := m.fieldValue(providerLabel)
	m.replaceChoices("service", m.providerServices(provider))
	m.replaceChoices("credential", m.providerCredentials(provider))
	if target := m.field(targetsLabel); target != nil {
		target.hint, target.detail = m.targetHint(m.fieldValue("service")), m.targetDetail(m.fieldValue("service"))
	}
	m.replaceEffectChoices(provider)
	m.syncFilesRows()
	if list := m.field(toolListLabel); list != nil {
		// Tool IDs carry their provider, so no tick survives a change of provider.
		list.choices, list.selected = m.toolChoices(provider, nil), map[string]bool{}
	}
	if m.editing == "" {
		m.applyRecommendedProfile()
	}
}

// accessModeChosen shows the row of the chosen access mode and hides the other one. A hidden row keeps its
// ticks for when its mode is chosen again.
func (m *Model) accessModeChosen() {
	byTools := m.fieldValue(accessLabel) == accessTools
	if effects := m.field(effectsLabel); effects != nil {
		effects.hidden = byTools
	}
	if list := m.field(toolListLabel); list != nil {
		list.hidden = !byTools
		list.hint, list.detail = tr(tFormToolListHint), tr(tFormToolListDetail)
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

// toolText is how one tool reads in the picker: its title, ID and effect, whether it is offered only when ticked
// here, and whether the targets set in the form do not reach it, with the reason as text. The same text is
// what the filter searches. A tool outside the targets stays tickable: the selection is kept and the tool is
// offered once the targets reach it.
func (m *Model) toolText(id string) string {
	metadata, _ := m.cfg.ProviderMetadata(m.formProvider())
	for _, tool := range metadata.Tools {
		if tool.ID != id {
			continue
		}
		lead := ""
		if tool.Title != "" {
			lead = tool.Title + "  "
		}
		text := lead + id + "  " + string(tool.Effect)
		if tool.RequiresToolAllowList {
			text += "  " + tr(tFormListedOnly)
		}
		if target := m.field(targetsLabel); target != nil {
			if reached, hint := metadata.Target.Reach(id, target.reachable()); !reached {
				text += "  " + tr(tFormOutsideTargets)
				if hint != "" {
					text += ": " + hint
				}
				text += ")"
			}
		}
		return text
	}
	return id + "  " + tr(tFormNotRegistered)
}

// formProvider is the provider the form belongs to: the provider row of a connection form, or the provider
// the guided setup was started for, whose later steps carry no provider row of their own.
func (m *Model) formProvider() string {
	if m.wizard != nil {
		return m.wizard.provider
	}
	return m.fieldValue(providerLabel)
}

// accessFields are the access mode row, the effects row and the tool list row of a connection, over the
// effects and tools it holds now. A connection selects by tools when it lists any, also an empty list; a new
// one starts by effect, so the editor writes a tool list only when selected tools is chosen here.
func (m *Model) accessFields(provider string, effects []config.Effect, current []string) []field {
	mode := accessEffects
	if current != nil {
		mode = accessTools
	}
	permissions := permissionField(m.permissionChoicesFor(provider, effects), effects)
	permissions.hint, permissions.detail = m.permissionHint(provider), m.permissionDetail(provider)
	tools := field{label: toolListLabel, kind: fieldToolList,
		choices: m.toolChoices(provider, current), selected: map[string]bool{}}
	for _, tool := range current {
		tools.selected[tool] = true
	}
	permissions.hidden, tools.hidden = current != nil, current == nil
	tools.hint, tools.detail = tr(tFormToolListHint), tr(tFormToolListDetail)
	return []field{choiceField(accessLabel, []string{accessEffects, accessTools}, mode).withHint(tr(tFormAccessHint)).withDetail(tr(tFormAccessDetail)),
		permissions, tools}
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
	if id := manage.ConnectionDefaults(m.cfg, m.formProvider()).ProfileID; id != "" {
		m.applyProfile(id)
	}
	m.syncProfile()
}

// applyProfile expands one profile into the rows below it: selected tools, and exactly its tools ticked. Every tick stays
// free to change before saving.
func (m *Model) applyProfile(id string) {
	metadata, _ := m.cfg.ProviderMetadata(m.formProvider())
	profile, ok := findProfile(metadata, id)
	if !ok {
		return
	}
	if mode := m.field(accessLabel); mode != nil {
		*mode = choiceField(accessLabel, mode.choices, accessTools).withHint(mode.hint).withDetail(mode.detail).inSection(mode.section)
	}
	if list := m.field(toolListLabel); list != nil {
		list.selected = map[string]bool{}
		for _, tool := range profile.Tools {
			list.selected[tool] = true
		}
	}
	m.accessModeChosen()
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

// matchingProfile is the profile whose tools the tool ticks equal, or empty when they match none.
func (m *Model) matchingProfile() string {
	list := m.field(toolListLabel)
	if list == nil || m.fieldValue(accessLabel) != accessTools {
		return ""
	}
	metadata, _ := m.cfg.ProviderMetadata(m.formProvider())
	ticked := list.marked()
	sort.Strings(ticked)
	for _, profile := range metadata.Profiles {
		tools := append([]string(nil), profile.Tools...)
		sort.Strings(tools)
		if strings.Join(tools, ",") == strings.Join(ticked, ",") {
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
		row.index, row.hint, row.detail = len(row.choices)-1, tr(tFormProfileCustomHint), tr(tFormProfileCustomDetail)+"; "+tr(tFormProfileHint)
		return
	}
	profile, _ := findProfile(metadata, match)
	*row = choiceField(profileLabel, row.choices, match).inSection(row.section)
	row.hint, row.detail = profile.Title, profileText(metadata, profile)+"; "+tr(tFormProfileHint)
	if profile.MutationReason != "" {
		row.hint += " " + tr(tFormPreselectsAChange)
	}
}

// profileText says what one profile is for and exactly what it ticks.
func profileText(metadata config.ProviderMetadata, profile config.ToolProfile) string {
	text := profile.Title + ": " + profile.Description
	if profile.MutationReason != "" {
		text += tr(tFormItPreselectsAChangeBecause, profile.MutationReason)
	}
	return text + tr(tFormSelectsToolsEffects, strings.Join(profile.Tools, ", "),
		config.FormatEffects(metadata.ProfileEffects(profile)))
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
		target.hint, target.detail = m.targetHint(m.fieldValue("service")), m.targetDetail(m.fieldValue("service"))
	}
}

func (m *Model) permissionHint(provider string) string {
	return tr(tFormEnterOpensTheEffectsTo)
}

// permissionDetail is the explanation behind permissionHint, with what default means now.
func (m *Model) permissionDetail(provider string) string {
	metadata, _ := m.cfg.ProviderMetadata(provider)
	permissions := manage.DefaultEffects(metadata)
	defaults := make([]string, len(permissions))
	for i, permission := range permissions {
		defaults[i] = string(permission)
	}
	return tr(tFormEnterOpensThisProviderS,
		strings.Join(defaults, ", "))
}

func (m *Model) permissionChoices(provider string) []string {
	metadata, _ := m.cfg.ProviderMetadata(provider)
	choices := []string{"default"}
	for _, permission := range metadata.SupportedEffects {
		choices = append(choices, string(permission))
	}
	return choices
}

func (m *Model) permissionChoicesFor(provider string, current []config.Effect) []string {
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

func (m *Model) replaceEffectChoices(provider string) {
	f := m.field(effectsLabel)
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
	f.hint, f.detail = m.permissionHint(provider), m.permissionDetail(provider)
}

func (m *Model) updateConfirm(key tea.KeyPressMsg) tea.Cmd {
	if m.vaultLockConfirm {
		switch {
		case keyIs(key, actYes):
			back := m.vaultLockBack
			m.vaultLockConfirm, m.screen = false, back
			return m.beginVaultLock()
		case keyIs(key, actNo) || keyIs(key, actBack):
			back := m.vaultLockBack
			m.vaultLockConfirm, m.screen = false, back
			m.status = "Cancelled; the vault stays unlocked"
		}
		return nil
	}
	if m.decryptConfirm {
		switch {
		case keyIs(key, actYes):
			passphrase := m.decryptPassphrase
			m.decryptConfirm, m.decryptPassphrase, m.screen = false, "", screenForm
			return m.runVaultDecrypt(passphrase)
		case keyIs(key, actNo) || keyIs(key, actBack):
			m.decryptConfirm, m.decryptPassphrase, m.screen = false, "", screenForm
			m.status = "Cancelled; the vault stays encrypted"
		}
		return nil
	}
	if m.migratePlanConfirm {
		switch {
		case keyIs(key, actYes):
			m.migratePlanConfirm = false
			return m.beginVaultMigrateWrite()
		case keyIs(key, actNo) || keyIs(key, actBack):
			m.migratePlanConfirm, m.migrate, m.screen = false, nil, screenForm
			m.status = "Cancelled; credentials.yaml was not touched"
		}
		return nil
	}
	if m.migrateDeleteConfirm {
		switch {
		case keyIs(key, actYes):
			m.migrateDeleteConfirm = false
			return m.deleteLegacyCredentials()
		case keyIs(key, actNo) || keyIs(key, actBack):
			m.migrateDeleteConfirm, m.screen = false, screenForm
			m.status = m.migrateResult + "; the file was kept"
			m.migrate, m.migrateResult = nil, ""
		}
		return nil
	}
	if cmd, ok := m.updateTokenScreens(key); ok {
		return cmd
	}
	if name := m.approvalDetail; name != "" {
		switch {
		case keyIs(key, actYes):
			m.approvalDetail, m.screen = "", screenList
			return m.requireAdmin(func() tea.Cmd { return m.approveOne(name) })
		case keyIs(key, actNo) || keyIs(key, actBack):
			m.approvalDetail, m.screen = "", screenList
			m.status = tr(tCancelled)
		case keyIs(key, actSpace):
			m.approvalExpanded = !m.approvalExpanded
		case keyIs(key, actLogs):
			return m.openApprovalLogs(name)
		default:
			m.approval.scroll(key, m.approvalPage)
		}
		return nil
	}
	if m.approveAllConfirm {
		switch {
		case keyIs(key, actYes):
			m.approveAllConfirm, m.screen = false, screenList
			return m.requireAdmin(m.approveAll)
		case keyIs(key, actNo) || keyIs(key, actBack):
			m.approveAllConfirm, m.screen = false, screenList
			m.status = tr(tCancelled)
		}
		return nil
	}
	if profile := m.pendingProfile; profile != "" {
		switch {
		case keyIs(key, actYes):
			m.pendingProfile, m.screen = "", screenForm
			m.applyProfile(profile)
			m.status = tr(tFormProfileTickedReviewTheSelected, profile)
		case keyIs(key, actNo) || keyIs(key, actBack):
			m.pendingProfile, m.screen = "", screenForm
			m.status = tr(tFormProfileNotAppliedTheTicks)
		}
		return nil
	}
	switch {
	case keyIs(key, actYes):
		// Deleting an entry, or a stored secret, is a managing action: requireAdmin gates the
		// actual removal behind this window's admin session, run only once the confirmation itself is given.
		if role := m.confirmRole; role != "" {
			m.confirmRole = ""
			m.screen = screenForm
			return m.requireAdmin(func() tea.Cmd {
				if m.credentialType() == config.CredentialTypeVault {
					return m.removeVaultSecret(m.editing, role)
				}
				return m.removeSecret(m.editing, role)
			})
		}
		m.screen = screenList
		return m.requireAdmin(m.delete)
	case keyIs(key, actNo) || keyIs(key, actBack):
		if m.confirmRole != "" {
			m.confirmRole = ""
			m.screen = screenForm
		} else {
			m.screen = screenList
		}
		m.status = tr(tCancelled)
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
		m.fail = tr(tFormConnectionTestingIsUnavailable)
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

// connectionTestError turns a missing credential role or a locked vault into an editor action. Other
// errors stay intact for redaction, but receive context when they are displayed by finishTest.
func connectionTestError(err error) string {
	var locked *secret.VaultLockedError
	if errors.As(err, &locked) {
		// The resolver never asked this editor's own terminal for the passphrase (see tuiSecrets in
		// internal/cli); it just reported the vault locked, the same as an agent gets over MCP.
		return err.Error() + tr(tFormRunQatlasVaultUnlockFirst)
	}
	var missing *secret.MissingSecretError
	if !errors.As(err, &missing) {
		return err.Error()
	}
	switch missing.Type {
	case config.CredentialTypeKeyring:
		return tr(tFormCredentialIsMissingOpenCredentials,
			missing.Credential, missing.Role, missing.Role)
	case config.CredentialTypeVault:
		return tr(tFormCredentialIsMissingOpenCredentials2,
			missing.Credential, missing.Role, missing.Role)
	}
	return tr(tFormCredentialIsMissingOpenCredentials3,
		missing.Credential, missing.Role)
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
		m.fail = tr(tFormConnectionTestCouldNotRun, m.redactor.Apply(done.err))
		return
	}
	m.testClass = done.class
	return
}

// quit ends the editor. A running test is cancelled first, so its context never outlives the editor.
func (m *Model) quit() tea.Cmd {
	m.stopTest()
	m.closePick()
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
	m.section, m.noticeFocus = s, false
	m.layoutWorkspace()
	m.resetList(s)
	if s.isPage() {
		// A settings page has no list: entering the section opens its form directly.
		return m.enterPage()
	}
	if s == sectionLogs {
		return m.openLogs()
	}
	return m.showList()
}

// returnToList comes back to the list a form or confirmation was opened from. The filter stays, and so
// does the selection: on the named entry when it is shown, on the entry that was selected otherwise, or,
// when that one is gone, on the one now in its place.
func (m *Model) returnToList(name string) tea.Cmd {
	m.list.setItems(m.listItems(m.section))
	if name != "" {
		m.selectEntry(name)
	}
	return m.showList()
}

func (m *Model) showList() tea.Cmd {
	m.stopTest()
	m.testName = ""
	m.editing, m.copyFrom = "", ""
	m.confirmRole = ""
	m.clearMessages()
	m.problems = problemState{}
	if m.section.isPage() {
		// A settings page has no list of its own, so "the list" a form or confirmation returns to is the
		// level above it, the tab line; openSection is what opens the form directly on the way in (see
		// enterPage).
		m.focusNav()
		return nil
	}
	if m.section == sectionLogs {
		m.screen = screenLogs
		return nil
	}
	m.screen = screenList
	if m.section != sectionCredentials {
		return nil
	}
	// The list says where each secret resolves from, and that answer comes from the resolver rather than
	// from anything this editor remembers.
	return m.refreshSources(m.keyringQueries())
}

// enterPage opens the form of a settings page directly: it has no list to show first, so entering the
// section from the tab line is the same one step as opening a list's own entry would be for any other
// section.
func (m *Model) enterPage() tea.Cmd {
	m.stopTest()
	m.testName = ""
	m.editing = ""
	m.confirmRole = ""
	m.clearMessages()
	return m.openPageForm()
}

// selected is the entry under the cursor; a group row is no entry, so no action applies to it.
func (m *Model) selected() (string, bool) {
	name, ok := m.list.selected()
	return name, ok && !isGroupRow(name)
}

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
	case sectionVault:
		// A settings form, not a list of named entries.
	case sectionApprovals:
		report, _ := m.approvalsReport()
		for _, c := range report.Open {
			names = append(names, c.Connection)
		}
	case sectionTokens:
		names = m.tokenNames()
	}
	sort.Strings(names)
	return names
}

// openForm builds the form for a new entry, or for the named existing one.
func (m *Model) openForm(name string) tea.Cmd { return m.openEntry(name, "") }

// openEntry builds the form for a new entry (name and source empty), for the named existing one, or, with
// a source and no name, for a new entry filled from that source.
func (m *Model) openEntry(name, source string) tea.Cmd {
	m.editing, m.copyFrom = name, source
	m.screen = screenForm
	m.clearMessages()
	m.problems = problemState{}
	m.sectionPinned = nil
	m.fields = m.buildFieldsFrom(name, source)
	if m.section == sectionConnections {
		// A new connection starts on the recommended profile, ticked and visible; a saved one opens as it
		// is saved, with the profile row only saying which profile its ticks match. A copy keeps the ticks
		// it was copied with.
		if name == "" && source == "" {
			m.applyRecommendedProfile()
		}
		m.syncProfile()
		m.syncFilesRows()
	}
	m.focus = m.firstEditable()
	m.applyFocus()
	m.pristine = m.formState()
	if m.section == sectionCredentials && !m.isPayloadForm() && m.credentialType() == config.CredentialTypeKeyring {
		return m.refreshSources(m.editedQuery())
	}
	return nil
}

// credentialProvider is the provider a credential belongs to for display; see manage.CredentialProvider.
func (m *Model) credentialProvider(name string, cred config.Credential) string {
	return manage.CredentialProvider(m.cfg, name, cred)
}

// credentialRoles are the secret roles offered for a credential that belongs to provider; with none, every
// compiled role, so a credential written without one stays editable. See manage.OfferedRoles.
func (m *Model) credentialRoles(provider string) []string {
	return manage.OfferedRoles(m.cfg, "", config.Credential{Provider: provider})
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
		fields = append(fields, m.roleField(provider, role, cred.Values[role], credType, i == 0))
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
// keyring or vault it stands for a masked secret. Drawing them alike is what let a keyring credential look
// like an env credential and turn into one on the next save. A variable name that was already typed
// survives a switch back, so trying out every type costs nothing.
func (m *Model) credentialTypeChosen() tea.Cmd {
	credType := m.credentialType()
	stored := credType == config.CredentialTypeKeyring || credType == config.CredentialTypeVault
	for i := range m.fields {
		f := &m.fields[i]
		if f.kind != fieldEnvName && f.kind != fieldSecret {
			continue
		}
		if stored {
			// A secret row builds its hint from where it resolves; a vault row never asks the store to
			// learn it, but the row still carries none of its own to make room for that.
			f.kind, f.hint = fieldSecret, ""
		} else {
			f.kind = fieldEnvName
			f.hint, f.detail = m.roleHints(m.formProvider(), f.label, f.roleLead)
		}
	}
	m.applyFocus()
	if credType == config.CredentialTypeKeyring {
		return m.refreshSources(m.editedQuery())
	}
	return nil
}

func (m *Model) buildFields(name string) []field { return m.buildFieldsFrom(name, "") }

// buildFieldsFrom builds the rows of an entry form. The values come from the entry name, or, for a new
// entry filled from another (name empty, source set), from source; the name row is then the suggested copy
// name and stays editable, because the entry is still new.
func (m *Model) buildFieldsFrom(edit, source string) []field {
	if m.section == sectionTokens {
		return m.tokenFields(source)
	}
	name, shown := edit, edit
	if source != "" {
		name, shown = source, m.copyName(source)
	}
	key := textField("name", shown, edit != "").withHint(tr(tDomainHint)).withDetail(tr(tFormNameDetail))
	if m.section == sectionDefaults {
		key.label, key.hint, key.detail = "domain", domainHint(), tr(tDomainDetail)
	}
	if key.readOnly {
		// A locked field must not go on describing a free choice: that hint would claim the opposite of
		// what the field does. It says that it is locked, and how to get the rename it refuses.
		key.hint, key.detail = tr(tFormLockedHint), ""
	}
	fields := []field{key}

	switch m.section {
	case sectionServices:
		s := m.cfg.Services[name]
		fields = append(fields,
			providerField(m.cfg.Providers(), s.Provider),
			textField("base url", s.BaseURL, false).withHint(tr(tFormBaseURLHint)).withDetail(tr(tFormBaseURLDetail)),
		)
		if name == "" && source == "" {
			metadata, _ := m.cfg.ProviderMetadata(fields[1].value())
			fields[2].input.SetValue(metadata.DefaultBaseURL)
		}
		// The note belongs to the provider, not to this service; it stands here because a service is
		// where a provider is set up.
		fields = append(fields, textField(providerNoteLabel, m.cfg.ProviderNotes[fields[1].value()], false).
			withHint(tr(tPayloadDescriptionHint)).withDetail(tr(tFormProviderNoteDetail)))
	case sectionCredentials:
		cred := m.cfg.Credentials[name]
		if cred.Forward {
			return m.payloadFields(edit, source)
		}
		// A new credential starts on defaults.secret_store; an existing one keeps showing where it is now.
		choice := storageKeyring
		if m.cfg.SecretStore() == config.CredentialTypeVault {
			choice = storageVault
		}
		if cred.Type != "" {
			choice = storageChoice(m.storagePlace(name, cred))
		}
		provider := m.credentialProvider(name, cred)
		fields = append(fields,
			providerField(m.credentialProviders(provider), provider).withHint(tr(tFormCredentialProviderHint)).withDetail(tr(tFormCredentialProviderDetail)),
			storageField(choice),
		)
		fields = append(fields, m.roleFields(cred, fields[1].value(), storageType(choice))...)
	case sectionConnections:
		conn := m.cfg.Connections[name]
		if source != "" {
			conn = conn.WithoutOutdatedTargets()
		}
		// The provider is not stored on a connection: its service already names one. It stands here
		// because it decides what the two rows below may offer, and a route that mixes two systems is a
		// route that cannot work.
		provider := m.cfg.Services[conn.Service].Provider
		if provider == "" {
			provider = m.firstProviderWithService()
		}
		fields = append(fields,
			providerField(m.connectionProviders(), provider).withHint(tr(tFormConnectionProviderHint)).withDetail(tr(tFormConnectionProviderDetail)),
			choiceField("service", m.providerServices(provider), conn.Service).withHint(tr(tFormConnectionServiceHint)).withDetail(tr(tFormConnectionServiceDetail)),
			choiceField("credential", m.providerCredentials(provider), conn.Credential).
				withHint(tr(tFormConnectionCredentialHint)).withDetail(tr(tFormConnectionCredentialDetail)),
			targetsFieldOf(conn),
			pathsField(conn.Paths).withHint(tr(tFormPathsHint)).withDetail(tr(tFormPathsDetail)),
			filesField(filesReadLabel, conn.Files.Read),
			filesField(filesWriteLabel, conn.Files.Write),
			// Description and access explain the route the fields above define. Only the description
			// is published during discovery.
			textField("description", conn.Description, false).withHint(tr(tPayloadDescriptionHint)).withDetail(tr(tFormDescriptionDetail)),
			choiceField(stateLabel, []string{stateActive, stateDisabled}, connectionState(conn.Disabled)).
				withHint(tr(tFormStateHint)).withDetail(tr(tFormStateDetail)),
			m.activeForField(conn),
			field{label: profileLabel, kind: fieldChoice, hidden: true},
		)
		fields = append(fields, m.accessFields(provider, conn.Effects, conn.Tools)...)
		if m.wantsForwardRow(conn) {
			fields = append(fields, m.forwardField(conn.ForwardSecrets))
		}
		fields[4].hint, fields[4].detail = m.targetHint(fields[2].value()), m.targetDetail(fields[2].value())
	case sectionDefaults:
		fields = append(fields,
			choiceField("connection", m.entryNames(sectionConnections), m.cfg.Defaults.Connections[name]),
		)
	}
	return m.groupSections(fields, -1)
}

// submit saves the form, unless the change first has to be checked against the places that keep secrets.
// Saving is a managing action: requireAdmin gates it behind this window's admin session first.
func (m *Model) submit() tea.Cmd {
	m.trimFields()
	if m.section == sectionGeneral {
		return m.saveGeneral(false)
	}
	if m.section == sectionTransfer || m.section == sectionUpdate || m.section == sectionSelftest {
		// These pages hold action rows only; there is nothing to save.
		return nil
	}
	if m.section == sectionVault {
		// The vault form has no name field, and its action rows already ran on their own enter, never
		// deferred to here; F2 saves only the two timeouts (see vaultsettings.go).
		return m.requireAdmin(m.saveVault)
	}
	if m.section == sectionTokens {
		// A token is written to the vault, not to the configuration.
		return m.requireAdmin(m.createToken)
	}
	if m.isPayloadForm() {
		return m.requireAdmin(m.savePayload)
	}
	return m.requireAdmin(func() tea.Cmd {
		if reason := m.guardVaultTypeChange(); reason != "" {
			m.status, m.fail = "", reason
			return nil
		}
		if cmd := m.guardTypeChange(); cmd != nil {
			return cmd
		}
		// The core owns every rule, including that a name must not be empty.
		return m.save(m.entryKey().value())
	})
}

// save applies the form to a copy of the configuration and saves it. The editor keeps the change only when
// the store accepted it.
func (m *Model) save(name string) tea.Cmd {
	credType := m.credentialType()
	wasNewStorable := m.section == sectionCredentials && m.editing == "" &&
		(credType == config.CredentialTypeKeyring || credType == config.CredentialTypeVault)
	choice := m.fieldValue(storageLabel)
	// Captured before the change so autoApprove can tell which connections it newly opened, in an encrypted
	// and unlocked vault only; see approvals.go.
	before := manage.SnapshotApprovals(m.secrets.Vault(), m.cfg)
	previous := m.cfg
	candidate := m.cfg.Clone()
	m.problems = problemState{}
	if err := m.apply(candidate, name); err != nil {
		m.reportSaveError(err, name, true)
		return nil
	}
	logged, err := m.svc.SaveConfig(previous, candidate, m.rev)
	if err != nil {
		if !m.conflicted(err) {
			m.reportSaveError(err, name, true)
		}
		return nil
	}
	m.adoptSaved(candidate)
	// A connection saved directly here is what autoApprove's direct means; every other section saves no
	// single connection by name.
	direct := ""
	if m.section == sectionConnections {
		direct = name
	}
	if wasNewStorable {
		cmd := m.openForm(name)
		// Nothing is stored yet, so the reopened form cannot rediscover the choice from the resolver; it
		// keeps the one that was made.
		*m.field(storageLabel) = storageField(choice)
		m.pristine = m.formState()
		for i := range m.fields {
			if m.fields[i].kind == fieldSecret {
				m.focus = i
				break
			}
		}
		m.applyFocus()
		where := valueText(storageLabel, placeKeyring)
		if credType == config.CredentialTypeVault {
			where = valueText(storageLabel, placeVault)
		}
		m.status = tr(tFormCredentialSavedStoreItsSecrets, where)
		m.addWarning(logged)
		return tea.Batch(cmd, m.autoApprove(before, direct))
	}
	cmd := m.returnToList(name)
	m.status = tr(tSetupSaved, name)
	m.addWarning(logged)
	return tea.Batch(cmd, m.autoApprove(before, direct))
}

func (m *Model) apply(cfg *config.Config, name string) error {
	switch m.section {
	case sectionServices:
		provider := m.fieldValue("provider")
		if err := cfg.SetService(name, config.Service{
			Provider: provider,
			BaseURL:  m.fieldValue("base url"),
			Options:  maps.Clone(cfg.Services[m.sourceEntry(name)].Options),
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
		// The access mode decides which one field is stored; the other row's ticks are not saved.
		var permissions []config.Effect
		var tools []string
		if list := m.field(toolListLabel); list != nil && m.fieldValue(accessLabel) == accessTools {
			tools = list.marked()
		} else {
			var err error
			if permissions, err = config.ParseEffects(m.fieldValue(effectsLabel)); err != nil {
				return err
			}
		}
		var reachable, exclude []string
		if row := targetRowOf(m.fields); row != nil {
			reachable, exclude = row.reachable(), excludesOf(row.entries, row.excluded)
		}
		target, targets := manage.SplitTargets(reachable)
		// No paths are written as none at all: an empty list would be refused.
		var paths []string
		if entries := pathEntries(m.fields); len(entries) > 0 {
			paths = append(paths, entries...)
		}
		// Like paths, a direction without entries is written as none at all.
		var files config.Files
		if entries := filesEntries(m.fields, filesReadLabel); len(entries) > 0 {
			files.Read = append(files.Read, entries...)
		}
		if entries := filesEntries(m.fields, filesWriteLabel); len(entries) > 0 {
			files.Write = append(files.Write, entries...)
		}
		// A connection form without the forward row has none to keep: the row is there whenever a payload
		// secret exists or the connection lists one.
		var forward []string
		if row := m.field(forwardLabel); row != nil {
			forward = append(forward, row.marked()...)
		}
		// SetConnection drops the labels of targets the list no longer holds.
		labels := targetLabelsOf(m.fields)
		return cfg.SetConnection(name, config.Connection{
			Exclude:        exclude,
			Service:        m.fieldValue("service"),
			Credential:     m.fieldValue("credential"),
			Target:         target,
			Targets:        targets,
			Description:    m.fieldValue("description"),
			Effects:        permissions,
			Tools:          tools,
			Paths:          paths,
			Files:          files,
			ForwardSecrets: forward,
			Disabled:       m.fieldValue(stateLabel) == stateDisabled,
			ActiveUntil:    m.activeUntilValue(),
			TargetLabels:   labels,
		})
	case sectionDefaults:
		return cfg.SetDefault(name, m.fieldValue("connection"))
	}
	return nil
}

// formSource is the name of the entry the open form was started from: the one being edited, or the one a
// new entry is copied from.
func (m *Model) formSource() string {
	if m.editing != "" {
		return m.editing
	}
	return m.copyFrom
}

// connectionState is the value of the state row for a connection.
func connectionState(disabled bool) string {
	if disabled {
		return stateDisabled
	}
	return stateActive
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
	section := m.section
	// Captured before the change; a deletion outside Connections cannot open one (the core refuses deleting
	// a service, credential, or default still in use), but computing it the same way as save keeps the two
	// paths in step. See approvals.go.
	before := manage.SnapshotApprovals(m.secrets.Vault(), m.cfg)

	previous := m.cfg
	candidate := m.cfg.Clone()
	m.problems = problemState{}
	if err := m.remove(candidate, name); err != nil {
		m.reportSaveError(err, name, false)
		m.screen = screenList
		return nil
	}
	logged, err := m.svc.SaveConfig(previous, candidate, m.rev)
	if err != nil {
		if !m.conflicted(err) {
			m.reportSaveError(err, name, false)
		}
		m.screen = screenList
		return nil
	}
	m.adoptSaved(candidate)
	cmd := m.returnToList("")
	m.status = tr(tFormDeleted, name)
	m.addWarning(logged)
	if section == sectionConnections {
		return tea.Batch(cmd, m.revokeApproval(name))
	}
	return tea.Batch(cmd, m.autoApprove(before, ""))
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

// hasNextField reports whether a row that takes focus follows the focused one.
func (m *Model) hasNextField() bool {
	for i := m.focus + 1; i < len(m.fields); i++ {
		if !m.fields[i].readOnly && !m.skipped(i) {
			return true
		}
	}
	return false
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
		prev := next
		next = wrap(next+by, len(m.fields))
		if wheelClamp && next == prev {
			// The wheel stops at the end of the form; it never lands on a locked field there.
			next = m.focus
			break
		}
		if !m.fields[next].readOnly && !m.skipped(next) {
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
		if m.fields[i].readOnly || m.skipped(i) {
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
			f.kind == fieldMasked || f.kind == fieldTargets || f.kind == fieldPaths || f.kind == fieldVaultState ||
			f.kind == fieldVaultAction || f.kind == fieldKey {
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
			m.fields[i].kind != fieldTargets && m.fields[i].kind != fieldPaths && m.fields[i].kind != fieldVaultAction &&
			m.fields[i].kind != fieldKey && !m.fields[i].readOnly {
			m.fields[i].input.Focus()
			continue
		}
		m.fields[i].input.Blur()
	}
}

func (m *Model) clearMessages() {
	m.status, m.fail = "", ""
	if m.fields == nil {
		// Without a form, no row remains to carry the problems of a refused deletion.
		m.problems = problemState{}
	}
}

func textField(label, value string, readOnly bool) field {
	in := newTextInput()
	in.SetValue(value)
	in.Prompt = ""
	return field{label: label, kind: fieldText, input: in, readOnly: readOnly}
}

// roleField is the row of one secret role. Under a credential of type env it holds the NAME of an
// environment variable, which the editor reads and writes; under keyring it stands for an entry in the
// credential store, which the editor can fill and remove but never read. The value is never read into the
// editor in either case.
//
// lead marks the first role row, the one that carries what all of them have in common.
func (m *Model) roleField(provider, role, envName, credType string, lead bool) field {
	f := textField(role, envName, false)
	f.section, f.roleLead = secCredential, lead
	f.kind = fieldEnvName
	f.hint, f.detail = m.roleHints(provider, role, lead)
	if credType == config.CredentialTypeKeyring || credType == config.CredentialTypeVault {
		// A secret row builds its hint from what the resolver reported, so it carries none itself. A
		// vault credential's row is drawn the same way, even though s and x on it lead to the CLI instead
		// of the system keyring: it is a masked secret, never a variable name to type.
		f.kind, f.hint = fieldSecret, ""
	}
	return f
}

// roleHint says what the provider-defined role means. Only the first row adds the explanation shared by
// every env row; repeating that part under the next role would read like a fault.
//
// The description is the one of the provider the credential belongs to: a role name like "token" exists in
// several providers, and another provider's text under the row would be wrong. The hint carries the short
// form of the description; detail the full one, or nothing where the two are the same.
func (m *Model) roleHints(provider, role string, lead bool) (hint, detail string) {
	return m.roleHintText(provider, role, tFormEnvHint, lead, false), m.roleDetail(provider, role, tFormEnvHint, lead)
}

// roleHintText joins the description of a role with what its first row adds (lead); full keeps the whole
// description instead of its short form.
func (m *Model) roleHintText(provider, role string, lead textID, isLead, full bool) string {
	parts := m.roleDescription(provider, role, full)
	if isLead && (full || len(parts) == 0) {
		// The sentence all env rows share is the detail; the line itself keeps it only when there is no
		// description to show.
		if !full {
			lead = shortLeads[lead]
		}
		parts = append(parts, tr(lead))
	}
	return strings.Join(parts, "; ")
}

// roleDescription is the help a provider authors for one of its roles, in its short form unless full, after
// the note that the role may stay empty when the provider declares it optional.
func (m *Model) roleDescription(provider, role string, full bool) []string {
	var parts []string
	if m.cfg.SecretRoleOptional(provider, role) {
		parts = append(parts, tr(tFormOptionalRole))
	}
	if description := m.cfg.SecretRoleDescriptionOf(provider, role); description != "" {
		if !full {
			description = shortDescription(description)
		}
		parts = append(parts, description)
	}
	return parts
}

// shortLeads are the one-line forms of the sentences the first role row adds.
var shortLeads = map[textID]textID{tFormEnvHint: tFormEnvShortHint, tSetupMaskedHint: tSetupMaskedShortHint}

// roleDetail is the full hint of a role row, or "" where the short hint already says all of it.
func (m *Model) roleDetail(provider, role string, lead textID, isLead bool) string {
	full := m.roleHintText(provider, role, lead, isLead, true)
	if full == m.roleHintText(provider, role, lead, isLead, false) {
		return ""
	}
	return full
}

// shortDescriptionLimit is how many characters of a provider's description stand inline under a row.
const shortDescriptionLimit = 64

// shortDescription is the part of a provider's description that fits a form: its first clause, or, where
// that is still long, what stands before its first colon, or else its first words. A cut text ends in an
// ellipsis.
func shortDescription(text string) string {
	ellipsis := ""
	if i := strings.Index(text, ";"); i >= 0 {
		text, ellipsis = text[:i], "…"
	}
	runes := []rune(text)
	if len(runes) <= shortDescriptionLimit {
		return text + ellipsis
	}
	if i := strings.Index(text, ":"); i > 0 && utf8.RuneCountInString(text[:i]) <= shortDescriptionLimit {
		return text[:i] + "…"
	}
	cut := string(runes[:shortDescriptionLimit])
	if i := strings.LastIndex(cut, " "); i > 0 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,") + "…"
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

func permissionField(choices []string, permissions []config.Effect) field {
	f := field{label: effectsLabel, kind: fieldMultiChoice, choices: choices, selected: map[string]bool{}}
	if permissions == nil {
		f.selected["default"] = true
		return f
	}
	for _, permission := range permissions {
		f.selected[string(permission)] = true
	}
	return f
}

// toggleMark ticks or unticks one value of a set of ticks. In the effects, default stands for the
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

// targetsLabel is the row of a connection's target list, pathsLabel the row of its path list.
const (
	targetsLabel = "targets"
	pathsLabel   = "paths"
)

// The rows of the two directory lists of a connection: the directories its tools may read files from
// (uploads) and write files to (downloads).
const (
	filesReadLabel  = "uploads"
	filesWriteLabel = "downloads"
)

// filesField is a directory list row of the local file release, a list like the path list.
func filesField(label string, values []string) field {
	hint, detail := tr(tFormFilesReadHint), tr(tFormFilesReadDetail)
	if label == filesWriteLabel {
		hint, detail = tr(tFormFilesWriteHint), tr(tFormFilesWriteDetail)
	}
	return field{label: label, kind: fieldPaths, entries: values}.withHint(hint).withDetail(detail)
}

// isFilesLabel reports whether label is one of the two local file rows.
func isFilesLabel(label string) bool { return label == filesReadLabel || label == filesWriteLabel }

// filesEntries are the entries of the local file row with label among fields, or none.
func filesEntries(fields []field, label string) []string {
	for _, f := range fields {
		if f.kind == fieldPaths && f.label == label {
			return f.entries
		}
	}
	return nil
}

// syncFilesRows shows a local file row only for a direction the form's provider has a tool for, or while it
// still holds entries: such a row stays editable, so the refusal of the core has something to act on.
func (m *Model) syncFilesRows() {
	metadata, _ := m.cfg.ProviderMetadata(m.formProvider())
	for label, offered := range map[string]bool{filesReadLabel: metadata.LocalFiles.Read, filesWriteLabel: metadata.LocalFiles.Write} {
		if f := m.field(label); f != nil {
			f.hidden = !offered && len(f.entries) == 0
		}
	}
}

// targetsField is the target list row over the targets a connection holds now.
func targetsField(values []string) field {
	return field{label: targetsLabel, kind: fieldTargets, entries: values}
}

// withLabels gives a target list row a copy of the labels of its targets.
func (f field) withLabels(labels map[string]string) field {
	f.labels = maps.Clone(labels)
	return f
}

// targetLabelsOf are the labels of the target list among fields, or none when there is no such row.
func targetLabelsOf(fields []field) map[string]string {
	for _, f := range fields {
		if f.kind == fieldTargets {
			return maps.Clone(f.labels)
		}
	}
	return nil
}

// pathsField is the path list row over the paths a connection holds now.
func pathsField(values []string) field {
	return field{label: pathsLabel, kind: fieldPaths, entries: values}
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

// pathEntries are the entries of the path list among fields, or none when there is no such row.
func pathEntries(fields []field) []string {
	for _, f := range fields {
		if f.kind == fieldPaths {
			return f.entries
		}
	}
	return nil
}

// targetSummary is the short form of a target list: a single target as it is, and otherwise how many there
// are with the first two of them.
func targetSummary(values []string) string {
	return listSummary(values, "targets")
}

// listSummary is the short form of a list row whose entries are called plural.
func listSummary(values []string, plural string) string {
	switch len(values) {
	case 0:
		return ""
	case 1:
		return values[0]
	}
	text := fmt.Sprintf("%d %s: %s", len(values), wordText(plural), strings.Join(values[:2], ", "))
	if len(values) > 2 {
		text += fmt.Sprintf(", +%d", len(values)-2)
	}
	return text
}

// listLines is the unfolded form of a list row: every entry on a line of its own, under how many there are.
func listLines(values []string, singular, plural string) string {
	if len(values) == 1 {
		return "1 " + wordText(singular) + "\n" + values[0]
	}
	return fmt.Sprintf("%d %s\n%s", len(values), wordText(plural), strings.Join(values, "\n"))
}

func wrap(i, n int) int {
	if n <= 0 {
		return 0
	}
	if wheelClamp {
		return min(max(i, 0), n-1)
	}
	return ((i % n) + n) % n
}

// sectionShortcut is the digit of an area: the section it opens on.
func sectionShortcut(key string) (section, bool) {
	if len(key) != 1 || key[0] < '1' || key[0] > '0'+byte(areaCount) {
		return 0, false
	}
	return areaSections[key[0]-'1'][0], true
}

// View is the Bubble Tea entry point; all drawing lives in render. The editor draws inline, below the shell
// prompt, never on the alternate screen.
func (m *Model) View() tea.View {
	v := tea.NewView(m.render())
	v.Cursor = m.cursor
	v.MouseMode = m.mouseMode()
	return v
}

// render draws the frame: the configuration path, the sections, and the workspace with the current screen.
func (m *Model) render() string {
	view, cursor := takeCursor(m.renderFrame())
	m.cursor = cursor
	if styles.filled && view != "" {
		view = paintCanvas(view, styles.canvasPrefix(), m.termWidth, m.termHeight)
	}
	return shown(view)
}

// renderFrame is the frame before the base colour of the theme is applied.
func (m *Model) renderFrame() string {
	if m.quitting {
		return ""
	}
	m.hits, m.frameHits, m.tooSmall = nil, nil, false
	m.drawn = true
	if m.dd != nil {
		m.dd.placed = false
	}
	m.screenKeys, m.screenPos = "", ""
	m.layoutWorkspace()
	if m.terminalTooSmall() {
		if m.screen == screenLeave {
			// With no frame there is no footer: the question keeps its keys.
			var footers []footerLine
			m.footerLog = &footers
			defer func() { m.footerLog = nil }()
			return m.boundKeepingKeys(m.leaveView(), m.termWidth, m.termHeight)
		}
		return m.resizeView()
	}
	if m.infoOpen {
		return m.infoView()
	}
	if view, ok := m.modalView(); ok {
		return view
	}
	if m.questionScreen() || m.promptScreen() {
		// A dialog that has no room for a modal fills the workspace; its keys stand in the footer, as buttons.
		view := m.frame(m.workspaceView())
		m.footerHits()
		return view
	}
	return m.overlayDropdown(m.frame(m.workspaceView()))
}

// promptScreen reports whether the current screen is a short input dialog or a connection test result.
func (m *Model) promptScreen() bool {
	_, ok := m.prompt()
	return ok
}

// workspaceView is the current screen, or, when it cannot fit the workspace, what it needs and the way out
// of it. The sections and the path stay on screen either way.
func (m *Model) workspaceView() string {
	view := m.fittedEditorView()
	if m.screen == screenLeave || m.viewFits(view) {
		return view
	}
	m.tooSmall = true
	width, height := lipgloss.Width(view), lipgloss.Height(view)
	way := "{back} leave, asking first if anything changed"
	if m.screen == screenNav || m.screen == screenList || m.screen == screenLogs {
		way = "q quit"
	}
	return m.boundView(wrapCells(tr(tResizeLine,
		width+m.termWidth-m.width, height+m.termHeight-m.height, footerKeys(way)), m.width))
}

// editorView renders the current screen. A form shows only the one-line hint of the focused field, directly under
// it: the field a person is on is the one they need explained, and that is true whatever the terminal size
// and however the screen was reached, so it never surprises with every hint at once. Validation errors and
// warnings are not hints and stay visible regardless of focus.
func (m *Model) editorView() string {
	switch m.screen {
	case screenNav, screenList:
		return m.listView()
	case screenPicker:
		return m.pickerView()
	case screenProviders:
		return m.providerTableView()
	case screenHelp:
		return m.helpView()
	case screenCommand:
		return m.commandLineView()
	case screenSelftest:
		return m.selftestView()
	case screenUpdate:
		return m.framedText(m.updateView) + m.notes()
	case screenRestart:
		return m.framedText(m.restartView) + m.notes()
	case screenTargets:
		return m.targetsView()
	case screenPaths:
		return m.pathsView()
	case screenLogs:
		return m.logsView()
	case screenLogDetail:
		return m.logDetailView() + m.notes()
	case screenTemplate:
		return m.templateView()
	case screenRename:
		return m.renameView()
	case screenFindings:
		return m.findingsView()
	case screenTransfer:
		if m.imp != nil {
			return m.importView()
		}
		return m.exportView()
	}
	var b strings.Builder
	switch m.screen {
	case screenForm:
		noun := m.section.entry()
		if m.isPayloadForm() {
			noun = tr(tFormPayloadSecret)
		}
		what := "New " + noun
		switch {
		case m.section == sectionTokens && m.copyFrom == "":
			what = tr(tFormNewToken)
		case m.section == sectionDefaults && m.copyFrom == "":
			what = tr(tFormNewDefault)
		}
		if m.editing != "" {
			what = tr(tFormEdit, noun)
		} else if m.copyFrom != "" {
			what = tr(tFormNewFrom, noun, m.copyFrom)
			if m.termWidth < narrowWidth {
				what = tr(tFormNewNfrom, noun, m.copyFrom)
			}
		} else if m.section.isPage() {
			what = m.section.pageTitle()
		}
		if m.wizard != nil {
			b.WriteString(m.setupHeading(false))
		} else {
			b.WriteString(m.formTitle(what) + "\n\n")
		}
		b.WriteString(m.otherProblems())
		m.formTop = m.workspaceTop() + strings.Count(b.String(), "\n")
		b.WriteString(m.formBody())
		if m.copyFrom != "" && m.editing == "" && m.wizard == nil {
			b.WriteString("\n" + m.wrapped(styles.hint, m.copyNote()) + "\n")
		}
		keys := "enter next · tab next field · " + formKeys
		switch m.fields[m.focus].kind {
		case fieldChoice:
			keys = "enter choose · left/right switch · tab next field · " + formKeys
		case fieldMultiChoice, fieldToolList:
			keys = "enter open · tab next field · " + formKeys
		case fieldProvider:
			keys = "enter choose provider in the table · tab next field · " + formKeys
		case fieldTargets, fieldPaths:
			keys = "enter edit list · right/left expand/collapse · tab next field · " + formKeys
		case fieldVaultAction:
			keys = "enter run · tab next field · " + formKeys
		case fieldKey:
			keys = "enter press new key · d standard · tab next field · " + formKeys
		}
		if m.keyCapture {
			keys = "press the new key · {back} cancel"
		}
		if m.isPayloadForm() {
			if payloadKeys := m.payloadKeys(); payloadKeys != "" {
				keys = payloadKeys
			}
		}
		if m.fields[m.focus].kind == fieldSecret {
			if m.editing == "" {
				keys = "{save} save credential first · tab next field · " + leaveKeys
			} else {
				keys = m.secretKeys() + " · tab next field · " + formKeys
			}
		}
		if m.wizard != nil {
			keys = strings.Replace(keys, formKeys, setupKeys(m.wizard.step), 1)
		}
		if m.section == sectionTokens && m.wizard == nil {
			// A token is created, not saved: it goes to the vault, never into the configuration.
			keys = strings.ReplaceAll(keys, " save", " create")
		}
		if m.section == sectionTransfer || m.section == sectionUpdate || m.section == sectionSelftest {
			keys = "enter run · tab next field · " + leaveKeys
		}
		if m.payloadRemoveAsk {
			b.WriteString("\n" + m.wrapped(styles.warning, tr(tFormRemoveTheField, m.shownLabel(m.fields[m.focus].label))) + "\n")
			keys = "y remove · n/{back} keep · {command} cmds · {help} help"
		}
		if !m.payloadRemoveAsk && m.infoWorks() {
			keys += " · {info} info"
		}
		if len(m.formSections()) >= 2 && !m.payloadRemoveAsk && !m.keyCapture {
			// The key to the sections is named only where that costs no extra footer line.
			if with := keys + " · pgup/pgdn section"; len(m.keyHintLines(with)) <= len(m.keyHintLines(keys)) {
				keys = with
			}
		}
		b.WriteString(m.keyHint(keys))
	case screenSecret, screenVaultOffer, screenAdminAuth, screenVaultUnlock:
		d, _ := m.prompt()
		b.WriteString(m.promptScreenText(d))
	case screenConfirm:
		b.WriteString(m.framedQuestion(m.confirmView))
	case screenSummary:
		b.WriteString(m.summaryView())
	case screenLeave:
		b.WriteString(m.framedText(m.leaveView))
	}

	b.WriteString(m.notes())
	return b.String()
}

// secretPrompt is the masked dialog for the secret of the credential role being edited.
func (m *Model) secretPrompt() promptDialog {
	where := tr(tFormOfThisMachine, secret.StoreLabel(platform))
	if m.credentialType() == config.CredentialTypeVault {
		where = tr(tFormTheVault)
	}
	return promptDialog{
		title: tr(tFormSecretFor, m.editing, m.secretRole),
		input: m.inputLine("  ", m.secretInput),
		field: true,
		hint:  tr(tFormMaskedGoesInto, where),
		keys:  "enter store · {back} cancel",
	}
}

// formKeys ends the key line of every form: F2 saves from every row and, in leaveKeys, esc is the way out
// of it, which asks before unsaved input is lost.
const (
	leaveKeys = "{back} leave"
	formKeys  = "{save} save · " + leaveKeys
)

// leaveView is the leave question. It names what would be lost and where the user was going, and offers
// exactly the answers that apply.
func (m *Model) leaveView() string {
	if m.leaveFrom == screenTransfer {
		if m.imp != nil {
			return m.importLeaveView()
		}
		return m.exportLeaveView()
	}
	what := tr(tLeaveNew, m.section.entry())
	where := tr(tLeaveWhereList, m.section.title())
	switch {
	case m.editing != "":
		what = m.editing
	case m.section == sectionVault:
		what, where = tr(tLeaveVaultSettings), sectionVault.title()
	case m.section == sectionGeneral:
		what, where = tr(tLeaveGeneralSettings), sectionGeneral.title()
	}
	title, warning, save := tr(tLeaveTitle, what), tr(tLeaveWarning, what), "{save} save"
	why := tr(tLeaveWhy, where)
	if m.wizard != nil {
		title, warning, save = tr(tLeaveSetupTitle), tr(tLeaveSetupWarning), "{save} next step"
		if m.leaveFrom == screenSummary {
			save = "{save} save"
		}
		why = tr(tLeaveSetupWhy, where)
	}
	if sub := m.leaveSubject(); sub != "" {
		title, warning = tr(tLeaveSubTitle), tr(tLeaveSubWarning, sub)
		why = tr(tLeaveSubWhy)
	}
	discard := "d discard"
	if m.leaveQuit {
		title = tr(tLeaveQuitTitle)
		discard = "d discard and quit"
		why = tr(tLeaveQuitWhy)
	}
	return m.questionText(question{
		title: title, consequence: why, danger: true,
		details: []string{m.indentedWith(styles.warning, warning), m.indented(tr(tLeaveNothingWritten))},
		keys:    save + " · " + discard + " · {back} keep editing · {command} cmds · {help} help",
	})
}

// leaveSubject names what a list, a picker or a builder would lose; it is empty for a form and a setup.
func (m *Model) leaveSubject() string {
	switch m.leaveFrom {
	case screenPicker:
		if m.pickerChanged() {
			return tr(tLeaveTicks)
		}
	case screenTargets:
		if !m.targetsPending() {
			return ""
		}
		switch {
		case m.targetPick != nil:
			return tr(tLeaveMarked)
		case m.targetAdd != nil && m.targetAdd.kind >= 0:
			return tr(tLeaveNewTarget)
		case m.targetAdd != nil || m.targetEdit >= 0:
			return tr(tLeaveTypedTarget)
		}
		if slices.Equal(m.targetList.all, m.fields[m.focus].entries) &&
			sameExcluded(m.targetExcluded, m.fields[m.focus].excluded) {
			return tr(tLeaveTargetLabels)
		}
		return tr(tLeaveTargetList)
	case screenPaths:
		if !m.pathsPending() {
			return ""
		}
		list := "path"
		if isFilesLabel(m.fields[m.focus].label) {
			list = m.fields[m.focus].label
		}
		if m.pathEdit >= 0 {
			return tr(tLeaveTypedList, list)
		}
		return tr(tLeaveListChanged, list)
	}
	return ""
}

// notes are the lines under every editor screen: what is still running, and what the last action did.
func (m *Model) notes() string {
	var b strings.Builder
	for _, note := range []string{m.busy, m.probing} {
		if note != "" {
			b.WriteString("\n\n" + m.wrapped(styles.hint, tr(tNoteRunning, note)))
		}
	}
	if m.fields == nil || m.screen == screenSummary {
		// A refused deletion has no form to stand above, nor has the summary of the setup.
		if other := m.otherProblems(); other != "" {
			b.WriteString("\n\n" + strings.TrimRight(other, "\n"))
		}
	}
	if m.fail != "" {
		b.WriteString("\n\n" + m.wrapped(styles.fail, tr(tNoteError, m.fail)))
	} else if m.status != "" {
		b.WriteString("\n\n" + m.wrapped(styles.hint, m.status))
	}
	return b.String()
}

// listView draws the list screen: its frame, and between them as many rows as fit. Logs and the Overview
// have no list; the tab line stays above them.
func (m *Model) listView() string {
	if m.section == sectionLogs {
		return m.logsView()
	}
	if m.section == sectionOverview {
		return m.overviewView()
	}
	if m.detailFocus && !m.detailFocused() {
		m.leaveDetail()
	}
	header, footer := m.listFrame()
	row := m.listRows()
	var b strings.Builder
	b.WriteString(header)
	start, end := m.windowIn(&m.list, header, footer, row)
	for i := start; i < end; i++ {
		b.WriteString(row(i) + "\n")
	}
	m.hitList(header, start, end, row)
	if m.paneWidth() > 0 {
		room := m.height - strings.Count(footer, "\n") - 1
		out := m.withPane(strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n"), footer, room, m.titleRows())
		m.hitNotice(out, footer)
		return out
	}
	b.WriteString(footer)
	m.hitNotice(b.String(), footer)
	return b.String()
}

// listFrame is everything of the list screen except its rows: above them the heading, the position of the
// selection, the filter and the column names; below them the test result, the keys and the notes. The rows
// get the lines that are left, so a list of any length fits the terminal and only the rows scroll.
func (m *Model) listFrame() (string, string) {
	var head strings.Builder
	framed, room := m.tableFramed(), m.tableRoom()
	// inside puts a line of the table into the frame, where there is one.
	inside := func(text string) string {
		if !framed {
			return text + "\n"
		}
		var b strings.Builder
		for _, line := range strings.Split(text, "\n") {
			b.WriteString(panelSide(line, room, m.tableFocused()) + "\n")
		}
		return b.String()
	}
	note := func(style lipgloss.Style, text string) string {
		if !framed {
			return m.wrapped(style, text) + "\n"
		}
		return inside(render(style, room, text))
	}
	total := m.entryTotal()
	shown, pos := m.entryPosition()
	filtered := m.list.editing || m.list.query() != ""
	if framed {
		position := ""
		switch {
		case filtered:
			position = fmt.Sprintf("%d/%d (%d total)", pos, shown, total)
		case total > 0:
			position = fmt.Sprintf("%d/%d", pos, total)
		}
		head.WriteString(m.titled(fmt.Sprintf("%s (%d)", m.section.title(), total), position, room+4))
		head.WriteString(panelTop("", "", room, m.tableFocused()) + "\n")
	} else {
		title := styles.title.Render(m.section.title())
		switch {
		case filtered:
			title += fmt.Sprintf("  %d/%d (%d total)", pos, shown, total)
		case total > 0:
			title += fmt.Sprintf("  %d/%d", pos, total)
		}
		head.WriteString(title + "\n")
	}
	bar := m.listBarShown()
	switch {
	case bar:
		// The search field stands in the line of the bar, so a filter costs no row.
		head.WriteString(inside(m.barLine(room)))
	case filtered:
		head.WriteString(inside(m.filterLine(&m.list)))
	case !framed:
		head.WriteString("\n")
	}
	if total > 0 {
		t := m.listTable()
		indent := m.entryIndent()
		_, width := m.fitIn(room, indent)
		head.WriteString(inside(styles.hint.Render(clipCells(indent+t.header(t.layout(width)), room))))
	}
	switch {
	case total == 0 && bar:
		head.WriteString(note(styles.hint, tr(tFormNoEntryMatchesTheFilter)))
	case total == 0:
		head.WriteString(note(styles.hint, m.emptyHelp()))
	case shown == 0:
		head.WriteString(note(styles.hint, tr(tFormNoEntryMatchesPressEsc, m.list.query())))
	}

	back := m.backToSectionsHint()
	var finds []renameFinding
	if m.screen == screenList && !m.list.editing {
		finds = m.sectionFindings()
	}
	var keys string
	switch {
	case m.screen == screenNav:
		keys = m.navKeys()
	case m.list.editing:
		keys = "type to filter · up/down move · enter keep filter · {back} clear filter"
	case m.noticeFocused():
		keys = "enter review · up/{back} rows · {command} cmds · {help} help · q quit"
	case m.barFocus > 0:
		keys = m.barKeys()
	case m.detailFocused() && m.detailPane.more:
		keys = "←/→ tab · tab next · shift+tab rows · up/down move · pgup/pgdn scroll · enter edit · {back} rows · {command} cmds · {help} help · q quit"
	case m.detailFocused():
		keys = "←/→ tab · tab next · shift+tab rows · up/down move · enter edit · {back} rows · {command} cmds · {help} help · q quit"
	case m.section == sectionApprovals && m.vaultLocked():
		keys = "enter unlock · " + back + " · {command} cmds · {help} help · q quit"
	case m.section == sectionApprovals:
		keys = "enter details · a approve all · " + back + " · {command} cmds · {help} help · q quit"
	case m.section == sectionTokens && m.vaultLocked():
		keys = "enter unlock · n new · " + back + " · {command} cmds · {help} help · q quit"
	case m.section == sectionTokens && m.tokensBlocked() != "" && total == 0:
		keys = back + " · {command} cmds · {help} help · q quit"
	case m.section == sectionTokens:
		keys = "/ filter · n new · p duplicate · enter show · {delete} revoke · " + back + " · {command} cmds · {help} help · q quit"
	case m.section == sectionConnections:
		keys = "/ filter · ←/→ fold · n new · p duplicate · enter/e edit/fold · r rename · {delete} delete · t test · g setup · " +
			back +
			" · {command} cmds · {help} help · q quit"
	case m.section == sectionCredentials:
		keys = "/ filter · ←/→ fold · n new · p duplicate · enter/e edit/fold · r rename · {delete} delete · g setup · " +
			back + " · {command} cmds · {help} help · q quit"
	case m.templateSection(m.section):
		keys = "/ filter · n new · p duplicate · enter/e edit/fold · r rename · {delete} delete · g setup · " + back +
			" · {command} cmds · {help} help · q quit"
	default:
		keys = "/ filter · n new · enter/e edit · {delete} delete · g setup · " + back + " · {command} cmds · {help} help · q quit"
	}
	if hint := m.vaultLockKeyHint(); hint != "" && !m.list.editing {
		keys = strings.Replace(keys, " · {command}", " · "+hint+" · {command}", 1)
	}
	if m.screen == screenList && !m.list.editing && m.list.query() != "" {
		// The first esc clears the filter, so the areas keep their digits only.
		keys = strings.Replace(keys, "1-6/{back} areas", "1-6 areas", 1) + " · {back} clear filter"
	}
	notice := m.findingsNotice(finds)
	foot := notice + m.keyHint(keys)
	m.noticeAt, m.noticeRows = -1, 0
	if notice != "" {
		// The notice starts with the blank line a hint has, so its text is the second line of the foot.
		m.noticeAt, m.noticeRows = 1, strings.Count(notice, "\n")
	}
	lines := strings.Count(foot, "\n")
	if m.section == sectionConnections {
		for _, name := range m.list.all {
			if !isGroupRow(name) && m.needsDescription(name) {
				foot = m.hint(tr(tFormUndescribedHint)) + foot
				break
			}
		}
		// A connection that offers no tool is valid, so it is named rather than refused, with the same
		// words 'qatlas config validate' uses.
		var idle strings.Builder
		for _, name := range m.list.all {
			if isGroupRow(name) {
				continue
			}
			if warning := m.cfg.IdleWarning(name); warning != "" {
				idle.WriteString("\n" + m.wrapped(styles.warning, warningLine(warning)))
			}
		}
		foot = m.testLine() + idle.String() + foot
	}
	if m.noticeAt >= 0 {
		m.noticeAt += strings.Count(foot, "\n") - lines
	}
	foot += m.notes()
	if framed {
		foot = panelBottom(room, m.tableFocused()) + foot
	}
	return head.String(), foot
}

// Below these sizes the table of a list screen is drawn without its frame, which costs a row and four cells.
const (
	tableFrameMinWidth  = 60
	tableFrameMinHeight = 16
)

// tableFramed reports whether the list screen draws its table in a panel.
func (m *Model) tableFramed() bool {
	return m.usable(0) >= tableFrameMinWidth && m.height >= tableFrameMinHeight
}

// tableFocused reports whether the frame of the table shows the focus: the list has it, not the tab line.
func (m *Model) tableFocused() bool { return m.screen != screenNav && !m.detailFocused() }

// tableRoom is the width of the rows of the list screen: what the list leaves, less the frame.
func (m *Model) tableRoom() int {
	if m.tableFramed() {
		return max(m.listWidth()-4, 1)
	}
	return m.listWidth()
}

// navKeys is the key line while the tab line has the focus: left/right step through the sections, and
// down (like enter and tab) moves the focus into the workspace below it; up does nothing here, so it is left
// out of the hint. Kept short enough to still wrap into two lines at the minimum terminal width.
func (m *Model) navKeys() string {
	keys := "←/→ move · down open · 1-6 open · n new · g setup · {command} cmds · {help} help · q quit"
	if hint := m.vaultLockKeyHint(); hint != "" {
		keys = strings.Replace(keys, " · {command}", " · "+hint+" · {command}", 1)
	}
	return keys
}

// backToSectionsHint names the keys that take the focus from the list back to the tab line.
func (m *Model) backToSectionsHint() string {
	if m.listBarShown() && m.screen == screenList {
		return "tab bar · 1-6/{back} areas"
	}
	return "1-6/{back}/tab areas"
}

// filterLine shows the filter of l: while it is typed with its cursor, afterwards as the text it holds.
func (m *Model) filterLine(l *filterList) string { return m.searchLine(l, tr(tFormFilter)) }

// searchLine shows the filter of l behind the given label.
func (m *Model) searchLine(l *filterList, label string) string {
	return m.searchLineIn(m.usable(0), l, label)
}

// searchLineIn is searchLine within the first room cells of the workspace.
func (m *Model) searchLineIn(room int, l *filterList, label string) string {
	prefix, width := m.fitIn(room, label)
	l.input.Placeholder = tr(tLogSearchPlaceholder)
	if !l.editing {
		return prefix + truncateCells(l.query(), width)
	}
	return prefix + inputView(fitInput(l.input, width))
}

// fitInput returns in scrolling horizontally inside width cells, the cell of the cursor included. The
// scroll state of a text input is only computed when it is edited or its cursor is set, so the cursor is
// set again here: a width given to a copy alone would still draw the whole value. The value itself is
// never cut, only what is drawn of it.
func fitInput(in textinput.Model, width int) textinput.Model {
	if lipgloss.Width(in.Value()) < width {
		// Nothing scrolls, and a width would only pad the line with blanks behind the cursor.
		return in
	}
	setInputWidth(&in, max(width-1, 1))
	pos := in.Position()
	in.CursorEnd()
	in.SetCursor(pos)
	return in
}

// inputLine draws in behind prefix on one line of the room the terminal leaves.
func (m *Model) inputLine(prefix string, in textinput.Model) string {
	return m.inputLineIn(m.usable(0), prefix, in)
}

// inputLineIn is inputLine within the first room cells of the workspace.
func (m *Model) inputLineIn(room int, prefix string, in textinput.Model) string {
	prefix, width := m.fitIn(room, prefix)
	return prefix + inputView(fitInput(in, width))
}

// listRows draws the shown entry at index i of the list as a row of its table, with the columns laid out
// once for all rows. The selection carries the focus marker only while the list has the focus, so the screen
// never shows two.
func (m *Model) listRows() func(i int) string {
	t := m.listTable()
	indent := m.entryIndent()
	room := m.tableRoom()
	_, width := m.fitIn(room, indent)
	layout := t.layout(width)
	return func(i int) string {
		name := m.list.matches[i]
		active := i == m.list.cursor && m.screen != screenNav && m.barFocus == 0 && !m.noticeFocused()
		var row string
		if isGroupRow(name) {
			row = m.listRowIn(room, active, m.groups.styledRow(name, m.groups.open(name) || m.list.query() != ""))
		} else {
			row = m.listRowIn(room, active, indent[2:]+t.line(t.rows[name], layout))
		}
		if m.tableFramed() {
			return panelSide(row, room, m.tableFocused())
		}
		return row
	}
}

// entryIndent is what an entry row is indented by behind the focus marker: below its group row, a grouped
// list's entries stand two cells further in.
func (m *Model) entryIndent() string {
	if m.groups != nil {
		return "    "
	}
	return "  "
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
	m.hitPicker(header, start, end)
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
	position := m.picker.cursor + 1
	if m.pickerEffects != nil {
		// The effect rows are no values: they count neither as a position nor in the totals.
		rows := 0
		for i, name := range m.picker.matches {
			if isEffectRow(name) {
				rows++
				if i < m.picker.cursor {
					position--
				}
			}
		}
		shown, total, position = shown-rows, len(f.choices), max(position, 0)
	}
	title := tr(tFormChooseTotal, m.shownLabel(f.label), min(position, shown), shown, total)
	if m.pickerGroups != nil {
		ticked, all := m.pickerGroups.count(m.pickerMarks)
		title = tr(tFormChooseTicked, m.shownLabel(f.label), ticked, all)
	}
	head.WriteString(m.boxTop(title, ""))
	head.WriteString(m.boxSearch(&m.picker, tr(tLogPickSearch)))
	keys := "type to search · up/down move · enter choose · {back} cancel"
	if m.pickerMarks != nil {
		ticked := 0
		for _, choice := range m.picker.all {
			if m.pickerMarks[choice] {
				ticked++
			}
		}
		if m.pickerEffects != nil {
			total = len(f.choices)
		}
		if m.pickerGroups == nil {
			if m.pickerEffects != nil {
				keys = "type to search · up/down move · space tick, on an effect row all its tools · enter keep · {back} cancel"
			}
			head.WriteString(m.boxText(styles.hint, tr(tFormTickedOf, ticked, total)))
			keys = "type to search · up/down move · space tick · enter keep · {back} cancel"
		} else {
			keys = "type to search · up/down move · space tick row, group or effect · left/right fold group · enter keep · {back} cancel"
		}
	} else {
		head.WriteString(m.boxText(styles.hint, tr(tLogPickCurrent, m.choiceShown(f.value()))))
	}
	if shown == 0 {
		head.WriteString(m.boxText(styles.hint, tr(tFormNoValueMatchesEscKeeps, m.picker.query())))
	}
	if f.label == forwardLabel {
		if note := m.forwardBindingNote(m.fieldValue("credential")); note != "" {
			head.WriteString(m.boxText(styles.warning, warningLine(note)))
		}
	}
	if m.infoWorks() {
		keys = strings.Replace(keys, " · {back} cancel", " · {info} info · {back} cancel", 1)
	}
	return head.String(), m.boxBottom() + m.keyHint(keys) + m.notes()
}

// pickerRow draws the shown value at index i of the picker and marks the value the row holds now. Its text
// is cut to fit beside the marker before listRow ever sees it, the way a table cell is cut rather than
// wrapped.
func (m *Model) pickerRow(i int) string { return m.boxRow(m.pickerRowText(i)) }

// pickerRowText is the row at index i without the frame of the panel.
func (m *Model) pickerRowText(i int) string {
	_, width := m.fitIn(m.boxRoom(), "  ")
	choice := m.picker.matches[i]
	active := i == m.picker.cursor
	var text string
	switch {
	case m.pickerEffects != nil && isEffectRow(choice):
		text = m.pickerEffects.styledRow(choice, m.pickerMarks)
	case m.pickerGroups != nil && m.pickerGroups.isRow(choice):
		text = m.pickerGroups.styledRow(choice, m.pickerMarks, m.picker.query() != "")
	case m.pickerMarks != nil:
		text = tickBox(m.pickerMarks[choice]) + " "
		rest := m.picker.text(choice)
		if !active && m.fields[m.focus].label == toolListLabel {
			// The ID and what follows it stand faint on rows that are not active; they stay text, so nothing
			// depends on the faint style. A row without a title is faint from the ID on as well.
			// rest is toolText: the title and its gap come before the ID.
			lead := rest[:max(strings.Index(rest, choice), 0)]
			text += lead + styles.hint.Render(strings.TrimPrefix(rest, lead))
		} else {
			text += rest
		}
	default:
		text = m.choiceShown(choice)
		if choice == m.fields[m.focus].value() {
			text += "  " + styles.ok.Render(tr(tFormCurrent))
		}
	}
	mark := m.pickerMark(m.fields[m.focus], choice)
	return m.listRowIn(m.boxRoom(), active, truncateCells(text, width-lipgloss.Width(mark))+mark)
}

// searchVisible is, while a search is typed in the grouped tool picker, the test for the tools it shows; a
// group row then acts on those only. Without a search it is nil, which stands for every tool.
func (m *Model) searchVisible() func(string) bool {
	query := m.picker.query()
	if query == "" {
		return nil
	}
	return func(tool string) bool { return m.picker.shows(tool, strings.ToLower(query)) }
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
	if m.section == sectionOverview {
		return
	}
	if m.section == sectionLogs && (m.screen == screenNav || m.screen == screenLogs) {
		m.keepLogsScrollPosition()
		return
	}
	switch m.screen {
	case screenNav, screenList:
		m.list.offset, _ = m.listWindow()
	case screenPicker:
		m.picker.offset, _ = m.pickerWindow()
	case screenTemplate:
		if m.template != nil && m.template.picking {
			m.templateList.offset, _ = m.templateWindow()
		}
	case screenProviders:
		m.providers.list.offset, _ = m.providerTableWindow()
	case screenTargets:
		if m.targetPick != nil {
			m.targetPick.top().list.offset, _ = m.pickWindow()
			return
		}
		if m.targetAdd != nil {
			m.targetAdd.choices.offset, _ = m.addWindow()
			return
		}
		m.targetList.offset, _ = m.targetWindow()
	case screenPaths:
		m.pathList.offset, _ = m.pathWindow()
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
	if m.terminalTooSmall() {
		return true
	}
	if _, ok := m.modalView(); ok {
		return false
	}
	return !m.viewFits(m.fittedEditorView())
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
		way = "{back} leave"
	}
	// The way out comes first, so it is the last thing a terminal of a single row loses.
	lines := []string{
		styles.key.Render(footerKeys(way)),
		styles.warning.Render(warnMark + " " + tr(tResizeTitle)),
		styles.hint.Render("  " + tr(tResizeNeed, minimumWidth, minimumHeight)),
		styles.hint.Render("  " + tr(tResizeNow, m.termWidth, m.termHeight)),
	}
	return m.boundTo(strings.Join(lines, "\n"), m.termWidth, m.termHeight)
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

// nextStepItem is one thing to do next and the section that deals with it.
type nextStepItem struct {
	text   string
	target section
}

// nextSteps are all the open next steps in order of importance, never empty: open approvals, then a vault
// without a passphrase, then the connection setup or its test.
func (m *Model) nextSteps() []nextStepItem {
	var steps []nextStepItem
	if m.hasOpenApprovals() {
		steps = append(steps, nextStepItem{tr(tStepOpen, sectionApprovals.ref()), sectionApprovals})
	}
	if m.vaultUnencrypted() {
		steps = append(steps, nextStepItem{tr(tStepVaultPassphrase, sectionVault.ref()), sectionVault})
	}
	switch {
	case len(m.cfg.Connections) == 0:
		// The guided setup creates whatever of service and credential is still missing, so it is the
		// next step however far the expert sections got.
		steps = append(steps, nextStepItem{tr(tStepSetup), sectionConnections})
	case len(m.cfg.Defaults.Connections) == 0:
		steps = append(steps, nextStepItem{tr(tStepTestDefaults), sectionConnections})
	default:
		steps = append(steps, nextStepItem{tr(tStepTest), sectionConnections})
	}
	return steps
}

func (m *Model) emptyHelp() string {
	switch m.section {
	case sectionServices:
		return tr(tFormNoServicesYetPressN)
	case sectionCredentials:
		return tr(tFormNoCredentialsYetPressN)
	case sectionConnections:
		return tr(tFormNoConnectionsYetPressN)
	case sectionDefaults:
		if reason := m.newEntryBlocked(); reason != "" {
			return reason + tr(tDefaultsOptional)
		}
		return tr(tDefaultsEmpty)
	case sectionVault, sectionGeneral, sectionTransfer, sectionUpdate, sectionSelftest:
		return tr(tSettingsNotListShort)
	case sectionApprovals:
		_, unavailable := m.approvalsReport()
		if unavailable != "" {
			return unavailable
		}
		return tr(tApprovalsNoneOpen)
	case sectionTokens:
		if _, unavailable := m.tokensReport(); unavailable != "" {
			return unavailable
		}
		if reason := m.tokensBlocked(); reason != "" {
			return reason
		}
		return tr(tTokensEmpty)
	}
	return tr(tFormNothingConfiguredYet)
}

// fittedEditorView is screenView, and for a form taller than the terminal, only as many rows around the
// focused one as fit. The hint of the focused row is one line, so rows farther away give way first.
func (m *Model) fittedEditorView() string {
	view := m.screenView()
	if m.screen != screenForm || m.viewFits(view) {
		return view
	}
	defer func() { m.window, m.formFlat = nil, false }()
	m.formFlat = true
	if view = m.screenView(); m.viewFits(view) {
		return view
	}
	lo, hi := 0, len(m.fields)-1
	// Cutting the headings alone, which a window does, may be enough.
	m.window = &[2]int{lo, hi}
	if view = m.screenView(); m.viewFits(view) {
		return view
	}
	for lo < m.focus || hi > m.focus {
		if lo < m.focus {
			lo++
		}
		m.window = &[2]int{lo, hi}
		if view = m.screenView(); m.viewFits(view) {
			return view
		}
		if hi > m.focus {
			hi--
			m.window = &[2]int{lo, hi}
			if view = m.screenView(); m.viewFits(view) {
				return view
			}
		}
	}
	return m.screenView()
}

// fieldHint is what one form row says about itself. A secret row adds the stages the resolver checked,
// which is the actionable half of a missing secret and names no value.
func (m *Model) fieldHint(f field) string { return m.fieldHintText(f, false) }

// fieldDetail is the whole hint of a row, which fieldHint may show shortened. The text is the same for
// every terminal size.
func (m *Model) fieldDetail(f field) string {
	return m.fieldHintText(f, true)
}

// fieldHintText is fieldHint, or with full the hint without the shortening of a provider's description.
func (m *Model) fieldHintText(f field, full bool) string {
	if f.kind == fieldSecret {
		if m.editing == "" {
			parts := m.roleDescription(m.formProvider(), f.label, full)
			if f.roleLead && (full || len(parts) == 0) {
				lead := tr(tFormSaveTheCredentialWithF2)
				if full {
					lead = tr(tFormSaveThisCredentialWithF2)
				}
				parts = append(parts, lead)
			}
			return strings.Join(parts, "; ")
		}
		return m.secretRowHint(m.editing, f.label, f.roleLead, full)
	}
	text := f.hint
	if full && f.detail != "" {
		text = f.detail
	}
	if f.kind == fieldProvider && !full {
		return providerHint(f)
	}
	if full && f.kind == fieldToolList && f.label != forwardLabel && len(f.marked()) > 0 {
		// The ticks are what is saved, so the explanation of the row lists them in full.
		return text + tr(tFormTicked, strings.Join(f.marked(), ", "))
	}
	return text
}

func (m *Model) fieldWarning(f field) string {
	if f.kind == fieldVaultState {
		return m.vaultStateWarning()
	}
	if f.kind == fieldToolList && f.label == forwardLabel && len(f.marked()) > 0 {
		return m.forwardBindingNote(m.fieldValue("credential"))
	}
	if f.kind == fieldPayloadNew {
		return m.forwardBindingNote("")
	}
	if f.kind != fieldTargets {
		return ""
	}
	if m.section == sectionConnections && m.editing != "" && m.cfg.Connections[m.editing].TargetsOutdated {
		return tr(tTargetsOutdatedWarning)
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
	providerName := tr(tFormTheProvider)
	if connection, ok := m.cfg.Connections[m.testName]; ok {
		if metadata, found := m.cfg.ProviderMetadata(m.cfg.Services[connection.Service].Provider); found {
			providerName = metadata.Name
		}
	}
	switch {
	case m.testing:
		return "\n" + m.wrapped(styles.hint, tr(tFormTestingEscCancels, m.testName))
	case m.backdrop:
		// The result stands in its own dialog (see testPrompt) over this screen.
		return ""
	case m.testClass == provider.ClassOK:
		return "\n" + m.wrapped(styles.ok,
			tr(tFormOkOkAcceptedTheConnection, m.testName, providerName))
	case m.testClass != "":
		explanation := provider.Explain(m.testClass, providerName)
		line := "\n" + m.wrapped(styles.fail,
			tr(tFormFailed, m.testName, m.testClass, explanation))
		if failDocHelps(m.testClass) {
			line += m.docText(m.testProvider())
		}
		return line
	}
	return ""
}

// domainHint is the hint of the key row of a default.
func domainHint() string { return tr(tDomainHint) }

// sectionColumns are the columns of the list of each section. The name leads, the free text of a section
// is the first to give way, and a column that only adds detail to the others may be left out of a narrow
// terminal.
var sectionColumns = map[section][]column{
	sectionServices: {{title: "NAME", tone: toneText}, {title: "BASE URL", flex: true, tone: toneText}},
	sectionCredentials: {{title: "NAME", tone: toneText},
		{title: "STORAGE", optional: true, tone: toneDim, short: map[string]string{
			placeKeyring: "keyring", placeEnv: "env", placePlaintext: "file", placeVault: "vault"},
			shown: func(place string) string { return valueText(storageLabel, place) }},
		{title: "SECRETS", flex: true, tone: toneText}},
	sectionConnections: {{title: "NAME", tone: toneText}, {title: "STATE", optional: true, tone: connectionStateTone},
		{title: "SERVICE", tone: toneText}, {title: "EFFECTS", optional: true, tone: toneDim},
		{title: "TOOLS", optional: true, tone: toneDim}, {title: "TARGETS", optional: true, tone: toneDim},
		{title: "DESCRIPTION", flex: true, tone: toneText}},
	sectionDefaults:  {{title: "PROVIDER OR TOOL", tone: toneText}, {title: "CONNECTION", tone: toneText}},
	sectionApprovals: {{title: "CONNECTION", tone: toneText}, {title: "CHANGE", flex: true, tone: toneText}},
	sectionTokens:    {{title: "NAME", tone: toneText}, {title: "EXPIRES", tone: toneDim}, {title: "VORBILDER", flex: true, tone: toneText}},
}

// connectionStateTone colours the state of a connection: active green, expired red, anything else (disabled)
// dimmed. The word stays in the cell.
func connectionStateTone(cell string) lipgloss.Style {
	switch {
	case strings.HasPrefix(cell, valueText(stateLabel, stateActive)):
		return styles.ok
	case strings.HasPrefix(cell, valueText(stateLabel, expiredText)):
		return styles.fail
	}
	return styles.hint
}

// listTable is the table of every entry of the section, filtered out or not.
func (m *Model) listTable() table {
	columns := slices.Clone(sectionColumns[m.section])
	for i := range columns {
		columns[i].title = columnText(columns[i].title)
	}
	t := table{columns: columns, rows: map[string][]string{}}
	for _, name := range m.list.all {
		if !isGroupRow(name) {
			t.rows[name] = m.cells(name)
		}
	}
	return t
}

// describe is what the filter of the list looks at for one entry: every cell of its row, so what can be
// read can be found. It never holds a secret value.
func (m *Model) describe(name string) string {
	text := strings.Join(m.cells(name), columnGap)
	if m.groups != nil || (groupedSection(m.section) && m.opts(m.section).flat) {
		// The provider is the group heading or the detail, not a column; it is found all the same.
		text = m.providerOf(m.section)(name) + columnGap + text
	}
	if m.section != sectionConnections {
		return text
	}
	// The column shows a label instead of its target, and a count instead of several; both are found.
	conn := m.cfg.Connections[name]
	for _, target := range append(conn.TargetValues(), conn.Exclude...) {
		text += columnGap + target + columnGap + m.logText(conn.TargetLabels[target])
	}
	return text
}

// cells are the values of one entry in the columns of its section. They never show a secret value.
func (m *Model) cells(name string) []string {
	switch m.section {
	case sectionServices:
		s := m.cfg.Services[name]
		// Masking comes first so that the log text cannot cut a credential in half and leave a part of it.
		return []string{name, m.logText(config.MaskURL(s.BaseURL))}
	case sectionCredentials:
		// Where the secrets are kept decides what the credential is, so it is a column rather than
		// something the reader has to open the form to find out.
		cred := m.cfg.Credentials[name]
		if cred.Forward {
			return m.payloadCells(name, cred)
		}
		provider := m.credentialProvider(name, cred)
		credentialRoles := m.credentialRoles(provider)
		roles := make([]string, 0, len(credentialRoles))
		for _, role := range credentialRoles {
			source := m.envSource(cred.Values[role])
			if cred.Type == config.CredentialTypeKeyring || cred.Type == config.CredentialTypeVault {
				source = m.roleState(name, role, cred.Type)
			}
			state := secretState(source)
			if state == tr(tSecretMissing) && m.cfg.SecretRoleOptional(provider, role) {
				state = tr(tSecretOptionalNotSet)
			}
			roles = append(roles, role+" "+state)
		}
		return []string{name, m.storagePlace(name, cred), strings.Join(roles, ", ")}
	case sectionConnections:
		conn := m.cfg.Connections[name]
		effects := make([]string, 0, len(config.Effects()))
		for _, permission := range m.cfg.ConnectionEffects(name) {
			effects = append(effects, string(permission))
		}
		tools := tr(tLogAll)
		if conn.Tools != nil {
			tools = tr(tFormListed, len(conn.Tools))
			if len(conn.Tools) == 0 {
				tools = tr(tFormNone)
			}
		}
		// A single target stands as it is, several by their number: the form lists them.
		targets, values := "-", conn.TargetValues()
		switch {
		case len(values) == 1:
			targets = values[0]
			if label := conn.TargetLabels[targets]; label != "" {
				targets = m.logText(label)
			}
		case len(values) > 1:
			targets = tr(tFormTargets, len(values))
		}
		if len(conn.Exclude) > 0 {
			if len(values) == 0 {
				targets = tr(tLogAll)
			}
			targets += " · " + tr(tTargetExceptCount, len(conn.Exclude))
		}
		if conn.TargetsOutdated {
			// The former targets are not what the connection reaches; the row must not read as if they were.
			targets = tr(tTargetsOutdatedCell)
		}
		description := m.logText(conn.Description)
		if description == "" && m.needsDescription(name) {
			description = tr(tFormUndescribedMarker)
		}
		return []string{name, connectionStateCell(m.connectionStateText(conn)), conn.Service, strings.Join(effects, ","), tools,
			targets, description}
	case sectionDefaults:
		return []string{name, m.cfg.Defaults.Connections[name]}
	case sectionApprovals:
		report, _ := m.approvalsReport()
		for _, c := range report.Open {
			if c.Connection == name {
				return []string{name, approvalSummary(c)}
			}
		}
		return []string{name, ""}
	case sectionTokens:
		return m.tokenCells(name)
	}
	return []string{name}
}

// Options is everything Run needs besides the terminal streams' defaults.
type Options struct {
	Service  *manage.Service
	Store    *config.Store
	Tester   Tester
	Secrets  Secrets
	Redactor *redact.Redactor
	// Version is the running qatlas version; empty is read as dev.
	Version string
	// Updater may be nil, in which case the editor does not look for a newer release.
	Updater Updater
	// Restart may be nil, in which case an installed release waits for a restart by hand.
	Restart *Restart
	// SelfTest says how the selftest commands run.
	SelfTest SelfTest
	// Prefs are the device-local preferences; the zero value is read as the defaults.
	Prefs uiprefs.Prefs
	// PrefsWarnings are problems found while loading Prefs; the editor shows the first one at start.
	PrefsWarnings []string
	// SavePrefs stores changed preferences. It may be nil, in which case they cannot be saved.
	SavePrefs func(uiprefs.Prefs) error
	In, Out   *os.File
}

// Run starts the editor on the terminal streams of opts.
func Run(opts Options) error {
	// Read before anything else, so that no child of the editor inherits the handoff.
	predecessor := takeHandoff()
	prefs := opts.Prefs
	if prefs == (uiprefs.Prefs{}) {
		prefs = uiprefs.Default()
	}
	setProfile(detectProfile(opts.Out))
	known, themeWarning := knownTheme(prefs.Theme)
	prefs.Theme = known
	applyTheme(prefs.Theme)
	setLanguage(prefs.Language)
	model, err := New(opts.Service, opts.Store, opts.Tester, opts.Secrets, opts.Redactor)
	if err != nil {
		return err
	}
	model.updater = opts.Updater
	model.version = opts.Version
	model.restart = opts.Restart
	model.selftest = opts.SelfTest
	model.prefs = prefs
	model.savePrefs = opts.SavePrefs
	warnings := opts.PrefsWarnings
	if themeWarning != "" {
		warnings = append(warnings, themeWarning)
	}
	if err := prefs.Keys.WithDefaults().Validate(); err != nil {
		warnings = append(warnings, "keys: "+err.Error()+", using the standard keys")
		prefs.Keys, model.prefs.Keys = uiprefs.DefaultKeys(), uiprefs.DefaultKeys()
	}
	applyKeys(prefs.Keys)
	if start, ok := areaByName(prefs.Start); ok {
		model.start = start
	} else {
		warnings = append(warnings, fmt.Sprintf("start %q is not an area, opening the Overview", prefs.Start))
	}
	if len(warnings) > 0 {
		model.status = "warning: " + warnings[0]
	}
	if predecessor != nil {
		model.adopt(predecessor)
	}
	fragments := newFragmentFilter(time.Now)
	program := tea.NewProgram(model, tea.WithInput(opts.In), tea.WithOutput(legacyKeys{opts.Out}),
		tea.WithFilter(fragments.filter), tea.WithColorProfile(toColorprofile(currentProfile())))
	fragments.program = program
	_, err = program.Run()
	return err
}

// profileConfirmView asks before a profile replaces ticks that were changed by hand or that belong to a
// saved connection. It names exactly what the profile selects.
func (m *Model) profileConfirmView() string {
	metadata, _ := m.cfg.ProviderMetadata(m.formProvider())
	profile, _ := findProfile(metadata, m.pendingProfile)
	return m.questionText(question{
		title:       tr(tFormReplaceTheAccessAndTool, profile.ID),
		consequence: tr(tFormEveryTickStaysChangeableAfterwards),
		details:     []string{m.indented(profileText(metadata, profile))},
		keys:        "y replace the ticks · n/{back} keep them · {command} cmds · {help} help",
	})
}

// confirmView is the question of screenConfirm that the set flag or field selects, or the delete question.
// It ends in the key line and carries no notes, so the same text serves the modal and the full screen.
func (m *Model) confirmView() string {
	switch {
	case m.vaultLockConfirm:
		return m.vaultLockConfirmView()
	case m.decryptConfirm:
		return m.questionText(question{
			title: "Turn the vault's encryption off?",
			consequence: "warning: every secret it holds is written back to disk unencrypted; anyone who can " +
				"read this machine's files can then read them",
			warn: true,
			keys: "y turn off · n/{back} keep it encrypted · {command} cmds · {help} help",
		})
	case m.migratePlanConfirm:
		return m.migratePlanView()
	case m.migrateDeleteConfirm:
		return m.migrateResultView()
	case m.approvalDetail != "":
		return m.approvalDetailView()
	case m.approveAllConfirm:
		return m.approveAllConfirmView()
	case m.tokenRevoke != "":
		return m.tokenRevokeView()
	case m.tokenDetail != "":
		return m.tokenDetailView()
	case m.pendingProfile != "":
		return m.profileConfirmView()
	}
	const keys = "y remove · n/{back} keep · {command} cmds · {help} help"
	if m.confirmRole != "" {
		consequence := tr(tFormItIsRemovedFromThe)
		if m.credentialType() != config.CredentialTypeVault {
			consequence = tr(tFormItIsRemovedFromThe2)
		}
		return m.questionText(question{
			title:       tr(tFormRemoveTheStoredSecretFor, m.editing, m.confirmRole),
			consequence: consequence, keys: keys, danger: true,
		})
	}
	name, _ := m.selected()
	credType := m.cfg.Credentials[name].Type
	consequence := tr(tFormItIsRemovedFromThe3)
	if m.section == sectionCredentials && m.cfg.Credentials[name].Forward {
		consequence = tr(tFormItsStoredValuesAreNot, name)
	} else if m.section == sectionCredentials &&
		(credType == config.CredentialTypeKeyring || credType == config.CredentialTypeVault) {
		consequence = tr(tFormItsStoredSecretsAreNot)
	}
	return m.questionText(question{title: tr(tFormDelete, name), consequence: consequence, keys: keys, danger: true,
		labels: map[string]string{keyOf(actYes): tr(tButtonDelete), keyOf(actNo): tr(tButtonCancel)}})
}
