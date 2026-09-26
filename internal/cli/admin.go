package cli

import (
	"errors"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// checkInteractive reports whether a person can be at this process's terminal. requireAdmin and
// requireAdminTerminal call it instead of vault.Interactive directly, so a test can simulate a terminal's
// presence, or its absence, without opening a real one. A real run always leaves it at vault.Interactive,
// exactly the rule 'qatlas vault' itself uses for ReadPassphrase and ReadConfirm.
var checkInteractive = vault.Interactive

// requireAdminTerminal is the half of the admin check that every CLI management command needs: a person
// has to be at an interactive terminal, checked before the command reads a secret from standard input,
// opens a file, or touches the credential store or the vault. 'qatlas vault passphrase' and 'qatlas vault
// decrypt' call only this, because asking for and verifying the vault's current passphrase is already the
// first thing each of them does; asking again through requireAdmin would ask the same question twice.
func requireAdminTerminal() error {
	if !checkInteractive() {
		return &UsageError{&application.AdminRequiredError{}}
	}
	return nil
}

// requireAdmin is the admin check every other CLI management command runs before it has any effect:
// storing or removing a credential's secret of any type, keyring included, and switching the vault's
// encryption on or carrying credentials.yaml into it with 'qatlas vault migrate'. It refuses a command with
// no interactive terminal outright, the same way requireAdminTerminal does, and, only once a vault exists
// and is encrypted, additionally asks for its passphrase on the terminal and unlocks it: a wrong passphrase
// aborts before the command's own effect, and a right one leaves the vault unlocked for the rest of this
// process, so the write that follows goes straight into it instead of a pending entry nobody has opened yet.
//
// This runs for every management command whatever credential it targets, because the vault's passphrase
// here is proof of an admin session, not merely the key to one particular secret.
func requireAdmin(opts *Options) error {
	if err := requireAdminTerminal(); err != nil {
		return err
	}
	secrets, err := opts.resolver()
	if err != nil {
		return err
	}
	v := secrets.Vault()
	if v == nil {
		// A resolver built directly with secret.NewWith, which only a test does, has no vault to lock.
		return nil
	}
	state, err := v.State()
	if err != nil {
		return classifyUserError(err)
	}
	if state != vault.StateLocked {
		return nil
	}

	passphrase, err := readVaultPassphrase("vault passphrase: ")
	if err != nil {
		if errors.Is(err, vault.ErrNoTerminal) {
			return &UsageError{&application.AdminRequiredError{}}
		}
		return err
	}
	if _, err := v.Unlock(passphrase); err != nil {
		if errors.Is(err, vault.ErrWrongPassphrase) {
			return &UsageError{err}
		}
		return classifyUserError(err)
	}
	return nil
}
