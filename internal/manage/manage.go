// Package manage holds the write completion every management surface shares: the terminal editor, the
// browser interface and the management commands of the command line all change the configuration, store
// secrets, keep a running vault process in step with the vault, and log connection changes the same way.
// Service is that one copy. It loads the configuration with its revision and saves it only if the file is
// unchanged, commits a credential's secrets together with its configuration entry and rolls the secrets
// back when the commit fails, hands vault changes on to the vault process or locks it, and appends the
// connection log. A warning that comes back from the vault process or the log is passed through the
// service's redaction, so no surface prints a secret value by way of one.
package manage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/connlog"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// Secrets is what committing a batch of new secrets needs from a resolver: enough to write them to the
// system keyring or the vault, and to remove again whatever a partial commit already wrote. A caller's
// *secret.Resolver satisfies it, and so does every fake its tests inject in its place.
type Secrets interface {
	Set(credential, role, value string) error
	Delete(credential, role string) ([]secret.Source, error)
	Vault() *vault.Vault
	SetVault(credential, role, value string, offer vault.PassphraseFunc) error
	DeleteVault(credential, role string) error
}

// Service performs the shared write completion for one surface. It never hands out its store, vault or
// resolver. Its methods are safe for the concurrent use the underlying store and vault already allow.
type Service struct {
	store     *config.Store
	secrets   Secrets
	surface   string
	redactor  *redact.Redactor
	processOK bool
}

// New returns the service for surface (one of the connlog.Surface constants). secrets may be nil for a
// caller that never stores secrets or reaches a vault; redactor may be nil to leave warnings unchanged. The
// vault process is used where vaultproc.Supported says it runs.
func New(store *config.Store, secrets Secrets, surface string, redactor *redact.Redactor) *Service {
	return &Service{store: store, secrets: secrets, surface: surface, redactor: redactor, processOK: vaultproc.Supported}
}

// ForVault returns the service for surface over exactly the vault v, for a caller that reaches no secret
// store beyond it: it syncs and locks the vault process and logs connections, and its CommitSecrets writes
// nothing and fails. store may be nil for a caller that only syncs or locks the vault process. v may be nil.
func ForVault(store *config.Store, v *vault.Vault, surface string, redactor *redact.Redactor) *Service {
	return New(store, vaultOnly{v: v}, surface, redactor)
}

// vaultOnly is the Secrets of a service built by ForVault: it names its vault and stores nothing.
type vaultOnly struct{ v *vault.Vault }

var errNoSecretStore = errors.New("no secret store is configured for this service")

func (o vaultOnly) Set(string, string, string) error               { return errNoSecretStore }
func (o vaultOnly) Delete(string, string) ([]secret.Source, error) { return nil, errNoSecretStore }
func (o vaultOnly) Vault() *vault.Vault                            { return o.v }
func (o vaultOnly) SetVault(string, string, string, vault.PassphraseFunc) error {
	return errNoSecretStore
}
func (o vaultOnly) DeleteVault(string, string) error { return errNoSecretStore }

// WithVaultProcessSupport returns a copy of s that treats the vault process as supported or not, in place
// of vaultproc.Supported. A caller with its own switch, such as a test lever, passes it here.
func (s *Service) WithVaultProcessSupport(supported bool) *Service {
	c := *s
	c.processOK = supported
	return &c
}

// WithSecrets returns a copy of s that stores and removes secrets through secrets, for a caller that was
// built without any and supplies a stand-in whose operations say why they cannot.
func (s *Service) WithSecrets(secrets Secrets) *Service {
	c := *s
	c.secrets = secrets
	return &c
}

// Load returns the configuration as the file holds it, with the revision the next SaveConfig or
// CommitSecrets must be based on. The errors are those of config.Store.LoadVersioned, including
// *config.NotFoundError for a file that does not exist.
func (s *Service) Load() (*config.Config, config.Revision, error) {
	return s.store.LoadVersioned()
}

// NewConfig returns an empty configuration for the store's providers.
func (s *Service) NewConfig() *config.Config { return s.store.New() }

// RevisionOf returns the revision of exactly the bytes cfg is saved as, without reading the file. It is
// only meaningful for a configuration the service just saved.
func (s *Service) RevisionOf(cfg *config.Config) (config.Revision, error) {
	return s.store.RevisionOf(cfg)
}

// Path returns the path of the configuration file.
func (s *Service) Path() string { return s.store.Path() }

// SaveConfig writes after only if the file still has revision base (see config.Store.SaveIfUnchanged) and,
// once saved, logs the connections that differ between before and after. The error is the save's, a
// *config.ConflictError (errors.Is(err, config.ErrConflict)) when the file changed, and is returned as it
// is; the warning is the redacted failure of the log, which never undoes the save, and is "" otherwise.
func (s *Service) SaveConfig(before, after *config.Config, base config.Revision) (warning string, err error) {
	if err := s.store.SaveIfUnchanged(after, base); err != nil {
		return "", err
	}
	return s.RecordConnections(before, after), nil
}

