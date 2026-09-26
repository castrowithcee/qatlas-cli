// Package vault stores credential secrets on disk next to the configuration, either unencrypted or
// encrypted to a passphrase. It is the alternative to the system keyring for a machine that has none, and
// the way a credential of type vault is filled and read.
//
// A vault lives in a directory named vault beside config.yaml. Unencrypted, it holds one file,
// secrets.json, a plain JSON document. Encrypted, it holds three files instead: key.age, the vault's own
// X25519 private key, itself encrypted to a passphrase with age's scrypt recipient; recipient, the
// matching public key as age text; and secrets.age, the document encrypted to that public key. Because
// recipient is public, a new or changed entry can always be added without the passphrase: it is written
// to pending/<id>.age, encrypted to the same public key, and merged into secrets.age the next time the
// vault is unlocked. Reading a value, deleting an entry, and merging pending entries all need the private
// key and therefore the passphrase.
//
// Every file follows the same rule the plaintext credential fallback uses: the vault directory is 0700,
// every file in it 0600, and a write replaces the target atomically through a temporary file in the same
// directory.
package vault

import (
	"errors"
	"os"
	"path/filepath"
)

// DirName is the vault directory beside config.yaml.
const DirName = "vault"

// File and directory modes. A vault holds secrets, encrypted or not, so it is readable by its owner alone.
const (
	fileMode = 0o600
	dirMode  = 0o700
)

// schemaVersion is the version of the JSON document a vault holds, encrypted or not.
const schemaVersion = 1

// Names of the files and the directory a vault is made of.
const (
	keyFile       = "key.age"
	recipientFile = "recipient"
	secretsFile   = "secrets.age"
	plainFile     = "secrets.json"
	pendingDir    = "pending"
)

// State is what a vault currently is, so a caller can act on it without reading its files itself.
type State string

// The states a vault can be in.
const (
	// StateAbsent means no vault directory exists yet; the first Set creates one.
	StateAbsent State = "absent"
	// StateUnencrypted means the vault holds its document as plain JSON, without a passphrase.
	StateUnencrypted State = "unencrypted"
	// StateLocked means the vault is encrypted and its identity has not been unlocked in this process.
	StateLocked State = "locked"
	// StateUnlocked means the vault is encrypted and was unlocked earlier in this process; its document is
	// held in memory for as long as the Vault value lives, and is asked again on the next process.
	StateUnlocked State = "unlocked"
)

// ErrNoTerminal is returned by a PassphraseFunc that could not ask for a passphrase because no terminal is
// attached, for example a headless session or an agent talking to qatlas mcp over stdio.
var ErrNoTerminal = errors.New("no terminal is attached to ask for the vault passphrase")

// ErrWrongPassphrase reports a passphrase that does not unlock the vault's key.
var ErrWrongPassphrase = errors.New("the passphrase does not unlock the vault")

// PassphraseFunc supplies the passphrase an encrypted vault needs to unlock, given the prompt to show. It
// is asked at most once per process: a successful unlock is cached for as long as the Vault value lives.
// It returns ErrNoTerminal when it cannot ask, which a locked read turns into the caller's own vault-locked
// diagnostic. ReadPassphrase is the interactive implementation every real run uses.
type PassphraseFunc func(prompt string) (string, error)

// Vault is one vault directory. The zero value is not usable; use New.
type Vault struct {
	dir string

	// unlocked caches the identity and the decrypted document once this process asked for the passphrase
	// successfully, so several reads and writes of one invocation ask only once.
	unlocked bool
	identity *identity
	doc      *document
}

// New returns the vault below dir, the directory holding config.yaml. The vault does not have to exist.
func New(dir string) *Vault { return &Vault{dir: filepath.Join(dir, DirName)} }

// Dir returns the vault directory.
func (v *Vault) Dir() string { return v.dir }

func (v *Vault) keyPath() string       { return filepath.Join(v.dir, keyFile) }
func (v *Vault) recipientPath() string { return filepath.Join(v.dir, recipientFile) }
func (v *Vault) secretsPath() string   { return filepath.Join(v.dir, secretsFile) }
func (v *Vault) plainPath() string     { return filepath.Join(v.dir, plainFile) }
func (v *Vault) pendingPath() string   { return filepath.Join(v.dir, pendingDir) }

// State reports what the vault currently is. It reads no secret: an encrypted vault is told apart from an
// unencrypted one by which files exist, never by their content.
func (v *Vault) State() (State, error) {
	if v.unlocked {
		return StateUnlocked, nil
	}
	if exists, err := fileExists(v.plainPath()); err != nil {
		return "", err
	} else if exists {
		return StateUnencrypted, nil
	}
	if exists, err := fileExists(v.keyPath()); err != nil {
		return "", err
	} else if exists {
		return StateLocked, nil
	}
	return StateAbsent, nil
}

func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	default:
		return false, err
	}
}
