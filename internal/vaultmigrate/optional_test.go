package vaultmigrate

import (
	"fmt"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

type optionalCatalog struct{}

var optionalProvider = config.ProviderMetadata{
	ID:          "site",
	SecretRoles: []config.SecretRole{{Name: "token"}, {Name: "password", Optional: true}},
}

func (optionalCatalog) ProviderMetadata(id string) (config.ProviderMetadata, bool) {
	return optionalProvider, id == "site"
}

func (optionalCatalog) ProviderMetadataAll() []config.ProviderMetadata {
	return []config.ProviderMetadata{optionalProvider}
}

// A switched credential carries its optional role into the vault where the keyring holds it, and nothing for
// it where the keyring holds none.
func TestPlanCarriesAnOptionalRole(t *testing.T) {
	cfg := config.New(optionalCatalog{})
	for _, name := range []string{"full", "bare"} {
		if err := cfg.SetCredential(name, config.Credential{Provider: "site", Type: config.CredentialTypeKeyring}); err != nil {
			t.Fatal(err)
		}
	}
	entries := map[string]map[string]string{"full": {"token": "old-token"}, "bare": {"token": "old-token"}}
	store := fakeStore{
		"full.token":    {value: "keyring-token", state: secret.StoreHolds},
		"full.password": {value: "keyring-password", state: secret.StoreHolds},
	}

	plan, switched, unsure, err := Plan(cfg, store, vault.New(t.TempDir()), entries, nil)
	if err != nil {
		t.Fatalf("Plan() = %v", err)
	}
	if fmt.Sprint(switched) != "[bare full]" || len(unsure) != 0 {
		t.Errorf("switched = %v, unsure = %v", switched, unsure)
	}
	want := "[{bare token old-token} {full password keyring-password} {full token keyring-token}]"
	if got := fmt.Sprint(plan); got != want {
		t.Errorf("plan = %s, want %s", got, want)
	}
}
