package tui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The guided setup leads from a provider to a saved and optionally tested connection. It is a sequence of
// ordinary forms over the same configuration core as the sections: every value it offers comes from the
// provider catalog or the configuration, and every step is checked by the core before the next one opens.
//
// Nothing is written before the last step. That includes the secrets of a new credential, so unlike the
// credential form the setup has to hold them until then: they stay in masked inputs, are never drawn, and
// are dropped when the setup is saved or cancelled.

// The steps of the guided setup, in order.
const (
	stepProvider = iota
	stepService
	stepCredential
	stepScope
	stepPermissions
	stepSummary
	setupSteps
)

var setupTitles = [setupSteps]string{"Provider", "Service", "Credential", "Scope", "Permissions", "Summary"}

// setupLeads say in context what a step decides, including the part about scope and rights that is easy to
// get wrong: local permissions narrow what the credential may do, they never widen it.
var setupLeads = [setupSteps]string{
	"Choose the system to connect to. The next steps offer only what this provider defines: its services, " +
		"secret roles, scope and permissions.",
	"A service is the API endpoint of one instance and can serve several connections. Reuse a configured " +
		"one, or add a new one.",
	"A credential says where the secrets come from; the configuration file never holds a secret. Reuse one " +
		"of this provider, or add one. Nothing is stored before you save in the last step.",
	"The connection is what agents and --connection select by name. The target narrows it to a scope inside " +
		"the service; the credential still decides what the provider lets it reach.",
	"Permissions limit locally what agents may do through this connection. They never grant more than the " +
		"credential is allowed at the provider, and they do not take away what it is allowed there. A new " +
		"connection starts on the provider's recommended profile; every tick stays yours to change.",
	"Nothing is written yet. enter stores the new secrets first and then the configuration; if the " +
		"configuration cannot be saved, those secrets are removed again.",
}

const (
	savedLead = "The connection is saved. t runs the same connection test as the Connections list; " +
		"enter opens the Connections list."

	// The choices that add an entry instead of reusing one. A name cannot start with a parenthesis, so
	// neither can stand for a configured entry.
	newService    = "(new service)"
	newCredential = "(new credential)"

	setupProviderHint       = "the system this connection leads to; it decides everything the next steps offer"
	setupServiceHint        = "reuse a configured instance of this provider, or choose " + newService + " to add one"
	setupCredentialHint     = "reuse a credential of this provider, or choose " + newCredential + " to add one"
	maskedHint              = "typed masked and never shown; stored only when you save in the last step"
	setupConnectionNameHint = "a key you choose, without spaces; agents and --connection select the " +
		"connection by it"
)

// setup is the state of a running guided setup. pages keeps the rows of every step that was opened, so
// going back and forth loses nothing that was typed.
type setup struct {
	step     int
	provider string
	pages    [][]field
	// candidate and plan are what the permissions step left behind: the configuration to save, and what
	// has to be written besides it.
	candidate *config.Config
	plan      setupPlan
	saving    bool
	saved     string
}

// setupPlan is what the steps decided. secrets holds the typed values of a new keyring credential until
// they are stored; it is never drawn.
type setupPlan struct {
	service, credential, connection string
	newService, newCredential       bool
	storage                         string
	roles                           []string
	secrets                         map[string]string
}

// setupSavedMsg carries the outcome of the final save back into the event loop.
type setupSavedMsg struct {
	cfg  *config.Config
	name string
	err  error
}

// startSetup opens the first step of the guided setup.
func (m *Model) startSetup() {
	m.clearMessages()
	if len(m.cfg.Providers()) == 0 {
		m.fail = "this build registers no provider"
		return
	}
	m.stopTest()
	m.testName = ""
	m.wizard = &setup{}
	m.setupShow(stepProvider)
}

