package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/manage"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

func newConfigCommand(opts *Options, reg *capability.Registry) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect the qatlas configuration and rename its entries",
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
			"listed once. A connection gives either effects or tools, not both. An unknown entry is reported\n" +
			"by its position, never quoted.\n\n" +
			"A connection that offers no tool, because its effects or its tools list are empty or allow\n" +
			"none of its provider's tools, is valid but useless to an agent. Validate names each one in a\n" +
			"warning on stderr and still succeeds.\n\n" +
			"A connection's optional paths list binds it to projects: each entry is a directory, absolute or\n" +
			"starting with '~/', and glob patterns are refused. An entry that names no existing directory is\n" +
			"valid, since the directory may be created later; validate names it by its position in a warning\n" +
			"on stderr and still succeeds. Validate itself checks every connection, wherever it is run.\n\n" +
			"Validate also looks for a connection or credential renamed by hand in the file while the vault\n" +
			"still holds its approval or entry under the old name. It reads the vault's names, never a secret,\n" +
			"and never asks for a passphrase or starts a vault process: an unencrypted vault is read directly,\n" +
			"an encrypted one only where a running vault process holds it unlocked. Each rename it finds is a\n" +
			"note on stderr with the 'qatlas config rename <kind> <old> <new> --adopt' command that moves the\n" +
			"vault after it, or, where it cannot tell which entry is meant, a note without a command. The\n" +
			"validation still succeeds. Where the vault cannot be read this way, one note says it was not\n" +
			"checked. --output json adds hand_renames (kind, old, new, ambiguous, command) when there are any\n" +
			"and vault_checked false when the vault was not checked.\n\n" +
			"With --secrets it additionally resolves the secrets of every connection and reports which\n" +
			"source delivers each of them, so an environment variable that overrides the credential store\n" +
			"is visible instead of silent. It prints where a secret comes from, never what it is, and it\n" +
			"may ask the credential store to unlock, or an encrypted vault for its passphrase, which is why\n" +
			"it is not the default. The report always covers every connection; --fields restricts which\n" +
			"columns it shows, in the order given.",
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
			// A connection that offers no tool is valid, so it only warns: the file stays usable, and the
			// warning goes to stderr so stdout keeps its answer.
			for _, name := range sortedConnections(cfg) {
				if warning := cfg.IdleWarning(name); warning != "" {
					fmt.Fprintf(c.ErrOrStderr(), "qatlas: warning: %s\n", warning)
				}
			}
			for _, warning := range append(append(cfg.PathWarnings(), cfg.FilesWarnings()...), cfg.ToolWarnings()...) {
				fmt.Fprintf(c.ErrOrStderr(), "qatlas: warning: %s\n", warning)
			}
			if legacyPath := filepath.Join(filepath.Dir(path), secret.FileName); fileExists(legacyPath) {
				fmt.Fprintf(c.ErrOrStderr(), "qatlas: warning: %s still holds plaintext secrets; run 'qatlas "+
					"vault migrate' to move them into the vault\n", opts.Redactor.Apply(legacyPath))
			}
			renames, notChecked := vaultHandRenames(c, opts, cfg)
			if !secrets {
				if opts.Format == output.FormatTable {
					_, err := fmt.Fprintf(c.OutOrStdout(), "configuration is valid: %s\n", opts.Redactor.Apply(path))
					return err
				}
				fields := []output.Field{{Name: "valid", Value: true}, {Name: "path", Value: path}}
				if len(renames) > 0 {
					fields = append(fields, output.Field{Name: "hand_renames", Value: renames})
				}
				if notChecked {
					fields = append(fields, output.Field{Name: "vault_checked", Value: false})
				}
				return emit(c, opts, output.Object{Fields: fields})
			}
			result, err := secretSources(contextOrBackground(c.Context()), cfg, opts)
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

	cmd.AddCommand(validate, newConfigRenameCommand(opts, reg))
	return cmd
}

// vaultHandRenames notes on stderr every rename made by hand in cfg that the vault shows, with the command that
// adopts it, as far as the vault can be read without a passphrase (see readableVaultNames); where it cannot,
// one sentence says so. It changes nothing and starts nothing. A notice never fails the validation.
func vaultHandRenames(c *cobra.Command, opts *Options, cfg *config.Config) (found []handRenameDocument, notChecked bool) {
	names, present, checked := readableVaultNames(contextOrBackground(c.Context()), opts)
	switch {
	case !present:
		return nil, false
	case !checked:
		fmt.Fprintln(c.ErrOrStderr(), "qatlas: note: the vault was not checked for renames made by hand: it is "+
			"encrypted and no vault process holds it unlocked that can tell its names")
		return nil, true
	}
	for _, r := range manage.HandRenames(cfg, names) {
		fmt.Fprintf(c.ErrOrStderr(), "qatlas: note: %s\n", handRenameText(opts, r))
		found = append(found, handRenameDocumentOf(opts, r))
	}
	return found, false
}

// secretSourceColumns is the stable field order of the source report.
var secretSourceColumns = []string{"connection", "credential", "role", "source", "checked"}

// secretSources resolves every secret every connection needs and reports the delivering stage. A secret
// that no stage delivers is reported as missing rather than aborting the run: the report is the answer to
// "where does this come from", and it stays useful exactly when something is wrong.
func secretSources(ctx context.Context, cfg *config.Config, opts *Options) (output.Result, error) {
	resolver, err := opts.resolver()
	if err != nil {
		return nil, err
	}

	names := sortedConnections(cfg)
	rows := make([]output.Row, 0, len(names))
	for _, name := range names {
		resolved, err := cfg.Resolve(name, "")
		if err != nil {
			return nil, classifyUserError(err)
		}
		for _, role := range cfg.ProviderSecretRoles(resolved.Provider) {
			source, checked := resolver.StatusContext(secret.ForConnection(ctx, resolved), resolved.Credential,
				resolved.Secrets, role)
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

// sortedConnections returns the names of the configured connections in name order.
func sortedConnections(cfg *config.Config) []string {
	names := make([]string, 0, len(cfg.Connections))
	for name := range cfg.Connections {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// fileExists reports whether path names a file or directory that is there, without saying anything about
// its content: the caller of this helper only ever asks whether something must still be migrated away.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func noArgs(_ *cobra.Command, args []string) error {
	if len(args) > 0 {
		return newSyntaxError(fmt.Errorf("unexpected argument %q", args[0]))
	}
	return nil
}
