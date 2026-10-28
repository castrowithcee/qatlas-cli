package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const optionalConfig = `version: 4
services:
  site:
    provider: site
    base_url: https://site.example.invalid
credentials:
  site-key:
    provider: site
    type: keyring
connections:
  site:
    service: site
    credential: site-key
`

// The source report lists an optional role that is not set as such, not as missing.
func TestConfigValidateSecretsReportsAnUnsetOptionalRole(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(optionalConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	reg := capability.NewRegistry()
	if err := reg.RegisterProvider(config.ProviderMetadata{
		ID: "site", Name: "Site",
		SecretRoles: []config.SecretRole{{Name: "token"}, {Name: "password", Optional: true}},
		Target:      config.TargetMetadata{Label: "target"},
	}, nil); err != nil {
		t.Fatal(err)
	}
	store := secret.NewMemoryStore()
	if err := store.Set(secret.StoreKey("site-key", "token"), canaryStored); err != nil {
		t.Fatal(err)
	}
	opts := testOptionsIn(t, dir, store)

	var stdout, stderr bytes.Buffer
	cmd := newRootCommand(opts, reg)
	code := run(cmd, opts, []string{"config", "validate", "--secrets", "--agent", "--config", configIn(dir)}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("exit code = %d (stderr: %s)", code, stderr.String())
	}
	want := "connection|credential|role|source|checked\n" +
		"site|site-key|token|credential store|environment variable (not set)\n" +
		"site|site-key|password|not set|environment variable (not set), credential store (no entry), plaintext file (not enabled)\n"
	if stdout.String() != want {
		t.Errorf("stdout = %q,\nwant %q", stdout.String(), want)
	}
	if strings.Contains(stdout.String()+stderr.String(), canaryStored) {
		t.Error("a secret reached the output")
	}
}
