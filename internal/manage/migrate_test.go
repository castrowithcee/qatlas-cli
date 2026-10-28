package manage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/connlog"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// schemaOne is a configuration written before ids, with a keyring, a vault, and an env credential.
const schemaOne = `version: 1
services:
    wiki:
        provider: docs
        base_url: https://wiki.example.test
credentials:
    env-reader:
        type: env
        values:
            key-id: WIKI_KEY_ID
            key-secret: WIKI_KEY_SECRET
    keyring-reader:
        type: keyring
    vault-reader:
        type: vault
connections:
    env-wiki:
        service: wiki
        credential: env-reader
        permissions:
            - read
    keyring-wiki:
        service: wiki
        credential: keyring-reader
        permissions:
            - read
    vault-wiki:
        service: wiki
        credential: vault-reader
        permissions:
            - read
defaults:
    connections:
        knowledge: vault-wiki
`

const migratePassphrase = "synthetic-passphrase-7d21"

// migrateWorld is a schema-1 file in a directory of its own, with the vault beside it in the state the test
// asks for. Every vault holds the secret of vault-reader; an encrypted one also approves vault-wiki.
type migrateWorld struct {
	dir, path string
	v         *vault.Vault
}

func newMigrateWorld(t *testing.T, state vault.State) migrateWorld {
	t.Helper()
	dir := t.TempDir()
	w := migrateWorld{dir: dir, path: filepath.Join(dir, "config.yaml"), v: vault.New(dir)}
	if err := os.WriteFile(w.path, []byte(schemaOne), 0o600); err != nil {
		t.Fatal(err)
	}
	switch state {
	case vault.StateUnencrypted:
		if err := w.v.Set("vault-reader", "key-id", "synthetic-id", nil); err != nil {
			t.Fatal(err)
		}
	case vault.StateLocked, vault.StateUnlocked:
		offer := func(string) (string, error) { return migratePassphrase, nil }
		if err := w.v.Set("vault-reader", "key-id", "synthetic-id", offer); err != nil {
			t.Fatal(err)
		}
		cfg, err := config.Load(w.path, setupCatalog{})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := approval.Approve(context.Background(), cfg, w.v, nil); err != nil {
			t.Fatalf("Approve() = %v", err)
		}
		if state == vault.StateLocked {
			w.v = vault.New(dir)
		}
	}
	return w
}

func (w migrateWorld) service() *Service {
	return ForVault(config.NewStore(w.path, setupCatalog{}), w.v, connlog.SurfaceCLI, nil).WithVaultProcessSupport(false)
}

