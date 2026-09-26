package vault

import (
	"errors"
	"fmt"
	"os"
	"time"
)

// The prompts a PassphraseFunc is asked with. An implementation may show its own text instead; these are
// the default an interactive terminal prompt uses.
const (
	unlockPrompt = "vault passphrase: "
	createPrompt = "passphrase to encrypt the vault, or leave empty to store it unencrypted: "
)

// Get returns the secret of one credential role. It reports the state the vault was in while answering,
// which becomes StateUnlocked once a locked vault was opened to answer it. ask is asked for the passphrase
// only when the vault is encrypted and locked; a nil ask, or one that returns ErrNoTerminal, leaves the
// vault locked and reports that error. found is false when the vault holds nothing for the pair, which is
// not itself an error.
func (v *Vault) Get(name, role string, ask PassphraseFunc) (value string, found bool, state State, err error) {
	state, err = v.State()
	if err != nil {
		return "", false, state, err
	}
	switch state {
	case StateAbsent:
		return "", false, state, nil
	case StateUnencrypted:
		doc, err := loadPlainDocument(v.plainPath())
		if err != nil {
			return "", false, state, err
		}
		value, found = roleOf(doc, name, role)
		return value, found, state, nil
	case StateLocked:
		if err := v.ensureUnlocked(ask); err != nil {
			return "", false, StateLocked, err
		}
		state = StateUnlocked
		fallthrough
	case StateUnlocked:
		value, found = roleOf(v.doc, name, role)
		return value, found, state, nil
	}
	return "", false, state, fmt.Errorf("vault: unreachable state %q", state)
}

func roleOf(doc *document, name, role string) (string, bool) {
	entry, ok := doc.byName(name)
	if !ok {
		return "", false
	}
	value, ok := entry.Roles[role]
	return value, ok
}

// Set stores the secret of one credential role, creating the vault when this is its first secret. offer is
// asked for a passphrase only then; an empty passphrase, or a nil offer, creates the vault unencrypted.
// While the vault is encrypted and locked, Set never needs the passphrase: it writes a pending entry,
// encrypted to the vault's public recipient, which is merged in on the next unlock.
func (v *Vault) Set(name, role, value string, offer PassphraseFunc) error {
	state, err := v.State()
	if err != nil {
		return err
	}
	switch state {
	case StateAbsent:
		passphrase := ""
		if offer != nil {
			passphrase, err = offer(createPrompt)
			if err != nil {
				return err
			}
		}
		return v.create(passphrase, name, role, value)
	case StateUnencrypted:
		doc, err := loadPlainDocument(v.plainPath())
		if err != nil {
			return err
		}
		doc.setRole(name, role, value, time.Now().UTC())
		return v.writePlain(doc)
	case StateLocked:
		return v.addPending(name, role, value)
	case StateUnlocked:
		v.doc.setRole(name, role, value, time.Now().UTC())
		return v.writeEncrypted(v.doc)
	}
	return fmt.Errorf("vault: unreachable state %q", state)
}

// Delete removes one credential role from the vault and reports whether it was there. Deleting from an
// encrypted, locked vault needs the passphrase the same way Get does, because the entry to remove is inside
// secrets.age.
func (v *Vault) Delete(name, role string, ask PassphraseFunc) (bool, error) {
	state, err := v.State()
	if err != nil {
		return false, err
	}
	switch state {
	case StateAbsent:
		return false, nil
	case StateUnencrypted:
		doc, err := loadPlainDocument(v.plainPath())
		if err != nil {
			return false, err
		}
		if !doc.deleteRole(name, role) {
			return false, nil
		}
		return true, v.writePlain(doc)
	case StateLocked:
		if err := v.ensureUnlocked(ask); err != nil {
			return false, err
		}
		fallthrough
	case StateUnlocked:
		if !v.doc.deleteRole(name, role) {
			return false, nil
		}
		return true, v.writeEncrypted(v.doc)
	}
	return false, fmt.Errorf("vault: unreachable state %q", state)
}

