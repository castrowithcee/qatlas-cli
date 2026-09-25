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

// Agents returns the guide for agents. 'qatlas agents' prints it, and 'qatlas mcp' hands the same text to
// every client as its server instructions, so both ways read one guide.
func Agents() Topic {
	return Topic{Name: "agents", Short: "Let an agent discover and invoke tools through qatlas", Text: agents}
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
2. Press c for the guided setup: choose a provider, its service (the root URL of the instance), a credential (the system keyring is recommended for its secrets), the scope, and the permissions. The summary saves the connection.
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
                                    permitted effects, and whether a tools
                                    list narrows them
  qatlas tools <provider>           the tools those connections offer, each
                                    with the connections that offer it
  qatlas describe <tool-id>         one compact contract: the arguments and
                                    result fields as tables, risk, examples,
                                    and the connections that can run it;
                                    --full adds both schemas
  qatlas invoke <tool-id> --connection <name>
                                    run it

Narrow or widen the catalog:

  qatlas tools --query "<terms>"    search every provider; the terms also
                                    match provider descriptions and notes
                                    and connection descriptions
  qatlas tools <provider> --connection <name>
                                    only the tools that connection offers
  qatlas tools <provider> --all     also the tools no connection offers,
                                    each with the reason

Invoke:

  qatlas invoke <tool-id> --connection <name> --arg name=value
  qatlas invoke <tool-id> --connection <name> --arg 'labels=["bug"]'
  echo '{"name":"value"}' | qatlas invoke <tool-id> --connection <name>

--arg is repeated once per argument and typed by the input schema: a string as written, a number, true or false, and a list or an object as JSON. The whole arguments object as one JSON object on stdin is the equal alternative; with --arg, stdin is not read. Every invoke ends within 60 seconds; what reaches that limit ends with timeout and the next step. A tool whose contract requires confirmation runs only with --confirm, and the audit event of that change goes to stderr. The result is one JSON object with a data field on stdout. --agent keeps other output machine-readable, without prose or colour.

Always pass --connection. Many tools require it (requires_explicit_connection in the contract); without it a tool runs only through a default or through the single connection that offers it.

Errors go to stderr as "qatlas: <code>: <message>"; branch on the code, not on the message. Exit code 0 is success.

Exit code 2 is a problem of the request or the configuration:

- usage: the command, a flag, or a field name is wrong.
- invalid-request: the arguments do not satisfy the input schema, or the request is malformed.
- config-missing: there is no configuration file; a person creates one with 'qatlas tui'.
- config-invalid: the configuration, or a file beside it, is not usable; only a person fixes it.
- connection-selection: no connection could be chosen for the tool; when the tool requires an explicit connection, one JSON line follows that lists the connections offering it, choose one and pass it with --connection.
- unknown-connection: the named connection is not configured; 'qatlas connections <provider>' lists them.
- connection-ambiguous: several connections offer the tool; one JSON line follows that lists the candidates, choose one and pass it with --connection.
- unknown-operation: no tool has this ID or version; find it with 'qatlas tools <provider>'.
- unsupported-capability: the connection does not offer the tool; one JSON line follows with the connection and the reason (effect-not-permitted, requires-tool-allow-list, not-in-tools-list, or other-provider). 'qatlas describe <tool-id>' names the connections that do, and only a person changes a connection, in 'qatlas tui'.
- missing-secret: the credential of the connection yields no secret; only a person stores it.
- confirmation-required: the tool changes data and runs only with --confirm.
- policy-denied: local policy refuses the call.

Exit code 1 is a runtime or provider failure:

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

MCP: 'qatlas mcp' serves the same catalog over stdio as three fixed MCP tools, with the same connections, rules, and error codes. qatlas.search finds tools like 'qatlas tools' and names the connections that offer each one; all set to true adds the others with their reason. qatlas.describe returns one compact contract like 'qatlas describe', or with full set to true the complete one like 'qatlas describe --full', and qatlas.invoke runs one like 'qatlas invoke', with confirm for --confirm. qatlas.describe and qatlas.invoke take the tool ID as operation and a connection name as connection; qatlas.invoke takes the arguments object as arguments. A failed call is a tool result with isError and structuredContent carrying the code. The server hands this guide to its client as instructions. Add the command "qatlas mcp" as a stdio server to the agent's client; it speaks MCP 2026-07-28 with the protocol version and client capabilities in the _meta of every request, and MCP 2025-11-25 and 2025-06-18 after an initialize request.

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

Credentials say with what: where the secrets of a provider come from, never the secrets themselves. Type keyring, the recommended one, keeps them in the system keyring of this machine; 'qatlas credential set' or s in the editor stores them. A set variable QATLAS_<CREDENTIAL>_<ROLE> overrides the keyring, for CI and containers. Type env names one environment variable per secret role. An unencrypted file beside the configuration is the last resort and is written only after an explicit confirmation. 'qatlas tui' offers the three places as its secrets choice: system keyring, environment variables, and unencrypted file; the file stores the first and the last as type keyring.

Connections are the routes an agent takes: one service and one credential, an optional scope inside the service (target or targets), the permitted effects (read, create, update, delete, execute), an optional tools list of tool IDs, and an optional one-line description. A tool runs through a connection only when its effect is permitted and, where a tools list exists, the list names it; a tool marked requires_tool_allow_list runs only through a connection whose tools list names it. 'qatlas tools <provider> --all' shows the tools a connection does not offer and why. Where a provider accepts several targets, targets is an allow-list: for GitHub it is optional, may mix repositories and projects, allows patterns such as repos/OWNER/*, and a tool names the repository or project it acts on, which must lie inside the list.

Provider notes say what a provider stands for here, one optional line per provider ID under provider_notes, for example bookstack: wiki or seatable: CRM. The provider's own description already says what kind of system it is; the note adds the word a task uses for it, so an agent can map "the CRM" to a provider. With a single connection the note may repeat its description or stay empty. 'qatlas tui' edits the note in the form of a service of that provider.

Defaults say which connection a tool uses without --connection, keyed by a provider or a tool ID. They apply only to tools that do not require an explicit connection, and many providers require one for every tool. Without a default, a tool runs through the single connection that offers it and asks for --connection when several do.

The sections stay apart so that a secret is stored once and used by several routes, each route carries only the rights its purpose needs, for example a read-only connection beside one that may create pages, and discovery shows an agent connection names, descriptions, and permitted effects only, never a URL, a credential, a target, or a secret. Give each connection a one-line description so an agent can choose between them. Discovery publishes descriptions and provider notes and searches them, so they must never hold a secret or personal data.`
