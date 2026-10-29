package cli

import (
	"errors"
	"fmt"
	"io"
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

func newConfigRenameCommand(opts *Options, reg *capability.Registry) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rename",
		Short: "Rename a service, a connection, or a credential together with every reference to it",
		Long: "Renames one entry of the configuration and rewrites every reference to it in the same write:\n" +
			"a service with the connections that use it, a connection with the defaults that name it, a\n" +
			"credential with the connections that read it or release it as a forward credential. The entry\n" +
			"keeps its id. Each command first lists every place the rename touches: the references it\n" +
			"rewrites, the approvals, the agent tokens whose cover it ends, and what qatlas cannot change,\n" +
			"such as agent calls, scripts, and instructions that name it, and log entries written before.\n" +
			"--dry-run stops there and changes nothing.\n\n" +
			"Without --dry-run the command runs only from an interactive terminal; where the vault is\n" +
			"encrypted it asks for the passphrase there first, like every management command, and then asks to\n" +
			"confirm the rename. No terminal fails with admin-required, a wrong passphrase with usage, both\n" +
			"before anything is touched; an agent never renames. With --dry-run at a terminal the passphrase is\n" +
			"asked too, so the list includes approvals and agent tokens; without a terminal it says the vault\n" +
			"was not read.\n\n" +
			"The configuration file is backed up before it is replaced, and the vault's files before the vault\n" +
			"is written. A renamed connection's approval moves to the new name as it is: an approved connection\n" +
			"stays approved and an open one stays open. An agent token that names the old name as a model no\n" +
			"longer covers anything through it; tokens are never rewritten. A name an agent token names as a\n" +
			"model, or that a stale approval in the vault still holds, is refused. The rename is logged as\n" +
			"one config.connection.rename entry with both names and the connection's id. Should the run stop\n" +
			"before it is complete, the approval left under the other name approves nothing and 'qatlas vault\n" +
			"approve' names it. A service is renamed in the configuration alone: approvals name the provider\n" +
			"and the endpoint, not the service.\n\n" +
			"A credential takes its secrets along: the system keyring's keys of a keyring credential and the\n" +
			"vault entry of a vault credential, with its id and its pending writes. The approval of every\n" +
			"connection that reads it and is approved moves to the new name; an open one stays open. An agent\n" +
			"token whose model's approval the rename does not carry, because it ran without an admin session,\n" +
			"no longer covers anything through it. Environment variables derived from the old name, which\n" +
			"override its stored secrets, are read under the new name from then on; the list names both, and\n" +
			"qatlas changes no environment. A name the vault, the system keyring, or the unencrypted fallback\n" +
			"file already holds secrets under is refused. Should the run stop after the secrets and before the\n" +
			"configuration, 'qatlas vault approve' names what it left; running the same rename again finishes\n" +
			"it, and the same command with --revert puts the secrets and the approvals back under the old name\n" +
			"and removes keyring keys under the new name that copy the old ones, leaving the configuration as it\n" +
			"is.\n\n" +
			"A connection or credential renamed by hand in config.yaml leaves its approvals and secrets under\n" +
			"the old name; 'qatlas config validate' and 'qatlas vault approve' name it when they can read the\n" +
			"vault. Renaming it again the usual way would not fix that. With --adopt the configuration is taken\n" +
			"as it is, with the new name and every reference to it, and what the vault and the system keyring\n" +
			"hold under the old name follows it: a connection's approval, a credential's vault entry and\n" +
			"keyring keys, and the approvals of the connections that read it. <old> is the name they are held\n" +
			"under and must no longer be configured. --adopt runs only in an admin session, since the\n" +
			"configuration alone does not prove who renamed it. Approvals move as they are, valid or open. It\n" +
			"is refused for a service, which nothing binds by name, and together with --revert. With --dry-run\n" +
			"it lists what it would move and changes nothing.\n\n" +
			"--output json writes one document with the fields kind, old, new, id, references, approval,\n" +
			"approval_carried, lapsed_tokens, vault_unread, outside, adopt, dry_run, renamed, and vault_backups;\n" +
			"for a credential also credential, with revert, resumes, keyring_roles, vault_entry, pending_writes,\n" +
			"readers (connection, approval, carried), env_vars (role, old, new), and carried.",
		Args: noArgs,
		RunE: func(c *cobra.Command, _ []string) error { return c.Help() },
	}
	for _, kind := range []struct{ name, short string }{
		{manage.RenameService, "Rename a service and the connections' references to it"},
		{manage.RenameConnection, "Rename a connection, the defaults that name it, and its approval"},
		{manage.RenameCredential, "Rename a credential, its secrets, the connections' references, and their approvals"},
	} {
		var dryRun, revert, adopt bool
		sub := &cobra.Command{
			Use:   kind.name + " <old> <new>",
			Short: kind.short,
			Long:  kind.short + "; see 'qatlas config rename'.",
			Args: func(_ *cobra.Command, args []string) error {
				if len(args) != 2 {
					return newSyntaxError(fmt.Errorf("expected the current and the new %s name, got %d arguments",
						kind.name, len(args)))
				}
				return nil
			},
			RunE: func(c *cobra.Command, args []string) error {
				return runRename(c, opts, reg, kind.name, args[0], args[1], dryRun, revert, adopt)
			},
		}
		sub.Flags().BoolVar(&dryRun, "dry-run", false, "list what the rename touches and change nothing")
		if kind.name == manage.RenameCredential {
			sub.Flags().BoolVar(&revert, "revert", false,
				"put back what a rename from <old> to <new> that stopped early left, instead of finishing it")
		}
		sub.Flags().BoolVar(&adopt, "adopt", false, "take a rename made by hand in config.yaml as it is and "+
			"move the vault's entry, the keyring's keys, and the approvals under the old name after it")
		cmd.AddCommand(sub)
	}
	return cmd
}

