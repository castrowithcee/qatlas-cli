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
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// Secrets is what the editor needs from the credential resolver: it hands a secret in, it removes one, and
// it asks where a role resolves from. There is deliberately no operation that hands a stored secret back,
// not even masked, so the editor cannot show one even by mistake. *secret.Resolver satisfies it; a test
// injects its own, so no test and no headless run depends on the credential store of the machine.
type Secrets interface {
	Status(credential string, cred config.Credential, role string) (secret.Source, []string)
	Stored(credential, role string) secret.Placement
	Set(credential, role, value string) error
	Delete(credential, role string) ([]secret.Source, error)
	Lookup(envName string) bool
	// Vault returns the vault a vault credential's secrets are kept in, or nil where none is configured for
	// this run. State and Status on it are answered synchronously wherever this editor calls them: both are
	// a handful of local file checks, never a round trip to a platform service the way the system keyring is.
	Vault() *vault.Vault
	// SetVault stores one secret in the vault, creating it when this is the vault's very first secret. offer
	// is asked for a passphrase only then, exactly as vault.Vault.Set defines it.
	SetVault(credential, role, value string, offer vault.PassphraseFunc) error
	// DeleteVault removes one secret from the vault.
	DeleteVault(credential, role string) error
}

// ErrNoResolver reports an editor that was started without a credential resolver. The configuration stays
// fully editable then; only the operations that would reach a store say why they cannot.
var ErrNoResolver = errors.New("this editor was started without a credential resolver")

// noSecrets stands in when no resolver was passed, so every call site can stay free of nil checks.
type noSecrets struct{}

func (noSecrets) Status(string, config.Credential, string) (secret.Source, []string) {
	return secret.SourceMissing, []string{"no credential resolver"}
}
func (noSecrets) Set(string, string, string) error                            { return ErrNoResolver }
func (noSecrets) Delete(string, string) ([]secret.Source, error)              { return nil, ErrNoResolver }
func (noSecrets) Lookup(string) bool                                          { return false }
func (noSecrets) Vault() *vault.Vault                                         { return nil }
func (noSecrets) SetVault(string, string, string, vault.PassphraseFunc) error { return ErrNoResolver }
func (noSecrets) DeleteVault(string, string) error                            { return ErrNoResolver }

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

// What the row of a keyring role says once the resolver has answered. The environment is named as what it
// is, an override. unencrypted file is shown only for a role that still resolves from a plaintext
// credentials.yaml left over from an earlier version: this editor offers no way to write one any more, so
// the state is read only, a nudge towards 'qatlas vault migrate' rather than a place a role can be sent to.
const (
	stateStored      = "in system keyring"
	stateStoredVault = "in the vault"
	stateVaultLocked = "vault locked"
	stateOverride    = "environment variable, overrides keyring"
	statePlaintext   = "unencrypted file"
	stateEmpty       = "not stored yet"
	stateLocked      = "keyring locked"
	stateUnreachable = "keyring unreachable"
	stateOff         = "keyring switched off"
)

// Where the secrets of a credential are kept. The guided setup and the credential form offer the same row,
// in the same order and with the same hint, and say it in these words rather than as the type the file
// stores: system keyring, vault, and environment variables are the three places a new secret can be sent
// to, whichever the credential is now. None is called out as the recommendation; defaults.secret_store
// decides only which one is preselected for a new credential.
const (
	storageLabel = "secrets"

	placeKeyring   = "system keyring"
	placeEnv       = "environment variables"
	placePlaintext = "unencrypted file"
	placeVault     = "vault"

	// storageKeyring, storageVault, and storageEnv are the choices of the storage row: exactly the place
	// names above, kept under these names because they are what the row's tests and helpers refer to.
	storageKeyring = placeKeyring
	storageVault   = placeVault
	storageEnv     = placeEnv
)