// files returns every file below dir by its relative path with its content.
func files(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return out
	}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		// The lock file is how the store takes its lock, never part of the state.
		if err != nil || d.IsDir() || strings.HasSuffix(path, ".lock") {
			return err
		}
		data, err := os.ReadFile(path)
		rel, _ := filepath.Rel(dir, path)
		out[filepath.ToSlash(rel)] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func backups(t *testing.T, dir string) []string {
	t.Helper()
	entries, _ := os.ReadDir(filepath.Join(dir, "backups"))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// vaultCredentialID reads the credential id the vault entry of vault-reader carries. An encrypted vault is
// unlocked, which merges what is pending, and decrypted to be read.
func vaultCredentialID(t *testing.T, dir string, encrypted bool) string {
	t.Helper()
	v := vault.New(dir)
	if encrypted {
		if err := v.Decrypt(migratePassphrase); err != nil {
			t.Fatalf("Decrypt() = %v", err)
		}
	}
	data, err := os.ReadFile(filepath.Join(v.Dir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Entries map[string]vault.Entry `json:"entries"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	for _, e := range doc.Entries {
		if e.Name == "vault-reader" {
			if e.Roles["key-id"] != "synthetic-id" {
				t.Errorf("the secret changed: %+v", e)
			}
			return e.CredentialID
		}
	}
	t.Fatal("the vault lost vault-reader")
	return ""
}

func logEntries(t *testing.T, dir string) []string {
	t.Helper()
	paths, _ := filepath.Glob(filepath.Join(dir, vault.DirName, "logs", "*.jsonl"))
	var lines []string
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if line != "" {
				lines = append(lines, line)
			}
		}
	}
	return lines
}

func TestMigrateSchemaInEveryVaultState(t *testing.T) {
	for _, state := range []vault.State{vault.StateAbsent, vault.StateUnencrypted, vault.StateLocked, vault.StateUnlocked} {
		t.Run(string(state), func(t *testing.T) {
			w := newMigrateWorld(t, state)
			vaultBefore := files(t, w.v.Dir())

			m, err := w.service().MigrateSchema()
			if err != nil || m == nil {
				t.Fatalf("MigrateSchema() = %+v, %v", m, err)
			}

			// The backup is the file as it was, private, and outlives the rotation of save backups.
			backup, err := os.ReadFile(m.Backup)
			if err != nil || string(backup) != schemaOne {
				t.Fatalf("backup %s = %q, %v", m.Backup, backup, err)
			}
			if fi, _ := os.Stat(m.Backup); fi.Mode().Perm() != 0o600 && os.PathSeparator == '/' {
				t.Errorf("backup mode = %v, want 0600", fi.Mode().Perm())
			}
			copied := map[string]bool{}
			for _, c := range m.VaultCopies {
				data, _ := os.ReadFile(c)
				name := filepath.Base(c)
				name = name[:strings.Index(name, "-")]
				if vaultBefore[name] != string(data) {
					t.Errorf("vault copy %s differs from %s", c, name)
				}
				copied[name] = true
			}
			for name := range vaultBefore {
				if !strings.Contains(name, "/") && !copied[name] {
					t.Errorf("vault file %s was not copied: %v", name, m.VaultCopies)
				}
			}

			cfg, err := config.Load(w.path, setupCatalog{})
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Version != config.Version {
				t.Errorf("version = %d", cfg.Version)
			}
			for name, c := range cfg.Credentials {
				if c.ID == "" {
					t.Errorf("credential %s has no id", name)
				}
			}
			if cfg.Services["wiki"].ID == "" || cfg.Connections["vault-wiki"].ID == "" {
				t.Errorf("ids missing: %+v", cfg)
			}

			switch state {
			case vault.StateAbsent:
				if _, err := os.Stat(filepath.Join(w.v.Dir(), "secrets.json")); !os.IsNotExist(err) {
					t.Errorf("a vault was created: %v", err)
				}
				if m.VaultIDs != 0 || len(m.VaultCopies) != 0 {
					t.Errorf("migration = %+v", m)
				}
			case vault.StateLocked:
				if !m.Pending || m.VaultIDs != 1 {
					t.Errorf("migration = %+v, want one pending id", m)
				}
				if after, _ := os.ReadFile(filepath.Join(w.v.Dir(), "secrets.age")); string(after) != vaultBefore["secrets.age"] {
					t.Error("the locked vault's document was written")
				}
			default:
				if m.Pending || m.VaultIDs != 1 {
					t.Errorf("migration = %+v, want one recorded id", m)
				}
			}

			// IDs are no trust anchor: what was approved stays approved, and no connection change is logged.
			if state == vault.StateLocked || state == vault.StateUnlocked {
				probe := vault.New(w.dir)
				if _, err := probe.Unlock(migratePassphrase); err != nil {
					t.Fatal(err)
				}
				report, err := approval.Pending(cfg, probe)
				if err != nil || len(report.Open) != 0 {
					t.Errorf("Pending() = %+v, %v, want nothing open", report.Open, err)
				}
			}
			lines := logEntries(t, w.dir)
			if len(lines) != 1 || !strings.Contains(lines[0], `"path":"cli","operation":"config.migrate"`) ||
				!strings.Contains(lines[0], `"version":4`) {
				t.Errorf("log = %v, want exactly the migration", lines)
			}

			if state != vault.StateAbsent {
				if got := vaultCredentialID(t, w.dir, state != vault.StateUnencrypted); got != cfg.Credentials["vault-reader"].ID {
					t.Errorf("vault entry credential id = %q, want %q", got, cfg.Credentials["vault-reader"].ID)
				}
			}
		})
	}
}

// A second start finds the file current and changes nothing, not even the backups.
func TestMigrateSchemaRunsOnce(t *testing.T) {
	w := newMigrateWorld(t, vault.StateUnencrypted)
	if _, err := w.service().MigrateSchema(); err != nil {
		t.Fatal(err)
	}
	before := files(t, w.dir)

	m, err := w.service().MigrateSchema()

	if m != nil || err != nil {
		t.Fatalf("second MigrateSchema() = %+v, %v, want nil, nil", m, err)
	}
	after := files(t, w.dir)
	if len(after) != len(before) {
		t.Errorf("files changed: %d before, %d after", len(before), len(after))
	}
	for name, data := range before {
		if after[name] != data {
			t.Errorf("%s changed", name)
		}
	}
}

// A file that does not load is left byte for byte, without a backup, with a message that says so.
func TestMigrateSchemaLeavesAnInvalidFile(t *testing.T) {
	w := newMigrateWorld(t, vault.StateUnencrypted)
	invalid := strings.Replace(schemaOne, "credential: env-reader", "credential: nobody", 1)
	if err := os.WriteFile(w.path, []byte(invalid), 0o600); err != nil {
		t.Fatal(err)
	}
	vaultBefore := files(t, w.v.Dir())

	m, err := w.service().MigrateSchema()

	var me *MigrationError
	if m != nil || !errors.As(err, &me) || me.Backup != "" || !strings.Contains(err.Error(), "not migrated") ||
		!strings.Contains(err.Error(), "is unchanged") || !strings.Contains(err.Error(), "connections.env-wiki") {
		t.Fatalf("MigrateSchema() = %+v, %v", m, err)
	}
	if data, _ := os.ReadFile(w.path); string(data) != invalid {
		t.Error("the invalid file changed")
	}
	if names := backups(t, w.dir); len(names) != 0 {
		t.Errorf("backups = %v, want none", names)
	}
	if after := files(t, w.v.Dir()); len(after) != len(vaultBefore) || after["secrets.json"] != vaultBefore["secrets.json"] {
		t.Error("the vault changed")
	}
}

// A failure after the vault took the ids and before the file is written puts the vault back, leaves the file
// as it was, and keeps no backup.
func TestMigrateSchemaFailureBeforeTheSaveChangesNothing(t *testing.T) {
	for _, state := range []vault.State{vault.StateUnencrypted, vault.StateLocked, vault.StateUnlocked} {
		t.Run(string(state), func(t *testing.T) {
			w := newMigrateWorld(t, state)
			before := files(t, w.dir)
			original := saveMigration
			saveMigration = func(func(*config.Config) error, *config.Config) error { return errors.New("injected") }
			t.Cleanup(func() { saveMigration = original })

			m, err := w.service().MigrateSchema()

			var me *MigrationError
			if m != nil || !errors.As(err, &me) || me.Backup != "" || !strings.Contains(err.Error(), "injected") {
				t.Fatalf("MigrateSchema() = %+v, %v", m, err)
			}
			after := files(t, w.dir)
			if len(after) != len(before) {
				t.Errorf("files: %v, want %v", keys(after), keys(before))
			}
			for name, data := range before {
				if after[name] != data {
					t.Errorf("%s changed", name)
				}
			}
			if state != vault.StateLocked {
				if got := vaultCredentialID(t, w.dir, state != vault.StateUnencrypted); got != "" {
					t.Errorf("the vault kept the credential id %q", got)
				}
			}
		})
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Starts that race each migrate with a store of their own, and the file is migrated exactly once.
func TestConcurrentStartsMigrateOnce(t *testing.T) {
	w := newMigrateWorld(t, vault.StateUnencrypted)
	const starts = 6
	results := make(chan *Migration, starts)
	errs := make(chan error, starts)
	var wg sync.WaitGroup
	for i := 0; i < starts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			own := migrateWorld{dir: w.dir, path: w.path, v: vault.New(w.dir)}
			m, err := own.service().MigrateSchema()
			results <- m
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)

	migrated := 0
	for m := range results {
		if m != nil {
			migrated++
		}
	}
	for err := range errs {
		if err != nil {
			t.Errorf("MigrateSchema() = %v", err)
		}
	}
	if migrated != 1 {
		t.Errorf("%d starts migrated, want 1", migrated)
	}
	var configCopies int
	for _, name := range backups(t, w.dir) {
		if strings.HasPrefix(name, "config.yaml-") {
			configCopies++
		}
	}
	if configCopies != 1 {
		t.Errorf("%d migration backups, want 1: %v", configCopies, backups(t, w.dir))
	}
	if lines := logEntries(t, w.dir); len(lines) != 1 {
		t.Errorf("log = %v, want one migration", lines)
	}
}

// The way back is to copy the backups in place: the file is then the schema-1 file byte for byte, and the
// vault reads as it did, the additive credential id aside.
func TestMigrationBackupsRestoreTheSchemaOneFile(t *testing.T) {
	w := newMigrateWorld(t, vault.StateUnencrypted)
	plainBefore, _ := os.ReadFile(filepath.Join(w.v.Dir(), "secrets.json"))
	m, err := w.service().MigrateSchema()
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(m.Backup)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range m.VaultCopies {
		name := filepath.Base(c)
		data, _ := os.ReadFile(c)
		if err := os.WriteFile(filepath.Join(w.v.Dir(), name[:strings.Index(name, "-")]), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	restored, _ := os.ReadFile(w.path)
	if !bytes.Equal(restored, []byte(schemaOne)) || bytes.Contains(restored, []byte(" id: ")) {
		t.Fatalf("restored file:\n%s", restored)
	}
	cfg, err := config.Load(w.path, setupCatalog{})
	if err != nil || cfg.Version != 1 || cfg.Credentials["vault-reader"].ID != "" {
		t.Fatalf("Load() = %+v, %v, want the schema-1 file", cfg, err)
	}
	if plain, _ := os.ReadFile(filepath.Join(w.v.Dir(), "secrets.json")); !bytes.Equal(plain, plainBefore) {
		t.Error("the vault copy does not restore the vault")
	}
}

// Once ids exist, every vault write of a credential records its id: a new credential's first commit, and a
// later secret of one whose entry has none yet.
func TestVaultWritesRecordTheCredentialID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	store := config.NewStore(path, setupCatalog{})
	v := vault.New(dir)
	secrets := &vaultSecrets{fakeSecrets: *newFakeSecrets(), v: v}
	svc := New(store, secrets, connlog.SurfaceCLI, nil).WithVaultProcessSupport(false)

	cfg := store.New()
	cfg.Credentials["vault-reader"] = config.Credential{Type: config.CredentialTypeVault}
	if _, err := svc.CommitSecrets(cfg, config.RevisionAbsent, "vault-reader", true, []string{"key-id"},
		map[string]string{"key-id": "synthetic-id"}, nil); err != nil {
		t.Fatalf("CommitSecrets() = %v", err)
	}
	saved, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if id := saved.Credentials["vault-reader"].ID; id == "" || vaultCredentialID(t, dir, false) != id {
		t.Fatalf("vault entry id %q, config id %q", vaultCredentialID(t, dir, false), id)
	}

	// An entry written before ids, then given a secret through the core, takes the id it lacked.
	if err := v.Set("older", "key-id", "x", nil); err != nil {
		t.Fatal(err)
	}
	saved.Credentials["older"] = config.Credential{Type: config.CredentialTypeVault, ID: "0123456789abcdef0123456789abcdef"}
	if _, err := svc.SetSecret(context.Background(), saved, SecretWrite{Name: "older", Entry: saved.Credentials["older"],
		Role: "key-secret", Value: "y"}); err != nil {
		t.Fatalf("SetSecret() = %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(v.Dir(), "secrets.json"))
	if !strings.Contains(string(data), `"credential_id": "0123456789abcdef0123456789abcdef"`) {
		t.Errorf("the older entry lacks its id:\n%s", data)
	}
}
