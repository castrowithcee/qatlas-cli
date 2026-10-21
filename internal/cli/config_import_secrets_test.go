package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/spf13/pflag"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/connlog"
	"github.com/castrowithcee/qatlas-cli/internal/manage"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

const (
	exportPass = "export-pass-4711"
	vaultPass  = "target-vault-pass"
)

// secretExport writes an export with the secret of wiki-vault through the core, which is the only way to
// make one, and returns its path. With encrypt the file is an age file at a low work factor.
func secretExport(t *testing.T, encrypt bool) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(configIn(dir), []byte(transferConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := vault.New(dir).Set("wiki-vault", "access-token", canaryVault, offeringPassphrase("")); err != nil {
		t.Fatal(err)
	}
	red := &redact.Redactor{}
	secrets := secret.NewWith(os.Getenv, secret.NewMemoryStore(), secret.NewFile(filepath.Join(dir, secret.FileName)), red).WithVault(vault.New(dir), nil)
	svc := manage.New(config.NewStore(configIn(dir), sharedRegistry()), secrets, connlog.SurfaceCLI, red)
	file := filepath.Join(t.TempDir(), "secrets.yaml")
	res, err := svc.ExportConfigWithSecrets(manage.ExportSelection{All: true, Defaults: true}, file,
		manage.SecretExport{Unencrypted: true})
	if err != nil || len(res.SecretsIncluded) != 1 {
		t.Fatalf("ExportConfigWithSecrets() = %+v, %v", res, err)
	}
	if encrypt {
		plain := readFile(t, file)
		recipient, err := age.NewScryptRecipient(exportPass)
		if err != nil {
			t.Fatal(err)
		}
		recipient.SetWorkFactor(2)
		var buf bytes.Buffer
		w, err := age.Encrypt(&buf, recipient)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(plain))
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, buf.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return file
}

// dialog answers the prompts of an import by their text and records which were asked.
type dialog struct {
	t        *testing.T
	approve  bool
	delete   bool
	asked    []string
	exported int
}

func (d *dialog) install() {
	withVaultPassphrase(d.t, func(prompt string) (string, error) {
		d.asked = append(d.asked, prompt)
		switch {
		case strings.Contains(prompt, "export passphrase"):
			d.exported++
			return exportPass, nil
		case strings.Contains(prompt, "vault passphrase"):
			return vaultPass, nil
		}
		return "", nil
	})
	withVaultConfirm(d.t, func(prompt string) (bool, error) {
		d.asked = append(d.asked, prompt)
		switch {
		case strings.HasPrefix(prompt, "approve"):
			return d.approve, nil
		case strings.HasPrefix(prompt, "delete"):
			return d.delete, nil
		}
		return true, nil
	})
}

func (d *dialog) askedFor(text string) bool {
	for _, a := range d.asked {
		if strings.Contains(a, text) {
			return true
		}
	}
	return false
}

func importInto(t *testing.T, dir string, store *secret.MemoryStore, args ...string) (int, string, string) {
	t.Helper()
	opts := testOptionsIn(t, dir, store)
	opts.Secrets = opts.Secrets.WithVault(vault.New(dir), nil)
	return runWithInput(t, opts, "", append([]string{"config", "import"}, append(args, "--config", configIn(dir))...)...)
}

// noSecretValue fails when the canary is in out or in any file below dir but the vault directory.
func noSecretValue(t *testing.T, dir string, out ...string) {
	t.Helper()
	for _, text := range out {
		if strings.Contains(text, canaryVault) || strings.Contains(text, exportPass) {
			t.Errorf("output holds a secret value: %q", text)
		}
	}
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && d.Name() == vault.DirName {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.Contains(readFile(t, path), canaryVault) {
			t.Errorf("%s holds the secret value", path)
		}
		return nil
	})
}

