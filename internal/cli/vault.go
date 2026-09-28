package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultmigrate"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// readVaultPassphrase is the terminal prompt every vault passphrase is asked with: the offer of the
// vault's very first secret, 'vault unlock', 'vault encrypt', 'vault passphrase', and 'vault decrypt' all
// go through it. It is a variable only so a test can simulate a terminal's answer, or its absence, without
// opening a real one; a real run always leaves it at vault.ReadPassphrase.
var readVaultPassphrase vault.PassphraseFunc = vault.ReadPassphrase

// readVaultConfirm is the terminal prompt a destructive, non-passphrase question is asked with: 'vault
// decrypt' and 'vault migrate' both go through it before they remove anything. Like readVaultPassphrase it
// is a seam for a test.
var readVaultConfirm = vault.ReadConfirm

// vaultProcessSupported reports whether 'vault unlock' hands the unlocked vault to a vault process on this
// platform, and whether 'vault status' and 'vault lock' ask one. It is a variable only so a test can keep
// a run to this process; a real run always leaves it at vaultProcessPlatform.
var vaultProcessSupported = vaultProcessPlatform

func newVaultCommand(opts *Options, reg *capability.Registry) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vault",
		Short: "Inspect and unlock the vault a credential of type vault reads from",
		Long: "The vault is a directory named vault beside the configuration file, unencrypted or\n" +
			"encrypted to a passphrase. 'qatlas credential set' fills it for a credential of type vault, and\n" +
			"offers a passphrase the first time it stores a secret there. 'vault encrypt', 'vault passphrase',\n" +
			"and 'vault decrypt' set, change, and remove that passphrase; 'vault migrate' carries the entries\n" +
			"of a plaintext credentials.yaml left over from an earlier version into the vault and removes it.\n\n" +
			"'qatlas credential set', 'qatlas credential delete', and every 'vault' command but status,\n" +
			"unlock, and lock manage a credential or the vault, keyring credentials included, and run only from an\n" +
			"interactive terminal; where the vault is encrypted they ask for its passphrase there too, once per\n" +
			"command and before doing anything, whatever credential they target. No terminal at all, or a wrong\n" +
			"passphrase, fails with the code admin-required; an agent never manages a credential or the vault.\n\n" +
			"An encrypted vault also needs its passphrase to answer a read outside such a command, unless a\n" +
			"vault process holds it unlocked, and asks for it on the terminal, never as a command line\n" +
			"argument, an environment variable, or a file. Without a terminal to ask on, such as an agent\n" +
			"talking to qatlas over MCP, such an access fails with the code vault-locked, and 'qatlas\n" +
			"connections' marks the connections it affects; a person runs 'qatlas vault unlock' to open it, or\n" +
			"presses ctrl+l in 'qatlas tui', which unlocks it the same way. An encrypted vault hands a\n" +
			"secret only to a connection it approved as it is configured now; any other access fails with\n" +
			"the code approval-required, which only a person resolves: 'vault approve' lists every open\n" +
			"connection with what changed and releases it, all at once or one at a time with --connection.\n\n" +
			"On Linux 'vault unlock' hands the unlocked vault to a vault process that holds it open until it\n" +
			"is idle for vault.idle_timeout (12h unless the configuration says otherwise), 'vault lock' ends\n" +
			"it, or the machine restarts; elsewhere unlocking only lasts for the current process. ctrl+l in\n" +
			"'qatlas tui' unlocks and locks it the same way. Every later\n" +
			"command and the MCP broker, one started before included, read their secrets from that process\n" +
			"without asking for anything. 'credential set', 'credential delete', and 'vault migrate' hand it\n" +
			"what they changed; 'vault passphrase', 'vault decrypt', and 'qatlas update' lock it first. A\n" +
			"process at the vault's socket that fails the check or does not answer is reported with its\n" +
			"process id and how to end it, never answered by asking for the passphrase instead. No command\n" +
			"ever shows a stored secret back.",
		Args: noArgs,
		RunE: func(c *cobra.Command, _ []string) error { return c.Help() },
	}

	status := &cobra.Command{
		Use:   "status",
		Short: "Show whether the vault exists, is encrypted, and is unlocked",
		Long: "Reports the vault's state (absent, unencrypted, locked, or unlocked), how many credentials it\n" +
			"holds, and how many entries are queued in pending, added or changed while it was locked. The\n" +
			"entry count is unknown while the vault is locked: counting it needs the passphrase, the same\n" +
			"way reading a secret does. For an encrypted vault it also reports whether a vault process holds\n" +
			"it unlocked (process running, with its pid and when it locks itself, or none); for an\n" +
			"unencrypted one it warns that connections are not bound to approvals. It asks for no\n" +
			"passphrase and shows no secret value.",
		Args: noArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runVaultStatus(c, opts)
		},
	}

	unlock := &cobra.Command{
		Use:   "unlock",
		Short: "Unlock the vault, merge its pending entries, and keep it open in a vault process",
		Long: "Asks for the vault's passphrase on the terminal and, once it opens the vault, merges every\n" +
			"entry that was queued in pending while it was locked. On Linux it then starts a vault process\n" +
			"that holds the unlocked vault in memory, detached from the terminal, so it outlives the session\n" +
			"that started it; it locks itself after vault.idle_timeout without a read, on 'qatlas vault lock',\n" +
			"or when the machine restarts. A vault process that is already running is reported, not started a\n" +
			"second time, and nothing is asked. Where systemd-logind would end the process at logout or remove\n" +
			"its socket, a warning names the setting, such as 'loginctl enable-linger'. On other platforms the\n" +
			"vault stays unlocked only for this process. Run against a vault that is not encrypted and locked,\n" +
			"it asks for nothing and just reports the vault's status.",
		Args: noArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runVaultUnlock(c, opts, reg)
		},
	}

	lock := &cobra.Command{
		Use:   "lock",
		Short: "Lock the vault: end the vault process that holds it unlocked",
		Long: "Asks the vault process to overwrite the secrets it holds and end. Without a vault process the\n" +
			"vault is already locked, which is reported as success, so locking twice is no mistake. Locking\n" +
			"only takes access away, so it needs neither a terminal nor the passphrase.",
		Args: noArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runVaultLock(c, opts)
		},
	}

	serve := &cobra.Command{
		Use:    "serve",
		Short:  "Hold the unlocked vault open; started by 'qatlas vault unlock'",
		Hidden: true,
		Args:   noArgs,
		RunE: func(*cobra.Command, []string) error {
			return runVaultServe(opts, reg)
		},
	}

	encrypt := &cobra.Command{
		Use:   "encrypt",
		Short: "Switch the vault's encryption on",
		Long: "Asks for a passphrase on the terminal, typed twice, and encrypts the vault to it: a fresh key\n" +
			"pair is generated, the private key is wrapped with the passphrase as key.age, and whatever the\n" +
			"vault already holds is re-encrypted as secrets.age. The plaintext document is removed only once\n" +
			"the encrypted one is safely written. Run against a vault that is already encrypted, it changes\n" +
			"nothing and says to use 'qatlas vault passphrase' instead; a passphrase left empty, or a mismatch\n" +
			"between the two entries, aborts the same way, without touching any file.\n\n" +
			"From then on the vault hands a secret only to a connection it approved as it is configured, and\n" +
			"fails any other with approval-required. Every connection that reads from the vault at this\n" +
			"moment is approved at once, since the passphrase was just set, and the command names them.",
		Args: noArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runVaultEncrypt(c, opts, reg)
		},
	}

	passphrase := &cobra.Command{
		Use:   "passphrase",
		Short: "Change the vault's passphrase",
		Long: "Asks for the current passphrase once and the new one twice, all on the terminal, and\n" +
			"re-encrypts only key.age with it. Every stored secret, the key pair, and the recipient stay\n" +
			"exactly as they were. A wrong current passphrase, an empty new one, or a mismatch between the\n" +
			"two new entries aborts without touching any file. Run against a vault that is not encrypted, it\n" +
			"refuses and says to use 'qatlas vault encrypt' instead. A vault process that holds the vault\n" +
			"unlocked with the old passphrase is locked once all three entries are made, and the command\n" +
			"says so; 'qatlas vault unlock' opens the vault again.",
		Args: noArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runVaultPassphrase(c, opts)
		},
	}

	var decryptConfirm bool
	decrypt := &cobra.Command{
		Use:   "decrypt",
		Short: "Switch the vault's encryption off",
		Long: "Asks for the current passphrase on the terminal, merges every entry queued in pending first so\n" +
			"nothing is lost, and then writes the document as plain secrets.json and removes key.age,\n" +
			"recipient, and secrets.age. --confirm is required, and a wrong passphrase aborts without\n" +
			"touching any file. Afterwards 'qatlas vault status' warns that the vault is unencrypted, the same\n" +
			"way it does for a vault that was never encrypted at all. Run against a vault that is not\n" +
			"encrypted, it refuses and says there is nothing to decrypt. A vault process that holds the vault\n" +
			"unlocked is locked once the passphrase is entered, since it could not be reached afterwards.",
		Args: noArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runVaultDecrypt(c, opts, decryptConfirm)
		},
	}
	decrypt.Flags().BoolVar(&decryptConfirm, "confirm", false,
		"confirm switching the vault's encryption off; required")

	migrate := &cobra.Command{
		Use:   "migrate",
		Short: "Carry the entries of a plaintext credentials.yaml into the vault",
		Long: "Reads every entry credentials.yaml still holds, regardless of whether it was switched on, and\n" +
			"stores each one in the vault the same way 'qatlas credential set' does: the vault's very first\n" +
			"secret offers a passphrase on the terminal, leaving it empty keeps the vault unencrypted. Managing\n" +
			"an already encrypted vault needs its passphrase up front instead, asked by the admin check every\n" +
			"'vault' command but status, unlock, and lock runs before it does anything.\n\n" +
			"Every credential that had an entry and is still of type keyring is switched to type vault, which\n" +
			"stops it reading the system keyring; every one of its secret roles is therefore resolved once\n" +
			"more, the way the keyring-then-plaintext cascade always did: the keyring first, credentials.yaml\n" +
			"otherwise, so a role held only in the keyring moves too and one held in both keeps the value that\n" +
			"already won. Nothing is ever removed from the keyring. Where the keyring cannot be asked at all,\n" +
			"the command carries on with the plaintext value alone and names the affected credentials in a\n" +
			"warning, never a value.\n\n" +
			"A credential that is not, or no longer, of type keyring — most often one an earlier run already\n" +
			"switched, its deletion question answered no or never asked — is never touched that way: a role it\n" +
			"already holds in the vault, from that earlier run or from 'qatlas credential set', keeps its value,\n" +
			"and only a role still missing is added from credentials.yaml. Checking what the vault already\n" +
			"holds needs it unlocked exactly like any other read, which the admin check already did before\n" +
			"credentials.yaml was even opened; without a terminal the command fails there instead, with the\n" +
			"code admin-required, and with a wrong passphrase with usage, before anything is written.\n\n" +
			"The configuration file is backed up first as config.yaml.bak (mode 0600, written atomically); a\n" +
			"failed backup stops the command before config.yaml is touched. Once every entry is written and,\n" +
			"where the vault was not locked, verified back, deleting credentials.yaml is asked for on the\n" +
			"terminal; without a terminal to ask on the file is kept and the message names this command as the\n" +
			"next step to run again. Run with nothing left to migrate, it says so and changes nothing. Run\n" +
			"again after a partial or a complete migration, it repeats safely: a role already in the vault\n" +
			"keeps its value, and only a role still missing is written. A vault process that holds the vault\n" +
			"unlocked is handed every entry written, the same way 'qatlas credential set' hands it one.",
		Args: noArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runVaultMigrate(c, opts, reg)
		},
	}

	var connectionNames []string
	approve := &cobra.Command{
		Use:   "approve",
		Short: "Release open connections to read the vault credential they are configured for",
		Long: "Lists every connection that is not approved to read its vault credential as it is configured now:\n" +
			"'new connection, not yet approved' for one never approved, or one line per field that changed since,\n" +
			"among origin, provider, permissions, targets, tools, and credential, before and after. Asks for the\n" +
			"vault's passphrase once and then approves every open connection listed, or only the ones named with\n" +
			"--connection (repeatable), and hands the change to a running vault process the way every other vault\n" +
			"change does.\n\n" +
			"An approval a connection no longer belongs to, because it was renamed, removed, or switched away\n" +
			"from the vault, is stale; approving every open connection also removes it and names it. --connection\n" +
			"leaves a stale approval untouched. A name given with --connection that does not read a stored vault\n" +
			"credential at all is refused before anything is approved; one that is already approved as it is\n" +
			"configured now is reported and left alone, nothing written.\n\n" +
			"Run against a vault that is not encrypted, which binds no connection to any approval, it says so and\n" +
			"changes nothing; with nothing open it says so too. Like every 'vault' command but status, unlock,\n" +
			"and lock, it runs only from an interactive terminal and, where the vault is encrypted, asks for its\n" +
			"passphrase there once before doing anything; without a terminal it fails with admin-required, and a\n" +
			"wrong passphrase with usage.",
		Args: noArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runVaultApprove(c, opts, reg, connectionNames)
		},
	}
	approve.Flags().StringArrayVar(&connectionNames, "connection", nil,
		"approve only this connection, repeated per connection; the default approves every open one")

	cmd.AddCommand(status, unlock, lock, serve, encrypt, passphrase, decrypt, migrate, approve)
	return cmd
}

