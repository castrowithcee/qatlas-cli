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
2. Press c for the guided setup: choose a provider, its service (the root URL of the instance), a credential (its secrets go to the system keyring, the vault, or environment variables), the scope, and the permissions. The summary saves the connection.
3. Test it: the summary offers the test right away, and t in the Connections list tests the selected connection again. [ok] means the provider accepted the connection.
4. Find a tool and run it through the connection:

     qatlas connections bookstack
     qatlas tools bookstack
     qatlas describe bookstack.pages.list
     qatlas invoke bookstack.pages.list --connection <name> --arg limit=5

   The result arrives on stdout as JSON. A tool that changes data also needs --confirm.
5. Stay current with 'qatlas update'; 'qatlas update --check' only reports whether a newer stable release exists. 'qatlas tui' shows a newer release at its top and installs it with u.

'qatlas help agents' explains how an agent uses qatlas, and 'qatlas help configuration' what services, credentials, connections, and defaults are. 'qatlas config validate' checks the configuration file.`

const agents = `An agent uses qatlas like a person on the command line, through connections a person configured beforehand. It never handles a secret: of a connection it knows only the name, its one-line description, and the effects it may use.

A tool is one versioned contract with an ID of the form <provider>.<object>.<action>. A provider is the namespace of its tools, and a connection is a configured route through which its tools run.

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
- unsupported-capability: the connection does not offer the tool; one JSON line follows with the connection and the reason (effect-not-permitted, requires-tool-allow-list, not-in-tools-list, or other-provider). 'qatlas describe <tool-id>' names the connections that do, and only a person changes a connection, in 'qatlas tui'.
- missing-secret: the credential of the connection yields no secret; only a person stores it.
- confirmation-required: the tool changes data and runs only with --confirm.
- policy-denied: local policy refuses the call.
- admin-required: 'qatlas credential set', 'qatlas credential delete', and 'qatlas vault encrypt', 'passphrase', 'decrypt', 'migrate', 'approve', and 'token' manage a credential or the vault, and run only when a person is at the terminal, typing the vault passphrase there if it is encrypted; agents never manage a credential or the vault, so stop and ask the user to run the command themselves, in a terminal or in 'qatlas tui'. The one exception is 'qatlas vault approve' with an agent token, described below; there admin-required means no token was found, the token is unknown, revoked, or expired, or it does not cover a change, and the message names each such change and what of it lies outside.
- approval-required: the connection reads its secret from an encrypted vault, which hands it only to a connection a person approved as it is configured now, and this one was never approved or changed since, its service URL, provider, permissions, targets, tools list, paths, or credential; nothing was read or sent. An agent never runs 'qatlas vault approve' either, so stop and ask the user. 'qatlas connections' marks the connections this affects as unusable.

Exit code 1 is a runtime or provider failure:

- vault-locked: the credential's secret sits in an encrypted vault that no vault process holds unlocked, and no terminal is attached to ask for its passphrase; agents cannot unlock a vault, so stop and ask the user to run 'qatlas vault unlock' in a terminal, which on Linux keeps it unlocked for later calls. 'qatlas connections' marks the connections this affects as unusable.
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

MCP: 'qatlas mcp' serves the same catalog over stdio as three fixed MCP tools, with the same connections, rules, and error codes. qatlas.search finds tools like 'qatlas tools', best match first, with the required arguments and their form, whether confirmation is needed, and the connections that offer them named once; describe is needed only for the optional arguments or an unclear contract. all set to true adds the others with their reason. qatlas.describe returns one compact contract like 'qatlas describe', or with full set to true the complete one like 'qatlas describe --full', and qatlas.invoke runs one like 'qatlas invoke', with confirm for --confirm. qatlas.describe and qatlas.invoke take the tool ID as operation and a connection name as connection; qatlas.invoke takes the arguments object as arguments. A successful call returns its result once, as one JSON text block in content. A failed call is a tool result with isError and structuredContent carrying the code. The server hands its client a shorter MCP guide as instructions instead of this one; the help of 'qatlas mcp' shows that guide too. Add the command "qatlas mcp" as a stdio server to the agent's client; it speaks MCP 2026-07-28 with the protocol version and client capabilities in the _meta of every request, and MCP 2025-11-25 and 2025-06-18 after an initialize request.

Agent tokens: a person may hand an agent an agent token, which lets it approve connection changes itself, within limits. The token names one or more vorbild connections; a change to a connection that reads a vault credential, creating, changing, or deleting it, is covered when it keeps the service and the credential of a vorbild and asks for no more than it: permissions among the vorbild's, tools within its tools list, targets among its targets, and paths inside its paths, where the vorbild has any. Anything else, a new credential, a secret, the vault's settings, a change to a vorbild itself, stays with the person.

  1. After the person or the agent changed a connection, 'qatlas connections' marks it approval-required.
  2. Run 'qatlas vault approve' without a terminal. It reads the token from QATLAS_AGENT_TOKEN, else from the nearest .qatlas/local/agent.env of the working directory or a directory above it, else from ~/.qatlas/local/agent.env, a line QATLAS_AGENT_TOKEN=<token>; --connection <name> limits it to one change.
  3. It approves every covered change and names it. A change partly outside stays open whole, and the command fails with admin-required naming the connection and the fields outside, such as permissions or paths; stop and ask the user to approve it. vault-locked means the vault is locked; ask the user to run 'qatlas vault unlock'.

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

