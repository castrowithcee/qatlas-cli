package web

import (
	"errors"
	"net/http"
	"net/url"
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

const (
	canarySiteToken    = "canary-site-token-62f0"
	canarySitePassword = "canary-site-password-a3d9"
)

// siteCatalog has one provider, "site", with a required role "token" and an optional role "password".
type siteCatalog struct{}

var siteMetadata = config.ProviderMetadata{
	ID: "site", Name: "Site", Description: "a synthetic test provider",
	SecretRoles: []config.SecretRole{
		{Name: "token", Description: "the site token"},
		{Name: "password", Description: "the site password", Optional: true},
	},
	Tools: []config.ToolMetadata{{ID: "site.read", Title: "Read", Effect: config.EffectRead}},
}

func (siteCatalog) ProviderMetadata(id string) (config.ProviderMetadata, bool) {
	return siteMetadata, id == siteMetadata.ID
}

func (siteCatalog) ProviderMetadataAll() []config.ProviderMetadata {
	return []config.ProviderMetadata{siteMetadata}
}

func newSiteTestServer(t *testing.T) (*Server, *config.Store, *secret.MemoryStore) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := config.NewStore(path, siteCatalog{})
	mem := secret.NewMemoryStore()
	red := &redact.Redactor{}
	resolver := secret.NewWith(func(string) string { return "" }, mem, nil, red)
	s, err := New(testOverview(), nil, manage.New(store, resolver, connlog.SurfaceWeb, red), resolver, red, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(s.close)
	return s, store, mem
}

func assertNotStored(t *testing.T, mem *secret.MemoryStore, credential, role string) {
	t.Helper()
	if _, err := mem.Get(t.Context(), secret.StoreKey(credential, role)); !errors.Is(err, secret.ErrNoEntry) {
		t.Errorf("%s/%s was stored: %v", credential, role, err)
	}
}

func TestCreateCredentialLeavesAnOptionalRoleEmpty(t *testing.T) {
	s, store, mem := newSiteTestServer(t)
	cookie, csrf := coupleAndApprove(t, s, nil)

	form := s.request(t, http.MethodGet, "/credentials/new?provider=site", s.addr, cookie, nil)
	body := form.Body.String()
	if !strings.Contains(body, "<legend>password (optional)</legend>") || strings.Contains(body, "token (optional)") {
		t.Fatalf("the form does not mark the optional role alone:\n%s", body)
	}
	cfgver := extractHiddenValue(t, body, "cfgver")

	rec := s.postForm(t, "/credentials/new", s.addr, cookie, "http://"+s.addr, csrf, url.Values{
		"provider": {"site"}, "name": {"site-key"}, "storage": {"keyring"}, "cfgver": {cfgver},
		"secret_token": {canarySiteToken}, "secret_password": {""},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /credentials/new status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if got, err := mem.Get(t.Context(), secret.StoreKey("site-key", "token")); err != nil || got != canarySiteToken {
		t.Fatalf("stored token: %v", err)
	}
	assertNotStored(t, mem, "site-key", "password")

	detail := s.request(t, http.MethodGet, "/credentials/site-key", s.addr, cookie, nil).Body.String()
	if !strings.Contains(detail, "password (optional)") || strings.Contains(detail, canarySiteToken) {
		t.Errorf("the detail page does not mark the optional role, or shows a secret:\n%s", detail)
	}

	form = s.request(t, http.MethodGet, "/credentials/new?provider=site", s.addr, cookie, nil)
	rec = s.postForm(t, "/credentials/new", s.addr, cookie, "http://"+s.addr, csrf, url.Values{
		"provider": {"site"}, "name": {"site-vars"}, "storage": {"env"},
		"cfgver": {extractHiddenValue(t, form.Body.String(), "cfgver")}, "envname_token": {"SITE_TOKEN"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /credentials/new env status = %d, body: %s", rec.Code, rec.Body.String())
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if values := cfg.Credentials["site-vars"].Values; len(values) != 1 || values["token"] != "SITE_TOKEN" {
		t.Errorf("values = %v, want the token alone", values)
	}

	form = s.request(t, http.MethodGet, "/credentials/new?provider=site", s.addr, cookie, nil)
	rec = s.postForm(t, "/credentials/new", s.addr, cookie, "http://"+s.addr, csrf, url.Values{
		"provider": {"site"}, "name": {"site-half"}, "storage": {"keyring"},
		"cfgver": {extractHiddenValue(t, form.Body.String(), "cfgver")}, "secret_password": {canarySitePassword},
	})
	if rec.Code == http.StatusSeeOther || !strings.Contains(rec.Body.String(), "token is empty") ||
		strings.Contains(rec.Body.String(), canarySitePassword) {
		t.Errorf("a missing required role was not refused cleanly: %d\n%s", rec.Code, rec.Body.String())
	}
	assertNotStored(t, mem, "site-half", "password")
}

func TestGuidedConnectionLeavesAnOptionalRoleEmpty(t *testing.T) {
	setup := func(storage string) url.Values {
		return url.Values{
			"provider": {"site"}, "service": {newServiceChoice}, "svcname": {"site-a"},
			"svcbaseurl": {"https://site.example.test"},
			"credential": {newCredentialChoice}, "credname": {"site-key"}, "credstorage": {storage},
			"connname": {"site-conn"}, "access": {"effects"}, "permmode": {"custom"}, "perm": {"read"},
		}
	}
	save := func(t *testing.T, s *Server, cookie *http.Cookie, csrf string, form url.Values, extra url.Values) {
		t.Helper()
		review := s.request(t, http.MethodGet, "/connections/new/review?"+form.Encode(), s.addr, cookie, nil)
		if review.Code != http.StatusOK {
			t.Fatalf("GET review status = %d, body: %s", review.Code, review.Body.String())
		}
		form.Set("cfgver", extractHiddenValue(t, review.Body.String(), "cfgver"))
		for key, values := range extra {
			form[key] = values
		}
		rec := s.postForm(t, "/connections/new/review", s.addr, cookie, "http://"+s.addr, csrf, form)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("POST review status = %d, body: %s", rec.Code, rec.Body.String())
		}
	}

	t.Run("keyring", func(t *testing.T) {
		s, store, mem := newSiteTestServer(t)
		cookie, csrf := coupleAndApprove(t, s, nil)
		save(t, s, cookie, csrf, setup("keyring"), url.Values{
			"secret_token": {canarySiteToken}, "secret_password": {""},
		})
		if got, err := mem.Get(t.Context(), secret.StoreKey("site-key", "token")); err != nil || got != canarySiteToken {
			t.Fatalf("stored token: %v", err)
		}
		assertNotStored(t, mem, "site-key", "password")
		if cfg, err := store.Load(); err != nil || cfg.Connections["site-conn"].Credential != "site-key" {
			t.Fatalf("the connection was not saved: %v", err)
		}
	})

	t.Run("env", func(t *testing.T) {
		s, store, _ := newSiteTestServer(t)
		cookie, csrf := coupleAndApprove(t, s, nil)
		form := setup("env")
		form.Set("envname_token", "SITE_TOKEN")
		form.Set("envname_password", "")
		save(t, s, cookie, csrf, form, nil)
		cfg, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		if values := cfg.Credentials["site-key"].Values; len(values) != 1 || values["token"] != "SITE_TOKEN" {
			t.Errorf("values = %v, want the token alone", values)
		}
	})
}
