package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/selfupdate"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

func newUpdateCommand(opts *Options, buildVersion string) *cobra.Command {
	var check, yes, list bool
	var to string
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update qatlas to the latest stable release",
		Long: "Update checks the latest stable GitHub release, verifies the signature of its checksums.txt\n" +
			"(checksums.txt.sig, an SSH signature checked against the release keys built into qatlas)\n" +
			"and the selected archive with its published SHA-256 checksum, and replaces a direct\n" +
			"<prefix>/bin/qatlas installation. A release with an invalid signature is refused and nothing is\n" +
			"changed; a release without a signature is still installed, and the output says signed: false.\n" +
			"It also refreshes qatlas.1 in the same prefix. On Windows the installation is\n" +
			"<prefix>\\bin\\qatlas.exe: the running file is renamed to qatlas.exe.old, the new one takes its place\n" +
			"(the old one is put back should that fail), and qatlas.exe.old is removed at the next start or update.\n" +
			"Running 'qatlas mcp', 'qatlas tui' and 'qatlas web' keep running the old version until they are\n" +
			"restarted or reconnected.\n" +
			"At a terminal, update shows how much of the archive has downloaded. A slow download goes on as\n" +
			"long as data arrives; one that receives nothing for 30 seconds ends, and nothing is changed.\n" +
			"Dev builds and symlink installations are not replaced. Use --check to report availability\n" +
			"without changing files.\n\n" +
			"--list shows the releases that can be chosen: the ten newest stable releases, every prerelease newer\n" +
			"than the newest stable one, and the running version, newest first, with prerelease, current, and\n" +
			"older marked. Only signed releases from v0.30.0 on appear. --to <tag> installs exactly one of them,\n" +
			"a prerelease included, with the same confirmation, signature check, and vault handling as above;\n" +
			"the running version is only reported as installed. Installing a version older than the running\n" +
			"one is not available yet and is refused. --list and --to cannot be combined with each other or\n" +
			"with --check.\n\n" +
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
			"id and how to end it. On Windows there is no handover: the vault process is always locked, whatever the\n" +
			"update behaviour says, and the command says so. A release with an invalid signature is refused\n" +
			"before anything is touched.",
		Args: noArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if (list && (to != "" || check || yes)) || (c.Flags().Changed("to") && (to == "" || check)) {
				return &UsageError{errors.New("--list, --to <tag>, and --check cannot be combined, and --to needs a tag; " +
					"--yes only goes with an installation")}
			}
			updater := opts.Updater
			if updater == nil {
				updater = selfupdate.New(buildVersion)
			}
			var result selfupdate.Result
			var err error
			switch {
			case list:
				entries, err := updater.List(c.Context())
				if err != nil {
					return updateError(err)
				}
				return emit(c, opts, listCollection(entries))
			case to != "":
				result, err = updateTo(c, opts, updater, to, yes)
			default:
				if !check && !yes && (opts.Agent || !checkInteractive()) {
					return &UsageError{errors.New(refusal)}
				}
				result, err = updater.Check(c.Context())
				if err == nil && !check && result.UpdateAvailable {
					if yes || confirmUpdate(result.Latest) {
						result, err = replaceProgram(c, opts, updater, func(client *selfupdate.Client) (selfupdate.Result, error) {
							return client.Update(c.Context())
						})
					} else {
						fmt.Fprintln(c.ErrOrStderr(), "qatlas: update cancelled, nothing was changed")
					}
				}
			}
			if err != nil {
				return updateError(err)
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
	cmd.Flags().BoolVar(&list, "list", false, "list the signed releases that can be chosen")
	cmd.Flags().StringVar(&to, "to", "", "install this release, a prerelease included, instead of the latest stable one")
	return cmd
}

const refusal = "update replaces the installed program and needs a confirmation: " +
	"run it at a terminal, or pass --yes; an agent runs it only after the user approved the update"

// updateError makes the errors that come from the request, not from the system, usage errors.
func updateError(err error) error {
	var unsupported *selfupdate.UnsupportedInstallationError
	var target *selfupdate.TargetError
	if errors.Is(err, selfupdate.ErrDevelopmentBuild) || errors.As(err, &unsupported) || errors.As(err, &target) {
		return &UsageError{err}
	}
	return err
}

func listCollection(entries []selfupdate.Entry) output.Collection {
	columns := []string{"version", "prerelease", "current", "older"}
	rows := make([]output.Row, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, output.Row{"version": e.Version, "prerelease": e.Prerelease, "current": e.Current, "older": e.Older})
	}
	return output.Collection{Columns: columns, Rows: rows}
}

