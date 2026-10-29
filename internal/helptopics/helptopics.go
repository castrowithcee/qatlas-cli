// Package helptopics holds the short guides that ship inside the binary. The command line prints them as
// help topics, the manpage carries them, and the terminal editor shows them behind ?, so all three read
// the same text. The package imports nothing of this module, which keeps it usable from every one of them.
package helptopics

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Topic is one guide: the name it is asked for by, one line on what it covers, and its text. The text is plain
// ASCII with one line per paragraph, list item, or command; Wrap fits it to the width where it is shown.
type Topic struct {
	Name  string
	Short string
	Text  string
}

// All returns the topics in the order a newcomer reads them.
func All() []Topic {
	return []Topic{
		{Name: "start", Short: "Set up a first connection and run a first tool", Text: start},
		Agents(),
		{Name: "configuration", Short: "Services, credentials, connections, and defaults explained",
			Text: configuration},
	}
}

// Agents returns the guide for agents. 'qatlas agents' prints it; it is the one guide for the CLI, and MCP
// clients read the shorter guide MCP returns instead, drawn from the same source.
func Agents() Topic {
	return Topic{Name: "agents", Short: "Let an agent discover and invoke tools through qatlas", Text: agents}
}

// MCP returns the guide 'qatlas mcp' hands its client as server instructions: a copy the client cannot
// truncate, describing only the three MCP tools, their arguments, and their error codes. It is not listed by
// All, since only the MCP server and 'qatlas mcp --help' show it; 'qatlas agents' stays the one guide for
// the CLI and covers the full catalog, including MCP, in more depth.
func MCP() Topic {
	return Topic{Name: "mcp", Short: "The MCP tools qatlas.search, qatlas.describe, and qatlas.invoke", Text: mcpGuide}
}

// listMarker is the start of a numbered or bulleted item; a wrapped item continues under its text.
var listMarker = regexp.MustCompile(`^ *(\d+\. |- )`)

// Wrap breaks every line of text wider than width at its spaces. A continuation keeps the indentation of
// its line, and under a list item the column of the item's text, so items and commands stay recognisable.
// An indentation deeper than half the width is given up, and a word wider than the width is cut, so no
// line of the result is wider than width.
func Wrap(text string, width int) string {
	width = max(width, 1)
	var out []string
	for _, line := range strings.Split(text, "\n") {
		out = append(out, wrapLine(line, width)...)
	}
	return strings.Join(out, "\n")
}

func wrapLine(line string, width int) []string {
	if utf8.RuneCountInString(line) <= width {
		return []string{line}
	}
	lead := len(line) - len(strings.TrimLeft(line, " "))
	hang := lead
	if marker := listMarker.FindString(line); marker != "" {
		hang = len(marker)
	}
	if lead > width/2 {
		lead = 0
	}
	if hang > width/2 {
		hang = lead
	}
	var lines []string
	current, empty := strings.Repeat(" ", lead), true
	for _, word := range strings.Fields(line) {
		if !empty && utf8.RuneCountInString(current)+1+utf8.RuneCountInString(word) <= width {
			current += " " + word
			continue
		}
		if !empty {
			lines = append(lines, current)
			current = strings.Repeat(" ", hang)
		}
		for utf8.RuneCountInString(current)+utf8.RuneCountInString(word) > width {
			room := width - utf8.RuneCountInString(current)
			runes := []rune(word)
			lines = append(lines, current+string(runes[:room]))
			current, word = strings.Repeat(" ", hang), string(runes[room:])
		}
		current, empty = current+word, false
	}
	return append(lines, current)
}

