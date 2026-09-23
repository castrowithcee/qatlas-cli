package cli

import (
	"context"
	"errors"
	"os"

	"github.com/spf13/cobra"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/tui"
)

func newTUICommand(opts *Options, reg *capability.Registry) *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Edit the configuration in a terminal interface",
		Long: "The editor manages services, credentials, connections, and domain defaults, can test a\n" +
			"selected connection, and stores the secrets of a keyring credential in a masked field. It\n" +
			"never displays a stored secret back: what it shows is which source delivers a role.\n\n" +
			"Every list scrolls within the terminal and shows the position of the selected entry. / filters\n" +
			"a list by the text of its rows, ignoring case; enter keeps the filter and esc clears it. Up/down\n" +
			"move through the entries, pgup/pgdown and home/end jump. Filtering never changes the file.\n\n" +
			"In a form, left/right step through the values of a choice row. / on a choice row opens a\n" +
			"searchable picker that marks the current value and filters as you type, ignoring case; up/down,\n" +
			"pgup/pgdown and home/end move, enter takes the selected value, and esc leaves the row unchanged.\n" +
			"The form points to / on rows with many values.",
		Args: noArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if opts.Agent {
				return &UsageError{errors.New("the terminal editor cannot run in agent mode")}
			}
			path, err := config.Path(opts.Config)
			if err != nil {
				return err
			}
			// The editor resolves secrets through the same cascade every command uses; it owns no
			// resolution path of its own.
			secrets, err := opts.resolver()
			if err != nil {
				return err
			}
			store := config.NewStore(path, reg)
			return classifyUserError(tui.Run(
				store, connectionTester(store, opts, reg), secrets, opts.Redactor, os.Stdin, os.Stdout))
		},
	}
}

// connectionTester binds the editor to the shared core function. The editor itself knows no provider.
func connectionTester(store *config.Store, opts *Options, reg *capability.Registry) tui.Tester {
	return func(ctx context.Context, connection string) (provider.Class, error) {
		cfg, err := store.Load()
		if err != nil {
			return "", err
		}
		resolved, err := cfg.Resolve(connection, "")
		if err != nil {
			return "", err
		}
		secrets, err := opts.resolver()
		if err != nil {
			return "", err
		}
		return reg.TestConnection(ctx, resolved, secrets, opts.Redactor)
	}
}
