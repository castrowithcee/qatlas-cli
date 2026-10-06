package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/castrowithcee/qatlas-cli/internal/release"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// releaseKeys returns the public keys a vault process requires a release to be signed with before it hands
// the vault over to a successor from it: the keys compiled into the program. It is a variable only so that a
// test of this package can trust its own key in the processes it starts; nothing at run time changes it.
var releaseKeys = release.TrustedKeys

// errHandoverNeedsEncryption refuses the update behaviour of a vault without a passphrase.
var errHandoverNeedsEncryption = errors.New("the update behaviour is stored in an encrypted vault: without a " +
	"passphrase nobody could manage it; run 'qatlas vault encrypt' first")

func newVaultHandoverCommand(opts *Options) *cobra.Command {
	return &cobra.Command{
		Use:   "handover [handover|lock]",
		Short: "Show or set what 'qatlas update' does with an unlocked vault",
		Long: "The update behaviour controls whether 'qatlas update' hands an unlocked vault over to the new\n" +
			"program or locks it. handover, the default, keeps the vault unlocked across the update; lock locks\n" +
			"it the way an update always did. Without an argument the command shows the current value; with\n" +
			"handover or lock it sets it. Windows has no handover: there an update always locks the vault process,\n" +
			"and this setting has no effect.\n\n" +
			"The setting is stored authenticated inside the encrypted vault as settings.age, never in\n" +
			"config.yaml, so only a person with the passphrase can change it. A settings.age that was not\n" +
			"written by this vault, or is not understood, reads as lock and is reported in a warning. Both\n" +
			"forms need an interactive terminal and ask for the passphrase every time, like every 'vault'\n" +
			"command but status, unlock, and lock; without a terminal they fail with admin-required, with a\n" +
			"wrong passphrase with usage, before anything is read or written. A vault that is not encrypted\n" +
			"has no place for the setting and is refused. 'qatlas vault passphrase' keeps the setting and\n" +
			"'qatlas vault decrypt' removes it.",
		Args: atMostOneArg("update behaviour"),
		RunE: func(c *cobra.Command, args []string) error {
			return runVaultHandover(c, opts, args)
		},
	}
}

func runVaultHandover(c *cobra.Command, opts *Options, args []string) error {
	var set vault.UpdateBehaviour
	if len(args) == 1 {
		var err error
		if set, err = vault.ParseUpdateBehaviour(args[0]); err != nil {
			return &UsageError{err}
		}
	}
	if err := requireAdmin(opts); err != nil {
		return err
	}
	v, err := vaultOf(opts)
	if err != nil {
		return err
	}
	state, err := v.State()
	if err != nil {
		return classifyUserError(err)
	}
	if state != vault.StateUnlocked {
		return &UsageError{errHandoverNeedsEncryption}
	}
	if set != "" {
		if err := v.SetUpdateBehaviour(set); err != nil {
			return classifyUserError(err)
		}
		fmt.Fprintf(c.OutOrStdout(), "update behaviour: %s\n", set)
		return nil
	}
	current, err := v.UpdateBehaviour()
	if errors.Is(err, vault.ErrSettingsUntrusted) {
		printNote(c, "warning: "+err.Error())
	} else if err != nil {
		return classifyUserError(err)
	}
	fmt.Fprintf(c.OutOrStdout(), "update behaviour: %s\n", current)
	return nil
}