func runRename(c *cobra.Command, opts *Options, reg *capability.Registry, kind, old, name string,
	dryRun, revert, adopt bool) error {
	if adopt && revert {
		return newSyntaxError(errors.New("--adopt and --revert cannot be combined"))
	}
	// The admin check is what lets a rename carry approvals over; a dry run without a terminal goes on
	// without it and reads no locked vault.
	admin := !dryRun || checkInteractive()
	if admin {
		if err := requireAdmin(opts); err != nil {
			return err
		}
	}
	path, err := config.Path(opts.Config)
	if err != nil {
		return err
	}
	svc, err := renameService(opts, config.NewStore(path, reg), kind)
	if err != nil {
		return err
	}
	var plan *manage.RenamePlan
	switch {
	case revert:
		plan, err = svc.PlanRevertRename(old, name, admin)
	case adopt:
		plan, err = svc.PlanAdopt(kind, old, name, admin)
	default:
		plan, err = svc.PlanRename(kind, old, name, admin)
	}
	if err != nil {
		var refused *manage.RenameRefusedError
		if errors.As(err, &refused) {
			return &UsageError{err}
		}
		return classifyUserError(err)
	}

	asJSON := c.Flags().Changed("output") && opts.Format == output.FormatJSON
	doc := renameDocumentOf(opts, plan, dryRun)
	out := c.OutOrStdout()
	if asJSON {
		out = io.Discard
	}
	writeRenamePlan(out, opts, plan)
	if dryRun {
		fmt.Fprintln(out, "dry run: nothing was changed")
		return finishRename(c, asJSON, doc)
	}

	confirmed, err := readVaultConfirm(renameQuestion(opts, plan))
	if err != nil {
		if errors.Is(err, vault.ErrNoTerminal) {
			return &UsageError{&application.AdminRequiredError{}}
		}
		return err
	}
	if !confirmed {
		what := "rename"
		if plan.Adopt {
			what = "adopt"
		}
		fmt.Fprintf(c.ErrOrStderr(), "qatlas: %s cancelled, nothing was changed\n", what)
		return finishRename(c, asJSON, doc)
	}
	res, err := svc.Rename(contextOrBackground(c.Context()), plan)
	if err != nil {
		var refused *manage.RenameRefusedError
		if errors.As(err, &refused) {
			return &UsageError{err}
		}
		return classifyUserError(err)
	}
	for _, warning := range res.Warnings {
		fmt.Fprintln(c.ErrOrStderr(), withQatlasPrefix(warning))
	}
	doc.Renamed, doc.VaultBackups = true, append(doc.VaultBackups, res.VaultCopies...)
	if len(res.VaultCopies) > 0 {
		fmt.Fprintf(out, "the vault files are backed up as %s\n", strings.Join(res.VaultCopies, ", "))
	}
	apply := opts.Redactor.Apply
	if doc.Credential != nil {
		doc.Credential.Carried = redactEach(opts.Redactor, res.Carried)
	}
	if plan.Revert {
		if len(res.Carried) > 0 {
			fmt.Fprintf(out, "approvals carried back to %s: %s\n", apply(old), redactAll(opts.Redactor, res.Carried))
		}
		fmt.Fprintf(out, "put credential %s back as it was before the rename to %s\n", apply(old), apply(name))
		return finishRename(c, asJSON, doc)
	}
	if len(res.Carried) > 0 {
		fmt.Fprintf(out, "approvals carried over to %s: %s\n", apply(name), redactAll(opts.Redactor, res.Carried))
	}
	if len(res.EnvVars) > 0 {
		pairs := make([]string, len(res.EnvVars))
		for i, env := range res.EnvVars {
			pairs[i] = apply(env.Old) + " -> " + apply(env.New)
		}
		fmt.Fprintf(out, "environment variables that override the stored secrets are read under the new name: %s; "+
			"qatlas changed no environment\n", strings.Join(pairs, ", "))
	}
	if plan.Adopt {
		fmt.Fprintf(out, "adopted the rename of %s %s to %s\n", kind, apply(old), apply(name))
		return finishRename(c, asJSON, doc)
	}
	fmt.Fprintf(out, "renamed %s %s to %s\n", kind, apply(old), apply(name))
	return finishRename(c, asJSON, doc)
}

