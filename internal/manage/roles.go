package manage

import (
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// CredentialProvider is the provider a credential belongs to for display. It is the one the credential
// names or, while it names none, the one every connection using it agrees on: a connection binds the
// credential to a service, and that service has a provider. A credential no connection uses, or one two
// providers disagree about, has none. name may be empty for a credential that is not saved yet; nothing is
// derived then.
//
// The derived provider only helps to show something fitting. It never decides what a credential may hold:
// see AcceptedRoles.
func CredentialProvider(cfg *config.Config, name string, cred config.Credential) string {
	if cred.Provider != "" {
		return cred.Provider
	}
	if name == "" {
		return ""
	}
	derived := ""
	for _, connection := range cfg.Connections {
		if connection.Credential != name {
			continue
		}
		provider := cfg.Services[connection.Service].Provider
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

// OfferedRoles are the secret roles a surface shows for a credential: those of its CredentialProvider, or
// every compiled role while there is none, so a credential written without a provider stays editable.
func OfferedRoles(cfg *config.Config, name string, cred config.Credential) []string {
	provider := CredentialProvider(cfg, name, cred)
	if provider == "" {
		return cfg.SecretRoles()
	}
	return cfg.SecretRolesOf(provider)
}

// AcceptedRoles are the secret roles that may be stored for or removed from a credential. Only the
// provider the credential names counts: its roles when it names one, every compiled role when it names
// none. A provider derived from connections never widens or narrows this.
func AcceptedRoles(cfg *config.Config, cred config.Credential) []string {
	if cred.Provider == "" {
		return cfg.SecretRoles()
	}
	return cfg.SecretRolesOf(cred.Provider)
}

// What RoleState says. The words are the vocabulary every surface shows for where a secret role sits.
const (
	StateStored         = "in system keyring"
	StateStoredVault    = "in the vault"
	StateVaultLocked    = "vault locked"
	StateVaultElsewhere = "locked in this window, unlocked in the vault process"
	StateNoVault        = "no vault is configured for this run"
	StateOverride       = "environment variable, overrides keyring"
	StatePlaintext      = "unencrypted file"
	StateEmpty          = "not stored yet"
	StateKeyringLocked  = "keyring locked"
	StateUnreachable    = "keyring unreachable"
	StateOff            = "keyring switched off"
)

// RoleStateInput is what RoleState needs to place one secret role of a keyring or vault credential.
type RoleStateInput struct {
	// Type is the credential's storage type; vault selects the vault path, anything else the keyring path.
	Type string
	// Credential and Role name the secret. They are only read for a vault credential.
	Credential, Role string
	// Vault is the run's vault, read for a vault credential. Nil means none is configured.
	Vault *vault.Vault
	// Source and Checked are the resolver's last answer for a keyring credential (secret.Resolver.Status).
	Source  secret.Source
	Checked []string
	// ProcessUnlocked reports a locked vault as held open by a vault process elsewhere. A surface that
	// does not show that distinction leaves it false and sees StateVaultLocked.
	ProcessUnlocked bool
}

// RoleState reports where one secret role currently sits, never what it holds. It never asks for a
// passphrase: an encrypted, locked vault answers StateVaultLocked, since its entry cannot be read without
// one. A failure reading the vault is returned as its own text, which the caller redacts as it does any
// error text.
func RoleState(in RoleStateInput) string {
	if in.Type == config.CredentialTypeVault {
		return vaultRoleState(in)
	}
	switch in.Source {
	case secret.SourceStore:
		return StateStored
	case secret.SourceEnv:
		return StateOverride
	case secret.SourcePlaintext:
		return StatePlaintext
	}
	switch secret.StoreStage(in.Checked) {
	case secret.StoreEmpty:
		return StateEmpty
	case secret.StoreLocked:
		return StateKeyringLocked
	case secret.StoreUnavailable, secret.StoreTimedOut:
		return StateUnreachable
	case secret.StoreOff:
		return StateOff
	}
	return string(in.Source)
}

func vaultRoleState(in RoleStateInput) string {
	v := in.Vault
	if v == nil {
		return StateNoVault
	}
	state, err := v.State()
	if err != nil {
		return err.Error()
	}
	switch state {
	case vault.StateAbsent:
		return StateEmpty
	case vault.StateLocked:
		if in.ProcessUnlocked {
			return StateVaultElsewhere
		}
		return StateVaultLocked
	}
	_, found, _, err := v.Get(in.Credential, in.Role, nil)
	if err != nil {
		return err.Error()
	}
	if found {
		return StateStoredVault
	}
	return StateEmpty
}
