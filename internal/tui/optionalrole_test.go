package tui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	canarySiteToken    = "site-token-canary-3b81"
	canarySitePassword = "site-password-canary-9e04"
)

// newOptionalModel is an editor over an empty configuration whose only provider, "site", has a required role
// "token" and an optional role "password".
func newOptionalModel(t *testing.T) (*Model, *config.Store, *secret.MemoryStore) {
	t.Helper()
	reg := capability.NewRegistry()
	if err := reg.RegisterProvider(config.ProviderMetadata{
		ID: "site", Name: "Site",
		SecretRoles: []config.SecretRole{
			{Name: "token", Description: "site token"},
			{Name: "password", Description: "site password", Optional: true},
		},
		Target: config.TargetMetadata{Label: "target"},
	}, nil); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "qatlas")
	store := config.NewStore(filepath.Join(dir, "config.yaml"), reg)
	secrets, mem := newVaultResolver(t, dir)
	m, err := buildModel(store, nil, secrets, nil)
	if err != nil {
		t.Fatal(err)
	}
	return m, store, mem
}

// walkOptionalSetup walks the guided setup up to its credential step, with a new service.
func walkOptionalSetup(t *testing.T, m *Model) {
	t.Helper()
	m.screen = screenNav
	press(t, m, "c", "enter")
	press(t, m, "tab")
	typeText(t, m, "site")
	press(t, m, "tab")
	typeText(t, m, "https://site.example.invalid")
	press(t, m, "f2")
	if m.fail != "" || m.wizard.step != stepCredential {
		t.Fatalf("the service step did not advance: %q", m.fail)
	}
	press(t, m, "tab")
	typeText(t, m, "reader")
}

func TestGuidedSetupLeavesAnOptionalRoleEmpty(t *testing.T) {
	t.Run("keyring", func(t *testing.T) {
		m, store, mem := newOptionalModel(t)
		walkOptionalSetup(t, m)
		press(t, m, "tab", "tab")
		if f := m.fields[m.focus]; f.label != "password" || !strings.Contains(m.fieldHint(f), tr(tFormOptionalRole)) {
			t.Errorf("row %q hint %q, want the optional password row to say it may stay empty", f.label, m.fieldHint(f))
		}
		press(t, m, "tab")
		if f := m.fields[m.focus]; f.label != "token" || strings.Contains(m.fieldHint(f), tr(tFormOptionalRole)) {
			t.Errorf("row %q hint %q, want the required token row without the optional note", f.label, m.fieldHint(f))
		}
		typeText(t, m, canarySiteToken)
		press(t, m, "f2")
		if m.fail != "" || m.wizard.step != stepScope {
			t.Fatalf("the credential step did not advance without the optional role: %q", m.fail)
		}
		press(t, m, "f2", "f2")
		if m.screen != screenSummary {
			t.Fatalf("screen = %v, want the summary", m.screen)
		}
		if summary := screenOf(m); !strings.Contains(summary, "system keyring: token") || strings.Contains(summary, "password") {
			t.Errorf("the summary must name the token alone:\n%s", summary)
		}
		pump(t, m, "f2")
		if m.fail != "" {
			t.Fatalf("save failed: %q", m.fail)
		}
		if got, err := mem.Get(context.Background(), secret.StoreKey("reader", "token")); err != nil || got != canarySiteToken {
			t.Errorf("stored token: %v", err)
		}
		if _, err := mem.Get(context.Background(), secret.StoreKey("reader", "password")); !errors.Is(err, secret.ErrNoEntry) {
			t.Errorf("the empty optional role was stored: %v", err)
		}
		if _, err := store.Load(); err != nil {
			t.Fatalf("Load() = %v", err)
		}
	})

	t.Run("a required role is still missing", func(t *testing.T) {
		m, _, _ := newOptionalModel(t)
		walkOptionalSetup(t, m)
		press(t, m, "tab", "tab")
		typeText(t, m, canarySitePassword)
		press(t, m, "f2")
		if m.wizard.step != stepCredential || !strings.Contains(m.fail, "token") {
			t.Fatalf("step %v, error %q, want the token missing", m.wizard.step, m.fail)
		}
		if strings.Contains(m.fail, canarySitePassword) {
			t.Errorf("the error shows a secret: %q", m.fail)
		}
	})

	t.Run("env", func(t *testing.T) {
		m, store, _ := newOptionalModel(t)
		walkOptionalSetup(t, m)
		press(t, m, "tab")
		selectChoice(t, m, storageEnv)
		press(t, m, "tab", "tab")
		typeText(t, m, "SITE_TOKEN")
		press(t, m, "f2", "f2", "f2")
		pump(t, m, "f2")
		if m.fail != "" {
			t.Fatalf("save failed: %q", m.fail)
		}
		saved, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		if values := saved.Credentials["reader"].Values; len(values) != 1 || values["token"] != "SITE_TOKEN" {
			t.Errorf("values = %v, want the token alone", values)
		}
	})
}

// The credential list says an unset optional role is not set, and a missing required one is missing.
func TestCredentialListMarksAnUnsetOptionalRole(t *testing.T) {
	m, store, _ := newOptionalModel(t)
	cfg := store.New()
	if err := cfg.SetCredential("vars", config.Credential{Provider: "site", Type: config.CredentialTypeEnv,
		Values: map[string]string{"token": "SITE_TOKEN"}}); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetCredential("kept", config.Credential{Provider: "site", Type: config.CredentialTypeVault}); err != nil {
		t.Fatal(err)
	}
	m.cfg = cfg
	m.section = sectionCredentials
	cells := strings.Join(m.cells("kept"), " | ")
	if !strings.Contains(cells, "password "+tr(tSecretOptionalNotSet)) || !strings.Contains(cells, "token "+tr(tSecretMissing)) {
		t.Errorf("cells = %q", cells)
	}
}
