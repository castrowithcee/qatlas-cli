package cli

import (
	"context"
	"errors"
	"os"

	"github.com/spf13/cobra"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/selfupdate"
	"github.com/castrowithcee/qatlas-cli/internal/tui"
)

// noUpdateCheck is the environment variable that keeps the editor from looking for a newer release.
const noUpdateCheck = "QATLAS_NO_UPDATE_CHECK"

func newTUICommand(opts *Options, reg *capability.Registry, buildVersion string) *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Set up connections and edit the configuration in a terminal interface",
		Long: "The editor is one screen: a sidebar with the four sections 1 Services, 2 Credentials,\n" +
			"3 Connections, and 4 Defaults, and beside it a workspace with the list, form, or setup step of\n" +
			"the active section. From 80 columns the sidebar stands on the left; a narrower terminal shows the\n" +
			"sections in one navigation line above the workspace. Below 40x12 the editor asks for a larger\n" +
			"terminal and keeps everything as it was until it gets one.\n\n" +
			"The editor opens with the focus on the sidebar. up/down (or j/k) choose a section and show its\n" +
			"list at once; enter, right, or tab move the focus into the list, and left, tab, or esc move it\n" +
			"back. 1-4 open a section directly from the sidebar or a list; in a form, where digits are text,\n" +
			"alt+1-4 do the same. In a list, / filters, n adds, enter edits, d deletes, t tests the selected\n" +
			"connection, c starts the guided setup, and q quits; ctrl+c quits anywhere without saving. esc\n" +
			"only ever steps back one level: it clears a filter, cancels a running test, closes a picker, a\n" +
			"table, or a question, or leaves a form.\n\n" +
			"A form with unsaved changes is never left silently. esc or a section key first asks: s saves\n" +
			"through the same checks as ctrl+s and goes on only when the save succeeds, d discards the changes\n" +
			"and goes on, and esc keeps editing with every input intact. An unchanged form closes at once. An\n" +
			"unfinished guided setup can only be kept or discarded, since it saves from its summary only.\n\n" +
			"Focus and state read without colour. The active section is marked > while the sidebar has the\n" +
			"focus and * while the workspace has it, the pane with the focus has a double border, and the\n" +
			"selected entry or field is marked >. A connection test reports [ok] or [failed], other messages\n" +
			"say warning: or error:. Colour only supports these marks; NO_COLOR turns it off.\n\n" +
			"c sets up a connection step by step in the workspace: provider, service, credential, scope,\n" +
			"permissions, and a summary that saves the connection and can test it right away. Each step offers\n" +
			"only what the chosen provider defines, and offers the configured services and credentials of that\n" +
			"provider first, so they are reused instead of duplicated; the same checks as every other save\n" +
			"refuse a step before the next one opens, and a refused step keeps its input. A new credential\n" +
			"keeps its secrets in the system keyring (recommended), names environment variables, or, after an\n" +
			"explicit confirmation, writes them to an unencrypted file. Secrets are typed masked and never\n" +
			"shown. Nothing is written before the summary is saved: esc cancels, asking first once a provider\n" +
			"is chosen, ctrl+b goes back one step, and should the configuration fail to save, the secrets just\n" +
			"stored are removed again.\n\n" +
			"The sections remain for direct editing. The editor manages services, credentials, connections,\n" +
			"and domain defaults, can test a selected connection, and stores the secrets of a credential in\n" +
			"a masked field. It never displays a stored secret back: what it shows is which source delivers\n" +
			"a role. The secrets row of a credential offers the same places as the guided setup, in the same\n" +
			"order and words: system keyring (recommended), environment variables, and unencrypted file\n" +
			"(asks first).\n\n" +
			"The system keyring is the recommended local place for secrets: the credential store the\n" +
			"operating system already provides, Secret Service on Linux (for example GNOME Keyring or\n" +
			"KWallet), the macOS Keychain, or the Windows Credential Manager. It needs no setup and no\n" +
			"exported variable, and qatlas keeps no secret store of its own. On a role row, s stores the\n" +
			"secret in the place the secrets row names. The role row says in system keyring, not stored yet,\n" +
			"keyring locked, keyring unreachable, or keyring switched off (QATLAS_CREDENTIAL_STORE=none),\n" +
			"and for each blocked case what to do next on this platform. A set variable\n" +
			"QATLAS_<CREDENTIAL>_<ROLE> still wins and the row says environment variable, overrides keyring;\n" +
			"that is the way for CI and containers. With unencrypted file chosen, s writes the secret to that\n" +
			"file instead, and only after a warning that has to be confirmed.\n\n" +
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
			"on to the next step and esc cancels the setup, asking first once a provider was chosen. A form\n" +
			"does not open on a provider row while another row takes input.\n\n" +
			"Every other choice row opens the same way: enter, space, or / on the row opens a searchable\n" +
			"picker that filters as you type, ignoring case; up/down, pgup/pgdown and home/end move, and esc\n" +
			"leaves the row unchanged. On a row that holds one value, such as service, credential, secrets,\n" +
			"profile, tools, or the connection of a default, the picker marks the current value and enter\n" +
			"takes the selected one; left/right also step through the values in place. On a row that holds\n" +
			"several values, the permissions and the tool list, space ticks the selected value, enter keeps\n" +
			"the ticks, and esc drops them. default in the permissions stands for the provider's own set, so\n" +
			"ticking it drops the explicit permissions and ticking one of those drops default.\n\n" +
			"The targets row of a connection, in its form and in the scope step of the guided setup alike,\n" +
			"holds a list. enter, space, or / opens it: a adds a target, enter edits the selected one, x or d\n" +
			"removes it after asking, ctrl+s keeps the list, and esc leaves the row unchanged. Each target is\n" +
			"typed on its own, checked by the provider when it is taken, and refused when the list holds it\n" +
			"already. In the form, right unfolds the row to show every target and left folds it again to their\n" +
			"number and the first two. The row says what an empty list means for the provider: a GitHub\n" +
			"connection without targets reaches whatever its credential reaches, while SeaTable or Telegram\n" +
			"cannot be saved without one, and Telegram takes one chat only. One target is saved as target,\n" +
			"several as targets.\n\n" +
			"enter on a text row saves the form, or goes on to the next step of the guided setup; ctrl+s does\n" +
			"the same from every row, choice rows included. The summary of the guided setup saves with enter.\n\n" +
			"A connection's tools row decides between every tool its permissions allow, which is how a\n" +
			"connection without a tools list behaves, and only selected tools. In the second mode the tool\n" +
			"list opens the tools the chosen provider registers, with their effect, to tick. Nothing ticked\n" +
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
			"local tick narrows what the credential itself may do at the provider.\n\n" +
			"? on the sidebar or in a list shows the help topics start, agents, and configuration, the same\n" +
			"text as 'qatlas help start' and the others. left/right switch the topic, up/down and pgup/pgdown\n" +
			"scroll, and esc closes the help.\n\n" +
			"At start the editor asks GitHub in the background, for at most five seconds, whether a newer\n" +
			"stable release exists, and names it in the top line, for example Update available v0.4.0 →\n" +
			"v0.5.0 · u update. u on the sidebar or in a list asks first and then installs it the way\n" +
			"'qatlas update' does; the editor keeps running the old version until it is restarted. A failed\n" +
			"check stays silent. A dev build never checks, and a non-empty QATLAS_NO_UPDATE_CHECK turns\n" +
			"the check off.",
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
			return classifyUserError(tui.Run(store, connectionTester(store, opts, reg), secrets, opts.Redactor,
				tuiUpdater(opts, buildVersion), os.Stdin, os.Stdout))
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

// tuiUpdater returns what the editor checks for a newer release with, or nil where it must not check: in a
// dev build, which has no release to compare with, and when QATLAS_NO_UPDATE_CHECK is set.
func tuiUpdater(opts *Options, buildVersion string) tui.Updater {
	if buildVersion == "" || buildVersion == "dev" || os.Getenv(noUpdateCheck) != "" {
		return nil
	}
	if opts.Updater != nil {
		return opts.Updater
	}
	return selfupdate.New(buildVersion)
}
