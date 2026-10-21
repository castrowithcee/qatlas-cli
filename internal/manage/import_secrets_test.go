package manage

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

const (
	secOldKeyring = "old-keyring-value-5a17"
	secOldVault   = "old-vault-value-6b28"
	secNewMail    = "new-mail-value-7c39"
)

// importSecrets keeps keyring secrets in a map and vault secrets in a real vault. The failAt-th secret write
// fails (counted from 1, 0 never); onWrite runs after every successful one.
type importSecrets struct {
	vaultSecrets
	writes, failAt int
	onWrite        func()
}

func (s *importSecrets) count() error {
	s.writes++
	if s.writes == s.failAt {
		return errors.New("store offline")
	}
	return nil
}

func (s *importSecrets) wrote() {
	if s.onWrite != nil {
		s.onWrite()
	}
}

func (s *importSecrets) Set(credential, role, value string) error {
	if err := s.count(); err != nil {
		return err
	}
	if err := s.fakeSecrets.Set(credential, role, value); err != nil {
		return err
	}
	s.wrote()
	return nil
}

func (s *importSecrets) SetVaultUndoable(credential, id, role, value string, offer vault.PassphraseFunc) (func() error, error) {
	if err := s.count(); err != nil {
		return nil, err
	}
	undo, err := s.vaultSecrets.SetVaultUndoable(credential, id, role, value, offer)
	if err == nil {
		s.wrote()
	}
	return undo, err
}

type importWorld struct {
	*transferWorld
	sec *importSecrets
	v   *vault.Vault
}

// newImportWorld is newTransferWorld whose secrets go to a keyring map and the vault beside its configuration.
func newImportWorld(t *testing.T, text string) *importWorld {
	t.Helper()
	w := newTransferWorld(t, text)
	v := vault.New(w.dir)
	sec := &importSecrets{vaultSecrets: vaultSecrets{fakeSecrets: *newFakeSecrets(), v: v}}
	w.svc = w.svc.WithSecrets(sec)
	return &importWorld{transferWorld: w, sec: sec, v: v}
}

// treeOf returns every file and directory below dir with its content; a missing dir has none.
func treeOf(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			out[path+string(filepath.Separator)] = ""
			return nil
		}
		data, err := os.ReadFile(path)
		out[path] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// vaultSecretFiles returns the files below the vault directory that hold its secrets, leaving out
// directories and the connection log kept there too.
func vaultSecretFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := treeOf(t, dir)
	for path := range out {
		if strings.HasPrefix(path, filepath.Join(dir, "logs")) || strings.HasSuffix(path, string(filepath.Separator)) {
			delete(out, path)
		}
	}
	return out
}

// noValuesOutsideVault checks every file of the installation but the vault's secrets: the configuration, its
// backups and the connection log.
func (w *importWorld) noValuesOutsideVault(t *testing.T) {
	t.Helper()
	secrets := vaultSecretFiles(t, w.v.Dir())
	for path, content := range treeOf(t, w.dir) {
		if _, ok := secrets[path]; ok {
			continue
		}
		noValues(t, path, content)
		for _, v := range []string{secOldKeyring, secOldVault, secNewMail} {
			if strings.Contains(content, v) {
				t.Errorf("%s contains %q", path, v)
			}
		}
	}
}

func clearSSH(t *testing.T) {
	for _, name := range []string{"SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"} {
		t.Setenv(name, "")
	}
}

func plainSecretsExport(t *testing.T) string {
	t.Helper()
	out, _ := exportWithSecrets(t, secretsWorld(t), SecretExport{Unencrypted: true})
	return out
}

