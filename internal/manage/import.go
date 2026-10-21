package manage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// ImportMode says what an import does with an entry whose name the target already has. Entries are matched
// by name alone.
type ImportMode string

const (
	// ImportModeCreate only creates: any name that exists in the target stops the whole import.
	ImportModeCreate ImportMode = "create"
	// ImportModeMerge adds what is missing and leaves existing entries as they are.
	ImportModeMerge ImportMode = "merge"
	// ImportModeOverwrite replaces existing entries with the file's. A replaced entry keeps the id of the
	// entry it replaces, so approvals and vault entries that record the id stay attached; an approval that
	// would cover a secret the file brings is withdrawn (see ApplyImportWith).
	ImportModeOverwrite ImportMode = "overwrite"
)

// ParseImportMode returns the mode named text.
func ParseImportMode(text string) (ImportMode, error) {
	switch m := ImportMode(text); m {
	case ImportModeCreate, ImportModeMerge, ImportModeOverwrite:
		return m, nil
	}
	return "", fmt.Errorf("unknown import mode %q, use %s, %s or %s", text, ImportModeCreate, ImportModeMerge, ImportModeOverwrite)
}

// What an import does with one entry.
const (
	ImportActionCreate  = "create"
	ImportActionSkip    = "skip"
	ImportActionReplace = "replace"
)

// The sections an ImportEntry belongs to.
const (
	ImportServices      = "services"
	ImportCredentials   = "credentials"
	ImportConnections   = "connections"
	ImportDefaults      = "defaults.connections"
	ImportSecretStore   = "defaults.secret_store"
	ImportProviderNotes = "provider_notes"
)

// ImportEntry is what an import does with one entry of the file.
type ImportEntry struct {
	// Section is one of the Import constants above; Name is the entry's name, the domain of a default or the
	// provider of a note. Action is ImportActionCreate, ImportActionSkip or ImportActionReplace.
	Section, Name, Action string
	// ID is the id the entry has afterwards, "" for a section without ids and for a skipped entry. NewID is
	// true when it is not the id the file brought: that one was taken in the target, the entry replaces
	// one that keeps its own, or the file had none.
	ID    string
	NewID bool
	// UsesTarget names the entries of the target that a connection of the file refers to because the file's
	// own entry of that name is skipped, such as "service wiki". The target's entry is the one that counts.
	UsesTarget []string
	// UsedBy names the connections of the target, outside the file, that use a replaced service or credential
	// and so are changed by it.
	UsedBy []string
	// Access names the local access a created or replaced connection brings: "paths", "files.read",
	// "files.write" and "forward_secrets". It is there so a person sees what a foreign file grants.
	Access []string
}

// ImportPlan is what importing a file does, found without writing anything. PlanImport makes it and
// ApplyImportWith carries it out.
type ImportPlan struct {
	Mode ImportMode
	// Source is the file read.
	Source  string
	Entries []ImportEntry
	// CredentialsWithoutSecret are the created credentials whose secrets live in the keyring or the vault. They
	// hold no secret in this installation until one is set, so no connection can use them before.
	CredentialsWithoutSecret []string
	// CredentialsKeepSecret are the replaced credentials. Whatever secret the target holds for the name stays
	// untouched, and it may not fit the replacement.
	CredentialsKeepSecret []string
	// SecretRoles names, per created or replaced credential, the roles whose secret the file brings (never a
	// value). Such a credential is in neither of the two lists above. The secrets of a skipped credential
	// are not part of the plan. SourceEncrypted tells whether the file was an age file.
	SecretRoles     map[string][]string
	SourceEncrypted bool

	before, cfg *config.Config
	base        config.Revision
	// secrets are the values behind SecretRoles. They stay in the plan and in nothing it prints.
	secrets map[string]map[string]string
}

// String and GoString keep the secret values of a plan out of every fmt verb, which would print the
// unexported fields.
func (p ImportPlan) String() string {
	created, replaced, skipped := p.Count()
	return fmt.Sprintf("import plan of %s: %d created, %d replaced, %d skipped", p.Source, created, replaced, skipped)
}

