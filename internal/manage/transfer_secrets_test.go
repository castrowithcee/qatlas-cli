package manage

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

const (
	secKeyring = "keyring-secret-value-7f3a"
	secVault   = "vault-secret-value-91bc"
	secForward = "forward-secret-value-02de"
	secVaultPP = "vault-passphrase-4411"
	secExportP = "export-passphrase-8c20"
)

func asking(p string) vault.PassphraseFunc { return func(string) (string, error) { return p, nil } }

// secretsWorld is a source installation with a keyring secret and unencrypted vault secrets.
func secretsWorld(t *testing.T) *transferWorld {
	t.Helper()
	w := newTransferWorld(t, transferSource)
	w.secrets.entries["mail-key.api-key"] = secKeyring
	v := vault.New(w.dir)
	w.secrets.vault = v
	if err := v.Set("spare-key", "api-key", secVault, asking("")); err != nil {
		t.Fatal(err)
	}
	if err := v.Set("payload", "user", secForward, asking("")); err != nil {
		t.Fatal(err)
	}
	return w
}

func exportWithSecrets(t *testing.T, w *transferWorld, opts SecretExport) (string, *ExportResult) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "export.yaml")
	res, err := w.svc.ExportConfigWithSecrets(ExportSelection{All: true, Defaults: true}, out, opts)
	if err != nil {
		t.Fatalf("ExportConfigWithSecrets() = %v", err)
	}
	return out, res
}

func noValues(t *testing.T, what string, text string) {
	t.Helper()
	for _, v := range []string{secKeyring, secVault, secForward, secVaultPP, secExportP} {
		if strings.Contains(text, v) {
			t.Errorf("%s contains %q", what, v)
		}
	}
}

func TestExportWithSecretsUnencrypted(t *testing.T) {
	w := secretsWorld(t)
	out, res := exportWithSecrets(t, w, SecretExport{Unencrypted: true})
	data, _ := os.ReadFile(out)
	for _, v := range []string{secKeyring, secVault, secForward, "format_version: 2"} {
		if !bytes.Contains(data, []byte(v)) {
			t.Errorf("the file lacks %q", v)
		}
	}
	if res.Encrypted || len(res.Warnings) != 1 {
		t.Errorf("Encrypted = %v, Warnings = %v", res.Encrypted, res.Warnings)
	}
	want := []ExportedSecret{{"mail-key", []string{"api-key"}}, {"payload", []string{"user"}}, {"spare-key", []string{"api-key"}}}
	if fmt.Sprint(res.SecretsIncluded) != fmt.Sprint(want) {
		t.Errorf("SecretsIncluded = %v, want %v", res.SecretsIncluded, want)
	}
	noValues(t, "the result", fmt.Sprintf("%+v", res))
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(out); info.Mode().Perm() != 0o600 {
			t.Errorf("mode = %v", info.Mode().Perm())
		}
	}
	if _, err := w.svc.ExportConfigWithSecrets(ExportSelection{All: true}, out, SecretExport{Unencrypted: true}); err == nil {
		t.Error("an existing file was replaced")
	}
}

func TestExportWithSecretsEncryptedAndImport(t *testing.T) {
	w := secretsWorld(t)
	out, res := exportWithSecrets(t, w, SecretExport{ExportPassphrase: asking(secExportP)})
	if !res.Encrypted || len(res.Warnings) != 0 {
		t.Errorf("Encrypted = %v, Warnings = %v", res.Encrypted, res.Warnings)
	}
	data, _ := os.ReadFile(out)
	noValues(t, "the encrypted file", string(data))
	id, _ := age.NewScryptIdentity(secExportP)
	r, err := age.Decrypt(bytes.NewReader(data), id)
	if err != nil {
		t.Fatal(err)
	}
	plain := new(bytes.Buffer)
	plain.ReadFrom(r)
	if !strings.Contains(plain.String(), secKeyring) || !strings.HasPrefix(plain.String(), "format: ") {
		t.Error("age does not yield the YAML export")
	}

	dst := newTransferWorld(t, "")
	plan, err := dst.svc.PlanImportFile(out, ImportModeCreate, asking(secExportP))
	if err != nil {
		t.Fatalf("PlanImportFile() = %v", err)
	}
	if !plan.SourceEncrypted || fmt.Sprint(plan.SecretRoles) != "map[mail-key:[api-key] payload:[user] spare-key:[api-key]]" {
		t.Errorf("plan = encrypted %v, roles %v", plan.SourceEncrypted, plan.SecretRoles)
	}
	for _, name := range []string{"mail-key", "payload", "spare-key"} {
		for _, list := range [][]string{plan.CredentialsWithoutSecret, plan.CredentialsKeepSecret} {
			for _, n := range list {
				if n == name {
					t.Errorf("%s is listed as without secret", name)
				}
			}
		}
	}
	noValues(t, "plan %+v", fmt.Sprintf("%+v", plan))
	noValues(t, "plan %v of value", fmt.Sprintf("%v %#v %+v", *plan, *plan, *plan))
	if _, err := dst.svc.ApplyImport(plan); err == nil || len(dst.raw()) != 0 {
		t.Errorf("ApplyImport() = %v, want a refusal that writes nothing", err)
	}
}

