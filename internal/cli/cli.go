// Package cli defines the command surface of the qatlas binary. Commands stay thin adapters: they parse
// global options, hand typed results to the encoders, and translate errors into exit codes.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/helptopics"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/selfupdate"
)

// version is overridden at build time with
// -ldflags "-X github.com/castrowithcee/qatlas-cli/internal/cli.version=<v>".
var version = "dev"

// Exit codes are part of the public contract.
const (
	exitOK      = 0
	exitRuntime = 1
	exitUsage   = 2
)

// Options carries the global flags to the application core.
type Options struct {
	Config     string
	Connection string
	Agent      bool
	Output     string
	Input      io.Reader
	Updater    *selfupdate.Client

	// Format is resolved from Output and Agent before a command runs.
	Format output.Format
	// Redactor removes secret values from anything the process prints.
	Redactor *redact.Redactor
	// Secrets resolves credentials through the cascade. It is built on first use, from the directory the
	// configuration was resolved from; a test sets it beforehand so no run touches the credential store
	// of the machine it runs on.
	Secrets *secret.Resolver
}

// resolver returns the credential resolver of this run, building it once.
func (o *Options) resolver() (*secret.Resolver, error) {
	if o.Secrets != nil {
		return o.Secrets, nil
	}
	path, err := config.Path(o.Config)
	if err != nil {
		return nil, err
	}
	// The plaintext fallback lives beside the configuration, so it follows the same resolution: an
	// explicit --config, QATLAS_CONFIG, QATLAS_CLI_HOME, or the default directory.
	resolver, err := secret.New(filepath.Dir(path), o.Redactor)
	if err != nil {
		// An unusable store selector is a mistake in the invocation, not a runtime failure.
		return nil, &UsageError{err}
	}
	o.Secrets = resolver
	return o.Secrets, nil
}

// UsageError marks a usage or validation problem, which maps to exit code 2. Every other error is a
// runtime error and maps to exit code 1.
type UsageError struct{ Err error }

func (e *UsageError) Error() string { return e.Err.Error() }

func (e *UsageError) Unwrap() error { return e.Err }

// syntaxError is a usage error in the shape of the command line itself: an unknown command or flag, a flag
// that does not parse, or the wrong number of positional arguments. Only such an error is followed by the
// usage block; a request that is well formed but refused names its problem and its next step instead.
type syntaxError struct{ usage *UsageError }

func (e *syntaxError) Error() string { return e.usage.Error() }

func (e *syntaxError) Unwrap() error { return e.usage }

// newSyntaxError marks err as a syntax error, which keeps exit code 2 and adds the usage block.
func newSyntaxError(err error) error { return &syntaxError{&UsageError{err}} }

// Run executes the root command against the given streams and returns the process exit code. It never
// terminates the process, so callers and tests share the same path.
func Run(args []string, stdout, stderr io.Writer) int {
	opts := &Options{Redactor: &redact.Redactor{}, Input: os.Stdin}
	// The standard library and other packages write their diagnostics through the standard logger, for
	// example the HTTP/2 transport under GODEBUG=http2debug, which prints every request header. Routing it
	// through the redactor of this run keeps a credential out of stderr even there.
	log.SetOutput(opts.Redactor.Writer(stderr))
	return run(newRootCommand(opts, defaultRegistry()), opts, args, stdout, stderr)
}

func run(cmd *cobra.Command, opts *Options, args []string, stdout, stderr io.Writer) int {
	cmd.SetArgs(args)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	if opts.Input != nil {
		cmd.SetIn(opts.Input)
	}

	executed, err := cmd.ExecuteC()
	if err == nil {
		return exitOK
	}

	// Redaction happens before anything is shown, including unexpected provider errors.
	fmt.Fprintf(stderr, "qatlas: %s: %s\n", codeFor(err), opts.Redactor.Error(err))
	// A detail follows the diagnostic as one JSON line, so a caller reads its fields instead of the text.
	if detail := errorDetailFor(err, opts.Redactor); detail != nil {
		_ = json.NewEncoder(stderr).Encode(detail)
	}
	code := exitCode(err)
	var syntax *syntaxError
	if errors.As(err, &syntax) {
		fmt.Fprint(stderr, executed.UsageString())
	}
	writeAudit(stderr, auditFrom(err), opts.Redactor)
	return code
}

// exitCode maps an error from the command layer to the documented process exit code.
func exitCode(err error) int {
	if err == nil {
		return exitOK
	}
	var usage *UsageError
	if errors.As(err, &usage) {
		return exitUsage
	}
	return exitRuntime
}