// setupShow opens one step, building its rows the first time it is reached.
func (m *Model) setupShow(step int) {
	w := m.wizard
	w.step = step
	m.clearMessages()
	if step == stepSummary {
		m.screen = screenSummary
		return
	}
	fresh := step == len(w.pages)
	if fresh {
		w.pages = append(w.pages, m.setupPage(step))
	}
	m.fields = w.pages[step]
	m.screen = screenForm
	if fresh && step == stepPermissions {
		// The permissions of a new connection start on the provider's recommended profile, ticked and
		// visible; going back and forth keeps whatever was changed since.
		m.applyRecommendedProfile()
	}
	m.setupRefresh()
	m.focus = m.firstEditable()
	m.applyFocus()
	if step == stepProvider {
		// The provider step is its table: every provider is shown with the search before anything is chosen.
		m.openProviderTable()
	}
}

// setupPage builds the rows of one step for the chosen provider.
func (m *Model) setupPage(step int) []field {
	provider := m.wizard.provider
	metadata, _ := m.cfg.ProviderMetadata(provider)
	switch step {
	case stepProvider:
		return []field{providerField(m.cfg.Providers(), provider).withHint(setupProviderHint)}
	case stepService:
		// A configured service comes first, so reusing one is what enter does without further choice.
		services := append(m.providerServices(provider), newService)
		return []field{
			choiceField("service", services, services[0]).withHint(setupServiceHint),
			textField("name", "", false).withHint(nameHint),
			textField("base url", metadata.DefaultBaseURL, false).withHint(baseURLHint),
		}
	case stepCredential:
		credentials := append(m.providerCredentials(provider), newCredential)
		fields := []field{
			choiceField("credential", credentials, credentials[0]).withHint(setupCredentialHint),
			textField("name", "", false).withHint(nameHint),
			storageField(storageKeyring),
		}
		// Every role has a masked row and a row for the name of a variable. Only one of them is shown, so
		// what was typed as a secret is never shown as a variable name after a change of mind.
		roles := m.credentialRoles(provider)
		for i, role := range roles {
			fields = append(fields, m.maskedField(role, i == 0))
		}
		for i, role := range roles {
			fields = append(fields, m.roleField(role, "", config.CredentialTypeEnv, i == 0))
		}
		return fields
	case stepScope:
		name := ""
		if _, taken := m.cfg.Connections[provider]; !taken {
			name = provider
		}
		return []field{
			textField("name", name, false).withHint(setupConnectionNameHint),
			textField("target", "", false).withHint(m.providerTargetHint(provider)),
			textField("description", "", false).withHint(descriptionHint),
		}
	case stepPermissions:
		permissions := permissionField(m.permissionChoices(provider), nil)
		permissions.hint = m.permissionHint(provider)
		return append([]field{{label: profileLabel, kind: fieldChoice, hidden: true}, permissions},
			m.toolFields(provider, nil)...)
	}
	return nil
}

// maskedField is the row that takes the secret of one role of a new keyring credential.
func (m *Model) maskedField(role string, lead bool) field {
	f := textField(role, "", false)
	f.kind, f.roleLead = fieldMasked, lead
	f.input.EchoMode = textinput.EchoPassword
	var parts []string
	if description := m.cfg.SecretRoleDescription(role); description != "" {
		parts = append(parts, description)
	}
	if lead {
		parts = append(parts, maskedHint)
	}
	f.hint = strings.Join(parts, "; ")
	return f
}

// setupRefresh shows the rows the choices of the current step call for.
func (m *Model) setupRefresh() {
	switch m.wizard.step {
	case stepService:
		chosen := m.fieldValue("service")
		existing := chosen != newService
		for i := 1; i < len(m.fields); i++ {
			m.fields[i].hidden = existing
		}
		m.fields[0].hint = setupServiceHint
		if existing {
			m.fields[0].hint = fmt.Sprintf("reuses %s at %s unchanged; choose %s to add another instance",
				chosen, m.cfg.Services[chosen].BaseURL, newService)
		}
	case stepCredential:
		chosen := m.fieldValue("credential")
		existing := chosen != newCredential
		env := m.fieldValue(storageLabel) == storageEnv
		for i := 1; i < len(m.fields); i++ {
			f := &m.fields[i]
			switch f.kind {
			case fieldMasked:
				f.hidden = existing || env
			case fieldEnvName:
				f.hidden = existing || !env
			default:
				f.hidden = existing
			}
		}
		m.fields[0].hint = setupCredentialHint
		if existing {
			m.fields[0].hint = fmt.Sprintf("reuses %s (%s) unchanged, and its secrets where they are; "+
				"choose %s to add one", chosen, m.storagePlace(chosen, m.cfg.Credentials[chosen]), newCredential)
		}
	case stepPermissions:
		m.toolsModeChosen()
		m.syncProfile()
	}
}

