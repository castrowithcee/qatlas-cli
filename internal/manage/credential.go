package manage

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// NotStorableError reports a credential whose type keeps no stored secret: its secrets come from the
// environment variables it names, so there is nothing to store or remove.
type NotStorableError struct{ Credential, Type string }

func (e *NotStorableError) Error() string {
	return fmt.Sprintf("credential %q has type %q: its secrets come from the environment variables it names, "+
		"so there is nothing to store", e.Credential, e.Type)
}

// UnknownFieldError reports a field a forward credential does not have.
type UnknownFieldError struct {
	Credential, Role string
	Fields           []string
}

func (e *UnknownFieldError) Error() string {
	return fmt.Sprintf("unknown field %q, the fields of %s are %s", e.Role, e.Credential, strings.Join(e.Fields, ", "))
}

// UnknownRoleError reports a secret role the credential does not accept. Provider is the one the
// credential names, "" for none. Accepted lists what it would have accepted.
type UnknownRoleError struct {
	Credential, Role, Provider string
	Accepted                   []string
}

func (e *UnknownRoleError) Error() string {
	if e.Provider != "" {
		return fmt.Sprintf("unknown secret role %q for %s, whose provider %s has the roles %s",
			e.Role, e.Credential, e.Provider, strings.Join(e.Accepted, ", "))
	}
	return fmt.Sprintf("unknown secret role %q, known roles are %s", e.Role, strings.Join(e.Accepted, ", "))
}

// ValueError reports a secret value a forward credential refuses. The message names the credential and the
// rule, never the value.
type ValueError struct {
	Credential string
	Err        error
}

func (e *ValueError) Error() string { return fmt.Sprintf("credential %s: %v", e.Credential, e.Err) }

func (e *ValueError) Unwrap() error { return e.Err }

// CheckSecretTarget looks up the saved credential name and verifies that role names something that can
// hold a stored secret at all: a typo would otherwise leave an entry nothing ever reads. It returns the
// credential so the caller branches on its type without loading the configuration again. The errors are
// *config.NotThereError, *NotStorableError, *UnknownFieldError and *UnknownRoleError.
func CheckSecretTarget(cfg *config.Config, name, role string) (config.Credential, error) {
	cred, ok := cfg.Credentials[name]
	if !ok {
		return config.Credential{}, &config.NotThereError{Kind: "credential", Name: name}
	}
	if err := checkEntry(cfg, name, cred, role); err != nil {
		return config.Credential{}, err
	}
	return cred, nil
}