// vaultOf returns the vault this run resolves credentials from. It is nil only for a resolver a test built
// directly with secret.NewWith, which never happens in a real qatlas invocation.
func vaultOf(opts *Options) (*vault.Vault, error) {
	secrets, err := opts.resolver()
	if err != nil {
		return nil, err
	}
	v := secrets.Vault()
	if v == nil {
		return nil, errors.New("no vault is configured for this run")
	}
	return v, nil
}

func runVaultStatus(c *cobra.Command, opts *Options) error {
	v, err := vaultOf(opts)
	if err != nil {
		return err
	}
	status, err := v.Status()
	if err != nil {
		return classifyUserError(err)
	}
	if status.State == vault.StateUnencrypted {
		addWarning(&status.Warning, "while the vault is unencrypted, connections are not bound to approvals: "+
			"any connection reading a vault credential gets its secret")
	}
	if legacyCredentialsExist(v) {
		addWarning(&status.Warning,
			"credentials.yaml still holds plaintext secrets; run 'qatlas vault migrate' to move them into the vault")
	}
	process := vaultProcessOf(c.Context(), v, status.State, &status.Warning)
	return emit(c, opts, vaultStatusObject(status, process))
}

func addWarning(warning *string, text string) {
	if *warning == "" {
		*warning = text
	} else {
		*warning += "; " + text
	}
}