// setupNext checks the current step against the core and opens the next one. A refused step stays open
// with everything that was typed into it.
func (m *Model) setupNext() {
	w := m.wizard
	m.trimFields()
	w.pages[w.step] = m.fields
	if w.step == stepProvider {
		if provider := m.fieldValue(providerLabel); provider != w.provider {
			// Every later step offers what this provider defines, so none of them survives a change.
			w.provider, w.pages = provider, w.pages[:1]
		}
	} else {
		candidate, plan, err := m.setupCandidate(w.step)
		if err != nil {
			m.status = ""
			m.fail = m.redactor.Apply(err.Error())
			return
		}
		w.candidate, w.plan = candidate, plan
	}
	if w.step == stepCredential && w.plan.storage == storagePlaintext {
		// Consent to an unencrypted file is asked for on its own, like in the credential form.
		m.screen = screenPlaintextConfirm
		m.clearMessages()
		return
	}
	m.setupShow(w.step + 1)
}

// setupBack returns to the previous step. Once saved there is nothing to go back to.
func (m *Model) setupBack() {
	w := m.wizard
	if w.step == stepProvider || w.saving || w.saved != "" {
		return
	}
	if w.step != stepSummary {
		m.trimFields()
		w.pages[w.step] = m.fields
	}
	m.setupShow(w.step - 1)
}

// leaveSetup is esc: it closes a saved setup, and cancels one that is not saved yet, asking first once a
// provider was chosen. Dropping the setup drops every typed secret with it; nothing reached the file or a
// store.
func (m *Model) leaveSetup() tea.Cmd { return m.requestLeave(-1) }

// finishSetup ends a saved setup on the Connections list, with the new connection selected.
func (m *Model) finishSetup() tea.Cmd {
	name := m.wizard.saved
	m.wizard, m.fields = nil, nil
	cmd := m.openSection(sectionConnections)
	m.list.selectName(name)
	m.status = "Saved " + name
	return cmd
}

// pageValue is the value of the shown row with the given label.
func pageValue(page []field, label string) string {
	for _, f := range page {
		if f.label == label && !f.hidden {
			return f.value()
		}
	}
	return ""
}