// checkEntry is CheckSecretTarget for an entry the caller already holds. A forward credential accepts its
// own fields; every other one accepts the roles AcceptedRoles names.
func checkEntry(cfg *config.Config, name string, cred config.Credential, role string) error {
	if cred.Type != config.CredentialTypeKeyring && cred.Type != config.CredentialTypeVault {
		return &NotStorableError{Credential: name, Type: cred.Type}
	}
	if cred.Forward {
		if !contains(cred.Fields, role) {
			return &UnknownFieldError{Credential: name, Role: role, Fields: cred.Fields}
		}
		return nil
	}
	if accepted := AcceptedRoles(cfg, cred); !contains(accepted, role) {
		return &UnknownRoleError{Credential: name, Role: role, Provider: cred.Provider, Accepted: accepted}
	}
	return nil
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// SecretWrite describes one secret to store or remove. The surface that holds unsaved form state names the
// entry it means in Entry; the others pass the saved one CheckSecretTarget returned.
type SecretWrite struct {
	// Name is the credential the secret belongs to.
	Name string
	// Entry decides where the secret goes (Type: vault or keyring) and which roles are accepted (see
	// AcceptedRoles). Only a keyring or vault entry can hold a secret.
	Entry config.Credential
	// Role is the secret role, or the field of a forward credential.
	Role string
	// Value is the secret to store. SetSecret only; it is never part of an error or a result.
	Value string
	// Offer is asked for a passphrase only when a vault secret here would be the vault's very first.
	Offer vault.PassphraseFunc
	// Approval is the policy for approving connections after a vault change. A surface names it
	// explicitly; ApprovalNone approves nothing. A keyring change approves nothing whatever the policy.
	Approval ApprovalPolicy
}

// SecretOutcome is what a stored or removed secret leaves for the surface to show.
type SecretOutcome struct {
	// ProcessWarning is the redacted warning of handing a vault change on to a vault process, "" for none.
	ProcessWarning string
	// Approval is the outcome of the approvals the policy asked for; zero when none ran.
	Approval ApprovalResult
	// Cleared lists the stores a keyring removal cleared.
	Cleared []secret.Source
}

// SetSecret stores one secret of an existing credential, in the vault when w.Entry is a vault credential
// and otherwise the system keyring, after checking the target as CheckSecretTarget does and a forward
// credential's value. A vault change is handed on to a vault process that holds the vault unlocked, and
// connections are approved by w.Approval afterwards. A refused target or value is returned as the typed
// errors above; a store failure is returned as the store reported it.
func (s *Service) SetSecret(ctx context.Context, cfg *config.Config, w SecretWrite) (SecretOutcome, error) {
	var out SecretOutcome
	if err := checkEntry(cfg, w.Name, w.Entry, w.Role); err != nil {
		return out, err
	}
	if w.Entry.Forward {
		if err := w.Entry.CheckForwardValue(w.Role, w.Value); err != nil {
			return out, &ValueError{Credential: w.Name, Err: err}
		}
	}
	if s.secrets == nil {
		return out, errNoSecretStore
	}
	if w.Entry.Type != config.CredentialTypeVault {
		return out, s.secrets.Set(w.Name, w.Role, w.Value)
	}
	before := s.snapshotFor(cfg, w.Approval)
	if err := s.secrets.SetVault(w.Name, w.Role, w.Value, w.Offer); err != nil {
		return out, err
	}
	out.ProcessWarning = s.SyncVaultProcess(ctx, func(ctx context.Context, client *vaultproc.Client) error {
		return client.Set(ctx, w.Name, w.Role, w.Value)
	})
	out.Approval = ApproveAfterChange(ctx, s.vault(), before, cfg, "", w.Approval)
	return out, nil
}

// DeleteSecret removes one secret of an existing credential from the vault when w.Entry is a vault
// credential and otherwise from every place the system keyring keeps one, with the checks, the hand-over
// to a vault process and the approvals of SetSecret. w.Value and w.Offer are not used. Errors are the
// typed ones above or the store's own, secret.ErrNoEntry among them.
func (s *Service) DeleteSecret(ctx context.Context, cfg *config.Config, w SecretWrite) (SecretOutcome, error) {
	var out SecretOutcome
	if err := checkEntry(cfg, w.Name, w.Entry, w.Role); err != nil {
		return out, err
	}
	if s.secrets == nil {
		return out, errNoSecretStore
	}
	if w.Entry.Type != config.CredentialTypeVault {
		cleared, err := s.secrets.Delete(w.Name, w.Role)
		out.Cleared = cleared
		return out, err
	}
	before := s.snapshotFor(cfg, w.Approval)
	if err := s.secrets.DeleteVault(w.Name, w.Role); err != nil {
		return out, err
	}
	out.ProcessWarning = s.SyncVaultProcess(ctx, func(ctx context.Context, client *vaultproc.Client) error {
		return client.Delete(ctx, w.Name, w.Role)
	})
	out.Approval = ApproveAfterChange(ctx, s.vault(), before, cfg, "", w.Approval)
	return out, nil
}

// snapshotFor captures the approvals before a vault change, for the one policy that compares with them.
func (s *Service) snapshotFor(cfg *config.Config, policy ApprovalPolicy) ApprovalSnapshot {
	if policy != ApprovalSweepNewlyOpened {
		return ApprovalSnapshot{}
	}
	return SnapshotApprovals(s.vault(), cfg)
}

// ErrNoVault reports a vault credential's operation in a run that has no vault.
var ErrNoVault = errors.New("no vault is configured for this run")

// NeedsPassphraseOffer reports whether a vault secret stored now would be the vault's very first: only
// then is a passphrase offered, exactly as vault.Vault.Set defines it. It needs nothing but the vault's
// local files. A nil vault answers false with ErrNoVault, an unreadable state false with that error.
func NeedsPassphraseOffer(v *vault.Vault) (bool, error) {
	if v == nil {
		return false, ErrNoVault
	}
	state, err := v.State()
	if err != nil {
		return false, err
	}
	return state == vault.StateAbsent, nil
}

// VaultHoldings is what a vault holds for one credential.
type VaultHoldings struct {
	// Locked reports an encrypted, locked vault: nothing could be read without its passphrase.
	Locked bool
	// Roles are the compiled secret roles the vault holds an entry for, in role order. Empty when Locked.
	Roles []string
}

// HeldInVault reads, without asking for a passphrase, which secret roles the vault v holds for credential.
// Every compiled role is checked, not only those the credential accepts, so no entry is overlooked. A nil
// vault holds nothing. An unreadable state counts as not locked; reading an entry then reports the cause.
// A failure reading the vault is returned as it came and is redacted by the caller as any error text.
func HeldInVault(cfg *config.Config, v *vault.Vault, credential string) (VaultHoldings, error) {
	var res VaultHoldings
	if v == nil {
		return res, nil
	}
	if state, err := v.State(); err == nil && state == vault.StateLocked {
		res.Locked = true
		return res, nil
	}
	for _, role := range cfg.SecretRoles() {
		_, found, _, err := v.Get(credential, role, nil)
		if err != nil {
			return VaultHoldings{}, err
		}
		if found {
			res.Roles = append(res.Roles, role)
		}
	}
	return res, nil
}

// PlacementSource answers where a keyring credential's secret sits; *secret.Resolver satisfies it.
type PlacementSource interface {
	Stored(credential, role string) secret.Placement
}

// PlacementsOf asks, place by place, which stores keep an entry for each of roles of credential. The
// environment is not consulted (see secret.Resolver.Stored). It may reach the system keyring, so a surface
// runs it off its event loop.
func PlacementsOf(src PlacementSource, credential string, roles []string) map[string]secret.Placement {
	places := make(map[string]secret.Placement, len(roles))
	for _, role := range roles {
		places[role] = src.Stored(credential, role)
	}
	return places
}

// PlacementReport sums up PlacementsOf for the roles a credential could hold. Held and Unsure are
// "role: source" in role order; Causes are what the unreachable places said, each once.
type PlacementReport struct {
	Held, Unsure, Causes []string
}

// Settled reports whether nothing is stored and every place could be asked.
func (r PlacementReport) Settled() bool { return len(r.Held) == 0 && len(r.Unsure) == 0 }

// ReportPlacements sums up places over roles. The causes name files, modes and switches, never a value.
func ReportPlacements(places map[string]secret.Placement, roles []string) PlacementReport {
	var rep PlacementReport
	seen := map[string]bool{}
	for _, role := range roles {
		place := places[role]
		for _, source := range place.Holding {
			rep.Held = append(rep.Held, fmt.Sprintf("%s: %s", role, source))
		}
		for _, source := range place.Unknown {
			rep.Unsure = append(rep.Unsure, fmt.Sprintf("%s: %s", role, source))
		}
		// Several roles usually fail for the same reason; saying it twice would only make the message longer.
		if place.Err == nil {
			continue
		}
		for _, cause := range strings.Split(place.Err.Error(), "\n") {
			if cause != "" && !seen[cause] {
				seen[cause] = true
				rep.Causes = append(rep.Causes, cause)
			}
		}
	}
	return rep
}

// Input errors of CreateCredential. Their text is the whole message; none carries a secret.
var (
	ErrNoProvider = errors.New("choose a provider first")
	ErrNameEmpty  = errors.New("a credential name must not be empty")
	ErrNoStorage  = errors.New("choose where the secrets are kept")
)

// ExistsError reports a credential name that is taken.
type ExistsError struct{ Name string }

func (e *ExistsError) Error() string {
	return fmt.Sprintf("a credential named %q already exists", e.Name)
}

// MissingValueError reports a role of a new credential for which nothing was given: an environment
// variable name when Env, otherwise a secret.
type MissingValueError struct {
	Role string
	Env  bool
}

func (e *MissingValueError) Error() string {
	if e.Env {
		return fmt.Sprintf("%s names no environment variable", e.Role)
	}
	return fmt.Sprintf("%s has no secret", e.Role)
}

// IsInputError reports whether err refuses what a surface passed to CreateCredential, as opposed to a
// failure of the configuration or a store. Such a message is shown as it is.
func IsInputError(err error) bool {
	var exists *ExistsError
	var missing *MissingValueError
	var unknown *UnknownRoleError
	if errors.As(err, &exists) || errors.As(err, &missing) || errors.As(err, &unknown) {
		return true
	}
	return errors.Is(err, ErrNoProvider) || errors.Is(err, ErrNameEmpty) || errors.Is(err, ErrNoStorage)
}

// NewCredential describes a credential to create. Only the saved configuration passed to CreateCredential
// decides what is accepted: the provider's roles (see AcceptedRoles) are exactly the keys of Secrets or
// EnvNames, none missing and none extra.
type NewCredential struct {
	Name, Provider string
	// Type is the storage: keyring, vault or env.
	Type string
	// Secrets holds the value of every role for a keyring or vault credential. They are written in the
	// same commit as the configuration entry.
	Secrets map[string]string
	// EnvNames holds the name of the variable of every role for an env credential.
	EnvNames map[string]string
	// Offer is asked for a passphrase only when a vault secret here would be the vault's very first.
	Offer vault.PassphraseFunc
}

// CreatedCredential is the outcome of CreateCredential.
type CreatedCredential struct {
	// Config is the configuration as saved.
	Config *config.Config
	// Warning is the redacted warning of the vault process hand-over and the connection log, "" for none.
	Warning string
}

// CreateCredential creates a credential of type keyring, vault or env in cfg, which was loaded at rev. A
// keyring or vault credential's secrets are committed with the entry (see CommitSecrets), so a store that
// turns out to be locked leaves neither a stray secret nor a stray entry; an env credential writes names
// only, never a value. cfg is not changed. Input is refused with the errors IsInputError names, the
// configuration with its own, and a file that changed since rev with a *config.ConflictError.
func (s *Service) CreateCredential(cfg *config.Config, rev config.Revision, n NewCredential) (CreatedCredential, error) {
	var res CreatedCredential
	if !contains(cfg.Providers(), n.Provider) {
		return res, ErrNoProvider
	}
	if n.Name == "" {
		return res, ErrNameEmpty
	}
	if _, taken := cfg.Credentials[n.Name]; taken {
		return res, &ExistsError{Name: n.Name}
	}
	if n.Type != config.CredentialTypeKeyring && n.Type != config.CredentialTypeVault &&
		n.Type != config.CredentialTypeEnv {
		return res, ErrNoStorage
	}

	cred := config.Credential{Provider: n.Provider, Type: n.Type}
	roles := AcceptedRoles(cfg, cred)
	given := n.Secrets
	if n.Type == config.CredentialTypeEnv {
		given = n.EnvNames
	}
	var extra []string
	for role := range given {
		if !contains(roles, role) {
			extra = append(extra, role)
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		return res, &UnknownRoleError{Credential: n.Name, Role: extra[0], Provider: n.Provider, Accepted: roles}
	}
	for _, role := range roles {
		if given[role] == "" {
			return res, &MissingValueError{Role: role, Env: n.Type == config.CredentialTypeEnv}
		}
	}
	if n.Type == config.CredentialTypeEnv {
		cred.Values = make(map[string]string, len(roles))
		for _, role := range roles {
			cred.Values[role] = n.EnvNames[role]
		}
	}

	candidate := cfg.Clone()
	if err := candidate.SetCredential(n.Name, cred); err != nil {
		return res, err
	}
	if err := candidate.Validate(); err != nil {
		return res, err
	}

	res.Config = candidate
	if n.Type == config.CredentialTypeEnv {
		warning, err := s.SaveConfig(cfg, candidate, rev)
		if err != nil {
			return CreatedCredential{}, err
		}
		res.Warning = warning
		return res, nil
	}
	warning, err := s.CommitSecrets(candidate, rev, n.Name, n.Type == config.CredentialTypeVault, roles, n.Secrets, n.Offer)
	if err != nil {
		return CreatedCredential{}, err
	}
	if logged := s.RecordConnections(cfg, candidate); logged != "" {
		if warning != "" {
			warning += "; "
		}
		warning += logged
	}
	res.Warning = warning
	return res, nil
}
