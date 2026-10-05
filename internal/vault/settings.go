package vault

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"filippo.io/age"
)

// settingsFile holds the vault's own settings, kept apart from secrets.age and tokens.age. It exists only
// while the vault is encrypted and only once a setting was changed; a missing file means every default.
const settingsFile = "settings.age"

// settingsSchemaVersion is the version of the JSON document settings.age holds.
const settingsSchemaVersion = 1

// settingsMACLabel is the HKDF context the key that authenticates settings.age is derived with. Like
// tokens.age it is encrypted to the public recipient, so a check value keyed from the private key is what
// only the vault itself can write. The label differs from the one of tokens and logs, so none of the three
// keys can stand in for another.
const settingsMACLabel = "qatlas vault settings v1"

// UpdateBehaviour says what 'qatlas update' does with a vault process that holds the vault unlocked.
type UpdateBehaviour string

const (
	// UpdateHandover hands the unlocked vault over to the new program.
	UpdateHandover UpdateBehaviour = "handover"
	// UpdateLock locks the vault, the way an update always did.
	UpdateLock UpdateBehaviour = "lock"
)

// ErrSettingsUntrusted reports a settings.age the vault cannot trust: it cannot be decrypted, its check
// value does not match, or its schema or content is unknown. The setting then reads as UpdateLock. It never
// carries any of the file's content.
var ErrSettingsUntrusted = errors.New("settings.age was not written by this vault or is not understood; " +
	"updates lock the vault")

// ParseUpdateBehaviour reads text as an UpdateBehaviour.
func ParseUpdateBehaviour(text string) (UpdateBehaviour, error) {
	switch b := UpdateBehaviour(text); b {
	case UpdateHandover, UpdateLock:
		return b, nil
	}
	return "", fmt.Errorf("the update behaviour is %q or %q", UpdateHandover, UpdateLock)
}

// settingsDocument is the JSON shape settings.age holds. MAC is the check value of UpdateHandover.
type settingsDocument struct {
	Schema         int    `json:"schema"`
	UpdateHandover string `json:"update_handover"`
	MAC            string `json:"mac"`
}

func (v *Vault) settingsPath() string { return filepath.Join(v.dir, settingsFile) }

// settingsMAC is the check value of the update behaviour: the hex HMAC-SHA256, keyed from key with
// settingsMACLabel, of the schema version and the behaviour.
func settingsMAC(key *age.X25519Identity, behaviour string) (string, error) {
	secret := []byte(key.String())
	defer clear(secret)
	macKey, err := hkdf.Key(sha256.New, secret, nil, settingsMACLabel, sha256.Size)
	if err != nil {
		return "", errors.New("cannot derive the settings key")
	}
	defer clear(macKey)
	h := hmac.New(sha256.New, macKey)
	fmt.Fprintf(h, "%d\n%s", settingsSchemaVersion, behaviour)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ReadUpdateBehaviour returns the update behaviour of the encrypted vault in dir, the vault directory itself
// as Dir reports it, read fresh from settings.age with key, its own private key, the way a vault process
// holds it. A missing file is the default, UpdateHandover. A file that cannot be decrypted, fails its check
// value, or has an unknown schema or value reads as UpdateLock with ErrSettingsUntrusted, so the caller can
// warn and still act on the safe value; a file that cannot be read for another reason does the same.
func ReadUpdateBehaviour(dir string, key *age.X25519Identity) (UpdateBehaviour, error) {
	if key == nil {
		return UpdateLock, ErrNotUnlocked
	}
	path := filepath.Join(dir, settingsFile)
	data, err := readFile(path)
	if err != nil {
		if isNotExist(err) {
			return UpdateHandover, nil
		}
		return UpdateLock, ErrSettingsUntrusted
	}
	plain, err := decryptFrom(data, key)
	if err != nil {
		return UpdateLock, ErrSettingsUntrusted
	}
	defer clear(plain)
	var doc settingsDocument
	if err := json.Unmarshal(plain, &doc); err != nil || doc.Schema != settingsSchemaVersion {
		return UpdateLock, ErrSettingsUntrusted
	}
	want, err := settingsMAC(key, doc.UpdateHandover)
	if err != nil || !hmac.Equal([]byte(want), []byte(doc.MAC)) {
		return UpdateLock, ErrSettingsUntrusted
	}
	behaviour, err := ParseUpdateBehaviour(doc.UpdateHandover)
	if err != nil {
		return UpdateLock, ErrSettingsUntrusted
	}
	return behaviour, nil
}

// UpdateBehaviour returns the update behaviour of the vault unlocked in this process, with the semantics of
// ReadUpdateBehaviour. It fails with ErrNotUnlocked or ErrNotEncrypted where there is no setting to read.
func (v *Vault) UpdateBehaviour() (UpdateBehaviour, error) {
	if _, err := v.unlockedDocument(); err != nil {
		return UpdateLock, err
	}
	return ReadUpdateBehaviour(v.dir, v.identity.key)
}

// SetUpdateBehaviour writes behaviour to settings.age of the vault unlocked in this process, authenticated
// for this vault. Whether the caller proved the passphrase is the caller's to check.
func (v *Vault) SetUpdateBehaviour(behaviour UpdateBehaviour) error {
	if _, err := ParseUpdateBehaviour(string(behaviour)); err != nil {
		return err
	}
	if _, err := v.unlockedDocument(); err != nil {
		return err
	}
	mac, err := settingsMAC(v.identity.key, string(behaviour))
	if err != nil {
		return err
	}
	plain, err := json.MarshalIndent(settingsDocument{
		Schema: settingsSchemaVersion, UpdateHandover: string(behaviour), MAC: mac,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot encode %s: %w", v.settingsPath(), err)
	}
	defer clear(plain)
	data, err := encryptTo(string(plain), v.identity.recipient)
	if err != nil {
		return err
	}
	return writeFile(v.settingsPath(), data)
}
