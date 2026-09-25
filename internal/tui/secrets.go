package tui

import (
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Secrets is what the editor needs from the credential resolver: it hands a secret in, it removes one, and
// it asks where a role resolves from. There is deliberately no operation that hands a stored secret back,
// not even masked, so the editor cannot show one even by mistake. *secret.Resolver satisfies it; a test
// injects its own, so no test and no headless run depends on the credential store of the machine.
type Secrets interface {
	Status(credential string, cred config.Credential, role string) (secret.Source, []string)
	Stored(credential, role string) secret.Placement
	Set(credential, role, value string) error
	SetPlaintext(credential, role, value string) error
	Delete(credential, role string) ([]secret.Source, error)
	Lookup(envName string) bool
	Plaintext() *secret.File
}

// ErrNoResolver reports an editor that was started without a credential resolver. The configuration stays
// fully editable then; only the operations that would reach a store say why they cannot.
var ErrNoResolver = errors.New("this editor was started without a credential resolver")

// noSecrets stands in when no resolver was passed, so every call site can stay free of nil checks.
type noSecrets struct{}

func (noSecrets) Status(string, config.Credential, string) (secret.Source, []string) {
	return secret.SourceMissing, []string{"no credential resolver"}
}
func (noSecrets) Set(string, string, string) error               { return ErrNoResolver }
func (noSecrets) SetPlaintext(string, string, string) error      { return ErrNoResolver }
func (noSecrets) Delete(string, string) ([]secret.Source, error) { return nil, ErrNoResolver }
func (noSecrets) Lookup(string) bool                             { return false }
func (noSecrets) Plaintext() *secret.File                        { return nil }

// Stored reports both places as unasked, because without a resolver neither can be asked. A caller that
// must not orphan a secret therefore stops, which is the right answer under total ignorance.
func (noSecrets) Stored(string, string) secret.Placement {
	return secret.Placement{
		Unknown: []secret.Source{secret.SourceStore, secret.SourcePlaintext},
		Err:     ErrNoResolver,
	}
}

// What a secret row says while the answer is not in yet, and before there is a credential to ask about.
const (
	sourcePending = "checking ..."
	sourceUnsaved = "save the credential first"
	sourceUnnamed = "no variable named"
)

// What the row of a keyring role says once the resolver has answered. The system keyring is the
// recommended place, so each state is said in its terms; the environment and the unencrypted file are
// named as what they are, an override and a fallback.
const (
	stateStored      = "in system keyring"
	stateOverride    = "environment variable, overrides keyring"
	statePlaintext   = "unencrypted file"
	stateEmpty       = "not stored yet"
	stateLocked      = "keyring locked"
	stateUnreachable = "keyring unreachable"
	stateOff         = "keyring switched off"
)

// Where the secrets of a credential are kept. The guided setup and the credential form offer the same row,
// in the same order and with the same hint, and say it in these words rather than as the type the file
// stores: the system keyring and the unencrypted file are both type keyring there, environment variables
// type env.
const (
	storageLabel = "secrets"

	placeKeyring   = "system keyring"
	placeEnv       = "environment variables"
	placePlaintext = "unencrypted file"

	storageKeyring   = placeKeyring + " (recommended)"
	storageEnv       = placeEnv
	storagePlaintext = placePlaintext + " (asks first)"
)

// storageHint says what each place for new secrets is for. The system keyring is named as this platform
// calls it, so a user recognises it as something the machine already has rather than something to set up.
var storageHint = "system keyring keeps the secrets in " + secret.StoreLabel(platform) + " of this " +
	"machine, with nothing to set up or export; environment variables suit CI and containers; unencrypted " +
	"file is the last resort without a keyring and asks first"

// storageField is the row that chooses where the secrets of a credential are kept.
func storageField(value string) field {
	return choiceField(storageLabel, []string{storageKeyring, storageEnv, storagePlaintext}, value).
		withHint(storageHint)
}

// storageType is the credential type the file stores for a choice of the storage row.
func storageType(choice string) string {
	if choice == storageEnv {
		return config.CredentialTypeEnv
	}
	return config.CredentialTypeKeyring
}

// storagePlace names where the secrets of a configured credential are kept. A keyring credential counts
// as kept in the unencrypted file once a role of it resolves from there, by the resolver's last answer; the
// question is never asked here, because that would block the editor on the store.
func (m *Model) storagePlace(name string, cred config.Credential) string {
	if cred.Type == config.CredentialTypeEnv {
		return placeEnv
	}
	for _, role := range m.credentialRoles(m.credentialProvider(name, cred)) {
		if m.sources[secret.StoreKey(name, role)] == secret.SourcePlaintext {
			return placePlaintext
		}
	}
	return placeKeyring
}

// storageChoice is the choice of the storage row that stands for a place.
func storageChoice(place string) string {
	switch place {
	case placeEnv:
		return storageEnv
	case placePlaintext:
		return storagePlaintext
	}
	return storageKeyring
}

// platform is the operating system whose keyring the texts name. The names themselves are checked for
// every platform in the secret package, without reaching any store.
var platform = runtime.GOOS

// credQuery is one credential the editor wants the resolved sources of.
type credQuery struct {
	name string
	cred config.Credential
}

// sourcesMsg carries resolved sources back into the event loop. It carries no value, because Status hands
// none out.
type sourcesMsg struct {
	id      int
	sources map[string]secret.Source
	checked map[string][]string
}

// placedMsg carries the answer to "what is stored where" back into the event loop. It travels on its own
// message and its own counter, so an ordinary display refresh cannot take the answer a waiting save needs.
type placedMsg struct {
	id         int
	credential string
	places     map[string]secret.Placement
}

// writtenMsg carries the outcome of one store write or delete back into the event loop. It carries no
// generation: every write has its own effect, so every outcome has to reach the user, even when a later
// write finished first.
type writtenMsg struct {
	credential string
	role       string
	done       string
	err        error
}

// refreshSources asks where the secrets of the given credentials resolve from.
//
// Every question may reach the platform store, which is allowed to take up to ten seconds, so the
// questions are asked in a command and the event loop keeps handling keys meanwhile. A later job wins: the
// result of an abandoned one is dropped through the same generation counter the connection test uses.
func (m *Model) refreshSources(queries []credQuery) tea.Cmd {
	if len(queries) == 0 {
		return nil
	}

	m.probeID++
	id := m.probeID
	m.probing = "checking where the secrets resolve from"

	secrets, roles := m.secrets, m.cfg.SecretRoles()
	return func() tea.Msg {
		msg := sourcesMsg{
			id:      id,
			sources: map[string]secret.Source{}, checked: map[string][]string{},
		}
		for _, q := range queries {
			for _, role := range roles {
				source, checked := secrets.Status(q.name, q.cred, role)
				key := secret.StoreKey(q.name, role)
				msg.sources[key] = source
				msg.checked[key] = checked
			}
		}
		return msg
	}
}

// keyringQueries lists the configured credentials whose secrets live in a store. An env credential is
// answered by the environment alone, so it needs no command and no store contact.
func (m *Model) keyringQueries() []credQuery {
	var queries []credQuery
	for _, name := range m.entryNames(sectionCredentials) {
		if cred := m.cfg.Credentials[name]; cred.Type == config.CredentialTypeKeyring {
			queries = append(queries, credQuery{name: name, cred: cred})
		}
	}
	return queries
}

// editedQuery asks about the credential the form is on, under the type the form currently shows. That is
// what makes an existing entry visible right after the type was switched to keyring, before it is saved.
func (m *Model) editedQuery() []credQuery {
	if m.editing == "" {
		return nil
	}
	return []credQuery{{name: m.editing, cred: config.Credential{Type: config.CredentialTypeKeyring}}}
}

func (m *Model) handleSources(msg sourcesMsg) tea.Cmd {
	if msg.id != m.probeID {
		return nil
	}
	m.probing = ""
	for key, source := range msg.sources {
		m.sources[key] = source
		m.checked[key] = msg.checked[key]
	}
	return nil
}

// handleWritten applies the outcome of one write or delete.
//
// Nothing is dropped here. A generation counter is right for a question, where only the newest answer
// matters, and wrong for a write, where each one changed something and each outcome belongs to the user: a
// second write must not report success over the failure of the first. A failure therefore stays on screen
// even when a later write succeeded, and two failures are shown together.
func (m *Model) handleWritten(msg writtenMsg) tea.Cmd {
	if m.writes > 0 {
		m.writes--
	}
	if m.writes == 0 {
		m.busy = ""
	}

	if msg.err != nil {
		text := m.redactor.Apply(m.explain(msg.err, msg.credential, msg.role))
		if m.fail == "" {
			m.fail = text
		} else {
			m.fail += "; " + text
		}
	} else if m.fail == "" {
		m.status = msg.done
	}
	// The rows are refreshed after a failure too: a delete that only cleared one place, or a write that
	// went nowhere, is exactly when the shown source must no longer be the one from before.
	return m.refreshSources([]credQuery{{
		name: msg.credential, cred: config.Credential{Type: config.CredentialTypeKeyring},
	}})
}

// askSecret opens the masked prompt for one role. The typed value lives in this one input and nowhere else
// in the model, it is drawn masked, and it is dropped the moment the prompt closes.
func (m *Model) askSecret(role string, plaintext bool) {
	in := textinput.New()
	in.Prompt = ""
	in.EchoMode = textinput.EchoPassword
	in.Cursor.SetMode(cursor.CursorStatic)
	in.Focus()

	m.secretInput = in
	m.secretRole = role
	m.secretPlain = plaintext
	m.screen = screenSecret
	m.clearMessages()
}

// confirmPlaintext separates curiosity about the unencrypted file from consent to write an unencrypted secret. The
// confirmation itself writes nothing and does not even ask for the value.
func (m *Model) confirmPlaintext(role string) {
	m.secretRole = role
	m.screen = screenPlaintextConfirm
	m.clearMessages()
}

func (m *Model) updatePlaintextConfirm(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "y":
		if m.wizard != nil {
			// The guided setup asks as its credential step is left and writes the secrets only on save.
			m.setupShow(stepCredential + 1)
			return nil
		}
		m.askSecret(m.secretRole, true)
	case "n", "esc":
		m.screen = screenForm
		m.status = "Cancelled; nothing was written"
	case "ctrl+c":
		return m.quit()
	}
	return nil
}

