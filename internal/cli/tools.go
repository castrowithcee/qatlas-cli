package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/output"
)

// toonContract names the format version of the discovery output. TOON 4.1 is a working draft, so the
// public contract names the exact target version instead of migrating silently with the specification.
const toonContract = "TOON 4.1 (https://github.com/toon-format/spec/blob/v4.1.1/SPEC.md)"

// newProvidersCommand lists the namespaces of the tool catalog. Discovery cascades: this command answers
// which namespaces exist, "connections" which routes a person configured, "tools <namespace>" which tools
// those routes offer, and "describe <id>" one complete contract. Each step stays small enough to read,
// however many providers are compiled in.
func newProvidersCommand(opts *Options, registry *capability.Registry) *cobra.Command {
	return &cobra.Command{
		Use:   "providers",
		Short: "List the tool namespaces this installation offers",
		Long: "Providers lists every namespace of the tool catalog with the number of tools it offers and the\n" +
			"number of configured connections that can run them. A provider without a connection stays\n" +
			"listed with zero. It is answered from the local configuration alone: no provider is contacted\n" +
			"and no secret is read.\n\n" +
			"The connections themselves, with what each one may do, are one 'qatlas connections' away, and\n" +
			"the tools of one namespace one 'qatlas tools <provider>' away.\n\n" +
			"The output is " + toonContract + " with LF line endings. --output json returns the same data as\n" +
			"JSON.",
		Args: noArgs,
		RunE: func(c *cobra.Command, args []string) error {
			format, err := discoveryFormat(c, opts)
			if err != nil {
				return err
			}
			core, err := applicationCore(opts, registry, false)
			if err != nil {
				return err
			}
			return emitDocument(c, format, map[string]any{"providers": core.Providers().Providers})
		},
	}
}

// newConnectionsCommand lists the configured routes. An agent learns here which connections exist, what
// each one is for, and what it may do, without ever seeing where a route leads or what it authenticates
// with.
func newConnectionsCommand(opts *Options, registry *capability.Registry) *cobra.Command {
	return &cobra.Command{
		Use:   "connections [provider]",
		Short: "List the configured connections and what each one may do",
		Long: "Connections lists every configured connection, or those of one provider, sorted by provider\n" +
			"and name. Each one is published with its name, which --connection takes, its provider, the\n" +
			"one-line description its owner maintains, which is empty where there is none, the effects its\n" +
			"permissions allow, separated by spaces, and how its tools list shapes what it offers:\n\n" +
			"  all-permitted  no tools list: every tool whose effect is permitted, except a tool whose\n" +
			"                 contract says requires_tool_allow_list\n" +
			"  listed         only the tools its tools list names\n" +
			"  none           an empty tools list: no tool at all\n\n" +
			"It is answered from the local configuration alone: no provider is contacted and no secret is\n" +
			"read. No URL, credential, target, or secret source is published. The tools one connection\n" +
			"offers are one 'qatlas tools <provider> --connection <name>' away.\n\n" +
			"The output is " + toonContract + " with LF line endings. --output json returns the same data as\n" +
			"JSON.",
		Args: atMostOneArg("provider"),
		RunE: func(c *cobra.Command, args []string) error {
			format, err := discoveryFormat(c, opts)
			if err != nil {
				return err
			}
			var provider string
			if len(args) == 1 {
				if _, ok := registry.ProviderMetadata(args[0]); !ok {
					return &UsageError{fmt.Errorf(
						"unknown provider %q; run 'qatlas providers' to list the providers", args[0])}
				}
				provider = args[0]
			}
			core, err := applicationCore(opts, registry, false)
			if err != nil {
				return err
			}
			return emitDocument(c, format, map[string]any{"connections": core.Connections(provider).Connections})
		},
	}
}

