package vaultmigrate

import (
	"context"
	"errors"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// SuccessorFlag is the hidden flag of 'qatlas vault serve' that starts it as the successor of a running
// vault process, part of the successor contract; see StartSuccessor and HandoverFD.
const SuccessorFlag = "--successor"

// SyncChange hands a change already written to the vault on to the vault process client of v, once one is
// reached: change runs against it, get, set, or delete, whatever the caller just did to the vault itself.
// It is the shared core the CLI's own syncVaultProcess and the TUI's equivalent both run, so 'qatlas
// credential set', 'qatlas credential delete', 'qatlas vault migrate', and the TUI's own role rows, guided
// setup, and migrate action all reach a running process the very same way.
//
// A change that went through is followed by the bindings of v, when it is unlocked in this process, so the
// process also knows the entries it was just handed secrets for.
//
// It reports nil wherever there is nothing to tell: v is nil, its vault has no vault process because it is
// not encrypted, or none is currently running to take the change at all. Any other failure is returned for
// the caller to turn into its own warning, together with secret.VaultProcessRemedy(err), since the vault
// itself already holds the change either way. The caller still checks first whether this platform runs a
// vault process at all (see vaultproc.Supported and the CLI's own vaultProcessSupported test seam), since
// that is a policy this package leaves entirely to its callers.
func SyncChange(ctx context.Context, v *vault.Vault, change func(context.Context, *vaultproc.Client) error) error {
	if v == nil {
		return nil
	}
	client, err := ProcessClientOf(v)
	if errors.Is(err, vault.ErrNotEncrypted) {
		return nil
	}
	if err == nil {
		err = change(ctx, client)
	}
	if err == nil {
		// A secret the process just took may belong to an entry it does not know yet, a forward credential
		// stored after it started among them. It checks every get against the entry ids and approvals of its
		// bindings, so it takes the ones of this process's vault, which the change is already part of. A vault
		// not unlocked in this process has none to hand on.
		if bindings, bindErr := v.Bindings(); bindErr == nil {
			err = client.Bind(ctx, bindings)
		}
	}
	if err == nil || errors.Is(err, vaultproc.ErrNotRunning) {
		return nil
	}
	return err
}

// LockProcess locks the vault process client of v, waiting a moment for its socket to close once it
// answers, so the very next read already finds the vault locked. It is the shared core the CLI's own
// lockVaultProcess and the TUI's equivalent both run ahead of a change after which a still-running process
// would serve what the vault no longer holds, or has no key to check any more: 'qatlas vault passphrase'
// and 'qatlas vault decrypt' retire the identity a process was unlocked with, and the TUI's own change
// passphrase and turn off encryption actions do the same. The change goes ahead either way.
//
// locked is true only once the process actually answered a lock; false with a nil err means there was
// nothing to lock at all (v nil, an unencrypted vault with no process, or none currently running). Any
// other err is for the caller to turn into its own warning, together with secret.EndVaultProcess(err).
func LockProcess(ctx context.Context, v *vault.Vault) (locked bool, err error) {
	if v == nil {
		return false, nil
	}
	client, err := ProcessClientOf(v)
	if err != nil {
		// An unencrypted vault has no vault process, and one whose recipient cannot be read has none this
		// program can reach either.
		return false, nil
	}
	if err := client.Lock(ctx); err != nil {
		if errors.Is(err, vaultproc.ErrNotRunning) {
			return false, nil
		}
		return false, err
	}
	awaitProcessLocked(ctx, client)
	return true, nil
}

// awaitProcessLocked waits briefly for a vault process that just answered a lock to close its socket, which
// it does right after answering. It mirrors the CLI's own awaitLocked (internal/cli/vault.go), kept here as
// its own small copy: both are a handful of lines around client.Status, not worth exporting either just for
// the other to share.
func awaitProcessLocked(ctx context.Context, client *vaultproc.Client) {
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if _, err := client.Status(ctx); errors.Is(err, vaultproc.ErrNotRunning) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}
