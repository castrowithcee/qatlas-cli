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
			"The system keyring is the recommended local place for secrets: the credential store the\n" +
			"operating system already provides, Secret Service on Linux (for example GNOME Keyring or\n" +
			"KWallet), the macOS Keychain, or the Windows Credential Manager. It needs no setup and no\n" +
			"exported variable, and qatlas keeps no secret store of its own. On a role of a keyring\n" +
			"credential, s stores the secret there. The role row says in system keyring, not stored yet,\n" +
			"keyring locked, keyring unreachable, or keyring switched off (QATLAS_CREDENTIAL_STORE=none),\n" +
			"and for each blocked case what to do next on this platform. A set variable\n" +
			"QATLAS_<CREDENTIAL>_<ROLE> still wins and the row says environment variable, overrides keyring;\n" +
			"that is the way for CI and containers. p writes an unencrypted file instead, and only after a\n" +
			"warning that has to be confirmed.\n\n" +
			"Every list scrolls within the terminal and shows the position of the selected entry. / filters\n" +
			"a list by the text of its rows, ignoring case; enter keeps the filter and esc clears it. Up/down\n" +
			"move through the entries, pgup/pgdown and home/end jump. Filtering never changes the file.\n\n" +
			"Every provider row, in the forms of services, credentials, and connections and as the first step\n" +
			"of the guided setup, is chosen in the same provider table, however many providers there are:\n" +
			"enter, space, or / on the row opens it. The table lists every provider the row offers with its\n" +
			"name and ID, marks the current one, and always shows the search line, the position of the\n" +
			"selection, and the total. Typing filters by name and ID, ignoring case; up/down, pgup/pgdown and\n" +
			"home/end move; enter takes the selected provider and updates the rows that depend on it, and esc\n" +
			"leaves the provider and every row that depends on it unchanged. In the guided setup, enter goes\n" +
			"on to the next step and esc cancels the setup. A form does not open on a provider row while\n" +
			"another row takes input, so enter on an opened entry still saves it.\n\n" +
			"In a form, left/right step through the values of every other choice row. / on such a row opens\n" +
			"a searchable picker that marks the current value and filters as you type, ignoring case; up/down,\n" +
			"pgup/pgdown and home/end move, enter takes the selected value, and esc leaves the row unchanged.\n" +
			"The form points to / on rows with many values.\n\n" +
			"A connection's tools row decides between every tool its permissions allow, which is how a\n" +
			"connection without a tools list behaves, and only selected tools. In the second mode space or /\n" +
			"on the tool list opens the tools the chosen provider registers, with their effect; space ticks\n" +
			"the selected tool, typing filters, enter keeps the ticks and esc drops them. Nothing ticked\n" +
			"stores an explicit empty list, which offers no tool at all. A tool marked listed only, such as\n" +
			"a high-risk administration tool, is offered only while it is ticked in the second mode; the first\n" +
			"mode never offers it. A change of provider clears the ticks, because tool IDs belong to their\n" +
			"provider.\n\n" +
			"A new connection, in its form and in the guided setup, starts on the provider's recommended\n" +
			"setup profile: a named starting selection that ticks the permissions its tools need, only\n" +
			"selected tools, and exactly the tools it names, all visible and each changeable before saving.\n" +
			"A recommended profile ticks reads only, unless the provider states why a change is safe, as\n" +
			"Telegram does for sending to the configured chat. The profile row shows the profile the ticks\n" +
			"match, or custom after a change by hand; choosing another profile replaces the ticks at once\n" +
			"while they are a profile's, and asks first once they were changed by hand or belong to a saved\n" +
			"connection. A saved connection opens and saves as it is; a profile is never applied to it on its\n" +
			"own. A profile is a starting selection, not a role: the configuration keeps only permissions\n" +
			"and the concrete tool IDs, so a tool a later version adds joins no saved connection, and no\n" +
			"local tick narrows what the credential itself may do at the provider.",
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
