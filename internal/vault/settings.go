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
	"time"

	"filippo.io/age"
)

// settingsFile holds the vault's own settings, kept apart from secrets.age and tokens.age. It exists only
// while the vault is encrypted and only once a setting was changed; a missing file means every default.
const settingsFile = "settings.age"

// settingsSchemaVersion is the version of the JSON document settings.age is written with. Version 1 held
// only the update behaviour and still reads.
const settingsSchemaVersion = 2

// settingsMACLabel is the HKDF context the key that authenticates settings.age is derived with. Like
// tokens.age it is encrypted to the public recipient, so a check value keyed from the private key is what
// only the vault itself can write. The label differs from the one of tokens and logs, so none of the three
// keys can stand in for another. The schema version is part of the authenticated input, so every schema
// shares the label without one's check value standing in for another's.
const settingsMACLabel = "qatlas vault settings v1"

// Defaults of the vault settings, used while settings.age does not exist.
const (
	DefaultIdleTimeout   = 12 * time.Hour
	DefaultAdminTimeout  = 10 * time.Minute
	DefaultRetentionDays = 90
)

// Settings are the vault's own settings, authenticated as a whole in settings.age.
type Settings struct {
	// Update is what 'qatlas update' does with an unlocked vault process.
	Update UpdateBehaviour
	// IdleTimeout is the idle time after which the vault locks itself. It is positive.
	IdleTimeout time.Duration
	// AdminTimeout is how long a proven passphrase authorises managing actions. Zero asks for the
	// passphrase on every managing action.
	AdminTimeout time.Duration
	// RetentionDays is how many days of log history are kept. Zero means never truncate and occurs only in
	// SafeSettings: it is never a valid value to store and never means "use the default".
	RetentionDays int
}

// DefaultSettings are the settings of a vault without settings.age.
func DefaultSettings() Settings {
	return Settings{
		Update: UpdateHandover, IdleTimeout: DefaultIdleTimeout,
		AdminTimeout: DefaultAdminTimeout, RetentionDays: DefaultRetentionDays,
	}
}

// SafeSettings are the settings in force while settings.age cannot be trusted: updates lock the vault,
// the idle timeout is the default, every managing action asks for the passphrase and no log is truncated.
func SafeSettings() Settings {
	return Settings{Update: UpdateLock, IdleTimeout: DefaultIdleTimeout}
}

// Validate reports why s cannot be stored. There is no upper or lower bound beyond the sign.
func (s Settings) Validate() error {
	if _, err := ParseUpdateBehaviour(string(s.Update)); err != nil {
		return err
	}
	if s.IdleTimeout <= 0 {
		return errors.New("the idle timeout must be positive")
	}
	if s.AdminTimeout < 0 {
		return errors.New("the admin session timeout must not be negative")
	}
	if s.RetentionDays <= 0 {
		return errors.New("the log retention must be a positive number of days")
	}
	return nil
}

// UpdateBehaviour says what 'qatlas update' does with a vault process that holds the vault unlocked.
type UpdateBehaviour string

const (
	// UpdateHandover hands the unlocked vault over to the new program.
	UpdateHandover UpdateBehaviour = "handover"
	// UpdateLock locks the vault, the way an update always did.
	UpdateLock UpdateBehaviour = "lock"
)

// ErrSettingsUntrusted reports a settings.age the vault cannot trust: it cannot be decrypted, its check
// value does not match, or its schema or content is unknown. The settings then read as SafeSettings. It never
// carries any of the file's content.
var ErrSettingsUntrusted = errors.New("settings.age was not written by this vault or is not understood; " +
	"the safe settings apply: updates lock the vault, managing actions ask for the passphrase and no log is truncated")

// ParseUpdateBehaviour reads text as an UpdateBehaviour.
func ParseUpdateBehaviour(text string) (UpdateBehaviour, error) {
	switch b := UpdateBehaviour(text); b {
	case UpdateHandover, UpdateLock:
		return b, nil
	}
	return "", fmt.Errorf("the update behaviour is %q or %q", UpdateHandover, UpdateLock)
}

