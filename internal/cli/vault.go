package cli

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// readVaultPassphrase is the terminal prompt every vault passphrase is asked with: the offer of the
// vault's very first secret and 'vault unlock' both go through it. It is a variable only so a test can
// simulate a terminal's answer, or its absence, without opening a real one; a real run always leaves it at
// vault.ReadPassphrase.
var readVaultPassphrase vault.PassphraseFunc = vault.ReadPassphrase

func newVaultCommand(opts *Options, _ *capability.Registry) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vault",
		Short: "Inspect and unlock the vault a credential of type vault reads from",
		Long: "The vault is a directory named vault beside the configuration file, unencrypted or\n" +
			"encrypted to a passphrase. 'qatlas credential set' fills it for a credential of type vault, and\n" +
			"offers a passphrase the first time it stores a secret there.\n\n" +
			"An encrypted vault needs its passphrase to answer a read, a delete, or 'vault unlock', and asks\n" +
			"for it on the terminal, never as a command line argument, an environment variable, or a file.\n" +
			"Without a terminal to ask on, such as an agent talking to qatlas over MCP, such an access fails\n" +
			"with the code vault-locked; a person runs 'qatlas vault unlock' to open it, or presses ctrl+l in\n" +
			"'qatlas tui'. Storing a secret needs no passphrase, even while the vault is locked: it is queued\n" +
			"and merged in on the next unlock.\n\n" +
			"Unlocking only lasts for the current process: the next qatlas invocation asks again, until a\n" +
			"long-lived vault process exists to hold it open. No command ever shows a stored secret back.",
		Args: noArgs,
		RunE: func(c *cobra.Command, _ []string) error { return c.Help() },
	}

	status := &cobra.Command{
		Use:   "status",
		Short: "Show whether the vault exists, is encrypted, and is unlocked",
		Long: "Reports the vault's state (absent, unencrypted, locked, or unlocked), how many credentials it\n" +
			"holds, and how many entries are queued in pending, added or changed while it was locked. The\n" +
			"entry count is unknown while the vault is locked: counting it needs the passphrase, the same\n" +
			"way reading a secret does. It asks for no passphrase and shows no secret value.",
		Args: noArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runVaultStatus(c, opts)
		},
	}

	unlock := &cobra.Command{
		Use:   "unlock",
		Short: "Unlock the vault for this process and merge its pending entries",
		Long: "Asks for the vault's passphrase on the terminal and, once it opens the vault, merges every\n" +
			"entry that was queued in pending while it was locked. The vault stays unlocked only for this\n" +
			"process: it is asked again on the next qatlas invocation. Run against a vault that is not\n" +
			"encrypted and locked, it asks for nothing and just reports the vault's status.",
		Args: noArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runVaultUnlock(c, opts)
		},
	}

	cmd.AddCommand(status, unlock)
	return cmd
}

// vaultOf returns the vault this run resolves credentials from. It is nil only for a resolver a test built
// directly with secret.NewWith, which never happens in a real qatlas invocation.
func vaultOf(opts *Options) (*vault.Vault, error) {
	secrets, err := opts.resolver()
	if err != nil {
		return nil, err
	}
	v := secrets.Vault()
	if v == nil {
		return nil, errors.New("no vault is configured for this run")
	}
	return v, nil
}

func runVaultStatus(c *cobra.Command, opts *Options) error {
	v, err := vaultOf(opts)
	if err != nil {
		return err
	}
	status, err := v.Status()
	if err != nil {
		return classifyUserError(err)
	}
	return emit(c, opts, vaultStatusObject(status))
}

func runVaultUnlock(c *cobra.Command, opts *Options) error {
	v, err := vaultOf(opts)
	if err != nil {
		return err
	}

	state, err := v.State()
	if err != nil {
		return classifyUserError(err)
	}
	if state != vault.StateLocked {
		// Nothing needs a passphrase: an absent or unencrypted vault has none, and one already unlocked in
		// this process stays that way. Reporting the status is more useful than asking for nothing.
		return runVaultStatus(c, opts)
	}

	passphrase, err := readVaultPassphrase("vault passphrase: ")
	if err != nil {
		if errors.Is(err, vault.ErrNoTerminal) {
			// Same code and exit as a locked read that could not ask for a passphrase either: unlocking
			// the vault itself is exactly that access, just without one particular secret behind it.
			return &secret.VaultLockedError{}
		}
		return err
	}
	merged, err := v.Unlock(passphrase)
	if err != nil {
		if errors.Is(err, vault.ErrWrongPassphrase) {
			return &UsageError{err}
		}
		return err
	}

	fmt.Fprintf(c.OutOrStdout(), "the vault is unlocked; merged %d pending %s\n", merged, plural(merged, "entry", "entries"))
	return nil
}

func plural(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

func vaultStatusObject(status vault.Status) output.Object {
	entries := "unknown, locked"
	if status.Entries >= 0 {
		entries = strconv.Itoa(status.Entries)
	}
	fields := []output.Field{
		{Name: "state", Value: string(status.State)},
		{Name: "entries", Value: entries},
		{Name: "pending", Value: int64(status.Pending)},
	}
	if status.Warning != "" {
		fields = append(fields, output.Field{Name: "warning", Value: status.Warning})
	}
	return output.Object{Fields: fields}
}
