package vault

import (
	"bytes"
	"errors"
	"fmt"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
)

// ErrNotUnlocked reports a vault that was not unlocked in this process, so there is nothing to take a
// snapshot of.
var ErrNotUnlocked = errors.New("the vault is not unlocked in this process")

// Snapshot is what an unlocked vault holds, handed to the vault process that keeps it open for later
// invocations: the vault's own key and the decrypted secrets, keyed by credential name and then by role.
//
// Its JSON is what a vault process reads from its handover pipe, and a vault process of one release hands it
// to its successor of the next (see vaultmigrate.StartSuccessor): the field names below are a contract
// between consecutive releases, and a change keeps what an older release writes readable.
type Snapshot struct {
	// Identity is the vault's private key as age text. It is as secret as the secrets it decrypts.
	Identity string `json:"identity"`
	// Secrets holds every role of every credential the vault holds.
	Secrets map[string]map[string]string `json:"secrets"`
	// Bindings says which connection may read which credential; see Bindings.
	Bindings Bindings `json:"bindings"`
	// LocksAt is when a vault process handing the vault over to a successor would have locked itself, which
	// the successor keeps; nil for an unlock, after which the idle timeout runs from the start.
	LocksAt *time.Time `json:"locks_at,omitempty"`
}

// Snapshot returns what this process unlocked, pending entries already merged in by Unlock. The maps are
// new; the values in them are the strings the vault already holds, not copies of their bytes. It fails
// with ErrNotUnlocked unless the vault was unlocked, or created encrypted, in this process.
func (v *Vault) Snapshot() (Snapshot, error) {
	if !v.unlocked || v.identity == nil || v.doc == nil {
		return Snapshot{}, ErrNotUnlocked
	}
	secrets := make(map[string]map[string]string, len(v.doc.Entries))
	for _, entry := range v.doc.Entries {
		roles := make(map[string]string, len(entry.Roles))
		for role, value := range entry.Roles {
			roles[role] = value
		}
		secrets[entry.Name] = roles
	}
	return Snapshot{Identity: v.identity.key.String(), Secrets: secrets, Bindings: v.doc.bindings()}, nil
}

// LogKey returns the invocation log key of a vault unlocked in this process, derived from its key (see
// invokelog.DeriveKey), so this process signs the entries it writes itself while no vault process runs. It
// fails with ErrNotUnlocked unless the vault was unlocked, or created encrypted, in this process. The key
// is the caller's to clear.
func (v *Vault) LogKey() (*invokelog.Key, error) {
	if !v.unlocked || v.identity == nil {
		return nil, ErrNotUnlocked
	}
	return invokelog.DeriveKey(v.identity.key)
}

// Recipient returns the public key an encrypted vault encrypts to, as age text, the content of its
// recipient file. It needs no passphrase: the recipient is not secret. It fails with ErrNotEncrypted for a
// vault without one.
func (v *Vault) Recipient() (string, error) {
	if v.identity != nil {
		return v.identity.recipient.String(), nil
	}
	data, err := readFile(v.recipientPath())
	if err != nil {
		if isNotExist(err) {
			return "", ErrNotEncrypted
		}
		return "", fmt.Errorf("cannot read %s: %w", v.recipientPath(), err)
	}
	return string(bytes.TrimSpace(data)), nil
}
