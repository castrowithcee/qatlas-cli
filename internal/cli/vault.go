package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
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
			"presses ctrl+l in 'qatlas tui' to open it for the editor alone.\n\n" +
			"On Linux 'vault unlock' hands the unlocked vault to a vault process that holds it open until it\n" +
			"is idle for vault.idle_timeout (12h unless the configuration says otherwise), 'vault lock' ends\n" +
			"it, or the machine restarts; elsewhere unlocking only lasts for the current process. Every later\n" +
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
			"it unlocked (process running, with its pid and when it locks itself, or none). It asks for no\n" +
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
			"between the two entries, aborts the same way, without touching any file.",
		Args: noArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runVaultEncrypt(c, opts)
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

	cmd.AddCommand(status, unlock, lock, serve, encrypt, passphrase, decrypt, migrate)
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

// vaultLockedCheck returns how a listing learns whether the vault is locked, asked only when it lists a
// connection that reads from the vault. A run whose resolver cannot be built lists without the state; the
// invoke that needs a secret reports that problem itself.
func vaultLockedCheck(ctx context.Context, opts *Options) func() bool {
	return func() bool {
		secrets, err := opts.resolver()
		return err == nil && secrets.VaultLocked(contextOrBackground(ctx))
	}
}

// vaultProcessClient returns the client of the vault process that serves v, checked against v's recipient.
func vaultProcessClient(v *vault.Vault) (*vaultproc.Client, error) {
	recipient, err := v.Recipient()
	if err != nil {
		return nil, err
	}
	path, err := vaultproc.SocketPath(v.Dir())
	if err != nil {
		return nil, err
	}
	return vaultproc.NewClient(path, recipient), nil
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

	if !vaultProcessSupported {
		fmt.Fprintf(c.OutOrStdout(), "the vault is unlocked; %s\n", mergedText)
		return nil
	}

	snap, err := v.Snapshot()
	if err != nil {
		return err
	}
	status, err := startVaultProcess(ctx, configPath, snap, client)
	if err != nil {
		return fmt.Errorf("%s, but the vault stays locked for later invocations: %w", mergedText, err)
	}
	fmt.Fprintf(c.OutOrStdout(), "the vault is unlocked in a vault process (pid %d) until %s, later when it is "+
		"read before; 'qatlas vault lock' locks it at once; %s\n", status.PID, formatLocksAt(status.LocksAt), mergedText)
	for _, warning := range sessionWarnings(client.Path) {
		fmt.Fprintf(c.ErrOrStderr(), "qatlas: warning: %s\n", warning)
	}
	return nil
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
	client, err := vaultProcessClient(v)
	if err != nil {
		// A vault that is not encrypted has no vault process, and one whose recipient cannot be read has
		// none this program can reach either.
		return ""
	}
	ctx = contextOrBackground(ctx)
	switch err := client.Lock(ctx); {
	case errors.Is(err, vaultproc.ErrNotRunning):
		return ""
	case err != nil:
		return fmt.Sprintf("qatlas: warning: the vault process could not be locked %s: %s; it keeps the "+
			"secrets it holds until it locks itself, unless you %s", why, err, secret.EndVaultProcess(err))
	}
	awaitLocked(ctx, client)
	return fmt.Sprintf("qatlas: the vault process was locked %s; %s", why, next)
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
	client, err := vaultProcessClient(v)
	if errors.Is(err, vault.ErrNotEncrypted) {
		return
	}
	if err == nil {
		err = change(contextOrBackground(c.Context()), client)
	}
	if err == nil || errors.Is(err, vaultproc.ErrNotRunning) {
		return
	}
	fmt.Fprintf(c.ErrOrStderr(), "qatlas: warning: the vault holds the change, but the vault process that "+
		"holds it unlocked could not take it and still answers with what it held before: %s; %s\n",
		err, secret.VaultProcessRemedy(err))
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

func runVaultEncrypt(c *cobra.Command, opts *Options) error {
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
	fmt.Fprintln(c.OutOrStdout(), "the vault is encrypted")
	return nil
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

// migrationEntry is one (credential, role, value) triple runVaultMigrate has decided to write into the
// vault, whichever place it came from.
type migrationEntry struct{ name, role, value string }

// runVaultMigrate carries every entry of a plaintext credentials.yaml into the vault and, for every
// credential that had one and is still of type keyring, switches it to type vault in the configuration.
//
// It reuses Vault.Set for every entry rather than inventing a locked-vault path of its own: an absent vault
// offers a passphrase exactly like 'qatlas credential set' does, and an encrypted, locked vault takes the
// entry as a pending write that needs no passphrase and is merged in on the next unlock. That is the
// simplest correct choice available, since the vault already has to support exactly that write for every
// other caller, and it keeps a migration from ever demanding a passphrase this command never had to ask
// for otherwise.
//
// A credential switched from keyring to vault stops reading the keyring at all, so a value that used to
// win there, or that lived only there, must move too, or the type change silently changes or loses the
// secret. For every such credential runVaultMigrate therefore resolves every one of its secret roles the
// way the keyring-then-plaintext cascade used to, environment excluded: the keyring first, the plaintext
// file otherwise. A keyring that cannot be asked does not block the migration; it falls back to the
// plaintext value and reports which credentials it could not check, without ever printing a value. Nothing
// is ever deleted from the keyring.
//
// A credential that is not, or no longer, of type keyring — most often one an earlier run already switched
// to vault, its 'delete credentials.yaml?' question answered no or never asked — never has a role
// overwritten by the plaintext file's copy of it: see planMigration for how an already-held role is told
// apart from one still missing, and what a locked vault does to that check.
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

	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)

	plan, switched, storeUnsureFor, err := planMigration(cfg, secrets, v, names, entries)
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

	for _, p := range plan {
		if err := v.Set(p.name, p.role, p.value, offerVaultPassphrase); err != nil {
			return classifyUserError(err)
		}
	}
	syncVaultProcess(c, v, func(ctx context.Context, client *vaultproc.Client) error {
		for _, p := range plan {
			if err := client.Set(ctx, p.name, p.role, p.value); err != nil {
				return err
			}
		}
		return nil
	})

	// The take-over is verified back wherever that needs no passphrase this run never asked for: a locked
	// vault took every entry as a pending write, unreadable without unlocking it. It checks exactly what
	// was just written, keyring values included, not the plaintext file's own content.
	if state, err := v.State(); err != nil {
		return classifyUserError(err)
	} else if state != vault.StateLocked {
		for _, p := range plan {
			got, found, _, err := v.Get(p.name, p.role, nil)
			if err != nil || !found || got != p.value {
				return fmt.Errorf("the vault does not hold what was just migrated for %s.%s", p.name, p.role)
			}
		}
	}

	// Every credential whose entries just moved is switched to type vault. The configuration is backed up
	// first, atomically and at mode 0600; a failed backup stops here, before config.yaml is touched.
	if len(switched) > 0 {
		if err := backupFile(path); err != nil {
			return err
		}
		for _, name := range switched {
			cred := cfg.Credentials[name]
			cred.Type = config.CredentialTypeVault
			if err := cfg.SetCredential(name, cred); err != nil {
				return classifyUserError(err)
			}
		}
		if err := config.NewStore(path, reg).Save(cfg); err != nil {
			return classifyUserError(err)
		}
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

// planMigration decides, for every credential named in entries, which (role, value) pairs move into the
// vault. It never writes anything itself, so a caller that refuses to proceed after seeing an error has
// changed nothing yet.
//
// A credential that is of type keyring and about to be switched to vault would otherwise lose whatever
// role the keyring alone held, or silently change a role held by both: every one of its secret roles is
// resolved keyring first, plaintext otherwise, the order the cascade already used.
//
// A credential that is not, or no longer, of type keyring — already vault, most often because an earlier
// migrate run switched it and its 'delete credentials.yaml?' question was answered no or never asked, or a
// credential this configuration no longer names at all — changes nothing about how secrets resolve here,
// so only a role the vault does not already hold moves from the plaintext file: an existing vault value,
// keyring-sourced or not, is never overwritten by a repeat of the plaintext file's own copy. Whether the
// vault already holds a role is asked through its own read, the same way any other read does: unlocking an
// encrypted, locked vault over the terminal, and failing with ErrNoTerminal or ErrWrongPassphrase, exactly
// as it would for 'qatlas vault unlock', before this function or its caller writes anything.
//
// It returns the resolved plan, the names actually switched to type vault, and the names whose keyring
// could not be asked for at least one role.
func planMigration(cfg *config.Config, secrets *secret.Resolver, v *vault.Vault, names []string,
	entries map[string]map[string]string) (plan []migrationEntry, switched, storeUnsureFor []string, err error) {
	for _, name := range names {
		legacy := entries[name]
		cred, ok := cfg.Credentials[name]
		if !ok || cred.Type != config.CredentialTypeKeyring {
			for _, role := range sortedRoles(legacy) {
				_, found, _, getErr := v.Get(name, role, readVaultPassphrase)
				if getErr != nil {
					return nil, nil, nil, getErr
				}
				if found {
					// A previous run, or 'qatlas credential set', already put this role in the vault;
					// the plaintext file's copy must not overwrite it.
					continue
				}
				plan = append(plan, migrationEntry{name, role, legacy[role]})
			}
			continue
		}
		switched = append(switched, name)

		unsure := false
		for _, role := range migratableRoles(cfg, cred, legacy) {
			value, state := secrets.StoreValue(context.Background(), name, role)
			switch {
			case state == secret.StoreHolds && value != "":
				plan = append(plan, migrationEntry{name, role, value})
			case state == secret.StoreHolds, state == secret.StoreEmpty:
				if v, ok := legacy[role]; ok {
					plan = append(plan, migrationEntry{name, role, v})
				}
			default:
				unsure = true
				if v, ok := legacy[role]; ok {
					plan = append(plan, migrationEntry{name, role, v})
				}
			}
		}
		if unsure {
			storeUnsureFor = append(storeUnsureFor, name)
		}
	}
	return plan, switched, storeUnsureFor, nil
}

// migratableRoles is every secret role a switched credential's keyring has to be checked for: the roles
// its declared provider defines, every registered role when no provider is named or it names one this
// build does not know, and, either way, every role a legacy plaintext entry already names, so an entry
// under a role the provider metadata does not list is still carried over rather than silently dropped.
func migratableRoles(cfg *config.Config, cred config.Credential, legacy map[string]string) []string {
	roles := map[string]bool{}
	if cred.Provider != "" {
		for _, role := range cfg.ProviderSecretRoles(cred.Provider) {
			roles[role] = true
		}
	}
	if len(roles) == 0 {
		for _, role := range cfg.SecretRoles() {
			roles[role] = true
		}
	}
	for role := range legacy {
		roles[role] = true
	}
	names := make([]string, 0, len(roles))
	for role := range roles {
		names = append(names, role)
	}
	sort.Strings(names)
	return names
}

func sortedRoles(m map[string]string) []string {
	names := make([]string, 0, len(m))
	for role := range m {
		names = append(names, role)
	}
	sort.Strings(names)
	return names
}

// backupFile writes a byte-for-byte copy of the file at path to path+".bak", atomically and at mode 0600,
// the same write-then-rename idiom every other file this module writes uses. A partial or failed backup
// therefore never leaves a truncated or wrongly permissioned copy behind, and an existing .bak is replaced
// outright rather than inheriting whatever mode it happened to have. The caller treats a failure here as
// reason to stop before the file it was meant to protect is touched.
func backupFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot read %s to back it up: %w", path, err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".qatlas-config-bak-*.tmp")
	if err != nil {
		return fmt.Errorf("cannot write a backup of %s: %w", path, err)
	}
	name := tmp.Name()
	moved := false
	defer func() {
		if !moved {
			_ = os.Remove(name)
		}
	}()
	if err := os.Chmod(name, 0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("cannot set the permissions of %s: %w", name, err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("cannot write %s: %w", name, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("cannot write %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("cannot write %s: %w", name, err)
	}
	backup := path + ".bak"
	if err := os.Rename(name, backup); err != nil {
		return fmt.Errorf("cannot replace %s: %w", backup, err)
	}
	moved = true
	return nil
}