func TestConfigImportSecretsUnencryptedFileIntoNewVault(t *testing.T) {
	withInteractive(t, true)
	file := secretExport(t, false)
	d := &dialog{t: t, delete: true}
	d.install()
	target := transferTarget(t)

	code, stdout, stderr := importInto(t, target, nil, file, "--secret-store", "vault")
	if code != exitOK {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	for _, want := range []string{"credential wiki-vault brings roles: access-token", "warning: the file holds secrets in plain text",
		"go to the vault", "secrets written to the vault for: wiki-vault", "not encrypted; connections are not bound",
		"deleted " + file} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want %q", stdout, want)
		}
	}
	if d.askedFor("approve") {
		t.Error("asked to approve although the vault binds nothing")
	}
	if _, err := os.Stat(file); err == nil {
		t.Error("the import file was not deleted after yes")
	}
	got, found, _, err := vault.New(target).Get("wiki-vault", "access-token", nil)
	if err != nil || !found || got != canaryVault {
		t.Errorf("vault Get() = %q, %v, %v", got, found, err)
	}
	if cfg := loadConfig(t, target); cfg.Credentials["wiki-vault"].Type != config.CredentialTypeVault {
		t.Errorf("credential type = %q", cfg.Credentials["wiki-vault"].Type)
	}
	noSecretValue(t, target, stdout, stderr)
}

func TestConfigImportSecretsKeepsFileOnNoAndYes(t *testing.T) {
	withInteractive(t, true)
	for _, args := range [][]string{{}, {"--yes"}} {
		file := secretExport(t, false)
		d := &dialog{t: t, delete: false}
		d.install()
		code, stdout, stderr := importInto(t, transferTarget(t), secret.NewMemoryStore(), append([]string{file}, args...)...)
		if code != exitOK || !strings.Contains(stdout, "was kept; delete it") {
			t.Fatalf("%v: exit %d, stdout %q, stderr %q", args, code, stdout, stderr)
		}
		if _, err := os.Stat(file); err != nil {
			t.Errorf("%v: the file is gone: %v", args, err)
		}
		if len(args) > 0 && d.askedFor("delete") {
			t.Error("--yes asked to delete")
		}
	}
}