const start = `Qatlas reaches the services you already run through named connections. A first session takes a few minutes:

1. Run 'qatlas tui'. The editor opens on an empty configuration and writes nothing before the first save. ? in the editor shows these help topics.
2. Press n in Connections (or g) for the guided setup: choose a provider, its service (the root URL of the instance), a credential (its secrets go to the system keyring, the vault, or environment variables), each reused or added right there, the scope, and the access (by effect or selected tools). The summary saves the connection.
3. Test it: the summary offers the test right away, and t in the Connections list tests the selected connection again. [ok] means the provider accepted the connection.
4. Find a tool and run it through the connection, for example with a BookStack connection:

     qatlas connections bookstack
     qatlas tools bookstack
     qatlas describe bookstack.pages.list
     qatlas invoke bookstack.pages.list --connection <name> --arg limit=5

   The result arrives on stdout as JSON. A tool that changes data also needs --confirm.
5. Stay current with 'qatlas update'; 'qatlas update --check' only reports whether a newer stable release exists, 'qatlas update --list' shows the signed releases from v0.30.0 on that can be chosen, and 'qatlas update --to <tag>' installs exactly one of them, a prerelease included. An older version is a rollback: qatlas first checks that it reads your configuration and, if not, offers the newest backup it reads, which only --restore-backup restores. Update verifies the release signature of checksums.txt against the keys built into qatlas and refuses an invalid one; a release without a signature installs and is reported as signed: false. 'qatlas update' asks for y/n at the terminal and names what the update does to running processes; an unlocked vault is handed over to the new version when the release is signed and the update behaviour is handover, otherwise it is locked first; on Windows, which has no handover, it is always locked first. 'qatlas tui' shows a newer release at its top and asks the same question before it installs with U; on Linux and macOS it then restarts itself. The command version (also Settings, Update, choose version) lists the same versions as 'qatlas update --list' and installs the chosen one with the same question, naming whether it is newer, a prerelease, or older; an older version is a checked rollback that asks separately before it restores a backup.

Keys in the editor mean the same everywhere. It opens on the Overview, which counts connections, services, credentials, agent tokens, and open approvals, shows the vault state, the next steps, a small heatmap of the latest weeks of the invocation log, and the latest calls, and opens the matching area with enter on a line; it changes nothing itself. A tab line at the top names the six areas (Overview, Connections, Activity, Access, Library, Settings), in every terminal width, with the active one in brackets and a > in front while the tab line has the focus; Access and Library hold two parts each (Tokens and Approvals, Services and Credentials) and Settings six (General, then after a bar Vault, Defaults, Transfer, Update, and Self-test), named on an option bar under the tab line, where left and right choose the part. General holds the preferences of this device: the theme (terminal, the default, takes the colours of the terminal and keeps its background; qatlas dark and qatlas light paint the whole screen in the editor's own colours; NO_COLOR turns colour off whatever is chosen, and a change applies at once), the start view (the area the editor opens on the next time), icons (unicode, the default, puts an emoji or symbol beside the area names and needs no special font; nerdfont uses Nerd Font icons instead, which show as boxes without a Nerd Font installed in the terminal, see nerdfonts.com; none shows no icons; a terminal without UTF-8 shows none whatever is chosen), and language (english, deutsch, or system, which follows the language of the device; a change applies at once and covers the editor's own texts, while guides, core messages, and provider texts stay English), and mouse (on or off; on is the default and takes effect at once). With the mouse on, a click does what the key for it does: it chooses a tab (from any level, going back the way esc does, with the same question about unsaved input), the [esc] back button in the header (one level back, where esc leads one), a part on the option bar, a row (the first click marks it, a click on the marked row opens or ticks it), a group (folds it), a field or section of a form, a value of an option bar (on a filter field it opens the list of values), a button of a dialog, or an (i) mark (opens its explanation), and the wheel scrolls; a click is never stronger than its key, so questions, confirmations, and the admin session apply as before, and it never types a character. The terminal then passes the mouse to the editor, so to select text hold Shift while dragging (macOS Terminal: fn, iTerm2: Option, VS Code on macOS: Option-click), or turn the mouse off in General. They are chosen from fixed lists, saved with F2 without an admin session or the vault's passphrase, and kept in tui.yaml beside config.yaml; that file belongs to this device, is not part of the configuration, and is neither exported nor imported. Below them, General lists the keys of the global actions save, help, command line, lock, and info: enter on a row and then pressing the new key binds it (a key that is already taken, a character for save, lock, or the extra info key, a letter, ctrl+c, esc, tab, or a digit is refused with its reason), d removes the own key so the standard applies again, and info can take a second key besides i that also works in text rows, such as F3; they are kept under keys in tui.yaml, and a key there that breaks a rule falls back to the standard with a warning at the start. Terminals and desktops take some F-keys for themselves (F1, F5, and F10 to F12 are never used), so choose one your terminal passes through; the footers, and in the editor the list at the end of this guide, show the keys in force, while the text here names the standard ones. The later parts only gather the export, import, update, and selftest actions; each keeps the rules it had. On the tab line left and right choose an area, 1 to 6 open one directly, on its first part, from the tab line, a list, or the Logs screen, down moves to the option bar and then, like enter or tab, into the list, and tab or esc come back to the bar. The arrow keys move on every screen, and h, j, k, and l do the same wherever no text is typed; pgup, pgdown, home, and end jump in every list, tab and shift+tab go to the next or previous field or area, and ? opens this help on every screen without a text line (a form keeps it for its text rows), with esc or ? returning to where you were. A field shows one line of hint under it; i opens its longer explanation, and the explanation of the word it is about (service, credential, connection, target, effect, profile, approval, token, vault), over the screen, and esc closes it. i works on every screen without a text line, and on a search line while it is empty. From a terminal width of 120 columns the explanation stands in a pane at the right of forms and pickers instead, and below it a field or entry that has one ends in (i). : opens the command line on the same screens: type to narrow the commands by name, choose with up, down, or tab, enter runs one, and esc closes it; a command that is not possible on that screen says why, so the same actions as the keys (switching areas, the guided setup, export, import, update, locking the vault, help, quit) are one line away. Each selftest scenario is a command too, such as selftest storage: it measures this machine on synthetic data in a scratch directory that is removed again, runs beside the editor and shows its progress, esc ends it, and W writes the result as JSON to a file in the temporary directory, whose path is shown. esc is one level back; in the guided setup it is one step back, and on the first step it cancels the setup. Input that is not saved is never dropped silently: the editor asks, F2 saves (in the setup it goes on to the next step), d discards, and esc keeps editing. ctrl+c quits at once and asks the same when something unsaved would be lost; q quits from the tab line, a list, and the Logs screen. Every other letter means one thing on every screen without a text line, and none acts where text is typed: n creates a new entry, path, or target (in Credentials it first asks for a credential or a payload secret), g starts the guided setup, e edits the selected entry like enter, p duplicates an entry, t tests a connection, U installs an update, A approves everything open in Approvals, V shows or hides a token's value in its detail, s shows the summary of the Logs, f their filter, and M their MCP sessions, o opens Activity on an approval's connection, c hands the full text of an open text or detail area (a log entry, the Raw tab, an explanation, an approval's detail) or, with none open, the name of the marked entry to the terminal for the clipboard, and nothing but what is shown: no secret value, no control characters; it travels as an OSC 52 sequence, which also works over SSH but which the terminal does not confirm, and some terminals need it enabled (tmux: set-clipboard on), W saves a selftest result as a file, and space ticks, and in an approval's detail shows or hides the unchanged entries. d deletes, removes, or revokes on every screen without text entry, ctrl+d on a text line such as a payload field; both ask first, y confirms, and n or esc keeps. r renames the selected service, credential, or connection on its list, and edits the label of a target on the target list: it asks for the new name, lists everything the rename touches, including what qatlas cannot change and the agent tokens whose cover ends, and renames only after y; n or esc leaves everything as it was. On Connections, Credentials, and Approvals a hint line under the list names the renames the vault shows that the configuration does not, a connection or credential renamed by hand in config.yaml or the rest of a rename that stopped early; down from the last row and then enter, or a click on the line, lists them and offers an action for each: enter adopts a hand rename or finishes an unfinished one, B puts a credential's unfinished rename back; each shows what it would change first and acts only after y, and a case that could belong to more than one entry is only shown. The command export, an admin action from the tab line or a list, writes configuration to a file in steps: tick what to take (a connection comes with its service and credentials; agent tokens, approvals, the log, and the vault settings are never exported; space ticks, enter goes on), choose whether the file holds secrets, name the file, read the preview, and press F2 in the preview to write. F2 goes on and esc goes one step back, as in the guided setup; on the first step esc cancels, asking first once something is ticked. Nothing is written before that F2, and an existing file is never replaced. Secrets are offered only where the vault has a passphrase and the admin session proves it: encrypted with an export passphrase that is typed twice and is not the vault passphrase, or, after a warning and an own y, as plain text. The preview and the result name credentials and roles, never a value; the file is imported on another installation with the command import or qatlas config import. The command import, also an admin action, takes such a file over in steps: name the file, type its export passphrase if it is encrypted, choose the mode (create stops where a name already exists, merge keeps existing entries, overwrite replaces them), read the preview, and press F2 in the preview to write; F2 and esc work as in the export, and nothing is written before that F2. The preview lists every entry with its action and id, what a replacement changes in this installation, the local access a connection brings, and per credential the roles of the secrets the file brings, never a value, and where they go. After the write it asks whether to approve the imported connections where the vault is encrypted and, for a file with plain-text secrets, whether to delete it; only y does either, and esc or n leaves things as they are. A vault that gets its first secret offers a passphrase first.

'qatlas help agents' explains how an agent uses qatlas, and 'qatlas help configuration' what services, credentials, connections, and defaults are. 'qatlas config validate' checks the configuration file.`

