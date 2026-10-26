package tui

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/manage"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
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
	stepEffects
	stepSummary
	setupSteps
)

var setupTitles = [setupSteps]string{"Provider", "Service", "Credential", "Scope", "Access", "Summary"}

// The summary step says tSetupSummaryLead about itself: it has no fields to focus, so the line stands as its
// only explanation. The form steps carry their explanation in the hints of their rows.
const (

	// The choices that add an entry instead of reusing one. A name cannot start with a parenthesis, so
	// neither can stand for a configured entry.
	newService    = "(new service)"
	newCredential = "(new credential)"
)

// setup is the state of a running guided setup. pages keeps the rows of every step that was opened, so
// going back and forth loses nothing that was typed.
type setup struct {
	step     int
	provider string
	// template names the connection the setup started from, "" for one that started empty; see template.go.
	template string
	pages    [][]field
	// candidate and plan are what the access step left behind: the configuration to save, and what
	// has to be written besides it.
	candidate *config.Config
	// built is the core's candidate behind candidate, the one saving hands back to the core.
	built manage.ConnectionCandidate
	plan  setupPlan
	// base is the revision of the configuration candidate was built from; saving commits against it.
	base   config.Revision
	saving bool
	saved  string
	// before is what Pending found just before finishSetupSave ran, captured while m.cfg still held what was
	// loaded, so setupSaved can tell autoApprove which connections this save newly opened, and, for the new
	// connection itself, whether it was already open for a reason its own form never showed (see
	// manage.SnapshotApprovals and approval.DirectApprovable).
	before manage.ApprovalSnapshot
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

// setupSavedMsg carries the outcome of the final save back into the event loop. warning is set only when a
// new vault credential's secrets could not be handed on to a vault process that holds the vault unlocked
// outside this run (see manage.Service.SaveConnectionSetup); "" the rest of the time.
type setupSavedMsg struct {
	cfg     *config.Config
	name    string
	err     error
	warning string
}

// startSetup opens the first step of the guided setup.
func (m *Model) startSetup() {
	m.clearMessages()
	if len(m.cfg.Providers()) == 0 {
		m.fail = tr(tSetupThisBuildRegistersNoProvider)
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
	m.problems = problemState{}
	if step == stepSummary {
		m.screen = screenSummary
		return
	}
	fresh := step == len(w.pages)
	if fresh {
		w.pages = append(w.pages, m.setupPage(step))
		if w.template != "" {
			m.setupPrefill(step, w.pages[step])
		}
	}
	m.fields = w.pages[step]
	m.sectionPinned = nil
	m.screen = screenForm
	if fresh && step == stepEffects && w.template == "" {
		// The access of a new connection starts on the provider's recommended profile, ticked and
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

// setupPage builds the rows of one step for the chosen provider, in the sections of the forms they belong to.
func (m *Model) setupPage(step int) []field { return m.groupSections(m.setupRows(step), step) }

func (m *Model) setupRows(step int) []field {
	provider := m.wizard.provider
	metadata, _ := m.cfg.ProviderMetadata(provider)
	switch step {
	case stepProvider:
		return []field{providerField(m.cfg.Providers(), provider).withHint(tr(tSetupSetupProviderHint)).withDetail(tr(tSetupSetupProviderDetail))}
	case stepService:
		// A configured service comes first, so reusing one is what enter does without further choice.
		services := append(m.providerServices(provider), newService)
		return []field{
			choiceField("service", services, services[0]).withHint(tr(tSetupSetupServiceHint)).withDetail(tr(tSetupSetupServiceDetail)),
			textField("name", "", false).withHint(tr(tDomainHint)).withDetail(tr(tFormNameDetail)),
			textField("base url", metadata.DefaultBaseURL, false).withHint(tr(tFormBaseURLHint)).withDetail(tr(tFormBaseURLDetail)),
		}
	case stepCredential:
		credentials := append(m.providerCredentials(provider), newCredential)
		storage := storageKeyring
		if m.cfg.SecretStore() == config.CredentialTypeVault {
			storage = storageVault
		}
		fields := []field{
			choiceField("credential", credentials, credentials[0]).withHint(tr(tSetupSetupCredentialHint)).withDetail(tr(tSetupSetupCredentialDetail)),
			textField("name", "", false).withHint(tr(tDomainHint)).withDetail(tr(tFormNameDetail)),
			storageField(storage),
		}
		// Every role has a masked row and a row for the name of a variable. Only one of them is shown, so
		// what was typed as a secret is never shown as a variable name after a change of mind.
		roles := m.credentialRoles(provider)
		for i, role := range roles {
			fields = append(fields, m.maskedField(provider, role, i == 0))
		}
		for i, role := range roles {
			fields = append(fields, m.roleField(provider, role, "", config.CredentialTypeEnv, i == 0))
		}
		return fields
	case stepScope:
		name := ""
		if _, taken := m.cfg.Connections[provider]; !taken {
			name = provider
		}
		return []field{
			textField("name", name, false).withHint(tr(tDomainHint)).withDetail(tr(tSetupSetupConnectionNameDetail)),
			targetsField(nil).withHint(m.providerTargetHint(provider)).withDetail(m.providerTargetDetail(provider)),
			textField("description", "", false).withHint(tr(tPayloadDescriptionHint)).withDetail(tr(tFormDescriptionDetail)),
		}
	case stepEffects:
		return append([]field{{label: profileLabel, kind: fieldChoice, hidden: true}},
			m.accessFields(provider, nil, nil)...)
	}
	return nil
}

// maskedField is the row that takes the secret of one role of a new keyring credential.
func (m *Model) maskedField(provider, role string, lead bool) field {
	f := textField(role, "", false)
	f.kind, f.roleLead = fieldMasked, lead
	f.input.EchoMode = textinput.EchoPassword
	f.hint = m.roleHintText(provider, role, tSetupMaskedHint, lead, false)
	f.detail = m.roleDetail(provider, role, tSetupMaskedHint, lead)
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
		m.fields[0].hint, m.fields[0].detail = tr(tSetupSetupServiceHint), tr(tSetupSetupServiceDetail)
		if existing {
			m.fields[0].hint = tr(tSetupReusesUnchanged, chosen)
			m.fields[0].detail = tr(tSetupReusesAtUnchangedChooseTo,
				chosen, m.cfg.Services[chosen].BaseURL, valueText("service", newService))
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
		m.fields[0].hint, m.fields[0].detail = tr(tSetupSetupCredentialHint), tr(tSetupSetupCredentialDetail)
		if existing {
			m.fields[0].hint = tr(tSetupReusesUnchanged, chosen)
			m.fields[0].detail = tr(tSetupReusesUnchangedAndItsSecrets,
				chosen, valueText(storageLabel, m.storagePlace(chosen, m.cfg.Credentials[chosen])), valueText("credential", newCredential))
		}
	case stepEffects:
		m.accessModeChosen()
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
			w.provider, w.pages, w.template = provider, w.pages[:1], ""
		}
	} else {
		built, plan, err := m.setupCandidate(w.step)
		if err != nil {
			m.problems = problemState{}
			m.reportSaveError(err, "", true)
			return
		}
		w.built, w.candidate, w.plan, w.base = built, built.Config, plan, m.rev
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

// setupEsc is esc in the setup: one step back, and on the first step the way out. That closes a saved setup
// and cancels one that is not saved yet, asking first once a provider was chosen. Dropping the setup drops
// every typed secret with it; nothing reached the file or a store.
func (m *Model) setupEsc() tea.Cmd {
	if w := m.wizard; w.step != stepProvider && !w.saving && w.saved == "" {
		m.setupBack()
		return nil
	}
	return m.requestLeave()
}

// finishSetup ends a saved setup on the Connections list, with the new connection selected.
func (m *Model) finishSetup() tea.Cmd {
	name := m.wizard.saved
	m.wizard, m.fields = nil, nil
	cmd := m.openSection(sectionConnections)
	m.selectEntry(name)
	m.status = tr(tSetupSaved, name)
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
func (m *Model) setupCandidate(upto int) (manage.ConnectionCandidate, setupPlan, error) {
	draft, stage, plan := m.setupDraft(upto)
	built, err := manage.BuildConnectionCandidate(m.cfg, draft, stage)
	if err == nil {
		err = built.MissingSecret(plan.secrets)
	}
	if err != nil {
		return manage.ConnectionCandidate{}, plan, setupProblem(err)
	}
	return built, plan, nil
}

// setupDraft is what the steps up to upto decided, as the draft the core builds its candidate from and
// reads targets for, with the stage it reaches and the plan beside it.
func (m *Model) setupDraft(upto int) (manage.ConnectionDraft, manage.ConnectionStage, setupPlan) {
	w := m.wizard
	var plan setupPlan
	draft := manage.ConnectionDraft{Provider: w.provider}
	stage := manage.StageService

	page := w.pages[stepService]
	plan.service = pageValue(page, "service")
	if plan.service == newService {
		plan.service, plan.newService = pageValue(page, "name"), true
		draft.NewService, draft.BaseURL = true, pageValue(page, "base url")
	}
	draft.Service = plan.service

	if upto >= stepCredential {
		stage = manage.StageCredential
		page := w.pages[stepCredential]
		plan.credential = pageValue(page, "credential")
		if plan.credential == newCredential {
			plan.credential, plan.newCredential = pageValue(page, "name"), true
			plan.storage = pageValue(page, storageLabel)
			draft.Storage = storageType(plan.storage)
			if draft.Storage == config.CredentialTypeEnv {
				draft.EnvNames = map[string]string{}
			} else {
				plan.secrets = map[string]string{}
			}
			// The first empty row of a required role is held back, so the core finds its role missing; any
			// later one is handed on as typed. An empty optional row is left out, of the summary too: nothing is
			// missing there.
			held := false
			for _, f := range page {
				if f.hidden || (f.kind != fieldEnvName && f.kind != fieldMasked) {
					continue
				}
				value := f.value()
				if value == "" && m.cfg.SecretRoleOptional(w.provider, f.label) {
					continue
				}
				plan.roles = append(plan.roles, f.label)
				if value == "" && !held {
					held = true
					continue
				}
				if f.kind == fieldEnvName {
					draft.EnvNames[f.label] = value
				} else {
					plan.secrets[f.label] = value
				}
			}
		}
		draft.NewCredential, draft.Credential = plan.newCredential, plan.credential
	}

	if upto >= stepScope {
		stage = manage.StageScope
		page := w.pages[stepScope]
		plan.connection = pageValue(page, "name")
		draft.Name, draft.Targets, draft.Description = plan.connection, targetEntries(page), pageValue(page, "description")
		if upto >= stepEffects {
			stage = manage.StageRights
			page := w.pages[stepEffects]
			if pageValue(page, accessLabel) != accessTools {
				draft.Effects = pageValue(page, effectsLabel)
			} else {
				draft.ToolsSelected = true
				for _, f := range page {
					if f.kind == fieldToolList {
						draft.Tools = f.marked()
					}
				}
			}
		}
		// Labels the target row holds come from the provider's list; a label the template carries for the
		// same target stays. Only targets that were chosen keep a label.
		for target, label := range targetLabelsOf(page) {
			if slices.Contains(draft.Targets, target) {
				if draft.TargetLabels == nil {
					draft.TargetLabels = map[string]string{}
				}
				draft.TargetLabels[target] = label
			}
		}
		if w.template != "" {
			var templateLabels map[string]string
			draft.Paths, draft.Files, templateLabels = m.setupTemplateExtras()
			if draft.TargetLabels == nil {
				draft.TargetLabels = map[string]string{}
			}
			maps.Copy(draft.TargetLabels, templateLabels)
			if len(draft.TargetLabels) == 0 {
				draft.TargetLabels = nil
			}
		}
	}

	return draft, stage, plan
}

// setupProblem words a problem of the core in the terms of the setup's rows.
func setupProblem(err error) error {
	var taken *manage.NameTakenError
	var missing *manage.RoleMissingError
	switch {
	case errors.As(err, &taken) && taken.Kind != manage.KindConnection:
		return errors.New(tr(tSetupANamedAlreadyExistsChoose,
			labelText(string(taken.Kind)), taken.Name, labelText(string(taken.Kind))))
	case errors.As(err, &missing):
		return errors.New(tr(tSetupIsEmptyTypeItsSecret, missing.Role, valueText(storageLabel, storageEnv)))
	}
	return err
}

// updateSummary handles the last step: saving, and once saved, testing.
func (m *Model) updateSummary(key tea.KeyMsg) tea.Cmd {
	w := m.wizard
	if w.saving {
		return nil
	}
	if w.saved != "" {
		switch key.String() {
		case "t":
			return m.testConnection(w.saved)
		case keyOf(actBack):
			if m.testing {
				m.stopTest()
				m.status = tr(tSetupConnectionTestCancelled)
				return nil
			}
			return m.finishSetup()
		}
		return nil
	}
	switch key.String() {
	case keyOf(actBack):
		return m.setupEsc()
	case keyOf(actSave):
		return m.startSetupSave()
	}
	return nil
}

// startSetupSave saves the setup. Committing it is a managing action: requireAdmin gates the
// whole save first, the same way submit does for an ordinary form, before finishSetupSave offers a
// passphrase when the new credential's first secret would create the vault; nothing before this point ever
// touched it, and that offer, answered or cancelled, is the only thing that decides whether it does.
func (m *Model) startSetupSave() tea.Cmd {
	return m.requireAdmin(m.finishSetupSave)
}

func (m *Model) finishSetupSave() tea.Cmd {
	// Captured now, once requireAdmin has run: a locked vault is unlocked by then, so this sees it as it
	// really is instead of failing closed on a state that no longer holds; m.cfg still holds what was
	// loaded, exactly what this function's own candidate is built from (see setupCandidate).
	m.wizard.before = manage.SnapshotApprovals(m.secrets.Vault(), m.cfg)
	plan := m.wizard.plan
	if !plan.newCredential || storageType(plan.storage) != config.CredentialTypeVault {
		return m.saveSetup(nil)
	}
	if offerNeeded, err := manage.NeedsPassphraseOffer(m.secrets.Vault()); err != nil || !offerNeeded {
		return m.saveSetup(nil)
	}
	m.openVaultOffer(
		tr(tVaultOfferTitle),
		vaultFirstSecretHint(),
		true, true,
		func(offer vault.PassphraseFunc) tea.Cmd { return m.saveSetup(offer) },
		func() tea.Cmd { m.screen = screenSummary; m.status = tr(tCancelled); return nil },
	)
	return nil
}

// saveSetup writes what the steps decided. The store may take its time, so the write runs as a command.
// offer is asked for a passphrase only when the new credential's secret would be the vault's very first.
func (m *Model) saveSetup(offer vault.PassphraseFunc) tea.Cmd {
	w := m.wizard
	candidate, built, plan, base := w.candidate, w.built, w.plan, w.base
	w.saving = true
	m.screen = screenSummary
	m.clearMessages()
	m.busy = tr(tPayloadSaving, plan.connection)
	svc, previous := m.svc, m.cfg
	return func() tea.Msg {
		res, err := svc.SaveConnectionSetup(previous, built, base, plan.secrets, offer)
		warning := res.Warning
		if err == nil && res.Logged != "" {
			if warning != "" {
				warning += "; "
			}
			warning += res.Logged
		}
		return setupSavedMsg{cfg: candidate, name: plan.connection, err: err, warning: warning}
	}
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
		if m.conflicted(msg.err) {
			m.setupRebase()
			return nil
		}
		m.problems = problemState{}
		if advice := m.setupStoreAdvice(msg.err); advice != "" {
			m.status, m.fail = "", m.redactor.Apply(advice)
			return nil
		}
		m.reportSaveError(msg.err, "", true)
		return nil
	}
	m.adoptSaved(msg.cfg)
	w.saved = msg.name
	// The secrets are where they belong now, and the editor drops its only copy of them.
	w.plan.secrets, w.pages, m.fields = nil, nil, nil
	m.status = tr(tSetupSaved, msg.name)
	if msg.warning != "" {
		m.status += "; " + msg.warning
	}
	// The new connection is always approved (see autoApprove); any other connection the same save newly
	// opened, such as one sharing the service or credential just created, is approved alongside it.
	sweep := m.autoApprove(w.before, msg.name)
	if m.tester != nil {
		m.status += tr(tSetupPressTToTestIt)
	}
	// Where the secrets of a new credential now resolve from is what the lists and the next setup name.
	return tea.Batch(sweep, m.refreshSources(m.keyringQueries()))
}

// setupRebase rebuilds the setup's candidate on the configuration reloaded after a conflict, so the next
// save commits what was decided against what the file holds now. If the steps no longer fit that file, the
// reason is shown and the steps can be changed; the next save is a conflict again until they are.
func (m *Model) setupRebase() {
	w := m.wizard
	built, plan, err := m.setupCandidate(stepEffects)
	if err != nil {
		conflict := m.fail
		m.problems = problemState{}
		m.reportSaveError(err, "", true)
		m.fail = conflict + tr(tSetupTheSetupNoLongerFits) + m.fail
		return
	}
	w.built, w.candidate, w.plan, w.base = built, built.Config, plan, m.rev
	m.fail += tr(tSetupTheSummaryShowsTheSetup)
}

// setupStoreAdvice turns a save that failed on the secret store into the way out, "" for any other error.
func (m *Model) setupStoreAdvice(err error) string {
	if errors.Is(err, secret.ErrUnavailable) || errors.Is(err, secret.ErrDisabled) {
		return tr(tSetupNothingWasSavedThenPress,
			err, secret.StoreAdvice(secret.StoreStateOf(err), platform), valueText(storageLabel, storageEnv))
	}
	return ""
}

func setupKeys(step int) string {
	if step == stepProvider {
		return "{save} next · {back} cancel setup"
	}
	return "{save} next · {back} back"
}

// setupHeading is the title of the current step and, with withLead, what it decides. The per-field form
// steps leave the lead out, in favor of the same single fixed hint every other form shows for its focused
// field; the summary has no fields to focus, so it keeps the lead as the step's only explanation.
func (m *Model) setupHeading(withLead bool) string {
	w := m.wizard
	title := tr(tSetupSetUpAConnectionStep, w.step+1, setupSteps, wordText(setupTitles[w.step]))
	lead := tr(tSetupSummaryLead)
	if w.saved != "" {
		title, lead = tr(tSetupSetUpAConnectionSaved), tr(tSetupSavedLead)
	}
	heading := m.wrapped(styles.title, title) + "\n"
	if withLead {
		heading += m.wrapped(styles.hint, lead) + "\n"
	}
	return heading + "\n"
}

// summaryView shows what the setup saves. It names where every secret goes, never what it is. Where the
// terminal has room the pairs of label and value stand aligned in a panel, beside the list of the steps on a
// wide one; otherwise they follow each other as plain rows.
func (m *Model) summaryView() string {
	w := m.wizard
	var b strings.Builder
	b.WriteString(m.setupHeading(true))
	pairs := m.summaryRows()
	steps, stepsWidth := "", 0
	if m.stepListFits(0) {
		steps, stepsWidth = m.stepList()
	}
	room := m.usable(0)
	if steps != "" {
		room -= stepsWidth + 1
	}
	if lines := summaryLines(pairs, max(room-4, 1)); room >= tableFrameMinWidth && len(lines)+2+4 <= m.height {
		box := strings.Join(panel(wordText(setupTitles[stepSummary]), lines, max(room-4, 1), len(lines), panelOpts{}), "\n")
		if steps != "" {
			box = lipgloss.JoinHorizontal(lipgloss.Top, steps, " ", box)
		}
		b.WriteString(box + "\n")
	} else {
		for _, pair := range pairs {
			b.WriteString(m.row(false, padLine(pair.label, summaryLabelWidth)+" "+pair.value) + "\n")
		}
	}
	if warning := w.candidate.IdleWarning(w.plan.connection); warning != "" {
		b.WriteString(m.indentedWith(styles.warning, warningLine(warning)) + "\n")
	}
	keys := "{save} save · {back} back · {command} cmds · {help} help"
	switch {
	case w.saving:
		keys = "ctrl+c quit · {command} cmds · {help} help"
	case w.saved != "" && m.testing:
		keys = "{back} cancel test · {command} cmds · {help} help"
	case w.saved != "":
		keys = "t test connection · {back} connections list · {command} cmds · {help} help"
	}
	if w.saved != "" {
		b.WriteString(m.testLine())
	}
	b.WriteString(m.keyHint(keys))
	return b.String()
}

// summaryLabelWidth is the width of the label column of the summary.
const summaryLabelWidth = 12

// summaryPair is one line of the summary: a label and what it says. A value may carry status marks.
type summaryPair struct{ label, value string }

// summaryLines draws the pairs aligned in width cells: the label dimmed in its column and the value beside it,
// continued under itself where it does not fit.
func summaryLines(pairs []summaryPair, width int) []string {
	var lines []string
	room := max(width-summaryLabelWidth-1, 1)
	for _, pair := range pairs {
		label := styles.hint.Render(padLine(truncateCells(pair.label, summaryLabelWidth), summaryLabelWidth))
		for i, line := range strings.Split(render(lipgloss.NewStyle(), room, pair.value), "\n") {
			if i > 0 {
				label = strings.Repeat(" ", summaryLabelWidth)
			}
			lines = append(lines, label+" "+line)
		}
	}
	return lines
}

// stepList is the numbered list of the steps of the guided setup, in the block the sections of a form use: the
// step being set up leads with ">" and the ones still to come are dimmed.
func (m *Model) stepList() (string, int) {
	names := make([]string, setupSteps)
	dim := make([]bool, setupSteps)
	for i := range names {
		names[i], dim[i] = wordText(setupTitles[i]), i > m.wizard.step
	}
	return numberedList(names, dim, m.wizard.step)
}

// stepListFits reports whether the steps stand as a list beside the rows, on a terminal wide and high enough
// for it and for extra more lines of other lists under it.
func (m *Model) stepListFits(extra int) bool {
	return m.wizard != nil && m.width >= sectionListMinWidth && m.height >= setupSteps+extra+8
}

func (m *Model) summaryRows() []summaryPair {
	w := m.wizard
	cfg, plan := w.candidate, w.plan
	metadata, _ := cfg.ProviderMetadata(w.provider)
	conn := cfg.Connections[plan.connection]
	state := func(added bool) string {
		if added {
			return statusMark(statusOK, tr(tSetupNew))
		}
		return statusMark(statusDim, tr(tSetupExistingUnchanged))
	}
	line := func(label, text string) summaryPair { return summaryPair{labelText(label), text} }

	secrets := valueText(storageLabel, m.storagePlace(plan.credential, cfg.Credentials[plan.credential])) +
		tr(tSetupItsSecretsStayWhereThey)
	roles := strings.Join(plan.roles, ", ")
	switch {
	case !plan.newCredential:
	case plan.storage == storageEnv:
		names := make([]string, len(plan.roles))
		for i, role := range plan.roles {
			names[i] = tr(tSetupFrom, role, cfg.Credentials[plan.credential].Values[role])
		}
		secrets = valueText(storageLabel, placeEnv) + ": " + strings.Join(names, ", ")
	default:
		// plan.storage is already exactly the place chosen: system keyring or vault.
		secrets = valueText(storageLabel, plan.storage) + ": " + roles
	}

	// Only the field the access mode stores is listed; the effects of selected tools follow from them.
	access := []summaryPair{line(accessLabel, valueText(accessLabel, accessEffects))}
	if conn.Tools == nil {
		effects := config.FormatEffects(conn.Effects)
		if conn.Effects == nil {
			effects = tr(tSetupDefault, config.FormatEffects(manage.DefaultEffects(metadata)))
		}
		access = append(access, line(effectsLabel, effects))
	} else {
		tools := tr(tSetupNoneNoToolIsOffered)
		if len(conn.Tools) > 0 {
			tools = strings.Join(conn.Tools, ", ")
		}
		access = []summaryPair{
			line(accessLabel, valueText(accessLabel, accessTools)),
			line(toolListLabel, tools),
			line(effectsLabel, config.FormatEffects(config.EffectsOfTools(metadata, conn.Tools))+" "+tr(tSetupFromTheTools)),
		}
	}

	return append([]summaryPair{
		line("provider", metadata.Name),
		line("service", fmt.Sprintf("%s %s · %s", plan.service, state(plan.newService), cfg.Services[plan.service].BaseURL)),
		line("credential", fmt.Sprintf("%s %s · %s", plan.credential, state(plan.newCredential), secrets)),
		line("connection", plan.connection),
		line(targetsLabel, choiceText(strings.Join(conn.TargetValues(), ", "))),
		line("description", choiceText(conn.Description)),
	}, access...)
}