// updateSecret handles the masked prompt.
func (m *Model) updateSecret(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "ctrl+c":
		m.secretInput.Reset()
		return m.quit()
	case "esc":
		m.secretInput.Reset()
		m.screen = screenForm
		m.status = "Cancelled"
		return nil
	case "enter":
		value := m.secretInput.Value()
		// The value leaves the model in the same event that submitted it: from here on it exists only
		// inside the command that hands it to the resolver.
		m.secretInput.Reset()
		m.screen = screenForm
		if value == "" {
			m.fail = "nothing was typed, so nothing was stored"
			return nil
		}
		return m.storeSecret(m.editing, m.secretRole, value, m.secretPlain)
	}

	var cmd tea.Cmd
	m.secretInput, cmd = m.secretInput.Update(key)
	return cmd
}

// storeSecret hands one secret to the resolver. The write may reach the platform store, so it runs as a
// command and the editor stays usable while it does.
func (m *Model) storeSecret(credential, role, value string, plaintext bool) tea.Cmd {
	where := "system keyring"
	if plaintext {
		where = placePlaintext
	}
	m.writes++
	m.busy = fmt.Sprintf("storing the secret for %s.%s in the %s", credential, role, where)

	secrets := m.secrets
	return func() tea.Msg {
		msg := writtenMsg{
			credential: credential, role: role,
			done: fmt.Sprintf("Stored %s.%s in the %s", credential, role, where),
		}
		if plaintext {
			msg.err = secrets.SetPlaintext(credential, role, value)
		} else {
			msg.err = secrets.Set(credential, role, value)
		}
		return msg
	}
}