// setupCandidate applies what the steps up to upto decided to a copy of the configuration and lets the core
// check it. The configuration it starts from was loaded, so every problem the core reports is one of the
// steps.
func (m *Model) setupCandidate(upto int) (*config.Config, setupPlan, error) {
	w := m.wizard
	cfg := m.cfg.Clone()
	var plan setupPlan
	var missing string

	page := w.pages[stepService]
	plan.service = pageValue(page, "service")
	if plan.service == newService {
		plan.service, plan.newService = pageValue(page, "name"), true
		if _, taken := cfg.Services[plan.service]; taken {
			return nil, plan, fmt.Errorf("a service named %q already exists; choose it in the service row "+
				"instead of adding it again", plan.service)
		}
		if err := cfg.SetService(plan.service, config.Service{
			Provider: w.provider, BaseURL: pageValue(page, "base url"),
		}); err != nil {
			return nil, plan, err
		}
	}

	if upto >= stepCredential {
		page := w.pages[stepCredential]
		plan.credential = pageValue(page, "credential")
		if plan.credential == newCredential {
			plan.credential, plan.newCredential = pageValue(page, "name"), true
			if _, taken := cfg.Credentials[plan.credential]; taken {
				return nil, plan, fmt.Errorf("a credential named %q already exists; choose it in the "+
					"credential row instead of adding it again", plan.credential)
			}
			plan.storage = pageValue(page, storageLabel)
			cred := config.Credential{Provider: w.provider, Type: storageType(plan.storage)}
			if cred.Type == config.CredentialTypeEnv {
				cred.Values = map[string]string{}
			} else {
				plan.secrets = map[string]string{}
			}
			for _, f := range page {
				if f.hidden || (f.kind != fieldEnvName && f.kind != fieldMasked) {
					continue
				}
				value := f.value()
				plan.roles = append(plan.roles, f.label)
				switch {
				case value == "" && missing == "" && f.kind == fieldEnvName:
					missing = fmt.Sprintf("%s names no environment variable; name the variable that holds it",
						f.label)
				case value == "" && missing == "":
					missing = fmt.Sprintf("%s is empty; type its secret, or choose %s", f.label, storageEnv)
				case f.kind == fieldEnvName:
					cred.Values[f.label] = value
				default:
					plan.secrets[f.label] = value
				}
			}
			// Only an env credential names anything in the file; the secrets of a keyring credential
			// go to a store and never into this configuration.
			if err := cfg.SetCredential(plan.credential, cred); err != nil {
				return nil, plan, err
			}
		}
	}

	if upto >= stepScope {
		page := w.pages[stepScope]
		plan.connection = pageValue(page, "name")
		if _, taken := cfg.Connections[plan.connection]; taken {
			return nil, plan, fmt.Errorf("a connection named %q already exists; choose another name",
				plan.connection)
		}
		metadata, _ := cfg.ProviderMetadata(w.provider)
		target, targets := pageValue(page, "target"), []string(nil)
		if metadata.Target.Multiple {
			var err error
			if target, targets, err = parseTargets(target); err != nil {
				return nil, plan, err
			}
		}
		conn := config.Connection{
			Service: plan.service, Credential: plan.credential,
			Target: target, Targets: targets, Description: pageValue(page, "description"),
		}
		if upto >= stepPermissions {
			page := w.pages[stepPermissions]
			permissions, err := config.ParsePermissions(pageValue(page, "permissions"))
			if err != nil {
				return nil, plan, err
			}
			conn.Permissions = permissions
			if pageValue(page, toolsLabel) == toolsSelected {
				for _, f := range page {
					if f.kind == fieldToolList {
						conn.Tools = f.marked()
					}
				}
			}
		}
		if err := cfg.SetConnection(plan.connection, conn); err != nil {
			return nil, plan, err
		}
	}

	if err := cfg.Validate(); err != nil {
		return nil, plan, err
	}
	if missing != "" {
		return nil, plan, errors.New(missing)
	}
	return cfg, plan, nil
}

// updateSummary handles the last step: saving, and once saved, testing.
func (m *Model) updateSummary(key tea.KeyMsg) tea.Cmd {
	w := m.wizard
	if key.String() == "ctrl+c" {
		return m.quit()
	}
	if w.saving {
		return nil
	}
	if w.saved != "" {
		switch key.String() {
		case "t":
			return m.testConnection(w.saved)
		case "esc":
			if m.testing {
				m.stopTest()
				m.status = "Connection test cancelled"
				return nil
			}
			return m.finishSetup()
		case "enter", "q":
			return m.finishSetup()
		}
		return nil
	}
	switch key.String() {
	case "esc":
		return m.leaveSetup()
	case "ctrl+b":
		m.setupBack()
	case "enter":
		return m.saveSetup()
	}
	return nil
}

// saveSetup writes what the steps decided. The store may take its time, so the write runs as a command.
func (m *Model) saveSetup() tea.Cmd {
	w := m.wizard
	candidate, plan := w.candidate, w.plan
	w.saving = true
	m.clearMessages()
	m.busy = "saving " + plan.connection
	store, secrets := m.store, m.secrets
	return func() tea.Msg {
		return setupSavedMsg{cfg: candidate, name: plan.connection, err: commitSetup(store, secrets, candidate, plan)}
	}
}