func TestPlanImportReadsPlainFileWithSecrets(t *testing.T) {
	out, _ := exportWithSecrets(t, secretsWorld(t), SecretExport{Unencrypted: true})
	dst := newTransferWorld(t, targetConfig)
	plan, err := dst.svc.PlanImport(out, ImportModeMerge)
	if err != nil {
		t.Fatal(err)
	}
	if plan.SourceEncrypted || len(plan.SecretRoles) == 0 {
		t.Errorf("plan = %v %v", plan.SourceEncrypted, plan.SecretRoles)
	}
}

func TestPlanImportSkippedCredentialBringsNoSecret(t *testing.T) {
	out, _ := exportWithSecrets(t, secretsWorld(t), SecretExport{Unencrypted: true})
	dst := newTransferWorld(t, "version: 4\ncredentials:\n    mail-key:\n        provider: mail\n        type: keyring\n")
	plan, err := dst.svc.PlanImport(out, ImportModeMerge)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := plan.SecretRoles["mail-key"]; ok {
		t.Error("a skipped credential brings a secret")
	}
}

func TestExportWithSecretsVaultEncryptedAndMissing(t *testing.T) {
	w := newTransferWorld(t, transferSource)
	v := vault.New(w.dir)
	w.secrets.vault = v
	if err := v.Set("spare-key", "api-key", secVault, asking(secVaultPP)); err != nil {
		t.Fatal(err)
	}
	locked := vault.New(w.dir)
	w.secrets.vault = locked
	out, res := exportWithSecrets(t, w, SecretExport{Unencrypted: true, VaultPassphrase: asking(secVaultPP)})
	if fmt.Sprint(res.SecretsIncluded) != "[{spare-key [api-key]}]" {
		t.Errorf("SecretsIncluded = %v", res.SecretsIncluded)
	}
	if fmt.Sprint(res.SecretsMissing) != "[mail-key payload.user]" {
		t.Errorf("SecretsMissing = %v", res.SecretsMissing)
	}
	if data, _ := os.ReadFile(out); strings.Contains(string(data), secVaultPP) {
		t.Error("the vault passphrase is in the file")
	}
	// A wrong vault passphrase fails the export and leaves no file.
	w.secrets.vault = vault.New(w.dir)
	bad := filepath.Join(t.TempDir(), "bad.yaml")
	if _, err := w.svc.ExportConfigWithSecrets(ExportSelection{All: true}, bad, SecretExport{Unencrypted: true, VaultPassphrase: asking("nope")}); err == nil {
		t.Error("a wrong vault passphrase exported")
	}
	if _, err := os.Stat(bad); err == nil {
		t.Error("a partial file remains")
	}
}

func TestExportWithSecretsRefusals(t *testing.T) {
	w := secretsWorld(t)
	out := filepath.Join(t.TempDir(), "e.yaml")
	sel := ExportSelection{All: true}
	for name, opts := range map[string]SecretExport{
		"neither":      {},
		"both":         {Unencrypted: true, ExportPassphrase: asking("x")},
		"empty phrase": {ExportPassphrase: asking("")},
	} {
		if _, err := w.svc.ExportConfigWithSecrets(sel, out, opts); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("a refused export wrote a file")
	}
	if _, err := w.svc.ExportConfigWithSecrets(sel, "", SecretExport{Unencrypted: true}); err == nil {
		t.Error("a preview with secrets was accepted")
	}
	// A keyring that cannot be read is an error, not a missing secret.
	w.secrets.readErr["api-key"] = errors.New("locked")
	if _, err := w.svc.ExportConfigWithSecrets(sel, out, SecretExport{Unencrypted: true}); err == nil {
		t.Error("an unreadable keyring exported")
	}
}