func newRootCommand(opts *Options, reg *capability.Registry) *cobra.Command {
	if opts.Redactor == nil {
		opts.Redactor = &redact.Redactor{}
	}

	cmd := &cobra.Command{
		Use:   "qatlas",
		Short: "Command-line client for self-hosted knowledge and service backends",
		Long: "Qatlas CLI is a single command-line entry point to the knowledge and service backends you\n" +
			"already run. People configure named connections; agents discover and invoke tools through them.\n\n" +
			"Start here:\n" +
			"  qatlas agents   an agent: how to discover, describe, and invoke tools\n" +
			"  qatlas tui      a person: set up connections and what each one may do\n\n" +
			"An agent discovers in small steps: 'qatlas providers' lists every provider with its tools and\n" +
			"connections, 'qatlas connections <provider>' the configured routes and what each one may do,\n" +
			"'qatlas tools <provider>' the tools they offer, 'qatlas describe <tool-id>' one complete\n" +
			"contract, and 'qatlas invoke <tool-id>' runs it. Discovery writes " + toonContract + ";\n" +
			"--output json is the interoperable alternative. 'qatlas mcp' serves the same steps as fixed\n" +
			"MCP tools over stdio and hands the guide of 'qatlas agents' to its client.",
		Version:       version,
		SilenceErrors: true,
		SilenceUsage:  true,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				return newSyntaxError(fmt.Errorf("unknown command %q", args[0]))
			}
			return nil
		},
		PersistentPreRunE: func(c *cobra.Command, _ []string) error { return resolveFormat(c, opts) },
		// Without a subcommand there is nothing to do yet, so the root command prints its own help.
		RunE: func(c *cobra.Command, _ []string) error { return c.Help() },
	}

	// Help and version follow Cobra conventions and go to stdout. Version output stays deterministic.
	cmd.SetVersionTemplate("{{.Name}} {{.Version}}\n")
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return newSyntaxError(err) })

	f := cmd.PersistentFlags()
	f.StringVar(&opts.Config, "config", "", "path to the configuration file")
	f.StringVar(&opts.Connection, "connection", "", "name of the connection to use")
	f.BoolVar(&opts.Agent, "agent", false, "agent mode: machine-readable output without prose or color")
	// The default depends on the command, so the flag carries none of its own; resolveFormat and the
	// commands with a narrower choice apply it.
	f.StringVar(&opts.Output, "output", "",
		"output format: table (default), json, compact, or toon; providers, connections, tools, and describe "+
			"write toon (default) or json; invoke writes json")

	cmd.AddCommand(
		newConfigCommand(opts, reg),
		newCredentialCommand(opts, reg),
		newProvidersCommand(opts, reg),
		newConnectionsCommand(opts, reg),
		newToolsCommand(opts, reg),
		newDescribeCommand(opts, reg, "describe", false),
		newDescribeCommand(opts, reg, "tool", true),
		newInvokeCommand(opts, reg),
		newMCPCommand(opts, reg),
		newTUICommand(opts, reg, version),
		newUpdateCommand(opts, version),
	)
	// A command without a run function is a help topic: 'qatlas help <topic>' prints its text, and the
	// root help lists it under the additional help topics.
	for _, topic := range helptopics.All() {
		cmd.AddCommand(&cobra.Command{
			Use: topic.Name, Short: topic.Short, Long: helptopics.Wrap(topic.Text, 80),
		})
	}

	return cmd
}

// DocumentationCommand returns the real command tree for release-time documentation generation. It is
// internal to this module, so the shipped CLI keeps one command definition without exposing a public Go API.
func DocumentationCommand(buildVersion string) *cobra.Command {
	opts := &Options{Redactor: &redact.Redactor{}, Input: os.Stdin}
	cmd := newRootCommand(opts, defaultRegistry())
	cmd.Version = buildVersion
	cmd.DisableAutoGenTag = true
	return cmd
}

// resolveFormat decides the output format once, before any command runs. An explicit --output always
// wins; otherwise agent mode selects the compact format.
func resolveFormat(c *cobra.Command, opts *Options) error {
	if c.Flags().Changed("output") {
		format, err := output.ParseFormat(opts.Output)
		if err != nil {
			return newSyntaxError(err)
		}
		opts.Format = format
		return nil
	}
	if opts.Agent {
		opts.Format = output.FormatCompact
		return nil
	}
	opts.Format = output.FormatTable
	return nil
}

// emit redacts and encodes a result to stdout. Only payload data reaches stdout. A command that offers a
// field selection projects before it emits; nothing here shortens a result, so a report never drops rows
// the caller believes it saw.
func emit(c *cobra.Command, opts *Options, result output.Result) error {
	return output.Encode(c.OutOrStdout(), opts.Format, redactResult(opts.Redactor, result))
}

// redactResult removes known secrets from string values before an encoder escapes or serializes them.
// Non-string values keep their types, and the input result stays unchanged.
func redactResult(redactor *redact.Redactor, result output.Result) output.Result {
	if redactor == nil {
		return result
	}

	switch r := result.(type) {
	case output.Collection:
		rows := make([]output.Row, len(r.Rows))
		for i, row := range r.Rows {
			rows[i] = make(output.Row, len(row))
			for name, value := range row {
				if text, ok := value.(string); ok {
					value = redactor.Apply(text)
				}
				rows[i][name] = value
			}
		}
		return output.Collection{Columns: r.Columns, Rows: rows}
	case output.Object:
		fields := make([]output.Field, len(r.Fields))
		for i, field := range r.Fields {
			fields[i] = field
			if text, ok := field.Value.(string); ok {
				fields[i].Value = redactor.Apply(text)
			}
		}
		return output.Object{Fields: fields}
	default:
		return result
	}
}