// updateTo installs the release named tag after the confirmation. The running version is reported as
// installed without asking and without a change.
func updateTo(c *cobra.Command, opts *Options, updater *selfupdate.Client, tag string, yes bool) (selfupdate.Result, error) {
	target, err := updater.Target(c.Context(), tag)
	if err != nil {
		return selfupdate.Result{}, err
	}
	if target.Current {
		fmt.Fprintf(c.ErrOrStderr(), "qatlas: %s is already installed, nothing was changed\n", tag)
		return updater.UpdateTo(c.Context(), target)
	}
	if !yes && (opts.Agent || !checkInteractive()) {
		return selfupdate.Result{}, &UsageError{errors.New(refusal)}
	}
	if !yes && !confirmTarget(target) {
		fmt.Fprintln(c.ErrOrStderr(), "qatlas: update cancelled, nothing was changed")
		return selfupdate.Result{Current: updater.Version, Latest: tag, UpdateAvailable: true}, nil
	}
	return replaceProgram(c, opts, updater, func(client *selfupdate.Client) (selfupdate.Result, error) {
		return client.UpdateTo(c.Context(), target)
	})
}

// replaceProgram installs the release. Once the program is replaced, a running vault process no longer
// passes for it: it refuses every client, a lock included. It is therefore handed over to the new program
// or locked while it still can be, around the replacement, and only here, after the confirmation.
func replaceProgram(c *cobra.Command, opts *Options, updater *selfupdate.Client,
	install func(*selfupdate.Client) (selfupdate.Result, error)) (selfupdate.Result, error) {
	replacement := &vaultReplacement{opts: opts}
	client := replacement.hooked(updater)
	progress, endProgress := downloadProgress(c, opts)
	if progress != nil {
		client.Progress = progress
	}
	result, err := install(client)
	endProgress()
	printNote(c, replacement.finish())
	return result, err
}

// downloadProgress shows the archive download as a percentage on one stderr line, and only where stderr
// is a terminal: a pipe, a log, or an agent keeps the command's plain output. The second function ends the
// line once anything was shown.
func downloadProgress(c *cobra.Command, opts *Options) (func(done, total int64), func()) {
	f, ok := c.ErrOrStderr().(*os.File)
	if opts.Agent || !ok || !term.IsTerminal(int(f.Fd())) {
		return nil, func() {}
	}
	last, shown := "", false
	return func(done, total int64) {
			line := fmt.Sprintf("qatlas: downloading %.1f MB", float64(done)/(1<<20))
			if total > 0 {
				line = fmt.Sprintf("qatlas: downloading %d%%", done*100/total)
			}
			if line != last {
				last, shown = line, true
				fmt.Fprint(f, "\r"+line)
			}
		}, func() {
			if shown {
				fmt.Fprintln(f)
			}
		}
}

// vaultReplacement carries a vault process through one installation, for the command and the editor alike.
// With a signed release and the update behaviour handover, the vault process verifies the release and
// prepares its successor before the program is replaced, and hands the unlocked vault over once it is. In
// every other case it is locked before the program is replaced, as an update always did; so it is on
// Windows, which has no handover whatever the update behaviour says.
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
	if !vaultHandoverPlatform {
		// Windows has no handover, so neither the update behaviour nor the signature matters here: the vault
		// process is locked.
		why = "because Windows locks the vault process instead of handing it over, before qatlas was replaced"
	} else if !release.Signed {
		why = "because the release is not signed, before qatlas was replaced"
	} else if client, err := vaultproc.ProcessClientOf(v); err == nil {
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
	r.note = lockVaultProcess(ctx, r.opts, v, why, unlockAgain)
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

// confirmTarget is confirmUpdate for a release chosen by version.
func confirmTarget(t selfupdate.Target) bool {
	ok, err := readVaultConfirm(selfupdate.TargetQuestion(t.Version, t.Prerelease) + "\nContinue? [y/N] ")
	return err == nil && ok
}