// storageHint says what each place for new secrets is for. The system keyring is named as this platform
// calls it, so a user recognises it as something the machine already has rather than something to set up.
var storageHint = "system keyring keeps the secrets in " + secret.StoreLabel(platform) + " of this " +
	"machine, with nothing to set up or export; vault keeps them in a file beside the configuration, " +
	"unencrypted unless a passphrase is set for it; environment variables suit CI and containers"

// storageField is the row that chooses where the secrets of a credential are kept: the system keyring, the
// vault, or environment variables. Every credential offers the same three choices in the same order,
// whichever it is now, so moving a secret from one place to another is one change to this row.
func storageField(value string) field {
	return choiceField(storageLabel, []string{storageKeyring, storageVault, storageEnv}, value).withHint(storageHint)
}

// storageType is the credential type the file stores for a choice of the storage row.
func storageType(choice string) string {
	switch choice {
	case storageEnv:
		return config.CredentialTypeEnv
	case storageVault:
		return config.CredentialTypeVault
	}
	return config.CredentialTypeKeyring
}

// storagePlace names where the secrets of a configured credential are kept. A keyring credential counts
// as kept in the unencrypted file once a role of it resolves from there, by the resolver's last answer; the
// question is never asked here, because that would block the editor on the store.
func (m *Model) storagePlace(name string, cred config.Credential) string {
	switch cred.Type {
	case config.CredentialTypeEnv:
		return placeEnv
	case config.CredentialTypeVault:
		return placeVault
	}
	for _, role := range m.credentialRoles(m.credentialProvider(name, cred)) {
		if m.sources[secret.StoreKey(name, role)] == secret.SourcePlaintext {
			return placePlaintext
		}
	}
	return placeKeyring
}