// settingsDocument is the JSON shape settings.age holds. MAC is the check value of every other field.
// Schema 1 has only UpdateHandover.
type settingsDocument struct {
	Schema         int    `json:"schema"`
	UpdateHandover string `json:"update_handover"`
	IdleTimeoutNS  int64  `json:"idle_timeout_ns"`
	AdminTimeoutNS int64  `json:"admin_timeout_ns"`
	RetentionDays  int    `json:"retention_days"`
	MAC            string `json:"mac"`
}

func (v *Vault) settingsPath() string { return filepath.Join(v.dir, settingsFile) }

// settingsMAC is the check value of a document: the hex HMAC-SHA256, keyed from key with settingsMACLabel,
// of the schema version and its fields, one per line in fixed order with numbers in decimal. Schema 1 is
// the version and the behaviour. Callers pass only a behaviour that parses, so it holds no line break.
func settingsMAC(key *age.X25519Identity, doc settingsDocument) (string, error) {
	secret := []byte(key.String())
	defer clear(secret)
	macKey, err := hkdf.Key(sha256.New, secret, nil, settingsMACLabel, sha256.Size)
	if err != nil {
		return "", errors.New("cannot derive the settings key")
	}
	defer clear(macKey)
	h := hmac.New(sha256.New, macKey)
	switch doc.Schema {
	case 1:
		fmt.Fprintf(h, "1\n%s", doc.UpdateHandover)
	case 2:
		fmt.Fprintf(h, "2\n%s\n%d\n%d\n%d", doc.UpdateHandover, doc.IdleTimeoutNS, doc.AdminTimeoutNS, doc.RetentionDays)
	default:
		return "", errors.New("unknown settings schema")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ReadSettings returns the settings of the encrypted vault in dir, the vault directory itself as Dir
// reports it, read fresh from settings.age with key, its own private key, the way a vault process holds
// it. A missing file is DefaultSettings. A schema 1 file keeps its update behaviour and has the defaults
// for the rest. A file that cannot be decrypted, fails its check value, or has an unknown schema or an
// invalid value reads as SafeSettings with ErrSettingsUntrusted, so the caller can warn and still act on
// the safe values; a file that cannot be read for another reason does the same. Without a key the safe
// settings come with ErrNotUnlocked.
func ReadSettings(dir string, key *age.X25519Identity) (Settings, error) {
	settings, _, err := readSettings(dir, key)
	return settings, err
}

// readSettings is ReadSettings that also returns the schema of the trusted file it read: 0 for a missing
// file and for any result that is not trusted.
func readSettings(dir string, key *age.X25519Identity) (Settings, int, error) {
	if key == nil {
		return SafeSettings(), 0, ErrNotUnlocked
	}
	data, err := readFile(filepath.Join(dir, settingsFile))
	if err != nil {
		if isNotExist(err) {
			return DefaultSettings(), 0, nil
		}
		return SafeSettings(), 0, ErrSettingsUntrusted
	}
	plain, err := decryptFrom(data, key)
	if err != nil {
		return SafeSettings(), 0, ErrSettingsUntrusted
	}
	defer clear(plain)
	var doc settingsDocument
	if err := json.Unmarshal(plain, &doc); err != nil {
		return SafeSettings(), 0, ErrSettingsUntrusted
	}
	behaviour, err := ParseUpdateBehaviour(doc.UpdateHandover)
	if err != nil {
		return SafeSettings(), 0, ErrSettingsUntrusted
	}
	want, err := settingsMAC(key, doc)
	if err != nil || !hmac.Equal([]byte(want), []byte(doc.MAC)) {
		return SafeSettings(), 0, ErrSettingsUntrusted
	}
	if doc.Schema == 1 {
		settings := DefaultSettings()
		settings.Update = behaviour
		return settings, 1, nil
	}
	settings := Settings{
		Update: behaviour, IdleTimeout: time.Duration(doc.IdleTimeoutNS),
		AdminTimeout: time.Duration(doc.AdminTimeoutNS), RetentionDays: doc.RetentionDays,
	}
	if settings.Validate() != nil {
		return SafeSettings(), 0, ErrSettingsUntrusted
	}
	return settings, doc.Schema, nil
}

// SettingsCurrent reports whether the vault unlocked in this process holds a trusted settings.age in the
// current schema, the one that carries the idle time, the admin session time and the log retention. A
// missing file and a schema 1 file are not current. A file that is not trusted fails with
// ErrSettingsUntrusted; without an unlocked vault the error is that of Settings.
func (v *Vault) SettingsCurrent() (bool, error) {
	if _, err := v.unlockedDocument(); err != nil {
		return false, err
	}
	_, schema, err := readSettings(v.dir, v.identity.key)
	return schema == settingsSchemaVersion, err
}

// Settings returns the settings of the vault unlocked in this process, with the semantics of ReadSettings.
// It fails with ErrNotUnlocked or ErrNotEncrypted where there is nothing to read.
func (v *Vault) Settings() (Settings, error) {
	if _, err := v.unlockedDocument(); err != nil {
		return SafeSettings(), err
	}
	return ReadSettings(v.dir, v.identity.key)
}

// EffectiveSettings are the settings in force for v, the one place that maps a vault's state to them: the
// vault unlocked in this process reads settings.age (untrusted: SafeSettings with ErrSettingsUntrusted for
// the caller to warn about), a locked vault has no key to read it with and so has SafeSettings, and an
// unencrypted, absent or nil vault has DefaultSettings. A state that cannot be determined reads as
// SafeSettings with that error. The settings are never taken from the configuration file.
func EffectiveSettings(v *Vault) (Settings, error) {
	if v == nil {
		return DefaultSettings(), nil
	}
	state, err := v.State()
	if err != nil {
		return SafeSettings(), err
	}
	switch state {
	case StateUnlocked:
		return v.Settings()
	case StateLocked:
		return SafeSettings(), nil
	}
	return DefaultSettings(), nil
}

// SetSettings writes settings to settings.age of the vault unlocked in this process, authenticated for this
// vault. Whether the caller proved the passphrase is the caller's to check.
func (v *Vault) SetSettings(settings Settings) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	if _, err := v.unlockedDocument(); err != nil {
		return err
	}
	doc := settingsDocument{
		Schema: settingsSchemaVersion, UpdateHandover: string(settings.Update),
		IdleTimeoutNS: int64(settings.IdleTimeout), AdminTimeoutNS: int64(settings.AdminTimeout),
		RetentionDays: settings.RetentionDays,
	}
	mac, err := settingsMAC(v.identity.key, doc)
	if err != nil {
		return err
	}
	doc.MAC = mac
	plain, err := json.MarshalIndent(doc, "", "  ")
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

// ReadUpdateBehaviour returns the update behaviour of ReadSettings.
func ReadUpdateBehaviour(dir string, key *age.X25519Identity) (UpdateBehaviour, error) {
	settings, err := ReadSettings(dir, key)
	return settings.Update, err
}

// UpdateBehaviour returns the update behaviour of Settings.
func (v *Vault) UpdateBehaviour() (UpdateBehaviour, error) {
	settings, err := v.Settings()
	return settings.Update, err
}

// SetUpdateBehaviour changes only the update behaviour. The other settings stay as they are; while
// settings.age is untrusted they start from the defaults, so the file is replaced by a trusted one.
func (v *Vault) SetUpdateBehaviour(behaviour UpdateBehaviour) error {
	if _, err := ParseUpdateBehaviour(string(behaviour)); err != nil {
		return err
	}
	settings, err := v.Settings()
	if errors.Is(err, ErrSettingsUntrusted) {
		settings, err = DefaultSettings(), nil
	}
	if err != nil {
		return err
	}
	settings.Update = behaviour
	return v.SetSettings(settings)
}

// FormatTimeout writes d as a compact Go duration such as 12h, 10m or 0s: the trailing zero units
// time.Duration.String adds are left out, and ParseDuration reads the result back.
func FormatTimeout(d time.Duration) string {
	s := d.String()
	if len(s) > 2 && s[len(s)-2:] == "0s" && d%time.Minute == 0 {
		s = s[:len(s)-2]
	}
	if len(s) > 2 && s[len(s)-2:] == "0m" && d%time.Hour == 0 {
		s = s[:len(s)-2]
	}
	return s
}