func (p ImportPlan) GoString() string { return p.String() }

// Count returns how many entries the plan creates, replaces and skips.
func (p *ImportPlan) Count() (created, replaced, skipped int) {
	for _, e := range p.Entries {
		switch e.Action {
		case ImportActionCreate:
			created++
		case ImportActionReplace:
			replaced++
		default:
			skipped++
		}
	}
	return
}

// ImportExistsError reports the entries ImportModeCreate refused because the target has them. Each is
// "section.name".
type ImportExistsError struct{ Entries []string }

func (e *ImportExistsError) Error() string {
	return "the target already has " + strings.Join(e.Entries, ", ") + "; use merge to keep them or overwrite to replace them"
}

// ImportResult is what ApplyImport did.
type ImportResult struct {
	Created, Replaced, Skipped int
	// Warnings are the redacted failures after the save, none of which undoes it.
	Warnings []string
	// SecretStore is where the secrets the file brought were written, config.CredentialTypeKeyring or
	// config.CredentialTypeVault, "" when it brought none. Every credential with such a secret has this type
	// afterwards. KeyringOverSSH is set when that is the system keyring in an SSH session (see SSHSession).
	SecretStore    string
	KeyringOverSSH bool
	// Withdrawn names the connections whose approval the import withdrew because they read or forward a
	// credential whose secrets it wrote to the vault: no approval given for the secrets before covers the
	// ones the file brought. They stay open until approved.
	Withdrawn []string
	// Connections are the connections the import created or replaced. UsedBy are the connections of the
	// target, outside the file, that use a service or credential it replaced (see ImportEntry.UsedBy).
	Connections, UsedBy []string

	// saved is the configuration as the import saved it, for ApproveImport.
	saved *config.Config
}

// PlanImport reads the export file at path, checks it completely and works out what mode does to the
// current configuration, which does not exist yet on a fresh installation. It writes nothing.
//
// The file must be self-contained: every reference in it resolves inside it. The configuration that would
// result - the file's entries as the plan places them, among the target's - must be valid as well, which
// is how a skipped entry of the target that does not fit a connection of the file is found. Either failure,
// and a mode conflict, is returned as an error; the text has passed the service's redaction.
//
// An id the file brings stays only if the target has no entry of that kind with it. Otherwise, and for an
// entry without one, a new id is assigned.
func (s *Service) PlanImport(path string, mode ImportMode) (*ImportPlan, error) {
	return s.PlanImportFile(path, mode, nil)
}