// storageChoice is the choice of the storage row that stands for a place. A role that currently resolves
// from a leftover plaintext credentials.yaml still shows as system keyring here: that file's entries belong
// to a keyring credential, and writing a new secret for it goes to the keyring, the only such place the
// row still offers.
func storageChoice(place string) string {
	if place == placePlaintext {
		return storageKeyring
	}
	return place
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
// write finished first. vault marks an outcome that came from the vault, the only kind that clears
// vaultBusy: a keyring outcome that happened to land while a vault write of another credential is still in
// flight must not release a guard that is not its own.
type writtenMsg struct {
	credential string
	role       string
	done       string
	err        error
	vault      bool
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
	if msg.vault {
		m.vaultBusy = false
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
	if msg.vault {
		// A vault role is never asked about here: its state comes from vaultRoleState, a synchronous local
		// file check, never from this asynchronous keyring refresh.
		return nil
	}
	// The rows are refreshed after a failure too: a delete that only cleared one place, or a write that
	// went nowhere, is exactly when the shown source must no longer be the one from before.
	return m.refreshSources([]credQuery{{
		name: msg.credential, cred: config.Credential{Type: config.CredentialTypeKeyring},
	}})
}

// askSecret opens the masked prompt for one role. The typed value lives in this one input and nowhere else
// in the model, it is drawn masked, and it is dropped the moment the prompt closes.
func (m *Model) askSecret(role string) {
	in := textinput.New()
	in.Prompt = ""
	in.EchoMode = textinput.EchoPassword
	in.Cursor.SetMode(cursor.CursorStatic)
	in.Focus()

	m.secretInput = in
	m.secretRole = role
	m.screen = screenSecret
	m.clearMessages()
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
		if m.credentialType() == config.CredentialTypeVault {
			return m.beginVaultSecret(m.editing, m.secretRole, value)
		}
		return m.storeSecret(m.editing, m.secretRole, value)
	}

	var cmd tea.Cmd
	m.secretInput, cmd = m.secretInput.Update(key)
	return cmd
}

// storeSecret hands one secret to the resolver. The write may reach the platform store, so it runs as a
// command and the editor stays usable while it does.
func (m *Model) storeSecret(credential, role, value string) tea.Cmd {
	where := "system keyring"
	m.writes++
	m.busy = fmt.Sprintf("storing the secret for %s.%s in the %s", credential, role, where)

	secrets := m.secrets
	return func() tea.Msg {
		return writtenMsg{
			credential: credential, role: role,
			done: fmt.Sprintf("Stored %s.%s in the %s", credential, role, where),
			err:  secrets.Set(credential, role, value),
		}
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

// vaultOffer is the state of a masked vault passphrase prompt, in the pattern of the masked secret prompt:
// one input, reused for the entry and, while twice is set, for typing it again to confirm it.
//
// It serves every masked passphrase prompt of the vault, not only the offer that follows a vault's very
// first secret: optional and twice, set independently, cover the offer (empty accepted as "stay
// unencrypted", a non-empty entry confirmed once more), a mandatory new passphrase for 'encrypt' or
// 'passphrase' (never empty, always confirmed), and a single ask for a current passphrase for 'passphrase'
// or 'decrypt' (never empty, taken at once, no confirmation). resume is called once the prompt is answered,
// with an offer that hands back exactly the passphrase just taken or confirmed; it is never asked for a
// prompt of its own, since the masked input already showed one. cancel is called on esc, before anything
// reaches the vault; both return the command, if any, that continues whatever opened the prompt.
type vaultOffer struct {
	input      textinput.Model
	first      string
	confirming bool
	// optional accepts an empty first entry as "no passphrase" instead of refusing it; only the offer that
	// follows a vault's very first secret sets it.
	optional bool
	// twice asks a non-empty first entry again to confirm it; every prompt but a single ask for an existing
	// passphrase sets it.
	twice bool
	// title and hint are the first entry's heading and explanation; the confirmation step always reads the
	// same way, so it needs none of its own.
	title, hint string
	resume      func(offer vault.PassphraseFunc) tea.Cmd
	cancel      func() tea.Cmd
}

// openVaultOffer opens a masked passphrase prompt. It never touches the vault itself: resume decides what
// happens once it is answered, so the same prompt serves a single role of the credential form, the several
// roles a guided setup commits at once, and every vault settings action (see vaultsettings.go).
func (m *Model) openVaultOffer(title, hint string, optional, twice bool,
	resume func(offer vault.PassphraseFunc) tea.Cmd, cancel func() tea.Cmd) {
	in := textinput.New()
	in.Prompt = ""
	in.EchoMode = textinput.EchoPassword
	in.Cursor.SetMode(cursor.CursorStatic)
	in.Focus()

	m.vaultOffer = &vaultOffer{input: in, optional: optional, twice: twice, title: title, hint: hint,
		resume: resume, cancel: cancel}
	m.screen = screenVaultOffer
	m.clearMessages()
}

// updateVaultOffer handles the masked entry of a vault passphrase prompt: the first time through it takes a
// passphrase, and, while twice is set, the second time asks for the same one again to confirm it. A
// mismatch is an error that leaves the prompt open, back at the first entry, rather than closing it or
// writing anything.
func (m *Model) updateVaultOffer(key tea.KeyMsg) tea.Cmd {
	o := m.vaultOffer
	switch key.String() {
	case "ctrl+c":
		m.vaultOffer = nil
		return m.quit()
	case "esc":
		cancel := o.cancel
		m.vaultOffer = nil
		return cancel()
	case "enter":
		typed := o.input.Value()
		o.input.Reset()
		m.clearMessages()
		if !o.confirming {
			if typed == "" {
				if o.optional {
					// Leaving it empty and continuing is the offer declined: the vault stays, or becomes,
					// unencrypted, the same as 'qatlas credential set' answered the same way on the terminal.
					resume := o.resume
					m.vaultOffer = nil
					return resume(nil)
				}
				m.fail = "a passphrase must not be empty"
				return nil
			}
			if !o.twice {
				passphrase, resume := typed, o.resume
				m.vaultOffer = nil
				return resume(func(string) (string, error) { return passphrase, nil })
			}
			o.first, o.confirming = typed, true
			return nil
		}
		if typed != o.first {
			o.first, o.confirming = "", false
			m.fail = "the two passphrases did not match; type the passphrase again"
			return nil
		}
		passphrase, resume := o.first, o.resume
		m.vaultOffer = nil
		return resume(func(string) (string, error) { return passphrase, nil })
	}

	var cmd tea.Cmd
	o.input, cmd = o.input.Update(key)
	return cmd
}

// beginVaultSecret decides whether the role's secret would be the vault's very first: only then is a
// passphrase offered, exactly as vault.Vault.Set defines it. Deciding needs nothing but the vault's local
// files, so it happens right here, never inside the command the write itself runs as.
func (m *Model) beginVaultSecret(credential, role, value string) tea.Cmd {
	v := m.secrets.Vault()
	if v == nil {
		m.fail = "no vault is configured for this run"
		return nil
	}
	state, err := v.State()
	if err != nil {
		m.fail = m.redactor.Apply(err.Error())
		return nil
	}
	if state != vault.StateAbsent {
		return m.writeVaultSecret(credential, role, value, nil)
	}
	m.openVaultOffer(
		"Set a passphrase for the vault?",
		"this is the vault's very first secret; a passphrase encrypts it, typed masked and twice, or "+
			"leave this empty and press enter to keep the vault unencrypted",
		true, true,
		func(offer vault.PassphraseFunc) tea.Cmd {
			m.screen = screenForm
			return m.writeVaultSecret(credential, role, value, offer)
		},
		func() tea.Cmd {
			m.screen = screenForm
			m.status = "Cancelled"
			return nil
		},
	)
	return nil
}

// writeVaultSecret hands one secret to the vault. Like storeSecret it runs as a command, so the scrypt work
// that turns on encryption for the vault's first secret never runs on the event loop; vaultBusy blocks a
// second vault write or offer from starting before this one is done.
func (m *Model) writeVaultSecret(credential, role, value string, offer vault.PassphraseFunc) tea.Cmd {
	m.vaultBusy = true
	m.writes++
	m.busy = fmt.Sprintf("storing the secret for %s.%s in the vault", credential, role)

	secrets := m.secrets
	return func() tea.Msg {
		return writtenMsg{
			credential: credential, role: role, vault: true,
			done: fmt.Sprintf("Stored %s.%s in the vault", credential, role),
			err:  secrets.SetVault(credential, role, value, offer),
		}
	}
}

// removeVaultSecret clears one stored secret from the vault.
func (m *Model) removeVaultSecret(credential, role string) tea.Cmd {
	m.vaultBusy = true
	m.writes++
	m.busy = fmt.Sprintf("removing the stored secret for %s.%s from the vault", credential, role)

	secrets := m.secrets
	return func() tea.Msg {
		err := secrets.DeleteVault(credential, role)
		msg := writtenMsg{credential: credential, role: role, vault: true, err: err}
		if err == nil {
			msg.done = fmt.Sprintf("Removed %s.%s from the vault", credential, role)
		}
		return msg
	}
}

// vaultOfferView draws a masked vault passphrase prompt, in the pattern of the masked secret prompt: a
// title, the masked line, an explanation, and a footer of enter and esc. It has two shapes, the first entry
// (title and hint of its own) and its confirmation (always worded the same way), told apart by
// vaultOffer.confirming.
func (m *Model) vaultOfferView() string {
	o := m.vaultOffer
	var b strings.Builder
	if !o.confirming {
		b.WriteString(titleStyle.Render(o.title) + "\n\n")
		b.WriteString("  " + o.input.View() + "\n")
		b.WriteString(m.indented(o.hint) + "\n")
	} else {
		b.WriteString(titleStyle.Render("Confirm the vault passphrase") + "\n\n")
		b.WriteString("  " + o.input.View() + "\n")
		b.WriteString(m.indented("type it again to confirm it") + "\n")
	}
	b.WriteString(m.hint("enter continue · esc cancel, nothing is saved"))
	return b.String()
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
// what the keyring's state means on this platform and how to fix it, and names the derived variable as the
// other way. It is built from the class of the failure and never carries a value.
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
	return fmt.Sprintf("%s. Alternatively export %s.", text, secret.DerivedEnvName(credential, role))
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

// vaultLocked reports whether the vault is encrypted and locked, the one state a vault write or read must
// not proceed into here: finding or removing an entry inside secrets.age needs the passphrase, and this
// editor never asks its own terminal for one (see tuiSecrets in internal/cli). Storing a new secret is
// unaffected: it always queues as a pending entry the vault's public key can take, whatever state the
// vault is in, so this guard has no reason to block it.
func (m *Model) vaultLocked() bool {
	v := m.secrets.Vault()
	if v == nil {
		return false
	}
	state, err := v.State()
	return err == nil && state == vault.StateLocked
}

// vaultRoleState reports where a role of a vault credential's secret currently sits. It never asks for a
// passphrase: an encrypted, locked vault answers "vault locked" instead, since the entry cannot be read
// without one, and asking would block the whole editor on it.
func (m *Model) vaultRoleState(credential, role string) string {
	v := m.secrets.Vault()
	if v == nil {
		return stateUnreachable
	}
	state, err := v.State()
	if err != nil {
		return errorText(err)
	}
	if state == vault.StateAbsent {
		return stateEmpty
	}
	if state == vault.StateLocked {
		return stateVaultLocked
	}
	_, found, _, err := v.Get(credential, role, nil)
	if err != nil {
		return errorText(err)
	}
	if found {
		return stateStoredVault
	}
	return stateEmpty
}

// roleState reports where a role of the credential being edited currently sits, whichever place the form's
// storage row now names.
func (m *Model) roleState(credential, role, credType string) string {
	if credential == "" {
		return sourceUnsaved
	}
	if credType == config.CredentialTypeVault {
		return m.vaultRoleState(credential, role)
	}
	return m.storedSource(credential, role)
}

// secretState is the short form of where a secret role resolves from: a mark when the secret is where the
// credential keeps it, and otherwise the state that needs attention.
func secretState(source string) string {
	switch source {
	case stateStored, stateStoredVault, statePlaintext, string(secret.SourceEnv):
		return "✓"
	case stateEmpty, string(secret.SourceMissing):
		return "missing"
	case stateOverride:
		return "env override"
	case stateVaultLocked:
		return "locked"
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
		return "next: press s to store it in " + secret.StoreLabel(platform)
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

// secretKeys names the keys of a secret row: s stores the value into the place the storage row now names.
func (m *Model) secretKeys() string {
	if m.credentialType() == config.CredentialTypeVault {
		return "s store in the " + placeVault + " · x remove"
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
			placeKeyring + " or the " + placeVault + " saves without any value, and its secrets are added " +
			"afterwards"
		return nil
	}
	if m.credentialType() == config.CredentialTypeVault && m.vaultBusy {
		// A second vault write, or a second passphrase offer, must not start before the one already in
		// flight is done: either could still be deciding whether this is the vault's very first secret.
		m.status = "a vault write is already in progress; wait for it to finish"
		return nil
	}

	switch action {
	case "s":
		m.askSecret(role)
	case "x":
		if m.credentialType() == config.CredentialTypeVault && m.vaultLocked() {
			// Removing a vault secret needs the passphrase to find the entry inside secrets.age, which
			// this editor never asks its own terminal for (see tuiSecrets in internal/cli): asking here
			// would try to prompt on the very terminal bubbletea holds in raw mode for its own screen.
			m.status = ""
			m.fail = "vault is locked; run 'qatlas vault unlock' first"
			return nil
		}
		// Removing a stored secret is irreversible, so it is confirmed like every other deletion here.
		m.confirmRole = role
		m.screen = screenConfirm
		m.clearMessages()
	}
	return nil
}