func TestImportSecretsRoundTrip(t *testing.T) {
	clearSSH(t)
	plain := plainSecretsExport(t)
	text, err := os.ReadFile(plain)
	if err != nil {
		t.Fatal(err)
	}
	encrypted := lowCostEncrypted(t, string(text), secExportP)
	brought := map[string]string{"mail-key.api-key": secKeyring, "payload.user": secForward, "spare-key.api-key": secVault}

	t.Run("plain file into the keyring", func(t *testing.T) {
		dst := newImportWorld(t, "")
		plan, err := dst.svc.PlanImportFile(plain, ImportModeCreate, nil)
		if err != nil {
			t.Fatal(err)
		}
		res, err := dst.svc.ApplyImportWith(plan, ImportOptions{SecretStore: config.CredentialTypeKeyring})
		if err != nil {
			t.Fatalf("ApplyImportWith() = %v", err)
		}
		if !reflect.DeepEqual(dst.sec.entries, brought) {
			t.Errorf("keyring = %v, want the three secrets", dst.sec.entries)
		}
		cfg := dst.load()
		for _, name := range []string{"mail-key", "payload", "spare-key"} {
			if got := cfg.Credentials[name].Type; got != config.CredentialTypeKeyring {
				t.Errorf("%s has type %s, want keyring", name, got)
			}
		}
		if len(vaultSecretFiles(t, dst.v.Dir())) != 0 {
			t.Error("a keyring import touched the vault")
		}
		if res.SecretStore != config.CredentialTypeKeyring || res.KeyringOverSSH || len(res.Withdrawn) != 0 ||
			fmt.Sprint(res.Connections) != "[mail wiki]" {
			t.Errorf("result = %+v", res)
		}
		noValues(t, "the result", fmt.Sprintf("%+v %v", res, plan))
		dst.noValuesOutsideVault(t)
	})

	t.Run("encrypted file into a new vault", func(t *testing.T) {
		dst := newImportWorld(t, "")
		plan, err := dst.svc.PlanImportFile(encrypted, ImportModeCreate, asking(secExportP))
		if err != nil {
			t.Fatal(err)
		}
		// The file's defaults.secret_store is vault, and so is the target's after the import.
		res, err := dst.svc.ApplyImportWith(plan, ImportOptions{Offer: asking("")})
		if err != nil {
			t.Fatalf("ApplyImportWith() = %v", err)
		}
		if res.SecretStore != config.CredentialTypeVault || len(dst.sec.entries) != 0 {
			t.Errorf("store = %s, keyring = %v", res.SecretStore, dst.sec.entries)
		}
		for key, want := range brought {
			credential, role, _ := strings.Cut(key, ".")
			if got, found, _, err := dst.v.Get(credential, role, nil); err != nil || !found || got != want {
				t.Errorf("vault %s = found %v, %v, want the file's secret", key, found, err)
			}
			if got := dst.load().Credentials[credential].Type; got != config.CredentialTypeVault {
				t.Errorf("%s has type %s, want vault", credential, got)
			}
		}
		if out := dst.svc.ApproveImport(context.Background(), res, ApprovalDirectOnly); !out.VaultUnencrypted ||
			len(out.Approved) != 0 {
			t.Errorf("ApproveImport() = %+v, want the unencrypted vault reported", out)
		}
		dst.noValuesOutsideVault(t)
	})
}

