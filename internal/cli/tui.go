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
		Short: "Set up connections and edit the configuration in a terminal interface",
		Long: "The editor opens on a dashboard whose primary entry, c, sets up a connection step by step:\n" +
			"provider, service, credential, scope, permissions, and a summary that saves the connection and\n" +
			"can test it right away. Each step offers only what the chosen provider defines, and offers the\n" +
			"configured services and credentials of that provider first, so they are reused instead of\n" +
			"duplicated; the same checks as every other save refuse a step before the next one opens, and a\n" +
			"refused step keeps its input. A new credential keeps its secrets in the system keyring\n" +
			"(recommended), names environment variables, or, after an explicit confirmation, writes them to\n" +
			"an unencrypted file. Secrets are typed masked and never shown. Nothing is written before the\n" +
			"summary is saved: esc cancels at any step, ctrl+b goes back one step, and should the\n" +
			"configuration fail to save, the secrets just stored are removed again.\n\n" +
			"The four sections of the dashboard remain for direct editing. The editor manages services,\n" +
			"credentials, connections, and domain defaults, can test a selected connection, and stores the\n" +
			"secrets of a keyring credential in a masked field. It never displays a stored secret back:\n" +
			"what it shows is which source delivers a role.\n\n" +
			"Every list scrolls within the terminal and shows the position of the selected entry. / filters\n" +
			"a list by the text of its rows, ignoring case; enter keeps the filter and esc clears it. Up/down\n" +
			"move through the entries, pgup/pgdown and home/end jump. Filtering never changes the file.\n\n" +
			"In a form, left/right step through the values of a choice row. / on a choice row opens a\n" +
			"searchable picker that marks the current value and filters as you type, ignoring case; up/down,\n" +
			"pgup/pgdown and home/end move, enter takes the selected value, and esc leaves the row unchanged.\n" +
			"The form points to / on rows with many values.\n\n" +
			"A connection's tools row decides between every tool its permissions allow, which is how a\n" +
			"connection without a tools list behaves and how a new one starts, and only selected tools.\n" +
			"In the second mode space or / on the tool list opens the tools the chosen provider registers,\n" +
			"with their effect; space ticks the selected tool, typing filters, enter keeps the ticks and\n" +
			"esc drops them. Nothing ticked stores an explicit empty list, which offers no tool at all. A\n" +
			"change of provider clears the ticks, because tool IDs belong to their provider.",
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
