// Package vaultmigrate carries the entries of a plaintext credentials.yaml into the vault: the logic
// 'qatlas vault migrate' and the TUI's own migrate action in the vault section both run, so a plan is built
// and written to the vault, verified, and its switched credentials moved to type vault, exactly the same
// way whichever one asks. Only how a passphrase is asked, how an existing entry is verified, and how the
// outcome and the question to delete credentials.yaml are shown differ: the CLI asks on its terminal, the
// TUI through its own masked prompts and screens, and package vaultmigrate never touches either.
package vaultmigrate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// Entry is one (credential, role, value) triple decided to move into the vault.
type Entry struct{ Name, Role, Value string }

// Sync hands the entries Write just stored on to whatever else needs to know, most often a vault process
// that holds the vault unlocked outside this run. Reaching one is platform specific, and for the CLI already
// has a test seam of its own, so Write leaves it entirely to the caller; nil means nothing else needs
// telling.
type Sync func(plan []Entry)

// KeyringStore is what Plan needs to resolve a switched credential's roles the way the keyring-then-plaintext
// cascade always did: the system keyring first, the plaintext file otherwise. *secret.Resolver satisfies it
// already; the TUI's own Secrets interface adds it too, so its editor can plan a migration without a
// resolver, and without internal/cli, of its own.
type KeyringStore interface {
	StoreValue(ctx context.Context, credential, role string) (string, secret.StoreState)
}

// Plan decides, for every credential named in entries (as loaded from a plaintext credentials.yaml), which
// (role, value) pairs move into the vault. It never writes anything itself, so a caller that refuses to
// proceed after seeing an error, or after showing the plan for confirmation, has changed nothing yet.
//
// A credential that is of type keyring and about to be switched to vault would otherwise lose whatever role
// the keyring alone held, or silently change a role held by both: every one of its secret roles is resolved
// keyring first, plaintext otherwise, the order the cascade already used.
//
// A credential that is not, or no longer, of type keyring — most often one an earlier run already switched,
// its 'delete credentials.yaml?' question answered no or never asked — is never touched that way: only a
// role the vault does not already hold moves from the plaintext file; an existing vault value, keyring
// sourced or not, is never overwritten by a repeat of the plaintext file's own copy. Whether the vault
// already holds a role is asked through its own read, the same way vault.Vault.Get always does: readCurrent
// is asked for the vault's passphrase only when an encrypted, locked vault needs it to answer that. A caller
// with no terminal of its own to ask on, such as the TUI, hands in a passphrase it already asked for and
// verified itself, wrapped as a PassphraseFunc that asks nothing more.
//
// It returns the resolved plan, the credential names actually switched to type vault, and the names whose
// keyring could not be asked for at least one role.
func Plan(cfg *config.Config, secrets KeyringStore, v *vault.Vault, entries map[string]map[string]string,
	readCurrent vault.PassphraseFunc) (plan []Entry, switched, storeUnsureFor []string, err error) {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		legacy := entries[name]
		cred, ok := cfg.Credentials[name]
		if !ok || cred.Type != config.CredentialTypeKeyring {
			for _, role := range sortedRoles(legacy) {
				_, found, _, getErr := v.Get(name, role, readCurrent)
				if getErr != nil {
					return nil, nil, nil, getErr
				}
				if found {
					// A previous run, or 'qatlas credential set', already put this role in the vault; the
					// plaintext file's copy must not overwrite it.
					continue
				}
				plan = append(plan, Entry{name, role, legacy[role]})
			}
			continue
		}
		switched = append(switched, name)

		unsure := false
		for _, role := range migratableRoles(cfg, cred, legacy) {
			value, state := secrets.StoreValue(context.Background(), name, role)
			switch {
			case state == secret.StoreHolds && value != "":
				plan = append(plan, Entry{name, role, value})
			case state == secret.StoreHolds, state == secret.StoreEmpty:
				if v, ok := legacy[role]; ok {
					plan = append(plan, Entry{name, role, v})
				}
			default:
				unsure = true
				if v, ok := legacy[role]; ok {
					plan = append(plan, Entry{name, role, v})
				}
			}
		}
		if unsure {
			storeUnsureFor = append(storeUnsureFor, name)
		}
	}
	return plan, switched, storeUnsureFor, nil
}

// migratableRoles is every secret role a switched credential's keyring has to be checked for: the roles its
// declared provider defines, every registered role when no provider is named or it names one this build does
// not know, and, either way, every role a legacy plaintext entry already names, so an entry under a role the
// provider metadata does not list is still carried over rather than silently dropped.
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
	out := make([]string, 0, len(roles))
	for role := range roles {
		out = append(out, role)
	}
	sort.Strings(out)
	return out
}

func sortedRoles(m map[string]string) []string {
	names := make([]string, 0, len(m))
	for role := range m {
		names = append(names, role)
	}
	sort.Strings(names)
	return names
}

// Write stores every entry of plan in the vault, offering a passphrase only for its very first secret (see
// vault.Vault.Set), and calls sync, if not nil, once every entry is written, so a caller can hand the same
// plan on to a vault process, or anything else that needs telling.
func Write(v *vault.Vault, plan []Entry, offer vault.PassphraseFunc, sync Sync) error {
	for _, p := range plan {
		if err := v.Set(p.Name, p.Role, p.Value, offer); err != nil {
			return err
		}
	}
	if sync != nil {
		sync(plan)
	}
	return nil
}

// Verify checks that the vault holds exactly what Write just wrote, wherever that needs no passphrase the
// caller never asked for: a locked vault took every entry as a pending write, unreadable without unlocking
// it, so nothing more can be confirmed there without asking for one.
func Verify(v *vault.Vault, plan []Entry) error {
	state, err := v.State()
	if err != nil {
		return err
	}
	if state == vault.StateLocked {
		return nil
	}
	for _, p := range plan {
		got, found, _, err := v.Get(p.Name, p.Role, nil)
		if err != nil || !found || got != p.Value {
			return fmt.Errorf("the vault does not hold what was just migrated for %s.%s", p.Name, p.Role)
		}
	}
	return nil
}

// SwitchCredentials backs up the configuration store's file first, atomically and at mode 0600 (see
// BackupConfig), and switches every named credential's type to vault in cfg, then saves it through store.
// base is the revision of the file cfg was loaded from (config.Store.LoadVersioned): if the file changed
// since, the result is a *config.ConflictError and the file is left as it is and no backup is made. A failed
// backup stops before the configuration file is touched. Called with no names, it does nothing and leaves
// cfg and the file untouched.
func SwitchCredentials(store *config.Store, cfg *config.Config, base config.Revision, switched []string) error {
	if len(switched) == 0 {
		return nil
	}
	return store.Transact(base, func(save func(*config.Config) error) error {
		if err := BackupConfig(store.Path()); err != nil {
			return err
		}
		for _, name := range switched {
			cred := cfg.Credentials[name]
			cred.Type = config.CredentialTypeVault
			if err := cfg.SetCredential(name, cred); err != nil {
				return err
			}
		}
		return save(cfg)
	})
}

// BackupConfig writes a byte-for-byte copy of the file at path to path+".bak", atomically and at mode 0600,
// the same write-then-rename idiom every other file the configuration package writes uses. A partial or
// failed backup therefore never leaves a truncated or wrongly permissioned copy behind, and an existing .bak
// is replaced outright rather than inheriting whatever mode it happened to have.
func BackupConfig(path string) error {
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
