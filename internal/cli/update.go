package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/selfupdate"
)

func newUpdateCommand(opts *Options, buildVersion string) *cobra.Command {
	var check, yes bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update qatlas to the latest stable release",
		Long: "Update checks the latest stable GitHub release, verifies the selected archive with its\n" +
			"published SHA-256 checksum, and replaces a direct <prefix>/bin/qatlas installation.\n" +
			"It also refreshes qatlas.1 in the same prefix. Dev builds and symlink installations are\n" +
			"not replaced. Use --check to report availability without changing files.\n\n" +
			"Before it downloads anything, update names the new version and what the replacement does to\n" +
			"running qatlas processes and asks for y/n at the terminal; n or an empty answer changes nothing.\n" +
			"--yes answers y. Without a terminal, and in agent mode, update refuses unless --yes is given;\n" +
			"an agent runs it only after the user approved the update, and then with --yes.\n\n" +
			"A vault process that holds the vault unlocked is locked right before qatlas is replaced: it\n" +
			"would not serve the new program. The command then says so, and 'qatlas vault unlock' unlocks\n" +
			"the vault again. Should the process not lock, the update goes ahead and the warning names its\n" +
			"process id and how to end it.",
		Args: noArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			updater := opts.Updater
			if updater == nil {
				updater = selfupdate.New(buildVersion)
			}
			if !check && !yes && (opts.Agent || !checkInteractive()) {
				return &UsageError{errors.New("update replaces the installed program and needs a confirmation: " +
					"run it at a terminal, or pass --yes; an agent runs it only after the user approved the update")}
			}
			result, err := updater.Check(c.Context())
			if err == nil && !check && result.UpdateAvailable {
				if yes || confirmUpdate(result.Latest) {
					result, err = replaceProgram(c, opts, updater)
				} else {
					fmt.Fprintln(c.ErrOrStderr(), "qatlas: update cancelled, nothing was changed")
				}
			}
			if err != nil {
				var unsupported *selfupdate.UnsupportedInstallationError
				if errors.Is(err, selfupdate.ErrDevelopmentBuild) || errors.As(err, &unsupported) {
					return &UsageError{err}
				}
				return err
			}
			return emit(c, opts, output.Object{Fields: []output.Field{
				{Name: "current", Value: result.Current},
				{Name: "latest", Value: result.Latest},
				{Name: "update_available", Value: result.UpdateAvailable},
				{Name: "updated", Value: result.Updated},
			}})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm the update without asking")
	cmd.Flags().BoolVar(&check, "check", false, "only report whether a newer stable release is available")
	return cmd
}

// replaceProgram installs the release. Once the program is replaced, a running vault process no longer
// passes for it: it refuses every client, a lock included, and keeps the secrets until its idle timeout.
// It is therefore locked while it still can be, right before the replacement, and only here, after the
// confirmation.
func replaceProgram(c *cobra.Command, opts *Options, updater *selfupdate.Client) (selfupdate.Result, error) {
	var note string
	replacing := *updater
	replacing.BeforeReplace = func(ctx context.Context) {
		note = lockVaultProcessOf(ctx, opts, "before qatlas was replaced",
			"run 'qatlas vault unlock' to unlock the vault again")
		if updater.BeforeReplace != nil {
			updater.BeforeReplace(ctx)
		}
	}
	result, err := replacing.Update(c.Context())
	printNote(c, note)
	return result, err
}

// confirmUpdate asks whether to install latest, naming what it does to running processes. A failure to
// read an answer is a no, so nothing is replaced on a guess.
func confirmUpdate(latest string) bool {
	ok, err := readVaultConfirm(selfupdate.Question(latest) + "\nContinue? [y/N] ")
	return err == nil && ok
}
