package cli

import (
	"context"
	"errors"
	"os"

	"github.com/spf13/cobra"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/selfupdate"
	"github.com/castrowithcee/qatlas-cli/internal/tui"
)

// noUpdateCheck is the environment variable that keeps the editor from looking for a newer release.
const noUpdateCheck = "QATLAS_NO_UPDATE_CHECK"

func newTUICommand(opts *Options, reg *capability.Registry, buildVersion string) *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Set up connections and edit the configuration in a terminal interface",
		Long: "The editor is one screen: a sidebar with the eight sections 1 Services, 2 Credentials,\n" +
			"3 Connections, 4 Defaults, 5 Vault, 6 Approvals, 7 Tokens, and 8 Logs, and beside it a\n" +
			"workspace with the list,\n" +
			"form, or setup step of the active section. From 80 columns the sidebar stands on the left; a\n" +
			"narrower terminal\n" +
			"shows the sections in one navigation line above the workspace. Below 40x12 the editor asks for a\n" +
			"larger terminal and keeps everything as it was until it gets one.\n\n" +
			"The editor opens with the focus on the sidebar or, below 80 columns, the navigation line above\n" +
			"the workspace. From 80 columns up/down (or j/k) choose a section and show its list at once, and\n" +
			"enter, right, or tab move the focus into the list; below 80 columns left/right (or h/l) choose a\n" +
			"section instead, wrapping from the last to the first and back, up/down still work too, and enter\n" +
			"or tab move the focus into the list. left, tab, or esc move the focus back to the navigation.\n" +
			"1-8 open a section directly from the sidebar, a list, or the Logs screen; in a form, digits are\n" +
			"text instead.\n" +
			"In a list, / filters, n adds, enter edits, d deletes, t tests the selected connection, c starts\n" +
			"the guided setup, and q quits; ctrl+c quits anywhere without saving. esc only ever steps back one\n" +
			"level: it clears a filter, cancels a running test, closes a picker, a table, or a question, or\n" +
			"leaves a form.\n\n" +
			"A form with unsaved changes is never left silently. esc first asks: s saves through the same\n" +
			"checks as F2 and goes on only when the save succeeds, d discards the changes\n" +
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
			"keeps its secrets in the system keyring, in the vault, or names environment variables;\n" +
			"defaults.secret_store decides which of the first two is preselected, and none is called a\n" +
			"recommendation. Secrets are typed masked and never shown. Storing the vault's very first\n" +
			"secret offers a passphrase, typed masked and twice; leaving it empty keeps the vault\n" +
			"unencrypted. Nothing is written before the summary is saved: esc cancels, asking first once a\n" +
			"provider is chosen, F3 goes back one step, and should the configuration fail to save, the\n" +
			"secrets just stored are removed again.\n\n" +
			"The sections remain for direct editing. The editor manages services, credentials, connections,\n" +
			"and domain defaults, can test a selected connection, and stores the secrets of a credential in\n" +
			"a masked field. It never displays a stored secret back: what it shows is which source delivers\n" +
			"a role. The secrets row of a credential offers the same places as the guided setup, in the same\n" +
			"order and words: system keyring, vault, and environment variables, whichever is now chosen; none\n" +
			"is a recommendation.\n\n" +
			"While the vault is encrypted, the editor opens read-only: saving a form, deleting an entry,\n" +
			"storing or removing a secret, and every action of 5 Vault (see below) each ask for the vault's\n" +
			"passphrase the first time, masked in a screen of their own, never on this terminal. Answering it\n" +
			"unlocks the vault where it was locked, and either way starts an admin session bound to this\n" +
			"window: further managing actions run at once until vault.admin_timeout passes without a key\n" +
			"press, after which the next one asks again; vault.admin_timeout: 0 asks every time instead of\n" +
			"keeping a session. A wrong passphrase reopens the same prompt with error: wrong passphrase; esc\n" +
			"cancels only that one action, leaving the form exactly as typed so it can be tried again. No\n" +
			"other window, and no vault process a Linux build may hold unlocked outside this run, ever shares\n" +
			"the session: the passphrase proves it, not merely an unlocked vault. An unencrypted vault, or\n" +
			"one that does not exist yet, needs none of this and manages exactly as before.\n\n" +
			"The header above the sections always shows the vault's state, unlocked, locked, or unencrypted,\n" +
			"symbol and word together so it reads without colour, plus the admin abbreviation while it is\n" +
			"encrypted: admin off, admin Nm left, or admin: confirm each change for vault.admin_timeout: 0.\n" +
			"ctrl+l works everywhere, forms included: on a locked vault it opens a masked prompt that unlocks\n" +
			"it, hands it to a vault process on Linux the same way 'qatlas vault unlock' does, and starts this\n" +
			"window's admin session in the same step; on an unlocked, encrypted vault it asks Lock the vault\n" +
			"now?, and y locks the vault process, forgets the key this window held, and ends the admin\n" +
			"session. An unencrypted vault has nothing to lock, and ctrl+l says so.\n\n" +
			"The system keyring is the credential store the operating system already provides: Secret\n" +
			"Service on Linux (for example GNOME Keyring or KWallet), the macOS Keychain, or the Windows\n" +
			"Credential Manager. It needs no setup and no exported variable. The vault is a directory beside\n" +
			"the configuration, unencrypted or encrypted to a passphrase, for a machine without a usable\n" +
			"keyring; storing its very first secret, here or with 'qatlas credential set', offers one, typed\n" +
			"masked and twice, and leaving it empty keeps the vault unencrypted. On a role row, s stores the\n" +
			"secret in the place the secrets row names; x removes it. On Linux, storing or removing a vault\n" +
			"secret here, or through the guided setup's own save, is handed on to a vault process that holds\n" +
			"the vault unlocked outside this run, exactly the way 'qatlas credential set' and 'qatlas\n" +
			"credential delete' already do; elsewhere, or when no such process runs, there is nothing to tell.\n" +
			"A keyring role says in system keyring,\n" +
			"not stored yet, keyring locked, keyring unreachable, or keyring switched off\n" +
			"(QATLAS_CREDENTIAL_STORE=none), and for each blocked case what to do next on this platform; a\n" +
			"vault role says in the vault, not stored yet, or vault locked. A set variable\n" +
			"QATLAS_<CREDENTIAL>_<ROLE> still wins over either one and the row says environment variable,\n" +
			"overrides keyring; that is the way for CI and containers.\n\n" +
			"5 Vault is a settings form, not a list: it shows the vault's state (no vault yet, unencrypted, or\n" +
			"encrypted and locked or unlocked) and the actions that apply to it. Unencrypted offers to turn\n" +
			"encryption on with a new passphrase, typed masked and twice; encrypted offers to change the\n" +
			"passphrase (the current one once, then a new one twice) and to turn encryption off (the current\n" +
			"passphrase, then an explicit confirmation that every secret ends up unencrypted on disk). The\n" +
			"current passphrase is verified before anything else is asked; a wrong one reopens that very\n" +
			"prompt with error: wrong passphrase instead of asking for a new passphrase or the confirmation\n" +
			"for nothing, and nothing is ever asked on this process's own terminal. On Linux, changing the\n" +
			"passphrase or turning encryption off also locks a vault process that holds the vault unlocked\n" +
			"with the old identity, the same way 'qatlas vault passphrase' and 'qatlas vault decrypt' already\n" +
			"do, and says so once the change is done. While a plaintext\n" +
			"credentials.yaml left over from an earlier version still holds an entry, migrate\n" +
			"credentials.yaml carries every one of them into the vault the way 'qatlas vault migrate' does:\n" +
			"an overview names every entry by credential and role, never a value, before asking to confirm;\n" +
			"the result then names what moved and which credentials switched to type vault, and a separate\n" +
			"question asks whether to delete credentials.yaml, kept on no. The form also edits\n" +
			"vault.idle_timeout and vault.admin_timeout, saved with F2 like any other setting; admin_timeout\n" +
			"sets how long this window's own admin session above stays open (see the read-only\n" +
			"paragraph further up), 0 asking for the passphrase on every managing action instead.\n\n" +
			"Saving a change while the vault is encrypted approves it automatically, without an extra\n" +
			"step: the connection saved directly, in its own form or by the guided setup, and any other\n" +
			"connection whose scope changed because of that same save, such as one sharing a service\n" +
			"whose base_url just changed. 6 Approvals lists every connection still open otherwise, with\n" +
			"what changed since it was last approved: enter opens its detail to approve it alone, and a\n" +
			"on the list approves every one currently open, asking to confirm the count first. While the\n" +
			"vault is unencrypted or locked, the section says so instead of a list.\n\n" +
			"7 Tokens lists the agent tokens of an encrypted vault with their vorbild connections and expiry,\n" +
			"and marks an expired token, or a vorbild that covers nothing now, with ⚠ and words behind its\n" +
			"name. Every action on a token needs the admin session above: n creates one from a name, one or\n" +
			"more connections that read a vault credential, ticked in a picker, and an optional expiry\n" +
			"(YYYY-MM-DD or an RFC 3339 time), like 'qatlas vault token create'; enter opens its detail, where\n" +
			"s shows its value and hides it again, only there and only while the session lasts; x revokes it\n" +
			"after asking. While the vault is unencrypted or locked, the section says so instead of a list.\n\n" +
			"8 Logs shows the invocation log, read only, the same log 'qatlas vault logs verify' checks. The\n" +
			"bar on top reads Mode: Day, Range, or All and, for the first two, the date or the range of UTC\n" +
			"days: tab moves the focus from the rows to the bar's fields and back, left/right change the\n" +
			"mode, and enter or left/right on the date open a small dialog that takes one date, or from and\n" +
			"to, as YYYY-MM-DD. The filter line names what f filters by: way, MCP client, tool, connection,\n" +
			"effect, and result, each chosen in a picker among the values the shown days hold, all by\n" +
			"default. / searches the text of the rows, enter keeps the search, and esc clears it. The table\n" +
			"shows status, time, way, client, tool, connection, result, and duration, leaving out the\n" +
			"duration, then the connection, then the client where the terminal is narrow; enter opens every\n" +
			"field of one entry, and esc returns. Every row and the bar, for everything the chosen days\n" +
			"hold, show a status, symbol and word together: ✓ verified for an entry that chains and whose\n" +
			"check value matches, ? unverified for one without a check value or one that could not be\n" +
			"checked, … gap where entries are missing right before it without a retention cut, and ✗ altered\n" +
			"for one that fails its check value, where the hash chain breaks, or that does not parse at all.\n" +
			"The check values are checked with the key of a vault unlocked in this window or by a running\n" +
			"vault process; while the vault is locked they stay unchecked and show as unverified, since the\n" +
			"editor never asks for the passphrase here, and ctrl+l unlocks it. Reading and checking run in\n" +
			"the background: the chain is followed through every day file, but only the chosen days are\n" +
			"kept and have their check values checked. A field of a log line is shown without its control\n" +
			"characters and with every known secret redacted. Nothing in the section writes or deletes the\n" +
			"log.\n\n" +
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
			"holds a list. enter, space, or / opens it: a adds a target, enter edits the selected one, and x or\n" +
			"d removes it after asking. F2 keeps the list and saves the connection in one step, taking a\n" +
			"target that is still being typed first; in the guided setup it goes on to the next step instead,\n" +
			"since the setup saves only from its summary. esc closes an unchanged list; after a change it asks\n" +
			"first: k keeps the list in the row, d discards the changes, and esc returns to the list.\n\n" +
			"a opens a menu whenever there is more to offer than typing: the targets other connections of the\n" +
			"same service use, marked with those connections, and for a provider that names kinds of targets,\n" +
			"such as GitHub's repository, project, and owner, a builder for a new one. Typing filters the menu,\n" +
			"and its first row takes the typed line as a target, the way for experts. The builder asks for the\n" +
			"parts of the kind one by one, for example the owner and then the repository name or the project\n" +
			"number, suggests the values the same service already uses there, offers * as all, and writes the\n" +
			"usual path such as repos/OWNER/REPO; backspace on an empty line steps back, and esc cancels the\n" +
			"entry. * is never selected when a step opens, so a pattern is added only when it is chosen with\n" +
			"enter or typed. F2 takes a typed line, or a typed value that completes a build, never a row\n" +
			"that is merely selected, and says what is missing otherwise. Every target, however it was added,\n" +
			"is checked by the provider when it is taken; a refused one stays typed with the reason, and one\n" +
			"the list holds already is refused. enter on a target edits it as typed text.\n\n" +
			"In the form, right unfolds the targets row to show every target and left folds it again to their\n" +
			"number and the first two. The row says what an empty list means for the provider: a GitHub\n" +
			"connection without targets reaches whatever its credential reaches, while SeaTable or Telegram\n" +
			"cannot be saved without one, and Telegram takes one chat only. One target is saved as target,\n" +
			"several as targets.\n\n" +
			"The paths row of a connection form holds the directories the connection is bound to, each\n" +
			"absolute or starting with ~/; without paths the connection applies in every project. The row\n" +
			"opens its list like the targets row, with the same keys: a adds a path, enter or e edits the\n" +
			"selected one, x or d removes it after asking, F2 keeps the list and saves the connection, and\n" +
			"esc asks before it drops a changed list. While a path is typed, tab takes the suggested\n" +
			"directory and up/down switch between several; only directories are suggested, and with nothing\n" +
			"typed the directory the TUI was started in. A path that does not exist yet is taken as typed,\n" +
			"and qatlas config validate warns about it.\n\n" +
			"enter on a text row saves the form, or goes on to the next step of the guided setup; F2 does\n" +
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
			secrets, err := tuiSecrets(opts)
			if err != nil {
				return err
			}
			store := config.NewStore(path, reg)
			return classifyUserError(tui.Run(store, connectionTester(store, opts, reg), secrets, opts.Redactor,
				tuiUpdater(opts, buildVersion), os.Stdin, os.Stdout))
		},
	}
}

// tuiSecrets returns the resolver 'qatlas tui' reads and writes secrets through. Unlike every other command
// it never asks a vault passphrase on this process's terminal: bubbletea holds that terminal in raw mode
// for its own screen, so a term.ReadPassword prompt there would corrupt it instead of being seen, and
// inputs would race between the prompt and the editor. A locked, encrypted vault therefore answers
// vault-locked wherever it is read or a role's secret removed from it, the same code an agent gets over
// MCP; a person unlocks it first with 'qatlas vault unlock'. Storing a new secret is unaffected: it always
// queues as a pending entry the vault's public key can take, whatever state the vault is in, and the
// editor's own credential form offers a passphrase for a vault it is creating itself without ever asking
// this terminal either. opts.resolver caches its result, so this is the same instance connectionTester's
// own opts.resolver() call reads back, and both stay free of the terminal ask.
func tuiSecrets(opts *Options) (*secret.Resolver, error) {
	secrets, err := opts.resolver()
	if err != nil {
		return nil, err
	}
	if v := secrets.Vault(); v != nil {
		secrets.WithVault(v, nil)
	}
	return secrets, nil
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
