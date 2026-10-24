package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/connlog"
	"github.com/castrowithcee/qatlas-cli/internal/manage"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/selftest"
	"github.com/castrowithcee/qatlas-cli/internal/selfupdate"
	"github.com/castrowithcee/qatlas-cli/internal/tui"
	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
)

// noUpdateCheck is the environment variable that keeps the editor from looking for a newer release.
const noUpdateCheck = "QATLAS_NO_UPDATE_CHECK"

func newTUICommand(opts *Options, reg *capability.Registry, buildVersion string) *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Set up connections and edit the configuration in a terminal interface",
		Long: "The editor is one screen: a tab line with the six areas 1 Overview, 2 Connections, 3 Activity,\n" +
			"4 Access, 5 Library, and 6 Settings, below it, for an area with several parts, an option bar\n" +
			"(Access: Tokens, Approvals; Library: Services, Credentials; Settings: General, then after a bar\n" +
			"Vault, Defaults, Transfer, Update, Self-test), and below that the workspace with the list, form,\n" +
			"or setup step of the active part. The tab line has the\n" +
			"same place at every width; the active area and part are in brackets, and a terminal too narrow\n" +
			"for all names shows the other areas by their numbers and only the active part. Below 40x13 the\n" +
			"editor asks for a larger terminal and keeps everything as it was until it gets one.\n\n" +
			"The editor opens on the Overview with the focus on the tab line. The Overview shows the number of\n" +
			"connections, services, credentials, agent tokens, and open approvals (tokens and approvals need an\n" +
			"unlocked, encrypted vault), the vault state, a newer release, and the next steps, such as open\n" +
			"approvals or an unencrypted vault; down moves into these lines, up and down select one, and enter\n" +
			"opens the area and part that deals with it. It changes nothing itself. The arrow keys move on every screen, and\n" +
			"h/j/k/l (left, down, up, right) do the same on every screen that takes no typed text; where text\n" +
			"is typed, a letter is text. pgup/pgdown and home/end jump in every list, picker, table, and the\n" +
			"tab line. tab and shift+tab go to the next or previous field, or between the option bar and the\n" +
			"list; in a path being typed, tab takes the suggested directory. space ticks in a multiple choice\n" +
			"and does nothing else.\n" +
			"On the tab line left/right choose an area, wrapping from the last to the first and back; down\n" +
			"moves the focus to the option bar of an area that has one, where left/right choose the part and\n" +
			"up returns to the tab line; down, enter, or tab move the focus into the list, and up does\n" +
			"nothing on the tab line. In that list, up/down\n" +
			"move the selection without wrapping, up at the first entry or in an empty list moves the focus\n" +
			"back to the option bar or tab line, down stays at the last entry, and left/right leave the focus\n" +
			"where it is; tab or esc still move it back.\n" +
			"1-6 open an area directly, on its first part, from the tab line, a list, or the Logs screen; in a\n" +
			"form, digits are text instead. The command line (:) opens an area with go overview, go\n" +
			"connections, go activity, go access, go library, or go settings, and a part directly with go\n" +
			"tokens, go approvals, go services, go credentials, go general, go vault, go defaults, go transfer,\n" +
			"go update, go self-test, or go logs.\n" +
			"In a list, / filters, n adds, enter edits, d deletes, t tests the selected connection, c starts\n" +
			"the guided setup, and q quits, as it does from the tab line and the Logs screen; on any other\n" +
			"screen q is not a key. esc is one level back on every screen, and the only key that\n" +
			"goes back: it clears a filter, cancels a running test, closes a picker, a table, a dialog, or a\n" +
			"log entry, or leaves a form. backspace never goes back.\n\n" +
			"Services, credentials, connections, and agent tokens can be copied. p duplicates the selected\n" +
			"entry into a filled form of a new entry named <name>-copy (or <name>-copy-2, and so on), and n\n" +
			"first asks whether the new entry starts empty or from an existing one, which is chosen in a\n" +
			"searchable list; c asks the same for the guided setup, which then starts from a connection. A\n" +
			"copy is saved like any new entry, in an admin session. Secret values are never copied: a copied\n" +
			"credential keeps only where its secrets are kept and you store its secrets afterwards, and a\n" +
			"copied agent token keeps its vorbilder and an expiry that still lies ahead, but gets its own\n" +
			"value.\n\n" +
			"Nothing unsaved is dropped silently, and one question asks about all of it: a changed form, a\n" +
			"changed list of targets or paths, a target or path typed but not yet taken, a half-built target,\n" +
			"a multiple choice with changed ticks, and an unfinished guided setup. F2 saves through the same\n" +
			"checks as on the screen itself and goes on only when that succeeds (in the guided setup it goes\n" +
			"on to the next step instead), d discards what the screen holds, and esc keeps editing with every\n" +
			"input intact. Unchanged input closes at once. ctrl+c quits at once, and asks the same question\n" +
			"when something unsaved would be lost; d then discards it and quits. A single-line dialog, such as\n" +
			"a passphrase, a secret, a search, or the date of the Logs, is closed by esc without a question.\n\n" +
			"Focus and state read without colour. The active section is in brackets, and > in front of the\n" +
			"tab line says it has the focus; the selected entry or field is marked >. A connection test reports [ok] or [failed], other messages\n" +
			"say warning: or error:. Colour only supports these marks; NO_COLOR turns it off.\n\n" +
			"c sets up a connection step by step in the workspace: provider, service, credential, scope,\n" +
			"access, and a summary that saves the connection and can test it right away. Each step offers\n" +
			"only what the chosen provider defines, and offers the configured services and credentials of that\n" +
			"provider first, so they are reused instead of duplicated; the same checks as every other save\n" +
			"refuse a step before the next one opens, and a refused step keeps its input. A new credential\n" +
			"keeps its secrets in the system keyring, in the vault, or names environment variables;\n" +
			"defaults.secret_store decides which of the first two is preselected, and none is called a\n" +
			"recommendation. Secrets are typed masked and never shown. Storing the vault's very first\n" +
			"secret offers a passphrase, typed masked and twice; leaving it empty keeps the vault\n" +
			"unencrypted. Nothing is written before the summary is saved: esc goes back one step, and on the\n" +
			"first step cancels the setup, asking first once a provider is chosen, and should the\n" +
			"configuration fail to save, the secrets just stored are removed again.\n\n" +
			"The sections remain for direct editing. The editor manages services, credentials, connections,\n" +
			"and domain defaults, can test a selected connection, and stores the secrets of a credential in\n" +
			"a masked field. It never displays a stored secret back: what it shows is which source delivers\n" +
			"a role. The state row of a connection form switches it between active and disabled; a disabled\n" +
			"connection keeps its whole selection but serves nothing until it is active again, and can still be\n" +
			"tested. The active for row, shown while the state is active, limits how long it stays active: from\n" +
			"then on it refuses like a disabled one; the list shows the time that remains, or expired. The secrets row of a credential offers the same places as the guided setup, in the same\n" +
			"order and words: system keyring, vault, and environment variables, whichever is now chosen; none\n" +
			"is a recommendation.\n\n" +
			"A payload secret is a credential with freely named fields whose values a tool may pass on to a\n" +
			"third party by reference. In 2 Credentials, s opens the form of a new one (and enter edits an\n" +
			"existing one): a name, where its values are kept (system keyring or vault, never environment\n" +
			"variables), an optional description that discovery publishes, and one masked row per field. Type\n" +
			"a field name in the new field row and press enter to add its row; ctrl+d removes the focused\n" +
			"field after asking, and a removed field's stored value is deleted when you save. Each value is typed masked,\n" +
			"never shown or prefilled, and at least 4 characters; on an existing payload secret an empty row\n" +
			"keeps the stored value. Saving stores the values and the configuration entry together, in an\n" +
			"admin session. A connection releases payload secrets with forward_secrets: its form gets a\n" +
			"forward row, opened with enter to tick any number of existing payload secrets. While the vault\n" +
			"is encrypted, forward_secrets and the fields of the listed payload secrets are part of the\n" +
			"connection's approval, and a change is never approved by saving: the connection stays open until\n" +
			"it is approved in Access > Approvals (4). Without an encrypted vault, and for a connection whose own\n" +
			"credential is not in it, no approval binds the release and only the forward_secrets list in the\n" +
			"configuration limits it; the forms say so.\n\n" +
			"While the vault is encrypted, the editor opens read-only: saving a form, deleting an entry,\n" +
			"storing or removing a secret, and every action of Settings > Vault (6, see below) each ask for the vault's\n" +
			"passphrase the first time, masked in a screen of their own, never on this terminal. Answering it\n" +
			"unlocks the vault where it was locked, and either way starts an admin session bound to this\n" +
			"window: further managing actions run at once until vault.admin_timeout passes without a key\n" +
			"press, after which the next one asks again; vault.admin_timeout: 0 asks every time instead of\n" +
			"keeping a session. A wrong passphrase reopens the same prompt with error: wrong passphrase; esc\n" +
			"cancels only that one action, leaving the form exactly as typed so it can be tried again. No\n" +
			"other window, and no vault process a Linux or macOS build may hold unlocked outside this run,\n" +
			"ever shares the session: the passphrase proves it, not merely an unlocked vault. An unencrypted\n" +
			"vault, or one that does not exist yet, needs none of this and manages exactly as before.\n\n" +
			"The header above the tab line always shows the vault's state, unlocked, locked, or unencrypted,\n" +
			"symbol and word together so it reads without colour, plus the admin abbreviation while it is\n" +
			"encrypted: admin off, admin Nm left, or admin: confirm each change for vault.admin_timeout: 0.\n" +
			"ctrl+l works on the tab line, the lists, the forms, the pickers and tables, the target and path\n" +
			"lists, the setup summary, and the Logs screens, never over a question, a masked prompt, the\n" +
			"template choice, or the help. On a locked vault it opens a masked prompt that unlocks it, hands\n" +
			"it to a vault process on Linux, macOS, and Windows the same way 'qatlas vault unlock' does, and\n" +
			"starts this window's admin session in the same step; on an unlocked, encrypted vault it asks\n" +
			"Lock the vault now?, and y locks the vault process, forgets the key this window held, and ends the\n" +
			"admin session. An unencrypted vault has nothing to lock, and ctrl+l says so.\n\n" +
			"The system keyring is the credential store the operating system already provides: Secret\n" +
			"Service on Linux (for example GNOME Keyring or KWallet), the macOS Keychain, or the Windows\n" +
			"Credential Manager. It needs no setup and no exported variable. The vault is a directory beside\n" +
			"the configuration, unencrypted or encrypted to a passphrase, for a machine without a usable\n" +
			"keyring; storing its very first secret, here or with 'qatlas credential set', offers one, typed\n" +
			"masked and twice, and leaving it empty keeps the vault unencrypted. On a role row, enter opens a\n" +
			"masked prompt that stores the secret in the place the secrets row names; d removes it after asking. On Linux, macOS, and Windows, storing or removing\n" +
			"a vault secret here, or through the guided setup's own save, is handed on to a vault process that\n" +
			"holds the vault unlocked outside this run, exactly the way 'qatlas credential set' and 'qatlas\n" +
			"credential delete' already do; elsewhere, or when no such process runs, there is nothing to tell.\n" +
			"A keyring role says in system keyring,\n" +
			"not stored yet, keyring locked, keyring unreachable, or keyring switched off\n" +
			"(QATLAS_CREDENTIAL_STORE=none), and for each blocked case what to do next on this platform; a\n" +
			"vault role says in the vault, not stored yet, or vault locked. A set variable\n" +
			"QATLAS_<CREDENTIAL>_<ROLE> still wins over either one and the row says environment variable,\n" +
			"overrides keyring; that is the way for CI and containers.\n\n" +
			"General (6, first part of Settings) holds the preferences of this device: the color scheme (auto,\n" +
			"light, or dark; auto follows the terminal's background and NO_COLOR wins over all of them), the\n" +
			"start view (the area the editor opens on the next start), and icons (none or nerdfont, an icon\n" +
			"before each area name that needs a Nerd Font). They are chosen from lists and saved with F2, with\n" +
			"no admin session. They live in tui.yaml beside the configuration, belong to this device, and are\n" +
			"never exported or imported. Transfer, Update, and Self-test only start the export and import, the\n" +
			"update, and the selftests that the commands and keys start too, under the same rules.\n\n" +
			"Vault (Settings) is a settings form, not a list: it shows the vault's state (no vault yet,\n" +
			"unencrypted, or\n" +
			"encrypted and locked or unlocked) and the actions that apply to it. Unencrypted offers to turn\n" +
			"encryption on with a new passphrase, typed masked and twice; encrypted offers to change the\n" +
			"passphrase (the current one once, then a new one twice) and to turn encryption off (the current\n" +
			"passphrase, then an explicit confirmation that every secret ends up unencrypted on disk). The\n" +
			"current passphrase is verified before anything else is asked; a wrong one reopens that very\n" +
			"prompt with error: wrong passphrase instead of asking for a new passphrase or the confirmation\n" +
			"for nothing, and nothing is ever asked on this process's own terminal. On Linux, macOS, and Windows,\n" +
			"changing the passphrase or turning encryption off also locks a vault process that holds the vault\n" +
			"unlocked with the old identity, the same way 'qatlas vault passphrase' and 'qatlas vault decrypt'\n" +
			"already do, and says so once the change is done. While a plaintext\n" +
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
			"whose base_url just changed. Approvals (4, second part of Access) lists every connection still open otherwise, with\n" +
			"what changed since it was last approved: enter opens its detail to approve it alone, and a\n" +
			"on the list approves every one currently open, asking to confirm the count first. While the\n" +
			"vault is unencrypted or locked, the section says so instead of a list.\n\n" +
			"Tokens (4, first part of Access) lists the agent tokens of an encrypted vault with their vorbild connections and expiry,\n" +
			"and marks an expired token, or a vorbild that covers nothing now, with ⚠ and words behind its\n" +
			"name. Every action on a token needs the admin session above: n creates one from a name, one or\n" +
			"more connections that read a vault credential, ticked in a picker, and an optional expiry\n" +
			"(YYYY-MM-DD or an RFC 3339 time), like 'qatlas vault token create'; enter opens its detail, where\n" +
			"s shows its value and hides it again, only there and only while the session lasts; d revokes it\n" +
			"after asking, y confirms and n or esc keeps it. While the vault is unencrypted or locked, the section says so instead of a list.\n\n" +
			"Activity (3) shows the invocation log, read only, the same log 'qatlas vault logs verify' checks. The\n" +
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
			"The View field of the bar, reached with tab and changed with left/right, or the commands view\n" +
			"heatmap and view table, swaps the table for a heatmap of the same rows: one cell per local day,\n" +
			"weekdays by weeks, leveled by the number of invokes (quartiles of the busy days) with ·░▒▓█ or,\n" +
			"in a terminal that is not UTF-8, .-=+#, a legend, and the calls, the success rate, and the errors.\n" +
			"A day outside logs.retention_days shows × instead of the dot of a day without calls, a day with a\n" +
			"changed line ✗, and a day with missing entries …. The mode, the days, the filters, and the search\n" +
			"apply to it; from a single day, the heatmap widens the days to the retention window.\n\n" +
			"Every list scrolls within the terminal and shows the position of the selected entry. / filters\n" +
			"a list by the text of its rows, ignoring case; enter keeps the filter and esc clears it. Up/down\n" +
			"move through the entries, pgup/pgdown and home/end jump. Filtering never changes the file.\n\n" +
			"Every provider row, in the forms of services, credentials, and connections and as the first step\n" +
			"of the guided setup, is chosen in the same provider table, however many providers there are:\n" +
			"enter or / on the row opens it. The table lists every provider the row offers with its\n" +
			"name and ID, marks the current one, and always shows the search line, the position of the\n" +
			"selection, and the total. Typing filters by name and ID, ignoring case; up/down, pgup/pgdown and\n" +
			"home/end move; enter takes the selected provider and updates the rows that depend on it, and esc\n" +
			"leaves the provider and every row that depends on it unchanged. In the guided setup, enter goes\n" +
			"on to the next step and esc cancels the setup, asking first once a provider was chosen. A form\n" +
			"does not open on a provider row while another row takes input.\n\n" +
			"Every other choice row opens the same way: enter or / on the row opens a searchable\n" +
			"picker that filters as you type, ignoring case; up/down, pgup/pgdown and home/end move, and esc\n" +
			"leaves the row unchanged. On a row that holds one value, such as service, credential, secrets,\n" +
			"profile, access, or the connection of a default, the picker marks the current value and enter\n" +
			"takes the selected one; left/right also step through the values in place. On a row that holds\n" +
			"several values, the effects and the tool list, space ticks the selected value, enter keeps\n" +
			"the ticks, and esc drops them. default in the effects stands for the provider's own set, so\n" +
			"ticking it drops the explicit effects and ticking one of those drops default.\n" +
			"For a provider that sorts its tools into groups, such as GitHub, the tool list opens as a list of\n" +
			"groups, folded, each with its tool count and how many of its tools are ticked, and the head shows\n" +
			"the ticks of all tools. left/right fold and unfold the group under the cursor; space on a group\n" +
			"row ticks all its tools, or unticks them when all are ticked, and the all groups row does so for\n" +
			"every group; space on a tool ticks that tool. Typing a search shows the matching tools under their\n" +
			"groups, folded or not, and space on a group row then acts on the matches only. The groups only\n" +
			"arrange the list: the saved tools are the same IDs as without them.\n" +
			"Above the groups, the tool list has one row per effect its tools have, such as [-] read 3/12: [x]\n" +
			"all, [-] some, [ ] none of that effect's tools ticked. space on it ticks all its tools, or unticks\n" +
			"them when all are ticked, and during a search acts on the matches only. Tools that are listed only\n" +
			"are never ticked this way, only one by one; space unticks them with the rest. The tools row of\n" +
			"the form shows how many tools are ticked and their effects.\n\n" +
			"The targets row of a connection, in its form and in the scope step of the guided setup alike,\n" +
			"holds a list. enter or / opens it: a adds a target, enter edits the selected one, and\n" +
			"d removes it after asking. F2 keeps the list and saves the connection in one step, taking a\n" +
			"target that is still being typed first; in the guided setup it goes on to the next step instead,\n" +
			"since the setup saves only from its summary. esc closes an unchanged list; after a change, or with\n" +
			"a target typed but not taken, it asks the question above, and F2 then keeps the list and saves.\n\n" +
			"a opens a menu whenever there is more to offer than typing: the targets other connections of the\n" +
			"same service use, marked with those connections, and for a provider that names kinds of targets,\n" +
			"such as GitHub's repository, project, and owner, a builder for a new one. Typing filters the menu,\n" +
			"and its first row takes the typed line as a target, the way for experts. The builder asks for the\n" +
			"parts of the kind one by one, for example the owner and then the repository name or the project\n" +
			"number, suggests the values the same service already uses there, offers * as all, and writes the\n" +
			"usual path such as repos/OWNER/REPO; esc steps back one part, and on the first part leaves the\n" +
			"builder after the question above. * is never selected when a step opens, so a pattern is added\n" +
			"only when it is chosen with enter or typed. F2 takes a typed line, or a typed value that completes\n" +
			"a build, never a row that is merely selected, and says what is missing otherwise. Every target,\n" +
			"however it was added, is checked by the provider when it is taken; a refused one stays typed with\n" +
			"the reason, and one the list holds already is refused. enter on a target edits it as typed text.\n\n" +
			"In the form, right unfolds the targets row to show every target and left folds it again to their\n" +
			"number and the first two. The row says what an empty list means for the provider: a GitHub\n" +
			"connection without targets reaches whatever its credential reaches, while SeaTable or Telegram\n" +
			"cannot be saved without one, and Telegram takes one chat only. One target is saved as target,\n" +
			"several as targets.\n\n" +
			"The paths row of a connection form holds the directories the connection is bound to, each\n" +
			"absolute or starting with ~/; without paths the connection applies in every project. The row\n" +
			"opens its list like the targets row, with the same keys: a adds a path, enter or e edits the\n" +
			"selected one, d removes it after asking, F2 keeps the list and saves the connection, and\n" +
			"esc asks before it drops a changed list or a typed path. While a path is typed, tab takes the\n" +
			"suggested directory and up/down switch between several; only directories are suggested, and with nothing\n" +
			"typed the directory the TUI was started in. A path that does not exist yet is taken as typed,\n" +
			"and qatlas config validate warns about it.\n\n" +
			"enter on a text row goes on to the next row, and on the last one does nothing; it never saves.\n" +
			"F2 saves the form from every row, or goes on to the next step of the guided setup; the summary of\n" +
			"the guided setup saves with F2 too. The hint under the focused row stands in full, wrapped.\n\n" +
			"A connection's access row decides between by effect and selected tools, and only the row of the\n" +
			"chosen mode is shown and saved. By effect offers every tool of the ticked effects, and also a tool\n" +
			"a later version adds; a tool marked listed only, such as a high-risk administration tool, is never\n" +
			"offered this way. Selected tools offers exactly the tools ticked in the tools row, which opens\n" +
			"the tools the chosen provider registers, with their effect; the effects follow from the ticked\n" +
			"tools and are not stored. Nothing ticked stores an explicit empty list, which offers no tool at\n" +
			"all. A change of provider clears the ticks, because tool IDs belong to their provider.\n\n" +
			"A new connection, in its form and in the guided setup, starts on the provider's recommended\n" +
			"setup profile: a named starting selection that chooses selected tools and exactly the tools it\n" +
			"names, visible and changeable before saving. A recommended profile ticks reads only, unless the\n" +
			"provider states why a change is safe, as Telegram does for sending to the configured chat. The\n" +
			"profile row shows the profile the ticks match, or custom after a change by hand; choosing another\n" +
			"profile replaces the ticks at once while they are a profile's, and asks first once they were\n" +
			"changed by hand or belong to a saved connection. A saved connection opens and saves as it is; a\n" +
			"profile is never applied to it on its own. A profile is a starting selection, not a role: the\n" +
			"configuration keeps only the concrete tool IDs, so a tool a later version adds joins no saved\n" +
			"connection, and no local tick narrows what the credential itself may do at the provider.\n\n" +
			"? shows the help topics start, agents, and configuration, the same text as 'qatlas help start'\n" +
			"and the others, on every screen that takes no typed text: the tab line, the lists, every question,\n" +
			"the target and path lists, the setup summary, and the details of an approval, a token, and a log\n" +
			"entry. It also opens on the provider table, the add menu of targets, and the template search while\n" +
			"the search is empty. A form and every typed line keep ? as text, and name their keys in the footer.\n" +
			"left/right switch the topic, up/down and pgup/pgdown scroll, and esc or ? closes the help and\n" +
			"returns to the screen it was opened from. Every footer names the keys of its screen, apart from the\n" +
			"h/j/k/l letters.\n\n" +
			"At start the editor asks GitHub in the background, for at most five seconds, whether a newer\n" +
			"stable release exists, and names it in the top line, for example Update available v0.4.0 →\n" +
			"v0.5.0 · u update. u on the tab line or in a list asks first and then installs it the way\n" +
			"'qatlas update' does. On Linux and macOS the editor then restarts itself: it locks a vault process\n" +
			"first, runs the new program at the same path with the same arguments, and says qatlas updated\n" +
			"from one version to the other. If the vault was unlocked before, the new editor opens the\n" +
			"unlock dialog at once; esc leaves it locked. Unsaved input in an open form is never dropped\n" +
			"silently: the editor asks first, and not now keeps editing on the old version. Where the editor\n" +
			"cannot restart itself, on Windows or when the new program cannot be run, it keeps running the\n" +
			"old version until it is restarted by hand. On Windows u also locks a vault process before the\n" +
			"program is replaced, since Windows has no handover; 'qatlas vault unlock' unlocks it again. A failed check stays silent. A dev build never\n" +
			"checks, and a non-empty QATLAS_NO_UPDATE_CHECK turns the check off.",
		Args: noArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			secret.SetLongRunning("tui")
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
			svc := manage.New(store, secrets, connlog.SurfaceTUI, opts.Redactor).
				WithTargetSource(targetSource(opts, reg))
			// Device-local preferences live beside the configuration and are not part of it. A missing or
			// damaged file never stops the editor; it opens with the defaults and says why.
			prefsPath := tuiPrefsPath(path)
			prefs, prefsWarnings := uiprefs.Load(prefsPath)
			return classifyUserError(tui.Run(tui.Options{
				Service: svc, Store: store, Tester: connectionTester(store, opts, reg), Secrets: secrets,
				Redactor: opts.Redactor, Updater: tuiUpdater(opts, buildVersion), Restart: tuiRestart(),
				SelfTest: tuiSelfTest(buildVersion), Prefs: prefs, PrefsWarnings: prefsWarnings,
				SavePrefs: func(p uiprefs.Prefs) error { return uiprefs.Save(prefsPath, p) },
				In:        os.Stdin, Out: os.Stdout,
			}))
		},
	}
}