// commitSetup stores the secrets of a new credential and then saves the configuration.
//
// The secrets go first because the configuration is the commit point: a keyring that is locked or missing is
// the common failure, and it then leaves the file untouched instead of a saved connection whose credential
// has nothing behind it. The configuration was checked before anything was written, so what can still fail
// after the secrets is writing the file; the secrets written for it are removed again then, so no store
// entry is left without a credential that names it.
func commitSetup(store *config.Store, secrets Secrets, cfg *config.Config, plan setupPlan) error {
	var written []string
	for _, role := range plan.roles {
		value, ok := plan.secrets[role]
		if !ok {
			continue
		}
		var err error
		if plan.storage == storagePlaintext {
			err = secrets.SetPlaintext(plan.credential, role, value)
		} else {
			err = secrets.Set(plan.credential, role, value)
		}
		if err != nil {
			return rollbackSecrets(secrets, plan.credential, written,
				fmt.Errorf("storing the secret for %s.%s: %w", plan.credential, role, err))
		}
		written = append(written, role)
	}
	if err := store.Save(cfg); err != nil {
		return rollbackSecrets(secrets, plan.credential, written, err)
	}
	return nil
}

// rollbackSecrets removes the secrets a failed setup wrote and says which ones stayed behind.
func rollbackSecrets(secrets Secrets, credential string, roles []string, cause error) error {
	var left []string
	for _, role := range roles {
		if _, err := secrets.Delete(credential, role); err != nil && !errors.Is(err, secret.ErrNoEntry) {
			left = append(left, role)
		}
	}
	if len(left) > 0 {
		return fmt.Errorf("%w; the secrets already stored for %s (%s) could not be removed again, remove "+
			"them with 'qatlas credential delete %s <role>'", cause, credential, strings.Join(left, ", "), credential)
	}
	return cause
}

// setupSaved applies the outcome of the final save.
func (m *Model) setupSaved(msg setupSavedMsg) tea.Cmd {
	m.busy = ""
	w := m.wizard
	if w == nil || m.quitting {
		return nil
	}
	w.saving = false
	if msg.err != nil {
		// Every input stays, secrets included, so the save can be retried or a step changed.
		m.fail = m.redactor.Apply(m.setupError(msg.err))
		return nil
	}
	m.cfg, m.configExists = msg.cfg, true
	w.saved = msg.name
	// The secrets are where they belong now, and the editor drops its only copy of them.
	w.plan.secrets, w.pages, m.fields = nil, nil, nil
	m.status = "Saved " + msg.name
	if m.tester != nil {
		m.status += ". Press t to test it now."
	}
	// Where the secrets of a new credential now resolve from is what the lists and the next setup name.
	return m.refreshSources(m.keyringQueries())
}

// setupError turns a failed save into the way out. The configuration is unchanged in every case.
func (m *Model) setupError(err error) string {
	if errors.Is(err, secret.ErrUnavailable) || errors.Is(err, secret.ErrDisabled) {
		return fmt.Sprintf("%v; nothing was saved. %s, then press enter again, or press ctrl+b to go back "+
			"and choose %s or %s", err, secret.StoreAdvice(secret.StoreStateOf(err), platform), storageEnv,
			storagePlaintext)
	}
	return err.Error() + "; the configuration was not changed"
}

func setupKeys(step int, next string) string {
	if step == stepProvider {
		return next + " next · esc cancel setup"
	}
	return next + " next · ctrl+b back · esc cancel setup · alt+1-4 section"
}

// setupHeading is the title of the current step and what it decides.
func (m *Model) setupHeading(dense bool) string {
	w := m.wizard
	title := fmt.Sprintf("Set up a connection · step %d of %d · %s", w.step+1, setupSteps, setupTitles[w.step])
	lead := setupLeads[w.step]
	if w.saved != "" {
		title, lead = "Set up a connection · saved", savedLead
	}
	heading := m.wrapped(titleStyle, title) + "\n"
	if !dense {
		heading += m.wrapped(hintStyle, lead) + "\n"
	}
	return heading + "\n"
}