// renameService is the management service a rename of kind runs on. A credential's needs every secret store,
// the system keyring among them; any other kind only the vault.
func renameService(opts *Options, store *config.Store, kind string) (*manage.Service, error) {
	if kind == manage.RenameCredential {
		secrets, err := opts.resolver()
		if err != nil {
			return nil, err
		}
		return manage.New(store, secrets, connlog.SurfaceCLI, opts.Redactor).WithVaultProcessSupport(vaultProcessSupported), nil
	}
	v, err := vaultOf(opts)
	if err != nil {
		return nil, err
	}
	return vaultService(opts, store, v), nil
}

func finishRename(c *cobra.Command, asJSON bool, doc renameDocument) error {
	if !asJSON {
		return nil
	}
	return emitDocument(c, output.FormatJSON, doc)
}

// renameQuestion asks to confirm plan, and repeats the cover agent tokens lose with it, since that is the
// one effect a person may not expect of a rename.
func renameQuestion(opts *Options, plan *manage.RenamePlan) string {
	if plan.Revert {
		return fmt.Sprintf("put credential %s back as it was before the rename to %s? [y/N] ",
			opts.Redactor.Apply(plan.Old), opts.Redactor.Apply(plan.New))
	}
	verb := "rename"
	if plan.Adopt {
		verb = "adopt the rename of"
	}
	question := fmt.Sprintf("%s %s %s to %s", verb, plan.Kind, opts.Redactor.Apply(plan.Old),
		opts.Redactor.Apply(plan.New))
	if len(plan.LapsedTokens) > 0 {
		question += fmt.Sprintf(", ending the cover of agent %s %s through it", plural(len(plan.LapsedTokens),
			"token", "tokens"), redactAll(opts.Redactor, plan.LapsedTokens))
	}
	return question + "? [y/N] "
}

// writeRenamePlan writes plan as text, one place per line.
func writeRenamePlan(out io.Writer, opts *Options, plan *manage.RenamePlan) {
	apply := opts.Redactor.Apply
	if plan.Revert {
		writeRevertPlan(out, opts, plan)
		return
	}
	if plan.Adopt {
		fmt.Fprintf(out, "adopt the rename of %s %s -> %s, made by hand in the configuration\n", plan.Kind,
			apply(plan.Old), apply(plan.New))
		fmt.Fprintf(out, "  id        %s, kept\n", plan.ID)
		fmt.Fprintf(out, "  config    %ss.%s is there already; written as it is\n", plan.Kind, apply(plan.New))
		for _, ref := range plan.References {
			fmt.Fprintf(out, "  changed   %s, by hand\n", apply(ref))
		}
	} else {
		fmt.Fprintf(out, "rename %s %s -> %s\n", plan.Kind, apply(plan.Old), apply(plan.New))
		fmt.Fprintf(out, "  id        %s, kept\n", plan.ID)
		fmt.Fprintf(out, "  entry     %ss.%s -> %ss.%s\n", plan.Kind, apply(plan.Old), plan.Kind, apply(plan.New))
		for _, ref := range plan.References {
			fmt.Fprintf(out, "  rewrites  %s\n", apply(ref))
		}
	}
	switch plan.Kind {
	case manage.RenameConnection:
		fmt.Fprintf(out, "  approval  %s\n", renameApprovalText(opts, plan))
	case manage.RenameCredential:
		writeCredentialPlan(out, opts, plan)
	}
	for _, token := range plan.LapsedTokens {
		if plan.Kind == manage.RenameCredential {
			fmt.Fprintf(out, "  warning   agent token %s has a model whose approval of %s is not carried over; "+
				"it no longer covers anything through it\n", apply(token), apply(plan.Old))
			continue
		}
		fmt.Fprintf(out, "  warning   agent token %s names %s as a model and no longer covers anything through it\n",
			apply(token), apply(plan.Old))
	}
	for _, outside := range plan.Outside {
		fmt.Fprintf(out, "  outside   %s, not changed\n", apply(outside))
	}
}

