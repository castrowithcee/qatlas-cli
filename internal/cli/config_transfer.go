package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/spf13/cobra"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/manage"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

func newConfigExportCommand(opts *Options, reg *capability.Registry) *cobra.Command {
	var (
		sel    manage.ExportSelection
		dryRun bool
	)
	cmd := &cobra.Command{
		Use:   "export <file>",
		Short: "Write chosen parts of the configuration, without any secret, to a file",
		Long: "Writes the services, credentials, and connections you choose to a new YAML file that only you\n" +
			"can read, so 'qatlas config import' can take them over into another installation. Choose with --all\n" +
			"or by name with --service, --credential, and --connection, each repeatable. A connection brings\n" +
			"the service, the credential, and the forward credentials it names; the output lists each of them\n" +
			"with the connections it was taken for. --defaults adds the default connections and the secret\n" +
			"store, but only those defaults whose connection is in the file; the others are listed as left\n" +
			"out. --provider-notes adds the provider notes. Neither is part of --all. Agent tokens,\n" +
			"approvals, the invocation log, and the vault's settings are never exported.\n\n" +
			"The file holds no secret. A keyring or vault credential is exported as its definition alone and\n" +
			"is listed as without secret; an env credential names its variables. There is no option to\n" +
			"export secrets. An existing file is never replaced. --dry-run lists what would be exported and\n" +
			"writes nothing.\n\n" +
			"Export is a management command: it runs only from an interactive terminal, and where the vault\n" +
			"is encrypted it asks for the passphrase first. No terminal fails with admin-required; an agent\n" +
			"never exports.\n\n" +
			"--output json writes one document with the fields file, services, credentials, connections,\n" +
			"dependencies (kind, name, for), defaults, defaults_omitted, provider_notes, secrets_not_included,\n" +
			"dry_run, and written.",
		Args: func(c *cobra.Command, args []string) error {
			if len(args) != 1 {
				return newSyntaxError(fmt.Errorf("expected the file to write, got %d arguments", len(args)))
			}
			return nil
		},
		RunE: func(c *cobra.Command, args []string) error {
			if err := requireAdmin(opts); err != nil {
				return err
			}
			svc, err := transferService(opts, reg)
			if err != nil {
				return err
			}
			target := args[0]
			if dryRun {
				target = ""
			}
			res, err := svc.ExportConfig(sel, target)
			if err != nil {
				return exportError(opts, err, args[0])
			}
			if opts.Format != output.FormatTable {
				return emitTransferDocument(c, opts, exportDocumentOf(opts, res, args[0], dryRun))
			}
			writeExportResult(c.OutOrStdout(), opts, res, args[0], dryRun)
			return nil
		},
	}
	// --connection is also a global flag that picks the connection of a call; here it selects entries.
	f := cmd.Flags()
	f.BoolVar(&sel.All, "all", false, "export every service, credential, and connection")
	f.StringSliceVar(&sel.Services, "service", nil, "export this service (repeatable)")
	f.StringSliceVar(&sel.Credentials, "credential", nil, "export this credential, without its secrets (repeatable)")
	f.StringSliceVar(&sel.Connections, "connection", nil,
		"export this connection with its service and credentials (repeatable)")
	f.BoolVar(&sel.Defaults, "defaults", false, "also export the defaults that name an exported connection")
	f.BoolVar(&sel.ProviderNotes, "provider-notes", false, "also export the provider notes")
	f.BoolVar(&dryRun, "dry-run", false, "list what would be exported and write nothing")
	return cmd
}