// ensureUnlocked opens a locked vault with the passphrase ask supplies, and caches the result. It is a
// no-op once the vault is already unlocked in this process.
func (v *Vault) ensureUnlocked(ask PassphraseFunc) error {
	if v.unlocked {
		return nil
	}
	if ask == nil {
		return ErrNoTerminal
	}
	passphrase, err := ask(unlockPrompt)
	if err != nil {
		return err
	}
	_, err = v.unlock(passphrase)
	return err
}

// Unlock opens an encrypted, locked vault with passphrase, merges every pending entry into it, and caches
// the result for the rest of this process. It reports how many pending entries were merged. Called on a
// vault that is not locked, it does nothing and reports zero: there is nothing to unlock.
func (v *Vault) Unlock(passphrase string) (merged int, err error) {
	state, err := v.State()
	if err != nil {
		return 0, err
	}
	if state != StateLocked {
		return 0, nil
	}
	return v.unlock(passphrase)
}

func (v *Vault) unlock(passphrase string) (int, error) {
	keyData, err := readFile(v.keyPath())
	if err != nil {
		return 0, fmt.Errorf("cannot read %s: %w", v.keyPath(), err)
	}
	recipientPEM, err := readFile(v.recipientPath())
	if err != nil {
		return 0, fmt.Errorf("cannot read %s: %w", v.recipientPath(), err)
	}
	id, err := decryptKey(keyData, passphrase, recipientPEM)
	if err != nil {
		return 0, err
	}

	doc := newDocument()
	if secretsData, err := readFile(v.secretsPath()); err == nil {
		plain, err := decryptFrom(secretsData, id.key)
		if err != nil {
			return 0, fmt.Errorf("cannot decrypt %s: %w", v.secretsPath(), err)
		}
		decoded, err := decodeDocument(v.secretsPath(), plain)
		if err != nil {
			return 0, err
		}
		doc = decoded
	} else if !isNotExist(err) {
		return 0, fmt.Errorf("cannot read %s: %w", v.secretsPath(), err)
	}

	names, err := v.pendingFiles()
	if err != nil {
		return 0, err
	}
	merged, err := v.mergePending(id, doc)
	if err != nil {
		return 0, err
	}
	if merged > 0 {
		if err := v.writeSecrets(id, doc); err != nil {
			return 0, err
		}
		v.removePending(names)
	}

	v.identity, v.doc, v.unlocked = id, doc, true
	return merged, nil
}

// create makes the vault's first entry, and with it the vault itself: unencrypted when passphrase is
// empty, encrypted to a freshly generated key otherwise.
func (v *Vault) create(passphrase, name, role, value string) error {
	doc := newDocument()
	doc.setRole(name, role, value, time.Now().UTC())

	if passphrase == "" {
		return v.writePlain(doc)
	}

	id, err := generateIdentity()
	if err != nil {
		return err
	}
	keyData, err := encryptKey(id, passphrase)
	if err != nil {
		return err
	}
	if err := writeFile(v.recipientPath(), []byte(id.recipient.String()+"\n")); err != nil {
		return err
	}
	if err := writeFile(v.keyPath(), keyData); err != nil {
		return err
	}
	if err := v.writeSecrets(id, doc); err != nil {
		return err
	}
	v.identity, v.doc, v.unlocked = id, doc, true
	return nil
}

func (v *Vault) writePlain(doc *document) error {
	data, err := encodeDocument(doc)
	if err != nil {
		return fmt.Errorf("cannot encode %s: %w", v.plainPath(), err)
	}
	return writeFile(v.plainPath(), data)
}

// writeEncrypted re-encrypts doc for the vault this process already unlocked.
func (v *Vault) writeEncrypted(doc *document) error {
	if v.identity == nil {
		return fmt.Errorf("vault: cannot write %s: the vault was never unlocked in this process", v.secretsPath())
	}
	return v.writeSecrets(v.identity, doc)
}

func (v *Vault) writeSecrets(id *identity, doc *document) error {
	plain, err := encodeDocument(doc)
	if err != nil {
		return fmt.Errorf("cannot encode %s: %w", v.secretsPath(), err)
	}
	data, err := encryptTo(string(plain), id.recipient)
	if err != nil {
		return err
	}
	return writeFile(v.secretsPath(), data)
}

func isNotExist(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}
