package vault

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"filippo.io/age"
)

// scryptLogN is the scrypt work factor a new vault key is encrypted with, as 2^scryptLogN iterations. It is
// a variable only so a test can lower it; a real vault leaves it at age's own default, the same order of
// magnitude the age command line tool uses for a passphrase.
var scryptLogN = 18

// identity is the vault's own key pair: an X25519 identity able to decrypt secrets.age and the pending
// entries, and the recipient it decrypts for, which is also the public content of the recipient file.
type identity struct {
	key       *age.X25519Identity
	recipient *age.X25519Recipient
}

// generateIdentity creates a fresh vault key pair. It is called once, when a vault is first encrypted.
func generateIdentity() (*identity, error) {
	key, err := age.GenerateX25519Identity()
	if err != nil {
		return nil, fmt.Errorf("cannot generate a vault key: %w", err)
	}
	return &identity{key: key, recipient: key.Recipient()}, nil
}

// encryptKey wraps the identity's private key with a passphrase, the content of key.age.
func encryptKey(id *identity, passphrase string) ([]byte, error) {
	recipient, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return nil, err
	}
	recipient.SetWorkFactor(scryptLogN)
	return encryptTo(id.key.String(), recipient)
}

// decryptKey recovers the identity from key.age using the passphrase. A wrong passphrase is reported as
// ErrWrongPassphrase, never as the age library's own message, which the vault's messages never quote.
func decryptKey(data []byte, passphrase string, recipientPEM []byte) (*identity, error) {
	scryptIdentity, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return nil, err
	}
	plain, err := decryptFrom(data, scryptIdentity)
	if err != nil {
		if errors.Is(err, age.ErrIncorrectIdentity) {
			return nil, ErrWrongPassphrase
		}
		return nil, err
	}
	key, err := age.ParseX25519Identity(string(bytes.TrimSpace(plain)))
	if err != nil {
		return nil, fmt.Errorf("cannot read the vault key: %w", err)
	}
	recipient, err := age.ParseX25519Recipient(string(bytes.TrimSpace(recipientPEM)))
	if err != nil {
		return nil, fmt.Errorf("cannot read the vault recipient: %w", err)
	}
	if key.Recipient().String() != recipient.String() {
		return nil, errors.New("the vault key and the vault recipient do not match")
	}
	return &identity{key: key, recipient: recipient}, nil
}

// encryptToRecipient wraps plaintext for the recipient text alone, without needing the private key. It is
// how a pending entry is written while the vault is locked.
func encryptToRecipient(plaintext string, recipientPEM []byte) ([]byte, error) {
	recipient, err := age.ParseX25519Recipient(string(bytes.TrimSpace(recipientPEM)))
	if err != nil {
		return nil, fmt.Errorf("cannot read the vault recipient: %w", err)
	}
	return encryptTo(plaintext, recipient)
}

func encryptTo(plaintext string, recipient age.Recipient) ([]byte, error) {
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, recipient)
	if err != nil {
		return nil, fmt.Errorf("cannot encrypt vault data: %w", err)
	}
	if _, err := io.WriteString(w, plaintext); err != nil {
		return nil, fmt.Errorf("cannot encrypt vault data: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("cannot encrypt vault data: %w", err)
	}
	return buf.Bytes(), nil
}

func decryptFrom(data []byte, id age.Identity) ([]byte, error) {
	r, err := age.Decrypt(bytes.NewReader(data), id)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}