// tuiPrefsPath is the device-local preferences file beside the resolved configuration file, so it follows
// QATLAS_CLI_HOME and QATLAS_CONFIG exactly as the configuration does.
func tuiPrefsPath(configPath string) string { return uiprefs.Path(filepath.Dir(configPath)) }

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

// connectionTester binds the editor, or the browser's own result page (see internal/cli/web.go, which
// converts this same func value to web.Tester), to the shared core function. Neither the editor nor
// internal/web knows any provider; only this func value, built in this package, does.
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

// targetSource binds the management core's target choice to the registry's target browsers, the way
// connectionTester binds the test. Surfaces reach it only through the manage.Service it is attached to. A
// resolver the core hands over is the only one the read uses; without one it is this run's own.
func targetSource(opts *Options, reg *capability.Registry) manage.TargetSource {
	return manage.TargetSource{
		Browses: reg.HasTargetBrowser,
		List: func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
			req config.TargetListRequest) (config.TargetPage, error) {
			secrets, err := targetSecrets(opts, secrets)
			if err != nil {
				return config.TargetPage{}, err
			}
			return reg.ListTargets(ctx, resolved, secrets, opts.Redactor, req)
		},
		ResolveURL: func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, origin,
			rawURL string) ([]config.TargetEntry, error) {
			secrets, err := targetSecrets(opts, secrets)
			if err != nil {
				return nil, err
			}
			return reg.ResolveTargetURL(ctx, resolved, secrets, opts.Redactor, origin, rawURL)
		},
	}
}