func newConfigImportCommand(opts *Options, reg *capability.Registry) *cobra.Command {
	var (
		modeText string
		dryRun   bool
		yes      bool
	)
	cmd := &cobra.Command{
		Use:   "import <file>",
		Short: "Take over an exported configuration file into this installation",
		Long: "Reads a file written by 'qatlas config export' and applies it to this configuration in one\n" +
			"step: all of it or nothing. Entries are matched by name. --mode says what happens to a name this\n" +
			"installation already has: create (the default) stops the whole import, merge adds what is missing\n" +
			"and keeps the existing entries, overwrite replaces them. A replaced entry keeps its id, so approvals\n" +
			"stay attached; an id the file brings is kept only if it is free, otherwise a new one is assigned.\n\n" +
			"The command first lists every entry with its action (create, replace, skip), its id and whether it\n" +
			"is new, the connections of this installation that a replaced service or credential changes, the\n" +
			"entries of this installation that an imported connection uses because its own is skipped, the\n" +
			"credentials that hold no secret here until one is set, and the replaced credentials whose secret\n" +
			"here stays as it is. Connections that bring paths, file directories, or forward credentials are\n" +
			"marked, because the file comes from elsewhere. Nothing is written until you confirm; --dry-run\n" +
			"lists and stops, --yes confirms in advance. With --output json, compact, or toon the list goes to\n" +
			"stderr before the question, and stdout keeps the document. The file must be complete in itself,\n" +
			"and the configuration that results must be valid, or nothing is written. If the configuration\n" +
			"changed since the list was made, nothing is written either.\n\n" +
			"The import touches no secret, approval, or agent token. Create a secret for a credential listed\n" +
			"as without secret with 'qatlas credential set'; approve new connections where the vault is\n" +
			"encrypted with 'qatlas vault approve'.\n\n" +
			"Import is a management command: it runs only from an interactive terminal, and where the vault\n" +
			"is encrypted it asks for the passphrase first. No terminal fails with admin-required; an agent\n" +
			"never imports.\n\n" +
			"--output json writes one document with the fields source, mode, entries (section, name, action,\n" +
			"id, new_id, uses_target, used_by, access), created, replaced, skipped, credentials_without_secret,\n" +
			"credentials_keep_secret, dry_run, and imported.",
		Args: func(c *cobra.Command, args []string) error {
			if len(args) != 1 {
				return newSyntaxError(fmt.Errorf("expected the file to import, got %d arguments", len(args)))
			}
			return nil
		},
		RunE: func(c *cobra.Command, args []string) error {
			mode, err := manage.ParseImportMode(modeText)
			if err != nil {
				return newSyntaxError(err)
			}
			if err := requireAdmin(opts); err != nil {
				return err
			}
			svc, err := transferService(opts, reg)
			if err != nil {
				return err
			}
			plan, err := svc.PlanImport(args[0], mode)
			if err != nil {
				return importError(err)
			}
			doc := importDocumentOf(opts, plan, dryRun)
			structured := opts.Format != output.FormatTable
			out := c.OutOrStdout()
			if structured {
				out = io.Discard
			}
			// A structured document only reaches stdout at the end, so a person asked to confirm reads the
			// plan on stderr instead.
			planOut := out
			if structured && !dryRun && !yes {
				planOut = c.ErrOrStderr()
			}
			writeImportPlan(planOut, opts, plan)
			created, replaced, _ := plan.Count()
			switch {
			case dryRun:
				fmt.Fprintln(out, "dry run: nothing was changed")
				return finishTransfer(c, opts, structured, doc)
			case created+replaced == 0:
				fmt.Fprintln(out, "nothing to import: the configuration already has everything")
				return finishTransfer(c, opts, structured, doc)
			}
			if !yes {
				question := fmt.Sprintf("import %s into the configuration (%d to create, %d to replace)? [y/N] ",
					opts.Redactor.Apply(args[0]), created, replaced)
				confirmed, err := readVaultConfirm(question)
				if err != nil {
					if errors.Is(err, vault.ErrNoTerminal) {
						return &UsageError{&application.AdminRequiredError{}}
					}
					return err
				}
				if !confirmed {
					fmt.Fprintln(c.ErrOrStderr(), "qatlas: import cancelled, nothing was changed")
					return finishTransfer(c, opts, structured, doc)
				}
			}
			res, err := svc.ApplyImport(plan)
			if err != nil {
				if errors.Is(err, config.ErrConflict) {
					return &UsageError{fmt.Errorf("nothing was written: %s; list the import again and repeat it",
						opts.Redactor.Apply(err.Error()))}
				}
				return classifyUserError(err)
			}
			for _, warning := range res.Warnings {
				fmt.Fprintln(c.ErrOrStderr(), withQatlasPrefix(warning))
			}
			doc.Imported = true
			fmt.Fprintf(out, "created %d, replaced %d, skipped %d\n", res.Created, res.Replaced, res.Skipped)
			if len(plan.CredentialsWithoutSecret) > 0 {
				fmt.Fprintf(out, "set a secret with 'qatlas credential set' before use: %s\n",
					redactAll(opts.Redactor, plan.CredentialsWithoutSecret))
			}
			if created > 0 {
				fmt.Fprintln(out, "new connections need the usual release; where the vault is encrypted, "+
					"run 'qatlas vault approve'")
			}
			return finishTransfer(c, opts, structured, doc)
		},
	}
	f := cmd.Flags()
	f.StringVar(&modeText, "mode", string(manage.ImportModeCreate),
		"what to do with a name this installation already has: create (stop), merge (keep it), overwrite (replace it)")
	f.BoolVar(&dryRun, "dry-run", false, "list what the import would do and change nothing")
	f.BoolVar(&yes, "yes", false, "apply without asking to confirm")
	return cmd
}