// CommitSecrets stores the secrets of roles named in values for credential, in the vault when toVault,
// otherwise the system keyring, and then saves cfg. cfg must already have the credential entry set and have
// passed cfg.Validate(), so the only way the save can still fail is the file itself; the secrets written
// for it are rolled back then, so no store entry is left without a credential that names it.
//
// base is the revision cfg was derived from (see Load). The commit is one config.Store.Transact: if the
// file no longer has base, nothing is written, no secret and no configuration, and the error is a
// *config.ConflictError. Only secrets this call wrote are ever rolled back. A role missing from values is
// skipped. offer is asked for a passphrase only when a vault secret here would be the vault's very first.
//
// Once the configuration is saved, a vault credential's secrets are handed on to a vault process that holds
// the vault unlocked. The redacted warning that returns, if any, is no reason to roll anything back.
func (s *Service) CommitSecrets(cfg *config.Config, base config.Revision, credential string, toVault bool,
	roles []string, values map[string]string, offer vault.PassphraseFunc) (warning string, err error) {
	// A forward credential's fields are checked before anything is written. The message never carries a value.
	if cred, ok := cfg.Credentials[credential]; ok && cred.Forward {
		for _, role := range roles {
			if value, ok := values[role]; ok {
				if err := cred.CheckForwardValue(role, value); err != nil {
					return "", fmt.Errorf("credential %s: %v", credential, err)
				}
			}
		}
	}
	var written []string
	// The revision is checked before the first secret is written and the lock is held until the configuration
	// is saved, so a conflict is found before any secret of another change could be overwritten, and a
	// rollback never races a second writer that uses the same credential name.
	err = s.store.Transact(base, func(save func(*config.Config) error) error {
		for _, role := range roles {
			value, ok := values[role]
			if !ok {
				continue
			}
			var writeErr error
			if toVault {
				writeErr = s.secrets.SetVault(credential, role, value, offer)
			} else {
				writeErr = s.secrets.Set(credential, role, value)
			}
			if writeErr != nil {
				return s.rollback(credential, toVault, written,
					fmt.Errorf("storing the secret for %s.%s: %w", credential, role, writeErr))
			}
			written = append(written, role)
		}
		if err := save(cfg); err != nil {
			return s.rollback(credential, toVault, written, err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if !toVault || len(written) == 0 {
		return "", nil
	}
	return s.SyncVaultProcess(context.Background(), func(ctx context.Context, client *vaultproc.Client) error {
		for _, role := range written {
			if err := client.Set(ctx, credential, role, values[role]); err != nil {
				return err
			}
		}
		return nil
	}), nil
}

// rollback removes the secrets a failed commit wrote and says which ones, if any, could not be removed
// again, so nothing is silently left behind.
func (s *Service) rollback(credential string, toVault bool, roles []string, cause error) error {
	var left []string
	for _, role := range roles {
		var err error
		if toVault {
			err = s.secrets.DeleteVault(credential, role)
		} else {
			_, err = s.secrets.Delete(credential, role)
		}
		if err != nil && !errors.Is(err, secret.ErrNoEntry) {
			left = append(left, role)
		}
	}
	if len(left) > 0 {
		return fmt.Errorf("%w; the secrets already stored for %s (%s) could not be removed again, remove "+
			"them with 'qatlas credential delete %s <role>'", cause, credential, strings.Join(left, ", "), credential)
	}
	return cause
}

// SyncVaultProcess hands one change already written to the vault on to the vault process that holds it
// unlocked outside this run (see vaultproc.SyncChange). An unsupported platform, no vault, an
// unencrypted vault or no process running needs nothing said and returns "". Any other failure returns the
// redacted warning, which starts with "warning: " and carries no secret value; the vault already holds the
// change and keeps it. The surface adds its own prefix and decides where the text goes.
func (s *Service) SyncVaultProcess(ctx context.Context, change func(context.Context, *vaultproc.Client) error) string {
	v := s.vault()
	if !s.processOK || v == nil {
		return ""
	}
	if err := vaultproc.SyncChange(ctx, v, change); err != nil {
		return s.redact(fmt.Sprintf("warning: the vault holds the change, but the vault process that holds it "+
			"unlocked could not take it and still answers with what it held before: %s; %s",
			err, secret.VaultProcessRemedy(err)))
	}
	return ""
}

// LockVaultProcess locks the vault process that holds the vault unlocked, ahead of a change after which it
// would serve what the vault no longer holds (see vaultproc.LockProcess); the change goes ahead either
// way. why says what it was locked for, in the words of a sentence part such as "before the vault was
// decrypted", or "" to leave it out; next is what to do once it is locked. The result is "" when there was
// nothing to lock, "the vault process was locked[ why]; next" when it was, and the redacted warning
// "warning: the vault process could not be locked[ why]: ..." when it refused to be locked.
func (s *Service) LockVaultProcess(ctx context.Context, why, next string) string {
	v := s.vault()
	if !s.processOK || v == nil {
		return ""
	}
	if why != "" {
		why = " " + why
	}
	locked, err := vaultproc.LockProcess(ctx, v)
	switch {
	case err != nil:
		return s.redact(fmt.Sprintf("warning: the vault process could not be locked%s: %s; it keeps the "+
			"secrets it holds until it locks itself, unless you %s", why, err, secret.EndVaultProcess(err)))
	case locked:
		return s.redact(fmt.Sprintf("the vault process was locked%s; %s", why, next))
	default:
		return ""
	}
}

// RecordConnections logs one entry per connection that differs between before and after, once after was
// saved to the configuration file, and returns the redacted warning to show when that failed ("" otherwise,
// also when nothing differs). A log failure never undoes the save.
func (s *Service) RecordConnections(before, after *config.Config) string {
	if len(connlog.Changes(before, after)) == 0 {
		return ""
	}
	recorder := connlog.NewRecorder(s.surface, s.store.Path(), after.LogRetentionDays(), s.vault(), s.processOK)
	if err := recorder.Record(before, after); err != nil {
		return s.redact("warning: " + err.Error())
	}
	return ""
}

// vault returns the vault of the secrets, nil without secrets.
func (s *Service) vault() *vault.Vault {
	if s.secrets == nil {
		return nil
	}
	return s.secrets.Vault()
}

// redact removes every registered secret value from text; without a redactor text is unchanged.
func (s *Service) redact(text string) string {
	if s.redactor == nil {
		return text
	}
	return s.redactor.Apply(text)
}