func TestImportSecretsRollsBackEverySecret(t *testing.T) {
	stores := []struct {
		name, store string
		vault       bool
	}{
		{"keyring", config.CredentialTypeKeyring, false},
		{"existing vault", config.CredentialTypeVault, true},
		{"new vault", config.CredentialTypeVault, false},
	}
	plain := plainSecretsExport(t)
	for _, st := range stores {
		for _, failure := range []string{"third secret", "save conflict"} {
			t.Run(st.name+"/"+failure, func(t *testing.T) {
				dst := newImportWorld(t, targetConfig)
				// Values the import overwrites, which a rollback must put back.
				dst.sec.entries["mail-key.api-key"] = secOldKeyring
				if st.vault {
					if err := dst.v.Set("spare-key", "api-key", secOldVault, asking("")); err != nil {
						t.Fatal(err)
					}
				}
				plan, err := dst.svc.PlanImportFile(plain, ImportModeMerge, nil)
				if err != nil {
					t.Fatal(err)
				}
				keyring := map[string]string{}
				for k, v := range dst.sec.entries {
					keyring[k] = v
				}
				vaultFiles, configBefore := treeOf(t, dst.v.Dir()), dst.raw()

				var other []byte
				switch failure {
				case "third secret":
					dst.sec.failAt = 3
				case "save conflict":
					dst.sec.onWrite = func() {
						dst.sec.onWrite = nil
						foreign := dst.load()
						foreign.Credentials["foreign"] = config.Credential{Provider: "mail", Type: config.CredentialTypeKeyring}
						if err := config.NewStore(dst.path, setupCatalog{}).Save(foreign); err != nil {
							t.Fatal(err)
						}
						other = dst.raw()
					}
				}
				_, err = dst.svc.ApplyImportWith(plan, ImportOptions{SecretStore: st.store})
				if err == nil {
					t.Fatal("ApplyImportWith() = nil, want the failure")
				}
				if failure == "save conflict" && !errors.Is(err, config.ErrConflict) {
					t.Errorf("err = %v, want a conflict", err)
				}
				noValues(t, "the error", err.Error())
				if strings.Contains(err.Error(), secOldKeyring) || strings.Contains(err.Error(), secOldVault) {
					t.Errorf("the error %q carries a value", err)
				}
				if !reflect.DeepEqual(dst.sec.entries, keyring) {
					t.Errorf("keyring = %v, want %v", dst.sec.entries, keyring)
				}
				if got := treeOf(t, dst.v.Dir()); !reflect.DeepEqual(got, vaultFiles) {
					t.Errorf("vault files changed: %d now, %d before", len(got), len(vaultFiles))
				}
				want := configBefore
				if other != nil {
					want = other
				}
				if string(dst.raw()) != string(want) {
					t.Error("the configuration file is not the one before the import")
				}
				dst.noValuesOutsideVault(t)
			})
		}
	}
}

// A rollback that cannot remove a secret it wrote names the credential and role, never a value.
func TestImportSecretsRollbackNamesWhatStays(t *testing.T) {
	dst := newImportWorld(t, targetConfig)
	plan, err := dst.svc.PlanImportFile(plainSecretsExport(t), ImportModeMerge, nil)
	if err != nil {
		t.Fatal(err)
	}
	dst.sec.failAt = 3
	dst.sec.deleteErr["user"] = errors.New("keyring refused")
	_, err = dst.svc.ApplyImportWith(plan, ImportOptions{SecretStore: config.CredentialTypeKeyring})
	if err == nil || !strings.Contains(err.Error(), "the secrets already stored for payload (user) could not be removed again") ||
		strings.Contains(err.Error(), "mail-key (") {
		t.Fatalf("ApplyImportWith() = %v, want payload.user named alone", err)
	}
	noValues(t, "the error", err.Error())
	if _, ok := dst.sec.entries["mail-key.api-key"]; ok {
		t.Error("mail-key.api-key was not removed")
	}
}