const configuration = `The configuration file has four sections and optional provider notes. 'qatlas tui' edits them and 'qatlas config validate' checks them.

Services say where: one provider and the root URL of one of its instances as base_url. A provider with a public API, such as GitHub, Telegram, or Todoist, uses that API when base_url is left out; a self-hosted system such as BookStack or Nextcloud always needs it. Two instances of one provider are two services.

Credentials say with what: where the secrets of a provider come from, never the secrets themselves. Type keyring keeps them in the system keyring of this machine; 'qatlas credential set' or s in the editor stores them. Type vault keeps them instead in the vault, a directory beside the configuration, unencrypted or encrypted to a passphrase, for a machine without a usable system keyring; 'qatlas vault status' shows its state, 'qatlas vault unlock' opens it, and 'qatlas vault encrypt', 'qatlas vault passphrase', and 'qatlas vault decrypt' set, change, and remove its passphrase. defaults.secret_store names which of the two a new credential starts from, keyring unless it is set to vault; neither is a recommendation, just the preselected choice. A set variable QATLAS_<CREDENTIAL>_<ROLE> overrides either one, for CI and containers. Type env names one environment variable per secret role. 'qatlas tui' offers system keyring, vault, and environment variables as its secrets choice, preselected the same way; storing the vault's very first secret there, or with 'qatlas credential set', offers a passphrase, typed masked and twice, and leaving it empty keeps the vault unencrypted. A plaintext credentials.yaml left over from an earlier version is still read for a keyring credential while it exists, and 'qatlas vault migrate', or migrate credentials.yaml in the editor's Vault section, carries it into the vault and removes it. 'qatlas credential set', 'qatlas credential delete', and every 'qatlas vault' command but status, unlock, and lock manage a credential or the vault: they run only from an interactive terminal, and where the vault is encrypted they ask for its passphrase there too, whatever credential they target; without a terminal, or with the wrong passphrase, they fail with admin-required or usage before touching anything. An agent never runs them; a person does, in a terminal or in 'qatlas tui'. The one exception is 'qatlas vault approve' with an agent token: 'qatlas vault token create' makes one, naming its vorbild connections with --vorbild, and an agent that presents it, in QATLAS_AGENT_TOKEN or a .qatlas/local/agent.env, approves without a terminal the connection changes that reach no further than a vorbild connection of the token; 'qatlas help agents' explains how. While the vault is encrypted, it hands a vault credential's secret only to a connection a person approved as it is configured: a connection whose service URL, provider, permissions, targets, tools list, paths, or credential changed since fails with approval-required until it is approved again; 'qatlas vault encrypt' approves every connection that reads from the vault at that moment, and 'qatlas vault approve' lists what changed about every connection still open and releases it, all at once or one at a time with --connection. An unencrypted vault binds no connection.

vault.idle_timeout sets how long a vault process that holds the vault unlocked does so without a read before it locks itself, a Go duration such as "12h" or "30m", 12h when it is left out. 'qatlas tui' opens read-only while the vault is encrypted; the first managing action of a run asks for its passphrase, masked, in a screen of its own, and a right answer starts an admin session bound to that window alone, no other window and no vault process sharing it. Its header always shows the vault's state, unlocked, locked, or unencrypted, plus, while it is encrypted, that window's own admin session; ctrl+l works anywhere in the editor and unlocks a locked vault, handing it to a vault process on Linux the same way 'qatlas vault unlock' does, or, once unlocked, asks "Lock the vault now?" and locks it again the same way 'qatlas vault lock' does. vault.admin_timeout sets how long that session stays open without a key press before it asks again, a Go duration such as "10m" or "0", 10m when it is left out; "0" asks for the passphrase on every managing action instead of keeping a session at all. 'qatlas tui' offers a Vault section (5 Vault) that shows the vault's state, turns its encryption on or off, changes its passphrase, migrates a leftover credentials.yaml into the vault, and edits both timeouts. Saving a change in that session approves exactly the connections it directly saved or newly opened; an Approvals section (6 Approvals) lists every connection still open otherwise, with what changed, to approve one at a time or all at once.

logs.retention_days sets how many days of the invocation log are kept, a positive integer, 90 when it is left out. Every invoke over the CLI and MCP appends one entry to a daily file under the vault directory, whether the vault exists yet, is locked, or is unencrypted, hash-chained across day boundaries so a changed or missing entry can be found later; it never records an argument, a result, a secret, a target, or a URL query. A day older than the retention window is removed on the next invoke, and the entry written right after that names what was removed, so 'qatlas vault logs verify' never mistakes a documented cut for a break. An entry written while a vault process holds the vault unlocked, or by a run that unlocked it itself, also carries a check value under a key derived from the vault's key; every other entry is unverified. That command walks the chain, has a running vault process check the check values, and reports one row per day: its entry count, how many entries carry no check value, how many carry one nobody could check because the vault is locked, how many fail their check value, how many are missing, and the first problem found; it exits successfully while the chain is intact, whatever entries are unverified or unchecked.

Connections are the routes an agent takes: one service and one credential, an optional scope inside the service (target or targets), the permitted effects (read, create, update, delete, execute), an optional tools list of tool IDs, and an optional one-line description. A tool runs through a connection only when its effect is permitted and, where a tools list exists, the list names it; a tool marked requires_tool_allow_list runs only through a connection whose tools list names it. 'qatlas tools <provider> --all' shows the tools a connection does not offer and why. Where a provider accepts several targets, targets is an allow-list: for GitHub it is optional, may mix repositories and projects, allows patterns such as repos/OWNER/*, and a tool names the repository or project it acts on, which must lie inside the list.

Paths optionally bind a connection to projects: a list of directories, absolute or starting with ~/, each covering itself and everything below it, compared by whole directory names, so ~/repos/kunde-a does not cover ~/repos/kunde-ab; glob patterns are refused. The project of a call is its working directory, or the root of the Git repository it lies in, with symbolic links resolved; a linked worktree checked out elsewhere also counts as its repository's main working tree, and 'qatlas mcp' uses the file:// roots its client names after initialize, or else the directory it was started in. Outside its paths a connection is missing from connections, providers, tools, describe, search, and every candidate list, a default that names it does not apply, and naming it fails with unknown-connection. Without paths it applies everywhere. 'qatlas config validate' and 'qatlas tui' still show every connection, and validate warns about an entry that names no existing directory. The binding keeps one customer's connections out of another customer's project by mistake, not by force: an agent that changes into a bound directory sees the connection there. While the vault is encrypted, a change of paths needs a new approval.

Provider notes say what a provider stands for here, one optional line per provider ID under provider_notes, for example bookstack: wiki or seatable: CRM. The provider's own description already says what kind of system it is; the note adds the word a task uses for it, so an agent can map "the CRM" to a provider. With a single connection the note may repeat its description or stay empty. 'qatlas tui' edits the note in the form of a service of that provider.

Defaults say which connection a tool uses without --connection, keyed by a provider or a tool ID; the default of the tool ID wins over the default of its provider. They apply to reading and changing tools alike, and a changing tool still needs --confirm. Without a default, a tool runs through the single connection of the project that offers it and asks for --connection when several do. A default that names a connection the tool does not offer is refused, never skipped.

The sections stay apart so that a secret is stored once and used by several routes, each route carries only the rights its purpose needs, for example a read-only connection beside one that may create pages, and discovery shows an agent connection names, descriptions, and permitted effects only, never a URL, a credential, a target, or a secret. Give each connection a one-line description so an agent can choose between them. Discovery publishes descriptions and provider notes and searches them, so they must never hold a secret or personal data.`

