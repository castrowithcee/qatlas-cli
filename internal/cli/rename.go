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
	"github.com/castrowithcee/qatlas-cli/internal/manage"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

func newConfigRenameCommand(opts *Options, reg *capability.Registry) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rename",
		Short: "Rename a service or a connection together with every reference to it",
		Long: "Renames one entry of the configuration and rewrites every reference to it in the same write:\n" +
			"a service with the connections that use it, a connection with the defaults that name it. The\n" +
			"entry keeps its id. Each command first lists every place the rename touches: the references it\n" +
			"rewrites, the connection's approval, the agent tokens that name the connection as a vorbild, and\n" +
			"what qatlas cannot change, such as agent calls, scripts, and instructions that name it, and log\n" +
			"entries written before. --dry-run stops there and changes nothing.\n\n" +
			"Without --dry-run the command runs only from an interactive terminal; where the vault is\n" +
			"encrypted it asks for the passphrase there first, like every management command, and then asks to\n" +
			"confirm the rename. No terminal fails with admin-required, a wrong passphrase with usage, both\n" +
			"before anything is touched; an agent never renames. With --dry-run at a terminal the passphrase is\n" +
			"asked too, so the list includes approvals and agent tokens; without a terminal it says the vault\n" +
			"was not read.\n\n" +
			"The configuration file is backed up before it is replaced, and the vault's files before the vault\n" +
			"is written. A renamed connection's approval moves to the new name as it is: an approved connection\n" +
			"stays approved and an open one stays open. An agent token that names the old name as a vorbild no\n" +
			"longer covers anything through it; tokens are never rewritten. A name an agent token names as a\n" +
			"vorbild, or that a stale approval in the vault still holds, is refused. The rename is logged as\n" +
			"one config.connection.rename entry with both names and the connection's id. Should the run stop\n" +
			"before it is complete, the approval left under the other name approves nothing and 'qatlas vault\n" +
			"approve' names it. A service is renamed in the configuration alone: approvals name the provider\n" +
			"and the endpoint, not the service.\n\n" +
			"--output json writes one document with the fields kind, old, new, id, references, approval,\n" +
			"approval_carried, lapsed_tokens, vault_unread, outside, dry_run, renamed, and vault_backups.",
		Args: noArgs,
		RunE: func(c *cobra.Command, _ []string) error { return c.Help() },
	}
	for _, kind := range []struct{ name, short string }{
		{manage.RenameService, "Rename a service and the connections' references to it"},
		{manage.RenameConnection, "Rename a connection, the defaults that name it, and its approval"},
	} {
		var dryRun bool
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
				return runRename(c, opts, reg, kind.name, args[0], args[1], dryRun)
			},
		}
		sub.Flags().BoolVar(&dryRun, "dry-run", false, "list what the rename touches and change nothing")
		cmd.AddCommand(sub)
	}
	return cmd
}

func runRename(c *cobra.Command, opts *Options, reg *capability.Registry, kind, old, name string, dryRun bool) error {
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
	v, err := vaultOf(opts)
	if err != nil {
		return err
	}
	svc := vaultService(opts, config.NewStore(path, reg), v)
	plan, err := svc.PlanRename(kind, old, name, admin)
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
		fmt.Fprintln(c.ErrOrStderr(), "qatlas: rename cancelled, nothing was changed")
		return finishRename(c, asJSON, doc)
	}
	res, err := svc.Rename(contextOrBackground(c.Context()), plan)
	if err != nil {
		return classifyUserError(err)
	}
	for _, warning := range res.Warnings {
		fmt.Fprintln(c.ErrOrStderr(), withQatlasPrefix(warning))
	}
	doc.Renamed, doc.VaultBackups = true, append(doc.VaultBackups, res.VaultCopies...)
	if len(res.VaultCopies) > 0 {
		fmt.Fprintf(out, "the vault files are backed up as %s\n", strings.Join(res.VaultCopies, ", "))
	}
	fmt.Fprintf(out, "renamed %s %s to %s\n", kind, opts.Redactor.Apply(old), opts.Redactor.Apply(name))
	return finishRename(c, asJSON, doc)
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
	question := fmt.Sprintf("rename %s %s to %s", plan.Kind, opts.Redactor.Apply(plan.Old), opts.Redactor.Apply(plan.New))
	if len(plan.LapsedTokens) > 0 {
		question += fmt.Sprintf(", ending the cover of agent %s %s through it", plural(len(plan.LapsedTokens),
			"token", "tokens"), redactAll(opts.Redactor, plan.LapsedTokens))
	}
	return question + "? [y/N] "
}

// writeRenamePlan writes plan as text, one place per line.
func writeRenamePlan(out io.Writer, opts *Options, plan *manage.RenamePlan) {
	apply := opts.Redactor.Apply
	fmt.Fprintf(out, "rename %s %s -> %s\n", plan.Kind, apply(plan.Old), apply(plan.New))
	fmt.Fprintf(out, "  id        %s, kept\n", plan.ID)
	fmt.Fprintf(out, "  entry     %ss.%s -> %ss.%s\n", plan.Kind, apply(plan.Old), plan.Kind, apply(plan.New))
	for _, ref := range plan.References {
		fmt.Fprintf(out, "  rewrites  %s\n", apply(ref))
	}
	if plan.Kind == manage.RenameConnection {
		fmt.Fprintf(out, "  approval  %s\n", renameApprovalText(opts, plan))
	}
	for _, token := range plan.LapsedTokens {
		fmt.Fprintf(out, "  warning   agent token %s names %s as a vorbild and no longer covers anything through it\n",
			apply(token), apply(plan.Old))
	}
	for _, outside := range plan.Outside {
		fmt.Fprintf(out, "  outside   %s, not changed\n", apply(outside))
	}
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

// renameDocument is what 'config rename --output json' writes.
type renameDocument struct {
	Kind            string   `json:"kind"`
	Old             string   `json:"old"`
	New             string   `json:"new"`
	ID              string   `json:"id"`
	References      []string `json:"references"`
	Approval        string   `json:"approval,omitempty"`
	ApprovalCarried bool     `json:"approval_carried"`
	LapsedTokens    []string `json:"lapsed_tokens"`
	VaultUnread     bool     `json:"vault_unread"`
	Outside         []string `json:"outside"`
	DryRun          bool     `json:"dry_run"`
	Renamed         bool     `json:"renamed"`
	VaultBackups    []string `json:"vault_backups"`
}

func renameDocumentOf(opts *Options, plan *manage.RenamePlan, dryRun bool) renameDocument {
	return renameDocument{
		Kind: plan.Kind, Old: opts.Redactor.Apply(plan.Old), New: opts.Redactor.Apply(plan.New), ID: plan.ID,
		References: redactEach(opts.Redactor, plan.References), Approval: plan.Approval,
		ApprovalCarried: plan.CarryApproval, LapsedTokens: redactEach(opts.Redactor, plan.LapsedTokens),
		VaultUnread: plan.VaultUnread, Outside: redactEach(opts.Redactor, plan.Outside), DryRun: dryRun,
		VaultBackups: []string{},
	}
}