// transferService is the management service export and import run on; neither touches a secret.
func transferService(opts *Options, reg *capability.Registry) (*manage.Service, error) {
	path, err := config.Path(opts.Config)
	if err != nil {
		return nil, err
	}
	v, err := vaultOf(opts)
	if err != nil {
		return nil, err
	}
	return vaultService(opts, config.NewStore(path, reg), v), nil
}

func exportError(opts *Options, err error, path string) error {
	if errors.Is(err, fs.ErrExist) {
		return &UsageError{fmt.Errorf("%s already exists; export never replaces a file, choose another name",
			opts.Redactor.Apply(path))}
	}
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return err
	}
	return &UsageError{errors.New(opts.Redactor.Apply(err.Error()))}
}

func importError(err error) error {
	var exists *manage.ImportExistsError
	var refused *manage.ImportRefusedError
	if errors.As(err, &exists) || errors.As(err, &refused) {
		return &UsageError{err}
	}
	return classifyUserError(err)
}

func writeExportResult(out io.Writer, opts *Options, res *manage.ExportResult, path string, dryRun bool) {
	apply := opts.Redactor.Apply
	if dryRun {
		fmt.Fprintln(out, "export preview (nothing is written)")
	} else {
		fmt.Fprintf(out, "exported to %s\n", apply(path))
	}
	for _, line := range []struct {
		label string
		names []string
	}{
		{"services", res.Services}, {"credentials", res.Credentials}, {"connections", res.Connections},
		{"defaults", res.Defaults}, {"provider notes", res.ProviderNotes},
	} {
		if len(line.names) > 0 {
			fmt.Fprintf(out, "  %-15s %s\n", line.label, redactAll(opts.Redactor, line.names))
		}
	}
	for _, dep := range res.Dependencies {
		fmt.Fprintf(out, "  dependency      %s %s, for %s\n", dep.Kind, apply(dep.Name), redactAll(opts.Redactor, dep.For))
	}
	if len(res.DefaultsOmitted) > 0 {
		fmt.Fprintf(out, "  left out        defaults %s: their connection is not exported\n",
			redactAll(opts.Redactor, res.DefaultsOmitted))
	}
	if len(res.SecretsNotIncluded) > 0 {
		fmt.Fprintf(out, "  without secret  %s: set them in the target with 'qatlas credential set'\n",
			redactAll(opts.Redactor, res.SecretsNotIncluded))
	}
}

func writeImportPlan(out io.Writer, opts *Options, plan *manage.ImportPlan) {
	apply := opts.Redactor.Apply
	fmt.Fprintf(out, "import %s, mode %s\n", apply(plan.Source), plan.Mode)
	for _, e := range plan.Entries {
		line := fmt.Sprintf("  %-8s %s %s", e.Action, e.Section, apply(e.Name))
		if e.ID != "" {
			line += " (id " + e.ID
			switch {
			case e.NewID && e.Action == manage.ImportActionReplace:
				line += ", kept from this installation"
			case e.NewID:
				line += ", new"
			}
			line += ")"
		}
		fmt.Fprintln(out, line)
		if len(e.UsesTarget) > 0 {
			fmt.Fprintf(out, "           uses this installation's %s\n", redactAll(opts.Redactor, e.UsesTarget))
		}
		if len(e.UsedBy) > 0 {
			fmt.Fprintf(out, "           changes connections of this installation: %s\n", redactAll(opts.Redactor, e.UsedBy))
		}
		if len(e.Access) > 0 {
			fmt.Fprintf(out, "           brings local access from the file: %s\n", strings.Join(e.Access, ", "))
		}
	}
	if len(plan.CredentialsWithoutSecret) > 0 {
		fmt.Fprintf(out, "  without secret here until one is set: %s\n", redactAll(opts.Redactor, plan.CredentialsWithoutSecret))
	}
	if len(plan.CredentialsKeepSecret) > 0 {
		fmt.Fprintf(out, "  secret here stays as it is, it may not fit the replacement: %s\n",
			redactAll(opts.Redactor, plan.CredentialsKeepSecret))
	}
	created, replaced, skipped := plan.Count()
	fmt.Fprintf(out, "%d to create, %d to replace, %d to skip\n", created, replaced, skipped)
}