// PlanImportFile is PlanImport for a file that may hold secrets and be encrypted. ask returns the export
// passphrase and is called only for an encrypted file; with a nil ask such a file is refused. A wrong
// passphrase is a refusal (errors.Is(err, vault.ErrWrongExportPassphrase)) with nothing planned. The
// secrets are checked together with the rest of the file and kept in the plan, never in its text.
func (s *Service) PlanImportFile(path string, mode ImportMode, ask vault.PassphraseFunc) (*ImportPlan, error) {
	if _, err := ParseImportMode(string(mode)); err != nil {
		return nil, err
	}
	file, fileCfg, err := s.readTransfer(path, ask)
	if err != nil {
		return nil, err
	}
	target, rev, err := s.Load()
	if err != nil {
		var nf *config.NotFoundError
		if !errors.As(err, &nf) {
			return nil, err
		}
		target = s.NewConfig()
	}
	before := target
	cand := target.Clone()
	plan := &ImportPlan{Mode: mode, Source: path, SourceEncrypted: file.encrypted, before: before, cfg: cand, base: rev}
	var exists []string

	// decide gives the action for one entry and records a conflict of ImportModeCreate.
	decide := func(section, name string, has bool) string {
		switch {
		case !has:
			return ImportActionCreate
		case mode == ImportModeMerge:
			return ImportActionSkip
		case mode == ImportModeOverwrite:
			return ImportActionReplace
		}
		exists = append(exists, section+"."+name)
		return ImportActionSkip
	}
	// takenIDs are the ids the target has per kind; its entries all stay, so none of them may be reused.
	taken := map[string]map[string]bool{ImportServices: {}, ImportCredentials: {}, ImportConnections: {}}
	for _, v := range target.Services {
		taken[ImportServices][v.ID] = true
	}
	for _, v := range target.Credentials {
		taken[ImportCredentials][v.ID] = true
	}
	for _, v := range target.Connections {
		taken[ImportConnections][v.ID] = true
	}
	freeID := func(section, id string) string {
		if id == "" || taken[section][id] {
			return ""
		}
		return id
	}
	actions := map[string]string{}
	add := func(e ImportEntry) { plan.Entries = append(plan.Entries, e); actions[e.Section+"."+e.Name] = e.Action }
	// settle fixes the id of an entry the plan puts in, keeping the target's own id on a replacement.
	settle := func(section, action, fileID, targetID string) string {
		if action == ImportActionReplace && targetID != "" {
			return targetID
		}
		return freeID(section, fileID)
	}

	for _, name := range sortedNames(file.Services) {
		old, has := target.Services[name]
		action := decide(ImportServices, name, has)
		e := ImportEntry{Section: ImportServices, Name: name, Action: action}
		if action != ImportActionSkip {
			v := file.Services[name]
			id := settle(ImportServices, action, v.ID, old.ID)
			v.ID = id
			cand.Services[name] = v
			e.ID = id
			if id != "" {
				taken[ImportServices][id] = true
			}
		}
		add(e)
	}
	for _, name := range sortedNames(file.Credentials) {
		old, has := target.Credentials[name]
		action := decide(ImportCredentials, name, has)
		e := ImportEntry{Section: ImportCredentials, Name: name, Action: action}
		if action != ImportActionSkip {
			v := file.Credentials[name]
			id := settle(ImportCredentials, action, v.ID, old.ID)
			v.ID = id
			cand.Credentials[name] = v
			e.ID = id
			if id != "" {
				taken[ImportCredentials][id] = true
			}
			if brought := file.Secrets[name]; len(brought) > 0 {
				if plan.secrets == nil {
					plan.secrets = map[string]map[string]string{}
					plan.SecretRoles = map[string][]string{}
				}
				plan.secrets[name] = brought
				plan.SecretRoles[name] = sortedNames(brought)
			}
			switch {
			case plan.secrets[name] != nil:
			case action == ImportActionReplace:
				plan.CredentialsKeepSecret = append(plan.CredentialsKeepSecret, name)
			case v.Type != config.CredentialTypeEnv:
				plan.CredentialsWithoutSecret = append(plan.CredentialsWithoutSecret, name)
			}
		}
		add(e)
	}
	for _, name := range sortedNames(file.Connections) {
		old, has := target.Connections[name]
		action := decide(ImportConnections, name, has)
		e := ImportEntry{Section: ImportConnections, Name: name, Action: action}
		if action != ImportActionSkip {
			v := file.Connections[name]
			id := settle(ImportConnections, action, v.ID, old.ID)
			v.ID = id
			if err := cand.SetConnection(name, v); err != nil {
				return nil, s.refuse(err)
			}
			e.ID = id
			if id != "" {
				taken[ImportConnections][id] = true
			}
			if len(v.Paths) > 0 {
				e.Access = append(e.Access, "paths")
			}
			if len(v.Files.Read) > 0 {
				e.Access = append(e.Access, "files.read")
			}
			if len(v.Files.Write) > 0 {
				e.Access = append(e.Access, "files.write")
			}
			if len(v.ForwardSecrets) > 0 {
				e.Access = append(e.Access, "forward_secrets")
			}
			ref := func(section, kind, ref string) {
				if actions[section+"."+ref] == ImportActionSkip {
					e.UsesTarget = append(e.UsesTarget, kind+" "+ref)
				}
			}
			ref(ImportServices, "service", v.Service)
			ref(ImportCredentials, "credential", v.Credential)
			for _, f := range v.ForwardSecrets {
				ref(ImportCredentials, "credential", f)
			}
		}
		add(e)
	}
	if file.Defaults != nil {
		for _, domain := range sortedNames(file.Defaults.Connections) {
			old, has := target.Defaults.Connections[domain]
			if has && old == file.Defaults.Connections[domain] {
				add(ImportEntry{Section: ImportDefaults, Name: domain, Action: ImportActionSkip})
				continue
			}
			action := decide(ImportDefaults, domain, has)
			if action != ImportActionSkip {
				cand.Defaults.Connections[domain] = file.Defaults.Connections[domain]
			}
			add(ImportEntry{Section: ImportDefaults, Name: domain, Action: action})
		}
		if store := file.Defaults.SecretStore; store != "" {
			old := target.Defaults.SecretStore
			if old == store {
				add(ImportEntry{Section: ImportSecretStore, Name: "secret_store", Action: ImportActionSkip})
			} else {
				action := decide(ImportSecretStore, "secret_store", old != "")
				if action != ImportActionSkip {
					cand.Defaults.SecretStore = store
				}
				add(ImportEntry{Section: ImportSecretStore, Name: "secret_store", Action: action})
			}
		}
	}
	for _, provider := range sortedNames(file.ProviderNotes) {
		old, has := target.ProviderNotes[provider]
		note := fileCfg.ProviderNotes[provider]
		if has && old == note {
			add(ImportEntry{Section: ImportProviderNotes, Name: provider, Action: ImportActionSkip})
			continue
		}
		action := decide(ImportProviderNotes, provider, has)
		if action != ImportActionSkip {
			if err := cand.SetProviderNote(provider, note); err != nil {
				return nil, s.refuse(err)
			}
		}
		add(ImportEntry{Section: ImportProviderNotes, Name: provider, Action: action})
	}
	if len(exists) > 0 {
		return nil, s.refuse(&ImportExistsError{Entries: exists})
	}

	cand.AssignIDs()
	for i, e := range plan.Entries {
		if e.Action == ImportActionSkip {
			continue
		}
		switch e.Section {
		case ImportServices:
			plan.Entries[i].ID = cand.Services[e.Name].ID
			plan.Entries[i].NewID = plan.Entries[i].ID != file.Services[e.Name].ID
		case ImportCredentials:
			plan.Entries[i].ID = cand.Credentials[e.Name].ID
			plan.Entries[i].NewID = plan.Entries[i].ID != file.Credentials[e.Name].ID
		case ImportConnections:
			plan.Entries[i].ID = cand.Connections[e.Name].ID
			plan.Entries[i].NewID = plan.Entries[i].ID != file.Connections[e.Name].ID
		}
	}
	if err := cand.Validate(); err != nil {
		return nil, s.refuse(fmt.Errorf("the import would leave the configuration invalid: %w", &config.InvalidError{Path: s.store.Path(), Err: err}))
	}
	plan.noteUsedBy(file)
	return plan, nil
}

