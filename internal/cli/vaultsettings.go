package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// errSettingsNeedEncryption refuses the settings of a vault without a passphrase.
var errSettingsNeedEncryption = errors.New("the idle timeout, the admin session timeout and the log " +
	"retention are stored in an encrypted vault: without a passphrase nobody could manage them; run " +
	"'qatlas vault encrypt' first")

// errRetentionNeeded is the refusal to replace an untrusted settings.age without a stated retention: the
// safe settings never truncate a log, so a retention would have to be invented, and a retention deletes data.
var errRetentionNeeded = errors.New("settings.age is not understood; set --retention-days with the new " +
	"values, since replacing it must not choose how many days of log history are kept")

func newVaultSettingsCommand(opts *Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "settings",
		Short: "Show or set the vault's idle timeout, admin session timeout, and log retention",
		Long: "The idle timeout is how long a vault process that holds the vault unlocked does so without a read\n" +
			"before it locks itself (12h unless set). The admin session timeout is how long a proven passphrase\n" +
			"keeps authorising managing actions without activity (10m unless set); 0s asks for the passphrase on\n" +
			"every managing action. The log retention is how many days of the invocation log are kept (90\n" +
			"unless set); an older day is removed on the next invoke. Durations are Go durations such as 12h or\n" +
			"30m.\n\n" +
			"Without a flag the command shows the three values; with --idle-timeout, --admin-timeout, or\n" +
			"--retention-days it sets exactly those and keeps the others, including the update behaviour of\n" +
			"'qatlas vault handover'. The idle timeout and the retention must be positive and the admin session\n" +
			"timeout must not be negative; nothing is written for an invalid value.\n\n" +
			"The settings are stored authenticated inside the encrypted vault as settings.age, never in\n" +
			"config.yaml, so only a person with the passphrase can change them. A settings.age that was not\n" +
			"written by this vault, or is not understood, shows the safe values (updates lock the vault, every\n" +
			"managing action asks for the passphrase, no log is truncated) in a warning, and setting then\n" +
			"replaces it and needs --retention-days. Both forms need an interactive terminal and ask for the\n" +
			"passphrase every time, like every 'vault' command but status, unlock, and lock; without a terminal\n" +
			"they fail with admin-required, with a wrong passphrase with usage, before anything is read or\n" +
			"written. A vault that is not encrypted has no place for the settings and is refused.\n" +
			"'qatlas vault passphrase' keeps the settings and 'qatlas vault decrypt' removes them.",
		Args: noArgs,
		RunE: func(c *cobra.Command, _ []string) error { return runVaultSettings(c, opts) },
	}
	cmd.Flags().Duration("idle-timeout", 0, "idle time before the vault locks itself")
	cmd.Flags().Duration("admin-timeout", 0, "idle time of an admin session; 0s asks every time")
	cmd.Flags().Int("retention-days", 0, "days of the invocation log to keep")
	return cmd
}

func runVaultSettings(c *cobra.Command, opts *Options) error {
	flags := c.Flags()
	idleSet, adminSet, retentionSet := flags.Changed("idle-timeout"), flags.Changed("admin-timeout"),
		flags.Changed("retention-days")
	idle, _ := flags.GetDuration("idle-timeout")
	admin, _ := flags.GetDuration("admin-timeout")
	retention, _ := flags.GetInt("retention-days")
	setting := idleSet || adminSet || retentionSet

	if setting {
		// Invalid values are refused before the passphrase is asked for.
		probe := vault.DefaultSettings()
		if idleSet {
			probe.IdleTimeout = idle
		}
		if adminSet {
			probe.AdminTimeout = admin
		}
		if retentionSet {
			probe.RetentionDays = retention
		}
		if err := probe.Validate(); err != nil {
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
		return &UsageError{errSettingsNeedEncryption}
	}
	current, err := v.Settings()
	untrusted := errors.Is(err, vault.ErrSettingsUntrusted)
	if err != nil && !untrusted {
		return classifyUserError(err)
	}
	if !setting {
		if untrusted {
			printNote(c, "warning: "+err.Error())
		}
		printSettings(c, current)
		return nil
	}
	if untrusted && !retentionSet {
		return &UsageError{errRetentionNeeded}
	}
	if untrusted {
		// The safe settings never truncate; every value not given stays as safe as it was.
		current.RetentionDays = 0
	}
	if idleSet {
		current.IdleTimeout = idle
	}
	if adminSet {
		current.AdminTimeout = admin
	}
	if retentionSet {
		current.RetentionDays = retention
	}
	if err := v.SetSettings(current); err != nil {
		return classifyUserError(err)
	}
	printSettings(c, current)
	return nil
}

func printSettings(c *cobra.Command, s vault.Settings) {
	fmt.Fprintf(c.OutOrStdout(), "idle timeout: %s\nadmin timeout: %s\nretention days: %d\n",
		vault.FormatTimeout(s.IdleTimeout), vault.FormatTimeout(s.AdminTimeout), s.RetentionDays)
}
