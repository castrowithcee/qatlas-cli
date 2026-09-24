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
		{Name: "agents", Short: "Let an agent discover and invoke tools through qatlas", Text: agents},
		{Name: "configuration", Short: "Services, credentials, connections, and defaults explained",
			Text: configuration},
	}
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

const agents = `An agent uses qatlas like a person on the command line, through connections a person configured beforehand. It never handles a secret: of a route it knows only the connection name, its one-line description, and the effects it may use.

Discover in small steps, then invoke. Discovery writes TOON; --output json returns JSON.

  qatlas connections [provider]     the configured connections: provider,
                                    description, permitted effects, and
                                    whether a tools list narrows them
  qatlas tools <namespace>          the tools those connections offer, each
                                    with the connections that offer it
  qatlas describe <tool-id>         one contract: schemas, risk, examples,
                                    and the connections that can run it
  qatlas invoke <tool-id> --connection <name>
                                    run it

Narrow or widen the catalog:

  qatlas tools --query "<terms>"    search every namespace
  qatlas tools <namespace> --connection <name>
                                    only the tools that connection offers
  qatlas tools <namespace> --all    also the tools no connection offers,
                                    each with the reason

'qatlas providers' counts the tools and connections of every namespace.

Invoke:

  qatlas invoke <tool-id> --connection <name> --arg name=value
  echo '{"name":"value"}' | qatlas invoke <tool-id> --connection <name>

--arg is repeated once per argument and typed by the input schema; a list or an object goes to stdin as one JSON object. Use one of the two ways, not both. A tool whose contract requires confirmation runs only with --confirm, and the audit event of that change goes to stderr. The result is one JSON object with a data field on stdout. --agent keeps other output machine-readable, without prose or colour.

Always pass --connection. Many tools require it (requires_explicit_connection in the contract); without it a tool runs only through a default or through the single connection that offers it.

Errors go to stderr as "qatlas: <code>: <message>". Exit code 0 is success, 2 a problem of the request or the configuration (for example invalid-request, unknown-connection, unsupported-capability, connection-ambiguous, confirmation-required, policy-denied, missing-secret), and 1 a runtime or provider failure (for example unreachable, auth, permission, not-found, timeout, rate-limited). not-found means the provider does not hold the resource or does not show it to this credential; the message names the target it addressed, so check that target and what the credential may see. connection-ambiguous is followed by one JSON line that lists the candidate connections; choose one and pass it with --connection. unsupported-capability is followed by one JSON line with the connection and the reason it does not offer the tool (effect-not-permitted, requires-tool-allow-list, not-in-tools-list, or other-provider); 'qatlas describe <tool-id>' names the connections that do, and only a person changes a connection, in 'qatlas tui'.

MCP: 'qatlas mcp' serves the fixed tools qatlas.search, qatlas.describe, and qatlas.invoke over stdio, with the same connections and the same rules. qatlas.search returns the offered tools; all set to true adds the others with their reason. Add the command "qatlas mcp" as a stdio server to the agent's client.

Tell the agent which connections it may use and what each one is for, and whether it may change data. Never hand it a token, password, or key: qatlas reads the secrets itself.

A block for the AGENTS.md or CLAUDE.md of a project:

  ## Qatlas
  Use the qatlas CLI to reach <what the connections are for>.
  - Start with 'qatlas agents'.
  - Connections: <name> for <purpose>; <name> for <purpose>.
  - Changes: <allowed with --confirm | not allowed, read only>.
  - Before invoking, run 'qatlas tools <namespace> --connection <name>' and 'qatlas describe <tool-id>'.
  - Invoke with 'qatlas invoke <tool-id> --connection <name>' and pass the arguments as --arg name=value or as one JSON object on stdin.
  - Never ask for, pass, or print a secret. On an auth or permission error, stop and report the code.`

const configuration = `The configuration file has four sections. 'qatlas tui' edits them and 'qatlas config validate' checks them.

Services say where: one provider and the root URL of one of its instances. Two instances of one provider are two services.

Credentials say with what: where the secrets of a provider come from, never the secrets themselves. Type keyring, the recommended one, keeps them in the system keyring of this machine; 'qatlas credential set' or s in the editor stores them. A set variable QATLAS_<CREDENTIAL>_<ROLE> overrides the keyring, for CI and containers. Type env names one environment variable per secret role. An unencrypted file beside the configuration is the last resort and is written only after an explicit confirmation. 'qatlas tui' offers the three places as its secrets choice: system keyring, environment variables, and unencrypted file; the file stores the first and the last as type keyring.

Connections are the routes an agent takes: one service and one credential, an optional scope inside the service (target or targets), the permitted effects (read, create, update, delete, execute), an optional tools list of tool IDs, and an optional one-line description. A tool runs through a connection only when its effect is permitted and, where a tools list exists, the list names it; a tool marked requires_tool_allow_list runs only through a connection whose tools list names it. 'qatlas tools <provider> --all' shows the tools a connection does not offer and why. Where a provider accepts several targets, targets is an allow-list: for GitHub it is optional, may mix repositories and projects, allows patterns such as repos/OWNER/*, and a tool names the repository or project it acts on, which must lie inside the list.

Defaults say which connection a tool uses without --connection, keyed by a provider or a tool ID. They apply only to tools that do not require an explicit connection, and many providers require one for every tool. Without a default, a tool runs through the single connection that offers it and asks for --connection when several do.

The sections stay apart so that a secret is stored once and used by several routes, each route carries only the rights its purpose needs, for example a read-only connection beside one that may create pages, and discovery shows an agent connection names, descriptions, and permitted effects only, never a URL, a credential, a target, or a secret. Give each connection a one-line description so an agent can choose between them; discovery publishes it, so it must never hold a secret or personal data.`
