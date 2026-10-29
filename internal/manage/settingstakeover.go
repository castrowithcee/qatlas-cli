package manage

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// Names of the configuration fields whose old values a first unlock offers to take over.
const (
	fieldIdleTimeout   = "vault.idle_timeout"
	fieldAdminTimeout  = "vault.admin_timeout"
	fieldRetentionDays = "logs.retention_days"
)

// TakeoverValue is one old configuration value that is offered: the field it stands in and the value as it
// would be stored, a duration in the form vault.FormatTimeout writes or a number of days.
type TakeoverValue struct {
	Field string
	Value string
}

// SettingsTakeover is the one-time question after the first unlock with the passphrase: should the values
// the configuration file still holds for the idle time, the admin session time and the log retention
// become the vault's settings? The file is untrusted, so nothing of it takes effect before Apply is called
// with yes. Offered lists what is asked, in the order of the fields above.
type SettingsTakeover struct {
	Offered []TakeoverValue

	svc     *Service
	v       *vault.Vault
	current vault.Settings
	next    vault.Settings
}

// Ask reports whether there is anything to ask. A takeover that holds only values the vault cannot store
// has nothing to offer: the surface then calls Apply(false) without a question, which still ends it.
func (t *SettingsTakeover) Ask() bool { return len(t.Offered) > 0 }

// TakeoverResult is what Apply did. Backup is the copy of the configuration file made before the fields
// were removed, "" if the file was not changed. Warning is the redacted failure to remove the fields, which
// never undoes the settings written to the vault, and "" otherwise.
type TakeoverResult struct {
	Backup  string
	Warning string
}

// PrepareSettingsTakeover decides whether the unlock that just succeeded asks the takeover question. It
// does when the vault is encrypted and unlocked in this process, its settings.age is missing or still the
// first schema (the takeover never ran), and the configuration file beside it holds at least one of
// vault.idle_timeout, vault.admin_timeout and logs.retention_days. Then the takeover is returned.
//
// Nothing is asked and nil is returned for an unencrypted or locked vault, a missing or unreadable
// configuration (the error says why, for a warning), and an untrusted settings.age, whose safe values stay
// in force. When the takeover already ran and the file holds one of the fields again, nothing is changed
// either: the warning says that the fields have no effect and where the settings live.
func (s *Service) PrepareSettingsTakeover() (*SettingsTakeover, string, error) {
	v := s.vault()
	if v == nil || s.store == nil || v.Dir() != vault.New(filepath.Dir(s.store.Path())).Dir() {
		return nil, "", nil
	}
	if state, err := v.State(); err != nil || state != vault.StateUnlocked {
		return nil, "", nil
	}
	cfg, _, err := s.store.LoadVersioned()
	var notFound *config.NotFoundError
	switch {
	case errors.As(err, &notFound):
		return nil, "", nil
	case err != nil:
		return nil, "", err
	}
	present := presentFields(cfg)
	if len(present) == 0 {
		return nil, "", nil
	}
	done, err := v.SettingsCurrent()
	if errors.Is(err, vault.ErrSettingsUntrusted) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	if done {
		return nil, s.redact(fmt.Sprintf("%s holds %s, which has no effect: the idle timeout, the admin session "+
			"timeout and the log retention are settings of the vault; see 'qatlas vault settings'",
			s.store.Path(), strings.Join(present, ", "))), nil
	}
	current, err := v.Settings()
	if err != nil {
		return nil, "", err
	}
	t := &SettingsTakeover{svc: s, v: v, current: current, next: current}
	t.offer(cfg)
	return t, "", nil
}

// presentFields names the three fields cfg holds.
func presentFields(cfg *config.Config) []string {
	var names []string
	if cfg.Vault.IdleTimeout != "" {
		names = append(names, fieldIdleTimeout)
	}
	if cfg.Vault.AdminTimeout != "" {
		names = append(names, fieldAdminTimeout)
	}
	if cfg.Logs.RetentionDays != 0 {
		names = append(names, fieldRetentionDays)
	}
	return names
}

// offer collects the values of cfg the vault can store. A value that does not parse or that the vault would
// refuse is left out.
func (t *SettingsTakeover) offer(cfg *config.Config) {
	try := func(field, value string, apply func(*vault.Settings)) {
		candidate := t.next
		apply(&candidate)
		if candidate.Validate() != nil {
			return
		}
		t.next = candidate
		t.Offered = append(t.Offered, TakeoverValue{Field: field, Value: value})
	}
	if text := cfg.Vault.IdleTimeout; text != "" {
		if d, err := time.ParseDuration(text); err == nil {
			try(fieldIdleTimeout, vault.FormatTimeout(d), func(s *vault.Settings) { s.IdleTimeout = d })
		}
	}
	if text := cfg.Vault.AdminTimeout; text != "" {
		if d, err := time.ParseDuration(text); err == nil {
			try(fieldAdminTimeout, vault.FormatTimeout(d), func(s *vault.Settings) { s.AdminTimeout = d })
		}
	}
	if days := cfg.Logs.RetentionDays; days != 0 {
		try(fieldRetentionDays, fmt.Sprintf("%d", days), func(s *vault.Settings) { s.RetentionDays = days })
	}
}

// Apply ends the takeover with the person's answer. It writes settings.age in the current schema, with the
// offered values when yes and with the current settings unchanged otherwise, so the question never returns.
// A failure of that write is the error and nothing else was changed. It then removes the three fields from
// the configuration file, with a backup of the file as it was, whether or not they were taken over. If that
// fails, for instance because the file cannot be written, settings.age stays as written and the result
// carries a warning. Apply must be called once.
func (t *SettingsTakeover) Apply(yes bool) (TakeoverResult, error) {
	settings := t.current
	if yes {
		settings = t.next
	}
	if err := t.v.SetSettings(settings); err != nil {
		return TakeoverResult{}, err
	}
	backup, err := t.svc.removeSettingFields()
	if err != nil {
		return TakeoverResult{Warning: t.svc.redact(fmt.Sprintf("the settings are saved in the vault, but %s could "+
			"not be cleaned of the old vault.idle_timeout, vault.admin_timeout and logs.retention_days, which have "+
			"no effect: %v", t.svc.store.Path(), err))}, nil
	}
	return TakeoverResult{Backup: backup}, nil
}

var errNoSettingFields = errors.New("the configuration holds none of the setting fields")

// removeSettingFields drops the three fields from the current configuration under the store's lock and
// returns the path of the backup the save made, "" if the file held none of them. A change by another writer
// meanwhile is not lost: the fields are removed from what the file holds then.
func (s *Service) removeSettingFields() (string, error) {
	before := s.backupNames()
	err := s.store.Update(func(cfg *config.Config) error {
		if len(presentFields(cfg)) == 0 {
			return errNoSettingFields
		}
		cfg.Vault.IdleTimeout, cfg.Vault.AdminTimeout, cfg.Logs.RetentionDays = "", "", 0
		return nil
	})
	if errors.Is(err, errNoSettingFields) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	backups, _ := s.store.Backups()
	for _, b := range backups {
		if !before[b.Name] {
			return b.Path(), nil
		}
	}
	return "", nil
}

func (s *Service) backupNames() map[string]bool {
	names := map[string]bool{}
	backups, _ := s.store.Backups()
	for _, b := range backups {
		names[b.Name] = true
	}
	return names
}
