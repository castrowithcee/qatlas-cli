package manage

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/connlog"
	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// Migration is what one migration of the configuration file to the current schema did. From is the schema
// the file had. Backup is the copy of
// the file as it was and VaultCopies the copies of the vault's files, all in the backups directory beside the
// file; together they are the way back. VaultIDs counts the vault entries that got their credential id, or,
// with Pending, the pending writes of a locked vault that carry them until its next unlock. Renamings are the
// former provider and tool IDs the file named, which it names by their current IDs now, as every save writes
// them. Reapproval names the connections that read a credential of an encrypted vault and whose effects the
// migration narrowed to those of their tools: their approval no longer matches, and each needs approving once
// ('qatlas vault approve', or in the editor). With a locked vault it lists every such connection, since
// whether it was approved is unknown. Retargeted names in the same way the connections whose targets or
// exclusions the migration rewrote into their current form (see config.Config.RetargetedConnections).
// OutdatedTargets names the connections whose targets their provider could not carry over: they are written as
// they were, marked, and invalid until a person chooses their targets again. All three hold names only, never
// a target. Warning is the redacted failure to log the migration, which never undoes it, and "" otherwise.
type Migration struct {
	Path            string
	From            int
	Backup          string
	VaultCopies     []string
	VaultIDs        int
	Pending         bool
	Renamings       []config.Renaming
	Reapproval      []string
	Retargeted      []string
	OutdatedTargets []string
	Warning         string
}

// MigrationError reports a migration that did not happen. Without Backup nothing was changed: the file, the
// vault and the backups directory are as they were. With Backup the file was written but did not read back
// as written, and Backup holds the file as it was before.
type MigrationError struct {
	Path   string
	Backup string
	Err    error
}

func (e *MigrationError) Error() string {
	if e.Backup != "" {
		return fmt.Sprintf("%s was migrated to configuration schema %d but does not read back as written: %v; "+
			"restore it from %s", e.Path, config.Version, e.Err, e.Backup)
	}
	return fmt.Sprintf("%s was not migrated to configuration schema %d and is unchanged: %v", e.Path,
		config.Version, e.Err)
}

func (e *MigrationError) Unwrap() error { return e.Err }

// migrationAttempts bounds how often a migration starts over after another writer changed the file.
const migrationAttempts = 3

// saveMigration is a seam so a test can fail the save after the vault took the credential ids.
var saveMigration = func(save func(*config.Config) error, cfg *config.Config) error { return save(cfg) }

// MigrateSchema moves a configuration file of an older schema to the current one, once: it backs up the file
// and the vault's files, gives every entry its id, records each vault credential's id in its vault entry, and
// saves and reads back the file, all under the configuration's lock and only if the file is still the one it
// read. A file that is missing or already current needs nothing and returns nil, nil, which is also what a
// second process sees once another one migrated. A file that does not load is left as it is, without a
// backup. Any failure before the file is written leaves file and vault unchanged and removes the backups it
// made; the error is then a *MigrationError without Backup. A connection whose targets cannot be carried over
// does not stop the migration: it is written as it was with its mark (see config.Store.ForMigration), and the
// file then fails to load until a person chooses its targets again.
//
// A vault that is encrypted and locked takes the ids as pending writes, merged on its next unlock; until then
// entries are matched by name, as they always were. No vault is created, and a vault other than the one
// beside the file is never written. The migration is logged as invokelog.OperationConfigMigrate with the
// service's surface as its path, which is "cli" for the log to accept it. It logs no connection
// change and touches no approval, since ids are part of neither.
func (s *Service) MigrateSchema() (*Migration, error) {
	if s.store.HasCurrentSchema() {
		return nil, nil
	}
	for attempt := 0; attempt < migrationAttempts; attempt++ {
		cfg, rev, err := s.store.ForMigration().LoadVersioned()
		var notFound *config.NotFoundError
		switch {
		case errors.As(err, &notFound):
			return nil, nil
		case err != nil:
			return nil, &MigrationError{Path: s.store.Path(), Err: err}
		case cfg.Version == config.Version:
			return nil, nil
		}
		m, err := s.migrate(cfg, rev)
		if errors.Is(err, config.ErrConflict) {
			continue
		}
		if err != nil {
			return nil, &MigrationError{Path: s.store.Path(), Backup: m.Backup, Err: err}
		}
		m.Warning = s.logMigration(cfg)
		return m, nil
	}
	return nil, &MigrationError{Path: s.store.Path(), Err: errors.New("the file kept changing while it was migrated")}
}