// removeSecret clears one stored secret from every place the resolver keeps one.
func (m *Model) removeSecret(credential, role string) tea.Cmd {
	m.writes++
	m.busy = fmt.Sprintf("removing the stored secret for %s.%s", credential, role)

	secrets := m.secrets
	return func() tea.Msg {
		msg := writtenMsg{credential: credential, role: role}
		cleared, err := secrets.Delete(credential, role)
		msg.err = err
		if err == nil {
			msg.done = fmt.Sprintf("Removed %s.%s from the %s", credential, role, joinSources(cleared))
		}
		return msg
	}
}

func joinSources(sources []secret.Source) string {
	names := make([]string, len(sources))
	for i, s := range sources {
		names[i] = string(s)
	}
	return strings.Join(names, " and the ")
}

// explain turns a store failure into the way out.
//
// A machine without a running secret service must not be a dead end in the editor either. The message says
// what the keyring's state means on this platform and how to fix it, names the derived variable as the
// other way, and names the plaintext file only as a deliberate last resort behind its own confirmation.
// It is built from the class of the failure and never carries a value.
func (m *Model) explain(err error, credential, role string) string {
	if errors.Is(err, secret.ErrNoEntry) {
		return fmt.Sprintf("no stored secret for %s.%s", credential, role)
	}
	if !errors.Is(err, secret.ErrUnavailable) && !errors.Is(err, secret.ErrDisabled) {
		return err.Error()
	}

	state := secret.StoreStateOf(err)
	text := secret.StoreAdvice(state, platform)
	retry := "s"
	var remaining *secret.RemainingError
	if errors.As(err, &remaining) {
		// A delete that could not clear the keyring says first what may still be stored.
		text = err.Error() + "; " + text
		retry = "x"
	}
	if state != secret.StoreOff {
		text += ", then retry " + retry
	}
	return fmt.Sprintf("%s. Alternatively export %s. Only if you deliberately accept an unencrypted file, "+
		"choose %s in the %s row and press s; it asks again before writing %s", text,
		secret.DerivedEnvName(credential, role), storagePlaintext, storageLabel, m.plaintextPath())
}