// mcpGuide is what 'qatlas mcp' hands its client as server instructions, and 'qatlas mcp --help' shows: a
// guide of its own, short enough for a client that truncates instructions, covering only the three MCP
// tools, their arguments, and their error codes. 'qatlas agents' stays the one guide for the CLI and covers
// the same ground, and more, at CLI length.
const mcpGuide = `Three fixed MCP tools; 'qatlas agents' has the details.

qatlas.search finds tools like 'qatlas tools', best match first: query, provider, connection, and effect filter; all adds the tools no connection offers, each with its reason. A hit has id, title, effect, requires (required arguments as name:form), confirm, and connections only where fewer than the top-level connections offer it: enough for a call; describe adds optional arguments. limit pages (default 50); pass next_cursor as cursor. list=providers or connections gives an overview; a connection may be unusable: vault-locked, or approval-required until the vault approves it.

qatlas.describe and qatlas.invoke take a tool ID as operation. connection is needed only when several connections offer the tool: without it qatlas takes the tool's default, then the provider's, then the only connection offering it. describe returns the compact contract, or with full the complete schemas. invoke runs the tool with arguments as its input; a tool that changes data needs confirm set to true.

A successful call returns its result once, as one JSON text block in content, without structuredContent. Empty fields and a field a set filter fixes (state with state open) are left out; missing means empty. fields (selectable_fields of describe) keeps only those members of each result-list entry; an unknown name or no list is invalid-request. A failed call has isError true and structuredContent with at least code and message; content holds the same text as "<code>: <message>". Key codes: confirmation-required (retry with confirm), vault-locked (ask the person to run 'qatlas vault unlock'), admin-required (only a person may), approval-required (a person must approve a changed connection; ask the user), connection-ambiguous (structuredContent lists the candidates: pass connection), connection-selection, unknown-operation, unknown-connection, unsupported-capability, auth, permission, not-found, timeout, rate-limited.`