// noteUsedBy names, for each replaced service and credential, the connections of the target that are not
// part of the file and still use it.
func (p *ImportPlan) noteUsedBy(file *transferFile) {
	for i, e := range p.Entries {
		if e.Action != ImportActionReplace || (e.Section != ImportServices && e.Section != ImportCredentials) {
			continue
		}
		for _, name := range sortedNames(p.cfg.Connections) {
			if _, inFile := file.Connections[name]; inFile {
				continue
			}
			conn := p.cfg.Connections[name]
			if (e.Section == ImportServices && conn.Service == e.Name) ||
				(e.Section == ImportCredentials && (conn.Credential == e.Name || slices.Contains(conn.ForwardSecrets, e.Name))) {
				p.Entries[i].UsedBy = append(p.Entries[i].UsedBy, name)
			}
		}
	}
}

// ApplyImport is ApplyImportWith without options for a plan that brings no secret; a plan with secrets is
// refused with nothing written, since where they go and whether a new vault is encrypted are choices only
// ApplyImportWith takes.
func (s *Service) ApplyImport(plan *ImportPlan) (ImportResult, error) {
	if plan != nil && len(plan.secrets) > 0 {
		return ImportResult{}, errors.New("this import brings secrets; apply it with the choice of where they go")
	}
	return s.ApplyImportWith(plan, ImportOptions{})
}