// writeCredentialPlan writes what a credential's rename changes besides the configuration.
func writeCredentialPlan(out io.Writer, opts *Options, plan *manage.RenamePlan) {
	apply := opts.Redactor.Apply
	if plan.Resumes {
		fmt.Fprintf(out, "  resumes   a rename to %s that stopped before the configuration was saved; what it "+
			"already moved and carried over stays\n", apply(plan.New))
	}
	for _, role := range plan.KeyringRoles {
		fmt.Fprintf(out, "  keyring   %s/%s -> %s/%s\n", apply(plan.Old), role, apply(plan.New), role)
	}
	switch {
	case plan.VaultUnread:
		fmt.Fprintln(out, "  vault     not read: the vault is locked and this run has no terminal to ask for its passphrase")
	case plan.VaultEntry:
		fmt.Fprintf(out, "  vault     entry %s -> %s, keeping its id and its secrets\n", apply(plan.Old), apply(plan.New))
	}
	if plan.PendingWrites > 0 {
		fmt.Fprintf(out, "  pending   %d pending %s for %s merged into the entry first\n", plan.PendingWrites,
			plural(plan.PendingWrites, "write", "writes"), apply(plan.Old))
	}
	for _, reader := range plan.Readers {
		text := renameReaderText(opts, reader)
		if plan.VaultUnread {
			text = "approval not read"
		}
		fmt.Fprintf(out, "  reader    %s: %s\n", apply(reader.Connection), text)
	}
	for _, env := range plan.EnvVars {
		fmt.Fprintf(out, "  env       %s -> %s (%s)\n", apply(env.Old), apply(env.New), env.Role)
	}
}

// writeRevertPlan writes what putting back a credential's stopped rename changes.
func writeRevertPlan(out io.Writer, opts *Options, plan *manage.RenamePlan) {
	apply := opts.Redactor.Apply
	fmt.Fprintf(out, "revert the stopped rename of credential %s -> %s\n", apply(plan.Old), apply(plan.New))
	fmt.Fprintf(out, "  id        %s\n", plan.ID)
	switch {
	case plan.VaultUnread:
		fmt.Fprintln(out, "  vault     not read: the vault is locked and this run has no terminal to ask for its passphrase")
	case plan.VaultEntry:
		fmt.Fprintf(out, "  vault     entry %s -> %s, keeping its id and its secrets\n", apply(plan.New), apply(plan.Old))
	}
	if plan.PendingWrites > 0 {
		fmt.Fprintf(out, "  pending   %d pending %s for %s merged into the entry first\n", plan.PendingWrites,
			plural(plan.PendingWrites, "write", "writes"), apply(plan.New))
	}
	for _, role := range plan.KeyringRoles {
		fmt.Fprintf(out, "  keyring   removes %s/%s, a copy of %s/%s\n", apply(plan.New), role, apply(plan.Old), role)
	}
	for _, reader := range plan.Readers {
		text := "no approval in the vault"
		switch {
		case reader.Carried:
			text = "approval carried back to " + apply(plan.Old)
		case plan.VaultUnread:
			text = "approval not read"
		case reader.Approval == manage.ApprovalValid:
			text = "approved; stays"
		case reader.Approval == manage.ApprovalOpen:
			text = "open; stays open until 'qatlas vault approve'"
		}
		fmt.Fprintf(out, "  reader    %s: %s\n", apply(reader.Connection), text)
	}
	fmt.Fprintln(out, "  config    not changed")
}

func renameReaderText(opts *Options, reader manage.RenameReader) string {
	name := opts.Redactor.Apply(reader.Connection)
	switch {
	case reader.Approval == "":
		return "no approval to carry over"
	case reader.Carried:
		return "approved; carried over, so " + name + " stays approved"
	case reader.Approval == manage.ApprovalOpen:
		return "open; stays open until 'qatlas vault approve'"
	}
	return "approved; not carried over, so " + name + " needs 'qatlas vault approve'"
}