const agents = `An agent uses qatlas like a person on the command line, through connections a person configured beforehand. It never handles a secret: of a connection it knows only the name, its one-line description, and the effects it may use.

A tool is one versioned contract with an ID of dot-separated segments: <provider>, an optional <service>, <object> and <action>. A provider is one segment or <vendor>.<product>; only a provider with a service level has the <service> segment. Words in a segment are joined by single hyphens. A provider is the namespace of its tools, and a connection is a configured route through which its tools run.

Three lines tell the systems apart. A provider's description says what kind of system it is. Its optional note, kept by a person, says what it stands for in this installation, for example wiki or CRM. A connection's description says what one route is for. Map a word of the task to a provider by its description and note, then choose the connection by its description.

Discover in small steps, in this order, then invoke. Discovery writes TOON; --output json returns JSON.

  qatlas agents                     this guide
  qatlas providers                  every provider: what kind of system it
                                    is, the note on what it stands for
                                    here, the number of its tools, of the
                                    connections that can run them, and of
                                    all configured connections
  qatlas connections <provider>     the configured connections: description,
                                    permitted effects, whether a tools list
                                    narrows them, and which ones a locked
                                    vault keeps unusable
  qatlas tools <provider>           the tools those connections offer, each
                                    with its required arguments and form,
                                    and whether it needs confirmation; the
                                    connections are named once above them
  qatlas describe <tool-id>         one compact contract: the arguments and
                                    result fields as tables, risk, examples,
                                    and the connections that can run it;
                                    --full adds both schemas
  qatlas invoke <tool-id> --connection <name>
                                    run it

Narrow or widen the catalog:

  qatlas tools --query "<terms>"    search every provider, best match
                                    first; the terms match the ID, title,
                                    description, tags, provider notes and
                                    connection descriptions
  qatlas tools <provider> --connection <name>
                                    only the tools that connection offers
  qatlas tools <provider> --all     also the tools no connection offers,
                                    each with the reason
  qatlas tools <provider> <group>   a provider with many tools (more than
                                    50) answers with its groups first;
                                    this lists the tools of one group;
                                    --query still searches every group

Invoke:

  qatlas invoke <tool-id> --connection <name> --arg name=value
  qatlas invoke <tool-id> --connection <name> --arg 'labels=["bug"]'
  echo '{"name":"value"}' | qatlas invoke <tool-id> --connection <name>

--arg is repeated once per argument and typed by the input schema: a string as written, a number, true or false, and a list or an object as JSON. The whole arguments object as one JSON object on stdin is the equal alternative; with --arg, stdin is not read. Every invoke ends within 60 seconds; what reaches that limit ends with timeout and the next step. A tool whose contract requires confirmation runs only with --confirm, and the audit event of that change goes to stderr. The result is one JSON object with a data field on stdout; empty fields (null, empty arrays, empty objects) are left out of it, so a missing field means empty, while "", 0 and false stay. A field that a filter you set fixes, such as state with state open, is missing from the entries too. --fields number,title (comma separated or repeated) keeps only those members in each entry of the tool's result list, the selectable_fields of 'qatlas describe'; the other members of the result stay, and an unknown name or a tool without a result list is refused with invalid-request. --agent keeps other output machine-readable, without prose or colour.

Pass --connection only when the choice is open. Without it qatlas takes the default of the tool ID, then the default of its provider, then the one connection of this project that offers the tool, for reading and changing tools alike; a default whose connection does not offer the tool is refused. With several candidates and no default it fails with connection-ambiguous, and the connection descriptions are the basis for choosing one.

Errors go to stderr as "qatlas: <code>: <message>"; branch on the code, not on the message. Exit code 0 is success.

Exit code 2 is a problem of the request or the configuration:

- usage: the command, a flag, or a field name is wrong.
- invalid-request: the arguments do not satisfy the input schema, or the request is malformed.
- config-missing: there is no configuration file; a person creates one with 'qatlas tui'.
- config-invalid: the configuration, or a file beside it, is not usable; only a person fixes it.
- connection-selection: no connection of this project offers the tool; 'qatlas describe <tool-id>' names the connections that do, and only a person changes a connection, in 'qatlas tui'.
- unknown-connection: the named connection is not configured, or it is bound to the paths of another project; 'qatlas connections <provider>' lists the ones this project can use.
- connection-ambiguous: several connections offer the tool; one JSON line follows that lists the candidates, choose one and pass it with --connection.
- unknown-operation: no tool has this ID or version; find it with 'qatlas tools <provider>'.
- unsupported-capability: the connection does not offer the tool; one JSON line follows with the connection and the reason (effect-not-permitted, requires-tool-allow-list, not-in-tools-list, no-local-files, outside-targets, connection-targets-outdated, connection-disabled, or other-provider). 'qatlas describe <tool-id>' names the connections that do, and only a person changes a connection, in 'qatlas tui'.
- missing-secret: the credential of the connection yields no secret; only a person stores it.
- confirmation-required: the tool changes data and runs only with --confirm; a tool with local files also needs it before it replaces an existing local file.
- policy-denied: local policy refuses the call.
- admin-required: 'qatlas credential set', 'qatlas credential delete', and 'qatlas vault encrypt', 'passphrase', 'decrypt', 'migrate', 'approve', and 'token' manage a credential or the vault, and run only when a person is at the terminal, typing the vault passphrase there if it is encrypted; agents never manage a credential or the vault, so stop and ask the user to run the command themselves, in a terminal or in 'qatlas tui'. The one exception is 'qatlas vault approve' with an agent token, described below; there admin-required means no token was found, the token is unknown, revoked, or expired, or it does not cover a change, and the message names each such change and what of it lies outside.
- approval-required: the connection reads its secret from an encrypted vault, which hands it only to a connection a person approved as it is configured now, and this one was never approved or changed since, its service URL, provider, effects, targets, tools list, paths, files, forward_secrets, the fields of a forward credential it lists, or credential; nothing was read or sent. An agent never runs 'qatlas vault approve' either, so stop and ask the user. 'qatlas connections' marks the connections this affects as unusable.
- secret-ref-not-allowed: an argument names a secret reference that this connection does not release, or that is not a forward credential; nothing was read or sent, and only a person changes the connection's forward_secrets. A reference the connection does release, whose secret lies in an encrypted vault, is handed out only after a person approved the connection with that release; until then the call fails with vault-locked or approval-required and 'qatlas connections' marks the reference unusable with the same code.

Exit code 1 is a runtime or provider failure:

- vault-locked: the credential's secret sits in an encrypted vault that no vault process holds unlocked, and no terminal is attached to ask for its passphrase; agents cannot unlock a vault, so stop and ask the user to run 'qatlas vault unlock' in a terminal, which on Linux, macOS, and Windows keeps it unlocked for later calls. 'qatlas connections' marks the connections this affects as unusable.
- unreachable: the provider host did not answer.
- tls: the TLS connection to the provider failed.
- auth: the provider rejected the credential; stop and report the code.
- permission: the credential may not do this; stop and report the code.
- not-found: the provider does not hold the resource or does not show it to this credential; the message names the target it addressed, so check that target and what the credential may see.
- timeout: the provider did not answer in time; a non-idempotent call is not retried.
- rate-limited: the provider refuses further requests for now; wait before retrying.
- invalid-provider-response: the provider result does not satisfy the output schema.
- provider-error: the provider answered with something unusable.
- runtime: anything else failed.

MCP: 'qatlas mcp' serves the same catalog over stdio as three fixed MCP tools, with the same connections, rules, and error codes. qatlas.search finds tools like 'qatlas tools', with the required arguments and their form, the target argument where a connection leaves it open, whether confirmation is needed, and the connections that offer them named once; describe is needed only for the other optional arguments or an unclear contract. all set to true adds the others with their reason. qatlas.describe returns one compact contract like 'qatlas describe', or with full set to true the complete one like 'qatlas describe --full', and qatlas.invoke runs one like 'qatlas invoke', with confirm for --confirm. qatlas.describe and qatlas.invoke take the tool ID as operation and a connection name as connection; qatlas.invoke takes arguments as an object. A successful call returns its result once, as one JSON text block in content. A failed call is a tool result with isError and structuredContent carrying the code. Where a provider answered the refusal, structuredContent adds provider with its own words, apart from Qatlas' message. The server hands its client a shorter guide as instructions, which the help of 'qatlas mcp' shows too. Add the command "qatlas mcp" as a stdio server to the agent's client; it speaks MCP 2026-07-28 with the protocol version and client capabilities in the _meta of every request, and MCP 2025-11-25 and 2025-06-18 after an initialize request. An agent runs 'qatlas update' only after the user approved the update, and then as 'qatlas update --yes'; without --yes, and without a terminal, the command refuses. On Linux and macOS the MCP server restarts itself after an update; an unlocked vault is handed over to the new program when the release is signed and the update behaviour is handover, and only a vault the update locked needs 'qatlas vault unlock' again; on Windows an update always locks the vault process. A server that cannot restart itself (Windows, or a failed restart) and is refused by the vault process after an update fails with the request to reconnect the MCP server in the client; 'qatlas tui' and 'qatlas web' in that case name the restart of their own command. On Linux and macOS 'qatlas tui' restarts itself after the update it asked for, and opens the unlock dialog again only if the update locked an unlocked vault; 'qatlas web' and 'qatlas update' leave the restart to the person.

Agent tokens: a person may hand an agent an agent token, which lets it approve connection changes itself, within limits. The token names one or more model connections; a change to a connection that reads a vault credential, creating, changing, or deleting it, is covered when it keeps the service and the credential of a model and asks for no more than it: effects among the model's, tools within its tools list, targets among its targets, paths inside its paths, where the model has any, and files inside the directories the model releases in the same direction (a change that releases no directory is always covered, one that releases any where the model releases none never), and exclusions that keep every exclusion of the model (they may be kept or added, never dropped). Anything else, a new credential, a secret, the vault's settings, a change to a model itself, stays with the person.

  1. After the person or the agent changed a connection, 'qatlas connections' marks it approval-required.
  2. Run 'qatlas vault approve' without a terminal. It reads the token from QATLAS_AGENT_TOKEN, else from the nearest .qatlas/local/agent.env of the working directory or a directory above it, else from ~/.qatlas/local/agent.env, a line QATLAS_AGENT_TOKEN=<token>; --connection <name> limits it to one change.
  3. It approves every covered change and names it. A change partly outside stays open whole, and the command fails with admin-required naming the connection and the fields outside, such as effects or paths; stop and ask the user to approve it. vault-locked means the vault is locked; ask the user to run 'qatlas vault unlock'.

Never print, copy, or pass on a token, never write an agent.env file, and never try to reach beyond a token's limits; it guards against mistakes, not against intent, and every approval is logged with the token's name.

Tell the agent which connections it may use and what each one is for, and whether it may change data. Never hand it a token, password, or key: qatlas reads the secrets itself.

A block for the AGENTS.md or CLAUDE.md of a project:

  ## Qatlas
  Use the qatlas CLI to reach <what the connections are for>.
  - Start with 'qatlas agents' and follow its discovery order.
  - Connections: <name> for <purpose>; <name> for <purpose>.
  - Changes: <allowed with --confirm | not allowed, read only>.
  - Before invoking, run 'qatlas tools <provider> --connection <name>' and 'qatlas describe <tool-id>'.
  - Invoke with 'qatlas invoke <tool-id> --connection <name>' and pass the arguments as --arg name=value, a list or an object as JSON, or as one JSON object on stdin.
  - Never ask for, pass, or print a secret. On an auth or permission error, stop and report the code.`

