package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/output"
)

func newConfigCommand(opts *Options, reg *capability.Registry) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect the qatlas configuration",
		Args:  noArgs,
		RunE:  func(c *cobra.Command, _ []string) error { return c.Help() },
	}

	var (
		secrets bool
		fields  []string
	)
	validate := &cobra.Command{
		Use:   "validate",
		Short: "Check that the configuration file is complete and consistent",
		Long: "Validate reads the configuration file and reports every schema and reference problem it\n" +
			"finds. It contacts no provider and reads no secret values. On success it prints\n" +
			"'configuration is valid: <path>'; --output json, compact, or toon, and --agent, print the same as\n" +
			"one object with the fields valid and path.\n\n" +
			"A connection's optional tools list names complete tool IDs of its provider, as 'qatlas tools\n" +
			"<provider> --all' lists them. Every entry must be a tool this build registers for that provider,\n" +
			"listed once, with an effect the connection's permissions allow. An unknown entry is reported\n" +
			"by its position, never quoted.\n\n" +
			"With --secrets it additionally resolves the secrets of every connection and reports which\n" +
			"source delivers each of them, so an environment variable that overrides the credential store\n" +
			"is visible instead of silent. It prints where a secret comes from, never what it is, and it\n" +
			"may ask the credential store to unlock, which is why it is not the default. The report always\n" +
			"covers every connection; --fields restricts which columns it shows, in the order given.",
		Args: noArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			path, err := config.Path(opts.Config)
			if err != nil {
				return err
			}
			cfg, err := config.Load(path, reg)
			if err != nil {
				return classifyUserError(err)
			}
			if !secrets {
				if opts.Format == output.FormatTable {
					_, err := fmt.Fprintf(c.OutOrStdout(), "configuration is valid: %s\n", opts.Redactor.Apply(path))
					return err
				}
				return emit(c, opts, output.Object{Fields: []output.Field{
					{Name: "valid", Value: true}, {Name: "path", Value: path},
				}})
			}
			result, err := secretSources(cfg, opts)
			if err != nil {
				return err
			}
			projected, err := output.Project(result, fields)
			if err != nil {
				return classifyUserError(err)
			}
			return emit(c, opts, projected)
		},
	}
	validate.Flags().BoolVar(&secrets, "secrets", false,
		"also report which source delivers each secret, without showing any value")
	validate.Flags().StringSliceVar(&fields, "fields", nil,
		"restrict the report to these fields, in this order")

	cmd.AddCommand(validate)
	return cmd
}

// secretSourceColumns is the stable field order of the source report.
var secretSourceColumns = []string{"connection", "credential", "role", "source", "checked"}

// secretSources resolves every secret every connection needs and reports the delivering stage. A secret
// that no stage delivers is reported as missing rather than aborting the run: the report is the answer to
// "where does this come from", and it stays useful exactly when something is wrong.
func secretSources(cfg *config.Config, opts *Options) (output.Result, error) {
	resolver, err := opts.resolver()
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(cfg.Connections))
	for name := range cfg.Connections {
		names = append(names, name)
	}
	sort.Strings(names)

	rows := make([]output.Row, 0, len(names))
	for _, name := range names {
		resolved, err := cfg.Resolve(name, "")
		if err != nil {
			return nil, classifyUserError(err)
		}
		for _, role := range cfg.ProviderSecretRoles(resolved.Provider) {
			source, checked := resolver.Status(resolved.Credential, resolved.Secrets, role)
			rows = append(rows, output.Row{
				"connection": name,
				"credential": resolved.Credential,
				"role":       role,
				"source":     string(source),
				"checked":    strings.Join(checked, ", "),
			})
		}
	}
	return output.Collection{Columns: secretSourceColumns, Rows: rows}, nil
}

func noArgs(_ *cobra.Command, args []string) error {
	if len(args) > 0 {
		return newSyntaxError(fmt.Errorf("unexpected argument %q", args[0]))
	}
	return nil
}
