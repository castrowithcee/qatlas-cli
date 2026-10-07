package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/connlog"
	"github.com/castrowithcee/qatlas-cli/internal/manage"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// sharedRoleCatalog has two providers that name their only secret role alike and explain it differently.
type sharedRoleCatalog struct{}

var sharedRoleMetadata = []config.ProviderMetadata{
	{ID: "alpha", Name: "Alpha", Description: "first", SecretRoles: []config.SecretRole{{Name: "token", Description: "ALPHA-ROLE-HELP"}}},
	{ID: "beta", Name: "Beta", Description: "second", SecretRoles: []config.SecretRole{{Name: "token", Description: "BETA-ROLE-HELP"}}},
}

func (sharedRoleCatalog) ProviderMetadata(id string) (config.ProviderMetadata, bool) {
	for _, m := range sharedRoleMetadata {
		if m.ID == id {
			return m, true
		}
	}
	return config.ProviderMetadata{}, false
}

func (sharedRoleCatalog) ProviderMetadataAll() []config.ProviderMetadata { return sharedRoleMetadata }

// TestRoleHelpComesFromTheCredentialsProvider proves the new-credential page, the credential detail page and
// the connection build page each explain a role with the text of its own provider only.
func TestRoleHelpComesFromTheCredentialsProvider(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatalf("write starter config: %v", err)
	}
	store := config.NewStore(path, sharedRoleCatalog{})
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	for name, provider := range map[string]string{"alpha-cred": "alpha", "beta-cred": "beta"} {
		if err := cfg.SetCredential(name, config.Credential{Provider: provider, Type: config.CredentialTypeKeyring}); err != nil {
			t.Fatalf("SetCredential: %v", err)
		}
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("store.Save: %v", err)
	}
	red := &redact.Redactor{}
	resolver := secret.NewWith(func(string) string { return "" }, secret.NewMemoryStore(), nil, red)
	s, err := New(testOverview(), nil, defaultTestAdminTimeout, manage.New(store, resolver, connlog.SurfaceWeb, red), resolver, red, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(s.close)
	cookie, _ := coupleAndApprove(t, s, nil)

	for _, tt := range []struct{ provider, own, foreign string }{
		{"alpha", "ALPHA-ROLE-HELP", "BETA-ROLE-HELP"},
		{"beta", "BETA-ROLE-HELP", "ALPHA-ROLE-HELP"},
	} {
		for page, target := range map[string]string{
			"new credential": "/credentials/new?provider=" + tt.provider,
			"detail":         "/credentials/" + tt.provider + "-cred",
			"connection":     "/connections/new?provider=" + tt.provider,
		} {
			rec := s.request(t, http.MethodGet, target, s.addr, cookie, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s/%s: status = %d, want 200", tt.provider, page, rec.Code)
			}
			body := rec.Body.String()
			if !strings.Contains(body, tt.own) {
				t.Errorf("%s/%s: missing its own role help %q", tt.provider, page, tt.own)
			}
			if strings.Contains(body, tt.foreign) {
				t.Errorf("%s/%s: shows the other provider's role help %q", tt.provider, page, tt.foreign)
			}
		}
	}
}