// vaultProcessState is what 'vault status' reports about the vault process.
type vaultProcessState struct {
	// State is running, none, unsupported, or unknown; empty for a vault that is not encrypted, which has
	// no vault process.
	State  string
	Status vaultproc.Status
}

// vaultProcessOf asks the vault process of v how it is. A process that cannot be asked, or does not pass
// the check, is reported as unknown, with the reason added to warning; it never fails the status.
func vaultProcessOf(ctx context.Context, v *vault.Vault, state vault.State, warning *string) vaultProcessState {
	if state != vault.StateLocked && state != vault.StateUnlocked {
		return vaultProcessState{}
	}
	if !vaultProcessSupported {
		return vaultProcessState{State: "unsupported"}
	}
	client, err := vaultProcessClient(v)
	if err == nil {
		var status vaultproc.Status
		status, err = client.Status(contextOrBackground(ctx))
		if err == nil {
			return vaultProcessState{State: "running", Status: status}
		}
	}
	if errors.Is(err, vaultproc.ErrNotRunning) {
		return vaultProcessState{State: "none"}
	}
	addWarning(warning, "the vault process cannot be asked: "+err.Error())
	return vaultProcessState{State: "unknown"}
}

// vaultAccessCheck returns how a listing learns why a connection that reads from the vault cannot use it
// now: the vault is locked, or it has not approved the connection (see secret.Resolver.Usable). A run whose
// resolver cannot be built lists without the state; the invoke that needs a secret reports that problem
// itself.
func vaultAccessCheck(ctx context.Context, opts *Options) func(*config.Resolved) error {
	return func(resolved *config.Resolved) error {
		secrets, err := opts.resolver()
		if err != nil {
			return nil
		}
		return secrets.Usable(contextOrBackground(ctx), resolved)
	}
}

