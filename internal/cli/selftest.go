package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/selftest"
)

// selfContainedAnnotation marks a command tree that never touches the installation: no configuration
// migration, no configuration, vault, or log access. The selftest measures synthetic fixtures only.
const selfContainedAnnotation = "qatlas/self-contained"

// selfContained reports whether c is, or lies below, a command marked self-contained.
func selfContained(c *cobra.Command) bool {
	for ; c != nil; c = c.Parent() {
		if c.Annotations[selfContainedAnnotation] != "" {
			return true
		}
	}
	return false
}

// quietExit ends the process with a code whose message the command has already written itself.
type quietExit struct{ code int }

func (e *quietExit) Error() string { return fmt.Sprintf("exit status %d", e.code) }

// newSelftestCommand is 'qatlas selftest': one subcommand per scenario of the selftest register, plus the
// hidden worker process the scenarios start. It reads no --config, QATLAS_CONFIG, or QATLAS_CLI_HOME.
func newSelftestCommand(opts *Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "selftest",
		Short: "Measure this installation's performance on the local file system",
		Long: "The selftest measures this program on this machine without a Go toolchain and without touching\n" +
			"your installation: it works on synthetic data in a scratch directory it creates and removes\n" +
			"again, also when it fails or is interrupted. It reads and writes no configuration, vault, or\n" +
			"log, and ignores --config, QATLAS_CONFIG, and QATLAS_CLI_HOME. Each scenario is a subcommand.\n\n" +
			"The scratch directory lies in the system's temporary directory; --dir picks another base\n" +
			"directory, which decides the file system that is measured. Progress goes to stderr, the result\n" +
			"to stdout, as a table or, with --output json, as one JSON document. A run ends with exit code 1\n" +
			"when it is interrupted with ctrl+c or SIGTERM. Nothing here is offered over MCP.",
		Annotations: map[string]string{selfContainedAnnotation: "true"},
		Args:        noArgs,
		RunE:        func(c *cobra.Command, _ []string) error { return c.Help() },
	}
	for _, scenario := range selftest.Scenarios() {
		cmd.AddCommand(newSelftestScenarioCommand(opts, scenario))
	}
	cmd.AddCommand(&cobra.Command{
		Use:    "worker",
		Short:  "Writer process of a selftest scenario; started by 'qatlas selftest'",
		Hidden: true,
		Args:   noArgs,
		RunE: func(*cobra.Command, []string) error {
			if code := selftest.RunWorker(os.Stdin, os.Stdout, os.Stderr); code != 0 {
				return &quietExit{code}
			}
			return nil
		},
	})
	return cmd
}

func newSelftestScenarioCommand(opts *Options, scenario selftest.Scenario) *cobra.Command {
	var profile, dir string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   scenario.Name,
		Short: scenario.Summary,
		Long: scenario.Summary + "\n\nThe profile 'standard' is a real measurement and takes minutes; 'smoke' is a small, quick run\n" +
			"that covers every measurement. See 'qatlas selftest --help' for what the selftest does and\n" +
			"does not touch.",
		Args: noArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			p, err := selftest.ProfileByName(profile)
			if err != nil {
				return newSyntaxError(err)
			}
			exe, err := os.Executable()
			if err != nil {
				return fmt.Errorf("cannot find the running program: %w", err)
			}
			ctx, stop := signal.NotifyContext(c.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			stderr := c.ErrOrStderr()
			report, err := selftest.Run(ctx, selftest.Options{
				Scenario: scenario.Name, Profile: p, BaseDir: dir, Version: version,
				Worker:   selftest.WorkerCommand{Program: exe, Args: []string{"selftest", "worker"}},
				Progress: func(line string) { fmt.Fprintln(stderr, line) },
			})
			if err != nil {
				if ctx.Err() != nil {
					return errors.New("selftest interrupted; the scratch directory is removed")
				}
				return err
			}
			if asJSON || opts.Format == output.FormatJSON {
				enc := json.NewEncoder(c.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(report)
			}
			return writeSelftestText(c.OutOrStdout(), report)
		},
	}
	f := cmd.Flags()
	f.StringVar(&profile, "profile", "standard", "size of the run: standard or smoke")
	f.StringVar(&dir, "dir", "", "base directory of the scratch directory, which decides the file system measured; default the system's temporary directory")
	f.BoolVar(&asJSON, "json", false, "write the report as JSON (same as --output json)")
	_ = f.MarkHidden("json") // kept as an alias of --output json for existing callers
	return cmd
}

// writeSelftestText prints a header line per fact and one table per measurement group.
func writeSelftestText(w io.Writer, r *selftest.Report) error {
	fmt.Fprintf(w, "selftest %s (profile %s)\n", r.Scenario, r.Profile)
	if r.Version != "" {
		fmt.Fprintf(w, "version:   %s\n", r.Version)
	}
	fmt.Fprintf(w, "system:    %s/%s, %d CPUs\n", r.GOOS, r.GOARCH, r.CPUs)
	fmt.Fprintf(w, "directory: %s (removed)\n", r.TempDir)
	fmt.Fprintf(w, "elapsed:   %.0f ms\n", r.ElapsedMs)
	for i := 0; i < len(r.Measurements); {
		group := r.Measurements[i].Group
		fmt.Fprintf(w, "\n%s\n", group)
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for ; i < len(r.Measurements) && r.Measurements[i].Group == group; i++ {
			m := r.Measurements[i]
			cells := make([]string, 0, len(m.Values))
			for _, v := range m.Values {
				cells = append(cells, fmt.Sprintf("%s=%s", v.Name, formatSelftestValue(v.Value)))
			}
			fmt.Fprintf(tw, "  %s\t%s\n", m.Case, strings.Join(cells, "  "))
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	return nil
}

func formatSelftestValue(v float64) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d", int64(v))
	}
	return fmt.Sprintf("%.2f", v)
}