func (m *Model) plaintextPath() string {
	if f := m.secrets.Plaintext(); f != nil {
		return f.Path()
	}
	return secret.FileName
}

// guardTypeChange refuses to turn a keyring credential into an env credential behind the user's back while
// a secret of it lies in a store or in the plaintext file.
//
// The configuration would keep validating, the entries would stay where they are, and nothing would read
// them again: the credential resolves from the variables it names from then on. The question is therefore
// not what delivers right now — a set variable, a switched-off store or an unreadable file would all make
// that look empty — but what lies somewhere. Stored asks exactly that, place by place.
//
// The answer travels on its own message and its own counter, because a save that waits must not lose its
// answer to an ordinary display refresh: an enter that neither saves nor complains is the worst outcome of
// all.
func (m *Model) guardTypeChange() tea.Cmd {
	if m.section != sectionCredentials || m.editing == "" {
		return nil
	}
	if m.cfg.Credentials[m.editing].Type != config.CredentialTypeKeyring {
		return nil
	}
	if m.credentialType() != config.CredentialTypeEnv {
		return nil
	}

	m.guardID++
	id := m.guardID
	m.probing = "checking what is stored for " + m.editing

	secrets, credential, roles := m.secrets, m.editing, m.cfg.SecretRoles()
	return func() tea.Msg {
		msg := placedMsg{id: id, credential: credential, places: map[string]secret.Placement{}}
		for _, role := range roles {
			msg.places[role] = secrets.Stored(credential, role)
		}
		return msg
	}
}