// vaultProcessClient returns the client of the vault process that serves v, checked against v's recipient.
func vaultProcessClient(v *vault.Vault) (*vaultproc.Client, error) {
	return vaultmigrate.ProcessClientOf(v)
}

func contextOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// legacyCredentialsExist reports whether a plaintext credentials.yaml, left over from an earlier version,
// still sits beside the configuration this vault belongs to. It reads no content, so it needs no
// permissions this command does not already have.
func legacyCredentialsExist(v *vault.Vault) bool {
	return fileExists(filepath.Join(filepath.Dir(v.Dir()), secret.FileName))
}

// runVaultUnlock unlocks the vault and, where the platform runs one, hands it to a vault process.
//
// Both ways share their first half, which is the unlock this command always did: the passphrase opens
// the vault in this process and merges the pending entries into secrets.age for good. Where a vault
// process runs, this process then hands it the result through an inherited pipe and ends; elsewhere the
// unlock stays with this process and ends with it. A vault process that fails to start leaves the merge in
// place and reports the failure.
func runVaultUnlock(c *cobra.Command, opts *Options, reg *capability.Registry) error {
	v, err := vaultOf(opts)
	if err != nil {
		return err
	}

	state, err := v.State()
	if err != nil {
		return classifyUserError(err)
	}
	if state != vault.StateLocked {
		// Nothing needs a passphrase: an absent or unencrypted vault has none, and one already unlocked in
		// this process stays that way. Reporting the status is more useful than asking for nothing.
		return runVaultStatus(c, opts)
	}

	ctx := contextOrBackground(c.Context())
	var client *vaultproc.Client
	var configPath string
	if vaultProcessSupported {
		if client, err = vaultProcessClient(v); err != nil {
			return classifyUserError(err)
		}
		// A running process already holds the vault: nothing is asked, and no second one is started.
		status, err := client.Status(ctx)
		if err == nil {
			fmt.Fprintf(c.OutOrStdout(), "the vault is already unlocked in a vault process (pid %d) until %s, "+
				"later when it is read before; 'qatlas vault lock' locks it at once\n",
				status.PID, formatLocksAt(status.LocksAt))
			return nil
		}
		if !errors.Is(err, vaultproc.ErrNotRunning) {
			return err
		}
		// The configuration the process reads its idle timeout from is checked before anything is asked.
		if configPath, err = absConfigPath(opts); err != nil {
			return err
		}
		if _, err := vaultIdleTimeout(configPath, reg); err != nil {
			return err
		}
	}

	passphrase, err := readVaultPassphrase("vault passphrase: ")
	if err != nil {
		if errors.Is(err, vault.ErrNoTerminal) {
			// Same code and exit as a locked read that could not ask for a passphrase either: unlocking
			// the vault itself is exactly that access, just without one particular secret behind it.
			return &secret.VaultLockedError{}
		}
		return err
	}
	merged, err := v.Unlock(passphrase)
	if err != nil {
		if errors.Is(err, vault.ErrWrongPassphrase) {
			return &UsageError{err}
		}
		return err
	}
	mergedText := fmt.Sprintf("merged %d pending %s", merged, plural(merged, "entry", "entries"))
	notice := pendingApprovalNotice(opts, reg, v)

	if !vaultProcessSupported {
		fmt.Fprintf(c.OutOrStdout(), "the vault is unlocked; %s\n", mergedText)
		printNote(c, notice)
		return nil
	}

	snap, err := v.Snapshot()
	if err != nil {
		return err
	}
	status, err := vaultmigrate.StartProcess(ctx, configPath, snap, client)
	if err != nil {
		return fmt.Errorf("%s, but the vault stays locked for later invocations: %w", mergedText, err)
	}
	fmt.Fprintf(c.OutOrStdout(), "the vault is unlocked in a vault process (pid %d) until %s, later when it is "+
		"read before; 'qatlas vault lock' locks it at once; %s\n", status.PID, formatLocksAt(status.LocksAt), mergedText)
	for _, warning := range sessionWarnings(client.Path) {
		fmt.Fprintf(c.ErrOrStderr(), "qatlas: warning: %s\n", warning)
	}
	printNote(c, notice)
	return nil
}

// pendingApprovalNotice reports, right after a locked vault was just unlocked, that it holds connections
// that are not approved as they are configured now, so 'qatlas vault approve' has something to release; ""
// when there is nothing to say, because none is open, the vault holds no approvals to compare with (an
// unencrypted vault, unreachable here since 'vault unlock' only ever asked for a passphrase to reach this
// point), or the configuration cannot be read, which is not this command's own failure to report.
func pendingApprovalNotice(opts *Options, reg *capability.Registry, v *vault.Vault) string {
	path, err := config.Path(opts.Config)
	if err != nil {
		return ""
	}
	cfg, err := config.Load(path, reg)
	if err != nil {
		return ""
	}
	report, err := approval.Pending(cfg, v)
	if err != nil || len(report.Open) == 0 {
		return ""
	}
	return fmt.Sprintf("qatlas: %d %s %s from the vault but %s not approved as configured; "+
		"run 'qatlas vault approve'", len(report.Open), plural(len(report.Open), "connection", "connections"),
		plural(len(report.Open), "reads", "read"), plural(len(report.Open), "is", "are"))
}

