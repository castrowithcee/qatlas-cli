package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/selfupdate"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultmigrate"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

func newUpdateCommand(opts *Options, buildVersion string) *cobra.Command {
	var check, yes bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update qatlas to the latest stable release",
		Long: "Update checks the latest stable GitHub release, verifies the signature of its checksums.txt\n" +
			"(checksums.txt.sig, an SSH signature checked against the release keys built into qatlas)\n" +
			"and the selected archive with its published SHA-256 checksum, and replaces a direct\n" +
			"<prefix>/bin/qatlas installation. A release with an invalid signature is refused and nothing is\n" +
			"changed; a release without a signature is still installed, and the output says signed: false.\n" +
			"It also refreshes qatlas.1 in the same prefix. Dev builds and symlink installations are\n" +
			"not replaced. Use --check to report availability without changing files.\n\n" +
			"Before it downloads anything, update names the new version and what the replacement does to\n" +
			"running qatlas processes and asks for y/n at the terminal; n or an empty answer changes nothing.\n" +
			"--yes answers y. Without a terminal, and in agent mode, update refuses unless --yes is given;\n" +
			"an agent runs it only after the user approved the update, and then with --yes.\n\n" +
			"A vault process that holds the vault unlocked would not serve the new program. When the release\n" +
			"is signed and the update behaviour is handover (the default, see 'qatlas vault handover'), the\n" +
			"vault process verifies the release itself and hands the unlocked vault over to the new program,\n" +
			"so running agents go on without an unlock. In every other case, an unsigned release, the\n" +
			"setting lock, or a handover that cannot be prepared, it is locked right before qatlas is\n" +
			"replaced; 'qatlas vault unlock' unlocks the vault again. The command says which of the two\n" +
			"happened. Should the process not lock, the update goes ahead and the warning names its process\n" +
			"id and how to end it. A release with an invalid signature is refused before anything is touched.",
		Args: noArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			updater := opts.Updater
			if updater == nil {
				updater = selfupdate.New(buildVersion)
			}
			if !check && !yes && (opts.Agent || !checkInteractive()) {
				return &UsageError{errors.New("update replaces the installed program and needs a confirmation: " +
					"run it at a terminal, or pass --yes; an agent runs it only after the user approved the update")}
			}
			result, err := updater.Check(c.Context())
			if err == nil && !check && result.UpdateAvailable {
				if yes || confirmUpdate(result.Latest) {
					result, err = replaceProgram(c, opts, updater)
				} else {
					fmt.Fprintln(c.ErrOrStderr(), "qatlas: update cancelled, nothing was changed")
				}
			}
			if err != nil {
				var unsupported *selfupdate.UnsupportedInstallationError
				if errors.Is(err, selfupdate.ErrDevelopmentBuild) || errors.As(err, &unsupported) {
					return &UsageError{err}
				}
				return err
			}
			return emit(c, opts, output.Object{Fields: []output.Field{
				{Name: "current", Value: result.Current},
				{Name: "latest", Value: result.Latest},
				{Name: "update_available", Value: result.UpdateAvailable},
				{Name: "updated", Value: result.Updated},
				{Name: "signed", Value: result.Signed},
			}})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm the update without asking")
	cmd.Flags().BoolVar(&check, "check", false, "only report whether a newer stable release is available")
	return cmd
}

// replaceProgram installs the release. Once the program is replaced, a running vault process no longer
// passes for it: it refuses every client, a lock included. It is therefore handed over to the new program
// or locked while it still can be, around the replacement, and only here, after the confirmation.
func replaceProgram(c *cobra.Command, opts *Options, updater *selfupdate.Client) (selfupdate.Result, error) {
	replacement := &vaultReplacement{opts: opts}
	result, err := replacement.hooked(updater).Update(c.Context())
	printNote(c, replacement.finish())
	return result, err
}

// vaultReplacement carries a vault process through one installation, for the command and the editor alike.
// With a signed release and the update behaviour handover, the vault process verifies the release and
// prepares its successor before the program is replaced, and hands the unlocked vault over once it is. In
// every other case it is locked before the program is replaced, as an update always did.
type vaultReplacement struct {
	opts     *Options
	handover *vaultproc.Handover
	note     string
}

// hooked returns a copy of base that runs the replacement's hooks, ahead of base's own.
func (r *vaultReplacement) hooked(base *selfupdate.Client) *selfupdate.Client {
	c := *base
	c.BeforeReplace = func(ctx context.Context, release selfupdate.Release) error {
		r.before(ctx, release)
		if base.BeforeReplace != nil {
			return base.BeforeReplace(ctx, release)
		}
		return nil
	}
	c.AfterReplace = func(ctx context.Context, release selfupdate.Release) {
		r.after(ctx)
		if base.AfterReplace != nil {
			base.AfterReplace(ctx, release)
		}
	}
	return &c
}

const unlockAgain = "run 'qatlas vault unlock' to unlock the vault again"

// before prepares the handover, or locks the vault process.
func (r *vaultReplacement) before(ctx context.Context, release selfupdate.Release) {
	r.note, r.handover = "", nil
	if !vaultProcessSupported {
		return
	}
	v, err := vaultOf(r.opts)
	if err != nil || v == nil {
		return
	}
	why := "before qatlas was replaced"
	if !release.Signed {
		why = "because the release is not signed, before qatlas was replaced"
	} else if client, err := vaultmigrate.ProcessClientOf(v); err == nil {
		behaviour, handover, err := client.PrepareHandover(contextOrBackground(ctx), vaultproc.ReleaseFiles{
			Checksums: release.Checksums, Signature: release.Signature, Archive: release.Archive,
			ArchiveName: release.ArchiveName,
		})
		switch {
		case errors.Is(err, vaultproc.ErrNotRunning):
			return
		case errors.Is(err, vaultproc.ErrHandover):
			r.note = "qatlas: the vault process did not hand the vault over and locked itself; " + unlockAgain
			return
		case err == nil && behaviour == vault.UpdateHandover:
			r.handover = handover
			return
		case err == nil:
			// The vault process locked itself, as the update behaviour lock says.
			r.note = "qatlas: the vault process was locked as the update behaviour says, before qatlas was " +
				"replaced; " + unlockAgain
			return
		}
	}
	r.note = lockVaultProcess(ctx, v, why, unlockAgain)
}

// after hands the vault over once the program is replaced.
func (r *vaultReplacement) after(ctx context.Context) {
	handover := r.handover
	if handover == nil {
		return
	}
	r.handover = nil
	pid, err := handover.Commit(contextOrBackground(ctx))
	if err != nil {
		r.note = fmt.Sprintf("qatlas: the vault process could not hand the vault over to the new program "+
			"and locked itself: %s; %s", err, unlockAgain)
		return
	}
	r.note = fmt.Sprintf("qatlas: the vault was handed over to the new program (vault process %d) and stays "+
		"unlocked", pid)
}

// finish ends the replacement and returns what to tell the person. A handover the update did not get to
// commit is ended, which makes the vault process lock itself.
func (r *vaultReplacement) finish() string {
	if r.handover != nil {
		_ = r.handover.Close()
		r.handover = nil
		r.note = "qatlas: the vault process locked itself because the update did not complete; " + unlockAgain
	}
	return r.note
}

// confirmUpdate asks whether to install latest, naming what it does to running processes. A failure to
// read an answer is a no, so nothing is replaced on a guess.
func confirmUpdate(latest string) bool {
	ok, err := readVaultConfirm(selfupdate.Question(latest) + "\nContinue? [y/N] ")
	return err == nil && ok
}
