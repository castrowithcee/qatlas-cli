package vault

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"filippo.io/age"
)

// ErrEmptyExportPassphrase reports an empty passphrase for an encrypted export.
var ErrEmptyExportPassphrase = errors.New("the export passphrase is empty")

// ErrWrongExportPassphrase reports a passphrase that does not decrypt the export.
var ErrWrongExportPassphrase = errors.New("the passphrase does not decrypt the export")

// ErrExportTooLarge reports a decrypted export above the limit the caller set.
var ErrExportTooLarge = errors.New("the decrypted export is too large")

// exportMagic starts every binary age file.
const exportMagic = "age-encryption.org/v1\n"

// IsEncryptedExport reports whether data starts like an age file, which is how an encrypted export differs
// from the YAML of a plain one.
func IsEncryptedExport(data []byte) bool {
	return bytes.HasPrefix(data, []byte(exportMagic))
}

// EncryptExport encrypts plaintext with an own passphrase into a standard age file that `age -d` reads. The
// passphrase is that of the export, not of any vault. The work factor is the one of the vault key.
func EncryptExport(plaintext []byte, passphrase string) ([]byte, error) {
	if passphrase == "" {
		return nil, ErrEmptyExportPassphrase
	}
	recipient, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return nil, err
	}
	recipient.SetWorkFactor(scryptLogN)
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, recipient)
	if err != nil {
		return nil, fmt.Errorf("cannot encrypt the export: %w", err)
	}
	if _, err := w.Write(plaintext); err != nil {
		return nil, fmt.Errorf("cannot encrypt the export: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("cannot encrypt the export: %w", err)
	}
	return buf.Bytes(), nil
}

// DecryptExport decrypts a file EncryptExport made. A wrong passphrase is ErrWrongExportPassphrase; more than
// maxPlain bytes of plaintext is ErrExportTooLarge. The work factor the file asks for is capped by age's own
// default limit, so a hostile file cannot make the decryption run for long.
func DecryptExport(data []byte, passphrase string, maxPlain int) ([]byte, error) {
	if passphrase == "" {
		return nil, ErrEmptyExportPassphrase
	}
	id, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return nil, err
	}
	r, err := age.Decrypt(bytes.NewReader(data), id)
	if err != nil {
		if errors.Is(err, age.ErrIncorrectIdentity) {
			return nil, ErrWrongExportPassphrase
		}
		return nil, errors.New("the export cannot be decrypted")
	}
	plain, err := io.ReadAll(io.LimitReader(r, int64(maxPlain)+1))
	if err != nil {
		return nil, errors.New("the export cannot be decrypted")
	}
	if len(plain) > maxPlain {
		return nil, ErrExportTooLarge
	}
	return plain, nil
}
