package secretcommit

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

type recordingSecrets struct{ written []string }

func (s *recordingSecrets) Set(credential, role, _ string) error {
	s.written = append(s.written, credential+"/"+role)
	return nil
}
func (s *recordingSecrets) Delete(string, string) ([]secret.Source, error) { return nil, nil }
func (s *recordingSecrets) Vault() *vault.Vault                            { return nil }
func (s *recordingSecrets) SetVault(string, string, string, vault.PassphraseFunc) error {
	return nil
}
func (s *recordingSecrets) DeleteVault(string, string) error { return nil }

// A field value of a payload secret under four characters is refused before anything is written or saved.
func TestCommitRefusesShortForwardValues(t *testing.T) {
	cfg := config.New()
	cfg.Credentials["shared"] = config.Credential{Type: config.CredentialTypeKeyring, Forward: true,
		Fields: []string{"user", "pass"}}
	store := config.NewStore(filepath.Join(t.TempDir(), "config.yaml"))
	secrets := &recordingSecrets{}

	_, err := Commit(store, secrets, cfg, "shared", false, []string{"user", "pass"},
		map[string]string{"user": "long-enough", "pass": "abc"}, nil)
	if err == nil || !strings.Contains(err.Error(), "pass") || strings.Contains(err.Error(), "abc") {
		t.Fatalf("Commit() = %v, want a refusal naming the field and no value", err)
	}
	if len(secrets.written) != 0 {
		t.Errorf("Commit() wrote %v before refusing", secrets.written)
	}

	if _, err := Commit(store, secrets, cfg, "shared", false, []string{"user", "pass"},
		map[string]string{"user": "long-enough", "pass": "abcd"}, nil); err != nil {
		t.Fatalf("Commit() = %v, want success for values of four characters", err)
	}
	if len(secrets.written) != 2 {
		t.Errorf("written = %v, want both fields", secrets.written)
	}
}