// migrate runs one attempt on cfg, read at revision rev. The returned Migration has Backup set once the file
// was written, also when the read-back failed.
func (s *Service) migrate(cfg *config.Config, rev config.Revision) (*Migration, error) {
	m := &Migration{Path: s.store.Path(), From: cfg.Version, Renamings: cfg.Renamings(),
		OutdatedTargets: cfg.OutdatedTargets()}
	v := s.vault()
	if v != nil && v.Dir() != vault.New(filepath.Dir(s.store.Path())).Dir() {
		v = nil
	}
	err := s.store.ForMigration().Transact(rev, func(save func(*config.Config) error) error {
		var copies []string
		discard := func() {
			for _, path := range copies {
				_ = os.Remove(path)
			}
		}
		backup, err := s.store.BackupCopy(s.store.Path())
		if err != nil {
			return err
		}
		copies = append(copies, backup)
		vaultCopies, err := s.backupVaultFiles(v)
		copies = append(copies, vaultCopies...)
		if err != nil {
			discard()
			return err
		}

		m.Reapproval = reapprovalNeeded(cfg, v, cfg.NarrowedEffects())
		m.Retargeted = reapprovalNeeded(cfg, v, cfg.RetargetedConnections())
		cfg.AssignIDs()
		undo := func() error { return nil }
		if v != nil {
			state, err := v.State()
			if err == nil {
				m.Pending = state == vault.StateLocked
				m.VaultIDs, undo, err = v.RecordCredentialIDs(vaultCredentialIDs(cfg))
			}
			if err != nil {
				discard()
				return fmt.Errorf("recording the credential ids in the vault: %w", err)
			}
		}
		if err := saveMigration(save, cfg); err != nil {
			if undoErr := undo(); undoErr != nil {
				// The copies are the only way back for the vault now, so they stay.
				return fmt.Errorf("%w; the vault could not be put back as it was (%v), its copies are %s", err,
					undoErr, strings.Join(vaultCopies, ", "))
			}
			discard()
			return err
		}
		m.Backup, m.VaultCopies = backup, vaultCopies
		return s.readBack(cfg)
	})
	return m, err
}

// reapprovalNeeded returns the connections of changed, whose scope the migration changes, that an encrypted
// vault approved or may have approved. It never widens anything and approves nothing.
func reapprovalNeeded(cfg *config.Config, v *vault.Vault, changed []string) []string {
	if v == nil || len(changed) == 0 {
		return nil
	}
	state, err := v.State()
	if err != nil || state != vault.StateLocked && state != vault.StateUnlocked {
		return nil
	}
	var approvals map[string]vault.Approval
	if state == vault.StateUnlocked {
		if approvals, err = v.Approvals(); err != nil {
			approvals = nil
		}
	}
	var names []string
	for _, name := range changed {
		if cred, ok := cfg.Credentials[cfg.Connections[name].Credential]; !ok || cred.Type != config.CredentialTypeVault {
			continue
		}
		if _, approved := approvals[name]; approvals != nil && !approved {
			continue
		}
		names = append(names, name)
	}
	return names
}

// backupVaultFiles copies every file at the top of the vault directory; the pending writes and the logs below
// it are not part of the vault's state a migration changes. A vault that does not exist has nothing to copy.
func (s *Service) backupVaultFiles(v *vault.Vault) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	entries, err := os.ReadDir(v.Dir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", v.Dir(), err)
	}
	var copies []string
	for _, e := range entries {
		// Names starting with a dot are a write's temporary files.
		if !e.Type().IsRegular() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		path, err := s.store.BackupCopy(filepath.Join(v.Dir(), e.Name()))
		if err != nil {
			return copies, err
		}
		copies = append(copies, path)
	}
	return copies, nil
}

// vaultCredentialIDs maps every vault credential's name to its id.
func vaultCredentialIDs(cfg *config.Config) map[string]string {
	ids := map[string]string{}
	for name, cred := range cfg.Credentials {
		if cred.Type == config.CredentialTypeVault {
			ids[name] = cred.ID
		}
	}
	return ids
}

// readBack loads the file just written and checks that it is in the current schema with the ids, targets and
// exclusions of cfg.
func (s *Service) readBack(cfg *config.Config) error {
	loaded, err := s.store.ForMigration().Load()
	if err != nil {
		return err
	}
	if loaded.Version != config.Version {
		return fmt.Errorf("it reads as schema %d", loaded.Version)
	}
	for name, entry := range cfg.Services {
		if loaded.Services[name].ID != entry.ID {
			return fmt.Errorf("services.%s.id differs", name)
		}
	}
	for name, entry := range cfg.Credentials {
		if loaded.Credentials[name].ID != entry.ID {
			return fmt.Errorf("credentials.%s.id differs", name)
		}
	}
	for name, entry := range cfg.Connections {
		got := loaded.Connections[name]
		switch {
		case got.ID != entry.ID:
			return fmt.Errorf("connections.%s.id differs", name)
		case got.Target != entry.Target || !slices.Equal(got.Targets, entry.Targets) ||
			!slices.Equal(got.Exclude, entry.Exclude) || got.TargetsOutdated != entry.TargetsOutdated ||
			!maps.Equal(got.TargetLabels, entry.TargetLabels):
			return fmt.Errorf("connections.%s: its targets differ", name)
		}
	}
	return nil
}

// logMigration appends the migration's entry to the invocation log and returns the redacted warning to show
// when that failed, "" otherwise.
func (s *Service) logMigration(cfg *config.Config) string {
	fields := invokelog.Fields{Path: s.surface, Operation: invokelog.OperationConfigMigrate, Effect: "update",
		Result: "success", Version: config.Version}
	err := fields.Validate()
	if err == nil {
		err = connlog.NewRecorder(s.surface, s.store.Path(), cfg.LogRetentionDays(), s.vault(), s.processOK).Writer.Append(fields)
	}
	var unsigned *invokelog.UnsignedError
	switch {
	case err == nil:
		return ""
	case errors.As(err, &unsigned):
		return s.redact("warning: the migration was logged, but unsigned: " + err.Error())
	default:
		return s.redact("warning: the migration could not be logged: " + err.Error())
	}
}