// ImportOptions are the choices of an import that brings secrets.
type ImportOptions struct {
	// SecretStore is where the secrets go, config.CredentialTypeKeyring or config.CredentialTypeVault, or ""
	// for the defaults.secret_store of the configuration as the import leaves it.
	SecretStore string
	// Offer is asked for a passphrase only when a secret here would be the vault's very first.
	Offer vault.PassphraseFunc
}

// ApplyImportWith writes plan, which PlanImport or PlanImportFile made, as one change: the configuration is
// saved only if the file still has the revision the plan was based on, and then completely. Another change
// since the plan is a *config.ConflictError (errors.Is(err, config.ErrConflict)) with nothing written and
// nothing merged; plan again to see the new state. A plan that changes nothing writes nothing.
//
// The secrets the plan brings are written first, all of them, to the store opts names (see SecretStoreFor),
// and every credential they belong to gets that store as its type, so they are read where they were
// written. The configuration is the commit point: when a secret or the save fails, every secret written is
// rolled back, the vault byte for byte, the keyring to the values it held. A failure of that rollback is
// named in the error, never a value. In an encrypted vault the approval of every connection that reads or
// forwards such a credential is withdrawn in the same change (ImportResult.Withdrawn); a locked vault is
// refused before anything is written, since its approvals cannot be withdrawn without its passphrase. A
// secret the plan does not bring stays as the target holds it. No approval or agent token is taken from the
// file; ApproveImport approves afterwards.
func (s *Service) ApplyImportWith(plan *ImportPlan, opts ImportOptions) (ImportResult, error) {
	var res ImportResult
	if plan == nil || plan.cfg == nil {
		return res, errors.New("no import was planned")
	}
	res.Created, res.Replaced, res.Skipped = plan.Count()
	res.Connections, res.UsedBy = plan.connections()
	if res.Created+res.Replaced == 0 {
		return res, nil
	}
	if len(plan.secrets) == 0 {
		warning, err := s.SaveConfig(plan.before, plan.cfg, plan.base)
		if err != nil {
			return ImportResult{}, err
		}
		if warning != "" {
			res.Warnings = append(res.Warnings, warning)
		}
		res.saved = plan.cfg
		return res, nil
	}

	store, overSSH, err := plan.SecretStoreFor(opts.SecretStore)
	if err != nil {
		return ImportResult{}, err
	}
	if s.secrets == nil {
		return ImportResult{}, errNoSecretStore
	}
	cand := plan.cfg.Clone()
	var sets []secretSet
	for _, name := range sortedNames(plan.secrets) {
		cred := cand.Credentials[name]
		cred.Type = store
		cand.Credentials[name] = cred
		values := plan.secrets[name]
		if s.redactor != nil {
			for _, value := range values {
				s.redactor.Add(value)
			}
		}
		sets = append(sets, secretSet{credential: name, toVault: store == config.CredentialTypeVault,
			roles: sortedNames(values), values: values})
	}
	if err := cand.Validate(); err != nil {
		return ImportResult{}, s.refuse(fmt.Errorf("the import would leave the configuration invalid: %w", &config.InvalidError{Path: s.store.Path(), Err: err}))
	}

	var prepare func() (func() error, error)
	unlocked := false
	if store == config.CredentialTypeVault {
		v := s.vault()
		if v == nil {
			return ImportResult{}, ErrNoVault
		}
		state, err := v.State()
		if err != nil {
			return ImportResult{}, s.redactErr(err)
		}
		if state == vault.StateLocked {
			return ImportResult{}, errors.New("the vault is locked: unlock it before importing secrets into it, " +
				"so the approvals that cover the secrets they replace can be withdrawn")
		}
		unlocked = state == vault.StateUnlocked
		prepare = func() (func() error, error) {
			restore, err := v.Checkpoint()
			if err != nil {
				return nil, err
			}
			if !unlocked {
				return restore, nil
			}
			names, err := approvalsCovering(cand, v, sortedNames(plan.secrets))
			if err == nil && len(names) > 0 {
				_, err = v.Revoke(names)
			}
			if err != nil {
				if restoreErr := restore(); restoreErr != nil {
					err = fmt.Errorf("%w; the vault could not be put back as it was before: %v", err, restoreErr)
				}
				return nil, err
			}
			res.Withdrawn = names
			return restore, nil
		}
	}
	written, err := s.commitSecretSets(cand, plan.base, sets, opts.Offer, prepare)
	if err != nil {
		return ImportResult{}, s.redactErr(err)
	}
	res.SecretStore, res.KeyringOverSSH, res.saved = store, overSSH, cand
	if store == config.CredentialTypeVault {
		v := s.vault()
		// The withdrawn approvals reach the vault process before the secrets they no longer cover.
		if warning := s.SyncVaultProcess(context.Background(), func(ctx context.Context, client *vaultproc.Client) error {
			if bindings, err := v.Bindings(); err == nil {
				if err := client.Bind(ctx, bindings); err != nil {
					return err
				}
			}
			for _, set := range written {
				if err := set.sendTo(ctx, client); err != nil {
					return err
				}
			}
			return nil
		}); warning != "" {
			res.Warnings = append(res.Warnings, warning)
		}
	}
	if warning := s.RecordConnections(plan.before, cand); warning != "" {
		res.Warnings = append(res.Warnings, warning)
	}
	return res, nil
}

