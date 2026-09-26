package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
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

func newVaultCommand(opts *Options, reg *capability.Registry) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vault",
		Short: "Inspect and unlock the vault a credential of type vault reads from",
		Long: "The vault is a directory named vault beside the configuration file, unencrypted or\n" +
			"encrypted to a passphrase. 'qatlas credential set' fills it for a credential of type vault, and\n" +
			"offers a passphrase the first time it stores a secret there. 'vault encrypt', 'vault passphrase',\n" +
			"and 'vault decrypt' set, change, and remove that passphrase; 'vault migrate' carries the entries\n" +
			"of a plaintext credentials.yaml left over from an earlier version into the vault and removes it.\n\n" +
			"An encrypted vault needs its passphrase to answer a read, a delete, or 'vault unlock', and asks\n" +
			"for it on the terminal, never as a command line argument, an environment variable, or a file.\n" +
			"Without a terminal to ask on, such as an agent talking to qatlas over MCP, such an access fails\n" +
			"with the code vault-locked; a person runs 'qatlas vault unlock' to open it, or presses ctrl+l in\n" +
			"'qatlas tui'. Storing a secret needs no passphrase, even while the vault is locked: it is queued\n" +
			"and merged in on the next unlock.\n\n" +
			"Unlocking only lasts for the current process: the next qatlas invocation asks again, until a\n" +
			"long-lived vault process exists to hold it open. No command ever shows a stored secret back.",
		Args: noArgs,
		RunE: func(c *cobra.Command, _ []string) error { return c.Help() },
	}

	status := &cobra.Command{
		Use:   "status",
		Short: "Show whether the vault exists, is encrypted, and is unlocked",
		Long: "Reports the vault's state (absent, unencrypted, locked, or unlocked), how many credentials it\n" +
			"holds, and how many entries are queued in pending, added or changed while it was locked. The\n" +
			"entry count is unknown while the vault is locked: counting it needs the passphrase, the same\n" +
			"way reading a secret does. It asks for no passphrase and shows no secret value.",
		Args: noArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runVaultStatus(c, opts)
		},
	}

	unlock := &cobra.Command{
		Use:   "unlock",
		Short: "Unlock the vault for this process and merge its pending entries",
		Long: "Asks for the vault's passphrase on the terminal and, once it opens the vault, merges every\n" +
			"entry that was queued in pending while it was locked. The vault stays unlocked only for this\n" +
			"process: it is asked again on the next qatlas invocation. Run against a vault that is not\n" +
			"encrypted and locked, it asks for nothing and just reports the vault's status.",
		Args: noArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runVaultUnlock(c, opts)
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
			"refuses and says to use 'qatlas vault encrypt' instead.",
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
			"encrypted, it refuses and says there is nothing to decrypt.",
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
			"secret offers a passphrase on the terminal, leaving it empty keeps the vault unencrypted, and an\n" +
			"already encrypted, locked vault takes every entry as a pending write that needs no passphrase and\n" +
			"is merged in on the next unlock.\n\n" +
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
			"holds needs it unlocked exactly like any other read: an encrypted, locked vault asks for the\n" +
			"passphrase on the terminal, and without one to ask on the command fails with the code vault-locked\n" +
			"before anything is written.\n\n" +
			"The configuration file is backed up first as config.yaml.bak (mode 0600, written atomically); a\n" +
			"failed backup stops the command before config.yaml is touched. Once every entry is written and,\n" +
			"where the vault was not locked, verified back, deleting credentials.yaml is asked for on the\n" +
			"terminal; without a terminal to ask on the file is kept and the message names this command as the\n" +
			"next step to run again. Run with nothing left to migrate, it says so and changes nothing. Run\n" +
			"again after a partial or a complete migration, it repeats safely: a role already in the vault\n" +
			"keeps its value, and only a role still missing is written.",
		Args: noArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runVaultMigrate(c, opts, reg)
		},
	}

	cmd.AddCommand(status, unlock, encrypt, passphrase, decrypt, migrate)
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
		hint := "credentials.yaml still holds plaintext secrets; run 'qatlas vault migrate' to move them into the vault"
		if status.Warning == "" {
			status.Warning = hint
		} else {
			status.Warning += "; " + hint
		}
	}
	return emit(c, opts, vaultStatusObject(status))
}

// legacyCredentialsExist reports whether a plaintext credentials.yaml, left over from an earlier version,
// still sits beside the configuration this vault belongs to. It reads no content, so it needs no
// permissions this command does not already have.
func legacyCredentialsExist(v *vault.Vault) bool {
	return fileExists(filepath.Join(filepath.Dir(v.Dir()), secret.FileName))
}

func runVaultUnlock(c *cobra.Command, opts *Options) error {
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

	fmt.Fprintf(c.OutOrStdout(), "the vault is unlocked; merged %d pending %s\n", merged, plural(merged, "entry", "entries"))
	return nil
}

func plural(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

func vaultStatusObject(status vault.Status) output.Object {
	entries := "unknown, locked"
	if status.Entries >= 0 {
		entries = strconv.Itoa(status.Entries)
	}
	fields := []output.Field{
		{Name: "state", Value: string(status.State)},
		{Name: "entries", Value: entries},
		{Name: "pending", Value: int64(status.Pending)},
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

	if err := v.ChangePassphrase(oldPassphrase, newPassphrase); err != nil {
		if errors.Is(err, vault.ErrWrongPassphrase) {
			return &UsageError{err}
		}
		return classifyUserError(err)
	}
	fmt.Fprintln(c.OutOrStdout(), "the vault's passphrase is changed")
	return nil
}

func runVaultDecrypt(c *cobra.Command, opts *Options, confirmed bool) error {
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