const configuration = `The configuration file has four sections and optional provider notes. 'qatlas tui' edits them and 'qatlas config validate' checks them. 'qatlas tui', 'qatlas web', and the commands that write the file may run at the same time: a change based on an older state of the file is refused, nothing is saved, and the editor reloads so the change can be repeated; nothing is merged for you.

'qatlas config export' writes chosen services, credentials, and connections, with what they need, to a new file without any secret, and 'qatlas config import' takes such a file over into another installation: it lists what it would create, replace, or skip, writes nothing until you confirm, and then writes all of it or nothing. Both run from an interactive terminal like every management command. The CLI has no export with secrets. Import also takes a file that brings secrets, encrypted (it asks for the export passphrase) or not: it lists per credential the roles it brings, never a value, writes the secrets to the keyring or the vault (--secret-store chooses for this import), offers to delete a plain-text file afterwards, and asks whether to approve the imported connections where the vault is encrypted. A credential without secrets in the file arrives without one; 'qatlas credential set' stores it, and new connections need the usual release.

Services say where: one provider and the root URL of one of its instances as base_url. A provider with a public API, such as GitHub, Telegram, or Todoist, uses that API when base_url is left out; a self-hosted system such as BookStack or Nextcloud always needs it. Two instances of one provider are two services.

Credentials say with what: where the secrets of a provider come from, never the secrets themselves. Type keyring keeps them in the system keyring of this machine; 'qatlas credential set' or enter on a role row in the editor stores them. Type vault keeps them instead in the vault, a directory beside the configuration, unencrypted or encrypted to a passphrase, for a machine without a usable system keyring; 'qatlas vault status' shows its state, 'qatlas vault unlock' opens it, and 'qatlas vault encrypt', 'qatlas vault passphrase', and 'qatlas vault decrypt' set, change, and remove its passphrase. defaults.secret_store names which of the two a new credential starts from, keyring unless it is set to vault; neither is a recommendation, just the preselected choice. A set variable QATLAS_<CREDENTIAL>_<ROLE> overrides either one, for CI and containers. Type env names one environment variable per secret role. 'qatlas tui' offers system keyring, vault, and environment variables as its secrets choice, preselected the same way; storing the vault's very first secret there, or with 'qatlas credential set', offers a passphrase, typed masked and twice, and leaving it empty keeps the vault unencrypted. A plaintext credentials.yaml left over from an earlier version is still read for a keyring credential while it exists, and 'qatlas vault migrate', or migrate credentials.yaml in the editor's Settings > Vault part, carries it into the vault and removes it. 'qatlas credential set', 'qatlas credential delete', and every 'qatlas vault' command but status, unlock, and lock manage a credential or the vault: they run only from an interactive terminal, and where the vault is encrypted they ask for its passphrase there too, whatever credential they target; without a terminal, or with the wrong passphrase, they fail with admin-required or usage before touching anything. An agent never runs them; a person does, in a terminal or in 'qatlas tui'. The one exception is 'qatlas vault approve' with an agent token: 'qatlas vault token create' makes one, naming its model connections with --model, and an agent that presents it, in QATLAS_AGENT_TOKEN or a .qatlas/local/agent.env, approves without a terminal the connection changes that reach no further than a model connection of the token; 'qatlas help agents' explains how. While the vault is encrypted, it hands a vault credential's secret only to a connection a person approved as it is configured: a connection whose service URL, provider, effects, targets, tools list, paths, files, or credential changed since fails with approval-required until it is approved again; 'qatlas vault encrypt' approves every connection that reads from the vault at that moment, and 'qatlas vault approve' lists what changed about every connection still open, for a list what is new and what falls away rather than both lists whole (--output json adds added, removed, and kept), says in a source line where the change came from (changed in qatlas tui, web, or cli with its time, taken from the invocation log; changed outside qatlas when the log holds no such change, so config.yaml was edited directly, by an agent among others; the vault entry stored anew; or origin unknown when the log cannot be read; --output json carries it as origin_of_change; it informs and never approves), and releases it, all at once or one at a time with --connection. An unencrypted vault binds no connection.

The vault's idle timeout sets how long a vault process that holds the vault unlocked does so without a read before it locks itself, a Go duration such as "12h" or "30m", 12h unless set. 'qatlas tui' opens read-only while the vault is encrypted; the first managing action of a run asks for its passphrase, masked, in a screen of its own, and a right answer starts an admin session bound to that window alone, no other window and no vault process sharing it. Its header always shows the vault's state, unlocked, locked, or unencrypted, plus, while it is encrypted, that window's own admin session; ctrl+l works on the tab line, lists, forms, pickers, tables, target and path lists, setup summary, and Logs screens, never over a question, a masked prompt, the template choice, or the help, and unlocks a locked vault, handing it to a vault process on Linux, macOS, and Windows the same way 'qatlas vault unlock' does, or, once unlocked, asks "Lock the vault now?" and locks it again the same way 'qatlas vault lock' does. The admin session timeout of the vault sets how long that session stays open without a key press before it asks again, a Go duration such as "10m" or "0s", 10m unless set; "0s" asks for the passphrase on every managing action instead of keeping a session at all. 'qatlas tui' offers a Vault part (Settings, 6) that shows the vault's state, turns its encryption on or off, changes its passphrase, migrates a leftover credentials.yaml into the vault, and sets the update behaviour, the idle timeout, the admin session timeout, and the log retention. The update behaviour is handover or lock (handover unless set) and controls whether 'qatlas update' hands an unlocked vault over to the new program or locks it (Windows has no handover and always locks); it is stored authenticated inside the encrypted vault as settings.age, never in config.yaml, and changed only in an admin session: 'qatlas vault handover' shows it, 'qatlas vault handover lock' or 'qatlas vault handover handover' sets it (interactive terminal, passphrase every time), and the update behaviour row in the Vault part changes it while the vault is unlocked. The idle timeout, the admin session timeout, and the log retention are stored the same way, in settings.age and never in config.yaml: 'qatlas vault settings' shows them, and its flags --idle-timeout, --admin-timeout, and --retention-days set them (interactive terminal, passphrase every time), and the rows of the Vault part change them while the vault is unlocked. A settings.age that was not written by this vault reads as lock with a warning; 'qatlas vault passphrase' keeps the setting and 'qatlas vault decrypt' removes it. Saving a change in that session approves exactly the connections it directly saved or newly opened; an Approvals part (Access, 4) lists every connection still open otherwise, with what is new and what falls away in each changed list and how many entries stay (space spells them out), to approve one at a time or all at once. Each shows a source line that says where the change came from, as 'qatlas vault approve' does, and o opens Activity (3), the invocation log, on that connection since it was last approved.

The vault's log retention sets how many days of the invocation log are kept, a positive integer, 90 unless set. Every invoke over the CLI and MCP appends one entry to a daily file under the vault directory, whether the vault exists yet, is locked, or is unencrypted, hash-chained across day boundaries so a changed or missing entry can be found later; it never records an argument, a search term, a result, a secret, a target, or a URL query. Over MCP, each entry carries the random session ID of its 'qatlas mcp' process, and the initialize, server/discover, and tools/list exchanges and every qatlas.search and qatlas.describe call are logged too, with the byte sizes of their JSON-RPC messages and no connection; 'qatlas vault logs stats' sums them up per session as baseline, navigation, and invoke with token estimates, as does the m key of the editor's Logs screen. Every change qatlas itself makes to a connection in config.yaml, in the editor, the web interface, or the CLI, also appends one entry per connection, an operation named config.connection plus create, change, delete, or rename, with the way (tui, web, or cli), the connection's name and id, the former name for a rename, and the result, never a value; the Approvals source line reads them. Every read of a provider to choose targets, in the editor or the web interface, appends one entry too, an operation named config.target plus list and the kind, or resolve, with the way (tui or web), the connection's name and id, and the result, never a target, a name, a search text, or a URL; a read for a connection that is not saved yet is named config.target plus draft.list and the kind, or draft.resolve, and names (draft) instead of a connection. A day older than the retention window is removed on the next invoke, and the entry written right after that names what was removed, so 'qatlas vault logs verify' never mistakes a documented cut for a break. An entry written while a vault process holds the vault unlocked, or by a run that unlocked it itself, also carries a check value under a key derived from the vault's key; every other entry is unverified. That command walks the chain, has a running vault process check the check values, and reports one row per day: its entry count, how many entries carry no check value, how many carry one nobody could check because the vault is locked, how many fail their check value, how many are missing, and the first problem found; it exits successfully while the chain is intact, whatever entries are unverified or unchecked.

Connections are the routes an agent takes: one service and one credential, an optional scope inside the service (target or targets), the permitted effects (read, create, update, delete, execute), an optional tools list of tool IDs, an optional one-line description, and an on/off state: a person can disable a connection without losing its selection, or limit it with active_until, a date as YYYY-MM-DD or an RFC 3339 time, and it then offers no tool (reason connection-disabled) from the moment the limit passes. A tool runs through a connection only when its effect is permitted and, where a tools list exists, the list names it; a tool marked requires_tool_allow_list runs only through a connection whose tools list names it. 'qatlas tools <provider> --all' shows the tools a connection does not offer and why. Where a provider accepts several targets, targets is an allow-list: for GitHub it is optional, may mix repositories and projects, allows patterns such as repos/OWNER/*, and a tool names the repository or project it acts on, which must lie inside the list. Optionally, target_labels maps a target of the connection to a one-line label for the person, such as area/5001: Projects; a label is display text only, grants nothing, needs no new approval, and is never shown to an agent; in 'qatlas tui' the target list of a connection sets, changes, and removes it with r. Where a provider supports it, exclude lists objects of the connection's service that the connection never reaches, though its targets or credential would (the provider's documentation names what an exclusion can be); an exclusion wins over every approval and takes its object with everything below it out, but it locks no tool, and it is never shown to an agent; in 'qatlas tui' the target list of a connection switches an entry between reachable and excluded with space, and the add menu (or the command add exclusion) takes the entries chosen next, from the provider's list, a web address, or an ID, as exclusions; in 'qatlas web' the setup page has an exclusions field, a switch on each target's label row, and an add as exclusion button in the chooser. A change of exclude needs a new approval.

Paths optionally bind a connection to projects: a list of directories, absolute or starting with ~/, each covering itself and everything below it, compared by whole directory names, so ~/repos/kunde-a does not cover ~/repos/kunde-ab; glob patterns are refused. The project of a call is its working directory, or the root of the Git repository it lies in, with symbolic links resolved; a linked worktree checked out elsewhere also counts as its repository's main working tree, and 'qatlas mcp' uses the file:// roots its client names after initialize, or else the directory it was started in. Outside its paths a connection is missing from connections, providers, tools, describe, search, and every candidate list, a default that names it does not apply, and naming it fails with unknown-connection. Without paths it applies everywhere. 'qatlas config validate' and 'qatlas tui' still show every connection, and validate warns about an entry that names no existing directory. The binding keeps one customer's connections out of another customer's project by mistake, not by force: an agent that changes into a bound directory sees the connection there. While the vault is encrypted, a change of paths needs a new approval.

Files release local directories to the tools of a connection that read or write local files: files.read lists the directories such a tool may read files from (an upload), files.write those it may write files to (a download), each directory absolute or starting with ~/ and covering itself and everything below it. Without a list for a direction the connection gives no local file access in it: a tool of that direction is not offered, and 'qatlas tools --all' names the reason no-local-files. A list is accepted only for a direction the connection's provider has a tool for, never names the file system root, the whole home directory, or a path with '..'. 'qatlas connections' and 'qatlas describe' name the released directories per direction, so a caller knows where a tool may work. 'qatlas config validate' warns about an entry that names no existing directory, naming its position and never the path. 'qatlas tui' offers the uploads and downloads rows, and 'qatlas web' the upload and download fields, only for such a provider. While the vault is encrypted, a change of files needs a new approval.

Forward secrets let a tool pass a stored secret on to a third party by reference, so a person or an agent never types the value. A credential with forward: true and fields (the names of its values, for example username and password) is such a payload secret: it belongs to no provider, uses type vault or keyring, never serves a connection itself, and is stored with 'qatlas credential set <credential> <field>'. A connection releases it to its tools with forward_secrets, a list of such credentials; without the list it releases none. A tool argument that is marked as a secret reference takes the credential's name, never a value: the core checks the name against forward_secrets before the confirmation gate, and only a confirmed call reads the fields, which the tool puts into the request it sends; every output, error, audit event, and invocation log entry shows at most the reference name, and a mirrored value appears as [redacted]. A name the connection does not release fails with secret-ref-not-allowed. While the vault is encrypted, forward_secrets and the fields of a credential listed there are part of the connection's approval: a change needs a new approval, and an agent token covers it only when the connection releases a subset of what the token's model connection releases, each credential with the same fields. A forward credential in an encrypted vault is released only through a connection whose own credential is in the vault too. Without an encrypted vault only forward_secrets guards the release.

Provider notes say what a provider stands for here, one optional line per provider ID under provider_notes, for example bookstack: wiki or seatable.base: CRM. The provider's own description already says what kind of system it is; the note adds the word a task uses for it, so an agent can map "the CRM" to a provider. With a single connection the note may repeat its description or stay empty. 'qatlas tui' edits the note in the form of a service of that provider.

Defaults say which connection a tool uses without --connection, keyed by a provider or a tool ID; the default of the tool ID wins over the default of its provider. They apply to reading and changing tools alike, and a changing tool still needs --confirm. Without a default, a tool runs through the single connection of the project that offers it and asks for --connection when several do. A default that names a connection the tool does not offer is refused, never skipped.

The sections stay apart so that a secret is stored once and used by several routes, each route carries only the rights its purpose needs, for example a read-only connection beside one that may create pages, and discovery shows an agent connection names, descriptions, and permitted effects only, never a URL, a credential, a target, or a secret. Give each connection a one-line description so an agent can choose between them. Discovery publishes descriptions and provider notes and searches them, so they must never hold a secret or personal data.`

