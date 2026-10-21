package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/connlog"
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
			"is listed as without secret; an env credential names its variables. This command has no option to\n" +
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
		modeText    string
		storeText   string
		dryRun      bool
		yes         bool
		storeChoice = func() error {
			switch storeText {
			case "", config.CredentialTypeKeyring, config.CredentialTypeVault:
				return nil
			}
			return newSyntaxError(fmt.Errorf("unknown secret store %q, use %s or %s", storeText,
				config.CredentialTypeKeyring, config.CredentialTypeVault))
		}
	)
	cmd := &cobra.Command{
		Use:   "import <file>",
		Short: "Take over an exported configuration file into this installation",
		Long: "Reads a file written by an export and applies it to this configuration in one step: all of it or\n" +
			"nothing. Entries are matched by name. --mode says what happens to a name this installation already\n" +
			"has: create (the default) stops the whole import, merge adds what is missing and keeps the existing\n" +
			"entries, overwrite replaces them. A replaced entry keeps its id, so approvals stay attached; an id\n" +
			"the file brings is kept only if it is free, otherwise a new one is assigned.\n\n" +
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
			"A file can bring secrets. The command then asks for the export passphrase if the file is\n" +
			"encrypted (a wrong one stops it before anything changes) and lists per credential the roles it\n" +
			"brings, never a value, whether the file was encrypted, and where the secrets go: the system keyring\n" +
			"or the vault, as defaults.secret_store of the result says, or as --secret-store keyring|vault\n" +
			"chooses for this import only. A secret is written together with the configuration or not at\n" +
			"all; if anything fails, what was written is put back. A file with secrets that is not encrypted\n" +
			"is a warning, and after a successful import the command offers to delete that file. Deleting it\n" +
			"is never done by --yes. A vault that gets its first secret here offers a passphrase as\n" +
			"'qatlas credential set' does; where the vault is encrypted, approvals that covered a replaced\n" +
			"secret are withdrawn. This command has no export with secrets.\n\n" +
			"After the write the command lists the connections it created or replaced and, where the vault is\n" +
			"encrypted, asks once whether to approve them now. --yes never approves: approving is its own\n" +
			"decision, made later with 'qatlas vault approve'. A vault that is not encrypted binds no\n" +
			"connection. Without secrets in the file the import touches no secret; set one for a credential\n" +
			"listed as without secret with 'qatlas credential set'. No agent token is taken from the file.\n\n" +
			"Import is a management command: it runs only from an interactive terminal, and where the vault\n" +
			"is encrypted it asks for the passphrase first. No terminal fails with admin-required; an agent\n" +
			"never imports.\n\n" +
			"--output json writes one document with the fields source, mode, entries (section, name, action,\n" +
			"id, new_id, uses_target, used_by, access), created, replaced, skipped, credentials_without_secret,\n" +
			"credentials_keep_secret, secrets (credential, roles), source_encrypted, secret_store,\n" +
			"keyring_over_ssh, withdrawn, approved, open (connection, reason, imported), source_deleted,\n" +
			"dry_run, and imported.",
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
			if err := storeChoice(); err != nil {
				return err
			}
			if err := requireAdmin(opts); err != nil {
				return err
			}
			svc, err := transferService(opts, reg)
			if err != nil {
				return err
			}
			plan, err := svc.PlanImportFile(args[0], mode, askExportPassphrase(opts))
			if err != nil {
				return importError(err)
			}
			store, overSSH, err := plan.SecretStoreFor(storeText)
			if err != nil {
				return newSyntaxError(err)
			}
			hasSecrets := len(plan.SecretRoles) > 0
			doc := importDocumentOf(opts, plan, dryRun)
			if hasSecrets {
				doc.SecretStore, doc.KeyringOverSSH = store, overSSH
			}
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
			writeImportPlan(planOut, opts, plan, store, overSSH)
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
				question := fmt.Sprintf("import %s into the configuration (%d to create, %d to replace",
					opts.Redactor.Apply(args[0]), created, replaced)
				if hasSecrets {
					question += fmt.Sprintf("; secrets of %d credentials go to the %s", len(plan.SecretRoles), store)
				}
				confirmed, err := readVaultConfirm(question + ")? [y/N] ")
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
			res, err := svc.ApplyImportWith(plan, manage.ImportOptions{SecretStore: storeText, Offer: offerVaultPassphrase})
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
			if res.SecretStore != "" {
				fmt.Fprintf(out, "secrets written to the %s for: %s\n", res.SecretStore,
					redactAll(opts.Redactor, secretCredentials(plan.SecretRoles)))
			}
			if len(res.Withdrawn) > 0 {
				doc.Withdrawn = redactEach(opts.Redactor, res.Withdrawn)
				fmt.Fprintf(out, "approvals withdrawn, they covered replaced secrets: %s\n",
					redactAll(opts.Redactor, res.Withdrawn))
			}
			if len(plan.CredentialsWithoutSecret) > 0 {
				fmt.Fprintf(out, "set a secret with 'qatlas credential set' before use: %s\n",
					redactAll(opts.Redactor, plan.CredentialsWithoutSecret))
			}
			approveImported(c, opts, svc, res, yes, out, planOut, &doc)
			if hasSecrets && !plan.SourceEncrypted {
				offerSourceDeletion(c, opts, args[0], yes, out, &doc)
			}
			return finishTransfer(c, opts, structured, doc)
		},
	}
	f := cmd.Flags()
	f.StringVar(&modeText, "mode", string(manage.ImportModeCreate),
		"what to do with a name this installation already has: create (stop), merge (keep it), overwrite (replace it)")
	f.StringVar(&storeText, "secret-store", "",
		"where secrets from the file go for this import: keyring or vault (default: defaults.secret_store)")
	f.BoolVar(&dryRun, "dry-run", false, "list what the import would do and change nothing")
	f.BoolVar(&yes, "yes", false, "apply without asking to confirm; it neither approves nor deletes the file")
	return cmd
}