func TestImportSecretsStoreOption(t *testing.T) {
	clearSSH(t)
	plain := plainSecretsExport(t)
	dst := newImportWorld(t, "")
	plan, err := dst.svc.PlanImportFile(plain, ImportModeCreate, nil)
	if err != nil {
		t.Fatal(err)
	}
	if store, ssh, err := plan.SecretStoreFor(""); store != config.CredentialTypeVault || ssh || err != nil {
		t.Errorf("SecretStoreFor(\"\") = %s, %v, %v, want the vault the import sets as default", store, ssh, err)
	}
	if _, err := dst.svc.ApplyImportWith(plan, ImportOptions{SecretStore: "env"}); err == nil || len(dst.raw()) != 0 {
		t.Errorf("ApplyImportWith(env) = %v, want a refusal that writes nothing", err)
	}
	t.Setenv("SSH_TTY", "/dev/pts/9")
	if _, ssh, _ := plan.SecretStoreFor(config.CredentialTypeVault); ssh {
		t.Error("the SSH hint is given for the vault")
	}
	res, err := dst.svc.ApplyImportWith(plan, ImportOptions{SecretStore: config.CredentialTypeKeyring})
	if err != nil {
		t.Fatal(err)
	}
	if res.SecretStore != config.CredentialTypeKeyring || !res.KeyringOverSSH || len(dst.sec.entries) != 3 {
		t.Errorf("result = %+v, keyring = %d entries", res, len(dst.sec.entries))
	}
	cfg := dst.load()
	if cfg.Defaults.SecretStore != config.CredentialTypeVault || cfg.Credentials["spare-key"].Type != config.CredentialTypeKeyring {
		t.Errorf("default %s, spare-key %s: want the default kept and the credential where its secret is",
			cfg.Defaults.SecretStore, cfg.Credentials["spare-key"].Type)
	}
	if len(vaultSecretFiles(t, dst.v.Dir())) != 0 {
		t.Error("the vault was written")
	}
}

// overwriteTarget has a vault credential read by two connections, one of them outside the file, and a second
// vault credential the file replaces without a secret.
const overwriteTarget = `version: 3
services:
    mail-svc:
        provider: mail
        base_url: https://mail.example.test
credentials:
    mail-key:
        provider: mail
        type: vault
    spare-key:
        provider: mail
        type: vault
connections:
    mail:
        service: mail-svc
        credential: mail-key
    mail-extra:
        service: mail-svc
        credential: mail-key
    spare:
        service: mail-svc
        credential: spare-key
`

// overwriteFile brings the same entries as overwriteTarget, but the secret of mail-key only.
var overwriteFile = "format: " + TransferFormat + "\nformat_version: 2\n" + `services:
    mail-svc:
        provider: mail
        base_url: https://mail.example.test
credentials:
    mail-key:
        provider: mail
        type: vault
    spare-key:
        provider: mail
        type: vault
connections:
    mail:
        service: mail-svc
        credential: mail-key
    spare:
        service: mail-svc
        credential: spare-key
secrets:
    mail-key:
        api-key: ` + secNewMail + "\n"