// runVaultLock ends the vault process. No process to end is the vault already locked, not a failure.
func runVaultLock(c *cobra.Command, opts *Options) error {
	v, err := vaultOf(opts)
	if err != nil {
		return err
	}
	state, err := v.State()
	if err != nil {
		return classifyUserError(err)
	}
	out := c.OutOrStdout()
	switch {
	case state != vault.StateLocked && state != vault.StateUnlocked:
		fmt.Fprintln(out, "the vault is not encrypted, so there is nothing to lock")
		return nil
	case !vaultProcessSupported:
		fmt.Fprintln(out, "the vault is already locked: no vault process runs on this platform")
		return nil
	}

	client, err := vaultProcessClient(v)
	if err != nil {
		return classifyUserError(err)
	}
	ctx := contextOrBackground(c.Context())
	err = client.Lock(ctx)
	if errors.Is(err, vaultproc.ErrNotRunning) {
		fmt.Fprintln(out, "the vault is already locked: no vault process is running")
		return nil
	}
	if err != nil {
		return err
	}
	awaitLocked(ctx, client)
	fmt.Fprintln(out, "the vault is locked: the vault process overwrote its secrets and ended")
	return nil
}

// awaitLocked waits a moment for a vault process that answered a lock to close its socket, which it does
// right after answering, so the next command already finds the vault locked.
func awaitLocked(ctx context.Context, client *vaultproc.Client) {
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if _, err := client.Status(ctx); errors.Is(err, vaultproc.ErrNotRunning) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// lockVaultProcess locks the vault process that holds v unlocked, ahead of a change after which it would
// serve what the vault no longer holds, or could not be reached to lock at all: an update replaces the
// program it passes for, 'vault decrypt' removes the key a client checks it with, and 'vault passphrase'
// retires the passphrase it was unlocked with. The change goes ahead either way.
//
// It returns what to tell the person once the change is done: that the process was locked, followed by
// next, or that it could not be, with its process id and how to end it; "" when no vault process runs.
func lockVaultProcess(ctx context.Context, v *vault.Vault, why, next string) string {
	if !vaultProcessSupported || v == nil {
		return ""
	}
	locked, err := vaultmigrate.LockProcess(contextOrBackground(ctx), v)
	switch {
	case err != nil:
		return fmt.Sprintf("qatlas: warning: the vault process could not be locked %s: %s; it keeps the "+
			"secrets it holds until it locks itself, unless you %s", why, err, secret.EndVaultProcess(err))
	case locked:
		return fmt.Sprintf("qatlas: the vault process was locked %s; %s", why, next)
	default:
		return ""
	}
}

// lockVaultProcessOf is lockVaultProcess for the vault of this run. A run that has no vault to find, such
// as one whose configuration cannot be located, has no vault process to lock either.
func lockVaultProcessOf(ctx context.Context, opts *Options, why, next string) string {
	v, err := vaultOf(opts)
	if err != nil {
		return ""
	}
	return lockVaultProcess(ctx, v, why, next)
}

// syncVaultProcess hands a change just written to the vault on to the vault process that holds it
// unlocked, so the process keeps answering what the vault holds. The client checks the process, its user
// and its key, before it sends anything, the secret included. Without a vault process there is nothing to
// update. Any other failure is a warning: the vault already holds the change and keeps it.
func syncVaultProcess(c *cobra.Command, v *vault.Vault, change func(context.Context, *vaultproc.Client) error) {
	if !vaultProcessSupported || v == nil {
		return
	}
	if err := vaultmigrate.SyncChange(contextOrBackground(c.Context()), v, change); err != nil {
		fmt.Fprintf(c.ErrOrStderr(), "qatlas: warning: the vault holds the change, but the vault process that "+
			"holds it unlocked could not take it and still answers with what it held before: %s; %s\n",
			err, secret.VaultProcessRemedy(err))
	}
}

// absConfigPath returns the configuration file of this run as an absolute path, the form a vault process
// started in another working directory is handed.
func absConfigPath(opts *Options) (string, error) {
	path, err := config.Path(opts.Config)
	if err != nil {
		return "", err
	}
	return filepath.Abs(path)
}

// vaultIdleTimeout returns vault.idle_timeout of the configuration at path, the default when there is no
// configuration file yet.
func vaultIdleTimeout(path string, reg *capability.Registry) (time.Duration, error) {
	cfg, err := config.Load(path, reg)
	var missing *config.NotFoundError
	if errors.As(err, &missing) {
		return config.DefaultVaultIdleTimeout, nil
	}
	if err != nil {
		return 0, classifyUserError(err)
	}
	return cfg.VaultIdleTimeout(), nil
}

func formatLocksAt(t time.Time) string {
	return t.Local().Format(time.RFC3339)
}

func plural(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

func vaultStatusObject(status vault.Status, process vaultProcessState) output.Object {
	entries := "unknown, locked"
	if status.Entries >= 0 {
		entries = strconv.Itoa(status.Entries)
	}
	fields := []output.Field{
		{Name: "state", Value: string(status.State)},
		{Name: "entries", Value: entries},
		{Name: "pending", Value: int64(status.Pending)},
	}
	if process.State != "" {
		fields = append(fields, output.Field{Name: "process", Value: process.State})
	}
	if process.State == "running" {
		fields = append(fields,
			output.Field{Name: "pid", Value: int64(process.Status.PID)},
			output.Field{Name: "locks_at", Value: formatLocksAt(process.Status.LocksAt)})
	}
	if status.Warning != "" {
		fields = append(fields, output.Field{Name: "warning", Value: status.Warning})
	}
	return output.Object{Fields: fields}
}

// askNewPassphrase asks for a passphrase on the terminal, typed twice, the same confirmation
// offerVaultPassphrase uses for the vault's very first secret. Unlike that offer an empty passphrase is not
// a valid answer here: encrypting the vault or changing its passphrase always needs one.
func askNewPassphrase(prompt string) (string, error) {
	passphrase, err := readVaultPassphrase(prompt)
	if err != nil {
		return "", err
	}
	if passphrase == "" {
		return "", &UsageError{errors.New("a passphrase must not be empty; run the command again")}
	}
	confirm, err := readVaultPassphrase("confirm the passphrase: ")
	if err != nil {
		return "", err
	}
	if confirm != passphrase {
		return "", &UsageError{errors.New("the two passphrases did not match; run the command again")}
	}
	return passphrase, nil
}

func runVaultEncrypt(c *cobra.Command, opts *Options, reg *capability.Registry) error {
	if err := requireAdmin(opts); err != nil {
		return err
	}
	v, err := vaultOf(opts)
	if err != nil {
		return err
	}
	state, err := v.State()
	if err != nil {
		return classifyUserError(err)
	}
	if state == vault.StateLocked || state == vault.StateUnlocked {
		return &UsageError{errors.New(
			"the vault is already encrypted; run 'qatlas vault passphrase' to change its passphrase")}
	}

	passphrase, err := askNewPassphrase("passphrase to encrypt the vault: ")
	if err != nil {
		if errors.Is(err, vault.ErrNoTerminal) {
			return &secret.VaultLockedError{}
		}
		return err
	}

	if err := v.Encrypt(passphrase); err != nil {
		return classifyUserError(err)
	}
	fmt.Fprintln(c.OutOrStdout(), "the vault is encrypted"+approveOnEncrypt(c, opts, reg, v))
	return nil
}

// approveOnEncrypt approves every connection that reads a secret the vault just encrypted, since the
// passphrase was proven a moment ago, and returns what to add to the outcome. From now on the vault hands a
// secret only to a connection it approved as it is configured now. A configuration that cannot be read
// approves nothing; the command says so, and the encryption stands.
func approveOnEncrypt(c *cobra.Command, opts *Options, reg *capability.Registry, v *vault.Vault) string {
	path, err := config.Path(opts.Config)
	var cfg *config.Config
	if err == nil {
		cfg, err = config.Load(path, reg)
	}
	var missing *config.NotFoundError
	switch {
	case errors.As(err, &missing):
		return ""
	case err != nil:
		fmt.Fprintf(c.ErrOrStderr(), "qatlas: warning: no connection was approved to read from the vault, "+
			"because the configuration cannot be read: %s\n", opts.Redactor.Error(err))
		return ""
	}
	approved, warning, err := approval.Approve(contextOrBackground(c.Context()), cfg, v, nil)
	if err != nil {
		fmt.Fprintf(c.ErrOrStderr(), "qatlas: warning: no connection was approved to read from the vault: %s\n",
			opts.Redactor.Error(err))
		return ""
	}
	if warning != "" {
		fmt.Fprintf(c.ErrOrStderr(), "qatlas: warning: %s\n", warning)
	}
	if len(approved) == 0 {
		return ""
	}
	return fmt.Sprintf("; approved %d %s to read from it: %s", len(approved),
		plural(len(approved), "connection", "connections"), strings.Join(approved, ", "))
}

// runVaultApprove lists what changed about every connection the vault has not approved as it is configured
// now and approves it, or only the connections names lists, once the vault's passphrase proved a person is
// asking. Nothing is written before every name in names is checked: an unknown one, or one whose credential
// is not of type vault or has no entry in the vault, refuses the whole run first.
func runVaultApprove(c *cobra.Command, opts *Options, reg *capability.Registry, names []string) error {
	if err := requireAdmin(opts); err != nil {
		return err
	}
	v, err := vaultOf(opts)
	if err != nil {
		return err
	}
	state, err := v.State()
	if err != nil {
		return classifyUserError(err)
	}
	if state != vault.StateLocked && state != vault.StateUnlocked {
		fmt.Fprintln(c.OutOrStdout(),
			"the vault is not encrypted; connections are not bound to approvals, so there is nothing to approve")
		return nil
	}

	path, err := config.Path(opts.Config)
	if err != nil {
		return err
	}
	cfg, err := config.Load(path, reg)
	if err != nil {
		return classifyUserError(err)
	}
	report, err := approval.Pending(cfg, v)
	if err != nil {
		return classifyUserError(err)
	}

	if len(names) == 0 && len(report.Open) == 0 && len(report.Stale) == 0 {
		fmt.Fprintln(c.OutOrStdout(), "nothing is open: every connection is approved as it is configured now")
		return nil
	}

	open := map[string]bool{}
	for _, change := range report.Open {
		open[change.Connection] = true
	}

	// Every requested name is classified before anything is written: an unknown one refuses the whole run,
	// so approving some of a batch and refusing the rest never happens.
	var toApprove, already []string
	if len(names) > 0 {
		seen := map[string]bool{}
		for _, name := range names {
			if seen[name] {
				continue
			}
			seen[name] = true
			if open[name] {
				toApprove = append(toApprove, name)
				continue
			}
			candidate, err := vaultCandidate(cfg, v, name)
			if err != nil {
				return classifyUserError(err)
			}
			if !candidate {
				return &UsageError{fmt.Errorf("connection %s: %w", name, approval.ErrUnknownConnection)}
			}
			already = append(already, name)
		}
	}

	out := c.OutOrStdout()
	if len(report.Open) > 0 {
		fmt.Fprintf(out, "%d %s open:\n", len(report.Open),
			plural(len(report.Open), "connection is", "connections are"))
		for _, change := range report.Open {
			fmt.Fprintln(out, opts.Redactor.Apply(change.Connection))
			if change.New {
				fmt.Fprintln(out, "  new connection, not yet approved")
				continue
			}
			for _, field := range change.Fields {
				fmt.Fprintf(out, "  %s  %s -> %s\n", field.Field,
					opts.Redactor.Apply(field.Before), opts.Redactor.Apply(field.After))
			}
		}
	}

	ctx := contextOrBackground(c.Context())
	if len(names) == 0 || len(toApprove) > 0 {
		approved, warning, err := approval.Approve(ctx, cfg, v, toApprove)
		if err != nil {
			return classifyUserError(err)
		}
		if warning != "" {
			fmt.Fprintf(c.ErrOrStderr(), "qatlas: warning: %s\n", warning)
		}
		if len(approved) > 0 {
			fmt.Fprintf(out, "approved %d %s: %s\n", len(approved),
				plural(len(approved), "connection", "connections"), redactAll(opts.Redactor, approved))
		}
	}
	for _, name := range already {
		fmt.Fprintf(out, "%s is already approved as it is configured now\n", opts.Redactor.Apply(name))
	}

	if len(names) == 0 && len(report.Stale) > 0 {
		removed, warning, err := approval.Revoke(ctx, v, report.Stale)
		if err != nil {
			return classifyUserError(err)
		}
		if warning != "" {
			fmt.Fprintf(c.ErrOrStderr(), "qatlas: warning: %s\n", warning)
		}
		if removed > 0 {
			fmt.Fprintf(out, "removed %d stale %s no longer read from the vault: %s\n", removed,
				plural(removed, "approval", "approvals"), redactAll(opts.Redactor, report.Stale))
		}
	}
	return nil
}

// vaultCandidate reports whether name is a connection of cfg whose credential is of type vault and has an
// entry in the vault unlocked in this process, the same filter approval.Pending applies before it compares a
// connection's scope: what Approve refuses with ErrUnknownConnection when it is asked for a name outside it.
func vaultCandidate(cfg *config.Config, v *vault.Vault, name string) (bool, error) {
	connection, ok := cfg.Connections[name]
	if !ok {
		return false, nil
	}
	if cfg.Credentials[connection.Credential].Type != config.CredentialTypeVault {
		return false, nil
	}
	_, ok, err := v.CredentialID(connection.Credential)
	return ok, err
}

// redactAll joins names with the redactor applied to each, the way a single one is shown.
func redactAll(redactor *redact.Redactor, names []string) string {
	out := make([]string, len(names))
	for i, name := range names {
		out[i] = redactor.Apply(name)
	}
	return strings.Join(out, ", ")
}

func runVaultPassphrase(c *cobra.Command, opts *Options) error {
	// requireAdminTerminal alone: this command's own next line is already the passphrase check requireAdmin
	// would otherwise repeat.
	if err := requireAdminTerminal(); err != nil {
		return err
	}
	v, err := vaultOf(opts)
	if err != nil {
		return err
	}
	state, err := v.State()
	if err != nil {
		return classifyUserError(err)
	}
	if state != vault.StateLocked && state != vault.StateUnlocked {
		return &UsageError{errors.New(
			"the vault is not encrypted; run 'qatlas vault encrypt' to switch encryption on")}
	}

	oldPassphrase, err := readVaultPassphrase("current vault passphrase: ")
	if err != nil {
		if errors.Is(err, vault.ErrNoTerminal) {
			return &secret.VaultLockedError{}
		}
		return err
	}
	newPassphrase, err := askNewPassphrase("new passphrase: ")
	if err != nil {
		return err
	}

	// A passphrase is changed because the old one should no longer open the vault; a vault process
	// unlocked with it would keep the vault open regardless, so it is locked first.
	note := lockVaultProcess(c.Context(), v, "before the passphrase changed",
		"run 'qatlas vault unlock' to unlock the vault again")
	defer printNote(c, note)
	if err := v.ChangePassphrase(oldPassphrase, newPassphrase); err != nil {
		if errors.Is(err, vault.ErrWrongPassphrase) {
			return &UsageError{err}
		}
		return classifyUserError(err)
	}
	fmt.Fprintln(c.OutOrStdout(), "the vault's passphrase is changed")
	return nil
}

// printNote writes what lockVaultProcess had to say to standard error, where it stays apart from the
// command's own output.
func printNote(c *cobra.Command, note string) {
	if note != "" {
		fmt.Fprintln(c.ErrOrStderr(), note)
	}
}

func runVaultDecrypt(c *cobra.Command, opts *Options, confirmed bool) error {
	// requireAdminTerminal alone: this command's own passphrase prompt below is already the check
	// requireAdmin would otherwise repeat.
	if err := requireAdminTerminal(); err != nil {
		return err
	}
	v, err := vaultOf(opts)
	if err != nil {
		return err
	}
	state, err := v.State()
	if err != nil {
		return classifyUserError(err)
	}
	if state != vault.StateLocked && state != vault.StateUnlocked {
		return &UsageError{errors.New("the vault is not encrypted; there is nothing to decrypt")}
	}
	if !confirmed {
		return &UsageError{errors.New("switching the vault's encryption off needs --confirm")}
	}

	passphrase, err := readVaultPassphrase("vault passphrase: ")
	if err != nil {
		if errors.Is(err, vault.ErrNoTerminal) {
			return &secret.VaultLockedError{}
		}
		return err
	}

	// Once the vault is decrypted, its recipient is gone and a vault process could not be checked, nor
	// therefore asked to lock, any more; it would hold the secrets until its idle timeout.
	note := lockVaultProcess(c.Context(), v, "before the vault was decrypted",
		"the unencrypted vault needs no unlocking")
	defer printNote(c, note)
	if err := v.Decrypt(passphrase); err != nil {
		if errors.Is(err, vault.ErrWrongPassphrase) {
			return &UsageError{err}
		}
		return classifyUserError(err)
	}
	fmt.Fprintln(c.OutOrStdout(),
		"the vault is decrypted; 'qatlas vault status' warns until it is encrypted again")
	return nil
}

// runVaultMigrate carries every entry of a plaintext credentials.yaml into the vault and, for every
// credential that had one and is still of type keyring, switches it to type vault in the configuration.
//
// The plan, the write, its verification, and the credential switch are exactly what package vaultmigrate
// runs for the TUI's own migrate action too; only asking for a passphrase, confirming the entries this run
// found, and asking whether to delete credentials.yaml stay here, on this command's own terminal.
//
// vaultmigrate.Write reuses Vault.Set for every entry rather than inventing a locked-vault path of its own:
// an absent vault offers a passphrase exactly like 'qatlas credential set' does, and an encrypted, locked
// vault takes the entry as a pending write that needs no passphrase and is merged in on the next unlock.
// That is the simplest correct choice available, since the vault already has to support exactly that write
// for every other caller, and it keeps a migration from ever demanding a passphrase this command never had
// to ask for otherwise.
//
// A credential switched from keyring to vault stops reading the keyring at all, so a value that used to
// win there, or that lived only there, must move too, or the type change silently changes or loses the
// secret. For every such credential vaultmigrate.Plan therefore resolves every one of its secret roles the
// way the keyring-then-plaintext cascade used to, environment excluded: the keyring first, the plaintext
// file otherwise. A keyring that cannot be asked does not block the migration; it falls back to the
// plaintext value and reports which credentials it could not check, without ever printing a value. Nothing
// is ever deleted from the keyring.
//
// A credential that is not, or no longer, of type keyring — most often one an earlier run already switched
// to vault, its 'delete credentials.yaml?' question answered no or never asked — never has a role
// overwritten by the plaintext file's copy of it: see vaultmigrate.Plan for how an already-held role is
// told apart from one still missing, and what a locked vault does to that check.
func runVaultMigrate(c *cobra.Command, opts *Options, reg *capability.Registry) error {
	if err := requireAdmin(opts); err != nil {
		return err
	}
	path, err := config.Path(opts.Config)
	if err != nil {
		return err
	}
	cfg, err := config.Load(path, reg)
	if err != nil {
		return classifyUserError(err)
	}

	secrets, err := opts.resolver()
	if err != nil {
		return err
	}
	file := secrets.Plaintext()
	if file == nil {
		return errors.New("no plaintext fallback file is configured for this run")
	}
	entries, err := file.All()
	if err != nil {
		return classifyUserError(err)
	}
	if len(entries) == 0 {
		fmt.Fprintln(c.OutOrStdout(), "nothing to migrate: credentials.yaml does not exist or holds no entry")
		return nil
	}

	v := secrets.Vault()
	if v == nil {
		return errors.New("no vault is configured for this run")
	}

	plan, switched, storeUnsureFor, err := vaultmigrate.Plan(cfg, secrets, v, entries, readVaultPassphrase)
	if err != nil {
		switch {
		case errors.Is(err, vault.ErrNoTerminal):
			return &secret.VaultLockedError{}
		case errors.Is(err, vault.ErrWrongPassphrase):
			return &UsageError{err}
		default:
			return classifyUserError(err)
		}
	}

	for _, name := range storeUnsureFor {
		fmt.Fprintf(c.ErrOrStderr(), "qatlas: warning: the system keyring could not be checked for %s; only "+
			"its plaintext credentials.yaml entries were migrated, and a secret held only in the keyring may "+
			"still be there and is not deleted\n", name)
	}

	sync := func(written []vaultmigrate.Entry) {
		syncVaultProcess(c, v, func(ctx context.Context, client *vaultproc.Client) error {
			for _, p := range written {
				if err := client.Set(ctx, p.Name, p.Role, p.Value); err != nil {
					return err
				}
			}
			return nil
		})
	}
	if err := vaultmigrate.Write(v, plan, offerVaultPassphrase, sync); err != nil {
		return classifyUserError(err)
	}

	// The take-over is verified back wherever that needs no passphrase this run never asked for: see
	// vaultmigrate.Verify.
	if err := vaultmigrate.Verify(v, plan); err != nil {
		return classifyUserError(err)
	}

	// Every credential whose entries just moved is switched to type vault. The configuration is backed up
	// first, atomically and at mode 0600; a failed backup stops here, before config.yaml is touched.
	if err := vaultmigrate.SwitchCredentials(config.NewStore(path, reg), cfg, switched); err != nil {
		return classifyUserError(err)
	}

	noun := plural(len(plan), "entry", "entries")
	confirmed, err := readVaultConfirm(fmt.Sprintf(
		"migrated %d %s into the vault; delete %s now? [y/N] ", len(plan), noun, file.Path()))
	if err != nil {
		if errors.Is(err, vault.ErrNoTerminal) {
			fmt.Fprintf(c.OutOrStdout(), "migrated %d %s into the vault; run 'qatlas vault migrate' again in a "+
				"terminal to delete %s, or remove it by hand once nothing else needs it\n", len(plan), noun, file.Path())
			return nil
		}
		return err
	}
	if !confirmed {
		fmt.Fprintf(c.OutOrStdout(), "migrated %d %s into the vault; %s was kept\n", len(plan), noun, file.Path())
		return nil
	}
	if err := os.Remove(file.Path()); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Fprintf(c.OutOrStdout(), "migrated %d %s into the vault and removed %s\n", len(plan), noun, file.Path())
	return nil
}

// backupFile is a thin call site into vaultmigrate.BackupConfig, kept under this name because
// TestBackupFileIsAtomicAndPrivate and TestBackupFileFailureLeavesNoFileBehind call it directly.
func backupFile(path string) error {
	return vaultmigrate.BackupConfig(path)
}