// SecretStoreFor returns where ApplyImportWith writes the secrets of the plan for the store option override:
// override itself, or for "" the defaults.secret_store of the configuration as the import leaves it.
// keyringOverSSH is set when that is the system keyring and this is an SSH session, where a keyring often
// cannot be unlocked. An override that is no store is an error.
func (p *ImportPlan) SecretStoreFor(override string) (store string, keyringOverSSH bool, err error) {
	switch override {
	case "":
		store = p.cfg.SecretStore()
	case config.CredentialTypeKeyring, config.CredentialTypeVault:
		store = override
	default:
		return "", false, fmt.Errorf("unknown secret store %q, use %s or %s", override, config.CredentialTypeKeyring,
			config.CredentialTypeVault)
	}
	return store, store == config.CredentialTypeKeyring && SSHSession(), nil
}

// SSHSession reports whether this process runs in an SSH session, as the variables sshd sets say.
func SSHSession() bool {
	for _, name := range []string{"SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"} {
		if os.Getenv(name) != "" {
			return true
		}
	}
	return false
}

// connections returns the connections the plan creates or replaces, and the connections of the target
// outside the file that use what it replaces, both sorted.
func (p *ImportPlan) connections() (imported, usedBy []string) {
	seen := map[string]bool{}
	for _, e := range p.Entries {
		if e.Section == ImportConnections && e.Action != ImportActionSkip {
			imported = append(imported, e.Name)
		}
		for _, name := range e.UsedBy {
			if !seen[name] {
				seen[name] = true
				usedBy = append(usedBy, name)
			}
		}
	}
	slices.Sort(imported)
	slices.Sort(usedBy)
	return imported, usedBy
}