func targetSecrets(opts *Options, secrets *secret.Resolver) (*secret.Resolver, error) {
	if secrets != nil {
		return secrets, nil
	}
	return opts.resolver()
}

// tuiSelfTest tells the editor how its selftest commands run: with this program as the writer process, the
// way 'qatlas selftest' starts it. A program that cannot name itself offers no selftest.
func tuiSelfTest(buildVersion string) tui.SelfTest {
	exe, err := os.Executable()
	if err != nil {
		return tui.SelfTest{}
	}
	return tui.SelfTest{Version: buildVersion,
		Worker: selftest.WorkerCommand{Program: exe, Args: []string{"selftest", "worker"}}}
}

// tuiUpdater returns what the editor checks for a newer release with, or nil where it must not check: in a
// dev build, which has no release to compare with, and when QATLAS_NO_UPDATE_CHECK is set. Its installation
// hands a running vault process over or locks it around the replacement, the way 'qatlas update' does.
func tuiUpdater(opts *Options, buildVersion string) tui.Updater {
	if buildVersion == "" || buildVersion == "dev" || os.Getenv(noUpdateCheck) != "" {
		return nil
	}
	client := opts.Updater
	if client == nil {
		client = selfupdate.New(buildVersion)
	}
	return &lockingUpdater{opts: opts, client: client}
}

// lockingUpdater is the editor's updater: the client with the vault handover or lock of 'qatlas update'
// around the replacement. Note keeps what that had to say, for the editor's status line.
type lockingUpdater struct {
	opts   *Options
	client *selfupdate.Client
	note   string
}

func (u *lockingUpdater) Check(ctx context.Context) (selfupdate.Result, error) {
	return u.client.Check(ctx)
}

func (u *lockingUpdater) Update(ctx context.Context) (selfupdate.Result, error) {
	replacement := &vaultReplacement{opts: u.opts}
	result, err := replacement.hooked(u.client).Update(ctx)
	u.note = replacement.finish()
	return result, err
}

// Note is what handing over or locking the vault process said during the last installation.
func (u *lockingUpdater) Note() string { return u.note }
