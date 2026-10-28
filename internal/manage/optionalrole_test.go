package manage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/connlog"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// optionalCatalog has one provider, "site", with a required role "token" and an optional role "password".
type optionalCatalog struct{}

var optionalProvider = config.ProviderMetadata{
	ID: "site", DefaultBaseURL: "https://site.example",
	SecretRoles: []config.SecretRole{{Name: "token"}, {Name: "password", Optional: true}},
	Tools:       []config.ToolMetadata{{ID: "site.read", Effect: config.EffectRead}},
}

func (optionalCatalog) ProviderMetadata(id string) (config.ProviderMetadata, bool) {
	return optionalProvider, id == "site"
}

func (optionalCatalog) ProviderMetadataAll() []config.ProviderMetadata {
	return []config.ProviderMetadata{optionalProvider}
}

const (
	secToken    = "site-token-value-51aa"
	secPassword = "site-password-value-d7e2"
)

type optionalFixture struct {
	svc     *Service
	secrets *fakeSecrets
	store   *config.Store
	cfg     *config.Config
	rev     config.Revision
	dir     string
}

func newOptionalFixture(t *testing.T) *optionalFixture {
	t.Helper()
	dir := t.TempDir()
	store := config.NewStore(filepath.Join(dir, "config.yaml"), optionalCatalog{})
	cfg := store.New()
	if err := cfg.SetService("site-a", config.Service{Provider: "site", BaseURL: "https://a.site.example"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	f := &optionalFixture{secrets: newFakeSecrets(), store: store, dir: dir}
	f.svc = New(store, f.secrets, connlog.SurfaceWeb, &redact.Redactor{}).WithVaultProcessSupport(false)
	var err error
	if f.cfg, f.rev, err = f.svc.Load(); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *optionalFixture) saved(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := f.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestCreateCredentialLeavesAnOptionalRoleEmpty(t *testing.T) {
	for _, storage := range []string{config.CredentialTypeKeyring, config.CredentialTypeVault} {
		t.Run(storage, func(t *testing.T) {
			f := newOptionalFixture(t)
			_, err := f.svc.CreateCredential(f.cfg, f.rev, NewCredential{Name: "key", Provider: "site", Type: storage,
				Secrets: map[string]string{"token": secToken, "password": ""}})
			if err != nil {
				t.Fatalf("CreateCredential() = %v", err)
			}
			if fmt.Sprint(f.secrets.entries) != fmt.Sprintf("map[key.token:%s]", secToken) {
				t.Errorf("entries = %v, want the token alone", f.secrets.entries)
			}
			if _, ok := f.saved(t).Credentials["key"]; !ok {
				t.Error("the credential was not saved")
			}
		})
	}

	t.Run("keyring with the optional role", func(t *testing.T) {
		f := newOptionalFixture(t)
		_, err := f.svc.CreateCredential(f.cfg, f.rev, NewCredential{Name: "key", Provider: "site",
			Type: config.CredentialTypeKeyring, Secrets: map[string]string{"token": secToken, "password": secPassword}})
		if err != nil || f.secrets.entries["key.password"] != secPassword {
			t.Fatalf("CreateCredential() = %v, entries %v", err, f.secrets.entries)
		}
	})

	t.Run("env", func(t *testing.T) {
		f := newOptionalFixture(t)
		_, err := f.svc.CreateCredential(f.cfg, f.rev, NewCredential{Name: "key", Provider: "site",
			Type: config.CredentialTypeEnv, EnvNames: map[string]string{"token": "SITE_TOKEN", "password": ""}})
		if err != nil {
			t.Fatalf("CreateCredential() = %v", err)
		}
		if got := f.saved(t).Credentials["key"].Values; fmt.Sprint(got) != "map[token:SITE_TOKEN]" {
			t.Errorf("values = %v, want no entry for the optional role", got)
		}
	})

	t.Run("a required role is still missing", func(t *testing.T) {
		f := newOptionalFixture(t)
		var missing *MissingValueError
		_, err := f.svc.CreateCredential(f.cfg, f.rev, NewCredential{Name: "key", Provider: "site",
			Type: config.CredentialTypeKeyring, Secrets: map[string]string{"password": secPassword}})
		if !errors.As(err, &missing) || missing.Role != "token" || len(f.secrets.entries) != 0 {
			t.Fatalf("CreateCredential() = %v, entries %v, want token missing and nothing written", err, f.secrets.entries)
		}
	})
}

func TestConnectionSetupLeavesAnOptionalRoleEmpty(t *testing.T) {
	draft := func(storage string) ConnectionDraft {
		return ConnectionDraft{Provider: "site", Service: "site-a", NewCredential: true, Credential: "key",
			Storage: storage, Name: "mine"}
	}

	t.Run("keyring", func(t *testing.T) {
		f := newOptionalFixture(t)
		cand, err := BuildConnectionCandidate(f.cfg, draft(config.CredentialTypeKeyring), StageRights)
		if err != nil {
			t.Fatal(err)
		}
		if err := cand.MissingSecret(map[string]string{"token": secToken}); err != nil {
			t.Errorf("MissingSecret() = %v, want nil", err)
		}
		var missing *RoleMissingError
		if err := cand.MissingSecret(map[string]string{"password": secPassword}); !errors.As(err, &missing) || missing.Role != "token" {
			t.Errorf("MissingSecret() = %v, want token missing", err)
		}
		if _, err := f.svc.SaveConnectionSetup(f.cfg, cand, f.rev, map[string]string{"token": secToken, "password": ""}, nil); err != nil {
			t.Fatalf("SaveConnectionSetup() = %v", err)
		}
		if fmt.Sprint(f.secrets.entries) != fmt.Sprintf("map[key.token:%s]", secToken) || len(f.secrets.sets) != 1 {
			t.Errorf("entries = %v, sets = %v, want the token alone", f.secrets.entries, f.secrets.sets)
		}
		if _, ok := f.saved(t).Connections["mine"]; !ok {
			t.Error("the connection was not saved")
		}
	})

	t.Run("env", func(t *testing.T) {
		f := newOptionalFixture(t)
		d := draft(config.CredentialTypeEnv)
		d.EnvNames = map[string]string{"token": "SITE_TOKEN", "password": ""}
		cand, err := BuildConnectionCandidate(f.cfg, d, StageRights)
		if err != nil {
			t.Fatalf("BuildConnectionCandidate() = %v", err)
		}
		if got := cand.Config.Credentials["key"].Values; fmt.Sprint(got) != "map[token:SITE_TOKEN]" {
			t.Errorf("values = %v, want no entry for the optional role", got)
		}
		d.EnvNames = map[string]string{"token": "SITE_TOKEN", "password": "SITE_PASS"}
		if cand, err = BuildConnectionCandidate(f.cfg, d, StageRights); err != nil ||
			cand.Config.Credentials["key"].Values["password"] != "SITE_PASS" {
			t.Errorf("BuildConnectionCandidate() = %v, values %v", err, cand.Config.Credentials["key"].Values)
		}
		d.EnvNames = map[string]string{"token": "", "password": "SITE_PASS"}
		if _, err := BuildConnectionCandidate(f.cfg, d, StageRights); !hasProblem(err, config.CodeInvalidEnvName) {
			t.Errorf("BuildConnectionCandidate() = %v, want the empty required name refused", err)
		}
	})
}

func hasProblem(err error, code string) bool {
	for _, p := range config.Problems(err) {
		if p.Code == code {
			return true
		}
	}
	return false
}

// A draft read from an unlocked encrypted vault needs the required roles stored, not the optional one.
func TestDraftSecretsSkipAnUnsetOptionalRole(t *testing.T) {
	draft := func(t *testing.T, stored map[string]string) (*optionalFixture, *config.Resolved) {
		t.Helper()
		f := newOptionalFixture(t)
		dir := t.TempDir()
		// An unrelated entry creates the encrypted vault even where no role of key is stored.
		if err := vault.New(dir).Set("other", "token", secToken, asking(secVaultPP)); err != nil {
			t.Fatal(err)
		}
		for role, value := range stored {
			if err := vault.New(dir).Set("key", role, value, asking(secVaultPP)); err != nil {
				t.Fatal(err)
			}
		}
		v := vault.New(dir)
		if err := VerifyAdmin(v, secVaultPP); err != nil {
			t.Fatal(err)
		}
		f.secrets.vault = v
		if err := f.cfg.SetCredential("key", config.Credential{Provider: "site", Type: config.CredentialTypeVault}); err != nil {
			t.Fatal(err)
		}
		return f, &config.Resolved{Provider: "site", Credential: "key", Secrets: f.cfg.Credentials["key"]}
	}

	t.Run("the optional role not stored", func(t *testing.T) {
		f, resolved := draft(t, map[string]string{"token": secToken})
		r, err := f.svc.draftSecrets(f.cfg, false, resolved)
		if err != nil || r == nil {
			t.Fatalf("draftSecrets() = %v, %v", r, err)
		}
		if got, err := r.Resolve(t.Context(), "key", resolved.Secrets, "token"); err != nil || got.Secret != secToken {
			t.Fatalf("Resolve(token) = %v", err)
		}
		if _, found, err := r.ResolveOptional(t.Context(), "key", resolved.Secrets, "password"); err != nil || found {
			t.Errorf("ResolveOptional(password) = %v, %v, want not set", found, err)
		}
	})

	t.Run("the optional role stored", func(t *testing.T) {
		f, resolved := draft(t, map[string]string{"token": secToken, "password": secPassword})
		r, err := f.svc.draftSecrets(f.cfg, false, resolved)
		if err != nil || r == nil {
			t.Fatalf("draftSecrets() = %v, %v", r, err)
		}
		got, found, err := r.ResolveOptional(t.Context(), "key", resolved.Secrets, "password")
		if err != nil || !found || got.Secret != secPassword {
			t.Errorf("ResolveOptional(password) = %v, %v", found, err)
		}
	})

	t.Run("the required role not stored", func(t *testing.T) {
		f, resolved := draft(t, map[string]string{"password": secPassword})
		if _, err := f.svc.draftSecrets(f.cfg, false, resolved); choiceCode(err) != "unsaved-secret" {
			t.Errorf("draftSecrets() = %v, want unsaved-secret", err)
		}
	})
}

const optionalTransferSource = `version: 4
services:
    site-a:
        provider: site
        base_url: https://a.site.example
credentials:
    full:
        provider: site
        type: keyring
    bare:
        provider: site
        type: keyring
connections:
    full:
        service: site-a
        credential: full
    bare:
        service: site-a
        credential: bare
`

// An export carries an optional role that is stored and leaves out one that is not; an import brings it
// like any other role.
func TestTransferCarriesAnOptionalRole(t *testing.T) {
	src := newOptionalTransferWorld(t, optionalTransferSource)
	src.secrets.entries["full.token"] = secToken
	src.secrets.entries["full.password"] = secPassword
	src.secrets.entries["bare.token"] = secToken
	out := filepath.Join(t.TempDir(), "export.yaml")
	res, err := src.svc.ExportConfigWithSecrets(ExportSelection{All: true}, out, SecretExport{Unencrypted: true})
	if err != nil {
		t.Fatalf("ExportConfigWithSecrets() = %v", err)
	}
	if got := fmt.Sprint(res.SecretsIncluded); got != "[{bare [token]} {full [password token]}]" {
		t.Errorf("SecretsIncluded = %s", got)
	}
	if len(res.SecretsMissing) != 0 {
		t.Errorf("SecretsMissing = %v, want none", res.SecretsMissing)
	}

	dst := newOptionalTransferWorld(t, "")
	plan, err := dst.svc.PlanImport(out, ImportModeCreate)
	if err != nil {
		t.Fatalf("PlanImport() = %v", err)
	}
	if got := fmt.Sprint(plan.SecretRoles); got != "map[bare:[token] full:[password token]]" {
		t.Errorf("SecretRoles = %s", got)
	}
	if _, err := dst.svc.ApplyImportWith(plan, ImportOptions{SecretStore: config.CredentialTypeKeyring}); err != nil {
		t.Fatalf("ApplyImportWith() = %v", err)
	}
	if dst.secrets.entries["full.password"] != secPassword || dst.secrets.entries["bare.token"] != secToken {
		t.Errorf("entries = %v", dst.secrets.entries)
	}
	if _, ok := dst.secrets.entries["bare.password"]; ok {
		t.Error("an unset optional role was written")
	}
}

func newOptionalTransferWorld(t *testing.T, text string) *transferWorld {
	t.Helper()
	dir := t.TempDir()
	w := &transferWorld{t: t, dir: dir, path: filepath.Join(dir, "config.yaml"), secrets: newFakeSecrets()}
	if text != "" {
		if err := os.WriteFile(w.path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	w.store = config.NewStore(w.path, optionalCatalog{})
	w.svc = New(w.store, w.secrets, connlog.SurfaceCLI, &redact.Redactor{}).WithVaultProcessSupport(false)
	return w
}