// askExportPassphrase asks for the passphrase of an encrypted export file, masked. It is not the vault's.
func askExportPassphrase(opts *Options) vault.PassphraseFunc {
	return func(string) (string, error) {
		passphrase, err := readVaultPassphrase("export passphrase: ")
		if err != nil {
			if errors.Is(err, vault.ErrNoTerminal) {
				return "", &UsageError{&application.AdminRequiredError{}}
			}
			return "", err
		}
		opts.Redactor.Add(passphrase)
		return passphrase, nil
	}
}

// approveImported reports the approval state of the imported connections and, where the vault binds them,
// asks once whether to approve them. A failure here never undoes the import.
func approveImported(c *cobra.Command, opts *Options, svc *manage.Service, res manage.ImportResult, yes bool,
	out, askOut io.Writer, doc *importDocument) {
	if len(res.Connections) == 0 && len(res.UsedBy) == 0 {
		return
	}
	errOut := c.ErrOrStderr()
	report := svc.ApproveImport(contextOrBackground(c.Context()), res, manage.ApprovalNone)
	asked := false
	if hasImportedOpen(report) && !yes {
		asked = true
		// The person sees which connections the answer approves before giving it.
		for _, o := range report.Open {
			if o.Imported {
				fmt.Fprintf(askOut, "to approve: %s, %s\n", opts.Redactor.Apply(o.Connection), opts.Redactor.Apply(o.Reason))
			}
		}
		confirmed, err := readVaultConfirm("approve the imported connections now? [y/N] ")
		if err == nil && confirmed {
			report = svc.ApproveImport(contextOrBackground(c.Context()), res, manage.ApprovalDirectOnly)
		}
	}
	for _, warning := range report.Warnings {
		fmt.Fprintln(errOut, withQatlasPrefix(warning))
	}
	if report.Err != nil {
		fmt.Fprintln(errOut, withQatlasPrefix("could not check the approvals: "+opts.Redactor.Apply(report.Err.Error())))
	}
	switch {
	case report.VaultUnencrypted:
		fmt.Fprintln(out, "the vault is not encrypted; connections are not bound to approvals, so there is nothing to approve")
	case report.VaultLocked:
		fmt.Fprintln(out, "the vault is locked; approve the connections with 'qatlas vault approve' after unlocking it")
	}
	if len(res.Connections) > 0 && report.Err == nil && len(report.Open) == 0 && len(report.Approved) == 0 &&
		!report.VaultUnencrypted && !report.VaultLocked && !vaultIsEncrypted(opts) {
		fmt.Fprintln(out, "new connections need the usual release; where the vault is encrypted, run 'qatlas vault approve'")
	}
	if len(report.Approved) > 0 {
		doc.Approved = redactEach(opts.Redactor, report.Approved)
		fmt.Fprintf(out, "approved: %s\n", redactAll(opts.Redactor, report.Approved))
	}
	for _, o := range report.Open {
		doc.Open = append(doc.Open, openDocument{Connection: opts.Redactor.Apply(o.Connection),
			Reason: opts.Redactor.Apply(o.Reason), Imported: o.Imported})
		who := "imported connection"
		if !o.Imported {
			who = "connection of this installation changed by the import"
		}
		fmt.Fprintf(out, "open: %s %s, %s\n", who, opts.Redactor.Apply(o.Connection), opts.Redactor.Apply(o.Reason))
	}
	if len(report.Open) > 0 {
		hint := "approve with 'qatlas vault approve'"
		if yes {
			hint = "--yes does not approve; " + hint
		} else if asked {
			hint = "nothing else was approved; " + hint
		}
		fmt.Fprintln(out, hint)
	}
}