// emitTransferDocument writes doc as JSON, or TOON for --output toon; compact and --agent are JSON as well.
func emitTransferDocument(c *cobra.Command, opts *Options, doc any) error {
	format := output.FormatJSON
	if opts.Format == output.FormatTOON {
		format = output.FormatTOON
	}
	return emitDocument(c, format, doc)
}

func finishTransfer(c *cobra.Command, opts *Options, structured bool, doc importDocument) error {
	if !structured {
		return nil
	}
	return emitTransferDocument(c, opts, doc)
}

type dependencyDocument struct {
	Kind string   `json:"kind"`
	Name string   `json:"name"`
	For  []string `json:"for"`
}

type exportDocument struct {
	File               string               `json:"file"`
	Services           []string             `json:"services"`
	Credentials        []string             `json:"credentials"`
	Connections        []string             `json:"connections"`
	Dependencies       []dependencyDocument `json:"dependencies"`
	Defaults           []string             `json:"defaults"`
	DefaultsOmitted    []string             `json:"defaults_omitted"`
	ProviderNotes      []string             `json:"provider_notes"`
	SecretsNotIncluded []string             `json:"secrets_not_included"`
	DryRun             bool                 `json:"dry_run"`
	Written            bool                 `json:"written"`
}

func exportDocumentOf(opts *Options, res *manage.ExportResult, path string, dryRun bool) exportDocument {
	r := opts.Redactor
	doc := exportDocument{
		File: r.Apply(path), Services: redactEach(r, res.Services), Credentials: redactEach(r, res.Credentials),
		Connections: redactEach(r, res.Connections), Dependencies: []dependencyDocument{},
		Defaults: redactEach(r, res.Defaults), DefaultsOmitted: redactEach(r, res.DefaultsOmitted),
		ProviderNotes: redactEach(r, res.ProviderNotes), SecretsNotIncluded: redactEach(r, res.SecretsNotIncluded),
		DryRun: dryRun, Written: !dryRun,
	}
	for _, dep := range res.Dependencies {
		doc.Dependencies = append(doc.Dependencies, dependencyDocument{
			Kind: dep.Kind, Name: r.Apply(dep.Name), For: redactEach(r, dep.For)})
	}
	return doc
}

type importEntryDocument struct {
	Section    string   `json:"section"`
	Name       string   `json:"name"`
	Action     string   `json:"action"`
	ID         string   `json:"id,omitempty"`
	NewID      bool     `json:"new_id"`
	UsesTarget []string `json:"uses_target"`
	UsedBy     []string `json:"used_by"`
	Access     []string `json:"access"`
}

type importDocument struct {
	Source                   string                `json:"source"`
	Mode                     string                `json:"mode"`
	Entries                  []importEntryDocument `json:"entries"`
	Created                  int                   `json:"created"`
	Replaced                 int                   `json:"replaced"`
	Skipped                  int                   `json:"skipped"`
	CredentialsWithoutSecret []string              `json:"credentials_without_secret"`
	CredentialsKeepSecret    []string              `json:"credentials_keep_secret"`
	DryRun                   bool                  `json:"dry_run"`
	Imported                 bool                  `json:"imported"`
}

func importDocumentOf(opts *Options, plan *manage.ImportPlan, dryRun bool) importDocument {
	r := opts.Redactor
	doc := importDocument{
		Source: r.Apply(plan.Source), Mode: string(plan.Mode), Entries: []importEntryDocument{},
		CredentialsWithoutSecret: redactEach(r, plan.CredentialsWithoutSecret),
		CredentialsKeepSecret:    redactEach(r, plan.CredentialsKeepSecret), DryRun: dryRun,
	}
	doc.Created, doc.Replaced, doc.Skipped = plan.Count()
	for _, e := range plan.Entries {
		doc.Entries = append(doc.Entries, importEntryDocument{
			Section: e.Section, Name: r.Apply(e.Name), Action: e.Action, ID: e.ID, NewID: e.NewID,
			UsesTarget: redactEach(r, e.UsesTarget), UsedBy: redactEach(r, e.UsedBy), Access: redactEach(r, e.Access),
		})
	}
	return doc
}