func renameApprovalText(opts *Options, plan *manage.RenamePlan) string {
	name := opts.Redactor.Apply(plan.New)
	switch {
	case plan.VaultUnread:
		return "not read: the vault is locked and this run has no terminal to ask for its passphrase"
	case plan.Approval == "":
		return "none to carry over"
	case plan.CarryApproval && plan.Approval == manage.ApprovalOpen:
		return "open; carried over, so " + name + " stays open until 'qatlas vault approve'"
	case plan.CarryApproval:
		return "approved; carried over, so " + name + " stays approved"
	}
	return plan.Approval + "; not carried over, so " + name + " needs 'qatlas vault approve'"
}

// renameDocument is what 'config rename --output json' writes. Credential is there only for a credential.
type renameDocument struct {
	Kind            string                    `json:"kind"`
	Old             string                    `json:"old"`
	New             string                    `json:"new"`
	ID              string                    `json:"id"`
	References      []string                  `json:"references"`
	Approval        string                    `json:"approval,omitempty"`
	ApprovalCarried bool                      `json:"approval_carried"`
	LapsedTokens    []string                  `json:"lapsed_tokens"`
	VaultUnread     bool                      `json:"vault_unread"`
	Outside         []string                  `json:"outside"`
	Adopt           bool                      `json:"adopt"`
	DryRun          bool                      `json:"dry_run"`
	Renamed         bool                      `json:"renamed"`
	VaultBackups    []string                  `json:"vault_backups"`
	Credential      *credentialRenameDocument `json:"credential,omitempty"`
}

// credentialRenameDocument is what a credential's rename changes besides the configuration: revert and
// resumes say the plan puts back or finishes a rename that stopped early, carried names the connections
// whose approvals were carried over, or back, once it ran.
type credentialRenameDocument struct {
	Revert        bool                   `json:"revert"`
	Resumes       bool                   `json:"resumes"`
	KeyringRoles  []string               `json:"keyring_roles"`
	VaultEntry    bool                   `json:"vault_entry"`
	PendingWrites int                    `json:"pending_writes"`
	Readers       []renameReaderDocument `json:"readers"`
	EnvVars       []envRenameDocument    `json:"env_vars"`
	Carried       []string               `json:"carried"`
}

type renameReaderDocument struct {
	Connection string `json:"connection"`
	Approval   string `json:"approval,omitempty"`
	Carried    bool   `json:"carried"`
}

type envRenameDocument struct {
	Role string `json:"role"`
	Old  string `json:"old"`
	New  string `json:"new"`
}

func renameDocumentOf(opts *Options, plan *manage.RenamePlan, dryRun bool) renameDocument {
	doc := renameDocument{
		Kind: plan.Kind, Old: opts.Redactor.Apply(plan.Old), New: opts.Redactor.Apply(plan.New), ID: plan.ID,
		References: redactEach(opts.Redactor, plan.References), Approval: plan.Approval,
		ApprovalCarried: plan.CarryApproval, LapsedTokens: redactEach(opts.Redactor, plan.LapsedTokens),
		VaultUnread: plan.VaultUnread, Outside: redactEach(opts.Redactor, plan.Outside), Adopt: plan.Adopt,
		DryRun: dryRun, VaultBackups: []string{},
	}
	if plan.Kind != manage.RenameCredential {
		return doc
	}
	cred := &credentialRenameDocument{
		Revert: plan.Revert, Resumes: plan.Resumes, KeyringRoles: append([]string{}, plan.KeyringRoles...),
		VaultEntry: plan.VaultEntry, PendingWrites: plan.PendingWrites, Readers: []renameReaderDocument{},
		EnvVars: []envRenameDocument{}, Carried: []string{},
	}
	for _, reader := range plan.Readers {
		cred.Readers = append(cred.Readers, renameReaderDocument{
			Connection: opts.Redactor.Apply(reader.Connection), Approval: reader.Approval, Carried: reader.Carried})
	}
	for _, env := range plan.EnvVars {
		cred.EnvVars = append(cred.EnvVars, envRenameDocument{
			Role: env.Role, Old: opts.Redactor.Apply(env.Old), New: opts.Redactor.Apply(env.New)})
	}
	doc.Credential = cred
	return doc
}