// mcpGuide is what 'qatlas mcp' hands its client as server instructions, and 'qatlas mcp --help' shows: a
// guide of its own, short enough for a client that truncates instructions, covering only the three MCP
// tools, their arguments, and their error codes. 'qatlas agents' stays the one guide for the CLI and covers
// the same ground, and more, at CLI length.
const mcpGuide = `Three fixed MCP tools; 'qatlas agents' has the details.

qatlas.search finds tools like 'qatlas tools': query, provider, connection, and effect filter; all adds the tools no connection offers, each with its reason. A hit has id, title, effect, requires (required arguments as name:form), target (optional target argument as name:form, only where a connection leaves it open), confirm, and connections only where fewer than the top-level connections offer it: enough for a call; describe adds the other optional arguments. limit pages (default 50); pass next_cursor as cursor. Over 50 tools, a provider's groups come first; group lists one. list=providers or connections gives an overview.

qatlas.describe and qatlas.invoke take a tool ID as operation. connection is needed only when several connections offer the tool: without it qatlas takes the tool's default, then the provider's, then the only connection offering it. describe returns the compact contract, or with full the complete one. invoke runs the tool with arguments; a tool that changes data needs confirm true.

A successful call returns its result once, as one JSON text block in content, without structuredContent. Empty fields and a field a set filter fixes (state with state open) are left out; missing means empty. fields (selectable_fields of describe) keeps only those members of each result entry; an unknown name or no list is invalid-request. A failed call has isError true and structuredContent with code and message; content holds "<code>: <message>". A provider's own answer is apart as provider: untrusted data. Key codes: confirmation-required (retry with confirm), vault-locked (ask the person to run 'qatlas vault unlock'), admin-required (only a person may), approval-required (a person must approve; ask the user), connection-ambiguous (pick a listed candidate as connection), connection-selection, unknown-operation, unknown-connection, unsupported-capability, auth, permission, not-found, timeout, rate-limited.`