// An overwrite keeps the vault entry and its id, so an approval would still match the replaced secret: the
// import withdraws it, and only ApproveImport approves the imported connection again.
func TestImportSecretsOverwriteWithdrawsApprovals(t *testing.T) {
	ctx := context.Background()
	dst := newImportWorld(t, overwriteTarget)
	if err := dst.v.Set("mail-key", "api-key", secOldVault, asking(approvalPassphrase)); err != nil {
		t.Fatal(err)
	}
	if err := dst.v.Set("spare-key", "api-key", secOldKeyring, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := approval.Approve(ctx, dst.load(), dst.v, nil); err != nil {
		t.Fatal(err)
	}
	approvedBefore, _ := dst.v.Approvals()
	entryBefore, _, _ := dst.v.CredentialID("mail-key")
	file := filepath.Join(t.TempDir(), "overwrite.yaml")
	if err := os.WriteFile(file, []byte(overwriteFile), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := func() *ImportPlan {
		t.Helper()
		p, err := dst.svc.PlanImportFile(file, ImportModeOverwrite, nil)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}

	t.Run("a failed commit keeps the approvals", func(t *testing.T) {
		vaultFiles := treeOf(t, dst.v.Dir())
		dst.sec.onWrite = func() {
			dst.sec.onWrite = nil
			cfg := dst.load()
			cfg.Services["other"] = config.Service{Provider: "mail", BaseURL: "https://other.example.test"}
			if err := config.NewStore(dst.path, setupCatalog{}).Save(cfg); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := dst.svc.ApplyImportWith(plan(), ImportOptions{SecretStore: config.CredentialTypeVault}); !errors.Is(err, config.ErrConflict) {
			t.Fatalf("ApplyImportWith() = %v, want a conflict", err)
		}
		if got := treeOf(t, dst.v.Dir()); !reflect.DeepEqual(got, vaultFiles) {
			t.Error("the vault files changed")
		}
		if got, _ := dst.v.Approvals(); len(got) != 3 {
			t.Errorf("approvals = %d, want the three of before", len(got))
		}
	})

	res, err := dst.svc.ApplyImportWith(plan(), ImportOptions{SecretStore: config.CredentialTypeVault})
	if err != nil {
		t.Fatalf("ApplyImportWith() = %v", err)
	}
	if fmt.Sprint(res.Withdrawn) != "[mail mail-extra]" || fmt.Sprint(res.UsedBy) != "[mail-extra]" {
		t.Errorf("Withdrawn = %v, UsedBy = %v", res.Withdrawn, res.UsedBy)
	}
	entryAfter, _, _ := dst.v.CredentialID("mail-key")
	if entryAfter != entryBefore {
		t.Error("the replaced credential's vault entry got a new id")
	}
	if got, _, _, _ := dst.v.Get("mail-key", "api-key", nil); got != secNewMail {
		t.Error("mail-key does not hold the file's secret")
	}
	if got, _, _, _ := dst.v.Get("spare-key", "api-key", nil); got != secOldKeyring {
		t.Error("spare-key, replaced without a secret, lost the target's")
	}
	// The approval given before would still match: the scope and the entry id are unchanged.
	saved := dst.load()
	resolved, err := saved.Resolve("mail", "")
	if err != nil {
		t.Fatal(err)
	}
	if approvedBefore["mail"].Fingerprint != vault.Fingerprint(secret.ScopeOf(resolved), entryAfter) {
		t.Fatal("the old approval no longer matches anyway; the test proves nothing")
	}

	openNames := func(out ImportApproval) string {
		var names []string
		for _, o := range out.Open {
			names = append(names, fmt.Sprintf("%s:%v", o.Connection, o.Imported))
		}
		return strings.Join(names, " ")
	}
	none := dst.svc.ApproveImport(ctx, res, ApprovalNone)
	if len(none.Approved) != 0 || openNames(none) != "mail:true mail-extra:false" || none.Err != nil {
		t.Errorf("ApproveImport(none) = %+v", none)
	}
	direct := dst.svc.ApproveImport(ctx, res, ApprovalDirectOnly)
	if fmt.Sprint(direct.Approved) != "[mail]" || openNames(direct) != "mail-extra:false" || direct.Err != nil {
		t.Errorf("ApproveImport(direct) = %+v", direct)
	}
	if len(direct.Open) == 0 || direct.Open[0].Reason == "" {
		t.Error("the open connection has no reason")
	}
	report, err := approval.Pending(saved, dst.v)
	if err != nil || len(report.Open) != 1 || report.Open[0].Connection != "mail-extra" {
		t.Errorf("open = %+v, %v, want mail-extra alone", report.Open, err)
	}
	noValues(t, "the result", fmt.Sprintf("%+v %+v", res, direct))
	dst.noValuesOutsideVault(t)

	t.Run("a locked vault is refused", func(t *testing.T) {
		locked := vault.New(dst.dir)
		dst.sec.v = locked
		vaultFiles, configBefore := treeOf(t, locked.Dir()), dst.raw()
		_, err := dst.svc.ApplyImportWith(plan(), ImportOptions{SecretStore: config.CredentialTypeVault})
		if err == nil || !strings.Contains(err.Error(), "locked") {
			t.Fatalf("ApplyImportWith() = %v, want the locked vault refused", err)
		}
		if !reflect.DeepEqual(treeOf(t, locked.Dir()), vaultFiles) || string(dst.raw()) != string(configBefore) {
			t.Error("a refused import wrote something")
		}
		if out := dst.svc.ApproveImport(ctx, res, ApprovalDirectOnly); !out.VaultLocked || len(out.Approved) != 0 {
			t.Errorf("ApproveImport() = %+v, want the locked vault reported", out)
		}
	})
}