// handlePlaced decides the save that was waiting for that answer.
func (m *Model) handlePlaced(msg placedMsg) tea.Cmd {
	if msg.id != m.guardID {
		return nil
	}
	m.probing = ""
	// The user may have moved on, or left, while the places were being asked. Nothing is saved behind their
	// back, and after ctrl+c nothing is written at all.
	if m.quitting || m.screen != screenForm || m.section != sectionCredentials ||
		m.editing != msg.credential {
		return nil
	}

	var held, unsure, causes []string
	seen := map[string]bool{}
	for _, role := range m.cfg.SecretRoles() {
		place := msg.places[role]
		for _, source := range place.Holding {
			held = append(held, fmt.Sprintf("%s: %s", role, source))
		}
		for _, source := range place.Unknown {
			unsure = append(unsure, fmt.Sprintf("%s: %s", role, source))
		}
		// Both roles usually fail for the same reason; saying it twice would only make the message longer.
		for _, cause := range strings.Split(errorText(place.Err), "\n") {
			if cause != "" && !seen[cause] {
				seen[cause] = true
				causes = append(causes, cause)
			}
		}
	}

	if len(held) == 0 && len(unsure) == 0 {
		return m.save(msg.credential)
	}

	// Both halves are said at once when both apply. Reporting only what was found would send the user to
	// clear that, try again, and only then learn that another place could not be asked at all.
	var problems, ways []string
	if len(held) > 0 {
		problems = append(problems,
			fmt.Sprintf("a secret of %s is still stored (%s)", msg.credential, strings.Join(held, ", ")))
		ways = append(ways, fmt.Sprintf("switch %s back to %s and remove it with x on the role, or "+
			"run 'qatlas credential delete %s <role>'", storageLabel, storageKeyring, msg.credential))
	}
	if len(unsure) > 0 {
		// Not knowing is not the same as nothing being there, and only one of the two is safe to act on.
		problems = append(problems, fmt.Sprintf("cannot tell whether a secret of %s is stored where it "+
			"could not be asked (%s): %s", msg.credential, strings.Join(unsure, ", "),
			strings.Join(causes, "; ")))
		ways = append(ways, "make that place answerable and try again")
	}

	m.fail = m.redactor.Apply(fmt.Sprintf("%s; the secrets cannot move to %s until that is settled, because "+
		"a copy left behind would have nothing that reads it: %s",
		strings.Join(problems, "; "), placeEnv, strings.Join(ways, "; ")))
	return nil
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// envSource reports whether the named variable carries something. For a credential of type env this is the
// whole answer: its resolution ends after the variable it names, so no store is asked and nothing blocks.
func (m *Model) envSource(name string) string {
	switch {
	case name == "":
		return sourceUnnamed
	case m.secrets.Lookup(name):
		return string(secret.SourceEnv)
	default:
		return string(secret.SourceMissing)
	}
}

// storedSource reports where a role of a keyring credential resolves from, out of the last answer the
// resolver gave. It never asks synchronously: that would block the whole editor on the store.
//
// A missing secret is told apart by what the keyring said, because each case has a different way out: an
// empty keyring wants the secret, a locked one wants unlocking, and a switched-off one wants the switch.
func (m *Model) storedSource(credential, role string) string {
	if credential == "" {
		return sourceUnsaved
	}
	key := secret.StoreKey(credential, role)
	source, ok := m.sources[key]
	if !ok {
		return sourcePending
	}
	switch source {
	case secret.SourceStore:
		return stateStored
	case secret.SourceEnv:
		return stateOverride
	case secret.SourcePlaintext:
		return statePlaintext
	}
	switch secret.StoreStage(m.checked[key]) {
	case secret.StoreEmpty:
		return stateEmpty
	case secret.StoreLocked:
		return stateLocked
	case secret.StoreUnavailable, secret.StoreTimedOut:
		return stateUnreachable
	case secret.StoreOff:
		return stateOff
	}
	return string(source)
}

// secretState is the short form of where a secret role resolves from: a mark when the secret is where the
// credential keeps it, and otherwise the state that needs attention.
func secretState(source string) string {
	switch source {
	case stateStored, statePlaintext, string(secret.SourceEnv):
		return "✓"
	case stateEmpty, string(secret.SourceMissing):
		return "missing"
	case stateOverride:
		return "env override"
	}
	return source
}

// secretNextStep names the one thing to do about a keyring role in its current state, or nothing when the
// secret is where it should be. It is built from the state alone and names no value.
func (m *Model) secretNextStep(credential, role string) string {
	key := secret.StoreKey(credential, role)
	source, ok := m.sources[key]
	if !ok {
		return ""
	}
	env := secret.DerivedEnvName(credential, role)
	if source == secret.SourceMissing && m.fieldValue(storageLabel) == storagePlaintext {
		return "next: press s to store it in the " + placePlaintext + "; it asks first"
	}
	switch source {
	case secret.SourceEnv:
		// The override stays visible: it wins over the keyring for as long as it is set.
		return fmt.Sprintf("%s is set and wins over the system keyring; unset it to use the keyring", env)
	case secret.SourceMissing:
	default:
		return ""
	}
	switch state := secret.StoreStage(m.checked[key]); state {
	case secret.StoreEmpty:
		return "next: press s to store it in " + secret.StoreLabel(platform) + " (recommended)"
	case secret.StoreLocked, secret.StoreUnavailable, secret.StoreTimedOut:
		return fmt.Sprintf("next: %s, then press s; or export %s", secret.StoreAdvice(state, platform), env)
	case secret.StoreOff:
		return fmt.Sprintf("next: %s; or export %s", secret.StoreAdvice(state, platform), env)
	}
	return ""
}

// secretRowHint says what the keys on a secret row do, and, once the resolver has answered, which stages
// were checked. The stages are the actionable part of a missing secret, and they name no value.
//
// The keys are the same on every role row, so they are said once, on the first one, like the sentence an
// env credential carries there; the stages differ per role, so every row keeps its own. The key line at the
// foot of the form repeats the keys for whichever row the focus is on.
func (m *Model) secretRowHint(credential, role string, lead bool) string {
	var parts []string
	if description := m.cfg.SecretRoleDescription(role); description != "" {
		parts = append(parts, description)
	}
	if lead {
		parts = append(parts, m.secretKeys()+"; typing is masked")
	}
	if checked := m.checked[secret.StoreKey(credential, role)]; len(checked) > 0 {
		parts = append(parts, "checked: "+strings.Join(checked, ", "))
	}
	if next := m.secretNextStep(credential, role); next != "" {
		parts = append(parts, next)
	}
	return strings.Join(parts, "; ")
}

// secretKeys names the keys of a secret row. s stores where the storage row says, so the unencrypted file
// is reached by choosing it there, the same way the guided setup reaches it, and still asks first.
func (m *Model) secretKeys() string {
	if m.fieldValue(storageLabel) == storagePlaintext {
		return "s store in " + storagePlaintext + " · x remove"
	}
	return "s store in " + storageKeyring + " · x remove"
}

// secretRowKey handles the keys of a focused secret row.
func (m *Model) secretRowKey(role string, key tea.KeyMsg) tea.Cmd {
	action := key.String()
	if action != "s" && action != "x" {
		return nil
	}
	if m.editing == "" {
		// A secret stored under a name that was never saved would sit in the store with nothing pointing
		// at it, which is the orphaning this editor exists to avoid.
		m.fail = "save the credential first, then store its secrets: a credential kept in the " +
			placeKeyring + " or an " + placePlaintext + " saves without any value, and its secrets are added " +
			"afterwards"
		return nil
	}

	switch action {
	case "s":
		if m.fieldValue(storageLabel) == storagePlaintext {
			m.confirmPlaintext(role)
			return nil
		}
		m.askSecret(role, false)
	case "x":
		// Removing a stored secret is irreversible, so it is confirmed like every other deletion here.
		m.confirmRole = role
		m.screen = screenConfirm
		m.clearMessages()
	}
	return nil
}
