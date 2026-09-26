package vault

import (
	"errors"
	"os"
)

// ErrAlreadyEncrypted reports that Encrypt was called on a vault that already is one.
var ErrAlreadyEncrypted = errors.New("the vault is already encrypted")

// ErrNotEncrypted reports that Decrypt or ChangePassphrase was called on a vault that has no passphrase to
// change or remove.
var ErrNotEncrypted = errors.New("the vault is not encrypted")

// ErrEmptyPassphrase reports a passphrase of "", refused wherever an empty passphrase would silently turn
// encryption off instead of switching it on or changing it.
var ErrEmptyPassphrase = errors.New("a passphrase must not be empty")

// Encrypt turns an absent or unencrypted vault into an encrypted one: it generates a fresh key pair, wraps
// the private key with passphrase, and re-encrypts whatever document already exists, or an empty one for a
// vault that did not exist yet. The plaintext document is removed only once the encrypted one was written
// successfully. Called on a vault that is already encrypted, it changes nothing and reports
// ErrAlreadyEncrypted; 'qatlas vault passphrase' is the way to change the passphrase of one.
func (v *Vault) Encrypt(passphrase string) error {
	if passphrase == "" {
		return ErrEmptyPassphrase
	}
	state, err := v.State()
	if err != nil {
		return err
	}
	switch state {
	case StateLocked, StateUnlocked:
		return ErrAlreadyEncrypted
	}

	doc := newDocument()
	if state == StateUnencrypted {
		doc, err = loadPlainDocument(v.plainPath())
		if err != nil {
			return err
		}
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

	// The plaintext document is removed last, once its encrypted replacement is safely on disk.
	if state == StateUnencrypted {
		if err := os.Remove(v.plainPath()); err != nil && !isNotExist(err) {
			return err
		}
	}

	v.identity, v.doc, v.unlocked = id, doc, true
	return nil
}

// ChangePassphrase re-encrypts the vault's key with a new passphrase, verifying the old one first. Nothing
// else about the vault changes: the key pair, the recipient, and every stored secret stay exactly as they
// were, so it touches only key.age. old is verified fresh against key.age on every call, never from a
// passphrase cached earlier in the process, since a change of passphrase is exactly the moment that cache
// must not be trusted blindly.
func (v *Vault) ChangePassphrase(oldPassphrase, newPassphrase string) error {
	if newPassphrase == "" {
		return ErrEmptyPassphrase
	}
	state, err := v.State()
	if err != nil {
		return err
	}
	if state != StateLocked && state != StateUnlocked {
		return ErrNotEncrypted
	}

	id, err := v.decryptIdentity(oldPassphrase)
	if err != nil {
		return err
	}
	keyData, err := encryptKey(id, newPassphrase)
	if err != nil {
		return err
	}
	if err := writeFile(v.keyPath(), keyData); err != nil {
		return err
	}

	// The key pair itself never changes, only its wrapping, so a process that already had the vault
	// unlocked keeps working with the identity it cached; a process that had not is left exactly as it was.
	if v.unlocked {
		v.identity = id
	}
	return nil
}

// Decrypt turns an encrypted vault back into an unencrypted one, after merging every pending entry so
// nothing queued while it was locked is lost. passphrase is verified fresh against key.age, the same way
// ChangePassphrase verifies old. Called on a vault that is not encrypted, it changes nothing and reports
// ErrNotEncrypted.
func (v *Vault) Decrypt(passphrase string) error {
	state, err := v.State()
	if err != nil {
		return err
	}
	if state != StateLocked && state != StateUnlocked {
		return ErrNotEncrypted
	}

	// unlock verifies the passphrase, decrypts the document, and merges and clears every pending entry; a
	// wrong passphrase fails here before anything is written.
	if _, err := v.unlock(passphrase); err != nil {
		return err
	}

	if err := v.writePlain(v.doc); err != nil {
		return err
	}

	// The encrypted files are removed only once the plaintext document they are replaced by was written
	// successfully.
	for _, path := range []string{v.secretsPath(), v.keyPath(), v.recipientPath()} {
		if err := os.Remove(path); err != nil && !isNotExist(err) {
			return err
		}
	}
	_ = os.Remove(v.pendingPath())

	v.identity, v.doc, v.unlocked = nil, nil, false
	return nil
}

// decryptIdentity reads key.age and recipient fresh from disk and verifies passphrase against them, without
// consulting or changing the process cache. It is how ChangePassphrase confirms the current passphrase
// every time it is called, whether or not this process already had the vault unlocked.
func (v *Vault) decryptIdentity(passphrase string) (*identity, error) {
	keyData, err := readFile(v.keyPath())
	if err != nil {
		return nil, err
	}
	recipientPEM, err := readFile(v.recipientPath())
	if err != nil {
		return nil, err
	}
	return decryptKey(keyData, passphrase, recipientPEM)
}