// newToolsCommand lists the tools of one namespace. A tool is one provider-qualified operation; the
// provider prefix is its namespace, not a command tree of its own, so the number of commands never grows
// with the catalog.
func newToolsCommand(opts *Options, registry *capability.Registry) *cobra.Command {
	var (
		query string
		all   bool
	)
	cmd := &cobra.Command{
		Use:   "tools <namespace>",
		Short: "List the tools the configured connections offer",
		Long: "Tools lists the tools of one namespace that a configured connection offers, each with its ID,\n" +
			"its title, whether it reads or changes the remote system, and the names of the connections that\n" +
			"offer it, separated by spaces. It is answered from the local configuration alone: no provider is\n" +
			"contacted and no secret is read.\n\n" +
			"The namespace argument is the provider prefix of the tool IDs; 'qatlas providers' lists the\n" +
			"namespaces. --query answers the same form for a targeted search and may be used without a\n" +
			"namespace, keeping only the tools whose ID, title, description, or tags contain every term.\n\n" +
			"A connection offers a tool when its permissions allow the tool's effect and, if the connection\n" +
			"has a tools list, that list names the tool; without a tools list every tool of an allowed effect\n" +
			"is offered, except a high-risk tool whose contract says requires_tool_allow_list, which only a\n" +
			"list that names it offers, and 'tools: []' offers none. Each connection is judged on its own\n" +
			"lists, even when it shares the provider, the service, or the credential with another one.\n" +
			"Describe, invoke and the MCP broker apply the same rule, and invoke refuses before any secret is\n" +
			"read or any provider is contacted.\n\n" +
			"--connection keeps only the tools that connection offers. --all lists the tools no connection\n" +
			"offers as well, and adds a reason to every row: empty for an offered tool, otherwise one of\n\n" +
			"  effect-not-permitted      the permissions do not allow the tool's effect\n" +
			"  requires-tool-allow-list  the tool needs a tools list that names it, and there is none\n" +
			"  not-in-tools-list         the tools list does not name the tool\n" +
			"  no-connection             the provider has no configured connection\n\n" +
			"With --connection the reason is that connection's. Otherwise, where several connections refuse a\n" +
			"tool for different reasons, the reason of the one closest to offering it is shown, in the order\n" +
			"not-in-tools-list, requires-tool-allow-list, effect-not-permitted. 'qatlas tui' changes what a\n" +
			"connection offers.\n\n" +
			"Everything else about a tool, including its schemas and the descriptions of the connections that\n" +
			"can run it, is one 'qatlas describe <tool-id>' away.\n\n" +
			"The output is " + toonContract + " with LF line endings. --output json returns the same data as\n" +
			"JSON. When no connection offers a listed tool, the list is empty and a note on stderr points to\n" +
			"--all.",
		Args: atMostOneArg("tool namespace"),
		RunE: func(c *cobra.Command, args []string) error {
			format, err := discoveryFormat(c, opts)
			if err != nil {
				return err
			}
			// Without a namespace and without a query this would print the whole catalog, which is the
			// answer the cascade exists to avoid. Naming the first step is more useful than that list.
			if len(args) == 0 && query == "" {
				return newSyntaxError(errors.New(
					"tools needs a namespace or --query; run 'qatlas providers' to list the namespaces"))
			}
			request := application.SearchRequest{Query: query, Connection: opts.Connection, All: all}
			if len(args) == 1 {
				if _, ok := registry.ProviderMetadata(args[0]); !ok {
					return &UsageError{fmt.Errorf(
						"unknown tool namespace %q; run 'qatlas providers' to list the namespaces", args[0])}
				}
				request.Provider = args[0]
			}
			core, err := applicationCore(opts, registry, false)
			if err != nil {
				return err
			}
			response, err := core.Tools(request)
			if err != nil {
				return classifyUserError(err)
			}
			if err := emitDocument(c, format, map[string]any{"tools": response.Tools}); err != nil {
				return err
			}
			// An empty answer that only the configuration causes says so on stderr, so stdout stays the
			// payload and the caller still learns where the tools went.
			if len(response.Tools) == 0 && !all {
				request.All = true
				if every, err := core.Tools(request); err == nil && len(every.Tools) > 0 {
					fmt.Fprintf(c.ErrOrStderr(), "qatlas: no connection offers any of the %d matching tools; "+
						"--all lists them with the reason, 'qatlas connections' lists the connections\n",
						len(every.Tools))
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&query, "query", "", "keep only the tools matching every term of this text")
	cmd.Flags().BoolVar(&all, "all", false, "also list the tools no connection offers, with the reason")
	return cmd
}

// newDescribeCommand describes exactly one tool. The tool ID is the only argument: there is no second verb
// below it, because the tool itself is the leaf of the public taxonomy. The former name "tool" stays a
// hidden alias, so existing scripts keep working while help and documentation name the verb only.
func newDescribeCommand(opts *Options, registry *capability.Registry, use string, hidden bool) *cobra.Command {
	return &cobra.Command{
		Use:    use + " <tool-id>",
		Hidden: hidden,
		Short:  "Describe one tool contract",
		Long: "Describe prints the complete contract of one tool: version, description, tags, input and output\n" +
			"schema, risk metadata, secret-free examples, and the connections that can run it. Every\n" +
			"connection is named with its stable invoke value and the optional one-line description its\n" +
			"owner maintains. It is answered from the local configuration alone: no provider is contacted\n" +
			"and no secret is read.\n\n" +
			"With --connection it refuses a connection that does not offer the tool with\n" +
			"unsupported-capability and the reason, as 'qatlas tools --all' names it.\n\n" +
			"The output is " + toonContract + " with LF line endings. --output json returns the same data as\n" +
			"JSON.",
		Args: exactlyOneArg("tool ID"),
		RunE: func(c *cobra.Command, args []string) error {
			format, err := discoveryFormat(c, opts)
			if err != nil {
				return err
			}
			core, err := applicationCore(opts, registry, false)
			if err != nil {
				return err
			}
			response, err := core.Describe(application.DescribeRequest{
				Operation: args[0], Connection: opts.Connection,
			})
			if err != nil {
				return classifyUserError(err)
			}
			return emitDocument(c, format, map[string]any{
				"tool": response.Operation, "connections": response.Connections,
			})
		},
	}
}

// newInvokeCommand runs one tool. The tool ID is positional and only the schema-dependent arguments come
// from --arg or stdin, so an agent can never smuggle a route, a header, or a credential past the contract.
func newInvokeCommand(opts *Options, registry *capability.Registry) *cobra.Command {
	var confirm bool
	var flagArgs []string
	cmd := &cobra.Command{
		Use:   "invoke <tool-id>",
		Short: "Invoke one tool",
		Long: "Invoke runs the named tool through the application core, which validates the arguments against\n" +
			"the input schema before it selects a connection or contacts a provider.\n\n" +
			"The arguments come either from --arg name=value, repeated once per argument, or from stdin as\n" +
			"exactly one JSON object; giving both is an error, and giving neither invokes the tool without\n" +
			"arguments. --arg reads its type from the input schema, so a numeric argument needs no quoting\n" +
			"and no JSON; an argument that is itself a list or an object belongs on stdin.\n\n" +
			"--connection selects the route when the configuration leaves more than one possibility, and\n" +
			"--confirm carries the confirmation a mutating tool requires for this request.\n\n" +
			"The result is written to stdout as JSON. Diagnostics and the audit event of a confirmed\n" +
			"mutation go to stderr. A connection-ambiguous diagnostic is followed by one JSON line with\n" +
			"code, message, operation, and connections: every candidate route with its name and its\n" +
			"description, which is empty where none is maintained. Nothing is chosen for you; pass one of\n" +
			"the names with --connection. A connection that does not offer the tool is refused with\n" +
			"unsupported-capability, followed by one JSON line with code, message, operation, connection,\n" +
			"and reason, the same reason 'qatlas tools --all' names.",
		Args: exactlyOneArg("tool ID"),
		RunE: func(c *cobra.Command, args []string) error {
			arguments, err := invokeArguments(c, registry, args[0], flagArgs)
			if err != nil {
				return &UsageError{err}
			}
			core, err := applicationCore(opts, registry, true)
			if err != nil {
				return err
			}
			var audit bytes.Buffer
			core.SetAudit(&audit)
			response, err := core.Invoke(c.Context(), application.InvokeRequest{
				Operation: args[0], Connection: opts.Connection, Arguments: arguments, Confirmed: confirm,
			})
			if err != nil {
				return withAudit(classifyUserError(err), audit.Bytes())
			}
			if err := writeEnvelope(c.OutOrStdout(), response); err != nil {
				return withAudit(err, audit.Bytes())
			}
			writeAudit(c.ErrOrStderr(), audit.Bytes(), opts.Redactor)
			return nil
		},
	}
	cmd.Flags().BoolVar(&confirm, "confirm", false,
		"confirm this exact request; required by a tool whose contract demands confirmation")
	cmd.Flags().StringArrayVar(&flagArgs, "arg", nil,
		"one tool argument as name=value, repeated per argument; typed by the input schema")
	return cmd
}

// invokeArguments takes the arguments from whichever way the caller used. Both ways describe the same
// object, so using them together would leave open which one applies; the CLI says so instead of merging.
func invokeArguments(c *cobra.Command, registry *capability.Registry, id string,
	flagArgs []string) (json.RawMessage, error) {
	stdin, err := readInvokeArguments(c.InOrStdin())
	if err != nil {
		return nil, err
	}
	if len(flagArgs) == 0 {
		return stdin, nil
	}
	if !bytes.Equal(stdin, []byte(`{}`)) {
		return nil, &application.InvalidRequestError{
			Message: "arguments were given both as --arg and on stdin; use one of the two",
		}
	}
	// An unknown tool has no schema to type against. The values stay strings and the core reports the
	// unknown tool, which is the error the caller actually made.
	descriptor, _, _ := registry.Lookup(id)
	return argumentsFromFlags(descriptor.InputSchema, flagArgs)
}

// discoveryFormat resolves the output format of the discovery commands. TOON is the default because these
// commands exist for agents; an explicit --output json is the interoperable alternative. The three scalar
// formats cannot render a nested tool contract, so asking for one is a usage error rather than a partial
// answer.
func discoveryFormat(c *cobra.Command, opts *Options) (output.Format, error) {
	if !c.Flags().Changed("output") {
		return output.FormatTOON, nil
	}
	switch opts.Format {
	case output.FormatTOON, output.FormatJSON:
		return opts.Format, nil
	}
	return "", &UsageError{fmt.Errorf("--output %s cannot render a tool contract, want %s or %s",
		opts.Format, output.FormatTOON, output.FormatJSON)}
}

// emitDocument writes one discovery document. Both formats render the same normalized JSON value, so the
// TOON default and --output json cannot drift apart: TOON is a rendering of the JSON data model here, not
// a second contract.
func emitDocument(c *cobra.Command, format output.Format, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode discovery document: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return fmt.Errorf("encode discovery document: %w", err)
	}
	if format == output.FormatJSON {
		return json.NewEncoder(c.OutOrStdout()).Encode(document)
	}
	toon, err := output.MarshalTOON(document)
	if err != nil {
		return fmt.Errorf("encode discovery document: %w", err)
	}
	_, err = c.OutOrStdout().Write(append(toon, '\n'))
	return err
}

func exactlyOneArg(what string) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) != 1 {
			return newSyntaxError(fmt.Errorf("expected exactly one %s, got %d", what, len(args)))
		}
		return nil
	}
}

func atMostOneArg(what string) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) > 1 {
			return newSyntaxError(fmt.Errorf("expected at most one %s, got %d", what, len(args)))
		}
		return nil
	}
}