// approvalsCovering names, sorted, the connections of cfg the unlocked vault v holds an approval for that
// read or forward one of credentials.
func approvalsCovering(cfg *config.Config, v *vault.Vault, credentials []string) ([]string, error) {
	approvals, err := v.Approvals()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, name := range sortedNames(cfg.Connections) {
		if _, ok := approvals[name]; !ok {
			continue
		}
		conn := cfg.Connections[name]
		for _, credential := range credentials {
			if conn.Credential == credential || slices.Contains(conn.ForwardSecrets, credential) {
				names = append(names, name)
				break
			}
		}
	}
	return names, nil
}

// redactErr returns err with its text passed through the service's redaction; errors.Is and errors.As still
// see err.
func (s *Service) redactErr(err error) error {
	if err == nil || s.redactor == nil {
		return err
	}
	return &redactedError{msg: s.redact(err.Error()), err: err}
}

type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.err }

// ImportApproval is what ApproveImport did. Failing to approve never undoes the import.
type ImportApproval struct {
	// Approved lists the connections approved, sorted.
	Approved []string
	// Open are the connections of ImportResult.Connections and ImportResult.UsedBy the vault still holds open,
	// sorted by name.
	Open []ImportOpen
	// VaultUnencrypted is set for a vault that is not encrypted: it binds no connection, so there is nothing
	// to approve. VaultLocked is set for an encrypted vault not unlocked here, where nothing can be approved.
	VaultUnencrypted, VaultLocked bool
	// Err is the first failure to determine or approve the open connections, never with a secret.
	Err error
	// Warnings are hints from handing the approvals to a running vault process.
	Warnings []string
}

// ImportOpen is one connection an import left open.
type ImportOpen struct {
	Connection string
	// Reason is the short reason it is open (see StaysOpenReason). Imported is false for a connection of the
	// target that only uses what the import replaced; ApproveImport never approves such a one.
	Reason   string
	Imported bool
	// ForwardChanged says it is open for a changed release of payload secrets, which only an explicit
	// approval releases.
	ForwardChanged bool
}

// ApproveImport approves, after res succeeded, the connections the import created or replaced, each by the
// rule for a connection saved directly by name (ApprovalDirectOnly): one open for a reason the import's own
// entry does not show, or for a changed release of payload secrets, stays open. ApprovalNone approves
// nothing and only reports what is open. Neither policy approves a connection of res.UsedBy: it changed
// without being part of the file. The vault must be encrypted and unlocked in this process.
func (s *Service) ApproveImport(ctx context.Context, res ImportResult, policy ApprovalPolicy) ImportApproval {
	var out ImportApproval
	v := s.vault()
	if v == nil || res.saved == nil {
		return out
	}
	state, err := v.State()
	switch {
	case err != nil:
		out.Err = s.redactErr(err)
		return out
	case state == vault.StateUnencrypted:
		out.VaultUnencrypted = true
		return out
	case state == vault.StateLocked:
		out.VaultLocked = true
		return out
	case state != vault.StateUnlocked:
		return out
	}
	if policy != ApprovalNone {
		for _, name := range res.Connections {
			r := ApproveAfterChange(ctx, v, ApprovalSnapshot{}, res.saved, name, ApprovalDirectOnly)
			out.Approved = append(out.Approved, r.Approved...)
			if r.Warning != "" {
				out.Warnings = append(out.Warnings, s.redact(r.Warning))
			}
			if err := errors.Join(r.CheckErr, r.ApproveErr); err != nil {
				out.Err = s.redactErr(err)
				break
			}
		}
		slices.Sort(out.Approved)
	}
	report, err := approval.Pending(res.saved, v)
	if err != nil {
		if out.Err == nil {
			out.Err = s.redactErr(err)
		}
		return out
	}
	for _, c := range report.Open {
		imported := slices.Contains(res.Connections, c.Connection)
		if !imported && !slices.Contains(res.UsedBy, c.Connection) {
			continue
		}
		out.Open = append(out.Open, ImportOpen{Connection: c.Connection, Reason: StaysOpenReason(c),
			Imported: imported, ForwardChanged: releaseChanged(c)})
	}
	return out
}
