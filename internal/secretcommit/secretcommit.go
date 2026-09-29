// Package secretcommit holds the one commit boundary a new credential's secrets and its configuration
// entry must share, wherever they are typed in: the guided setup of internal/tui and the browser credential
// form of internal/web both create a credential and its first secrets in the same breath, and both must
// leave the same thing behind on a failure. Secrets go first, the configuration save is the commit point,
// and a failure after some secrets were written rolls those back so no store entry is left without a
// credential that names it in the configuration file.
package secretcommit

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultmigrate"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// Secrets is what committing a batch of new secrets needs from a resolver: enough to write them to the
// system keyring or the vault, and to remove again whatever a partial commit already wrote. *secret.Resolver
// satisfies it, and so does every fake a caller's own tests already inject in its place.
type Secrets interface {
	Set(credential, role, value string) error
	Delete(credential, role string) ([]secret.Source, error)
	Vault() *vault.Vault
	SetVault(credential, role, value string, offer vault.PassphraseFunc) error
	DeleteVault(credential, role string) error
}

// Commit stores the secrets of roles named in values for credential, in the vault when toVault, otherwise
// the system keyring, and then saves cfg with store. cfg must already have the credential entry set (and
// must already have passed cfg.Validate()), so the only way store.Save can still fail is the file itself; the
// secrets written for it are rolled back then, so no store entry is left without a credential that names it.
//
// A role missing from values is skipped: a credential started without every role filled in yet writes only
// what it was given. offer is asked for a passphrase only when a vault secret here would be the vault's very
// first, exactly as vault.Vault.Set defines it.
//
// Once the configuration is saved, a new vault credential's secrets are handed on to a vault process that
// holds the vault unlocked outside this run, the same way 'qatlas credential set' already does for each one
// it writes. The warning that returns, if any, is not a reason to roll anything back: the vault and the
// configuration already agree by then.
func Commit(store *config.Store, secrets Secrets, cfg *config.Config, credential string, toVault bool,
	roles []string, values map[string]string, offer vault.PassphraseFunc) (warning string, err error) {
	var written []string
	for _, role := range roles {
		value, ok := values[role]
		if !ok {
			continue
		}
		var writeErr error
		if toVault {
			writeErr = secrets.SetVault(credential, role, value, offer)
		} else {
			writeErr = secrets.Set(credential, role, value)
		}
		if writeErr != nil {
			return "", rollback(secrets, credential, toVault, written,
				fmt.Errorf("storing the secret for %s.%s: %w", credential, role, writeErr))
		}
		written = append(written, role)
	}

	if err := store.Save(cfg); err != nil {
		return "", rollback(secrets, credential, toVault, written, err)
	}
	if !toVault || len(written) == 0 {
		return "", nil
	}

	warning = syncVaultProcess(secrets.Vault(), func(ctx context.Context, client *vaultproc.Client) error {
		for _, role := range written {
			if err := client.Set(ctx, credential, role, values[role]); err != nil {
				return err
			}
		}
		return nil
	})
	return warning, nil
}

// rollback removes the secrets a failed commit wrote and says which ones, if any, could not be removed
// again, so nothing is silently left behind.
func rollback(secrets Secrets, credential string, toVault bool, roles []string, cause error) error {
	var left []string
	for _, role := range roles {
		var err error
		if toVault {
			err = secrets.DeleteVault(credential, role)
		} else {
			_, err = secrets.Delete(credential, role)
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

// syncVaultProcess mirrors internal/tui/vaultsettings.go's own helper of the same name and internal/cli's
// own of the same shape: both are thin wrappers around vaultmigrate.SyncChange for their own return type,
// and this one is this package's.
func syncVaultProcess(v *vault.Vault, change func(context.Context, *vaultproc.Client) error) string {
	if !vaultproc.Supported || v == nil {
		return ""
	}
	if err := vaultmigrate.SyncChange(context.Background(), v, change); err != nil {
		return fmt.Sprintf("warning: the vault holds the change, but the vault process that holds it "+
			"unlocked could not take it and still answers with what it held before: %s; %s",
			err, secret.VaultProcessRemedy(err))
	}
	return ""
}