// lowCostEncrypted is an age file made with a cheap work factor, as the decryption honours the file's own.
func lowCostEncrypted(t *testing.T, plain, phrase string) string {
	t.Helper()
	r, err := age.NewScryptRecipient(phrase)
	if err != nil {
		t.Fatal(err)
	}
	r.SetWorkFactor(4)
	var buf bytes.Buffer
	w, _ := age.Encrypt(&buf, r)
	w.Write([]byte(plain))
	w.Close()
	file := filepath.Join(t.TempDir(), "enc.age")
	if err := os.WriteFile(file, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestImportEncryptedFileFailures(t *testing.T) {
	out, _ := exportWithSecrets(t, secretsWorld(t), SecretExport{Unencrypted: true})
	text, _ := os.ReadFile(out)
	enc := lowCostEncrypted(t, string(text), secExportP)
	dst := newTransferWorld(t, targetConfig)
	before := dst.raw()
	_, err := dst.svc.PlanImportFile(enc, ImportModeMerge, asking("wrong-phrase"))
	if !errors.Is(err, vault.ErrWrongExportPassphrase) {
		t.Errorf("wrong passphrase = %v", err)
	}
	noValues(t, "error", err.Error())
	if _, err := dst.svc.PlanImport(enc, ImportModeMerge); err == nil || !strings.Contains(err.Error(), "encrypted") {
		t.Errorf("no passphrase function = %v", err)
	}
	if _, err := dst.svc.PlanImportFile(enc, ImportModeMerge, asking("")); err == nil {
		t.Error("an empty passphrase was accepted")
	}
	if !bytes.Equal(before, dst.raw()) {
		t.Error("a refused file changed the configuration")
	}
	if _, err := dst.svc.PlanImportFile(enc, ImportModeMerge, asking(secExportP)); err != nil {
		t.Errorf("right passphrase = %v", err)
	}
}

func TestImportRefusesBadSecretsSections(t *testing.T) {
	out, _ := exportWithSecrets(t, secretsWorld(t), SecretExport{Unencrypted: true})
	raw, _ := os.ReadFile(out)
	good := string(raw)
	// withSecrets is a small version 2 file whose secrets section is entry.
	withSecrets := func(entry string) string {
		return "format: " + TransferFormat + "\nformat_version: 2\ncredentials:\n    wiki-env:\n        provider: docs\n        type: env\n" +
			"        values:\n            key-id: A\n            key-secret: B\n" +
			"    spare-key:\n        provider: mail\n        type: vault\n" +
			"    payload:\n        type: vault\n        forward: true\n        fields:\n            - user\nsecrets:\n" + entry
	}
	tests := []struct{ name, text, want string }{
		{"secrets in version 1", strings.Replace(good, "format_version: 2", "format_version: 1", 1), "unknown key"},
		{"version 2 without secrets", "format: " + TransferFormat + "\nformat_version: 2\n", "no secrets"},
		{"unknown version", strings.Replace(good, "format_version: 2", "format_version: 3", 1), "format version 3"},
		{"env credential", withSecrets("    wiki-env:\n        key-id: LEAKED-VALUE-1\n"), "holds no secret"},
		{"credential not in file", withSecrets("    gone:\n        api-key: LEAKED-VALUE-2\n"), "no such credential"},
		{"foreign role", withSecrets("    spare-key:\n        bogus-role: LEAKED-VALUE-3\n"), "not a secret role"},
		{"forward field", withSecrets("    payload:\n        other: LEAKED-VALUE-4\n"),
			"not a field"},
		{"short forward value", withSecrets("    payload:\n        user: x\n"), "shorter than"},
		{"empty value", withSecrets("    spare-key:\n        api-key: \"\"\n"), "empty"},
	}
	dst := newTransferWorld(t, targetConfig)
	before := dst.raw()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "in.yaml")
			os.WriteFile(file, []byte(tt.text), 0o600)
			_, err := dst.svc.PlanImport(file, ImportModeMerge)
			var refused *ImportRefusedError
			if !errors.As(err, &refused) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("PlanImport() = %v, want a refusal containing %q", err, tt.want)
			}
			if strings.Contains(err.Error(), "LEAKED-VALUE") {
				t.Errorf("the error quotes a value: %v", err)
			}
			if !bytes.Equal(before, dst.raw()) {
				t.Error("the configuration changed")
			}
		})
	}
}

func TestExportWithoutSecretsStaysVersion1(t *testing.T) {
	path, _ := exported(t, ExportSelection{All: true})
	data, _ := os.ReadFile(path)
	if !bytes.Contains(data, []byte("format_version: 1")) || bytes.Contains(data, []byte("\nsecrets:")) {
		t.Error("an export without secrets is not a version 1 file")
	}
}
