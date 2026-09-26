package cli

import (
	"context"
	"errors"

	"github.com/spf13/cobra"

	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/selfupdate"
)

func newUpdateCommand(opts *Options, buildVersion string) *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update qatlas to the latest stable release",
		Long: "Update checks the latest stable GitHub release, verifies the selected archive with its\n" +
			"published SHA-256 checksum, and replaces a direct <prefix>/bin/qatlas installation.\n" +
			"It also refreshes qatlas.1 in the same prefix. Dev builds and symlink installations are\n" +
			"not replaced. Use --check to report availability without changing files.\n\n" +
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
			var (
				result selfupdate.Result
				err    error
			)
			if check {
				result, err = updater.Check(c.Context())
			} else {
				// Once the program is replaced, a running vault process no longer passes for it: it
				// refuses every client, a lock included, and keeps the secrets until its idle timeout.
				// It is therefore locked while it still can be, right before the replacement.
				var note string
				replacing := *updater
				replacing.BeforeReplace = func(ctx context.Context) {
					note = lockVaultProcessOf(ctx, opts, "before qatlas was replaced",
						"run 'qatlas vault unlock' to unlock the vault again")
					if updater.BeforeReplace != nil {
						updater.BeforeReplace(ctx)
					}
				}
				result, err = replacing.Update(c.Context())
				printNote(c, note)
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
	cmd.Flags().BoolVar(&check, "check", false, "only report whether a newer stable release is available")
	return cmd
}