// setupPlaintextView asks for consent to the unencrypted file before the setup moves on with it.
func (m *Model) setupPlaintextView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Store these secrets unencrypted?") + "\n\n")
	b.WriteString(m.wrapped(failStyle, fmt.Sprintf("This does not use the system keyring. When you save in the "+
		"last step, the secrets of %s are written as readable text for your user account into %s.",
		m.wizard.plan.credential, m.plaintextPath())) + "\n")
	b.WriteString(m.indented("Choose no to stay on this step and pick the system keyring or environment "+
		"variables.") + "\n")
	b.WriteString(m.hint("y continue · n/esc stay on this step"))
	return b.String()
}

// summaryView shows what the setup saves. It names where every secret goes, never what it is.
func (m *Model) summaryView() string {
	w := m.wizard
	var b strings.Builder
	b.WriteString(m.setupHeading(false))
	for _, row := range m.summaryRows() {
		b.WriteString(m.row(false, row) + "\n")
	}
	if w.plan.storage == storagePlaintext && w.saved == "" {
		b.WriteString(m.indentedWith(warningStyle, "warning: the secrets are written unencrypted into "+
			m.plaintextPath()) + "\n")
	}
	keys := "enter save · ctrl+b back · esc cancel setup · alt+1-4 section"
	switch {
	case w.saving:
		keys = "ctrl+c quit"
	case w.saved != "" && m.testing:
		keys = "esc cancel test · enter done"
	case w.saved != "":
		keys = "t test connection · enter done"
	}
	if w.saved != "" {
		b.WriteString(m.testLine())
	}
	b.WriteString(m.hint(keys))
	return b.String()
}

func (m *Model) summaryRows() []string {
	w := m.wizard
	cfg, plan := w.candidate, w.plan
	metadata, _ := cfg.ProviderMetadata(w.provider)
	conn := cfg.Connections[plan.connection]
	state := func(added bool) string {
		if added {
			return "new"
		}
		return "existing, unchanged"
	}

	secrets := m.storagePlace(plan.credential, cfg.Credentials[plan.credential]) + "; its secrets stay where they are"
	roles := strings.Join(plan.roles, ", ")
	switch {
	case !plan.newCredential:
	case plan.storage == storageEnv:
		names := make([]string, len(plan.roles))
		for i, role := range plan.roles {
			names[i] = role + " from $" + cfg.Credentials[plan.credential].Values[role]
		}
		secrets = placeEnv + ": " + strings.Join(names, ", ")
	case plan.storage == storagePlaintext:
		secrets = placePlaintext + ": " + roles
	default:
		secrets = placeKeyring + ": " + roles
	}

	permissions := config.FormatPermissions(conn.Permissions)
	if conn.Permissions == nil {
		defaults := defaultPermissions(metadata)
		permissions = "default: " + config.FormatPermissions(defaults)
	}
	tools := toolsAll
	switch {
	case conn.Tools == nil:
	case len(conn.Tools) == 0:
		tools = "none: no tool is offered"
	default:
		tools = strings.Join(conn.Tools, ", ")
	}

	return []string{
		fmt.Sprintf("%-12s %s", "provider", metadata.Name),
		fmt.Sprintf("%-12s %s (%s) · %s", "service", plan.service, state(plan.newService),
			cfg.Services[plan.service].BaseURL),
		fmt.Sprintf("%-12s %s (%s) · %s", "credential", plan.credential, state(plan.newCredential), secrets),
		fmt.Sprintf("%-12s %s", "connection", plan.connection),
		fmt.Sprintf("%-12s %s", "target", choiceText(formatTargets(conn))),
		fmt.Sprintf("%-12s %s", "description", choiceText(conn.Description)),
		fmt.Sprintf("%-12s %s", "permissions", permissions),
		fmt.Sprintf("%-12s %s", "tools", tools),
	}
}