func TestConfigImportSecretsEncryptedFileIntoKeyring(t *testing.T) {
	withInteractive(t, true)
	file := secretExport(t, true)
	d := &dialog{t: t}
	d.install()
	target := transferTarget(t)
	store := secret.NewMemoryStore()

	code, stdout, stderr := importInto(t, target, store, file, "--output", "json")
	if code != exitOK {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	var doc importDocument
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil || !doc.Imported || !doc.SourceEncrypted ||
		doc.SecretStore != config.CredentialTypeKeyring || len(doc.Secrets) != 1 ||
		doc.Secrets[0].Credential != "wiki-vault" || doc.Secrets[0].Roles[0] != "access-token" || doc.SourceDeleted {
		t.Fatalf("document = %+v, %v", doc, err)
	}
	if d.exported != 1 || d.askedFor("delete") {
		t.Errorf("export passphrase asked %d times, asked %q", d.exported, d.asked)
	}
	if got, err := store.Get(context.Background(), secret.StoreKey("wiki-vault", "access-token")); err != nil || got != canaryVault {
		t.Errorf("keyring Get() = %q, %v", got, err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Errorf("an encrypted import file was touched: %v", err)
	}
	if !strings.Contains(stderr, "the file was encrypted") {
		t.Errorf("stderr = %q, want the plan with the encrypted note", stderr)
	}
	noSecretValue(t, target, stdout, stderr)
}

func TestConfigImportSecretsWrongExportPassphraseChangesNothing(t *testing.T) {
	withInteractive(t, true)
	file := secretExport(t, true)
	target := transferTarget(t)
	if err := os.WriteFile(configIn(target), []byte(otherConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	before := readFile(t, configIn(target))
	withVaultPassphrase(t, func(string) (string, error) { return "not-the-one-0815", nil })
	withVaultConfirm(t, func(string) (bool, error) { t.Error("asked a question"); return true, nil })
	store := secret.NewMemoryStore()

	code, stdout, stderr := importInto(t, target, store, file, "--yes", "--secret-store", "vault")
	if code != exitUsage || stdout != "" || !strings.Contains(stderr, "wrong export passphrase") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if readFile(t, configIn(target)) != before {
		t.Error("the configuration changed")
	}
	if _, err := os.Stat(filepath.Join(target, vault.DirName)); err == nil {
		t.Error("a vault was created")
	}
	if _, err := os.Stat(file); err != nil {
		t.Errorf("the file is gone: %v", err)
	}
}

// An encrypted target vault: the approval question, answered both ways.
func TestConfigImportSecretsApprovalQuestion(t *testing.T) {
	withInteractive(t, true)
	for _, approve := range []bool{true, false} {
		file := secretExport(t, true)
		target := transferTarget(t)
		if err := os.WriteFile(configIn(target), []byte(otherConfig), 0o600); err != nil {
			t.Fatal(err)
		}
		seedEncryptedVault(t, target, "other-vault", "access-token", "other-value", vaultPass)
		d := &dialog{t: t, approve: approve}
		d.install()

		code, stdout, stderr := importInto(t, target, secret.NewMemoryStore(), file, "--secret-store", "vault", "--mode", "merge")
		if code != exitOK || !d.askedFor("approve the imported connections now? [y/N]") ||
			!strings.Contains(stdout, "to approve: wiki, ") {
			t.Fatalf("approve=%v: exit %d, asked %q, stdout %q, stderr %q", approve, code, d.asked, stdout, stderr)
		}
		approved := strings.Contains(stdout, "approved: wiki")
		open := strings.Contains(stdout, "open: imported connection wiki")
		if approved != approve || open == approve {
			t.Errorf("approve=%v: stdout %q", approve, stdout)
		}
		if !approve && !strings.Contains(stdout, "qatlas vault approve") {
			t.Errorf("stdout = %q, want the hint to approve later", stdout)
		}
		noSecretValue(t, target, stdout, stderr)
	}
}

func TestConfigImportSecretsYesNeverApproves(t *testing.T) {
	withInteractive(t, true)
	file := secretExport(t, true)
	target := transferTarget(t)
	if err := os.WriteFile(configIn(target), []byte(otherConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	seedEncryptedVault(t, target, "other-vault", "access-token", "other-value", vaultPass)
	d := &dialog{t: t, approve: true}
	d.install()

	code, stdout, stderr := importInto(t, target, secret.NewMemoryStore(), file, "--secret-store", "vault", "--yes")
	if code != exitOK || d.askedFor("approve") || !strings.Contains(stdout, "--yes does not approve") ||
		!strings.Contains(stdout, "open: imported connection wiki") {
		t.Errorf("exit %d, asked %q, stdout %q, stderr %q", code, d.asked, stdout, stderr)
	}
}

func TestConfigImportSecretsRequiresATerminal(t *testing.T) {
	file := secretExport(t, true)
	target := transferTarget(t)
	withInteractive(t, false)
	withVaultPassphrase(t, func(string) (string, error) { t.Error("asked for a passphrase"); return "", nil })
	code, stdout, stderr := importInto(t, target, nil, file, "--yes")
	if code != exitUsage || stdout != "" || !strings.Contains(stderr, "admin-required") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if _, err := os.Stat(configIn(target)); err == nil {
		t.Error("a configuration was written")
	}
}

func TestConfigImportRejectsUnknownSecretStore(t *testing.T) {
	withInteractive(t, true)
	code, _, stderr := importInto(t, transferTarget(t), nil, secretExport(t, false), "--secret-store", "file")
	if code != exitUsage || !strings.Contains(stderr, `unknown secret store "file"`) {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
}

func TestConfigCLIOffersNoExportWithSecrets(t *testing.T) {
	root := DocumentationCommand("test")
	cmd, _, err := root.Find([]string{"config", "export"})
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"all": true, "service": true, "credential": true, "connection": true,
		"defaults": true, "provider-notes": true, "dry-run": true}
	cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
		if !allowed[f.Name] {
			t.Errorf("export has the unexpected flag --%s", f.Name)
		}
	})
	if !strings.Contains(cmd.Long, "no option to\nexport secrets") {
		t.Error("the export help does not say that it has no secrets option")
	}
	if imp, _, _ := root.Find([]string{"config", "import"}); imp.Flags().Lookup("secret-store") == nil {
		t.Error("import has no --secret-store")
	}
	config, _, _ := root.Find([]string{"config"})
	for _, sub := range config.Commands() {
		if strings.Contains(sub.Name(), "secret") {
			t.Errorf("config has the subcommand %s", sub.Name())
		}
	}
}