// vaultIsEncrypted reports whether the vault exists and is encrypted, locked or not.
func vaultIsEncrypted(opts *Options) bool {
	v, err := vaultOf(opts)
	if err != nil {
		return false
	}
	state, err := v.State()
	return err == nil && (state == vault.StateLocked || state == vault.StateUnlocked)
}

func hasImportedOpen(report manage.ImportApproval) bool {
	for _, o := range report.Open {
		if o.Imported {
			return true
		}
	}
	return false
}

// offerSourceDeletion asks whether to delete the import file after a file with plain-text secrets was
// taken over. Only an explicit answer deletes, never --yes, and only this one path.
func offerSourceDeletion(c *cobra.Command, opts *Options, path string, yes bool, out io.Writer, doc *importDocument) {
	shown := opts.Redactor.Apply(path)
	if yes {
		fmt.Fprintf(out, "%s holds secrets in plain text and was kept; delete it\n", shown)
		return
	}
	confirmed, err := readVaultConfirm(fmt.Sprintf("delete %s, which holds secrets in plain text? [y/N] ", shown))
	if err != nil || !confirmed {
		fmt.Fprintf(out, "%s holds secrets in plain text and was kept; delete it\n", shown)
		return
	}
	if err := os.Remove(path); err != nil {
		fmt.Fprintln(c.ErrOrStderr(), withQatlasPrefix(fmt.Sprintf("%s could not be deleted: %s", shown,
			opts.Redactor.Apply(err.Error()))))
		return
	}
	doc.SourceDeleted = true
	fmt.Fprintf(out, "deleted %s\n", shown)
}

func secretCredentials(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// transferService is the management service export and import run on. Export reads no secret; import writes
// the secrets of a file with them, so it reaches every secret store.
func transferService(opts *Options, reg *capability.Registry) (*manage.Service, error) {
	path, err := config.Path(opts.Config)
	if err != nil {
		return nil, err
	}
	secrets, err := opts.resolver()
	if err != nil {
		return nil, err
	}
	return manage.New(config.NewStore(path, reg), secrets, connlog.SurfaceCLI, opts.Redactor).
		WithVaultProcessSupport(vaultProcessSupported), nil
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
	if errors.Is(err, vault.ErrWrongExportPassphrase) {
		return &UsageError{errors.New("wrong export passphrase; nothing was changed")}
	}
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

func writeImportPlan(out io.Writer, opts *Options, plan *manage.ImportPlan, store string, overSSH bool) {
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
	if len(plan.SecretRoles) > 0 {
		for _, name := range secretCredentials(plan.SecretRoles) {
			fmt.Fprintf(out, "  secret   credential %s brings roles: %s\n", apply(name),
				redactAll(opts.Redactor, plan.SecretRoles[name]))
		}
		if plan.SourceEncrypted {
			fmt.Fprintln(out, "  the file was encrypted; its secrets go to the "+store)
		} else {
			fmt.Fprintln(out, "  warning: the file holds secrets in plain text; its secrets go to the "+store)
		}
		if overSSH {
			fmt.Fprintln(out, "  warning: this is an SSH session, where the system keyring often cannot be "+
				"unlocked; use --secret-store vault if the write fails")
		}
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
	Secrets                  []secretDocument      `json:"secrets"`
	SourceEncrypted          bool                  `json:"source_encrypted"`
	SecretStore              string                `json:"secret_store,omitempty"`
	KeyringOverSSH           bool                  `json:"keyring_over_ssh"`
	Withdrawn                []string              `json:"withdrawn"`
	Approved                 []string              `json:"approved"`
	Open                     []openDocument        `json:"open"`
	SourceDeleted            bool                  `json:"source_deleted"`
	DryRun                   bool                  `json:"dry_run"`
	Imported                 bool                  `json:"imported"`
}

// secretDocument names the roles a credential's secret is brought for, never a value.
type secretDocument struct {
	Credential string   `json:"credential"`
	Roles      []string `json:"roles"`
}

type openDocument struct {
	Connection string `json:"connection"`
	Reason     string `json:"reason"`
	Imported   bool   `json:"imported"`
}

func importDocumentOf(opts *Options, plan *manage.ImportPlan, dryRun bool) importDocument {
	r := opts.Redactor
	doc := importDocument{
		Source: r.Apply(plan.Source), Mode: string(plan.Mode), Entries: []importEntryDocument{},
		CredentialsWithoutSecret: redactEach(r, plan.CredentialsWithoutSecret),
		CredentialsKeepSecret:    redactEach(r, plan.CredentialsKeepSecret), DryRun: dryRun,
		Secrets: []secretDocument{}, SourceEncrypted: plan.SourceEncrypted,
		Withdrawn: []string{}, Approved: []string{}, Open: []openDocument{},
	}
	for _, name := range secretCredentials(plan.SecretRoles) {
		doc.Secrets = append(doc.Secrets, secretDocument{Credential: r.Apply(name), Roles: redactEach(r, plan.SecretRoles[name])})
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
