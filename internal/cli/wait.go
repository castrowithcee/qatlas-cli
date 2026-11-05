package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/castrowithcee/qatlas-connect/internal/application"
	"github.com/castrowithcee/qatlas-connect/internal/capability"
)

// Exit codes of 'qconnect wait' beyond the three of every command. They tell the ends of a wait apart from
// an error, which keeps 1 and 2.
const (
	exitWaitFail        = 3
	exitWaitTimeout     = 4
	exitWaitNone        = 5
	exitWaitHeadChanged = 6
)

// waitResultCodes maps the result member of a waiting tool to the exit code of the command.
var waitResultCodes = map[string]int{
	"pass": exitOK, "fail": exitWaitFail, "timeout": exitWaitTimeout, "none": exitWaitNone,
	"head-changed": exitWaitHeadChanged,
}

// The bounds of the flags of 'qconnect wait'. They are variables only so a test can shorten them.
var (
	defaultWaitTimeout  = 45 * time.Minute
	maxWaitTimeout      = 2 * time.Hour
	defaultWaitInterval = 20 * time.Second
	minWaitInterval     = 5 * time.Second
	// waitGrace is how long the context outlasts the limit of the tool, so the tool ends the wait with its own
	// answer before the context ends it with an error.
	waitGrace = time.Minute
)

// newWaitCommand runs a tool that waits for the end of something with a limit of its own instead of the
// 60 seconds of an invoke. It selects the connection, checks the target, and resolves the secret exactly as
// invoke does: the tool runs through the same core.
func newWaitCommand(opts *Options, registry *capability.Registry) *cobra.Command {
	var (
		flagArgs          []string
		timeout, interval time.Duration
	)
	cmd := &cobra.Command{
		Use:   "wait <tool-id>",
		Short: "Wait for the end of something with a tool that waits",
		Long: "Wait runs a tool that waits, one whose contract says that it waits, until its wait ends, and\n" +
			"writes its result as invoke does: exactly one JSON object with a data field on stdout. The tool\n" +
			"decides how it ends; its result member names how. Through MCP the same tool answers after at\n" +
			"most 50 seconds that it is still waiting, so an agent with a shell uses this command instead.\n\n" +
			"The arguments, --arg, stdin, --connection, and the refusals before a secret is read are those of\n" +
			"'qconnect invoke'. --timeout (default 45m, at most 2h) is how long the wait may take, not the\n" +
			"60 seconds of an invoke, and --interval (default 20s, at least 5s) how often the tool checks. A\n" +
			"wait that reaches --timeout ends with the result timeout. Interrupting the command ends it\n" +
			"without a result.\n\n" +
			"The exit code names how the wait ended:\n\n" +
			"  0  pass          the awaited condition holds\n" +
			"  3  fail          the awaited condition failed\n" +
			"  4  timeout       --timeout passed\n" +
			"  5  none          there was nothing to wait for\n" +
			"  6  head-changed  what was awaited was replaced by something newer\n\n" +
			"An error keeps its code: 1 for a runtime error, 2 for a request or a configuration to correct. The\n" +
			"tool's contract, 'qconnect describe <tool-id>', defines the results.",
		Args: exactlyOneArg("tool ID"),
		RunE: func(c *cobra.Command, args []string) error {
			if err := checkInvokeFormat(c, opts); err != nil {
				return err
			}
			if timeout <= 0 || timeout > maxWaitTimeout {
				return &UsageError{fmt.Errorf("--timeout must be longer than zero and at most %s", maxWaitTimeout)}
			}
			if interval < minWaitInterval {
				return &UsageError{fmt.Errorf("--interval must be at least %s", minWaitInterval)}
			}
			if descriptor, _, ok := registry.Lookup(args[0]); ok && !descriptor.Waits {
				return &UsageError{fmt.Errorf("tool %s does not wait; run it with 'qconnect invoke'", args[0])}
			}
			// Reading the arguments keeps the limit of an invoke; only the wait itself is longer.
			readCtx, stop := context.WithTimeout(c.Context(), invokeTimeout)
			arguments, err := invokeArguments(readCtx, c, registry, args[0], flagArgs)
			stop()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(c.Context(), timeout+waitGrace)
			defer cancel()
			core, err := applicationCoreForInvoke(opts, registry, "cli", nil)
			if err != nil {
				return err
			}
			var audit bytes.Buffer
			core.SetAudit(&audit)
			response, err := core.Invoke(capability.WithWait(ctx, capability.WaitOptions{Limit: timeout, Interval: interval}),
				application.InvokeRequest{Operation: args[0], Connection: opts.Connection, Arguments: arguments})
			if err != nil {
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					err = pastDeadline(err, timeout+waitGrace)
				}
				return withAudit(classifyUserError(err), audit.Bytes())
			}
			if err := writeEnvelope(c.OutOrStdout(), response); err != nil {
				return err
			}
			var outcome struct {
				Result string `json:"result"`
			}
			_ = json.Unmarshal(response.Result, &outcome)
			code, known := waitResultCodes[outcome.Result]
			if !known {
				return fmt.Errorf("tool %s ended its wait with the result %q, which this command does not know",
					args[0], outcome.Result)
			}
			if code != exitOK {
				return &quietExit{code}
			}
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&flagArgs, "arg", nil,
		"one tool argument as name=value, repeated per argument; typed by the input schema")
	cmd.Flags().DurationVar(&timeout, "timeout", defaultWaitTimeout, "how long the wait may take, at most 2h")
	cmd.Flags().DurationVar(&interval, "interval", defaultWaitInterval, "how often the tool checks, at least 5s")
	return cmd
}
